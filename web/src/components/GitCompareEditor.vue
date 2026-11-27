<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { basicSetup } from "codemirror";
import { isolateHistory } from "@codemirror/commands";
import { EditorState, RangeSet, StateEffect, StateField, type Range } from "@codemirror/state";
import { Decoration, EditorView, GutterMarker, WidgetType, gutter, type DecorationSet } from "@codemirror/view";
import { Chunk } from "@codemirror/merge";
import { ArrowLeft, ArrowRight, ChevronDown, ChevronUp, GitMerge } from "@lucide/vue";
import { editorTheme } from "../editorTheme";
import { joinConflictSides, mapTextRanges, normalizeGitText, overlaps, parseMergeConflicts, type TextRange } from "../gitMerge";

const props = defineProps<{ left: string; right: string; result?: string; merge?: boolean }>();
const emit = defineEmits<{ change: [content: string]; conflicts: [count: number] }>();
const leftElement = ref<HTMLElement>();
const resultElement = ref<HTMLElement>();
const rightElement = ref<HTMLElement>();
const vertical = ref<HTMLElement>();
const horizontal = ref<HTMLElement>();
const scrollHeight = ref(1);
const scrollWidth = ref(1);
const yRatio = ref(0), xRatio = ref(0);
const verticalThumb = computed(() => Math.min(100, Math.max(4, (vertical.value?.clientHeight || 1) / scrollHeight.value * 100)));
const horizontalThumb = computed(() => Math.min(100, Math.max(4, (horizontal.value?.clientWidth || 1) / scrollWidth.value * 100)));
const conflictCount = ref(0);
const changeCount = ref(0);
const activeIndex = ref(0);
let leftView: EditorView | undefined, centerView: EditorView | undefined, rightView: EditorView | undefined;
let observer: ResizeObserver | undefined;
let timer: ReturnType<typeof setTimeout> | undefined;
let frame = 0;
let syncing = false;
let disposed = false;
let navigation: TextRange[] = [];
const views = () => [leftView, centerView, rightView].filter((v): v is EditorView => Boolean(v));
const updateHighlights = StateEffect.define<DecorationSet>();
const highlights = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(value, transaction) {
    value = value.map(transaction.changes);
    for (const effect of transaction.effects) if (effect.is(updateHighlights)) value = effect.value;
    return value;
  },
  provide: field => EditorView.decorations.from(field),
});
class Spacer extends WidgetType {
  constructor(readonly height: number) { super(); }
  eq(other: Spacer) { return other.height === this.height; }
  toDOM() { const el = document.createElement("div"); el.className = "git-alignment-space"; el.style.height = `${this.height}px`; return el; }
  get estimatedHeight() { return this.height; }
}
const updateSpacers = StateEffect.define<DecorationSet>();
const spacers = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(value, tr) {
    value = value.map(tr.changes);
    for (const e of tr.effects) if (e.is(updateSpacers)) value = e.value;
    return value;
  },
  provide: field => EditorView.decorations.from(field),
});

class ApplyMarker extends GutterMarker {
  constructor(readonly side: "left" | "right", readonly conflict: boolean, readonly apply: () => void) { super(); }
  toDOM() {
    const button = document.createElement("button");
    button.type = "button";
    button.className = `git-apply-arrow${this.conflict ? " is-conflict" : ""}`;
    button.title = `使用${this.side === "left" ? "左侧" : "右侧"}${this.conflict ? "冲突" : "变更"}`;
    button.setAttribute("aria-label", button.title);
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24"); svg.setAttribute("width", "17"); svg.setAttribute("height", "17");
    const path = document.createElementNS(svg.namespaceURI, "path");
    path.setAttribute("d", this.side === "left" ? "M4 12h16m-6-6 6 6-6 6" : "M20 12H4m6-6-6 6 6 6");
    path.setAttribute("fill", "none"); path.setAttribute("stroke", "currentColor"); path.setAttribute("stroke-width", "2");
    svg.append(path); button.append(svg);
    button.addEventListener("mousedown", e => e.preventDefault());
    button.addEventListener("click", this.apply);
    return button;
  }
}
const updateArrows = StateEffect.define<Range<GutterMarker>[]>();
const arrows = StateField.define<RangeSet<GutterMarker>>({
  create: () => RangeSet.empty,
  update(value, tr) {
    value = value.map(tr.changes);
    for (const e of tr.effects) if (e.is(updateArrows)) value = RangeSet.of(e.value, true);
    return value;
  },
});

