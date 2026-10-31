import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../core/models.dart';
import '../core/remote_api.dart';
import '../core/service_profiles.dart';
import 'theme.dart';
import 'auth_layout.dart';
import 'service_profiles_screen.dart';

class LoginScreen extends StatefulWidget {
  final SharedPreferences preferences;
  final bool demo;
  final Future<void> Function(RemoteApi?, Map<String, dynamic>) onLoggedIn;
  const LoginScreen({
    super.key,
    required this.preferences,
    required this.demo,
    required this.onLoggedIn,
  });
  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final server = TextEditingController(),
      account = TextEditingController(),
      password = TextEditingController(),
      captcha = TextEditingController(),
      totp = TextEditingController();
  bool busy = false, showPassword = false, needsTotp = false;
  String error = '', captchaID = '', captchaImage = '';
  RemoteApi? api;
  bool transferred = false;
  @override
  void initState() {
    super.initState();
    server.text = widget.preferences.getString('mobile.server') ?? '';
    account.text = widget.preferences.getString('mobile.account') ?? '';
  }

  @override
  void dispose() {
    for (final c in [server, account, password, captcha, totp]) {
      c.dispose();
    }
    if (!transferred) api?.close();
    super.dispose();
  }

  Future<void> _captcha() async {
    try {
      final data = await api!.request(
        '/api/auth/captcha',
        query: {'username': account.text.trim()},
      );
      if (mounted) {
        setState(() {
          captchaID = data['id'];
          captchaImage = data['image'];
          captcha.clear();
        });
      }
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    }
  }

  Future<void> _chooseProfile() async {
    final profile = await Navigator.push<ServiceProfile>(
      context,
      MaterialPageRoute(
        builder: (_) => ServiceProfilesScreen(preferences: widget.preferences),
      ),
    );
    if (!mounted) return;
    if (profile != null) {
      api?.close();
      api = null;
      server.text = profile.server;
      account.text = profile.account;
      password.clear();
      totp.clear();
      captcha.clear();
      captchaID = '';
      captchaImage = '';
      needsTotp = false;
      error = '';
    }
    setState(() {});
  }

