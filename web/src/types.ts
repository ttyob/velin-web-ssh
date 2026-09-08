export interface User {
  id: string;
  username: string;
  role: "admin" | "user";
  disabled: boolean;
  forcePasswordChange?: boolean;
  sessionLocked?: boolean;
  createdAt?: string;
}
export interface LoginDevice {
  id: string;
  userAgent: string;
  ip: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  current: boolean;
}
export interface SecurityPolicy {
  passwordMinLength: number;
  loginFailureThreshold: number;
  lockMinutes: number;
  rememberDays: number;
  forceChangeOnCreate: boolean;
}
export interface AIModelConfig {
  baseURL: string;
  model: string;
  apiKeyConfigured: boolean;
  configured: boolean;
}
export interface TailscaleConfig {
  enabled: boolean;
  hostname: string;
  controlURL: string;
  authKeyConfigured: boolean;
  status: TailscaleStatus;
}
export interface TailscaleStatus {
  enabled: boolean;
  state: string;
  tun: boolean;
  ips?: string[];
  magicDnsSuffix?: string;
  version?: string;
  authUrl?: string;
  health?: string[];
}
export interface UpdateCheck {
  currentVersion: string;
  latestVersion: string;
  versionKnown: boolean;
  updateAvailable: boolean;
  releaseURL: string;
  releaseName: string;
  publishedAt: string;
  checkedAt: string;
}
export interface Snippet {
  id: string;
  userID?: string;
  name: string;
  groupName: string;
  tags: string;
  command: string;
  description: string;
  createdAt?: string;
  updatedAt?: string;
}
export interface PortForward {
  id: string;
  hostID: string;
  name: string;
  kind: "local" | "remote" | "dynamic";
  listenAddress: string;
  listenPort: number;
  targetHost: string;
  targetPort: number;
  status: string;
  lastError: string;
  bytesIn: number;
  bytesOut: number;
}
export interface WebService {
  id: string;
  hostID: string;
  name: string;
  proxyMode: "path" | "host_port";
  pageMode: "html" | "vite";
  listenPort: number;
  targetURL: string;
  upstreamHost: string;
  skipTLSVerify: boolean;
  createdAt?: string;
  updatedAt?: string;
}
export interface Host {
  id: string;
  userID?: string;
  name: string;
  address: string;
  protocol: "ssh" | "vnc" | "rdp";
  port: number;
  username: string;
  credentialID: string;
  hasPassword?: boolean;
  groupName: string;
  sortOrder?: number;
  tags: string;
  notes: string;
  initialDirectory: string;
  connectTimeout: number;
  keepaliveInterval: number;
  maxRetries: number;
  terminalType: string;
  sessionMode: "tmux" | "normal";
  jumpHostID: string;
  rdpMode: "web" | "native";
  rdpQuality: "crisp" | "smooth";
  rdpClipboard: boolean;
  rdpAudio: boolean;
  rdpDrive: boolean;
  rdpPrinting: boolean;
  rdpMultiMonitor: boolean;
  desktopDomain: string;
  desktopSecurity: "any" | "nla" | "tls" | "rdp";
  ignoreCertificate: boolean;
  desktopReadOnly: boolean;
  platform?: "linux" | "windows" | "macos" | "bsd" | "unix" | "";
  distribution?: string;
  lastStatus?: string;
  lastLatencyMs?: number;
  lastConnectedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}
export interface AgentStatus {
  hostID: string;
  state: "disconnected" | "connecting" | "connected" | "error";
  backend: "velin";
  aiConfigured: boolean;
  model?: string;
  hostname?: string;
  os?: string;
  arch?: string;
  kernel?: string;
  connectedAt?: string;
  lastSeenAt?: string;
  lastError?: string;
}
export interface AgentModel {
  id: string;
  ownedBy?: string;
  contextWindow?: number;
  maxOutputTokens?: number;
}
export interface AgentSnapshot {
  system: {
    hostname: string;
    os: string;
    arch: string;
    kernel: string;
  };
  uptimeSeconds: number;
  load1: number;
  load5: number;
  load15: number;
  memory: {
    totalBytes: number;
    availableBytes: number;
    usedBytes: number;
    usedPercent: number;
  };
  disks: Array<{
    path: string;
    totalBytes: number;
    freeBytes: number;
    usedBytes: number;
    usedPercent: number;
  }>;
  collectedAt: string;
}
export interface AgentProcess {
  pid: number;
  user: string;
  state: string;
  memoryBytes: number;
  command: string;
}
export interface Credential {
  id: string;
  name: string;
  kind: "password" | "key";
  createdAt?: string;
}
export type SessionStatus =
  | "creating"
  | "attached"
  | "background"
  | "reconnecting"
  | "auth_required"
  | "unreachable"
  | "ended"
  | "ownership_error"
  | "host_key_required";
export interface TerminalSession {
  id: string;
  userID: string;
  hostID: string;
  credentialID: string;
  name: string;
  remoteUser: string;
  sessionMode: "tmux" | "normal";
  tmuxSocket: string;
  tmuxName: string;
  ownerMarker: string;
  status: SessionStatus;
  lastError: string;
  createdAt: string;
  updatedAt: string;
}

export interface TerminalShareViewer {
  id: string;
  name: string;
  ip: string;
  permission: "view" | "operate";
  connectedAt: string;
}

export interface TerminalShare {
  id: string;
  sessionID: string;
  sessionName: string;
  permission: "view" | "operate";
  passwordRequired: boolean;
  record: boolean;
  recordingID?: string;
  expiresAt: string;
  revokedAt?: string;
  createdAt: string;
  active: boolean;
  authorized?: boolean;
  canManage?: boolean;
  url?: string;
  viewerCount: number;
  viewers?: TerminalShareViewer[];
}
export interface Preferences {
  theme: "dark" | "vscode-dark" | "graphite" | "light";
  accentColor: string;
  terminalTheme: string;
  fontSize: number;
  lineHeight: number;
  fontWeight: number;
  letterSpacing: number;
  foreground: string;
  background: string;
  cursorColor: string;
  cursorStyle: "block" | "underline" | "bar";
  cursorBlink: boolean;
  pasteGuard: boolean;
  visualBell: boolean;
  soundBell: boolean;
	lockEnabled: boolean;
  autoLockMinutes: number;
  lockOnShortcut: boolean;
}

export const terminalFontFamily =
  '"JetBrains Mono", "Cascadia Mono", "Cascadia Code", Menlo, Consolas, "Sarasa Mono SC", "Noto Sans Mono CJK SC", "Source Han Mono SC", "Microsoft YaHei", monospace';
export interface PaneLeaf {
  type: "leaf";
  id: string;
  sessionID: string;
}
export interface PaneSplit {
  type: "split";
  id: string;
  direction: "horizontal" | "vertical";
  ratio: number;
  first: PaneNode;
  second: PaneNode;
}
export type PaneNode = PaneLeaf | PaneSplit;
export interface WorkspaceLayout {
  tabs: string[];
  active?: string;
  trees?: Record<string, PaneNode>;
  focused?: Record<string, string>;
  maximized?: Record<string, string>;
  panes?: string[];
  split?: "single" | "horizontal" | "vertical" | "grid";
	pinnedSessionIDs?: string[];
}
export interface ApiErrorBody {
  code: string;
  message: string;
  captchaRequired?: boolean;
  fingerprint?: string;
  hostName?: string;
  hostAddress?: string;
}
