import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:webview_flutter/webview_flutter.dart';
import 'terminal_port.dart';

class TerminalSurface extends StatefulWidget {
  final TerminalPort port;
  const TerminalSurface({super.key, required this.port});
  @override
  State<TerminalSurface> createState() => _TerminalSurfaceState();
}

class _TerminalSurfaceState extends State<TerminalSurface> {
  late final WebViewController controller;
  Color get _background {
    final hex = widget.port.background;
    final value = hex == null
        ? null
        : int.tryParse(hex.substring(1), radix: 16);
    return value == null ? const Color(0xff101113) : Color(0xff000000 | value);
  }

  Future<void> _writes = Future.value();
  @override
  void initState() {
    super.initState();
    controller = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(_background)
      // Zoom is handled inside the page so every edge stays reachable.
      ..enableZoom(false)
      ..addJavaScriptChannel(
        'VelinTerminal',
        onMessageReceived: (message) {
          if (!mounted) return;
          final data = Map<String, dynamic>.from(jsonDecode(message.message));
          if (data['type'] == 'ready') {
            widget.port.bind((value) {
              _writes = _writes
                  .then((_) async {
                    if (mounted) {
                      await controller.runJavaScript(
                        'window.Velin.receive(${jsonEncode(value)})',
                      );
                    }
                  })
                  .catchError((Object error) {
                    if (mounted) {
                      widget.port.onMessage?.call({
                        'type': 'bridge_error',
                        'message': '$error',
                      });
                    }
                  });
            });
          } else {
            widget.port.receive(data);
          }
        },
      )
      ..setNavigationDelegate(
        NavigationDelegate(
          onPageStarted: (_) {
            final hex = widget.port.background;
            if (hex != null && RegExp(r'^#[0-9a-fA-F]{6}$').hasMatch(hex)) {
              controller.runJavaScript(
                "document.documentElement.style.backgroundColor='$hex'",
              );
            }
          },
          onWebResourceError: (error) {
            if (mounted && error.isForMainFrame != false) {
              widget.port.onMessage?.call({'type': 'bridge_error'});
            }
          },
          onNavigationRequest: (r) =>
              r.url.startsWith('file:') &&
                  r.url.endsWith('/assets/terminal/index.html')
              ? NavigationDecision.navigate
              : NavigationDecision.prevent,
        ),
      )
      ..loadFlutterAsset('assets/terminal/index.html');
  }

  @override
  void dispose() {
    widget.port.unbind();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: _background,
    child: WebViewWidget(controller: controller),
  );
}
