import 'dart:async';
import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import '../terminal/palettes.dart';
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
      tab = 1;
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
              leading: const Icon(Icons.delete_outline, color: V.danger),
              title: const Text('删除主机', style: TextStyle(color: V.danger)),
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

  Future<void> _webServices() => Navigator.push<void>(
    context,
    MaterialPageRoute(
      builder: (_) =>
          WebServicesScreen(api: widget.api, hosts: hosts, demo: widget.demo),
    ),
  );

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
              leading: const Icon(Icons.stop_circle_outlined, color: V.danger),
              title: const Text('终止远端会话', style: TextStyle(color: V.danger)),
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

  Widget _empty(String title, String hint) => ListView(
    physics: const AlwaysScrollableScrollPhysics(),
    padding: const EdgeInsets.fromLTRB(24, 80, 24, 24),
    children: [
      Text(
        title,
        textAlign: TextAlign.center,
        style: const TextStyle(fontSize: 16),
      ),
      const SizedBox(height: 8),
      Text(
        hint,
        textAlign: TextAlign.center,
        style: const TextStyle(fontSize: 12, color: V.muted),
      ),
    ],
  );

  Widget _sessionList() {
    final visible =
        sessions
            .where(
              (s) =>
                  (filter != 1 || _favorite(s)) &&
                  (filter != 0 || !s.ended) &&
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
          ? _empty(search.isEmpty ? '暂无会话' : '没有匹配的会话', '从右上角新建，或切换筛选条件。')
          : ListView.separated(
              key: const ValueKey('session-list'),
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.only(bottom: 12),
              itemCount: visible.length,
              separatorBuilder: (_, _) => const Divider(indent: 64),
              itemBuilder: (context, index) {
                final session = visible[index],
                    host = _host(visible[index].hostID);
                final state = session.ended
                    ? '已结束'
                    : session.status == 'attached'
                    ? '已连接'
                    : '待恢复';
                return ListTile(
                  key: ValueKey('session-${session.id}'),
                  minTileHeight: 76,
                  contentPadding: const EdgeInsets.only(left: 16, right: 4),
                  horizontalTitleGap: 12,
                  leading: Container(
                    width: 36,
                    height: 36,
                    decoration: BoxDecoration(
                      color: V.surface,
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: const Icon(
                      Icons.terminal_rounded,
                      size: 19,
                      color: V.text,
                    ),
                  ),
                  title: Text(
                    _sessionTitle(session),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                  subtitle: Padding(
                    padding: const EdgeInsets.only(top: 3),
                    child: Text(
                      '${host?.name ?? session.hostID} · ${session.createdLabel} · $state',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 11, color: V.muted),
                    ),
                  ),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (_favorite(session))
                        const Icon(
                          Icons.star_rounded,
                          size: 13,
                          color: V.muted,
                        ),
                      IconButton(
                        tooltip: '会话操作',
                        onPressed: () => _sessionMenu(session),
                        icon: const Icon(
                          Icons.more_horiz,
                          size: 20,
                          color: V.muted,
                        ),
                      ),
                    ],
                  ),
                  onTap: () => _open(session),
                  onLongPress: () => _sessionMenu(session),
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
          : ListView.separated(
              physics: const AlwaysScrollableScrollPhysics(),
              itemCount: visible.length,
              separatorBuilder: (_, _) => const Divider(indent: 64),
              itemBuilder: (_, index) {
                final host = visible[index];
                return ListTile(
                  minTileHeight: 76,
                  contentPadding: const EdgeInsets.symmetric(horizontal: 16),
                  horizontalTitleGap: 12,
                  leading: Container(
                    width: 36,
                    height: 36,
                    decoration: BoxDecoration(
                      color: V.surface,
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: const Icon(
                      Icons.dns_outlined,
                      size: 19,
                      color: V.text,
                    ),
                  ),
                  title: Text(
                    host.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                  subtitle: Text(
                    '${host.username}@${host.address}:${host.port}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontSize: 11, color: V.muted),
                  ),
                  trailing: IconButton(
                    tooltip: '主机操作',
                    onPressed: () => _hostMenu(host),
                    icon: const Icon(
                      Icons.more_horiz,
                      size: 20,
                      color: V.muted,
                    ),
                  ),
                  onTap: () => _connectHost(host),
                  onLongPress: () => _hostMenu(host),
                );
              },
            ),
    );
  }

  Widget _settings() => ListView(
    padding: const EdgeInsets.all(20),
    children: [
      ListTile(
        contentPadding: EdgeInsets.zero,
        leading: const CircleAvatar(
          backgroundColor: V.surface,
          child: Icon(Icons.person_outline, color: V.accent),
        ),
        title: Text('${widget.user['username']}'),
        subtitle: Text(
          widget.demo ? '设计预览 · 示例数据' : '${widget.api!.base}',
          style: const TextStyle(color: V.muted, fontSize: 12),
        ),
      ),
      const SizedBox(height: 24),
      const Divider(),
      ListTile(
        contentPadding: EdgeInsets.zero,
        leading: const Icon(Icons.dns_outlined),
        title: const Text('Velin 服务管理'),
        onTap: _profiles,
      ),
      ListTile(
        contentPadding: EdgeInsets.zero,
        leading: const Icon(Icons.language),
        title: const Text('反向代理'),
        onTap: _webServices,
      ),
      const Divider(),
      ListTile(
        contentPadding: EdgeInsets.zero,
        title: const Text('终端字号'),
        subtitle: const Text(
          '仅保存在这台手机',
          style: TextStyle(color: V.muted, fontSize: 12),
        ),
        trailing: DropdownButton<double>(
          value: widget.preferences.getDouble('mobile.fontSize') ?? 13,
          items: [12.0, 13.0, 14.0, 15.0, 16.0, 18.0]
              .map(
                (v) => DropdownMenuItem(value: v, child: Text('${v.toInt()}')),
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
        contentPadding: EdgeInsets.zero,
        title: const Text('终端配色'),
        subtitle: const Text(
          '仅影响手机显示',
          style: TextStyle(color: V.muted, fontSize: 12),
        ),
        trailing: DropdownButton<String>(
          value:
              widget.preferences.getString('mobile.terminalTheme') ??
              'graphite',
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
              await widget.preferences.setString('mobile.terminalTheme', value);
              if (mounted) setState(() {});
            }
          },
        ),
      ),
      const Divider(),
      ListTile(
        contentPadding: EdgeInsets.zero,
        leading: const Icon(Icons.cleaning_services_outlined),
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
          if (mounted) setState(() {});
        },
      ),
      const Divider(),
      const SizedBox(height: 24),
      OutlinedButton.icon(
        onPressed: widget.onLogout,
        icon: const Icon(Icons.logout, size: 18),
        label: const Text('退出登录'),
      ),
      const SizedBox(height: 36),
      const Center(
        child: Text(
          'VELIN MOBILE · 0.1.2',
          style: TextStyle(color: V.muted, fontSize: 11, letterSpacing: 1),
        ),
      ),
    ],
  );
  Widget _tab(String label, int value) => Expanded(
    child: TextButton(
      onPressed: () => setState(() => tab = value),
      style: TextButton.styleFrom(
        minimumSize: const Size(48, 48),
        padding: const EdgeInsets.symmetric(horizontal: 8),
        foregroundColor: tab == value ? V.text : V.muted,
        shape: const RoundedRectangleBorder(),
      ),
      child: Text(
        label,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        style: TextStyle(
          fontSize: 15,
          fontWeight: tab == value ? FontWeight.w600 : FontWeight.w400,
        ),
      ),
    ),
  );

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      toolbarHeight: 52,
      titleSpacing: tab == 2 ? 0 : 8,
      leading: tab == 2
          ? BackButton(onPressed: () => setState(() => tab = 0))
          : null,
      title: tab == 2
          ? const Text('设置', style: TextStyle(fontSize: 16))
          : Row(
              children: [
                _tab(widget.demo ? '会话 · 示例' : '会话', 0),
                _tab('主机', 1),
              ],
            ),
      actions: tab == 2
          ? null
          : [
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
                tooltip: tab == 1 ? '添加 SSH 主机' : '新建会话',
                onPressed: () {
                  if (tab == 1) {
                    _editHost();
                  } else {
                    setState(() => tab = 1);
                  }
                },
                icon: const Icon(Icons.add_rounded, size: 22),
              ),
              PopupMenuButton<String>(
                tooltip: '列表选项',
                icon: const Icon(Icons.more_horiz, size: 22),
                onSelected: (value) async {
                  if (value == 'refresh') {
                    _load();
                    return;
                  }
                  if (value == 'host') {
                    _editHost();
                    return;
                  }
                  if (value == 'proxy') {
                    _webServices();
                    return;
                  }
                  if (value == 'lock') {
                    try {
                      await widget.onLock?.call();
                    } catch (e) {
                      if (context.mounted) notice(context, e);
                    }
                    return;
                  }
                  if (value == 'profiles') {
                    _profiles();
                    return;
                  }
                  setState(() {
                    if (value == 'settings') {
                      tab = 2;
                    }
                    if (value.startsWith('filter:')) {
                      filter = int.parse(value.substring(7));
                      tab = 0;
                    }
                  });
                },
                itemBuilder: (_) => [
                  if (tab == 0) ...[
                    for (final (index, label) in ['进行中', '已收藏', '全部会话'].indexed)
                      CheckedPopupMenuItem(
                        value: 'filter:$index',
                        checked: filter == index,
                        child: Text(label),
                      ),
                    const PopupMenuDivider(),
                  ],
                  const PopupMenuItem(value: 'refresh', child: Text('刷新列表')),
                  const PopupMenuItem(value: 'host', child: Text('添加 SSH 主机')),
                  const PopupMenuItem(value: 'proxy', child: Text('反向代理')),
                  const PopupMenuItem(
                    value: 'profiles',
                    child: Text('Velin 服务管理'),
                  ),
                  if (widget.onLock != null)
                    const PopupMenuItem(value: 'lock', child: Text('锁定工作台')),
                  const PopupMenuItem(value: 'settings', child: Text('设置')),
                ],
              ),
            ],
    ),
    body: SafeArea(
      top: false,
      child: Column(
        children: [
          const Divider(),
          if (searchOpen && tab != 2)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 6, 12, 6),
              child: TextField(
                key: const ValueKey('session-search'),
                controller: searchController,
                autofocus: true,
                onChanged: (value) => setState(() => search = value),
                decoration: InputDecoration(
                  hintText: tab == 0 ? '搜索会话或主机' : '搜索主机',
                  prefixIcon: const Icon(Icons.search, size: 18),
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 10,
                  ),
                ),
              ),
            ),
          if (tab == 0 && filter != 0)
            Align(
              alignment: Alignment.centerLeft,
              child: Padding(
                padding: const EdgeInsets.only(left: 16, top: 8, bottom: 2),
                child: Text(
                  filter == 1 ? '已收藏' : '全部会话',
                  style: const TextStyle(fontSize: 11, color: V.muted),
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
                  style: const TextStyle(color: V.danger, fontSize: 12),
                ),
                trailing: TextButton(onPressed: _load, child: const Text('重试')),
              ),
            ),
          Expanded(
            child: busy && sessions.isEmpty && error.isEmpty
                ? const Center(child: CircularProgressIndicator(strokeWidth: 2))
                : [_sessionList, _hostList, _settings][tab](),
          ),
        ],
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
    body: Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (busy)
              const CircularProgressIndicator()
            else
              const Icon(Icons.cloud_off_outlined, color: V.danger, size: 40),
            const SizedBox(height: 24),
            Text(busy ? '正在创建终端…' : error, textAlign: TextAlign.center),
            if (!busy) ...[
              const SizedBox(height: 20),
              FilledButton(onPressed: _create, child: const Text('重试')),
            ],
            const SizedBox(height: 12),
            const Text(
              '返回页面会保留已创建的远端会话',
              style: TextStyle(color: V.muted, fontSize: 12),
            ),
          ],
        ),
      ),
    ),
  );
}
