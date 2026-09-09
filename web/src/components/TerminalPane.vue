<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from "vue";
import { Terminal } from "@xterm/xterm";
import { CanvasAddon } from "@xterm/addon-canvas";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import QrcodeVue from "qrcode.vue";
import {
  ArrowDown,
  Circle,
  CircleStop,
  Copy,
  ExternalLink,
  Search,
  Eye,
  Keyboard,
  KeyboardOff,
  LoaderCircle,
  Unplug,
  Wifi,
  X,
} from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { ApiError, api, json } from "../api";
import { terminalFontFamily } from "../types";
import type { Preferences, TerminalSession } from "../types";
import { terminalXtermTheme } from "../themePresets";
import { reconnectDelay } from "../reconnect";
import {
  terminalOutputSettleDelay,
  type TerminalAttentionEvent,
} from "../terminalAttention";
import { normalizeTerminalWheelDelta } from "../terminalWheel";

const props = withDefaults(
  defineProps<{
    session: TerminalSession;
    preferences: Preferences;
    visible: boolean;
    closable?: boolean;
    mobileCtrl?: boolean;
    mobileAlt?: boolean;
  }>(),
  { closable: false, mobileCtrl: false, mobileAlt: false },
);
const emit = defineEmits<{
  status: [id: string, status: string, message?: string];
  title: [id: string, title: string];
  directory: [id: string, path: string];
  conversation: [id: string, active: boolean];
  attention: [id: string, event: TerminalAttentionEvent];
  close: [];
  modifiersUsed: [];
}>();
const container = ref<HTMLElement>();
const status = ref(props.session.status);
const statusMessage = ref(props.session.lastError || "");
const connected = ref(false);
const terminalLatency = ref<number>();
const recording = ref<{ id: string; status: string; bytes?: number }>();
const recordingBusy = ref(false);
const controller = ref(false);
const searchOpen = ref(false);
const searchText = ref("");
const bellActive = ref(false);
const hasNewOutput = ref(false);
const historyLocked = ref(false);
const remoteHistorySize = ref(0);
const remoteHistoryPosition = ref(0);
const linkPreviewOpen = ref(false);
const linkPreviewURL = ref("");
const linkPreviewHost = computed(() => {
  try {
    return new URL(linkPreviewURL.value).host;
  } catch {
    return "";
  }
});
let terminal: Terminal | undefined;
let fit: FitAddon | undefined;
let search: SearchAddon | undefined;
let mediaRecorder: MediaRecorder | undefined;
let recordingStream: MediaStream | undefined;
let recordingChunks: Blob[] = [];
let socket: WebSocket | undefined;
let observer: ResizeObserver | undefined;
let reconnectTimer: number | undefined;
let reconnectAttempts = 0;
let heartbeatTimer: number | undefined;
let latencyPingAt = 0;
let disposed = false;
let clientID = "";
const reconnectKey = terminalReconnectKey();
const initialTerminalDimensions = storedTerminalDimensions();
let streamID = "";
let streamOffset = 0;
let replayTargetStreamID = "";
let replayTargetOffset = 0;
let replayPreviewedOffset = 0;
let terminalInitialized = false;
let replaying = false;
let replayResumesCurrentTerminal = false;
let replayResetApplied = false;
let replayWasTruncated = false;
let replayPreviewTerminal: Terminal | undefined;
let replayPreviewHost: HTMLElement | undefined;
let replayPreviewData: Uint8Array | undefined;
let previewSwapFrame: number | undefined;
let previewTailReconnect = false;
let forceTailReplayReset = false;
let seedReplayFromPreview = false;
let replaySeedPending = false;
let replayFinishPending: WebSocket | undefined;
let pageIsHiding = false;
let controlPromptOpen = false;
let controlRequestPending = false;
let pendingControlInput = "";
let pasting = false;
let terminalTextarea: HTMLTextAreaElement | undefined;
let terminalTitle = "";
let conversationMode = false;
let transcriptModeForced = false;
let connectionRecoveryOpen = false;
let lastConnectionNotice = "";
let outputSettleTimer: number | undefined;
let resizeFrame: number | undefined;
let resizeSettleTimer: number | undefined;
let resizeSocketTimer: number | undefined;
let resizeApplyFrame: number | undefined;
let resizeReleaseFrame: number | undefined;
let resizeReleaseTimer: number | undefined;
let resizeSnapshot: HTMLElement | undefined;
let resizeSnapshotDeadline = 0;
let resizeFollowingBottom = false;
let resizeAwaitingRemoteOutput = false;
let resizeRemoteOutputSeen = false;
let pendingWrites = 0;
let resizeAfterWrite = false;
let scrollFrame: number | undefined;
let smoothScrolling = false;
const remoteHistoryMode = ref(false);
let historyTransitionSnapshot: HTMLElement | undefined;
let historyTransitionTimer: number | undefined;
let historyTransitionFrame: number | undefined;
let historyTransitionDeadline = 0;
let historyDragPointer: number | undefined;
let historyDragOffset = 12;
let historyPositionFrame: number | undefined;
let historySendTimer: number | undefined;
let historyAckTimer: number | undefined;
let historyRefreshTimer: number | undefined;
let pendingHistoryPosition = 0;
let historyRequestPosition = 0;
let historyRequestSequence = 0;
let historyRequestInFlight = 0;
let lastHistoryRequestAt = 0;
let lastHistoryErrorAt = 0;
let lastSentCols = 0;
let lastSentRows = 0;
let wheelRemainder = 0;
let lastWheelAt = 0;

const statusLabel = computed(
  () =>
    ({
      attached: "已连接",
      background: "后台运行",
      reconnecting: "重连中",
      auth_required: "等待认证",
      unreachable: "不可达",
      ended: "已结束",
      ownership_error: "所有权异常",
      creating: "连接中",
      host_key_required: "等待确认",
    })[status.value] || status.value,
);
const latencyTone = computed(() => {
  if (terminalLatency.value === undefined) return "";
  if (terminalLatency.value <= 80) return "good";
  if (terminalLatency.value <= 200) return "warn";
  return "bad";
});
const showRemoteHistoryScrollbar = computed(
  () => props.session.sessionMode === "tmux" && remoteHistorySize.value > 0,
);
const remoteHistoryThumbStyle = computed(() => {
	const positionRatio = remoteHistorySize.value
		? remoteHistoryPosition.value / remoteHistorySize.value
		: 1;
	const visibleRows = Math.max(1, terminal?.rows || 24);
	const visibleRatio = visibleRows / (remoteHistorySize.value + visibleRows);
	return {
		"--history-position-ratio": Math.max(0, Math.min(1, positionRatio)),
		"--history-visible-ratio": Math.max(0, Math.min(1, visibleRatio)),
	};
});

