import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:velin_mobile/core/models.dart';
import 'package:velin_mobile/ui/account_setup_screen.dart';
import 'package:velin_mobile/ui/login_screen.dart';
import 'package:velin_mobile/ui/session_lock.dart';
import 'package:velin_mobile/ui/theme.dart';

void main() {
  testWidgets('lock shields pushed routes, validates PIN and retains draft', (
    tester,
  ) async {
    final locked = ValueNotifier(false);
    final guard = LockNavigationObserver();
    final navigator = GlobalKey<NavigatorState>();
    var attempts = 0;
    await tester.pumpWidget(
      MaterialApp(
        theme: V.theme(),
        navigatorKey: navigator,
        navigatorObservers: [guard],
        builder: (context, child) => ValueListenableBuilder<bool>(
          valueListenable: locked,
          child: child,
          builder: (context, value, child) {
            guard.locked = value;
            return WorkspaceShield(
              locked: value,
              cover: value
                  ? SessionLock(
                      account: 'tester',
                      navigationGuard: guard,
                      onLogout: () async {},
                      onUnlock: (pin) async {
                        attempts++;
                        if (pin != '123456') {
                          throw ApiFailure(
                            401,
                            'invalid_lock_pin',
                            '锁屏 PIN 错误',
                          );
                        }
                        locked.value = false;
                      },
                    )
                  : null,
              child: child!,
            );
          },
        ),
        home: const Scaffold(body: Text('首页')),
      ),
    );
    navigator.currentState!.push(
      MaterialPageRoute<void>(
        builder: (_) =>
            const Scaffold(body: TextField(key: ValueKey('retained-draft'))),
      ),
    );
    await tester.pumpAndSettle();
    final draft = find.byKey(const ValueKey('retained-draft'));
    await tester.enterText(draft, '未发送的开发需求');
    final originalState = tester.state(draft);
    locked.value = true;
    await tester.pumpAndSettle();
    expect(find.byType(TextField), findsNothing);
    expect(find.text('工作台已锁定'), findsOneWidget);
    final underlyingInput = tester.widget<EditableText>(
      find.byType(EditableText, skipOffstage: false),
    );
    expect(underlyingInput.focusNode.hasFocus, false);
    await tester.tap(find.byKey(const ValueKey('lock-cover')));
    await tester.pumpAndSettle();
    final pin = find.byKey(const ValueKey('lock-pin'));
    await tester.enterText(pin, 'a12');
    await tester.pump();
    expect(
      tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
      isNull,
    );
    expect(attempts, 0);
    await tester.enterText(pin, '000000');
    await tester.pump();
    await tester.tap(find.text('解锁工作台'));
    await tester.pumpAndSettle();
    expect(find.text('锁屏 PIN 错误'), findsOneWidget);
    expect(locked.value, true);
    expect(tester.widget<TextField>(pin).controller!.text, '');
    await navigator.currentState!.maybePop();
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('lock-pin')), findsNothing);
    expect(find.text('工作台已锁定'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('lock-cover')));
    await tester.pumpAndSettle();
    await tester.enterText(pin, '123456');
    await tester.pump();
    await tester.tap(find.text('解锁工作台'));
    await tester.pumpAndSettle();
    expect(locked.value, false);
    expect(attempts, 2);
    expect(tester.state(draft), same(originalState));
    expect(
      tester.widget<EditableText>(find.byType(EditableText)).controller.text,
      '未发送的开发需求',
    );
    expect(tester.takeException(), isNull);
    guard.dispose();
    await tester.pumpWidget(const SizedBox());
    locked.dispose();
  });

  testWidgets('password confirmation cannot submit mismatched passwords', (
    tester,
  ) async {
    var saved = false;
    await tester.pumpWidget(
      MaterialApp(
        theme: V.theme(),
        home: AccountSetupScreen(
          api: null,
          demo: true,
          onDone: () async => saved = true,
          onLogout: () async {},
        ),
      ),
    );
    final fields = find.byType(TextField);
    await tester.enterText(fields.at(0), 'old-password');
    await tester.enterText(fields.at(1), 'new-password');
    await tester.enterText(fields.at(2), 'different');
    await tester.ensureVisible(find.text('保存并继续'));
    await tester.tap(find.text('保存并继续'));
    await tester.pumpAndSettle();
    expect(saved, false);
    expect(find.text('两次输入的新密码不一致'), findsOneWidget);
    await tester.enterText(fields.at(2), 'new-password');
    await tester.ensureVisible(find.text('保存并继续'));
    await tester.tap(find.text('保存并继续'));
    await tester.pumpAndSettle();
    expect(saved, true);
  });

  testWidgets('auth screens fit narrow, landscape and keyboard viewports', (
    tester,
  ) async {
    SharedPreferences.setMockInitialValues({});
    final prefs = await SharedPreferences.getInstance();
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    for (final size in [const Size(320, 640), const Size(844, 390)]) {
      tester.view.physicalSize = size;
      await tester.pumpWidget(
        MaterialApp(
          theme: V.theme(),
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(
              context,
            ).copyWith(textScaler: TextScaler.linear(1.3)),
            child: child!,
          ),
          home: LoginScreen(
            preferences: prefs,
            demo: true,
            onLoggedIn: (_, _) async {},
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(
        MaterialApp(
          theme: V.theme(),
          home: SessionLock(
            account: '开发者',
            onUnlock: (_) async {},
            onLogout: () async {},
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      await tester.ensureVisible(find.byKey(const ValueKey('lock-cover')));
      await tester.tap(find.byKey(const ValueKey('lock-cover')));
      await tester.pumpAndSettle();
      tester.view.viewInsets = FakeViewPadding(bottom: size.height * .45);
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('解锁工作台'));
      expect(tester.takeException(), isNull);
      tester.view.viewInsets = FakeViewPadding.zero;
      await tester.pumpWidget(const SizedBox());
    }
  });
}
