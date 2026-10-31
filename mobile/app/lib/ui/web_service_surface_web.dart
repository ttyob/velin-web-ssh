import 'package:flutter/material.dart';
import '../core/models.dart';

class WebServiceSurface extends StatelessWidget {
  final WebServiceAccess access;
  final int reload;
  final ValueChanged<String> onError;
  const WebServiceSurface({
    super.key,
    required this.access,
    required this.reload,
    required this.onError,
  });
  @override
  Widget build(BuildContext context) =>
      const Center(child: Text('浏览器构建仅提供示例预览'));
}