function bytesToBase64(data: string) {
  const bytes = new TextEncoder().encode(data);
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

function terminalReconnectKey() {
  const storageKey = `velin-terminal-reconnect:${props.session.id}`;
  try {
    const existing = sessionStorage.getItem(storageKey);
    if (existing) return existing;
    const created = globalThis.crypto?.randomUUID?.() || "";
    if (created) sessionStorage.setItem(storageKey, created);
    return created;
  } catch {
    return "";
  }
}

function storedTerminalDimensions() {
  const fallback = { cols: 120, rows: 30 };
  try {
    const value = JSON.parse(
      sessionStorage.getItem(`velin-terminal-size:${props.session.id}`) || "null",
    );
    const cols = Number(value?.cols);
    const rows = Number(value?.rows);
    if (cols >= 2 && cols <= 1000 && rows >= 2 && rows <= 500)
      return { cols, rows };
  } catch {}
  return fallback;
}

function rememberTerminalDimensions(cols: number, rows: number) {
  if (cols < 2 || rows < 2) return;
  try {
    sessionStorage.setItem(
      `velin-terminal-size:${props.session.id}`,
      JSON.stringify({ cols, rows }),
    );
  } catch {}
}
function base64ToBytes(data: string) {
  const binary = atob(data);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}

function isAtBottom() {
  if (!terminal) return true;
  const buffer = terminal.buffer.active;
  return buffer.baseY === buffer.viewportY;
}

function handleTerminalWheel(event: WheelEvent) {
	if (!terminal || event.deltaY === 0) return true;
	const buffer = terminal.buffer.active;
	// Normal SSH sessions have no remote transcript for alternate-screen apps.
	// In tmux mode, keep upward wheel input out of the TUI so xterm does not
	// turn one gesture into a burst of cursor keys.
	if (
		buffer.type === "alternate" &&
		props.session.sessionMode !== "tmux" &&
		!remoteHistoryMode.value
	)
		return true;
	const scrollingUp = event.deltaY < 0;
	const viewingHistory = buffer.viewportY < buffer.baseY;
  // Full-screen TUIs can enable mouse tracking and consume wheel events as
  // input. Keep local scrollback reachable while the user reviews history.
	if (
		scrollingUp ||
		viewingHistory ||
		remoteHistoryMode.value
	) {
		const now = performance.now();
		if (now - lastWheelAt > 180) wheelRemainder = 0;
		lastWheelAt = now;
		const normalized = normalizeTerminalWheelDelta(
			event.deltaY,
			event.deltaMode,
			wheelRemainder,
		);
		wheelRemainder = normalized.remainder;
		const atLocalTop = buffer.viewportY === 0;
		if (normalized.lines !== 0) {
			if (
				remoteHistoryMode.value ||
				(scrollingUp &&
					atLocalTop &&
					remoteHistorySize.value > 0 &&
					controller.value &&
					socket?.readyState === WebSocket.OPEN)
			) {
				if (!remoteHistoryMode.value) {
					captureHistoryTransitionSnapshot();
					remoteHistoryMode.value = true;
					historyLocked.value = false;
					terminal.scrollToBottom();
				}
				sendRemoteHistoryScroll(normalized.lines);
			} else {
				terminal.scrollLines(normalized.lines);
			}
		}
		event.preventDefault();
    event.stopPropagation();
    return false;
  }
	return true;
}

function sendRemoteHistoryScroll(lines: number) {
	if (socket?.readyState !== WebSocket.OPEN || !controller.value) return;
	const next = Math.max(
		0,
		Math.min(remoteHistorySize.value, remoteHistoryPosition.value + lines),
	);
	remoteHistoryPosition.value = next;
	remoteHistoryMode.value = next < remoteHistorySize.value;
	sendRemoteHistoryPosition(next, lines === 0);
}

function leaveRemoteHistoryMode() {
	if (!remoteHistoryMode.value) return;
	disposeHistoryTransitionSnapshot();
	remoteHistoryMode.value = false;
	remoteHistoryPosition.value = remoteHistorySize.value;
	sendRemoteHistoryScroll(0);
}

function updateRemoteHistorySize(size: number) {
  size = Math.max(0, size);
  const followedBottom =
    !remoteHistoryMode.value ||
    remoteHistoryPosition.value >= remoteHistorySize.value;
  remoteHistorySize.value = size;
  if (followedBottom) remoteHistoryPosition.value = size;
  else remoteHistoryPosition.value = Math.min(remoteHistoryPosition.value, size);
}

function scheduleHistoryStateRefresh() {
  clearTimeout(historyRefreshTimer);
  historyRefreshTimer = window.setTimeout(() => {
    if (socket?.readyState === WebSocket.OPEN)
      socket.send(JSON.stringify({ type: "history_state" }));
  }, 600);
}

function sendRemoteHistoryPosition(position: number, immediate = false) {
	pendingHistoryPosition = Math.round(position);
	const send = () => {
		historyPositionFrame = undefined;
		if (
			socket?.readyState !== WebSocket.OPEN ||
			!controller.value ||
			pendingHistoryPosition === historyRequestPosition
		)
			return;
		const elapsed = performance.now() - lastHistoryRequestAt;
		if (!immediate && elapsed < 32) {
			clearTimeout(historySendTimer);
			historySendTimer = window.setTimeout(
				() => sendRemoteHistoryPosition(pendingHistoryPosition),
				32 - elapsed,
			);
			return;
		}
		historyRequestPosition = pendingHistoryPosition;
		historyRequestSequence++;
		historyRequestInFlight = historyRequestSequence;
		lastHistoryRequestAt = performance.now();
		socket.send(
			JSON.stringify({
				type: "scroll_history_to",
				position: historyRequestPosition,
				historySize: remoteHistorySize.value,
				sequence: historyRequestSequence,
			}),
		);
		clearTimeout(historyAckTimer);
		const requestSequence = historyRequestSequence;
		historyAckTimer = window.setTimeout(() => {
			if (historyRequestInFlight !== requestSequence) return;
			historyRequestInFlight = 0;
			historyRequestPosition = -1;
			sendRemoteHistoryPosition(pendingHistoryPosition, true);
		}, 1500);
	};
	if (immediate) {
		clearTimeout(historySendTimer);
		if (historyPositionFrame !== undefined)
			cancelAnimationFrame(historyPositionFrame);
		historyPositionFrame = undefined;
		send();
	}
	else if (historyPositionFrame === undefined)
		historyPositionFrame = requestAnimationFrame(send);
}

function handleRemoteHistoryPosition(message: Record<string, unknown>) {
	const sequence = Number(message.sequence) || 0;
	if (sequence && sequence < historyRequestInFlight) return;
	clearTimeout(historyAckTimer);
	historyRequestInFlight = 0;
	const size = Number(message.historySize);
	if (Number.isFinite(size) && size >= 0) updateRemoteHistorySize(size);
	const actual = Math.max(
		0,
		Math.min(remoteHistorySize.value, Number(message.position) || 0),
	);
	if (message.error) {
		remoteHistoryPosition.value = actual;
		remoteHistoryMode.value = actual < remoteHistorySize.value;
		if (performance.now() - lastHistoryErrorAt > 3000) {
			lastHistoryErrorAt = performance.now();
			ElMessage.warning(`历史定位失败：${String(message.error)}`);
		}
		scheduleHistoryTransitionRelease(120);
	} else if (
		historyDragPointer === undefined &&
		pendingHistoryPosition === historyRequestPosition
	) {
		remoteHistoryPosition.value = actual;
		remoteHistoryMode.value = actual < remoteHistorySize.value;
		scheduleHistoryTransitionRelease(120);
	}
	if (pendingHistoryPosition !== historyRequestPosition)
		sendRemoteHistoryPosition(pendingHistoryPosition);
}

function remoteHistoryThumbHeight(track: HTMLElement) {
	return (
		track.querySelector<HTMLElement>(".terminal-history-scrollbar-thumb")
			?.offsetHeight || 24
	);
}

function updateRemoteHistoryFromPointer(
  event: PointerEvent,
  track: HTMLElement,
) {
	const rect = track.getBoundingClientRect();
	const thumbHeight = remoteHistoryThumbHeight(track);
	const available = Math.max(1, rect.height - thumbHeight);
  const offset = Math.max(
    0,
    Math.min(available, event.clientY - rect.top - historyDragOffset),
  );
  const position = Math.round(
    (offset / available) * remoteHistorySize.value,
  );
	remoteHistoryPosition.value = position;
	remoteHistoryMode.value = position < remoteHistorySize.value;
	historyLocked.value = false;
	sendRemoteHistoryPosition(position);
}

function startRemoteHistoryDrag(event: PointerEvent) {
  if (!controller.value) return requestControl();
  const track = event.currentTarget as HTMLElement;
	const rect = track.getBoundingClientRect();
	const thumbHeight = remoteHistoryThumbHeight(track);
	const available = Math.max(1, rect.height - thumbHeight);
  const thumbTop =
    available *
    (remoteHistorySize.value
      ? remoteHistoryPosition.value / remoteHistorySize.value
      : 1);
	const localY = event.clientY - rect.top;
	historyDragOffset =
		localY >= thumbTop && localY <= thumbTop + thumbHeight
			? localY - thumbTop
			: thumbHeight / 2;
  historyDragPointer = event.pointerId;
  track.setPointerCapture(event.pointerId);
  updateRemoteHistoryFromPointer(event, track);
  event.preventDefault();
}

function moveRemoteHistoryDrag(event: PointerEvent) {
  if (historyDragPointer !== event.pointerId) return;
  updateRemoteHistoryFromPointer(event, event.currentTarget as HTMLElement);
}

function finishRemoteHistoryDrag(event: PointerEvent) {
  if (historyDragPointer !== event.pointerId) return;
	const track = event.currentTarget as HTMLElement;
	updateRemoteHistoryFromPointer(event, track);
	sendRemoteHistoryPosition(remoteHistoryPosition.value, true);
  if (track.hasPointerCapture(event.pointerId))
    track.releasePointerCapture(event.pointerId);
  historyDragPointer = undefined;
}

function jumpRemoteHistory(position: number) {
  remoteHistoryPosition.value = Math.max(
    0,
    Math.min(remoteHistorySize.value, position),
  );
  remoteHistoryMode.value = remoteHistoryPosition.value < remoteHistorySize.value;
  historyLocked.value = false;
  sendRemoteHistoryPosition(remoteHistoryPosition.value, true);
}

function captureHistoryTransitionSnapshot() {
	if (historyTransitionSnapshot || !terminal?.element || !container.value) return;
	const snapshot = document.createElement("div");
	snapshot.className = "terminal-history-transition";
	snapshot.style.backgroundColor =
		terminalXtermTheme(props.preferences.terminalTheme).background ||
		props.preferences.background;
	const clone = terminal.element.cloneNode(true) as HTMLElement;
	clone.querySelectorAll("textarea").forEach((element) => element.remove());
	clone.removeAttribute("tabindex");
	clone.style.width = "100%";
	clone.style.height = "100%";
	// cloneNode does not copy canvas pixels, so copy xterm's rendered layers
	// before placing the snapshot above the live terminal.
	const sourceCanvases = terminal.element.querySelectorAll<HTMLCanvasElement>(
		"canvas",
	);
	const cloneCanvases = clone.querySelectorAll<HTMLCanvasElement>("canvas");
	sourceCanvases.forEach((sourceCanvas, index) => {
		const cloneCanvas = cloneCanvases[index];
		if (!cloneCanvas) return;
		cloneCanvas.width = sourceCanvas.width;
		cloneCanvas.height = sourceCanvas.height;
		cloneCanvas.getContext("2d")?.drawImage(sourceCanvas, 0, 0);
	});
	const sourceViewport = terminal.element.querySelector<HTMLElement>(
		".xterm-viewport",
	);
	const cloneViewport = clone.querySelector<HTMLElement>(".xterm-viewport");
	const scrollTop = sourceViewport?.scrollTop || 0;
	const scrollLeft = sourceViewport?.scrollLeft || 0;
	snapshot.appendChild(clone);
	container.value.appendChild(snapshot);
	if (cloneViewport) {
		cloneViewport.scrollTop = scrollTop;
		cloneViewport.scrollLeft = scrollLeft;
	}
	historyTransitionSnapshot = snapshot;
	historyTransitionDeadline = performance.now() + 900;
	scheduleHistoryTransitionRelease(900);
}

function disposeHistoryTransitionSnapshot() {
	if (historyTransitionFrame !== undefined)
		cancelAnimationFrame(historyTransitionFrame);
	clearTimeout(historyTransitionTimer);
	historyTransitionFrame = undefined;
	historyTransitionTimer = undefined;
	historyTransitionSnapshot?.remove();
	historyTransitionSnapshot = undefined;
	historyTransitionDeadline = 0;
}

function scheduleHistoryTransitionRelease(delay: number) {
	if (!historyTransitionSnapshot) return;
	clearTimeout(historyTransitionTimer);
	const remaining = Math.max(0, historyTransitionDeadline - performance.now());
	historyTransitionTimer = window.setTimeout(() => {
		historyTransitionTimer = undefined;
		historyTransitionFrame = requestAnimationFrame(() => {
			historyTransitionFrame = requestAnimationFrame(
				disposeHistoryTransitionSnapshot,
			);
		});
	}, Math.min(delay, remaining));
}

function resumeOutputFollow(smooth = false) {
	leaveRemoteHistoryMode();
	historyLocked.value = false;
  hasNewOutput.value = false;
  if (!smooth || !terminal) {
    terminal?.scrollToBottom();
    return;
  }
  if (scrollFrame !== undefined) cancelAnimationFrame(scrollFrame);
  const start = terminal.buffer.active.viewportY;
  const duration = 180;
  const started = performance.now();
  smoothScrolling = true;
  const step = (now: number) => {
    if (!terminal) return;
    const target = terminal.buffer.active.baseY;
    const progress = Math.min(1, (now - started) / duration);
    const eased = 1 - Math.pow(1 - progress, 3);
    terminal.scrollToLine(Math.round(start + (target - start) * eased));
    if (progress < 1 && terminal.buffer.active.viewportY < target) {
      scrollFrame = requestAnimationFrame(step);
      return;
    }
    terminal.scrollToBottom();
    scrollFrame = undefined;
    smoothScrolling = false;
  };
  scrollFrame = requestAnimationFrame(step);
}

function writeTerminal(data: Uint8Array, callback?: () => void) {
  if (!terminal) return callback?.();
  const resizingAtBottom = Boolean(resizeSnapshot && resizeFollowingBottom);
  if (!resizingAtBottom && (historyLocked.value || !isAtBottom())) {
    historyLocked.value = true;
    hasNewOutput.value = true;
  }
  // xterm keeps ydisp aligned with ybase when already at the bottom and
  // preserves the viewport when the user has scrolled up.
  pendingWrites++;
  terminal.write(data, () => {
    updateConversationMode();
    pendingWrites = Math.max(0, pendingWrites - 1);
    callback?.();
    if (resizeSnapshot) {
      if (resizeFollowingBottom) {
        historyLocked.value = false;
        hasNewOutput.value = false;
        terminal?.scrollToBottom();
      }
      if (resizeAwaitingRemoteOutput) {
        resizeAwaitingRemoteOutput = false;
        resizeRemoteOutputSeen = true;
      }
      if (resizeRemoteOutputSeen) releaseResizeSnapshot(100);
    }
    if (!pendingWrites && resizeAfterWrite) {
      resizeAfterWrite = false;
      resize();
    }
  });
}

function updateConversationMode() {
  if (!terminal) return;
  const titleMatch = /(?:^|[\s/])(codex|claude|aider|gemini|opencode|goose|cursor-agent)(?:$|\s)/i.test(
    terminalTitle.trim(),
  );
  const activeScreen = terminal.buffer.active.type === "alternate";
  const detected = transcriptModeForced || titleMatch || activeScreen;
  if (detected === conversationMode) return;
  conversationMode = detected;
  emit("conversation", props.session.id, detected);
}

function forceTranscriptMode() {
  if (transcriptModeForced) return;
  transcriptModeForced = true;
  if (!conversationMode) {
    conversationMode = true;
    emit("conversation", props.session.id, true);
  }
}

function resetTerminalForReplay() {
  if (replayResetApplied || replayResumesCurrentTerminal) return;
  terminal?.reset();
  replayResetApplied = true;
}

function disposeReplayPreview() {
  if (previewSwapFrame !== undefined) cancelAnimationFrame(previewSwapFrame);
  previewSwapFrame = undefined;
  replayPreviewTerminal?.dispose();
  replayPreviewTerminal = undefined;
  replayPreviewHost?.remove();
  replayPreviewHost = undefined;
}

function showReplayPreview(data: Uint8Array) {
  if (!terminal || !container.value || replayResumesCurrentTerminal) return;
  disposeReplayPreview();
  replayPreviewData = data;
  const host = document.createElement("div");
  host.className = "terminal-replay-preview";
  host.style.backgroundColor = props.preferences.background;
  container.value.appendChild(host);
  const preview = new Terminal({
    cols: terminal.cols,
    rows: terminal.rows,
    cursorBlink: false,
    cursorStyle: props.preferences.cursorStyle,
    disableStdin: true,
    fontSize: props.preferences.fontSize,
    lineHeight: props.preferences.lineHeight,
    fontFamily: terminalFontFamily,
    fontWeight: props.preferences.fontWeight,
    letterSpacing: props.preferences.letterSpacing,
    theme: terminalXtermTheme(props.preferences.terminalTheme),
    scrollback: 0,
    smoothScrollDuration: 0,
  });
  replayPreviewHost = host;
  replayPreviewTerminal = preview;
  preview.open(host);
  preview.write(data, () => preview.scrollToBottom());
  // The opaque preview host is attached before the main terminal is reset, so
  // the browser never paints an empty main-buffer frame during full replay.
  resetTerminalForReplay();
}

function writeReplayPreviewOutput(data: Uint8Array) {
  if (!replaying || !replayPreviewTerminal) return;
  replayPreviewTerminal.write(data, () => replayPreviewTerminal?.scrollToBottom());
}

function isReplayProtocolResponse(data: string) {
  if (!replaying) return false;
  return (
    /^\x1b\[(?:\?1;2c|\?6c|>0;276;0c|>85;95;0c|>83;40003;0c|0n|\??\d+;\d+R|8;\d+;\d+t|\??\d+;\d+\$y)$/.test(
      data,
    ) || /^\x1bP[\s\S]*\x1b\\$/.test(data)
  );
}

function finishReplay(current: WebSocket) {
  if (socket !== current) return;
  replayFinishPending = undefined;
  replaySeedPending = false;
  seedReplayFromPreview = false;
  replayPreviewData = undefined;
  if (replayWasTruncated)
    terminal?.writeln("\r\n\x1b[33m[部分历史输出已省略]\x1b[0m");
  replaying = false;
  streamID = replayTargetStreamID;
  streamOffset = replayTargetOffset;
  if (!historyLocked.value) resumeOutputFollow();
  if (replayPreviewHost) {
    terminal?.refresh(0, Math.max(0, (terminal?.rows || 1) - 1));
    previewSwapFrame = requestAnimationFrame(() => {
      previewSwapFrame = requestAnimationFrame(disposeReplayPreview);
    });
  }
  resize();
}

async function ensureAttached() {
  status.value = "reconnecting";
  try {
    await api(`/api/sessions/${props.session.id}/restore`, {
      method: "POST",
      body: json({}),
    });
    connectSocket();
  } catch (error) {
    const message = error instanceof Error ? error.message : "恢复失败";
    if (
      error instanceof ApiError &&
      error.body.code === "normal_session_ended"
    ) {
      status.value = "ended";
      statusMessage.value = message;
      emit("status", props.session.id, status.value, statusMessage.value);
      return;
    } else if (message.toLowerCase().includes("credential required")) {
      status.value = "auth_required";
      statusMessage.value = "需要重新输入 SSH 凭据";
      try {
        const { value } = await ElMessageBox.prompt(
          "远程 tmux 任务仍在运行，请输入同一远程账号的 SSH 密码。",
          "恢复会话",
          {
            confirmButtonText: "重新附着",
            cancelButtonText: "稍后处理",
            inputType: "password",
            inputValidator: (v) => Boolean(v) || "请输入密码",
          },
        );
        await restoreWith({ secret: value });
        connectSocket();
        return;
      } catch {}
    } else if (
      error instanceof ApiError &&
      (error.body.code === "unknown_host_key" ||
        error.body.code === "host_key_changed")
    ) {
      try {
        await ElMessageBox.confirm(
          `远程主机${error.body.hostName ? `“${error.body.hostName}”` : ""}${error.body.hostAddress ? `（${error.body.hostAddress}）` : ""}指纹：\n${error.body.fingerprint}`,
          "确认主机指纹",
          {
            confirmButtonText: "信任并恢复",
            cancelButtonText: "取消",
            type: "warning",
          },
        );
        await restoreWith({ trustFingerprint: error.body.fingerprint || "" });
        connectSocket();
        return;
      } catch {}
    } else {
      status.value =
        props.session.status === "auth_required"
          ? "auth_required"
          : "unreachable";
      statusMessage.value = message;
	  scheduleReconnect();
    }
    emit("status", props.session.id, status.value, statusMessage.value);
  }
}

function scheduleReconnect() {
  if (disposed || status.value === "ended" || status.value === "auth_required") return;
  clearTimeout(reconnectTimer);
  const delay = reconnectDelay(reconnectAttempts);
  reconnectAttempts = Math.min(reconnectAttempts + 1, 5);
  reconnectTimer = window.setTimeout(ensureAttached, delay);
}

function handlePageHide() {
  pageIsHiding = true;
  clearTimeout(reconnectTimer);
  if (socket?.readyState === WebSocket.OPEN && controller.value)
    socket.send(JSON.stringify({ type: "release_control" }));
  if (
    socket?.readyState === WebSocket.OPEN ||
    socket?.readyState === WebSocket.CONNECTING
  )
    socket.close(1000, "page hidden");
}

function handlePageShow() {
  if (!pageIsHiding) return;
  pageIsHiding = false;
  if (!disposed && (!socket || socket.readyState === WebSocket.CLOSED))
    ensureAttached();
}

async function restoreWith(extra: {
  secret?: string;
  trustFingerprint?: string;
}) {
  await api(`/api/sessions/${props.session.id}/restore`, {
    method: "POST",
    body: json({
      secret: extra.secret || "",
      trustFingerprint: extra.trustFingerprint || "",
    }),
  });
}

function writeConnectionNotice(message: string, tone: "muted" | "error" = "muted") {
  const clean = message.replace(/[\u0000-\u001f\u007f]/g, " ").trim();
  if (!terminal || !clean || clean === lastConnectionNotice) return;
  lastConnectionNotice = clean;
  const color = tone === "error" ? "31" : "90";
  terminal.writeln(`\r\n\x1b[${color}m[VelinWebSSH] ${clean}\x1b[0m`);
}

async function recoverConnection(statusName: string, message: string, detail: Record<string, unknown> = {}) {
  if (connectionRecoveryOpen) return;
  connectionRecoveryOpen = true;
  try {
    if (statusName === "auth_required") {
      const { value } = await ElMessageBox.prompt(
        "请输入该主机的 SSH 密码，密码只用于本次连接。",
        "等待 SSH 认证",
        {
          confirmButtonText: "连接",
          cancelButtonText: "稍后处理",
          inputType: "password",
          inputValidator: (v) => Boolean(v) || "请输入密码",
        },
      );
      await restoreWith({ secret: value });
      status.value = "reconnecting";
      statusMessage.value = "正在使用输入的密码重试连接";
      connectSocket();
      return;
    }
    if (statusName === "host_key_required" && detail.fingerprint) {
      await ElMessageBox.confirm(
        `远程主机${detail.hostName ? `“${detail.hostName}”` : ""}${detail.hostAddress ? `（${detail.hostAddress}）` : ""}指纹：\n${String(detail.fingerprint)}`,
        "确认主机指纹",
        {
          confirmButtonText: "信任并连接",
          cancelButtonText: "取消",
          type: "warning",
        },
      );
      await restoreWith({ trustFingerprint: String(detail.fingerprint) });
      status.value = "reconnecting";
      statusMessage.value = "正在使用已确认的主机指纹重试连接";
      connectSocket();
      return;
    }
    if (statusName === "unreachable") writeConnectionNotice(message, "error");
  } catch {}
  finally {
    connectionRecoveryOpen = false;
  }
}

function connectSocket() {
  if (disposed) return;
  clearTimeout(reconnectTimer);
  clearInterval(heartbeatTimer);
  socket?.close();
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
	const query = new URLSearchParams();
	if (streamID) {
		query.set("stream", streamID);
		query.set("offset", String(streamOffset));
	}
	if (reconnectKey) query.set("reconnect", reconnectKey);
	const resumeQuery = query.size ? `?${query}` : "";
	const current = new WebSocket(
		`${protocol}//${location.host}/ws/sessions/${props.session.id}${resumeQuery}`,
  );
  socket = current;
  current.onopen = () => {
    if (socket !== current) return;
    lastSentCols = 0;
    lastSentRows = 0;
    connected.value = true;
	terminalLatency.value = undefined;
	latencyPingAt = 0;
	reconnectAttempts = 0;
    if (status.value !== "creating") {
      status.value = "attached";
      emit("status", props.session.id, "attached");
    }
    const sendPing = () => {
      if (current.readyState !== WebSocket.OPEN || latencyPingAt) return;
      latencyPingAt = performance.now();
      current.send(JSON.stringify({ type: "ping" }));
    };
    sendPing();
    heartbeatTimer = window.setInterval(sendPing, 5_000);
  };
  current.onmessage = (event) => {
    if (socket !== current) return;
    const message = JSON.parse(event.data);
		if (message.type === "pong") {
      if (latencyPingAt) {
        terminalLatency.value = Math.max(
          0,
          Math.round(performance.now() - latencyPingAt),
        );
        latencyPingAt = 0;
      }
		} else if (message.type === "hello") {
      clientID = message.clientID || "";
      controller.value = Boolean(clientID && message.controller === clientID);
			updateRemoteHistorySize(Number(message.historySize) || 0);
			pendingHistoryPosition = remoteHistorySize.value;
			historyRequestPosition = remoteHistorySize.value;
			historyRequestInFlight = 0;
      sendTerminalTheme();
      replaying = true;
      const nextStreamID = String(message.streamID || "");
      replayResumesCurrentTerminal =
        !forceTailReplayReset &&
        terminalInitialized &&
        Boolean(nextStreamID) &&
        nextStreamID === streamID &&
        !message.truncated;
      forceTailReplayReset = false;
      replayWasTruncated = Boolean(message.truncated);
      replayResetApplied = replayResumesCurrentTerminal;
      replayTargetStreamID = nextStreamID;
      replayTargetOffset = Number(message.offset) || 0;
      replayPreviewedOffset = 0;
      previewTailReconnect =
        !terminalInitialized && Boolean(nextStreamID) && replayTargetOffset > 0;
      if (!replayResumesCurrentTerminal) {
        historyLocked.value = false;
        hasNewOutput.value = false;
      }
      terminalInitialized = true;
      if (message.status && message.status !== "attached") {
        status.value = message.status;
        statusMessage.value = message.message || "";
        emit("status", props.session.id, message.status, statusMessage.value);
        if (message.status === "creating")
          writeConnectionNotice("正在建立 SSH 连接，请稍候…");
        else if (message.status === "auth_required" || message.status === "host_key_required")
          void recoverConnection(message.status, message.message || "SSH 连接失败", message);
      }
      if (controller.value) flushPendingInput();
      if (seedReplayFromPreview && replayPreviewData?.length) {
        const seed = replayPreviewData;
        seedReplayFromPreview = false;
        resetTerminalForReplay();
        replaySeedPending = true;
        writeTerminal(seed, () => {
          replaySeedPending = false;
          if (replayFinishPending === current) finishReplay(current);
        });
      }
      // New servers stream replay chunks and finish with replay_end. Keep the
      // legacy inline-data path for older connections during a rolling update.
      if (message.data) {
        resetTerminalForReplay();
        writeTerminal(base64ToBytes(message.data), () => finishReplay(current));
      }
    } else if (message.type === "replay_preview" && message.data) {
      showReplayPreview(base64ToBytes(message.data));
			if (previewTailReconnect) {
				previewTailReconnect = false;
				streamID = replayTargetStreamID;
				streamOffset = replayTargetOffset;
				forceTailReplayReset = true;
				seedReplayFromPreview = true;
				connectSocket();
			}
		} else if (message.type === "replay" && message.data) {
			resetTerminalForReplay();
			writeTerminal(
				base64ToBytes(message.data),
				() => {
	          if (!historyLocked.value) terminal?.scrollToBottom();
					if (message.replayFinal) finishReplay(current);
				},
			);
    } else if (message.type === "replay_end") {
      resetTerminalForReplay();
      if (replaySeedPending) replayFinishPending = current;
      else finishReplay(current);
    } else if (message.type === "replay_live" && message.data) {
      const outputOffset = Number(message.offset);
      if (Number.isFinite(outputOffset))
        replayPreviewedOffset = Math.max(replayPreviewedOffset, outputOffset);
      writeReplayPreviewOutput(base64ToBytes(message.data));
    } else if (message.type === "output" && message.data) {
      const outputOffset = Number(message.offset);
      const output = base64ToBytes(message.data);
      const alreadyPreviewed =
        Number.isFinite(outputOffset) &&
        replayPreviewedOffset > 0 &&
        outputOffset <= replayPreviewedOffset;
      if (!alreadyPreviewed) writeReplayPreviewOutput(output);
      if (
        Number.isFinite(outputOffset) &&
        outputOffset >= replayPreviewedOffset
      )
        replayPreviewedOffset = 0;
      writeTerminal(output, () => {
        if (Number.isFinite(outputOffset)) streamOffset = outputOffset;
      });
      emit("attention", props.session.id, "clear");
      scheduleHistoryStateRefresh();
      scheduleOutputSettledAttention();
		} else if (message.type === "history_state") {
			updateRemoteHistorySize(Number(message.historySize) || 0);
			if (!remoteHistoryMode.value) {
				pendingHistoryPosition = remoteHistorySize.value;
				historyRequestPosition = remoteHistorySize.value;
			}
		} else if (message.type === "history_position") {
			handleRemoteHistoryPosition(message);
    } else if (message.type === "control_granted") {
      controller.value = true;
      controlRequestPending = false;
      sendTerminalTheme();
      flushPendingInput();
      ElMessage.success("已取得终端控制权");
      resize();
    } else if (message.type === "control_pending") {
      controlRequestPending = true;
      ElMessage.info("已向当前控制设备发送接管请求");
    } else if (message.type === "control_denied") {
      controller.value = false;
      controlRequestPending = false;
      pendingControlInput = "";
      ElMessage.warning("当前控制设备拒绝了接管请求");
    } else if (message.type === "controller") {
      controller.value = message.controller === clientID;
      if (controller.value) {
        controlRequestPending = false;
        flushPendingInput();
      }
    }
    else if (
      message.type === "control_request" &&
      message.requester &&
      !controlPromptOpen
    ) {
      controlPromptOpen = true;
      emit("attention", props.session.id, "bell");
      ElMessageBox.confirm(
        "另一台设备请求接管此终端。允许后，当前设备会立即变为只读。",
        "终端控制权",
        {
          confirmButtonText: "允许接管",
          cancelButtonText: "保持控制",
          distinguishCancelAndClose: true,
          type: "warning",
        },
      )
        .then(() =>
          current.send(
            JSON.stringify({
              type: "control_response",
              requester: message.requester,
              approved: true,
            }),
          ),
        )
        .catch(() =>
          current.send(
            JSON.stringify({
              type: "control_response",
              requester: message.requester,
              approved: false,
            }),
          ),
        )
        .finally(() => {
          controlPromptOpen = false;
          emit("attention", props.session.id, "clear");
        });
    } else if (message.type === "status") {
      status.value = message.status;
      statusMessage.value = message.message || "";
      emit("status", props.session.id, message.status, message.message);
      if (message.status === "attached")
        writeConnectionNotice("SSH 连接已建立");
      else if (message.status === "creating")
        writeConnectionNotice("正在建立 SSH 连接，请稍候…");
      else {
        writeConnectionNotice(message.message || "SSH 连接失败", "error");
        void recoverConnection(message.status, message.message || "SSH 连接失败", message);
      }
    }
  };
	current.onclose = () => {
	    if (socket !== current) return;
		clearInterval(heartbeatTimer);
	replaying = false;
	disposeHistoryTransitionSnapshot();
	remoteHistoryMode.value = false;
		remoteHistoryPosition.value = remoteHistorySize.value;
		clearTimeout(historyAckTimer);
		historyRequestInFlight = 0;
	connected.value = false;
    terminalLatency.value = undefined;
    latencyPingAt = 0;
    controller.value = false;
    controlRequestPending = false;
    if (
      !disposed &&
      !pageIsHiding &&
      !["ended", "auth_required", "host_key_required", "unreachable"].includes(
        status.value,
      )
    ) {
      status.value = "reconnecting";
	  scheduleReconnect();
    }
  };
}

function sendTerminalSize() {
  if (!terminal || socket?.readyState !== WebSocket.OPEN || !controller.value)
    return;
  if (terminal.cols === lastSentCols && terminal.rows === lastSentRows) return;
  socket.send(
    JSON.stringify({
      type: "resize",
      rows: terminal.rows,
      cols: terminal.cols,
    }),
  );
  lastSentCols = terminal.cols;
  lastSentRows = terminal.rows;
}
function sendTerminalTheme() {
  if (socket?.readyState !== WebSocket.OPEN || !controller.value) return;
  const theme = terminalXtermTheme(props.preferences.terminalTheme);
  socket.send(
    JSON.stringify({
      type: "terminal_theme",
      foreground: theme.foreground,
      background: theme.background,
    }),
  );
}

function captureResizeSnapshot() {
  if (
    resizeSnapshot ||
    replayPreviewHost ||
    !terminalInitialized ||
    !container.value ||
    !terminal?.element
  )
    return false;
  const sources = terminal.element.querySelectorAll<HTMLCanvasElement>(
    ".xterm-screen canvas",
  );
  const bounds = container.value.getBoundingClientRect();
  if (bounds.width < 1 || bounds.height < 1) return false;
  const snapshot = document.createElement("div");
  snapshot.className = "terminal-resize-snapshot";
  snapshot.style.width = `${bounds.width}px`;
  snapshot.style.height = `${bounds.height}px`;
  snapshot.style.backgroundColor =
    terminalXtermTheme(props.preferences.terminalTheme).background ||
    props.preferences.background;
  let clonedViewportScroll: { top: number; left: number } | undefined;

  if (sources.length) {
    const ratio = window.devicePixelRatio || 1;
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bounds.width * ratio));
    canvas.height = Math.max(1, Math.round(bounds.height * ratio));
    canvas.style.width = `${bounds.width}px`;
    canvas.style.height = `${bounds.height}px`;
    const context = canvas.getContext("2d");
    if (!context) return false;
    context.scale(ratio, ratio);
    context.fillStyle = snapshot.style.backgroundColor;
    context.fillRect(0, 0, bounds.width, bounds.height);
    for (const source of sources) {
      const rect = source.getBoundingClientRect();
      context.drawImage(
        source,
        rect.left - bounds.left,
        rect.top - bounds.top,
        rect.width,
        rect.height,
      );
    }
    snapshot.appendChild(canvas);
  } else {
    // xterm 5 uses the DOM renderer by default. Cloning its visible rows keeps
    // the latest viewport opaque while the real buffer reflows in the back.
    const clone = terminal.element.cloneNode(true) as HTMLElement;
    clone.querySelectorAll("textarea").forEach((element) => element.remove());
    clone.removeAttribute("tabindex");
    clone.style.width = "100%";
    clone.style.height = "100%";
    const sourceViewport = terminal.element.querySelector<HTMLElement>(
      ".xterm-viewport",
    );
    const cloneViewport = clone.querySelector<HTMLElement>(".xterm-viewport");
    if (sourceViewport && cloneViewport) {
      clonedViewportScroll = {
        top: sourceViewport.scrollTop,
        left: sourceViewport.scrollLeft,
      };
    }
    snapshot.appendChild(clone);
  }
  resizeSnapshot = snapshot;
  container.value.appendChild(snapshot);
  if (clonedViewportScroll) {
    const viewport = snapshot.querySelector<HTMLElement>(".xterm-viewport");
    if (viewport) {
      viewport.scrollTop = clonedViewportScroll.top;
      viewport.scrollLeft = clonedViewportScroll.left;
    }
  }
  return true;
}

