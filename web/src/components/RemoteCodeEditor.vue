<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { ArrowDown, ArrowUp, ChevronRight, Copy, ClipboardPaste, Scissors, Undo2, Redo2, Search, Save, Sparkles, X } from "@lucide/vue";
import { contextMenuPosition, contextMenuKey } from "../contextMenu";
import type { CodeLanguageId } from "../codeLanguageDetection";
import { detectIndent, type EditorIndent } from "../editorPreferences";
import { indentUnit } from "@codemirror/language";
import { basicSetup } from "codemirror";
import { Compartment, EditorState, Prec, Text } from "@codemirror/state";
import { Decoration, EditorView, keymap, ViewPlugin } from "@codemirror/view";
import { indentWithTab, undo, redo, undoDepth, redoDepth, selectAll } from "@codemirror/commands";
import { search as codeSearch, SearchQuery, getSearchQuery, setSearchQuery, findNext, findPrevious, replaceNext, replaceAll } from "@codemirror/search";
import { codeLanguage, workbenchEditorTheme } from "../codeLanguages";

const props = defineProps<{ content: string; path: string; visible: boolean; initialLine?: number; initialColumn?: number; initialScroll?: number; languageID?: CodeLanguageId; indent?: EditorIndent }>();
const emit = defineEmits<{ change: [content: string]; position: [line: number, column: number]; scroll: [top: number]; save: []; analyze: [] }>();
const mount = ref<HTMLElement>();
const searchVisible = ref(false), replaceVisible = ref(false), query = ref(''), replacement = ref(''), caseSensitive = ref(false), regexp = ref(false), wholeWord = ref(false);
const queryInput = ref<HTMLInputElement>(), matchCount = ref(0), matchNumber = ref(0), invalidQuery = ref(false);
const menu = ref<{ x: number; y: number }>(), menuElement = ref<HTMLElement>();
const gotoVisible = ref(false), gotoValue = ref(""), gotoError = ref("");
const gotoInput = ref<HTMLInputElement>();
function promptGoTo() { closeMenu(); closeSearch(); gotoVisible.value = true; gotoValue.value = ""; gotoError.value = ""; nextTick(() => gotoInput.value?.focus()); }
function jumpToInput() {
  const match = /^(\d+)(?::(\d+))?$/.exec(gotoValue.value.trim());
  if (!match || !editor || Number(match[1]) < 1 || Number(match[1]) > editor.state.doc.lines || (match[2] && Number(match[2]) < 1)) { gotoError.value = `请输入 1–${editor?.state.doc.lines || 1} 的行号，可加 :列号`; return; }
  gotoVisible.value = false; goTo(Number(match[1]), Number(match[2]) || 1);
}
const revision = ref(0);
const hasSelection = computed(() => { void revision.value; return Boolean(editor && !editor.state.selection.main.empty); });
const canUndo = computed(() => { void revision.value; return editor ? undoDepth(editor.state) > 0 : false; });
const canRedo = computed(() => { void revision.value; return editor ? redoDepth(editor.state) > 0 : false; });
let editor: EditorView | undefined;
let observer: ResizeObserver | undefined;
const language = new Compartment(), attributes = new Compartment(), indentation = new Compartment(), lineEnding = new Compartment();
function indentExtension() { const value = props.indent || detectIndent(props.content); return [indentUnit.of(value === "tab" ? "\t" : " ".repeat(Number(value))), EditorState.tabSize.of(value === "tab" ? 4 : Number(value))]; }
const fileSearchHighlights = ViewPlugin.fromClass(class {
  decorations;
  constructor(view: EditorView) { this.decorations = this.highlight(view); }
  update(update: { view: EditorView }) { this.decorations = this.highlight(update.view); }
  highlight(view: EditorView) {
    const query = getSearchQuery(view.state);
    if (!searchVisible.value || !query.valid) return Decoration.none;
    const ranges = [];
    for (const { from, to } of view.visibleRanges) {
      const cursor = query.getCursor(view.state.doc, from, to);
      for (let match = cursor.next(); !match.done; match = cursor.next()) {
        if (match.value.from === match.value.to) continue;
        const selected = view.state.selection.ranges.some(range => range.from === match.value.from && range.to === match.value.to);
        ranges.push(Decoration.mark({ class: selected ? 'cm-searchMatch cm-searchMatch-selected' : 'cm-searchMatch' }).range(match.value.from, match.value.to));
      }
    }
    return Decoration.set(ranges, true);
  }
}, { decorations: value => value.decorations });

