<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Maximize2, TerminalSquare, X } from '@lucide/vue';
import { clampPanel, transformPanel, type FloatingPanelBounds } from '../floatingPanel';
const props = defineProps<{ modelValue: boolean; bounds: FloatingPanelBounds; hostName: string; active?: boolean }>();
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; 'update:bounds': [value: FloatingPanelBounds]; resizing: [value: boolean] }>();
const panel = ref<HTMLElement>(), dragging = ref(false);
let observer: ResizeObserver | undefined, stopDrag: (() => void) | undefined;
const style = computed(() => ({ width: `${props.bounds.width}px`, height: `${props.bounds.height}px`, right: `${props.bounds.right}px`, bottom: `${props.bounds.bottom}px` }));
function size() { const host = panel.value?.parentElement; return { width: host?.clientWidth || 0, height: host?.clientHeight || 0 }; }
function update(bounds = props.bounds) {
  const { width, height } = size(); if (!width || !height) return;
  const next = clampPanel(bounds, width, height);
  if (JSON.stringify(next) !== JSON.stringify(props.bounds)) emit('update:bounds', next);
}
function reset() { update({ width: 640, height: 300, right: 12, bottom: 12 }); }
function start(event: PointerEvent, edge = 'move') {
  if (event.button !== 0 || (event.target as HTMLElement).closest('button')) return;
  const { width, height } = size(); if (!width || !height) return;
  event.preventDefault(); event.stopPropagation(); stopDrag?.();
  const target = event.currentTarget as HTMLElement, x = event.clientX, y = event.clientY, original = { ...props.bounds };
  target.setPointerCapture(event.pointerId); dragging.value = true;
  if (edge !== 'move') emit('resizing', true);
  const move = (next: PointerEvent) => emit('update:bounds', transformPanel(original, width, height, next.clientX - x, next.clientY - y, edge));
  const stop = () => { dragging.value = false; target.removeEventListener('pointermove', move); target.removeEventListener('pointerup', stop); target.removeEventListener('pointercancel', stop); target.removeEventListener('lostpointercapture', stop); if (target.hasPointerCapture(event.pointerId)) target.releasePointerCapture(event.pointerId); stopDrag = undefined; if (edge !== 'move') emit('resizing', false); };
  stopDrag = stop;
  target.addEventListener('pointermove', move); target.addEventListener('pointerup', stop); target.addEventListener('pointercancel', stop); target.addEventListener('lostpointercapture', stop);
}
function key(event: KeyboardEvent, edge = 'move') {
  if ((event.target as HTMLElement).closest('button') || !['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home'].includes(event.key)) return;
  event.preventDefault(); event.stopPropagation(); if (event.key === 'Home') { reset(); return; }
  const step = event.shiftKey ? 40 : 10, { width, height } = size();
  emit('update:bounds', transformPanel(props.bounds, width, height, event.key === 'ArrowLeft' ? -step : event.key === 'ArrowRight' ? step : 0, event.key === 'ArrowUp' ? -step : event.key === 'ArrowDown' ? step : 0, edge));
}
watch(() => props.modelValue, async open => { stopDrag?.(); if (open) { await nextTick(); update(); panel.value?.querySelector<HTMLTextAreaElement>('.xterm-helper-textarea')?.focus({ preventScroll: true }); } });
watch(() => props.active, active => { if (active === false) stopDrag?.(); });
onMounted(() => { observer = new ResizeObserver(() => update()); if (panel.value?.parentElement) observer.observe(panel.value.parentElement); update(); });
onBeforeUnmount(() => { stopDrag?.(); observer?.disconnect(); });
</script>
<template>
  <section v-show="modelValue" ref="panel" class="code-terminal-panel code-floating-terminal" :class="{ 'is-dragging': dragging }" :style="style" aria-label="悬浮终端">
    <header tabindex="0" aria-label="移动悬浮终端" title="拖动移动 · 双击恢复默认位置 · 方向键微调" @pointerdown="start($event)" @keydown="key($event)" @dblclick.self="reset"><TerminalSquare :size="13" /><strong @dblclick="reset">终端</strong><span :title="hostName" @dblclick="reset">{{ hostName }}</span><button type="button" title="恢复默认大小和位置" aria-label="重置悬浮终端" @click="reset"><Maximize2 :size="13" /></button><button type="button" title="收起终端 (Ctrl+`)" aria-label="收起终端" @click="emit('update:modelValue', false)"><X :size="14" /></button></header>
    <slot />
    <div v-for="edge in ['n', 's', 'e', 'w', 'nw', 'ne', 'sw', 'se']" :key="edge" class="floating-terminal-handle" :class="`handle-${edge}`" :tabindex="edge === 'nw' ? 0 : -1" role="separator" :aria-label="edge === 'nw' ? '调整悬浮终端大小' : `调整终端边缘 ${edge}`" :aria-orientation="edge === 'e' || edge === 'w' ? 'vertical' : 'horizontal'" @pointerdown="start($event, edge)" @keydown="key($event, edge)" />
  </section>
</template>
<style scoped>
.code-floating-terminal { position: absolute; z-index: 10; display: flex; flex-direction: column; min-height: 0; max-height: none; border: 1px solid var(--floating-terminal-border); --floating-terminal-border: color-mix(in srgb, var(--icon-warm, #b39a72) 78%, var(--line)); border-radius: 5px; background: var(--bg); box-shadow: 0 0 0 1px color-mix(in srgb, var(--icon-warm, #b39a72) 13%, transparent), 0 8px 28px #0005; overflow: visible; }
.code-floating-terminal > header { display: flex; align-items: center; gap: 7px; min-height: 30px; flex-shrink: 0; padding: 2px 7px 2px 10px; border-bottom: 1px solid color-mix(in srgb, var(--floating-terminal-border) 55%, var(--line)); border-radius: 5px 5px 0 0; background: color-mix(in srgb, var(--icon-warm, #b39a72) 7%, var(--surface)); cursor: move; touch-action: none; user-select: none; }
.code-floating-terminal > header > span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.code-floating-terminal > header > button { cursor: pointer; }
.code-floating-terminal :deep(.code-terminal-mount) { border-radius: 0 0 5px 5px; }
.code-floating-terminal:focus-within { border-color: var(--icon-warm, #b39a72); }
:global(:root[data-interface-theme="light"]) .code-floating-terminal { box-shadow: 0 0 0 1px #826b4510, 0 8px 24px #0002; }
.code-floating-terminal.is-dragging { user-select: none; }
.code-floating-terminal.is-dragging :deep(.code-terminal-mount) { pointer-events: none; }
.floating-terminal-handle { position: absolute; z-index: 3; touch-action: none; }
.handle-n, .handle-s { left: 8px; right: 8px; height: 6px; cursor: ns-resize; }
.handle-n { top: -3px; } .handle-s { bottom: -3px; }
.handle-e, .handle-w { top: 8px; bottom: 8px; width: 6px; cursor: ew-resize; }
.handle-e { right: -3px; } .handle-w { left: -3px; }
.handle-nw, .handle-ne, .handle-sw, .handle-se { width: 12px; height: 12px; }
.handle-nw { top: -4px; left: -4px; cursor: nwse-resize; }
.handle-ne { top: -4px; right: -4px; cursor: nesw-resize; }
.handle-sw { bottom: -4px; left: -4px; cursor: nesw-resize; }
.handle-se { bottom: -4px; right: -4px; cursor: nwse-resize; }
.floating-terminal-handle:focus-visible { outline: 1px solid var(--accent); }
</style>
