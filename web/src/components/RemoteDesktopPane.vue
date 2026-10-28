<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  ClipboardCopy,
  ClipboardPaste,
  Fullscreen,
  Keyboard,
  RefreshCw,
} from "@lucide/vue";
import RFB from "@novnc/novnc";
import Guacamole from "guacamole-common-js";
import { ApiError, api, json } from "../api";
import { getNativeClipboardText, setNativeClipboardText } from "../rdp";
import type { Host } from "../types";

interface DesktopSession {
  id: string;
  hostID: string;
  protocol: "vnc" | "rdp";
  websocketPath: string;
  password?: string;
  readOnly: boolean;
}

type DesktopStatus = "connecting" | "connected" | "disconnected" | "error";
type ClipboardStatus = "waiting" | "synced" | "blocked";

const props = defineProps<{ host: Host; visible: boolean }>();
const emit = defineEmits<{ status: [status: DesktopStatus] }>();

const root = ref<HTMLElement>();
const viewport = ref<HTMLElement>();
const surface = ref<HTMLElement>();
const status = ref<DesktopStatus>("connecting");
const statusMessage = ref("正在连接");
const scaleMode = ref<"fit" | "actual">("fit");
const remoteClipboard = ref("");
const clipboardStatus = ref<ClipboardStatus>("waiting");
const connectionInfo = ref("");
const downloadBytes = ref(0);
const uploadBytes = ref(0);
const downloadRate = ref(0);
const uploadRate = ref(0);
const busy = ref(false);
let rfb: any;
let guacClient: any;
let guacTunnel: any;
let guacMouse: any;
let guacKeyboard: any;
let disposed = false;
let rdpHealthTimer: number | undefined;
let rdpPendingFrameAt: number | undefined;
let rdpLatestFrame: string | undefined;
let rdpAcknowledgedFrame: string | undefined;
let lastRDPWidth = 0;
let lastRDPHeight = 0;
const rdpFrameTimeout = 20_000;
const pressedRDPKeys = new Set<number>();
const clipboardPasteKeys = new Set<number>();
let resizeObserver: ResizeObserver | undefined;
let reconnectTimer: number | undefined;
let reconnectAttempt = 0;
let connectStartedAt = 0;
let connectionLatency = 0;
let connectGeneration = 0;
let trafficTimer: number | undefined;
let trafficSampleAt = 0;
let trafficSampleDownload = 0;
let trafficSampleUpload = 0;

const protocolLabel = computed(() => props.host.protocol.toUpperCase());
const connected = computed(() => status.value === "connected");
const clipboardStatusLabel = computed(() => {
  if (clipboardStatus.value === "synced") return "剪贴板已同步";
  if (clipboardStatus.value === "blocked") return "剪贴板需授权";
  return "剪贴板待同步";
});

function websocketURL(path: string) {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}${path}`;
}

function setStatus(next: typeof status.value, message: string) {
  status.value = next;
  statusMessage.value = message;
  emit("status", next);
}

function desktopPixelRatio() {
  return Math.max(1, Math.min(2.5, window.devicePixelRatio || 1));
}

function viewportPixels() {
  const bounds = viewport.value?.getBoundingClientRect();
  const ratio = desktopPixelRatio();
  return {
    width: Math.max(320, Math.round((bounds?.width || 1280) * ratio)),
    height: Math.max(200, Math.round((bounds?.height || 720) * ratio)),
  };
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** index;
  return `${amount >= 100 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
}

function formatRate(value: number) {
  return `${formatBytes(value)}/s`;
}

function updateTrafficRate() {
  const now = performance.now();
  const elapsed = trafficSampleAt ? (now - trafficSampleAt) / 1000 : 0;
  if (elapsed > 0) {
    downloadRate.value = Math.max(
      0,
      (downloadBytes.value - trafficSampleDownload) / elapsed,
    );
    uploadRate.value = Math.max(
      0,
      (uploadBytes.value - trafficSampleUpload) / elapsed,
    );
  }
  trafficSampleAt = now;
  trafficSampleDownload = downloadBytes.value;
  trafficSampleUpload = uploadBytes.value;
}

