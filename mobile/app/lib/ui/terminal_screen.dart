import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:dio/dio.dart';
import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import '../terminal/session_controller.dart';
import '../terminal/palettes.dart';
import '../terminal/terminal_surface.dart';
import 'theme.dart';
import 'upload_location_sheet.dart';

class TerminalScreen extends StatefulWidget {
  final RemoteApi? api;
  final RemoteSession session;
  final RemoteHost? host;
  final String device;
  final bool demo;
  final SharedPreferences preferences;
  const TerminalScreen({
    super.key,
    required this.api,
    required this.session,
    required this.host,
    required this.device,
    required this.demo,
    required this.preferences,
  });
  @override
  State<TerminalScreen> createState() => _TerminalScreenState();
}

class _TerminalScreenState extends State<TerminalScreen>
    with WidgetsBindingObserver {
  late final SessionController terminal;
  final draft = TextEditingController();
  final focus = FocusNode();
  Timer? _save;
  bool _recovering = false, uploading = false, expanded = false;
  bool inputOpen = false, immersive = false;
  double uploadProgress = 0;
  String uploadName = '';
  CancelToken? cancel;
  String get draftKey =>
      'mobile.draft.${widget.api?.base}|${widget.demo ? 'demo' : widget.api!.account}.${widget.session.id}';
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    draft.text = widget.preferences.getString(draftKey) ?? '';
    draft.addListener(() {
      _save?.cancel();
      _save = Timer(const Duration(milliseconds: 350), _persist);
    });
    terminal = SessionController(
      widget.api,
      widget.session,
      widget.device,
      demo: widget.demo,
    )..onRecovery = _recover;
    terminal.fontSize = widget.preferences.getDouble('mobile.fontSize') ?? 13;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        terminal.port.push({'type': 'font', 'size': terminal.fontSize});
        terminal.port.push({
          'type': 'theme',
          'colors':
              terminalPalettes[widget.preferences.getString(
                    'mobile.terminalTheme',
                  ) ??
                  'graphite'],
        });
        terminal.connect();
      }
    });
  }

  Future<void> _persist() async {
    // Bound device-local drafts to 48 KiB; a long unsent draft remains in memory.
    if (utf8.encode(draft.text).length <= 48 * 1024) {
      await widget.preferences.setString(draftKey, draft.text);
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      unawaited(terminal.resume());
    } else if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached ||
        state == AppLifecycleState.hidden) {
      unawaited(_persist());
      terminal.pause();
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _save?.cancel();
    unawaited(_persist());
    cancel?.cancel();
    terminal.dispose();
    draft.dispose();
    focus.dispose();
    super.dispose();
  }

  Future<void> _recover(Map<String, dynamic> message) async {
    if (_recovering || !mounted) return;
    _recovering = true;
    try {
      if (message['type'] == 'control_request') {
        final allow = await confirm(
          context,
          '允许其他客户端接管？',
          '允许后，此手机切换为查看模式。',
          action: '允许',
        );
        terminal.send({
          'type': 'control_response',
          'requester': message['requester'],
          'approved': allow,
        });
        return;
      }
      if (message['fingerprint'] != null) {
        final trust = await confirm(
          context,
          message['code'] == 'host_key_changed' ? '主机指纹已变化' : '确认主机指纹',
          '${message['hostName'] ?? widget.host?.name ?? ''}\n${message['hostAddress'] ?? ''}\n\n${message['fingerprint']}\n\n确认此指纹属于目标主机后继续。',
          action: '信任并连接',
        );
        if (trust && mounted) {
          await terminal.connect(trust: '${message['fingerprint']}');
        }
      } else {
        final secret = TextEditingController(),
            passphrase = TextEditingController();
        final value = await showDialog<Map<String, String>>(
          context: context,
          builder: (c) => AlertDialog(
            title: const Text('连接需要凭据'),
            content: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Text('按主机认证方式填写，本次连接使用，不保存。'),
                  const SizedBox(height: 16),
                  TextField(
                    controller: secret,
                    obscureText: true,
                    decoration: const InputDecoration(labelText: '主机密码'),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: passphrase,
                    obscureText: true,
                    decoration: const InputDecoration(labelText: '私钥口令（如有）'),
                  ),
                ],
              ),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(c),
                child: const Text('取消'),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(c, {
                  'secret': secret.text,
                  'passphrase': passphrase.text,
                }),
                child: const Text('连接'),
              ),
            ],
          ),
        );
        await Future<void>.delayed(const Duration(milliseconds: 300));
        secret.dispose();
        passphrase.dispose();
        if (value != null && mounted) {
          await terminal.connect(
            secret: value['secret'] ?? '',
            passphrase: value['passphrase'] ?? '',
          );
        }
      }
    } finally {
      _recovering = false;
    }
  }

  Future<void> _submit({bool submit = true}) async {
    if (draft.value.composing.isValid && !draft.value.composing.isCollapsed) {
      notice(context, '请先确认输入法候选文字');
      return;
    }
    if (!terminal.canInput) {
      notice(context, '请先接管并等待终端同步完成');
      return;
    }
    final text = draft.text;
    if (text.isEmpty) return;
    if (RegExp(r'[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]').hasMatch(text)) {
      notice(context, '正文包含控制字符，请清理后发送');
      return;
    }
    if (!terminal.bracketed &&
        (text.contains('\n') || text.contains('\r') || text.contains('\t'))) {
      if (!await confirm(
        context,
        '远端未启用粘贴保护',
        '换行或 Tab 可能执行多条命令或触发补全，是否继续？',
        action: '继续',
      )) {
        return;
      }
    }
    try {
      final body = terminal.bracketed ? '\x1b[200~$text\x1b[201~' : text;
      final sent = await terminal.input(
        utf8.encode('$body${submit ? '\r' : ''}'),
      );
      if (sent && mounted && draft.text == text) {
        draft.clear();
        await _persist();
      } else if (!sent && mounted) {
        notice(
          context,
          terminal.detail.isEmpty ? '内容未发送，草稿已保留' : terminal.detail,
        );
      }
    } catch (e) {
      if (mounted) notice(context, e);
    }
  }

  Future<void> _key(String name, List<int> data) async {
    if (!terminal.canInput) {
      notice(context, '请先接管终端');
      return;
    }
    try {
      final ok = await terminal.input(data);
      if (!ok && mounted) notice(context, terminal.detail);
    } catch (e) {
      if (mounted) notice(context, e);
    }
  }

  Future<void> _attach() async {
    if (widget.demo) {
      final selected = await showModalBottomSheet<String>(
        context: context,
        isScrollControlled: true,
        builder: (_) => const UploadLocationSheet(
          directory: '/workspace/velin',
          files: ['界面参考.png'],
          demo: true,
        ),
      );
      if (selected != null && mounted) {
        draft.text =
            '${draft.text}\n${shellQuote('$selected/clipboard/design-reference.png')}';
      }
      return;
    }
    final picked = await FilePicker.platform.pickFiles(
      allowMultiple: true,
      withData: false,
    );
    if (picked == null) return;
    if (!mounted) {
      await _clearPickedFiles();
      return;
    }
    String directory = '';
    try {
      final result = await widget.api!.request(
        '/api/sessions/${widget.session.id}/directory',
      );
      directory = result['path'] as String? ?? '';
    } catch (_) {}
    if (!mounted) {
      await _clearPickedFiles();
      return;
    }
    final selected = await showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      builder: (_) => UploadLocationSheet(
        directory: directory,
        files: picked.files.map((file) => file.name).toList(),
      ),
    );
    if (!mounted || selected == null) {
      await _clearPickedFiles();
      return;
    }
    if (!selected.startsWith('/') ||
        selected == '/' ||
        selected.split('/').contains('..')) {
      notice(context, '请填写有效的远端项目绝对目录');
      await _clearPickedFiles();
      return;
    }
    final folder = '${selected.replaceFirst(RegExp(r'/+$'), '')}/clipboard';
    cancel = CancelToken();
    setState(() => uploading = true);
    try {
      await widget.api!.request(
        '/api/sftp/${widget.session.hostID}/mkdir',
        method: 'POST',
        query: {'session': widget.session.id},
        data: {'path': folder},
      );
      for (final item in picked.files) {
        if (cancel!.isCancelled || !mounted) break;
        if (item.path == null) throw const FormatException('无法读取选中的本地文件');
        final file = File(item.path!);
        final length = await file.length();
        if (length > 2 * 1024 * 1024 * 1024) {
          throw const FormatException('单个文件超过 2 GiB 上限');
        }
        final name = item.name.replaceAll(RegExp(r'[/\\\x00-\x1f\x7f]'), '_');
        final path =
            '$folder/${DateTime.now().millisecondsSinceEpoch}-${const Uuid().v4().substring(0, 8)}-$name';
        setState(() {
          uploadName = item.name;
          uploadProgress = 0;
        });
        final result = await widget.api!.upload(
          widget.session,
          path,
          file,
          cancel!,
          (sent, total) {
            if (mounted) {
              setState(() => uploadProgress = total > 0 ? sent / total : 0);
            }
          },
        );
        if (result['offset'] != length) {
          throw const FormatException('服务端文件大小未确认，请检查后重试');
        }
        if (!mounted || cancel!.isCancelled) break;
        draft.text =
            '${draft.text}${draft.text.isEmpty ? '' : '\n'}${shellQuote(path)}';
      }
      if (mounted) notice(context, '上传完成，路径已加入草稿');
    } catch (e) {
      if (mounted) {
        notice(
          context,
          CancelToken.isCancel(
                e is DioException
                    ? e
                    : DioException(requestOptions: RequestOptions(), error: e),
              )
              ? '上传已取消'
              : '上传失败：$e',
        );
      }
    } finally {
      if (mounted) setState(() => uploading = false);
      cancel = null;
      await _clearPickedFiles();
    }
  }

  Future<void> _clearPickedFiles() async {
    try {
      await FilePicker.platform.clearTemporaryFiles();
    } catch (_) {}
  }

  Future<void> _menu() async {
    final action = await showModalBottomSheet<String>(
      context: context,
      builder: (c) => SafeArea(
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              ListTile(
                title: Text(widget.session.displayName(widget.host)),
                subtitle: Text(
                  '${widget.host?.name ?? ''} · ${widget.session.mode}',
                  style: const TextStyle(color: V.muted),
                ),
              ),
              ListTile(
                leading: const Icon(Icons.keyboard_outlined),
                title: const Text('编辑输入'),
                onTap: () => Navigator.pop(c, 'input'),
              ),
              ListTile(
                leading: const Icon(Icons.attach_file_rounded),
                title: const Text('上传附件'),
                enabled: !uploading,
                onTap: () => Navigator.pop(c, 'attach'),
              ),
              ListTile(
                leading: const Icon(Icons.keyboard_command_key_rounded),
                title: const Text('终端快捷键'),
                onTap: () => Navigator.pop(c, 'keys'),
              ),
              ListTile(
                leading: const Icon(Icons.fullscreen_rounded),
                title: const Text('全屏终端'),
                onTap: () => Navigator.pop(c, 'fullscreen'),
              ),
              ListTile(
                leading: const Icon(Icons.info_outline_rounded),
                title: const Text('连接状态与详情'),
                onTap: () => Navigator.pop(c, 'info'),
              ),
              ListTile(
                leading: const Icon(Icons.select_all_rounded),
                title: Text(terminal.selection ? '退出文本选择' : '选择终端文本'),
                onTap: () => Navigator.pop(c, 'select'),
              ),
              if (terminal.selection)
                ListTile(
                  leading: const Icon(Icons.copy_rounded),
                  title: const Text('复制选中文本'),
                  enabled: terminal.selectionText.isNotEmpty,
                  onTap: () => Navigator.pop(c, 'copy'),
                ),
              ListTile(
                leading: const Icon(Icons.fit_screen_rounded),
                title: const Text('终端适配手机宽度'),
                subtitle: const Text('接管后调整远端尺寸'),
                onTap: () => Navigator.pop(c, 'fit'),
              ),
              if (terminal.controlled)
                ListTile(
                  leading: const Icon(Icons.visibility_outlined),
                  title: const Text('释放控制，切换为查看'),
                  onTap: () => Navigator.pop(c, 'release'),
                ),
              ListTile(
                leading: const Icon(Icons.refresh_rounded),
                title: const Text('重新连接并同步'),
                onTap: () => Navigator.pop(c, 'reconnect'),
              ),
              ListTile(
                leading: const Icon(
                  Icons.stop_circle_outlined,
                  color: V.danger,
                ),
                title: const Text('终止远端会话', style: TextStyle(color: V.danger)),
                onTap: () => Navigator.pop(c, 'end'),
              ),
              const SizedBox(height: 16),
            ],
          ),
        ),
      ),
    );
    if (!mounted) return;
    if (action == 'fit') {
      if (terminal.controlled) {
        terminal.port.push({'type': 'fit'});
      } else {
        notice(context, '请先接管终端');
      }
    }
    if (action == 'release') terminal.release();
    if (action == 'select') _toggleSelection();
    if (action == 'input') _openComposer();
    if (action == 'keys') await _shortcuts();
    if (action == 'info') await _connectionInfo();
    if (action == 'fullscreen') {
      focus.unfocus();
      setState(() => immersive = true);
    }
    if (action == 'attach') {
      focus.unfocus();
      setState(() => inputOpen = true);
      await _attach();
    }
    if (action == 'copy' && terminal.selectionText.isNotEmpty) {
      await Clipboard.setData(ClipboardData(text: terminal.selectionText));
      if (mounted) notice(context, '已复制');
    }
    if (action == 'reconnect') await terminal.connect();
    if (mounted &&
        action == 'end' &&
        await confirm(
          context,
          '终止远端会话？',
          '此操作会停止会话中的程序。返回上一级可以保留任务。',
          action: '终止',
        )) {
      if (widget.demo) return;
      try {
        await widget.api!.request(
          '/api/sessions/${widget.session.id}',
          method: 'DELETE',
        );
        if (mounted) Navigator.pop(context);
      } catch (e) {
        if (mounted) notice(context, '远端终止失败，可返回列表重试：$e');
      }
    }
  }

  void _openComposer() {
    if (terminal.selection) terminal.toggleSelection();
    setState(() {
      inputOpen = true;
      immersive = false;
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) focus.requestFocus();
    });
  }

  void _hideComposer() {
    focus.unfocus();
    setState(() => inputOpen = false);
  }

  void _toggleSelection() {
    focus.unfocus();
    setState(() => inputOpen = false);
    terminal.toggleSelection();
  }

  Future<void> _copy() async {
    if (terminal.selectionText.isEmpty) return;
    await Clipboard.setData(ClipboardData(text: terminal.selectionText));
    if (mounted) notice(context, '已复制');
  }

  Future<void> _connectionInfo() => showDialog<void>(
    context: context,
    builder: (context) => AlertDialog(
      title: const Text('会话状态'),
      content: SingleChildScrollView(
        child: SelectableText(
          [
            widget.session.name,
            '会话编号：${widget.session.id}\n创建：${widget.session.createdLabel}',
            '${widget.host?.name ?? widget.session.hostID} · ${widget.session.mode}',
            widget.demo ? '示例数据 · ${terminal.status}' : terminal.status,
            if (terminal.detail.isNotEmpty) terminal.detail,
          ].join('\n\n'),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('关闭'),
        ),
      ],
    ),
  );

  Future<void> _shortcuts() async {
    focus.unfocus();
    await showModalBottomSheet<void>(
      context: context,
      builder: (context) => SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text(
                '终端快捷键',
                style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 16),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  _shortcut('Esc', [27]),
                  _shortcut('Tab', [9]),
                  _shortcut('Ctrl+C', [3]),
                  _shortcut('↑', [27, 91, 65]),
                  _shortcut('↓', [27, 91, 66]),
                  _shortcut('←', [27, 91, 68]),
                  _shortcut('→', [27, 91, 67]),
                  _shortcut('Enter', [13]),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _inputOptions() => PopupMenuButton<String>(
    tooltip: '输入选项',
    icon: const Icon(Icons.add_rounded, size: 20, color: V.muted),
    onSelected: (action) async {
      if (action == 'attach') {
        focus.unfocus();
        await _attach();
      }
      if (action == 'paste') await _submit(submit: false);
      if (action == 'expand') setState(() => expanded = !expanded);
      if (action == 'keys') await _shortcuts();
    },
    itemBuilder: (_) => [
      PopupMenuItem(
        value: 'attach',
        enabled: !uploading,
        child: const Text('上传附件'),
      ),
      PopupMenuItem(
        value: 'paste',
        enabled: terminal.canInput,
        child: const Text('只粘贴正文，不回车'),
      ),
      PopupMenuItem(value: 'expand', child: Text(expanded ? '收起长文本' : '展开长文本')),
      const PopupMenuItem(value: 'keys', child: Text('终端快捷键')),
    ],
  );

  Widget _composer(double height) => Padding(
    key: const ValueKey('terminal-composer'),
    padding: const EdgeInsets.fromLTRB(0, 5, 4, 5),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        IconButton(
          tooltip: '收起输入',
          onPressed: _hideComposer,
          icon: const Icon(
            Icons.keyboard_hide_outlined,
            size: 20,
            color: V.muted,
          ),
        ),
        Expanded(
          child: TextField(
            key: const ValueKey('terminal-draft'),
            controller: draft,
            focusNode: focus,
            minLines: 1,
            maxLines: (height * .3 / MediaQuery.textScalerOf(context).scale(22))
                .floor()
                .clamp(1, expanded ? 8 : 3),
            style: const TextStyle(fontSize: 14, height: 1.5),
            keyboardType: TextInputType.multiline,
            textInputAction: TextInputAction.newline,
            decoration: InputDecoration(
              hintText: terminal.controlled ? '输入，回车换行' : '草稿 · 接管后发送',
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 12,
                vertical: 11,
              ),
              suffixIcon: _inputOptions(),
            ),
          ),
        ),
        const SizedBox(width: 2),
        IconButton.filled(
          tooltip: terminal.sending ? '等待写入确认' : '发送',
          onPressed: terminal.canInput ? () => _submit() : null,
          style: IconButton.styleFrom(
            backgroundColor: V.text,
            foregroundColor: V.bg,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(10),
            ),
          ),
          icon: terminal.sending
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.arrow_upward_rounded, size: 21),
        ),
      ],
    ),
  );

  Widget _dock() => SizedBox(
    key: const ValueKey('terminal-dock'),
    height: 48,
    child: terminal.selection
        ? Row(
            children: [
              IconButton(
                tooltip: '退出选择',
                onPressed: _toggleSelection,
                icon: const Icon(Icons.close_rounded, size: 20),
              ),
              Expanded(
                child: Text(
                  terminal.selectionText.isEmpty
                      ? '划选终端文本'
                      : '已选择 ${terminal.selectionText.runes.length} 个字符',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontSize: 12, color: V.muted),
                ),
              ),
              TextButton(
                onPressed: terminal.selectionText.isEmpty ? null : _copy,
                child: const Text('复制'),
              ),
              const SizedBox(width: 8),
            ],
          )
        : Row(
            children: [
              IconButton(
                tooltip: '展开输入',
                onPressed: _openComposer,
                icon: const Icon(Icons.keyboard_outlined, size: 21),
              ),
              Expanded(
                child: SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: Row(
                    children: [
                      _shortcut('Esc', [27]),
                      _shortcut('Ctrl+C', [3]),
                    ],
                  ),
                ),
              ),
              IconButton(
                tooltip: '选择文本',
                onPressed: _toggleSelection,
                icon: const Icon(
                  Icons.select_all_rounded,
                  size: 20,
                  color: V.muted,
                ),
              ),
              IconButton(
                tooltip: '快捷键',
                onPressed: _shortcuts,
                icon: const Icon(
                  Icons.keyboard_command_key_rounded,
                  size: 19,
                  color: V.muted,
                ),
              ),
              IconButton(
                tooltip: '全屏终端',
                onPressed: () => setState(() => immersive = true),
                icon: const Icon(
                  Icons.fullscreen_rounded,
                  size: 22,
                  color: V.muted,
                ),
              ),
            ],
          ),
  );

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: terminal,
    builder: (context, _) => PopScope<void>(
      canPop: !immersive,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop && immersive) setState(() => immersive = false);
      },
      child: Scaffold(
        appBar: immersive
            ? null
            : AppBar(
                toolbarHeight: 48,
                titleSpacing: 0,
                title: InkWell(
                  onTap: _connectionInfo,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        widget.session.displayName(widget.host),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          fontSize: 14,
                          fontWeight: FontWeight.w500,
                        ),
                      ),
                      const SizedBox(height: 2),
                      Text(
                        widget.demo
                            ? '示例 · ${terminal.status}'
                            : terminal.status,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontSize: 10, color: V.muted),
                      ),
                    ],
                  ),
                ),
                actions: [
                  if (!terminal.controlled)
                    TextButton(
                      onPressed:
                          terminal.connected &&
                              terminal.remoteReady &&
                              !terminal.replaying
                          ? terminal.requestControl
                          : null,
                      child: const Text('接管', style: TextStyle(fontSize: 12)),
                    ),
                  IconButton(
                    tooltip: '终端操作',
                    onPressed: _menu,
                    icon: const Icon(Icons.more_horiz, size: 22),
                  ),
                ],
              ),
        body: SafeArea(
          top: immersive,
          child: LayoutBuilder(
            builder: (context, constraints) => Column(
              children: [
                SizedBox(height: immersive ? 0 : 1, child: const Divider()),
                Expanded(
                  key: const ValueKey('terminal-output'),
                  child: Stack(
                    children: [
                      Positioned.fill(
                        child: TerminalSurface(port: terminal.port),
                      ),
                      if (terminal.controlled &&
                          terminal.historySize > 0 &&
                          !terminal.applicationAlternate &&
                          !terminal.selection)
                        Positioned(
                          right: 0,
                          top: 8,
                          bottom: 8,
                          child: _HistoryRail(
                            size: terminal.historySize,
                            position: terminal.historyPosition,
                            onPosition: terminal.scrollTo,
                          ),
                        ),
                      if (!terminal.following)
                        Positioned(
                          right: 8,
                          bottom: 8,
                          child: IconButton.filledTonal(
                            tooltip: '回到底部',
                            onPressed: terminal.bottom,
                            icon: const Icon(Icons.arrow_downward, size: 20),
                          ),
                        ),
                      if (immersive)
                        Positioned(
                          right: 6,
                          top: 6,
                          child: IconButton.filledTonal(
                            tooltip: '退出全屏',
                            onPressed: () => setState(() => immersive = false),
                            icon: const Icon(
                              Icons.fullscreen_exit_rounded,
                              size: 22,
                            ),
                          ),
                        ),
                      if (!terminal.connected && !widget.demo)
                        Positioned(
                          left: 12,
                          right: 12,
                          top: 12,
                          child: Material(
                            color: V.surface,
                            borderRadius: BorderRadius.circular(10),
                            child: Padding(
                              padding: const EdgeInsets.fromLTRB(12, 4, 4, 4),
                              child: Row(
                                children: [
                                  Expanded(
                                    child: Text(
                                      terminal.detail.isEmpty
                                          ? terminal.status
                                          : '${terminal.status}\n${terminal.detail}',
                                      maxLines: 3,
                                      overflow: TextOverflow.ellipsis,
                                      style: const TextStyle(
                                        fontSize: 12,
                                        color: V.muted,
                                      ),
                                    ),
                                  ),
                                  TextButton(
                                    onPressed: () => terminal.connect(),
                                    child: const Text('重试'),
                                  ),
                                ],
                              ),
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
                if (!immersive) ...[
                  if (uploading)
                    Row(
                      children: [
                        Expanded(
                          child: Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 12),
                            child: Tooltip(
                              message: uploadName,
                              child: LinearProgressIndicator(
                                value: uploadProgress,
                              ),
                            ),
                          ),
                        ),
                        Text(
                          '${(uploadProgress * 100).round()}%',
                          style: const TextStyle(fontSize: 11, color: V.muted),
                        ),
                        IconButton(
                          tooltip: '取消上传',
                          onPressed: () => cancel?.cancel(),
                          icon: const Icon(Icons.close_rounded, size: 18),
                        ),
                      ],
                    ),
                  const Divider(),
                  if (inputOpen && !terminal.selection)
                    _composer(constraints.maxHeight)
                  else
                    _dock(),
                ],
              ],
            ),
          ),
        ),
      ),
    ),
  );

  Widget _shortcut(String label, List<int> data) => TextButton(
    style: TextButton.styleFrom(
      minimumSize: const Size(48, 48),
      padding: const EdgeInsets.symmetric(horizontal: 12),
      foregroundColor: V.text,
      textStyle: const TextStyle(fontSize: 12),
    ),
    onPressed: terminal.canInput ? () => _key(label, data) : null,
    child: Text(label),
  );
}

