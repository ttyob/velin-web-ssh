<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  nextTick,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import { useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  Activity,
  Archive,
  BellRing,
  Box,
  Bot,
  BrushCleaning,
  ChevronRight,
  ClipboardPaste,
  CircleCheck,
  CircleStop,
  Columns2,
  Copy,
  FolderOpen,
  GitBranch,
  KeyRound,
  LockKeyhole,
  LogOut,
  Menu,
  Monitor,
  MoreHorizontal,
  PanelLeftClose,
  Plus,
  Rows2,
  Search,
  Server,
  Settings,
  Share2,
  TerminalSquare,
  TextSelect,
  Trash2,
  TriangleAlert,
  X,
} from "@lucide/vue";
import { ApiError, api, json } from "../api";
import { useAuthStore } from "../stores/auth";
import type {
  Credential,
  Host,
  PaneNode,
  Preferences,
  TerminalSession,
  WebService,
  WorkspaceLayout,
} from "../types";
import SplitPaneNode from "../components/SplitPaneNode.vue";
import HostDialog from "../components/HostDialog.vue";
import HostResourceList from "../components/HostResourceList.vue";
import type { HostDropTarget } from "../components/HostGroupNode.vue";
import TmuxInstallDialog from "../components/TmuxInstallDialog.vue";
import WebServiceList from "../components/WebServiceList.vue";
import { useWorkspaceLock } from "../composables/useWorkspaceLock";
import { launchNativeRDP, nativeRDPAvailable } from "../rdp";
import {
  applyAccent,
  applyInterfaceTheme,
  findAccent,
  findInterfaceTheme,
  findTerminalTheme,
} from "../themePresets";
import {
  resolveTabAttention,
  type TabAttention,
  type TabAttentionKind,
  type TerminalAttentionEvent,
  type TerminalAttentionNotice,
} from "../terminalAttention";

const SettingsDrawer = defineAsyncComponent(
  () => import("../components/SettingsDrawer.vue"),
);
const FileManagerDialog = defineAsyncComponent(
  () => import("../components/FileManagerDialog.vue"),
);
const WebProxyDialog = defineAsyncComponent(
  () => import("../components/WebProxyDialog.vue"),
);
const AgentDialog = defineAsyncComponent(
  () => import("../components/AgentDialog.vue"),
);
const SessionShareDialog = defineAsyncComponent(
  () => import("../components/SessionShareDialog.vue"),
);
const HostMonitorDialog = defineAsyncComponent(
  () => import("../components/HostMonitorDialog.vue"),
);
const DockerDialog = defineAsyncComponent(
  () => import("../components/DockerDialog.vue"),
);
const GitDialog = defineAsyncComponent(
  () => import("../components/GitDialog.vue"),
);
const RemoteDesktopPane = defineAsyncComponent(
  () => import("../components/RemoteDesktopPane.vue"),
);

const router = useRouter(),
  auth = useAuthStore();
const hosts = ref<Host[]>([]),
  credentials = ref<Credential[]>([]),
  sessions = ref<TerminalSession[]>([]),
  webServices = ref<WebService[]>([]);
const desktopTabs = reactive<
  Record<
    string,
    {
      host: Host;
      status: "connecting" | "connected" | "disconnected" | "error";
    }
  >
>({});
const search = ref(""),
  sessionSearch = ref(""),
  sidebarOpen = ref(true),
  mobileSidebar = ref(false);
const defaultSidebarWidth = 274;
const sidebarWidth = ref(defaultSidebarWidth);
let sidebarResizeCleanup: (() => void) | undefined;
const defaultWebServiceHeight = 210;
const webServiceHeight = ref(defaultWebServiceHeight);
let webServiceResizeCleanup: (() => void) | undefined;
const sessionStatusFilter = ref(""),
  sessionHostFilter = ref(""),
  restoringBackground = ref(false);
const hostDialog = ref(false),
  settingsOpen = ref(false),
  sessionsDialog = ref(false),
  newTabDialog = ref(false),
  newTabMode = ref<"choice" | "hosts">("choice"),
  newTabHostID = ref(""),
  fileManagerOpen = ref(false),
  fileManagerHost = ref<Host>(),
  fileManagerSessionID = ref(""),
  fileManagerPath = ref("."),
  webProxyOpen = ref(false),
  webProxyHost = ref<Host>(),
  agentOpen = ref(false),
  agentHost = ref<Host>(),
  agentTabID = ref(""),
  sessionShareOpen = ref(false),
  sessionShareSession = ref<TerminalSession>(),
  hostMonitorOpen = ref(false),
  hostMonitorHost = ref<Host>(),
  dockerOpen = ref(false),
  dockerHost = ref<Host>(),
  dockerSessionID = ref(""),
  gitOpen = ref(false),
  gitHost = ref<Host>(),
  gitSessionID = ref(""),
  gitPath = ref(""),
  tmuxInstallOpen = ref(false),
  tmuxInstallHost = ref<Host>(),
  editingWebService = ref<WebService>(),
  editingHost = ref<Host>();
let tmuxInstallRetry: (() => Promise<void>) | undefined;
let tmuxNormalFallback: (() => Promise<void>) | undefined;
const sessionDirectories = reactive<Record<string, string>>({});
const conversationSessions = reactive<Record<string, boolean>>({});
const sessionAttention = reactive<Record<string, TerminalAttentionNotice>>({});
const pendingTerminalCommands = reactive<Record<string, string>>({});
const connecting = ref<string>(),
  testing = ref<string>(),
  openingWebService = ref<string>(),
  workspaceVersion = ref(0),
  saveTimer = ref<number>();
const paneTreeRefs = new Map<string, any>();
let savedInterfaceTheme = "dark";
let savedAccentColor = "#5b8cff";
try {
  savedInterfaceTheme = localStorage.getItem("velin-interface-theme") || "dark";
  savedAccentColor =
    findAccent(localStorage.getItem("velin-accent-color") || savedAccentColor).value;
} catch {}
const preferences = reactive<Preferences>({
  theme: findInterfaceTheme(savedInterfaceTheme).id,
  accentColor: savedAccentColor,
  terminalTheme: "velin",
  fontSize: 14,
  lineHeight: 1.25,
  fontWeight: 400,
  letterSpacing: 0,
  foreground: "#d8deea",
  background: "#111318",
  cursorColor: "#8eafff",
  cursorStyle: "block",
  cursorBlink: true,
  pasteGuard: true,
  visualBell: true,
  soundBell: false,
  lockEnabled: false,
  autoLockMinutes: 15,
  lockOnShortcut: true,
});
watch(() => preferences.accentColor, applyAccent, { immediate: true });
watch(() => preferences.theme, applyInterfaceTheme, { immediate: true });
const sidebarStyle = computed(() => ({
  "--sidebar-width": `${sidebarWidth.value}px`,
  "--web-service-height": `${webServiceHeight.value}px`,
}));
const layout = reactive<WorkspaceLayout>({
  tabs: [],
  trees: {},
  focused: {},
  maximized: {},
});
const pinnedSessionIDs = ref<string[]>([]);
const mobileCtrl = ref(false),
  mobileAlt = ref(false);
const contextMenu = reactive({ open: false, x: 0, y: 0, leafID: "" });
const dockerContext = reactive({
  sessionID: "",
  checking: false,
  conversation: false,
  installed: undefined as boolean | undefined,
});
let dockerContextRequest = 0;
const gitMenuDisabled = computed(
  () => dockerContext.checking,
);
const dockerMenuDisabled = computed(
  () =>
    dockerContext.checking ||
    dockerContext.installed === false,
);
const splitDialog = reactive({
  open: false,
  direction: "horizontal" as "horizontal" | "vertical",
  leafID: "",
  choice: "",
});

const visibleHosts = computed(() => {
  const q = search.value.toLowerCase();
  return hosts.value.filter(
    (h) =>
      !q ||
      `${h.name} ${h.address} ${h.groupName} ${h.tags}`
        .toLowerCase()
        .includes(q),
  );
});
const visibleSessions = computed(() => {
  const q = sessionSearch.value.toLowerCase();
  return sessions.value
    .filter((s) => {
      const host = sessionHost(s);
      return (
        (!q ||
          `${s.name} ${s.status} ${host?.name || ""} ${host?.address || ""}`
            .toLowerCase()
            .includes(q)) &&
        (!sessionStatusFilter.value ||
          s.status === sessionStatusFilter.value) &&
        (!sessionHostFilter.value || s.hostID === sessionHostFilter.value)
      );
    })
    .sort(
      (a, b) =>
        Number(pinnedSessionIDs.value.includes(b.id)) -
          Number(pinnedSessionIDs.value.includes(a.id)) ||
        b.updatedAt.localeCompare(a.updatedAt),
    );
});
const activeTree = computed<PaneNode | undefined>(() =>
  layout.active ? layout.trees?.[layout.active] : undefined,
);
const activePaneSessions = computed(() => {
  const tree = activeTree.value;
  if (!tree) return [];
  return collectSessionIDs(tree)
    .map((id) => sessions.value.find((session) => session.id === id))
    .filter((session): session is TerminalSession => Boolean(session));
});
const visibleSessionIDs = computed(() => {
  const ids = Object.values(layout.trees || {}).flatMap((tree) =>
    collectSessionIDs(tree),
  );
  return new Set(ids);
});
const currentSessionIDs = computed(
  () =>
    new Set(
      Object.values(layout.trees || {}).flatMap((tree) =>
        collectSessionIDs(tree),
      ),
    ),
);
const backgroundSessions = computed(() =>
  sessions.value.filter(
    (s) => !visibleSessionIDs.value.has(s.id) && s.status !== "ended",
  ),
);
const splitSessions = computed(() =>
  sessions.value.filter(
    (session) =>
      session.status !== "ended" && !visibleSessionIDs.value.has(session.id),
  ),
);
const currentPaneCount = computed(() =>
  activeTree.value ? collectSessionIDs(activeTree.value).length : 0,
);
const tabIndicators = computed<Record<string, TabAttention>>(() => {
  const indicators: Record<string, TabAttention> = {};
  for (const tabID of layout.tabs) {
    const tree = layout.trees?.[tabID];
    if (!tree) continue;
    const indicator = resolveTabAttention(
      collectSessionIDs(tree)
        .map((id) => sessions.value.find((session) => session.id === id))
        .filter(Boolean)
        .map((session) => ({
          name: session!.name,
          status: session!.status,
          notice: sessionAttention[session!.id],
        })),
    );
    if (indicator) indicators[tabID] = indicator;
  }
  return indicators;
});
const activeWorkspaceSessions = computed(
  () =>
    [...new Set(Object.values(layout.trees || {}).flatMap(collectSessionIDs))]
      .map((id) => sessions.value.find((item) => item.id === id))
      .filter(Boolean) as TerminalSession[],
);