function resetTraffic() {
  if (trafficTimer !== undefined) window.clearInterval(trafficTimer);
  trafficTimer = undefined;
  downloadBytes.value = 0;
  uploadBytes.value = 0;
  downloadRate.value = 0;
  uploadRate.value = 0;
  trafficSampleAt = 0;
  trafficSampleDownload = 0;
  trafficSampleUpload = 0;
}

function startTraffic() {
  resetTraffic();
  trafficSampleAt = performance.now();
  trafficTimer = window.setInterval(updateTrafficRate, 1000);
}

function stopTraffic() {
  if (trafficTimer !== undefined) window.clearInterval(trafficTimer);
  trafficTimer = undefined;
  updateTrafficRate();
}

function recordTraffic(direction: "download" | "upload", bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return;
  if (direction === "download") downloadBytes.value += bytes;
  else uploadBytes.value += bytes;
}

function guacamoleInstructionBytes(values: unknown[]) {
  const instruction = values
    .map((value) => {
      const text = String(value);
      return `${text.length}.${text}`;
    })
    .join(",") + ";";
  return new TextEncoder().encode(instruction).byteLength;
}

function instrumentGuacamoleTunnel(tunnel: any) {
  const sendMessage = tunnel.sendMessage.bind(tunnel);
  tunnel.sendMessage = (...values: unknown[]) => {
    if (guacTunnel !== tunnel) return;
    recordTraffic("upload", guacamoleInstructionBytes(values));
    if (values[0] === "sync") {
      rdpAcknowledgedFrame = String(values[1]);
      rdpPendingFrameAt = rdpAcknowledgedFrame === rdpLatestFrame
        ? undefined
        : Date.now();
    }
    return sendMessage(...values);
  };
  const oninstruction = tunnel.oninstruction;
  tunnel.oninstruction = (opcode: string, parameters: string[]) => {
    if (guacTunnel !== tunnel) return;
    recordTraffic("download", guacamoleInstructionBytes([opcode, ...parameters]));
    if (opcode === "sync") {
      rdpLatestFrame = String(Number(parameters[0]));
      if (rdpLatestFrame !== rdpAcknowledgedFrame && rdpPendingFrameAt === undefined)
        rdpPendingFrameAt = Date.now();
    }
    return oninstruction?.call(tunnel, opcode, parameters);
  };
}

function recoverRDP(client: any, message: string) {
  if (disposed || guacClient !== client) return;
  disconnect();
  scheduleReconnect(message);
}

function checkRDPHealth() {
  if (!guacClient || !props.visible || document.hidden) return;
  if (guacTunnel?.state === Guacamole.Tunnel.State.CLOSED) {
    recoverRDP(guacClient, "RDP 连接已断开");
  } else if (
    rdpPendingFrameAt !== undefined &&
    Date.now() - rdpPendingFrameAt >= rdpFrameTimeout
  ) {
    // A live WebSocket can still have a blocked display queue after WebView
    // suspension. Track frame acknowledgements, not changes in screen pixels.
    recoverRDP(guacClient, "RDP 画面响应超时");
  }
}

function resumeDesktop() {
  if (
    disposed ||
    busy.value ||
    !props.visible ||
    document.hidden ||
    props.host.protocol !== "rdp"
  )
    return;
  checkRDPHealth();
  if (reconnectTimer !== undefined) {
    clearReconnect();
    void connectDesktop(true);
  } else if (guacTunnel?.isConnected()) {
    guacTunnel.sendMessage("nop");
    resizeDisplay();
  }
}

function clearReconnect() {
  if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer);
  reconnectTimer = undefined;
}

function releasePressedKeys() {
  for (const keysym of pressedRDPKeys) guacClient?.sendKeyEvent(0, keysym);
  pressedRDPKeys.clear();
  clipboardPasteKeys.clear();
  guacKeyboard?.reset();
}

function scheduleReconnect(message = "连接已断开") {
  if (disposed) return;
  if (reconnectTimer !== undefined) return;
  if (reconnectAttempt >= 5) {
    setStatus("error", "连接失败，请手动重连");
    return;
  }
  reconnectAttempt++;
  const delay = Math.min(30000, 1000 * 2 ** (reconnectAttempt - 1));
  setStatus("connecting", `${message}，${Math.ceil(delay / 1000)} 秒后重连`);
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = undefined;
    void connectDesktop(true);
  }, delay);
}

