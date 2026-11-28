<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import OperationFeedback from "./OperationFeedback.vue";
import { useDialogReturnFocus } from "../composables/useDialogReturnFocus";
import { basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { defaultKeymap, historyKeymap } from "@codemirror/commands";
import { searchKeymap } from "@codemirror/search";
import { codeLanguage, workbenchEditorTheme } from "../codeLanguages";
import { ElMessage, ElMessageBox } from "element-plus";
import { ApiError, api } from "../api";
import type { Host } from "../types";
import DOMPurify from "dompurify";
import { marked } from "marked";
import { Eye, FilePenLine } from "@lucide/vue";

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  sessionId?: string;
  file?: { name: string; path: string };
  initialMode?: "edit" | "preview";
}>();
const emit = defineEmits<{
  "update:modelValue": [boolean];
  saved: [];
}>();

const { restoreFocus } = useDialogReturnFocus(() => props.modelValue);
const fileError = ref(""), saveError = ref("");
const editorElement = ref<HTMLElement>();
const loading = ref(false);
const saving = ref(false);
const ready = ref(false);
const dirty = ref(false);
const version = ref("");
const byteSize = ref(0);
const markdownContent = ref("");
const mode = ref<"edit" | "preview">("edit");
let editor: EditorView | undefined;
let originalContent = "";

const isMarkdown = computed(() => {
  const name = props.file?.name.toLowerCase() || "";
  return name.endsWith(".md") || name.endsWith(".markdown");
});

function renderMarkdown(value: string) {
  const html = marked.parse(value, { gfm: true, breaks: true, async: false });
  return DOMPurify.sanitize(String(html));
}

function destroyEditor() {
  editor?.destroy();
  editor = undefined;
  ready.value = false;
  dirty.value = false;
  version.value = "";
  markdownContent.value = "";
  originalContent = "";
}

async function loadFile() {
  if (!props.host || !props.file || !editorElement.value) return;
  destroyEditor();
  loading.value = true; fileError.value = ""; saveError.value = "";
  try {
    const session = props.sessionId
      ? `&session=${encodeURIComponent(props.sessionId)}`
      : "";
    const result = await api<{
      content: string;
      version: string;
      size: number;
    }>(
      `/api/sftp/${props.host.id}/text?path=${encodeURIComponent(props.file.path)}${session}`,
    );
    originalContent = result.content;
    markdownContent.value = result.content;
    mode.value = isMarkdown.value ? props.initialMode || "edit" : "edit";
    version.value = result.version;
    byteSize.value = result.size;
    editor = new EditorView({
      parent: editorElement.value,
      state: EditorState.create({
        doc: result.content,
        extensions: [
          basicSetup,
          EditorState.lineSeparator.of(result.content.includes("\r\n") ? "\r\n" : "\n"),
          keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap]),
          codeLanguage(props.file.path).extension,
          workbenchEditorTheme,
          EditorView.lineWrapping,
          EditorView.contentAttributes.of({ "aria-label": `编辑 ${props.file.path}` }),
          EditorView.updateListener.of((update) => {
            if (update.docChanged) {
              markdownContent.value = update.state.sliceDoc();
              dirty.value = markdownContent.value !== originalContent;
              byteSize.value = new TextEncoder().encode(markdownContent.value).length;
            }
          }),
        ],
      }),
    });
    ready.value = true;
    editor.focus();
  } catch (error) {
    fileError.value = error instanceof Error ? error.message : "文本文件读取失败";
  } finally {
    loading.value = false;
  }
}

