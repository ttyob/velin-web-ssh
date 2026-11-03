import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:dio/dio.dart';
import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:webview_flutter/webview_flutter.dart';
import 'package:webview_flutter_android/webview_flutter_android.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import '../tools/tool_gateway.dart';
import 'theme.dart';
import 'web_service_surface.dart';

class TerminalToolScreen extends StatefulWidget {
  final RemoteApi api;
  final RemoteHost host;
  final RemoteSession session;
  final String tool;
  const TerminalToolScreen({
    super.key,
    required this.api,
    required this.host,
    required this.session,
    required this.tool,
  });
  @override
  State<TerminalToolScreen> createState() => _TerminalToolScreenState();
}

class _TerminalToolScreenState extends State<TerminalToolScreen> {
  static const files = MethodChannel('velin/tools/files');
  late final ToolGateway gateway;
  WebViewController? controller;
  String error = '';
  bool saving = false;
  int progress = 0;
  final CancelToken _downloads = CancelToken();
  @override
  void initState() {
    super.initState();
    gateway = ToolGateway(
      api: widget.api,
      session: widget.session,
      host: widget.host,
      tool: widget.tool,
    );
    unawaited(_open());
  }

  Future<void> _open() async {
    try {
      await gateway.start();
      if (!mounted) {
        await gateway.close();
        return;
      }
      final view = WebViewController()
        ..setJavaScriptMode(JavaScriptMode.unrestricted)
        ..setBackgroundColor(const Color(0xff101113))
        ..addJavaScriptChannel(
          'VelinTools',
          onMessageReceived: (message) {
            if (!mounted) return;
            try {
              unawaited(
                _message(
                  Map<String, dynamic>.from(jsonDecode(message.message)),
                ),
              );
            } catch (_) {}
          },
        )
        ..setNavigationDelegate(
          NavigationDelegate(
            onProgress: (value) {
              if (mounted) setState(() => progress = value);
            },
            onNavigationRequest: (request) {
              final uri = Uri.tryParse(request.url);
              if (!mounted ||
                  uri == null ||
                  !['http', 'https'].contains(uri.scheme)) {
                return NavigationDecision.prevent;
              }
              if (uri.origin == gateway.origin.origin &&
                  uri.path == '/mobile-tools') {
                return NavigationDecision.navigate;
              }
              if (uri.origin == gateway.origin.origin &&
                  uri.path.endsWith('/download')) {
                unawaited(
                  _download(uri.path + (uri.hasQuery ? '?${uri.query}' : '')),
                );
              }
              return NavigationDecision.prevent;
            },
            onWebResourceError: (value) {
              if (mounted && value.isForMainFrame == true) {
                setState(() => error = '工具加载失败：${value.description}');
              }
            },
          ),
        );
      if (view.platform is AndroidWebViewController) {
        await (view.platform as AndroidWebViewController).setOnShowFileSelector(
          (params) async {
            final selected = await FilePicker.platform.pickFiles(
              allowMultiple: params.mode == FileSelectorMode.openMultiple,
            );
            return selected?.paths
                    .whereType<String>()
                    .map((path) => Uri.file(path).toString())
                    .toList() ??
                [];
          },
        );
      }
      await view.loadRequest(gateway.page, headers: gateway.bootstrapHeaders);
      if (mounted) setState(() => controller = view);
    } catch (e) {
      if (mounted) setState(() => error = '无法打开工具：$e');
    }
  }

