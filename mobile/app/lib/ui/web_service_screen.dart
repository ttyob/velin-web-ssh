import 'package:flutter/material.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import 'theme.dart';
import 'web_service_surface.dart';

class WebServiceScreen extends StatefulWidget {
  final RemoteApi? api;
  final RemoteWebService service;
  final bool demo;
  const WebServiceScreen({
    super.key,
    required this.api,
    required this.service,
    this.demo = false,
  });
  @override
  State<WebServiceScreen> createState() => _WebServiceScreenState();
}

class _WebServiceScreenState extends State<WebServiceScreen> {
  WebServiceAccess? access;
  String error = '';
  int reload = 0;
  @override
  void initState() {
    super.initState();
    if (!widget.demo) _open();
  }

  Future<void> _open() async {
    setState(() {
      error = '';
      access = null;
    });
    try {
      final next = await widget.api!.openWebService(widget.service);
      if (mounted) setState(() => access = next);
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      toolbarHeight: 48,
      title: Text(widget.service.name, style: const TextStyle(fontSize: 15)),
      actions: [
        IconButton(
          tooltip: '刷新网页',
          onPressed: widget.demo
              ? () {}
              : access == null
              ? _open
              : () => setState(() => reload++),
          icon: const Icon(Icons.refresh, size: 20),
        ),
      ],
    ),
    body: SafeArea(
      top: false,
      child: widget.demo
          ? ListView(
              padding: const EdgeInsets.all(24),
              children: [
                const Text('示例网页', style: TextStyle(color: V.muted)),
                const SizedBox(height: 24),
                Text(
                  widget.service.name,
                  style: Theme.of(context).textTheme.headlineSmall,
                ),
                const SizedBox(height: 12),
                Text(
                  widget.service.targetURL,
                  style: const TextStyle(color: V.accent),
                ),
                const SizedBox(height: 24),
                const Text('原生 App 会通过当前 Velin 服务访问此网页。此预览不连接远端服务。'),
              ],
            )
          : error.isNotEmpty
          ? Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(error, textAlign: TextAlign.center),
                    const SizedBox(height: 16),
                    FilledButton(onPressed: _open, child: const Text('重新打开')),
                  ],
                ),
              ),
            )
          : access == null
          ? const Center(child: CircularProgressIndicator(strokeWidth: 2))
          : WebServiceSurface(
              access: access!,
              reload: reload,
              onError: (e) {
                if (mounted) setState(() => error = e);
              },
            ),
    ),
  );
}
