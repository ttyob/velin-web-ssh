<script setup lang="ts">
import { downloadToolFile } from "../utils/toolDownloads";
import { computed, defineAsyncComponent, ref, watch } from "vue";
import {
  Download,
  Eye,
  EyeOff,
  File,
  FilePenLine,
  Folder,
  FolderPlus,
  MoreVertical,
  Pencil,
  RefreshCw,
  Trash2,
  Upload,
} from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, csrfHeaders, json } from "../api";
import type { Host } from "../types";

const TextFileEditorDialog = defineAsyncComponent(
  () => import("./TextFileEditorDialog.vue"),
);
const ImagePreviewDialog = defineAsyncComponent(
  () => import("./ImagePreviewDialog.vue"),
);

interface FileEntry {
  name: string;
  path: string;
  size: number;
  mode: string;
  directory: boolean;
  symlink: boolean;
  modifiedAt: string;
}

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  sessionId?: string;
  initialPath?: string;
}>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();
const currentPath = ref(".");
const entries = ref<FileEntry[]>([]);
const showHidden = ref(false);
const loading = ref(false);
const uploadQueue = ref<Array<{ name: string; progress: number; status: string }>>([]);
const uploading = ref(false);
const fileInput = ref<HTMLInputElement>();
const editorOpen = ref(false);
const editingFile = ref<FileEntry>();
const editorMode = ref<"edit" | "preview">("edit");
const imagePreviewOpen = ref(false);
const previewingImage = ref<FileEntry>();
const sessionQuery = computed(() =>
  props.sessionId ? `&session=${encodeURIComponent(props.sessionId)}` : "",
);

const shownEntries = computed(() =>
  entries.value
    .filter((item) => showHidden.value || !item.name.startsWith("."))
    .sort(
      (a, b) =>
        Number(b.directory) - Number(a.directory) ||
        a.name.localeCompare(b.name),
    ),
);

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return;
    currentPath.value =
      props.initialPath?.trim() || props.host?.initialDirectory || ".";
    entries.value = [];
    listFiles(currentPath.value);
  },
);

async function listFiles(next = currentPath.value) {
  if (!props.host) return;
  loading.value = true;
  try {
    const result = await api<{ path: string; entries: FileEntry[] }>(
      `/api/sftp/${props.host.id}/list?path=${encodeURIComponent(next)}${sessionQuery.value}`,
    );
    currentPath.value = result.path;
    entries.value = result.entries;
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "目录读取失败");
  } finally {
    loading.value = false;
  }
}

function parentPath() {
  const parts = currentPath.value.replace(/\/+$/, "").split("/");
  parts.pop();
  return parts.join("/") || "/";
}

async function mkdir() {
  if (!props.host) return;
  try {
    const { value } = await ElMessageBox.prompt("输入新目录名称", "新建目录", {
      inputValidator: (name) =>
        (Boolean(name) && !name.includes("/")) || "名称不能为空且不能包含 /",
    });
    await api(
      `/api/sftp/${props.host.id}/mkdir?session=${encodeURIComponent(props.sessionId || "")}`,
      {
        method: "POST",
        body: json({ path: `${currentPath.value}/${value}` }),
      },
    );
    await listFiles();
  } catch {}
}

async function renameEntry(entry: FileEntry) {
  if (!props.host) return;
  try {
    const { value } = await ElMessageBox.prompt("输入新名称", "重命名", {
      inputValue: entry.name,
      inputValidator: (name) =>
        (Boolean(name) && !name.includes("/")) || "名称不能为空且不能包含 /",
    });
    await api(
      `/api/sftp/${props.host.id}/rename?session=${encodeURIComponent(props.sessionId || "")}`,
      {
        method: "POST",
        body: json({
          source: entry.path,
          destination: `${currentPath.value}/${value}`,
        }),
      },
    );
    await listFiles();
  } catch {}
}

async function deleteEntry(entry: FileEntry) {
  if (!props.host) return;
  try {
    await ElMessageBox.confirm(
      `删除“${entry.name}”${entry.directory ? "及其全部内容" : ""}？`,
      "删除远程文件",
      { type: "warning" },
    );
    await api(
      `/api/sftp/${props.host.id}/delete?session=${encodeURIComponent(props.sessionId || "")}`,
      {
        method: "POST",
        body: json({ path: entry.path, recursive: entry.directory }),
      },
    );
    await listFiles();
  } catch (error: any) {
    if (error !== "cancel" && error !== "close")
      ElMessage.error(error instanceof Error ? error.message : "删除失败");
  }
}

function downloadEntry(entry: FileEntry) {
  if (!props.host) return;
  downloadToolFile(`/api/sftp/${props.host.id}/download?path=${encodeURIComponent(entry.path)}${sessionQuery.value}`);
}

