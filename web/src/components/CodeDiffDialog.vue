<script setup lang="ts">
import { useDialogReturnFocus } from "../composables/useDialogReturnFocus";
import { useDialogGeometry } from "../dialogGeometry";
import { ref, watch } from "vue";
import { CodeXml, Columns2, Minus, Plus, RefreshCw, Undo2 } from "@lucide/vue";
import type { CodeHunk } from "../codeHunks";
import CodeDiffView from "./CodeDiffView.vue";
import DiffFilePreview from "./DiffFilePreview.vue";
import { readDiffState, type DiffFilePreview as FilePreview } from "../codeDiff";
const props = defineProps<{ modelValue: boolean; path: string; stateKey?: string; preview?: FilePreview; left: string; right: string; leftLabel: string; rightLabel: string; loading: boolean; error: string; canEdit: boolean; refreshable: boolean; group?: "staged" | "worktree" | "changes"; hunkAllowed?: boolean; busy?: boolean; hunkHint?: string }>();
defineEmits<{ "update:modelValue": [boolean]; refresh: []; load: []; edit: []; applyHunk: [CodeHunk, "stage" | "unstage" | "discard"] }>();
const { apply: applyGeometry } = useDialogGeometry(() => props.modelValue, () => "code-diff-dialog");
const { restoreFocus } = useDialogReturnFocus(() => props.modelValue);
const inline = ref(false);
const selectedHunk = ref<CodeHunk>();
try { inline.value = localStorage.getItem('code-diff-inline') === 'true'; } catch { /* Optional preference. */ }
function toggleLayout() { inline.value = !inline.value; try { localStorage.setItem('code-diff-inline', String(inline.value)); } catch { /* Optional preference. */ } }
watch([() => props.path, () => props.stateKey, () => props.modelValue], () => {
  if (props.modelValue) { inline.value = readDiffState(props.stateKey || props.path)?.inline ?? inline.value; selectedHunk.value = undefined; }
}, { immediate: true });
</script>
<template>
  <el-dialog :model-value="modelValue" :title="`文件变更对比 · ${path}`" class="code-diff-dialog" width="min(1440px, calc(100vw - 32px))" top="4vh" append-to-body destroy-on-close draggable @opened="applyGeometry" @close-auto-focus="restoreFocus" :close-on-click-modal="false" @update:model-value="$emit('update:modelValue', $event)">
    <div v-loading="loading" class="code-diff-dialog-content"><div v-if="error" class="code-document-error"><p>{{ error }}</p><el-button @click="$emit('refresh')">重新读取</el-button></div><DiffFilePreview v-else-if="preview && !loading" :preview="preview" :left-label="leftLabel" :right-label="rightLabel" @load="$emit('load')" /><CodeDiffView v-else-if="!loading" :state-key="stateKey" :left="left" :right="right" :path="path" :inline="inline" :visible="modelValue" :left-label="leftLabel" :right-label="rightLabel" @hunk="selectedHunk = $event"><template #actions><div class="code-diff-actions"><span v-if="group" class="code-diff-action-group" :title="hunkHint || (selectedHunk ? `操作第 ${selectedHunk.index + 1} 块修改` : '请先选择修改块')"><button v-if="group === 'worktree'" aria-label="暂存此块" title="暂存当前修改块" :disabled="!hunkAllowed || !selectedHunk || busy || loading" @click="selectedHunk && $emit('applyHunk', selectedHunk, 'stage')"><Plus :size="14" /></button><button v-if="group === 'staged'" aria-label="取消暂存此块" title="取消暂存当前修改块" :disabled="!hunkAllowed || !selectedHunk || busy || loading" @click="selectedHunk && $emit('applyHunk', selectedHunk, 'unstage')"><Minus :size="14" /></button><button v-if="group === 'worktree' || group === 'changes'" class="danger" aria-label="丢弃此块" title="丢弃当前修改块" :disabled="!hunkAllowed || !selectedHunk || busy || loading" @click="selectedHunk && $emit('applyHunk', selectedHunk, 'discard')"><Undo2 :size="14" /></button></span><span class="code-diff-action-group"><button v-if="refreshable" aria-label="刷新对比" title="刷新对比" :disabled="loading" @click="$emit('refresh')"><RefreshCw :size="14" /></button><button v-if="canEdit" aria-label="编辑文件" title="在编辑器中打开" @click="$emit('edit')"><CodeXml :size="14" /></button><button :aria-label="inline ? '切换为并排对比' : '切换为行内对比'" :title="inline ? '切换为并排对比' : '切换为行内对比'" :aria-pressed="inline" @click="toggleLayout"><Columns2 :size="14" /></button></span></div></template></CodeDiffView></div>
  </el-dialog>
</template>
<style>
.code-diff-dialog { resize: none; overflow: hidden; min-width: min(340px, calc(100vw - 24px)); min-height: 260px; max-width: calc(100vw - 24px); display: flex; flex-direction: column; height: 88dvh; max-height: 1100px; margin-bottom: 0; }
.code-diff-dialog > .el-dialog__header { min-height: 38px; flex-shrink: 0; margin: 0; padding: 8px 42px 8px 12px !important; border-bottom: 1px solid var(--line); }
.code-diff-dialog > .el-dialog__header .el-dialog__title { display: block; overflow: hidden; color: var(--text); font-size: 12px; font-weight: 500; text-overflow: ellipsis; white-space: nowrap; }
.code-diff-dialog > .el-dialog__header .el-dialog__headerbtn { top: 0 !important; right: 0 !important; width: 38px !important; height: 38px !important; }
.code-diff-dialog > .el-dialog__body { display: flex; flex: 1; flex-direction: column; min-height: 0; overflow: hidden; padding: 0 !important; }
.code-diff-dialog-content { flex: 1; min-height: 0; overflow: hidden; }
.code-diff-dialog .code-diff-toolbar > button { display: inline-flex; align-items: center; justify-content: center; padding: 3px; border: 0; background: transparent; color: var(--text); cursor: pointer; }
.code-diff-dialog .code-diff-toolbar > button:disabled { opacity: .4; cursor: default; }
.code-diff-dialog .code-diff-toolbar > button:hover { background: var(--surface-2); }
.code-diff-actions { display: flex; align-items: center; gap: 5px; margin-left: 2px; }
.code-diff-action-group { display: inline-flex; align-items: center; overflow: hidden; border: 1px solid var(--line); border-radius: 3px; }
.code-diff-action-group button { display: inline-flex; width: 27px; height: 25px; align-items: center; justify-content: center; padding: 0; border: 0; background: transparent; color: var(--muted); cursor: pointer; }
.code-diff-action-group button + button { border-left: 1px solid var(--line); }
.code-diff-action-group button:hover:not(:disabled), .code-diff-action-group button:focus-visible { outline: none; background: var(--surface-2); color: var(--text); }
.code-diff-action-group button.danger:hover:not(:disabled) { color: var(--icon-danger); }
.code-diff-action-group button:disabled { opacity: .35; cursor: default; }
@media (max-width: 640px) { .code-diff-dialog > .el-dialog__header { padding-left: 8px !important; } .code-diff-actions { gap: 3px; } }
</style>
