<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Download, File, Image as ImageIcon } from "@lucide/vue";
import { canLoadDiff, type DiffFilePreview } from "../codeDiff";
import { downloadToolFile } from "../utils/toolDownloads";
const props = defineProps<{ preview: DiffFilePreview; leftLabel?: string; rightLabel?: string }>();
defineEmits<{ load: [] }>();
const failed = ref<string[]>([]);
watch(() => props.preview, () => { failed.value = []; });
const sides = computed(() => [{ ...props.preview.left, key: "left", label: props.leftLabel || "提交版本" }, { ...props.preview.right, key: "right", label: props.rightLabel || "当前文件" }]);
const loadable = computed(() => canLoadDiff(props.preview));
const title = computed(() => loadable.value ? "较大的文本文件" : sides.value.some(side => side.kind === "image") ? "图片变更" : "文件信息");
function bytes(size: number) { return size >= 1024 * 1024 ? `${(size / 1024 / 1024).toFixed(1)} MiB` : size >= 1024 ? `${(size / 1024).toFixed(1)} KiB` : `${size} B`; }
</script>
<template>
  <section class="diff-file-preview" aria-label="文件预览">
    <header><strong>{{ title }}</strong><p>{{ loadable ? '为保持流畅，确认后再加载完整代码对比。' : '图片直接预览；二进制文件与超过 2 MiB 的文本可下载到本地查看。' }}</p></header>
    <div class="diff-preview-sides">
      <article v-for="side in sides" :key="side.key">
        <div class="diff-preview-label"><span>{{ side.label }}</span><small>{{ side.exists ? bytes(side.size) : '不存在' }}</small></div>
        <div class="diff-preview-media">
          <img v-if="side.exists && side.kind === 'image' && side.size <= 32 * 1024 * 1024 && side.imageURL && !failed.includes(side.key)" :src="side.imageURL" :alt="side.label" @error="failed.push(side.key)" />
          <template v-else><component :is="side.kind === 'image' ? ImageIcon : File" :size="28" /><span>{{ !side.exists ? '此版本没有该文件' : failed.includes(side.key) ? '图片加载失败，可下载查看' : side.kind === 'image' ? '图片超过 32 MiB 预览上限' : side.kind === 'binary' ? '二进制文件' : side.kind === 'unsupported' ? '符号链接或非普通文件' : side.size > 2 * 1024 * 1024 ? '超过 2 MiB 在线对比上限' : '文本文件' }}</span></template>
        </div>
        <button v-if="side.exists && side.kind !== 'unsupported' && side.downloadURL" @click="downloadToolFile(side.downloadURL)"><Download :size="14" />下载当前文件</button>
      </article>
    </div>
    <button v-if="loadable" class="diff-preview-load" @click="$emit('load')">加载完整对比</button>
  </section>
</template>
<style scoped>
.diff-file-preview { height: 100%; overflow: auto; padding: 22px; color: var(--text); }
.diff-file-preview header strong { font-size: 13px; font-weight: 500; }
.diff-file-preview header p { color: var(--muted); font-size: 12px; line-height: 1.6; margin: 8px 0 20px; }
.diff-preview-sides { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 20px; }
.diff-preview-label { display: flex; gap: 10px; justify-content: space-between; color: var(--muted); font-size: 11px; padding-bottom: 8px; border-bottom: 1px solid var(--line); }
.diff-preview-label small { white-space: nowrap; font: inherit; }
.diff-preview-media { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 180px; padding: 20px 0; color: var(--muted); font-size: 12px; }
.diff-preview-media img { max-width: 100%; max-height: 52vh; object-fit: contain; background: repeating-conic-gradient(#8881 0% 25%, transparent 0% 50%) 0 0 / 16px 16px; }
.diff-file-preview button { display: inline-flex; gap: 6px; align-items: center; padding: 6px 10px; border: 1px solid var(--line); border-radius: 3px; background: var(--surface); color: var(--text); font: inherit; font-size: 12px; cursor: pointer; }
.diff-file-preview button:hover { background: var(--surface-2); }
.diff-preview-load { margin-top: 20px; }
@media (max-width: 640px) { .diff-file-preview { padding: 14px; } .diff-preview-sides { grid-template-columns: minmax(0, 1fr); gap: 14px; } }
</style>
