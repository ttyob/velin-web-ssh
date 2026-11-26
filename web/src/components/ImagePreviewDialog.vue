<script setup lang="ts">
import { downloadToolFile } from "../utils/toolDownloads";
import { computed, ref, watch } from "vue";
import { Download, Image as ImageIcon } from "@lucide/vue";
import type { Host } from "../types";

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  hostId?: string;
  sessionId?: string;
  source?: string;
  file?: { name: string; path: string; size?: number };
}>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();
const loaded = ref(false);
const failed = ref(false);
const dimensions = ref<{ width: number; height: number }>();

const hostID = computed(() => props.host?.id || props.hostId || "");

const source = computed(() => {
  if (props.source) return props.source;
  if (!hostID.value || !props.file) return "";
  const session = props.sessionId
    ? `&session=${encodeURIComponent(props.sessionId)}`
    : "";
  return `/api/sftp/${hostID.value}/preview-image?path=${encodeURIComponent(props.file.path)}${session}`;
});

watch(
  () => [props.modelValue, props.file?.path],
  () => {
    loaded.value = false;
    failed.value = false;
    dimensions.value = undefined;
  },
);

function download() {
  if (!hostID.value || !props.file) return;
  const session = props.sessionId
    ? `&session=${encodeURIComponent(props.sessionId)}`
    : "";
  downloadToolFile(`/api/sftp/${hostID.value}/download?path=${encodeURIComponent(props.file.path)}${session}`);
}

function handleLoad(event: Event) {
  const image = event.currentTarget as HTMLImageElement;
  loaded.value = true;
  dimensions.value = { width: image.naturalWidth, height: image.naturalHeight };
}

function formatType() {
  const name = props.file?.name || source.value;
  const extension = name.split(/[?#]/, 1)[0]?.split(".").pop();
  return extension ? extension.toUpperCase() : "图片";
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
          {{ formatType() }}<template v-if="dimensions"> · {{ dimensions.width }} × {{ dimensions.height }}</template><template v-if="file?.size"> · {{ formatBytes(file.size) }}</template>
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
        @load="handleLoad"
        @error="failed = true"
      />
    </div>
    <template #footer>
      <el-button :icon="Download" @click="download">下载</el-button>
    </template>
  </el-dialog>
</template>