async function saveFile() {
  if (!props.host || !props.file || !editor || !ready.value || !dirty.value || saving.value) return;
  const content = editor.state.sliceDoc();
  let allowEmpty = false;
  if (!content.length && originalContent.length) {
    try {
      await ElMessageBox.confirm(
        "当前内容为空，保存后远程文件将变成 0 字节。确认清空该文件？",
        "确认清空文件",
        { type: "warning", confirmButtonText: "确认清空" },
      );
      allowEmpty = true;
    } catch {
      return;
    }
  }
  saving.value = true; saveError.value = "";
  try {
    const session = props.sessionId
      ? `&session=${encodeURIComponent(props.sessionId)}`
      : "";
    const result = await api<{ version: string; bytes: number }>(
      `/api/sftp/${props.host.id}/text?path=${encodeURIComponent(props.file.path)}${session}`,
      {
        method: "PUT",
        headers: {
          "Content-Type": "text/plain; charset=utf-8",
          "If-Match": `"${version.value}"`,
          ...(allowEmpty ? { "X-Velin-Allow-Empty": "true" } : {}),
        },
        body: new Blob([content], { type: "text/plain; charset=utf-8" }),
      },
    );
    originalContent = content;
    markdownContent.value = editor.state.sliceDoc();
    version.value = result.version;
    byteSize.value = new TextEncoder().encode(markdownContent.value).length;
    dirty.value = markdownContent.value !== originalContent;
    ElMessage.success("文件已安全保存");
    emit("saved");
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      saveError.value = "远程文件已经变化，未覆盖原文件。请先复制保留当前修改，再重新读取远程版本进行合并。";
    } else {
      saveError.value = error instanceof Error ? error.message : "文件保存失败";
    }
  } finally {
    saving.value = false;
  }
}

async function confirmClose() {
  if (saving.value) { ElMessage.info("文件正在保存，请稍后关闭"); return false; }
	if (dirty.value) {
		try {
      await ElMessageBox.confirm("尚有未保存的修改，确定关闭？", "放弃修改", {
        type: "warning",
        confirmButtonText: "放弃修改", closeOnClickModal: false,
      });
		} catch {
			return false;
		}
	}
	return true;
}

async function closeEditor() {
	if (!(await confirmClose())) return;
	emit("update:modelValue", false);
}

async function beforeClose(done: () => void) {
	if (await confirmClose()) done();
}

watch(
  () => props.modelValue,
  async (open) => {
    if (open) {
      await nextTick();
      await loadFile();
    } else {
      destroyEditor();
    }
  },
);
onBeforeUnmount(destroyEditor);
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="text-editor-dialog" @close-auto-focus="restoreFocus"
    width="min(900px, calc(100vw - 24px))"
    append-to-body
    :close-on-click-modal="false"
		:before-close="beforeClose"
		@update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div class="text-editor-heading">
        <strong>{{ file?.name || "文本编辑" }}</strong>
        <small>{{ file?.path }}</small>
      </div>
    </template>
    <div v-if="isMarkdown" class="text-editor-mode">
      <el-radio-group v-model="mode" size="small">
        <el-radio-button label="edit"><FilePenLine :size="14" /> 编辑</el-radio-button>
        <el-radio-button label="preview"><Eye :size="14" /> 预览</el-radio-button>
      </el-radio-group>
    </div>
    <OperationFeedback :pending="saving ? '正在保存远程文件…' : undefined" :error="fileError || saveError" :retry-label="fileError ? '重新读取' : '重试保存'" @retry="fileError ? loadFile() : saveFile()" @dismiss="fileError = ''; saveError = ''" />
    <div v-loading="loading" class="text-editor-shell">
      <div
        ref="editorElement"
        class="text-editor-mount"
        :class="{ 'is-hidden': isMarkdown && mode === 'preview' }"
      />
      <article
        v-if="isMarkdown && mode === 'preview'"
        class="markdown-preview"
        v-html="renderMarkdown(markdownContent)"
      />
      <div v-if="!loading && !ready" class="text-editor-unavailable">无法加载文本内容</div>
    </div>
    <template #footer>
      <div class="text-editor-footer">
        <span>{{ byteSize.toLocaleString() }} B<span v-if="dirty"> · 未保存</span></span>
        <div>
          <el-button @click="closeEditor">关闭</el-button>
          <el-button type="primary" :disabled="!ready || !dirty" :loading="saving" @click="saveFile">保存</el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<style>
