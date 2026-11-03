import 'package:flutter/material.dart';
import 'theme.dart';

/// Shared visual shell for authentication. No credentials live in this layer.
class AuthBackdrop extends StatelessWidget {
  final Widget child;
  const AuthBackdrop({super.key, required this.child});

  @override
  Widget build(BuildContext context) => Scene(child: child);
}

class AuthBrand extends StatelessWidget {
  final bool demo;
  const AuthBrand({super.key, this.demo = false});

  @override
  Widget build(BuildContext context) => Row(
    children: [
      const SoftBadge(Icons.terminal_rounded, size: 46),
      const SizedBox(width: 12),
      Expanded(
        child: ShaderMask(
          shaderCallback: (r) =>
              LinearGradient(colors: [V.accent, V.sky]).createShader(r),
          child: const Text(
            'velin',
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(
              fontSize: 28,
              fontWeight: FontWeight.w700,
              letterSpacing: 2,
              color: Colors.white,
            ),
          ),
        ),
      ),
      if (demo) StateChip('设计预览', color: V.yellow),
    ],
  );
}

class AuthPage extends StatelessWidget {
  final String title, subtitle;
  final Widget child;
  final Widget? footer;
  final bool demo;
  const AuthPage({
    super.key,
    required this.title,
    required this.subtitle,
    required this.child,
    this.footer,
    this.demo = false,
  });

  @override
  Widget build(BuildContext context) => Scaffold(
    body: AuthBackdrop(
      child: SafeArea(
        child: LayoutBuilder(
          builder: (context, constraints) => SingleChildScrollView(
            keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
            padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 24),
            child: ConstrainedBox(
              constraints: BoxConstraints(
                minHeight: (constraints.maxHeight - 48).clamp(
                  0,
                  double.infinity,
                ),
              ),
              child: Center(
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 408),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      AuthBrand(demo: demo),
                      const SizedBox(height: 36),
                      const SectionLabel('欢迎回来，今晚也写点代码吧'),
                      const SizedBox(height: 10),
                      Text(
                        title,
                        style: TextStyle(
                          fontSize: 28,
                          fontWeight: FontWeight.w700,
                          letterSpacing: 1,
                          shadows: [Shadow(color: V.accent, blurRadius: 18)],
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(subtitle, style: TextStyle(color: V.muted)),
                      const SizedBox(height: 24),
                      SoftPanel(
                        padding: const EdgeInsets.all(20),
                        radius: 22,
                        glow: true,
                        child: child,
                      ),
                      const SizedBox(height: 20),
                      if (footer != null) footer!,
                    ],
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

class AuthError extends StatelessWidget {
  final String message;
  const AuthError(this.message, {super.key});

  @override
  Widget build(BuildContext context) => Semantics(
    liveRegion: true,
    child: Container(
      margin: const EdgeInsets.only(top: 16),
      padding: const EdgeInsets.all(12),
      decoration: ShapeDecoration(
        color: V.danger.withValues(alpha: .1),
        shape: V.cut(r: 8, color: V.danger.withValues(alpha: .7)),
      ),
      child: Text(message, style: TextStyle(color: V.danger, fontSize: 13)),
    ),
  );
}
