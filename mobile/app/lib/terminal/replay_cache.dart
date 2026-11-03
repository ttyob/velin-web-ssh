import 'dart:convert';
import 'dart:io';
import 'package:flutter/foundation.dart';
import 'package:path_provider/path_provider.dart';

/// The end of a session's output stream as of [end] (a server stream offset).
class CachedStream {
  final String streamID;
  final int end;
  final Uint8List bytes;
  const CachedStream(this.streamID, this.end, this.bytes);
}

/// Device-local copy of the latest output of each session, so reopening it only
/// has to download what was printed since. Lives in the app's private storage.
/// Every operation is best effort: a failure just means a full replay.
abstract final class ReplayCache {
  static const _keep = Duration(days: 14);
  static bool _pruned = false;

  static Future<Directory?> _directory() async {
    if (kIsWeb) return null;
    final base = await getApplicationSupportDirectory();
    final directory = Directory('${base.path}/terminal_cache');
    if (!await directory.exists()) await directory.create(recursive: true);
    return directory;
  }

  static String _name(String sessionID) =>
      '${sessionID.replaceAll(RegExp(r'[^A-Za-z0-9_-]'), '_')}.bin';

  static Future<CachedStream?> load(String sessionID) async {
    try {
      final directory = await _directory();
      if (directory == null) return null;
      final file = File('${directory.path}/${_name(sessionID)}');
      if (!await file.exists()) return null;
      final data = await file.readAsBytes();
      final newline = data.indexOf(0x0a);
      if (newline <= 0) return null;
      final header =
          jsonDecode(utf8.decode(data.sublist(0, newline)))
              as Map<String, dynamic>;
      final body = Uint8List.sublistView(data, newline + 1);
      final end = (header['end'] as num?)?.toInt() ?? -1;
      final stream = header['stream'] as String? ?? '';
      if (header['v'] != 1 ||
          stream.isEmpty ||
          end < 0 ||
          header['len'] != body.length) {
        return null;
      }
      return CachedStream(stream, end, body);
    } catch (_) {
      return null;
    }
  }

  static Future<void> save(String sessionID, CachedStream cache) async {
    try {
      final directory = await _directory();
      if (directory == null) return;
      final header = utf8.encode(
        '${jsonEncode({'v': 1, 'stream': cache.streamID, 'end': cache.end, 'len': cache.bytes.length})}\n',
      );
      final target = File('${directory.path}/${_name(sessionID)}');
      final temporary = File('${target.path}.tmp');
      await temporary.writeAsBytes([...header, ...cache.bytes], flush: true);
      await temporary.rename(target.path);
      if (!_pruned) {
        _pruned = true;
        await _prune(directory);
      }
    } catch (_) {}
  }

  static Future<void> delete(String sessionID) async {
    try {
      final directory = await _directory();
      if (directory == null) return;
      final file = File('${directory.path}/${_name(sessionID)}');
      if (await file.exists()) await file.delete();
    } catch (_) {}
  }

  static Future<void> clear() async {
    try {
      final directory = await _directory();
      if (directory != null && await directory.exists()) {
        await directory.delete(recursive: true);
      }
    } catch (_) {}
  }

  static Future<void> _prune(Directory directory) async {
    final limit = DateTime.now().subtract(_keep);
    await for (final entry in directory.list()) {
      if (entry is File && (await entry.lastModified()).isBefore(limit)) {
        await entry.delete();
      }
    }
  }
}