class _HistoryRail extends StatelessWidget {
  final int size, position;
  final ValueChanged<int> onPosition;
  const _HistoryRail({
    required this.size,
    required this.position,
    required this.onPosition,
  });
  @override
  Widget build(BuildContext context) => SizedBox(
    width: 24,
    child: LayoutBuilder(
      builder: (context, constraints) {
        final thumb = (constraints.maxHeight * 28 / (size + 28)).clamp(
          constraints.maxHeight < 32 ? constraints.maxHeight : 32.0,
          constraints.maxHeight,
        );
        final travel = constraints.maxHeight - thumb;
        void move(Offset offset) {
          if (travel > 0) {
            onPosition(
              ((offset.dy - thumb / 2) / travel * size).round().clamp(0, size),
            );
          }
        }

        return Semantics(
          label: '远端历史位置',
          value: '$position / $size',
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTapDown: (event) => move(event.localPosition),
            onVerticalDragUpdate: (event) => move(event.localPosition),
            child: Stack(
              children: [
                Positioned(
                  right: 5,
                  top: 0,
                  bottom: 0,
                  child: Container(
                    width: 3,
                    color: V.line.withValues(alpha: .4),
                  ),
                ),
                Positioned(
                  right: 4,
                  top: size == 0 ? 0 : travel * position / size,
                  child: Container(
                    width: 5,
                    height: thumb,
                    decoration: BoxDecoration(
                      color: V.muted.withValues(alpha: .65),
                      borderRadius: BorderRadius.circular(3),
                    ),
                  ),
                ),
              ],
            ),
          ),
        );
      },
    ),
  );
}