function createEditor(parent: HTMLElement, text: string, editable: boolean, side?: "left" | "right") {
  return new EditorView({ parent, state: EditorState.create({
    doc: normalizeGitText(text),
    extensions: [basicSetup, editorTheme, highlights, spacers, arrows,
      EditorState.readOnly.of(!editable), EditorView.editable.of(editable),
      EditorView.contentAttributes.of({ "aria-label": editable ? "最终文件，可编辑完整内容" : side === "left" ? "左侧完整文件" : "右侧完整文件" }),
      ...(side && props.merge ? [gutter({ class: "git-arrow-gutter", side: side === "left" ? "after" : "before", markers: v => v.state.field(arrows) })] : []),
      EditorView.theme({
        "&": { height: "100%", fontSize: "12px" },
        ".cm-scroller": { overflow: "scroll", fontFamily: "var(--font-mono, monospace)", lineHeight: "21px", scrollbarGutter: "stable" },
        ".cm-content": { padding: "6px 0 40px" },
        ".cm-line": { padding: "0 8px" },
        ".cm-gutters": { backgroundColor: "var(--surface-2)", color: "var(--muted)", borderRight: "1px solid var(--line-strong)" },
        ".cm-activeLine": { backgroundColor: "#5588aa15" },
        ".git-line-change": { backgroundColor: "#70b8f529" },
        ".git-line-conflict": { backgroundColor: "#ef5e6750" },
        ".git-word-change": { backgroundColor: "#80c8ff38" },
        ".git-arrow-gutter .cm-gutterElement": { width: "26px", textAlign: "center" },
      }),
      EditorView.updateListener.of(update => {
        if (editable && update.docChanged) {
          emit("change", update.state.doc.toString());
          schedulePaint();
        }
        if (update.geometryChanged) scheduleMeasure();
      }),
    ],
  }) });
}

function schedulePaint() {
  clearTimeout(timer);
  timer = setTimeout(paint, 100);
}
function scheduleMeasure() {
  cancelAnimationFrame(frame);
  frame = requestAnimationFrame(measure);
}
function measure() {
  if (disposed) return;
  scrollHeight.value = Math.max(1, ...views().map(v => v.scrollDOM.scrollHeight - v.scrollDOM.clientHeight)) + (vertical.value?.clientHeight || 1);
  scrollWidth.value = Math.max(1, ...views().map(v => v.scrollDOM.scrollWidth - v.scrollDOM.clientWidth)) + (horizontal.value?.clientWidth || 1);
}
function lines(view: EditorView, ranges: TextRange[], className: string, decorations: Map<number, string>) {
  for (const range of ranges) {
    const from = Math.min(range.from, view.state.doc.length);
    const to = Math.min(range.to, view.state.doc.length);
    let number = view.state.doc.lineAt(from).number;
    const last = view.state.doc.lineAt(Math.max(from, to - 1)).number;
    while (number <= last) {
      const start = view.state.doc.line(number++).from;
      if (className === "git-line-conflict" || !decorations.has(start)) decorations.set(start, className);
    }
  }
}
function decorate(view: EditorView, changed: TextRange[], conflicts: TextRange[], markers: Range<GutterMarker>[] = [], words: TextRange[] = []) {
  const lineClasses = new Map<number, string>();
  lines(view, changed, "git-line-change", lineClasses);
  lines(view, conflicts, "git-line-conflict", lineClasses);
  const ranges = [...lineClasses].map(([position, cls]) => Decoration.line({ class: cls }).range(position));
  for (const r of words) if (r.to > r.from) ranges.push(Decoration.mark({ class: "git-word-change" }).range(r.from, r.to));
  view.dispatch({ effects: [updateHighlights.of(Decoration.set(ranges, true)), updateArrows.of(markers)] });
  // CodeMirror hides decorative gutters by default; these gutters contain
  // interactive merge buttons and must remain reachable to assistive tools.
  view.dom.querySelectorAll('.cm-gutters').forEach(el => el.removeAttribute('aria-hidden'));
}

