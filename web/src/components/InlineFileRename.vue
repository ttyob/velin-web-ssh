<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { Check, X } from '@lucide/vue';
const props = defineProps<{ name: string; directory?: boolean; busy?: boolean; error?: string }>();
defineEmits<{ confirm: [name: string]; cancel: [] }>();
const value = ref(props.name), input = ref<HTMLInputElement>();
onMounted(() => { input.value?.focus({ preventScroll: true }); const dot = props.directory ? -1 : props.name.lastIndexOf('.'); input.value?.setSelectionRange(0, dot > 0 ? dot : props.name.length); });
</script>
<template>
  <form class="inline-file-rename" :title="error" @submit.prevent="$emit('confirm', value)" @click.stop @dblclick.stop @pointerdown.stop @contextmenu.stop @keydown.stop @keydown.esc.prevent="!busy && $emit('cancel')" @dragstart.prevent.stop>
    <input ref="input" v-model="value" aria-label="文件新名称" :aria-invalid="!!error" :disabled="busy" :title="error || 'Enter 确认，Esc 取消'" />
    <button type="submit" aria-label="确认重命名" :disabled="busy"><Check :size="12" /></button><button type="button" aria-label="取消重命名" :disabled="busy" @click="$emit('cancel')"><X :size="12" /></button>
  </form>
</template>
<style>
.inline-file-rename { display: inline-flex; align-items: center; gap: 2px; flex: 1; min-width: 0; }
.inline-file-rename input { width: 100%; flex: 1; min-width: 25px; border: 1px solid var(--accent); border-radius: 2px; outline: none; padding: 2px 4px; color: var(--text); background: var(--bg); font: inherit; }
.inline-file-rename input[aria-invalid='true'] { border-color: var(--danger); }
.inline-file-rename button { display: inline-flex; align-items: center; justify-content: center; width: 20px; min-height: 20px; padding: 0 !important; border: 0; background: transparent; color: var(--muted); cursor: pointer; }
</style>