const editableExtensions = new Set([
  "txt", "text", "md", "markdown", "json", "jsonc", "ini", "env",
  "properties", "conf", "cfg", "yaml", "yml", "xml", "toml", "csv",
  "log", "sh", "bash", "zsh", "js", "mjs", "cjs", "ts", "tsx", "jsx",
  "css", "scss", "less", "html", "htm", "vue", "go", "py", "rb", "php",
  "java", "c", "h", "cpp", "hpp", "rs", "sql",
]);
const imageExtensions = new Set([
  "avif", "bmp", "gif", "jpe", "jpeg", "jpg", "png", "webp",
]);

function isEditable(entry: FileEntry) {
  if (entry.directory || entry.symlink || entry.size > 2 * 1024 * 1024) return false;
  const lower = entry.name.toLowerCase();
  if (lower === ".env" || lower.startsWith(".env.")) return true;
  const dot = lower.lastIndexOf(".");
  return dot >= 0 && editableExtensions.has(lower.slice(dot + 1));
}

function isMarkdown(entry: FileEntry) {
  const lower = entry.name.toLowerCase();
  return !entry.directory && !entry.symlink &&
    (lower.endsWith(".md") || lower.endsWith(".markdown"));
}

function isImage(entry: FileEntry) {
  if (entry.directory || entry.symlink || entry.size <= 0 || entry.size > 32 * 1024 * 1024)
    return false;
  const dot = entry.name.lastIndexOf(".");
  return dot >= 0 && imageExtensions.has(entry.name.toLowerCase().slice(dot + 1));
}

function editEntry(entry: FileEntry, mode: "edit" | "preview" = "edit") {
  editingFile.value = entry;
  editorMode.value = mode;
  editorOpen.value = true;
}

function previewImage(entry: FileEntry) {
  previewingImage.value = entry;
  imagePreviewOpen.value = true;
}

function activateEntry(entry: FileEntry) {
  if (entry.directory) listFiles(entry.path);
  else if (isEditable(entry)) editEntry(entry);
  else if (isImage(entry)) previewImage(entry);
}

function activateRow(entry: FileEntry, event: MouseEvent, doubleClick = false) {
  if ((event.target as HTMLElement).closest(".file-row-action-zone")) return;
  if (entry.directory && !doubleClick) listFiles(entry.path);
  else if (!entry.directory && doubleClick) activateEntry(entry);
}

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`;
  const units = ["KB", "MB", "GB"];
  let value = size / 1024;
  let unit = units[0];
  for (let i = 1; value >= 1024 && i < units.length; i += 1) {
    value /= 1024;
    unit = units[i];
  }
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)} ${unit}`;
}

function uploadChunk(pathname: string, file: Blob, offset: number, overwrite: boolean) {
	return new Promise<number>((resolve, reject) => {
		const request = new XMLHttpRequest();
		request.open("PUT", `/api/sftp/${props.host!.id}/upload?path=${encodeURIComponent(pathname)}&offset=${offset}&overwrite=${overwrite}${sessionQuery.value}`);
		void csrfHeaders().then((headers) => { Object.entries(headers).forEach(([key, value]) => request.setRequestHeader(key, value)); request.send(file); }).catch(reject);
		request.onload = () => {
			if (request.status < 200 || request.status >= 300) { reject(new Error("分块上传失败")); return; }
			try { resolve(Number(JSON.parse(request.responseText).offset) || offset + file.size); } catch { resolve(offset + file.size); }
		};
		request.onerror = () => reject(new Error("网络连接中断"));
	});
}

async function uploadOne(file: File, queueItem: { name: string; progress: number; status: string }) {
	if (!props.host) return;
	const target = `${currentPath.value}/${file.name}`;
	let offset = 0;
	let overwrite = false;
	const status = await api<{ size: number; exists: boolean }>(`/api/sftp/${props.host.id}/transfer-status?path=${encodeURIComponent(target)}${sessionQuery.value}`);
	if (status.exists && status.size >= file.size) {
		await ElMessageBox.confirm("同名远程文件已存在，是否覆盖？", "覆盖文件", { type: "warning" });
		offset = 0; overwrite = true;
	} else if (status.exists) {
		offset = status.size; overwrite = true; queueItem.status = `续传 ${Math.floor(offset / file.size * 100)}%`;
	}
	const chunkSize = 8 * 1024 * 1024;
	while (offset < file.size) {
		const end = Math.min(file.size, offset + chunkSize);
		offset = await uploadChunk(target, file.slice(offset, end), offset, overwrite);
		queueItem.progress = Math.min(100, Math.round(offset / file.size * 100));
		queueItem.status = `${queueItem.progress}%`;
	}
	queueItem.progress = 100; queueItem.status = "完成";
}

