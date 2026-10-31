import 'package:flutter/material.dart';
import 'package:webview_flutter/webview_flutter.dart';
import '../core/models.dart';
import 'theme.dart';

class WebServiceSurface extends StatefulWidget {
  final WebServiceAccess access;
  final int reload;
  final ValueChanged<String> onError;
  const WebServiceSurface({
    super.key,
    required this.access,
    required this.reload,
    required this.onError,
  });
  @override
  State<WebServiceSurface> createState() => _WebServiceSurfaceState();
}

class _WebServiceSurfaceState extends State<WebServiceSurface> {
  late final WebViewController controller;
  bool ready = false;
  int progress = 0;
  @override
  void initState() {
    super.initState();
    _prepare();
  }

  Future<void> _prepare() async {
    try {
      final access = widget.access;
      if (access.cookieName.isNotEmpty) {
        await WebViewCookieManager().setCookie(
          WebViewCookie(
            name: access.cookieName,
            value: access.cookieValue,
            domain: access.url.host,
            path: access.cookiePath,
          ),
        );
      }
      if (!mounted) return;
      controller = WebViewController()
        ..setJavaScriptMode(JavaScriptMode.unrestricted)
        ..setNavigationDelegate(
          NavigationDelegate(
            onProgress: (p) {
              if (mounted) setState(() => progress = p);
            },
            onWebResourceError: (e) {
              if (mounted && e.isForMainFrame == true) {
                widget.onError('网页加载失败：${e.description}');
              }
            },
            onNavigationRequest: (r) {
              final uri = Uri.tryParse(r.url);
              if (uri != null &&
                  ['http', 'https'].contains(uri.scheme) &&
                  uri.origin == access.url.origin) {
                return NavigationDecision.navigate;
              }
              if (r.isMainFrame && mounted) {
                notice(context, '该链接位于当前代理之外：${uri?.host ?? r.url}');
              }
              return NavigationDecision.prevent;
            },
          ),
        );
      await controller.loadRequest(access.url);
      if (mounted) setState(() => ready = true);
    } catch (e) {
      if (mounted) widget.onError('无法打开网页：$e');
    }
  }

  @override
  void didUpdateWidget(covariant WebServiceSurface oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (ready && oldWidget.reload != widget.reload) controller.reload();
  }

  @override
  Widget build(BuildContext context) => !ready
      ? const Center(child: CircularProgressIndicator(strokeWidth: 2))
      : Column(
          children: [
            if (progress < 100)
              LinearProgressIndicator(value: progress / 100, minHeight: 2),
            Expanded(child: WebViewWidget(controller: controller)),
          ],
        );
}
