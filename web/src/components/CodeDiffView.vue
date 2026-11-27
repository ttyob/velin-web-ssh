<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { MergeView, unifiedMergeView, getChunks, type Chunk } from "@codemirror/merge";
import { ChevronDown, ChevronUp } from "@lucide/vue";
import type { CodeHunk } from "../codeHunks";
import { codeLanguage, workbenchEditorTheme } from "../codeLanguages";

const props = withDefaults(defineProps<{ left: string; right: string; path: string; inline: boolean; visible: boolean; leftLabel?: string; rightLabel?: string }>(), { leftLabel: "", rightLabel: "" });
const emit = defineEmits<{ hunk: [CodeHunk | undefined] }>();
const mount = ref<HTMLElement>();
const chunks = ref<readonly Chunk[]>([]);
const activeIndex = ref(-1);
const totalLines = ref(1);
let merge: MergeView | undefined;
let unified: EditorView | undefined;
let observer: ResizeObserver | undefined;
let generation = 0;
let disposed = false;
const description = computed(() => chunks.value.length ? `${activeIndex.value + 1} / ${chunks.value.length} 处修改` : "没有差异");
function destroy() { merge?.destroy(); unified?.destroy(); merge = undefined; unified = undefined; }
function measure() { merge?.a.requestMeasure(); merge?.b.requestMeasure(); unified?.requestMeasure(); }
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
  destroy();
  activeIndex.value = -1;
  const extensions = [basicSetup, workbenchEditorTheme, codeLanguage(props.path).extension,
    EditorState.readOnly.of(true), EditorView.editable.of(false), EditorView.contentAttributes.of({ tabindex: "0" })];
  if (props.inline) {
    unified = new EditorView({ parent: mount.value, state: EditorState.create({ doc: props.right, extensions: [
      ...extensions, EditorView.contentAttributes.of({ "aria-label": "行内变更对比" }),
      unifiedMergeView({ original: props.left, mergeControls: false, allowInlineDiffs: true, highlightChanges: true, gutter: true }),
    ] }) });
    chunks.value = getChunks(unified.state)?.chunks || [];
  } else {
    merge = new MergeView({ parent: mount.value,
      a: { doc: props.left, extensions: [...extensions, EditorView.contentAttributes.of({ "aria-label": "变更前" })] },
      b: { doc: props.right, extensions: [...extensions, EditorView.contentAttributes.of({ "aria-label": "变更后" })] },
      highlightChanges: true, gutter: true,
    });
    chunks.value = merge.chunks;
  }
  totalLines.value = (merge?.b || unified)!.state.doc.lines;
  if (props.visible) jump(0, false);
}
onMounted(() => {
  void create();
  observer = new ResizeObserver(measure);
  observer.observe(mount.value!);
});
watch([() => props.left, () => props.right, () => props.path, () => props.inline], create);
watch(() => props.visible, visible => { if (visible) nextTick(measure); });
onBeforeUnmount(() => { disposed = true; generation++; observer?.disconnect(); destroy(); });
</script>

<template>
  <section class="code-diff-view" @keydown.capture="navigateKey" aria-label="文件变更对比">
    <header class="code-diff-toolbar">
      <span class="code-diff-summary" aria-live="polite">{{ description }}</span>
      <span class="code-diff-added">新增</span><span class="code-diff-removed">删除</span>
      <select :value="activeIndex" aria-label="跳转到修改" :disabled="!chunks.length" @change="jump(Number(($event.target as HTMLSelectElement).value))"><option v-if="!chunks.length" :value="-1">没有差异</option><option v-for="(chunk, index) in chunks" :key="index" :value="index">{{ label(chunk, index) }}</option></select>
      <button :disabled="!chunks.length" aria-label="上一处修改" title="上一处修改 (Shift+F7)" @click="jump(activeIndex - 1)"><ChevronUp :size="15" /></button>
      <button :disabled="!chunks.length" aria-label="下一处修改" title="下一处修改 (F7)" @click="jump(activeIndex + 1)"><ChevronDown :size="15" /></button>
      <slot name="actions" />
    </header>
    <div v-if="leftLabel || rightLabel" class="code-diff-labels" :class="{ inline }"><span>{{ leftLabel }}</span><span>{{ rightLabel }}</span></div>
    <div class="code-diff-body">
      <div ref="mount" class="code-diff-mount" />
      <nav class="code-diff-overview" aria-label="修改位置分布"><button v-for="(chunk, index) in chunks" :key="index" :title="label(chunk, index)" :aria-label="label(chunk, index)" :aria-current="index === activeIndex ? 'location' : undefined" :class="{ deletion: chunk.fromB === chunk.toB, insertion: chunk.fromA === chunk.toA, selected: index === activeIndex }" :style="markerStyle(chunk)" @click="jump(index)" /></nav>
    </div>
  </section>
