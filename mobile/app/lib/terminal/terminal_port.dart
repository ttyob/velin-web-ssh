import 'package:uuid/uuid.dart';

class TerminalPort {
  final String sessionID;
  String generation = const Uuid().v4();
  void Function(Map<String, dynamic>)? onMessage;
  void Function(Map<String, dynamic>)? _send;
  final List<Map<String, dynamic>> _pending = [];
  TerminalPort(this.sessionID);
  void bind(void Function(Map<String, dynamic>) send) {
    generation = const Uuid().v4();
    _send = send;
    send({
      'version': 1,
      'type': 'configure',
      'sessionID': sessionID,
      'generation': generation,
      'rows': 30,
      'cols': 120,
    });
    for (final message in _pending) {
      push(message);
    }
    _pending.clear();
  }

  void unbind() {
    _send = null;
    generation = const Uuid().v4();
  }

  void push(Map<String, dynamic> message) {
    if (_send == null) {
      if (message['type'] == 'reset') {
        _pending.removeWhere(
          (item) => !['font', 'theme'].contains(item['type']),
        );
      }
      _pending.add(message);
      return;
    }
    _send!({
      'version': 1,
      ...message,
      'sessionID': sessionID,
      'generation': generation,
    });
  }

  void receive(Map<String, dynamic> message) {
    if (message['version'] != 1 ||
        message['sessionID'] != sessionID ||
        message['generation'] != generation) {
      return;
    }
    onMessage?.call(message);
  }
}
