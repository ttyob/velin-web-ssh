<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { CircleAlert, RefreshCw } from '@lucide/vue';
import { api, json } from '../api';
import GitSurface from './GitSurface.vue';
const props = defineProps<{ modelValue: boolean; path: string; content: string; dirty: boolean }>();
const emit = defineEmits<{ 'update:modelValue': [boolean]; locate: [line: number, content: string] }>();
type Diagnostic = { line: number; severity: 'error' | 'warning' | 'info'; message: string; suggestion: string };
const busy = ref(false), error = ref(''), summary = ref(''), model = ref(''), snapshot = ref(''), snapshotPath = ref('');
const diagnostics = ref<Diagnostic[]>([]);
const stale = computed(() => snapshot.value !== props.content || snapshotPath.value !== props.path);
const severity = { error: '错误', warning: '警告', info: '提示' };
let controller: AbortController | undefined, generation = 0;
function cancel() { generation++; controller?.abort(); busy.value = false; }
async function analyze() {
  cancel(); snapshot.value = props.content; snapshotPath.value = props.path;
  summary.value = ''; diagnostics.value = []; model.value = ''; error.value = '';
  if (new TextEncoder().encode(snapshot.value).length > 120 * 1024) { error.value = '当前文件超过 120 KiB 分析上限，请缩小文件后重试。'; return; }
  const request = ++generation; controller = new AbortController(); busy.value = true;
  try {
    const result = await api<{ summary: string; diagnostics: Diagnostic[]; model: string }>('/api/agent/file-analysis', { method: 'POST', body: json({ path: snapshotPath.value, content: snapshot.value }), signal: controller.signal });
    if (request !== generation) return;
    summary.value = result.summary; diagnostics.value = result.diagnostics; model.value = result.model;
  } catch (cause) { if (request === generation) error.value = cause instanceof Error ? cause.message : '文件分析失败'; }
  finally { if (request === generation) busy.value = false; }
}
watch(() => props.modelValue, open => { if (open) void analyze(); else cancel(); }, { immediate: true });
onBeforeUnmount(cancel);
</script>
<template>
  <GitSurface :model-value="modelValue" title="AI 分析当前文件" class="code-file-analysis" width="720px" append-to-body :close-on-click-modal="false" @update:model-value="emit('update:modelValue', $event)">
    <p class="code-analysis-path" :title="snapshotPath">{{ snapshotPath }}</p>
    <p class="code-analysis-note">{{ dirty ? '分析包含当前未保存的修改' : '分析当前编辑内容' }} · 仅供审阅，不会执行代码或修改文件</p>
    <p v-if="stale" class="code-analysis-stale">文件内容已变化，以下结果来自先前版本，请重新分析后定位。</p>
    <div v-if="busy" class="code-analysis-pending" role="status">正在检查语法、逻辑和潜在运行错误…</div>
    <p v-if="error" class="code-analysis-error" role="alert">{{ error }}</p>
    <p v-if="summary" class="code-analysis-summary">{{ summary }}</p>
    <ol v-if="diagnostics.length" class="code-analysis-list">
      <li v-for="(item, index) in diagnostics" :key="index" :data-severity="item.severity"><header><CircleAlert :size="15" /><strong>{{ severity[item.severity] }}</strong><button :disabled="stale" :aria-label="`跳转到第 ${item.line} 行`" @click="emit('locate', item.line, snapshot)">第 {{ item.line }} 行</button></header><p>{{ item.message }}</p><p v-if="item.suggestion" class="code-analysis-suggestion">{{ item.suggestion }}</p></li>
    </ol>
    <p v-if="summary && !diagnostics.length" class="code-analysis-note">本次分析未发现明确问题。</p>
    <template #footer><span class="code-analysis-model">{{ model }}</span><el-button v-if="busy" @click="cancel">停止分析</el-button><el-button v-else :icon="RefreshCw" @click="analyze">重新分析</el-button><el-button @click="emit('update:modelValue', false)">关闭</el-button></template>
  </GitSurface>
</template>
<style>
.code-file-analysis { max-width: calc(100vw - 24px); }
.code-file-analysis .el-dialog__body { max-height: 65dvh; overflow: auto; }
.code-analysis-path { margin: 0 0 8px; overflow-wrap: anywhere; font: 12px/1.6 var(--font-mono, monospace); color: var(--text); }
.code-analysis-note, .code-analysis-model { color: var(--muted); font-size: 11px; line-height: 1.7; }
.code-analysis-model { margin-right: auto; }
.code-analysis-pending, .code-analysis-summary { margin: 18px 0; white-space: pre-wrap; line-height: 1.8; }
.code-analysis-error { color: var(--icon-danger); overflow-wrap: anywhere; }
.code-analysis-stale { padding: 9px; background: var(--surface-2); color: var(--icon-warm); }
.code-analysis-list { list-style: none; padding: 0; margin: 16px 0 0; }
.code-analysis-list li { padding: 13px 0; border-top: 1px solid var(--line); }
.code-analysis-list header { display: flex; gap: 7px; align-items: center; color: var(--icon-warm); font-size: 12px; }
.code-analysis-list li[data-severity="error"] header { color: var(--icon-danger); }
.code-analysis-list li[data-severity="info"] header { color: var(--muted); }
.code-analysis-list header button { margin-left: auto; border: 1px solid var(--line); background: var(--surface); color: var(--text); padding: 4px 8px; cursor: pointer; border-radius: 3px; font: inherit; }
.code-analysis-list header button:disabled { opacity: .4; cursor: default; }
.code-analysis-list p { margin: 9px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 12px; line-height: 1.8; }
.code-analysis-suggestion { color: var(--muted); }
.code-file-analysis .el-dialog__footer { display: flex; align-items: center; gap: 8px; }
</style>
