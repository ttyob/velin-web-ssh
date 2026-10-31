import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'auth_layout.dart';
import 'theme.dart';

/// The cover is above the Navigator. Register its back policy on the actual
/// current route, including terminals and dialogs pushed after the home page.
class LockNavigationObserver extends NavigatorObserver {
  late final _entry = _LockPopEntry(() => onLockedBack?.call());
  ModalRoute<dynamic>? _route;
  VoidCallback? onLockedBack;

  set locked(bool value) => _entry.canPopNotifier.value = !value;

  @override
  void didChangeTop(Route<dynamic> topRoute, Route<dynamic>? previousTopRoute) {
    _route?.unregisterPopEntry(_entry);
    _route = topRoute is ModalRoute<dynamic> ? topRoute : null;
    _route?.registerPopEntry(_entry);
  }

  void dispose() {
    _route?.unregisterPopEntry(_entry);
    _entry.canPopNotifier.dispose();
  }
}

class _LockPopEntry extends PopEntry<dynamic> {
  final VoidCallback onBack;
  _LockPopEntry(this.onBack);
  @override
  final ValueNotifier<bool> canPopNotifier = ValueNotifier(true);
  @override
  void onPopInvokedWithResult(bool didPop, dynamic result) {
    if (!didPop && !canPopNotifier.value) onBack();
  }
}

class WorkspaceShield extends StatelessWidget {
  final bool locked;
  final Widget child;
  final Widget? cover;
  const WorkspaceShield({
    super.key,
    required this.locked,
    required this.child,
    this.cover,
  });

  @override
  Widget build(BuildContext context) => Stack(
    fit: StackFit.expand,
    children: [
      ExcludeFocus(
        excluding: locked,
        child: Offstage(offstage: locked, child: child),
      ),
      if (locked) Overlay.wrap(child: cover!),
    ],
  );
}

/// Covers the root Navigator, so pushed terminal routes stay mounted and locked.
class SessionLock extends StatefulWidget {
  final String account;
  final Future<void> Function(String pin) onUnlock;
  final Future<void> Function() onLogout;
  final bool demo;
  final LockNavigationObserver? navigationGuard;
  const SessionLock({
    super.key,
    required this.account,
    required this.onUnlock,
    required this.onLogout,
    this.demo = false,
    this.navigationGuard,
  });

  @override
  State<SessionLock> createState() => _SessionLockState();
}

class _SessionLockState extends State<SessionLock> {
  final pin = TextEditingController();
  final pinFocus = FocusNode();
  final coverFocus = FocusNode();
  late final Timer clock;
  DateTime now = DateTime.now();
  bool expanded = false, busy = false;
  String error = '';

  @override
  void initState() {
    super.initState();
    widget.navigationGuard?.onLockedBack = _collapse;
    clock = Timer.periodic(const Duration(seconds: 1), (_) {
      final next = DateTime.now();
      if (next.minute != now.minute || next.day != now.day) {
        setState(() => now = next);
      }
    });
  }

  @override
  void dispose() {
    widget.navigationGuard?.onLockedBack = null;
    clock.cancel();
    pin.dispose();
    pinFocus.dispose();
    coverFocus.dispose();
    super.dispose();
  }