function disposeResizeSnapshot() {
  if (resizeApplyFrame !== undefined) cancelAnimationFrame(resizeApplyFrame);
  if (resizeReleaseFrame !== undefined)
    cancelAnimationFrame(resizeReleaseFrame);
  resizeApplyFrame = undefined;
  resizeReleaseFrame = undefined;
  clearTimeout(resizeReleaseTimer);
  resizeReleaseTimer = undefined;
  resizeSnapshot?.remove();
  resizeSnapshot = undefined;
  resizeSnapshotDeadline = 0;
  resizeFollowingBottom = false;
  resizeAwaitingRemoteOutput = false;
  resizeRemoteOutputSeen = false;
}

function releaseResizeSnapshot(delay = 0) {
  if (!resizeSnapshot) return;
  clearTimeout(resizeReleaseTimer);
  const remaining = Math.max(0, resizeSnapshotDeadline - performance.now());
  resizeReleaseTimer = window.setTimeout(() => {
    resizeReleaseTimer = undefined;
    resizeReleaseFrame = requestAnimationFrame(() => {
      resizeReleaseFrame = requestAnimationFrame(disposeResizeSnapshot);
    });
  }, Math.min(delay, remaining));
}

function fitTerminal() {
  if (!props.visible || !fit || !terminal || !container.value) return;
  if (pendingWrites) {
    resizeAfterWrite = true;
    return;
  }
  try {
    const dimensions = fit.proposeDimensions();
    if (!dimensions || dimensions.cols < 2 || dimensions.rows < 1) return;
    const keepBottom = !historyLocked.value && isAtBottom();
    if (
      dimensions.cols === terminal.cols &&
      dimensions.rows === terminal.rows
    ) {
      if (keepBottom) terminal.scrollToBottom();
      sendTerminalSize();
      return;
    }
    const applyResize = () => {
      resizeApplyFrame = undefined;
      if (!terminal) return disposeResizeSnapshot();
      if (pendingWrites) {
        resizeAfterWrite = true;
        return;
      }
      clearTimeout(resizeReleaseTimer);
      resizeReleaseTimer = undefined;
      if (resizeReleaseFrame !== undefined)
        cancelAnimationFrame(resizeReleaseFrame);
      resizeReleaseFrame = undefined;
      resizeFollowingBottom = Boolean(resizeSnapshot && keepBottom);
      resizeSnapshotDeadline = resizeSnapshot ? performance.now() + 700 : 0;
      resizeRemoteOutputSeen = false;
      terminal.resize(dimensions.cols, dimensions.rows);
      if (keepBottom) terminal.scrollToBottom();
      clearTimeout(resizeSocketTimer);
      resizeSocketTimer = window.setTimeout(() => {
        resizeAwaitingRemoteOutput = Boolean(resizeSnapshot);
        sendTerminalSize();
        releaseResizeSnapshot(320);
      }, 80);
      terminal.refresh(0, Math.max(0, terminal.rows - 1));
    };
    if (captureResizeSnapshot())
      resizeApplyFrame = requestAnimationFrame(applyResize);
    else applyResize();
  } catch {}
}
function resize() {
  if (!props.visible || !fit || !terminal) return;
  // Pane dragging and history replay can both trigger many layout changes.
  // Let the container move first and perform one terminal reflow afterward.
  if (document.body.classList.contains("is-resizing-panes") || replaying)
    return;
  clearTimeout(resizeSettleTimer);
  if (resizeFrame !== undefined) cancelAnimationFrame(resizeFrame);
  if (resizeApplyFrame !== undefined) cancelAnimationFrame(resizeApplyFrame);
  resizeSettleTimer = window.setTimeout(() => {
    nextTick(() => {
      resizeFrame = requestAnimationFrame(fitTerminal);
    });
  }, 90);
}
function requestControl() {
  if (socket?.readyState === WebSocket.OPEN && !controlRequestPending) {
    controlRequestPending = true;
    socket.send(JSON.stringify({ type: "request_control" }));
  }
}
function toggleControl() {
  if (socket?.readyState !== WebSocket.OPEN) return;
  socket.send(
    JSON.stringify({
      type: controller.value ? "release_control" : "request_control",
    }),
  );
}
function sendKey(key: string) {
  void sendInput(key);
}
function modifiedKey(data: string) {
  if (!props.mobileCtrl && !props.mobileAlt) return data;
  const arrows: Record<string, string> = {
    "\x1b[A": "A",
    "\x1b[B": "B",
    "\x1b[C": "C",
    "\x1b[D": "D",
    "\x1b[H": "H",
    "\x1b[F": "F",
  };
  const suffix = arrows[data];
  let output = data;
  if (suffix) {
    const modifier = 1 + (props.mobileAlt ? 2 : 0) + (props.mobileCtrl ? 4 : 0);
    output = `\x1b[1;${modifier}${suffix}`;
  } else {
    if (props.mobileCtrl && data.length === 1) {
      const code = data.toUpperCase().charCodeAt(0);
      if (code >= 64 && code <= 95) output = String.fromCharCode(code & 31);
      else if (data === "?") output = "\x7f";
      else if (data === " ") output = "\x00";
    }
    if (props.mobileAlt) output = "\x1b" + output;
  }
  emit("modifiersUsed");
  return output;
}
function sendModifiedKey(key: string) {
  sendKey(modifiedKey(key));
}
function findNext() {
  if (searchText.value)
    search?.findNext(searchText.value, { caseSensitive: false });
}
function focus() {
  terminal?.focus();
}
function restoreTerminalFocus() {
  nextTick(() => {
    requestAnimationFrame(() => {
      if (!disposed && props.visible && container.value?.isConnected) {
        terminal?.focus();
      }
    });
  });
}
function previewTerminalLink(event: MouseEvent, value: string) {
  event.preventDefault();
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:")
      throw new Error();
    linkPreviewURL.value = parsed.href;
    linkPreviewOpen.value = true;
  } catch {
    ElMessage.warning("无法识别此链接");
  }
}
async function copyTerminalLink() {
  try {
    await navigator.clipboard.writeText(linkPreviewURL.value);
    ElMessage.success("链接已复制");
  } catch {
    const input = document.createElement("textarea");
    input.value = linkPreviewURL.value;
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    const copied = document.execCommand("copy");
    input.remove();
    if (copied) ElMessage.success("链接已复制");
    else ElMessage.error("浏览器禁止访问剪贴板");
  }
}
function openTerminalLink() {
  const popup = window.open();
  if (!popup) return ElMessage.warning("浏览器阻止了新标签页");
  popup.opener = null;
  popup.location.href = linkPreviewURL.value;
}
async function sendInput(data: string) {
  if (!data) return;
  if (
    props.preferences.pasteGuard &&
    (data.includes("\n") || data.includes("\r")) &&
    data.length > 2
  ) {
    try {
      await ElMessageBox.confirm(
        `即将向远程终端粘贴 ${data.split(/\r\n|\r|\n/).length} 行内容。`,
        "确认多行粘贴",
        {
          confirmButtonText: "发送",
          cancelButtonText: "取消",
          type: "warning",
        },
      );
    } catch {
      return;
    } finally {
      restoreTerminalFocus();
    }
  }
  if (!controller.value || socket?.readyState !== WebSocket.OPEN) {
    const wasEmpty = pendingControlInput.length === 0;
    pendingControlInput = (pendingControlInput + data).slice(0, 128 * 1024);
    if (!controller.value) {
      requestControl();
      if (wasEmpty) ElMessage.info("正在取得终端控制权，输入已暂存");
    }
    return;
  }
  sendInputNow(data);
}