.el-dialog.text-editor-dialog {
  width: min(900px, calc(100vw - 28px)) !important;
  padding: 0 !important;
  overflow: hidden;
  border: 1px solid var(--line) !important;
  border-radius: 6px !important;
  margin: max(18px, calc((100dvh - 730px) / 2)) auto !important;
}
.el-dialog.text-editor-dialog > .el-dialog__header {
  display: flex;
  min-height: 43px;
  align-items: center;
  margin: 0;
  padding: 8px 43px 8px 14px !important;
  border-bottom: 1px solid var(--line);
}
.el-dialog.text-editor-dialog > .el-dialog__header .el-dialog__headerbtn { top: 0; right: 0; width: 42px; height: 42px; }
.el-dialog.text-editor-dialog > .el-dialog__body { padding: 0 !important; overflow: hidden; }
.el-dialog.text-editor-dialog > .el-dialog__footer {
  min-height: 42px;
  padding: 6px 10px !important;
  border-top: 1px solid var(--line);
  background: var(--surface);
}
.text-editor-heading { display: flex; min-width: 0; flex: 1; flex-direction: row; align-items: baseline; gap: 10px; }
.text-editor-heading strong { flex: 0 0 auto; max-width: 34%; overflow: hidden; color: var(--text); font-size: 12px; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
.text-editor-heading small { min-width: 0; max-width: none; flex: 1; overflow: hidden; color: var(--muted); font: 10px/1.4 var(--font-mono); text-overflow: ellipsis; white-space: nowrap; }
.text-editor-mode { display: flex; min-height: 35px; align-items: center; justify-content: flex-end; padding: 4px 9px; border-bottom: 1px solid var(--line); background: var(--surface-2); }
.text-editor-mode .el-radio-button__inner { display: inline-flex; min-height: 25px; align-items: center; gap: 5px; padding: 4px 9px; border-color: var(--line) !important; background: transparent; color: var(--muted); font-size: 11px; box-shadow: none !important; }
.text-editor-mode .el-radio-button__original-radio:checked + .el-radio-button__inner { background: var(--surface-3); color: var(--text); }
.text-editor-shell { position: relative; height: min(68vh, 680px); min-height: 360px; overflow: hidden; border: 0; background: var(--bg); }
.text-editor-mount, .text-editor-mount .cm-editor { height: 100%; }
.text-editor-mount .cm-editor { color: var(--text); background: var(--bg); font: 13px var(--font-mono); }
.text-editor-mount .cm-scroller { overflow: auto; line-height: 1.65; }
.text-editor-mount .cm-gutters { color: var(--muted); border-right: 1px solid var(--line); background: var(--bg); }
.text-editor-mount .cm-activeLine, .text-editor-mount .cm-activeLineGutter { background: var(--surface-2); }
.text-editor-mount .cm-selectionBackground, .text-editor-mount .cm-content ::selection { background: var(--accent-soft) !important; }
.text-editor-mount .cm-focused { outline: none; }
.text-editor-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.text-editor-footer > span { color: var(--muted); font-size: 10px; }
.text-editor-footer > div { display: flex; gap: 6px; }
.text-editor-footer .el-button { min-height: 28px; padding: 5px 12px; font-size: 11px; }
.text-editor-dialog .markdown-preview { height: 100%; box-sizing: border-box; overflow: auto; padding: 22px 26px 50px; color: var(--text-soft); background: var(--bg); font-size: 13px; line-height: 1.7; }
.text-editor-dialog .markdown-preview h1, .text-editor-dialog .markdown-preview h2, .text-editor-dialog .markdown-preview h3, .text-editor-dialog .markdown-preview h4 { color: var(--text); }
.text-editor-dialog .markdown-preview h1 { border-bottom-color: var(--line); }
.text-editor-dialog .markdown-preview blockquote { border-left-color: var(--icon-warm); color: var(--muted); }
.text-editor-dialog .markdown-preview code { background: var(--surface-2); }
.text-editor-dialog .markdown-preview pre { border-color: var(--line); background: var(--surface-2); }
.text-editor-dialog .markdown-preview a { color: var(--accent-strong); }
.text-editor-dialog .markdown-preview th, .text-editor-dialog .markdown-preview td { border-color: var(--line); }
@media (max-width: 760px) {
  .el-dialog.text-editor-dialog { width: calc(100vw - 16px) !important; margin: 8px auto !important; }
  .el-dialog.text-editor-dialog > .el-dialog__header { padding-left: 10px !important; }
  .text-editor-heading { display: block; }
  .text-editor-heading strong { display: block; max-width: 100%; }
  .text-editor-heading small { display: block; margin-top: 2px; }
  .text-editor-shell { height: calc(100dvh - 138px); min-height: 280px; }
  .text-editor-dialog .markdown-preview { padding: 16px 14px 40px; }
}
</style>
