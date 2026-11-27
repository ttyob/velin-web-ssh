<script setup lang="ts">
import { computed, ref } from "vue";
import { ChevronRight, Folder, FolderTree, List, Minus, Plus } from "@lucide/vue";
import { buildGitChangeTree, type GitChange, type GitTreeNode } from "../git";
import CodeGitChangeRow from "./CodeGitChangeRow.vue";
const props = defineProps<{ changes: GitChange[]; group: "staged" | "worktree" | "changes"; busy: boolean; tree: boolean; storageKey?: string }>();
defineEmits<{ compare: [GitChange]; edit: [GitChange]; toggle: [GitChange]; toggleAll: []; toggleView: [] }>();
const collapsed = ref<string[]>([]);
const groupOpen = ref(true);
try { const saved = JSON.parse(localStorage.getItem(props.storageKey || '') || '[]'); if (Array.isArray(saved)) collapsed.value = saved.filter(item => typeof item === 'string'); } catch { /* Optional preference. */ }
function remember(id: string, open: boolean) {
  collapsed.value = open ? collapsed.value.filter(item => item !== id) : [...new Set([...collapsed.value, id])];
  try { if (props.storageKey) localStorage.setItem(props.storageKey, JSON.stringify(collapsed.value)); } catch { /* Optional preference. */ }
}
const expanded = computed(() => { const keys: string[] = []; const walk = (nodes: ReturnType<typeof buildGitChangeTree>) => { for (const node of nodes) { if (node.directory && !collapsed.value.includes(node.id)) keys.push(node.id); if (node.children) walk(node.children); } }; walk(directories.value); return keys; });
const directories = computed(() => buildGitChangeTree(props.changes));
</script>
<template>
  <section class="code-change-group" :class="{ collapsed: !groupOpen, empty: !changes.length }" :aria-label="group === 'staged' ? '已暂存更改' : group === 'changes' ? '更改' : '未暂存更改'">
    <header><button class="code-change-disclosure" :aria-expanded="groupOpen" :aria-label="`${groupOpen ? '折叠' : '展开'}${group === 'staged' ? '已暂存' : '更改'}`" @click="groupOpen = !groupOpen"><ChevronRight :size="13" :class="{ open: groupOpen }" /></button><strong @click="groupOpen = !groupOpen">{{ group === 'staged' ? '已暂存的更改' : '更改' }}</strong><span>{{ changes.length }}</span><button class="code-change-view-action" :title="tree ? '切换为按文件排列' : '切换为按目录排列'" :aria-label="tree ? '切换为按文件排列' : '切换为按目录排列'" @click="$emit('toggleView')"><component :is="tree ? List : FolderTree" :size="14" /></button><button v-if="group !== 'changes'" class="code-change-group-action" :disabled="busy || !changes.length" :title="group === 'staged' ? '取消暂存全部' : '暂存全部'" :aria-label="group === 'staged' ? '取消暂存全部' : '暂存全部'" @click="$emit('toggleAll')"><component :is="group === 'staged' ? Minus : Plus" :size="14" /></button></header>
    <div v-if="groupOpen" class="code-change-list">
      <el-tree v-if="tree && changes.length" class="code-file-tree code-git-change-tree" :data="directories" node-key="id" :default-expanded-keys="expanded" @node-expand="(data: GitTreeNode) => remember(data.id, true)" @node-collapse="(data: GitTreeNode) => remember(data.id, false)" :expand-on-click-node="true">
        <template #default="{ data }"><span v-if="data.directory" class="code-tree-entry"><Folder :size="14" /><span>{{ data.label }}</span></span><CodeGitChangeRow v-else-if="data.change" :change="data.change" :group="group" :busy="busy" tree @compare="$emit('compare', $event)" @edit="$emit('edit', $event)" @toggle="$emit('toggle', $event)" /></template>
      </el-tree>
      <template v-else-if="!tree"><CodeGitChangeRow v-for="change in changes" :key="change.path" :change="change" :group="group" :busy="busy" @compare="$emit('compare', $event)" @edit="$emit('edit', $event)" @toggle="$emit('toggle', $event)" /></template>
      <p v-if="!changes.length" class="code-group-empty">{{ group === 'staged' ? '没有已暂存的更改' : '工作区干净' }}</p>
    </div>
  </section>
</template>