function sendInputNow(data: string) {
  if (!data || socket?.readyState !== WebSocket.OPEN || !controller.value)
    return;
  resumeOutputFollow();
  clearTimeout(outputSettleTimer);
  emit("attention", props.session.id, "clear");
  socket.send(JSON.stringify({ type: "input", data: bytesToBase64(data) }));
}

function flushPendingInput() {
  if (!pendingControlInput || !controller.value) return;
  const data = pendingControlInput;
  pendingControlInput = "";
  sendInputNow(data);
}
function pasteThroughTerminal(text: string) {
  if (!terminal) return void sendInput(text);
  const multiline = /[\r\n]/.test(text);
  if (multiline && !terminal.modes.bracketedPasteMode) {
    // Some TUIs (including Codex) can be ready for bracketed paste before
    // xterm has observed their mode switch. Keep the whole paste atomic.
    void sendInput(bracketedPasteData(text));
    return;
  }
  // xterm normalizes line endings and adds bracketed-paste markers only when
  // the remote application has enabled that terminal mode (used by Codex).
  pasting = true;
  try {
    terminal.paste(text);
  } finally {
    pasting = false;
  }
}
function isBracketedPaste(data: string) {
  return data.startsWith("\x1b[200~") && data.endsWith("\x1b[201~");
}
function bracketedPasteData(text: string) {
  const normalized = text.replace(/\r\n?|\n/g, "\r");
  return `\x1b[200~${normalized}\x1b[201~`;
}
function handleTerminalPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData("text/plain");
  if (text == null) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  pasteThroughTerminal(text);
}
async function copySelection() {
  const selected = terminal?.getSelection() || "";
  if (!selected) return ElMessage.info("请先选中终端内容");
  try {
    await navigator.clipboard.writeText(selected);
  } catch {
    const input = document.createElement("textarea");
    input.value = selected;
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    const copied = document.execCommand("copy");
    input.remove();
    if (!copied) return ElMessage.error("浏览器禁止访问剪贴板");
  }
  ElMessage.success("已复制");
}
async function pasteClipboard() {
  try {
    let text = "";
    try {
      text = await navigator.clipboard.readText();
    } catch {
      try {
        const result = await ElMessageBox.prompt(
          "浏览器无法直接读取剪贴板，请在此粘贴内容。",
          "粘贴到终端",
          {
            confirmButtonText: "发送",
            cancelButtonText: "取消",
            inputType: "textarea",
          },
        );
        text = result.value;
      } catch {
        return;
      }
    }
    pasteThroughTerminal(text);
  } finally {
    restoreTerminalFocus();
  }
}
function selectAll() {
  terminal?.selectAll();
  terminal?.focus();
}
function clearTerminal() {
  terminal?.clear();
  terminal?.focus();
}
function downloadText() {
  if (!terminal) return;
  const lines: string[] = [];
  const buffer = terminal.buffer.active;
  for (let index = 0; index < buffer.length; index++)
    lines.push(buffer.getLine(index)?.translateToString(true) || "");
  const blob = new Blob([lines.join("\n")], {
    type: "text/plain;charset=utf-8",
  });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `${props.session.name.replace(/[^a-zA-Z0-9._-]+/g, "_") || "terminal"}-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`;
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(link.href), 0);
}
function recordingCanvas() {
  return terminal?.element?.querySelector<HTMLCanvasElement>(
    ".xterm-screen canvas",
  );
}

