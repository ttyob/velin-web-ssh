<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { ElMessage, ElMessageBox, type TreeInstance } from "element-plus";
import { ClipboardPaste, Copy, Download, File as FileIcon, FilePlus, Folder, FolderInput, FolderOpen, FolderPlus, Pencil, RefreshCw, Scissors, Trash2, Upload, ListChecks } from "@lucide/vue";
import { createRemoteFiles, fileName, joinRemotePath, parentRemotePath, relativeRemotePath, withinRemotePath, type RemoteFileEntry } from "../remoteFiles";
import { availableFileName } from "../fileOperations";
import { contextMenuPosition, contextMenuKey } from "../contextMenu";
import InlineFileRename from "./InlineFileRename.vue";
import OperationFeedback from "./OperationFeedback.vue";
import RemoteDirectoryPicker from "./RemoteDirectoryPicker.vue";
import { downloadToolFile } from "../utils/toolDownloads";

export type FileMutation = { kind: "create" | "rename" | "move" | "delete" | "copy" | "upload"; source: string; destination?: string };
const props = defineProps<{
  hostID: string; sessionID: string; rootPath: string; activePath?: string; visible: boolean; locked?: boolean;
  badge: (path: string) => string;
  beforeMutation: (entry: RemoteFileEntry) => Promise<boolean>;
  expandedPaths?: string[];
  deleteWarning: (entry: RemoteFileEntry) => string;
}>();
const emit = defineEmits<{ open: [entry: RemoteFileEntry]; changed: [mutation: FileMutation]; loading: [boolean]; busy: [boolean]; expanded: [string[]] }>();
const files = createRemoteFiles(props.hostID, props.sessionID);
const tree = ref<TreeInstance>();
const version = ref(0), loading = ref(false), busy = ref(false), error = ref("");
const expanded = ref<string[]>(props.expandedPaths || []);
watch(expanded, value => emit("expanded", [...value]), { deep: true });
const selectedPath = ref("");
const renaming = ref<{ path: string; error: string }>();
const operationError = ref(""), transferProgress = ref("");
const selectedEntry = ref<RemoteFileEntry>();
const rootEntry = computed<RemoteFileEntry>(() => ({ name: fileName(props.rootPath), path: props.rootPath, directory: true, symlink: false, size: 0, mode: "", modifiedAt: "" }));
const menu = ref<{ entry: RemoteFileEntry; x: number; y: number }>();
const menuElement = ref<HTMLElement>();
const cutEntry = ref<RemoteFileEntry>();
const clipboardEntries = ref<RemoteFileEntry[]>([]), clipboardCut = ref(false);
const multiSelect = ref(false), checkedEntries = ref<RemoteFileEntry[]>([]);
const pickerOpen = ref(false), pickerEntries = ref<RemoteFileEntry[]>([]);
const pickerPurpose = ref<"move" | "copy">("move");
const pickerInitial = ref(props.rootPath);
const uploadInput = ref<HTMLInputElement>(), uploadFolderInput = ref<HTMLInputElement>();
const uploadDirectory = ref(props.rootPath), uploadProgress = ref("");
let uploadAbort: AbortController | undefined;
const menuEntries = computed(() => {
  const entry = menu.value?.entry;
  if (!entry) return [];
  return checkedEntries.value.some(item => item.path === entry.path) ? topEntries(checkedEntries.value) : [entry];
});
function topEntries(entries: RemoteFileEntry[]) { return entries.filter(entry => !entries.some(parent => parent.path !== entry.path && parent.directory && withinRemotePath(parent.path, entry.path))); }
function updateChecked() { checkedEntries.value = (tree.value?.getCheckedNodes(false, false) || []) as RemoteFileEntry[]; }
function toggleSelection() { multiSelect.value = !multiSelect.value; if (!multiSelect.value) { checkedEntries.value = []; tree.value?.setCheckedKeys([]); } }
function copyEntries(entries: RemoteFileEntry[], cut = false) { clipboardEntries.value = entries.map(entry => ({ ...entry })); clipboardCut.value = cut; cutEntry.value = cut ? entries[0] : undefined; closeMenu(); }

