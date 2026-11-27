<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue';
import { Search, Square, X } from '@lucide/vue';
import { api, json } from '../api';
import { relativeRemotePath, withinRemotePath, joinRemotePath } from '../remoteFiles';
const props = defineProps<{ hostID: string; sessionID: string; rootPath: string; locked?: boolean }>();
const emit = defineEmits<{ open: [path: string, line: number, column: number] }>();
type Match = { path: string; line: number; column: number; text: string; start: number; end: number };
const query = ref(''), directory = ref(''), include = ref(''), exclude = ref(''), caseSensitive = ref(false), regex = ref(false);
const busy = ref(false), error = ref(''), searched = ref(false), limited = ref(false), files = ref(0), skipped = ref(0), matches = ref<Match[]>([]);
const queryInput = ref<HTMLInputElement>();
let controller: AbortController | undefined;
let generation = 0;
const groups = computed(() => { const map = new Map<string, Match[]>(); for (const match of matches.value) { if (!map.has(match.path)) map.set(match.path, []); map.get(match.path)!.push(match); } return [...map]; });
function cancel() { generation++; controller?.abort(); busy.value = false; }
async function search() {
  cancel(); if (!query.value || props.locked) return;
  const root = directory.value.trim() ? directory.value.startsWith('/') ? directory.value.trim() : joinRemotePath(props.rootPath, directory.value.trim()) : props.rootPath;
  if (root.split('/').includes('..') || !withinRemotePath(props.rootPath, root)) { error.value = '搜索目录必须在当前工作区内'; return; }
  const request = ++generation; controller = new AbortController(); busy.value = true; error.value = ''; searched.value = true;
  try {
    const result = await api<{ matches: Match[]; files: number; skipped: number; limited: boolean }>(`/api/sftp/${encodeURIComponent(props.hostID)}/search?session=${encodeURIComponent(props.sessionID)}`, { method: 'POST', body: json({ root, query: query.value, include: include.value, exclude: exclude.value, caseSensitive: caseSensitive.value, regex: regex.value }), signal: controller.signal });
    if (request !== generation) return;
    matches.value = result.matches; files.value = result.files; skipped.value = result.skipped; limited.value = result.limited;
  } catch (cause) { if (request === generation) error.value = cause instanceof Error ? cause.message : '搜索失败'; }
  finally { if (request === generation) busy.value = false; }
}
function focus() { queryInput.value?.focus(); }
defineExpose({ focus });
onBeforeUnmount(cancel);
</script>
<template>
  <div class="code-project-search">
    <form @submit.prevent="search">
      <div class="code-search-query"><input ref="queryInput" v-model="query" aria-label="搜索项目内容" placeholder="搜索项目内容" /><button type="button" :aria-pressed="caseSensitive" aria-label="区分大小写" title="区分大小写" @click="caseSensitive = !caseSensitive">Aa</button><button type="button" :aria-pressed="regex" aria-label="使用正则表达式" title="正则表达式" @click="regex = !regex">.*</button></div>
      <label>搜索范围<input v-model="directory" aria-label="搜索目录" placeholder="整个项目，例如 src" /></label>
      <label>包含文件<input v-model="include" aria-label="包含文件类型" placeholder="*.ts,*.vue 或 src/**" /></label>
      <label>排除文件<input v-model="exclude" aria-label="排除文件" placeholder="*.min.js,tests/**" /></label>
      <div class="code-search-controls"><button type="submit" :disabled="!query || busy || locked"><Search :size="13" />搜索</button><button v-if="busy" type="button" @click="cancel"><Square :size="12" />停止</button><button v-else-if="matches.length" type="button" @click="matches = []; searched = false"><X :size="13" />清除</button></div>
    </form>
    <p v-if="error" class="code-inline-error">{{ error }}</p>
    <p class="code-search-summary" aria-live="polite">{{ busy ? '正在搜索远程文件…' : searched ? `${matches.length} 处匹配 · ${groups.length} 个文件 · 已搜索 ${files} 个文件` : 'Enter 搜索，点击结果跳转到对应行' }}<span v-if="limited"> · 已达搜索上限，请缩小目录或文件范围</span><span v-if="skipped"> · {{ skipped }} 个文件无法读取或不是文本</span></p>
    <div class="code-content-search-results"><details v-for="[path, items] in groups" :key="path" open><summary :title="path">{{ relativeRemotePath(rootPath, path) }} <small>{{ items.length }}</small></summary><button v-for="(match, index) in items" :key="index" :aria-label="`${relativeRemotePath(rootPath, path)}:${match.line}:${match.column}`" @click="emit('open', path, match.line, match.column)"><span>{{ match.line }}</span><code>{{ match.text.slice(0, match.start) }}<mark>{{ match.text.slice(match.start, match.end) }}</mark>{{ match.text.slice(match.end) }}</code></button></details></div>
    <p v-if="searched && !busy && !matches.length && !error" class="code-panel-empty">没有匹配内容</p>
    <small class="code-search-summary">自动跳过依赖、构建目录、符号链接及二进制文件。</small>
  </div>
</template>
