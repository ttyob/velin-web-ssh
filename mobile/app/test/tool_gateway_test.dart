import 'dart:convert';
import 'dart:io';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:velin_mobile/core/models.dart';
import 'package:velin_mobile/core/remote_api.dart';
import 'package:velin_mobile/tools/tool_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    HttpOverrides.global = null;
    FlutterSecureStorage.setMockInitialValues({});
  });
  test(
    'tools require a local capability and preserve native auth for streamed HTTP',
    () async {
      final upstream = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final observed = <String>[];
      final subscription = upstream.listen((request) async {
        observed.add(request.uri.toString());
        if (request.uri.path == '/api/csrf') {
          request.response.cookies.add(
            Cookie('account_session', 'native-only')..httpOnly = true,
          );
          request.response.headers.contentType = ContentType.json;
          request.response.write('{"token":"csrf-token"}');
        } else if (request.uri.path.endsWith('/upload')) {
          expect(
            request.cookies.any(
              (c) => c.name == 'account_session' && c.value == 'native-only',
            ),
            true,
          );
          expect(request.headers.value('x-csrf-token'), 'csrf-token');
          expect(
            request.headers.contentType?.mimeType,
            'application/octet-stream',
          );
          final bytes = await request.fold<List<int>>(
            [],
            (all, chunk) => all..addAll(chunk),
          );
          expect(bytes, [0, 1, 127, 128, 255]);
          request.response.headers.contentType = ContentType.json;
          request.response.write('{"offset":5}');
        } else {
          request.response.headers.contentType = ContentType.html;
          request.response.write('<html>tools</html>');
        }
        await request.response.close();
      });
      final api = RemoteApi(
        Uri.parse('http://127.0.0.1:${upstream.port}/'),
        'tester',
      );
      final gateway = ToolGateway(
        api: api,
        tool: 'files',
        session: const RemoteSession(id: 's', hostID: 'h', name: 'Session'),
        host: const RemoteHost(
          id: 'h',
          name: 'Host',
          address: 'localhost',
          config: {'password': 'must-not-leak'},
        ),
      );
      await gateway.start();
      final client = HttpClient();
      addTearDown(() async {
        client.close(force: true);
        await gateway.close();
        api.close();
        await subscription.cancel();
        await upstream.close(force: true);
      });
      Future<HttpClientResponse> get(
        String path, {
        String? cookie,
        Map<String, String> headers = const {},
      }) async {
        final request = await client.getUrl(gateway.origin.resolve(path));
        headers.forEach(request.headers.set);
        if (cookie != null) request.headers.set('cookie', cookie);
        return request.close();
      }

      final denied = await get('/mobile-tools');
      expect(denied.statusCode, 404);
      await denied.drain<void>();
      expect(observed, isEmpty);
      final bootstrap = await get(
        '/mobile-tools',
        headers: gateway.bootstrapHeaders,
      );
      expect(bootstrap.statusCode, 200);
      final cookie = bootstrap.cookies.single;
      expect(cookie.httpOnly, true);
      final capability = '${cookie.name}=${cookie.value}';
      expect(await utf8.decoder.bind(bootstrap).join(), contains('tools'));
      final repeat = await get(
        '/mobile-tools',
        headers: gateway.bootstrapHeaders,
      );
      expect(repeat.statusCode, 404);
      await repeat.drain<void>();
      final cross = await get(
        '/native/context',
        cookie: capability,
        headers: {'Origin': 'https://foreign.test'},
      );
      expect(cross.statusCode, 403);
      await cross.drain<void>();
      final context = await get('/native/context', cookie: capability);
      final body = await utf8.decoder.bind(context).join();
      expect(jsonDecode(body)['session']['id'], 's');
      expect(body, isNot(contains('must-not-leak')));
      expect(body, isNot(contains('native-only')));
      for (final path in [
        '/api/hosts',
        '/api/sftp/other/list',
        '/api/sessions/other/directory',
        '/api/hosts/h/agent/command',
        '/ws/hosts/h/agent',
      ]) {
        final response = await get(path, cookie: capability);
        expect(response.statusCode, 403, reason: path);
        await response.drain<void>();
      }
      final csrf = await get('/api/csrf', cookie: capability);
      expect(csrf.headers['set-cookie'], isNull);
      expect(await utf8.decoder.bind(csrf).join(), contains('csrf-token'));
      expect(api.cookieHeader(api.base), contains('native-only'));
      final upload = await client.putUrl(
        gateway.origin.resolve('/api/sftp/h/upload?session=s&path=/test'),
      );
      upload.headers.set('cookie', capability);
      upload.headers.set('x-csrf-token', 'csrf-token');
      upload.headers.contentType = ContentType.binary;
      upload.add([0, 1]);
      upload.add([127, 128, 255]);
      final result = await upload.close();
      expect(result.statusCode, 200);
      expect(jsonDecode(await utf8.decoder.bind(result).join())['offset'], 5);
      expect(gateway.allows('/assets/../api/hosts'), false);
    },
  );

  test(
    'Agent sockets relay text and binary, then close with the tool',
    () async {
      final upstream = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      WebSocket? remote;
      final subscription = upstream.listen((request) async {
        if (WebSocketTransformer.isUpgradeRequest(request)) {
          remote = await WebSocketTransformer.upgrade(request);
          remote!.listen((value) => remote!.add(value));
        } else {
          request.response.write('tools');
          await request.response.close();
        }
      });
      final api = RemoteApi(
        Uri.parse('http://127.0.0.1:${upstream.port}/'),
        'tester',
      );
      final gateway = ToolGateway(
        api: api,
        tool: 'agent',
        session: const RemoteSession(id: 's', hostID: 'h', name: 'Session'),
        host: const RemoteHost(id: 'h', name: 'Host', address: 'localhost'),
      );
      await gateway.start();
      final client = HttpClient();
      final request = await client.getUrl(gateway.page);
      gateway.bootstrapHeaders.forEach(request.headers.set);
      final page = await request.close();
      final cookie = page.cookies.single;
      await page.drain<void>();
      final socket = await WebSocket.connect(
        gateway.origin
            .replace(scheme: 'ws', path: '/ws/hosts/h/agent')
            .toString(),
        headers: {'Cookie': '${cookie.name}=${cookie.value}'},
      );
      final messages = socket.asBroadcastStream();
      final text = messages.first;
      socket.add('command');
      expect(await text.timeout(const Duration(seconds: 3)), 'command');
      final binary = messages.first;
      socket.add([0, 255, 128]);
      expect(await binary.timeout(const Duration(seconds: 3)), [0, 255, 128]);
      final done = socket.done;
      await gateway.close();
      await done.timeout(const Duration(seconds: 3));
      client.close(force: true);
      api.close();
      await remote?.close();
      await subscription.cancel();
      await upstream.close(force: true);
    },
  );
}
