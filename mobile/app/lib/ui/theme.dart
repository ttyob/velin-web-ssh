import 'dart:async';
import 'dart:io';
import 'dart:ui' show ImageFilter;
import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

/// One selectable colour scheme together with its default backdrop artwork.
class AppPalette {
  final String id, name, art;
  final Color bg, surface, raised, line, text, muted;
  final Color accent, sky, yellow, danger;
  const AppPalette({
    required this.id,
    required this.name,
    required this.art,
    required this.bg,
    required this.surface,
    required this.raised,
    required this.line,
    required this.text,
    required this.muted,
    required this.accent,
    required this.sky,
    required this.yellow,
    required this.danger,
  });
}

/// Design tokens. Colours are read from the active [AppPalette], so every
/// widget must resolve them at build time (never in a `const` expression).
abstract final class V {
  static const palettes = <AppPalette>[
    AppPalette(
      id: 'sakura',
      name: '樱夜',
      art: 'night',
      bg: Color(0xff12132b),
      surface: Color(0xff1c1e40),
      raised: Color(0xff282b55),
      line: Color(0xff3d4075),
      text: Color(0xfff6f1ff),
      muted: Color(0xffa2a5d2),
      accent: Color(0xffff9ec8),
      sky: Color(0xff8fd3ff),
      yellow: Color(0xffffe2a0),
      danger: Color(0xffff8fa3),
    ),
    AppPalette(
      id: 'aurora',
      name: '极光',
      art: 'aurora',
      bg: Color(0xff0a1a1f),
      surface: Color(0xff11292f),
      raised: Color(0xff193a42),
      line: Color(0xff2b5560),
      text: Color(0xffeafff8),
      muted: Color(0xff8fb8b4),
      accent: Color(0xff6df5c1),
      sky: Color(0xffa497ff),
      yellow: Color(0xfff5e9a0),
      danger: Color(0xffff8c9d),
    ),
    AppPalette(
      id: 'sunset',
      name: '夕烧',
      art: 'sunset',
      bg: Color(0xff1f1226),
      surface: Color(0xff2d1a36),
      raised: Color(0xff3d2447),
      line: Color(0xff5c3a66),
      text: Color(0xfffff3ea),
      muted: Color(0xffc9a8b8),
      accent: Color(0xffff9a6b),
      sky: Color(0xffc3a2ff),
      yellow: Color(0xffffd27a),
      danger: Color(0xffff6b81),
    ),
    AppPalette(
      id: 'galaxy',
      name: '星河',
      art: 'galaxy',
      bg: Color(0xff070b1f),
      surface: Color(0xff10173a),
      raised: Color(0xff1b2456),
      line: Color(0xff33408a),
      text: Color(0xffeef2ff),
      muted: Color(0xff97a3d9),
      accent: Color(0xff7ab8ff),
      sky: Color(0xffc58bff),
      yellow: Color(0xffffe58a),
      danger: Color(0xffff7a9a),
    ),
    AppPalette(
      id: 'forest',
      name: '萤林',
      art: 'forest',
      bg: Color(0xff0d1712),
      surface: Color(0xff15241c),
      raised: Color(0xff1f3529),
      line: Color(0xff33573f),
      text: Color(0xfff0fff0),
      muted: Color(0xff9bb8a2),
      accent: Color(0xffb6f06b),
      sky: Color(0xff6fe0c8),
      yellow: Color(0xffffe27a),
      danger: Color(0xffff8c7a),
    ),
  ];

  /// Backdrop choices besides "follow theme" (`''`) and "none".
  static const arts = <(String, String)>[
    ('night', '樱夜'),
    ('aurora', '极光'),
    ('sunset', '夕烧'),
    ('galaxy', '星河'),
    ('forest', '萤林'),
  ];

  static AppPalette _palette = palettes.first;
  static String _backdrop = '';

  /// Bumped on every change so the app can restyle itself.
  static final revision = ValueNotifier<int>(0);

  static String get paletteId => _palette.id;
  static String get backdrop => _backdrop;

