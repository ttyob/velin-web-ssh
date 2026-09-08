<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Eye, Keyboard, KeyboardOff, Radio, Square, WifiOff } from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { useRoute } from "vue-router";
import { api, json } from "../api";
import type { TerminalShare } from "../types";

const route = useRoute();
const token = computed(() => String(route.params.token || ""));
const share = ref<TerminalShare>();
const displayName = ref("");
const password = ref("");
const loading = ref(true);
const joining = ref(false);
const connected = ref(false);
const controller = ref(false);
const controlPending = ref(false);
const endedMessage = ref("");
const terminalHost = ref<HTMLElement>();
let terminal: Terminal | undefined;
let fit: FitAddon | undefined;
let socket: WebSocket | undefined;
let heartbeat: number | undefined;
let resizeObserver: ResizeObserver | undefined;
let clientID = "";
let streamID = "";
let streamOffset = 0;

const canOperate = computed(() => share.value?.permission === "operate" && !share.value?.canManage);
const accessRequired = computed(() => share.value?.active && !share.value?.authorized);
const expiresText = computed(() => share.value ? new Date(share.value.expiresAt).toLocaleString() : "");

onMounted(async () => {
  await nextTick();
  initializeTerminal();
  await loadShare();
});

onBeforeUnmount(dispose);

function initializeTerminal() {
  if (!terminalHost.value) return;
  terminal = new Terminal({
    cursorBlink: true,
    disableStdin: true,
    fontFamily: '"JetBrains Mono", "SFMono-Regular", Consolas, monospace',
    fontSize: 14,
    lineHeight: 1.2,
    scrollback: 10000,
    theme: {
      background: "#0b0e0d",
      foreground: "#dce5df",
      cursor: "#64d98b",
      selectionBackground: "#315b45",
      black: "#121715",
      red: "#ff6b70",
      green: "#64d98b",
      yellow: "#e8c35a",
      blue: "#68a7ff",
      magenta: "#d694e8",
      cyan: "#63c8cf",
      white: "#dce5df",
    },
  });
  fit = new FitAddon();
  terminal.loadAddon(fit);
  terminal.open(terminalHost.value);
  terminal.onData((data) => {
    if (!controller.value || socket?.readyState !== WebSocket.OPEN) return;
    socket.send(JSON.stringify({ type: "input", data: bytesToBase64(data) }));
  });
  resizeObserver = new ResizeObserver(resizeTerminal);
  resizeObserver.observe(terminalHost.value);
  resizeTerminal();
}

async function loadShare() {
  loading.value = true;
  try {
    share.value = await api<TerminalShare>(
      `/api/public/shares/${encodeURIComponent(token.value)}`,
    );
    if (!share.value.active) endedMessage.value = "分享已中止或过期";
    else if (share.value.authorized) connectSocket();
  } catch (error) {
    endedMessage.value = error instanceof Error ? error.message : "分享链接不存在";
  } finally {
    loading.value = false;
  }
}

async function joinShare() {
  if (joining.value) return;
  joining.value = true;
  try {
    share.value = await api<TerminalShare>(
      `/api/public/shares/${encodeURIComponent(token.value)}/access`,
      {
        method: "POST",
        body: json({ displayName: displayName.value, password: password.value }),
      },
    );
    password.value = "";
    connectSocket();
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "加入分享失败");
  } finally {
    joining.value = false;
  }
}

