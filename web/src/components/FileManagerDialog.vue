<script setup lang="ts">
import { useDialogReturnFocus } from "../composables/useDialogReturnFocus";
import { availableFileName } from "../fileOperations";
import { contextMenuPosition, contextMenuKey } from "../contextMenu";
import InlineFileRename from "./InlineFileRename.vue";
import OperationFeedback from "./OperationFeedback.vue";
import { downloadToolFile } from "../utils/toolDownloads";
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { ArrowLeft, ArrowRight, ArrowUp, Check, ChevronDown, Copy, Download, Eye, File as FileIcon, FilePlus2, FilePenLine, Folder, FolderOpen, FolderPlus, Info, MoreHorizontal, PanelLeft, Pencil, RefreshCw, Scissors, Search, Trash2, Upload, ClipboardPaste, FolderInput, X } from "@lucide/vue";
import { ElMessage, ElMessageBox, type ElTree } from "element-plus";
import { api, csrfHeaders } from "../api";
import { createRemoteFiles, joinRemotePath, parentRemotePath, withinRemotePath, type RemoteFileEntry as FileEntry } from "../remoteFiles";
import type { Host } from "../types";

const TextFileEditorDialog = defineAsyncComponent(() => import("./TextFileEditorDialog.vue"));
const ImagePreviewDialog = defineAsyncComponent(() => import("./ImagePreviewDialog.vue"));
const RemoteDirectoryPicker = defineAsyncComponent(() => import("./RemoteDirectoryPicker.vue"));
const props = defineProps<{ modelValue: boolean; host?: Host; sessionId?: string; initialPath?: string }>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();
const { restoreFocus } = useDialogReturnFocus(() => props.modelValue);
const files = computed(() => createRemoteFiles(props.host?.id || "", props.sessionId || ""));
function beforeClose(done: () => void) {
  if (busy.value || uploading.value) { ElMessage.info("文件操作正在进行，请完成或停止传输后关闭"); return; }
  done();
}
const currentPath = ref("."), pathInput = ref("."), filter = ref("");
const entries = ref<FileEntry[]>([]), selected = ref<string[]>([]), anchor = ref("");
const showHidden = ref(false), loading = ref(false), busy = ref(false), error = ref("");
const history = ref<string[]>([]), historyIndex = ref(-1);
const renaming = ref<{ path: string; error: string }>();
const operationError = ref(""), transferProgress = ref("");
const sortBy = ref<"name" | "size" | "modifiedAt">("name"), descending = ref(false);
type TransferItem = { name: string; progress: number; status: string; file: File; directory: string; error: string };
const uploadQueue = ref<TransferItem[]>([]);
let uploadAbort: AbortController | undefined;
const uploading = ref(false), fileInput = ref<HTMLInputElement>();
let uploadTarget: string | undefined;
const editorOpen = ref(false), editingFile = ref<FileEntry>(), editorMode = ref<"edit" | "preview">("edit");
const imagePreviewOpen = ref(false), previewingImage = ref<FileEntry>();
const tree = ref<InstanceType<typeof ElTree>>(), treeKey = ref(0), sidebar = ref<HTMLElement>(), sidebarWidth = ref(210), showSidebar = ref(false);
const directoryCache = new Map<string, FileEntry[]>();
let listController: AbortController | undefined;
const sessionQuery = computed(() => props.sessionId ? `&session=${encodeURIComponent(props.sessionId)}` : "");
const clipboard = ref<{ entries: FileEntry[]; cut: boolean }>();
const pickerOpen = ref(false), pickerEntries = ref<FileEntry[]>([]), pickerCopy = ref(false);
const properties = ref<FileEntry>();
const dragged = ref<FileEntry[]>([]), dropPath = ref("");
const list = ref<HTMLElement>(), menuElement = ref<HTMLElement>();
const menu = ref<{ x: number; y: number; targets: FileEntry[]; directory: string }>();
const shownEntries = computed(() => entries.value.filter(item => (showHidden.value || !item.name.startsWith(".")) && item.name.toLocaleLowerCase().includes(filter.value.toLocaleLowerCase())).sort((a, b) => {
  const folders = Number(b.directory) - Number(a.directory);
  const order = sortBy.value === "size" ? a.size - b.size : sortBy.value === "modifiedAt" ? a.modifiedAt.localeCompare(b.modifiedAt) : a.name.localeCompare(b.name, undefined, { numeric: true });
  return folders || (descending.value ? -order : order) || a.name.localeCompare(b.name);
}));
const selectedEntries = computed(() => shownEntries.value.filter(entry => selected.value.includes(entry.path)));
const expandedPaths = computed(() => { const paths = ["/"]; let path = ""; for (const part of currentPath.value.split("/").filter(Boolean)) { path += "/" + part; paths.push(path); } return paths; });
const selectedSize = computed(() => selectedEntries.value.reduce((sum, entry) => sum + (entry.directory ? 0 : entry.size), 0));
function report(cause: unknown) { if (cause !== "cancel" && cause !== "close") operationError.value = cause instanceof Error ? cause.message : "文件操作失败"; }
function validName(name: string) { return !!name.trim() && name !== "." && name !== ".." && !/[\/\0]/.test(name) || "名称不能为空，不能包含 /，也不能为 . 或 .."; }
function synthetic(path: string): FileEntry { return { path, name: path === "/" ? "文件系统" : path.split("/").at(-1) || path, directory: true, symlink: false, size: 0, mode: "", modifiedAt: "" }; }
watch(() => props.modelValue, open => {
  menu.value = undefined;
  if (!open) { listController?.abort(); clipboard.value = undefined; return; }
  directoryCache.clear(); treeKey.value++; history.value = []; historyIndex.value = -1; filter.value = ""; selected.value = []; entries.value = []; error.value = "";
  currentPath.value = props.initialPath?.trim() || props.host?.initialDirectory || ".";
  pathInput.value = currentPath.value;
  void listFiles(currentPath.value);
}, { immediate: true });
watch(showHidden, () => { treeKey.value++; });
function dismissMenu(event: Event) { if (!menuElement.value?.contains(event.target as Node)) menu.value = undefined; }
function closeMenu() { menu.value = undefined; }
function removeMenuListeners() { document.removeEventListener("pointerdown", dismissMenu, true); document.removeEventListener("wheel", dismissMenu, true); window.removeEventListener("resize", closeMenu); window.removeEventListener("blur", closeMenu); }
watch(() => !!menu.value, open => {
  removeMenuListeners();
  if (open) { document.addEventListener("pointerdown", dismissMenu, true); document.addEventListener("wheel", dismissMenu, { capture: true, passive: true }); window.addEventListener("resize", closeMenu); window.addEventListener("blur", closeMenu); }
});
onBeforeUnmount(() => { uploadAbort?.abort(); listController?.abort(); stopResize?.(); removeMenuListeners(); });

