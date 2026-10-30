<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from "vue";
import TerminalPane from "./TerminalPane.vue";
import type { PaneNode, Preferences, TerminalSession } from "../types";
import type { TerminalAttentionEvent } from "../terminalAttention";
import { findTerminalTheme } from "../themePresets";

defineOptions({ name: "SplitPaneNode" });
const props = defineProps<{
  node: PaneNode;
  sessions: TerminalSession[];
  preferences: Preferences;
  root: boolean;
  visible: boolean;
  focusedLeafId?: string;
  maximizedLeafId?: string;
  mobileCtrl?: boolean;
  mobileAlt?: boolean;
}>();
const emit = defineEmits<{
  focus: [leafID: string];
  context: [event: MouseEvent, leafID: string];
  close: [leafID: string];
  status: [id: string, status: string, message?: string];
  title: [id: string, title: string];
  directory: [id: string, path: string];
  conversation: [id: string, active: boolean];
  attention: [id: string, event: TerminalAttentionEvent];
  ratio: [nodeID: string, ratio: number];
  modifiersUsed: [];
}>();
const firstRef = ref<any>(),
  secondRef = ref<any>(),
  terminalRef = ref<any>();
let resizeFrame: number | undefined;

const session = computed(() => {
  const node = props.node;
  return node.type === "leaf"
    ? props.sessions.find((item) => item.id === node.sessionID)
    : undefined;
});
const leafIDs = computed(() => collectLeafIDs(props.node));
const hiddenByMaximize = computed(() =>
  Boolean(
    props.maximizedLeafId && !leafIDs.value.includes(props.maximizedLeafId),
  ),
);
const paneColors = computed(() => {
  const theme = findTerminalTheme(props.preferences.terminalTheme);
  return {
    "--pane-background": theme.background,
    "--pane-foreground": theme.foreground,
    "--pane-focus": theme.cursor,
  };
});
const splitStyle = computed(() => {
  if (props.node.type !== "split") return {};
  const firstHasMax = Boolean(
    props.maximizedLeafId &&
    collectLeafIDs(props.node.first).includes(props.maximizedLeafId),
  );
  const secondHasMax = Boolean(
    props.maximizedLeafId &&
    collectLeafIDs(props.node.second).includes(props.maximizedLeafId),
  );
  if (props.node.direction === "horizontal") {
    if (firstHasMax) return { gridTemplateColumns: "minmax(0, 1fr) 0 0" };
    if (secondHasMax) return { gridTemplateColumns: "0 0 minmax(0, 1fr)" };
    return {
      gridTemplateColumns: `minmax(280px, ${props.node.ratio}fr) 9px minmax(280px, ${1 - props.node.ratio}fr)`,
    };
  }
  if (firstHasMax) return { gridTemplateRows: "minmax(0, 1fr) 0 0" };
  if (secondHasMax) return { gridTemplateRows: "0 0 minmax(0, 1fr)" };
  return {
    gridTemplateRows: `minmax(180px, ${props.node.ratio}fr) 9px minmax(180px, ${1 - props.node.ratio}fr)`,
  };
});

function collectLeafIDs(node: PaneNode): string[] {
  return node.type === "leaf"
    ? [node.id]
    : [...collectLeafIDs(node.first), ...collectLeafIDs(node.second)];
}

