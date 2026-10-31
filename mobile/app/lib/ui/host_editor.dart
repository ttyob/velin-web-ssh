import 'package:flutter/material.dart';
import 'package:uuid/uuid.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import 'theme.dart';

class HostEditor extends StatefulWidget {
  final RemoteApi? api;
  final RemoteHost? host;
  final List<RemoteHost> hosts;
  final bool demo;
  const HostEditor({
    super.key,
    required this.api,
    this.host,
    required this.hosts,
    this.demo = false,
  });
  @override
  State<HostEditor> createState() => _HostEditorState();
}

class _HostEditorState extends State<HostEditor> {
  final form = GlobalKey<FormState>();
  final fields = <String, TextEditingController>{};
  List<Map<String, dynamic>> credentials = [];
  String auth = 'password',
      credential = '',
      jump = '',
      mode = 'tmux',
      error = '',
      credentialError = '';
  bool busy = false;
  TextEditingController field(String key) => fields[key]!;
  @override
  void initState() {
    super.initState();
    final host = widget.host;
    final initial = <String, String>{
      'name': host?.name ?? '',
      'address': host?.address ?? '',
      'username': host?.username ?? 'root',
      'port': '${host?.port ?? 22}',
      'password': '',
      'initialDirectory': host?.directory ?? '',
      'groupName': host?.config['groupName'] as String? ?? '',
      'notes': host?.config['notes'] as String? ?? '',
    };
    for (final entry in initial.entries) {
      fields[entry.key] = TextEditingController(text: entry.value);
    }
    credential = host?.credentialID ?? '';
    auth = credential.isNotEmpty
        ? 'credential'
        : host == null || host.hasPassword
        ? 'password'
        : 'prompt';
    jump = host?.config['jumpHostID'] as String? ?? '';
    mode = host?.config['sessionMode'] as String? ?? 'tmux';
    _loadCredentials();
  }

  Future<void> _loadCredentials() async {
    try {
      final items = widget.demo
          ? <Map<String, dynamic>>[
              {'id': 'demo-key', 'name': '开发密钥', 'kind': 'key'},
            ]
          : await widget.api!.credentials();
      if (mounted) {
        setState(() {
          credentials = items;
          credentialError = '';
        });
      }
    } catch (e) {
      if (mounted) setState(() => credentialError = '$e');
    }
  }

