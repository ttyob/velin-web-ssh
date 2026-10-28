<script setup lang="ts">
import { computed } from "vue";

type ChartSeries = {
  label: string;
  color: string;
  values: number[];
};

const props = defineProps<{
  title: string;
  value: string;
  detail?: string;
  series: ChartSeries[];
  max?: number;
}>();

const scaleMax = computed(() => {
  if (props.max && props.max > 0) return props.max;
  const largest = Math.max(0, ...props.series.flatMap((item) => item.values));
  return Math.max(1, largest * 1.15);
});

function points(values: number[]) {
  if (!values.length) return "";
  return values.map((value, index) => {
    const x = values.length === 1 ? 100 : (index / (values.length - 1)) * 100;
    const y = 38 - Math.max(0, Math.min(1, value / scaleMax.value)) * 34;
    return `${x.toFixed(2)},${y.toFixed(2)}`;
  }).join(" ");
}
</script>

<template>
  <section class="monitor-chart">
    <header><span>{{ title }}</span><strong>{{ value }}</strong></header>
    <svg viewBox="0 0 100 40" preserveAspectRatio="none" role="img" :aria-label="`${title}动态曲线`">
      <line v-for="line in [12, 24, 36]" :key="line" x1="0" :y1="line" x2="100" :y2="line" />
      <polyline v-for="item in series" :key="item.label" :points="points(item.values)" :style="{ stroke: item.color }" />
    </svg>
    <footer>
      <div class="monitor-chart-legend">
        <span v-for="item in series" :key="item.label"><i :style="{ background: item.color }"></i>{{ item.label }}</span>
      </div>
      <small>{{ detail }}</small>
    </footer>
  </section>
</template>

<style scoped>
.monitor-chart { min-width: 0; display: grid; gap: 7px; padding: 11px 12px; border: 1px solid var(--line-strong); border-radius: 6px; background: var(--surface); }
.monitor-chart header, .monitor-chart footer, .monitor-chart-legend, .monitor-chart-legend span { display: flex; align-items: center; }
.monitor-chart header { justify-content: space-between; gap: 8px; }
.monitor-chart header span { color: var(--muted); font-size: 12px; }
.monitor-chart header strong { color: var(--text); font-size: 17px; }
.monitor-chart svg { width: 100%; height: 112px; overflow: visible; }
.monitor-chart svg line { stroke: #2b3430; stroke-width: .35; vector-effect: non-scaling-stroke; }
.monitor-chart svg polyline { fill: none; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; vector-effect: non-scaling-stroke; }
.monitor-chart footer { min-height: 18px; justify-content: space-between; gap: 8px; }
.monitor-chart-legend { flex-wrap: wrap; gap: 9px; }
.monitor-chart-legend span { gap: 4px; color: var(--muted); font-size: 10px; }
.monitor-chart-legend i { width: 7px; height: 7px; border-radius: 2px; }
.monitor-chart footer small { overflow: hidden; color: var(--muted); font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
</style>
