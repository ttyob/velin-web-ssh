<script setup lang="ts">
import { ref, watch } from 'vue';
import { Folder, FolderOpen } from '@lucide/vue';
import { createRemoteFiles, fileName, withinRemotePath, type RemoteFileEntry } from '../remoteFiles';
const props = defineProps<{ modelValue: boolean; hostID: string; sessionID: string; rootPath: string; initialPath?: string; title?: string; disabledPaths?: string[] }>();
const emit = defineEmits<{ 'update:modelValue': [boolean]; select: [path: string] }>();
const selected = ref(props.rootPath), error = ref(''), treeKey = ref(0);
const files = createRemoteFiles(props.hostID, props.sessionID);
watch(() => props.modelValue, open => { if (open) { selected.value = props.initialPath && withinRemotePath(props.rootPath, props.initialPath) ? props.initialPath : props.rootPath; error.value = ''; treeKey.value++; } });
function disabled(entry: RemoteFileEntry) { return !!props.disabledPaths?.some(path => withinRemotePath(path, entry.path)); }
async function load(node: { level: number; data: RemoteFileEntry }, resolve: (entries: RemoteFileEntry[]) => void, reject: () => void) {
  if (!node.level) { resolve([{ name: fileName(props.rootPath), path: props.rootPath, directory: true, symlink: false, size: 0, mode: '', modifiedAt: '' }]); return; }
  try { const result = await files.list(node.data.path); resolve(result.entries.filter(entry => entry.directory && !entry.symlink).sort((a, b) => a.name.localeCompare(b.name))); }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '目录读取失败'; reject(); }
}
</script>
<template>
  <el-dialog :model-value="modelValue" :title="title || '选择目标目录'" width="min(560px, calc(100vw - 28px))" append-to-body :close-on-click-modal="false" @update:model-value="emit('update:modelValue', $event)">
    <p class="code-directory-path">{{ selected }}</p><p v-if="error" class="code-inline-error">{{ error }}</p>
    <el-tree :key="treeKey" class="code-directory-picker code-file-tree" node-key="path" lazy :load="load" :props="{ label: 'name', disabled }" :default-expanded-keys="[rootPath]" :current-node-key="selected" highlight-current @node-click="(entry: RemoteFileEntry) => { if (!disabled(entry)) selected = entry.path; }"><template #default="{ node, data }"><span class="code-tree-entry" :class="{ 'is-disabled': disabled(data) }"><component :is="node.expanded ? FolderOpen : Folder" :size="14" /><span>{{ data.name }}</span></span></template></el-tree>
    <template #footer><el-button @click="emit('update:modelValue', false)">取消</el-button><el-button type="primary" :disabled="disabled({ path: selected } as RemoteFileEntry)" @click="emit('select', selected); emit('update:modelValue', false)">选择此目录</el-button></template>
  </el-dialog>
</template>
<style scoped>
.code-directory-picker { height: min(50vh, 400px); overflow: auto; border: 1px solid var(--line); }
.code-directory-path { color: var(--muted); overflow-wrap: anywhere; font: 12px monospace; }
.code-tree-entry.is-disabled { opacity: .4; }
</style>
