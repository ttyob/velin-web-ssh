import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter/foundation.dart';
import 'package:uuid/uuid.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import 'replay_cache.dart';
import 'replay_tail.dart';
import 'terminal_port.dart';

class SessionController extends ChangeNotifier {
  final RemoteApi? api;
  RemoteSession session;
  final String device;
  final TerminalPort port;
  final bool demo;
  WebSocket? _socket;
  StreamSubscription<dynamic>? _subscription;
  Timer? _heartbeat, _retry, _historyRefresh;
  int historySize = 0, historyPosition = 0, _historySequence = 0;
  bool remoteAlternate = false, localAlternate = false;
  bool get applicationAlternate =>
      session.mode == 'tmux' ? remoteAlternate : localAlternate;
  Completer<bool>? _pendingInput;
  String? _pendingID;
  int _connection = 0, _receivedOffset = 0, _queuedBytes = 0, _attempts = 0;
  bool _active = true, _disposed = false;
  bool connected = false,
      remoteReady = false,
      controlled = false,
      replaying = true,
      sending = false,
      selection = false,
      following = true,
      bracketed = false;
  String status = '正在连接', detail = '', selectionText = '';
  double fontSize = 13;
  Future<void> Function(Map<String, dynamic>)? onRecovery;
  Future<void> Function()? onContextMenu;
  SessionController(this.api, this.session, this.device, {this.demo = false})
    : port = TerminalPort(session.id) {
    port.onMessage = _bridge;
  }
  bool get canInput =>
      connected && remoteReady && controlled && !replaying && !sending;
  void _notify() {
    if (!_disposed) notifyListeners();
  }

  Future<void> connect({
    String secret = '',
    String trust = '',
    String passphrase = '',
  }) async {
    if (_disposed || !_active) return;
    final current = ++_connection;
    _retry?.cancel();
    _heartbeat?.cancel();
    _historyRefresh?.cancel();
    _historyRefresh = null;
    await _subscription?.cancel();
    unawaited(_socket?.close());
    if (_disposed || current != _connection) return;
    connected = false;
    remoteReady = false;
    controlled = false;
    replaying = true;
    _queuedBytes = 0;
    _pendingInput?.complete(false);
    _pendingInput = null;
    _pendingID = null;
    sending = false;
    historySize = 0;
    historyPosition = 0;
    remoteAlternate = false;
    localAlternate = false;
    following = true;
    selection = false;
    selectionText = '';
    _clientID = '';
    _replayTail.finish();
    port.push({'type': 'reset'});
    port.push({'type': 'control', 'enabled': false});
    port.push({'type': 'selection', 'enabled': false});
    port.push({
      'type': 'application',
      'remoteHistory': session.mode == 'tmux',
      'alternate': false,
    });
    status = demo ? '设计预览' : '正在连接';
    detail = '';
    _notify();
    if (demo) {
      connected = true;
      remoteReady = true;
      replaying = false;
      status = '查看模式';
      port.push({'type': 'setViewport', 'cols': 48, 'rows': 28});
      final text =
          '\x1b[90m/workspace/velin\x1b[0m\r\n\$ codex\r\n\r\n\x1b[1m› 优化手机端布局，保留更多终端空间\x1b[0m\r\n\r\n  首页改用紧凑列表。\r\n  搜索和筛选按需打开。\r\n\r\n  终端默认收起输入区，\r\n  需要时点底部键盘按钮。\r\n\r\n  \x1b[38;2;179;192;214m✓\x1b[0m 输入草稿按会话保留\r\n  \x1b[38;2;179;192;214m✓\x1b[0m 快捷键、附件收进菜单\r\n  \x1b[38;2;179;192;214m✓\x1b[0m 支持全屏查看\r\n\r\n\x1b[90m示例终端 · 没有连接真实主机\x1b[0m\r\n';
      port.push({
        'type': 'write',
        'id': 'demo',
        'data': base64Encode(utf8.encode(text)),
        'bytes': 0,
      });
      _notify();
      return;
    }
    try {
      await api!.restore(
        session,
        secret: secret,
        trust: trust,
        passphrase: passphrase,
      );
      if (_disposed || current != _connection) return;
      await _prepareResume(current);
      if (_disposed || current != _connection) return;
      final socket = await api!.connect(
        session,
        device,
        streamID: _resumeStream,
        offset: _streamEnd,
        tail: _tailRequest,
      );
      if (_disposed || current != _connection) {
        await socket.close();
        return;
      }
      _socket = socket;
      socket.pingInterval = const Duration(seconds: 15);
      connected = true;
      _attempts = 0;
      _subscription = socket.listen(
        (raw) {
          if (current != _connection || _disposed) return;
          try {
            _event(Map<String, dynamic>.from(jsonDecode(raw as String)));
          } catch (error) {
            _fail('终端数据异常：$error', retry: false);
          }
        },
        onDone: () {
          if (current == _connection && !_disposed) _fail('连接已断开，草稿已保留');
        },
        onError: (Object error) {
          if (current == _connection && !_disposed) _fail('连接中断：$error');
        },
      );
      _heartbeat = Timer.periodic(
        const Duration(seconds: 5),
        (_) => send({'type': 'ping'}),
      );
      _notify();
    } on ApiFailure catch (error) {
      if (current != _connection || _disposed) return;
      status = '需要处理';
      detail = error.message;
      _notify();
      if ([
        'host_key_unknown',
        'unknown_host_key',
        'host_key_changed',
        'host_key_required',
        'auth_required',
        'authentication_failed',
      ].contains(error.code)) {
        await onRecovery?.call({...error.details, 'code': error.code});
      }
    } catch (error) {
      if (current == _connection && !_disposed) _fail('连接失败：$error');
    }
  }

