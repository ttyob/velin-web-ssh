import 'package:flutter/material.dart';
import 'theme.dart';

class UploadLocationSheet extends StatefulWidget {
  final String directory;
  final List<String> files;
  final bool demo;
  const UploadLocationSheet({
    super.key,
    required this.directory,
    required this.files,
    this.demo = false,
  });

  @override
  State<UploadLocationSheet> createState() => _UploadLocationSheetState();
}

class _UploadLocationSheetState extends State<UploadLocationSheet> {
  late final TextEditingController directory;
  final form = GlobalKey<FormState>();

  @override
  void initState() {
    super.initState();
    directory = TextEditingController(text: widget.directory);
  }

  @override
  void dispose() {
    directory.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => SafeArea(
    child: SingleChildScrollView(
      padding: EdgeInsets.fromLTRB(
        24,
        0,
        24,
        24 + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: Form(
        key: form,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text(
              '添加附件',
              style: TextStyle(fontSize: 22, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 8),
            Text(
              widget.demo ? '示例附件 · 不会读取或上传本地文件' : '上传到当前会话的项目，再将路径加入输入草稿。',
              style: const TextStyle(fontSize: 13, color: V.muted),
            ),
            const SizedBox(height: 20),
            Container(
              constraints: const BoxConstraints(maxHeight: 160),
              decoration: BoxDecoration(
                color: V.bg,
                borderRadius: BorderRadius.circular(12),
              ),
              child: ListView.separated(
                shrinkWrap: true,
                itemCount: widget.files.length,
                separatorBuilder: (_, _) => const Divider(indent: 48),
                itemBuilder: (_, index) => ListTile(
                  dense: true,
                  leading: const Icon(
                    Icons.insert_drive_file_outlined,
                    size: 22,
                    color: V.accent,
                  ),
                  title: Text(
                    widget.files[index],
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ),
            ),
            const SizedBox(height: 22),
            TextFormField(
              controller: directory,
              autocorrect: false,
              enableSuggestions: false,
              decoration: const InputDecoration(
                labelText: '远端项目目录',
                hintText: '/workspace/my-project',
              ),
              validator: (value) {
                final path = (value ?? '').trim().replaceFirst(
                  RegExp(r'/+$'),
                  '',
                );
                if (!path.startsWith('/') ||
                    path.split('/').contains('..') ||
                    RegExp(r'[\x00-\x1f\x7f]').hasMatch(path)) {
                  return '请填写有效的项目绝对目录，不使用根目录';
                }
                return null;
              },
            ),
            const SizedBox(height: 12),
            const Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(Icons.folder_outlined, size: 18, color: V.muted),
                SizedBox(width: 8),
                Expanded(
                  child: Text(
                    '文件保存在项目的 clipboard/ 目录中。上传后仍需发送草稿，附件才会交给终端中的工具。',
                    style: TextStyle(fontSize: 12, color: V.muted, height: 1.6),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 24),
            SizedBox(
              width: double.infinity,
              child: FilledButton.icon(
                onPressed: () {
                  if (form.currentState!.validate()) {
                    Navigator.pop(
                      context,
                      directory.text.trim().replaceFirst(RegExp(r'/+$'), ''),
                    );
                  }
                },
                icon: Icon(
                  widget.demo ? Icons.add_rounded : Icons.upload_rounded,
                  size: 19,
                ),
                label: Text(widget.demo ? '加入示例引用' : '上传附件'),
              ),
            ),
          ],
        ),
      ),
    ),
  );
}
