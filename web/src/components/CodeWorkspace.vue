<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { Check, ChevronDown, ChevronsDownUp, CodeXml, File, FileDiff, Files, FolderOpen, GitBranch, GitPullRequest, RefreshCw, Save, Search, Settings2, Sparkles, TerminalSquare, X } from "@lucide/vue";
import CodeProjectSearch from "./CodeProjectSearch.vue";
import { useAuthStore } from "../stores/auth";
import { loadWorkspaceState, saveWorkspaceState, workspaceStateKey, type WorkspaceDocumentState } from "../codeWorkspaceState";
import { replaceCodeHunk, type CodeHunk } from "../codeHunks";
import { ApiError } from "../api";
import { createRemoteFiles, fileName, joinRemotePath, relativeRemotePath, withinRemotePath, type RemoteFileEntry } from "../remoteFiles";
import { createRemoteGit } from "../remoteGit";
import { type GitBranch as GitBranchInfo, type GitChange, type GitStatus } from "../git";
import { codeLanguage } from "../codeLanguages";
import type { Host } from "../types";
import RemoteCodeEditor from "./RemoteCodeEditor.vue";
import CodeDiffDialog from "./CodeDiffDialog.vue";
import RemoteFileTree, { type FileMutation } from "./RemoteFileTree.vue";
import CodeGitChanges from "./CodeGitChanges.vue";