  /// Asset of the active backdrop, or null when the user chose none.
  static String? get artAsset {
    if (_backdrop == 'none') return null;
    if (_backdrop == 'custom') {
      // A deleted photo falls back to the theme's own artwork.
      return customImage == null ? 'assets/art/${_palette.art}.svg' : null;
    }
    return 'assets/art/${_backdrop.isEmpty ? _palette.art : _backdrop}.svg';
  }

  static String _customPath = '';
  static double _dim = .5;

  /// User-chosen backdrop photo (when selected and still on disk).
  static File? get customImage {
    if (_backdrop != 'custom' || _customPath.isEmpty) return null;
    final file = File(_customPath);
    return file.existsSync() ? file : null;
  }

  static String get customPath => _customPath;

  /// How strongly the backdrop is darkened for readability (0 – 0.9).
  static double get dim => _dim;

  static void restore(
    String? palette,
    String? backdrop, {
    String? file,
    double? dim,
  }) {
    _customPath = file ?? _customPath;
    _dim = (dim ?? _dim).clamp(0.0, .9);
    _palette = palettes.firstWhere(
      (p) => p.id == palette,
      orElse: () => palettes.first,
    );
    _backdrop = backdrop ?? '';
  }

  static void select({
    String? palette,
    String? backdrop,
    String? file,
    double? dim,
  }) {
    restore(
      palette ?? _palette.id,
      backdrop ?? _backdrop,
      file: file,
      dim: dim,
    );
    revision.value++;
  }

  static Color get bg => _palette.bg;
  static Color get surface => _palette.surface;
  static Color get raised => _palette.raised;
  static Color get line => _palette.line;
  static Color get text => _palette.text;
  static Color get muted => _palette.muted;
  static Color get accent => _palette.accent;
  static Color get sky => _palette.sky;
  static Color get yellow => _palette.yellow;
  static Color get danger => _palette.danger;
  static const mono = 'monospace';

  /// Soft rounded border used by panels, buttons and inputs.
  static RoundedRectangleBorder cut({
    double r = 10,
    Color? color,
    double width = 1,
    bool flip = false,
  }) => RoundedRectangleBorder(
    borderRadius: BorderRadius.circular(r + 6),
    side: color == null
        ? BorderSide.none
        : BorderSide(color: color, width: width),
  );