function recordingMimeType() {
  if (typeof MediaRecorder === "undefined") return "";
  const candidates = [
    "video/mp4;codecs=avc1.42E01E",
    "video/webm;codecs=vp9",
    "video/webm;codecs=vp8",
    "video/webm",
  ];
  return candidates.find((value) => MediaRecorder.isTypeSupported(value)) || "";
}

function recordingVideoBitsPerSecond(canvas: HTMLCanvasElement) {
  // Terminal glyphs need more bitrate than ordinary moving video. Scale the
  // budget with the actual device-pixel dimensions without allowing very large
  // panes to create unexpectedly huge files.
  const pixels = canvas.width * canvas.height;
  return Math.min(30_000_000, Math.max(12_000_000, Math.round(pixels * 4)));
}

function stopLocalRecorder() {
  const recorder = mediaRecorder;
  if (!recorder) return Promise.resolve<Blob | undefined>(undefined);
  return new Promise<Blob>((resolve, reject) => {
    const finish = () => {
      const blob = new Blob(recordingChunks, {
        type: recorder.mimeType || "video/webm",
      });
      mediaRecorder = undefined;
      recordingChunks = [];
      recordingStream?.getTracks().forEach((track) => track.stop());
      recordingStream = undefined;
      resolve(blob);
    };
    recorder.addEventListener("stop", finish, { once: true });
    recorder.addEventListener(
      "error",
      () => reject(new Error("浏览器录制失败")),
      { once: true },
    );
    if (recorder.state === "inactive") finish();
    else recorder.stop();
  });
}

