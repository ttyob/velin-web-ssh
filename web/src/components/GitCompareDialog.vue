<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { Save, Sparkles } from "@lucide/vue";
import { api, csrfHeaders, json } from "../api";
import type { GitChange } from "../git";
import type { Host } from "../types";
import { normalizeGitText, parseMergeConflicts } from "../gitMerge";
import GitCompareEditor from "./GitCompareEditor.vue";
import GitSurface from "./GitSurface.vue";
import CodeDiffView from "./CodeDiffView.vue";

const props = defineProps<{
  modelValue: boolean; host?: Host; sessionId?: string; repository: string;
  change?: GitChange; merge?: boolean; aiConfigured?: boolean; embedded?: boolean;
}>();
const emit = defineEmits<{ "update:modelValue": [boolean]; saved: []; dirty: [boolean]; busy: [boolean] }>();
const savedSnapshot = ref<string>();
const loading = ref(false), saving = ref(false), analyzing = ref(false);
const error = ref("");
const left = ref(""), right = ref(""), initialResult = ref(""), result = ref("");
const version = ref("");
const ready = ref(false);
const conflicts = ref(0);
const analysis = ref("");
const analysisOpen = ref(false);
const dirty = computed(() => props.modelValue && props.merge && result.value !== (savedSnapshot.value ?? normalizeGitText(initialResult.value)));
let generation = 0;
let originalText = "";

