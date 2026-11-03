import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:velin_mobile/ui/theme.dart';

void main() {
  tearDown(() => V.restore(null, null));

  test('palette and backdrop selection restyles and falls back safely', () {
    var changes = 0;
    V.revision.addListener(() => changes++);
    V.restore('unknown', null);
    expect(V.paletteId, V.palettes.first.id);
    V.select(palette: 'aurora');
    expect(V.bg, V.palettes.firstWhere((p) => p.id == 'aurora').bg);
    expect(V.artAsset, 'assets/art/aurora.svg');
    V.select(backdrop: 'forest');
    expect(V.artAsset, 'assets/art/forest.svg');
    V.select(backdrop: 'none');
    expect(V.artAsset, isNull);
    expect(changes, 3);
  });

  test(
    'custom backdrop uses the photo, falls back to theme art if missing',
    () {
      final dir = Directory.systemTemp.createTempSync('velin-bg');
      addTearDown(() => dir.deleteSync(recursive: true));
      final photo = File('${dir.path}/bg.jpg')..writeAsBytesSync([1, 2, 3]);
      V.select(
        palette: 'sunset',
        backdrop: 'custom',
        file: photo.path,
        dim: .7,
      );
      expect(V.customImage?.path, photo.path);
      expect(V.artAsset, isNull);
      expect(V.dim, .7);
      photo.deleteSync();
      expect(V.customImage, isNull);
      expect(V.artAsset, 'assets/art/sunset.svg');
      V.select(dim: 5);
      expect(V.dim, .9);
    },
  );

  testWidgets('notice appears in the middle of the screen, above the bottom', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(400, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    late BuildContext context;
    await tester.pumpWidget(
      MaterialApp(
        theme: V.theme(),
        home: Scaffold(
          body: Builder(
            builder: (c) {
              context = c;
              return const SizedBox.expand();
            },
          ),
        ),
      ),
    );
    notice(context, '已复制');
    await tester.pump(const Duration(milliseconds: 300));
    final center = tester.getCenter(find.text('已复制'));
    expect((center.dy - 400).abs() < 60, isTrue, reason: '$center');
    await tester.pump(const Duration(seconds: 6));
    expect(find.text('已复制'), findsNothing);
  });
}
