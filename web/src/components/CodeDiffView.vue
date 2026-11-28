<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { MergeView, unifiedMergeView, getChunks, uncollapseUnchanged, type Chunk } from "@codemirror/merge";
import { ChevronDown, ChevronUp, FoldVertical, Space, WrapText } from "@lucide/vue";
import { diffIgnoringWhitespace, readDiffState, saveDiffState, type DiffReadingState } from "../codeDiff";
import type { CodeHunk } from "../codeHunks";
import { codeLanguage, workbenchEditorTheme } from "../codeLanguages";

const props = withDefaults(defineProps<{ left: string; right: string; path: string; stateKey?: string; inline: boolean; visible: boolean; leftLabel?: string; rightLabel?: string }>(), { leftLabel: "", rightLabel: "" });
const emit = defineEmits<{ hunk: [CodeHunk | undefined] }>();
const mount = ref<HTMLElement>();
const horizontal = ref<HTMLElement>();
const horizontalOverflow = ref(0);
const horizontalPosition = ref(0);
const horizontalWidth = ref(1);
const horizontalThumbWidth = computed(() => Math.max(28, horizontalWidth.value ** 2 / (horizontalWidth.value + horizontalOverflow.value)));
const horizontalThumbStyle = computed(() => ({ width: `${horizontalThumbWidth.value}px`, left: `${horizontalOverflow.value ? horizontalPosition.value / horizontalOverflow.value * (horizontalWidth.value - horizontalThumbWidth.value) : 0}px` }));
let horizontalDrag: { id: number; x: number; left: number } | undefined;
const chunks = ref<readonly Chunk[]>([]);
const activeIndex = ref(-1);
const totalLines = ref(1);
let preferences: Record<string, unknown> = {};
try { preferences = JSON.parse(localStorage.getItem("code-diff-preferences") || "{}") || {}; } catch { /* Optional preferences. */ }
const wrap = ref(preferences.wrap === true), collapsed = ref(preferences.collapsed !== false), ignoreWhitespace = ref(preferences.ignoreWhitespace === true);
const split = ref(typeof preferences.split === "number" ? Math.max(.2, Math.min(.8, preferences.split)) : .5);
const divider = ref<HTMLElement>();
let dividerPointer: number | undefined;
let renderedKey = "";
let renderedInline = false;
let renderedOptions = { wrap: false, collapsed: true, ignoreWhitespace: false };
let expandedA: number[] = [], expandedB: number[] = [];
let merge: MergeView | undefined;
let unified: EditorView | undefined;
let observer: ResizeObserver | undefined;
let generation = 0;
let disposed = false;
let measureFrame = 0;
const description = computed(() => chunks.value.length ? `${activeIndex.value + 1} / ${chunks.value.length} 处修改` : ignoreWhitespace.value ? "没有内容差异" : "没有差异");
function remember(): DiffReadingState | undefined {
  const view = merge?.b || unified, scroller = merge?.dom || unified?.scrollDOM;
  if (!view || !scroller || !renderedKey) return;
  const state = { scroll: scroller.scrollTop, horizontal: horizontalPosition.value, anchorA: merge?.a.state.selection.main.head || 0,
    anchorB: view.state.selection.main.head, top: view.lineBlockAtHeight(scroller.scrollTop).from, active: activeIndex.value,
    inline: renderedInline, ...renderedOptions, expandedA: [...expandedA], expandedB: [...expandedB] };
  saveDiffState(renderedKey, state);
  return state;
}
function dividerMove(event: PointerEvent) {
  if (dividerPointer !== event.pointerId || !mount.value) return;
  const bounds = mount.value.getBoundingClientRect();
  split.value = Math.max(.2, Math.min(.8, (event.clientX - bounds.left) / bounds.width));
}
function dividerDown(event: PointerEvent) {
  if (event.button !== 0) return;
  event.preventDefault(); dividerPointer = event.pointerId;
  divider.value?.focus(); divider.value?.setPointerCapture(event.pointerId); dividerMove(event);
}
function dividerKey(event: KeyboardEvent) {
  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
  event.preventDefault(); event.stopPropagation();
  split.value = event.key === "Home" ? .2 : event.key === "End" ? .8 : Math.max(.2, Math.min(.8, split.value + (event.key === "ArrowLeft" ? -.02 : .02)));
}
function destroy() { merge?.destroy(); unified?.destroy(); merge = undefined; unified = undefined; horizontalOverflow.value = 0; horizontalPosition.value = 0; horizontalDrag = undefined; }
function measureHorizontal() {
  cancelAnimationFrame(measureFrame);
  measureFrame = requestAnimationFrame(() => {
    if (disposed) return;
    horizontalOverflow.value = Math.max(0, ...[merge?.a, merge?.b].map(view => view ? view.scrollDOM.scrollWidth - view.scrollDOM.clientWidth : 0));
    horizontalWidth.value = Math.max(1, mount.value?.clientWidth || 1);
    mount.value?.querySelectorAll<HTMLElement>(".cm-collapsedLines").forEach(node => { node.tabIndex = 0; node.title = "点击展开未修改代码"; });
    setHorizontal(horizontalPosition.value);
  });
}
function measure() { merge?.a.requestMeasure(); merge?.b.requestMeasure(); unified?.requestMeasure(); measureHorizontal(); }
function setHorizontal(value: number) {
  const left = horizontalPosition.value = Math.max(0, Math.min(horizontalOverflow.value, value));
  for (const view of [merge?.a, merge?.b]) if (view) view.scrollDOM.scrollLeft = left;
}
function syncHorizontal(event: Event) {
  const scroller = event.currentTarget as HTMLElement;
  const expected = Math.min(horizontalPosition.value, scroller.scrollWidth - scroller.clientWidth);
  if (Math.abs(scroller.scrollLeft - expected) > 1) setHorizontal(scroller.scrollLeft);
}
function startHorizontalDrag(event: PointerEvent) {
  if (event.button !== 0 || !horizontal.value) return;
  event.preventDefault();
  horizontal.value.focus();
  if (event.target === horizontal.value) setHorizontal((event.clientX - horizontal.value.getBoundingClientRect().left - horizontalThumbWidth.value / 2) / Math.max(1, horizontalWidth.value - horizontalThumbWidth.value) * horizontalOverflow.value);
  horizontalDrag = { id: event.pointerId, x: event.clientX, left: horizontalPosition.value };
  horizontal.value.setPointerCapture(event.pointerId);
}
function moveHorizontal(event: PointerEvent) {
  if (horizontalDrag?.id !== event.pointerId) return;
  setHorizontal(horizontalDrag.left + (event.clientX - horizontalDrag.x) / Math.max(1, horizontalWidth.value - horizontalThumbWidth.value) * horizontalOverflow.value);
}
function horizontalKey(event: KeyboardEvent) {
  const destination = event.key === "Home" ? 0 : event.key === "End" ? horizontalOverflow.value : event.key === "ArrowLeft" ? horizontalPosition.value - 48 : event.key === "ArrowRight" ? horizontalPosition.value + 48 : event.key === "PageUp" ? horizontalPosition.value - horizontalWidth.value : event.key === "PageDown" ? horizontalPosition.value + horizontalWidth.value : undefined;
  if (destination === undefined) return;
  event.preventDefault(); event.stopPropagation(); setHorizontal(destination);
}
function jump(index: number, focus = true) {
  if (!chunks.value.length) { emit("hunk", undefined); return; }
  activeIndex.value = (index + chunks.value.length) % chunks.value.length;
  const chunk = chunks.value[activeIndex.value]!;
  emit("hunk", { fromA: chunk.fromA, toA: chunk.toA, fromB: chunk.fromB, toB: chunk.toB, index: activeIndex.value, total: chunks.value.length });
  for (const [view, position] of [[merge?.a, chunk.fromA], [merge?.b || unified, chunk.fromB]] as const) {
    if (!view) continue;
    const anchor = Math.min(position, view.state.doc.length);
    view.dispatch({ selection: { anchor }, effects: EditorView.scrollIntoView(anchor, { y: "center" }) });
  }
  if (focus) (merge?.b || unified)?.focus();
}
function navigateKey(event: KeyboardEvent) {
  if ((event.key === "Enter" || event.key === " ") && (event.target as HTMLElement).classList.contains("cm-collapsedLines")) {
    event.preventDefault(); (event.target as HTMLElement).click(); return;
  }
  if (event.key !== "F7") return;
  event.preventDefault(); event.stopPropagation();
  jump(activeIndex.value + (event.shiftKey ? -1 : 1));
}
function label(chunk: Chunk, index: number) {
  const view = merge?.b || unified;
  if (!view) return "";
  const first = view.state.doc.lineAt(Math.min(chunk.fromB, view.state.doc.length)).number;
  const last = view.state.doc.lineAt(Math.min(Math.max(chunk.fromB, chunk.toB - 1), view.state.doc.length)).number;
  const kind = chunk.fromA === chunk.toA ? "新增" : chunk.fromB === chunk.toB ? "删除" : "修改";
  return `第 ${index + 1} 处 · 行 ${first}${last > first ? `–${last}` : ""} · ${kind}`;
}
function markerStyle(chunk: Chunk) {
  const view = merge?.b || unified;
  if (!view) return {};
  const first = view.state.doc.lineAt(Math.min(chunk.fromB, view.state.doc.length)).number - 1;
  const last = view.state.doc.lineAt(Math.min(Math.max(chunk.fromB, chunk.toB - 1), view.state.doc.length)).number;
  return { top: `${first / totalLines.value * 100}%`, height: `${Math.max(.7, (last - first) / totalLines.value * 100)}%` };
}
async function create() {
  const request = ++generation;
  await nextTick();
  if (disposed || request !== generation || !mount.value) return;
  remember();
  const key = props.stateKey || props.path;
  const previous = readDiffState(key);
  destroy();
  renderedKey = key; renderedInline = props.inline;
  renderedOptions = { wrap: wrap.value, collapsed: collapsed.value, ignoreWhitespace: ignoreWhitespace.value };
  expandedA = []; expandedB = [];
  activeIndex.value = -1;
  const extensions = [basicSetup, workbenchEditorTheme, codeLanguage(props.path).extension,
    ...(wrap.value ? [EditorView.lineWrapping] : []),
    EditorState.phrases.of({ "$ unchanged lines": "$ 行未修改 · 点击展开" }),
    EditorView.updateListener.of(update => {
      for (const transaction of update.transactions) for (const effect of transaction.effects) if (effect.is(uncollapseUnchanged)) {
        const expanded = getChunks(update.state)?.side === "a" ? expandedA : expandedB;
        if (!expanded.includes(effect.value)) expanded.push(effect.value);
      }
      if (update.geometryChanged || update.viewportChanged) measureHorizontal();
    }),
    EditorState.readOnly.of(true), EditorView.editable.of(false), EditorView.contentAttributes.of({ tabindex: "0" })];
  const options = { highlightChanges: false, gutter: true, collapseUnchanged: collapsed.value ? { margin: 3, minSize: 6 } : undefined,
    diffConfig: { scanLimit: 500, timeout: 100, ...(ignoreWhitespace.value ? { override: diffIgnoringWhitespace } : {}) } };
  if (props.inline) {
    unified = new EditorView({ parent: mount.value, state: EditorState.create({ doc: props.right, extensions: [
      ...extensions, EditorView.contentAttributes.of({ "aria-label": "行内变更对比" }),
      unifiedMergeView({ original: props.left, mergeControls: false, allowInlineDiffs: false, ...options }),
    ] }) });
    chunks.value = getChunks(unified.state)?.chunks || [];
  } else {
    merge = new MergeView({ parent: mount.value,
      a: { doc: props.left, extensions: [...extensions, EditorView.contentAttributes.of({ "aria-label": "变更前" })] },
      b: { doc: props.right, extensions: [...extensions, EditorView.contentAttributes.of({ "aria-label": "变更后" })] },
      ...options,
    });
    chunks.value = merge.chunks;
    merge.a.scrollDOM.addEventListener("scroll", syncHorizontal);
    merge.b.scrollDOM.addEventListener("scroll", syncHorizontal);
  }
  totalLines.value = (merge?.b || unified)!.state.doc.lines;
  if (previous) {
    const sameOptions = previous.inline === props.inline && previous.wrap === wrap.value && previous.collapsed === collapsed.value && previous.ignoreWhitespace === ignoreWhitespace.value;
    activeIndex.value = chunks.value.length ? Math.max(0, Math.min(previous.active, chunks.value.length - 1)) : -1;
    for (const [view, anchor, expanded] of [[merge?.a, previous.anchorA, previous.expandedA], [merge?.b || unified, previous.anchorB, previous.expandedB]] as const) {
      if (!view) continue;
      view.dispatch({ selection: { anchor: Math.min(anchor, view.state.doc.length) },
        effects: sameOptions ? expanded.filter(position => position <= view.state.doc.length).map(position => uncollapseUnchanged.of(position)) : [] });
    }
    const chunk = chunks.value[activeIndex.value];
    emit("hunk", chunk ? { fromA: chunk.fromA, toA: chunk.toA, fromB: chunk.fromB, toB: chunk.toB, index: activeIndex.value, total: chunks.value.length } : undefined);
    requestAnimationFrame(() => requestAnimationFrame(() => {
      if (disposed || request !== generation) return;
      const view = merge?.b || unified, scroller = merge?.dom || unified?.scrollDOM;
      if (view && scroller) scroller.scrollTop = sameOptions ? previous.scroll : view.lineBlockAt(Math.min(previous.top, view.state.doc.length)).top;
      measureHorizontal();
      requestAnimationFrame(() => { if (!disposed && request === generation) setHorizontal(previous.horizontal); });
    }));
  } else if (props.visible) jump(0, false);
  measureHorizontal();
}
onMounted(() => {
  void create();
  observer = new ResizeObserver(measure);
  observer.observe(mount.value!);
});
watch([() => props.left, () => props.right, () => props.path, () => props.stateKey, () => props.inline, wrap, collapsed, ignoreWhitespace], create);
watch([wrap, collapsed, ignoreWhitespace, split], () => {
  try { localStorage.setItem("code-diff-preferences", JSON.stringify({ wrap: wrap.value, collapsed: collapsed.value, ignoreWhitespace: ignoreWhitespace.value, split: split.value })); } catch { /* Optional preferences. */ }
});
watch(split, () => nextTick(measure));
watch(() => props.visible, visible => { if (visible) nextTick(measure); else remember(); });
onBeforeUnmount(() => { if (props.visible) remember(); disposed = true; generation++; cancelAnimationFrame(measureFrame); observer?.disconnect(); destroy(); });
</script>

