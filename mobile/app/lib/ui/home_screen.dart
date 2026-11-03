import 'dart:async';
import 'dart:ui' show ImageFilter;
import 'dart:io';
import 'dart:ui' as ui;
import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:path_provider/path_provider.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import '../terminal/palettes.dart';
import '../terminal/replay_cache.dart';
import '../core/service_profiles.dart';
import 'host_editor.dart';
import 'service_profiles_screen.dart';
import 'web_services_screen.dart';
import 'terminal_screen.dart';
import 'theme.dart';

class HomeScreen extends StatefulWidget {
  final RemoteApi? api;
  final Map<String, dynamic> user;
  final String device;
  final bool demo;
  final SharedPreferences preferences;
  final Future<void> Function() onLogout;
  final Future<void> Function()? onServiceSwitch, onLock;
  const HomeScreen({
    super.key,
    required this.api,
    required this.user,
    required this.device,
    required this.demo,
    required this.preferences,
    required this.onLogout,
    this.onServiceSwitch,
    this.onLock,
  });
  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> with WidgetsBindingObserver {
  List<RemoteSession> sessions = [];
  List<RemoteHost> hosts = [];
  bool busy = true;
  String error = '', search = '';
  bool searchOpen = false;
  final searchController = TextEditingController();
  int tab = 0, filter = 0;
  Timer? poll;
  String get scope => '${widget.api?.base}|${widget.api?.account ?? 'demo'}';
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _load();
    poll = Timer.periodic(const Duration(seconds: 15), (_) {
      if (mounted && ModalRoute.of(context)?.isCurrent == true) {
        _load(silent: true);
      }
    });
  }

