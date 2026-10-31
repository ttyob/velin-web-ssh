import 'package:flutter/material.dart';
import '../core/remote_api.dart';
import 'auth_layout.dart';
import 'theme.dart';

class AccountSetupScreen extends StatefulWidget {
  final RemoteApi? api;
  final Future<void> Function() onDone, onLogout;
  final bool demo;
  const AccountSetupScreen({
    super.key,
    required this.api,
    required this.onDone,
    required this.onLogout,
    this.demo = false,
  });
  @override
  State<AccountSetupScreen> createState() => _AccountSetupScreenState();
}

class _AccountSetupScreenState extends State<AccountSetupScreen> {
  final current = TextEditingController(),
      next = TextEditingController(),
      confirmation = TextEditingController();
  bool busy = false, visible = false;
  String error = '';
  @override
  void dispose() {
    current.dispose();
    next.dispose();
    confirmation.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    if (busy) return;
    if (current.text.isEmpty || next.text.isEmpty) {
      setState(() => error = '请填写当前密码和新密码');
      return;
    }
    if (next.text != confirmation.text) {
      setState(() => error = '两次输入的新密码不一致');
      return;
    }
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() {
      busy = true;
      error = '';
    });
    try {
      if (!widget.demo) {
        await widget.api!.request(
          '/api/auth/password',
          method: 'POST',
          data: {'currentPassword': current.text, 'newPassword': next.text},
        );
      }
      await widget.onDone();
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => AuthPage(
    title: '设置你的密码',
    subtitle: '首次登录，先更新初始密码。',
    demo: widget.demo,
    footer: Center(
      child: TextButton(
        onPressed: busy ? null : widget.onLogout,
        style: TextButton.styleFrom(foregroundColor: V.muted),
        child: const Text('返回登录'),
      ),
    ),
    child: AutofillGroup(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Row(
            children: [
              Icon(Icons.key_outlined, size: 18, color: V.accent),
              SizedBox(width: 8),
              Text('账户安全', style: TextStyle(fontWeight: FontWeight.w500)),
            ],
          ),
          const SizedBox(height: 24),
          TextField(
            controller: current,
            enabled: !busy,
            obscureText: !visible,
            autofillHints: const [AutofillHints.password],
            textInputAction: TextInputAction.next,
            decoration: const InputDecoration(labelText: '当前密码'),
          ),
          const SizedBox(height: 16),
          TextField(
            controller: next,
            enabled: !busy,
            obscureText: !visible,
            autofillHints: const [AutofillHints.newPassword],
            textInputAction: TextInputAction.next,
            decoration: InputDecoration(
              labelText: '新密码',
              suffixIcon: IconButton(
                tooltip: visible ? '隐藏密码' : '显示密码',
                onPressed: () => setState(() => visible = !visible),
                icon: Icon(
                  visible
                      ? Icons.visibility_off_outlined
                      : Icons.visibility_outlined,
                  size: 19,
                ),
              ),
            ),
          ),
          const SizedBox(height: 16),
          TextField(
            controller: confirmation,
            enabled: !busy,
            obscureText: !visible,
            autofillHints: const [AutofillHints.newPassword],
            textInputAction: TextInputAction.done,
            onSubmitted: (_) => _save(),
            decoration: const InputDecoration(labelText: '确认新密码'),
          ),
          if (error.isNotEmpty) AuthError(error),
          const SizedBox(height: 24),
          FilledButton(
            onPressed: busy ? null : _save,
            child: Text(busy ? '正在保存…' : '保存并继续'),
          ),
        ],
      ),
    ),
  );
}