  static ThemeData theme() => ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    scaffoldBackgroundColor: bg,
    // Calm touch feedback: no sparkle shader or lingering highlight while
    // a finger drags a list.
    splashFactory: InkRipple.splashFactory,
    highlightColor: Colors.transparent,
    splashColor: accent.withValues(alpha: .1),
    colorScheme: ColorScheme.dark(
      primary: accent,
      onPrimary: Color(0xff2a1030),
      surface: surface,
      onSurface: text,
      secondary: sky,
      tertiary: yellow,
      error: danger,
      outline: line,
    ),
    fontFamily: 'sans-serif',
    textTheme: TextTheme(
      headlineLarge: TextStyle(
        fontSize: 24,
        fontWeight: FontWeight.w700,
        letterSpacing: 1,
      ),
      headlineSmall: TextStyle(
        fontSize: 24,
        fontWeight: FontWeight.w700,
        letterSpacing: 1,
      ),
      titleLarge: TextStyle(
        fontSize: 20,
        fontWeight: FontWeight.w700,
        letterSpacing: .6,
      ),
      titleMedium: TextStyle(
        fontSize: 16,
        fontWeight: FontWeight.w600,
        letterSpacing: .4,
      ),
      bodyLarge: TextStyle(fontSize: 15, height: 1.5),
      bodyMedium: TextStyle(fontSize: 14, height: 1.5),
      bodySmall: TextStyle(fontSize: 12, color: muted, height: 1.5),
    ),
    appBarTheme: AppBarTheme(
      backgroundColor: bg,
      surfaceTintColor: Colors.transparent,
      centerTitle: false,
      titleTextStyle: TextStyle(
        color: text,
        fontSize: 16,
        fontWeight: FontWeight.w700,
        letterSpacing: .6,
      ),
      shape: Border(bottom: BorderSide(color: line.withValues(alpha: .7))),
    ),
    dividerTheme: DividerThemeData(
      color: line.withValues(alpha: .8),
      thickness: 1,
      space: 1,
    ),
    listTileTheme: ListTileThemeData(iconColor: accent, textColor: text),
    iconTheme: IconThemeData(color: accent),
    progressIndicatorTheme: ProgressIndicatorThemeData(
      color: accent,
      linearTrackColor: line,
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(foregroundColor: accent),
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: surface,
      surfaceTintColor: Colors.transparent,
      shape: cut(r: 16, color: accent.withValues(alpha: .35)),
      titleTextStyle: TextStyle(
        color: accent,
        fontSize: 18,
        fontWeight: FontWeight.w700,
        letterSpacing: 1,
      ),
    ),
    switchTheme: SwitchThemeData(
      thumbColor: WidgetStateProperty.resolveWith(
        (s) => s.contains(WidgetState.selected) ? accent : muted,
      ),
      trackColor: WidgetStateProperty.resolveWith(
        (s) => s.contains(WidgetState.selected)
            ? accent.withValues(alpha: .28)
            : raised,
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: bg,
      labelStyle: TextStyle(color: muted, letterSpacing: .4),
      hintStyle: TextStyle(color: muted, fontSize: 14),
      prefixIconColor: WidgetStateColor.resolveWith(
        (s) => s.contains(WidgetState.focused) ? accent : muted,
      ),
      contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      border: _inputBorder(line),
      enabledBorder: _inputBorder(line),
      focusedBorder: _inputBorder(accent, 1.4),
      errorBorder: _inputBorder(danger),
      focusedErrorBorder: _inputBorder(danger, 1.4),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        minimumSize: const Size(48, 48),
        backgroundColor: accent,
        foregroundColor: const Color(0xff2a1030),
        disabledBackgroundColor: raised,
        elevation: 6,
        shadowColor: accent.withValues(alpha: .55),
        textStyle: const TextStyle(
          fontSize: 15,
          fontWeight: FontWeight.w700,
          letterSpacing: .6,
        ),
        shape: cut(r: 12),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(48, 48),
        foregroundColor: sky,
        side: BorderSide(color: sky),
        textStyle: const TextStyle(
          fontWeight: FontWeight.w600,
          letterSpacing: .6,
        ),
        shape: cut(r: 12),
      ),
    ),
    popupMenuTheme: PopupMenuThemeData(
      color: surface,
      surfaceTintColor: Colors.transparent,
      shape: cut(r: 12, color: accent.withValues(alpha: .5)),
    ),
    snackBarTheme: SnackBarThemeData(
      backgroundColor: raised,
      contentTextStyle: TextStyle(color: text, fontSize: 13),
      shape: cut(r: 10, color: sky),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: IconButton.styleFrom(
        minimumSize: const Size(48, 48),
        foregroundColor: text,
      ),
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: surface,
      surfaceTintColor: Colors.transparent,
      showDragHandle: true,
      dragHandleColor: accent.withValues(alpha: .6),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(26)),
      ),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: bg,
      indicatorColor: accent.withValues(alpha: .16),
      labelTextStyle: WidgetStateProperty.resolveWith(
        (s) => TextStyle(
          fontSize: 11,
          letterSpacing: .8,
          color: s.contains(WidgetState.selected) ? accent : muted,
        ),
      ),
      height: 72,
    ),
  );

  static InputBorder _inputBorder(Color c, [double w = 1]) =>
      OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: BorderSide(color: c, width: w),
      );
}