</template>

<style scoped>
.code-diff-view { --diff-add-line: #264c36a6; --diff-remove-line: #602e34a6; --diff-add-word: #498357b3; --diff-remove-word: #a74e58a6; --diff-add-ink: #a4d6af; --diff-remove-ink: #edb0b5; display: flex; flex-direction: column; height: 100%; min-height: 0; overflow: hidden; }
.code-diff-toolbar { display: flex; align-items: center; flex: 0 0 auto; min-height: 34px; gap: 9px; padding: 3px 10px; border-bottom: 1px solid var(--line); background: var(--surface); font-size: 11px; }
.code-diff-summary { white-space: nowrap; color: var(--muted); }
.code-diff-added { color: var(--diff-add-ink); }
.code-diff-added::before, .code-diff-removed::before { content: ""; display: inline-block; width: 9px; height: 9px; margin-right: 4px; background: var(--diff-add-line); border: 1px solid currentColor; }
.code-diff-removed { color: var(--diff-remove-ink); }
.code-diff-removed::before { background: var(--diff-remove-line); }
.code-diff-toolbar select { min-width: 0; max-width: 260px; margin-left: auto; padding: 3px 6px; border: 1px solid var(--line); border-radius: 3px; background: var(--bg); color: var(--text); font: inherit; }
.code-diff-labels { display: flex; gap: 0; flex: 0 0 23px; border-bottom: 1px solid var(--line); }
.code-diff-labels span { flex: 1; padding: 4px 10px; color: var(--muted); font: 10px/1.3 monospace; }
.code-diff-labels span + span { border-left: 1px solid var(--line); }
.code-diff-labels.inline span { flex: 0 1 auto; }
.code-diff-labels.inline span + span { border: 0; }
.code-diff-body { display: flex; flex: 1; min-height: 0; overflow: hidden; }
.code-diff-mount { flex: 1; min-width: 0; min-height: 0; overflow: hidden; }
/* MergeView owns the shared vertical scroll. Its editors must keep their natural height. */
.code-diff-mount :deep(.cm-mergeView) { height: 100%; overflow: auto; }
.code-diff-mount :deep(.cm-mergeViewEditor) { min-width: 0; }
.code-diff-mount :deep(.cm-merge-b) { border-left: 1px solid var(--line); }
.code-diff-mount :deep(.cm-mergeViewEditor .cm-scroller) { overflow-x: auto; }
:global(:root[data-interface-theme="light"]) .code-diff-view { --diff-add-line: #d9efdd; --diff-remove-line: #f6dcdf; --diff-add-word: #9ed1a8; --diff-remove-word: #eda7af; --diff-add-ink: #1e6834; --diff-remove-ink: #a52b3b; }
.code-diff-mount :deep(.cm-merge-a .cm-line.cm-changedLine), .code-diff-mount :deep(.cm-deletedChunk) { background-color: var(--diff-remove-line); box-shadow: inset 3px 0 var(--diff-remove-ink); }
.code-diff-mount :deep(.cm-merge-b .cm-line.cm-changedLine), .code-diff-mount :deep(.cm-line.cm-inlineChangedLine) { background-color: var(--diff-add-line); box-shadow: inset 3px 0 var(--diff-add-ink); }
.code-diff-mount :deep(.cm-deletedText), .code-diff-mount :deep(.cm-merge-a .cm-changedText) { background: var(--diff-remove-word); box-shadow: 0 1px var(--diff-remove-ink); font-weight: 600; }
.code-diff-mount :deep(.cm-insertedText), .code-diff-mount :deep(.cm-merge-b .cm-changedText) { background: var(--diff-add-word); box-shadow: 0 1px var(--diff-add-ink); font-weight: 600; }
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
@media (max-width: 640px) { .code-diff-toolbar { gap: 3px; padding: 3px 4px; } .code-diff-toolbar select { flex: 1; width: 70px; } .code-diff-added, .code-diff-removed { display: none; } .code-diff-labels span { padding: 4px 6px; font-size: 9px; } }
</style>
