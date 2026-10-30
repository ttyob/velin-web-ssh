<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { ArrowDown, Clock3, Copy, Eye, Keyboard, KeyboardOff, LoaderCircle, LockKeyhole, Maximize, Minimize, Search, Square, TerminalSquare, WifiOff, X } from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import type { InputInstance } from "element-plus";
import { useRoute } from "vue-router";
import { api, json } from "../api";
import type { TerminalShare } from "../types";
import { terminalFontFamily } from "../types";
import { terminalXtermTheme } from "../themePresets";
import { terminalClipboardFiles } from "../terminalClipboard";
import { handleTerminalKeyEvent, terminalNewlineInput } from "../terminalKeyboard";
import { normalizeTerminalWheelDelta } from "../terminalWheel";

const route = useRoute();
const token = computed(() => String(route.params.token || ""));
const share = ref<TerminalShare>();
const displayName = ref("");
const password = ref("");
const loading = ref(true);
const joining = ref(false);
const connected = ref(false);
const connecting = ref(false);
const controller = ref(false);
const controlPending = ref(false);
const endedMessage = ref("");
const terminalHost = ref<HTMLElement>();
const pageHost = ref<HTMLElement>();
const searchInput = ref<InputInstance>();
const searchOpen = ref(false);
const searchText = ref("");
const searchMissing = ref(false);
const fullscreen = ref(false);
const atBottom = ref(true);
const dimensions = ref("");
const fullscreenAvailable = Boolean(document.fullscreenEnabled);
const terminalTheme = terminalXtermTheme("velin");
let terminal: Terminal | undefined;
let fit: FitAddon | undefined;
let search: SearchAddon | undefined;
let socket: WebSocket | undefined;
let heartbeat: number | undefined;
let resizeObserver: ResizeObserver | undefined;
let terminalViewport: HTMLElement | undefined;
let clientID = "";
let streamID = "";
let streamOffset = 0;
let replayOffset = 0;
let resizeFrame: number | undefined;
let wheelRemainder = 0;
let lastWheelAt = 0;

// tmux attaches on the outer terminal's alternate screen, which has no scrollback,
// so a viewer could never scroll up. Viewers are read-only and cannot drive tmux's
// own history browsing, so keep them on the normal buffer where output accumulates.
const ALTERNATE_SCREEN_MODES = new Set([47, 1047, 1049]);

const canOperate = computed(() => share.value?.permission === "operate" && !share.value?.canManage);
const accessRequired = computed(() => share.value?.active && !share.value?.authorized);
const expiresText = computed(() => share.value ? new Date(share.value.expiresAt).toLocaleString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }) : "");
const statusText = computed(() => loading.value ? "正在载入" : endedMessage.value ? "分享已结束" : accessRequired.value ? "等待加入" : connecting.value ? "正在连接" : connected.value ? "实时连接" : "连接已断开");
const modeText = computed(() => controller.value ? "正在操作" : share.value?.canManage ? "创建者预览" : canOperate.value ? "可申请控制" : "只读观看");

onMounted(async () => {
  await nextTick();
  initializeTerminal();
  document.addEventListener("fullscreenchange", fullscreenChanged);
  await loadShare();
});

onBeforeUnmount(dispose);

function initializeTerminal() {
  if (!terminalHost.value) return;
  terminal = new Terminal({
    cursorBlink: true,
    disableStdin: true,
    fontFamily: terminalFontFamily,
    fontSize: 13,
    lineHeight: 1.15,
    scrollback: 100000,
    scrollSensitivity: 1,
    theme: terminalTheme,
  });
  fit = new FitAddon();
  terminal.loadAddon(fit);
  search = new SearchAddon();
  terminal.loadAddon(search);
  terminal.open(terminalHost.value);
  for (const final of ["h", "l"])
    terminal.parser.registerCsiHandler({ prefix: "?", final }, (params) => {
      // Plain SSH sessions run full-screen programs (vim, less) on the alternate
      // screen themselves; only tmux's outer attach needs to be flattened.
      if (share.value?.sessionMode === "normal") return false;
      const modes = params.flat();
      if (!modes.some((mode) => ALTERNATE_SCREEN_MODES.has(mode))) return false;
      // Keep any other private modes that shared the sequence.
      const rest = modes.filter((mode) => !ALTERNATE_SCREEN_MODES.has(mode));
      if (rest.length) queueMicrotask(() => terminal?.write(`\x1b[?${rest.join(";")}${final}`));
      return true;
    });
  terminal.attachCustomKeyEventHandler((event) => handleTerminalKeyEvent(event, () => {
    if (!controller.value || socket?.readyState !== WebSocket.OPEN) return;
    socket.send(JSON.stringify({ type: "input", data: bytesToBase64(terminalNewlineInput) }));
  }));
  terminal.attachCustomWheelEventHandler(handleTerminalWheel);
  terminal.textarea?.addEventListener("paste", handleTerminalPaste, true);
  terminal.onData((data) => {
    if (!controller.value || socket?.readyState !== WebSocket.OPEN) return;
    socket.send(JSON.stringify({ type: "input", data: bytesToBase64(data) }));
  });
  terminal.onScroll(updateHistoryPosition);
  terminalViewport = terminalHost.value.querySelector<HTMLElement>(".xterm-viewport") || undefined;
  terminalViewport?.addEventListener("scroll", updateHistoryPosition, { passive: true });
  resizeObserver = new ResizeObserver(scheduleResize);
  resizeObserver.observe(terminalHost.value);
  resizeTerminal();
}