/// Frosted-glass rounded panel with a thin tinted rim.
class SoftPanel extends StatelessWidget {
  final Widget child;
  final EdgeInsetsGeometry padding;
  final Color? _tint;
  Color get color => _tint ?? V.accent;
  final double radius;
  final bool glow, stripe;
  final VoidCallback? onTap, onLongPress;
  const SoftPanel({
    super.key,
    required this.child,
    this.padding = const EdgeInsets.all(14),
    Color? color,
    this.radius = 14,
    this.glow = false,
    this.stripe = false,
    this.onTap,
    this.onLongPress,
  }) : _tint = color;
  @override
  Widget build(BuildContext context) {
    final shape = RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(radius + 4),
      side: BorderSide(color: color.withValues(alpha: glow ? .55 : .22)),
    );
    return DecoratedBox(
      decoration: ShapeDecoration(
        shape: shape,
        shadows: [
          BoxShadow(
            color: (glow ? color : Colors.black).withValues(alpha: .22),
            blurRadius: 18,
            offset: const Offset(0, 6),
          ),
        ],
      ),
      child: ClipPath(
        clipper: ShapeBorderClipper(shape: shape),
        child: BackdropFilter(
          filter: ImageFilter.blur(sigmaX: 10, sigmaY: 10),
          child: Material(
            color: V.surface.withValues(alpha: .66),
            shape: shape,
            child: InkWell(
              onTap: onTap,
              onLongPress: onLongPress,
              child: stripe
                  ? Stack(
                      children: [
                        Padding(padding: padding, child: child),
                        Positioned(
                          left: 0,
                          top: 14,
                          bottom: 14,
                          width: 3.5,
                          child: DecoratedBox(
                            decoration: BoxDecoration(
                              gradient: LinearGradient(
                                begin: Alignment.topCenter,
                                end: Alignment.bottomCenter,
                                colors: [color, color.withValues(alpha: .25)],
                              ),
                              borderRadius: const BorderRadius.horizontal(
                                right: Radius.circular(4),
                              ),
                            ),
                          ),
                        ),
                      ],
                    )
                  : Padding(padding: padding, child: child),
            ),
          ),
        ),
      ),
    );
  }
}

/// Round pastel icon badge used as list leading element.
class SoftBadge extends StatelessWidget {
  final IconData icon;
  final Color? _tint;
  Color get color => _tint ?? V.accent;
  final double size;
  const SoftBadge(this.icon, {super.key, Color? color, this.size = 40})
    : _tint = color;
  @override
  Widget build(BuildContext context) => Container(
    width: size,
    height: size,
    decoration: BoxDecoration(
      shape: BoxShape.circle,
      gradient: LinearGradient(
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
        colors: [color.withValues(alpha: .32), color.withValues(alpha: .1)],
      ),
      border: Border.all(color: color.withValues(alpha: .5)),
    ),
    child: Icon(icon, size: size * .5, color: color),
  );
}

/// Small section caption with a sparkle.
class SectionLabel extends StatelessWidget {
  final String text;
  final Color? _tint;
  Color get color => _tint ?? V.sky;
  const SectionLabel(this.text, {super.key, Color? color}) : _tint = color;
  @override
  Widget build(BuildContext context) => Row(
    children: [
      Icon(Icons.auto_awesome, size: 13, color: color),
      const SizedBox(width: 6),
      Flexible(
        child: Text(
          text,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(fontSize: 12, letterSpacing: 1.2, color: color),
        ),
      ),
    ],
  );
}

/// Night-sky illustration backdrop. The artwork is dimmed toward the bottom
/// so lists and forms stay readable.
class Scene extends StatelessWidget {
  final Widget child;
  const Scene({super.key, required this.child});
  @override
  Widget build(BuildContext context) => Stack(
    fit: StackFit.expand,
    children: [
      DecoratedBox(decoration: BoxDecoration(color: V.bg)),
      if (V.customImage != null)
        Positioned.fill(
          child: IgnorePointer(
            child: AnimatedSwitcher(
              duration: const Duration(milliseconds: 450),
              child: Image.file(
                V.customImage!,
                key: ValueKey(V.customImage!.path),
                fit: BoxFit.cover,
                width: double.infinity,
                height: double.infinity,
                cacheWidth: 1080,
                gaplessPlayback: true,
                errorBuilder: (_, _, _) => const SizedBox.shrink(),
              ),
            ),
          ),
        )
      else if (V.artAsset != null)
        Positioned.fill(
          child: IgnorePointer(
            child: AnimatedSwitcher(
              duration: const Duration(milliseconds: 450),
              child: SvgPicture.asset(
                V.artAsset!,
                key: ValueKey(V.artAsset),
                fit: BoxFit.cover,
                alignment: Alignment.bottomCenter,
                width: double.infinity,
                height: double.infinity,
              ),
            ),
          ),
        ),
      Positioned.fill(
        child: IgnorePointer(
          child: DecoratedBox(
            decoration: BoxDecoration(
              gradient: LinearGradient(
                begin: Alignment.topCenter,
                end: Alignment.bottomCenter,
                colors: [
                  V.bg.withValues(alpha: V.dim * .1),
                  V.bg.withValues(alpha: V.dim),
                  V.bg.withValues(alpha: (V.dim + .3).clamp(0.0, .95)),
                ],
                stops: const [0, .45, 1],
              ),
            ),
          ),
        ),
      ),
      child,
    ],
  );
}

