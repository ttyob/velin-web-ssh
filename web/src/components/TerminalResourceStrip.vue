<script setup lang="ts">
import { computed } from 'vue';
import { useTerminalResources } from '../terminalResources';
const props = defineProps<{ hostID: string; sessionID: string; active: boolean }>();
const state = useTerminalResources(() => props.hostID, () => props.sessionID, () => props.active);
type ResourceLevel = 'healthy' | 'warning' | 'critical' | 'unavailable';
function level(value: number | undefined, warning: number, critical: number): ResourceLevel {
  if (value === undefined || !Number.isFinite(value)) return 'unavailable';
  if (value >= critical) return 'critical';
  if (value >= warning) return 'warning';
  return 'healthy';
}
const metrics = computed(() => [
  { label: '内存', value: state.value?.snapshot?.memory?.usedPercent, warning: 70, critical: 90 },
  { label: 'CPU', value: state.value?.cpu, warning: 70, critical: 90 },
  { label: '磁盘', value: state.value?.snapshot?.disk?.usedPercent, warning: 80, critical: 95 },
].map(metric => ({ ...metric, level: level(metric.value, metric.warning, metric.critical) })));
function percent(value?: number) { return value !== undefined && Number.isFinite(value) ? `${Math.max(0, Math.min(100, value)).toFixed(0)}%` : '--'; }
const levelLabel: Record<ResourceLevel, string> = { healthy: '正常', warning: '警告', critical: '报警', unavailable: '不可用' };
const description = computed(() => metrics.value.map(metric => `${metric.label} ${percent(metric.value)} ${levelLabel[metric.level]}`).join('，') + (state.value?.unavailable ? '，资源数据暂不可用' : '，磁盘为根分区'));
</script>
<template><div class="terminal-resource-strip" role="group" aria-label="主机资源" :title="description"><span v-for="metric in metrics" :key="metric.label" :class="`is-${metric.level}`" :data-state="levelLabel[metric.level]"><small>{{ metric.label }}</small><b>{{ percent(metric.value) }}</b><i :style="{ '--resource-used': `${metric.value ?? 0}%` }" /></span></div></template>
<style scoped>
.terminal-resource-strip { position: absolute; top: 5px; right: 13px; z-index: 4; display: flex; gap: 10px; max-width: calc(100% - 24px); padding: 5px 8px; border: 1px solid color-mix(in srgb, var(--line) 65%, transparent); border-radius: 3px; background: color-mix(in srgb, var(--surface) 72%, transparent); backdrop-filter: blur(4px); color: var(--text); pointer-events: none; user-select: none; }
.terminal-resource-strip > span { --resource-tone: var(--icon-positive); display: grid; grid-template-columns: auto 4ch; gap: 3px 5px; align-items: center; min-width: 0; }
.terminal-resource-strip > span.is-warning { --resource-tone: var(--icon-warm); }
.terminal-resource-strip > span.is-critical { --resource-tone: var(--icon-danger); }
.terminal-resource-strip > span.is-unavailable { --resource-tone: var(--muted); }
.terminal-resource-strip small { color: var(--muted); font: 10px/1.3 var(--font-ui, sans-serif); white-space: nowrap; }
.terminal-resource-strip b { color: var(--resource-tone); text-align: right; font: 10px/1.3 var(--font-mono, monospace); font-variant-numeric: tabular-nums; }
.terminal-resource-strip i { grid-column: 1 / -1; height: 2px; border-radius: 1px; background: linear-gradient(to right, var(--resource-tone) var(--resource-used), color-mix(in srgb, var(--resource-tone) 20%, transparent) var(--resource-used)); opacity: .8; }
.terminal-resource-strip .is-unavailable i { opacity: .25; }
@media (max-width: 640px) { .terminal-resource-strip { gap: 7px; padding: 4px 6px; } }
</style>
