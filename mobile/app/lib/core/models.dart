class ApiFailure implements Exception {
  final int status;
  final String code, message;
  final Map<String, dynamic> details;
  ApiFailure(this.status, this.code, this.message, [this.details = const {}]);
  @override
  String toString() => message;
}

class RemoteSession {
  final String id, hostID, name, mode, status, error;
  final DateTime? createdAt;
  const RemoteSession({
    required this.id,
    required this.hostID,
    required this.name,
    this.mode = 'tmux',
    this.status = 'attached',
    this.error = '',
    this.createdAt,
  });
  factory RemoteSession.fromJson(Map<String, dynamic> j) => RemoteSession(
    id: j['id'] as String,
    hostID: j['hostID'] as String,
    name: j['name'] as String? ?? '未命名会话',
    mode: j['sessionMode'] as String? ?? 'tmux',
    status: j['status'] as String? ?? 'unknown',
    error: j['lastError'] as String? ?? '',
    createdAt: DateTime.tryParse(j['createdAt'] as String? ?? '')?.toLocal(),
  );
  bool get ended => status == 'ended';
  String get shortID {
    final value = id.replaceAll('-', '');
    return value.length <= 8 ? value : value.substring(value.length - 8);
  }

  String displayName(RemoteHost? host, {bool duplicate = false}) =>
      duplicate || name.trim().isEmpty || name == host?.name
      ? '${name.trim().isEmpty ? host?.name ?? '会话' : name} · $shortID'
      : name;
  String get createdLabel {
    final time = createdAt;
    if (time == null) return '#$shortID';
    String pad(int n) => '$n'.padLeft(2, '0');
    return '${pad(time.month)}/${pad(time.day)} ${pad(time.hour)}:${pad(time.minute)}';
  }
}

class RemoteHost {
  final String id, name, address, username, directory, protocol;
  final Map<String, dynamic> config;
  const RemoteHost({
    required this.id,
    required this.name,
    required this.address,
    this.username = '',
    this.directory = '',
    this.protocol = 'ssh',
    this.config = const {},
  });
  factory RemoteHost.fromJson(Map<String, dynamic> j) => RemoteHost(
    id: j['id'] as String,
    name: j['name'] as String? ?? '',
    address: j['address'] as String? ?? '',
    username: j['username'] as String? ?? '',
    directory: j['initialDirectory'] as String? ?? '',
    protocol: j['protocol'] as String? ?? 'ssh',
    config: Map.unmodifiable(j),
  );
  int get port => (config['port'] as num?)?.toInt() ?? 22;
  String get credentialID => config['credentialID'] as String? ?? '';
  bool get hasPassword => config['hasPassword'] == true;
  bool get savedAuth => credentialID.isNotEmpty || hasPassword;
}

class RemoteWebService {
  final String id, hostID, name, targetURL, proxyMode, pageMode, upstreamHost;
  final int listenPort;
  final bool skipTLSVerify;
  const RemoteWebService({
    required this.id,
    required this.hostID,
    required this.name,
    required this.targetURL,
    this.proxyMode = 'path',
    this.pageMode = 'html',
    this.upstreamHost = '',
    this.listenPort = 18080,
    this.skipTLSVerify = false,
  });
  factory RemoteWebService.fromJson(Map<String, dynamic> j) => RemoteWebService(
    id: j['id'] as String,
    hostID: j['hostID'] as String,
    name: j['name'] as String,
    targetURL: j['targetURL'] as String,
    proxyMode: j['proxyMode'] as String? ?? 'path',
    pageMode: j['pageMode'] as String? ?? 'html',
    upstreamHost: j['upstreamHost'] as String? ?? '',
    listenPort: (j['listenPort'] as num?)?.toInt() ?? 18080,
    skipTLSVerify: j['skipTLSVerify'] == true,
  );
}

class WebServiceAccess {
  final Uri url;
  final String cookieName, cookieValue, cookiePath;
  const WebServiceAccess(
    this.url, {
    this.cookieName = '',
    this.cookieValue = '',
    this.cookiePath = '/',
  });
}

String defaultSessionName(RemoteHost host, [DateTime? time]) {
  final at = time ?? DateTime.now();
  String pad(int n) => '$n'.padLeft(2, '0');
  final base = String.fromCharCodes(host.name.runes.take(50));
  return '$base · ${pad(at.month)}${pad(at.day)}-${pad(at.hour)}${pad(at.minute)}${pad(at.second)}';
}

Uri serviceURI(String value) {
  final uri = Uri.tryParse(value.trim());
  if (uri == null ||
      !['http', 'https'].contains(uri.scheme) ||
      uri.host.isEmpty ||
      uri.userInfo.isNotEmpty ||
      uri.hasQuery ||
      uri.hasFragment) {
    throw const FormatException('请输入完整的 HTTP 或 HTTPS 服务地址');
  }
  return uri.replace(path: uri.path.endsWith('/') ? uri.path : '${uri.path}/');
}

String shellQuote(String text) => "'${text.replaceAll("'", "'\\''")}'";

final demoSessions = [
  RemoteSession(id: 'demo-api', hostID: 'demo-host', name: 'Velin · 移动端开发'),
  RemoteSession(id: 'demo-ui', hostID: 'demo-host', name: '控制台界面优化'),
  RemoteSession(id: 'demo-docs', hostID: 'demo-host', name: '文档与发布'),
  RemoteSession(id: 'demo-integration', hostID: 'demo-host', name: 'API 联调'),
  RemoteSession(
    id: 'da210431-1c87-4b00-a721-b53f0a2e0401',
    hostID: 'demo-host',
    name: '开发服务器',
    createdAt: DateTime(2026, 10, 31, 14, 20),
  ),
  RemoteSession(
    id: 'da210431-1c87-4b00-a721-b53f0a2e0402',
    hostID: 'demo-host',
    name: '开发服务器',
    status: 'background',
    createdAt: DateTime(2026, 10, 31, 15, 10),
  ),
];
const demoHosts = [
  RemoteHost(
    id: 'demo-host',
    name: '开发服务器',
    address: '192.168.1.10',
    username: 'dev',
    directory: '/workspace/velin',
    config: {'hasPassword': true, 'port': 22, 'sessionMode': 'tmux'},
  ),
];