// A viewer that is not driving the session always scrolls its own history. Left to
// xterm, the wheel would be forwarded to the application whenever it enables mouse
// reporting, and the read-only input would swallow it.
function handleTerminalWheel(event: WheelEvent) {
  if (!terminal || controller.value || event.deltaY === 0) return true;
  const now = performance.now();
  if (now - lastWheelAt > 180) wheelRemainder = 0;
  lastWheelAt = now;
  const delta = normalizeTerminalWheelDelta(event.deltaY, event.deltaMode, wheelRemainder);
  wheelRemainder = delta.remainder;
  if (delta.lines !== 0) terminal.scrollLines(delta.lines);
  event.preventDefault();
  return false;
}

function handleTerminalPaste(event: ClipboardEvent) {
  if (!terminalClipboardFiles(event.clipboardData).length) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  if (controller.value) ElMessage.info("分享终端暂不支持上传文件，请在主控制台上传");
}

async function loadShare() {
  loading.value = true;
  endedMessage.value = "";
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
  const previous = socket;
  socket = undefined;
  previous?.close();
  clearInterval(heartbeat);
  connected.value = false;
  connecting.value = true;
  controller.value = false;
  controlPending.value = false;
  updateInputMode();
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
    connecting.value = false;
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
      const nextStreamID = String(message.streamID || "");
      if (!streamID || streamID !== nextStreamID || message.truncated) {
        terminal?.reset();
        streamOffset = 0;
      }
      streamID = nextStreamID;
      replayOffset = Number(message.offset) || 0;
      updateInputMode();
      resizeTerminal();
    } else if ((message.type === "replay" || message.type === "output") && message.data) {
      terminal?.write(base64ToBytes(message.data), () => {
        if (socket !== current) return;
        const offset = message.type === "replay" && message.replayFinal ? replayOffset : Number(message.offset);
        if (Number.isFinite(offset) && offset > 0) streamOffset = offset;
      });
    } else if (message.type === "replay_end") {
      streamOffset = replayOffset;
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
      connected.value = false;
      controller.value = false;
      updateInputMode();
    }
  };
  current.onclose = (event) => {
    if (socket !== current) return;
    clearInterval(heartbeat);
    connected.value = false;
    connecting.value = false;
    controller.value = false;
    controlPending.value = false;
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

function updateHistoryPosition() {
  if (terminal) atBottom.value = terminal.buffer.active.viewportY >= terminal.buffer.active.baseY;
}

function scheduleResize() {
  if (resizeFrame !== undefined) cancelAnimationFrame(resizeFrame);
  resizeFrame = requestAnimationFrame(() => {
    resizeFrame = undefined;
    resizeTerminal();
  });
}

function resizeTerminal() {
  if (!terminal || !fit || !terminalHost.value) return;
  try {
    fit.fit();
    dimensions.value = `${terminal.cols} × ${terminal.rows}`;
    if (controller.value && socket?.readyState === WebSocket.OPEN)
      socket.send(JSON.stringify({ type: "resize", rows: terminal.rows, cols: terminal.cols }));
  } catch {}
}

async function toggleSearch() {
  searchOpen.value = !searchOpen.value;
  searchMissing.value = false;
  if (searchOpen.value) {
    await nextTick();
    searchInput.value?.focus();
  } else search?.clearDecorations();
}

function findNext(previous = false) {
  searchMissing.value = false;
  if (!searchText.value) {
    search?.clearDecorations();
    return;
  }
  searchMissing.value = !(previous ? search?.findPrevious(searchText.value) : search?.findNext(searchText.value));
}

async function copySelection() {
  const selected = terminal?.getSelection();
  if (!selected) {
    ElMessage.info("请先在终端中选中文本");
    return;
  }
  try {
    await navigator.clipboard.writeText(selected);
  } catch {
    const input = document.createElement("textarea");
    input.value = selected;
    input.style.cssText = "position:fixed;left:-9999px;top:0";
    document.body.appendChild(input);
    input.select();
    let copied = false;
    try { copied = document.execCommand("copy"); } catch {}
    input.remove();
    terminal?.focus();
    if (!copied) {
      ElMessage.warning("复制失败，请使用浏览器的复制菜单");
      return;
    }
  }
  ElMessage.success("已复制选中内容");
}

function fullscreenChanged() {
  fullscreen.value = document.fullscreenElement === pageHost.value;
  scheduleResize();
}

async function toggleFullscreen() {
  try {
    if (fullscreen.value) await document.exitFullscreen();
    else await pageHost.value?.requestFullscreen();
  } catch { ElMessage.warning("当前浏览器无法进入全屏"); }
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
  terminalViewport?.removeEventListener("scroll", updateHistoryPosition);
  if (resizeFrame !== undefined) cancelAnimationFrame(resizeFrame);
  document.removeEventListener("fullscreenchange", fullscreenChanged);
  const previous = socket;
  socket = undefined;
  previous?.close();
  terminal?.textarea?.removeEventListener("paste", handleTerminalPaste, true);
  terminal?.dispose();
}
</script>

<template>
  <main ref="pageHost" class="share-page">
    <header class="share-header">
      <div class="share-brand">
        <span class="share-brand-mark"><TerminalSquare :size="19" /></span>
        <strong>VelinWebSSH</strong>
      </div>
      <div class="share-heading"><span>终端分享</span><span class="share-heading-divider">/</span><span :title="share?.sessionName">{{ share?.sessionName || "共享终端" }}</span></div>
      <div class="share-header-actions">
        <span class="share-mode" :class="{ controlling: controller }"><component :is="controller ? Keyboard : Eye" :size="14" /><span>{{ modeText }}</span></span>
        <el-button v-if="share?.canManage && share.active" class="share-revoke" size="small" type="danger" plain :icon="Square" @click="revokeShare">中止分享</el-button>
      </div>
    </header>

    <section class="share-terminal-shell" :style="{ '--share-terminal-bg': terminalTheme.background }" aria-label="共享终端">
      <div class="share-terminal-bar">
        <div class="share-connection" role="status" aria-live="polite">
          <span class="share-status-dot" :class="{ live: connected, waiting: connecting || loading, ended: !!endedMessage }" />
          <span>{{ statusText }}</span>
          <span v-if="controller" class="share-control-hint">你正在控制此终端</span>
        </div>
        <div class="share-terminal-tools">
          <el-button
            v-if="canOperate && connected"
            class="share-control-button"
            size="small"
            :type="controller ? 'primary' : 'default'"
            :loading="controlPending"
            :icon="controller ? KeyboardOff : Keyboard"
            @click="toggleControl"
          >{{ controller ? "释放控制" : controlPending ? "等待同意" : "请求控制" }}</el-button>
          <span v-if="canOperate && connected" class="share-tool-divider" />
          <button class="icon-btn" type="button" title="复制选中内容" aria-label="复制选中内容" :disabled="!connected" @click="copySelection"><Copy :size="15" /></button>
          <button class="icon-btn" :class="{ active: searchOpen }" type="button" title="搜索终端" aria-label="搜索终端" :aria-expanded="searchOpen" :disabled="!connected" @click="toggleSearch"><Search :size="15" /></button>
          <button v-if="fullscreenAvailable" class="icon-btn" type="button" :title="fullscreen ? '退出全屏' : '全屏显示'" :aria-label="fullscreen ? '退出全屏' : '全屏显示'" @click="toggleFullscreen"><component :is="fullscreen ? Minimize : Maximize" :size="15" /></button>
        </div>
      </div>
      <form v-if="searchOpen" class="share-search" role="search" @submit.prevent="findNext()" @keydown.esc.prevent="toggleSearch">
        <Search :size="14" />
        <el-input ref="searchInput" v-model="searchText" size="small" placeholder="搜索终端内容" aria-label="搜索终端内容" @input="searchMissing = false" @keydown.enter.shift.prevent="findNext(true)" />
        <span v-if="searchMissing" class="share-search-empty" role="status">未找到</span>
        <button class="icon-btn" type="submit" title="下一个匹配" aria-label="下一个匹配"><ArrowDown :size="15" /></button>
        <button class="icon-btn" type="button" title="关闭搜索" aria-label="关闭搜索" @click="toggleSearch"><X :size="15" /></button>
      </form>
      <div class="share-terminal-stage">
        <div ref="terminalHost" class="share-terminal" />
        <button v-if="!atBottom && connected" class="share-back-bottom" type="button" @click="terminal?.scrollToBottom()"><ArrowDown :size="14" />回到底部</button>
        <div v-if="loading || connecting" class="share-overlay">
          <div class="share-state"><LoaderCircle :size="24" class="share-spinner" /><strong>{{ loading ? "正在载入分享" : "正在连接终端" }}</strong><p>连接后将自动同步终端内容</p></div>
        </div>
        <div v-else-if="endedMessage" class="share-overlay">
          <div class="share-state"><span class="share-state-icon"><WifiOff :size="24" /></span><h1>分享已结束</h1><p>{{ endedMessage }}</p><span class="share-state-help">如需继续访问，请联系分享者。</span></div>
        </div>
        <div v-else-if="accessRequired" class="share-overlay share-access">
          <form class="share-access-panel" @submit.prevent="joinShare">
            <span class="share-access-icon"><component :is="share?.passwordRequired ? LockKeyhole : TerminalSquare" :size="24" /></span>
            <span class="share-access-eyebrow">终端邀请</span>
            <h1>{{ share?.sessionName || "共享终端" }}</h1>
            <p>{{ canOperate ? "加入后可查看实时输出，并向当前控制者申请操作。" : "加入后可实时查看终端输出和历史内容。" }}</p>
            <div class="share-access-fields">
              <label for="share-display-name">你的名称</label>
              <el-input id="share-display-name" v-model="displayName" maxlength="40" placeholder="输入你的名称" autocomplete="name" />
              <template v-if="share?.passwordRequired">
                <label for="share-access-password">访问密码</label>
                <el-input id="share-access-password" v-model="password" type="password" show-password placeholder="输入分享者提供的密码" autocomplete="current-password" />
              </template>
              <el-button type="primary" native-type="submit" :loading="joining">{{ joining ? "正在加入" : "加入终端" }}</el-button>
            </div>
            <div class="share-access-note"><component :is="canOperate ? Keyboard : Eye" :size="13" />{{ canOperate ? "允许申请控制" : "只读访问，不会影响分享者的操作" }}</div>
          </form>
        </div>
        <div v-else-if="!connected" class="share-disconnected" role="status">
          <WifiOff :size="18" /><div><strong>连接已断开</strong><span>已接收的内容仍可查看，请重新连接以继续同步。</span></div><el-button size="small" :icon="TerminalSquare" @click="loadShare">重新连接</el-button>
        </div>
      </div>
    </section>

    <footer class="share-footer">
      <div class="share-footer-left"><TerminalSquare :size="13" /><span>{{ controller ? "可输入命令" : "实时共享终端" }}</span></div>
      <div class="share-footer-right"><span v-if="connected" class="share-dimensions">{{ dimensions }}</span><span v-if="share" class="share-expiry" :title="`分享有效至 ${expiresText}`"><Clock3 :size="12" /><span>有效至 {{ expiresText }}</span></span></div>
    </footer>
  </main>
</template>

<style scoped>
.share-page {
  display: grid;
  grid-template-rows: 48px minmax(0, 1fr) auto;
  width: 100%;
  height: 100%;
  height: 100dvh;
  min-width: 0;
  overflow: hidden;
  color: var(--text);
  background: var(--workspace-bg);
}
.share-page:fullscreen { height: 100%; }
.share-header { display: flex; align-items: center; min-width: 0; gap: 20px; padding: 0 16px; border-bottom: 1px solid var(--line); background: var(--sidebar-bg); }
.share-brand { display: flex; align-items: center; flex: 0 0 auto; gap: 9px; }
.share-brand strong { font-size: 13px; font-weight: 650; letter-spacing: -.2px; }
.share-brand-mark { display: grid; place-items: center; width: 28px; height: 28px; border: 1px solid var(--line-strong); border-radius: var(--radius-sm); color: var(--accent-strong); background: var(--surface-2); }
.share-heading { display: flex; align-items: center; flex: 1; gap: 12px; min-width: 0; font-size: 12px; color: var(--muted); }
.share-heading > span:first-child { flex-shrink: 0; }
.share-heading > span:last-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-soft); }
.share-heading-divider { color: var(--line-strong); }
.share-header-actions { display: flex; align-items: center; flex-shrink: 0; gap: 12px; }
.share-mode { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); font-size: 11px; white-space: nowrap; }
.share-mode.controlling { color: var(--accent-strong); }
.share-revoke { margin: 0; }
.share-terminal-shell { display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden; background: var(--share-terminal-bg); }
.share-terminal-bar { display: flex; align-items: center; justify-content: space-between; flex: 0 0 36px; gap: 12px; padding: 0 9px 0 14px; border-bottom: 1px solid var(--line); color: var(--muted); background: var(--terminal-toolbar-bg); }
.share-connection { display: flex; align-items: center; gap: 8px; min-width: 0; font-size: 11px; white-space: nowrap; }
.share-status-dot { width: 6px; height: 6px; flex: 0 0 6px; border-radius: 50%; background: var(--muted); }
.share-status-dot.live { background: #70c98d; }
.share-status-dot.waiting { background: var(--warning); }
.share-status-dot.ended { background: var(--danger); }
.share-control-hint { padding-left: 9px; margin-left: 3px; border-left: 1px solid var(--line); color: var(--accent-strong); }
.share-terminal-tools { display: flex; align-items: center; gap: 3px; flex-shrink: 0; }
.share-terminal-tools .icon-btn, .share-search .icon-btn { width: 27px; height: 27px; border-radius: var(--radius-sm); }
.share-terminal-tools .icon-btn:disabled { opacity: .35; cursor: default; background: transparent; }
.share-tool-divider { width: 1px; height: 14px; margin: 0 5px; background: var(--line); }
.share-control-button { height: 24px; padding: 0 8px; font-size: 11px; }
.share-search { display: flex; align-items: center; gap: 7px; flex: 0 0 42px; padding: 5px 10px; border-bottom: 1px solid var(--line); color: var(--muted); background: var(--surface); }
.share-search .el-input { max-width: 320px; min-width: 0; }
.share-search-empty { font-size: 11px; color: var(--warning); white-space: nowrap; }
.share-terminal-stage { position: relative; flex: 1; min-width: 0; min-height: 0; overflow: hidden; }
.share-terminal { width: 100%; height: 100%; }
.share-terminal :deep(.xterm) { height: 100%; padding: 8px 0 6px 10px; }
.share-back-bottom { position: absolute; bottom: 16px; left: 50%; z-index: 1; display: flex; align-items: center; gap: 6px; transform: translateX(-50%); padding: 7px 12px; border: 1px solid var(--line-strong); border-radius: var(--radius-sm); color: var(--text); background: var(--surface-2); font-size: 12px; cursor: pointer; box-shadow: var(--shadow-control); }
.share-back-bottom:hover { border-color: var(--accent-border); background: var(--surface-hover); }
.share-overlay { position: absolute; inset: 0; z-index: 2; display: grid; place-items: center; overflow: auto; padding: 24px; background: var(--workspace-bg); }
.share-state { display: flex; flex-direction: column; align-items: center; gap: 14px; max-width: 420px; text-align: center; color: var(--muted); }
.share-state > strong { font-size: 14px; color: var(--text-soft); }
.share-state h1 { margin: 0; font-size: 18px; font-weight: 600; color: var(--text); }
.share-state p { margin: 0; max-width: 100%; overflow-wrap: anywhere; font-size: 13px; line-height: 1.7; }
.share-state-help { font-size: 12px; }
.share-state-icon { display: grid; place-items: center; width: 52px; height: 52px; margin-bottom: 4px; border: 1px solid var(--line); border-radius: 12px; background: var(--surface); }
.share-spinner { color: var(--accent); animation: share-spin 1s linear infinite; }
@keyframes share-spin { to { transform: rotate(360deg); } }
.share-access-panel { width: min(380px, 100%); display: flex; flex-direction: column; align-items: flex-start; padding: 28px; border: 1px solid var(--line); border-radius: 10px; background: var(--surface); box-shadow: 0 8px 32px var(--theme-shadow); }
.share-access-icon { display: grid; place-items: center; width: 44px; height: 44px; margin-bottom: 24px; border: 1px solid var(--accent-border); border-radius: 10px; color: var(--accent-strong); background: var(--accent-surface); }
.share-access-eyebrow { margin-bottom: 7px; color: var(--muted); font-size: 11px; }
.share-access-panel h1 { max-width: 100%; margin: 0; overflow-wrap: anywhere; font-size: 20px; line-height: 1.5; letter-spacing: -.3px; font-weight: 600; }
.share-access-panel > p { margin: 10px 0 24px; color: var(--muted); font-size: 12px; line-height: 1.8; }
.share-access-fields { display: grid; gap: 8px; width: 100%; }
.share-access-fields label { color: var(--text-soft); font-size: 12px; }
.share-access-fields label:not(:first-child) { margin-top: 8px; }
.share-access-fields .el-button { width: 100%; margin: 12px 0 0; }
.share-access-note { display: flex; align-items: center; gap: 6px; margin-top: 18px; color: var(--muted); font-size: 11px; line-height: 1.6; }
.share-access-note svg { flex-shrink: 0; }
.share-disconnected { position: absolute; top: 16px; left: 50%; z-index: 2; display: flex; align-items: center; gap: 12px; width: max-content; max-width: calc(100% - 32px); padding: 14px 16px; transform: translateX(-50%); border: 1px solid var(--line-strong); border-radius: var(--radius-md); color: var(--muted); background: var(--surface); box-shadow: var(--shadow-float); }
.share-disconnected > svg { flex-shrink: 0; color: var(--warning); }
.share-disconnected > div { display: grid; gap: 5px; min-width: 0; font-size: 12px; }
.share-disconnected strong { color: var(--text); font-weight: 500; }
.share-disconnected span { line-height: 1.6; }
.share-disconnected .el-button { flex-shrink: 0; }
.share-footer { display: flex; align-items: center; justify-content: space-between; gap: 16px; min-width: 0; min-height: 28px; padding: 5px 14px max(5px, env(safe-area-inset-bottom)); border-top: 1px solid var(--line); background: var(--dialog-footer-bg); color: var(--muted); font-size: 10px; }
.share-footer-left, .share-footer-right { display: flex; align-items: center; gap: 12px; min-width: 0; }
.share-footer-left > svg { margin-right: -6px; }
.share-footer-left > span { white-space: nowrap; }
.share-expiry { display: inline-flex; align-items: center; gap: 5px; }
.share-dimensions { white-space: nowrap; font-family: var(--font-mono); }
.share-expiry { min-width: 0; }
.share-expiry svg { flex-shrink: 0; }
.share-expiry > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
@media (max-width: 680px) {
  .share-page { grid-template-rows: auto minmax(0, 1fr) auto; }
  .share-header { flex-wrap: wrap; gap: 0 12px; padding: 10px 12px 0; }
  .share-brand { order: 0; }
  .share-brand strong { font-size: 12px; }
  .share-brand-mark { width: 25px; height: 25px; }
  .share-header-actions { order: 1; margin-left: auto; gap: 8px; min-height: 28px; }
  .share-heading { order: 2; flex: 1 1 100%; gap: 8px; height: 36px; font-size: 11px; }
  .share-heading > span:first-child { font-size: 10px; }
  .share-mode { font-size: 10px; gap: 4px; }
  .share-revoke { padding: 0 7px; font-size: 10px; }
  .share-terminal-bar { padding: 0 8px 0 12px; gap: 6px; }
  .share-control-hint { display: none; }
  .share-terminal-tools { gap: 1px; }
  .share-tool-divider { margin: 0 3px; }
  .share-terminal :deep(.xterm) { padding-left: 8px; }
  .share-overlay { padding: 16px; }
  .share-access-panel { padding: 24px; }
  .share-footer { flex-wrap: wrap; gap: 5px 12px; padding-left: 12px; padding-right: 12px; }
  .share-footer-left, .share-footer-right { gap: 8px; }
  .share-footer-right { margin-left: auto; }
  .share-dimensions { display: none; }
  .share-expiry { font-size: 9px; }
  .share-disconnected { flex-wrap: wrap; gap: 10px; padding: 12px; }
  .share-disconnected > div { flex: 1; }
  .share-disconnected .el-button { margin-left: 28px; }
}
@media (max-width: 380px) {
  .share-mode > span { max-width: 64px; overflow: hidden; text-overflow: ellipsis; }
}
@media (max-height: 560px) {
  .share-overlay { place-items: start center; }
  .share-state { align-self: center; }
  .share-access-panel { padding: 20px 24px; }
  .share-access-icon { display: none; }
  .share-access-panel > p { margin-bottom: 16px; }
}
@media (prefers-reduced-motion: reduce) { .share-spinner { animation: none; } }
</style>
