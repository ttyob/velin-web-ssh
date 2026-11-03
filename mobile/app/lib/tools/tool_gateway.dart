import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'package:dio/dio.dart';
import '../core/models.dart';
import '../core/remote_api.dart';

const terminalToolNames = {
  'files': '当前目录',
  'agent': 'Agent',
  'share': '分享会话',
  'monitor': '主机监控',
  'git': 'Git 管理',
  'docker': 'Docker 管理',
};

// A capability-protected loopback gateway keeps account Cookies in native code.
// Only the chosen host/session's tool endpoints and static assets are exposed.
class ToolGateway {
  final RemoteApi api;
  final RemoteSession session;
  final RemoteHost host;
  final String tool;
  final String _secret = base64Url.encode(
    List<int>.generate(32, (_) => Random.secure().nextInt(256)),
  );
  final String _cookie = 'velin_tools_${Random.secure().nextInt(1 << 32)}';
  final CancelToken _cancel = CancelToken();
  final Set<WebSocket> _sockets = {};
  HttpServer? _server;
  bool _bootstrapped = false;
  ToolGateway({
    required this.api,
    required this.session,
    required this.host,
    required this.tool,
  });

  Uri get origin => Uri.parse('http://127.0.0.1:${_server!.port}');
  Uri get page => origin.replace(path: '/mobile-tools');
  Map<String, String> get bootstrapHeaders => {'X-Velin-Tools': _secret};

  Future<void> start() async {
    if (!terminalToolNames.containsKey(tool)) {
      throw const FormatException('未知终端工具');
    }
    _server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _server!.listen((request) => unawaited(_serve(request)));
  }

  bool _matches(String? value) {
    if (value == null || value.length != _secret.length) return false;
    var result = 0;
    for (var i = 0; i < value.length; i++) {
      result |= value.codeUnitAt(i) ^ _secret.codeUnitAt(i);
    }
    return result == 0;
  }

  bool allows(String path) {
    if (path.split('/').any((part) => part == '.' || part == '..') ||
        path.contains('\\')) {
      return false;
    }
    if (path == '/native/context' ||
        path == '/mobile-tools' ||
        path == '/favicon.ico' ||
        path.startsWith('/assets/')) {
      return true;
    }
    if (path == '/api/csrf' ||
        path == '/api/auth/me' ||
        path == '/api/agent/models' ||
        path == '/api/agent/backends') {
      return true;
    }
    if (path == '/api/sessions/${session.id}/directory' ||
        path == '/api/sessions/${session.id}/shares') {
      return true;
    }
    if (path.startsWith('/api/session-shares/')) return tool == 'share';
    if (path.startsWith('/api/sftp/${host.id}/')) return tool == 'files';
    if (path.startsWith('/api/hosts/${host.id}/agent') ||
        path == '/api/hosts/${host.id}/docker/login') {
      return ['agent', 'monitor', 'git', 'docker'].contains(tool);
    }
    return path == '/ws/hosts/${host.id}/agent' && tool == 'agent';
  }

  Future<void> _serve(HttpRequest request) async {
    final response = request.response;
    try {
      final originHeader = request.headers.value('origin');
      if (originHeader != null && originHeader != origin.origin ||
          request.headers.value('sec-fetch-site') == 'cross-site') {
        response.statusCode = HttpStatus.forbidden;
        await response.close();
        return;
      }
      final bootstrap =
          !_bootstrapped &&
          request.method == 'GET' &&
          request.uri.path == '/mobile-tools' &&
          _matches(request.headers.value('X-Velin-Tools'));
      if (!bootstrap &&
          !request.cookies.any(
            (cookie) => cookie.name == _cookie && _matches(cookie.value),
          )) {
        response.statusCode = HttpStatus.notFound;
        await response.close();
        return;
      }
      if (bootstrap) {
        _bootstrapped = true;
        response.cookies.add(
          Cookie(_cookie, _secret)
            ..httpOnly = true
            ..path = '/'
            ..sameSite = SameSite.strict,
        );
      }
      if (!allows(request.uri.path)) {
        response.statusCode = HttpStatus.forbidden;
        await response.close();
        return;
      }
      if (request.uri.path == '/native/context') {
        response.headers.contentType = ContentType.json;
        response.headers.set('Cache-Control', 'no-store');
        response.write(
          jsonEncode({
            'tool': tool,
            'serviceURL': api.base.toString(),
            'host': {
              'id': host.id,
              'name': host.name,
              'address': host.address,
              'username': host.username,
              'port': host.port,
              'protocol': host.protocol,
              'credentialID': host.credentialID,
              'hasPassword': host.hasPassword,
              'initialDirectory': host.directory,
            },
            'session': {
              'id': session.id,
              'hostID': session.hostID,
              'name': session.name,
              'sessionMode': session.mode,
              'status': session.status,
            },
          }),
        );
        await response.close();
        return;
      }
      if (WebSocketTransformer.isUpgradeRequest(request)) {
        final remote = await api.toolSocket(request.uri.toString());
        _sockets.add(remote);
        late final WebSocket local;
        try {
          local = await WebSocketTransformer.upgrade(request);
        } catch (_) {
          _sockets.remove(remote);
          unawaited(remote.close());
          rethrow;
        }
        _sockets.addAll([local, remote]);
        void closePair() {
          _sockets.removeAll([local, remote]);
          unawaited(local.close());
          unawaited(remote.close());
        }

        local.listen(
          (value) {
            if (remote.readyState == WebSocket.open) remote.add(value);
          },
          onDone: closePair,
          onError: (_) => closePair(),
        );
        remote.listen(
          (value) {
            if (local.readyState == WebSocket.open) local.add(value);
          },
          onDone: closePair,
          onError: (_) => closePair(),
        );
        return;
      }
      final forwarded = <String, dynamic>{};
      for (final name in [
        'accept',
        'content-type',
        'content-length',
        'x-csrf-token',
        'if-none-match',
        'if-modified-since',
        'range',
      ]) {
        final value = request.headers.value(name);
        if (value != null) forwarded[name] = value;
      }
      // The upstream HTTP client decodes compressed bodies before forwarding.
      forwarded['accept-encoding'] = 'identity';
      final upstream = await api.toolRequest(
        request.uri.path == '/mobile-tools'
            ? '/mobile-tools'
            : request.uri.toString(),
        method: request.method,
        data: ['GET', 'HEAD'].contains(request.method) ? null : request,
        headers: forwarded,
        cancel: _cancel,
      );
      response.statusCode = upstream.statusCode ?? HttpStatus.badGateway;
      for (final name in [
        'content-type',
        'content-disposition',
        'cache-control',
        'etag',
        'last-modified',
        'content-range',
        'accept-ranges',
        'content-security-policy',
        'x-content-type-options',
      ]) {
        final values = upstream.headers[name];
        if (values != null) response.headers.set(name, values);
      }
      // Set-Cookie and any upstream authentication headers stay in RemoteApi.
      if (upstream.data != null) {
        await response.addStream(upstream.data!.stream);
      }
      await response.close();
    } catch (_) {
      try {
        response.statusCode = HttpStatus.badGateway;
        response.headers.contentType = ContentType.json;
        response.write(
          jsonEncode({
            'code': 'tool_gateway_error',
            'message': '工具连接失败，请返回后重试',
          }),
        );
        await response.close();
      } catch (_) {}
    }
  }

  Future<void> close() async {
    _cancel.cancel();
    for (final socket in _sockets.toList()) {
      unawaited(socket.close());
    }
    _sockets.clear();
    await _server?.close(force: true);
    _server = null;
  }
}