function connectSocket() {
  if (!share.value?.active) return;
  socket?.close();
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const query = new URLSearchParams();
  if (streamID) {
    query.set("stream", streamID);
    query.set("offset", String(streamOffset));
  }
  const current = new WebSocket(
    `${protocol}//${location.host}/ws/shares/${encodeURIComponent(token.value)}${query.size ? `?${query}` : ""}`,
  );
  socket = current;
  current.onopen = () => {
    if (socket !== current) return;
    connected.value = true;
    heartbeat = window.setInterval(() => {
      if (current.readyState === WebSocket.OPEN)
        current.send(JSON.stringify({ type: "ping" }));
    }, 5000);
  };
  current.onmessage = (event) => {
    if (socket !== current) return;
    const message = JSON.parse(String(event.data));
    if (message.type === "hello") {
      clientID = String(message.clientID || "");
      controller.value = Boolean(clientID && message.controller === clientID);
      streamID = String(message.streamID || "");
      streamOffset = Number(message.offset) || 0;
      terminal?.reset();
      updateInputMode();
      resizeTerminal();
    } else if ((message.type === "replay" || message.type === "output") && message.data) {
      terminal?.write(base64ToBytes(message.data));
      const offset = Number(message.offset);
      if (Number.isFinite(offset) && offset > 0) streamOffset = offset;
    } else if (message.type === "controller") {
      controller.value = message.controller === clientID;
      controlPending.value = false;
      updateInputMode();
      resizeTerminal();
    } else if (message.type === "control_granted") {
      controller.value = true;
      controlPending.value = false;
      updateInputMode();
      resizeTerminal();
      terminal?.focus();
    } else if (message.type === "control_pending") {
      controlPending.value = true;
    } else if (message.type === "control_denied") {
      controller.value = false;
      controlPending.value = false;
      updateInputMode();
      ElMessage.warning("当前控制者拒绝了接管请求");
    } else if (message.type === "status" && message.status !== "attached") {
      endedMessage.value = message.message || "共享终端已断开";
    }
  };
  current.onclose = (event) => {
    if (socket !== current) return;
    clearInterval(heartbeat);
    connected.value = false;
    controller.value = false;
    updateInputMode();
    if (event.code === 4003) endedMessage.value = event.reason || "分享已中止或过期";
  };
}

function toggleControl() {
  if (!canOperate.value || socket?.readyState !== WebSocket.OPEN) return;
  socket.send(JSON.stringify({ type: controller.value ? "release_control" : "request_control" }));
  if (!controller.value) controlPending.value = true;
}

function updateInputMode() {
  if (terminal) terminal.options.disableStdin = !controller.value;
}

function resizeTerminal() {
  if (!terminal || !fit || !terminalHost.value) return;
  try {
    fit.fit();
    if (controller.value && socket?.readyState === WebSocket.OPEN)
      socket.send(JSON.stringify({ type: "resize", rows: terminal.rows, cols: terminal.cols }));
  } catch {}
}

async function revokeShare() {
  if (!share.value?.canManage) return;
  try {
    await ElMessageBox.confirm("在线访客会立即断开，且此链接永久失效。", "中止会话分享", {
      confirmButtonText: "立即中止",
      cancelButtonText: "取消",
      type: "warning",
    });
    await api(`/api/session-shares/${share.value.id}`, { method: "DELETE" });
    share.value.active = false;
    endedMessage.value = "分享已中止";
    socket?.close();
  } catch (error) {
    if (error !== "cancel" && error !== "close")
      ElMessage.error(error instanceof Error ? error.message : "中止分享失败");
  }
}

function bytesToBase64(data: string) {
  const bytes = new TextEncoder().encode(data);
  let value = "";
  for (const byte of bytes) value += String.fromCharCode(byte);
  return btoa(value);
}

function base64ToBytes(data: string) {
  const value = atob(data);
  const bytes = new Uint8Array(value.length);
  for (let index = 0; index < value.length; index++) bytes[index] = value.charCodeAt(index);
  return bytes;
}

function dispose() {
  clearInterval(heartbeat);
  resizeObserver?.disconnect();
  socket?.close();
  terminal?.dispose();
}
</script>