<template>
  <section class="code-diff-view" :class="{ 'is-wrapped': wrap }" :style="{ '--diff-left-width': `${split * 100}%` }" @keydown.capture="navigateKey" aria-label="文件变更对比">
    <header class="code-diff-toolbar">
      <span class="code-diff-summary" aria-live="polite">{{ description }}</span>
      <span class="code-diff-added">新增</span><span class="code-diff-removed">删除</span>
      <div class="code-diff-options"><button aria-label="自动换行" title="自动换行" :aria-pressed="wrap" @click="wrap = !wrap"><WrapText :size="14" /></button><button aria-label="忽略空白差异" title="忽略每行缩进与行尾空白，仅影响对比显示" :aria-pressed="ignoreWhitespace" @click="ignoreWhitespace = !ignoreWhitespace"><Space :size="14" /></button><button aria-label="折叠未修改代码" title="折叠未修改代码，保留前后 3 行上下文" :aria-pressed="collapsed" @click="collapsed = !collapsed"><FoldVertical :size="14" /></button></div>
      <select :value="activeIndex" aria-label="跳转到修改" :disabled="!chunks.length" @change="jump(Number(($event.target as HTMLSelectElement).value))"><option v-if="!chunks.length" :value="-1">没有差异</option><option v-for="(chunk, index) in chunks" :key="index" :value="index">{{ label(chunk, index) }}</option></select>
      <button :disabled="!chunks.length" aria-label="上一处修改" title="上一处修改 (Shift+F7)" @click="jump(activeIndex - 1)"><ChevronUp :size="15" /></button>
      <button :disabled="!chunks.length" aria-label="下一处修改" title="下一处修改 (F7)" @click="jump(activeIndex + 1)"><ChevronDown :size="15" /></button>
      <slot name="actions" />
    </header>
    <div v-if="leftLabel || rightLabel" class="code-diff-labels" :class="{ inline }"><span>{{ leftLabel }}</span><span>{{ rightLabel }}</span></div>
    <div class="code-diff-body">
      <div ref="mount" class="code-diff-mount" />
      <div v-if="!inline" ref="divider" class="code-diff-divider" :style="{ left: `calc(${split * 100}% - ${18 * split}px)` }" role="separator" aria-label="调整对比左右宽度" aria-orientation="vertical" :aria-valuenow="Math.round(split * 100)" :aria-valuemin="20" :aria-valuemax="80" tabindex="0" title="拖动调整左右宽度，双击恢复均分" @pointerdown="dividerDown" @pointermove="dividerMove" @pointerup="dividerPointer = undefined" @pointercancel="dividerPointer = undefined" @lostpointercapture="dividerPointer = undefined" @dblclick="split = .5" @keydown="dividerKey" />
      <nav class="code-diff-overview" aria-label="修改位置分布"><button v-for="(chunk, index) in chunks" :key="index" :title="label(chunk, index)" :aria-label="label(chunk, index)" :aria-current="index === activeIndex ? 'location' : undefined" :class="{ deletion: chunk.fromB === chunk.toB, insertion: chunk.fromA === chunk.toA, selected: index === activeIndex }" :style="markerStyle(chunk)" @click="jump(index)" /></nav>
    </div>
    <div v-if="!inline && horizontalOverflow > 0" ref="horizontal" class="code-diff-horizontal" role="scrollbar" aria-label="代码横向滚动" aria-orientation="horizontal" :aria-valuenow="Math.round(horizontalPosition)" :aria-valuemax="horizontalOverflow" :aria-valuemin="0" tabindex="0" @pointerdown="startHorizontalDrag" @pointermove="moveHorizontal" @pointerup="horizontalDrag = undefined" @pointercancel="horizontalDrag = undefined" @lostpointercapture="horizontalDrag = undefined" @keydown="horizontalKey" @wheel.prevent="setHorizontal(horizontalPosition + ($event.deltaX || $event.deltaY))">
      <div class="code-diff-horizontal-thumb" :style="horizontalThumbStyle" />
    </div>
  </section>