function paint() {
  if (disposed || !leftView || !rightView) return;
  const target = props.merge ? centerView : rightView;
  if (!target) return;
  const result = target.state.doc.toString();
  const conflicts = props.merge ? parseMergeConflicts(result) : [];
  conflictCount.value = conflicts.length;
  emit("conflicts", conflicts.length);
  const targetChanges: TextRange[] = [];
  let chunksCount = 0;
  navigation = [];
  for (const [view, side] of [[leftView, "left"], ...(props.merge ? [[rightView, "right"]] : [])] as [EditorView, "left" | "right"][]) {
    const chunks = Chunk.build(view.state.doc, target.state.doc, { timeout: 1000 });
    const sourceConflicts = props.merge ? mapTextRanges(result, view.state.doc.toString(), conflicts) : [];
    const sourceChanges: TextRange[] = [];
    const wordChanges: TextRange[] = [];
    const markers: Range<GutterMarker>[] = [];
    const usedLines = new Set<number>();
    const arrow = (range: TextRange, targetRange: TextRange, text: string, isConflict: boolean) => {
      const position = view.state.doc.lineAt(Math.min(range.from, view.state.doc.length)).from;
      if (usedLines.has(position)) return;
      usedLines.add(position);
      const captured = target.state;
      markers.push(new ApplyMarker(side, isConflict, () => {
        // Reject stale controls if the result changed before decorations updated.
        if (target.state.doc !== captured.doc) { schedulePaint(); return; }
        target.dispatch({ changes: { from: targetRange.from, to: targetRange.to, insert: text }, selection: { anchor: targetRange.from }, annotations: isolateHistory.of("full"), effects: EditorView.scrollIntoView(targetRange.from, { y: "center" }) });
        target.focus();
        paint();
      }).range(position));
    };
    conflicts.forEach((c, index) => arrow(sourceConflicts[index], c, c[side], true));
    for (const chunk of chunks) {
      const a = { from: chunk.fromA, to: Math.min(chunk.toA, view.state.doc.length) };
      const b = { from: chunk.fromB, to: Math.min(chunk.toB, target.state.doc.length) };
      sourceChanges.push(a); targetChanges.push(b);
      if (conflicts.some(c => overlaps(b, c))) continue;
      chunksCount++;
      navigation.push(b);
      for (const c of chunk.changes) {
        wordChanges.push({ from: chunk.fromA + c.fromA, to: chunk.fromA + c.toA });
      }
      if (props.merge) {
        // Chunk bounds include the line ending, except at end of file.
        const text = view.state.doc.sliceString(a.from, a.to);
        arrow(a, b, text, false);
      }
    }
    decorate(view, sourceChanges, sourceConflicts, markers, wordChanges);
  }
  decorate(target, targetChanges, conflicts);
  align(target, targetChanges);
  navigation = [...conflicts, ...navigation].sort((a, b) => a.from - b.from).filter((r, i, all) => !i || r.from !== all[i - 1].from);
  changeCount.value = chunksCount;
  activeIndex.value = Math.min(activeIndex.value, Math.max(0, navigation.length - 1));
  scheduleMeasure();
}

// Add display space, never document text, so unchanged lines line up across
// all panes even when a conflict or insertion has different line counts.
function align(target: EditorView, changed: TextRange[]) {
  const groups: TextRange[] = [];
  for (const range of [...changed].sort((a, b) => a.from - b.from)) {
    const last = groups.at(-1);
    if (last && range.from <= last.to) last.to = Math.max(last.to, range.to);
    else groups.push({ ...range });
  }
  const targetText = target.state.doc.toString();
  const perView = views().map(view => ({ view, ranges: view === target ? groups : mapTextRanges(targetText, view.state.doc.toString(), groups), decorations: [] as Range<Decoration>[] }));
  const count = (text: string) => text ? text.split("\n").length - (text.endsWith("\n") ? 1 : 0) : 0;
  for (let i = 0; i < groups.length; i++) {
    const heights = perView.map(({ view, ranges }) => count(view.state.doc.sliceString(ranges[i].from, ranges[i].to)));
    const max = Math.max(...heights);
    perView.forEach(({ ranges, decorations }, index) => {
      const extra = max - heights[index];
      if (extra) decorations.push(Decoration.widget({ widget: new Spacer(extra * 21), block: true, side: -1 }).range(ranges[i].to));
    });
  }
  for (const { view, decorations } of perView) view.dispatch({ effects: updateSpacers.of(Decoration.set(decorations, true)) });
}

