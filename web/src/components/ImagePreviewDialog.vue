<script setup lang="ts">
import { downloadToolFile } from "../utils/toolDownloads";
import { computed, ref, watch } from "vue";
import { Download, Image as ImageIcon } from "@lucide/vue";
import type { Host } from "../types";

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  sessionId?: string;
  file?: { name: string; path: string; size?: number };
}>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();
const loaded = ref(false);
const failed = ref(false);

const source = computed(() => {
  if (!props.host || !props.file) return "";
  const session = props.sessionId
    ? `&session=${encodeURIComponent(props.sessionId)}`
    : "";
  return `/api/sftp/${props.host.id}/preview-image?path=${encodeURIComponent(props.file.path)}${session}`;
});

watch(
  () => [props.modelValue, props.file?.path],
  () => {
    loaded.value = false;
    failed.value = false;
  },
);

function download() {
  if (!props.host || !props.file) return;
  const session = props.sessionId
    ? `&session=${encodeURIComponent(props.sessionId)}`
    : "";
  downloadToolFile(`/api/sftp/${props.host.id}/download?path=${encodeURIComponent(props.file.path)}${session}`);
}

function formatBytes(size?: number) {
  if (size == null) return "";
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="image-preview-dialog"
    width="min(980px, calc(100vw - 24px))"
    append-to-body
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div class="image-preview-heading">
        <strong>{{ file?.name || "图片预览" }}</strong>
        <small>
          {{ file?.path }}<template v-if="file?.size"> · {{ formatBytes(file.size) }}</template>
        </small>
      </div>
    </template>
    <div class="image-preview-stage" :class="{ loaded, failed }">
      <ImageIcon v-if="failed" :size="34" />
      <span v-if="failed">图片加载失败或格式不受支持</span>
      <span v-else-if="!loaded">正在加载图片…</span>
      <img
        v-if="source && !failed"
        :src="source"
        :alt="file?.name || '图片预览'"
        @load="loaded = true"
        @error="failed = true"
      />
    </div>
    <template #footer>
      <el-button :icon="Download" @click="download">下载</el-button>
    </template>
  </el-dialog>
</template>
