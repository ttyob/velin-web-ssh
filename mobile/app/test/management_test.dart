import 'dart:convert';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:velin_mobile/core/models.dart';
import 'package:velin_mobile/core/remote_api.dart';
import 'package:velin_mobile/core/service_profiles.dart';
import 'package:velin_mobile/ui/host_editor.dart';
import 'package:velin_mobile/ui/theme.dart';

class HostApi extends RemoteApi {
  Map<String, dynamic>? saved;
  HostApi() : super(Uri.parse('http://localhost/'), 'tester');
  @override
  Future<List<Map<String, dynamic>>> credentials() async => [];
  @override
  Future<RemoteHost> saveHost(Map<String, dynamic> value, {String? id}) async {
    saved = value;
    return RemoteHost.fromJson({...value, 'id': id!});
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test(
    'legacy service profiles keep aliases across login and isolate cache on edit',
    () async {
      SharedPreferences.setMockInitialValues({
        'mobile.profiles': [
          jsonEncode({'server': 'https://old.example/', 'account': 'dev'}),
        ],
        'mobile.draft.https://old.example/|dev.session': 'old draft',
      });
      final prefs = await SharedPreferences.getInstance();
      final store = ServiceProfiles(prefs);
      expect(store.all.single.name, '');
      await store.replace(
        store.all.single,
        const ServiceProfile('https://new.example/', 'dev', name: '开发'),
      );
      await store.remember(const ServiceProfile('https://new.example/', 'dev'));
      expect(store.all.single.name, '开发');
      expect(
        prefs.getString('mobile.draft.https://new.example/|dev.session'),
        isNull,
      );
      expect(
        prefs.getString('mobile.draft.https://old.example/|dev.session'),
        'old draft',
      );
    },
  );

  test(
    'existing host-named sessions have distinct labels without renaming remote tasks',
    () {
      const host = RemoteHost(id: 'h', name: '开发机', address: 'localhost');
      const a = RemoteSession(
        id: '7a6e52ba-1401-4a07-9ff3-11aafef70001',
        hostID: 'h',
        name: '开发机',
      );
      const b = RemoteSession(
        id: '7a6e52ba-1401-4a07-9ff3-11aafef70002',
        hostID: 'h',
        name: '开发机',
      );
      expect(a.displayName(host), isNot(b.displayName(host)));
      expect(a.name, '开发机');
      final name = defaultSessionName(host, DateTime(2026, 10, 31, 16, 2, 3));
      expect(name, '开发机 · 1031-160203');
    },
  );

  testWidgets(
    'host edit preserves saved password and advanced connection options',
    (tester) async {
      final api = HostApi();
      final host = RemoteHost.fromJson({
        'id': 'host',
        'name': '开发机',
        'address': '127.0.0.1',
        'username': 'dev',
        'port': 2222,
        'protocol': 'ssh',
        'hasPassword': true,
        'connectTimeout': 45,
        'keepaliveInterval': 60,
        'maxRetries': 9,
        'terminalType': 'screen-256color',
        'tags': 'keep',
        'initialDirectory': '/srv/project',
        'sessionMode': 'normal',
      });
      await tester.pumpWidget(
        MaterialApp(
          theme: V.theme(),
          home: HostEditor(api: api, host: host, hosts: [host]),
        ),
      );
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('保存主机'));
      await tester.tap(find.text('保存主机'));
      await tester.pumpAndSettle();
      expect(api.saved, isNotNull);
      for (final key in [
        'connectTimeout',
        'keepaliveInterval',
        'maxRetries',
        'terminalType',
        'tags',
        'initialDirectory',
        'sessionMode',
        'port',
      ]) {
        expect(api.saved![key], host.config[key], reason: key);
      }
      expect(api.saved!['authMode'], 'password');
      expect(api.saved!['password'], ''); // Existing secret remains on server.
      api.close();
    },
  );

  test(
    'management API keeps path prefixes and exports only service-scoped browser access',
    () async {
      HttpOverrides.global = null;
      FlutterSecureStorage.setMockInitialValues({});
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      // Shared fixture with Go's security.TokenHash: SHA-256, raw URL base64.
      const ticketName = 'velin_web_service_EkGTbU3TqtaP';
      bool badURL = false;
      final posts = <String, dynamic>{};
      final subscription = server.listen((r) async {
        r.response.headers.contentType = ContentType.json;
        if (r.uri.path == '/velin/api/csrf') {
          r.response.write('{"token":"csrf"}');
        } else if (r.uri.path == '/velin/api/auth/login') {
          await r.drain<void>();
          r.response.cookies.add(
            Cookie('velin_session_http', 'account-secret')
              ..httpOnly = true
              ..path = '/',
          );
          r.response.write('{"user":{"username":"dev"}}');
        } else if (r.uri.path == '/velin/api/sessions') {
          final body = jsonDecode(await utf8.decoder.bind(r).join());
          posts.addAll(Map<String, dynamic>.from(body));
          r.response.write(jsonEncode({...body, 'id': 'session'}));
        } else if (r.uri.path == '/velin/api/web-services/proxy/open') {
          await r.drain<void>();
          expect(r.cookies.any((c) => c.value == 'account-secret'), true);
          r.response.cookies.add(
            Cookie(ticketName, 'service-ticket')
              ..httpOnly = true
              ..path = '/',
          );
          r.response.write(
            jsonEncode({
              'url': badURL
                  ? 'https://other.example/?secret=x'
                  : '/?__velin_web_service=proxy',
            }),
          );
        } else {
          r.response.statusCode = 404;
          r.response.write('{}');
        }
        await r.response.close();
      });
      final api = RemoteApi(
        Uri.parse('http://127.0.0.1:${server.port}/velin/'),
        'dev',
      );
      try {
        await api.login('password');
        final host = RemoteHost.fromJson({
          'id': 'h',
          'name': '主机',
          'address': '127.0.0.1',
          'sessionMode': 'normal',
        });
        await api.create(host, name: '前端开发');
        expect(posts['name'], '前端开发');
        expect(posts['sessionMode'], 'normal');
        const service = RemoteWebService(
          id: 'proxy',
          hostID: 'h',
          name: '预览',
          targetURL: 'http://localhost',
        );
        final access = await api.openWebService(service);
        expect(access.url.path, '/velin/');
        expect(access.url.queryParameters['__velin_web_service'], 'proxy');
        expect(access.cookieName, ticketName);
        expect(access.cookieValue, 'service-ticket');
        expect(access.cookiePath, '/velin/');
        expect(access.cookieValue, isNot('account-secret'));
        badURL = true;
        await expectLater(api.openWebService(service), throwsFormatException);
      } finally {
        api.close();
        await subscription.cancel();
        await server.close(force: true);
      }
    },
  );
}
