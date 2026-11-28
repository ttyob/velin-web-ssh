<script setup lang="ts">
import { ref, watch } from 'vue';
import { codeLanguage, codeLanguageOptions } from '../codeLanguages';
import type { CodeLanguageId } from '../codeLanguageDetection';
import type { EditorIndent } from '../editorPreferences';
const props = defineProps<{ path: string; line: number; column: number; content: string; language?: CodeLanguageId; indent: EditorIndent; disabled?: boolean }>();
const emit = defineEmits<{ language: [value: CodeLanguageId | undefined]; indent: [value: EditorIndent]; ending: [value: 'LF' | 'CRLF']; goto: [] }>();
const languageOpen = ref(false), indentOpen = ref(false), endingOpen = ref(false), encodingOpen = ref(false), query = ref('');
watch(() => props.path, () => { languageOpen.value = indentOpen.value = endingOpen.value = encodingOpen.value = false; query.value = ''; });
function selectLanguage(id?: CodeLanguageId) { emit('language', id); languageOpen.value = false; }
function selectIndent(id: EditorIndent) { emit('indent', id); indentOpen.value = false; }
</script>
<template>
  <button title="跳转到行 (Ctrl+G)" aria-label="跳转到行" :disabled="disabled" @click="$emit('goto')">行 {{ line }}，列 {{ column }}</button>
  <el-popover v-model:visible="indentOpen" trigger="click" placement="top" :width="180" popper-class="editor-status-popper"><template #reference><button aria-label="设置缩进" :disabled="disabled">{{ indent === 'tab' ? 'Tab' : `空格: ${indent}` }}</button></template><div class="editor-status-options"><small>后续输入的缩进</small><button v-for="id in (['2', '4', '8', 'tab'] as const)" :key="id" :aria-pressed="indent === id" @click="selectIndent(id)">{{ id === 'tab' ? '制表符' : `${id} 个空格` }}</button></div></el-popover>
  <el-popover v-model:visible="languageOpen" trigger="click" placement="top" :width="230" popper-class="editor-status-popper"><template #reference><button aria-label="选择代码语言" :disabled="disabled">{{ codeLanguage(path, language).name }}</button></template><div class="editor-status-options"><input v-model="query" aria-label="筛选代码语言" placeholder="搜索语言" /><button :aria-pressed="!language" @click="selectLanguage()">自动识别 · {{ codeLanguage(path).name }}</button><div class="editor-language-list"><button v-for="item in codeLanguageOptions.filter(item => `${item.name} ${item.id}`.toLowerCase().includes(query.toLowerCase()))" :key="item.id" :aria-pressed="language === item.id" @click="selectLanguage(item.id)">{{ item.name }}</button></div></div></el-popover>
  <el-popover v-model:visible="encodingOpen" trigger="click" placement="top" :width="220" popper-class="editor-status-popper"><template #reference><button title="文件编码" aria-label="查看文件编码">UTF-8</button></template><div class="editor-status-options"><strong>UTF-8</strong><small>远程文本编辑使用 UTF-8。其他编码文件请下载后转换，避免保存时乱码。</small></div></el-popover>
  <el-popover v-model:visible="endingOpen" trigger="click" placement="top" :width="190" popper-class="editor-status-popper"><template #reference><button aria-label="设置换行符" :disabled="disabled">{{ content.includes('\r\n') ? 'CRLF' : 'LF' }}</button></template><div class="editor-status-options"><small>转换当前文件的换行符</small><button v-for="id in (['LF', 'CRLF'] as const)" :key="id" :aria-pressed="content.includes('\r\n') ? id === 'CRLF' : id === 'LF'" @click="emit('ending', id); endingOpen = false">{{ id === 'LF' ? 'LF · Linux / macOS' : 'CRLF · Windows' }}</button></div></el-popover>
</template>
<style>
.editor-status-popper.el-popover { padding: 7px !important; }
.editor-status-options { display: flex; flex-direction: column; gap: 3px; color: var(--text); font-size: 12px; }
.editor-status-options > small, .editor-status-options > strong { padding: 5px 7px; }
.editor-status-options small { color: var(--muted); }
.editor-status-options button { width: 100%; border: 0; border-radius: 3px; padding: 6px 7px; text-align: left; color: var(--text); background: transparent; font: inherit; cursor: pointer; }
.editor-status-options button:hover, .editor-status-options button[aria-pressed='true'] { background: var(--surface-2); }
.editor-status-options button[aria-pressed='true']::after { content: '✓'; float: right; color: var(--accent); }
.editor-status-options input { min-width: 0; width: 100%; box-sizing: border-box; padding: 6px 7px; border: 1px solid var(--line); border-radius: 3px; color: var(--text); background: var(--bg); font: inherit; outline-color: var(--accent); }
.editor-language-list { max-height: 240px; overflow: auto; }
</style>