  void _fail(String message, {bool retry = true}) {
    // Retire this connection before closing it, so onDone and late render
    // receipts cannot override the failure or schedule another retry.
    _connection++;
    unawaited(_subscription?.cancel());
    _subscription = null;
    unawaited(_socket?.close());
    _socket = null;
    connected = false;
    remoteReady = false;
    controlled = false;
    status = retry ? '等待重连' : '需要重连';
    detail = message;
    _heartbeat?.cancel();
    _historyRefresh?.cancel();
    _historyRefresh = null;
    _pendingInput?.complete(false);
    _pendingInput = null;
    _pendingID = null;
    sending = false;
    port.push({'type': 'control', 'enabled': false});
    _notify();
    _retry?.cancel();
    if (retry && _active && !_disposed) {
      _attempts++;
      _retry = Timer(
        Duration(seconds: (_attempts * 2).clamp(2, 30)),
        () => connect(),
      );
    }
  }

  void _event(Map<String, dynamic> e) {
    switch (e['type']) {
      case 'hello':
        _updateHistory(e);
        remoteReady = (e['status'] ?? session.status) == 'attached';
        _receivedOffset = (e['offset'] as num?)?.toInt() ?? 0;
        status = remoteReady ? '正在同步' : '正在连接 SSH';
        replaying = true;
        // Resuming is only valid when the server confirms the same stream and
        // sends the output after our cached offset; otherwise start clean.
        final helloStream = e['streamID'] as String? ?? '';
        final resumed =
            _resumeStream.isNotEmpty &&
            helloStream == _resumeStream &&
            e['truncated'] != true;
        if (!resumed) {
          _replayTail.finish();
          _raw.clear();
        }
        _streamID = helloStream;
        _streamEnd = (e['offset'] as num?)?.toInt() ?? 0;
        _resumeStream = '';
        port.push({
          'type': 'setViewport',
          'rows': e['rows'] ?? 30,
          'cols': e['cols'] ?? 120,
        });
        if (e['truncated'] == true) detail = '服务端历史已截断，仅恢复当前保留的内容';
        if (e['status'] == 'host_key_required' ||
            e['status'] == 'auth_required') {
          unawaited(onRecovery?.call(e));
        }
      case 'replay_preview':
      case 'replay_live':
        break; // Preview is never appended twice.
      case 'replay':
        // Only the end of a long history is rendered: old output is never
        // parsed, so the session opens on its last screen immediately.
        final replayBytes = base64Decode(e['data'] as String? ?? '');
        _replayTail.add(replayBytes);
        _raw.add(replayBytes);
        if (e['replayFinal'] == true) _flushReplay();
      case 'replay_end':
        // An empty final write is an ordered rendering barrier after reset.
        _flushReplay();
      case 'output':
        final bytes = base64Decode(e['data'] as String);
        final end = (e['offset'] as num).toInt(), start = end - bytes.length;
        if (end <= _receivedOffset) return;
        if (start > _receivedOffset) {
          _invalidateStream();
          _fail('输出存在缺口，请重新同步', retry: false);
          return;
        }
        final suffix = bytes.sublist(
          (_receivedOffset - start).clamp(0, bytes.length),
        );
        _receivedOffset = end;
        _raw.add(Uint8List.fromList(suffix));
        _streamEnd = end;
        _scheduleCacheSave();
        _write({'data': base64Encode(suffix), 'offset': end});
        _historyRefresh ??= Timer(const Duration(milliseconds: 600), () {
          _historyRefresh = null;
          send({'type': 'history_state'});
        });
      case 'history_state':
        _updateHistory(e);
      case 'history_position':
        if (e['sequence'] == _historySequence) {
          historyPosition = ((e['position'] as num?)?.toInt() ?? historySize)
              .clamp(0, historySize);
          following = historyPosition >= historySize;
          if (e['error'] is String && (e['error'] as String).isNotEmpty) {
            detail = e['error'];
          }
        }
      case 'geometry':
        port.push({
          'type': 'setViewport',
          'rows': e['rows'],
          'cols': e['cols'],
        });
      case 'controller':
      case 'control_granted':
        if (e['type'] == 'control_granted') {
          controlled = true;
        } else {
          controlled = e['controller'] != null && e['controller'] == _clientID;
        }
        status = !remoteReady
            ? '正在连接 SSH'
            : replaying
            ? '正在同步'
            : controlled
            ? '已接管'
            : '查看模式';
        port.push({'type': 'control', 'enabled': controlled});
      case 'control_pending':
        // A server without forced takeover keeps asking the active controller.
        // Retry for a minute: it succeeds once that page closes or goes idle.
        status = '等待电脑端允许接管（自动重试中）';
        _controlRetries = 0;
        _controlRetry ??= Timer.periodic(const Duration(seconds: 3), (timer) {
          if (controlled || !connected || ++_controlRetries > 20) {
            timer.cancel();
            _controlRetry = null;
            return;
          }
          send({'type': 'request_control', 'force': true});
        });
      case 'control_denied':
        controlled = false;
        status = '接管请求未获批准';
      case 'control_request':
        unawaited(onRecovery?.call(e));
      case 'input_ack':
        if (e['inputID'] == _pendingID) {
          final ok = e['result'] == 'written';
          detail = ok
              ? '已写入终端'
              : '输入${e['result'] == 'uncertain' ? '结果未确认' : '未写入'}，草稿已保留';
          _pendingInput?.complete(ok);
          _pendingInput = null;
          _pendingID = null;
          sending = false;
        }
      case 'status':
        remoteReady = e['status'] == 'attached';
        if (remoteReady) {
          status = replaying
              ? '正在同步'
              : controlled
              ? '已接管'
              : '查看模式';
        }
        if (e['status'] == 'host_key_required' ||
            e['status'] == 'auth_required') {
          _fail(e['message'] as String? ?? '连接需要确认', retry: false);
          unawaited(onRecovery?.call(e));
          return;
        } else if ([
          'unreachable',
          'ended',
          'background',
          'error',
        ].contains(e['status'])) {
          _fail(e['message'] as String? ?? '远端会话已离线', retry: false);
          return;
        } else if (e['status'] != 'attached') {
          status = '${e['status']}';
          detail = e['message'] as String? ?? '';
        }
      case 'close':
        _fail(e['message'] as String? ?? '远端会话已离线', retry: false);
    }
    if (e['type'] == 'hello') _clientID = e['clientID'] as String? ?? '';
    _notify();
  }

