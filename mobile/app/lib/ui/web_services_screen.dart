import 'package:flutter/material.dart';
import 'package:uuid/uuid.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import 'theme.dart';
import 'web_service_screen.dart';

class WebServicesScreen extends StatefulWidget {
  final RemoteApi? api;
  final List<RemoteHost> hosts;
  final bool demo;
  const WebServicesScreen({
    super.key,
    required this.api,
    required this.hosts,
    this.demo = false,
  });
  @override
  State<WebServicesScreen> createState() => _WebServicesScreenState();
}

class _WebServicesScreenState extends State<WebServicesScreen> {
  List<RemoteWebService> services = [];
  bool busy = true;
  String error = '';
  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final items = widget.demo
          ? [
              const RemoteWebService(
                id: 'demo-web',
                hostID: 'demo-host',
                name: '项目开发预览',
                targetURL: 'http://127.0.0.1:5173',
                pageMode: 'vite',
              ),
            ]
          : await widget.api!.webServices();
      if (mounted) {
        setState(() {
          services = items;
          error = '';
        });
      }
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> _edit([RemoteWebService? service]) async {
    final saved = await Navigator.push<RemoteWebService>(
      context,
      MaterialPageRoute(
        builder: (_) => WebServiceEditor(
          api: widget.api,
          hosts: widget.hosts,
          service: service,
          demo: widget.demo,
        ),
      ),
    );
    if (saved == null || !mounted) return;
    setState(() {
      services = [...services.where((s) => s.id != saved.id), saved]
        ..sort((a, b) => a.name.compareTo(b.name));
    });
  }

  Future<void> _delete(RemoteWebService service) async {
    if (!await confirm(
      context,
      '删除反向代理？',
      '删除“${service.name}”的访问入口。',
      action: '删除',
    )) {
      return;
    }
    try {
      if (!widget.demo) {
        await widget.api!.request(
          '/api/web-services/${service.id}',
          method: 'DELETE',
        );
      }
      if (mounted) {
        setState(() => services.removeWhere((s) => s.id == service.id));
      }
    } catch (e) {
      if (mounted) notice(context, e);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      toolbarHeight: 52,
      title: const Text('反向代理', style: TextStyle(fontSize: 16)),
      actions: [
        IconButton(
          tooltip: '添加反向代理',
          onPressed: _edit,
          icon: const Icon(Icons.add),
        ),
      ],
    ),
    body: SafeArea(
      top: false,
      child: busy
          ? const Center(child: CircularProgressIndicator(strokeWidth: 2))
          : error.isNotEmpty
          ? Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(error),
                    TextButton(onPressed: _load, child: const Text('重试')),
                  ],
                ),
              ),
            )
          : RefreshIndicator(
              onRefresh: _load,
              child: ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                children: [
                  if (widget.demo)
                    const Padding(
                      padding: EdgeInsets.all(16),
                      child: Text(
                        '示例数据 · 不连接真实服务',
                        style: TextStyle(color: V.muted, fontSize: 12),
                      ),
                    ),
                  if (services.isEmpty)
                    const Padding(
                      padding: EdgeInsets.all(32),
                      child: Text(
                        '暂无反向代理，点击右上角添加。',
                        style: TextStyle(color: V.muted),
                      ),
                    ),
                  for (final service in services) ...[
                    ListTile(
                      minTileHeight: 76,
                      leading: const Icon(Icons.language, color: V.accent),
                      title: Text(
                        service.name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontSize: 14),
                      ),
                      subtitle: Text(
                        service.targetURL,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontSize: 11, color: V.muted),
                      ),
                      onTap: () => Navigator.push(
                        context,
                        MaterialPageRoute(
                          builder: (_) => WebServiceScreen(
                            api: widget.api,
                            service: service,
                            demo: widget.demo,
                          ),
                        ),
                      ),
                      trailing: PopupMenuButton<String>(
                        tooltip: '代理操作',
                        onSelected: (v) {
                          if (v == 'edit') _edit(service);
                          if (v == 'delete') _delete(service);
                        },
                        itemBuilder: (_) => [
                          const PopupMenuItem(value: 'edit', child: Text('编辑')),
                          const PopupMenuItem(
                            value: 'delete',
                            child: Text('删除'),
                          ),
                        ],
                      ),
                    ),
                    const Divider(indent: 64),
                  ],
                ],
              ),
            ),
    ),
  );
}

class WebServiceEditor extends StatefulWidget {
  final RemoteApi? api;
  final List<RemoteHost> hosts;
  final RemoteWebService? service;
  final bool demo;
  const WebServiceEditor({
    super.key,
    required this.api,
    required this.hosts,
    this.service,
    this.demo = false,
  });
  @override
  State<WebServiceEditor> createState() => _WebServiceEditorState();
}

class _WebServiceEditorState extends State<WebServiceEditor> {
  final form = GlobalKey<FormState>();
  final name = TextEditingController(),
      target = TextEditingController(),
      upstream = TextEditingController(),
      port = TextEditingController();
  String host = '', proxyMode = 'path', pageMode = 'html', error = '';
  bool skipTLS = false, busy = false;
  @override
  void initState() {
    super.initState();
    final s = widget.service;
    name.text = s?.name ?? '';
    target.text = s?.targetURL ?? 'http://127.0.0.1:80';
    upstream.text = s?.upstreamHost ?? '';
    port.text = '${s?.listenPort == 0 ? 18080 : s?.listenPort ?? 18080}';
    host =
        s?.hostID ??
        widget.hosts.where((h) => h.savedAuth).firstOrNull?.id ??
        '';
    proxyMode = s?.proxyMode ?? 'path';
    pageMode = s?.pageMode ?? 'html';
    skipTLS = s?.skipTLSVerify ?? false;
  }

