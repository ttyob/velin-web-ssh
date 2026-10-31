import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'models.dart';

// Only this native layer owns credentials. The terminal WebView receives bytes
// and display settings, never Cookies, passwords, or HTTP access.
class RemoteApi {
  final Uri base;
  final String account;
  final Dio _dio;
  final FlutterSecureStorage _vault;
  final Map<String, Map<String, dynamic>> _cookies = {};
  String? _csrf;
  void Function(String code)? onAccessRequired;
  RemoteApi(this.base, this.account, {Dio? dio, FlutterSecureStorage? vault})
    : _dio =
          dio ??
          Dio(
            BaseOptions(
              connectTimeout: const Duration(seconds: 15),
              receiveTimeout: const Duration(seconds: 30),
              sendTimeout: const Duration(seconds: 30),
              followRedirects: false,
              validateStatus: (_) => true,
            ),
          ),
      _vault = vault ?? const FlutterSecureStorage();
  String get _storageKey =>
      'velin.mobile.cookies.${base64Url.encode(utf8.encode('$base|$account'))}';
  Uri endpoint(String path) =>
      base.resolve(path.startsWith('/') ? path.substring(1) : path);
  Future<void> restoreCookies() async {
    final raw = await _vault.read(key: _storageKey);
    if (raw == null) return;
    final values = jsonDecode(raw) as Map<String, dynamic>;
    for (final item in values.entries) {
      _cookies[item.key] = Map<String, dynamic>.from(item.value);
    }
  }

  String cookieHeader(Uri uri) {
    final now = DateTime.now().millisecondsSinceEpoch;
    return _cookies.values
        .where(
          (c) =>
              (c['expires'] == null || (c['expires'] as int) > now) &&
              (!(c['secure'] as bool) ||
                  uri.scheme == 'https' ||
                  uri.scheme == 'wss') &&
              uri.path.startsWith(c['path'] as String),
        )
        .map((c) => '${c['name']}=${c['value']}')
        .join('; ');
  }

  Future<void> _capture(Response<dynamic> response) async {
    final headers = response.headers.map['set-cookie'] ?? [];
    if (headers.isEmpty) return;
    for (final raw in headers) {
      final cookie = Cookie.fromSetCookieValue(raw);
      if (cookie.domain != null &&
          cookie.domain!.replaceFirst(RegExp(r'^\.'), '').toLowerCase() !=
              base.host.toLowerCase()) {
        continue;
      }
      final key = '${cookie.name}|${cookie.path ?? '/'}';
      final expires = cookie.maxAge != null
          ? DateTime.now()
                .add(Duration(seconds: cookie.maxAge!))
                .millisecondsSinceEpoch
          : cookie.expires?.millisecondsSinceEpoch;
      if (cookie.maxAge == 0 || cookie.maxAge != null && cookie.maxAge! < 0) {
        _cookies.remove(key);
      } else {
        _cookies[key] = {
          'name': cookie.name,
          'value': cookie.value,
          'secure': cookie.secure,
          'path': cookie.path ?? '/',
          'expires': expires,
        };
      }
    }
    await _vault.write(key: _storageKey, value: jsonEncode(_cookies));
  }