class StateChip extends StatelessWidget {
  final String text;
  final Color? _tint;
  Color get color => _tint ?? V.accent;
  const StateChip(this.text, {super.key, Color? color}) : _tint = color;
  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
    decoration: BoxDecoration(
      color: color.withValues(alpha: .14),
      borderRadius: BorderRadius.circular(20),
      border: Border.all(color: color.withValues(alpha: .45)),
    ),
    child: Text(
      text,
      style: TextStyle(color: color, fontSize: 11, letterSpacing: .4),
    ),
  );
}

OverlayEntry? _toast;
Timer? _toastTimer;

/// Centered, non-blocking toast. It never covers the input area at the
/// bottom and lets touches pass through to the page underneath.
void notice(BuildContext context, Object error) {
  final text = '$error'.replaceFirst(RegExp(r'^(Exception|Error): '), '');
  final overlay = Overlay.maybeOf(context, rootOverlay: true);
  if (overlay == null) {
    ScaffoldMessenger.maybeOf(
      context,
    )?.showSnackBar(SnackBar(content: Text(text)));
    return;
  }
  _toastTimer?.cancel();
  _toast?.remove();
  final bad =
      error is Exception ||
      error is Error ||
      RegExp(r'失败|错误|无法|异常|拒绝|超时|不可用|Exception').hasMatch(text);
  final entry = OverlayEntry(builder: (_) => _Toast(text, bad: bad));
  _toast = entry;
  overlay.insert(entry);
  _toastTimer = Timer(
    Duration(milliseconds: (1800 + text.length * 45).clamp(2200, 5000)),
    () {
      if (_toast == entry) {
        entry.remove();
        _toast = null;
      }
    },
  );
}

class _Toast extends StatelessWidget {
  final String message;
  final bool bad;
  const _Toast(this.message, {required this.bad});
  @override
  Widget build(BuildContext context) {
    final color = bad ? V.danger : V.accent;
    return Positioned.fill(
      child: IgnorePointer(
        child: SafeArea(
          child: Center(
            child: TweenAnimationBuilder<double>(
              tween: Tween(begin: 0, end: 1),
              duration: const Duration(milliseconds: 220),
              curve: Curves.easeOutBack,
              builder: (context, t, child) => Opacity(
                opacity: t.clamp(0.0, 1.0),
                child: Transform.scale(scale: .9 + .1 * t, child: child),
              ),
              child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 300),
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(22),
                  child: BackdropFilter(
                    filter: ImageFilter.blur(sigmaX: 14, sigmaY: 14),
                    child: DecoratedBox(
                      decoration: BoxDecoration(
                        color: V.surface.withValues(alpha: .86),
                        borderRadius: BorderRadius.circular(22),
                        border: Border.all(color: color.withValues(alpha: .55)),
                        boxShadow: [
                          BoxShadow(
                            color: color.withValues(alpha: .28),
                            blurRadius: 24,
                          ),
                        ],
                      ),
                      child: Padding(
                        padding: const EdgeInsets.fromLTRB(14, 12, 18, 12),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(
                              bad
                                  ? Icons.error_outline_rounded
                                  : Icons.auto_awesome,
                              size: 20,
                              color: color,
                            ),
                            const SizedBox(width: 10),
                            Flexible(
                              child: Text(
                                message,
                                maxLines: 6,
                                overflow: TextOverflow.ellipsis,
                                style: DefaultTextStyle.of(context).style
                                    .copyWith(
                                      color: V.text,
                                      fontSize: 14,
                                      height: 1.4,
                                      decoration: TextDecoration.none,
                                      fontWeight: FontWeight.w500,
                                    ),
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
      ),
    );
  }
}

Future<bool> confirm(
  BuildContext context,
  String title,
  String body, {
  String action = '确认',
}) async =>
    await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(title),
        content: Text(body),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, true),
            child: Text(action),
          ),
        ],
      ),
    ) ??
    false;