function jump(direction: number) {
  if (!navigation.length) return;
  activeIndex.value = (activeIndex.value + direction + navigation.length) % navigation.length;
  const range = navigation[activeIndex.value];
  const target = centerView || rightView!;
  target.dispatch({ selection: { anchor: range.from }, effects: EditorView.scrollIntoView(range.from, { y: "center" }) });
  for (const v of views()) {
    if (v === target) continue;
    const [mapped] = mapTextRanges(target.state.doc.toString(), v.state.doc.toString(), [range]);
    v.dispatch({ effects: EditorView.scrollIntoView(mapped.from, { y: "center" }) });
  }
}

const selectedConflict = computed(() => conflictCount.value > 0);
function choose(choice: "left" | "right" | "both") {
  if (!centerView) return;
  const text = centerView.state.doc.toString();
  const conflicts = parseMergeConflicts(text);
  const cursor = centerView.state.selection.main.head;
  const c = conflicts.find(c => cursor >= c.from && cursor <= c.to) || conflicts.find(c => c.from >= cursor) || conflicts[0];
  if (!c) return;
  const insert = choice === "both" ? joinConflictSides(c.left, c.right) : c[choice];
  centerView.dispatch({ changes: { from: c.from, to: c.to, insert }, selection: { anchor: c.from }, annotations: isolateHistory.of("full"), effects: EditorView.scrollIntoView(c.from, { y: "center" }) });
  centerView.focus(); paint();
}

function synchronize(source: HTMLElement, axis: "x" | "y") {
  if (syncing) return;
  syncing = true;
  const x = axis === "x";
  const extent = x ? source.scrollWidth - source.clientWidth : source.scrollHeight - source.clientHeight;
  const ratio = extent > 0 ? (x ? source.scrollLeft : source.scrollTop) / extent : 0;
  if (x) xRatio.value = ratio; else yRatio.value = ratio;
  for (const target of views().map(v => v.scrollDOM)) {
    if (!target || target === source) continue;
    const max = x ? target.scrollWidth - target.clientWidth : target.scrollHeight - target.clientHeight;
    if (x) target.scrollLeft = ratio * max;
    else target.scrollTop = ratio * max;
  }
  requestAnimationFrame(() => { syncing = false; });
}
function scrollGlobal(axis: "x" | "y", value: number) {
  const ratio = Math.max(0, Math.min(1, value));
  if (axis === "x") xRatio.value = ratio; else yRatio.value = ratio;
  syncing = true;
  for (const v of views()) {
    const el = v.scrollDOM;
    if (axis === "x") el.scrollLeft = ratio * (el.scrollWidth - el.clientWidth);
    else el.scrollTop = ratio * (el.scrollHeight - el.clientHeight);
  }
  requestAnimationFrame(() => { syncing = false; });
}
function dragScrollbar(event: PointerEvent, axis: "x" | "y") {
  event.preventDefault();
  const track = event.currentTarget as HTMLElement;
  track.focus();
  const rect = track.getBoundingClientRect();
  const length = axis === "x" ? rect.width : rect.height;
  const size = axis === "x" ? horizontalThumb.value : verticalThumb.value;
  const travel = length * (1 - size / 100);
  if (travel <= 0) return;
  const coordinate = (e: PointerEvent) => axis === "x" ? e.clientX - rect.left : e.clientY - rect.top;
  const origin = coordinate(event);
  const current = axis === "x" ? xRatio.value : yRatio.value;
  const onThumb = (event.target as HTMLElement).classList.contains("git-global-thumb");
  const offset = onThumb ? origin - current * travel : length * size / 200;
  const move = (e: PointerEvent) => scrollGlobal(axis, (coordinate(e) - offset) / travel);
  const end = () => { track.removeEventListener("pointermove", move); track.removeEventListener("pointerup", end); track.removeEventListener("pointercancel", end); };
  track.setPointerCapture(event.pointerId);
  track.addEventListener("pointermove", move);
  track.addEventListener("pointerup", end);
  track.addEventListener("pointercancel", end);
  move(event);
}
function scrollbarKey(event: KeyboardEvent, axis: "x" | "y") {
  const value = axis === "x" ? xRatio.value : yRatio.value;
  const step = event.key === "PageDown" || event.key === "PageUp" ? 0.15 : 0.025;
  if (["Home", "End", "ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight", "PageDown", "PageUp"].includes(event.key)) {
    event.preventDefault();
    scrollGlobal(axis, event.key === "Home" ? 0 : event.key === "End" ? 1 : value + (["ArrowDown", "ArrowRight", "PageDown"].includes(event.key) ? step : -step));
  }
}

