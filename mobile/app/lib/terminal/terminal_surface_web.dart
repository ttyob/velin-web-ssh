import 'dart:js_interop';
import 'dart:ui_web' as ui;
import 'package:web/web.dart' as web;
import 'package:flutter/material.dart';
import 'terminal_port.dart';

// Browser support is for the independent design preview. Production networking
// and authentication remain in the Android/iOS native client.
class TerminalSurface extends StatefulWidget {
  final TerminalPort port;
  const TerminalSurface({super.key, required this.port});
  @override
  State<TerminalSurface> createState() => _TerminalSurfaceState();
}

class _TerminalSurfaceState extends State<TerminalSurface> {
  late final web.HTMLIFrameElement frame;
  late final JSFunction listener;
  late final String viewType;
  @override
  void initState() {
    super.initState();
    viewType = 'velin-terminal-${widget.port.generation}';
    frame = web.HTMLIFrameElement()
      ..src = 'assets/assets/terminal/index.html'
      ..style.border = '0'
      ..style.width = '100%'
      ..style.height = '100%';
    listener = ((web.MessageEvent event) {
      if (event.origin != web.window.location.origin ||
          event.source != frame.contentWindow) {
        return;
      }
      final data = Map<String, dynamic>.from(event.data.dartify() as Map);
      if (data['type'] == 'ready') {
        widget.port.bind(
          (value) => frame.contentWindow?.postMessage(
            value.jsify(),
            web.window.location.origin.toJS,
          ),
        );
      } else {
        widget.port.receive(data);
      }
    }).toJS;
    web.window.addEventListener('message', listener);
    ui.platformViewRegistry.registerViewFactory(viewType, (_) => frame);
  }

  @override
  void dispose() {
    web.window.removeEventListener('message', listener);
    widget.port.unbind();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => HtmlElementView(viewType: viewType);
}