  @override
  void didUpdateWidget(covariant HomeScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.user['sessionLocked'] == true &&
        widget.user['sessionLocked'] != true) {
      _load(silent: true);
    }
  }

  @override
  void dispose() {
    poll?.cancel();
    searchController.dispose();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _load(silent: true);
  }

  Future<void> _load({bool silent = false}) async {
    if (widget.user['sessionLocked'] == true) return;
    if (!silent) setState(() => busy = true);
    try {
      if (widget.demo) {
        if (sessions.isEmpty && hosts.isEmpty) {
          sessions = [...demoSessions];
          hosts = [...demoHosts];
        }
      } else {
        await widget.api!.checkCapabilities();
        final data = await Future.wait([
          widget.api!.sessions(),
          widget.api!.hosts(),
        ]);
        sessions = data[0] as List<RemoteSession>;
        hosts = data[1] as List<RemoteHost>;
      }
      if (mounted) setState(() => error = '');
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  RemoteHost? _host(String id) {
    for (final h in hosts) {
      if (h.id == id) return h;
    }
    return null;
  }

  String _sessionTitle(RemoteSession s) => s.displayName(
    _host(s.hostID),
    duplicate: sessions.where((other) => other.name == s.name).length > 1,
  );

  Future<void> _editHost([RemoteHost? host]) async {
    final saved = await Navigator.push<RemoteHost>(
      context,
      MaterialPageRoute(
        builder: (_) => HostEditor(
          api: widget.api,
          host: host,
          hosts: hosts,
          demo: widget.demo,
        ),
      ),
    );
    if (saved == null || !mounted) return;
    setState(() {
      hosts = [...hosts.where((h) => h.id != saved.id), saved];
      tab = 2;
    });
  }

  Future<void> _hostMenu(RemoteHost host) async {
    final action = await showModalBottomSheet<String>(
      context: context,
      builder: (c) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              title: Text(host.name),
              subtitle: Text('${host.username}@${host.address}:${host.port}'),
            ),
            ListTile(
              leading: const Icon(Icons.terminal),
              title: const Text('新建命名会话'),
              onTap: () => Navigator.pop(c, 'connect'),
            ),
            ListTile(
              leading: const Icon(Icons.edit_outlined),
              title: const Text('编辑主机'),
              onTap: () => Navigator.pop(c, 'edit'),
            ),
            ListTile(
              leading: Icon(Icons.delete_outline, color: V.danger),
              title: Text('删除主机', style: TextStyle(color: V.danger)),
              onTap: () => Navigator.pop(c, 'delete'),
            ),
            const SizedBox(height: 12),
          ],
        ),
      ),
    );
    if (!mounted) return;
    if (action == 'connect') {
      await _connectHost(host);
      return;
    }
    if (action == 'edit') {
      await _editHost(host);
      return;
    }
    if (action == 'delete' &&
        await confirm(
          context,
          '删除主机？',
          '删除“${host.name}”及关联的反向代理配置。有活动会话时需先处理会话。',
          action: '删除',
        )) {
      try {
        if (!widget.demo) {
          await widget.api!.request('/api/hosts/${host.id}', method: 'DELETE');
        }
        if (mounted) setState(() => hosts.removeWhere((h) => h.id == host.id));
      } catch (e) {
        if (mounted) notice(context, e);
      }
    }
  }

  Future<void> _profiles() async {
    final current = widget.api == null
        ? null
        : ServiceProfile('${widget.api!.base}', widget.api!.account);
    final next = await Navigator.push<ServiceProfile>(
      context,
      MaterialPageRoute(
        builder: (_) => ServiceProfilesScreen(
          preferences: widget.preferences,
          current: current,
        ),
      ),
    );
    if (next == null ||
        !mounted ||
        next.server == current?.server && next.account == current?.account) {
      return;
    }
    if (next.server.isNotEmpty) {
      await widget.preferences.setString('mobile.server', next.server);
      await widget.preferences.setString('mobile.account', next.account);
    }
    await (widget.onServiceSwitch ?? widget.onLogout)();
  }

  bool _favorite(RemoteSession s) =>
      widget.preferences.getBool('mobile.favorite.$scope.${s.id}') ?? false;
  Future<void> _open(RemoteSession s) async {
    await widget.preferences.setInt(
      'mobile.recent.$scope.${s.id}',
      DateTime.now().millisecondsSinceEpoch,
    );
    if (!mounted) return;
    await Navigator.push(
      context,
      MaterialPageRoute<void>(
        builder: (_) => TerminalScreen(
          api: widget.api,
          session: s,
          host: _host(s.hostID),
          device: widget.device,
          demo: widget.demo,
          preferences: widget.preferences,
        ),
      ),
    );
    await _load(silent: true);
  }

  Future<void> _rename(RemoteSession s) async {
    final controller = TextEditingController(text: s.name);
    final name = await showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('重命名会话'),
        content: TextField(
          controller: controller,
          maxLength: 80,
          autofocus: true,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, controller.text.trim()),
            child: const Text('保存'),
          ),
        ],
      ),
    );
    // Dialog's closing animation may still reference its text controller.
    await Future<void>.delayed(const Duration(milliseconds: 300));
    controller.dispose();
    if (name == null || name.isEmpty) return;
    try {
      if (widget.demo) {
        sessions = sessions
            .map(
              (entry) => entry.id == s.id
                  ? RemoteSession(
                      id: s.id,
                      hostID: s.hostID,
                      name: name,
                      mode: s.mode,
                      status: s.status,
                      createdAt: s.createdAt,
                    )
                  : entry,
            )
            .toList();
        if (mounted) setState(() {});
        return;
      }
      await widget.api!.request(
        '/api/sessions/${s.id}',
        method: 'PATCH',
        data: {'name': name},
      );
      await _load(silent: true);
    } catch (e) {
      if (mounted) notice(context, e);
    }
  }

  Future<void> _sessionMenu(RemoteSession s) async {
    final action = await showModalBottomSheet<String>(
      context: context,
      builder: (c) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              title: Text(
                s.name,
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
            ),
            ListTile(
              leading: const Icon(Icons.star_outline),
              title: Text(_favorite(s) ? '取消收藏' : '收藏会话'),
              onTap: () => Navigator.pop(c, 'favorite'),
            ),
            ListTile(
              leading: const Icon(Icons.edit_outlined),
              title: const Text('重命名'),
              onTap: () => Navigator.pop(c, 'rename'),
            ),
            ListTile(
              leading: Icon(Icons.stop_circle_outlined, color: V.danger),
              title: Text('终止远端会话', style: TextStyle(color: V.danger)),
              onTap: () => Navigator.pop(c, 'end'),
            ),
            const SizedBox(height: 12),
          ],
        ),
      ),
    );
    if (!mounted) return;
    if (action == 'favorite') {
      await widget.preferences.setBool(
        'mobile.favorite.$scope.${s.id}',
        !_favorite(s),
      );
      setState(() {});
    }
    if (action == 'rename') await _rename(s);
    if (mounted &&
        action == 'end' &&
        await confirm(
          context,
          '终止远端会话？',
          '会停止此会话中的远端程序。仅返回页面可保留任务。',
          action: '终止',
        )) {
      if (widget.demo) return;
      try {
        await widget.api!.request('/api/sessions/${s.id}', method: 'DELETE');
        await _load(silent: true);
      } catch (e) {
        if (mounted) notice(context, e);
      }
    }
  }

  Future<void> _connectHost(RemoteHost host) async {
    final controller = TextEditingController(text: defaultSessionName(host));
    final name = await showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('新建会话'),
        content: TextField(
          controller: controller,
          autofocus: true,
          maxLength: 80,
          decoration: const InputDecoration(
            labelText: '会话名称',
            hintText: '例如：前端开发、日志排查',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () {
              if (controller.text.trim().isNotEmpty &&
                  controller.text.runes.length <= 80) {
                Navigator.pop(c, controller.text.trim());
              }
            },
            child: const Text('创建并连接'),
          ),
        ],
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 300));
    controller.dispose();
    if (!mounted || name == null) return;
    if (widget.demo) {
      final session = RemoteSession(
        id: 'demo-${DateTime.now().microsecondsSinceEpoch}',
        hostID: host.id,
        name: name,
        createdAt: DateTime.now(),
      );
      setState(() => sessions = [session, ...sessions]);
      await _open(session);
      return;
    }
    // Navigate immediately; the new page owns connection progress and failures.
    await Navigator.push(
      context,
      MaterialPageRoute<void>(
        builder: (_) => NewSessionScreen(
          api: widget.api!,
          host: host,
          name: name,
          onCreated: (s) => TerminalScreen(
            api: widget.api,
            session: s,
            host: host,
            device: widget.device,
            demo: false,
            preferences: widget.preferences,
          ),
        ),
      ),
    );
    await _load(silent: true);
  }

  Widget _empty(String title, String hint, {Widget? header}) => ListView(
    physics: const AlwaysScrollableScrollPhysics(),
    padding: EdgeInsets.fromLTRB(
      header == null ? 24 : 14,
      header == null ? 72 : 12,
      header == null ? 24 : 14,
      24,
    ),
    children: [
      ?header,
      if (header != null) const SizedBox(height: 40),
      Center(child: SoftBadge(Icons.nightlight_round, size: 64)),
      const SizedBox(height: 20),
      Text(
        title,
        textAlign: TextAlign.center,
        style: const TextStyle(
          fontSize: 16,
          fontWeight: FontWeight.w700,
          letterSpacing: 1,
        ),
      ),
      const SizedBox(height: 8),
      Text(
        hint,
        textAlign: TextAlign.center,
        style: TextStyle(fontSize: 12, color: V.muted, fontFamily: V.mono),
      ),
    ],
  );

  Widget _sessionList() {
    final visible =
        sessions
            .where(
              (s) =>
                  (tab != 1 || _favorite(s)) &&
                  (tab == 1 || filter != 0 || !s.ended) &&
                  '${s.name} ${s.shortID} ${_host(s.hostID)?.name ?? ''}'
                      .toLowerCase()
                      .contains(search.toLowerCase()),
            )
            .toList()
          ..sort(
            (a, b) =>
                (widget.preferences.getInt('mobile.recent.$scope.${b.id}') ?? 0)
                    .compareTo(
                      widget.preferences.getInt(
                            'mobile.recent.$scope.${a.id}',
                          ) ??
                          0,
                    ),
          );
    return RefreshIndicator(
      onRefresh: _load,
      child: visible.isEmpty
          ? _empty(
              search.isEmpty ? (tab == 1 ? '还没有收藏的会话' : '暂无会话') : '没有匹配的会话',
              tab == 1 ? '在会话上长按或点 ⋮，选择“收藏会话”。' : '从右上角新建，或切换筛选条件。',
              header: tab == 0 && !searchOpen ? _hero() : null,
            )
          : ListView.builder(
              key: const ValueKey('session-list'),
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(14, 12, 14, 16),
              itemCount: visible.length + (tab == 0 && !searchOpen ? 1 : 0),
              itemBuilder: (context, rawIndex) {
                final withHero = tab == 0 && !searchOpen;
                if (withHero && rawIndex == 0) return _hero();
                final index = withHero ? rawIndex - 1 : rawIndex;
                final session = visible[index],
                    host = _host(visible[index].hostID);
                final state = session.ended
                    ? '已结束'
                    : session.status == 'attached'
                    ? '已连接'
                    : '待恢复';
                final stateColor = session.ended
                    ? V.muted
                    : session.status == 'attached'
                    ? V.accent
                    : V.yellow;
                return Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: SoftPanel(
                    key: ValueKey('session-${session.id}'),
                    padding: const EdgeInsets.fromLTRB(12, 10, 2, 10),
                    color: stateColor,
                    stripe: true,
                    glow: session.status == 'attached' && !session.ended,
                    onTap: () => _open(session),
                    onLongPress: () => _sessionMenu(session),
                    child: Row(
                      children: [
                        SoftBadge(Icons.terminal_rounded, color: stateColor),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                _sessionTitle(session),
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: const TextStyle(
                                  fontSize: 15,
                                  fontWeight: FontWeight.w600,
                                  letterSpacing: .4,
                                ),
                              ),
                              const SizedBox(height: 4),
                              Text(
                                '${host?.name ?? session.hostID} · ${session.createdLabel}',
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: TextStyle(
                                  fontSize: 11,
                                  color: V.muted,
                                  fontFamily: V.mono,
                                ),
                              ),
                              const SizedBox(height: 6),
                              StateChip(state, color: stateColor),
                            ],
                          ),
                        ),
                        if (_favorite(session))
                          Icon(Icons.star_rounded, size: 16, color: V.yellow),
                        IconButton(
                          tooltip: '会话操作',
                          onPressed: () => _sessionMenu(session),
                          icon: Icon(Icons.more_vert, size: 20, color: V.muted),
                        ),
                      ],
                    ),
                  ),
                );
              },
            ),
    );
  }

  Widget _hostList() {
    final visible = hosts
        .where(
          (host) => '${host.name} ${host.address}'.toLowerCase().contains(
            search.toLowerCase(),
          ),
        )
        .toList();
    return RefreshIndicator(
      onRefresh: _load,
      child: visible.isEmpty
          ? _empty('暂无匹配的 SSH 主机', '点击右上角“＋”添加 SSH 主机。')
          : ListView.builder(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(14, 12, 14, 16),
              itemCount: visible.length,
              itemBuilder: (_, index) {
                final host = visible[index];
                return Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: SoftPanel(
                    padding: const EdgeInsets.fromLTRB(12, 10, 2, 10),
                    color: V.sky,
                    stripe: true,
                    onTap: () => _connectHost(host),
                    onLongPress: () => _hostMenu(host),
                    child: Row(
                      children: [
                        SoftBadge(Icons.dns_outlined, color: V.sky),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                host.name,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: const TextStyle(
                                  fontSize: 15,
                                  fontWeight: FontWeight.w600,
                                  letterSpacing: .4,
                                ),
                              ),
                              const SizedBox(height: 4),
                              Text(
                                '${host.username}@${host.address}:${host.port}',
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: TextStyle(
                                  fontSize: 11,
                                  color: V.muted,
                                  fontFamily: V.mono,
                                ),
                              ),
                            ],
                          ),
                        ),
                        IconButton(
                          tooltip: '主机操作',
                          onPressed: () => _hostMenu(host),
                          icon: Icon(Icons.more_vert, size: 20, color: V.muted),
                        ),
                      ],
                    ),
                  ),
                );
              },
            ),
    );
  }

  Widget _group(String label, List<Widget> children, {Color? color}) => Padding(
    padding: const EdgeInsets.only(bottom: 18),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SectionLabel(label, color: color),
        const SizedBox(height: 10),
        SoftPanel(
          padding: EdgeInsets.zero,
          color: color,
          child: Column(children: children),
        ),
      ],
    ),
  );

  Future<void> _pickTheme(String id) async {
    await widget.preferences.setString('mobile.appTheme', id);
    V.select(palette: id);
  }

  Future<void> _customBackdrop() async {
    // First tap (or tapping the active one again) picks a photo; otherwise it
    // just switches back to the saved photo.
    if (V.customPath.isNotEmpty &&
        File(V.customPath).existsSync() &&
        V.backdrop != 'custom') {
      await widget.preferences.setString('mobile.appBackdrop', 'custom');
      V.select(backdrop: 'custom');
      return;
    }
    try {
      final picked = await FilePicker.platform.pickFiles(
        type: FileType.image,
        withData: false,
      );
      final source = picked?.files.single.path;
      if (source == null) return;
      final input = File(source);
      if (await input.length() > 25 * 1024 * 1024) {
        if (mounted) notice(context, '图片太大，请选择 25MB 以内的图片');
        return;
      }
      final codec = await ui.instantiateImageCodec(await input.readAsBytes());
      codec.dispose();
      final dir = await getApplicationSupportDirectory();
      final suffix = source.split('.').last.toLowerCase();
      final extension = {'png', 'webp', 'jpg', 'jpeg'}.contains(suffix)
          ? suffix
          : 'jpg';
      final saved = await input.copy(
        '${dir.path}/backdrop_${DateTime.now().millisecondsSinceEpoch}.$extension',
      );
      final old = V.customPath;
      await widget.preferences.setString('mobile.appBackdrop', 'custom');
      await widget.preferences.setString('mobile.appBackdropFile', saved.path);
      V.select(backdrop: 'custom', file: saved.path);
      if (old.isNotEmpty && old != saved.path) {
        try {
          await File(old).delete();
        } catch (_) {}
      }
      await FilePicker.platform.clearTemporaryFiles();
    } catch (e) {
      if (mounted) notice(context, '图片无法使用，请换一张试试');
    }
  }

  Future<void> _pickBackdrop(String id) async {
    await widget.preferences.setString('mobile.appBackdrop', id);
    V.select(backdrop: id);
  }

  Widget _settings() => ListView(
    padding: const EdgeInsets.all(14),
    children: [
      SoftPanel(
        glow: true,
        radius: 18,
        padding: const EdgeInsets.all(14),
        child: Row(
          children: [
            SoftBadge(Icons.person_outline, size: 48, color: V.sky),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '${widget.user['username']}',
                    style: const TextStyle(
                      fontSize: 17,
                      fontWeight: FontWeight.w700,
                      letterSpacing: .6,
                    ),
                  ),
                  const SizedBox(height: 3),
                  Text(
                    widget.demo ? '设计预览 · 示例数据' : '${widget.api!.base}',
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: V.muted,
                      fontSize: 11,
                      fontFamily: V.mono,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
      const SizedBox(height: 22),
      _group('网络与服务', [
        ListTile(
          leading: const Icon(Icons.dns_outlined),
          title: const Text('Velin 服务管理'),
          trailing: Icon(Icons.chevron_right, color: V.muted),
          onTap: _profiles,
        ),
        const Divider(),
        ListTile(
          leading: const Icon(Icons.language),
          title: const Text('反向代理'),
          trailing: Icon(Icons.chevron_right, color: V.muted),
          onTap: () => setState(() => tab = 3),
        ),
      ], color: V.accent),
      if (widget.onLock != null)
        _group('安全', [
          ListTile(
            leading: Icon(Icons.lock_outline_rounded, color: V.yellow),
            title: const Text('锁定工作台'),
            subtitle: Text(
              '需输入 PIN 才能继续使用',
              style: TextStyle(color: V.muted, fontSize: 12),
            ),
            trailing: Icon(Icons.chevron_right, color: V.muted),
            onTap: () async {
              try {
                await widget.onLock?.call();
              } catch (e) {
                if (mounted) notice(context, e);
              }
            },
          ),
        ], color: V.yellow),
      _group('外观', [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 14, 16, 6),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('主题配色', style: TextStyle(fontWeight: FontWeight.w600)),
              const SizedBox(height: 10),
              SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: Row(
                  children: [
                    for (final p in V.palettes)
                      _ThemeSwatch(
                        palette: p,
                        selected: V.paletteId == p.id,
                        onTap: () => _pickTheme(p.id),
                      ),
                  ],
                ),
              ),
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 14),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('背景插画', style: TextStyle(fontWeight: FontWeight.w600)),
              const SizedBox(height: 10),
              SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: Row(
                  children: [
                    _BackdropTile(
                      label: '跟随主题',
                      selected: V.backdrop.isEmpty,
                      onTap: () => _pickBackdrop(''),
                    ),
                    for (final (id, name) in V.arts)
                      _BackdropTile(
                        label: name,
                        art: 'assets/art/$id.svg',
                        selected: V.backdrop == id,
                        onTap: () => _pickBackdrop(id),
                      ),
                    _BackdropTile(
                      label: '自定义',
                      file: V.customPath.isEmpty ? null : V.customPath,
                      selected: V.backdrop == 'custom',
                      onTap: _customBackdrop,
                    ),
                    _BackdropTile(
                      label: '纯色',
                      selected: V.backdrop == 'none',
                      onTap: () => _pickBackdrop('none'),
                    ),
                  ],
                ),
              ),
              if (V.backdrop != 'none') ...[
                const SizedBox(height: 6),
                Row(
                  children: [
                    Text(
                      '背景暗度',
                      style: TextStyle(color: V.muted, fontSize: 12),
                    ),
                    Expanded(
                      child: Slider(
                        value: V.dim,
                        min: 0,
                        max: .9,
                        onChanged: (value) => V.select(dim: value),
                        onChangeEnd: (value) => widget.preferences.setDouble(
                          'mobile.appBackdropDim',
                          value,
                        ),
                      ),
                    ),
                  ],
                ),
              ],
            ],
          ),
        ),
      ]),
      _group('终端偏好', [
        SwitchListTile(
          title: const Text('悬浮滚动按钮'),
          subtitle: Text(
            '默认关闭；开启后终端右下角出现，按住上下拖动滚动',
            style: TextStyle(color: V.muted, fontSize: 12),
          ),
          value: widget.preferences.getBool('mobile.scrollKnob') ?? false,
          onChanged: (value) async {
            await widget.preferences.setBool('mobile.scrollKnob', value);
            if (mounted) setState(() {});
          },
        ),
        const Divider(),
        ListTile(
          title: const Text('终端字号'),
          subtitle: Text(
            '仅保存在这台手机',
            style: TextStyle(color: V.muted, fontSize: 12),
          ),
          trailing: DropdownButton<double>(
            value: widget.preferences.getDouble('mobile.fontSize') ?? 13,
            dropdownColor: V.surface,
            underline: const SizedBox.shrink(),
            style: TextStyle(color: V.accent, fontFamily: V.mono),
            items: [12.0, 13.0, 14.0, 15.0, 16.0, 18.0]
                .map(
                  (v) =>
                      DropdownMenuItem(value: v, child: Text('${v.toInt()}')),
                )
                .toList(),
            onChanged: (v) async {
              if (v != null) {
                await widget.preferences.setDouble('mobile.fontSize', v);
                setState(() {});
              }
            },
          ),
        ),
        const Divider(),
        ListTile(
          title: const Text('终端配色'),
          subtitle: Text(
            '仅影响手机显示',
            style: TextStyle(color: V.muted, fontSize: 12),
          ),
          trailing: DropdownButton<String>(
            value:
                widget.preferences.getString('mobile.terminalTheme') ??
                'graphite',
            dropdownColor: V.surface,
            underline: const SizedBox.shrink(),
            style: TextStyle(color: V.accent, fontFamily: V.mono),
            items: terminalPalettes.entries
                .map(
                  (entry) => DropdownMenuItem(
                    value: entry.key,
                    child: Text(entry.value['name']!),
                  ),
                )
                .toList(),
            onChanged: (value) async {
              if (value != null) {
                await widget.preferences.setString(
                  'mobile.terminalTheme',
                  value,
                );
                if (mounted) setState(() {});
              }
            },
          ),
        ),
      ]),
      _group('本地数据', [
        ListTile(
          leading: Icon(Icons.cleaning_services_outlined, color: V.yellow),
          title: const Text('清理本地草稿与最近记录'),
          onTap: () async {
            if (!await confirm(
              context,
              '清理本地记录？',
              '会删除当前服务和账户的手机草稿、收藏和最近记录。远端会话和附件保留。',
              action: '清理',
            )) {
              return;
            }
            for (final key
                in widget.preferences
                    .getKeys()
                    .where(
                      (k) =>
                          k.startsWith('mobile.draft.$scope.') ||
                          k.startsWith('mobile.recent.$scope.') ||
                          k.startsWith('mobile.favorite.$scope.'),
                    )
                    .toList()) {
              await widget.preferences.remove(key);
            }
            await ReplayCache.clear();
            if (mounted) setState(() {});
          },
        ),
      ], color: V.yellow),
      const SizedBox(height: 6),
      OutlinedButton.icon(
        onPressed: widget.onLogout,
        icon: const Icon(Icons.logout, size: 18),
        label: const Text('退出登录'),
      ),
      const SizedBox(height: 32),
      Center(
        child: Text(
          '✦ velin mobile · 0.1.2 ✦',
          style: TextStyle(
            color: V.muted,
            fontSize: 11,
            letterSpacing: 2,
            fontFamily: V.mono,
          ),
        ),
      ),
    ],
  );
  Widget _proxy() => WebServicesScreen(
    key: const ValueKey('proxy-tab'),
    api: widget.api,
    hosts: hosts,
    demo: widget.demo,
    embedded: true,
  );

  Widget _filterPill() => Container(
    padding: const EdgeInsets.all(3),
    decoration: BoxDecoration(
      color: V.surface.withValues(alpha: .7),
      borderRadius: BorderRadius.circular(18),
      border: Border.all(color: V.accent.withValues(alpha: .3)),
    ),
    child: Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        for (final (value, label) in [(0, '进行中'), (2, '全部')])
          GestureDetector(
            key: ValueKey('filter-$value'),
            behavior: HitTestBehavior.opaque,
            onTap: () => setState(() => filter = value),
            child: AnimatedContainer(
              duration: const Duration(milliseconds: 200),
              padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 6),
              decoration: BoxDecoration(
                color: filter == value ? V.accent : Colors.transparent,
                borderRadius: BorderRadius.circular(15),
              ),
              child: Text(
                label,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  color: filter == value ? V.bg : V.muted,
                ),
              ),
            ),
          ),
      ],
    ),
  );

  Widget _stat(String label, int value, Color color) => Expanded(
    child: Container(
      margin: const EdgeInsets.only(right: 8),
      padding: const EdgeInsets.symmetric(vertical: 8),
      decoration: BoxDecoration(
        color: color.withValues(alpha: .1),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: color.withValues(alpha: .35)),
      ),
      child: Column(
        children: [
          Text(
            '$value',
            style: TextStyle(
              color: color,
              fontSize: 19,
              fontWeight: FontWeight.w800,
            ),
          ),
          Text(label, style: TextStyle(color: V.muted, fontSize: 10)),
        ],
      ),
    ),
  );

  Widget _hero() {
    final hour = DateTime.now().hour;
    final greeting = hour < 6
        ? '夜深了'
        : hour < 11
        ? '早上好'
        : hour < 14
        ? '中午好'
        : hour < 18
        ? '下午好'
        : '晚上好';
    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: SoftPanel(
        glow: true,
        radius: 22,
        padding: const EdgeInsets.fromLTRB(16, 14, 8, 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        '$greeting，${widget.user['username']}',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          fontSize: 20,
                          fontWeight: FontWeight.w800,
                          letterSpacing: .5,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        '今晚也写点漂亮的代码吧 ✦',
                        style: TextStyle(color: V.muted, fontSize: 12),
                      ),
                    ],
                  ),
                ),
                Padding(
                  padding: EdgeInsets.only(right: 8),
                  child: SoftBadge(
                    Icons.nightlight_round,
                    size: 46,
                    color: V.yellow,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                _stat(
                  '在线',
                  sessions
                      .where((s) => !s.ended && s.status == 'attached')
                      .length,
                  V.accent,
                ),
                _stat('会话', sessions.where((s) => !s.ended).length, V.sky),
                _stat('收藏', sessions.where(_favorite).length, V.yellow),
                _stat('主机', hosts.length, V.accent),
              ],
            ),
          ],
        ),
      ),
    );
  }

  static const _titles = ['会话', '收藏', '主机', '代理', '设置'];

  @override
  Widget build(BuildContext context) => Scaffold(
    extendBody: true,
    extendBodyBehindAppBar: true,
    appBar: AppBar(
      toolbarHeight: 52,
      backgroundColor: Colors.transparent,
      scrolledUnderElevation: 0,
      shape: const Border(),
      title: Row(
        children: [
          Icon(Icons.auto_awesome, size: 16, color: V.accent),
          const SizedBox(width: 8),
          Text(
            tab == 0 && widget.demo ? '会话 · 示例' : _titles[tab],
            style: const TextStyle(
              fontSize: 18,
              fontWeight: FontWeight.w700,
              letterSpacing: 1.5,
            ),
          ),
        ],
      ),
      actions: tab > 2
          ? null
          : [
              if (tab == 0) Center(child: _filterPill()),
              IconButton(
                tooltip: searchOpen ? '收起搜索' : '搜索',
                onPressed: () => setState(() {
                  searchOpen = !searchOpen;
                  if (!searchOpen) {
                    search = '';
                    searchController.clear();
                  }
                }),
                icon: Icon(
                  searchOpen ? Icons.search_off_rounded : Icons.search_rounded,
                  size: 20,
                ),
              ),
              IconButton(
                tooltip: tab == 2 ? '添加 SSH 主机' : '新建会话',
                onPressed: () {
                  if (tab == 2) {
                    _editHost();
                  } else {
                    setState(() => tab = 2);
                  }
                },
                icon: const Icon(Icons.add_rounded, size: 22),
              ),
            ],
    ),
    bottomNavigationBar: SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 0, 14, 10),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(30),
          child: BackdropFilter(
            filter: ImageFilter.blur(sigmaX: 16, sigmaY: 16),
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: V.surface.withValues(alpha: .78),
                borderRadius: BorderRadius.circular(30),
                border: Border.all(color: V.accent.withValues(alpha: .28)),
              ),
              child: NavigationBar(
                backgroundColor: Colors.transparent,
                surfaceTintColor: Colors.transparent,
                elevation: 0,
                height: 64,
                selectedIndex: tab,
                onDestinationSelected: (value) => setState(() => tab = value),
                destinations: [
                  NavigationDestination(
                    icon: Icon(Icons.terminal_rounded),
                    selectedIcon: Icon(Icons.terminal_rounded, color: V.accent),
                    label: '会话',
                  ),
                  NavigationDestination(
                    icon: Icon(Icons.star_border_rounded),
                    selectedIcon: Icon(Icons.star_rounded, color: V.accent),
                    label: '收藏',
                  ),
                  NavigationDestination(
                    icon: Icon(Icons.dns_outlined),
                    selectedIcon: Icon(Icons.dns_rounded, color: V.accent),
                    label: '主机',
                  ),
                  NavigationDestination(
                    icon: Icon(Icons.language),
                    selectedIcon: Icon(Icons.language, color: V.accent),
                    label: '代理',
                  ),
                  NavigationDestination(
                    icon: Icon(Icons.settings_outlined),
                    selectedIcon: Icon(Icons.settings_rounded, color: V.accent),
                    label: '设置',
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    ),
    body: Scene(
      child: SafeArea(
        top: true,
        child: Column(
          children: [
            if (searchOpen && tab < 3)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 6, 12, 6),
                child: TextField(
                  key: const ValueKey('session-search'),
                  controller: searchController,
                  autofocus: true,
                  onChanged: (value) => setState(() => search = value),
                  decoration: InputDecoration(
                    hintText: tab == 2 ? '搜索主机' : '搜索会话或主机',
                    prefixIcon: const Icon(Icons.search, size: 18),
                    contentPadding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 10,
                    ),
                  ),
                ),
              ),
            if (error.isNotEmpty)
              Material(
                color: V.surface,
                child: ListTile(
                  dense: true,
                  title: Text(
                    error,
                    style: TextStyle(color: V.danger, fontSize: 12),
                  ),
                  trailing: TextButton(
                    onPressed: _load,
                    child: const Text('重试'),
                  ),
                ),
              ),
            Expanded(
              child: tab < 3 && busy && sessions.isEmpty && error.isEmpty
                  ? const Center(
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : [
                      _sessionList,
                      _sessionList,
                      _hostList,
                      _proxy,
                      _settings,
                    ][tab](),
            ),
          ],
        ),
      ),
    ),
  );
}

