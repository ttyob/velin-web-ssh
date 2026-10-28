<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import {
  Activity,
  Ban,
  Clock3,
  Cpu,
  Gauge,
  HardDrive,
  LogIn,
  LogOut,
  MemoryStick,
  Network,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  ShieldX,
} from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import {
  deriveCounterRates,
  monitorCountersCommand,
  parseMonitorCounters,
  parseSSHMonitor,
  sshMonitorCommand,
  type MonitorCounters,
  type SSHMonitor,
} from "../hostMonitor";
import type { AgentProcess, AgentSnapshot, AgentStatus, Host } from "../types";
import MonitorSparkline from "./MonitorSparkline.vue";

type MonitorTab = "basic" | "processes" | "ssh";
type MonitorSample = {
  collectedAt: number;
  cpu: number;
  memory: number;
  disk: number;
  received: number;
  sent: number;
};

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
}>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();

const status = ref<AgentStatus>();
const snapshot = ref<AgentSnapshot>();
const processes = ref<AgentProcess[]>([]);
const initialLoading = ref(false);
const refreshing = ref(false);
const error = ref("");
const autoRefresh = ref(true);
const refreshSeconds = ref(10);
const processSearch = ref("");
const activeTab = ref<MonitorTab>("basic");
const counterSnapshot = ref<MonitorCounters>();
const samples = ref<MonitorSample[]>([]);
const sshMonitor = ref<SSHMonitor>();
const sshActionKey = ref("");
let refreshTimer: number | undefined;

const connected = computed(() => status.value?.state === "connected");
const filteredProcesses = computed(() => {
  const query = processSearch.value.trim().toLowerCase();
  if (!query) return processes.value;
  return processes.value.filter((item) =>
    `${item.pid} ${item.user} ${item.state} ${item.command}`.toLowerCase().includes(query),
  );
});
const primaryDisk = computed(() => snapshot.value?.disks[0]);
const memoryPercent = computed(() => clampPercent(snapshot.value?.memory.usedPercent || 0));
const diskPercent = computed(() => clampPercent(primaryDisk.value?.usedPercent || 0));
const latestSample = computed(() => samples.value.at(-1));
const cpuChartSeries = computed(() => [{ label: "CPU", color: "#79a8ef", values: samples.value.map((item) => item.cpu) }]);
const memoryChartSeries = computed(() => [{ label: "内存", color: "#73bd91", values: samples.value.map((item) => item.memory) }]);
const diskChartSeries = computed(() => [{ label: "磁盘", color: "#d0aa69", values: samples.value.map((item) => item.disk) }]);
const networkChartSeries = computed(() => [
  { label: "接收", color: "#70b6c7", values: samples.value.map((item) => item.received) },
  { label: "发送", color: "#d68a82", values: samples.value.map((item) => item.sent) },
]);