  void _expand() {
    if (expanded) return;
    setState(() => expanded = true);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && expanded) pinFocus.requestFocus();
    });
  }

  void _collapse() {
    if (busy) return;
    pinFocus.unfocus();
    setState(() {
      expanded = false;
      pin.clear();
      error = '';
    });
    coverFocus.requestFocus();
  }

  Future<void> _unlock() async {
    if (busy || !RegExp(r'^\d{6}$').hasMatch(pin.text)) return;
    setState(() {
      busy = true;
      error = '';
    });
    try {
      await widget.onUnlock(pin.text);
    } catch (e) {
      if (mounted) {
        setState(() {
          error = '$e';
          pin.clear();
        });
        pinFocus.requestFocus();
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final time =
        '${now.hour.toString().padLeft(2, '0')}:${now.minute.toString().padLeft(2, '0')}';
    final weekday = ['一', '二', '三', '四', '五', '六', '日'][now.weekday - 1];
    return BlockSemantics(
      child: Scaffold(
        body: AuthBackdrop(
          child: SafeArea(
            child: Focus(
              focusNode: coverFocus,
              autofocus: true,
              onKeyEvent: (_, event) {
                if (event is! KeyDownEvent) return KeyEventResult.ignored;
                if (!expanded &&
                    [
                      LogicalKeyboardKey.enter,
                      LogicalKeyboardKey.space,
                    ].contains(event.logicalKey)) {
                  _expand();
                  return KeyEventResult.handled;
                }
                if (expanded && event.logicalKey == LogicalKeyboardKey.escape) {
                  _collapse();
                  return KeyEventResult.handled;
                }
                return KeyEventResult.ignored;
              },
              child: LayoutBuilder(
                builder: (context, constraints) => SingleChildScrollView(
                  child: ConstrainedBox(
                    constraints: BoxConstraints(
                      minHeight: constraints.maxHeight,
                    ),
                    child: IntrinsicHeight(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          Padding(
                            padding: const EdgeInsets.fromLTRB(24, 20, 24, 0),
                            child: Row(
                              children: [
                                const Icon(
                                  Icons.lock_outline_rounded,
                                  size: 17,
                                  color: V.muted,
                                ),
                                const SizedBox(width: 8),
                                const Text(
                                  'velin',
                                  style: TextStyle(
                                    color: V.muted,
                                    fontSize: 14,
                                  ),
                                ),
                                const Spacer(),
                                if (widget.demo) const StateChip('设计预览'),
                              ],
                            ),
                          ),
                          Expanded(
                            child: AnimatedSwitcher(
                              duration: const Duration(milliseconds: 220),
                              child: expanded
                                  ? _pinPanel()
                                  : _cover(
                                      time,
                                      '${now.month}月${now.day}日  星期$weekday',
                                    ),
                            ),
                          ),
                          Padding(
                            padding: const EdgeInsets.fromLTRB(24, 12, 24, 20),
                            child: Row(
                              children: [
                                const Expanded(
                                  child: Text(
                                    '远端任务继续运行',
                                    style: TextStyle(
                                      fontSize: 12,
                                      color: V.muted,
                                    ),
                                  ),
                                ),
                                TextButton(
                                  onPressed: busy ? null : widget.onLogout,
                                  style: TextButton.styleFrom(
                                    foregroundColor: V.muted,
                                  ),
                                  child: const Text('退出登录'),
                                ),
                              ],
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _cover(String time, String date) => SizedBox(
    key: const ValueKey('lock-cover'),
    width: double.infinity,
    child: Semantics(
      button: true,
      label: '点击解锁',
      child: InkWell(
        onTap: _expand,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 32),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              FittedBox(
                fit: BoxFit.scaleDown,
                child: Text(
                  time,
                  style: const TextStyle(
                    fontSize: 88,
                    fontWeight: FontWeight.w200,
                    letterSpacing: -4,
                    height: 1.15,
                    fontFeatures: [FontFeature.tabularFigures()],
                  ),
                ),
              ),
              const SizedBox(height: 10),
              Text(date, style: const TextStyle(fontSize: 16, color: V.accent)),
              const SizedBox(height: 64),
              _avatar(),
              const SizedBox(height: 14),
              Text(
                widget.account,
                textAlign: TextAlign.center,
                style: const TextStyle(
                  fontSize: 18,
                  fontWeight: FontWeight.w500,
                ),
              ),
              const SizedBox(height: 6),
              const Text(
                '工作台已锁定',
                style: TextStyle(color: V.muted, fontSize: 13),
              ),
              const SizedBox(height: 40),
              const Icon(Icons.keyboard_arrow_up_rounded, color: V.accent),
              const SizedBox(height: 6),
              const Text(
                '轻触屏幕，输入 PIN 解锁',
                style: TextStyle(color: V.accent, fontSize: 13),
              ),
            ],
          ),
        ),
      ),
    ),
  );

  Widget _avatar() => Container(
    width: 64,
    height: 64,
    decoration: BoxDecoration(
      color: Colors.white.withValues(alpha: .06),
      shape: BoxShape.circle,
      border: Border.all(color: Colors.white.withValues(alpha: .1)),
    ),
    child: const Icon(Icons.person_outline_rounded, size: 30, color: V.accent),
  );

  Widget _pinPanel() => Center(
    key: const ValueKey('lock-pin-panel'),
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 32),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 320),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Center(child: _avatar()),
            const SizedBox(height: 18),
            Text(
              widget.account,
              textAlign: TextAlign.center,
              style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w500),
            ),
            const SizedBox(height: 8),
            const Text(
              '输入 6 位锁屏 PIN',
              textAlign: TextAlign.center,
              style: TextStyle(color: V.muted, fontSize: 13),
            ),
            const SizedBox(height: 28),
            TextField(
              key: const ValueKey('lock-pin'),
              controller: pin,
              focusNode: pinFocus,
              readOnly: busy,
              obscureText: true,
              enableSuggestions: false,
              autocorrect: false,
              enableInteractiveSelection: false,
              keyboardType: TextInputType.number,
              textInputAction: TextInputAction.done,
              textAlign: TextAlign.center,
              style: const TextStyle(fontSize: 22, letterSpacing: 14),
              inputFormatters: [
                FilteringTextInputFormatter.digitsOnly,
                LengthLimitingTextInputFormatter(6),
              ],
              decoration: const InputDecoration(
                labelText: '锁屏 PIN',
                floatingLabelBehavior: FloatingLabelBehavior.always,
                contentPadding: EdgeInsets.symmetric(
                  horizontal: 12,
                  vertical: 16,
                ),
              ),
              onChanged: (_) => setState(() {}),
              onSubmitted: (_) => _unlock(),
            ),
            if (error.isNotEmpty) AuthError(error),
            const SizedBox(height: 16),
            FilledButton(
              onPressed: busy || pin.text.length != 6 ? null : _unlock,
              child: Text(busy ? '正在解锁…' : '解锁工作台'),
            ),
            const SizedBox(height: 8),
            TextButton(
              onPressed: busy ? null : _collapse,
              style: TextButton.styleFrom(foregroundColor: V.muted),
              child: const Text('返回锁屏'),
            ),
          ],
        ),
      ),
    ),
  );
}