class NewSessionScreen extends StatefulWidget {
  final RemoteApi api;
  final RemoteHost host;
  final String name;
  final Widget Function(RemoteSession) onCreated;
  const NewSessionScreen({
    super.key,
    required this.api,
    required this.host,
    required this.name,
    required this.onCreated,
  });
  @override
  State<NewSessionScreen> createState() => _NewSessionScreenState();
}

class _NewSessionScreenState extends State<NewSessionScreen> {
  bool busy = true;
  String error = '';
  @override
  void initState() {
    super.initState();
    _create();
  }

  Future<void> _create() async {
    setState(() {
      busy = true;
      error = '';
    });
    try {
      final session = await widget.api.create(widget.host, name: widget.name);
      if (mounted) {
        Navigator.pushReplacement(
          context,
          MaterialPageRoute<void>(builder: (_) => widget.onCreated(session)),
        );
      }
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text(widget.name)),
    body: Scene(
      child: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (busy)
                const CircularProgressIndicator()
              else
                Icon(Icons.cloud_off_outlined, color: V.danger, size: 40),
              const SizedBox(height: 24),
              Text(busy ? '正在创建终端…' : error, textAlign: TextAlign.center),
              if (!busy) ...[
                const SizedBox(height: 20),
                FilledButton(onPressed: _create, child: const Text('重试')),
              ],
              const SizedBox(height: 12),
              Text(
                '返回页面会保留已创建的远端会话',
                style: TextStyle(color: V.muted, fontSize: 12),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class _ThemeSwatch extends StatelessWidget {
  final AppPalette palette;
  final bool selected;
  final VoidCallback onTap;
  const _ThemeSwatch({
    required this.palette,
    required this.selected,
    required this.onTap,
  });
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(right: 12),
    child: GestureDetector(
      onTap: onTap,
      child: Column(
        children: [
          AnimatedContainer(
            duration: const Duration(milliseconds: 200),
            width: 64,
            height: 64,
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(
              color: palette.bg,
              borderRadius: BorderRadius.circular(20),
              border: Border.all(
                color: selected ? V.accent : palette.line,
                width: selected ? 2.2 : 1,
              ),
              boxShadow: selected
                  ? [
                      BoxShadow(
                        color: V.accent.withValues(alpha: .35),
                        blurRadius: 12,
                      ),
                    ]
                  : null,
            ),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    _dot(palette.accent),
                    const SizedBox(width: 5),
                    _dot(palette.sky),
                  ],
                ),
                const SizedBox(height: 5),
                Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    _dot(palette.yellow),
                    const SizedBox(width: 5),
                    _dot(palette.surface),
                  ],
                ),
              ],
            ),
          ),
          const SizedBox(height: 6),
          Text(
            palette.name,
            style: TextStyle(
              fontSize: 12,
              color: selected ? V.accent : V.muted,
              fontWeight: selected ? FontWeight.w700 : FontWeight.w400,
            ),
          ),
        ],
      ),
    ),
  );
  Widget _dot(Color c) => Container(
    width: 16,
    height: 16,
    decoration: BoxDecoration(color: c, shape: BoxShape.circle),
  );
}