async function toggleRecording() {
  if (recordingBusy.value) return;
  try {
    if (!recording.value) {
      const canvas = recordingCanvas();
      const mimeType = recordingMimeType();
      if (!canvas?.captureStream || !mimeType)
        throw new Error("当前浏览器不支持终端视频录制");
      const recorder = new MediaRecorder(canvas.captureStream(30), {
        mimeType,
        videoBitsPerSecond: recordingVideoBitsPerSecond(canvas),
      });
      recordingChunks = [];
      recorder.addEventListener("dataavailable", (event) => {
        if (event.data.size) recordingChunks.push(event.data);
      });
      recordingStream = recorder.stream;
      const value = await api<{ id: string; status: string }>(
        `/api/sessions/${props.session.id}/recording`,
        { method: "POST" },
      );
      mediaRecorder = recorder;
      recording.value = value;
      try {
        recorder.start(250);
      } catch (error) {
        recording.value = undefined;
        await api(`/api/sessions/${props.session.id}/recording`, {
          method: "DELETE",
        });
        throw error;
      }
      ElMessage.success("MP4 录制已开始");
      return;
    }

    recordingBusy.value = true;
    const active = recording.value;
    const blob = await stopLocalRecorder();
    if (!blob || !blob.size) throw new Error("没有录制到终端画面");
    const value = await api<{ id: string }>(
      `/api/recordings/${active.id}/upload`,
      {
        method: "POST",
        headers: { "Content-Type": blob.type || "video/webm" },
        body: blob,
      },
    );
    recording.value = undefined;
    const link = document.createElement("a");
    link.href = `/api/recordings/${value.id}/download`;
    link.click();
    ElMessage.success("MP4 录制已生成，正在下载");
  } catch (error) {
    if (mediaRecorder || recordingStream) {
      try {
        await stopLocalRecorder();
      } catch {}
    }
    if (recording.value) {
      try {
        await api(`/api/sessions/${props.session.id}/recording`, {
          method: "DELETE",
        });
      } catch {}
    }
    recording.value = undefined;
    ElMessage.error(error instanceof Error ? error.message : "录制操作失败");
  } finally {
    recordingBusy.value = false;
  }
}
function ringBell() {
  if (replaying) return;
  emit("attention", props.session.id, "bell");
  if (props.preferences.visualBell) {
    bellActive.value = true;
    window.setTimeout(() => (bellActive.value = false), 220);
  }
  if (props.preferences.soundBell) {
    try {
      const AudioContextClass =
        window.AudioContext || (window as any).webkitAudioContext;
      const context = new AudioContextClass(),
        oscillator = context.createOscillator(),
        gain = context.createGain();
      oscillator.frequency.value = 740;
      gain.gain.value = 0.035;
      oscillator.connect(gain);
      gain.connect(context.destination);
      oscillator.start();
      gain.gain.exponentialRampToValueAtTime(0.001, context.currentTime + 0.1);
      oscillator.stop(context.currentTime + 0.11);
    } catch {}
  }
}
function scheduleOutputSettledAttention() {
  clearTimeout(outputSettleTimer);
  if (props.visible || replaying) return;
  outputSettleTimer = window.setTimeout(() => {
    emit("attention", props.session.id, "settled");
  }, terminalOutputSettleDelay);
}
onMounted(() => {
  terminal = new Terminal({
    cols: initialTerminalDimensions.cols,
    rows: initialTerminalDimensions.rows,
    cursorBlink: props.preferences.cursorBlink,
    cursorStyle: props.preferences.cursorStyle,
    fontSize: props.preferences.fontSize,
    lineHeight: props.preferences.lineHeight,
    fontFamily: terminalFontFamily,
    fontWeight: props.preferences.fontWeight,
    letterSpacing: props.preferences.letterSpacing,
    theme: terminalXtermTheme(props.preferences.terminalTheme),
    // Codex and other interactive tools can emit thousands of lines quickly.
    // Keep the early Codex transcript available while the TUI is still
    // redrawing the active screen.
    scrollback: 100000,
    scrollOnUserInput: true,
    // Normal SSH alternate-screen apps still rely on xterm's native wheel
    // forwarding. Limit the generated cursor/mouse event rate there as well.
    scrollSensitivity: 0.25,
    // A short duration keeps normal output smooth without making fast TUI
    // updates visibly lag behind the remote session.
    // Streamed output should follow immediately; animated scrolling is
    // reserved for an explicit jump to the latest output.
    smoothScrollDuration: 0,
    allowProposedApi: false,
  });
  fit = new FitAddon();
  search = new SearchAddon();
  terminal.loadAddon(fit);
  terminal.loadAddon(new CanvasAddon());
  terminal.loadAddon(search);
  terminal.loadAddon(new WebLinksAddon(previewTerminalLink));
  terminal.open(container.value!);
  if (props.session.sessionMode === "tmux") {
    // Full-screen CLIs such as Codex redraw a footer inside a partial scroll
    // region. xterm correctly honors that region, but lines leaving it are
    // overwritten instead of entering scrollback. Keep the browser buffer a
    // transcript for tmux sessions once such a region is detected.
    terminal.parser.registerCsiHandler({ final: "r" }, (params) => {
      if (transcriptModeForced) return true;
      const values = params.map((value) =>
        Array.isArray(value) ? value[0] || 0 : value,
      );
      const top = values[0] || 1;
      const bottom = values[1] || terminal?.rows || 0;
      if (values.length > 1 && (top > 1 || bottom < (terminal?.rows || 0))) {
        forceTranscriptMode();
        return true;
      }
      return false;
    });
    for (const final of ["h", "l"] as const) {
      terminal.parser.registerCsiHandler({ prefix: "?", final }, (params) => {
        const values = params.map((value) =>
          Array.isArray(value) ? value[0] || 0 : value,
        );
        if (!values.some((value) => [47, 1047, 1049].includes(value)))
          return false;
        forceTranscriptMode();
        return true;
      });
    }
  }
  fitTerminal();
  if (status.value === "creating")
    writeConnectionNotice("正在建立 SSH 连接，请稍候…");
  rememberTerminalDimensions(terminal.cols, terminal.rows);
	terminalTextarea = terminal.textarea || undefined;
	terminalTextarea?.addEventListener("paste", handleTerminalPaste, true);
  terminal.parser.registerOscHandler(7, (value) => {
    try {
      const url = new URL(value);
      if (url.protocol !== "file:") return false;
      emit("directory", props.session.id, decodeURIComponent(url.pathname));
      return true;
    } catch {
      return false;
    }
  });
  terminal.onScroll(() => {
    if (smoothScrolling) return;
    historyLocked.value = !isAtBottom();
    if (!historyLocked.value) hasNewOutput.value = false;
  });
  terminal.onResize(({ cols, rows }) =>
    rememberTerminalDimensions(cols, rows),
  );
  terminal.attachCustomWheelEventHandler(handleTerminalWheel);
  terminal.onTitleChange((title) => {
    terminalTitle = title.replace(/[\u0000-\u001f\u007f]/g, "").slice(0, 80);
    emit("title", props.session.id, terminalTitle);
    updateConversationMode();
  });
	terminal.onBell(ringBell);
	terminal.onData(async (data) => {
		if (isReplayProtocolResponse(data)) return;
		await sendInput(pasting || isBracketedPaste(data) ? data : modifiedKey(data));
	});
  observer = new ResizeObserver(resize);
  observer.observe(container.value!);
  window.addEventListener("pagehide", handlePageHide);
  window.addEventListener("pageshow", handlePageShow);
  ensureAttached();
});

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      clearTimeout(outputSettleTimer);
      emit("attention", props.session.id, "clear");
      resize();
    }
  },
);
watch(
  () => props.preferences,
  (value) => {
    if (terminal) {
      terminal.options.fontSize = value.fontSize;
      terminal.options.lineHeight = value.lineHeight;
      terminal.options.fontWeight = value.fontWeight;
      terminal.options.letterSpacing = value.letterSpacing;
      terminal.options.cursorStyle = value.cursorStyle;
      terminal.options.cursorBlink = value.cursorBlink;
      terminal.options.theme = terminalXtermTheme(value.terminalTheme);
      sendTerminalTheme();
      resize();
    }
  },
  { deep: true },
);
onBeforeUnmount(() => {
  disposed = true;
  window.removeEventListener("pagehide", handlePageHide);
  window.removeEventListener("pageshow", handlePageShow);
  clearTimeout(reconnectTimer);
  clearTimeout(outputSettleTimer);
  clearTimeout(resizeSettleTimer);
	clearTimeout(resizeSocketTimer);
	clearTimeout(historySendTimer);
	clearTimeout(historyAckTimer);
	clearTimeout(historyRefreshTimer);
  clearInterval(heartbeatTimer);
	if (resizeFrame !== undefined) cancelAnimationFrame(resizeFrame);
	if (historyPositionFrame !== undefined)
		cancelAnimationFrame(historyPositionFrame);
	disposeHistoryTransitionSnapshot();
	disposeResizeSnapshot();
  if (scrollFrame !== undefined) cancelAnimationFrame(scrollFrame);
	observer?.disconnect();
	disposeReplayPreview();
	terminalTextarea?.removeEventListener("paste", handleTerminalPaste, true);
	if (recording.value) {
		void api(`/api/sessions/${props.session.id}/recording`, { method: "DELETE" });
	}
	if (mediaRecorder && mediaRecorder.state !== "inactive") mediaRecorder.stop();
	recordingStream?.getTracks().forEach((track) => track.stop());
	mediaRecorder = undefined;
	recordingStream = undefined;
	socket?.close();
  terminal?.dispose();
});
defineExpose({
  resize,
  focus,
  sendKey,
  sendModifiedKey,
  sendInput,
  copySelection,
  pasteClipboard,
  selectAll,
  clearTerminal,
  downloadText,
});
</script>