function focus() { editor?.focus(); }
function search(replace = false) {
  searchVisible.value = true; replaceVisible.value = replace;
  const selected = editor?.state.sliceDoc(editor.state.selection.main.from, editor.state.selection.main.to);
  if (selected && !selected.includes('\n')) query.value = selected;
  nextTick(() => { queryInput.value?.focus(); queryInput.value?.select(); });
}
function closeSearch() { searchVisible.value = false; editor?.dispatch({ effects: setSearchQuery.of(new SearchQuery({ search: '' })) }); focus(); }
function searchState() { return new SearchQuery({ search: query.value, replace: replacement.value, caseSensitive: caseSensitive.value, regexp: regexp.value, wholeWord: wholeWord.value }); }
function countMatches() {
  matchCount.value = 0; matchNumber.value = 0;
  if (!editor || !searchVisible.value) return;
  const value = searchState(); invalidQuery.value = !value.valid && Boolean(query.value);
  if (!value.valid) return;
  const cursor = value.getCursor(editor.state.doc), selected = editor.state.selection.main;
  for (let match = cursor.next(); !match.done && matchCount.value < 10000; match = cursor.next()) { matchCount.value++; if (match.value.from === selected.from && match.value.to === selected.to) matchNumber.value = matchCount.value; }
}
watch([query, replacement, caseSensitive, regexp, wholeWord, searchVisible], () => {
  if (editor) editor.dispatch({ effects: setSearchQuery.of(searchVisible.value ? searchState() : new SearchQuery({ search: '' })) });
  countMatches();
});
watch([searchVisible, replaceVisible], () => nextTick(() => {
  if (!editor) return;
  editor.requestMeasure();
  if (searchVisible.value) editor.dispatch({ effects: EditorView.scrollIntoView(editor.state.selection.main.head, { y: 'center' }) });
}));
function find(previous = false) { if (editor) { (previous ? findPrevious : findNext)(editor); countMatches(); } }
function replace(all = false) { if (editor && !invalidQuery.value) { (all ? replaceAll : replaceNext)(editor); countMatches(); } }
function closeMenu(restore = false) { menu.value = undefined; if (restore) focus(); }
async function showMenu(event: MouseEvent) {
  event.preventDefault(); event.stopPropagation();
  if (!editor) return;
  const pos = editor.posAtCoords({ x: event.clientX, y: event.clientY });
  const selection = editor.state.selection.main;
  if (pos !== null && (selection.empty || pos < selection.from || pos > selection.to)) editor.dispatch({ selection: { anchor: pos } });
  menu.value = { x: event.clientX, y: event.clientY };
  await nextTick();
  if (!menu.value || !menuElement.value) return;
  menu.value = contextMenuPosition(menu.value, menuElement.value);
  menuElement.value.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus({ preventScroll: true });
}
function outsideMenu(event: PointerEvent) { if (!menuElement.value?.contains(event.target as Node)) closeMenu(); }
function menuKey(event: KeyboardEvent) { contextMenuKey(event, menuElement.value, () => closeMenu(true)); }
async function clipboard(action: 'copy' | 'cut' | 'paste') {
  closeMenu(); if (!editor) return;
  const current = editor, selection = current.state.selection.main, original = current.state.doc;
  const text = current.state.sliceDoc(selection.from, selection.to);
  try {
    if (action === 'paste') {
      if (!navigator.clipboard?.readText) throw new Error('浏览器无法读取剪贴板，请使用 Ctrl+V 粘贴');
      const value = await navigator.clipboard.readText();
      if (current !== editor || current.state.doc !== original || !current.state.selection.main.eq(selection) || !props.visible) throw new Error('编辑内容或光标已变化，请重新粘贴');
      current.dispatch({ ...current.state.replaceSelection(value), userEvent: 'input.paste', scrollIntoView: true });
    } else {
      if (!text) return;
      if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(text);
      else {
        const input = document.createElement('textarea'); input.value = text; input.style.cssText = 'position:fixed;opacity:0'; document.body.append(input); input.select();
        try { if (!document.execCommand('copy')) throw new Error('复制失败，请使用 Ctrl+C'); } finally { input.remove(); }
      }
      if (action === 'cut' && current === editor && current.state.doc === original && current.state.selection.main.eq(selection) && props.visible) current.dispatch({ changes: { from: selection.from, to: selection.to, insert: '' }, selection: { anchor: selection.from }, userEvent: 'delete.cut', scrollIntoView: true });
    }
  } catch (cause) { ElMessage.warning(cause instanceof Error ? cause.message : '剪贴板访问失败，请使用键盘快捷键'); }
  if (current === editor && props.visible) focus();
}
function menuAction(action: 'undo' | 'redo' | 'select' | 'search' | 'replace' | 'save' | 'analyze' | 'goto') {
  closeMenu(true); if (!editor) return;
  if (action === 'undo') undo(editor); else if (action === 'redo') redo(editor); else if (action === 'select') selectAll(editor);
  else if (action === 'search' || action === 'replace') search(action === 'replace'); else if (action === 'goto') promptGoTo(); else if (action === 'save') emit('save'); else emit('analyze');
}
function goTo(number: number, column = 1) {
  if (!editor) return;
  const line = editor.state.doc.line(Math.max(1, Math.min(editor.state.doc.lines, number)));
  const position = Math.min(line.to, line.from + Math.max(0, column - 1));
  editor.dispatch({ selection: { anchor: position }, effects: EditorView.scrollIntoView(position, { y: "center" }) }); editor.focus();
}
function restorePosition(line: number, column: number, scroll: number) {
  goTo(line, column); const current = editor;
  void nextTick(() => requestAnimationFrame(() => { if (current && current === editor) current.scrollDOM.scrollTop = scroll; }));
}
function onScroll() { if (editor) emit("scroll", editor.scrollDOM.scrollTop); }
defineExpose({ focus, search, goTo, restorePosition, promptGoTo });
onMounted(() => {
  editor = new EditorView({
    parent: mount.value!,
    state: EditorState.create({
      doc: props.content,
      extensions: [basicSetup, codeSearch(), fileSearchHighlights, keymap.of([indentWithTab]), Prec.highest(keymap.of([
        { key: 'Mod-f', run: () => { search(); return true; } }, { key: 'Mod-h', run: () => { search(true); return true; } },
        { key: 'F3', run: () => { if (searchVisible.value) find(); else search(); return true; }, shift: () => { if (searchVisible.value) find(true); else search(); return true; } },
        { key: 'Mod-g', run: () => { promptGoTo(); return true; }, shift: () => { if (searchVisible.value) find(true); else search(); return true; } },
        { key: 'Shift-F10', run: view => { const coords = view.coordsAtPos(view.state.selection.main.head); if (coords) void showMenu(new MouseEvent('contextmenu', { clientX: coords.left, clientY: coords.bottom })); return true; } },
        { key: 'Escape', run: () => { if (!searchVisible.value) return false; closeSearch(); return true; } },
      ])), language.of(codeLanguage(props.path, props.languageID).extension), indentation.of(indentExtension()),
        workbenchEditorTheme,
        lineEnding.of(EditorState.lineSeparator.of(props.content.includes("\r\n") ? "\r\n" : "\n")),
        attributes.of(EditorView.contentAttributes.of({ "aria-label": `编辑 ${props.path}` })),
        EditorView.updateListener.of(update => {
          if (update.docChanged || update.selectionSet) { revision.value++; countMatches(); }
          if (update.docChanged) emit("change", update.state.sliceDoc());
          if (update.selectionSet || update.docChanged) {
            const head = update.state.selection.main.head;
            const line = update.state.doc.lineAt(head);
            emit("position", line.number, head - line.from + 1);
          }
        }),
      ],
    }),
  });
  editor.scrollDOM.addEventListener("scroll", onScroll, { passive: true });
  document.addEventListener('pointerdown', outsideMenu, true);
  window.addEventListener('resize', closeContextMenu);
  if (props.initialLine || props.initialScroll) restorePosition(props.initialLine || 1, props.initialColumn || 1, props.initialScroll || 0);
  observer = new ResizeObserver(() => editor?.requestMeasure());
  observer.observe(mount.value!);
  if (props.visible) focus();
});
watch(() => props.content, content => {
  if (editor && editor.state.sliceDoc() !== content) {
    const view = editor, scrollTop = view.scrollDOM.scrollTop, scrollLeft = view.scrollDOM.scrollLeft;
    const before = view.state.doc, selection = view.state.selection.main, after = Text.of(content.split(/\r\n|\r|\n/));
    const mapPosition = (position: number) => { const line = before.lineAt(position), target = after.line(Math.min(after.lines, line.number)); return Math.min(target.to, target.from + position - line.from); };
    view.dispatch({ effects: lineEnding.reconfigure(EditorState.lineSeparator.of(content.includes("\r\n") ? "\r\n" : "\n")), changes: { from: 0, to: before.length, insert: after }, selection: { anchor: mapPosition(selection.anchor), head: mapPosition(selection.head) } });
    void nextTick(() => { if (view === editor) { view.scrollDOM.scrollTop = scrollTop; view.scrollDOM.scrollLeft = scrollLeft; } });
  }
});
watch([() => props.path, () => props.languageID], () => {
  editor?.dispatch({ effects: [language.reconfigure(codeLanguage(props.path, props.languageID).extension), attributes.reconfigure(EditorView.contentAttributes.of({ "aria-label": `编辑 ${props.path}` }))] });
});
watch(() => props.indent, () => editor?.dispatch({ effects: indentation.reconfigure(indentExtension()) }));
watch(() => props.visible, visible => {
  if (!visible) closeMenu();
  if (visible) { editor?.requestMeasure(); focus(); }
});
function closeContextMenu() { closeMenu(); }
onBeforeUnmount(() => { document.removeEventListener('pointerdown', outsideMenu, true); window.removeEventListener('resize', closeContextMenu); observer?.disconnect(); editor?.scrollDOM.removeEventListener("scroll", onScroll); editor?.destroy(); });
</script>