  /// One-shot message for the screen to show (cleared by the screen).
  String scrollHint = '';
  DateTime _lastScrollHint = DateTime.fromMillisecondsSinceEpoch(0);

  String _clientID = '';
  final _replayTail = ReplayTail();

  // Exact newest bytes of the output stream and where they end. They let a
  // reconnect (or the next time the session is opened, via ReplayCache) ask the
  // server for only what was printed since, instead of the whole history.
  static const _tailRequest = 256 * 1024;
  final _raw = StreamTail(max: _tailRequest);
  String _streamID = '', _resumeStream = '';
  int _streamEnd = -1;
  Timer? _cacheTimer;

  Future<void> _prepareResume(int connection) async {
    _resumeStream = '';
    if (_streamID.isEmpty || _streamEnd < 0 || _raw.size == 0) {
      final cached = await ReplayCache.load(session.id);
      if (_disposed || connection != _connection) return;
      if (cached != null) {
        _raw
          ..clear()
          ..add(cached.bytes);
        _streamID = cached.streamID;
        _streamEnd = cached.end;
      }
    }
    if (_streamID.isNotEmpty && _streamEnd >= 0 && _raw.size > 0) {
      _resumeStream = _streamID;
      _replayTail.finish();
      _replayTail.add(_raw.bytes());
    }
  }