async function mountEditors() {
  await nextTick();
  if (!leftElement.value || !rightElement.value) return;
  views().forEach(v => v.destroy());
  leftView = createEditor(leftElement.value, props.left, false, "left");
  rightView = createEditor(rightElement.value, props.right, false, "right");
  centerView = props.merge && resultElement.value ? createEditor(resultElement.value, props.result || "", true) : undefined;
  for (const view of views()) {
    let lastTop = 0, lastLeft = 0;
    view.scrollDOM.addEventListener("scroll", () => {
      if (view.scrollDOM.scrollTop !== lastTop) synchronize(view.scrollDOM, "y");
      if (view.scrollDOM.scrollLeft !== lastLeft) synchronize(view.scrollDOM, "x");
      lastTop = view.scrollDOM.scrollTop; lastLeft = view.scrollDOM.scrollLeft;
    });
  }
  observer?.disconnect();
  observer = new ResizeObserver(scheduleMeasure);
  for (const view of views()) observer.observe(view.dom);
  paint();
}
watch(() => [props.left, props.right, props.result, props.merge], mountEditors);
onMounted(mountEditors);
onBeforeUnmount(() => { disposed = true; clearTimeout(timer); cancelAnimationFrame(frame); observer?.disconnect(); views().forEach(v => v.destroy()); });
</script>

<template>
  <div class="git-compare-editor" :class="{ 'is-merge': merge }">
    <div class="git-compare-controls">
      <span class="git-compare-legend"><i class="change" />变更 <i class="conflict" />冲突</span>
      <span>{{ changeCount }} 处差异<span v-if="merge"> · {{ conflictCount }} 处待处理冲突</span></span>
      <el-button text size="small" :icon="ChevronUp" @click="jump(-1)">上一处</el-button>
      <el-button text size="small" :icon="ChevronDown" @click="jump(1)">下一处</el-button>
      <template v-if="merge">
        <el-button size="small" :icon="ArrowRight" :disabled="!selectedConflict" @click="choose('left')">使用左侧</el-button>
        <el-button size="small" :icon="GitMerge" :disabled="!selectedConflict" @click="choose('both')">合并两侧（左后右）</el-button>
        <el-button size="small" :icon="ArrowLeft" :disabled="!selectedConflict" @click="choose('right')">使用右侧</el-button>
      </template>
    </div>
    <div class="git-compare-headings">
      <span>{{ merge ? '当前分支 · 只读' : '原始版本 · 只读' }}</span>
      <strong v-if="merge">最终文件 · 可编辑完整内容</strong>
      <span>{{ merge ? '传入分支 · 只读' : '当前版本 · 只读' }}</span>
    </div>
    <div class="git-compare-body">
      <div ref="leftElement" class="git-code-pane left" />
      <div v-if="merge" ref="resultElement" class="git-code-pane result" />
      <div ref="rightElement" class="git-code-pane right" />
      <div ref="vertical" class="git-global-vertical" role="scrollbar" tabindex="0" aria-label="全局纵向滚动条" aria-orientation="vertical" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="Math.round(yRatio * 100)" @pointerdown="dragScrollbar($event, 'y')" @keydown="scrollbarKey($event, 'y')">
        <div class="git-global-thumb" :style="{ height: `${verticalThumb}%`, top: `${yRatio * (100 - verticalThumb)}%` }" />
      </div>
    </div>
    <div ref="horizontal" class="git-global-horizontal" role="scrollbar" tabindex="0" aria-label="全局横向滚动条" aria-orientation="horizontal" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="Math.round(xRatio * 100)" @pointerdown="dragScrollbar($event, 'x')" @keydown="scrollbarKey($event, 'x')">
      <div class="git-global-thumb" :style="{ width: `${horizontalThumb}%`, left: `${xRatio * (100 - horizontalThumb)}%` }" />
    </div>
  </div>