<template>
  <section
    class="terminal-pane"
    :class="{ inactive: !visible, 'terminal-bell': bellActive }"
    :style="{ backgroundColor: preferences.background }"
  >
    <div class="terminal-statusbar">
      <div class="status-left">
        <span class="status-dot" :class="status"></span
        ><strong>{{ session.name }}</strong
        ><span>{{ statusLabel }}</span
        ><span
          v-if="terminalLatency !== undefined"
          class="terminal-latency"
          :class="latencyTone"
          title="终端网络往返延迟"
          >{{ terminalLatency }} ms</span
        >
      </div>
      <div class="terminal-tools">
        <el-tooltip :content="controller ? '释放控制权' : '请求控制权'"
          ><button
            class="icon-btn"
            :class="{ active: controller }"
            @click="toggleControl"
          >
            <component
              :is="controller ? KeyboardOff : Keyboard"
              :size="15"
            /></button
        ></el-tooltip>
        <el-tooltip content="搜索终端"
          ><button class="icon-btn" @click="searchOpen = !searchOpen">
            <Search :size="15" /></button
        ></el-tooltip>
        <el-tooltip
          :content="
            recordingBusy
              ? '正在生成 MP4'
              : recording
                ? '停止并下载录制'
                : '开始终端录制'
          "
        >
          <button
            class="icon-btn"
            :class="{ active: recording, busy: recordingBusy }"
            :disabled="!connected || recordingBusy"
            @click="toggleRecording"
          >
            <LoaderCircle
              v-if="recordingBusy"
              :size="15"
              class="recording-spinner"
            />
            <component
              v-else
              :is="recording ? CircleStop : Circle"
              :size="15"
            />
          </button>
        </el-tooltip>
        <el-tooltip v-if="closable" content="终止并关闭"
          ><button class="icon-btn pane-close" @click="emit('close')">
            <X :size="15" /></button
        ></el-tooltip>
        <el-tooltip :content="connected ? '连接正常' : '连接断开'">
          <component
            :is="connected ? Wifi : Unplug"
            :size="14"
            class="connection-icon"
            :class="{ disconnected: !connected }"
          />
        </el-tooltip>
      </div>
    </div>
    <div v-if="searchOpen" class="terminal-search">
      <el-input
        v-model="searchText"
        size="small"
        placeholder="搜索"
        @keyup.enter="findNext"
      /><el-button size="small" @click="findNext">下一个</el-button>
    </div>
    <div v-if="!controller && connected" class="readonly-banner">
      <Eye :size="14" /> 只读观看，点击键盘图标请求控制
    </div>
		<div class="terminal-stage">
			<div
				ref="container"
				class="terminal-canvas"
				:class="{
					'has-remote-history': showRemoteHistoryScrollbar,
					'is-browsing-history': remoteHistoryMode,
				}"
			></div>
			<div
				v-if="showRemoteHistoryScrollbar"
				class="terminal-history-scrollbar"
				role="scrollbar"
				aria-orientation="vertical"
				:aria-valuemin="0"
				:aria-valuemax="remoteHistorySize"
				:aria-valuenow="remoteHistoryPosition"
				tabindex="0"
				@pointerdown="startRemoteHistoryDrag"
				@pointermove="moveRemoteHistoryDrag"
				@pointerup="finishRemoteHistoryDrag"
				@pointercancel="finishRemoteHistoryDrag"
				@keydown.up.prevent="sendRemoteHistoryScroll(-3)"
				@keydown.down.prevent="sendRemoteHistoryScroll(3)"
				@keydown.page-up.prevent="sendRemoteHistoryScroll(-20)"
				@keydown.page-down.prevent="sendRemoteHistoryScroll(20)"
				@keydown.home.prevent="jumpRemoteHistory(0)"
				@keydown.end.prevent="jumpRemoteHistory(remoteHistorySize)"
			>
				<span
					class="terminal-history-scrollbar-thumb"
					:style="remoteHistoryThumbStyle"
				></span>
			</div>
		</div>
    <button
      v-if="historyLocked && hasNewOutput"
      class="terminal-new-output"
      title="跳转到最新输出"
      @click="resumeOutputFollow(true)"
    >
      <ArrowDown :size="14" />
      <span>有新消息</span>
    </button>
    <div v-if="!connected && statusMessage" class="terminal-error">
      <strong>{{ statusLabel }}</strong
      ><span>{{ statusMessage }}</span
      ><el-button size="small" @click="ensureAttached">重新连接</el-button>
    </div>
    <el-dialog
      v-model="linkPreviewOpen"
      class="terminal-link-dialog"
      title="扫描二维码"
      width="min(420px, calc(100vw - 28px))"
      append-to-body
      destroy-on-close
      @closed="restoreTerminalFocus"
    >
      <div class="terminal-link-preview">
        <div class="terminal-link-qr">
          <QrcodeVue
            v-if="linkPreviewURL"
            :value="linkPreviewURL"
            :size="232"
            level="L"
            render-as="svg"
            background="#ffffff"
            foreground="#111318"
          />
        </div>
        <strong>{{ linkPreviewHost }}</strong>
        <code :title="linkPreviewURL">{{ linkPreviewURL }}</code>
      </div>
      <template #footer>
        <el-button :icon="Copy" @click="copyTerminalLink">复制链接</el-button>
        <el-button :icon="ExternalLink" @click="openTerminalLink"
          >浏览器打开</el-button
        >
      </template>
    </el-dialog>
  </section>
</template>