  Future<dynamic> request(
    String path, {
    String method = 'GET',
    dynamic data,
    Map<String, dynamic>? query,
    CancelToken? cancel,
    ProgressCallback? onProgress,
    bool retryCsrf = true,
    Map<String, dynamic>? headers,
  }) async {
    if (method != 'GET' && _csrf == null) {
      final token = await request('/api/csrf');
      _csrf = (token as Map)['token'] as String;
    }
    final uri = endpoint(
      path,
    ).replace(queryParameters: query?.map((k, v) => MapEntry(k, '$v')));
    final response = await _dio.requestUri<dynamic>(
      uri,
      data: data,
      cancelToken: cancel,
      onSendProgress: onProgress,
      options: Options(
        method: method,
        headers: {
          'Cookie': cookieHeader(uri),
          'User-Agent': 'Velin-Mobile/0.1',
          if (_csrf != null && method != 'GET') 'X-CSRF-Token': _csrf,
          ...?headers,
        },
      ),
    );
    await _capture(response);
    final body = response.data;
    if ((response.statusCode ?? 500) >= 400 ||
        (response.statusCode ?? 500) < 200 ||
        (response.statusCode ?? 500) >= 300) {
      final details = body is Map
          ? Map<String, dynamic>.from(body)
          : <String, dynamic>{};
      final code = details['code'] as String? ?? 'http_error';
      if (code == 'csrf_rejected' && retryCsrf && data is! Stream) {
        _csrf = null;
        return request(
          path,
          method: method,
          data: data,
          query: query,
          cancel: cancel,
          onProgress: onProgress,
          retryCsrf: false,
          headers: headers,
        );
      }
      if (!path.startsWith('/api/auth/') &&
          [
            'unauthorized',
            'session_locked',
            'password_change_required',
          ].contains(code)) {
        onAccessRequired?.call(code);
      }
      throw ApiFailure(
        response.statusCode ?? 500,
        code,
        details['message'] as String? ?? '服务请求失败',
        details,
      );
    }
    return body;
  }

  Future<Map<String, dynamic>> me() async =>
      Map<String, dynamic>.from(await request('/api/auth/me'));
  Future<Map<String, dynamic>> login(
    String password, {
    String captchaID = '',
    String captcha = '',
    String totp = '',
  }) async {
    final body = await request(
      '/api/auth/login',
      method: 'POST',
      data: {
        'username': account,
        'password': password,
        'remember': true,
        'captchaID': captchaID,
        'captcha': captcha,
        'totpCode': totp,
      },
    );
    return Map<String, dynamic>.from(body['user']);
  }

  Future<void> checkCapabilities() async {
    dynamic j;
    try {
      j = await request('/api/mobile/v1/capabilities');
    } on ApiFailure catch (error) {
      if (error.status == 404) {
        throw ApiFailure(409, 'mobile_upgrade_required', '服务端尚未支持手机连接，请升级服务端');
      }
      rethrow;
    }
    if (j is! Map ||
        j['protocolVersion'] != 1 ||
        j['viewAttach'] != true ||
        j['geometry'] != true ||
        j['inputAck'] != true) {
      throw ApiFailure(409, 'mobile_upgrade_required', '服务端尚未支持手机连接，请升级服务端');
    }
  }

  Future<List<RemoteSession>> sessions() async =>
      (await request('/api/sessions') as List)
          .map((j) => RemoteSession.fromJson(Map<String, dynamic>.from(j)))
          .toList();
  Future<List<RemoteHost>> hosts() async =>
      (await request('/api/hosts') as List)
          .map((j) => RemoteHost.fromJson(Map<String, dynamic>.from(j)))
          .where((h) => h.protocol == 'ssh' || h.protocol.isEmpty)
          .toList();
  Future<RemoteSession> create(
    RemoteHost host, {
    String? name,
    String secret = '',
    String trust = '',
  }) async => RemoteSession.fromJson(
    Map<String, dynamic>.from(
      await request(
        '/api/sessions',
        method: 'POST',
        data: {
          'hostID': host.id,
          'name': name ?? defaultSessionName(host),
          'sessionMode': host.config['sessionMode'] ?? 'tmux',
          'secret': secret,
          'trustFingerprint': trust,
        },
      ),
    ),
  );
  Future<RemoteHost> saveHost(Map<String, dynamic> value, {String? id}) async =>
      RemoteHost.fromJson(
        Map<String, dynamic>.from(
          await request(
            id == null ? '/api/hosts' : '/api/hosts/$id',
            method: id == null ? 'POST' : 'PUT',
            data: value,
          ),
        ),
      );

  Future<List<Map<String, dynamic>>> credentials() async =>
      (await request('/api/credentials') as List)
          .map((j) => Map<String, dynamic>.from(j))
          .toList();

  Future<List<RemoteWebService>> webServices() async =>
      (await request('/api/web-services') as List)
          .map((j) => RemoteWebService.fromJson(Map<String, dynamic>.from(j)))
          .toList();

