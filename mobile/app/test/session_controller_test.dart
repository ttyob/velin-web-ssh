import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:velin_mobile/core/models.dart';
import 'package:velin_mobile/core/remote_api.dart';
import 'package:velin_mobile/terminal/session_controller.dart';

class TestApi extends RemoteApi {
  final Uri ws;
  TestApi(this.ws) : super(ws, 'tester');
  @override
  Future<void> restore(
    RemoteSession session, {
    String secret = '',
    String trust = '',
    String passphrase = '',
  }) async {}
  final queries = <Map<String, String>>[];
  @override
  Future<WebSocket> connect(
    RemoteSession session,
    String device, {
    String streamID = '',
    int offset = 0,
    int tail = 0,
  }) {
    queries.add({
      if (streamID.isNotEmpty) 'stream': streamID,
      if (streamID.isNotEmpty) 'offset': '$offset',
      if (tail > 0) 'tail': '$tail',
    });
    return WebSocket.connect('$ws');
  }
}

Future<void> until(bool Function() ready) async {
  for (var i = 0; i < 200; i++) {
    if (ready()) return;
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
  throw StateError('condition timed out');
}

void main() {
  test(
    'long-press selection opens native copy controls and clears on exit',
    () {
      const session = RemoteSession(id: 'selection', hostID: 'h', name: 'test');
      final controller = SessionController(null, session, 'device', demo: true);
      final messages = <Map<String, dynamic>>[];
      controller.port.bind(messages.add);
      void gesture(Map<String, dynamic> message) => controller.port.receive({
        'version': 1,
        'sessionID': session.id,
        'generation': controller.port.generation,
        ...message,
      });
      gesture({'type': 'selectionMode', 'enabled': true});
      gesture({'type': 'selection', 'text': 'hello 中文🙂'});
      expect(controller.selection, true);
      expect(controller.selectionText, 'hello 中文🙂');
      expect(controller.controlled, false);
      controller.toggleSelection();
      expect(controller.selection, false);
      expect(controller.selectionText, isEmpty);
      expect(messages.last['type'], 'selection');
      expect(messages.last['enabled'], false);
      gesture({
        'type': 'selectionMode',
        'enabled': true,
        'generation': 'stale',
      });
      expect(controller.selection, false);
      controller.dispose();
    },
  );

  test(
    'rename validates before PATCH, retains identity and preserves name on failure',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      HttpOverrides.global = null;
      final requests = <Map<String, dynamic>>[];
      var fail = false;
      final subscription = server.listen((request) async {
        request.response.headers.contentType = ContentType.json;
        if (request.uri.path == '/api/csrf') {
          request.response.write('{"token":"csrf"}');
        } else {
          expect(request.method, 'PATCH');
          expect(request.uri.path, '/api/sessions/rename');
          requests.add(
            Map<String, dynamic>.from(
              jsonDecode(await utf8.decoder.bind(request).join()),
            ),
          );
          if (fail) request.response.statusCode = 500;
          request.response.write(fail ? '{"message":"save failed"}' : '{}');
        }
        await request.response.close();
      });
      final api = RemoteApi(
        Uri.parse('http://127.0.0.1:${server.port}/'),
        'tester',
      );
      const session = RemoteSession(
        id: 'rename',
        hostID: 'h',
        name: 'original',
      );
      final controller = SessionController(api, session, 'device');
      final generation = controller.port.generation;
      await expectLater(controller.rename('  '), throwsFormatException);
      await expectLater(controller.rename('字' * 81), throwsFormatException);
      expect(requests, isEmpty);
      await controller.rename('  中文开发  ');
      expect(requests.single['name'], '中文开发');
      expect(controller.session.name, '中文开发');
      expect(controller.session.id, session.id);
      expect(controller.port.generation, generation);
      fail = true;
      await expectLater(
        controller.rename('failed'),
        throwsA(isA<ApiFailure>()),
      );
      expect(controller.session.name, '中文开发');
      controller.dispose();
      api.close();
      await subscription.cancel();
      await server.close(force: true);
    },
  );

  test(
    'replay ignores previews, de-duplicates output and gates control and input',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      late WebSocket peer;
      final inputs = <Map<String, dynamic>>[];
      final requests = <Map<String, dynamic>>[];
      final subscription = server.listen((request) async {
        peer = await WebSocketTransformer.upgrade(request);
        peer.listen((raw) {
          final e = Map<String, dynamic>.from(jsonDecode(raw as String));
          requests.add(e);
          if (e['type'] == 'request_control') {
            peer.add(jsonEncode({'type': 'control_granted'}));
          }
          if (e['type'] == 'input') {
            inputs.add(e);
            peer.add(
              jsonEncode({
                'type': 'input_ack',
                'inputID': e['inputID'],
                'result': 'written',
                'bytes': base64Decode(e['data']).length,
              }),
            );
          }
        });
        for (final event in [
          {
            'type': 'hello',
            'clientID': 'phone',
            'status': 'creating',
            'streamID': 'stream',
            'offset': 3,
            'rows': 30,
            'cols': 120,
          },
          {
            'type': 'replay_preview',
            'data': base64Encode(utf8.encode('PREVIEW')),
          },
          {
            'type': 'replay',
            'data': base64Encode(utf8.encode('one')),
            'replayFinal': true,
          },
          {
            'type': 'replay_live',
            'data': base64Encode(utf8.encode('two')),
            'offset': 6,
          },
          {
            'type': 'output',
            'data': base64Encode(utf8.encode('two')),
            'offset': 6,
          },
          {
            'type': 'output',
            'data': base64Encode(utf8.encode('two')),
            'offset': 6,
          },
        ]) {
          peer.add(jsonEncode(event));
        }
      });
      final api = TestApi(Uri.parse('ws://127.0.0.1:${server.port}/'));
      const session = RemoteSession(id: 'a', hostID: 'h', name: 'test');
      final controller = SessionController(api, session, 'device');
      final writes = <String>[];
      controller.port.bind((message) {
        if (message['type'] == 'write') {
          writes.add(utf8.decode(base64Decode(message['data'])));
          scheduleMicrotask(
            () => controller.port.receive({...message, 'type': 'rendered'}),
          );
        }
      });
      await controller.connect();
      await until(() => !controller.replaying && writes.length == 2);
      expect(writes, ['one', 'two']);
      expect(controller.controlled, false);
      expect(await controller.input(utf8.encode('blocked')), false);
      expect(inputs, isEmpty);
      // Opening a WebSocket is not proof that SSH is ready.
      controller.requestControl();
      await Future<void>.delayed(const Duration(milliseconds: 20));
      expect(requests.where((e) => e['type'] == 'request_control'), isEmpty);
      expect(controller.remoteReady, false);
      peer.add(jsonEncode({'type': 'status', 'status': 'attached'}));
      await until(() => controller.remoteReady);
      controller.requestControl();
      await until(() => controller.controlled);
      expect(await controller.input(utf8.encode('hello\r')), true);
      expect(inputs.length, 1);
      void gesture(Map<String, dynamic> message) => controller.port.receive({
        'version': 1,
        'sessionID': session.id,
        'generation': controller.port.generation,
        ...message,
      });
      // The outer tmux terminal uses an alternate screen even for a shell.
      // Route from the pane's real mode instead of xterm's outer mode.
      peer.add(
        jsonEncode({
          'type': 'history_state',
          'historySize': 120,
          'alternateScreen': false,
        }),
      );
      await until(() => controller.historySize == 120);
      gesture({'type': 'modes', 'alternate': true});
      expect(controller.applicationAlternate, false);
      gesture({'type': 'scroll', 'lines': -1, 'row': 5, 'column': 8});
      await until(() => requests.any((e) => e['type'] == 'scroll_history_to'));
      expect(requests.last['position'], 119);
      peer.add(
        jsonEncode({
          'type': 'history_state',
          'historySize': 120,
          'alternateScreen': true,
        }),
      );
      await until(() => controller.applicationAlternate);
      gesture({'type': 'scroll', 'lines': -40, 'row': 5, 'column': 8});
      await until(() => requests.any((e) => e['type'] == 'scroll_application'));
      expect(requests.last['lines'], -3);
      peer.add(
        jsonEncode({'type': 'status', 'status': 'ended', 'message': '远端程序已退出'}),
      );
      await until(() => !controller.connected);
      await Future<void>.delayed(const Duration(milliseconds: 30));
      expect(controller.status, '需要重连');
      expect(controller.detail, '远端程序已退出');
      controller.pause();
      expect(controller.controlled, false);
      expect(controller.connected, false);
      expect(controller.canInput, false);
      controller.dispose();
      api.close();
      await peer.close();
      await subscription.cancel();
      await server.close(force: true);
    },
  );

  test('a huge replay renders only its tail, as one write', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    late WebSocket peer;
    final subscription = server.listen((request) async {
      peer = await WebSocketTransformer.upgrade(request);
      peer.add(
        jsonEncode({
          'type': 'hello',
          'clientID': 'phone',
          'status': 'attached',
          'streamID': 'stream',
          'offset': 0,
          'rows': 30,
          'cols': 120,
        }),
      );
      // ~6 MB of history in 96 chunks, ending with the visible screen.
      for (var i = 0; i < 96; i++) {
        final last = i == 95;
        final text = last
            ? '\x1b[H\x1b[2JLAST SCREEN\r\n'
            : 'history line $i ${'x' * 60}\r\n' * 900;
        peer.add(
          jsonEncode({
            'type': 'replay',
            'data': base64Encode(utf8.encode(text)),
            'replayFinal': last,
          }),
        );
      }
    });
    final api = TestApi(Uri.parse('ws://127.0.0.1:${server.port}/'));
    const session = RemoteSession(id: 'big', hostID: 'h', name: 'test');
    final controller = SessionController(api, session, 'device');
    final writes = <Map<String, dynamic>>[];
    controller.port.bind((message) {
      if (message['type'] == 'write') {
        writes.add(message);
        scheduleMicrotask(
          () => controller.port.receive({...message, 'type': 'rendered'}),
        );
      }
    });
    await controller.connect();
    await until(() => !controller.replaying && writes.isNotEmpty);
    expect(writes.length, 1);
    expect(writes.single['id'], 'replay-final');
    expect(writes.single['bytes'] as int, lessThan(200 * 1024));
    final rendered = utf8.decode(base64Decode(writes.single['data']));
    expect(rendered, contains('LAST SCREEN'));
    expect(rendered, isNot(contains('history line 3 ')));
    controller.dispose();
    api.close();
    await subscription.cancel();
    await server.close(force: true);
  });

  test('scrolling an application in view mode explains why nothing moves', () {
    const session = RemoteSession(id: 'hint', hostID: 'h', name: 'test');
    final controller = SessionController(null, session, 'device', demo: true);
    controller.port.bind((_) {});
    void scroll() => controller.port.receive({
      'version': 1,
      'sessionID': session.id,
      'generation': controller.port.generation,
      'type': 'scroll',
      'lines': -3,
      'row': 1,
      'column': 1,
    });
    scroll();
    expect(controller.scrollHint, contains('接管'));
    controller.scrollHint = '';
    scroll(); // rate limited: no repeat toast while the finger keeps moving
    expect(controller.scrollHint, isEmpty);
  });

  test(
    'taking control asks for a forced takeover and retries while pending',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final requests = <Map<String, dynamic>>[];
      final subscription = server.listen((request) async {
        final peer = await WebSocketTransformer.upgrade(request);
        peer.listen((raw) {
          final e = Map<String, dynamic>.from(jsonDecode(raw as String));
          requests.add(e);
          // An older server: ignores `force`, keeps asking the computer.
          if (e['type'] == 'request_control') {
            peer.add(jsonEncode({'type': 'control_pending'}));
          }
        });
        peer.add(
          jsonEncode({
            'type': 'hello',
            'clientID': 'phone',
            'status': 'attached',
            'streamID': 's',
            'offset': 0,
            'rows': 30,
            'cols': 120,
          }),
        );
        peer.add(jsonEncode({'type': 'replay_end'}));
      });
      final api = TestApi(Uri.parse('ws://127.0.0.1:${server.port}/'));
      const session = RemoteSession(id: 'take', hostID: 'h', name: 'test');
      final controller = SessionController(api, session, 'device');
      controller.port.bind((message) {
        if (message['type'] == 'write') {
          scheduleMicrotask(
            () => controller.port.receive({...message, 'type': 'rendered'}),
          );
        }
      });
      await controller.connect();
      await until(() => !controller.replaying && controller.remoteReady);
      controller.requestControl();
      await until(
        () => requests.where((e) => e['type'] == 'request_control').isNotEmpty,
      );
      expect(
        requests.firstWhere((e) => e['type'] == 'request_control')['force'],
        true,
      );
      await until(() => controller.status.contains('自动重试'));
      controller.dispose();
      api.close();
      await subscription.cancel();
      await server.close(force: true);
    },
  );

  Future<(List<String>, TestApi)> reconnectWith(
    List<List<Map<String, dynamic>>> responses,
  ) async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var connection = 0;
    final subscription = server.listen((request) async {
      final peer = await WebSocketTransformer.upgrade(request);
      peer.listen((_) {});
      for (final event in responses[connection++]) {
        peer.add(jsonEncode(event));
      }
    });
    final api = TestApi(Uri.parse('ws://127.0.0.1:${server.port}/'));
    const session = RemoteSession(id: 'resume', hostID: 'h', name: 'test');
    final controller = SessionController(api, session, 'device');
    final writes = <String>[];
    controller.port.bind((message) {
      if (message['type'] == 'write') {
        writes.add(utf8.decode(base64Decode(message['data'])));
        scheduleMicrotask(
          () => controller.port.receive({...message, 'type': 'rendered'}),
        );
      }
    });
    await controller.connect();
    await until(() => !controller.replaying && writes.length == 1);
    await controller.connect();
    await until(() => !controller.replaying && writes.length == 2);
    controller.dispose();
    api.close();
    await subscription.cancel();
    await server.close(force: true);
    return (writes, api);
  }

  Map<String, dynamic> hello(
    String stream,
    int offset, {
    bool truncated = false,
  }) => {
    'type': 'hello',
    'clientID': 'phone',
    'status': 'attached',
    'streamID': stream,
    'offset': offset,
    if (truncated) 'truncated': true,
    'rows': 30,
    'cols': 120,
  };
  Map<String, dynamic> replay(String text, {bool last = false}) => {
    'type': 'replay',
    'data': base64Encode(utf8.encode(text)),
    if (last) 'replayFinal': true,
  };

  test(
    'reconnect resumes from the known stream offset and appends the delta',
    () async {
      final (writes, api) = await reconnectWith([
        [hello('s1', 10), replay('abcde'), replay('fghij', last: true)],
        [hello('s1', 14), replay('klmn', last: true)],
      ]);
      expect(api.queries[0], {'tail': '262144'});
      expect(api.queries[1], {
        'stream': 's1',
        'offset': '10',
        'tail': '262144',
      });
      expect(writes, ['abcdefghij', 'abcdefghijklmn']);
    },
  );

  test('a different or truncated stream discards the cached output', () async {
    final (otherStream, _) = await reconnectWith([
      [hello('s1', 10), replay('abcdefghij', last: true)],
      [hello('s2', 4), replay('newer', last: true)],
    ]);
    expect(otherStream, ['abcdefghij', 'newer']);
    final (truncated, _) = await reconnectWith([
      [hello('s1', 10), replay('abcdefghij', last: true)],
      [hello('s1', 30, truncated: true), replay('fresh', last: true)],
    ]);
    expect(truncated, ['abcdefghij', 'fresh']);
  });
}