  void _invalidateStream() {
    _streamID = '';
    _resumeStream = '';
    _streamEnd = -1;
    _raw.clear();
    _replayTail.finish();
    unawaited(ReplayCache.delete(session.id));
  }

  void _scheduleCacheSave() {
    if (demo || api == null || replaying || _disposed) return;
    _cacheTimer ??= Timer(const Duration(seconds: 3), saveCache);
  }

  /// Persists the latest output so the next open can resume from it.
  void saveCache() {
    _cacheTimer?.cancel();
    _cacheTimer = null;
    if (demo || api == null || replaying) return;
    if (_streamID.isEmpty || _streamEnd < 0 || _raw.size == 0) return;
    unawaited(
      ReplayCache.save(
        session.id,
        CachedStream(_streamID, _streamEnd, _raw.bytes()),
      ),
    );
  }

  void _flushReplay() {
    final bytes = _replayTail.finish();
    _write({'data': base64Encode(bytes)}, finalReplay: true);
  }

  void _write(Map<String, dynamic> e, {bool finalReplay = false}) {
    final data = e['data'] as String? ?? '', length = base64Decode(data).length;
    _queuedBytes += length;
    if (_queuedBytes > 4 * 1024 * 1024) {
      _invalidateStream();
      _fail('输出队列超出上限，请重新同步', retry: false);
      return;
    }
    port.push({
      'type': 'write',
      'id': finalReplay ? 'replay-final' : const Uuid().v4(),
      'data': data,
      'bytes': length,
      'offset': e['offset'],
      'connection': _connection,
    });
    if (_queuedBytes >= 512 * 1024 && !(_subscription?.isPaused ?? true)) {
      _subscription?.pause();
    }
  }

