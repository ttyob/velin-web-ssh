import 'package:flutter/material.dart';
import '../core/models.dart';
import '../core/remote_api.dart';

class TerminalToolScreen extends StatelessWidget {
  final RemoteApi api;
  final RemoteHost host;
  final RemoteSession session;
  final String tool;
  const TerminalToolScreen({
    super.key,
    required this.api,
    required this.host,
    required this.session,
    required this.tool,
  });
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('终端工具')),
    body: const Center(child: Text('工具连接请使用安卓客户端')),
  );
}
