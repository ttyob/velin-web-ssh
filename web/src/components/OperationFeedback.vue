<script setup lang="ts">
import { computed } from 'vue';
import { CircleAlert, LoaderCircle, X } from '@lucide/vue';
import { operationHint } from '../operationFeedback';
const props = defineProps<{ pending?: string; error?: string; hint?: string; retryLabel?: string }>();
defineEmits<{ retry: []; dismiss: [] }>();
const guidance = computed(() => props.hint || operationHint(props.error || ''));
</script>
<template>
  <div v-if="pending || error" class="operation-feedback" :class="{ 'is-error': !pending && error }" role="status" aria-live="polite">
    <LoaderCircle v-if="pending" class="operation-spinner" :size="14" /><CircleAlert v-else :size="14" />
    <div><span>{{ pending || '操作未完成' }}</span><template v-if="!pending && error"><p>{{ error }}</p><small>{{ guidance }}</small><button v-if="retryLabel" type="button" @click="$emit('retry')">{{ retryLabel }}</button></template></div>
    <button v-if="!pending" class="operation-dismiss" type="button" aria-label="收起操作提示" @click="$emit('dismiss')"><X :size="13" /></button>
  </div>
</template>
<style>
.operation-feedback { display: flex; align-items: flex-start; gap: 7px; padding: 8px 10px; flex-shrink: 0; color: var(--muted); background: var(--surface-2); border-left: 2px solid var(--accent); font-size: 12px; overflow-wrap: anywhere; }
.operation-feedback > svg { flex-shrink: 0; margin-top: 2px; }
.operation-feedback > div { min-width: 0; flex: 1; }
.operation-feedback p { margin: 4px 0; white-space: pre-wrap; max-height: 140px; overflow: auto; color: var(--text); }
.operation-feedback small { display: block; color: var(--muted); }
.operation-feedback button { border: 0; padding: 3px 0; background: transparent; color: var(--accent); cursor: pointer; font: inherit; }
.operation-feedback .operation-dismiss { padding: 0; color: var(--muted); }
.operation-feedback.is-error { border-left-color: var(--danger); }
.operation-feedback.is-error > svg { color: var(--danger); }
.operation-spinner { animation: operation-spin 1s linear infinite; }
@keyframes operation-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .operation-spinner { animation: none; } }
</style>