async function createSession(secret = "") {
  const size = viewportPixels();
  return api<DesktopSession>("/api/desktop/sessions", {
    method: "POST",
    body: json({
      hostID: props.host.id,
      secret,
      width: size.width,
      height: size.height,
      dpi: Math.max(72, Math.min(240, Math.round(96 * desktopPixelRatio()))),
    }),
  });
}

async function requestSession(): Promise<DesktopSession> {
  try {
    return await createSession();
  } catch (error) {
    if (!(error instanceof ApiError) || error.body.code !== "credential_required")
      throw error;
    const { value } = await ElMessageBox.prompt(
      `输入 ${props.host.name} 的 ${protocolLabel.value} 密码`,
      "临时凭据",
      {
        inputType: "password",
        confirmButtonText: "连接",
        cancelButtonText: "取消",
        inputValidator: (input) => Boolean(input) || "请输入密码",
      },
    );
    return createSession(value);
  }
}

function disconnect() {
  connectGeneration++;
  stopTraffic();
  window.clearInterval(rdpHealthTimer);
  rdpHealthTimer = undefined;
  rdpPendingFrameAt = undefined;
  rdpLatestFrame = undefined;
  rdpAcknowledgedFrame = undefined;
  lastRDPWidth = 0;
  lastRDPHeight = 0;
  releasePressedKeys();
  const previousRFB = rfb;
  const previousClient = guacClient;
  const previousTunnel = guacTunnel;
  rfb = undefined;
  guacClient = undefined;
  if (guacKeyboard) {
    guacKeyboard.onkeydown = null;
    guacKeyboard.onkeyup = null;
  }
  if (previousClient) {
    previousClient.onstatechange = null;
    previousClient.onerror = null;
    previousClient.onclipboard = null;
    previousClient.getDisplay().onresize = null;
  }
  if (previousTunnel) {
    previousTunnel.onstatechange = null;
    previousTunnel.onerror = null;
  }
  try {
    previousRFB?.disconnect();
  } catch {}
  try {
    previousClient?.disconnect();
  } catch {}
  guacTunnel = undefined;
  guacMouse = undefined;
  pressedRDPKeys.clear();
  clipboardPasteKeys.clear();
  if (surface.value) surface.value.replaceChildren();
}

async function connectDesktop(automatic = false) {
  if (disposed || busy.value) return;
  clearReconnect();
  if (!automatic) reconnectAttempt = 0;
  busy.value = true;
  disconnect();
  const generation = connectGeneration;
  connectStartedAt = performance.now();
  setStatus("connecting", `正在连接 ${protocolLabel.value}`);
  clipboardStatus.value = "waiting";
  connectionInfo.value = "";
  resetTraffic();
  try {
    const session = await requestSession();
    if (generation !== connectGeneration || !surface.value) return;
    if (session.protocol === "vnc") connectVNC(session);
    else connectRDP(session);
  } catch (error: any) {
    if (disposed || generation !== connectGeneration) return;
    if (error !== "cancel" && error !== "close") {
      setStatus("error", error instanceof Error ? error.message : "连接失败");
      scheduleReconnect("连接失败");
    } else setStatus("disconnected", "连接已取消");
  } finally {
    busy.value = false;
  }
}

function connectVNC(session: DesktopSession) {
  if (!surface.value) return;
  rfb = new RFB(surface.value, websocketURL(session.websocketPath), {
    credentials: { password: session.password || "" },
  });
  const client = rfb;
  session.password = "";
  rfb.viewOnly = session.readOnly;
  rfb.scaleViewport = scaleMode.value === "fit";
  rfb.resizeSession = scaleMode.value === "fit" && !session.readOnly;
  rfb.background = "#0d1014";
  rfb.addEventListener("connect", () => {
    if (rfb !== client) return;
    reconnectAttempt = 0;
    setStatus("connected", "已连接");
  });
  rfb.addEventListener("disconnect", (event: any) => {
    if (rfb !== client) return;
    const clean = Boolean(event.detail?.clean);
    setStatus(clean ? "disconnected" : "error", clean ? "已断开" : "连接意外中断");
    if (!clean) scheduleReconnect("连接中断");
  });
  rfb.addEventListener("securityfailure", (event: any) =>
    setStatus("error", event.detail?.reason || "VNC 认证失败"),
  );
  rfb.addEventListener("clipboard", (event: any) =>
    receiveRemoteClipboard(event.detail?.text || ""),
  );
}