  @override
  void dispose() {
    for (final c in [name, target, upstream, port]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _save() async {
    if (!form.currentState!.validate()) return;
    setState(() {
      busy = true;
      error = '';
    });
    try {
      final value = {
        'hostID': host,
        'name': name.text.trim(),
        'targetURL': target.text.trim(),
        'upstreamHost': upstream.text.trim(),
        'proxyMode': proxyMode,
        'pageMode': pageMode,
        'listenPort': proxyMode == 'host_port' ? int.parse(port.text) : 0,
        'skipTLSVerify': skipTLS,
      };
      final saved = widget.demo
          ? RemoteWebService.fromJson({
              ...value,
              'id': widget.service?.id ?? const Uuid().v4(),
            })
          : await widget.api!.saveWebService(value, id: widget.service?.id);
      if (mounted) Navigator.pop(context, saved);
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      toolbarHeight: 52,
      title: Text(
        widget.service == null ? '添加反向代理' : '编辑反向代理',
        style: const TextStyle(fontSize: 16),
      ),
    ),
    body: SafeArea(
      top: false,
      child: Form(
        key: form,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            TextFormField(
              controller: name,
              decoration: const InputDecoration(labelText: '服务名称'),
              validator: (v) =>
                  (v ?? '').trim().isEmpty || (v ?? '').runes.length > 100
                  ? '名称需为 1 到 100 个字符'
                  : null,
            ),
            const SizedBox(height: 16),
            DropdownButtonFormField<String>(
              initialValue: host,
              isExpanded: true,
              decoration: const InputDecoration(labelText: 'SSH 代理主机'),
              items: [
                const DropdownMenuItem(
                  value: '',
                  child: Text('请选择已保存密码或凭据的主机'),
                ),
                for (final h in widget.hosts)
                  DropdownMenuItem(
                    value: h.id,
                    enabled: h.savedAuth || widget.demo,
                    child: Text(
                      '${h.name}${h.savedAuth || widget.demo ? '' : '（需先保存认证）'}',
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
              ],
              validator: (v) => (v ?? '').isEmpty ? '请选择代理主机' : null,
              onChanged: (v) => host = v!,
            ),
            const SizedBox(height: 16),
            TextFormField(
              controller: target,
              autocorrect: false,
              enableSuggestions: false,
              decoration: const InputDecoration(
                labelText: '目标地址',
                hintText: 'http://127.0.0.1:5173',
              ),
              validator: (v) {
                final uri = Uri.tryParse((v ?? '').trim());
                return uri == null ||
                        !['http', 'https'].contains(uri.scheme) ||
                        uri.host.isEmpty ||
                        uri.userInfo.isNotEmpty ||
                        uri.hasFragment
                    ? '请填写有效的 HTTP / HTTPS 地址'
                    : null;
              },
            ),
            const Padding(
              padding: EdgeInsets.only(top: 8, bottom: 12),
              child: Text(
                '目标地址由 SSH 主机访问，127.0.0.1 指该主机本身。',
                style: TextStyle(fontSize: 12, color: V.muted),
              ),
            ),
            ExpansionTile(
              tilePadding: EdgeInsets.zero,
              title: const Text('高级设置', style: TextStyle(fontSize: 14)),
              subtitle: const Text(
                '网页类型、代理模式、上游 Host、证书',
                style: TextStyle(fontSize: 11, color: V.muted),
              ),
              children: [
                DropdownButtonFormField<String>(
                  initialValue: pageMode,
                  decoration: const InputDecoration(labelText: '网页类型'),
                  items: const [
                    DropdownMenuItem(value: 'html', child: Text('普通网页')),
                    DropdownMenuItem(value: 'vite', child: Text('Vite 开发预览')),
                  ],
                  onChanged: (v) => pageMode = v!,
                ),
                const SizedBox(height: 16),
                DropdownButtonFormField<String>(
                  initialValue: proxyMode,
                  decoration: const InputDecoration(labelText: '代理模式'),
                  items: const [
                    DropdownMenuItem(value: 'path', child: Text('当前服务路径（推荐）')),
                    DropdownMenuItem(value: 'host_port', child: Text('独立监听端口')),
                  ],
                  onChanged: (v) => setState(() => proxyMode = v!),
                ),
                if (proxyMode == 'host_port') ...[
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: port,
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(labelText: 'Velin 监听端口'),
                    validator: (v) {
                      final p = int.tryParse(v ?? '');
                      return p == null || p < 1 || p > 65535 ? '请填写有效端口' : null;
                    },
                  ),
                  const Padding(
                    padding: EdgeInsets.only(top: 8),
                    child: Text(
                      '手机需能访问该端口；容器部署还需映射该端口。',
                      style: TextStyle(fontSize: 12, color: V.muted),
                    ),
                  ),
                ],
                const SizedBox(height: 16),
                TextFormField(
                  controller: upstream,
                  decoration: const InputDecoration(labelText: '上游 Host（可选）'),
                ),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text(
                    '允许自签 HTTPS 证书',
                    style: TextStyle(fontSize: 14),
                  ),
                  value: skipTLS,
                  onChanged: (v) => setState(() => skipTLS = v),
                ),
              ],
            ),
            if (error.isNotEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 12),
                child: Text(error, style: const TextStyle(color: V.danger)),
              ),
            const SizedBox(height: 16),
            FilledButton(
              onPressed: busy ? null : _save,
              child: Text(busy ? '正在保存…' : '保存代理'),
            ),
          ],
        ),
      ),
    ),
  );
}
