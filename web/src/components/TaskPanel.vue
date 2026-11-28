<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import { ListTodo, Play, RefreshCw } from "@lucide/vue";
import { ElMessage } from "element-plus";
import { api, json } from "../api";
import type { TerminalSession } from "../types";
type Task = { id: string; command: string; sessionIDs: string[]; status: string; output?: string; error?: string; createdAt: string; finishedAt?: string };
const props = defineProps<{ sessions: TerminalSession[] }>();
const command = ref(""); const targets = ref<string[]>([]); const tasks = ref<Task[]>([]); const loading = ref(false); let timer: number | undefined;
async function load() { tasks.value = await api<Task[]>("/api/tasks").catch(() => []); }
async function submit() { if (!command.value.trim() || !targets.value.length) return ElMessage.warning("请输入命令并选择终端"); loading.value = true; try { const task = await api<Task>("/api/tasks", { method: "POST", body: json({ command: command.value, sessionIDs: targets.value }) }); tasks.value.unshift(task); command.value = ""; ElMessage.success("任务已加入队列"); } catch (error) { ElMessage.error(error instanceof Error ? error.message : "任务创建失败"); } finally { loading.value = false; } }
function startPolling() { if (timer === undefined) timer = window.setInterval(load, 2000); }
onMounted(async () => { await load(); startPolling(); }); onBeforeUnmount(() => { if (timer !== undefined) window.clearInterval(timer); });
function statusLabel(value: string) { return ({ queued: "排队中", running: "执行中", completed: "已发送", failed: "失败" } as Record<string, string>)[value] || value; }
</script>
<template>
  <div class="settings-section task-panel">
    <div class="tool-heading"><div><strong>命令任务队列</strong><small>适合批量发送，任务状态会持续保存</small></div><el-button :icon="RefreshCw" size="small" :loading="loading" @click="load">刷新</el-button></div>
    <el-input v-model="command" type="textarea" :rows="3" placeholder="输入要发送到多个终端的命令" />
    <div class="task-submit-row">
      <el-select v-model="targets" multiple collapse-tags placeholder="选择目标终端" class="task-targets"><el-option v-for="session in props.sessions.filter((item) => item.status === 'attached')" :key="session.id" :label="session.name" :value="session.id" /></el-select>
      <el-button type="primary" :icon="Play" :loading="loading" @click="submit">加入队列</el-button>
    </div>
    <div class="task-list"><div v-for="task in tasks" :key="task.id" class="data-row"><div><strong><ListTodo :size="14" /> {{ statusLabel(task.status) }}</strong><small>{{ new Date(task.createdAt).toLocaleString() }} · {{ task.sessionIDs.length }} 个终端</small><code>{{ task.command }}</code><small v-if="task.error" class="danger-text">{{ task.error }}</small></div><span class="task-status" :class="task.status">{{ statusLabel(task.status) }}</span></div><div v-if="!tasks.length" class="empty-small">暂无任务</div></div>
  </div>
</template>