function connectRDP(session: DesktopSession) {
  if (!surface.value) return;
  const tunnel = new Guacamole.WebSocketTunnel(websocketURL(session.websocketPath));
  guacTunnel = tunnel;
  guacClient = new Guacamole.Client(tunnel);
  instrumentGuacamoleTunnel(tunnel);
  startTraffic();
  const client = guacClient;
  // Guacamole.Client reports protocol errors, but does not forward tunnel
  // errors or closure. Without these handlers a timed-out socket leaves a
  // frozen display which still appears connected.
  tunnel.onerror = (error: any) =>
    recoverRDP(client, error?.message || "RDP 连接超时");
  tunnel.onstatechange = (state: number) => {
    if (state === Guacamole.Tunnel.State.CLOSED)
      recoverRDP(client, "RDP 连接中断");
  };
  rdpHealthTimer = window.setInterval(checkRDPHealth, 2_000);
  const display = guacClient.getDisplay();
  surface.value.appendChild(display.getElement());
  display.onresize = () => resizeDisplay();
  guacClient.onstatechange = (state: number) => {
    if (state === Guacamole.Client.State.CONNECTED) {
      if (guacClient !== client) return;
      reconnectAttempt = 0;
      connectionLatency = Math.round(performance.now() - connectStartedAt);
      setStatus("connected", "已连接");
      resizeDisplay();
    } else if (state === Guacamole.Client.State.DISCONNECTED && guacClient === client) {
      recoverRDP(client, "RDP 连接中断");
    }
  };
  guacClient.onerror = (error: any) => {
    recoverRDP(client, error?.message || "RDP 连接失败");
  };
  guacClient.onclipboard = (stream: any, mimetype: string) => {
    const reader = new Guacamole.StringReader(stream);
    let value = "";
    reader.ontext = (text: string) => (value += text);
    reader.onend = () => {
      if (mimetype.startsWith("text/")) receiveRemoteClipboard(value);
    };
  };
  if (!session.readOnly) {
    guacMouse = new Guacamole.Mouse(display.getElement());
    guacMouse.onEach(["mousedown", "mousemove", "mouseup"], (event: any) => {
      if (guacClient === client) client.sendMouseState(event.state, true);
    });
    guacKeyboard ||= new Guacamole.Keyboard(document);
    guacKeyboard.onkeydown = (keysym: number) => {
      if (!props.visible || !connected.value || guacClient !== client) return true;
      if (
        (keysym === 0x76 || keysym === 0x56) &&
        [0xffe3, 0xffe4].some((modifier) => pressedRDPKeys.has(modifier))
      ) {
        if (!clipboardPasteKeys.has(keysym)) {
          clipboardPasteKeys.add(keysym);
          void pasteFromKeyboard(keysym);
        }
        return false;
      }
      pressedRDPKeys.add(keysym);
      guacClient?.sendKeyEvent(1, keysym);
      return false;
    };
    guacKeyboard.onkeyup = (keysym: number) => {
      if (clipboardPasteKeys.delete(keysym)) return;
      if (!pressedRDPKeys.delete(keysym)) return;
      guacClient?.sendKeyEvent(0, keysym);
    };
  }
  guacClient.connect();
  resizeDisplay();
}