</template>

<style scoped>
.code-diff-view { --diff-add-line: #264c36a6; --diff-remove-line: #602e34a6; --diff-add-ink: #a4d6af; --diff-remove-ink: #edb0b5; display: flex; flex-direction: column; height: 100%; min-height: 0; overflow: hidden; }
.code-diff-toolbar { display: flex; align-items: center; flex: 0 0 auto; min-height: 34px; gap: 9px; padding: 3px 10px; border-bottom: 1px solid var(--line); background: var(--surface); font-size: 11px; }
.code-diff-summary { white-space: nowrap; color: var(--muted); }
.code-diff-options { display: flex; align-items: center; gap: 1px; }
.code-diff-options button { display: inline-flex; width: 25px; height: 25px; align-items: center; justify-content: center; border: 0; border-radius: 3px; padding: 0; background: transparent; color: var(--muted); cursor: pointer; }
.code-diff-options button:hover { color: var(--text); background: var(--surface-2); }
.code-diff-options button[aria-pressed="true"] { color: var(--text); background: var(--accent-soft, #6383a733); }
.code-diff-added { color: var(--diff-add-ink); }
.code-diff-added::before, .code-diff-removed::before { content: ""; display: inline-block; width: 9px; height: 9px; margin-right: 4px; background: var(--diff-add-line); border: 1px solid currentColor; }
.code-diff-removed { color: var(--diff-remove-ink); }
.code-diff-removed::before { background: var(--diff-remove-line); }
.code-diff-toolbar select { min-width: 0; max-width: 260px; margin-left: auto; padding: 3px 6px; border: 1px solid var(--line); border-radius: 3px; background: var(--bg); color: var(--text); font: inherit; }
.code-diff-labels { display: flex; gap: 0; flex: 0 0 23px; border-bottom: 1px solid var(--line); }
.code-diff-labels span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 4px 10px; color: var(--muted); font: 10px/1.3 monospace; }
.code-diff-labels:not(.inline) { margin-right: 18px; }
.code-diff-labels:not(.inline) span:first-child { flex: 0 0 var(--diff-left-width); box-sizing: border-box; }
.code-diff-labels span + span { border-left: 1px solid var(--line); }
.code-diff-labels.inline span { flex: 0 1 auto; }
.code-diff-labels.inline span + span { border: 0; }
.code-diff-body { position: relative; display: flex; flex: 1; min-height: 0; overflow: hidden; }
.code-diff-divider { position: absolute; z-index: 3; top: 0; bottom: 0; width: 7px; transform: translateX(-50%); cursor: col-resize; touch-action: none; }
.code-diff-divider:hover, .code-diff-divider:focus-visible { background: var(--accent-soft, #6383a733); outline: none; }
.code-diff-mount { flex: 1; min-width: 0; min-height: 0; overflow: hidden; }
/* MergeView owns the shared vertical scroll. Its editors must keep their natural height. */
.code-diff-mount :deep(.cm-mergeView) { height: 100%; overflow: auto; }
.code-diff-mount :deep(.cm-mergeViewEditor) { min-width: 0; }
.code-diff-mount :deep(.cm-mergeViewEditor:first-child) { flex: 0 0 var(--diff-left-width); }
.is-wrapped .code-diff-mount :deep(.cm-mergeViewEditor .cm-content) { white-space: pre-wrap; overflow-wrap: anywhere; }
.code-diff-mount :deep(.cm-collapsedLines) { padding: 5px 12px; color: var(--muted); background: var(--surface); border-block: 1px solid var(--line); font: 11px/1.5 var(--font-mono, monospace); cursor: pointer; }
.code-diff-mount :deep(.cm-collapsedLines:hover), .code-diff-mount :deep(.cm-collapsedLines:focus-visible) { color: var(--text); background: var(--surface-2); }
.code-diff-mount :deep(.cm-merge-b) { border-left: 1px solid var(--line); }
.code-diff-mount :deep(.cm-mergeViewEditor .cm-scroller) { overflow-x: auto; scrollbar-width: none; }
.code-diff-mount :deep(.cm-mergeViewEditor .cm-scroller::-webkit-scrollbar) { width: 0; height: 0; }
.code-diff-horizontal { position: relative; flex: 0 0 14px; height: 14px; margin-right: 18px; background: var(--surface); touch-action: none; user-select: none; }
.code-diff-horizontal-thumb { position: absolute; top: 3px; height: 8px; border-radius: 4px; background: color-mix(in srgb, var(--muted) 55%, transparent); cursor: ew-resize; }
.code-diff-horizontal:hover .code-diff-horizontal-thumb, .code-diff-horizontal:focus-visible .code-diff-horizontal-thumb { background: var(--muted); }
:global(:root[data-interface-theme="light"]) .code-diff-view { --diff-add-line: #d9efdd; --diff-remove-line: #f6dcdf; --diff-add-ink: #1e6834; --diff-remove-ink: #a52b3b; }
.code-diff-mount :deep(.cm-merge-a .cm-line.cm-changedLine), .code-diff-mount :deep(.cm-deletedChunk) { background-color: var(--diff-remove-line); box-shadow: inset 3px 0 var(--diff-remove-ink); }
.code-diff-mount :deep(.cm-editor:not(.cm-merge-a) .cm-line.cm-changedLine), .code-diff-mount :deep(.cm-line.cm-inlineChangedLine) { background-color: var(--diff-add-line); box-shadow: inset 3px 0 var(--diff-add-ink); }
.code-diff-mount :deep(.cm-changedText), .code-diff-mount :deep(.cm-deletedText), .code-diff-mount :deep(.cm-insertedText), .code-diff-mount :deep(.cm-insertedLine), .code-diff-mount :deep(.cm-deletedLine) { background: none; box-shadow: none; text-decoration: none; font-weight: inherit; }
.code-diff-mount :deep(.cm-changeGutter) { width: 14px; padding: 0 2px; }
.code-diff-mount :deep(.cm-merge-a .cm-changedLineGutter), .code-diff-mount :deep(.cm-deletedLineGutter) { background: var(--diff-remove-line); color: var(--diff-remove-ink); font-weight: bold; text-align: center; }
.code-diff-mount :deep(.cm-merge-b .cm-changedLineGutter), .code-diff-mount :deep(.cm-inlineChangedLineGutter) { background: var(--diff-add-line); color: var(--diff-add-ink); font-weight: bold; text-align: center; }
.code-diff-mount :deep(.cm-merge-a .cm-changedLineGutter::before), .code-diff-mount :deep(.cm-deletedLineGutter::before) { content: "−"; }
.code-diff-mount :deep(.cm-merge-b .cm-changedLineGutter::before), .code-diff-mount :deep(.cm-inlineChangedLineGutter::before) { content: "+"; }
.code-diff-overview { position: relative; flex: 0 0 18px; border-left: 1px solid var(--line); background: var(--surface); }
.code-diff-overview button { position: absolute; left: 3px; width: 12px; min-height: 7px; padding: 0; border: 0; border-radius: 0; background: #c2a56c; cursor: pointer; }
.code-diff-overview button.insertion { background: #80b68b; }
.code-diff-overview button.deletion { background: #d48690; }
.code-diff-overview button.selected { outline: 1px solid var(--text); outline-offset: 1px; }
@media (max-width: 640px) { .code-diff-toolbar { flex-wrap: wrap; gap: 3px; padding: 3px 4px; } .code-diff-toolbar select { flex: 1; width: 70px; } .code-diff-added, .code-diff-removed { display: none; } .code-diff-labels span { padding: 4px 6px; font-size: 9px; } }
</style>
