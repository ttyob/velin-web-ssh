<script setup lang="ts">
import { CodeXml, File, Minus, Plus } from "@lucide/vue";
import { computed } from "vue";
import { fileName } from "../remoteFiles";
import type { GitChange } from "../git";
const props = defineProps<{ change: GitChange; group: "staged" | "worktree" | "changes"; busy: boolean; tree?: boolean }>();
defineEmits<{ compare: [GitChange]; edit: [GitChange]; toggle: [GitChange] }>();
const deleted = computed(() => props.change.worktree === "D" || (props.change.index === "D" && props.change.worktree === " "));
const status = computed(() => {
  const value = props.group === "staged" ? props.change.index : props.change.worktree.trim() ? props.change.worktree : props.change.index;
  return value === "?" ? "U" : value;
});
const detail = computed(() => {
  if (props.tree) return "";
  const slash = props.change.path.lastIndexOf("/");
  if (slash >= 0) return props.change.path.slice(0, slash);
  const dot = props.change.path.lastIndexOf(".");
  return dot > 0 ? props.change.path.slice(dot + 1).toLowerCase() : "";
});
</script>
<template>
  <div class="code-change-row" :title="change.path">
    <button class="code-change-file" :aria-label="`${group === 'staged' ? '查看已暂存' : group === 'changes' ? '查看更改' : '查看未暂存'} ${change.path}`" @click.stop="$emit('compare', change)"><File :size="15" /><span>{{ fileName(change.path) }}<small v-if="detail">{{ detail }}</small></span><b class="code-git-badge" :data-status="status">{{ status }}</b></button>
    <button class="code-change-action" :aria-label="`编辑 ${change.path}`" title="打开文件" :disabled="deleted || busy" @click.stop="$emit('edit', change)"><CodeXml :size="13" /></button>
    <button v-if="group !== 'changes'" class="code-change-action" :aria-label="`${group === 'staged' ? '取消暂存' : '暂存'} ${change.path}`" :title="group === 'staged' ? '取消暂存' : '暂存'" :disabled="busy" @click.stop="$emit('toggle', change)"><component :is="group === 'staged' ? Minus : Plus" :size="13" /></button>
  </div>
</template>