function nodeID() {
  return (
    globalThis.crypto?.randomUUID?.() ||
    `pane-${Date.now()}-${Math.random().toString(16).slice(2)}`
  );
}
function leaf(sessionID: string): PaneNode {
  return { type: "leaf", id: nodeID(), sessionID };
}
function collectSessionIDs(node: PaneNode): string[] {
  return node.type === "leaf"
    ? [node.sessionID]
    : [...collectSessionIDs(node.first), ...collectSessionIDs(node.second)];
}
function collectLeafIDs(node: PaneNode): string[] {
  return node.type === "leaf"
    ? [node.id]
    : [...collectLeafIDs(node.first), ...collectLeafIDs(node.second)];
}
function firstLeafID(node: PaneNode) {
  return collectLeafIDs(node)[0];
}
function leafForSession(node: PaneNode, sessionID: string): string | undefined {
  if (node.type === "leaf")
    return node.sessionID === sessionID ? node.id : undefined;
  return (
    leafForSession(node.first, sessionID) ||
    leafForSession(node.second, sessionID)
  );
}
function tabForSession(sessionID: string) {
  return layout.tabs.find(
    (tab) =>
      layout.trees?.[tab] &&
      collectSessionIDs(layout.trees[tab]).includes(sessionID),
  );
}
function tabSession(tabID: string) {
  const tree = layout.trees?.[tabID];
  return tree
    ? sessions.value.find(
        (session) => session.id === collectSessionIDs(tree)[0],
      )
    : undefined;
}
function tabTitle(tabID: string) {
  return desktopTabs[tabID]?.host.name || tabSession(tabID)?.name || "已结束会话";
}
function tabStatus(tabID: string) {
  return desktopTabs[tabID]?.status || tabSession(tabID)?.status || "ended";
}
function cleanTree(
  node: unknown,
  valid: Set<string>,
  used: Set<string>,
): PaneNode | null {
  if (!node || typeof node !== "object") return null;
  const value = node as any;
  if (
    value.type === "leaf" &&
    typeof value.sessionID === "string" &&
    valid.has(value.sessionID) &&
    !used.has(value.sessionID)
  ) {
    used.add(value.sessionID);
    return {
      type: "leaf",
      id: typeof value.id === "string" ? value.id : nodeID(),
      sessionID: value.sessionID,
    };
  }
  if (
    value.type === "split" &&
    (value.direction === "horizontal" || value.direction === "vertical")
  ) {
    const first = cleanTree(value.first, valid, used),
      second = cleanTree(value.second, valid, used);
    if (!first) return second;
    if (!second) return first;
    return {
      type: "split",
      id: typeof value.id === "string" ? value.id : nodeID(),
      direction: value.direction,
      ratio: Math.min(0.82, Math.max(0.18, Number(value.ratio) || 0.5)),
      first,
      second,
    };
  }
  return null;
}
function splitTree(
  node: PaneNode,
  target: string,
  direction: "horizontal" | "vertical",
  sessionID: string,
): PaneNode {
  if (node.type === "leaf")
    return node.id === target
      ? {
          type: "split",
          id: nodeID(),
          direction,
          ratio: 0.5,
          first: node,
          second: leaf(sessionID),
        }
      : node;
  return {
    ...node,
    first: splitTree(node.first, target, direction, sessionID),
    second: splitTree(node.second, target, direction, sessionID),
  };
}
function removeLeaf(node: PaneNode, target: string): PaneNode | null {
  if (node.type === "leaf") return node.id === target ? null : node;
  const first = removeLeaf(node.first, target),
    second = removeLeaf(node.second, target);
  return first && second ? { ...node, first, second } : first || second;
}
function removeSession(node: PaneNode, sessionID: string): PaneNode | null {
  if (node.type === "leaf") return node.sessionID === sessionID ? null : node;
  const first = removeSession(node.first, sessionID),
    second = removeSession(node.second, sessionID);
  return first && second ? { ...node, first, second } : first || second;
}
function setRatio(
  node: PaneNode,
  nodeIDValue: string,
  ratio: number,
): PaneNode {
  if (node.type === "leaf") return node;
  if (node.id === nodeIDValue) return { ...node, ratio };
  return {
    ...node,
    first: setRatio(node.first, nodeIDValue, ratio),
    second: setRatio(node.second, nodeIDValue, ratio),
  };
}
function copyLayout(value: WorkspaceLayout): WorkspaceLayout {
  return JSON.parse(JSON.stringify(value));
}
function captureLayout(): WorkspaceLayout {
  const tabs = layout.tabs.filter((id) => !desktopTabs[id]);
  const trees = Object.fromEntries(
    tabs.flatMap((id) => (layout.trees?.[id] ? [[id, layout.trees[id]]] : [])),
  );
  const focused = Object.fromEntries(
    tabs.flatMap((id) => (layout.focused?.[id] ? [[id, layout.focused[id]]] : [])),
  );
  return copyLayout({
    tabs,
    active: tabs.includes(layout.active || "") ? layout.active : tabs[0],
    trees,
    focused,
    maximized: {},
    pinnedSessionIDs: pinnedSessionIDs.value,
  });
}
function normalizeLayout(
  saved: WorkspaceLayout,
  valid: Set<string>,
): WorkspaceLayout {
  const normalized: WorkspaceLayout = {
      tabs: [],
      trees: {},
      focused: {},
      maximized: {},
    },
    used = new Set<string>();
  for (const id of Array.isArray(saved.tabs) ? saved.tabs : []) {
    const restored =
      cleanTree(saved.trees?.[id], valid, used) ||
      (valid.has(id) && !used.has(id) ? leaf(id) : null);
    if (!restored) continue;
    for (const sessionID of collectSessionIDs(restored)) used.add(sessionID);
    normalized.tabs.push(id);
    normalized.trees![id] = restored;
    const leaves = collectLeafIDs(restored),
      savedFocus = saved.focused?.[id];
    normalized.focused![id] =
      savedFocus && leaves.includes(savedFocus) ? savedFocus : leaves[0];
  }
  normalized.active = normalized.tabs.includes(saved.active || "")
    ? saved.active
    : normalized.tabs[0];
  return normalized;
}
function applyLayout(value: WorkspaceLayout) {
  layout.tabs = [...value.tabs];
  layout.active = value.active;
  layout.trees = copyLayout(value).trees || {};
  layout.focused = { ...(value.focused || {}) };
  layout.maximized = {};
}
function setSidebarWidth(value: number, persist = false) {
  const max = Math.max(280, Math.min(480, window.innerWidth - 360));
  sidebarWidth.value = Math.round(Math.min(max, Math.max(220, value)));
  if (persist) {
    try {
      localStorage.setItem("velin-sidebar-width", String(sidebarWidth.value));
    } catch {}
  }
}
function startSidebarResize(event: PointerEvent) {
  if (!sidebarOpen.value || window.innerWidth <= 760) return;
  event.preventDefault();
  webServiceResizeCleanup?.();
  document.body.classList.add("is-resizing-sidebar");
  const move = (next: PointerEvent) => setSidebarWidth(next.clientX);
  const stop = () => {
    setSidebarWidth(sidebarWidth.value, true);
    document.body.classList.remove("is-resizing-sidebar");
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", stop);
    window.removeEventListener("pointercancel", stop);
    window.removeEventListener("blur", stop);
    sidebarResizeCleanup = undefined;
  };
  sidebarResizeCleanup?.();
  sidebarResizeCleanup = stop;
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", stop);
  window.addEventListener("pointercancel", stop);
  window.addEventListener("blur", stop);
}
function resizeSidebarByKeyboard(event: KeyboardEvent) {
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault();
  setSidebarWidth(
    sidebarWidth.value + (event.key === "ArrowRight" ? 12 : -12),
    true,
  );
}
function resetSidebarWidth() {
  setSidebarWidth(defaultSidebarWidth, true);
}
function setWebServiceHeight(value: number, persist = false) {
  const sidebar = document.querySelector<HTMLElement>(".sidebar");
  const availableHeight = sidebar?.clientHeight || window.innerHeight;
  const max = Math.max(82, availableHeight - 276);
  webServiceHeight.value = Math.round(Math.min(max, Math.max(82, value)));
  if (persist) {
    try {
      localStorage.setItem(
        "velin-web-service-height",
        String(webServiceHeight.value),
      );
    } catch {}
  }
}
function startWebServiceResize(event: PointerEvent) {
  if (!sidebarOpen.value || window.innerWidth <= 760) return;
  event.preventDefault();
  event.stopPropagation();
  sidebarResizeCleanup?.();
  webServiceResizeCleanup?.();
  document.body.classList.add("is-resizing-web-service");
  const sidebar = (event.currentTarget as HTMLElement).closest(".sidebar");
  const panelBottom =
    sidebar?.querySelector<HTMLElement>(".sidebar-footer")?.getBoundingClientRect()
      .top || window.innerHeight - 50;
  const move = (next: PointerEvent) =>
    setWebServiceHeight(panelBottom - next.clientY);
  const stop = () => {
    setWebServiceHeight(webServiceHeight.value, true);
    document.body.classList.remove("is-resizing-web-service");
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", stop);
    window.removeEventListener("pointercancel", stop);
    window.removeEventListener("blur", stop);
    webServiceResizeCleanup = undefined;
  };
  webServiceResizeCleanup = stop;
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", stop);
  window.addEventListener("pointercancel", stop);
  window.addEventListener("blur", stop);
}
function resizeWebServiceByKeyboard(event: KeyboardEvent) {
  if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
  event.preventDefault();
  setWebServiceHeight(
    webServiceHeight.value + (event.key === "ArrowUp" ? 12 : -12),
    true,
  );
}
function resetWebServiceHeight() {
  setWebServiceHeight(defaultWebServiceHeight, true);
}
async function load() {
  try {
    const [h, c, s, w, p, web, lockPINStatus] = await Promise.all([
      api<Host[]>("/api/hosts"),
      api<Credential[]>("/api/credentials"),
      api<TerminalSession[]>("/api/sessions"),
      api<{ layout: WorkspaceLayout; version: number }>("/api/workspace"),
      api<Partial<Preferences>>("/api/preferences"),
      api<WebService[]>("/api/web-services"),
      api<{ configured: boolean }>("/api/auth/lock-pin"),
    ]);
    hosts.value = h;
    credentials.value = c;
    sessions.value = s;
    webServices.value = web;
    workspaceVersion.value = w.version;
    Object.assign(preferences, p);
    preferences.accentColor = findAccent(preferences.accentColor).value;
    if (preferences.terminalTheme === "velin") {
      const terminalPreset = findTerminalTheme("velin");
      preferences.background = terminalPreset.background;
      preferences.foreground = terminalPreset.foreground;
      preferences.cursorColor = terminalPreset.cursor;
    }
    delete (preferences as Preferences & { lockOnHidden?: boolean })
      .lockOnHidden;
    preferences.lockEnabled &&= lockPINStatus.configured;
    delete (preferences as Preferences & { fontFamily?: string }).fontFamily;
    const valid = new Set(s.map((item) => item.id)),
      saved = (w.layout || {}) as unknown;
    const isDocument = (
      value: unknown,
    ): value is {
      activeWorkspaceID: string;
      workspaces: Array<{ id: string; layout: WorkspaceLayout }>;
      pinnedSessionIDs?: string[];
    } =>
      Boolean(
        value &&
        typeof value === "object" &&
        (value as any).schema === 2 &&
        Array.isArray((value as any).workspaces),
      );
    if (isDocument(saved)) {
      const document = saved;
      pinnedSessionIDs.value = (document.pinnedSessionIDs || []).filter((id) =>
        valid.has(id),
      );
      const selected =
        document.workspaces.find(
          (item) => item.id === document.activeWorkspaceID,
        ) || document.workspaces[0];
      applyLayout(normalizeLayout(selected?.layout || { tabs: [] }, valid));
    } else {
      const savedLayout = (saved || {}) as WorkspaceLayout;
      pinnedSessionIDs.value = (savedLayout.pinnedSessionIDs || []).filter(
        (id) => valid.has(id),
      );
      applyLayout(normalizeLayout(savedLayout, valid));
    }
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "加载工作区失败");
  }
}
async function refreshHosts() {
  try {
    hosts.value = await api<Host[]>("/api/hosts");
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "刷新主机失败");
  }
}

