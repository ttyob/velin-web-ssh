import 'dart:async';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';
import 'core/models.dart';
import 'core/remote_api.dart';
import 'core/service_profiles.dart';
import 'ui/home_screen.dart';
import 'ui/login_screen.dart';
import 'ui/theme.dart';
import 'ui/account_setup_screen.dart';
import 'ui/session_lock.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  SystemChrome.setSystemUIOverlayStyle(
    const SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      statusBarIconBrightness: Brightness.light,
      systemNavigationBarColor: V.bg,
    ),
  );
  final preferences = await SharedPreferences.getInstance();
  runApp(VelinApp(preferences: preferences));
}

class VelinApp extends StatefulWidget {
  final SharedPreferences preferences;
  const VelinApp({super.key, required this.preferences});
  @override
  State<VelinApp> createState() => _VelinAppState();
}

class _VelinAppState extends State<VelinApp> {
  final demo = kIsWeb || const bool.fromEnvironment('VELIN_DEMO');
  RemoteApi? api;
  Map<String, dynamic>? user;
  bool loading = true, workspaceReady = false;
  bool _handlingAccess = false;
  final navigation = GlobalKey<NavigatorState>();
  final lockNavigation = LockNavigationObserver();
  String device = '';
  @override
  void initState() {
    super.initState();
    _restore();
  }

  Future<void> _restore() async {
    device = widget.preferences.getString('mobile.device') ?? const Uuid().v4();
    await widget.preferences.setString('mobile.device', device);
    if (demo) {
      final screen = Uri.base.queryParameters['screen'];
      user = screen == 'login'
          ? null
          : {
              'username': '开发者',
              if (screen == 'lock') 'sessionLocked': true,
              if (screen == 'setup') 'forcePasswordChange': true,
            };
    } else {
      final server = widget.preferences.getString('mobile.server'),
          account = widget.preferences.getString('mobile.account');
      if (server != null && account != null) {
        try {
          api = RemoteApi(serviceURI(server), account);
          await api!.restoreCookies();
          user = await api!.me();
          _watchAccess(api!);
        } catch (_) {
          api?.close();
          api = null;
        }
      }
    }
    if (mounted) {
      setState(() {
        loading = false;
        workspaceReady = _canOpenWorkspace(user);
      });
    }
  }

  Future<void> _loggedIn(RemoteApi? next, Map<String, dynamic> value) async {
    if (next != null) {
      _watchAccess(next);
      await widget.preferences.setString('mobile.server', '${next.base}');
      await widget.preferences.setString('mobile.account', next.account);
      await ServiceProfiles(
        widget.preferences,
      ).remember(ServiceProfile('${next.base}', next.account));
    }
    if (mounted) {
      setState(() {
        api = next;
        user = value;
        workspaceReady = _canOpenWorkspace(value);
      });
    }
  }

  void _watchAccess(RemoteApi next) {
    next.onAccessRequired = (code) {
      unawaited(_recoverAccess(next, code));
    };
  }

  Future<void> _recoverAccess(RemoteApi source, String code) async {
    if (!mounted || api != source || _handlingAccess) return;
    _handlingAccess = true;
    if (code == 'session_locked' && user != null) {
      FocusManager.instance.primaryFocus?.unfocus();
      setState(() => user = {...user!, 'sessionLocked': true});
    }
    try {
      if (code == 'unauthorized') {
        navigation.currentState?.popUntil((route) => route.isFirst);
        await source.forgetAuthentication();
        if (mounted && api == source) {
          setState(() {
            user = null;
            api = null;
            workspaceReady = false;
          });
        }
        source.close();
      } else {
        if (code != 'session_locked') {
          navigation.currentState?.popUntil((route) => route.isFirst);
        }
        final value = await source.me();
        if (mounted && api == source) setState(() => user = value);
      }
    } catch (_) {
      // A network failure must not remove a lock already reported by the server.
      if (code == 'session_locked') return;
      navigation.currentState?.popUntil((route) => route.isFirst);
      if (mounted && api == source) {
        setState(() {
          user = null;
          api = null;
          workspaceReady = false;
        });
      }
      source.close();
    } finally {
      _handlingAccess = false;
    }
  }

  Future<void> _logout() async {
    navigation.currentState?.popUntil((route) => route.isFirst);
    try {
      await api?.logout();
    } catch (_) {}
    api?.close();
    if (mounted) {
      setState(() {
        api = null;
        user = null;
        workspaceReady = false;
      });
    }
  }

  Future<void> _switchService() async {
    navigation.currentState?.popUntil((route) => route.isFirst);
    api?.close();
    if (mounted) {
      setState(() {
        api = null;
        user = null;
        workspaceReady = false;
      });
    }
  }

  @override
  void dispose() {
    lockNavigation.dispose();
    api?.close();
    super.dispose();
  }

  bool _canOpenWorkspace(Map<String, dynamic>? value) =>
      value != null &&
      value['forcePasswordChange'] != true &&
      value['sessionLocked'] != true;

  Future<void> _accountUpdated() async {
    if (demo) {
      setState(() {
        user = {...user!, 'forcePasswordChange': false, 'sessionLocked': false};
        workspaceReady = true;
      });
      return;
    }
    final source = api!;
    final next = await source.me();
    if (_canOpenWorkspace(next)) await source.checkCapabilities();
    if (mounted && api == source) {
      setState(() {
        user = next;
        workspaceReady = _canOpenWorkspace(next);
      });
    }
  }

  Future<void> _lock() async {
    if (!demo) {
      await api!.request(
        '/api/auth/lock',
        method: 'POST',
        data: {'reason': 'manual'},
      );
    }
    if (!mounted || user == null) return;
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() => user = {...user!, 'sessionLocked': true});
  }

  Future<void> _unlock(String pin) async {
    if (demo) {
      if (pin != '123456') throw const FormatException('示例 PIN 为 123456');
    } else {
      await api!.request(
        '/api/auth/unlock',
        method: 'POST',
        data: {'pin': pin},
      );
    }
    await _accountUpdated();
  }

  @override
  Widget build(BuildContext context) {
    final locked =
        user?['sessionLocked'] == true && user?['forcePasswordChange'] != true;
    lockNavigation.locked = locked;
    return MaterialApp(
      navigatorKey: navigation,
      navigatorObservers: [lockNavigation],
      title: 'Velin Mobile',
      debugShowCheckedModeBanner: false,
      theme: V.theme(),
      builder: (context, child) => WorkspaceShield(
        locked: locked,
        cover: locked
            ? SessionLock(
                account: user?['username'] as String? ?? '',
                onUnlock: _unlock,
                navigationGuard: lockNavigation,
                onLogout: _logout,
                demo: demo,
              )
            : null,
        child: child!,
      ),
      home: loading
          ? const Scaffold(body: Center(child: CircularProgressIndicator()))
          : user == null
          ? LoginScreen(
              preferences: widget.preferences,
              demo: demo,
              onLoggedIn: _loggedIn,
            )
          : user!['forcePasswordChange'] == true
          ? AccountSetupScreen(
              api: api,
              onDone: _accountUpdated,
              onLogout: _logout,
              demo: demo,
            )
          : workspaceReady
          ? HomeScreen(
              api: api,
              user: user!,
              device: device,
              demo: demo,
              preferences: widget.preferences,
              onLogout: _logout,
              onServiceSwitch: _switchService,
              onLock: _lock,
            )
          : const Scaffold(),
    );
  }
}