  void _bridge(Map<String, dynamic> e) {
    switch (e['type']) {
      case 'rendered':
        if (e['connection'] != null && e['connection'] != _connection) {
          return;
        }
        _queuedBytes = (_queuedBytes - ((e['bytes'] as num?)?.toInt() ?? 0))
            .clamp(0, 4 * 1024 * 1024);
        if (_queuedBytes < 128 * 1024 && (_subscription?.isPaused ?? false)) {
          _subscription?.resume();
        }
        if (e['id'] == 'replay-final') {
          replaying = false;
          _scheduleCacheSave();
          status = remoteReady ? (controlled ? '已接管' : '查看模式') : '正在连接 SSH';
        }
      case 'modes':
        bracketed = e['bracketedPaste'] == true;
        localAlternate = e['alternate'] == true;
        port.push({
          'type': 'application',
          'remoteHistory': session.mode == 'tmux',
          'alternate': applicationAlternate,
        });
      case 'selection':
        selectionText = e['text'] as String? ?? '';
      case 'selectionMode':
        selection = e['enabled'] == true;
        if (!selection) selectionText = '';
      case 'contextMenu':
        unawaited(onContextMenu?.call());
      case 'following':
        following = e['enabled'] == true;
      case 'fit':
        if (controlled) {
          send({'type': 'resize', 'rows': e['rows'], 'cols': e['cols']});
        }
      case 'scroll':
        if (!controlled) {
          detail = '查看模式可浏览本地历史；远端历史需要先接管';
          final now = DateTime.now();
          if (now.difference(_lastScrollHint) > const Duration(seconds: 4)) {
            _lastScrollHint = now;
            scrollHint = '当前是查看模式，点右上角“接管”后才能滚动 Codex 等程序';
          }
          break;
        }
        if (!remoteReady || replaying) break;
        if (applicationAlternate && historyPosition >= historySize) {
          // The transcript sits above the composer, so wheel steps target it
          // rather than the finger position.
          _appScrollRow = (e['anchorRow'] as num?)?.toInt() ?? e['row'] as int;
          _appScrollColumn =
              (e['anchorColumn'] as num?)?.toInt() ?? e['column'] as int;
          _queueApplicationScroll(e['lines'] as int);
        } else if (session.mode == 'tmux') {
          scrollTo(historyPosition + (e['lines'] as int));
        }
      case 'click':
        if (controlled && remoteReady && !replaying && applicationAlternate) {
          send({
            'type': 'click_application',
            'row': e['row'],
            'column': e['column'],
          });
        }
      case 'bridge_error':
        _fail('终端页面不可用，请退出后重新打开', retry: false);
    }
    _notify();
  }

  void send(Map<String, dynamic> message) {
    if (connected && !demo) {
      try {
        _socket?.add(jsonEncode(message));
      } catch (_) {
        _fail('连接已断开，未自动重发输入');
      }
    }
  }

  void _updateHistory(Map<String, dynamic> event) {
    final atEnd = historyPosition >= historySize;
    remoteAlternate = event['alternateScreen'] == true;
    historySize = ((event['historySize'] as num?)?.toInt() ?? 0).clamp(
      0,
      1000000,
    );
    historyPosition = atEnd || remoteAlternate
        ? historySize
        : historyPosition.clamp(0, historySize);
    port.push({
      'type': 'application',
      'remoteHistory': session.mode == 'tmux',
      'alternate': applicationAlternate,
    });
  }

  // Wheel steps are merged and paced: each one is a separate remote command, so
  // sending every touch move would queue up and keep scrolling after release.
  int _appScrollLines = 0, _appScrollRow = 1, _appScrollColumn = 1;
  Timer? _appScrollTimer;

  void _queueApplicationScroll(int lines) {
    _appScrollLines = (_appScrollLines + lines).clamp(-12, 12);
    if (_appScrollTimer != null) return;
    _flushApplicationScroll();
    _appScrollTimer = Timer.periodic(const Duration(milliseconds: 60), (timer) {
      if (_appScrollLines == 0 || !connected) {
        _appScrollLines = 0;
        timer.cancel();
        _appScrollTimer = null;
        return;
      }
      _flushApplicationScroll();
    });
  }

  void _flushApplicationScroll() {
    final step = _appScrollLines.clamp(-3, 3);
    if (step == 0) return;
    _appScrollLines -= step;
    send({
      'type': 'scroll_application',
      'lines': step,
      'row': _appScrollRow,
      'column': _appScrollColumn,
    });
  }