async function listFiles(next = currentPath.value, index?: number) {
  if (!props.host || !props.modelValue) return;
  listController?.abort(); const controller = new AbortController(); listController = controller;
  const sameDirectory = next === currentPath.value;
  const scroll = sameDirectory ? list.value?.scrollTop || 0 : 0;
  loading.value = true; error.value = ""; menu.value = undefined;
  try {
    const result = await files.value.list(next, controller.signal);
    if (controller.signal.aborted) return;
    currentPath.value = result.path; pathInput.value = result.path; entries.value = result.entries;
    directoryCache.set(result.path, result.entries);
    if (index !== undefined) historyIndex.value = index;
    else if (history.value[historyIndex.value] !== result.path) { history.value.splice(historyIndex.value + 1); history.value.push(result.path); historyIndex.value = history.value.length - 1; }
    selected.value = selected.value.filter(path => result.entries.some(entry => entry.path === path));
    if (!sameDirectory) { filter.value = ""; renaming.value = undefined; }
    showSidebar.value = false;
    await nextTick(); if (list.value) list.value.scrollTop = scroll; tree.value?.setCurrentKey(result.path);
  } catch (cause) {
    if (!controller.signal.aborted) { error.value = cause instanceof Error ? cause.message : "目录读取失败"; pathInput.value = currentPath.value; }
  } finally { if (listController === controller) loading.value = false; }
}
async function refresh() {
  directoryCache.clear();
  await listFiles();
  // Reload folder nodes after mutations, retaining the expanded ancestry.
  treeKey.value++;
}
async function loadDirectories(node: { level: number; data: FileEntry }, resolve: (items: FileEntry[]) => void, reject: () => void) {
  if (!node.level) { resolve([synthetic("/")]); return; }
  try {
    const path = node.data.path;
    const children = directoryCache.get(path) || (await files.value.list(path)).entries;
    directoryCache.set(path, children);
    resolve(children.filter(entry => entry.directory && !entry.symlink && (showHidden.value || !entry.name.startsWith("."))).sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true })));
  } catch (cause) { reject(); report(cause); }
}
function sort(column: "name" | "size" | "modifiedAt") { if (sortBy.value === column) descending.value = !descending.value; else { sortBy.value = column; descending.value = false; } }
function selectEntry(entry: FileEntry, event: MouseEvent) {
  if (event.shiftKey && anchor.value) {
    const a = shownEntries.value.findIndex(item => item.path === anchor.value), b = shownEntries.value.indexOf(entry);
    if (a >= 0) { selected.value = shownEntries.value.slice(Math.min(a, b), Math.max(a, b) + 1).map(item => item.path); return; }
  }
  if (event.ctrlKey || event.metaKey) selected.value = selected.value.includes(entry.path) ? selected.value.filter(path => path !== entry.path) : [...selected.value, entry.path];
  else selected.value = [entry.path];
  anchor.value = entry.path; menu.value = undefined;
}
async function createEntry(directory: boolean, parent = currentPath.value) {
  try {
    const { value } = await ElMessageBox.prompt(directory ? "输入新目录名称" : "输入新文件名称", directory ? "新建目录" : "新建文件", { inputValidator: validName, closeOnClickModal: false });
    await mutate(async () => { const path = joinRemotePath(parent, value); if (directory) await files.value.mkdir(path); else await files.value.create(path); }, "创建完成");
  } catch (cause) { report(cause); }
}
function renameEntry(entry: FileEntry) {
  if (busy.value || uploading.value) return;
  menu.value = undefined; selected.value = [entry.path];
  if (parentRemotePath(entry.path) !== currentPath.value) { void listFiles(parentRemotePath(entry.path)).then(() => { if (entries.value.some(item => item.path === entry.path)) renaming.value = { path: entry.path, error: "" }; }); }
  else renaming.value = { path: entry.path, error: "" };
}
async function finishRename(entry: FileEntry, value: string) {
  if (!renaming.value || busy.value) return;
  const validation = validName(value);
  if (validation !== true) { renaming.value.error = validation; return; }
  if (value === entry.name) { renaming.value = undefined; list.value?.focus({ preventScroll: true }); return; }
  const destination = joinRemotePath(parentRemotePath(entry.path), value);
  if (entries.value.some(item => item.path === destination)) { renaming.value.error = `目标已存在“${value}”`; return; }
  const ok = await mutate(async () => { await files.value.rename(entry.path, destination); selected.value = [destination]; anchor.value = destination; }, "已重命名");
  if (ok) { renaming.value = undefined; list.value?.focus({ preventScroll: true }); }
  else if (renaming.value) renaming.value.error = operationError.value || "操作未完成，请重试";
}
async function removeEntries(targets: FileEntry[]) {
  if (!targets.length) return;
  try {
    await ElMessageBox.confirm(targets.length === 1 ? `永久删除“${targets[0].name}”${targets[0].directory ? "及其全部内容" : ""}？` : `永久删除选中的 ${targets.length} 个项目？文件夹及其内容也会删除。`, "删除远程文件", { type: "warning", confirmButtonText: "删除", closeOnClickModal: false });
    await mutate(async () => { for (const entry of targets) await files.value.remove(entry.path, entry.directory); }, "已删除");
  } catch (cause) { report(cause); }
}
async function mutate(operation: () => Promise<unknown>, message?: string) {
  if (busy.value || uploading.value) return false;
  busy.value = true; operationError.value = "";
  try { await operation(); if (message) ElMessage.success(message); return true; }
  catch (cause) { report(cause); return false; }
  finally { await refresh(); busy.value = false; transferProgress.value = ""; }
}
function setClipboard(targets: FileEntry[], cut: boolean) { clipboard.value = { entries: [...targets], cut }; }
async function paste(directory = currentPath.value) {
  const clip = clipboard.value; if (!clip?.entries.length) return;
  await transfer(clip.entries, directory, !clip.cut, true);
}
async function transfer(targets: FileEntry[], directory: string, copy: boolean, fromClipboard = false) {
  if (targets.some(entry => entry.directory && withinRemotePath(entry.path, directory))) { ElMessage.warning("不能将文件夹放入自身或其子目录"); return; }
  const completed = new Set<string>();
  await mutate(async () => {
    const destinationEntries = (await files.value.list(directory)).entries;
    const names = new Set(destinationEntries.map(entry => entry.name));
    for (const entry of targets) {
      if (!copy && parentRemotePath(entry.path) === directory) { completed.add(entry.path); continue; }
      let name = entry.name;
      if (copy && parentRemotePath(entry.path) === directory) {
        const dot = !entry.directory ? name.lastIndexOf(".") : -1, stem = dot > 0 ? name.slice(0, dot) : name, extension = dot > 0 ? name.slice(dot) : "";
        let index = 1; do { name = `${stem} - 副本${index === 1 ? "" : ` (${index})`}${extension}`; index++; } while (names.has(name));
      } else { const available = await availableFileName(name, entry.directory, names); if (!available) continue; name = available; }
      transferProgress.value = `正在${copy ? "复制" : "移动"} ${completed.size + 1}/${targets.length} · ${name}`;
      const destination = joinRemotePath(directory, name);
      if (copy) await files.value.copy("/", entry.path, destination); else await files.value.rename(entry.path, destination);
      names.add(name); completed.add(entry.path);
      if (fromClipboard && !copy && clipboard.value) clipboard.value.entries = clipboard.value.entries.filter(item => item.path !== entry.path);
    }
    if (completed.size) ElMessage.success(`${copy ? "已复制" : "已移动"} ${completed.size} 个项目`);
  });
}
function chooseDestination(targets: FileEntry[], copy: boolean) { pickerEntries.value = [...targets]; pickerCopy.value = copy; pickerOpen.value = true; }
function downloadEntry(entry: FileEntry) { if (props.host) downloadToolFile(`/api/sftp/${props.host.id}/download?path=${encodeURIComponent(entry.path)}${sessionQuery.value}`); }
const editableExtensions = new Set(["txt", "text", "md", "markdown", "mdx", "json", "jsonc", "json5", "ini", "env", "properties", "conf", "cfg", "yaml", "yml", "xml", "toml", "csv", "log", "sh", "bash", "zsh", "fish", "js", "mjs", "cjs", "ts", "mts", "cts", "tsx", "jsx", "css", "scss", "sass", "less", "styl", "html", "htm", "xhtml", "vue", "svelte", "go", "py", "pyw", "rb", "php", "php3", "php4", "php5", "php8", "phtml", "phps", "java", "c", "h", "cpp", "cc", "cxx", "hpp", "hh", "hxx", "cs", "scala", "kt", "kts", "m", "mm", "dart", "rs", "sql", "lua", "pl", "pm", "r", "swift", "ps1", "psm1", "cmake", "proto", "diff", "patch", "gradle", "groovy", "gvy", "hs", "lhs", "clj", "cljs", "cljc", "edn", "lisp", "lsp", "scm", "erl", "hrl", "f", "f90", "f95", "pas", "pp", "vb", "vbs", "v", "vh", "sv", "svh", "vhd", "vhdl", "coffee", "elm", "pug", "jade", "j2", "jinja", "jinja2", "feature", "mk"]);
const editableNames = new Set(["dockerfile", "makefile", "gnumakefile", "bsdmakefile", "cmakelists.txt", "jenkinsfile", "gemfile", "rakefile", "guardfile", "podfile"]);
const imageExtensions = new Set(["avif", "bmp", "gif", "jpe", "jpeg", "jpg", "png", "webp"]);
function isEditable(entry: FileEntry) { const name = entry.name.toLowerCase(); return !entry.directory && !entry.symlink && entry.size <= 2 * 1024 * 1024 && (name === ".env" || name.startsWith(".env.") || name.startsWith("dockerfile.") || editableNames.has(name) || editableExtensions.has(name.split(".").at(-1) || "")); }
function isMarkdown(entry: FileEntry) { return !entry.directory && !entry.symlink && /\.(md|markdown)$/i.test(entry.name); }
function isImage(entry: FileEntry) { return !entry.directory && !entry.symlink && entry.size > 0 && entry.size <= 32 * 1024 * 1024 && imageExtensions.has(entry.name.split(".").at(-1)?.toLowerCase() || ""); }
function editEntry(entry: FileEntry, mode: "edit" | "preview" = "edit") { editingFile.value = entry; editorMode.value = mode; editorOpen.value = true; }
function previewImage(entry: FileEntry) { previewingImage.value = entry; imagePreviewOpen.value = true; }
function activateEntry(entry: FileEntry) { if (entry.directory) void listFiles(entry.path); else if (isEditable(entry)) editEntry(entry); else if (isImage(entry)) previewImage(entry); else downloadEntry(entry); }
function formatBytes(size: number) { if (size < 1024) return `${size} B`; const units = ["KB", "MB", "GB", "TB"]; let value = size / 1024, index = 0; while (value >= 1024 && index < units.length - 1) { value /= 1024; index++; } return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`; }
function dateLabel(date: string) { return date ? new Date(date).toLocaleString("zh-CN", { hour12: false }) : "—"; }
function typeLabel(entry: FileEntry) { return entry.symlink ? "符号链接" : entry.directory ? "文件夹" : entry.name.includes(".") ? `${entry.name.split(".").at(-1)?.toUpperCase()} 文件` : "文件"; }
async function copyPath(targets: FileEntry[], directory = currentPath.value) { try { await navigator.clipboard.writeText(targets.length ? targets.map(entry => entry.path).join("\n") : directory); ElMessage.success("路径已复制"); } catch { ElMessage.error("剪贴板不可用，请从地址栏复制路径"); } }

interface MenuAction { label: string; icon: unknown; shortcut?: string; disabled?: boolean; danger?: boolean; divider?: boolean; run: () => unknown }
const menuActions = computed<MenuAction[]>(() => {
  const state = menu.value; if (!state) return [];
  const targets = state.targets, entry = targets.length === 1 ? targets[0] : undefined, directory = state.directory;
  const actions: MenuAction[] = [];
  if (entry) {
    actions.push({ label: entry.directory ? "打开文件夹" : isImage(entry) ? "预览图片" : isEditable(entry) ? "在线编辑" : "下载", icon: entry.directory ? FolderOpen : isImage(entry) ? Eye : isEditable(entry) ? FilePenLine : Download, shortcut: "Enter", run: () => activateEntry(entry) });
    if (isMarkdown(entry)) actions.push({ label: "Markdown 预览", icon: Eye, run: () => editEntry(entry, "preview") });
    if (!entry.directory && (isEditable(entry) || isImage(entry))) actions.push({ label: "下载", icon: Download, run: () => downloadEntry(entry) });
  }
  if (targets.length) actions.push(
    { label: "剪切", icon: Scissors, shortcut: "Ctrl+X", divider: true, run: () => setClipboard(targets, true) },
    { label: "复制", icon: Copy, shortcut: "Ctrl+C", run: () => setClipboard(targets, false) },
    { label: "移动到…", icon: FolderInput, run: () => chooseDestination(targets, false) },
    { label: "复制到…", icon: Copy, run: () => chooseDestination(targets, true) },
    ...(entry ? [{ label: "重命名", icon: Pencil, shortcut: "F2", run: () => renameEntry(entry) }] : []),
    { label: "删除", icon: Trash2, shortcut: "Del", danger: true, run: () => removeEntries(targets) },
  );
  if (!targets.length || entry?.directory) {
    const parent = entry?.directory ? entry.path : directory;
    actions.push({ label: "粘贴", icon: ClipboardPaste, shortcut: "Ctrl+V", disabled: !clipboard.value?.entries.length, divider: true, run: () => paste(parent) }, { label: "新建文件", icon: FilePlus2, run: () => createEntry(false, parent) }, { label: "新建文件夹", icon: FolderPlus, run: () => createEntry(true, parent) });
    if (!targets.length) actions.push({ label: "上传文件", icon: Upload, run: () => pickUpload(directory) }, { label: "刷新", icon: RefreshCw, shortcut: "F5", run: refresh });
  }
  actions.push({ label: "复制路径", icon: Copy, divider: true, run: () => copyPath(targets, directory) });
  if (entry) actions.push({ label: "属性", icon: Info, run: () => { properties.value = entry; } });
  return actions;
});
async function openMenu(event: MouseEvent, entry?: FileEntry, fromTree = false) {
  event.preventDefault(); event.stopPropagation();
  if (busy.value || uploading.value) return;
  if (entry && (fromTree || !selected.value.includes(entry.path))) selected.value = [entry.path];
  if (!entry) selected.value = [];
  const root = entry?.path === "/";
  if (root) selected.value = [];
  menu.value = { x: event.clientX, y: event.clientY, targets: entry && !root ? (fromTree ? [entry] : selectedEntries.value) : [], directory: root ? "/" : currentPath.value };
  await nextTick();
  if (menu.value && menuElement.value) { Object.assign(menu.value, contextMenuPosition(menu.value, menuElement.value)); menuElement.value.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus({ preventScroll: true }); }
}
function runMenu(action: MenuAction) { menu.value = undefined; list.value?.focus(); if (!action.disabled) void action.run(); }
function menuKey(event: KeyboardEvent) { contextMenuKey(event, menuElement.value, () => { menu.value = undefined; list.value?.focus({ preventScroll: true }); }); }
function listKey(event: KeyboardEvent) {
  if ((event.target as HTMLElement).closest("input,textarea") || busy.value || uploading.value || menu.value) return;
  const key = event.key.toLowerCase(), control = event.ctrlKey || event.metaKey;
  if (control && ["a", "c", "x", "v"].includes(key)) { event.preventDefault(); if (key === "a") selected.value = shownEntries.value.map(entry => entry.path); else if (key === "v") void paste(); else if (selectedEntries.value.length) setClipboard(selectedEntries.value, key === "x"); }
  else if (event.key === "Delete") { event.preventDefault(); void removeEntries(selectedEntries.value); }
  else if (event.key === "F2" && selectedEntries.value.length === 1) { event.preventDefault(); void renameEntry(selectedEntries.value[0]); }
  else if (event.key === "F5") { event.preventDefault(); void refresh(); }
  else if (event.key === "Enter" && selectedEntries.value.length === 1) { event.preventDefault(); activateEntry(selectedEntries.value[0]); }
  else if ((event.altKey && event.key === "ArrowUp") || event.key === "Backspace") { event.preventDefault(); void listFiles(parentRemotePath(currentPath.value)); }
  else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault(); const index = shownEntries.value.findIndex(entry => entry.path === selected.value.at(-1)); const entry = shownEntries.value[Math.max(0, Math.min(shownEntries.value.length - 1, index + (event.key === "ArrowDown" ? 1 : -1)))];
    if (entry) { selected.value = [entry.path]; anchor.value = entry.path; void nextTick(() => list.value?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" })); }
  } else if (event.key === "Escape") selected.value = [];
  else if ((event.shiftKey && event.key === "F10") || event.key === "ContextMenu") { event.preventDefault(); const rect = list.value?.getBoundingClientRect(); void openMenu(new MouseEvent("contextmenu", { clientX: (rect?.x || 0) + 80, clientY: (rect?.y || 0) + 60 }), selectedEntries.value[0]); }
}
function startDrag(event: DragEvent, entry: FileEntry) {
  if (busy.value || uploading.value) { event.preventDefault(); return; }
  if (!selected.value.includes(entry.path)) selected.value = [entry.path];
  dragged.value = [...selectedEntries.value]; menu.value = undefined;
  event.dataTransfer?.setData("application/x-velin-files", JSON.stringify(dragged.value.map(item => item.path)));
  if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
}
function allowDrop(event: DragEvent, directory: string) {
  if (busy.value || uploading.value) return;
  if (!event.dataTransfer?.types.some(type => type === "Files" || type === "application/x-velin-files")) return;
  if (dragged.value.some(entry => entry.directory && withinRemotePath(entry.path, directory))) return;
  event.preventDefault(); event.stopPropagation(); dropPath.value = directory;
  if (event.dataTransfer) event.dataTransfer.dropEffect = dragged.value.length ? "move" : "copy";
}
async function drop(event: DragEvent, directory: string) {
  event.preventDefault(); event.stopPropagation(); dropPath.value = "";
  if (busy.value || uploading.value) return;
  if (event.dataTransfer?.files.length) {
    if ([...event.dataTransfer.items].some(item => item.webkitGetAsEntry?.()?.isDirectory)) { ElMessage.warning("请拖入文件；文件夹可先新建，再上传其中的文件"); return; }
    await uploadFiles(Array.from(event.dataTransfer.files), directory);
  }
  else if (dragged.value.length) { const targets = [...dragged.value]; dragged.value = []; await transfer(targets, directory, false); }
}
let stopResize: (() => void) | undefined;
function resize(event: PointerEvent) {
  if (event.button !== 0) return;
  event.preventDefault(); const start = event.clientX, width = sidebarWidth.value;
  const move = (e: PointerEvent) => { const max = Math.max(170, Math.min(360, (sidebar.value?.parentElement?.clientWidth || 900) - 360)); sidebarWidth.value = Math.min(max, Math.max(150, width + e.clientX - start)); };
  const stop = () => { window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", stop); window.removeEventListener("pointercancel", stop); stopResize = undefined; };
  stopResize?.(); stopResize = stop; window.addEventListener("pointermove", move); window.addEventListener("pointerup", stop); window.addEventListener("pointercancel", stop);
}

function uploadChunk(pathname: string, file: Blob, offset: number, overwrite: boolean, signal: AbortSignal, progress: (bytes: number) => void) {
  return new Promise<number>((resolve, reject) => {
    if (signal.aborted) { reject(new DOMException("传输已停止", "AbortError")); return; }
    const request = new XMLHttpRequest();
    request.open("PUT", `/api/sftp/${props.host!.id}/upload?path=${encodeURIComponent(pathname)}&offset=${offset}&overwrite=${overwrite}${sessionQuery.value}`);
    request.timeout = 90_000;
    const abort = () => request.abort();
    signal.addEventListener("abort", abort, { once: true });
    const cleanup = () => signal.removeEventListener("abort", abort);
    request.upload.onprogress = event => progress(event.loaded);
    request.onload = () => {
      cleanup();
      if (request.status < 200 || request.status >= 300) {
        let message = "分块上传失败"; try { message = JSON.parse(request.responseText).message || message; } catch { /* Use the fallback. */ }
        reject(new Error(message)); return;
      }
      try { resolve(Number(JSON.parse(request.responseText).offset) || offset + file.size); } catch { resolve(offset + file.size); }
    };
    request.onerror = () => { cleanup(); reject(new Error("网络连接中断")); };
    request.ontimeout = () => { cleanup(); reject(new Error("上传超时，请检查主机连接后重试")); };
    request.onabort = () => { cleanup(); reject(new DOMException("传输已停止", "AbortError")); };
    void csrfHeaders().then(headers => {
      if (signal.aborted) { cleanup(); reject(new DOMException("传输已停止", "AbortError")); return; }
      Object.entries(headers).forEach(([key, value]) => request.setRequestHeader(key, value)); request.send(file);
    }).catch(cause => { cleanup(); reject(cause); });
  });
}
async function uploadOne(queueItem: TransferItem, signal: AbortSignal) {
  if (!props.host) return;
  const file = queueItem.file, directory = queueItem.directory;
  const names = new Set((await files.value.list(directory)).entries.map(item => item.name));
  const name = await availableFileName(file.name, false, names);
  if (!name) { queueItem.status = "已跳过"; return; }
  if (signal.aborted) throw new DOMException("传输已停止", "AbortError");
  const target = joinRemotePath(directory, name);
  queueItem.name = name; queueItem.error = "";
  let offset = 0;
  const chunkSize = 8 * 1024 * 1024;
  if (!file.size) await uploadChunk(target, file, 0, false, signal, () => {});
  while (offset < file.size) {
    const end = Math.min(file.size, offset + chunkSize);
    const next = await uploadChunk(target, file.slice(offset, end), offset, offset > 0, signal, bytes => {
      queueItem.progress = Math.min(99, Math.round((offset + bytes) / file.size * 100)); queueItem.status = `${queueItem.progress}%`;
    });
    if (next <= offset || next > file.size) throw new Error("上传偏移异常，请重新读取目录后重试");
    offset = next;
    queueItem.progress = Math.min(100, Math.round(offset / file.size * 100)); queueItem.status = `${queueItem.progress}%`;
  }
  queueItem.progress = 100; queueItem.status = "完成";
}
async function runUploads(items: TransferItem[]) {
  if (uploading.value || busy.value || !props.host) return;
  uploading.value = true; uploadAbort = new AbortController(); const signal = uploadAbort.signal;
  try {
    for (const item of items) {
      if (signal.aborted) { item.status = "已停止"; continue; }
      item.status = "准备中"; item.error = "";
      try { await uploadOne(item, signal); }
      catch (cause) {
        item.status = signal.aborted ? "已停止" : cause === "cancel" || cause === "close" ? "已取消" : "失败";
        if (item.status === "失败") item.error = cause instanceof Error ? cause.message : "上传失败";
      }
    }
    await refresh();
  } finally { uploading.value = false; if (fileInput.value) fileInput.value.value = ""; }
}
function retryTransfer(item: TransferItem) { item.progress = 0; void runUploads([item]); }
function pickUpload(directory = currentPath.value) { uploadTarget = directory; fileInput.value?.click(); }
async function uploadFile(event: Event) {
  const directory = uploadTarget || currentPath.value; uploadTarget = undefined;
  await uploadFiles(Array.from((event.target as HTMLInputElement).files || []), directory);
}

async function uploadFiles(selectedFiles: File[], directory = currentPath.value) {
  if (!props.host || uploading.value || busy.value || !selectedFiles.length) return;
  uploadQueue.value = selectedFiles.map(file => ({ name: file.name, file, directory, progress: 0, status: "等待", error: "" }));
  await runUploads(uploadQueue.value);
}
</script>

<template>
  <el-dialog :model-value="modelValue" class="file-manager-dialog explorer-dialog" @close-auto-focus="restoreFocus" :before-close="beforeClose" :title="`文件管理 · ${host?.name || '当前终端'}`" append-to-body :close-on-click-modal="false" @update:model-value="emit('update:modelValue', $event)">
    <div class="explorer" @pointerdown="menu = undefined">
      <div class="explorer-addressbar">
        <div class="explorer-navigation">
          <button title="后退" aria-label="后退" :disabled="historyIndex <= 0 || loading" @click="listFiles(history[historyIndex - 1], historyIndex - 1)"><ArrowLeft :size="16" /></button>
          <button title="前进" aria-label="前进" :disabled="historyIndex >= history.length - 1 || loading" @click="listFiles(history[historyIndex + 1], historyIndex + 1)"><ArrowRight :size="16" /></button>
          <button title="上一级 · Alt+↑" aria-label="上一级" :disabled="currentPath === '/' || loading" @click="listFiles(parentRemotePath(currentPath))"><ArrowUp :size="16" /></button>
        </div>
        <form class="explorer-address" @submit.prevent="listFiles(pathInput)"><Folder :size="15" /><input v-model="pathInput" aria-label="目录路径" spellcheck="false" autocomplete="off" @focus="($event.target as HTMLInputElement).select()" /><button type="submit" title="前往目录" aria-label="前往目录"><ArrowRight :size="14" /></button></form>
        <label class="explorer-search"><Search :size="15" /><input v-model="filter" aria-label="搜索当前目录" placeholder="搜索当前目录" /><button v-if="filter" aria-label="清除筛选" @click="filter = ''"><X :size="13" /></button></label>
      </div>
      <div class="explorer-toolbar">
        <div class="explorer-tools-primary">
          <button class="explorer-mobile-tree" aria-label="切换目录栏" :aria-pressed="showSidebar" @click="showSidebar = !showSidebar"><PanelLeft :size="16" /></button>
          <el-dropdown trigger="click" :disabled="busy || uploading" @command="createEntry($event === 'folder')"><button :disabled="busy || uploading"><FolderPlus :size="16" /><span>新建</span><ChevronDown :size="12" /></button><template #dropdown><el-dropdown-menu><el-dropdown-item command="folder" :icon="FolderPlus">新建文件夹</el-dropdown-item><el-dropdown-item command="file" :icon="FilePlus2">新建文件</el-dropdown-item></el-dropdown-menu></template></el-dropdown>
          <button :disabled="busy || uploading" @click="pickUpload()"><Upload :size="16" /><span>{{ uploading ? '上传中…' : '上传' }}</span></button>
          <span class="explorer-tool-divider" />
          <button class="explorer-clipboard-tool" title="剪切 · Ctrl+X" aria-label="剪切" :disabled="!selectedEntries.length || busy || uploading" @click="setClipboard(selectedEntries, true)"><Scissors :size="16" /></button>
          <button class="explorer-clipboard-tool" title="复制 · Ctrl+C" aria-label="复制" :disabled="!selectedEntries.length || busy || uploading" @click="setClipboard(selectedEntries, false)"><Copy :size="16" /></button>
          <button title="粘贴 · Ctrl+V" aria-label="粘贴" :disabled="!clipboard?.entries.length || busy || uploading" @click="paste()"><ClipboardPaste :size="16" /></button>
          <button title="重命名 · F2" aria-label="重命名" :disabled="selectedEntries.length !== 1 || busy || uploading" @click="renameEntry(selectedEntries[0])"><Pencil :size="16" /></button>
          <button title="删除 · Delete" aria-label="删除" :disabled="!selectedEntries.length || busy || uploading" @click="removeEntries(selectedEntries)"><Trash2 :size="16" /></button>
        </div>
        <div class="explorer-tools-secondary">
          <button title="显示隐藏文件" aria-label="显示隐藏文件" :aria-pressed="showHidden" @click="showHidden = !showHidden"><Eye :size="16" /><span class="explorer-hidden-label">隐藏文件</span><Check v-if="showHidden" :size="12" /></button>
          <button title="刷新 · F5" aria-label="刷新" :disabled="loading || busy" @click="refresh"><RefreshCw :size="16" :class="{ 'explorer-spinning': loading }" /></button>
        </div>
        <input ref="fileInput" hidden type="file" multiple @change="uploadFile" />
      </div>
      <OperationFeedback :pending="transferProgress" :error="renaming?.error || operationError" retry-label="重新连接并刷新" @retry="refresh" @dismiss="operationError = ''; if (renaming) renaming.error = ''" />
      <div class="explorer-panes" :style="{ '--explorer-sidebar': `${sidebarWidth}px` }" :class="{ 'show-directories': showSidebar }">
        <aside ref="sidebar" class="explorer-directories" aria-label="文件夹目录">
          <div class="explorer-directory-heading"><Folder :size="14" /><span>文件夹</span><button class="explorer-mobile-tree" aria-label="收起目录栏" @click="showSidebar = false"><X :size="14" /></button></div>
          <el-tree :key="treeKey" ref="tree" node-key="path" lazy :load="loadDirectories" :props="{ label: 'name' }" :default-expanded-keys="expandedPaths" :current-node-key="currentPath" highlight-current :expand-on-click-node="false" @node-click="(entry: FileEntry) => listFiles(entry.path)" @node-contextmenu="(event: MouseEvent, entry: FileEntry) => openMenu(event, entry, true)">
            <template #default="{ node, data }"><span class="explorer-tree-entry" :class="{ 'is-drop-target': dropPath === data.path }" :title="data.path" @dragover="allowDrop($event, data.path)" @dragleave="dropPath = ''" @drop="drop($event, data.path)"><component :is="node.expanded ? FolderOpen : Folder" :size="15" /><span>{{ data.name }}</span></span></template>
          </el-tree>
        </aside>
        <div role="separator" aria-label="调整文件夹栏宽度" aria-orientation="vertical" :aria-valuenow="sidebarWidth" :aria-valuemin="150" :aria-valuemax="360" tabindex="0" class="explorer-divider" @pointerdown="resize" @keydown.left.prevent="sidebarWidth = Math.max(150, sidebarWidth - 10)" @keydown.right.prevent="sidebarWidth = Math.min(360, sidebarWidth + 10)" @dblclick="sidebarWidth = 210" />
        <section class="explorer-files" aria-label="目录文件列表" :aria-busy="loading || busy">
          <div v-if="error" class="explorer-error" role="alert"><span>{{ error }}</span><button @click="listFiles(pathInput)">重试</button></div>
          <div class="explorer-columns" role="row">
            <button role="columnheader" :aria-sort="sortBy === 'name' ? descending ? 'descending' : 'ascending' : 'none'" @click="sort('name')">名称<ChevronDown v-if="sortBy === 'name'" :size="12" :class="{ reversed: descending }" /></button>
            <span role="columnheader" class="explorer-type">类型</span>
            <button role="columnheader" :aria-sort="sortBy === 'size' ? descending ? 'descending' : 'ascending' : 'none'" class="explorer-size" @click="sort('size')">大小<ChevronDown v-if="sortBy === 'size'" :size="12" :class="{ reversed: descending }" /></button>
            <button role="columnheader" :aria-sort="sortBy === 'modifiedAt' ? descending ? 'descending' : 'ascending' : 'none'" class="explorer-date" @click="sort('modifiedAt')">修改时间<ChevronDown v-if="sortBy === 'modifiedAt'" :size="12" :class="{ reversed: descending }" /></button>
            <span />
          </div>
          <div ref="list" role="listbox" aria-label="文件列表" aria-multiselectable="true" tabindex="0" class="explorer-file-list" :class="{ 'is-drop-target': dropPath === currentPath, 'is-loading': loading }" @contextmenu="openMenu($event)" @keydown="listKey" @click.self="selected = []" @dragover="allowDrop($event, currentPath)" @dragleave.self="dropPath = ''" @drop="drop($event, currentPath)">
            <div v-if="!shownEntries.length && !error" class="explorer-empty" @contextmenu="openMenu($event)"><component :is="filter ? Search : FolderOpen" :size="30" /><strong>{{ loading ? '正在读取目录…' : filter ? '没有匹配的文件' : '此文件夹为空' }}</strong><span v-if="!loading">{{ filter ? '试试其他名称，或清除筛选' : '右键新建文件，或将文件拖到这里上传' }}</span><button v-if="filter" @click="filter = ''">清除筛选</button></div>
            <div v-for="entry in shownEntries" :key="entry.path" class="explorer-file-row" role="option" :aria-label="entry.name" :aria-selected="selected.includes(entry.path)" :class="{ 'is-directory': entry.directory, 'is-file': !entry.directory, 'is-selected': selected.includes(entry.path), 'is-cut': clipboard?.cut && clipboard.entries.some(item => item.path === entry.path), 'is-drop-target': dropPath === entry.path, 'is-hidden': entry.name.startsWith('.') }" :draggable="!busy && !uploading && !renaming" @click="selectEntry(entry, $event); list?.focus()" @dblclick="activateEntry(entry)" @contextmenu="openMenu($event, entry)" @dragstart="startDrag($event, entry)" @dragend="dragged = []; dropPath = ''" @dragover="entry.directory && allowDrop($event, entry.path)" @dragleave="dropPath = ''" @drop="entry.directory ? drop($event, entry.path) : undefined">
              <span class="explorer-name" :title="entry.name"><component :is="entry.directory ? Folder : FileIcon" :size="17" :class="entry.directory ? 'explorer-folder-icon' : 'explorer-file-icon'" /><InlineFileRename v-if="renaming?.path === entry.path" :name="entry.name" :directory="entry.directory" :busy="busy" :error="renaming?.error" @confirm="finishRename(entry, $event)" @cancel="renaming = undefined; list?.focus({ preventScroll: true })" /><span v-else>{{ entry.name }}</span><small v-if="entry.symlink">↗</small></span>
              <span class="explorer-type">{{ typeLabel(entry) }}</span><span class="explorer-size">{{ entry.directory ? '—' : formatBytes(entry.size) }}</span><span class="explorer-date" :title="dateLabel(entry.modifiedAt)">{{ dateLabel(entry.modifiedAt) }}</span>
              <button class="explorer-row-menu" :aria-label="`${entry.name} 的操作`" title="更多操作" @pointerdown.stop @click.stop="openMenu($event, entry)" @dblclick.stop><MoreHorizontal :size="16" /></button>
            </div>
          </div>
        </section>
      </div>
      <div class="explorer-status" role="status"><span>{{ filter ? `${shownEntries.length} / ${entries.filter(item => showHidden || !item.name.startsWith('.')).length}` : shownEntries.length }} 个项目<span v-if="selectedEntries.length"> · 已选择 {{ selectedEntries.length }} 项<span v-if="selectedSize">，{{ formatBytes(selectedSize) }}</span></span></span><span v-if="busy">正在处理…</span><span v-else-if="clipboard?.entries.length">已{{ clipboard.cut ? '剪切' : '复制' }} {{ clipboard.entries.length }} 项，选择目标目录后粘贴</span><span v-else class="explorer-status-hint">双击打开 · 右键操作 · 拖动移动</span></div>
      <div v-if="uploadQueue.length" class="explorer-transfers"><div class="explorer-transfer-title"><span>文件传输 · {{ uploadQueue.filter(item => item.status === '完成').length }}/{{ uploadQueue.length }}</span><button v-if="uploading" @click="uploadAbort?.abort()">停止传输</button><button v-if="!uploading" aria-label="收起传输记录" @click="uploadQueue = []"><X :size="14" /></button></div><div class="explorer-transfer-items"><div v-for="(item, index) in uploadQueue" :key="index" class="explorer-transfer"><span :title="item.name">{{ item.name }}</span><el-progress :percentage="item.progress" :show-text="false" :stroke-width="3" :status="item.status === '失败' ? 'exception' : item.status === '完成' ? 'success' : undefined" /><small :title="item.error">{{ item.status }}</small><button v-if="!uploading && ['失败', '已停止'].includes(item.status)" :aria-label="`重试上传 ${item.name}`" :title="item.error || '重新上传，已有文件将询问新名称'" @click="retryTransfer(item)">重试</button></div></div></div>
    </div>
    <div v-if="menu" ref="menuElement" role="menu" aria-label="文件管理操作" class="explorer-context-menu" :style="{ left: `${menu.x}px`, top: `${menu.y}px` }" @pointerdown.stop @contextmenu.prevent @keydown="menuKey"><template v-for="action in menuActions" :key="action.label"><div v-if="action.divider" class="explorer-menu-divider" role="separator" /><button role="menuitem" :disabled="action.disabled" :class="{ danger: action.danger }" @click="runMenu(action)"><component :is="action.icon" :size="14" /><span>{{ action.label }}</span><kbd v-if="action.shortcut">{{ action.shortcut }}</kbd></button></template></div>
    <TextFileEditorDialog v-model="editorOpen" :host="host" :session-id="sessionId" :file="editingFile" :initial-mode="editorMode" @saved="refresh" />
    <ImagePreviewDialog v-model="imagePreviewOpen" :host="host" :session-id="sessionId" :file="previewingImage" />
    <RemoteDirectoryPicker v-if="pickerOpen && host" v-model="pickerOpen" :hostID="host.id" :sessionID="sessionId || ''" root-path="/" :initial-path="currentPath" :disabled-paths="pickerEntries.filter(entry => entry.directory).map(entry => entry.path)" :title="pickerCopy ? '复制到文件夹' : '移动到文件夹'" @select="transfer(pickerEntries, $event, pickerCopy)" />
    <el-dialog :model-value="!!properties" title="属性" width="min(460px, calc(100vw - 28px))" append-to-body :close-on-click-modal="false" @update:model-value="properties = undefined"><dl v-if="properties" class="explorer-properties"><dt>名称</dt><dd>{{ properties.name }}</dd><dt>位置</dt><dd>{{ properties.path }}</dd><dt>类型</dt><dd>{{ typeLabel(properties) }}</dd><dt>大小</dt><dd>{{ properties.directory ? '—' : `${formatBytes(properties.size)}（${properties.size.toLocaleString()} 字节）` }}</dd><dt>权限</dt><dd>{{ properties.mode || '—' }}</dd><dt>修改时间</dt><dd>{{ dateLabel(properties.modifiedAt) }}</dd></dl><template #footer><el-button @click="properties = undefined">关闭</el-button></template></el-dialog>
  </el-dialog>
</template>

<style>
.el-dialog.file-manager-dialog.explorer-dialog { width: min(1100px, calc(100vw - 28px)) !important; padding: 0 !important; border: 1px solid var(--line) !important; border-radius: 6px !important; overflow: visible; margin: max(20px, calc((100dvh - 750px) / 2)) auto !important; }
.el-dialog.explorer-dialog > .el-dialog__header { margin: 0; padding: 12px 42px 12px 16px !important; border-bottom: 1px solid var(--line); }
.el-dialog.explorer-dialog > .el-dialog__header .el-dialog__title { font-size: 13px; font-weight: 500; }
.el-dialog.explorer-dialog > .el-dialog__header .el-dialog__headerbtn { top: 0; right: 0; width: 42px; height: 43px; }
.el-dialog.file-manager-dialog.explorer-dialog > .el-dialog__body { padding: 0 !important; max-height: none; overflow: visible; }
.explorer { display: flex; flex-direction: column; height: min(680px, calc(100dvh - 100px)); min-height: 260px; color: var(--text); font-size: 12px; }
.explorer button, .explorer-context-menu button { display: inline-flex; align-items: center; justify-content: center; gap: 7px; border: 0; border-radius: 3px; padding: 6px; min-height: 28px; color: var(--text); background: transparent; font: inherit; white-space: nowrap; cursor: pointer; box-shadow: none; }
.explorer button:hover:not(:disabled), .explorer-context-menu button:hover:not(:disabled) { background: var(--surface-hover); }
.explorer button:focus-visible, .explorer-context-menu button:focus-visible { outline: 1px solid var(--accent); outline-offset: -1px; background: var(--surface-hover); }
.explorer button:disabled, .explorer-context-menu button:disabled { opacity: .35; cursor: default; }
.explorer button[aria-pressed="true"] { background: var(--accent-surface); color: var(--accent); }
.explorer svg, .explorer-context-menu svg { flex: 0 0 auto; }
.explorer-addressbar { display: flex; gap: 10px; align-items: center; padding: 10px 12px 8px; }
.explorer-navigation { display: flex; gap: 2px; flex-shrink: 0; }
.explorer-navigation button { color: var(--muted); }
.explorer-address, .explorer-search { display: flex; align-items: center; gap: 8px; height: 30px; min-width: 0; border: 1px solid var(--line); border-radius: 3px; padding: 0 8px; color: var(--muted); background: var(--surface-2); }
.explorer-address:focus-within, .explorer-search:focus-within { border-color: var(--accent); }
.explorer-address { flex: 1; }
.explorer-address input, .explorer-search input { border: 0; outline: 0; border-radius: 0; box-shadow: none; padding: 0; height: 26px; min-height: 0; min-width: 0; flex: 1; width: 100%; background: transparent; color: var(--text); font-size: 12px; }
.explorer-address input { font-family: var(--font-mono); }
.explorer-address button, .explorer-search button { padding: 2px; min-height: 24px; color: var(--muted); }
.explorer-search { width: 205px; flex-shrink: 0; }
.explorer-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 0 12px 8px; border-bottom: 1px solid var(--line); }
.explorer-tools-primary, .explorer-tools-secondary { display: flex; align-items: center; gap: 3px; }
.explorer-tools-primary > button, .explorer-tools-secondary > button { min-width: 30px; }
.explorer-tools-primary > button:has(span), .explorer-tools-primary .el-dropdown button { padding-inline: 9px; }
.explorer-tool-divider { width: 1px; height: 16px; background: var(--line); margin: 0 5px; }
.explorer-panes { flex: 1; min-height: 0; display: grid; grid-template-columns: var(--explorer-sidebar) 5px minmax(0, 1fr); position: relative; }
.explorer-directories { min-width: 0; overflow: auto; padding: 10px 6px 8px; background: var(--surface-2); }
.explorer-directory-heading { display: flex; align-items: center; gap: 8px; height: 26px; padding: 0 10px; margin-bottom: 5px; font-size: 11px; color: var(--muted); }
.explorer-directory-heading > span { flex: 1; }
.explorer-directories .el-tree { background: transparent; --el-tree-node-content-height: 29px; --el-tree-text-color: var(--text); --el-tree-node-hover-bg-color: var(--surface-hover); }
.explorer-directories .el-tree-node__content { border-radius: 3px; }
.explorer-directories .el-tree-node.is-current > .el-tree-node__content { background: var(--accent-surface); }
.explorer-tree-entry { min-width: 0; display: flex; align-items: center; gap: 7px; flex: 1; height: 29px; font-size: 12px; padding-right: 8px; }
.explorer-tree-entry > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.explorer-tree-entry svg, .explorer-folder-icon { --icon-color: var(--icon-warm) !important; color: var(--icon-warm); fill: color-mix(in srgb, var(--icon-warm) 16%, transparent); }
.explorer-divider { cursor: col-resize; background: linear-gradient(to right, var(--line) 1px, transparent 1px); touch-action: none; }
.explorer-divider:hover, .explorer-divider:focus-visible { background: var(--accent-border); outline: none; }
.explorer-files { display: flex; flex-direction: column; min-width: 0; min-height: 0; }
.explorer-columns, .explorer-file-row { display: grid; grid-template-columns: minmax(140px, 1fr) 85px 80px 160px 28px; column-gap: 12px; align-items: center; padding: 0 12px; }
.explorer-columns { flex: 0 0 33px; border-bottom: 1px solid var(--line); color: var(--muted); font-size: 11px; }
.explorer-columns > button { justify-content: flex-start; color: var(--muted); padding: 0; min-width: 0; font-size: 11px; }
.explorer-columns .reversed { transform: rotate(180deg); }
.explorer-file-list { flex: 1; overflow: auto; min-height: 0; outline: none; padding-bottom: 36px; position: relative; overscroll-behavior: contain; }
.explorer-file-list:focus-visible { box-shadow: inset 0 0 0 1px var(--accent-border); }
.explorer-file-row { min-height: 34px; border: 1px solid transparent; border-width: 1px 0; user-select: none; cursor: default; }
.explorer-file-row.is-directory { background: color-mix(in srgb, var(--icon-warm) 3%, transparent); }
.explorer-file-row:hover { background: var(--surface-hover); }
.explorer-file-row.is-directory:hover { background: color-mix(in srgb, var(--icon-warm) 8%, var(--surface-hover)); }
.explorer-file-row.is-selected { background: var(--accent-surface); border-color: var(--accent-border); }
.explorer-file-row.is-cut { opacity: .45; }
.explorer-name { display: flex; align-items: center; gap: 8px; min-width: 0; }
.explorer-name > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.explorer-file-row.is-directory .explorer-name > span { font-weight: 600; color: var(--text); }
.explorer-file-icon { --icon-color: var(--icon-cool) !important; color: var(--icon-cool); opacity: .88; }
.explorer-file-row.is-directory .explorer-type { color: var(--icon-warm); font-weight: 500; }
.explorer-name > small { color: var(--muted); }
.explorer-file-row .explorer-type, .explorer-file-row .explorer-size, .explorer-file-row .explorer-date { color: var(--muted); font-size: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.explorer-size { font-variant-numeric: tabular-nums; text-align: right; }
.explorer-columns button.explorer-size { justify-content: flex-end; }
.explorer-file-row .explorer-row-menu { min-height: 26px; height: 26px; padding: 3px; color: var(--muted); opacity: 0; }
.explorer-file-row:hover .explorer-row-menu, .explorer-file-row.is-selected .explorer-row-menu, .explorer-row-menu:focus-visible { opacity: 1; }
.explorer-empty { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; min-height: 220px; height: 100%; padding: 20px; text-align: center; color: var(--muted); }
.explorer-empty > svg { opacity: .5; }
.explorer-empty strong { font-size: 13px; font-weight: 500; color: var(--text); }
.explorer-empty > span { font-size: 11px; }
.explorer-status { display: flex; gap: 12px; align-items: center; justify-content: space-between; border-top: 1px solid var(--line); min-height: 30px; padding: 5px 12px; font-size: 11px; color: var(--muted); }
.explorer-status > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.explorer-error { display: flex; gap: 8px; align-items: center; justify-content: space-between; padding: 5px 12px; background: var(--danger-soft); color: var(--danger); font-size: 12px; overflow-wrap: anywhere; }
.explorer-error button { color: inherit; flex-shrink: 0; }
.explorer-transfers { border-top: 1px solid var(--line); padding: 6px 12px; }
.explorer-transfer-title { display: flex; justify-content: space-between; align-items: center; font-size: 11px; color: var(--muted); }
.explorer-transfer-items { max-height: 95px; overflow: auto; }
.explorer-transfer { display: grid; grid-template-columns: minmax(80px, 1fr) minmax(40px, 200px) 50px auto; gap: 12px; align-items: center; min-height: 26px; font-size: 11px; }
.explorer-transfer > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.explorer-transfer > small { text-align: right; color: var(--muted); }
.explorer .is-drop-target { background: var(--accent-surface); box-shadow: inset 0 0 0 1px var(--accent); }
.explorer-context-menu { position: fixed; z-index: 5; width: 220px; max-height: calc(100dvh - 16px); overflow-y: auto; background: var(--surface); color: var(--text); border: 1px solid var(--line); border-radius: 4px; padding: 4px; box-shadow: 0 8px 28px #0004; font-size: 12px; }
.explorer-context-menu > button { display: flex; justify-content: flex-start; width: 100%; min-height: 28px; padding: 5px 8px; gap: 9px; }
.explorer-context-menu > button > span { flex: 1; text-align: left; }
.explorer-context-menu kbd { font: 10px var(--font-mono); color: var(--muted); border: none; background: none; box-shadow: none; }
.explorer-context-menu .danger { color: var(--danger); }
.explorer-menu-divider { height: 1px; background: var(--line); margin: 4px 3px; }
.explorer-properties { display: grid; grid-template-columns: 64px minmax(0, 1fr); gap: 14px 10px; font-size: 12px; }
.explorer-properties dt { color: var(--muted); }
.explorer-properties dd { margin: 0; overflow-wrap: anywhere; user-select: text; }
.explorer .explorer-mobile-tree { display: none; }
.explorer-spinning { animation: explorer-spin 1.2s linear infinite; }
@keyframes explorer-spin { to { transform: rotate(360deg); } }
@media (max-width: 980px) { .explorer-columns, .explorer-file-row { grid-template-columns: minmax(100px, 1fr) 70px 145px 24px; gap: 10px; } .explorer-type { display: none; } .explorer-search { width: 170px; } }
@media (max-width: 760px) {
  .el-dialog.file-manager-dialog.explorer-dialog { width: calc(100vw - 16px) !important; margin: 12px auto !important; }
  .explorer { height: calc(100dvh - 80px); }
  .explorer-addressbar { flex-wrap: wrap; gap: 6px; padding: 8px; }
  .explorer-address { flex-basis: calc(100% - 110px); }
  .explorer-search { width: 100%; }
  .explorer-toolbar { padding: 0 8px 6px; gap: 2px; }
  .explorer-tools-primary, .explorer-tools-secondary { gap: 0; }
  .explorer-tool-divider { margin: 0 2px; }
  .explorer-hidden-label, .explorer-tools-primary > .explorer-clipboard-tool, .explorer-tools-primary > button > span { display: none; }
  .explorer-tools-primary > button:has(span) { padding-inline: 6px; }
  .explorer-panes { grid-template-columns: minmax(0, 1fr); }
  .explorer-directories, .explorer-divider { display: none; }
  .explorer-panes.show-directories .explorer-directories { display: block; position: absolute; inset: 0 auto 0 0; width: min(280px, 85%); z-index: 3; border-right: 1px solid var(--line); box-shadow: 8px 0 24px #0003; }
  .explorer .explorer-mobile-tree { display: inline-flex; }
  .explorer-columns, .explorer-file-row { grid-template-columns: minmax(90px, 1fr) 70px 26px; padding-inline: 10px; }
  .explorer .explorer-columns .explorer-date, .explorer .explorer-file-row .explorer-date { display: none; }
  .explorer-file-row { min-height: 40px; }
  .explorer-file-row .explorer-row-menu { opacity: 1; }
  .explorer-status { font-size: 10px; padding-inline: 10px; }
  .explorer-status-hint { display: none; }
}
@media (prefers-reduced-motion: reduce) { .explorer-spinning { animation: none; } }
</style>