function quote(value: string) { return `'${value.replace(/'/g, `'\\''`)}'`; }
function textURL() {
  const q = new URLSearchParams({ path: `${props.repository.replace(/\/$/, "")}/${props.change!.path}` });
  if (props.sessionId) q.set("session", props.sessionId);
  return `/api/sftp/${props.host!.id}/text?${q}`;
}
async function blob(revision: string, path = props.change!.path) {
  const q = new URLSearchParams({ repository: props.repository, path, revision });
  return api<{ content: string; exists: boolean }>(`/api/hosts/${props.host!.id}/agent/git-text?${q}`);
}
async function load() {
  if (!props.modelValue || !props.change || !props.host) return;
  const current = ++generation;
  ready.value = false; loading.value = true; error.value = ""; analysis.value = "";
  try {
    if (props.merge) {
      const [a, b, work] = await Promise.all([
        blob("2"), blob("3"), api<{ content: string; version: string }>(textURL()),
      ]);
      if (current !== generation) return;
      left.value = a.content; right.value = b.content;
      originalText = work.content;
      initialResult.value = work.content;
      result.value = normalizeGitText(work.content);
      version.value = work.version;
      conflicts.value = parseMergeConflicts(result.value).length;
    } else {
      const a = props.change.index === "?" ? { content: "" } : await blob("HEAD", props.change.originalPath || props.change.path);
      let content = "";
      if (props.change.worktree !== "D" && !(props.change.index === "D" && props.change.worktree === " ")) {
        content = (await api<{ content: string }>(textURL())).content;
      }
      if (current !== generation) return;
      left.value = a.content; right.value = content;
    }
    ready.value = true;
  } catch (cause) {
    if (current === generation) error.value = cause instanceof Error ? cause.message : "完整文件读取失败";
  } finally {
    if (current === generation) loading.value = false;
  }
}

async function save(resolve: boolean) {
  if (!ready.value || saving.value) return;
  if (resolve && /^(?:<{7,}|={7,}|>{7,}|\|{7,})(?: .*)?$/m.test(result.value)) {
    ElMessage.warning("最终文件仍含冲突标记，请处理后再标记已解决"); return;
  }
  const content = originalText.includes("\r\n") ? result.value.replace(/\n/g, "\r\n") : result.value;
  if (!content && originalText) {
    try { await ElMessageBox.confirm("最终文件为空，保存将清空整个文件，确认继续？", "保存空文件", { type: "warning" }); } catch { return; }
  }
  saving.value = true;
  try {
    // Verify the source index has not changed while the merge dialog was open.
    const [a, b] = await Promise.all([blob("2"), blob("3")]);
    if (a.content !== left.value || b.content !== right.value) throw new Error("Git 冲突版本已变化，请重新打开文件后处理");
    const response = await fetch(textURL(), {
      method: "PUT", credentials: "same-origin",
      headers: { ...await csrfHeaders(), "Content-Type": "text/plain; charset=utf-8", "If-Match": `"${version.value}"`, ...(!content ? { "X-Velin-Allow-Empty": "true" } : {}) },
      body: content,
    });
    const body = await response.json();
    if (!response.ok) throw new Error(body.message || "保存失败");
    version.value = body.version; originalText = content;
    // Do not reset the editor document (or undo history) after saving.
    savedSnapshot.value = result.value;
    if (resolve) {
      const outcome = await api<{ success: boolean; output?: string; error?: string }>(`/api/hosts/${props.host!.id}/agent/command`, {
        method: "POST", body: json({ command: `git -C ${quote(props.repository)} --literal-pathspecs add -- ${quote(props.change!.path)}` }),
      });
      if (!outcome.success) throw new Error(outcome.output || outcome.error || "文件已保存，但标记解决失败");
      ElMessage.success("完整文件已保存并标记为已解决");
      emit("saved"); emit("update:modelValue", false);
    } else {
      ElMessage.success("完整文件已保存"); emit("saved");
    }
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "保存失败");
  } finally { saving.value = false; }
}
async function beforeClose(done: () => void) {
  if (saving.value) return;
  if (dirty.value) {
    try { await ElMessageBox.confirm("最终文件还有未保存的修改，确定关闭？", "关闭合并", { type: "warning", confirmButtonText: "放弃修改", cancelButtonText: "取消" }); } catch { return; }
  }
  done();
}
async function analyze() {
  analyzing.value = true; analysisOpen.value = true;
  try {
    const request = await api<{ message: string }>("/api/agent/git-assist", {
      method: "POST", body: json({ kind: "conflict_analysis", path: props.change?.path, content: `当前分支完整文件：\n${left.value}\n\n传入分支完整文件：\n${right.value}\n\n当前合并结果：\n${result.value}`.slice(0, 35000) }),
    });
    analysis.value = request.message;
  } catch (cause) { analysis.value = cause instanceof Error ? cause.message : "分析失败"; }
  finally { analyzing.value = false; }
}
watch(() => props.modelValue, open => {
  if (open) { savedSnapshot.value = undefined; void load(); }
  else { generation++; ready.value = false; analysisOpen.value = false; }
}, { immediate: true });
async function confirmClose() {
  let allowed = false;
  await beforeClose(() => { allowed = true; });
  return allowed;
}
async function saveCurrent() { if (props.modelValue && props.merge) await save(false); }
defineExpose({ confirmClose, saveCurrent });
watch(dirty, value => emit("dirty", Boolean(value)));
watch(saving, value => emit("busy", value));
</script>

<template>
  <div :class="{ 'git-compare-embedded': embedded }" v-show="modelValue">
  <GitSurface v-show="!embedded || !analysisOpen" :embedded="embedded" :model-value="modelValue" class="git-full-compare-dialog" :title="merge ? '手动合并 · 完整文件' : '文件对比 · 完整文件'" width="calc(100vw - 40px)" top="3vh" append-to-body :close-on-click-modal="false" :before-close="beforeClose" @update:model-value="emit('update:modelValue', $event)">
    <div class="git-full-compare-path">{{ change?.path }}</div>
    <p v-if="merge" class="git-full-compare-hint">左右箭头将对应块应用到中间文件。中间可修改整份文件；处理完后保存并标记已解决。</p>
    <div v-loading="loading" class="git-full-compare-content" :class="{ 'is-diff': !merge }">
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
      <CodeDiffView v-if="ready && !merge" :left="left" :right="right" :path="change?.path || ''" :inline="false" :visible="modelValue" />
      <GitCompareEditor v-else-if="ready" :left="left" :right="right" :result="initialResult" :merge="merge" @change="result = $event" @conflicts="conflicts = $event" />
    </div>
    <template #footer>
      <el-button v-if="merge" :icon="Sparkles" :disabled="!ready || !aiConfigured || saving" :loading="analyzing" @click="analyze">AI 分析</el-button>
      <el-button :disabled="saving" @click="beforeClose(() => emit('update:modelValue', false))">关闭</el-button>
      <el-button v-if="merge" :icon="Save" :disabled="!ready || saving" :loading="saving" @click="save(false)">保存完整文件</el-button>
      <el-button v-if="merge" type="primary" :disabled="!ready || saving || conflicts > 0" :loading="saving" @click="save(true)">保存并标记已解决</el-button>
    </template>
  </GitSurface>
  <GitSurface :embedded="embedded" v-model="analysisOpen" title="AI 冲突分析" width="min(820px, calc(100vw - 30px))" append-to-body>
    <pre v-loading="analyzing" class="git-merge-ai-result">{{ analysis }}</pre>
  </GitSurface>
  </div>
</template>

<style scoped>
.git-compare-embedded { height: 100%; min-height: 0; position: relative; }
.git-compare-embedded :deep(.git-inline-body) { display: flex; flex-direction: column; min-height: 0; overflow: hidden; }
.git-compare-embedded .git-full-compare-content { flex: 1; min-height: 0; overflow: hidden; }
.git-compare-embedded :deep(.git-compare-editor) { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.git-compare-embedded :deep(.git-compare-body) { flex: 1; height: auto; min-height: 0; }
.git-compare-embedded :deep(.git-compare-controls), .git-compare-embedded :deep(.git-compare-headings), .git-compare-embedded :deep(.git-global-horizontal) { flex-shrink: 0; }
.git-compare-embedded > .git-inline-surface + .git-inline-surface { position: absolute; inset: 0; z-index: 2; }
.git-compare-embedded .git-merge-ai-result { flex: 1; max-height: none; min-height: 0; }
.git-compare-embedded :deep(.cm-editor) { color: var(--text); background: var(--bg); }
.git-compare-embedded :deep(.cm-gutters) { color: var(--muted); background: var(--surface); border-color: var(--line); }
.git-compare-embedded :deep(.cm-activeLine), .git-compare-embedded :deep(.cm-activeLineGutter) { color: inherit; background: var(--surface-2); }
.git-compare-embedded :deep(.cm-cursor) { border-color: var(--text); }
.git-compare-embedded :deep(.git-line-change), .git-compare-embedded :deep(.git-word-change) { background: #789b832b; }
.git-compare-embedded :deep(.git-line-conflict) { background: #bd737335; }
.git-compare-embedded :deep(.git-global-horizontal), .git-compare-embedded :deep(.git-global-vertical) { background: var(--surface); }
.git-compare-embedded :deep(.git-global-thumb) { background: var(--muted); border-color: var(--surface); }


.git-full-compare-path { color: var(--accent-strong); font: 12px/1.5 var(--font-mono, monospace); overflow-wrap: anywhere; margin-bottom: 8px; }
.git-full-compare-hint { margin: 0 0 10px; color: var(--text-soft); font-size: 12px; }
.git-full-compare-content { min-height: 300px; }
.git-full-compare-content.is-diff { height: min(68dvh, 900px); }
.git-compare-embedded .git-full-compare-content.is-diff { height: auto; }
.git-merge-ai-result { min-height: 240px; max-height: 65vh; overflow: auto; white-space: pre-wrap; color: var(--text); font: 13px/1.6 var(--font-mono, monospace); }
</style>
