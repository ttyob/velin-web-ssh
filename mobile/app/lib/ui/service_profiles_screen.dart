import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import '../core/service_profiles.dart';
import 'theme.dart';

class ServiceProfilesScreen extends StatefulWidget {
  final SharedPreferences preferences;
  final ServiceProfile? current;
  const ServiceProfilesScreen({
    super.key,
    required this.preferences,
    this.current,
  });
  @override
  State<ServiceProfilesScreen> createState() => _ServiceProfilesScreenState();
}

class _ServiceProfilesScreenState extends State<ServiceProfilesScreen> {
  late final store = ServiceProfiles(widget.preferences);
  bool _isCurrent(ServiceProfile p) =>
      p.server == widget.current?.server &&
      p.account == widget.current?.account;
  Future<void> _edit([ServiceProfile? previous]) async {
    final name = TextEditingController(text: previous?.name ?? ''),
        server = TextEditingController(text: previous?.server ?? ''),
        account = TextEditingController(text: previous?.account ?? '');
    final form = GlobalKey<FormState>();
    final next = await showDialog<ServiceProfile>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(previous == null ? '添加 Velin 服务' : '编辑 Velin 服务'),
        content: SizedBox(
          width: 380,
          child: Form(
            key: form,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextFormField(
                    controller: name,
                    decoration: const InputDecoration(labelText: '备注名称（可选）'),
                  ),
                  const SizedBox(height: 14),
                  TextFormField(
                    controller: server,
                    autocorrect: false,
                    enableSuggestions: false,
                    decoration: const InputDecoration(
                      labelText: '服务地址',
                      hintText: 'https://ssh.example.com',
                    ),
                    validator: (v) {
                      try {
                        serviceURI(v ?? '');
                        return null;
                      } catch (_) {
                        return '请填写完整 HTTP / HTTPS 地址';
                      }
                    },
                  ),
                  const SizedBox(height: 14),
                  TextFormField(
                    controller: account,
                    autocorrect: false,
                    decoration: const InputDecoration(labelText: '账户'),
                    validator: (v) => (v ?? '').trim().isEmpty ? '请填写账户' : null,
                  ),
                  const SizedBox(height: 12),
                  const Text(
                    '地址或账户变更后需重新登录。',
                    style: TextStyle(fontSize: 12, color: V.muted),
                  ),
                ],
              ),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () {
              if (form.currentState!.validate()) {
                Navigator.pop(
                  c,
                  ServiceProfile(
                    '${serviceURI(server.text)}',
                    account.text.trim(),
                    name: name.text.trim(),
                  ),
                );
              }
            },
            child: const Text('保存'),
          ),
        ],
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 300));
    for (final c in [name, server, account]) {
      c.dispose();
    }
    if (next == null || !mounted) return;
    if (previous != null) {
      await store.replace(previous, next);
      if (_isCurrent(previous) &&
          (previous.server != next.server ||
              previous.account != next.account)) {
        if (mounted) Navigator.pop(context, next);
        return;
      }
    } else {
      await store.remember(next);
    }
    if (mounted) setState(() {});
  }

  Future<void> _delete(ServiceProfile profile) async {
    if (!await confirm(
      context,
      '删除服务档案？',
      '清除此服务和账户在手机上的认证、草稿、收藏和最近记录。',
      action: '删除',
    )) {
      return;
    }
    final api = RemoteApi(serviceURI(profile.server), profile.account);
    try {
      await api.forgetAuthentication();
      await store.remove(profile);
      if (!mounted) return;
      if (_isCurrent(profile)) {
        Navigator.pop(context, const ServiceProfile('', ''));
      } else {
        setState(() {});
      }
    } catch (e) {
      if (mounted) notice(context, e);
    } finally {
      api.close();
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      toolbarHeight: 52,
      title: const Text('Velin 服务', style: TextStyle(fontSize: 16)),
      actions: [
        IconButton(
          tooltip: '添加 Velin 服务',
          onPressed: _edit,
          icon: const Icon(Icons.add),
        ),
      ],
    ),
    body: SafeArea(
      top: false,
      child: ListView(
        children: [
          if (store.all.isEmpty)
            const Padding(
              padding: EdgeInsets.all(24),
              child: Text(
                '添加服务地址后，可选择该服务登录。',
                style: TextStyle(color: V.muted),
              ),
            ),
          for (final p in store.all) ...[
            ListTile(
              minTileHeight: 80,
              leading: Icon(
                _isCurrent(p) ? Icons.check_circle_outline : Icons.dns_outlined,
                color: V.accent,
              ),
              title: Text(
                p.name.isEmpty ? p.account : p.name,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(fontSize: 14),
              ),
              subtitle: Text(
                '${p.account} · ${p.server}',
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(fontSize: 11, color: V.muted),
              ),
              onTap: () => Navigator.pop(context, p),
              trailing: PopupMenuButton<String>(
                tooltip: '服务操作',
                onSelected: (v) {
                  if (v == 'edit') _edit(p);
                  if (v == 'delete') _delete(p);
                },
                itemBuilder: (_) => [
                  const PopupMenuItem(value: 'edit', child: Text('编辑')),
                  const PopupMenuItem(value: 'delete', child: Text('删除')),
                ],
              ),
            ),
            const Divider(indent: 64),
          ],
        ],
      ),
    ),
  );
}