function isTmuxMissing(error: unknown) {
  return (
    (error instanceof ApiError && error.body.code === "tmux_missing") ||
    (error instanceof Error &&
      error.message.toLowerCase().includes("tmux is required"))
  );
}

async function showTmuxInstallGuide(
  host: Host,
  retry: () => Promise<void>,
  fallback: () => Promise<void>,
) {
  await refreshHosts();
  tmuxInstallHost.value =
    hosts.value.find((item) => item.id === host.id) || host;
  tmuxInstallRetry = retry;
  tmuxNormalFallback = fallback;
  tmuxInstallOpen.value = true;
}

function setTmuxInstallOpen(value: boolean) {
  tmuxInstallOpen.value = value;
  if (!value) {
    tmuxInstallHost.value = undefined;
    tmuxInstallRetry = undefined;
    tmuxNormalFallback = undefined;
  }
}

async function retryAfterTmuxInstall() {
  const retry = tmuxInstallRetry;
  setTmuxInstallOpen(false);
  await retry?.();
}

async function fallbackToNormalSession() {
  const fallback = tmuxNormalFallback;
  setTmuxInstallOpen(false);
  await fallback?.();
}

async function connect(
  host: Host,
  trustFingerprint = "",
  temporary?: { secret: string; passphrase: string },
  placement?: {
    tabID: string;
    leafID: string;
    direction: "horizontal" | "vertical";
  },
  sessionMode?: Host["sessionMode"],
  initialCommand = "",
  sessionName = "",
) {
  if ((host.protocol || "ssh") !== "ssh") {
    if (placement) {
      ElMessage.warning("远程桌面只能在独立标签中打开，不能加入分屏");
      return;
    }
    if (host.protocol === "rdp" && host.rdpMode === "native") {
      await openNativeRDP(host);
      return;
    }
    openDesktop(host);
    return;
  }
  connecting.value = host.id;
  try {
    const body = {
      hostID: host.id,
      credentialID: host.credentialID,
      secret: temporary?.secret || "",
      passphrase: temporary?.passphrase || "",
      trustFingerprint,
      name: sessionName || host.name,
      sessionMode: sessionMode || "",
    };
    const session = await api<TerminalSession>("/api/sessions", {
      method: "POST",
      body: json(body),
    });
    if (initialCommand) pendingTerminalCommands[session.id] = initialCommand;
    sessions.value.unshift(session);
    void refreshHosts();
    if (placement)
      addSplitSession(
        placement.tabID,
        placement.leafID,
        placement.direction,
        session.id,
      );
    else openSession(session);
  } catch (e) {
    if (
      e instanceof ApiError &&
      (e.body.code === "unknown_host_key" || e.body.code === "host_key_changed")
    ) {
      try {
        await ElMessageBox.confirm(
          `${e.body.code === "host_key_changed" ? "主机指纹发生变化" : "首次连接需要信任主机指纹"}${e.body.hostName ? `\n主机：${e.body.hostName}${e.body.hostAddress ? `（${e.body.hostAddress}）` : ""}` : ""}\n指纹：${e.body.fingerprint}`,
          "确认主机指纹",
          {
            confirmButtonText: "信任并连接",
            cancelButtonText: "取消",
            type: "warning",
          },
        );
        await connect(
          host,
          e.body.fingerprint || "",
          temporary,
          placement,
          sessionMode,
          initialCommand,
          sessionName,
        );
      } catch {}
    } else if (isTmuxMissing(e)) {
      await showTmuxInstallGuide(
        host,
        () => connect(host, trustFingerprint, temporary, placement, "tmux", initialCommand, sessionName),
        () => connect(host, trustFingerprint, temporary, placement, "normal", initialCommand, sessionName),
      );
    } else if (e instanceof Error && e.message.includes("credential required"))
      await promptTemporary(host, placement, initialCommand, sessionName);
    else ElMessage.error(e instanceof Error ? e.message : "连接失败");
  } finally {
    connecting.value = undefined;
  }
}
async function openNativeRDP(host: Host) {
  if (host.jumpHostID) {
    ElMessage.warning("本地 RDP 客户端不支持 SSH 跳板机，请改用浏览器内连接");
    return;
  }
  try {
    const result = await startNativeRDP(host);
    if (result === "launched") ElMessage.success("已启动本地远程桌面");
    else ElMessage.info("RDP 配置文件已下载，请用远程桌面连接打开");
  } catch (error) {
    if (error instanceof ApiError && error.body.code === "credential_required") {
      try {
        const { value } = await ElMessageBox.prompt(
          `输入 ${host.name} 的 RDP 密码`,
          "临时凭据",
          {
            inputType: "password",
            confirmButtonText: "连接",
            cancelButtonText: "取消",
            inputValidator: (input) => Boolean(input) || "请输入密码",
          },
        );
        const result = await startNativeRDP(host, value);
        if (result === "launched") ElMessage.success("已启动本地远程桌面");
        else ElMessage.info("RDP 配置文件已下载，请用远程桌面连接打开");
      } catch (promptError) {
        if (promptError !== "cancel" && promptError !== "close")
          ElMessage.error(
            promptError instanceof Error ? promptError.message : "无法启动本地远程桌面",
          );
      }
      return;
    }
    ElMessage.error(error instanceof Error ? error.message : "无法启动本地远程桌面");
  }
}
async function startNativeRDP(host: Host, secret = "") {
  if (!nativeRDPAvailable()) return launchNativeRDP(host);
  const session = await api<{ password?: string }>("/api/desktop/sessions", {
    method: "POST",
    body: json({ hostID: host.id, native: true, secret }),
  });
  return launchNativeRDP(host, session.password || "");
}
function openDesktop(host: Host) {
  const tabID = `desktop-${nodeID()}`;
  desktopTabs[tabID] = { host: { ...host }, status: "connecting" };
  layout.tabs.push(tabID);
  layout.trees![tabID] = leaf(tabID);
  layout.focused![tabID] = firstLeafID(layout.trees![tabID]);
  layout.active = tabID;
  mobileSidebar.value = false;
  scheduleSave();
}
function updateDesktopStatus(
  tabID: string,
  status: "connecting" | "connected" | "disconnected" | "error",
) {
  if (desktopTabs[tabID]) desktopTabs[tabID].status = status;
}
async function promptTemporary(
  host: Host,
  placement?: {
    tabID: string;
    leafID: string;
    direction: "horizontal" | "vertical";
  },
  initialCommand = "",
  sessionName = "",
) {
  try {
    const { value } = await ElMessageBox.prompt(
      `输入 ${host.username}@${host.address} 的 SSH 密码`,
      "临时凭据",
      {
        confirmButtonText: "连接",
        cancelButtonText: "取消",
        inputType: "password",
        inputValidator: (v) => Boolean(v) || "请输入密码",
      },
    );
    await connect(host, "", { secret: value, passphrase: "" }, placement, undefined, initialCommand, sessionName);
  } catch {}
}
function openSession(session: TerminalSession) {
  const existing = tabForSession(session.id);
  if (existing) return activate(existing);
  const tabID = layout.tabs.includes(session.id)
    ? `tab-${nodeID()}`
    : session.id;
  layout.tabs.push(tabID);
  layout.trees![tabID] = leaf(session.id);
  layout.focused![tabID] = firstLeafID(layout.trees![tabID]);
  layout.active = tabID;
  mobileSidebar.value = false;
  scheduleSave();
}
function setPaneTreeRef(id: string, value: any) {
  if (value) paneTreeRefs.set(id, value);
  else paneTreeRefs.delete(id);
}
function activePaneTree() {
  return layout.active ? paneTreeRefs.get(layout.active) : undefined;
}
function activate(id: string) {
  layout.active = id;
  clearTabAttention(id);
  mobileSidebar.value = false;
  nextTick(() => {
    activePaneTree()?.resize();
    activePaneTree()?.focusLeaf(layout.focused?.[id]);
  });
  scheduleSave();
}
function addSplitSession(
  tabID: string,
  leafID: string,
  direction: "horizontal" | "vertical",
  sessionID: string,
) {
  const tree = layout.trees?.[tabID];
  if (!tree || visibleSessionIDs.value.has(sessionID)) return;
  const next = splitTree(tree, leafID, direction, sessionID);
  layout.trees![tabID] = next;
  layout.focused![tabID] = leafForSession(next, sessionID) || leafID;
  delete layout.maximized?.[tabID];
  nextTick(() => {
    paneTreeRefs.get(tabID)?.resize();
    paneTreeRefs.get(tabID)?.focusLeaf(layout.focused?.[tabID]);
  });
  scheduleSave();
}
function focusPane(leafID: string) {
  if (!layout.active) return;
  layout.focused![layout.active] = leafID;
  const tree = layout.trees?.[layout.active];
  const session = tree && sessionForLeaf(tree, leafID);
  if (session) delete sessionAttention[session.id];
  nextTick(() => activePaneTree()?.focusLeaf(leafID));
  scheduleSave();
}
function openContext(event: MouseEvent, leafID: string) {
  focusPane(leafID);
  contextMenu.open = true;
  contextMenu.leafID = leafID;
  const tree = layout.active ? layout.trees?.[layout.active] : undefined;
  const session = tree && sessionForLeaf(tree, leafID);
  dockerContext.sessionID = session?.id || "";
  dockerContext.conversation = Boolean(session && conversationSessions[session.id]);
  dockerContext.installed = undefined;
  dockerContext.checking = Boolean(session);
  contextMenu.x = Math.min(event.clientX, window.innerWidth - 188);
  contextMenu.y = Math.max(
    6,
    Math.min(event.clientY, window.innerHeight - 445),
  );
  const request = ++dockerContextRequest;
  if (!session) {
    dockerContext.checking = false;
    return;
  }
  const checks: Promise<unknown>[] = [
    api<{ installed: boolean }>(`/api/sessions/${session.id}/docker/status`)
      .then((result) => {
        if (request === dockerContextRequest && dockerContext.sessionID === session.id)
          dockerContext.installed = result.installed;
      })
      .catch(() => {}),
  ];
  if (session.sessionMode === "tmux") {
    checks.push(
      api<{ command: string }>(`/api/sessions/${session.id}/foreground`)
        .then((result) => {
          if (request !== dockerContextRequest || dockerContext.sessionID !== session.id) return;
          dockerContext.conversation = isConversationCommand(result.command);
        })
        .catch(() => {}),
    );
  }
  void Promise.all(checks).finally(() => {
    if (request === dockerContextRequest) dockerContext.checking = false;
  });
}
function isConversationCommand(command: string) {
  return /(?:^|[\s/])(codex|claude|aider|gemini|opencode|goose|cursor-agent)(?:$|\s)/i.test(
    command.trim(),
  );
}
function updateConversation(id: string, active: boolean) {
  conversationSessions[id] = active;
  if (dockerContext.sessionID === id && !dockerContext.checking)
    dockerContext.conversation = active;
}
function requestSplit(direction: "horizontal" | "vertical") {
  splitDialog.direction = direction;
  splitDialog.leafID = contextMenu.leafID;
  splitDialog.choice = "";
  splitDialog.open = true;
  contextMenu.open = false;
}
async function confirmSplit() {
  if (!layout.active || !splitDialog.choice) return;
  const placement = {
    tabID: layout.active,
    leafID: splitDialog.leafID,
    direction: splitDialog.direction,
  };
  splitDialog.open = false;
  const [kind, id] = splitDialog.choice.split(":");
  if (kind === "session")
    addSplitSession(placement.tabID, placement.leafID, placement.direction, id);
  else {
    const host = hosts.value.find((item) => item.id === id);
    if (host) await connect(host, "", undefined, placement);
  }
}
function sessionForLeaf(
  node: PaneNode,
  leafID: string,
): TerminalSession | undefined {
  if (node.type === "leaf")
    return node.id === leafID
      ? sessions.value.find((session) => session.id === node.sessionID)
      : undefined;
  return (
    sessionForLeaf(node.first, leafID) || sessionForLeaf(node.second, leafID)
  );
}
function removePane(tabID: string, leafID: string) {
  const tree = layout.trees?.[tabID];
  if (!tree) return;
  const next = removeLeaf(tree, leafID);
  if (!next) {
    moveBackground(tabID);
    return;
  }
  layout.trees![tabID] = next;
  const leaves = collectLeafIDs(next);
  if (!leaves.includes(layout.focused?.[tabID] || ""))
    layout.focused![tabID] = leaves[0];
  if (!leaves.includes(layout.maximized?.[tabID] || ""))
    delete layout.maximized?.[tabID];
  nextTick(() => paneTreeRefs.get(tabID)?.resize());
  scheduleSave();
}
async function terminateSession(session: TerminalSession) {
  if (session.status === "ended") return true;
  try {
    await api(`/api/sessions/${session.id}`, {
      method: "DELETE",
      body: json({}),
    });
  } catch (e: any) {
    if (
      e instanceof Error &&
      e.message.toLowerCase().includes("credential required")
    ) {
      try {
        const { value } = await ElMessageBox.prompt(
          "请输入创建该远程会话时使用的 SSH 密码。",
          "认证后终止",
          {
            confirmButtonText: "终止并关闭",
            cancelButtonText: "取消",
            inputType: "password",
            inputValidator: (v) => Boolean(v) || "请输入密码",
          },
        );
        await api(`/api/sessions/${session.id}`, {
          method: "DELETE",
          body: json({ secret: value }),
        });
      } catch (inner: any) {
        if (inner !== "cancel" && inner !== "close")
          ElMessage.error(inner instanceof Error ? inner.message : "终止失败");
        return false;
      }
    } else {
      ElMessage.error(e instanceof Error ? e.message : "终止失败");
      return false;
    }
  }
  session.status = "ended";
  removeLocal(session.id);
  return true;
}
async function terminatePane(leafID: string) {
  if (!layout.active) return;
  const tabID = layout.active,
    tree = layout.trees?.[tabID];
  if (!tree) return;
  contextMenu.open = false;
  const session = sessionForLeaf(tree, leafID);
  if (!session) return;
  if (session.status === "ended") {
    removePane(tabID, leafID);
    return;
  }
  try {
    await ElMessageBox.confirm(
      `终止将结束 ${session.name} 的${session.sessionMode === "normal" ? "普通 SSH 会话" : "远程 tmux 会话"}及其中任务。`,
      "关闭分屏",
      {
        confirmButtonText: "终止并关闭",
        cancelButtonText: "移到后台",
        distinguishCancelAndClose: true,
        type: "warning",
      },
    );
    if (await terminateSession(session)) ElMessage.success("远程会话已终止");
  } catch (action) {
    if (action === "cancel") {
      removePane(tabID, leafID);
      ElMessage.info("会话已移到后台");
    }
  }
}
function copyPaneSelection() {
  activePaneTree()?.copySelection?.(contextMenu.leafID);
  contextMenu.open = false;
}
function pasteIntoPane() {
  activePaneTree()?.pasteClipboard?.(contextMenu.leafID);
  contextMenu.open = false;
}
function selectPaneAll() {
  activePaneTree()?.selectAll?.(contextMenu.leafID);
  contextMenu.open = false;
}
function clearPane() {
  activePaneTree()?.clearTerminal?.(contextMenu.leafID);
  contextMenu.open = false;
}
function updateSessionDirectory(sessionID: string, path: string) {
  if (path.startsWith("/")) sessionDirectories[sessionID] = path;
}
async function openPaneFiles() {
  if (!layout.active) return;
  const tree = layout.trees?.[layout.active];
  const session = tree && sessionForLeaf(tree, contextMenu.leafID);
  const host =
    session && hosts.value.find((item) => item.id === session.hostID);
  contextMenu.open = false;
  if (!session || !host) return ElMessage.warning("当前终端没有可用的主机信息");
  fileManagerHost.value = host;
  fileManagerSessionID.value = session.id;
  let current = sessionDirectories[session.id];
  if (!current) {
    try {
      const result = await api<{ path: string }>(
        `/api/sessions/${session.id}/directory`,
      );
      current = result.path;
      sessionDirectories[session.id] = current;
    } catch {}
  }
  fileManagerPath.value = current || host.initialDirectory || ".";
  fileManagerOpen.value = true;
}
function openPaneAgent() {
  if (!layout.active) return;
  const tree = layout.trees?.[layout.active];
  const session = tree && sessionForLeaf(tree, contextMenu.leafID);
  const host =
    session && hosts.value.find((item) => item.id === session.hostID);
  contextMenu.open = false;
  if (!session || !host)
    return ElMessage.warning("当前终端没有可用的主机信息");
  if (!host.credentialID && !host.hasPassword)
    return ElMessage.warning("请先为当前主机保存 SSH 密码或配置 SSH 凭据");
  agentHost.value = host;
  agentTabID.value = layout.active;
  agentOpen.value = true;
}
function openPaneShare() {
  if (!layout.active) return;
  const tree = layout.trees?.[layout.active];
  const session = tree && sessionForLeaf(tree, contextMenu.leafID);
  contextMenu.open = false;
  if (!session)
    return ElMessage.warning("当前终端会话不可用");
  if (session.status !== "attached")
    return ElMessage.warning("只能分享当前已连接的终端会话");
  sessionShareSession.value = session;
  sessionShareOpen.value = true;
}
function openPaneMonitor() {
  if (!layout.active) return;
  const tree = layout.trees?.[layout.active];
  const session = tree && sessionForLeaf(tree, contextMenu.leafID);
  const host =
    session && hosts.value.find((item) => item.id === session.hostID);
  contextMenu.open = false;
  if (!session || !host)
    return ElMessage.warning("当前终端没有可用的主机信息");
  if (!host.credentialID && !host.hasPassword)
    return ElMessage.warning("请先为当前主机保存 SSH 密码或配置 SSH 凭据");
  hostMonitorHost.value = host;
  hostMonitorOpen.value = true;
}
function openPaneDocker() {
  if (dockerMenuDisabled.value || !layout.active) return;
  const tree = layout.trees?.[layout.active];
  const session = tree && sessionForLeaf(tree, contextMenu.leafID);
  const host =
    session && hosts.value.find((item) => item.id === session.hostID);
  contextMenu.open = false;
  if (!session || !host)
    return ElMessage.warning("当前终端没有可用的主机信息");
  if (dockerContext.installed === false)
    return ElMessage.warning("当前主机未安装 Docker");
  if (!host.credentialID && !host.hasPassword)
    return ElMessage.warning("请先为当前主机保存 SSH 密码或配置 SSH 凭据");
  dockerHost.value = host;
  dockerSessionID.value = session.id;
  dockerOpen.value = true;
}
async function openPaneGit() {
  if (dockerMenuDisabled.value || !layout.active) return;
  const tree = layout.trees?.[layout.active];
  const session = tree && sessionForLeaf(tree, contextMenu.leafID);
  const host =
    session && hosts.value.find((item) => item.id === session.hostID);
  contextMenu.open = false;
  if (!session || !host)
    return ElMessage.warning("当前终端没有可用的主机信息");
  if (!host.credentialID && !host.hasPassword)
    return ElMessage.warning("请先为当前主机保存 SSH 密码或配置 SSH 凭据");
  let current = sessionDirectories[session.id];
  if (!current) {
    try {
      const result = await api<{ path: string }>(
        `/api/sessions/${session.id}/directory`,
      );
      current = result.path;
      sessionDirectories[session.id] = current;
    } catch {}
  }
  gitHost.value = host;
  gitSessionID.value = session.id;
  gitPath.value = current || host.initialDirectory || ".";
  gitOpen.value = true;
}
function sendSnippet(text: string, execute = false) {
  if (!layout.active) return;
  const target = layout.focused?.[layout.active];
  if (!target) return ElMessage.warning("请先选择一个终端分屏");
  activePaneTree()?.sendText?.(target, text + (execute ? "\r" : ""));
  settingsOpen.value = false;
}
function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}
async function openDockerTerminal(target: { id: string; name: string }) {
  const host = dockerHost.value;
  if (!host) return ElMessage.warning("当前终端没有可用的主机信息");
  dockerOpen.value = false;
  await connect(
    host,
    "",
    undefined,
    undefined,
    undefined,
    `docker exec -it ${shellQuote(target.id)} sh\r`,
    `${host.name} · ${target.name}`,
  );
}
function sendBatchSnippet(text: string, sessionIDs: string[]) {
  let sent = 0;
  for (const sessionID of sessionIDs) {
    for (const tab of layout.tabs) {
      const tree = layout.trees?.[tab],
        target = tree && leafForSession(tree, sessionID);
      if (target) {
        paneTreeRefs.get(tab)?.sendText?.(target, text + "\r");
        sent++;
        break;
      }
    }
  }
  settingsOpen.value = false;
  ElMessage.info(`已向 ${sent} 个终端发送命令`);
}
function updateRatio(nodeIDValue: string, ratio: number) {
  if (!layout.active) return;
  const tree = layout.trees?.[layout.active];
  if (tree) layout.trees![layout.active] = setRatio(tree, nodeIDValue, ratio);
  scheduleSave();
}
function moveBackground(id: string) {
  delete desktopTabs[id];
  if (agentTabID.value === id) {
    agentOpen.value = false;
    agentHost.value = undefined;
    agentTabID.value = "";
  }
  clearTabAttention(id);
  layout.tabs = layout.tabs.filter((item) => item !== id);
  delete layout.trees?.[id];
  delete layout.focused?.[id];
  delete layout.maximized?.[id];
  if (layout.active === id) layout.active = layout.tabs[0];
  scheduleSave();
}
async function closeTab(tabID: string) {
  if (desktopTabs[tabID]) {
    moveBackground(tabID);
    return;
  }
  const tree = layout.trees?.[tabID];
  if (!tree) return;
  const targets = [...new Set(collectSessionIDs(tree))]
    .map((id) => sessions.value.find((session) => session.id === id))
    .filter(Boolean) as TerminalSession[];
  const running = targets.filter((session) => session.status !== "ended");
  if (!running.length) {
    moveBackground(tabID);
    return;
  }
  try {
    const detail =
      running.length === 1
        ? `${running[0].name} 的${running[0].sessionMode === "normal" ? "普通 SSH 会话" : "远程 tmux 会话"}及其中任务`
        : `该标签内 ${running.length} 个远程会话及其中任务`;
    await ElMessageBox.confirm(`终止将结束${detail}。`, "关闭终端标签", {
      confirmButtonText: "全部终止并关闭",
      cancelButtonText: "全部移到后台",
      distinguishCancelAndClose: true,
      type: "warning",
    });
    let completed = 0;
    for (const session of running)
      if (await terminateSession(session)) completed++;
    if (completed === running.length)
      ElMessage.success(
        running.length === 1 ? "远程会话已终止" : "标签内会话已全部终止",
      );
  } catch (action) {
    if (action === "cancel") {
      moveBackground(tabID);
      ElMessage.info("标签内会话已移到后台");
    }
  }
}
function removeLocal(id: string) {
  delete sessionAttention[id];
  delete conversationSessions[id];
  for (const tab of [...layout.tabs]) {
    const next = removeSession(layout.trees![tab], id);
    if (next) {
      layout.trees![tab] = next;
      const leaves = collectLeafIDs(next);
      if (!leaves.includes(layout.focused?.[tab] || ""))
        layout.focused![tab] = leaves[0];
      if (!leaves.includes(layout.maximized?.[tab] || ""))
        delete layout.maximized?.[tab];
    } else moveBackground(tab);
  }
  scheduleSave();
}
function cycleTab(offset: number) {
  if (layout.tabs.length < 2) return;
  const index = Math.max(0, layout.tabs.indexOf(layout.active || ""));
  activate(
    layout.tabs[(index + offset + layout.tabs.length) % layout.tabs.length],
  );
}
function cyclePane(offset: number) {
  if (!layout.active || !activeTree.value) return;
  const leaves = collectLeafIDs(activeTree.value),
    current = layout.focused?.[layout.active] || leaves[0],
    index = Math.max(0, leaves.indexOf(current)),
    next = leaves[(index + offset + leaves.length) % leaves.length];
  focusPane(next);
  nextTick(() => activePaneTree()?.focusLeaf(next));
}
function switchMobilePane(sessionID: string) {
  const tree = activeTree.value;
  if (!tree) return;
  const leafID = leafForSession(tree, sessionID);
  if (!leafID) return;
  focusPane(leafID);
  nextTick(() => activePaneTree()?.focusLeaf(leafID));
}
function clearMobileModifiers() {
  mobileCtrl.value = false;
  mobileAlt.value = false;
}
function sendMobileKey(key: string, modified = false) {
  if (modified) activePaneTree()?.sendModifiedKey?.(key);
  else activePaneTree()?.sendKey?.(key);
}
function updateStatus(id: string, status: string, message?: string) {
  const session = sessions.value.find((item) => item.id === id);
  if (session) {
    session.status = status as any;
    session.lastError = message || "";
  }
  const command = pendingTerminalCommands[id];
  if (status === "attached" && command) {
    void nextTick(() => {
      for (const tabID of layout.tabs) {
        const tree = layout.trees?.[tabID];
        const leafID = tree && leafForSession(tree, id);
        const paneTree = leafID ? paneTreeRefs.get(tabID) : undefined;
        if (!leafID || !paneTree) continue;
        delete pendingTerminalCommands[id];
        paneTree.sendText?.(leafID, command);
        paneTree.focusLeaf?.(leafID);
        return;
      }
    });
  }
}
function updateAttention(id: string, event: TerminalAttentionEvent) {
  if (event === "clear") delete sessionAttention[id];
  else sessionAttention[id] = event;
}
function clearTabAttention(tabID: string) {
  const tree = layout.trees?.[tabID];
  if (!tree) return;
  for (const id of collectSessionIDs(tree)) delete sessionAttention[id];
}
function tabAttentionIcon(kind: TabAttentionKind) {
  return {
    required: TriangleAlert,
    bell: BellRing,
    ended: CircleStop,
    settled: CircleCheck,
  }[kind];
}
function updateTitle(id: string, title: string) {
  const session = sessions.value.find((item) => item.id === id);
  if (session && title) session.name = title;
}
function scheduleSave() {
  clearTimeout(saveTimer.value);
  saveTimer.value = window.setTimeout(saveWorkspace, 500);
}
async function saveWorkspace() {
  const document = captureLayout();
  try {
    const result = await api<{ version: number }>("/api/workspace", {
      method: "PUT",
      body: json({ layout: document, version: workspaceVersion.value }),
    });
    workspaceVersion.value = result.version;
  } catch (e) {
    if (e instanceof ApiError && e.body.code === "workspace_conflict") {
      const w = await api<{ version: number }>("/api/workspace");
      workspaceVersion.value = w.version;
      scheduleSave();
    } else ElMessage.error(e instanceof Error ? e.message : "保存工作区失败");
  }
}
function editHost(host?: Host) {
  editingHost.value = host ? { ...host } : undefined;
  hostDialog.value = true;
}
function openWebProxy(host: Host) {
  webProxyHost.value = host;
  editingWebService.value = undefined;
  webProxyOpen.value = true;
}
function addWebService() {
  webProxyHost.value = undefined;
  editingWebService.value = undefined;
  webProxyOpen.value = true;
}
function editWebService(service: WebService) {
  webProxyHost.value = hosts.value.find((host) => host.id === service.hostID);
  editingWebService.value = { ...service };
  webProxyOpen.value = true;
}
function webServiceSaved(service: WebService) {
  const index = webServices.value.findIndex((item) => item.id === service.id);
  if (index >= 0) webServices.value[index] = service;
  else webServices.value.push(service);
  webServices.value.sort((a, b) => a.name.localeCompare(b.name));
}
async function openSavedWebService(service: WebService) {
  if (openingWebService.value) return;
  const popup = window.open("about:blank", "_blank");
  if (!popup)
    return ElMessage.warning("浏览器阻止了新标签，请允许弹出窗口后重试");
  popup.opener = null;
  openingWebService.value = service.id;
  try {
    const result = await api<{ url: string }>(
      `/api/web-services/${service.id}/open`,
      { method: "POST" },
    );
    popup.location.replace(result.url);
    mobileSidebar.value = false;
  } catch (error) {
    popup.close();
    ElMessage.error(
      error instanceof Error ? error.message : "内网 Web 打开失败",
    );
  } finally {
    openingWebService.value = undefined;
  }
}
async function deleteWebService(service: WebService) {
  try {
    await ElMessageBox.confirm(
      `删除内网 Web“${service.name}”？`,
      "删除内网 Web",
      {
        confirmButtonText: "删除",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    await api(`/api/web-services/${service.id}`, { method: "DELETE" });
    webServices.value = webServices.value.filter(
      (item) => item.id !== service.id,
    );
  } catch (error: any) {
    if (error !== "cancel" && error !== "close")
      ElMessage.error(error instanceof Error ? error.message : "删除失败");
  }
}
async function quickConnect() {
  try {
    const { value } = await ElMessageBox.prompt(
      "输入 user@host:port，端口省略时使用 22。",
      "快速连接",
      {
        confirmButtonText: "继续",
        cancelButtonText: "取消",
        inputPlaceholder: "root@server.example.com:22",
        inputPattern: /^[^@\s]+@(?:\[[^\]]+\]|[^:\s]+)(?::\d{1,5})?$/,
        inputErrorMessage: "格式应为 user@host:port",
      },
    );
    const match = value
      .trim()
      .match(/^([^@\s]+)@(?:\[([^\]]+)\]|([^:\s]+))(?::(\d+))?$/);
    if (!match) return;
    const address = match[2] || match[3],
      port = Number(match[4] || 22);
    if (port < 1 || port > 65535)
      return ElMessage.warning("端口范围应为 1 到 65535");
    const host = await api<Host>("/api/hosts", {
      method: "POST",
      body: json({
        name: address,
        address,
        port,
        username: match[1],
        credentialID: "",
        groupName: "快速连接",
        tags: "",
        notes: "",
        initialDirectory: "",
        connectTimeout: 12,
        keepaliveInterval: 30,
        maxRetries: 5,
        terminalType: "xterm-256color",
      }),
    });
    hostSaved(host);
    await connect(host);
  } catch {}
}
function duplicateHost(host: Host) {
  editingHost.value = {
    ...host,
    id: "",
    name: `${host.name} 副本`,
    lastStatus: "",
    lastLatencyMs: 0,
    lastConnectedAt: undefined,
    createdAt: undefined,
    updatedAt: undefined,
  };
  hostDialog.value = true;
}
async function testHost(
  host: Host,
  trustFingerprint = "",
  temporary?: { secret: string; passphrase: string },
) {
  testing.value = host.id;
  try {
    const result = await api<{
      latencyMs: number;
      tmuxVersion: string;
      sessionMode: Host["sessionMode"];
      platform?: Host["platform"];
      distribution?: string;
    }>(
      `/api/hosts/${host.id}/test`,
      {
        method: "POST",
        body: json({
          credentialID: host.credentialID,
          secret: temporary?.secret || "",
          passphrase: temporary?.passphrase || "",
          trustFingerprint,
        }),
      },
    );
    host.lastStatus = "online";
    host.lastLatencyMs = result.latencyMs;
    host.lastConnectedAt = new Date().toISOString();
    if (result.platform) host.platform = result.platform;
    if (result.distribution) host.distribution = result.distribution;
    ElMessage.success(
      (host.protocol || "ssh") === "ssh"
        ? `连接正常 · ${result.latencyMs} ms · ${result.sessionMode === "normal" ? "普通 SSH" : result.tmuxVersion}`
        : `${host.protocol.toUpperCase()} 端口可达 · ${result.latencyMs} ms`,
    );
  } catch (e) {
    host.lastStatus = "offline";
    if (
      e instanceof ApiError &&
      (e.body.code === "unknown_host_key" || e.body.code === "host_key_changed")
    ) {
      try {
        await ElMessageBox.confirm(
          `${e.body.code === "host_key_changed" ? "主机指纹发生变化" : "首次连接需要信任主机指纹"}${e.body.hostName ? `\n主机：${e.body.hostName}${e.body.hostAddress ? `（${e.body.hostAddress}）` : ""}` : ""}\n指纹：${e.body.fingerprint}`,
          "确认主机指纹",
          {
            confirmButtonText: "信任并测试",
            cancelButtonText: "取消",
            type: "warning",
          },
        );
        await testHost(host, e.body.fingerprint || "", temporary);
      } catch {}
    } else if (isTmuxMissing(e)) {
      await showTmuxInstallGuide(
        host,
        () => testHost(host, trustFingerprint, temporary),
        () => connect(host, trustFingerprint, temporary, undefined, "normal"),
      );
    } else if (
      e instanceof Error &&
      e.message.toLowerCase().includes("credential required")
    ) {
      try {
        const protocol = host.protocol || "ssh";
        const { value } = await ElMessageBox.prompt(
          protocol === "ssh"
            ? `输入 ${host.username}@${host.address} 的 SSH 密码`
            : `输入 ${host.name} 的 ${protocol.toUpperCase()} 密码`,
          "测试连接",
          {
            confirmButtonText: "测试",
            cancelButtonText: "取消",
            inputType: "password",
            inputValidator: (v) => Boolean(v) || "请输入密码",
          },
        );
        await testHost(host, "", { secret: value, passphrase: "" });
      } catch {}
    } else
      ElMessage.error(
        e instanceof ApiError
          ? {
              authentication_failed: "认证失败，请检查用户名和凭据",
              dns_failed: "域名解析失败",
              network_timeout: "连接超时",
              connection_refused: "目标端口拒绝连接",
              tmux_missing: "SSH 可用，但远程主机未安装 tmux",
            }[e.body.code] || e.message
          : e instanceof Error
            ? e.message
            : "测试失败",
      );
  } finally {
    testing.value = undefined;
  }
}
async function deleteHost(host: Host) {
  try {
    const active = await api<TerminalSession[]>(
      `/api/hosts/${host.id}/sessions`,
    );
    if (active.length) {
      await ElMessageBox.alert(
        `该主机仍有关联的活动会话：\n${active.map((item) => `• ${item.name}`).join("\n")}\n\n请先明确终止这些会话后再删除主机。`,
        "无法删除主机",
        { confirmButtonText: "知道了", type: "warning" },
      );
      return;
    }
    await ElMessageBox.confirm(
      `删除主机“${host.name}”？已保存的凭据不会被删除。`,
      "删除主机",
      { confirmButtonText: "删除", cancelButtonText: "取消", type: "warning" },
    );
    await api(`/api/hosts/${host.id}`, { method: "DELETE" });
    hosts.value = hosts.value.filter((item) => item.id !== host.id);
    webServices.value = webServices.value.filter(
      (item) => item.hostID !== host.id,
    );
  } catch (e: any) {
    if (e !== "cancel" && e !== "close")
      ElMessage.error(e instanceof Error ? e.message : "删除失败");
  }
}
function hostSaved(host: Host) {
  const i = hosts.value.findIndex((item) => item.id === host.id);
  if (i >= 0) hosts.value[i] = host;
  else hosts.value.push(host);
  api<Credential[]>("/api/credentials")
    .then((items) => (credentials.value = items))
    .catch(() => {});
}
function normalizeHostGroup(value: string) {
  return value
    .split("/")
    .map((part) => part.trim())
    .filter(Boolean)
    .join("/");
}
function compareHostOrder(a: Host, b: Host) {
  return (
    Number(a.sortOrder ?? 0) - Number(b.sortOrder ?? 0) ||
    a.name.localeCompare(b.name)
  );
}
async function reorderHost(payload: {
  hostID: string;
  target: HostDropTarget;
}) {
  const moved = hosts.value.find((host) => host.id === payload.hostID);
  if (!moved) return;
  if (payload.target.kind === "host" && payload.target.hostID === moved.id)
    return;

  const sourceGroup = normalizeHostGroup(moved.groupName);
  const targetGroup = normalizeHostGroup(
    payload.target.groupPath === "__ungrouped__"
      ? ""
      : payload.target.groupPath,
  );
  const sourceHosts = hosts.value
    .filter((host) => normalizeHostGroup(host.groupName) === sourceGroup)
    .sort(compareHostOrder)
    .filter((host) => host.id !== moved.id);
  const targetHosts =
    sourceGroup === targetGroup
      ? sourceHosts
      : hosts.value
          .filter((host) => normalizeHostGroup(host.groupName) === targetGroup)
          .sort(compareHostOrder);
  let insertAt = targetHosts.length;
  const target = payload.target;
  if (target.kind === "host") {
    const targetIndex = targetHosts.findIndex(
      (host) => host.id === target.hostID,
    );
    if (targetIndex >= 0)
      insertAt = targetIndex + (target.after ? 1 : 0);
  }
  targetHosts.splice(insertAt, 0, moved);

  const updates = new Map<string, { groupName: string; sortOrder: number }>();
  for (const [index, host] of sourceHosts.entries())
    updates.set(host.id, { groupName: sourceGroup, sortOrder: index + 1 });
  for (const [index, host] of targetHosts.entries())
    updates.set(host.id, { groupName: targetGroup, sortOrder: index + 1 });

  const previous = new Map<string, { groupName: string; sortOrder?: number }>();
  for (const [id, update] of updates) {
    const host = hosts.value.find((item) => item.id === id);
    if (!host) continue;
    previous.set(id, { groupName: host.groupName, sortOrder: host.sortOrder });
    host.groupName = update.groupName;
    host.sortOrder = update.sortOrder;
  }
  try {
    await api("/api/hosts/reorder", {
      method: "POST",
      body: json({
        items: [...updates].map(([id, update]) => ({ id, ...update })),
      }),
    });
    ElMessage.success(
      sourceGroup === targetGroup ? "主机顺序已更新" : "主机已移动到目标分组",
    );
  } catch (e) {
    for (const [id, value] of previous) {
      const host = hosts.value.find((item) => item.id === id);
      if (host) Object.assign(host, value);
    }
    ElMessage.error(e instanceof Error ? e.message : "主机移动失败");
  }
}
function credentialSaved(credential: Credential) {
  const index = credentials.value.findIndex(
    (item) => item.id === credential.id,
  );
  if (index >= 0) credentials.value[index] = credential;
  else credentials.value.push(credential);
  credentials.value.sort((a, b) => a.name.localeCompare(b.name));
}
function credentialDeleted(id: string) {
  credentials.value = credentials.value.filter((item) => item.id !== id);
}
function openNewTabDialog() {
  newTabMode.value = "choice";
  newTabHostID.value = "";
  newTabDialog.value = true;
}
function chooseQuickConnect() {
  newTabDialog.value = false;
  quickConnect();
}
function connectSelectedHost() {
  const host = hosts.value.find((item) => item.id === newTabHostID.value);
  if (!host) return ElMessage.warning("请选择一台主机");
  newTabDialog.value = false;
  connect(host);
}
function sessionHost(session: TerminalSession) {
  return hosts.value.find((host) => host.id === session.hostID);
}
async function renameSession(session: TerminalSession) {
  try {
    const { value } = await ElMessageBox.prompt(
      "输入新的会话名称",
      "重命名会话",
      {
        confirmButtonText: "保存",
        cancelButtonText: "取消",
        inputValue: session.name,
        inputValidator: (v) => Boolean(v.trim()) || "名称不能为空",
      },
    );
    const saved = await api<TerminalSession>(`/api/sessions/${session.id}`, {
      method: "PATCH",
      body: json({ name: value }),
    });
    session.name = saved.name;
  } catch {}
}
async function duplicateSession(session: TerminalSession) {
  const host = sessionHost(session);
  if (host) await connect(host, "", undefined, undefined, session.sessionMode);
}
function togglePin(session: TerminalSession) {
  pinnedSessionIDs.value = pinnedSessionIDs.value.includes(session.id)
    ? pinnedSessionIDs.value.filter((id) => id !== session.id)
    : [...pinnedSessionIDs.value, session.id];
  scheduleSave();
}
async function restoreBackgroundSessions() {
  const targets = visibleSessions.value
    .filter(
      (session) =>
        !visibleSessionIDs.value.has(session.id) && session.status !== "ended",
    )
    .slice(0, 30);
  if (!targets.length)
    return ElMessage.info("当前筛选范围没有可恢复的后台会话");
  try {
    await ElMessageBox.confirm(
      `将以最多 3 个并发恢复 ${targets.length} 个后台会话。需要临时凭据的会话会保留等待认证状态。`,
      "批量恢复后台会话",
      { confirmButtonText: "开始恢复", cancelButtonText: "取消" },
    );
    restoringBackground.value = true;
    let cursor = 0,
      succeeded = 0,
      failed = 0;
    const worker = async () => {
      while (cursor < targets.length) {
        const session = targets[cursor++];
        try {
          await api(`/api/sessions/${session.id}/restore`, {
            method: "POST",
            body: json({}),
          });
          session.status = "attached";
          openSession(session);
          succeeded++;
        } catch (e) {
          session.status =
            e instanceof Error &&
            e.message.toLowerCase().includes("credential required")
              ? "auth_required"
              : "unreachable";
          session.lastError = e instanceof Error ? e.message : "恢复失败";
          failed++;
        }
      }
    };
    await Promise.all(
      Array.from({ length: Math.min(3, targets.length) }, worker),
    );
    ElMessage.success(`后台恢复完成：成功 ${succeeded}，失败 ${failed}`);
  } catch {
  } finally {
    restoringBackground.value = false;
  }
}
function statusColor(status: string) {
  return status === "attached" || status === "connected"
    ? "online"
    : status === "background"
      ? "idle"
      : status === "ended" || status === "disconnected"
        ? "off"
        : "warning";
}
function openManagedSession(session: TerminalSession) {
  sessionsDialog.value = false;
  openSession(session);
}
async function logout() {
  try {
    await auth.logout();
  } finally {
    clearStoredLock();
    router.replace("/login");
  }
}
function closeWorkspaceOverlays() {
  settingsOpen.value = false;
  sessionsDialog.value = false;
  newTabDialog.value = false;
  hostDialog.value = false;
  fileManagerOpen.value = false;
  webProxyOpen.value = false;
  agentOpen.value = false;
  agentHost.value = undefined;
  agentTabID.value = "";
  sessionShareOpen.value = false;
  sessionShareSession.value = undefined;
  dockerOpen.value = false;
  dockerHost.value = undefined;
  dockerSessionID.value = "";
  setTmuxInstallOpen(false);
  contextMenu.open = false;
  splitDialog.open = false;
  mobileSidebar.value = false;
  mobileCtrl.value = false;
  mobileAlt.value = false;
}
const {
  locked,
  lockPIN,
  unlocking,
  lockError,
  lockPINInput,
  lockWorkspace,
  unlockWorkspace,
  clearStoredLock,
} = useWorkspaceLock({
  preferences,
  auth,
  closeOverlays: closeWorkspaceOverlays,
  reload: load,
  logout,
});
async function confirmLogout() {
  try {
    await ElMessageBox.confirm("退出账号不会终止正在运行的远程终端任务。", "退出登录", {
      confirmButtonText: "退出",
      cancelButtonText: "取消",
      type: "warning",
    });
    await logout();
  } catch {}
}
function handleKeyboard(event: KeyboardEvent) {
  if (locked.value) return;
  if (event.key === "Escape") {
    contextMenu.open = false;
    splitDialog.open = false;
    return;
  }
  if (!(event.ctrlKey || event.metaKey) || !event.shiftKey) return;
  if (event.code === "BracketLeft" || event.code === "BracketRight") {
    event.preventDefault();
    event.stopPropagation();
    cycleTab(event.code === "BracketRight" ? 1 : -1);
  } else if (
    event.code === "ArrowLeft" ||
    event.code === "ArrowUp" ||
    event.code === "ArrowRight" ||
    event.code === "ArrowDown"
  ) {
    event.preventDefault();
    event.stopPropagation();
    cyclePane(
      event.code === "ArrowRight" || event.code === "ArrowDown" ? 1 : -1,
    );
  }
}
onMounted(() => {
  try {
    const savedWidth = Number(localStorage.getItem("velin-sidebar-width"));
    if (Number.isFinite(savedWidth) && savedWidth > 0)
      setSidebarWidth(savedWidth);
    const savedWebServiceHeight = Number(
      localStorage.getItem("velin-web-service-height"),
    );
    if (Number.isFinite(savedWebServiceHeight) && savedWebServiceHeight > 0)
      setWebServiceHeight(savedWebServiceHeight);
  } catch {}
  if (!locked.value) load();
  window.addEventListener("keydown", handleKeyboard, true);
});
onBeforeUnmount(() => {
  sidebarResizeCleanup?.();
  webServiceResizeCleanup?.();
  clearTimeout(saveTimer.value);
  window.removeEventListener("keydown", handleKeyboard, true);
});
</script>