  @override
  void dispose() {
    for (final c in fields.values) {
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
      final value = <String, dynamic>{
        ...?widget.host?.config,
        'protocol': 'ssh',
        'sessionMode': mode,
        'jumpHostID': jump,
        for (final key in [
          'name',
          'address',
          'username',
          'initialDirectory',
          'groupName',
          'notes',
        ])
          key: field(key).text.trim(),
        'port': int.parse(field('port').text),
        'credentialID': auth == 'credential' ? credential : '',
        'authMode': auth,
        'password': auth == 'password' ? field('password').text : '',
      };
      final saved = widget.demo
          ? RemoteHost.fromJson({
              ...value,
              'id': widget.host?.id ?? const Uuid().v4(),
              'hasPassword': auth == 'password',
            })
          : await widget.api!.saveHost(value, id: widget.host?.id);
      if (mounted) Navigator.pop(context, saved);
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Widget _text(
    String key,
    String label, {
    String? hint,
    bool required = false,
    bool secret = false,
  }) => Padding(
    padding: const EdgeInsets.only(bottom: 14),
    child: TextFormField(
      controller: field(key),
      obscureText: secret,
      autocorrect: false,
      enableSuggestions: false,
      decoration: InputDecoration(labelText: label, hintText: hint),
      validator: (v) {
        if (required && (v ?? '').trim().isEmpty) return '请填写$label';
        if (key == 'name' && (v ?? '').runes.length > 80) return '名称最多 80 个字符';
        if (key == 'password' &&
            auth == 'password' &&
            (v ?? '').isEmpty &&
            widget.host?.hasPassword != true) {
          return '请填写密码';
        }
        return null;
      },
    ),
  );
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      toolbarHeight: 52,
      title: Text(
        widget.host == null ? '添加 SSH 主机' : '编辑 SSH 主机',
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
            if (widget.demo)
              const Padding(
                padding: EdgeInsets.only(bottom: 16),
                child: Text(
                  '示例预览 · 修改仅在本次预览生效',
                  style: TextStyle(color: V.muted, fontSize: 12),
                ),
              ),
            _text('name', '主机名称', hint: '例如：开发服务器', required: true),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: _text(
                    'address',
                    '主机地址',
                    hint: 'IP 或域名',
                    required: true,
                  ),
                ),
                const SizedBox(width: 12),
                SizedBox(
                  width: 92,
                  child: TextFormField(
                    controller: field('port'),
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(labelText: '端口'),
                    validator: (v) {
                      final p = int.tryParse(v ?? '');
                      return p == null || p < 1 || p > 65535 ? '端口无效' : null;
                    },
                  ),
                ),
              ],
            ),
            _text('username', '用户名', required: true),
            DropdownButtonFormField<String>(
              initialValue: auth,
              decoration: const InputDecoration(labelText: '认证方式'),
              items: const [
                DropdownMenuItem(value: 'password', child: Text('保存密码')),
                DropdownMenuItem(value: 'credential', child: Text('已存凭据')),
                DropdownMenuItem(value: 'prompt', child: Text('连接时输入')),
              ],
              onChanged: (v) => setState(() => auth = v!),
            ),
            const SizedBox(height: 14),
            if (auth == 'password')
              _text(
                'password',
                'SSH 密码',
                secret: true,
                hint: widget.host?.hasPassword == true ? '留空保留当前密码' : null,
              ),
            if (auth == 'credential') ...[
              DropdownButtonFormField<String>(
                initialValue: credential,
                isExpanded: true,
                decoration: const InputDecoration(labelText: '选择凭据'),
                items: [
                  const DropdownMenuItem(value: '', child: Text('请选择')),
                  if (credential.isNotEmpty &&
                      !credentials.any((c) => c['id'] == credential))
                    DropdownMenuItem(
                      value: credential,
                      child: const Text('当前绑定凭据'),
                    ),
                  for (final c in credentials)
                    DropdownMenuItem(
                      value: c['id'] as String,
                      child: Text(
                        '${c['name']}',
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                ],
                validator: (v) => v == null || v.isEmpty ? '请选择凭据' : null,
                onChanged: (v) => credential = v!,
              ),
              if (credentialError.isNotEmpty)
                TextButton(
                  onPressed: _loadCredentials,
                  child: Text('凭据加载失败，点击重试：$credentialError'),
                ),
              const SizedBox(height: 14),
            ],
            ExpansionTile(
              tilePadding: EdgeInsets.zero,
              title: const Text('更多设置', style: TextStyle(fontSize: 14)),
              subtitle: const Text(
                '启动目录、会话模式、跳板机、分组',
                style: TextStyle(fontSize: 11, color: V.muted),
              ),
              children: [
                _text('initialDirectory', '启动目录', hint: '/workspace/project'),
                DropdownButtonFormField<String>(
                  initialValue: mode,
                  decoration: const InputDecoration(labelText: '会话模式'),
                  items: const [
                    DropdownMenuItem(value: 'tmux', child: Text('tmux 持久会话')),
                    DropdownMenuItem(value: 'normal', child: Text('普通 SSH')),
                  ],
                  onChanged: (v) => mode = v!,
                ),
                const SizedBox(height: 14),
                DropdownButtonFormField<String>(
                  initialValue: jump,
                  isExpanded: true,
                  decoration: const InputDecoration(labelText: '跳板机'),
                  items: [
                    const DropdownMenuItem(value: '', child: Text('直连')),
                    for (final h in widget.hosts.where(
                      (h) =>
                          h.id != widget.host?.id &&
                          (h.savedAuth || h.id == jump),
                    ))
                      DropdownMenuItem(
                        value: h.id,
                        child: Text(h.name, overflow: TextOverflow.ellipsis),
                      ),
                  ],
                  onChanged: (v) => jump = v!,
                ),
                const SizedBox(height: 14),
                _text('groupName', '分组'),
                _text('notes', '备注'),
              ],
            ),
            if (error.isNotEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 16),
                child: Text(error, style: const TextStyle(color: V.danger)),
              ),
            const SizedBox(height: 16),
            FilledButton(
              onPressed: busy ? null : _save,
              child: Text(busy ? '正在保存…' : '保存主机'),
            ),
            const SizedBox(height: 24),
          ],
        ),
      ),
    ),
  );
}