  Future<RemoteWebService> saveWebService(
    Map<String, dynamic> value, {
    String? id,
  }) async => RemoteWebService.fromJson(
    Map<String, dynamic>.from(
      await request(
        id == null ? '/api/web-services' : '/api/web-services/$id',
        method: id == null ? 'POST' : 'PUT',
        data: value,
      ),
    ),
  );

  Future<WebServiceAccess> openWebService(RemoteWebService service) async {
    final result = await request(
      '/api/web-services/${service.id}/open',
      method: 'POST',
    );
    final raw = result['url'] as String;
    final parsed = Uri.parse(raw);
    // Existing web API returns root-relative paths. Resolve within a configured
    // reverse-proxy prefix while leaving explicit host-port URLs intact.
    final url = parsed.hasScheme ? parsed : endpoint(raw);
    if (!['http', 'https'].contains(url.scheme) ||
        url.userInfo.isNotEmpty ||
        url.host != base.host) {
      throw const FormatException('服务返回的代理访问地址无效');
    }
    if (service.proxyMode == 'host_port') return WebServiceAccess(url);
    final hash = base64Url
        .encode(sha256.convert(utf8.encode(service.id)).bytes)
        .replaceAll('=', '');
    final name = 'velin_web_service_${hash.substring(0, 12)}';
    final cookie = _cookies.values.where((c) => c['name'] == name).firstOrNull;
    if (cookie == null) throw const FormatException('未获取到代理访问凭据，请重试');
    // Only a service-scoped ticket reaches the browser. Account cookies stay
    // in the native HTTP client and are never exported to remote web content.
    return WebServiceAccess(
      url,
      cookieName: name,
      cookieValue: cookie['value'] as String,
      cookiePath: base.path,
    );
  }

  Future<void> restore(
    RemoteSession session, {
    String secret = '',
    String trust = '',
    String passphrase = '',
  }) async {
    await request(
      '/api/sessions/${session.id}/restore',
      method: 'POST',
      data: {
        'secret': secret,
        'trustFingerprint': trust,
        'passphrase': passphrase,
      },
    );
  }

  Future<WebSocket> connect(RemoteSession session, String device) async {
    final http = endpoint('/ws/sessions/${session.id}');
    final uri = http.replace(
      scheme: base.scheme == 'https' ? 'wss' : 'ws',
      queryParameters: {'attach': 'view', 'device': device},
    );
    var abandoned = false;
    return WebSocket.connect(
          '$uri',
          headers: {
            'Cookie': cookieHeader(http),
            'Origin': base.origin,
            'User-Agent': 'Velin-Mobile/0.1',
          },
        )
        .then((socket) {
          if (abandoned) unawaited(socket.close());
          return socket;
        })
        .timeout(
          const Duration(seconds: 20),
          onTimeout: () {
            abandoned = true;
            throw TimeoutException('终端连接超时');
          },
        );
  }

  Future<dynamic> upload(
    RemoteSession session,
    String remote,
    File file,
    CancelToken cancel,
    ProgressCallback onProgress,
  ) async {
    // Explicit CSRF setup before opening a non-replayable file stream.
    final token = await request('/api/csrf');
    _csrf = token['token'] as String;
    return request(
      '/api/sftp/${session.hostID}/upload',
      method: 'PUT',
      data: file.openRead(),
      query: {'session': session.id, 'path': remote, 'offset': 0},
      cancel: cancel,
      onProgress: onProgress,
      retryCsrf: false,
      headers: {
        'Content-Type': 'application/octet-stream',
        'Content-Length': await file.length(),
      },
    );
  }

  Future<void> logout() async {
    try {
      await request(
        '/api/auth/logout',
        method: 'POST',
        data: <String, dynamic>{},
      );
    } finally {
      await forgetAuthentication();
    }
  }

  Future<void> forgetAuthentication() async {
    _cookies.clear();
    _csrf = null;
    await _vault.delete(key: _storageKey);
  }

  void close() => _dio.close(force: true);
}