function resizeDisplay() {
  if (rfb) {
    rfb.scaleViewport = scaleMode.value === "fit";
    rfb.resizeSession = scaleMode.value === "fit" && !props.host.desktopReadOnly;
    return;
  }
  if (!guacClient || !viewport.value) return;
  const display = guacClient.getDisplay();
  const bounds = viewport.value.getBoundingClientRect();
  const width = display.getWidth();
  const height = display.getHeight();
  const scale =
    scaleMode.value === "fit" && width && height
      ? Math.min(bounds.width / width, bounds.height / height)
      : 1;
  display.scale(Math.max(0.1, scale || 1));
  if (width && height) {
    connectionInfo.value = `${width}×${height} · ${Math.round(96 * desktopPixelRatio())} DPI · ${props.host.rdpQuality === "smooth" ? "流畅" : "清晰"}${connectionLatency ? ` · ${connectionLatency} ms` : ""}`;
  }
  if (connected.value && !props.host.desktopReadOnly) {
    if (!props.visible || bounds.width <= 0 || bounds.height <= 0) return;
    const size = viewportPixels();
    if (size.width === lastRDPWidth && size.height === lastRDPHeight) return;
    lastRDPWidth = size.width;
    lastRDPHeight = size.height;
    guacClient.sendSize(size.width, size.height);
  }
}

async function receiveRemoteClipboard(value: string) {
  remoteClipboard.value = value;
  try {
    await writeClipboardText(value);
    clipboardStatus.value = "synced";
  } catch {
    clipboardStatus.value = "blocked";
  }
}

function sendRDPClipboard(value: string) {
  if (!guacClient || !connected.value || props.host.desktopReadOnly) return false;
  const writer = new Guacamole.StringWriter(
    guacClient.createClipboardStream("text/plain"),
  );
  writer.sendText(value);
  writer.sendEnd();
  return true;
}

async function clipboardText() {
  try {
    const nativeText = await getNativeClipboardText();
    if (nativeText !== undefined) return nativeText;
  } catch {}
  try {
    if (navigator.clipboard?.readText)
      return await navigator.clipboard.readText();
  } catch {
  }
  const { value } = await ElMessageBox.prompt("输入要发送到远程桌面的文本", "发送剪贴板", {
    confirmButtonText: "发送",
    cancelButtonText: "取消",
    inputType: "textarea",
  });
  return value;
}

async function pasteRemote() {
  if (!connected.value || props.host.desktopReadOnly) return;
  try {
    const text = await clipboardText();
    if (rfb) rfb.clipboardPasteFrom(text);
    else sendRDPClipboard(text);
    ElMessage.success("剪贴板已发送");
  } catch {}
}

async function pasteFromKeyboard(keysym: number) {
  try {
    const text = await clipboardText();
    if (!sendRDPClipboard(text) || !guacClient) return;
    guacClient.sendKeyEvent(1, keysym);
    guacClient.sendKeyEvent(0, keysym);
  } catch {}
}

function isEditableTarget(target: EventTarget | null) {
  const element = target instanceof HTMLElement ? target : undefined;
  return Boolean(
    element?.closest("input, textarea, select, [contenteditable='true']"),
  );
}

function handleDesktopPaste(event: ClipboardEvent) {
  if (
    !props.visible ||
    !connected.value ||
    !guacClient ||
    props.host.desktopReadOnly ||
    isEditableTarget(event.target) ||
    (event.target instanceof Node && root.value && !root.value.contains(event.target))
  )
    return;
  const text = event.clipboardData?.getData("text/plain");
  if (!text || !sendRDPClipboard(text)) return;
  event.preventDefault();
}

async function copyRemote() {
  if (!remoteClipboard.value) return ElMessage.info("远程剪贴板暂无文本");
  try {
    await writeClipboardText(remoteClipboard.value);
    ElMessage.success("远程剪贴板已复制");
  } catch {
    await ElMessageBox.alert(remoteClipboard.value, "远程剪贴板", {
      confirmButtonText: "关闭",
    });
  }
}

async function writeClipboardText(value: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return;
    }
  } catch {}

  if (await setNativeClipboardText(value)) return;

  const input = document.createElement("textarea");
  input.value = value;
  input.setAttribute("readonly", "true");
  input.style.position = "fixed";
  input.style.left = "-9999px";
  input.style.top = "0";
  document.body.appendChild(input);
  input.select();
  input.setSelectionRange(0, input.value.length);
  const copied = document.execCommand("copy");
  input.remove();
  if (!copied) throw new Error("浏览器禁止访问剪贴板");
}