<template>
  <div class="remote-code-editor">
    <form v-if="gotoVisible" class="code-goto-bar" aria-label="跳转到行" @submit.prevent="jumpToInput" @keydown.esc.stop.prevent="gotoVisible = false; focus()"><input ref="gotoInput" v-model="gotoValue" aria-label="行号与列号" placeholder="行号:列号，例如 25:8" /><button type="submit">跳转</button><button type="button" aria-label="关闭跳转" @click="gotoVisible = false; focus()"><X :size="14" /></button><small v-if="gotoError">{{ gotoError }}</small></form>
    <div v-if="searchVisible" class="code-file-search" role="search" aria-label="当前文件查找与替换" @keydown.esc.stop.prevent="closeSearch">
      <div class="code-find-row"><button :aria-expanded="replaceVisible" aria-label="展开替换" title="替换 (Ctrl+H)" @click="replaceVisible = !replaceVisible"><ChevronRight :size="14" :class="{ expanded: replaceVisible }" /></button><div class="code-find-input"><input ref="queryInput" v-model="query" aria-label="查找当前文件" placeholder="查找" @keydown.enter.prevent="find($event.shiftKey)" /><button :aria-pressed="caseSensitive" aria-label="查找区分大小写" title="区分大小写" @click="caseSensitive = !caseSensitive">Aa</button><button :aria-pressed="wholeWord" aria-label="全词匹配" title="全词匹配" @click="wholeWord = !wholeWord">ab</button><button :aria-pressed="regexp" aria-label="查找使用正则" title="正则表达式" @click="regexp = !regexp">.*</button></div><span class="code-find-count" aria-live="polite">{{ invalidQuery ? '表达式无效' : query ? `${matchNumber}/${matchCount}${matchCount === 10000 ? '+' : ''}` : '无匹配' }}</span><button aria-label="上一个匹配" title="上一个 (Shift+Enter)" :disabled="!matchCount" @click="find(true)"><ArrowUp :size="14" /></button><button aria-label="下一个匹配" title="下一个 (Enter)" :disabled="!matchCount" @click="find()"><ArrowDown :size="14" /></button><button aria-label="关闭文件查找" @click="closeSearch"><X :size="14" /></button></div>
      <div v-if="replaceVisible" class="code-replace-row"><input v-model="replacement" aria-label="替换为" placeholder="替换为" @keydown.enter.prevent="replace()" /><button :disabled="!matchCount || invalidQuery" @click="replace()">替换</button><button :disabled="!matchCount || invalidQuery" @click="replace(true)">全部替换</button></div>
    </div>
    <div ref="mount" class="code-editor-mount" @contextmenu="showMenu" />
    <Teleport to="body"><div v-if="menu" ref="menuElement" class="code-editor-context-menu" :style="{ left: `${menu.x}px`, top: `${menu.y}px` }" role="menu" aria-label="代码编辑菜单" @contextmenu.prevent.stop @keydown="menuKey"><button role="menuitem" :disabled="!canUndo" @click="menuAction('undo')"><Undo2 :size="14" />撤销<kbd>Ctrl Z</kbd></button><button role="menuitem" :disabled="!canRedo" @click="menuAction('redo')"><Redo2 :size="14" />重做<kbd>Ctrl Shift Z</kbd></button><hr /><button role="menuitem" :disabled="!hasSelection" @click="clipboard('cut')"><Scissors :size="14" />剪切<kbd>Ctrl X</kbd></button><button role="menuitem" :disabled="!hasSelection" @click="clipboard('copy')"><Copy :size="14" />复制<kbd>Ctrl C</kbd></button><button role="menuitem" @click="clipboard('paste')"><ClipboardPaste :size="14" />粘贴<kbd>Ctrl V</kbd></button><button role="menuitem" @click="menuAction('select')">全选<kbd>Ctrl A</kbd></button><hr /><button role="menuitem" @click="menuAction('search')"><Search :size="14" />查找<kbd>Ctrl F</kbd></button><button role="menuitem" @click="menuAction('replace')">替换<kbd>Ctrl H</kbd></button><button role="menuitem" @click="menuAction('goto')">跳转到行<kbd>Ctrl G</kbd></button><button role="menuitem" @click="menuAction('save')"><Save :size="14" />保存<kbd>Ctrl S</kbd></button><hr /><button role="menuitem" @click="menuAction('analyze')"><Sparkles :size="14" />AI 分析当前文件</button></div></Teleport>
  </div>
</template>

<style scoped>
.remote-code-editor { display: flex; flex-direction: column; height: 100%; min-height: 0; overflow: hidden; }
.code-goto-bar { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; padding: 7px 10px; border-bottom: 1px solid var(--line); background: var(--surface); font-size: 12px; }
.code-goto-bar input { flex: 1; min-width: 120px; border: 1px solid var(--line); border-radius: 3px; padding: 5px 7px; color: var(--text); background: var(--bg); outline-color: var(--accent); }
.code-goto-bar small { flex-basis: 100%; color: var(--danger); }
.code-editor-mount { flex: 1; min-height: 0; overflow: hidden; }
.remote-code-editor :deep(.cm-editor) { outline: none; }
</style>