  void scrollTo(int position) {
    if (!controlled || !remoteReady || replaying) return;
    historyPosition = position.clamp(0, historySize);
    following = historyPosition >= historySize;
    send({
      'type': 'scroll_history_to',
      'position': historyPosition,
      'historySize': historySize,
      'sequence': ++_historySequence,
    });
    _notify();
  }

  Timer? _controlRetry;
  int _controlRetries = 0;

  void requestControl() {
    if (!connected || !remoteReady || replaying) return;
    if (demo) {
      controlled = true;
      status = '已接管';
      port.push({'type': 'control', 'enabled': true});
      _notify();
    } else {
      // `force` takes over from the owner's own computer without waiting for
      // approval; older servers ignore it and answer control_pending.
      send({'type': 'request_control', 'force': true});
    }
  }

  void release() {
    send({'type': 'release_control'});
    controlled = false;
    status = '查看模式';
    port.push({'type': 'control', 'enabled': false});
    _notify();
  }

  Future<bool> input(List<int> bytes) async {
    if (!canInput) return false;
    if (bytes.isEmpty || bytes.length > 48 * 1024) {
      throw const FormatException('输入超过 48 KiB，请改用文件上传');
    }
    if (demo) {
      detail = '设计预览不会发送到真实主机';
      _notify();
      return false;
    }
    sending = true;
    final pending = Completer<bool>();
    _pendingInput = pending;
    _pendingID = const Uuid().v4();
    _notify();
    send({'type': 'input', 'inputID': _pendingID, 'data': base64Encode(bytes)});
    final result = await pending.future.timeout(
      const Duration(seconds: 15),
      onTimeout: () => false,
    );
    if (_pendingInput == pending) {
      _pendingInput = null;
      _pendingID = null;
      sending = false;
      detail = '输入结果未确认，未自动重发；请检查终端后再操作';
      _notify();
    }
    return result;
  }

  void toggleSelection() {
    selection = !selection;
    if (!selection) selectionText = '';
    port.push({'type': 'selection', 'enabled': selection});
    _notify();
  }

  void selectAll() => port.push({'type': 'selectAll'});

  void clearDisplay() => port.push({'type': 'clear'});

  Future<void> rename(String value) async {
    final name = value.trim();
    if (name.isEmpty || name.runes.length > 80) {
      throw const FormatException('会话名称应为 1～80 个字符');
    }
    if (!demo) {
      await api!.request(
        '/api/sessions/${session.id}',
        method: 'PATCH',
        data: {'name': name},
      );
    }
    session = session.withName(name);
    _notify();
  }

  void bottom() {
    following = true;
    port.push({'type': 'bottom'});
    if (controlled && session.mode == 'tmux') scrollTo(historySize);
    _notify();
  }

  void setFont(double value) {
    fontSize = value;
    port.push({'type': 'font', 'size': value});
    _notify();
  }

  void pause() {
    saveCache();
    _active = false;
    _connection++;
    _retry?.cancel();
    _heartbeat?.cancel();
    _historyRefresh?.cancel();
    _historyRefresh = null;
    release();
    _subscription?.cancel();
    _socket?.close();
    connected = false;
    remoteReady = false;
    _pendingInput?.complete(false);
    _pendingInput = null;
    _pendingID = null;
    sending = false;
    _notify();
  }

  Future<void> resume() async {
    _active = true;
    await connect();
  }

  @override
  void dispose() {
    _disposed = true;
    _active = false;
    _connection++;
    _retry?.cancel();
    _heartbeat?.cancel();
    _historyRefresh?.cancel();
    _appScrollTimer?.cancel();
    _controlRetry?.cancel();
    saveCache();
    _cacheTimer?.cancel();
    send({'type': 'release_control'});
    _pendingInput?.complete(false);
    _pendingInput = null;
    _subscription?.cancel();
    _socket?.close();
    port.onMessage = null;
    super.dispose();
  }
}
