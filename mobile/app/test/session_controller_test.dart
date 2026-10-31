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
  @override
  Future<WebSocket> connect(RemoteSession session, String device) =>
      WebSocket.connect('$ws');
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
}
