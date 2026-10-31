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
  Future<void> _writes = Future.value();
  @override
  void initState() {
    super.initState();
    controller = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(const Color(0xff101113))
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
  Widget build(BuildContext context) => WebViewWidget(controller: controller);
}
