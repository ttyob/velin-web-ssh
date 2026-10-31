import 'dart:convert';
import 'dart:io';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:velin_mobile/core/models.dart';
import 'package:velin_mobile/core/remote_api.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    FlutterSecureStorage.setMockInitialValues({});
    HttpOverrides.global =
        null; // Loopback contract tests use a real HTTP peer.
  });
  test(
    'native login preserves Set-Cookie and CSRF across restart and account isolation',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final uri = Uri.parse('http://127.0.0.1:${server.port}/');
      final observed = <String>[];
      final subscription = server.listen((request) async {
        request.response.headers.contentType = ContentType.json;
        if (request.uri.path == '/api/csrf') {
          request.response.cookies.add(
            Cookie('velin_csrf_http', 'token')..path = '/',
          );
          request.response.write('{"token":"token"}');
        } else if (request.uri.path == '/api/auth/login') {
          observed.add(request.headers.value('x-csrf-token') ?? '');
          expect(
            request.cookies.any(
              (c) => c.name == 'velin_csrf_http' && c.value == 'token',
            ),
            true,
          );
          final body = jsonDecode(await utf8.decoder.bind(request).join());
          expect(body['username'], 'tester');
          request.response.cookies.add(
            Cookie('velin_session_http', 'session')
              ..path = '/'
              ..httpOnly = true
              ..maxAge = 3600,
          );
          request.response.write('{"user":{"username":"tester"}}');
        } else {
          observed.add(
            request.cookies.any(
                  (c) => c.name == 'velin_session_http' && c.value == 'session',
                )
                ? 'authenticated'
                : 'missing',
          );
          request.response.write('{"username":"tester"}');
        }
        await request.response.close();
      });
      final api = RemoteApi(uri, 'tester');
      expect((await api.login('password'))['username'], 'tester');
      final restored = RemoteApi(uri, 'tester');
      await restored.restoreCookies();
      await restored.me();
      final other = RemoteApi(uri, 'other');
      await other.restoreCookies();
      await other.me();
      expect(observed, ['token', 'authenticated', 'missing']);
      api.close();
      restored.close();
      other.close();
      await subscription.cancel();
      await server.close(force: true);
    },
  );
  test('CSRF refresh retries a rejected request only once', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    int attempts = 0, bootstraps = 0;
    final subscription = server.listen((request) async {
      await request.drain<void>();
      request.response.headers.contentType = ContentType.json;
      if (request.uri.path == '/api/csrf') {
        bootstraps++;
        request.response.write('{"token":"csrf"}');
      } else {
        attempts++;
        request.response.statusCode = 403;
        request.response.write('{"code":"csrf_rejected","message":"rejected"}');
      }
      await request.response.close();
    });
    final api = RemoteApi(
      Uri.parse('http://127.0.0.1:${server.port}/'),
      'tester',
    );
    await expectLater(
      api.request('/api/action', method: 'POST', data: {'x': 1}),
      throwsA(isA<ApiFailure>()),
    );
    expect(attempts, 2);
    expect(bootstraps, 2);
    api.close();
    await subscription.cancel();
    await server.close(force: true);
  });
  test(
    'service address rejects embedded credentials and keeps path prefixes',
    () {
      expect(() => serviceURI('ftp://example.com'), throwsFormatException);
      expect(
        () => serviceURI('https://user:secret@example.com'),
        throwsFormatException,
      );
      expect(
        serviceURI('https://example.com/velin').resolve('api/csrf').path,
        '/velin/api/csrf',
      );
    },
  );
}