async function uploadFile(event: Event) {
	if (!props.host) return;
	const files = Array.from((event.target as HTMLInputElement).files || []);
	if (!files.length) return;
	uploading.value = true;
	uploadQueue.value = files.map((file) => ({ name: file.name, progress: 0, status: "等待" }));
	try {
		for (let index = 0; index < files.length; index++) {
			try { await uploadOne(files[index], uploadQueue.value[index]); }
			catch (error) { uploadQueue.value[index].status = "失败"; ElMessage.error(`${files[index].name}：${error instanceof Error ? error.message : "上传失败"}`); }
		}
		await listFiles();
	} finally {
		uploading.value = false;
		if (fileInput.value) fileInput.value.value = "";
	}
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="file-manager-dialog"
    :title="`文件管理 · ${host?.name || '当前终端'}`"
    width="min(940px, calc(100vw - 28px))"
    append-to-body
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="file-manager-toolbar">
      <el-input v-model="currentPath" @keyup.enter="listFiles()" />
      <el-tooltip content="刷新">
        <el-button
          class="file-refresh"
          :icon="RefreshCw"
          :loading="loading"
          @click="listFiles()"
        />
      </el-tooltip>
      <el-button class="file-mkdir" :icon="FolderPlus" @click="mkdir"
        >新建目录</el-button
      >
      <el-button class="file-upload" :icon="Upload" :loading="uploading" @click="fileInput?.click()"
        >上传</el-button
      >
      <input ref="fileInput" hidden type="file" multiple @change="uploadFile" />
    </div>
    <div class="file-options">
      <el-button
        class="file-hidden-toggle"
        :type="showHidden ? 'primary' : 'default'"
        :icon="showHidden ? Eye : EyeOff"
        @click="showHidden = !showHidden"
      >
        {{ showHidden ? "隐藏文件" : "显示隐藏文件" }}
      </el-button>
      <button @click="listFiles(parentPath())">返回上级</button>
    </div>
    <div v-loading="loading" class="file-manager-list">
      <div v-if="!loading && !shownEntries.length" class="empty-small">
        <Folder :size="26" /><span>当前目录为空</span>
      </div>
      <div
        v-for="entry in shownEntries"
        :key="entry.path"
        class="file-row"
        :class="{ 'is-directory': entry.directory }"
        @click="activateRow(entry, $event)"
        @dblclick="activateRow(entry, $event, true)"
      >
        <span class="file-kind-icon" :class="entry.directory ? 'directory' : 'regular'">
          <component :is="entry.directory ? Folder : File" :size="21" />
        </span>
        <div class="file-row-copy">
          <strong>{{ entry.name }}</strong>
          <small>
            {{ entry.mode }} ·
            {{ entry.directory ? "目录" : formatBytes(entry.size) }} ·
            {{ new Date(entry.modifiedAt).toLocaleString() }}
          </small>
        </div>
        <div
          class="file-row-action-zone"
          @pointerdown.stop
          @click.stop
          @dblclick.stop
        >
          <el-dropdown trigger="click" placement="bottom-end">
            <button class="icon-btn file-row-menu" aria-label="文件操作" title="文件操作">
              <MoreVertical :size="18" />
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-if="entry.directory" :icon="Folder" @click="listFiles(entry.path)">打开</el-dropdown-item>
                <el-dropdown-item v-if="isMarkdown(entry)" :icon="Eye" @click="editEntry(entry, 'preview')">Markdown 预览</el-dropdown-item>
                <el-dropdown-item v-if="isEditable(entry)" :icon="FilePenLine" @click="editEntry(entry)">在线编辑</el-dropdown-item>
                <el-dropdown-item v-if="isImage(entry)" :icon="Eye" @click="previewImage(entry)">预览图片</el-dropdown-item>
                <el-dropdown-item v-if="!entry.directory" :icon="Download" @click="downloadEntry(entry)">下载</el-dropdown-item>
                <el-dropdown-item :icon="Pencil" @click="renameEntry(entry)">重命名</el-dropdown-item>
                <el-dropdown-item divided :icon="Trash2" @click="deleteEntry(entry)">删除</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
    </div>
    <div v-if="uploadQueue.length" class="file-upload-queue">
      <div v-for="item in uploadQueue" :key="item.name" class="file-upload-item"><span>{{ item.name }}</span><el-progress :percentage="item.progress" :status="item.status === '失败' ? 'exception' : item.status === '完成' ? 'success' : undefined" /><small>{{ item.status }}</small></div>
    </div>
    <TextFileEditorDialog
      v-model="editorOpen"
      :host="host"
      :session-id="sessionId"
      :file="editingFile"
      :initial-mode="editorMode"
      @saved="listFiles()"
    />
    <ImagePreviewDialog
      v-model="imagePreviewOpen"
      :host="host"
      :session-id="sessionId"
      :file="previewingImage"
    />
  </el-dialog>
</template>