const GitDialog = defineAsyncComponent(() => import("./GitDialog.vue"));
const CodeFileAnalysis = defineAsyncComponent(() => import("./CodeFileAnalysis.vue"));
const props = defineProps<{ host: Host; sessionId: string; rootPath: string; visible: boolean; locked?: boolean }>();
const emit = defineEmits<{ dirty: [value: boolean]; terminal: []; files: [path: string]; dock: [target: HTMLElement | undefined] }>();
const stateKey = workspaceStateKey(useAuthStore().user?.id || "local", props.host.id, props.rootPath);
const remembered = loadWorkspaceState(stateKey);
const restoring = ref(true), storageError = ref("");
const projectSearch = ref<InstanceType<typeof CodeProjectSearch>>();
const terminalOpen = ref(Boolean(remembered.terminalOpen));
const terminalHeight = ref(Math.max(120, Math.min(500, Number(remembered.terminalHeight) || 240)));
const dockElement = ref<HTMLElement>();
let persistenceTimer: number | undefined;
let autoRefreshTimer: number | undefined;
const workspaceElement = ref<HTMLElement>();
const files = createRemoteFiles(props.host.id, props.sessionId);
const git = createRemoteGit(props.host.id);
type FileDocument = { id: string; kind: "file"; path: string; content: string; saved: string; version: string; loading: boolean; saving: boolean; error: string; conflict: boolean; line: number; column: number; scroll: number; recovery?: WorkspaceDocumentState };
type DiffDocument = { id: string; kind: "diff"; path: string; left: string; right: string; leftLabel: string; rightLabel: string; loading: boolean; error: string; leftExists?: boolean; rightExists?: boolean; version?: string; group?: "staged" | "worktree" | "changes"; change?: GitChange };
type Document = FileDocument;
const documents = ref<Document[]>([]);
let documentSequence = 0;
const activeID = ref("");
const activeDocument = computed(() => documents.value.find(doc => doc.id === activeID.value));
const analysisOpen = ref(false), analysisID = ref("");
const analysisDocument = computed(() => documents.value.find(doc => doc.id === analysisID.value));
function analyzeFile(doc: FileDocument) { analysisID.value = doc.id; analysisOpen.value = true; }
function locateAnalysis(line: number, content: string) {
  const doc = analysisDocument.value;
  if (!doc || doc.content !== content) { ElMessage.warning("文件已变化，请重新分析"); return; }
  analysisOpen.value = false; activateDocument(doc);
  nextTick(() => editorRefs.get(doc.id)?.goTo(line));
}
const recoveryDocuments = computed(() => documents.value.filter(doc => doc.recovery?.draft !== undefined));
const dirtyDocuments = computed(() => documents.value.filter((doc): doc is FileDocument => doc.kind === "file" && doc.content !== doc.saved));
const panel = ref<"files" | "search" | "git">(["files", "search", "git"].includes(remembered.panel || "") ? remembered.panel! : "files");
const panelOpen = ref(remembered.panelOpen !== false);
const fileTree = ref<InstanceType<typeof RemoteFileTree>>();
const fileMutationBusy = ref(false);
const treeLoading = ref(false);
const expandedPaths = ref<string[]>(Array.isArray(remembered.expanded) ? remembered.expanded : []);
const treeFilter = ref("");
const rootName = computed(() => fileName(props.rootPath) || "/");
const gitStatus = ref<GitStatus>();
const repository = ref("");
const gitLoading = ref(false);
const gitError = ref("");
const gitBusy = ref("");
const aiCommitLoading = ref(false);
const gitChangeView = ref<"list" | "tree">(remembered.gitView === "tree" ? "tree" : "list");
const commitMessage = ref(remembered.commitMessage || "");
const gitRepositoryOpen = ref(true);
const commitDropdown = ref<{ handleClose: () => void }>();
const branchPopoverOpen = ref(false), branchLoading = ref(false), branchesLoaded = ref(false), branchError = ref("");
const workspaceBranches = ref<GitBranchInfo[]>([]);
const localBranches = computed(() => workspaceBranches.value.filter(item => !item.remote));
const remoteBranches = computed(() => workspaceBranches.value.filter(item => item.remote));
const fullGitRef = ref<{ confirmClose: () => Promise<boolean>; refresh: () => Promise<void>; saveCurrent: () => Promise<void>; selectTab: (tab: "status" | "history" | "branches") => void }>();
const fullGitDirty = ref(false), fullGitBusy = ref(false);
const gitTab = ref<"status" | "history" | "branches">("status");
const gitDialogOpen = ref(false);
const gitConflict = ref<GitChange>();
const diffOpen = ref(false);
const diffPreview = ref<DiffDocument>();
const panelWidth = ref(Number(remembered.width) || 260);
try { const saved = Number(localStorage.getItem("code-sidebar-width")); if (!remembered.width && saved >= 180 && saved <= 640) panelWidth.value = saved; } catch { /* Storage may be unavailable. */ }
const resizingPanel = ref(false);
let workspaceObserver: ResizeObserver | undefined;
function panelMaximum() { return Math.max(180, Math.min(640, (workspaceElement.value?.clientWidth || window.innerWidth) - 342)); }
function resizePanel(width: number, persist = true) {
  panelWidth.value = Math.round(Math.max(180, Math.min(panelMaximum(), width)));
  if (persist) try { localStorage.setItem("code-sidebar-width", String(panelWidth.value)); } catch { /* Optional preference. */ }
}
function stopPanelDrag() { resizingPanel.value = false; document.documentElement.classList.remove("code-panel-dragging"); }
function startPanelDrag(event: PointerEvent) {
  if (event.button !== 0) return;
  event.preventDefault();
  const target = event.currentTarget as HTMLElement;
  const startX = event.clientX, startWidth = panelWidth.value;
  target.setPointerCapture(event.pointerId);
  resizingPanel.value = true;
  document.documentElement.classList.add("code-panel-dragging");
  const move = (next: PointerEvent) => resizePanel(startWidth + next.clientX - startX);
  const end = () => { stopPanelDrag(); target.removeEventListener("pointermove", move); target.removeEventListener("pointerup", end); target.removeEventListener("pointercancel", end); target.removeEventListener("lostpointercapture", end); };
  target.addEventListener("pointermove", move); target.addEventListener("pointerup", end); target.addEventListener("pointercancel", end); target.addEventListener("lostpointercapture", end);
}
function panelResizeKey(event: KeyboardEvent) {
  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  resizePanel(event.key === "Home" ? 180 : event.key === "End" ? panelMaximum() : panelWidth.value + (event.key === "ArrowLeft" ? -1 : 1) * (event.shiftKey ? 50 : 10));
}
const quickOpen = ref(false);
const quickQuery = ref("");
const quickSelected = ref(0);
const quickInput = ref<HTMLInputElement>();
const indexedFiles = ref<RemoteFileEntry[]>([]);
const indexing = ref(false);
const indexLimited = ref(false);
const indexError = ref("");
const indexDone = ref(false);
const editorRefs = new Map<string, InstanceType<typeof RemoteCodeEditor>>();
let indexController: AbortController | undefined;
let disposed = false;
let gitRequest = 0;
let gitRefreshInFlight: Promise<void> | undefined;
let autoRefreshing = false;
watch([gitBusy, fullGitBusy, fileMutationBusy], () => { gitRequest++; }, { flush: "sync" });
const changes = computed(() => (gitStatus.value?.changes || []).filter(change => !isConflict(change)));
const conflicts = computed(() => (gitStatus.value?.changes || []).filter(isConflict));
const gitChangesByPath = computed(() => new Map((gitStatus.value?.changes || []).map(change => [joinRemotePath(repository.value, change.path), change])));
const searchResults = computed(() => {
  const query = (quickOpen.value ? quickQuery.value : treeFilter.value).toLowerCase().trim();
  return indexedFiles.value.filter(file => !query || relativeRemotePath(props.rootPath, file.path).toLowerCase().includes(query)).slice(0, 80);
});
watch([quickQuery, searchResults], () => { quickSelected.value = 0; });
function quickNavigate(event: KeyboardEvent) {
  if (!["ArrowDown", "ArrowUp", "Enter"].includes(event.key)) return;
  event.preventDefault();
  if (event.key === "Enter") { const entry = searchResults.value[quickSelected.value]; if (entry) openSearchResult(entry); return; }
  if (!searchResults.value.length) return;
  quickSelected.value = (quickSelected.value + (event.key === "ArrowDown" ? 1 : -1) + searchResults.value.length) % searchResults.value.length;
  nextTick(() => workspaceElement.value?.querySelector('.code-quick-result.active')?.scrollIntoView({ block: 'nearest' }));
}
function directoryLabel(path: string) { const relative = relativeRemotePath(props.rootPath, path); return relative.includes('/') ? relative.slice(0, relative.lastIndexOf('/')) : '.'; }
function isConflict(change: GitChange) { return change.index === "U" || change.worktree === "U" || change.index + change.worktree === "AA" || change.index + change.worktree === "DD"; }
function isDirty(doc: Document) { return doc.content !== doc.saved; }
function message(error: unknown) { return error instanceof Error ? error.message : "操作失败，请重试"; }
function badge(path: string) {
  const change = gitChangesByPath.value.get(path);
    return change ? isConflict(change) ? "!" : change.worktree === "?" ? "U" : change.worktree.trim() || change.index.trim() : "";
}
function setEditorRef(id: string, value: unknown) {
  if (value) editorRefs.set(id, value as InstanceType<typeof RemoteCodeEditor>);
  else editorRefs.delete(id);
}
function activateDocument(doc: Document) {
  activeID.value = doc.id;
  nextTick(() => editorRefs.get(doc.id)?.focus());
}
function invalidateIndex() { indexController?.abort(); indexDone.value = false; indexedFiles.value = []; }
function refreshTree() { fileTree.value?.refresh(); invalidateIndex(); }
async function beforeFileMutation(entry: RemoteFileEntry) {
  if (gitBusy.value || fullGitBusy.value || fullGitDirty.value) { ElMessage.warning("请先完成 Git 操作或保存合并文件"); return false; }
  if (documents.value.some(doc => withinRemotePath(entry.path, doc.path) && (doc.saving || doc.loading))) { ElMessage.warning("文件正在读取或保存，请稍后操作"); return false; }
  return true;
}
function deleteWarning(entry: RemoteFileEntry) {
  const count = documents.value.filter(doc => withinRemotePath(entry.path, doc.path) && (isDirty(doc) || doc.recovery)).length;
  return count ? `其中 ${count} 个打开的文件有未保存的修改，删除后这些修改也会被丢弃。` : "";
}
function filesChanged(change: FileMutation) {
  if (change.kind === "rename" || change.kind === "move") {
    for (const doc of documents.value) if (withinRemotePath(change.source, doc.path)) doc.path = change.destination! + doc.path.slice(change.source.length);
  } else if (change.kind === "delete") {
    documents.value = documents.value.filter(doc => !withinRemotePath(change.source, doc.path));
    if (!documents.value.some(doc => doc.id === activeID.value)) activeID.value = documents.value.at(-1)?.id || "";
  }
  if (diffPreview.value) {
    const path = diffPreview.value.group ? joinRemotePath(repository.value, diffPreview.value.path) : diffPreview.value.path;
    if ((change.kind === "rename" || change.kind === "move" || change.kind === "delete") && withinRemotePath(change.source, path)) diffOpen.value = false;
  }
  invalidateIndex();
  if (repository.value || panel.value === "git") void refreshGit(false);
  if (gitDialogOpen.value) void fullGitRef.value?.refresh();
}
function selectFile(entry: RemoteFileEntry) {
  if (entry.symlink) { ElMessage.info("符号链接请通过 SFTP 或终端处理"); return; }
  if (!entry.directory) void openFile(entry.path, entry.size);
}
async function openFile(path: string, size?: number, location?: { line: number; column: number }) {
  if (fileMutationBusy.value) return;
  if (window.innerWidth <= 640) panelOpen.value = false;
  const existing = documents.value.find(doc => doc.kind === "file" && doc.path === path);
  if (existing) { activateDocument(existing); if (location) nextTick(() => editorRefs.get(existing.id)?.goTo(location.line, location.column)); return; }
  if (size !== undefined && size > 2 * 1024 * 1024) { ElMessage.warning("文件超过 2 MiB 在线编辑上限"); return; }
  const doc = reactive<FileDocument>({ id: `file:${++documentSequence}`, kind: "file", path, content: "", saved: "", version: "", loading: true, saving: false, error: "", conflict: false, line: 1, column: 1, scroll: 0 });
  documents.value.push(doc);
  activateDocument(doc);
  await loadDocument(doc);
  if (location) { doc.line = location.line; doc.column = location.column; await nextTick(); editorRefs.get(doc.id)?.goTo(location.line, location.column); }
}
async function loadDocument(doc: FileDocument) {
  doc.loading = true; doc.error = "";
  try {
    const text = await files.read(doc.path);
    doc.content = text.content; doc.saved = text.content; doc.version = text.version; doc.conflict = false; doc.recovery = undefined;
  } catch (error) { doc.error = message(error); }
  finally { doc.loading = false; }
}
async function reloadDocument(doc: FileDocument) {
  if (isDirty(doc)) {
    try { await ElMessageBox.confirm("重新加载会放弃该文件尚未保存的修改。", "重新加载文件", { confirmButtonText: "重新加载", cancelButtonText: "取消", type: "warning" }); }
    catch { return; }
  }
  await loadDocument(doc);
}
async function saveDocument(doc: FileDocument): Promise<boolean> {
  if (fileMutationBusy.value) { ElMessage.info("文件操作正在进行，请稍后保存"); return false; }
  if (doc.loading || doc.saving || doc.error || !isDirty(doc)) return !isDirty(doc);
  const content = doc.content;
  const empty = !content && Boolean(doc.saved);
  if (empty) {
    try { await ElMessageBox.confirm("保存后远程文件将变为 0 字节，确认清空？", "保存空文件", { confirmButtonText: "确认清空", cancelButtonText: "取消", type: "warning" }); }
    catch { return false; }
  }
  doc.saving = true;
  try {
    const result = await files.save(doc.path, content, doc.version, empty);
    doc.saved = content; doc.version = result.version; doc.conflict = false;
    if (repository.value) void refreshGit(false);
    return true;
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      doc.conflict = true;
      ElMessage.warning("远程文件已变化，未覆盖。请比较远程版本后重新加载或合并。");
    } else ElMessage.error(message(error));
    return false;
  } finally { doc.saving = false; }
}
async function saveAll() {
  if (fullGitDirty.value) { await fullGitRef.value?.saveCurrent(); if (fullGitDirty.value) return false; }
  for (const doc of [...dirtyDocuments.value]) if (!(await saveDocument(doc))) return false;
  return true;
}
async function confirmDocumentClose(doc: Document) {
  if (doc.kind === "file" && doc.saving) return false;
  if (doc.recovery) { try { await ElMessageBox.confirm(`“${fileName(doc.path)}”还有待恢复的草稿，关闭会丢弃草稿。`, "关闭文件", { confirmButtonText: "丢弃草稿并关闭", cancelButtonText: "取消", closeOnClickModal: false }); } catch { return false; } }
  if (!isDirty(doc)) return true;
  try {
    await ElMessageBox.confirm(`“${fileName(doc.path)}”有未保存的修改。`, "关闭文件", {
      confirmButtonText: "保存并关闭", cancelButtonText: "放弃修改", distinguishCancelAndClose: true, type: "warning",
    });
    return doc.kind === "file" && await saveDocument(doc);
  } catch (action) { return action === "cancel"; }
}
async function closeDocument(doc: Document) {
  if (!(await confirmDocumentClose(doc))) return;
  const index = documents.value.indexOf(doc);
  documents.value.splice(index, 1);
  if (activeID.value === doc.id) activeID.value = documents.value[Math.min(index, documents.value.length - 1)]?.id || "";
}
async function confirmClose() {
  if (fileMutationBusy.value) { ElMessage.info("文件操作正在进行，请稍后关闭"); return false; }
  if (fullGitRef.value && !(await fullGitRef.value.confirmClose())) return false;
  if (documents.value.some(doc => doc.kind === "file" && doc.saving)) { ElMessage.info("文件正在保存，请稍后关闭"); return false; }
  if (!dirtyDocuments.value.length) return true;
  try {
    await ElMessageBox.confirm(`有 ${dirtyDocuments.value.length} 个文件尚未保存。`, "关闭代码编辑器", {
      confirmButtonText: "全部保存并关闭", cancelButtonText: "放弃修改", distinguishCancelAndClose: true, type: "warning",
    });
    return await saveAll();
  } catch (action) { if (action !== "cancel") return false; for (const doc of documents.value) { doc.content = doc.saved; doc.recovery = undefined; } persistWorkspace(); return true; }
}
function focus() {
  if (activeDocument.value?.kind === "file") editorRefs.get(activeID.value)?.focus();
  else workspaceElement.value?.focus();
}
async function refresh() { refreshTree(); await refreshCleanFiles(); if (repository.value || panel.value === "git") await refreshGit(); }
defineExpose({ confirmClose, focus, refresh });
async function refreshGit(connect = true, silent = false): Promise<void> {
  if (props.locked || disposed) return;
  if (gitRefreshInFlight) {
    if (silent) return;
    await gitRefreshInFlight;
    return refreshGit(connect, silent);
  }
  const request = ++gitRequest;
  if (!silent) { gitLoading.value = true; gitError.value = ""; }
  const task = (async () => {
    try {
      if (connect || !repository.value) await git.connect();
      const root = repository.value && !connect ? repository.value : await git.root(props.rootPath);
      const status = await git.status(root);
      if (disposed || props.locked || request !== gitRequest) return;
      repository.value = root; gitError.value = "";
      if (JSON.stringify(gitStatus.value) !== JSON.stringify(status)) {
        const previous = new Map(gitStatus.value?.changes.map(change => [change.path, change]));
        status.changes = status.changes.map(change => JSON.stringify(previous.get(change.path)) === JSON.stringify(change) ? previous.get(change.path)! : change);
        gitStatus.value = status;
      }
    } catch (error) {
      if (!silent && !disposed && request === gitRequest) gitError.value = message(error);
    } finally { if (!silent) gitLoading.value = false; }
  })();
  gitRefreshInFlight = task;
  try { await task; } finally { gitRefreshInFlight = undefined; }
}
async function gitOperation(key: string, operation: () => Promise<unknown>, success: string) {
  if (gitBusy.value || fullGitBusy.value || fileMutationBusy.value || gitLoading.value) return;
  if (fullGitDirty.value) { ElMessage.warning("请先保存或关闭正在编辑的合并文件"); return; }
  gitBusy.value = key;
  try {
    await operation();
    ElMessage.success(success);
    await gitChanged();
    if (gitDialogOpen.value) await fullGitRef.value?.refresh();
  } catch (error) { ElMessage.error(message(error)); }
  finally { gitBusy.value = ""; }
}
async function gitChanged() {
    await refreshGit(false);
    await refreshCleanFiles();
    refreshTree();
    const doc = diffPreview.value;
    if (diffOpen.value && doc?.change && doc.group) {
      const change = gitStatus.value?.changes.find(change => change.path === doc.path);
      if (change) { doc.change = change; await loadDiff(doc); }
      else diffOpen.value = false;
    }
}
async function refreshCleanFiles() {
  await Promise.all(documents.value.filter((doc): doc is FileDocument => doc.kind === "file" && !isDirty(doc) && !doc.loading && !doc.saving && !doc.recovery).map(async doc => {
    try {
      const path = doc.path;
      const text = await files.read(path);
      if (doc.path === path && !isDirty(doc) && !doc.saving && text.version !== doc.version) {
        doc.content = text.content; doc.saved = text.content; doc.version = text.version; doc.conflict = false;
      }
    } catch { /* Deleted files remain visible until explicitly reloaded. */ }
  }));
}
type CommitAction = "commit" | "amend" | "push" | "sync";
async function commit(action: CommitAction = "commit") {
  const text = commitMessage.value.trim();
  if (!text) return ElMessage.warning("请输入提交说明");
  if (conflicts.value.length) return ElMessage.warning("请先处理冲突");
  if (action !== "amend" && !changes.value.length) return ElMessage.warning("没有可提交的更改");
  if (action === "sync" && dirtyDocuments.value.length) return ElMessage.warning("请先保存或关闭未保存的文件，再同步代码");
  const success = action === "amend" ? "上次提交已修改" : action === "push" ? "提交已创建并推送" : action === "sync" ? "提交已创建并同步" : "提交已创建";
  await gitOperation(`commit-${action}`, async () => {
    if (changes.value.length) await git.stage(repository.value);
    await git.commit(repository.value, text, action === "amend");
    if (action === "push") await git.push(repository.value);
    if (action === "sync") { await git.pull(repository.value); await git.push(repository.value); }
    commitMessage.value = "";
  }, success);
}
function commitPrimary() { commitDropdown.value?.handleClose(); void commit(); }
function commitMenuAction(action: CommitAction) { commitDropdown.value?.handleClose(); void commit(action); }
async function sync(direction: "pull" | "push") {
  if (direction === "pull" && dirtyDocuments.value.length) return ElMessage.warning("请先保存或关闭未保存的文件，再拉取更新");
  try {
    await ElMessageBox.confirm(direction === "pull" ? "从上游拉取更新，仅允许快进合并。" : `推送分支“${gitStatus.value?.branch}”到上游？`, direction === "pull" ? "拉取更新" : "推送提交", { confirmButtonText: "确认", cancelButtonText: "取消" });
  } catch { return; }
  await gitOperation(direction, () => git[direction](repository.value), direction === "pull" ? "已拉取更新" : "已推送提交");
}
function toggleGitChangeView() {
  gitChangeView.value = gitChangeView.value === "tree" ? "list" : "tree";
}
async function loadBranches() {
  if (!repository.value || branchLoading.value) return;
  branchLoading.value = true; branchError.value = "";
  try { workspaceBranches.value = await git.branches(repository.value); branchesLoaded.value = true; }
  catch (error) { branchError.value = message(error); }
  finally { branchLoading.value = false; }
}
async function toggleBranchPopover() {
  if (branchPopoverOpen.value) { branchPopoverOpen.value = false; return; }
  await loadBranches();
  branchPopoverOpen.value = true;
}
async function switchWorkspaceBranch(item: GitBranchInfo) {
  if (item.current || gitBusy.value || fullGitBusy.value || fileMutationBusy.value) return;
  if (dirtyDocuments.value.length) { ElMessage.warning("请先保存或关闭未保存的文件，再切换分支"); return; }
  const localName = item.remote ? item.name.slice(item.name.indexOf("/") + 1) : item.name;
  const existing = item.remote
    ? localBranches.value.find(branch => branch.name === localName || branch.upstream === item.name)
    : undefined;
  branchPopoverOpen.value = false;
  await gitOperation(
    "switch-branch",
    () => item.remote && !existing
      ? git.switchRemoteBranch(repository.value, item.name)
      : git.switchBranch(repository.value, existing?.name || item.name),
    `已切换到 ${existing?.name || localName}`,
  );
  await loadBranches();
}
async function beforeFullGitOperation() {
  if (gitBusy.value || fileMutationBusy.value) return false;
  if (dirtyDocuments.value.length) { ElMessage.warning("请先保存或关闭未保存的文件，再进行仓库操作"); return false; }
  return true;
}
async function openGitManager(tab: "status" | "history" | "branches" = "status", conflict?: GitChange) {
  gitConflict.value = conflict;
  gitTab.value = tab;
  gitDialogOpen.value = true;
  await nextTick();
  fullGitRef.value?.selectTab(tab);
}
async function generateCommitMessage() {
  if (!repository.value || gitBusy.value || fullGitBusy.value || fileMutationBusy.value || gitLoading.value) return;
  if (conflicts.value.length) { ElMessage.warning("请先处理合并冲突，再生成提交说明"); return; }
  if (!changes.value.length) { ElMessage.warning("没有可分析的代码变更"); return; }
  aiCommitLoading.value = true; gitBusy.value = "ai-commit";
  try {
    if (!(await git.connect()).aiConfigured) { ElMessage.warning("请先在设置中配置 AI 模型"); return; }
    const result = await git.generateCommitMessage(repository.value, gitStatus.value?.branch || "HEAD");
    commitMessage.value = result.message;
    ElMessage.success("已生成提交说明，可编辑后提交");
  } catch (error) { ElMessage.error(message(error)); }
  finally { aiCommitLoading.value = false; gitBusy.value = ""; }
}
async function loadDiff(doc: DiffDocument) {
  if (!doc.change || !doc.group) return;
  doc.loading = true; doc.error = "";
  try {
    const change = doc.change;
    if (doc.group === "staged") {
      const [left, right] = await Promise.all([
        git.blob(repository.value, change.originalPath || change.path, "HEAD"),
        git.blob(repository.value, change.path, "0"),
      ]);
      doc.left = left.content; doc.right = right.content; doc.leftExists = left.exists; doc.rightExists = right.exists; doc.version = undefined;
    } else if (doc.group === "worktree") {
      const [left, right] = await Promise.all([
        change.index === "?" ? Promise.resolve({ content: "", exists: false }) : git.blob(repository.value, change.path, "0"),
        change.worktree === "D" ? Promise.resolve({ content: "", version: "" }) : files.read(joinRemotePath(repository.value, change.path)),
      ]);
      doc.left = left.content; doc.right = right.content; doc.leftExists = left.exists; doc.rightExists = change.worktree !== "D"; doc.version = right.version;
    } else {
      const deleted = change.worktree === "D" || (change.index === "D" && change.worktree === " ");
      const [left, right] = await Promise.all([
        change.index === "?" ? Promise.resolve({ content: "", exists: false }) : git.blob(repository.value, change.originalPath || change.path, "HEAD"),
        deleted ? Promise.resolve({ content: "", version: "" }) : files.read(joinRemotePath(repository.value, change.path)),
      ]);
      doc.left = left.content; doc.right = right.content; doc.leftExists = left.exists; doc.rightExists = !deleted; doc.version = right.version;
    }
  } catch (error) { doc.error = message(error); }
  finally { doc.loading = false; }
}
async function openDiff(change: GitChange, group: "staged" | "worktree" | "changes" = "changes") {
  const doc = reactive<DiffDocument>({ id: `diff:${group}:${change.path}`, kind: "diff", path: change.path, group, change,
    left: "", right: "", leftLabel: group === "staged" || group === "changes" ? "HEAD · 提交版本" : "Index · 暂存区",
    rightLabel: group === "staged" ? "Index · 已暂存" : "Working Tree · 远程文件", loading: true, error: "" });
  diffPreview.value = doc; diffOpen.value = true;
  await loadDiff(doc);
}
async function compareRemote(doc: FileDocument) {
  try {
    const remote = await files.read(doc.path);
    diffPreview.value = { id: `remote:${doc.path}`, kind: "diff", path: doc.path, left: remote.content, right: doc.content,
      leftLabel: "当前远程版本", rightLabel: "编辑器未保存内容", loading: false, error: "" };
    diffOpen.value = true;
  } catch (error) { ElMessage.error(message(error)); }
}
function editComparedFile() {
  const doc = diffPreview.value;
  if (!doc) return;
  diffOpen.value = false;
  void openFile(doc.group ? joinRemotePath(repository.value, doc.path) : doc.path);
}
async function indexFiles(force = false) {
  if (indexing.value || (indexDone.value && !force)) return;
  indexController?.abort();
  indexController = new AbortController();
  const signal = indexController.signal;
  indexing.value = true; indexError.value = ""; indexLimited.value = false;
  indexedFiles.value = [];
  const queue = [props.rootPath];
  const ignored = new Set([".git", "node_modules", "vendor", ".venv", "__pycache__", "dist", "build", ".next", "target"]);
  let visited = 0;
  try {
    while (queue.length && visited < 150 && indexedFiles.value.length < 5000 && !signal.aborted) {
      const batch = queue.splice(0, Math.min(4, 150 - visited)); visited += batch.length;
      const listings = await Promise.allSettled(batch.map(path => files.list(path, signal)));
      for (const listing of listings) {
        if (signal.aborted) return;
        if (listing.status === "rejected") { indexError.value = "部分目录无法读取，搜索结果可能不完整"; continue; }
        for (const entry of listing.value.entries) {
          if (entry.symlink) continue;
          if (entry.directory) { if (!ignored.has(entry.name)) queue.push(entry.path); }
          else if (indexedFiles.value.length < 5000) indexedFiles.value.push(entry);
          else indexLimited.value = true;
        }
      }
    }
    indexLimited.value ||= queue.length > 0;
    indexDone.value = true;
  } finally { indexing.value = false; }
}
const hunkAllowed = computed(() => { const change = diffPreview.value?.change; return !!change && !change.originalPath && !isConflict(change) && !["R", "C"].includes(change.index) && !["R", "C"].includes(change.worktree); });
async function applyHunk(hunk: CodeHunk, action: "stage" | "unstage" | "discard") {
  const doc = diffPreview.value;
  if (!doc?.change || !doc.group || !hunkAllowed.value) return;
  const path = joinRemotePath(repository.value, doc.path);
  if (action === "discard" && dirtyDocuments.value.some(file => file.path === path)) { ElMessage.warning("请先保存或关闭该文件的未保存内容，再丢弃远程修改"); return; }
  if (action === "discard") {
    try { await ElMessageBox.confirm(`丢弃“${doc.path}”第 ${hunk.index + 1} 块修改？只恢复此块，其余修改保留。`, "丢弃修改块", { confirmButtonText: "丢弃此块", cancelButtonText: "取消", type: "warning", closeOnClickModal: false }); } catch { return; }
  }
  await gitOperation(`hunk-${action}`, async () => {
    // Never apply coordinates to a changed source snapshot.
    if (action === "unstage") {
      const head = await git.blob(repository.value, doc.change!.originalPath || doc.path, "HEAD");
      if (head.content !== doc.left || head.exists !== doc.leftExists) throw new Error("提交版本已变化，请刷新对比后重试");
      const content = replaceCodeHunk(doc.left, doc.right, hunk, true);
      await git.editIndex(repository.value, doc.path, doc.right, !!doc.rightExists, content, !doc.leftExists && content === "");
    } else {
      const current = doc.rightExists ? await files.read(path) : undefined;
      if ((current?.content || "") !== doc.right || (doc.version && current?.version !== doc.version)) throw new Error("远程文件已变化，请刷新对比后重试");
      if (action === "stage") {
        const content = replaceCodeHunk(doc.left, doc.right, hunk);
        await git.editIndex(repository.value, doc.path, doc.left, !!doc.leftExists, content, !doc.rightExists && content === "");
      } else {
        const base = doc.group === "changes"
          ? await git.blob(repository.value, doc.change!.originalPath || doc.path, "HEAD")
          : await git.blob(repository.value, doc.path, "0");
        if (base.content !== doc.left || base.exists !== doc.leftExists) throw new Error(`${doc.group === "changes" ? "提交" : "暂存"}版本已变化，请刷新对比后重试`);
        const content = replaceCodeHunk(doc.left, doc.right, hunk, true);
        if (!current) await files.upload(path, new Blob([content], { type: "text/plain; charset=utf-8" }));
        else await files.save(path, content, current.version, !content);
      }
    }
  }, action === "stage" ? "已暂存选中修改块" : action === "unstage" ? "已取消暂存选中修改块" : "已丢弃选中修改块");
}
function recoverDrafts() {
  for (const doc of recoveryDocuments.value) {
    const draft = doc.recovery!;
    const changed = doc.version !== draft.version;
    doc.content = draft.draft!; doc.saved = draft.saved ?? doc.saved;
    doc.version = draft.version || doc.version; doc.conflict = changed || !!doc.error; doc.error = ""; doc.recovery = undefined;
  }
  persistWorkspace();
}
async function discardDrafts() {
  try { await ElMessageBox.confirm(`丢弃 ${recoveryDocuments.value.length} 份本地草稿？远程文件保持原样。`, "丢弃恢复草稿", { confirmButtonText: "丢弃草稿", cancelButtonText: "取消", closeOnClickModal: false }); } catch { return; }
  for (const doc of recoveryDocuments.value) doc.recovery = undefined;
  persistWorkspace();
}
function persistWorkspace() {
  if (restoring.value) return;
  try {
    saveWorkspaceState(stateKey, { documents: documents.value.map(doc => ({ path: doc.path, line: doc.line, column: doc.column, scroll: doc.scroll, ...(doc.recovery ? { draft: doc.recovery.draft, saved: doc.recovery.saved, version: doc.recovery.version } : isDirty(doc) ? { draft: doc.content, saved: doc.saved, version: doc.version } : {}) })), activePath: activeDocument.value?.path || "", panel: panel.value, panelOpen: panelOpen.value, width: panelWidth.value, gitView: gitChangeView.value, expanded: expandedPaths.value, terminalOpen: terminalOpen.value, terminalHeight: terminalHeight.value, commitMessage: commitMessage.value });
    storageError.value = "";
  } catch { storageError.value = "浏览器存储空间不足，草稿尚未备份，请保存远程文件。"; }
}
function schedulePersist() { window.clearTimeout(persistenceTimer); if (!restoring.value) persistenceTimer = window.setTimeout(persistWorkspace, 400); }
async function restoreWorkspace() {
  try {
    for (const saved of remembered.documents || []) {
      if (!withinRemotePath(props.rootPath, saved.path)) continue;
      await openFile(saved.path);
      const doc = documents.value.find(doc => doc.path === saved.path);
      if (!doc) continue;
      doc.line = saved.line || 1; doc.column = saved.column || 1; doc.scroll = saved.scroll || 0;
      if (typeof saved.draft === "string") doc.recovery = saved;
      await nextTick(); editorRefs.get(doc.id)?.restorePosition(doc.line, doc.column, doc.scroll);
    }
    const active = documents.value.find(doc => doc.path === remembered.activePath);
    if (active) activateDocument(active);
  } finally { restoring.value = false; }
}
async function autoRefresh() {
  if (autoRefreshing || disposed || !props.visible || props.locked || document.visibilityState === "hidden" || gitBusy.value || gitLoading.value || fullGitBusy.value || fullGitDirty.value || fileMutationBusy.value) return;
  autoRefreshing = true;
  try {
  const previous = JSON.stringify(gitStatus.value);
  if (repository.value || panel.value === "git") await refreshGit(false, true);
  if (disposed || gitBusy.value || fullGitBusy.value || fileMutationBusy.value) return;
  await refreshCleanFiles();
  if (previous !== JSON.stringify(gitStatus.value)) {
    fileTree.value?.refresh();
    const doc = diffPreview.value;
    if (diffOpen.value && doc?.group) {
      const change = gitStatus.value?.changes.find(item => item.path === doc.path);
      if (change) { doc.change = change; await loadDiff(doc); } else diffOpen.value = false;
    }
    if (gitDialogOpen.value) void fullGitRef.value?.refresh();
  }
  } finally { autoRefreshing = false; }
}
function toggleTerminal() { terminalOpen.value = !terminalOpen.value; }
function startTerminalResize(event: PointerEvent) {
  if (event.button !== 0) return; event.preventDefault();
  const target = event.currentTarget as HTMLElement, start = event.clientY, height = terminalHeight.value;
  target.setPointerCapture(event.pointerId);
  const move = (next: PointerEvent) => { terminalHeight.value = Math.round(Math.max(100, Math.min((workspaceElement.value?.clientHeight || 800) * .65, height + start - next.clientY))); };
  const stop = () => { target.removeEventListener("pointermove", move); target.removeEventListener("pointerup", stop); target.removeEventListener("pointercancel", stop); target.removeEventListener("lostpointercapture", stop); };
  target.addEventListener("pointermove", move); target.addEventListener("pointerup", stop); target.addEventListener("pointercancel", stop); target.addEventListener("lostpointercapture", stop);
}
function showQuickOpen() {
  quickOpen.value = true; quickQuery.value = "";
  void indexFiles();
  nextTick(() => quickInput.value?.focus());
}
function openSearchResult(entry: RemoteFileEntry) { quickOpen.value = false; void openFile(entry.path, entry.size); }
function showPanel(next: typeof panel.value) {
  panelOpen.value = panel.value === next ? !panelOpen.value : true;
  panel.value = next;
  if (next === "git" && !gitStatus.value && !gitLoading.value) void refreshGit();
  if (next === "search") nextTick(() => projectSearch.value?.focus());
}
function handleKeyboard(event: KeyboardEvent) {
  const target = event.target;
  if (!props.visible || props.locked || !(target instanceof Element) || !target.closest(".code-workspace")) return;
  const mod = event.ctrlKey || event.metaKey;
  if (target.closest(".code-terminal-mount") && !(mod && event.code === "Backquote")) return;
  if (mod && !event.altKey && event.code === "KeyS") {
    event.preventDefault(); event.stopPropagation();
    if (event.shiftKey) void saveAll();
    else if (activeDocument.value?.kind === "file") void saveDocument(activeDocument.value);
  } else if (mod && event.shiftKey && ["KeyE", "KeyG"].includes(event.code)) {
    event.preventDefault(); event.stopPropagation(); panelOpen.value = true; panel.value = event.code === "KeyE" ? "files" : "git"; if (panel.value === "git" && !gitStatus.value) void refreshGit();
  } else if (mod && event.shiftKey && event.code === "KeyF") {
    event.preventDefault(); event.stopPropagation(); panel.value = "search"; panelOpen.value = true; nextTick(() => projectSearch.value?.focus());
  } else if (mod && event.code === "Backquote") {
    event.preventDefault(); event.stopPropagation(); toggleTerminal();
  } else if (mod && !event.shiftKey && !event.altKey && event.code === "KeyP") {
    event.preventDefault(); event.stopPropagation(); showQuickOpen();
  } else if (mod && !event.shiftKey && !event.altKey && event.code === "KeyW") {
    event.preventDefault(); event.stopPropagation();
    if (activeDocument.value) void closeDocument(activeDocument.value);
  } else if (event.key === "Escape" && quickOpen.value) {
    event.preventDefault(); event.stopPropagation(); quickOpen.value = false;
    if (activeDocument.value) activateDocument(activeDocument.value);
  }
}
function beforeUnload(event: BeforeUnloadEvent) {
  persistWorkspace();
  if (dirtyDocuments.value.length || fullGitDirty.value) { event.preventDefault(); event.returnValue = ""; }
}
watch(() => dirtyDocuments.value.length > 0 || fullGitDirty.value, dirty => emit("dirty", dirty));
watch(() => props.locked, locked => { if (locked) { quickOpen.value = false; gitDialogOpen.value = false; diffOpen.value = false; analysisOpen.value = false; indexController?.abort(); } });
onMounted(() => {
  window.addEventListener("beforeunload", beforeUnload);
  window.addEventListener("pagehide", persistWorkspace);
  window.addEventListener("focus", autoRefresh);
  document.addEventListener("visibilitychange", autoRefresh);
  autoRefreshTimer = window.setInterval(autoRefresh, 5000);
  void restoreWorkspace();
  workspaceObserver = new ResizeObserver(() => { if (window.innerWidth > 640) resizePanel(panelWidth.value, false); });
  if (workspaceElement.value) workspaceObserver.observe(workspaceElement.value);
  if (props.visible) nextTick(focus);
  if (props.host.credentialID || props.host.hasPassword) void refreshGit();
});
watch([documents, activeID, panel, panelOpen, panelWidth, gitChangeView, expandedPaths, terminalOpen, terminalHeight, commitMessage], schedulePersist, { deep: true });
watch(() => props.visible, visible => { if (visible) void autoRefresh(); });
watch([terminalOpen, dockElement, () => props.visible, () => props.locked], () => { emit("dock", terminalOpen.value && props.visible && !props.locked ? dockElement.value : undefined); }, { flush: "post" });
onBeforeUnmount(() => { persistWorkspace(); window.clearTimeout(persistenceTimer); window.clearInterval(autoRefreshTimer); emit("dock", undefined); window.removeEventListener("pagehide", persistWorkspace); window.removeEventListener("focus", autoRefresh); document.removeEventListener("visibilitychange", autoRefresh); stopPanelDrag(); workspaceObserver?.disconnect(); disposed = true; gitRequest++; indexController?.abort(); window.removeEventListener("beforeunload", beforeUnload); });
</script>