class _BackdropTile extends StatelessWidget {
  final String label;
  final String? art, file;
  final bool selected;
  final VoidCallback onTap;
  const _BackdropTile({
    required this.label,
    this.art,
    this.file,
    required this.selected,
    required this.onTap,
  });
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(right: 12),
    child: GestureDetector(
      onTap: onTap,
      child: Column(
        children: [
          AnimatedContainer(
            duration: const Duration(milliseconds: 200),
            width: 58,
            height: 96,
            clipBehavior: Clip.antiAlias,
            decoration: BoxDecoration(
              color: V.surface,
              borderRadius: BorderRadius.circular(16),
              border: Border.all(
                color: selected ? V.accent : V.line,
                width: selected ? 2.2 : 1,
              ),
              boxShadow: selected
                  ? [
                      BoxShadow(
                        color: V.accent.withValues(alpha: .35),
                        blurRadius: 12,
                      ),
                    ]
                  : null,
            ),
            child: file != null
                ? Image.file(
                    File(file!),
                    fit: BoxFit.cover,
                    cacheWidth: 200,
                    errorBuilder: (_, _, _) =>
                        const Center(child: Icon(Icons.broken_image_outlined)),
                  )
                : art == null
                ? Center(
                    child: Icon(
                      label == '纯色'
                          ? Icons.format_color_fill_rounded
                          : label == '自定义'
                          ? Icons.add_photo_alternate_outlined
                          : Icons.auto_awesome,
                      color: selected ? V.accent : V.muted,
                    ),
                  )
                : SvgPicture.asset(
                    art!,
                    fit: BoxFit.cover,
                    alignment: Alignment.bottomCenter,
                  ),
          ),
          const SizedBox(height: 6),
          Text(
            label,
            style: TextStyle(
              fontSize: 12,
              color: selected ? V.accent : V.muted,
              fontWeight: selected ? FontWeight.w700 : FontWeight.w400,
            ),
          ),
        ],
      ),
    ),
  );
}