</template>

<style scoped>
.git-compare-editor { --columns: 2; border: 1px solid var(--line-strong); background: var(--field-bg); }
.git-compare-editor.is-merge { --columns: 3; }
.git-compare-controls { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; min-height: 42px; padding: 7px 10px; border-bottom: 1px solid var(--line-strong); font-size: 12px; color: var(--text-soft); }
.git-compare-controls :deep(.el-button + .el-button) { margin-left: 0; }
.git-compare-legend { display: inline-flex; align-items: center; gap: 7px; margin-right: auto; }
.git-compare-legend i { width: 10px; height: 10px; border-radius: 2px; }
.git-compare-legend .change { background: #70b8f580; }
.git-compare-legend .conflict { background: #ef5e67b0; }
.git-compare-headings { display: grid; grid-template-columns: repeat(var(--columns), minmax(0, 1fr)); padding-right: 16px; border-bottom: 1px solid var(--line-strong); background: var(--surface-2); color: var(--text-soft); font-size: 12px; }
.git-compare-headings > * { padding: 10px 12px; border-right: 1px solid var(--line-strong); }
.git-compare-headings strong { color: var(--accent-strong); }
.git-compare-body { display: grid; grid-template-columns: repeat(var(--columns), minmax(0, 1fr)) 16px; height: min(65vh, 760px); min-height: 280px; overflow: hidden; }
.git-code-pane { min-width: 0; min-height: 0; overflow: hidden; border-right: 1px solid var(--line-strong); }
.git-code-pane.result { box-shadow: inset 0 0 0 1px #79b8ec55; }
.git-code-pane :deep(.cm-editor) { height: 100%; }
.git-code-pane :deep(.cm-scroller) { scrollbar-width: thin; scrollbar-color: #66798e #151b21; }
.git-code-pane :deep(.cm-scroller::-webkit-scrollbar) { width: 0; height: 12px; }
.git-code-pane :deep(.cm-scroller::-webkit-scrollbar-thumb) { background: #66798e; border: 2px solid #151b21; border-radius: 6px; }
.git-code-pane :deep(.git-apply-arrow) { display: inline-grid; place-items: center; width: 24px; height: 20px; padding: 0; border: 1px solid #76b8ed70; border-radius: 3px; background: #284863; color: #b7ddff; cursor: pointer; }
.git-code-pane :deep(.git-apply-arrow.is-conflict) { background: #77343d; border-color: #f1858c99; color: #ffd1d4; }
.git-code-pane :deep(.git-apply-arrow:hover) { filter: brightness(1.4); }
.git-global-vertical, .git-global-horizontal { position: relative; overflow: hidden; background: #18202b; touch-action: none; cursor: pointer; }
.git-global-horizontal { height: 16px; }
.git-global-thumb { position: absolute; box-sizing: border-box; border: 3px solid #18202b; border-radius: 8px; background: #7a8c9f; }
.git-global-vertical .git-global-thumb { width: 16px; }
.git-global-horizontal .git-global-thumb { height: 16px; }
.git-global-vertical:hover .git-global-thumb, .git-global-horizontal:hover .git-global-thumb { background: #a9c3dd; }
.git-global-vertical:focus-visible, .git-global-horizontal:focus-visible { outline: 1px solid #a9c3dd; outline-offset: -1px; }
</style>