function clampPercent(value: number) {
  return Math.max(0, Math.min(100, value));
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** index;
  return `${amount >= 100 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
}

function formatUptime(value: number) {
  const seconds = Math.max(0, Math.floor(value || 0));
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days) return `${days} 天 ${hours} 小时`;
  if (hours) return `${hours} 小时 ${minutes} 分钟`;
  return `${minutes} 分钟`;
}

function formatTime(value?: string) {
  if (!value) return "--";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "--" : parsed.toLocaleTimeString();
}

function formatDateTime(value?: string) {
  if (!value) return "--";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}

function formatRate(value: number) {
  return `${formatBytes(value)}/s`;
}

function formatSessionDuration(value: string) {
  const started = new Date(value).getTime();
  if (!Number.isFinite(started)) return "--";
  const seconds = Math.max(0, Math.floor((Date.now() - started) / 1000));
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remaining = seconds % 60;
  if (days) return `${days} 天 ${hours} 小时`;
  if (hours) return `${hours} 小时 ${minutes} 分钟`;
  return `${minutes} 分 ${remaining} 秒`;
}

function canBanAddress(value: string) {
  return Boolean(value) && value !== "-" && value !== "localhost" && value !== "::1" && !value.startsWith("127.");
}

function progressStatus(value: number): "success" | "warning" | "exception" {
  if (value >= 90) return "exception";
  if (value >= 75) return "warning";
  return "success";
}

async function ensureConnected() {
  if (!props.host) throw new Error("当前终端没有可用的主机信息");
  status.value = await api<AgentStatus>(`/api/hosts/${props.host.id}/agent`);
  if (connected.value) return;
  status.value = await api<AgentStatus>(
    `/api/hosts/${props.host.id}/agent/connect`,
    { method: "POST", body: json({}) },
  );
}

async function command(commandText: string) {
  if (!props.host) throw new Error("当前终端没有可用的主机信息");
  const result = await api<{ output: string; success: boolean; error?: string }>(
    `/api/hosts/${props.host.id}/agent/command`,
    { method: "POST", body: json({ command: commandText }) },
  );
  if (!result.success) throw new Error(result.error || result.output || "监控命令执行失败");
  return result.output || "";
}

async function refresh(showMessage = false) {
  if (!props.host || refreshing.value) return;
  refreshing.value = true;
  error.value = "";
  try {
    await ensureConnected();
    const requests: Promise<unknown>[] = [
      api<AgentSnapshot>(`/api/hosts/${props.host.id}/agent/snapshot`),
      command(monitorCountersCommand),
    ];
    if (activeTab.value === "processes")
      requests.push(api<AgentProcess[]>(`/api/hosts/${props.host.id}/agent/processes`));
    if (activeTab.value === "ssh") requests.push(command(sshMonitorCommand));
    const results = await Promise.all(requests);
    const nextSnapshot = results[0] as AgentSnapshot;
    const nextCounters = parseMonitorCounters(results[1] as string);
    const rates = deriveCounterRates(counterSnapshot.value, nextCounters);
    counterSnapshot.value = nextCounters;
    snapshot.value = nextSnapshot;
    samples.value = [...samples.value, {
      collectedAt: nextCounters.collectedAt,
      cpu: rates.cpuPercent,
      memory: clampPercent(nextSnapshot.memory.usedPercent),
      disk: clampPercent(nextSnapshot.disks[0]?.usedPercent || 0),
      received: rates.receivedPerSecond,
      sent: rates.sentPerSecond,
    }].slice(-36);
    let resultIndex = 2;
    if (activeTab.value === "processes") {
      const nextProcesses = results[resultIndex++] as AgentProcess[];
      processes.value = Array.isArray(nextProcesses) ? nextProcesses : [];
    }
    if (activeTab.value === "ssh") sshMonitor.value = parseSSHMonitor(results[resultIndex] as string);
if (showMessage) ElMessage.success("主机状态已刷新");
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "主机状态获取失败";
  } finally {
    refreshing.value = false;
    initialLoading.value = false;
  }
}

async function terminateSSHSession(session: SSHMonitor["sessions"][number]) {
  if (!props.host || !session.pid) return;
  try {
    await ElMessageBox.confirm(
      `确定强制退出 ${session.user} 在 ${session.terminal} 上的 SSH 会话？`,
      "强制退出 SSH 会话",
      { confirmButtonText: "强制退出", cancelButtonText: "取消", type: "warning" },
    );
  } catch {
    return;
  }
  sshActionKey.value = `terminate:${session.terminal}:${session.pid}`;
  try {
    await api(`/api/hosts/${props.host.id}/agent/ssh-sessions/terminate`, {
      method: "POST",
      body: json({ pid: session.pid, terminal: session.terminal, user: session.user }),
    });
    ElMessage.success("SSH 会话已发送退出信号");
    await refresh();
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "强制退出失败");
  } finally {
    sshActionKey.value = "";
  }
}

async function banSSHAddress(address: string) {
  if (!props.host || !canBanAddress(address)) return;
  try {
    await ElMessageBox.confirm(
      `封禁来源 IP ${address} 后，远端防火墙将拒绝该地址的连接。确认继续？`,
      "封禁 SSH 来源 IP",
      { confirmButtonText: "封禁 IP", cancelButtonText: "取消", type: "warning" },
    );
  } catch {
    return;
  }
  sshActionKey.value = `ban:${address}`;
  try {
    await api(`/api/hosts/${props.host.id}/agent/ssh-sessions/ban`, {
      method: "POST",
      body: json({ address }),
    });
    ElMessage.success(`已提交封禁 ${address}`);
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "封禁 IP 失败");
  } finally {
    sshActionKey.value = "";
  }
}

async function unbanSSHAddress(address: string) {
  if (!props.host || !canBanAddress(address)) return;
  try {
    await ElMessageBox.confirm(
      `确定解除远端防火墙对 ${address} 的封禁？`,
      "解除 SSH 来源 IP 封禁",
      { confirmButtonText: "解除封禁", cancelButtonText: "取消", type: "info" },
    );
  } catch {
    return;
  }
  sshActionKey.value = `unban:${address}`;
  try {
    await api(`/api/hosts/${props.host.id}/agent/ssh-sessions/unban`, {
      method: "POST",
      body: json({ address }),
    });
    ElMessage.success(`已提交解除 ${address} 封禁`);
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "解除封禁失败");
  } finally {
    sshActionKey.value = "";
  }
}

function scheduleRefresh() {
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer);
  refreshTimer = undefined;
  if (!props.modelValue || !autoRefresh.value) return;
  refreshTimer = window.setInterval(() => void refresh(), refreshSeconds.value * 1000);
}

watch(
  () => props.modelValue,
  (open) => {
    scheduleRefresh();
    if (!open) return;
    status.value = undefined;
    snapshot.value = undefined;
    processes.value = [];
    activeTab.value = "basic";
    counterSnapshot.value = undefined;
    samples.value = [];
    sshMonitor.value = undefined;
    processSearch.value = "";
    error.value = "";
    initialLoading.value = true;
    void refresh();
  },
);

watch([autoRefresh, refreshSeconds], scheduleRefresh);
watch(activeTab, () => {
  if (props.modelValue) void refresh();
});
onBeforeUnmount(() => {
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer);
});
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="host-monitor-dialog"
    :title="`主机监控 · ${host?.name || '当前主机'}`"
    width="min(1060px, calc(100vw - 28px))"
    append-to-body
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="monitor-toolbar">
      <div class="monitor-connection" :class="{ connected }">
        <span></span>
        {{ connected ? '监控通道已连接' : status?.state === 'connecting' ? '正在连接' : '监控通道未连接' }}
      </div>
      <div class="monitor-actions">
        <label>自动刷新 <el-switch v-model="autoRefresh" /></label>
        <el-select v-model="refreshSeconds" class="monitor-interval" :disabled="!autoRefresh">
          <el-option :value="5" label="5 秒" />
          <el-option :value="10" label="10 秒" />
          <el-option :value="30" label="30 秒" />
        </el-select>
        <el-button :icon="RefreshCw" :loading="refreshing" @click="refresh(true)">刷新</el-button>
      </div>
    </div>

    <el-alert v-if="error" class="monitor-error" type="error" :closable="false" :title="error" />

    <div v-loading="initialLoading" class="monitor-body">
      <template v-if="snapshot">
        <section class="monitor-system-strip">
          <Server :size="19" />
          <div><strong>{{ snapshot.system.hostname || host?.address }}</strong><span>{{ snapshot.system.os }} · {{ snapshot.system.arch }} · {{ snapshot.system.kernel }}</span></div>
          <time>采集于 {{ formatTime(snapshot.collectedAt) }}</time>
        </section>

        <el-tabs v-model="activeTab" class="monitor-tabs">
          <el-tab-pane name="basic">
            <template #label><span class="monitor-tab-label"><Activity :size="14" />基础监控</span></template>
            <div class="monitor-metrics">
              <section class="monitor-metric">
                <Cpu :size="18" />
                <span>CPU</span>
                <strong>{{ latestSample ? `${latestSample.cpu.toFixed(1)}%` : '--' }}</strong>
                <small>{{ counterSnapshot?.cpuCores || '--' }} 核 · 负载 {{ snapshot.load1.toFixed(2) }} / {{ snapshot.load5.toFixed(2) }} / {{ snapshot.load15.toFixed(2) }}</small>
                <el-progress :percentage="latestSample?.cpu || 0" :status="progressStatus(latestSample?.cpu || 0)" :show-text="false" />
              </section>
              <section class="monitor-metric">
                <MemoryStick :size="18" />
                <span>内存</span>
                <strong>{{ memoryPercent.toFixed(1) }}%</strong>
                <small>{{ formatBytes(snapshot.memory.usedBytes) }} / {{ formatBytes(snapshot.memory.totalBytes) }}</small>
                <el-progress :percentage="memoryPercent" :status="progressStatus(memoryPercent)" :show-text="false" />
              </section>
              <section class="monitor-metric">
                <HardDrive :size="18" />
                <span>根分区</span>
                <strong>{{ diskPercent.toFixed(1) }}%</strong>
                <small>{{ formatBytes(primaryDisk?.usedBytes || 0) }} / {{ formatBytes(primaryDisk?.totalBytes || 0) }}</small>
                <el-progress :percentage="diskPercent" :status="progressStatus(diskPercent)" :show-text="false" />
              </section>
              <section class="monitor-metric">
                <Clock3 :size="18" />
                <span>运行时间</span>
                <strong>{{ formatUptime(snapshot.uptimeSeconds) }}</strong>
                <small>最近 {{ samples.length }} 个动态采样</small>
              </section>
            </div>

            <div class="monitor-charts">
              <MonitorSparkline title="CPU 使用率" :value="latestSample ? `${latestSample.cpu.toFixed(1)}%` : '--'" :detail="`${counterSnapshot?.cpuCores || '--'} 核`" :series="cpuChartSeries" :max="100" />
              <MonitorSparkline title="内存使用率" :value="`${memoryPercent.toFixed(1)}%`" :detail="`${formatBytes(snapshot.memory.usedBytes)} / ${formatBytes(snapshot.memory.totalBytes)}`" :series="memoryChartSeries" :max="100" />
              <MonitorSparkline title="磁盘使用率" :value="`${diskPercent.toFixed(1)}%`" :detail="primaryDisk?.path || '/'" :series="diskChartSeries" :max="100" />
              <MonitorSparkline title="网络吞吐" :value="`${formatRate(latestSample?.received || 0)} ↓`" :detail="`${formatRate(latestSample?.sent || 0)} ↑`" :series="networkChartSeries" />
            </div>

            <section class="monitor-section">
              <header><div><HardDrive :size="17" /><strong>磁盘</strong></div><span>{{ snapshot.disks.length }} 个挂载点</span></header>
              <div class="monitor-disks">
                <div v-for="disk in snapshot.disks" :key="disk.path" class="monitor-disk-row">
                  <code>{{ disk.path }}</code>
                  <span>{{ formatBytes(disk.usedBytes) }} / {{ formatBytes(disk.totalBytes) }}</span>
                  <el-progress :percentage="clampPercent(disk.usedPercent)" :status="progressStatus(disk.usedPercent)" :show-text="false" />
                  <strong>{{ clampPercent(disk.usedPercent).toFixed(1) }}%</strong>
                </div>
              </div>
            </section>
          </el-tab-pane>

          <el-tab-pane name="processes">
            <template #label><span class="monitor-tab-label"><Gauge :size="14" />进程</span></template>
            <section class="monitor-section monitor-processes">
              <header>
                <div><Activity :size="17" /><strong>进程列表</strong><span>{{ filteredProcesses.length }} / {{ processes.length }}</span></div>
                <el-input v-model="processSearch" class="monitor-process-search" :prefix-icon="Search" clearable placeholder="搜索 PID、用户或命令" />
              </header>
              <el-table :data="filteredProcesses" height="430" empty-text="暂无进程数据">
                <el-table-column prop="pid" label="PID" width="86" sortable />
                <el-table-column prop="user" label="用户" width="120" show-overflow-tooltip />
                <el-table-column prop="state" label="状态" width="78" />
                <el-table-column label="内存" width="112" sortable :sort-by="(row: AgentProcess) => row.memoryBytes">
                  <template #default="{ row }">{{ formatBytes(row.memoryBytes) }}</template>
                </el-table-column>
                <el-table-column prop="command" label="命令" min-width="380" show-overflow-tooltip>
                  <template #default="{ row }"><code class="monitor-command">{{ row.command }}</code></template>
                </el-table-column>
              </el-table>
            </section>
          </el-tab-pane>

          <el-tab-pane name="ssh">
            <template #label><span class="monitor-tab-label"><LogIn :size="14" />SSH 监控</span></template>
            <div class="ssh-summary">
              <section><ShieldCheck :size="18" /><span>成功登录</span><strong>{{ sshMonitor?.successful || 0 }}</strong><small>最近 24 小时</small></section>
              <section><ShieldX :size="18" /><span>失败登录</span><strong>{{ sshMonitor?.failed || 0 }}</strong><small>最近 24 小时</small></section>
              <section><Network :size="18" /><span>当前会话</span><strong>{{ sshMonitor?.activeSessions || 0 }}</strong><small>who 活跃记录</small></section>
            </div>
            <el-alert v-if="sshMonitor && !sshMonitor.available" type="warning" :closable="false" title="当前 SSH 账号无法读取认证日志，成功和失败次数可能不可用" />
            <section class="monitor-section monitor-ssh-sessions">
              <header><div><Network :size="17" /><strong>当前 SSH 会话</strong><span>{{ sshMonitor?.sessions.length || 0 }} 个</span></div><span>来自远端 who -u</span></header>
              <el-table :data="sshMonitor?.sessions || []" height="250" empty-text="暂无活动 SSH 会话">
                <el-table-column prop="user" label="用户" width="120" show-overflow-tooltip />
                <el-table-column prop="terminal" label="终端" width="100" />
                <el-table-column label="连接时间" width="170"><template #default="{ row }">{{ formatDateTime(row.loginTime) }}</template></el-table-column>
                <el-table-column label="连接时长" width="130"><template #default="{ row }">{{ formatSessionDuration(row.loginTime) }}</template></el-table-column>
                <el-table-column prop="address" label="来源 IP" min-width="150" show-overflow-tooltip>
                  <template #default="{ row }">{{ row.address || "未知" }}</template>
                </el-table-column>
                <el-table-column label="操作" width="270" fixed="right">
                  <template #default="{ row }">
                    <el-button text type="warning" :icon="LogOut" :loading="sshActionKey === `terminate:${row.terminal}:${row.pid}`" :disabled="!row.pid || Boolean(sshActionKey)" title="强制退出此 SSH 会话" @click="terminateSSHSession(row)">退出</el-button>
                    <el-button text type="danger" :icon="Ban" :loading="sshActionKey === `ban:${row.address}`" :disabled="!canBanAddress(row.address) || Boolean(sshActionKey)" title="在远端防火墙封禁此来源 IP" @click="banSSHAddress(row.address)">封禁 IP</el-button>
                    <el-button text :icon="ShieldCheck" :loading="sshActionKey === `unban:${row.address}`" :disabled="!canBanAddress(row.address) || Boolean(sshActionKey)" title="撤销远端防火墙对该来源 IP 的封禁" @click="unbanSSHAddress(row.address)">解封 IP</el-button>
                  </template>
                </el-table-column>
              </el-table>
            </section>
            <section class="monitor-section monitor-ssh-records">
              <header><div><LogIn :size="17" /><strong>登录记录</strong><span>{{ sshMonitor?.records.length || 0 }} 条 · {{ sshMonitor?.source || '日志不可用' }}</span></div></header>
              <el-table :data="sshMonitor?.records || []" height="390" empty-text="暂无 SSH 登录记录">
                <el-table-column label="结果" width="82">
                  <template #default="{ row }"><span class="ssh-result" :class="row.status">{{ row.status === 'success' ? '成功' : '失败' }}</span></template>
                </el-table-column>
                <el-table-column label="时间" width="170"><template #default="{ row }">{{ formatDateTime(row.time) }}</template></el-table-column>
                <el-table-column prop="user" label="用户" width="120" show-overflow-tooltip />
                <el-table-column prop="address" label="来源地址" width="150" show-overflow-tooltip />
                <el-table-column prop="method" label="方式" width="105" />
                <el-table-column prop="message" label="日志" min-width="320" show-overflow-tooltip />
              </el-table>
            </section>
          </el-tab-pane>
        </el-tabs>
      </template>
      <div v-else-if="!initialLoading && !error" class="monitor-empty"><Activity :size="28" />暂无监控数据</div>
    </div>
  </el-dialog>
</template>

<style scoped>
.host-monitor-dialog { color: var(--text); font-family: Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; letter-spacing: 0; }
.monitor-toolbar, .monitor-actions, .monitor-connection, .monitor-system-strip, .monitor-section > header, .monitor-section > header > div, .monitor-disk-row { display: flex; align-items: center; }
.monitor-toolbar { justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.monitor-connection { gap: 7px; color: var(--muted); font-size: 12px; }
.monitor-connection > span { width: 8px; height: 8px; border-radius: 50%; background: #a86460; }
.monitor-connection.connected > span { background: #66b786; box-shadow: 0 0 0 3px rgb(102 183 134 / 12%); }
.monitor-actions { flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
.monitor-actions label { display: inline-flex; align-items: center; gap: 7px; color: var(--muted); font-size: 12px; }
.monitor-interval { width: 92px; }
.monitor-error { margin-bottom: 10px; }
.monitor-body { min-height: 470px; }
.monitor-system-strip { gap: 10px; padding: 10px 12px; border-top: 1px solid var(--line-strong); border-bottom: 1px solid var(--line-strong); color: var(--muted); }
.monitor-system-strip > div { min-width: 0; flex: 1; display: grid; gap: 3px; }
.monitor-system-strip strong { color: var(--text); font-size: 14px; }
.monitor-system-strip span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font: 11px var(--font-mono, monospace); }
.monitor-system-strip time { flex: 0 0 auto; font-size: 11px; }
.monitor-tabs { margin-top: 4px; }
.monitor-tabs :deep(.el-tabs__item) { height: 44px; font-size: 12px; }
.monitor-tab-label { display: inline-flex; align-items: center; gap: 6px; }
.monitor-metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 9px; padding: 12px 0; }
.monitor-metric { min-width: 0; min-height: 125px; display: grid; grid-template-columns: auto 1fr; align-content: start; gap: 5px 8px; padding: 12px; border: 1px solid var(--line-strong); border-radius: 6px; background: var(--surface); color: var(--muted); }
.monitor-metric > span { font-size: 12px; }
.monitor-metric > strong { grid-column: 1 / -1; overflow-wrap: anywhere; color: var(--text); font-size: 21px; font-weight: 650; }
.monitor-metric > small { grid-column: 1 / -1; min-height: 17px; color: var(--muted); font-size: 11px; }
.monitor-metric :deep(.el-progress) { grid-column: 1 / -1; margin-top: 5px; }
.monitor-charts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 9px; }
.monitor-section { margin-top: 8px; border-top: 1px solid var(--line-strong); }
.monitor-section > header { min-height: 48px; justify-content: space-between; gap: 10px; }
.monitor-section > header > div { gap: 7px; }
.monitor-section > header strong { font-size: 13px; }
.monitor-section > header span { color: var(--muted); font-size: 11px; }
.monitor-disks { display: grid; gap: 6px; padding-bottom: 8px; }
.monitor-disk-row { display: grid; grid-template-columns: minmax(80px, 1fr) 190px minmax(160px, 2fr) 58px; gap: 10px; padding: 6px 8px; color: var(--muted); font-size: 11px; }
.monitor-disk-row code { overflow: hidden; text-overflow: ellipsis; color: var(--text); white-space: nowrap; }
.monitor-disk-row strong { text-align: right; color: var(--text); }
.monitor-process-search { width: min(310px, 40vw); }
.monitor-processes :deep(.el-table), .monitor-ssh-sessions :deep(.el-table), .monitor-ssh-records :deep(.el-table) { --el-table-bg-color: transparent; --el-table-tr-bg-color: transparent; --el-table-header-bg-color: #1b2125; --el-table-row-hover-bg-color: #202b31; --el-table-border-color: #303a34; color: var(--text); }
.monitor-command { font: 11px var(--font-mono, monospace); }
.ssh-summary { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 9px; padding: 12px 0; }
.ssh-summary section { min-width: 0; display: grid; grid-template-columns: auto 1fr; gap: 5px 8px; padding: 12px; border: 1px solid var(--line-strong); border-radius: 6px; background: var(--surface); color: var(--muted); }
.ssh-summary section > span { font-size: 12px; }
.ssh-summary section > strong { grid-column: 1 / -1; color: var(--text); font-size: 24px; }
.ssh-summary section > small { grid-column: 1 / -1; color: var(--muted); font-size: 11px; }
.ssh-result { display: inline-flex; min-width: 40px; justify-content: center; padding: 2px 6px; border-radius: 3px; font-size: 11px; }
.ssh-result.success { background: #173324; color: #91cca5; }
.ssh-result.failed { background: #3a2023; color: #dfa0a5; }
.monitor-empty { min-height: 430px; display: grid; place-items: center; align-content: center; gap: 8px; color: var(--muted); font-size: 12px; }
@media (max-width: 820px) {
  .monitor-toolbar { align-items: stretch; flex-direction: column; }
  .monitor-actions { justify-content: flex-start; }
  .monitor-metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .monitor-charts { grid-template-columns: 1fr; }
}
@media (max-width: 560px) {
  .monitor-system-strip { align-items: flex-start; flex-wrap: wrap; }
  .monitor-system-strip time { flex-basis: 100%; padding-left: 29px; }
  .monitor-metrics { grid-template-columns: 1fr; }
  .ssh-summary { grid-template-columns: 1fr; }
  .monitor-metric { min-height: 105px; }
  .monitor-section > header { align-items: stretch; flex-direction: column; padding: 9px 0; }
  .monitor-process-search { width: 100%; }
  .monitor-disk-row { grid-template-columns: 1fr auto; }
  .monitor-disk-row .el-progress { grid-column: 1 / -1; grid-row: 2; }
}
</style>