const dragged = ref<RemoteFileEntry>();
const dropTarget = ref("");
let disposed = false;
const menuDirectory = computed(() => menu.value?.entry.directory ? menu.value.entry.path : parentRemotePath(menu.value?.entry.path || props.rootPath));
function report(cause: unknown) { if (cause !== "cancel" && cause !== "close") operationError.value = cause instanceof Error ? cause.message : "文件操作失败"; }
const loadedDirectories = new Set<string>();
let refreshRequest = 0;
async function refresh() {
  const request = ++refreshRequest;
  const panel = (tree.value?.$el as HTMLElement | undefined)?.parentElement;
  const scroll = panel?.scrollTop || 0;
  const paths = [props.rootPath, ...[...loadedDirectories].filter(path => path !== props.rootPath && tree.value?.getNode(path)).sort((a, b) => a.length - b.length)];
  const results = await Promise.allSettled(paths.map(path => files.list(path)));
  if (disposed || request !== refreshRequest) return;
  error.value = "";
  results.forEach((result, index) => {
    if (result.status === "rejected") { if (index === 0) error.value = result.reason instanceof Error ? result.reason.message : "目录读取失败"; return; }
    const path = paths[index]!;
    const parent = path === props.rootPath ? tree.value?.store.root : tree.value?.getNode(path);
    if (!parent || !tree.value) return;
    const entries = result.value.entries.sort((a, b) => Number(b.directory) - Number(a.directory) || a.name.localeCompare(b.name));
    const names = new Set(entries.map(entry => entry.path));
    for (const node of [...parent.childNodes]) if (!names.has(node.data.path)) tree.value.remove(node);
    for (const entry of entries) {
      const node = tree.value.getNode(entry.path);
      if (node) Object.assign(node.data, entry);
      else tree.value.append(entry, parent);
    }
    const order = new Map(entries.map((entry, position) => [entry.path, position]));
    parent.childNodes.sort((a, b) => (order.get(a.data.path) || 0) - (order.get(b.data.path) || 0));
  });
  for (const path of expanded.value) { const node = tree.value?.getNode(path); if (node && !node.expanded) node.expand(); }
  tree.value?.setCurrentKey(selectedPath.value);
  tree.value?.setCheckedKeys(checkedEntries.value.map(entry => entry.path));
  await nextTick(); if (panel) panel.scrollTop = scroll;
}
let collapsingBranch = false;
function collapsePaths(paths: string[]) {
  collapsingBranch = true;
  try {
    for (const path of paths) {
      const node = tree.value?.getNode(path);
      if (node) node.expanded = false;
    }
    expanded.value = expanded.value.filter(path => !paths.includes(path));
  } finally { collapsingBranch = false; }
}
function collapseBranch(entry: RemoteFileEntry) {
  if (collapsingBranch) return;
  collapsePaths(expanded.value.filter(path => withinRemotePath(entry.path, path)));
}
function collapseAll() { collapsePaths([...expanded.value]); }
defineExpose({ refresh, collapseAll });
watch(loading, value => emit("loading", value));
watch(busy, value => emit("busy", value));
async function load(node: { level: number; data: RemoteFileEntry }, resolve: (entries: RemoteFileEntry[]) => void, reject: () => void) {
  if (node.level === 0) { loading.value = true; error.value = ""; }
  try {
    const path = node.level === 0 ? props.rootPath : node.data.path;
    const result = await files.list(path); loadedDirectories.add(path);
    if (!disposed) resolve(result.entries.sort((a, b) => Number(b.directory) - Number(a.directory) || a.name.localeCompare(b.name)));
  } catch (cause) { if (!disposed) { error.value = cause instanceof Error ? cause.message : "目录读取失败"; reject(); } }
  finally { if (node.level === 0) loading.value = false; }
}
function select(entry: RemoteFileEntry) {
  selectedEntry.value = entry; selectedPath.value = entry.path;
  if (!busy.value && !renaming.value) emit("open", entry);
}
function closeMenu() { menu.value = undefined; }
async function showMenu(event: MouseEvent, entry = rootEntry.value) {
  event.preventDefault(); event.stopPropagation();
  if (busy.value || props.locked) return;
  selectedEntry.value = entry; selectedPath.value = entry.path;
  tree.value?.setCurrentKey(entry.path);
  menu.value = { entry, x: event.clientX, y: event.clientY };
  await nextTick();
  if (!menu.value || !menuElement.value) return;
  Object.assign(menu.value, contextMenuPosition(menu.value, menuElement.value));
  menuElement.value.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus({ preventScroll: true });
}
function menuKey(event: KeyboardEvent) { contextMenuKey(event, menuElement.value, () => { closeMenu(); tree.value?.$el.querySelector('.el-tree-node.is-current')?.focus({ preventScroll: true }); }); }
function validName(name: string) { return Boolean(name.trim()) && name !== "." && name !== ".." && !/[\/\x00-\x1f]/.test(name) || "名称不能为空，不能为 .、.. 或包含 / 和控制字符"; }
async function mutate(entry: RemoteFileEntry, operation: () => Promise<unknown>, change: FileMutation, success: string) {
  if (busy.value || props.locked || !(await props.beforeMutation(entry))) return false;
  busy.value = true; operationError.value = "";
  try {
    await operation();
    if (change.destination && (change.kind === "rename" || change.kind === "move")) {
      expanded.value = expanded.value.map(path => withinRemotePath(change.source, path) ? change.destination! + path.slice(change.source.length) : path);
      const parent = parentRemotePath(change.destination);
      if (!expanded.value.includes(parent)) expanded.value.push(parent);
      selectedPath.value = change.destination;
      for (const selection of [selectedEntry, cutEntry]) if (selection.value && withinRemotePath(change.source, selection.value.path)) {
        const path = change.destination + selection.value.path.slice(change.source.length);
        selection.value = { ...selection.value, path, name: fileName(path) };
      }
    } else if (change.kind === "create" || change.kind === "upload" || change.kind === "copy") {
      const parent = parentRemotePath(change.destination || change.source);
      if (!expanded.value.includes(parent)) expanded.value.push(parent);
      selectedPath.value = change.source;
    } else if (change.kind === "delete") {
      expanded.value = expanded.value.filter(path => !withinRemotePath(change.source, path));
      if (withinRemotePath(change.source, cutEntry.value?.path || "")) cutEntry.value = undefined;
      if (withinRemotePath(change.source, selectedEntry.value?.path || "")) selectedEntry.value = undefined;
    }
    if (change.kind === "delete") checkedEntries.value = checkedEntries.value.filter(item => !withinRemotePath(change.source, item.path));
    if (change.destination && (change.kind === "rename" || change.kind === "move")) checkedEntries.value = checkedEntries.value.map(item => withinRemotePath(change.source, item.path) ? { ...item, path: change.destination! + item.path.slice(change.source.length), name: fileName(change.destination! + item.path.slice(change.source.length)) } : item);
    emit("changed", change); await refresh(); if (success) ElMessage.success(success);
    return true;
  } catch (cause) { report(cause); return false; }
  finally { busy.value = false; }
}
async function create(directory: string, folder: boolean) {
  closeMenu();
  try {
    const { value } = await ElMessageBox.prompt(folder ? "输入文件夹名称" : "输入文件名称", folder ? "新建文件夹" : "新建文件", { inputValidator: validName, confirmButtonText: "创建", cancelButtonText: "取消", closeOnClickModal: false });
    const path = joinRemotePath(directory, value);
    const ok = await mutate(rootEntry.value, () => folder ? files.mkdir(path) : files.create(path), { kind: "create", source: path }, folder ? "文件夹已创建" : "文件已创建");
    if (ok && !folder) emit("open", { ...rootEntry.value, path, name: value, directory: false, size: 0 });
  } catch (cause) { report(cause); }
}
function rename(entry: RemoteFileEntry) { closeMenu(); if (busy.value || props.locked || entry.path === props.rootPath) return; renaming.value = { path: entry.path, error: "" }; }
async function finishRename(entry: RemoteFileEntry, value: string) {
  if (!renaming.value || busy.value) return;
  const validation = validName(value);
  if (validation !== true) { renaming.value.error = validation; return; }
  if (value === entry.name) { renaming.value = undefined; return; }
  const destination = joinRemotePath(parentRemotePath(entry.path), value);
  const ok = await mutate(entry, () => files.rename(entry.path, destination), { kind: "rename", source: entry.path, destination }, "已重命名");
  if (ok) renaming.value = undefined;
  else if (renaming.value) renaming.value.error = operationError.value || "操作未完成，请重试";
}
async function remove(entry: RemoteFileEntry) {
  closeMenu();
  try {
    await ElMessageBox.confirm(`删除“${entry.name}”${entry.directory ? "及其全部内容" : ""}？此操作无法撤销。${props.deleteWarning(entry)}`, "删除远程文件", { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消", closeOnClickModal: false });
    await mutate(entry, () => files.remove(entry.path, entry.directory), { kind: "delete", source: entry.path }, "已删除");
  } catch (cause) { report(cause); }
}
function canDrop(entry: RemoteFileEntry | undefined, directory: string) {
  return Boolean(entry && !entry.symlink && entry.path !== props.rootPath && !busy.value && !props.locked && !withinRemotePath(entry.path, directory) && parentRemotePath(entry.path) !== directory && withinRemotePath(props.rootPath, directory));
}
function chooseDirectory(entries: RemoteFileEntry[], purpose: "move" | "copy" = "move") {
  closeMenu(); pickerEntries.value = topEntries(entries); pickerPurpose.value = purpose; pickerInitial.value = parentRemotePath(entries[0]?.path || props.rootPath); pickerOpen.value = true;
}
async function applyDirectory(directory: string) {
  if (pickerPurpose.value === "move") await moveEntries(pickerEntries.value, directory);
  else await pasteEntries(pickerEntries.value, directory, false);
}
async function moveEntries(entries: RemoteFileEntry[], directory: string) {
  const valid = topEntries(entries).filter(entry => canDrop(entry, directory));
  if (!valid.length) return;
  try {
    await ElMessageBox.confirm(`将 ${valid.length > 1 ? `${valid.length} 个项目` : `“${valid[0]!.name}”`}移动到“${relativeRemotePath(props.rootPath, directory) || directory}”？`, "移动文件", { confirmButtonText: "移动", cancelButtonText: "取消", closeOnClickModal: false });
    const names = new Set((await files.list(directory)).entries.map(item => item.name));
    let completed = 0;
    for (const entry of valid) {
      const name = await availableFileName(entry.name, entry.directory, names); if (!name) continue;
      transferProgress.value = `正在移动 ${completed + 1}/${valid.length} · ${entry.name}`;
      const destination = joinRemotePath(directory, name);
      if (!(await mutate(entry, () => files.rename(entry.path, destination), { kind: "move", source: entry.path, destination }, ""))) return;
      names.add(name); completed++;
      clipboardEntries.value = clipboardEntries.value.filter(item => item.path !== entry.path);
    }
    cutEntry.value = undefined; ElMessage.success(`已移动 ${completed} 个项目`);
  } catch (cause) { report(cause); } finally { transferProgress.value = ""; }
}
async function pasteEntries(entries: RemoteFileEntry[], directory: string, cut: boolean) {
  closeMenu(); if (cut) { await moveEntries(entries, directory); return; }
  try {
    const names = new Set((await files.list(directory)).entries.map(item => item.name));
    const targets = topEntries(entries); let completed = 0;
    for (const entry of targets) {
      const name = await availableFileName(entry.name, entry.directory, names); if (!name) continue;
      const destination = joinRemotePath(directory, name);
      if (!withinRemotePath(props.rootPath, directory) || (entry.directory && withinRemotePath(entry.path, destination))) { ElMessage.warning("不能复制目录到自身内部"); return; }
      transferProgress.value = `正在复制 ${completed + 1}/${targets.length} · ${entry.name}`;
      if (!(await mutate(entry, () => files.copy(props.rootPath, entry.path, destination), { kind: "copy", source: entry.path, destination }, ""))) return;
      names.add(name); completed++;
    }
    ElMessage.success(`已复制 ${completed} 个项目`);
  } catch (cause) { report(cause); } finally { transferProgress.value = ""; }
}
async function removeEntries(entries: RemoteFileEntry[]) {
  closeMenu(); entries = topEntries(entries).filter(entry => entry.path !== props.rootPath);
  if (entries.length === 1) { await remove(entries[0]!); return; }
  if (!entries.length) return;
  try {
    await ElMessageBox.confirm(`删除以下 ${entries.length} 个项目及目录内容？此操作无法撤销。\n${entries.map(entry => relativeRemotePath(props.rootPath, entry.path) + props.deleteWarning(entry)).join('\n')}`, "批量删除远程文件", { confirmButtonText: "删除", cancelButtonText: "取消", type: "warning", closeOnClickModal: false });
    for (const entry of entries) if (!(await mutate(entry, () => files.remove(entry.path, entry.directory), { kind: "delete", source: entry.path }, ""))) return;
    ElMessage.success("所选项目已删除");
  } catch (cause) { report(cause); }
}
function chooseUpload(directory: string, folder = false) { closeMenu(); uploadDirectory.value = directory; (folder ? uploadFolderInput.value : uploadInput.value)?.click(); }
async function uploadFiles(selected: File[], directory: string) {
  if (!selected.length || busy.value || props.locked || !(await props.beforeMutation(rootEntry.value))) return;
  try { await ElMessageBox.confirm(`上传 ${selected.length} 个文件到“${relativeRemotePath(props.rootPath, directory) || directory}”？同名文件不会覆盖。`, "上传文件", { confirmButtonText: "上传", cancelButtonText: "取消", closeOnClickModal: false }); } catch { return; }
  busy.value = true; uploadAbort = new AbortController(); let completed = 0;
  const createdDirectories = new Set<string>();
  const directoryNames = new Map<string, Set<string>>();
  try {
    for (const file of selected) {
      if (uploadAbort.signal.aborted) break;
      const relative = file.webkitRelativePath || file.name;
      if (relative.split('/').some(part => !part || validName(part) !== true)) throw new Error("文件路径无效");
      let destination = joinRemotePath(directory, relative); const parent = parentRemotePath(destination);
      if (parent !== directory && !createdDirectories.has(parent)) { await files.mkdir(parent); createdDirectories.add(parent); }
      if (!directoryNames.has(parent)) directoryNames.set(parent, new Set((await files.list(parent)).entries.map(item => item.name)));
      const names = directoryNames.get(parent)!; const name = await availableFileName(fileName(destination), false, names); if (!name) continue;
      destination = joinRemotePath(parent, name);
      if (uploadAbort.signal.aborted) break;
      uploadProgress.value = `${completed + 1}/${selected.length} · ${relative}`;
      await files.upload(destination, file, uploadAbort.signal); names.add(name); completed++;
      if (!expanded.value.includes(parent)) expanded.value.push(parent);
      emit("changed", { kind: "upload", source: destination });
    }
    ElMessage.success(`已上传 ${completed} 个文件`);
  } catch (cause) { if (!uploadAbort.signal.aborted) report(cause); }
  finally { busy.value = false; uploadProgress.value = ""; refresh(); }
}
function inputUpload(event: Event) { const input = event.target as HTMLInputElement; const selected = Array.from(input.files || []); input.value = ""; void uploadFiles(selected, uploadDirectory.value); }
function dragStart(event: DragEvent, entry: RemoteFileEntry) {
  closeMenu();
  if (busy.value || props.locked || entry.symlink) { event.preventDefault(); return; }
  renaming.value = undefined;
  dragged.value = entry;
  event.dataTransfer!.effectAllowed = "move";
  event.dataTransfer!.setData("text/plain", entry.path);
}
function dragOver(event: DragEvent, entry = rootEntry.value) {
  if (event.dataTransfer?.types.includes("Files") && entry.directory && !busy.value && !props.locked) { event.preventDefault(); event.stopPropagation(); dropTarget.value = entry.path; return; }
  event.stopPropagation();
  if (entry.directory && !entry.symlink && canDrop(dragged.value, entry.path)) {
    event.preventDefault(); event.dataTransfer!.dropEffect = "move"; dropTarget.value = entry.path;
  } else { if (event.dataTransfer) event.dataTransfer.dropEffect = "none"; dropTarget.value = ""; }
}
function dragEnd() { dragged.value = undefined; dropTarget.value = ""; }
function drop(event: DragEvent, entry = rootEntry.value) {
  if (event.dataTransfer?.files.length && entry.directory) { event.preventDefault(); event.stopPropagation(); dropTarget.value = ""; void uploadFiles(Array.from(event.dataTransfer.files), entry.path); return; }
  event.preventDefault(); event.stopPropagation();
  const source = dragged.value;
  dragEnd();
  if (entry.directory && !entry.symlink && source) { const entries = checkedEntries.value.some(item => item.path === source.path) ? checkedEntries.value : [source]; void moveEntries(entries, entry.path); }
}
async function copyPath(entry: RemoteFileEntry, relative = false) {
  closeMenu();
  try { await navigator.clipboard.writeText(relative ? relativeRemotePath(props.rootPath, entry.path) : entry.path); ElMessage.success("路径已复制"); } catch { ElMessage.error("复制失败，请检查浏览器剪贴板权限"); }
}
function download(entry: RemoteFileEntry) { closeMenu(); downloadToolFile(`/api/sftp/${props.hostID}/download?${new URLSearchParams({ path: entry.path, session: props.sessionID })}`); }
function onWheel(event: WheelEvent) { if (!menuElement.value?.contains(event.target as Node)) closeMenu(); }
function outside(event: PointerEvent) { if (!menuElement.value?.contains(event.target as Node)) closeMenu(); }
function treeKey(event: KeyboardEvent) {
  if ((event.target as HTMLElement).closest("input, textarea")) return;
  const entry = selectedEntry.value;
  if (!entry || entry.path === props.rootPath || busy.value || props.locked) return;
  if ((event.ctrlKey || event.metaKey) && ["c", "x", "v"].includes(event.key.toLowerCase())) {
    event.preventDefault(); event.stopPropagation();
    if (event.key.toLowerCase() === "v") void pasteEntries(clipboardEntries.value, entry.directory ? entry.path : parentRemotePath(entry.path), clipboardCut.value);
    else copyEntries(checkedEntries.value.length ? topEntries(checkedEntries.value) : [entry], event.key.toLowerCase() === "x");
  }
  if (event.key === "F2") { event.preventDefault(); void rename(entry); }
  if (event.key === "Delete" && !entry.symlink) { event.preventDefault(); void removeEntries(checkedEntries.value.length ? checkedEntries.value : [entry]); }
}
watch(() => props.activePath, path => { if (path) selectedPath.value = path; });
watch(() => props.visible && !props.locked, visible => { if (!visible) { closeMenu(); dragEnd(); } });
onMounted(() => { document.addEventListener("pointerdown", outside, true); window.addEventListener("resize", closeMenu); window.addEventListener("wheel", onWheel, { capture: true, passive: true }); });
onBeforeUnmount(() => { disposed = true; refreshRequest++; uploadAbort?.abort(); document.removeEventListener("pointerdown", outside, true); window.removeEventListener("resize", closeMenu); window.removeEventListener("wheel", onWheel, true); });
</script>

<template>
  <div class="code-file-panel" :aria-busy="busy || loading" @contextmenu="showMenu($event)" @dragover="dragOver($event)" @drop="drop($event)" @keydown="treeKey">
    <input ref="uploadInput" class="code-upload-input" type="file" multiple aria-label="选择上传文件" @change="inputUpload" /><input ref="uploadFolderInput" class="code-upload-input" type="file" multiple webkitdirectory aria-label="选择上传文件夹" @change="inputUpload" />
    <OperationFeedback :pending="transferProgress" :error="renaming?.error || operationError" retry-label="刷新目录" @retry="refresh" @dismiss="operationError = ''; if (renaming) renaming.error = ''" />
    <div v-if="uploadProgress" class="code-upload-progress"><span>{{ uploadProgress }}</span><button @click="uploadAbort?.abort()">停止</button></div>
    <div class="code-root-label" :class="{ 'is-drop-target': dropTarget === rootPath }" :title="rootPath" @contextmenu.stop="showMenu($event)" @dragover.stop="dragOver($event)" @drop.stop="drop($event)"><FolderOpen :size="14" />{{ fileName(rootPath) || '/' }}</div>
    <div v-if="error" class="code-inline-error">{{ error }}<button @click="refresh">重试</button></div>
    <el-tree :key="version" ref="tree" v-loading="loading" class="code-file-tree" node-key="path" lazy :load="load" :show-checkbox="multiSelect" check-strictly @check="updateChecked" :props="{ label: 'name', isLeaf: (entry: RemoteFileEntry) => !entry.directory || entry.symlink }" :default-expanded-keys="expanded" :expand-on-click-node="true" highlight-current :current-node-key="selectedPath" @node-click="select" @node-contextmenu="(event: MouseEvent, entry: RemoteFileEntry) => showMenu(event, entry)" @node-expand="(entry: RemoteFileEntry) => { if (!expanded.includes(entry.path)) expanded.push(entry.path); }" @node-collapse="collapseBranch">
      <template #default="{ node, data }"><span class="code-tree-entry" :class="{ 'is-directory': data.directory, 'is-file': !data.directory, 'is-symlink': data.symlink, 'is-cut': cutEntry?.path === data.path, 'is-drop-target': dropTarget === data.path }" :title="data.path" :draggable="!data.symlink && !busy && !locked && !renaming" @dragstart.stop="dragStart($event, data)" @dragend="dragEnd" @dragover="dragOver($event, data)" @dragleave="dropTarget = ''" @drop="drop($event, data)"><component :is="data.directory ? node.expanded ? FolderOpen : Folder : FileIcon" :size="14" /><InlineFileRename v-if="renaming?.path === data.path" :name="data.name" :directory="data.directory" :busy="busy" :error="renaming?.error" @confirm="finishRename(data, $event)" @cancel="renaming = undefined" /><span v-else>{{ data.name }}</span><b v-if="badge(data.path)" class="code-git-badge" :data-status="badge(data.path)">{{ badge(data.path) }}</b></span></template>
    </el-tree>
  </div>
  <Teleport to="body"><nav v-if="menu && visible && !locked" ref="menuElement" class="code-file-context-menu" role="menu" aria-label="文件操作" :style="{ left: `${menu.x}px`, top: `${menu.y}px` }" @keydown="menuKey" @contextmenu.prevent>
    <small>{{ menuEntries.length > 1 ? `已选择 ${menuEntries.length} 个项目` : menu.entry.name || '/' }}</small>
    <button v-if="!menu.entry.directory" role="menuitem" :disabled="menu.entry.symlink" @click="emit('open', menu.entry); closeMenu()"><FileIcon :size="14" />打开文件</button>
    <button role="menuitem" @click="create(menuDirectory, false)"><FilePlus :size="14" />新建文件</button>
    <button role="menuitem" @click="create(menuDirectory, true)"><FolderPlus :size="14" />新建文件夹</button>
    <hr />
    <button v-if="menu.entry.path !== rootPath && menuEntries.length === 1" role="menuitem" @click="rename(menu.entry)"><Pencil :size="14" />重命名<kbd>F2</kbd></button>
    <button v-if="menu.entry.path !== rootPath" role="menuitem" :disabled="menu.entry.symlink" @click="copyEntries(menuEntries, true)"><Scissors :size="14" />剪切</button>
    <button v-if="menu.entry.path !== rootPath" role="menuitem" :disabled="menuEntries.some(entry => entry.symlink)" @click="copyEntries(menuEntries)"><Copy :size="14" />复制</button>
    <button role="menuitem" :disabled="!clipboardEntries.length || clipboardCut && !clipboardEntries.some(entry => canDrop(entry, menuDirectory))" @click="pasteEntries(clipboardEntries, menuDirectory, clipboardCut)"><ClipboardPaste :size="14" />{{ clipboardCut ? '粘贴（移动）' : '粘贴' }}</button>
    <button v-if="menu.entry.path !== rootPath" role="menuitem" :disabled="menu.entry.symlink" @click="chooseDirectory(menuEntries)"><FolderInput :size="14" />移动到…</button>
    <button v-if="menu.entry.path !== rootPath" role="menuitem" @click="chooseDirectory(menuEntries, 'copy')"><Copy :size="14" />复制到…</button>
    <button role="menuitem" @click="toggleSelection(); closeMenu()"><ListChecks :size="14" />{{ multiSelect ? '结束多选' : '多选文件' }}</button>
    <button role="menuitem" @click="chooseUpload(menuDirectory)"><Upload :size="14" />上传文件</button>
    <button role="menuitem" @click="chooseUpload(menuDirectory, true)"><FolderPlus :size="14" />上传文件夹</button>
    <button role="menuitem" @click="copyPath(menu.entry)"><Copy :size="14" />复制路径</button>
    <button role="menuitem" @click="copyPath(menu.entry, true)"><Copy :size="14" />复制相对路径</button>
    <button v-if="!menu.entry.directory" role="menuitem" :disabled="menu.entry.symlink" @click="download(menu.entry)"><Download :size="14" />下载</button>
    <hr />
    <button role="menuitem" @click="refresh(); closeMenu()"><RefreshCw :size="14" />刷新目录</button>
    <button v-if="menu.entry.path !== rootPath" role="menuitem" class="danger" :disabled="menu.entry.symlink" @click="removeEntries(menuEntries)"><Trash2 :size="14" />删除<kbd>Del</kbd></button>
  </nav></Teleport>
  <RemoteDirectoryPicker v-model="pickerOpen" :hostID="hostID" :sessionID="sessionID" :root-path="rootPath" :initial-path="pickerInitial" :title="pickerPurpose === 'move' ? '选择移动目标目录' : '选择复制目标目录'" :disabled-paths="pickerEntries.filter(entry => entry.directory).map(entry => entry.path)" @select="applyDirectory" />
</template>

<style scoped>
.code-file-panel .code-upload-input { display: none; }
.code-upload-progress { display: flex; align-items: center; gap: 5px; padding: 6px 10px; font-size: 11px; }
.code-upload-progress span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-right: auto; }
.code-tree-entry { min-height: 28px; }
.code-tree-entry.is-directory { color: var(--text); font-weight: 550; }
.code-tree-entry.is-directory > svg { --icon-color: var(--icon-warm); fill: color-mix(in srgb, var(--icon-warm) 15%, transparent); }
.code-tree-entry.is-file { color: var(--text-soft, var(--text)); font-weight: 400; }
.code-tree-entry.is-file > svg { --icon-color: var(--icon-cool); opacity: .85; }
.code-tree-entry.is-symlink { font-style: italic; opacity: .78; }
.code-tree-entry.is-cut { opacity: .45; }
.code-tree-entry.is-drop-target, .code-root-label.is-drop-target { background: var(--surface-2); outline: 1px solid var(--accent); outline-offset: -1px; }
.code-file-context-menu { position: fixed; z-index: 2100; display: flex; flex-direction: column; min-width: 200px; max-width: min(300px, calc(100vw - 12px)); max-height: calc(100dvh - 12px); overflow: auto; padding: 5px; border: 1px solid var(--line-strong, var(--line)); border-radius: 5px; background: var(--surface); box-shadow: 0 8px 28px #0005; color: var(--text); }
.code-file-context-menu small { padding: 7px 9px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--muted); font-size: 11px; }
.code-file-context-menu button { display: flex; align-items: center; gap: 9px; min-height: 30px; padding: 5px 9px; border: 0; border-radius: 3px; background: transparent; color: var(--text); font-family: inherit; font-size: 12px; text-align: left; cursor: pointer; }
.code-file-context-menu button:hover, .code-file-context-menu button:focus-visible { outline: none; background: var(--surface-2); }
.code-file-context-menu button:disabled { opacity: .4; cursor: default; }
.code-file-context-menu button.danger { color: var(--danger, #ce8585); }
.code-file-context-menu svg { color: var(--muted); flex-shrink: 0; }
.code-file-context-menu kbd { margin-left: auto; color: var(--muted); font: 10px monospace; }
.code-file-context-menu hr { width: 100%; margin: 4px 0; border: 0; border-top: 1px solid var(--line); }
</style>