function startResize(event: PointerEvent) {
  if (props.node.type !== "split") return;
  event.preventDefault();
  const split = props.node;
  const host = (event.currentTarget as HTMLElement).parentElement!;
  const rect = host.getBoundingClientRect();
  const horizontal = split.direction === "horizontal";
  const styleProperty = horizontal
    ? "gridTemplateColumns"
    : "gridTemplateRows";
  let nextRatio = split.ratio;
  document.body.classList.add("is-resizing-panes");
  const move = (next: PointerEvent) => {
    const raw =
      horizontal
        ? (next.clientX - rect.left) / rect.width
        : (next.clientY - rect.top) / rect.height;
    nextRatio = Math.min(0.82, Math.max(0.18, raw));
    const first = `${nextRatio}fr`;
    const second = `${1 - nextRatio}fr`;
    host.style[styleProperty] = horizontal
      ? `minmax(280px, ${first}) 9px minmax(280px, ${second})`
      : `minmax(180px, ${first}) 9px minmax(180px, ${second})`;
  };
  const stop = () => {
    document.body.classList.remove("is-resizing-panes");
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", stop);
    window.removeEventListener("pointercancel", stop);
    if (resizeFrame !== undefined) cancelAnimationFrame(resizeFrame);
    emit("ratio", split.id, nextRatio);
    // Keep the direct CSS value until Vue has committed the final ratio. This
    // avoids one frame reverting to the old layout when the pointer is released.
    void nextTick(() => {
      host.style.removeProperty(styleProperty);
      resizeFrame = requestAnimationFrame(resize);
    });
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", stop);
  window.addEventListener("pointercancel", stop);
}
function scheduleResize() {
  if (resizeFrame !== undefined) cancelAnimationFrame(resizeFrame);
  resizeFrame = requestAnimationFrame(resize);
}
function resize() {
  firstRef.value?.resize?.();
  secondRef.value?.resize?.();
  terminalRef.value?.resize?.();
}
onBeforeUnmount(() => {
  if (resizeFrame !== undefined) cancelAnimationFrame(resizeFrame);
});
function sendKey(key: string) {
  if (props.node.type === "leaf") {
    if (props.node.id === props.focusedLeafId)
      terminalRef.value?.sendKey?.(key);
    return;
  }
  firstRef.value?.sendKey?.(key);
  secondRef.value?.sendKey?.(key);
}
function sendModifiedKey(key: string) {
  if (props.node.type === "leaf") {
    if (props.node.id === props.focusedLeafId)
      terminalRef.value?.sendModifiedKey?.(key);
    return;
  }
  firstRef.value?.sendModifiedKey?.(key);
  secondRef.value?.sendModifiedKey?.(key);
}
function sendText(leafID: string, text: string, options?: { confirmPaste?: boolean }) {
  if (props.node.type === "leaf") {
    if (props.node.id === leafID) terminalRef.value?.sendInput?.(text, options);
    return;
  }
  firstRef.value?.sendText?.(leafID, text, options);
  secondRef.value?.sendText?.(leafID, text, options);
}
function focusLeaf(leafID?: string) {
  if (!leafID) return;
  if (props.node.type === "leaf") {
    if (props.node.id === leafID) terminalRef.value?.focus?.();
    return;
  }
  firstRef.value?.focusLeaf?.(leafID);
  secondRef.value?.focusLeaf?.(leafID);
}
function copySelection(leafID: string) {
  if (props.node.type === "leaf") {
    if (props.node.id === leafID) terminalRef.value?.copySelection?.();
    return;
  }
  firstRef.value?.copySelection?.(leafID);
  secondRef.value?.copySelection?.(leafID);
}
function pasteClipboard(leafID: string) {
  if (props.node.type === "leaf") {
    if (props.node.id === leafID) terminalRef.value?.pasteClipboard?.();
    return;
  }
  firstRef.value?.pasteClipboard?.(leafID);
  secondRef.value?.pasteClipboard?.(leafID);
}
function selectAll(leafID: string) {
  if (props.node.type === "leaf") {
    if (props.node.id === leafID) terminalRef.value?.selectAll?.();
    return;
  }
  firstRef.value?.selectAll?.(leafID);
  secondRef.value?.selectAll?.(leafID);
}
function clearTerminal(leafID: string) {
  if (props.node.type === "leaf") {
    if (props.node.id === leafID) terminalRef.value?.clearTerminal?.();
    return;
  }
  firstRef.value?.clearTerminal?.(leafID);
  secondRef.value?.clearTerminal?.(leafID);
}
function downloadText(leafID: string) {
  if (props.node.type === "leaf") {
    if (props.node.id === leafID) terminalRef.value?.downloadText?.();
    return;
  }
  firstRef.value?.downloadText?.(leafID);
  secondRef.value?.downloadText?.(leafID);
}
defineExpose({
  resize,
  sendKey,
  sendModifiedKey,
  sendText,
  focusLeaf,
  copySelection,
  pasteClipboard,
  selectAll,
  clearTerminal,
  downloadText,
});
</script>

<template>
  <div
    v-if="node.type === 'split'"
    class="split-node"
    :class="[
      `split-${node.direction}`,
      { 'pane-maximized-away': hiddenByMaximize },
    ]"
    :style="[paneColors, splitStyle]"
  >
    <SplitPaneNode
      ref="firstRef"
      :node="node.first"
      :sessions="sessions"
      :preferences="preferences"
      :root="false"
      :visible="visible && !hiddenByMaximize"
      :focused-leaf-id="focusedLeafId"
      :maximized-leaf-id="maximizedLeafId"
      :mobile-ctrl="mobileCtrl"
      :mobile-alt="mobileAlt"
      @focus="(id) => emit('focus', id)"
      @context="(...args) => emit('context', ...args)"
      @close="(id) => emit('close', id)"
      @status="(...args) => emit('status', ...args)"
      @title="(...args) => emit('title', ...args)"
      @directory="(...args) => emit('directory', ...args)"
      @conversation="(...args) => emit('conversation', ...args)"
      @attention="(...args) => emit('attention', ...args)"
      @ratio="(...args) => emit('ratio', ...args)"
      @modifiers-used="emit('modifiersUsed')"
    />
    <button
      v-show="!maximizedLeafId"
      class="split-resizer"
      :class="`resizer-${node.direction}`"
      title="拖动调整分屏大小"
      @pointerdown="startResize"
    >
      <span />
    </button>
    <SplitPaneNode
      ref="secondRef"
      :node="node.second"
      :sessions="sessions"
      :preferences="preferences"
      :root="false"
      :visible="visible && !hiddenByMaximize"
      :focused-leaf-id="focusedLeafId"
      :maximized-leaf-id="maximizedLeafId"
      :mobile-ctrl="mobileCtrl"
      :mobile-alt="mobileAlt"
      @focus="(id) => emit('focus', id)"
      @context="(...args) => emit('context', ...args)"
      @close="(id) => emit('close', id)"
      @status="(...args) => emit('status', ...args)"
      @title="(...args) => emit('title', ...args)"
      @directory="(...args) => emit('directory', ...args)"
      @conversation="(...args) => emit('conversation', ...args)"
      @attention="(...args) => emit('attention', ...args)"
      @ratio="(...args) => emit('ratio', ...args)"
      @modifiers-used="emit('modifiersUsed')"
    />
  </div>
  <div
    v-else
    class="split-leaf"
    :class="{
      focused: node.id === focusedLeafId,
      'mobile-pane-hidden': Boolean(
        focusedLeafId && node.id !== focusedLeafId,
      ),
      'pane-maximized-away': hiddenByMaximize,
    }"
    @pointerdown="emit('focus', node.id)"
    @contextmenu.prevent="emit('context', $event, node.id)"
  >
    <TerminalPane
      v-if="session"
      ref="terminalRef"
      :session="session"
      :preferences="preferences"
      :visible="visible && !hiddenByMaximize"
      :closable="!root"
      :mobile-ctrl="mobileCtrl"
      :mobile-alt="mobileAlt"
      @close="emit('close', node.id)"
      @status="(...args) => emit('status', ...args)"
      @title="(...args) => emit('title', ...args)"
      @directory="(...args) => emit('directory', ...args)"
      @conversation="(...args) => emit('conversation', ...args)"
      @attention="(...args) => emit('attention', ...args)"
      @modifiers-used="emit('modifiersUsed')"
    />
    <div v-else class="empty-pane"><span>终端会话已不可用</span></div>
  </div>
</template>

<style scoped>
/* Keep the divider with the terminal palette even when the surrounding app
   uses a different theme. The full 9 px strip remains a drag target. */
.split-node {
  background: var(--pane-background);
}
.split-resizer {
  background: color-mix(in srgb, var(--pane-background) 92%, var(--pane-foreground));
}
.split-resizer span {
  background: color-mix(in srgb, var(--pane-background) 20%, var(--pane-foreground));
  opacity: 1;
}
.split-resizer:hover,
.split-resizer:focus-visible,
.split-resizer:active {
  background: color-mix(in srgb, var(--pane-background) 84%, var(--pane-foreground));
  box-shadow: none;
}
.split-resizer:hover span,
.split-resizer:focus-visible span,
.split-resizer:active span {
  background: var(--pane-foreground);
  opacity: 1;
}
.split-resizer:focus-visible {
  outline: 2px solid var(--pane-focus);
  outline-offset: -2px;
}
</style>
