<script setup lang="ts">
import { useAttrs } from "vue";
import { useDialogReturnFocus } from "../composables/useDialogReturnFocus";
import { useDialogGeometry } from "../dialogGeometry";
import { ElDialog } from "element-plus";
import { X } from "@lucide/vue";
defineOptions({ inheritAttrs: false });
const props = defineProps<{ modelValue: boolean; embedded?: boolean; title?: string; beforeClose?: (done: () => void) => unknown }>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();
const attrs = useAttrs();
const { restoreFocus } = useDialogReturnFocus(() => props.modelValue && !props.embedded);
const { apply: applyGeometry } = useDialogGeometry(() => props.modelValue && !props.embedded, () => String(attrs.class || "").split(" ")[0] || "");
function close() {
  const done = () => emit("update:modelValue", false);
  if (props.beforeClose) void props.beforeClose(done); else done();
}
</script>
<template>
  <ElDialog v-if="!embedded" v-bind="$attrs" draggable :close-on-click-modal="false" @close-auto-focus="restoreFocus" @opened="applyGeometry" :model-value="modelValue" :title="title" :before-close="beforeClose" @update:model-value="emit('update:modelValue', $event)">
    <slot /><template v-if="$slots.footer" #footer><slot name="footer" /></template>
  </ElDialog>
  <section v-else-if="modelValue" v-bind="$attrs" class="git-inline-surface" :aria-label="title || 'Git 工作台'">
    <header v-if="title" class="git-inline-heading"><strong>{{ title }}</strong><button aria-label="返回 Git 工作台" @click="close"><X :size="16" /></button></header>
    <div class="git-inline-body"><slot /></div>
    <footer v-if="$slots.footer" class="git-inline-footer"><slot name="footer" /></footer>
  </section>
</template>
<style scoped>
.git-inline-surface { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; overflow: hidden; background: var(--bg); color: var(--text); }
.git-inline-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-shrink: 0; min-height: 38px; padding: 4px 14px; border-bottom: 1px solid var(--line); }
.git-inline-heading strong { font-size: 12px; font-weight: 500; }
.git-inline-body { flex: 1; min-height: 0; overflow: auto; padding: 16px 18px; }
.git-inline-footer { display: flex; flex-shrink: 0; justify-content: flex-end; flex-wrap: wrap; gap: 8px; padding: 9px 14px; border-top: 1px solid var(--line); }
.git-inline-footer :deep(.el-button + .el-button) { margin-left: 0; }
</style>

<style>
.el-dialog.git-dialog, .el-dialog.git-full-compare-dialog { resize: none; overflow: hidden; display: flex; flex-direction: column; min-width: min(340px, calc(100vw - 24px)); min-height: 260px; max-width: calc(100vw - 24px); max-height: 92dvh; }
.el-dialog.git-dialog > .el-dialog__body, .el-dialog.git-full-compare-dialog > .el-dialog__body { flex: 1; min-height: 0; overflow: auto; }
.el-dialog.git-dialog > .el-dialog__body { padding: 22px 24px 24px !important; }
.el-dialog.git-full-compare-dialog .git-full-compare-content { max-height: 100%; }
@media (max-width: 720px) {
  .el-dialog.git-dialog > .el-dialog__body { padding: 16px !important; }
}
</style>