function sendCtrlAltDelete() {
  if (!connected.value || props.host.desktopReadOnly) return;
  if (rfb) return rfb.sendCtrlAltDel();
  if (!guacClient) return;
  const keys = [0xffe3, 0xffe9, 0xffff];
  keys.forEach((key) => guacClient.sendKeyEvent(1, key));
  [...keys].reverse().forEach((key) => guacClient.sendKeyEvent(0, key));
}

function sendCtrlAltEnd() {
  if (!connected.value || props.host.desktopReadOnly || !guacClient) return;
  const keys = [0xffe3, 0xffe9, 0xff57];
  keys.forEach((key) => guacClient.sendKeyEvent(1, key));
  [...keys].reverse().forEach((key) => guacClient.sendKeyEvent(0, key));
}

async function toggleFullscreen() {
  if (!root.value) return;
  if (document.fullscreenElement) await document.exitFullscreen();
  else await root.value.requestFullscreen();
  await nextTick();
  resizeDisplay();
}

watch(scaleMode, resizeDisplay);
watch(
  () => props.visible,
  (visible) => {
    if (visible) return void nextTick(() => {
      resumeDesktop();
      resizeDisplay();
    });
    releasePressedKeys();
  },
);
onMounted(() => {
  resizeObserver = new ResizeObserver(resizeDisplay);
  if (viewport.value) resizeObserver.observe(viewport.value);
  document.addEventListener("paste", handleDesktopPaste, true);
  document.addEventListener("fullscreenchange", resizeDisplay);
  document.addEventListener("visibilitychange", resumeDesktop);
  window.addEventListener("blur", releasePressedKeys);
  window.addEventListener("focus", resumeDesktop);
  window.addEventListener("pageshow", resumeDesktop);
  window.addEventListener("online", resumeDesktop);
  void connectDesktop();
});
onBeforeUnmount(() => {
  disposed = true;
  resizeObserver?.disconnect();
  document.removeEventListener("paste", handleDesktopPaste, true);
  document.removeEventListener("fullscreenchange", resizeDisplay);
  document.removeEventListener("visibilitychange", resumeDesktop);
  window.removeEventListener("blur", releasePressedKeys);
  window.removeEventListener("focus", resumeDesktop);
  window.removeEventListener("pageshow", resumeDesktop);
  window.removeEventListener("online", resumeDesktop);
  clearReconnect();
  disconnect();
});
</script>