<template>
  <section ref="workspaceElement" class="code-workspace" :style="{ '--code-sidebar-width': `${panelWidth}px` }" aria-label="代码编辑器" tabindex="-1" @keydown.capture="handleKeyboard">
    <header class="code-project-bar">
      <div class="code-project-identity"><CodeXml :size="16" /><strong>{{ rootName }}</strong><span>{{ host.name }}</span><small :title="rootPath">{{ rootPath }}</small></div>
      <div class="code-project-actions">
        <button title="SFTP 文件管理" aria-label="SFTP 文件管理" @click="emit('files', rootPath)"><FolderOpen :size="16" /></button>
        <button title="快速打开文件 (Ctrl+P)" aria-label="快速打开文件" @click="showQuickOpen"><Search :size="15" /><kbd>Ctrl P</kbd></button>
        <button title="保存全部 (Ctrl+Shift+S)" aria-label="保存全部" :disabled="!dirtyDocuments.length && !fullGitDirty" @click="saveAll"><Save :size="15" /></button>
        <button title="切换底部终端 (Ctrl+`)" aria-label="切换底部终端" :aria-pressed="terminalOpen" @click="toggleTerminal"><TerminalSquare :size="16" /></button>
        <button title="返回终端" aria-label="返回终端" @click="emit('terminal')"><TerminalSquare :size="16" /></button>
      </div>
    </header>
    <div v-if="recoveryDocuments.length" class="code-recovery-banner"><span>发现 {{ recoveryDocuments.length }} 份未保存草稿，已读取远程版本。恢复后如有版本差异，会提示比较。</span><button @click="recoverDrafts">恢复草稿</button><button @click="discardDrafts">丢弃草稿</button></div>
    <div v-if="storageError" class="code-inline-error">{{ storageError }}</div>
    <div class="code-workbench-body">
      <nav class="code-activity-bar" aria-label="编辑器侧栏">
        <button :class="{ active: panel === 'files' && panelOpen }" title="文件" aria-label="文件" @click="showPanel('files')"><Files :size="20" /></button>
        <button :class="{ active: panel === 'search' && panelOpen }" title="项目全文搜索 (Ctrl+Shift+F)" aria-label="项目全文搜索" @click="showPanel('search')"><Search :size="20" /></button>
        <button :class="{ active: panel === 'git' && panelOpen }" title="源代码管理" aria-label="源代码管理" @click="showPanel('git')"><GitBranch :size="20" /><i v-if="gitStatus?.changes.length">{{ gitStatus.changes.length }}</i></button>
      </nav>
      <aside v-show="panelOpen" class="code-side-panel">
        <header class="code-panel-title"><strong>{{ panel === 'files' ? '资源管理器' : panel === 'search' ? '文件搜索' : '源代码管理' }}</strong>
          <template v-if="panel === 'files'"><button aria-label="全部折叠目录" title="全部折叠目录" :disabled="treeLoading || !expandedPaths.length" @click="fileTree?.collapseAll()"><ChevronsDownUp :size="14" /></button><button aria-label="刷新目录" title="刷新目录" :disabled="treeLoading" @click="refreshTree"><RefreshCw :size="14" /></button></template>
          <template v-else-if="panel === 'git'"><button aria-label="刷新 Git" title="刷新 Git" :disabled="gitLoading || (!!gitBusy || fullGitBusy || fileMutationBusy)" @click="refreshGit()"><RefreshCw :size="14" /></button><button aria-label="拉取更新" :title="gitStatus?.tracking ? `从 ${gitStatus.tracking} 拉取更新` : '当前分支没有上游分支'" :disabled="!gitStatus?.tracking || gitLoading || !!gitBusy || fullGitBusy || fileMutationBusy" @click="sync('pull')"><GitPullRequest :size="15" /></button><button aria-label="打开 Git 管理" title="打开 Git 管理" :disabled="fullGitBusy || fileMutationBusy" @click="openGitManager('status')"><Settings2 :size="15" /></button></template>
        </header>
        <div v-show="panel === 'files'" class="code-file-panel-host"><RemoteFileTree ref="fileTree" :hostID="host.id" :sessionID="sessionId" :root-path="rootPath" :active-path="activeDocument?.path" :visible="visible && panelOpen && panel === 'files'" :locked="locked" :badge="badge" :before-mutation="beforeFileMutation" :delete-warning="deleteWarning" :expanded-paths="expandedPaths" @expanded="expandedPaths = $event" @open="selectFile" @changed="filesChanged" @loading="treeLoading = $event" @busy="fileMutationBusy = $event" /></div>
        <CodeProjectSearch v-show="panel === 'search'" ref="projectSearch" :hostID="host.id" :sessionID="sessionId" :root-path="rootPath" :locked="locked" @open="(path, line, column) => openFile(path, undefined, { line, column })" />
        <div v-show="panel === 'git'" class="code-git-panel" :aria-busy="gitLoading || (!!gitBusy || fullGitBusy || fileMutationBusy)">
          <div v-if="gitLoading && !gitStatus" class="code-panel-empty">正在读取仓库…</div>
          <div v-if="gitError" class="code-inline-error">{{ gitError }}<button :disabled="gitLoading" @click="refreshGit()">重新连接</button><button @click="openGitManager('status')">仓库设置 / 初始化</button></div>
          <template v-if="gitStatus">
            <div class="code-scm-repository" :title="repository"><button :aria-label="gitRepositoryOpen ? '折叠仓库' : '展开仓库'" :aria-expanded="gitRepositoryOpen" @click="gitRepositoryOpen = !gitRepositoryOpen"><ChevronDown :size="14" :class="{ closed: !gitRepositoryOpen }" /></button><strong @click="gitRepositoryOpen = !gitRepositoryOpen">{{ gitStatus.branch || rootName }}</strong><span>{{ gitStatus.changes.length }}</span><small v-if="gitStatus.ahead || gitStatus.behind">↑{{ gitStatus.ahead }} ↓{{ gitStatus.behind }}</small></div>
            <div v-if="gitRepositoryOpen" class="code-scm-repository-body">
            <div class="code-scm-fixed">
            <div class="code-commit-composer"><textarea v-model="commitMessage" aria-label="提交说明" :placeholder="`消息 (Ctrl+Enter) 在“${gitStatus.branch}”上提交`" rows="2" :disabled="aiCommitLoading" @keydown.ctrl.enter.prevent="commit()" @keydown.meta.enter.prevent="commit()" /><button aria-label="AI 生成提交说明" :disabled="!!gitBusy || fullGitBusy || fileMutationBusy || gitLoading" :title="conflicts.length ? '请先处理合并冲突' : !changes.length ? '没有可分析的代码变更' : '根据全部代码变更生成提交说明'" @click="generateCommitMessage"><Sparkles :size="14" /><span>{{ aiCommitLoading ? '生成中' : 'AI' }}</span></button></div>
            <div class="code-commit-actions"><button class="code-commit-button" aria-label="提交更改" :title="changes.length ? '暂存全部更改并提交' : '没有可提交的更改'" :disabled="!changes.length || !!conflicts.length || !commitMessage.trim() || (!!gitBusy || fullGitBusy || fileMutationBusy) || gitLoading" @click="commitPrimary"><Check :size="14" />{{ gitBusy.startsWith('commit') ? '正在提交…' : '提交' }}</button><el-dropdown ref="commitDropdown" trigger="click" placement="bottom-end" :disabled="!!conflicts.length || !commitMessage.trim() || (!!gitBusy || fullGitBusy || fileMutationBusy) || gitLoading" @command="commitMenuAction"><button class="code-commit-menu-button" aria-label="更多提交操作" title="更多提交操作" :disabled="!!conflicts.length || !commitMessage.trim() || (!!gitBusy || fullGitBusy || fileMutationBusy) || gitLoading"><ChevronDown :size="14" /></button><template #dropdown><el-dropdown-menu class="code-commit-menu"><el-dropdown-item command="commit" :disabled="!changes.length">提交</el-dropdown-item><el-dropdown-item command="amend">提交(修改)</el-dropdown-item><el-dropdown-item command="push" :disabled="!changes.length" divided>提交和推送</el-dropdown-item><el-dropdown-item command="sync" :disabled="!changes.length">提交和同步</el-dropdown-item></el-dropdown-menu></template></el-dropdown></div>
            <p v-if="dirtyDocuments.length" class="code-git-note">{{ dirtyDocuments.length }} 个文件尚未保存，Git 操作使用远程已保存的内容。</p>
            <section v-if="conflicts.length" class="code-change-group"><header><strong>合并冲突</strong><span>{{ conflicts.length }}</span></header><button v-for="change in conflicts" :key="change.path" class="code-conflict-file" @click="openGitManager('status', change)"><FileDiff :size="14" />{{ change.path }}</button><button class="code-inline-link" @click="openGitManager('status')">处理合并冲突</button></section>
            </div>
            <div class="code-change-groups">
            <CodeGitChanges :storage-key="`${stateKey}:git:changes`" group="changes" :changes="changes" :tree="gitChangeView === 'tree'" :busy="gitLoading || !!gitBusy || fullGitBusy || fileMutationBusy" @compare="openDiff" @edit="change => openFile(joinRemotePath(repository, change.path))" @toggle-view="toggleGitChangeView" />
            </div>
            </div>
          </template>
        </div>
      </aside>
      <div v-show="panelOpen" class="code-panel-separator" :class="{ resizing: resizingPanel }" role="separator" tabindex="0" aria-label="调整侧栏宽度" aria-orientation="vertical" :aria-valuenow="panelWidth" :aria-valuemin="180" :aria-valuemax="panelMaximum()" @pointerdown="startPanelDrag" @keydown="panelResizeKey" @dblclick="resizePanel(260)" />
      <main class="code-editor-area">
        <nav class="code-document-tabs" aria-label="打开的文件">
          <div v-for="doc in documents" :key="doc.id" class="code-document-tab" :class="{ active: activeID === doc.id }" :title="doc.path" @click="activateDocument(doc)"><File :size="14" /><span>{{ fileName(doc.path) }}</span><i v-if="isDirty(doc)" aria-label="未保存" /><button :aria-label="`关闭 ${fileName(doc.path)}`" @click.stop="closeDocument(doc)"><X :size="13" /></button></div>
        </nav>
        <template v-if="activeDocument">
          <div class="code-breadcrumb"><span :title="activeDocument.path">{{ relativeRemotePath(rootPath, activeDocument.path) }}</span><div><button title="分析当前文件的错误和潜在问题" aria-label="AI 分析当前文件" :disabled="activeDocument.loading || !!activeDocument.error" @click="analyzeFile(activeDocument)"><Sparkles :size="14" /><span class="code-analysis-label">AI 分析</span></button><button title="文件内查找" aria-label="文件内查找" @click="editorRefs.get(activeID)?.search()"><Search :size="14" /></button><button title="重新加载文件" aria-label="重新加载文件" :disabled="activeDocument.loading || activeDocument.saving || fileMutationBusy" @click="reloadDocument(activeDocument)"><RefreshCw :size="14" /></button><button :disabled="!isDirty(activeDocument) || activeDocument.saving || fileMutationBusy" aria-label="保存文件" @click="saveDocument(activeDocument)"><Save :size="14" />{{ activeDocument.saving ? '保存中' : '保存' }}</button></div></div>
          <div v-if="activeDocument.conflict" class="code-conflict-banner"><span>远程版本已变化，当前修改尚未保存。</span><button @click="compareRemote(activeDocument)">比较远程版本</button><button @click="reloadDocument(activeDocument)">重新加载</button></div>
        </template>
        <div v-if="!documents.length" class="code-editor-empty"><CodeXml :size="38" /><h2>{{ rootName }}</h2><p>从左侧选择文件，或查看 Git 更改</p><button @click="showQuickOpen">快速打开文件<kbd>Ctrl P</kbd></button><button @click="showPanel('git')">源代码管理<GitBranch :size="14" /></button></div>
        <div v-for="doc in documents" v-show="doc.id === activeID" :key="doc.id" class="code-document-surface" v-loading="doc.loading">
          <div v-if="doc.error" class="code-document-error"><File :size="26" /><p>{{ doc.error }}</p><button @click="loadDocument(doc)">重新读取</button></div>
          <RemoteCodeEditor v-else-if="!doc.loading" :ref="value => setEditorRef(doc.id, value)" :path="doc.path" :content="doc.content" :visible="visible && !locked && doc.id === activeID" :initial-line="doc.line" :initial-column="doc.column" :initial-scroll="doc.scroll" @save="saveDocument(doc)" @analyze="analyzeFile(doc)" @scroll="doc.scroll = $event" @change="doc.content = $event" @position="(line, column) => { doc.line = line; doc.column = column; }" />
        </div>
        <div v-show="terminalOpen" class="code-terminal-panel" :style="{ height: `${terminalHeight}px` }"><div class="code-terminal-separator" role="separator" tabindex="0" aria-label="调整终端高度" aria-orientation="horizontal" @pointerdown="startTerminalResize" @keydown.up.prevent="terminalHeight = Math.min(500, terminalHeight + 20)" @keydown.down.prevent="terminalHeight = Math.max(100, terminalHeight - 20)" /><header><strong>终端</strong><span>{{ host.name }} · 当前会话</span><button title="收起终端" aria-label="收起终端" @click="terminalOpen = false"><X :size="14" /></button></header><div ref="dockElement" class="code-terminal-mount" /></div>
      </main>
    </div>
    <footer class="code-status-bar"><el-popover v-model:visible="branchPopoverOpen" placement="top-start" trigger="manual" :width="250" popper-class="code-branch-popper"><template #reference><button aria-label="切换 Git 分支" :disabled="!repository || branchLoading" @click="toggleBranchPopover"><GitBranch :size="12" />{{ gitStatus?.branch || 'Git' }}<span v-if="gitStatus">{{ gitStatus.changes.length }} 个更改</span></button></template><section class="code-branch-picker" role="dialog" aria-label="切换分支"><header><strong>切换分支</strong><button aria-label="刷新分支" title="刷新分支" :disabled="branchLoading" @click="loadBranches"><RefreshCw :size="13" /></button></header><p v-if="branchError" class="error">{{ branchError }}</p><p v-else-if="branchLoading && !branchesLoaded">正在读取分支…</p><div v-else class="code-branch-groups"><section class="code-branch-group" role="group" aria-label="本地分支"><h4><span>本地分支</span><small>{{ localBranches.length }}</small></h4><button v-for="item in localBranches" :key="item.name" :class="{ current: item.current }" :disabled="item.current || !!gitBusy" @click="switchWorkspaceBranch(item)"><GitBranch :size="13" /><span>{{ item.name }}</span><Check v-if="item.current" :size="13" /></button><p v-if="!localBranches.length" class="empty">暂无本地分支</p></section><section class="code-branch-group" role="group" aria-label="远程分支"><h4><span>远程分支</span><small>{{ remoteBranches.length }}</small></h4><button v-for="item in remoteBranches" :key="item.name" :disabled="!!gitBusy" @click="switchWorkspaceBranch(item)"><GitPullRequest :size="13" /><span>{{ item.name }}</span></button><p v-if="!remoteBranches.length" class="empty">暂无远程分支</p></section></div></section></el-popover><span class="code-status-host">{{ host.username }}@{{ host.address }}</span><span v-if="activeDocument?.kind === 'file'">行 {{ activeDocument.line }}，列 {{ activeDocument.column }}</span><span v-if="activeDocument">{{ codeLanguage(activeDocument.path).name }}</span><span>UTF-8</span><span v-if="activeDocument?.kind === 'file'">{{ activeDocument.content.includes('\r\n') ? 'CRLF' : 'LF' }}</span></footer>
    <CodeDiffDialog v-if="diffPreview && !locked" v-model="diffOpen" :path="diffPreview.path" :left="diffPreview.left" :right="diffPreview.right" :left-label="diffPreview.leftLabel" :right-label="diffPreview.rightLabel" :loading="diffPreview.loading" :error="diffPreview.error" :refreshable="!!diffPreview.group" :can-edit="!!diffPreview.group && diffPreview.change?.worktree !== 'D' && !(diffPreview.change?.index === 'D' && diffPreview.change?.worktree === ' ')" :group="diffPreview.group" :hunk-allowed="hunkAllowed" :busy="!!gitBusy || fullGitBusy || fileMutationBusy || gitLoading" :hunk-hint="!hunkAllowed && diffPreview.group ? '重命名、复制及冲突文件请按文件操作' : undefined" @apply-hunk="applyHunk" @refresh="loadDiff(diffPreview)" @edit="editComparedFile" />
    <GitDialog v-if="!locked" ref="fullGitRef" v-model="gitDialogOpen" :host="host" :session-id="sessionId" :initial-path="repository || rootPath" :initial-tab="gitTab" :initial-conflict="gitConflict" :before-operation="beforeFullGitOperation" @changed="gitChanged" @dirty="fullGitDirty = $event" @busy="fullGitBusy = $event" />
    <CodeFileAnalysis v-if="analysisDocument && !locked" v-model="analysisOpen" :path="analysisDocument.path" :content="analysisDocument.content" :dirty="isDirty(analysisDocument)" @locate="locateAnalysis" />
    <div v-if="quickOpen" class="code-quick-overlay" @click.self="quickOpen = false"><section class="code-quick-open" role="dialog" aria-label="快速打开文件" aria-modal="true"><header><Search :size="16" /><input ref="quickInput" v-model="quickQuery" role="combobox" aria-label="快速搜索文件" aria-controls="code-quick-results" aria-expanded="true" :aria-activedescendant="searchResults.length ? `code-quick-result-${quickSelected}` : undefined" placeholder="按文件名或路径查找" @keydown="quickNavigate" /><button aria-label="关闭快速打开" @click="quickOpen = false"><X :size="15" /></button></header><div class="code-quick-heading"><span>文件</span><span>所在目录</span></div><div id="code-quick-results" class="code-search-results" role="listbox" aria-label="文件搜索结果"><button v-for="(entry, index) in searchResults" :id="`code-quick-result-${index}`" :key="entry.path" class="code-quick-result" :class="{ active: quickSelected === index }" role="option" :aria-selected="quickSelected === index" :title="relativeRemotePath(rootPath, entry.path)" @pointermove="quickSelected = index" @click="openSearchResult(entry)"><File :size="14" /><span>{{ fileName(entry.path) }}</span><small>{{ directoryLabel(entry.path) }}</small></button><p v-if="!searchResults.length" class="code-panel-empty">{{ indexing ? '正在查找文件…' : '没有匹配的文件' }}</p></div><footer><span>{{ indexing ? '正在索引目录…' : `${indexedFiles.length} 个文件` }}{{ indexLimited ? ' · 已达索引上限' : '' }}<span v-if="indexError"> · {{ indexError }}</span></span><span>↑ ↓ 选择 · Enter 打开 · Esc 关闭</span></footer></section></div>
  </section>
</template>

<style src="../styles-code-workspace.css" />