  Future<void> _login() async {
    if (busy) return;
    FocusManager.instance.primaryFocus?.unfocus();
    if (widget.demo) {
      await widget.onLoggedIn(null, {'username': '开发者'});
      return;
    }
    setState(() {
      busy = true;
      error = '';
    });
    try {
      final uri = serviceURI(server.text);
      final name = account.text.trim();
      if (name.isEmpty) {
        throw const FormatException('请填写用户名');
      }
      if (api == null || api!.base != uri || api!.account != name) {
        api?.close();
        api = RemoteApi(uri, name);
        captchaID = '';
        captchaImage = '';
      }
      Map<String, dynamic> user;
      if (password.text.isEmpty) {
        await api!.restoreCookies();
        try {
          user = await api!.me();
        } on ApiFailure {
          throw const FormatException('请填写密码，保存的登录状态已失效');
        }
      } else {
        user = await api!.login(
          password.text,
          captchaID: captchaID,
          captcha: captcha.text.trim(),
          totp: totp.text.trim(),
        );
      }
      if (user['forcePasswordChange'] != true &&
          user['sessionLocked'] != true) {
        await api!.checkCapabilities();
      }
      transferred = true;
      await widget.onLoggedIn(api, user);
    } on ApiFailure catch (e) {
      if (mounted) {
        setState(() {
          error = e.message;
          needsTotp =
              needsTotp ||
              e.code == 'totp_required' ||
              e.code == 'invalid_totp';
        });
      }
      if (e.details['captchaRequired'] == true) await _captcha();
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => AuthPage(
    title: '登录工作台',
    subtitle: '连接你的服务，继续手边的工作。',
    demo: widget.demo,
    footer: const Row(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        Icon(Icons.shield_outlined, size: 14, color: V.muted),
        SizedBox(width: 6),
        Flexible(
          child: Text(
            '使用服务端账户登录',
            style: TextStyle(color: V.muted, fontSize: 12),
          ),
        ),
      ],
    ),
    child: AutofillGroup(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const Text('服务', style: TextStyle(fontWeight: FontWeight.w500)),
              const Spacer(),
              TextButton.icon(
                onPressed: busy ? null : _chooseProfile,
                icon: const Icon(Icons.dns_outlined, size: 16),
                label: const Text('切换 / 管理'),
                style: TextButton.styleFrom(
                  foregroundColor: V.accent,
                  textStyle: const TextStyle(fontSize: 12),
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                  minimumSize: const Size(48, 40),
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          TextField(
            controller: server,
            enabled: !busy,
            keyboardType: TextInputType.url,
            textInputAction: TextInputAction.next,
            autocorrect: false,
            decoration: const InputDecoration(
              labelText: '服务地址',
              hintText: 'https://ssh.example.com',
              prefixIcon: Icon(Icons.link_rounded, size: 19),
            ),
          ),
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 20),
            child: Divider(),
          ),
          TextField(
            controller: account,
            enabled: !busy,
            autofillHints: const [AutofillHints.username],
            textInputAction: TextInputAction.next,
            autocorrect: false,
            decoration: const InputDecoration(
              labelText: '账户',
              hintText: '用户名',
              prefixIcon: Icon(Icons.person_outline_rounded, size: 19),
            ),
          ),
          const SizedBox(height: 16),
          TextField(
            controller: password,
            enabled: !busy,
            autofillHints: const [AutofillHints.password],
            textInputAction: captchaImage.isNotEmpty || needsTotp
                ? TextInputAction.next
                : TextInputAction.done,
            onSubmitted: (_) {
              if (captchaImage.isEmpty && !needsTotp) _login();
            },
            obscureText: !showPassword,
            autocorrect: false,
            enableSuggestions: false,
            decoration: InputDecoration(
              labelText: '密码',
              prefixIcon: const Icon(Icons.lock_outline_rounded, size: 19),
              suffixIcon: IconButton(
                tooltip: showPassword ? '隐藏密码' : '显示密码',
                onPressed: () => setState(() => showPassword = !showPassword),
                icon: Icon(
                  showPassword
                      ? Icons.visibility_off_outlined
                      : Icons.visibility_outlined,
                  size: 19,
                ),
              ),
            ),
          ),
          if (captchaImage.isNotEmpty) ...[
            const SizedBox(height: 16),
            LayoutBuilder(
              builder: (context, constraints) => Wrap(
                spacing: 8,
                runSpacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  SizedBox(
                    width: constraints.maxWidth < 280
                        ? constraints.maxWidth
                        : constraints.maxWidth - 138,
                    child: TextField(
                      controller: captcha,
                      enabled: !busy,
                      textInputAction: needsTotp
                          ? TextInputAction.next
                          : TextInputAction.done,
                      onSubmitted: (_) {
                        if (!needsTotp) _login();
                      },
                      decoration: const InputDecoration(labelText: '图形验证码'),
                    ),
                  ),
                  InkWell(
                    onTap: busy ? null : _captcha,
                    child: Semantics(
                      label: '点击刷新验证码',
                      button: true,
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(8),
                        child: SvgPicture.string(
                          captchaImage,
                          width: 130,
                          height: 48,
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
          if (needsTotp) ...[
            const SizedBox(height: 16),
            TextField(
              controller: totp,
              enabled: !busy,
              textInputAction: TextInputAction.done,
              onSubmitted: (_) => _login(),
              autofillHints: const [AutofillHints.oneTimeCode],
              decoration: const InputDecoration(labelText: '双因素验证码 / 恢复码'),
            ),
          ],
          if (error.isNotEmpty) AuthError(error),
          const SizedBox(height: 24),
          FilledButton(
            onPressed: busy ? null : _login,
            style: FilledButton.styleFrom(
              minimumSize: const Size(48, 50),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                if (busy) ...[
                  const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: V.muted,
                    ),
                  ),
                  const SizedBox(width: 10),
                ],
                Flexible(
                  child: Text(
                    busy
                        ? '正在连接…'
                        : widget.demo
                        ? '进入设计预览'
                        : '登录',
                  ),
                ),
                if (!busy) ...[
                  const SizedBox(width: 8),
                  const Icon(Icons.arrow_forward_rounded, size: 18),
                ],
              ],
            ),
          ),
        ],
      ),
    ),
  );
}
