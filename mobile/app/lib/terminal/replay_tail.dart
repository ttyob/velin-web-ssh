import 'dart:typed_data';

/// Keeps only the end of a long output replay.
///
/// Opening a session replays the server's whole output buffer. Parsing
/// megabytes of old Codex/CLI history is slow and ends on the last screen
/// anyway, so only the tail is rendered. The cut is made at a point that keeps
/// the terminal state valid: the last screen clear / alternate-screen entry in
/// the retained bytes, otherwise a line boundary. Modes switched on in the
/// skipped part (bracketed paste, mouse reporting, ...) are re-applied.
class ReplayTail {
  final int limit;
  ReplayTail({this.limit = 128 * 1024});

  final _chunks = <Uint8List>[];
  int _size = 0;
  bool _cut = false;
  final _modes = <int, bool>{};

  static final _modeSequence = RegExp(r'\x1b\[\?([0-9;]+)([hl])');
  // ESC[2J (optionally after ESC[H) and alternate-screen entry sequences.
  static const _markers = <List<int>>[
    [0x1b, 0x5b, 0x32, 0x4a],
    [0x1b, 0x5b, 0x3f, 0x31, 0x30, 0x34, 0x39, 0x68],
    [0x1b, 0x5b, 0x3f, 0x31, 0x30, 0x34, 0x37, 0x68],
    [0x1b, 0x5b, 0x3f, 0x34, 0x37, 0x68],
  ];
  static const _altScreenModes = {47, 1047, 1049};

  bool get truncated => _cut;
  int get retainedBytes => _size;

  void add(Uint8List bytes) {
    if (bytes.isEmpty) return;
    _chunks.add(bytes);
    _size += bytes.length;
    while (_chunks.length > 1 && _size - _chunks.first.length >= limit) {
      final dropped = _chunks.removeAt(0);
      _size -= dropped.length;
      _track(dropped);
      _cut = true;
    }
  }

  void _track(Uint8List bytes) {
    for (final m in _modeSequence.allMatches(String.fromCharCodes(bytes))) {
      for (final part in m.group(1)!.split(';')) {
        final mode = int.tryParse(part);
        if (mode != null) _modes[mode] = m.group(2) == 'h';
      }
    }
  }

  /// The bytes to render, and resets the collector.
  Uint8List finish() {
    var data = Uint8List(_size);
    var at = 0;
    for (final chunk in _chunks) {
      data.setRange(at, at + chunk.length, chunk);
      at += chunk.length;
    }
    final cut = _cut;
    _chunks.clear();
    _size = 0;
    _cut = false;
    if (!cut) {
      _modes.clear();
      return data;
    }
    var start = 0;
    var marker = -1;
    for (final pattern in _markers) {
      marker = marker > _lastIndexOf(data, pattern)
          ? marker
          : _lastIndexOf(data, pattern);
    }
    if (marker >= 0) {
      // Keep a preceding cursor-home with the clear.
      start =
          marker >= 3 &&
              data[marker - 3] == 0x1b &&
              data[marker - 2] == 0x5b &&
              data[marker - 1] == 0x48
          ? marker - 3
          : marker;
      _track(Uint8List.sublistView(data, 0, start));
    } else {
      // Start on a whole line: the first chunk was cut at an arbitrary byte.
      final newline = data.indexOf(0x0a, 0);
      if (newline >= 0 && newline < 8192) {
        start = newline + 1;
        _track(Uint8List.sublistView(data, 0, start));
      }
    }
    final inAlternate =
        marker < 0 && _altScreenModes.any((mode) => _modes[mode] == true);
    final prefix = StringBuffer('\x1b[0m');
    if (inAlternate) prefix.write('\x1b[?1049h');
    _modes.forEach((mode, on) {
      if (_altScreenModes.contains(mode)) return;
      if (on && mode != 25) prefix.write('\x1b[?${mode}h');
      if (!on && mode == 25) prefix.write('\x1b[?25l');
    });
    _modes.clear();
    final head = Uint8List.fromList(prefix.toString().codeUnits);
    final body = Uint8List.sublistView(data, start);
    data = Uint8List(head.length + body.length)
      ..setRange(0, head.length, head)
      ..setRange(head.length, head.length + body.length, body);
    return data;
  }

  static int _lastIndexOf(Uint8List data, List<int> pattern) {
    for (var i = data.length - pattern.length; i >= 0; i--) {
      var match = true;
      for (var j = 0; j < pattern.length; j++) {
        if (data[i + j] != pattern[j]) {
          match = false;
          break;
        }
      }
      if (match) return i;
    }
    return -1;
  }
}

/// The newest bytes of a terminal stream, kept exactly (no synthetic prefix),
/// so they can be cached and resumed from a known stream offset.
class StreamTail {
  final int max;
  StreamTail({this.max = 256 * 1024});

  final _chunks = <Uint8List>[];
  int _size = 0;

  int get size => _size;

  void add(Uint8List bytes) {
    if (bytes.isEmpty) return;
    _chunks.add(bytes);
    _size += bytes.length;
    while (_chunks.length > 1 && _size - _chunks.first.length >= max) {
      _size -= _chunks.removeAt(0).length;
    }
    if (_size > max) {
      final first = _chunks.removeAt(0);
      final kept = Uint8List.sublistView(first, _size - max);
      _chunks.insert(0, kept);
      _size = max;
    }
  }

  Uint8List bytes() {
    final out = Uint8List(_size);
    var at = 0;
    for (final chunk in _chunks) {
      out.setRange(at, at + chunk.length, chunk);
      at += chunk.length;
    }
    return out;
  }

  void clear() {
    _chunks.clear();
    _size = 0;
  }
}