  Future<void> _message(Map<String, dynamic> message) async {
    switch (message['type']) {
      case 'close':
        if (mounted) Navigator.pop(context);
      case 'dockerTerminal':
        final id = message['id'];
        if (id is String &&
            RegExp(r'^[a-fA-F0-9]{12,64}$').hasMatch(id) &&
            mounted) {
          Navigator.pop(context, {
            'dockerID': id,
            'name': message['name'] as String? ?? id,
          });
        }
      case 'accessRequired':
        widget.api.onAccessRequired?.call(
          message['code'] as String? ?? 'unauthorized',
        );
      case 'download':
        if (message['path'] is String) await _download(message['path']);
      case 'downloadData':
        if (saving ||
            message['data'] is! String ||
            message['name'] is! String) {
          return;
        }
        final data = message['data'] as String;
        if (data.length > 24 * 1024 * 1024) return;
        await _save(
          message['name'],
          (file) => file.writeAsBytes(base64Decode(data)),
        );
      case 'openLink':
        final uri = Uri.tryParse(message['url'] as String? ?? '');
        if (uri == null ||
            !['http', 'https'].contains(uri.scheme) ||
            uri.origin != widget.api.base.origin ||
            !uri.path.startsWith('${widget.api.base.path}share/')) {
          return;
        }
        if (mounted) {
          await Navigator.push<void>(
            context,
            MaterialPageRoute(
              builder: (_) => Scaffold(
                appBar: AppBar(title: const Text('分享预览')),
                body: WebServiceSurface(
                  access: WebServiceAccess(uri),
                  reload: 0,
                  onError: (text) {
                    if (mounted) notice(context, text);
                  },
                ),
              ),
            ),
          );
        }
    }
  }

  Future<void> _download(String path) async {
    final uri = Uri.tryParse(path);
    if (saving ||
        uri == null ||
        uri.hasAuthority ||
        uri.scheme.isNotEmpty ||
        uri.path != '/api/sftp/${widget.host.id}/download' ||
        !gateway.allows(uri.path)) {
      return;
    }
    final target = uri.replace(
      queryParameters: {...uri.queryParameters, 'session': widget.session.id},
    );
    final name = (uri.queryParameters['path'] ?? 'download').split('/').last;
    await _save(name.isEmpty ? 'download' : name, (file) async {
      final response = await widget.api.toolRequest(
        target.toString(),
        cancel: _downloads,
      );
      if (response.statusCode != 200 || response.data == null) {
        throw const FormatException('远端文件下载失败');
      }
      final sink = file.openWrite();
      try {
        await sink.addStream(response.data!.stream);
      } finally {
        await sink.close();
      }
      return file;
    });
  }

  Future<void> _save(String name, Future<File> Function(File) write) async {
    if (saving) return;
    setState(() => saving = true);
    Directory? temporary;
    try {
      temporary = await Directory.systemTemp.createTemp('velin-tool-download-');
      final file = await write(File('${temporary.path}/download'));
      if (!mounted) return;
      final saved = await files.invokeMethod<bool>('save', {
        'path': file.path,
        'name': name.replaceAll(RegExp(r'[/\\\x00-\x1f]'), '_'),
      });
      if (mounted && saved == true) notice(context, '已保存到所选位置');
    } catch (e) {
      if (mounted && !(e is DioException && CancelToken.isCancel(e))) {
        notice(context, '保存失败：$e');
      }
    } finally {
      if (temporary != null) {
        try {
          await temporary.delete(recursive: true);
        } catch (_) {}
      }
      if (mounted) setState(() => saving = false);
    }
  }

  @override
  void dispose() {
    _downloads.cancel();
    unawaited(gateway.close());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text(
        terminalToolNames[widget.tool] ?? '终端工具',
        style: const TextStyle(fontSize: 15),
      ),
    ),
    body: SafeArea(
      child: Column(
        children: [
          if (saving) const LinearProgressIndicator(minHeight: 2),
          if (progress < 100 && controller != null)
            LinearProgressIndicator(value: progress / 100, minHeight: 2),
          Expanded(
            child: error.isNotEmpty
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(20),
                      child: Text(error),
                    ),
                  )
                : controller == null
                ? const Center(child: CircularProgressIndicator(strokeWidth: 2))
                : WebViewWidget(controller: controller!),
          ),
        ],
      ),
    ),
  );
}
