<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { basicSetup } from "codemirror";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { StreamLanguage } from "@codemirror/language";
import { properties } from "@codemirror/legacy-modes/mode/properties";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { yaml } from "@codemirror/legacy-modes/mode/yaml";
import { EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { defaultKeymap, historyKeymap } from "@codemirror/commands";
import { searchKeymap } from "@codemirror/search";
import { editorTheme } from "../editorTheme";
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

function languageFor(name: string) {
  const lower = name.toLowerCase();
  if (lower.endsWith(".json") || lower.endsWith(".jsonc")) return json();
  if (lower.endsWith(".md") || lower.endsWith(".markdown")) return markdown();
  if (/\.(ya?ml)$/.test(lower)) return StreamLanguage.define(yaml);
  if (/\.(sh|bash|zsh)$/.test(lower)) return StreamLanguage.define(shell);
  if (/\.(ini|env|properties|conf|cfg)$/.test(lower) || /(^|\/)\.env(\.|$)/.test(lower)) {
    return StreamLanguage.define(properties);
  }
  return [];
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
  loading.value = true;
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
          keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap]),
          languageFor(props.file.name),
          editorTheme,
          EditorView.lineWrapping,
          EditorView.updateListener.of((update) => {
            if (update.docChanged) {
              markdownContent.value = update.state.doc.toString();
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
    ElMessage.error(error instanceof Error ? error.message : "文本文件读取失败");
  } finally {
    loading.value = false;
  }
}

async function saveFile() {
  if (!props.host || !props.file || !editor || !ready.value || !dirty.value) return;
  const content = editor.state.doc.toString();
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
  saving.value = true;
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
        body: content,
      },
    );
    originalContent = content;
    markdownContent.value = content;
    version.value = result.version;
    byteSize.value = result.bytes;
    dirty.value = false;
    ElMessage.success("文件已安全保存");
    emit("saved");
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      ElMessage.error("远程文件已经变化，未覆盖原文件。请关闭并重新打开后合并修改");
    } else {
      ElMessage.error(error instanceof Error ? error.message : "文件保存失败");
    }
  } finally {
    saving.value = false;
  }
}

async function confirmClose() {
	if (dirty.value) {
		try {
      await ElMessageBox.confirm("尚有未保存的修改，确定关闭？", "放弃修改", {
        type: "warning",
        confirmButtonText: "放弃修改",
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
    class="text-editor-dialog"
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