<template>
  <section ref="root" class="remote-desktop-pane">
    <header class="remote-desktop-toolbar">
      <div class="desktop-connection">
        <span class="desktop-status" :class="status"></span>
        <strong>{{ host.name }}</strong>
        <small>
          {{ protocolLabel }} · {{ statusMessage }}
          <span v-if="connectionInfo"> · {{ connectionInfo }}</span>
        </small>
        <span
          v-if="protocolLabel === 'RDP'"
          class="clipboard-status"
          :class="clipboardStatus"
        >{{ clipboardStatusLabel }}</span>
        <span
          v-if="protocolLabel === 'RDP'"
          class="traffic-info"
          :title="`下行 ${formatRate(downloadRate)} · 上行 ${formatRate(uploadRate)} · 总计 ${formatBytes(downloadBytes + uploadBytes)}`"
        >
          ↓{{ formatRate(downloadRate) }} ↑{{ formatRate(uploadRate) }} · {{ formatBytes(downloadBytes + uploadBytes) }}
        </span>
      </div>
      <div class="desktop-tools">
        <el-segmented
          v-model="scaleMode"
          size="small"
          :options="[
            { label: '适应', value: 'fit' },
            { label: '1:1', value: 'actual' },
          ]"
        />
        <el-tooltip :content="clipboardStatus === 'blocked' ? '点击授权并复制远程剪贴板' : '复制远程剪贴板'">
          <button class="icon-btn" :disabled="!remoteClipboard" @click="copyRemote">
            <ClipboardCopy :size="16" />
          </button>
        </el-tooltip>
        <el-tooltip content="发送本地剪贴板">
          <button class="icon-btn" :disabled="!connected || host.desktopReadOnly" @click="pasteRemote">
            <ClipboardPaste :size="16" />
          </button>
        </el-tooltip>
        <el-tooltip content="发送 Ctrl+Alt+Del">
          <button class="icon-btn" :disabled="!connected || host.desktopReadOnly" @click="sendCtrlAltDelete">
            <Keyboard :size="16" />
          </button>
        </el-tooltip>
        <el-tooltip content="发送 Ctrl+Alt+End">
          <button class="icon-btn" :disabled="!connected || host.desktopReadOnly" @click="sendCtrlAltEnd">
            <Keyboard :size="16" />
          </button>
        </el-tooltip>
        <el-tooltip content="重新连接">
          <button class="icon-btn" :disabled="busy" @click="connectDesktop()">
            <RefreshCw :size="16" />
          </button>
        </el-tooltip>
        <el-tooltip content="全屏">
          <button class="icon-btn" @click="toggleFullscreen">
            <Fullscreen :size="16" />
          </button>
        </el-tooltip>
      </div>
    </header>
    <div ref="viewport" class="remote-desktop-viewport" :class="`scale-${scaleMode}`">
      <div ref="surface" class="remote-desktop-surface"></div>
      <div v-if="status !== 'connected'" class="desktop-status-layer">
        <span class="desktop-status-spinner" :class="status"></span>
        <strong>{{ statusMessage }}</strong>
        <button v-if="status === 'error' || status === 'disconnected'" @click="connectDesktop()">
          <RefreshCw :size="15" />重新连接
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.remote-desktop-pane {
  display: grid;
  grid-template-rows: 44px minmax(0, 1fr);
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  background: var(--field-bg);
}
.remote-desktop-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 0 10px 0 14px;
  border-bottom: 1px solid var(--border, #2b3138);
  background: var(--panel, #171b20);
}
.desktop-connection,
.desktop-tools { display: flex; align-items: center; min-width: 0; }
.desktop-connection { gap: 8px; }
.desktop-connection strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }
.desktop-connection small { color: var(--muted, #8f9aa5); font-size: 11px; white-space: nowrap; }
.clipboard-status { color: var(--muted); font-size: 10px; white-space: nowrap; }
.clipboard-status.synced { color: #67c99a; }
.clipboard-status.blocked { color: #e0ad57; }
.traffic-info { color: var(--muted); font-size: 10px; white-space: nowrap; font-variant-numeric: tabular-nums; }
.desktop-tools { flex: none; gap: 5px; }
.desktop-status { width: 7px; height: 7px; flex: none; border-radius: 50%; background: #7e8992; }
.desktop-status.connected { background: #58c98d; box-shadow: 0 0 0 3px rgba(88, 201, 141, 0.12); }
.desktop-status.connecting { background: #e0ad57; }
.desktop-status.error { background: #ef7169; }
.remote-desktop-viewport { position: relative; min-width: 0; min-height: 0; overflow: auto; background: var(--workspace-bg); }
.remote-desktop-surface { width: 100%; height: 100%; display: grid; place-items: center; outline: none; }
.remote-desktop-surface :deep(canvas) { max-width: none; max-height: none; }
.desktop-status-layer { position: absolute; inset: 0; display: grid; place-content: center; justify-items: center; gap: 14px; background: rgba(9, 11, 14, 0.9); color: var(--text); }
.desktop-status-layer button { display: inline-flex; align-items: center; gap: 7px; height: 34px; padding: 0 12px; border: 1px solid var(--line-strong); border-radius: 6px; background: var(--surface-2); color: var(--text); cursor: pointer; }
.desktop-status-spinner { width: 24px; height: 24px; border: 2px solid var(--line-strong); border-top-color: #68c99a; border-radius: 50%; }
.desktop-status-spinner.connecting { animation: desktop-spin 0.8s linear infinite; }
.desktop-status-spinner.error { border-color: #ef7169; }
.desktop-status-spinner.disconnected { border-color: #7e8992; }
@keyframes desktop-spin { to { transform: rotate(360deg); } }
@media (max-width: 760px) {
  .remote-desktop-pane { grid-template-rows: auto minmax(0, 1fr); }
  .remote-desktop-toolbar { height: auto; min-height: 44px; flex-wrap: wrap; padding-block: 6px; }
  .desktop-connection,
  .desktop-tools { width: 100%; }
  .desktop-connection small,
  .traffic-info { display: none; }
  .desktop-tools { justify-content: flex-end; overflow-x: auto; }
}
</style>