<template>
  <main class="share-page">
    <header class="share-header">
      <div class="share-brand"><Radio :size="20" /><strong>Velin Live</strong></div>
      <div v-if="share" class="share-status">
        <span><i :class="{ live: connected }" />{{ connected ? "实时连接" : "未连接" }}</span>
        <span>{{ share.permission === "operate" ? "可操作" : "仅观看" }}</span>
        <span>有效至 {{ expiresText }}</span>
        <span v-if="share.record">正在录屏</span>
      </div>
      <el-button v-if="share?.canManage && share.active" type="danger" plain :icon="Square" @click="revokeShare">中止分享</el-button>
    </header>

    <section class="share-terminal-shell">
      <div class="share-terminal-bar">
        <div>
          <strong>{{ share?.sessionName || "共享终端" }}</strong>
          <small v-if="connected">{{ controller ? "你正在操作" : canOperate ? "当前为观看状态" : "只读实时观看" }}</small>
        </div>
        <el-button
          v-if="canOperate && connected"
          size="small"
          :type="controller ? 'primary' : 'default'"
          :loading="controlPending"
          :icon="controller ? KeyboardOff : Keyboard"
          @click="toggleControl"
        >{{ controller ? "释放控制" : "请求控制" }}</el-button>
        <span v-else-if="connected" class="view-mode"><Eye :size="15" />仅观看</span>
      </div>
      <div ref="terminalHost" class="share-terminal" />
      <div v-if="loading" class="share-overlay">正在载入分享</div>
      <div v-else-if="endedMessage" class="share-overlay share-ended">
        <WifiOff :size="28" /><strong>{{ endedMessage }}</strong>
      </div>
      <form v-else-if="accessRequired" class="share-overlay share-access" @submit.prevent="joinShare">
        <div class="share-access-panel">
          <Eye :size="24" />
          <h1>{{ share?.sessionName || "共享终端" }}</h1>
          <el-input v-model="displayName" maxlength="40" placeholder="你的名称" autocomplete="name" />
          <el-input v-if="share?.passwordRequired" v-model="password" type="password" show-password placeholder="访问密码" autocomplete="current-password" />
          <el-button type="primary" native-type="submit" :loading="joining">进入终端</el-button>
        </div>
      </form>
    </section>
  </main>
</template>

<style scoped>
.share-page { min-height: 100vh; display: grid; grid-template-rows: 58px minmax(0, 1fr); color: #dce5df; background: #090c0b; }
.share-header { display: flex; align-items: center; gap: 18px; padding: 0 18px; border-bottom: 1px solid #28302d; background: #111614; }
.share-brand { display: flex; align-items: center; gap: 8px; color: #64d98b; white-space: nowrap; }
.share-status { display: flex; align-items: center; gap: 14px; min-width: 0; color: #89958f; font-size: 12px; flex: 1; }
.share-status span { white-space: nowrap; }
.share-status i { display: inline-block; width: 7px; height: 7px; margin-right: 6px; border-radius: 50%; background: #59615e; }
.share-status i.live { background: #64d98b; box-shadow: 0 0 0 3px rgb(100 217 139 / 12%); }
.share-terminal-shell { position: relative; display: grid; grid-template-rows: 44px minmax(0, 1fr); margin: 14px; min-height: 420px; overflow: hidden; border: 1px solid #28302d; border-radius: 6px; background: #0b0e0d; }
.share-terminal-bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 0 12px; border-bottom: 1px solid #222927; background: #121715; }
.share-terminal-bar div { display: flex; align-items: baseline; gap: 10px; min-width: 0; }
.share-terminal-bar small, .view-mode { color: #89958f; font-size: 12px; }
.view-mode { display: flex; align-items: center; gap: 6px; }
.share-terminal { min-width: 0; min-height: 0; padding: 8px; }
.share-overlay { position: absolute; inset: 44px 0 0; z-index: 2; display: grid; place-items: center; background: #0b0e0d; color: #89958f; }
.share-ended { align-content: center; gap: 10px; }
.share-access { padding: 20px; }
.share-access-panel { width: min(340px, 100%); display: grid; gap: 12px; }
.share-access-panel > svg { color: #64d98b; }
.share-access-panel h1 { margin: 0 0 4px; font-size: 20px; letter-spacing: 0; }
@media (max-width: 760px) { .share-header { gap: 10px; padding: 0 10px; } .share-status span:nth-child(n+3) { display: none; } .share-terminal-shell { margin: 8px; min-height: 360px; } .share-terminal-bar small { display: none; } }
</style>