<template>
  <main class="workspace-shell" @click="contextMenu.open = false">
    <aside
      :inert="locked || undefined"
      class="sidebar"
      :class="{
        collapsed: !sidebarOpen,
        'mobile-open': mobileSidebar,
      }"
      :style="sidebarStyle"
    >
      <header class="sidebar-header">
        <div class="brand-mini">
          <span class="brand-mark"><TerminalSquare :size="20" /></span
          ><strong>Velin</strong>
        </div>
        <button
          class="icon-btn desktop-only"
          title="收起侧栏"
          @click="sidebarOpen = !sidebarOpen"
        >
          <PanelLeftClose :size="18" /></button
        ><button
          class="icon-btn mobile-only"
          title="关闭侧栏"
          @click="mobileSidebar = false"
        >
          <X :size="18" />
        </button>
      </header>
      <template v-if="sidebarOpen || mobileSidebar"
        ><div class="sidebar-search host-only-search">
          <Search :size="15" /><input
            v-model="search"
            placeholder="搜索主机、分组或标签"
          />
        </div>
        <HostResourceList
          :hosts="visibleHosts"
          :testing="testing"
          @connect="connect"
          @web="openWebProxy"
          @test="testHost"
          @edit="editHost"
          @duplicate="duplicateHost"
          @delete="deleteHost"
          @add="editHost()"
          @refresh="refreshHosts"
          @reorder="reorderHost"
        />
        <button
          class="web-service-resizer desktop-only"
          title="拖动调整内网 Web 高度，双击恢复默认"
          aria-label="调整内网 Web 区域高度"
          @pointerdown="startWebServiceResize"
          @dblclick="resetWebServiceHeight"
          @keydown="resizeWebServiceByKeyboard"
        >
          <span></span>
        </button>
        <WebServiceList
          :services="webServices"
          :hosts="hosts"
          :opening="openingWebService"
          @add="addWebService"
          @open="openSavedWebService"
          @edit="editWebService"
          @delete="deleteWebService"
        />
        <footer class="sidebar-footer">
          <button @click="settingsOpen = true">
            <Settings :size="16" /><span>设置</span></button
          ><el-dropdown trigger="hover" placement="top-end">
            <button class="user-avatar" :title="auth.user?.username">
              {{ auth.user?.username.slice(0, 1).toUpperCase() }}
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item disabled>
                  {{ auth.user?.username }}
                </el-dropdown-item>
                <el-dropdown-item
                  :icon="Monitor"
                  @click="sessionsDialog = true"
                >
                  会话管理
                  <span v-if="backgroundSessions.length" class="menu-count">{{
                    backgroundSessions.length
                  }}</span>
                </el-dropdown-item>
                <el-dropdown-item
                  v-if="preferences.lockEnabled"
                  :icon="LockKeyhole"
                  @click="lockWorkspace('manual')"
                >
                  锁定工作区
                </el-dropdown-item>
                <el-dropdown-item divided :icon="LogOut" @click="confirmLogout">
                  退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </footer></template
      ><button
        v-else
        class="collapsed-open"
        title="展开侧栏"
        @click="sidebarOpen = true"
      >
        <ChevronRight :size="18" />
      </button>
      <button
        v-if="sidebarOpen"
        class="sidebar-resizer desktop-only"
        title="拖动调整侧栏宽度，双击恢复默认"
        aria-label="调整侧栏宽度"
        @pointerdown="startSidebarResize"
        @dblclick="resetSidebarWidth"
        @keydown="resizeSidebarByKeyboard"
      >
        <span></span>
      </button>
    </aside>
    <section class="workspace-main" :inert="locked || undefined">
      <header class="tabbar">
        <button
          class="icon-btn mobile-only"
          title="打开侧栏"
          @click="mobileSidebar = true"
        >
          <Menu :size="18" />
        </button>
        <div class="tabs-scroll">
          <transition-group name="tab"
            ><div
              v-for="id in layout.tabs"
              :key="id"
              class="terminal-tab"
              :class="{ active: layout.active === id }"
              @click="activate(id)"
            >
              <span
                class="status-dot"
                :class="statusColor(tabStatus(id))"
              ></span
              ><span class="tab-title">{{ tabTitle(id) }}</span
              ><el-tooltip
                v-if="tabIndicators[id]"
                :content="tabIndicators[id].label"
                placement="bottom"
              >
                <span
                  class="tab-attention"
                  :class="tabIndicators[id].kind"
                  :aria-label="tabIndicators[id].label"
                >
                  <component
                    :is="tabAttentionIcon(tabIndicators[id].kind)"
                    :size="14"
                  />
                </span>
              </el-tooltip>
              <button
                class="tab-close danger"
                :title="desktopTabs[id] ? '关闭远程桌面标签' : '关闭终端标签'"
                @click.stop="closeTab(id)"
              >
                <X :size="13" />
              </button></div></transition-group
          ><button class="new-tab" title="新建终端" @click="openNewTabDialog">
            <Plus :size="16" />
          </button>
        </div>
        <button
          class="mobile-session-switch mobile-only"
          title="切换终端会话"
          aria-label="切换终端会话"
          @click="sessionsDialog = true"
        >
          <Monitor :size="15" /><span>{{ layout.tabs.length }}</span>
        </button>
        <div class="workspace-actions">
          <span v-if="currentPaneCount > 1" class="pane-count"
            ><Columns2 :size="14" />{{ currentPaneCount }}</span
          >
          <button
            v-if="preferences.lockEnabled"
            class="icon-btn workspace-lock-button"
            title="手动锁屏"
            aria-label="手动锁屏"
            @click="lockWorkspace('manual')"
          >
            <LockKeyhole :size="16" />
          </button>
        </div>
      </header>
      <template v-if="!locked && layout.tabs.length"
        ><nav
          v-if="activePaneSessions.length > 1"
          class="mobile-pane-switcher"
          aria-label="切换当前标签内的终端"
        >
          <button
            v-for="session in activePaneSessions"
            :key="session.id"
            class="mobile-pane-tab"
            :class="{
              active:
                layout.focused?.[layout.active || ''] ===
                leafForSession(activeTree!, session.id),
            }"
            :title="session.name"
            @click="switchMobilePane(session.id)"
          >
            <span class="status-dot" :class="statusColor(session.status)"></span>
            <span>{{ session.name }}</span>
          </button>
        </nav
        ><div
          v-for="id in layout.tabs"
          v-show="layout.active === id"
          :key="id"
          class="terminal-workspace"
        >
          <RemoteDesktopPane
            v-if="desktopTabs[id]"
            :host="desktopTabs[id].host"
            :visible="layout.active === id"
            @status="updateDesktopStatus(id, $event)"
          />
          <SplitPaneNode
            v-else
            :ref="(value) => setPaneTreeRef(id, value)"
            :node="layout.trees![id]"
            :sessions="sessions"
            :preferences="preferences"
            :root="layout.trees![id].type === 'leaf'"
            :visible="layout.active === id"
            :focused-leaf-id="layout.focused?.[id]"
            :maximized-leaf-id="layout.maximized?.[id]"
            :mobile-ctrl="mobileCtrl"
            :mobile-alt="mobileAlt"
            @focus="focusPane"
            @context="openContext"
            @close="terminatePane"
            @status="updateStatus"
            @title="updateTitle"
            @directory="updateSessionDirectory"
            @conversation="updateConversation"
            @attention="updateAttention"
            @ratio="updateRatio"
            @modifiers-used="clearMobileModifiers"
          /></div
      ></template>
      <div v-else-if="!locked" class="workspace-empty">
        <div class="empty-terminal-mark"><TerminalSquare :size="34" /></div>
        <h2>没有打开的终端</h2>
        <p>从左侧主机列表双击连接，或新建一台主机。</p>
        <el-button type="primary" :icon="Plus" @click="editHost()"
          >新增主机</el-button
        >
      </div>
      <div
        v-if="!locked && activeTree && !desktopTabs[layout.active || '']"
        class="mobile-keybar"
      >
        <button
          class="modifier"
          :class="{ active: mobileCtrl }"
          @click="mobileCtrl = !mobileCtrl"
        >
          Ctrl</button
        ><button
          class="modifier"
          :class="{ active: mobileAlt }"
          @click="mobileAlt = !mobileAlt"
        >
          Alt</button
        ><button @click="sendMobileKey('\x1b')">Esc</button
        ><button @click="sendMobileKey('\t')">Tab</button
        ><button @click="sendMobileKey('c', true)">C</button
        ><button @click="sendMobileKey('d', true)">D</button
        ><button @click="sendMobileKey('|', true)">|</button
        ><button @click="sendMobileKey('~', true)">~</button
        ><button @click="sendMobileKey('\x1b[H', true)">Home</button
        ><button @click="sendMobileKey('\x1b[F', true)">End</button
        ><button @click="sendMobileKey('\x1b[5~')">PgUp</button
        ><button @click="sendMobileKey('\x1b[6~')">PgDn</button
        ><button aria-label="向上" @click="sendMobileKey('\x1b[A', true)">
          ↑</button
        ><button aria-label="向下" @click="sendMobileKey('\x1b[B', true)">
          ↓</button
        ><button aria-label="向左" @click="sendMobileKey('\x1b[D', true)">
          ←</button
        ><button aria-label="向右" @click="sendMobileKey('\x1b[C', true)">
          →
        </button>
      </div>
    </section>
    <transition name="context"
      ><nav
        v-if="contextMenu.open"
        class="pane-context-menu"
        :style="{ left: `${contextMenu.x}px`, top: `${contextMenu.y}px` }"
        @click.stop
      >
        <button @click="copyPaneSelection">
          <Copy :size="16" /><span>复制选中内容</span></button
        ><button @click="pasteIntoPane">
          <ClipboardPaste :size="16" /><span>粘贴</span></button
        ><button @click="selectPaneAll">
          <TextSelect :size="16" /><span>全选终端内容</span></button
        ><button @click="clearPane">
          <BrushCleaning :size="16" /><span>清屏</span></button
        ><button @click="openPaneFiles">
          <FolderOpen :size="16" /><span>打开当前目录</span></button
        ><button @click="openPaneAgent">
          <Bot :size="16" /><span>打开 Agent</span></button
        ><button @click="openPaneShare">
          <Share2 :size="16" /><span>分享会话</span></button
        ><button @click="openPaneMonitor">
          <Activity :size="16" /><span>主机监控</span></button
        ><button
          :disabled="gitMenuDisabled"
          :title="
            dockerContext.checking
              ? '正在确认当前终端状态'
              : 'Git 管理'
          "
          @click="openPaneGit"
        >
          <GitBranch :size="16" /><span>Git 管理</span></button
        ><button
          :disabled="dockerMenuDisabled"
          :title="
            dockerContext.checking
              ? '正在确认当前终端状态'
              : dockerContext.installed === false
                ? '当前主机未安装 Docker'
                : 'Docker 管理'
          "
          @click="openPaneDocker"
        >
          <Box :size="16" /><span>Docker 管理</span></button
        ><span class="context-separator" /><button
          @click="requestSplit('horizontal')"
        >
          <Columns2 :size="16" /><span>左右分屏</span></button
        ><button @click="requestSplit('vertical')">
          <Rows2 :size="16" /><span>上下分屏</span></button
        ><button class="danger" @click="terminatePane(contextMenu.leafID)">
          <Trash2 :size="16" /><span>关闭分屏</span>
        </button>
      </nav></transition
    >
    <div
      v-if="mobileSidebar"
      class="mobile-overlay"
      @click="mobileSidebar = false"
    ></div>
    <el-dialog
      v-model="splitDialog.open"
      class="split-dialog"
      :title="
        splitDialog.direction === 'horizontal' ? '添加左右分屏' : '添加上下分屏'
      "
      width="min(560px, calc(100vw - 28px))"
      append-to-body
    >
      <el-radio-group v-model="splitDialog.choice" class="split-targets">
        <div v-if="splitSessions.length" class="split-target-section">
          <div class="split-target-heading">
            <Monitor :size="15" />运行中的终端
          </div>
          <el-radio
            v-for="session in splitSessions"
            :key="session.id"
            :value="`session:${session.id}`"
            class="split-target"
            ><span
              class="status-dot"
              :class="statusColor(session.status)"
            ></span
            ><span
              ><strong>{{ session.name }}</strong
              ><small>{{
                sessionHost(session)?.address || session.remoteUser
              }}</small></span
            ></el-radio
          >
        </div>
        <div class="split-target-section">
          <div class="split-target-heading"><Server :size="15" />连接主机</div>
          <el-radio
            v-for="host in hosts.filter(
              (item) => !item.protocol || item.protocol === 'ssh',
            )"
            :key="host.id"
            :value="`host:${host.id}`"
            class="split-target"
            ><span
              ><strong>{{ host.name }}</strong
              ><small
                >{{ host.username }}@{{ host.address }}:{{ host.port }}</small
              ></span
            ></el-radio
          ><button
            v-if="!hosts.length"
            class="dialog-empty-action"
            @click="
              splitDialog.open = false;
              editHost();
            "
          >
            <Plus :size="15" />新增主机
          </button>
        </div>
      </el-radio-group>
      <template #footer
        ><el-button @click="splitDialog.open = false">取消</el-button
        ><el-button
          type="primary"
          :disabled="!splitDialog.choice"
          :loading="Boolean(connecting)"
          @click="confirmSplit"
          >打开分屏</el-button
        ></template
      >
    </el-dialog>
    <el-dialog
      v-model="newTabDialog"
      class="new-tab-dialog"
      title="新建终端"
      width="min(540px, calc(100vw - 28px))"
      append-to-body
    >
      <div v-if="newTabMode === 'choice'" class="new-tab-choices">
        <button @click="chooseQuickConnect">
          <TerminalSquare :size="22" />
          <span><strong>快速连接</strong><small>输入新的 SSH 地址</small></span>
          <ChevronRight :size="16" />
        </button>
        <button @click="newTabMode = 'hosts'">
          <Server :size="22" />
          <span
            ><strong>从主机列表选择</strong><small>打开已保存主机</small></span
          >
          <ChevronRight :size="16" />
        </button>
      </div>
      <template v-else>
        <el-select
          v-model="newTabHostID"
          class="new-tab-host-select"
          filterable
          placeholder="搜索并选择主机"
        >
          <el-option
            v-for="host in hosts"
            :key="host.id"
            :label="`${host.groupName ? `${host.groupName} / ` : ''}${host.name}`"
            :value="host.id"
          />
        </el-select>
        <div class="new-tab-dialog-actions">
          <el-button @click="newTabMode = 'choice'">返回</el-button>
          <el-button
            type="primary"
            :disabled="!newTabHostID"
            @click="connectSelectedHost"
            >打开终端</el-button
          >
        </div>
      </template>
    </el-dialog>
    <el-dialog
      v-model="sessionsDialog"
      class="sessions-dialog"
      title="会话管理"
      width="min(820px, calc(100vw - 28px))"
      append-to-body
    >
      <div class="session-dialog-toolbar">
        <el-input
          v-model="sessionSearch"
          clearable
          placeholder="搜索会话、主机或状态"
          :prefix-icon="Search"
        />
        <el-select
          v-model="sessionStatusFilter"
          clearable
          placeholder="全部状态"
        >
          <el-option label="已连接" value="attached" />
          <el-option label="后台运行" value="background" />
          <el-option label="等待认证" value="auth_required" />
          <el-option label="不可达" value="unreachable" />
          <el-option label="已结束" value="ended" />
        </el-select>
        <el-select
          v-model="sessionHostFilter"
          clearable
          filterable
          placeholder="全部主机"
        >
          <el-option
            v-for="host in hosts"
            :key="host.id"
            :label="host.name"
            :value="host.id"
          />
        </el-select>
        <el-button
          :loading="restoringBackground"
          :disabled="!backgroundSessions.length"
          @click="restoreBackgroundSessions"
          >批量恢复</el-button
        >
      </div>
      <div v-if="visibleSessions.length" class="session-dialog-list">
        <article
          v-for="session in visibleSessions"
          :key="session.id"
          class="session-row"
          @click="openManagedSession(session)"
        >
          <span class="status-dot" :class="statusColor(session.status)"></span>
          <div class="host-copy">
            <strong>{{ session.name }}</strong
            ><small
              >{{ session.status }} ·
              {{ session.sessionMode === "normal" ? "普通 SSH" : "tmux"
              }}<template v-if="sessionHost(session)">
                · {{ sessionHost(session)?.name }} ·
                {{ sessionHost(session)?.address }}</template
              >
              · {{ new Date(session.updatedAt).toLocaleString() }}</small
            >
          </div>
          <Archive
            v-if="
              !visibleSessionIDs.has(session.id) && session.status !== 'ended'
            "
            :size="14"
          />
          <el-dropdown trigger="click" @click.stop>
            <button class="icon-btn row-menu">
              <MoreHorizontal :size="16" />
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item @click="openManagedSession(session)"
                  >打开</el-dropdown-item
                ><el-dropdown-item @click="renameSession(session)"
                  >重命名</el-dropdown-item
                ><el-dropdown-item @click="duplicateSession(session)"
                  >复制新开</el-dropdown-item
                >
                <el-dropdown-item @click="togglePin(session)">
                  {{
                    pinnedSessionIDs.includes(session.id)
                      ? "取消置顶"
                      : "置顶会话"
                  }}
                </el-dropdown-item>
                <el-dropdown-item
                  v-if="currentSessionIDs.has(session.id)"
                  @click="moveBackground(tabForSession(session.id)!)"
                  >移到后台</el-dropdown-item
                ><el-dropdown-item divided @click="terminateSession(session)"
                  >终止会话</el-dropdown-item
                >
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </article>
      </div>
      <div v-else class="empty-small">
        <Monitor :size="28" /><span>没有符合条件的会话</span>
      </div>
    </el-dialog>
    <HostDialog
      v-model="hostDialog"
      :host="editingHost"
      :hosts="hosts"
      :credentials="credentials"
      @saved="hostSaved"
    />
    <TmuxInstallDialog
      :model-value="tmuxInstallOpen"
      :host="tmuxInstallHost"
      @update:model-value="setTmuxInstallOpen"
      @retry="retryAfterTmuxInstall"
      @fallback="fallbackToNormalSession"
    />
    <SettingsDrawer
      v-if="!locked"
      v-model="settingsOpen"
      :preferences="preferences"
      :hosts="hosts"
      :credentials="credentials"
      :sessions="activeWorkspaceSessions"
      @preferences="Object.assign(preferences, $event)"
      @logout="logout"
      @insert="sendSnippet($event, false)"
      @execute="sendSnippet($event, true)"
      @batch-execute="sendBatchSnippet"
      @credential-saved="credentialSaved"
      @credential-deleted="credentialDeleted"
    />
    <FileManagerDialog
      v-model="fileManagerOpen"
      :host="fileManagerHost"
      :session-id="fileManagerSessionID"
      :initial-path="fileManagerPath"
    />
    <WebProxyDialog
      v-model="webProxyOpen"
      :hosts="hosts"
      :host="webProxyHost"
      :service="editingWebService"
      @saved="webServiceSaved"
    />
    <AgentDialog
      v-model="agentOpen"
      :host="agentHost"
      :suspended="Boolean(agentTabID && agentTabID !== layout.active)"
    />
    <SessionShareDialog
      v-model="sessionShareOpen"
      :session="sessionShareSession"
    />
    <HostMonitorDialog
      v-model="hostMonitorOpen"
      :host="hostMonitorHost"
    />
    <DockerDialog
      v-model="dockerOpen"
      :host="dockerHost"
      :session-id="dockerSessionID"
      @terminal="openDockerTerminal"
    />
    <GitDialog
      v-model="gitOpen"
      :host="gitHost"
      :session-id="gitSessionID"
      :initial-path="gitPath"
    />
    <Transition name="workspace-lock">
      <section v-if="locked" class="workspace-lock" aria-modal="true" role="dialog">
        <form class="workspace-lock-panel" @submit.prevent="unlockWorkspace">
          <span class="workspace-lock-icon"><LockKeyhole :size="24" /></span>
          <div class="workspace-lock-copy">
            <h1>工作区已锁定</h1>
            <p>{{ auth.user?.username }}</p>
          </div>
          <label for="workspace-unlock-pin">6 位锁屏 PIN</label>
          <el-input
            id="workspace-unlock-pin"
            ref="lockPINInput"
            :model-value="lockPIN"
            type="password"
            maxlength="6"
            inputmode="numeric"
            autocomplete="one-time-code"
            show-password
            :prefix-icon="KeyRound"
            @input="lockError = ''"
            @update:model-value="lockPIN = $event.replace(/\D/g, '').slice(0, 6)"
          />
          <p v-if="lockError" class="workspace-lock-error">{{ lockError }}</p>
          <el-button
            native-type="submit"
            type="primary"
            :loading="unlocking"
            :disabled="lockPIN.length !== 6"
          >
            解锁
          </el-button>
          <button type="button" class="workspace-lock-logout" @click="logout">
            切换账号
          </button>
        </form>
      </section>
    </Transition>
  </main>
</template>
