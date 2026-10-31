import 'package:flutter/material.dart';
import 'theme.dart';

/// Shared visual shell for authentication. No credentials live in this layer.
class AuthBackdrop extends StatelessWidget {
  final Widget child;
  const AuthBackdrop({super.key, required this.child});

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: const BoxDecoration(color: Color(0xff101216)),
    child: Stack(
      fit: StackFit.expand,
      children: [
        const DecoratedBox(
          decoration: BoxDecoration(
            gradient: RadialGradient(
              center: Alignment(.9, -.9),
              radius: 1.4,
              colors: [Color(0xff283342), Color(0xff14181f), Color(0xff101216)],
              stops: [0, .5, 1],
            ),
          ),
        ),
        Positioned(
          right: -160,
          top: -160,
          child: IgnorePointer(
            child: Container(
              width: 420,
              height: 420,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(color: Colors.white.withValues(alpha: .035)),
              ),
              child: Center(
                child: Container(
                  width: 300,
                  height: 300,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    border: Border.all(
                      color: Colors.white.withValues(alpha: .035),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
        child,
      ],
    ),
  );
}

class AuthBrand extends StatelessWidget {
  final bool demo;
  const AuthBrand({super.key, this.demo = false});

  @override
  Widget build(BuildContext context) => Row(
    children: [
      Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: Colors.white.withValues(alpha: .06),
          borderRadius: BorderRadius.circular(11),
          border: Border.all(color: Colors.white.withValues(alpha: .1)),
        ),
        child: const Icon(Icons.terminal_rounded, size: 21, color: V.text),
      ),
      const SizedBox(width: 10),
      const Expanded(
        child: Text(
          'velin',
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            fontSize: 25,
            fontWeight: FontWeight.w600,
            letterSpacing: -.9,
          ),
        ),
      ),
      if (demo) const StateChip('设计预览'),
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
                      Text(
                        title,
                        style: const TextStyle(
                          fontSize: 28,
                          fontWeight: FontWeight.w600,
                          letterSpacing: -.7,
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(subtitle, style: const TextStyle(color: V.muted)),
                      const SizedBox(height: 24),
                      Container(
                        padding: const EdgeInsets.all(20),
                        decoration: BoxDecoration(
                          color: const Color(0xff1a1e25).withValues(alpha: .85),
                          borderRadius: BorderRadius.circular(22),
                          border: Border.all(
                            color: Colors.white.withValues(alpha: .08),
                          ),
                        ),
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
      decoration: BoxDecoration(
        color: V.danger.withValues(alpha: .08),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(
        message,
        style: const TextStyle(color: V.danger, fontSize: 13),
      ),
    ),
  );
}
