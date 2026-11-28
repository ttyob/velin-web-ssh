<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, ref, watch } from "vue";
import {
  Check,
  ChevronRight,
  CircleAlert,
  CirclePlus,
  Download,
  FileDiff,
  FilePenLine,
  FileText,
  Folder,
  FolderTree,
  GitBranch,
  GitCommitHorizontal,
  GitMerge,
  History,
  List,
  Plus,
  RefreshCw,
  Sparkles,
  Trash2,
  Upload,
} from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import { createRemoteGit } from "../remoteGit";
import { truncateGitAIContent } from "../gitAI";
import {
  buildGitChangeTree,
  parseGitBranches,
  parseGitCommits,
  parseGitRemotes,
  parseGitStatus,
  type GitBranch as GitBranchInfo,
  type GitChange,
  type GitCommit,
  type GitRemote,
} from "../git";
import type { AgentStatus, Host } from "../types";
import OperationFeedback from "./OperationFeedback.vue";
import { operationLabel } from "../operationFeedback";
import { getActivePinia } from "pinia";
import { useAuthStore } from "../stores/auth";
import GitSurface from "./GitSurface.vue";

const GitCompareDialog = defineAsyncComponent(
  () => import("./GitCompareDialog.vue"),
);

type GitTab = "status" | "history" | "branches";
type ChangeView = "list" | "tree";

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  sessionId?: string;
  initialPath?: string;
  embedded?: boolean;
  initialTab?: GitTab;
  initialConflict?: GitChange;
  beforeOperation?: () => Promise<boolean>;
}>();
const emit = defineEmits<{ "update:modelValue": [boolean]; changed: []; dirty: [boolean]; busy: [boolean] }>();
const compareRef = ref<{ confirmClose: () => Promise<boolean>; saveCurrent: () => Promise<void> }>();
const mergeDirty = ref(false), mergeSaving = ref(false);

const activeTab = ref<GitTab>("status");
const status = ref<AgentStatus>();
const loading = ref(false);
const refreshing = ref(false);
const busy = ref("");
const error = ref("");
const operationError = ref(""), operationProgress = ref("");
const expandedChangePaths = ref<string[]>([]), selectedChangePath = ref("");
let refreshEpoch = 0;
const repoPath = ref(".");
const repoRoot = ref("");
const branch = ref("");
const tracking = ref("");
const ahead = ref(0);
const behind = ref(0);
const changes = ref<GitChange[]>([]);
const commits = ref<GitCommit[]>([]);
const branches = ref<GitBranchInfo[]>([]);
const remotes = ref<GitRemote[]>([]);
const commitMessage = ref("");
const userID = getActivePinia() ? useAuthStore().user?.id || "local" : "local";
const draftKey = computed(() => `velin-git-message:${userID}:${props.host?.id || ""}:${repoPath.value}`);
watch(commitMessage, value => { try { localStorage.setItem(draftKey.value, value); } catch { /* Keep the in-memory message. */ } }, { flush: "sync" });
const newBranchName = ref("");
const remoteName = ref("");
const remoteURL = ref("");
const selectedRemote = ref("");
const changeView = ref<ChangeView>("list");
const changePreviewOpen = ref(false);
const previewChange = ref<GitChange>();
const previewMerge = ref(false);
const commitDetailOpen = ref(false);
const commitDetailLoading = ref(false);
const commitDetailHash = ref("");
const commitDetail = ref("");
const mergeInProgress = ref(false);
const aiCommitLoading = ref(false);
const conflictAnalysisOpen = ref(false);
const conflictAnalysisLoading = ref(false);
const conflictAnalysisPath = ref("");
const conflictAnalysis = ref("");

const connected = computed(() => status.value?.state === "connected");
const repositoryName = computed(() => repoRoot.value.split(/[\\/]/).filter(Boolean).slice(-1)[0] || "Git 工作区");
const localBranches = computed(() => branches.value.filter((item) => !item.remote));
const remoteBranches = computed(() => branches.value.filter((item) => item.remote));
const currentRemote = computed(() => selectedRemote.value || remotes.value[0]?.name || "");
const changeTree = computed(() => buildGitChangeTree(changes.value));
const conflicts = computed(() => changes.value.filter(isConflict));
const REMOTE_REFRESH_INTERVAL = 30_000;
let lastRemoteRefreshAttempt = 0;
let remoteRefreshTimer: number | undefined;

function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

function isConflict(item: GitChange) {
  return item.index === "U" || item.worktree === "U" || (item.index === "A" && item.worktree === "A") || (item.index === "D" && item.worktree === "D");
}

function changeSource(item: GitChange) {
  if (item.index === "?" || item.worktree === "?") return "未跟踪";
  return [item.index !== " " ? "暂存区" : "", item.worktree !== " " ? "工作区" : ""].filter(Boolean).join(" · ");
}

async function loadStatus() {
  if (!props.host) return;
  status.value = await api<AgentStatus>(`/api/hosts/${props.host.id}/agent`);
}

async function ensureConnected() {
  if (!props.host) throw new Error("当前终端没有可用的主机信息");
  await loadStatus();
  if (connected.value) return;
  status.value = await api<AgentStatus>(
    `/api/hosts/${props.host.id}/agent/connect`,
    { method: "POST", body: json({}) },
  );
}

async function command(commandText: string) {
  if (!props.host) throw new Error("当前终端没有可用的主机信息");
  if (busy.value) {
    if (/\bfetch\b/.test(commandText)) operationProgress.value = "正在获取远程状态…";
    else if (/\bpull\b/.test(commandText)) operationProgress.value = "正在拉取最新代码…";
    else if (/\bpush\b/.test(commandText)) operationProgress.value = "正在推送提交…";
    else if (/\badd -A\b/.test(commandText)) operationProgress.value = "正在准备全部更改…";
    else if (/\bcommit -m\b/.test(commandText)) operationProgress.value = "正在创建提交…";
  }
  const result = await api<{ output: string; success: boolean; error?: string }>(
    `/api/hosts/${props.host.id}/agent/command`,
    { method: "POST", body: json({ command: commandText }) },
  );
  if (!result.success) throw new Error(result.error || result.output || "Git 命令执行失败");
  return result.output || "";
}

function gitCommand(args: string) {
  return `git -C ${shellQuote(repoRoot.value || repoPath.value.trim() || ".")} --literal-pathspecs ${args}`;
}

function fetchTrackingCommand() {
  const remote = tracking.value.split("/", 1)[0];
  return gitCommand(`fetch --quiet ${shellQuote(remote)}`);
}

function applyStatus(output: string) {
  const value = parseGitStatus(output);
  branch.value = value.branch;
  tracking.value = value.tracking;
  ahead.value = value.ahead;
  behind.value = value.behind;
  const previous = new Map(changes.value.map(item => [item.path, item]));
  if (JSON.stringify(changes.value) !== JSON.stringify(value.changes)) changes.value = value.changes.map(item => JSON.stringify(previous.get(item.path)) === JSON.stringify(item) ? previous.get(item.path)! : item);
}

async function refreshRemoteStatus(force = false, silent = false) {
  if (!repoRoot.value || !tracking.value) return;
  if (!force && Date.now() - lastRemoteRefreshAttempt < REMOTE_REFRESH_INTERVAL) return;
  lastRemoteRefreshAttempt = Date.now(); const epoch = refreshEpoch, operation = busy.value;
  try {
    await command(fetchTrackingCommand());
    const output = await command(gitCommand("status --porcelain=v1 -z --branch --untracked-files=all"));
    if (epoch === refreshEpoch && operation === busy.value && props.modelValue) applyStatus(output);
  } catch (cause) {
    if (!silent) throw cause;
  }
}

async function refresh(silent = false) {
  if (!props.host || refreshing.value || (silent && busy.value)) return;
  const epoch = ++refreshEpoch;
  refreshing.value = true; if (!silent) { loading.value = true; error.value = ""; }
  try {
    await ensureConnected();
    const root = (await command(`git -C ${shellQuote(repoPath.value.trim() || ".")} rev-parse --show-toplevel`)).trim();
    if (epoch !== refreshEpoch || !props.modelValue) return;
    repoRoot.value = root;
    const [statusOutput, commitOutput, branchOutput, remoteOutput, mergeHead] = await Promise.all([
      command(gitCommand("status --porcelain=v1 -z --branch --untracked-files=all")),
      command(`if ${gitCommand("rev-parse --verify HEAD")} >/dev/null 2>&1; then ${gitCommand("log -50 --date=iso-strict --pretty=format:'%H%x09%h%x09%an%x09%ad%x09%s'")}; fi`),
      command(gitCommand("for-each-ref --format='%(HEAD)%09%(refname)%09%(objectname:short)%09%(upstream:short)%09%(upstream:track)%09%(subject)' refs/heads refs/remotes")),
      command(gitCommand("remote -v")),
      command(`${gitCommand("rev-parse -q --verify MERGE_HEAD")} 2>/dev/null || true`),
    ]);
    if (epoch !== refreshEpoch || !props.modelValue) return;
    applyStatus(statusOutput);
    const nextCommits = parseGitCommits(commitOutput), nextBranches = parseGitBranches(branchOutput), nextRemotes = parseGitRemotes(remoteOutput);
    if (JSON.stringify(commits.value) !== JSON.stringify(nextCommits)) commits.value = nextCommits;
    if (JSON.stringify(branches.value) !== JSON.stringify(nextBranches)) branches.value = nextBranches;
    if (JSON.stringify(remotes.value) !== JSON.stringify(nextRemotes)) remotes.value = nextRemotes;
    mergeInProgress.value = Boolean(mergeHead.trim());
    if (!selectedRemote.value || !remotes.value.some((item) => item.name === selectedRemote.value))
      selectedRemote.value = remotes.value[0]?.name || "";
    await refreshRemoteStatus(false, true);
  } catch (cause) {
    if (epoch === refreshEpoch && !silent) error.value = cause instanceof Error ? cause.message : "Git 仓库读取失败";
  } finally {
    if (epoch === refreshEpoch) { loading.value = false; refreshing.value = false; }
  }
}

async function runOperation(key: string, operation: () => Promise<unknown>, message: string) {
  if (busy.value || loading.value || !(await allowOperation())) return;
  refreshEpoch++; refreshing.value = false;
  busy.value = key; operationError.value = ""; operationProgress.value = "";
  try {
    const result = await operation();
    if (result !== false) ElMessage.success(message);
    await refresh();
  } catch (cause) {
    operationError.value = cause instanceof Error ? cause.message : "Git 操作失败";
    await refresh();
  } finally {
    busy.value = ""; operationProgress.value = "";
    emit("changed");
  }
}

async function allowOperation() {
  if (mergeDirty.value || mergeSaving.value) { ElMessage.warning("请先保存或关闭正在编辑的合并文件"); return false; }
  return !props.beforeOperation || await props.beforeOperation();
}
async function confirmClose() {
  if (busy.value || mergeSaving.value) { ElMessage.info("Git 操作正在进行，请稍后关闭"); return false; }
  return !changePreviewOpen.value || !compareRef.value || await compareRef.value.confirmClose();
}
function selectTab(tab: GitTab) { activeTab.value = tab; }
async function saveCurrent() { if (changePreviewOpen.value) await compareRef.value?.saveCurrent(); }
defineExpose({ confirmClose, refresh, selectTab, saveCurrent });
watch(mergeDirty, value => emit("dirty", value));
watch(() => Boolean(busy.value) || mergeSaving.value, value => emit("busy", value));

async function commitChanges() {
  const message = commitMessage.value.trim();
  if (!behind.value && !message) return ElMessage.warning("请输入提交说明");
  if (mergeInProgress.value || conflicts.value.length) return ElMessage.warning("请先完成合并并处理冲突");
  if (!behind.value && !changes.value.length) return ElMessage.warning("没有可提交的更改");
  let pulledLatest = false;
  await runOperation(behind.value ? "pull" : "commit", async () => {
    if (tracking.value) {
      await refreshRemoteStatus(true);
      if (behind.value > 0) {
        try {
          await ElMessageBox.confirm(
            `远程分支“${tracking.value}”比本地新 ${behind.value} 个提交。先拉取最新代码吗？拉取后请检查变更，再重新提交。`,
            "远程代码有更新",
            { confirmButtonText: "拉取最新代码", cancelButtonText: "暂不拉取", type: "warning", closeOnClickModal: false },
          );
        } catch { return false; }
        await command(gitCommand("pull --ff-only"));
        pulledLatest = true;
        return false;
      }
    }
    if (!changes.value.length) {
      ElMessage.info("远程状态已更新，没有可提交的更改");
      return false;
    }
    if (!commitMessage.value.trim()) {
      ElMessage.warning("请输入提交说明");
      return false;
    }
    await command(gitCommand("add -A"));
    await command(gitCommand(`commit -m ${shellQuote(message)}`));
    commitMessage.value = "";
  }, "提交已创建");
  if (pulledLatest) ElMessage.success("已拉取最新代码，请检查变更后重新提交");
}

async function generateCommitMessage() {
  if (busy.value || loading.value) return;
  if (mergeInProgress.value || conflicts.value.length) return ElMessage.warning("请先完成合并并处理冲突，再生成提交说明");
  if (!changes.value.length) return ElMessage.warning("没有可分析的代码变更");
  aiCommitLoading.value = true;
  busy.value = "ai-commit";
  try {
    await ensureConnected();
    if (!status.value?.aiConfigured) return ElMessage.warning("请先在设置中配置 AI 模型");
    const result = await createRemoteGit(props.host!.id).generateCommitMessage(repoRoot.value, branch.value);
    commitMessage.value = result.message;
    ElMessage.success(`已由 ${result.model} 生成提交说明`);
  } catch (cause) {
    operationError.value = cause instanceof Error ? cause.message : "提交说明生成失败";
  } finally {
    aiCommitLoading.value = false;
    busy.value = "";
  }
}

async function mergeBranch(item: GitBranchInfo) {
  if (busy.value || !(await allowOperation())) return;
  try {
    await ElMessageBox.confirm(
      `将“${item.name}”合并到当前分支“${branch.value}”？合并后如有冲突，可在状态页手动处理。`,
      "合并分支",
      { confirmButtonText: "合并", cancelButtonText: "取消", type: "warning" },
    );
  } catch {
    return;
  }
  busy.value = `merge:${item.name}`; operationError.value = ""; operationProgress.value = "";
  try {
    await command(gitCommand(`merge --no-edit -- ${shellQuote(item.name)}`));
    ElMessage.success(`已合并 ${item.name}`);
  } catch (cause) {
    await refresh();
    if (mergeInProgress.value || conflicts.value.length) {
      activeTab.value = "status";
      ElMessage.warning(`合并产生 ${conflicts.value.length} 个冲突，请处理后继续`);
      if (conflicts.value[0]) editConflict(conflicts.value[0]);
      return;
    }
    operationError.value = cause instanceof Error ? cause.message : "分支合并失败";
    return;
  } finally {
    busy.value = "";
    emit("changed");
  }
  await refresh();
}

function editConflict(item: GitChange) {
  previewChange.value = item;
  previewMerge.value = true;
  changePreviewOpen.value = true;
}

async function resolveConflict(item: GitChange, side?: "ours" | "theirs") {
  const action = side === "ours" ? "使用当前分支" : side === "theirs" ? "使用传入分支" : "标记已解决";
  if (side) {
    try {
      await ElMessageBox.confirm(`${action}版本覆盖“${item.path}”并标记为已解决？`, action, {
        confirmButtonText: "确认",
        cancelButtonText: "取消",
        type: "warning",
      });
    } catch {
      return;
    }
  }
  const selectedStage = side === "ours" ? 2 : side === "theirs" ? 3 : 0;
  const quotedPath = shellQuote(item.path);
  const checkout = side
    ? `if ${gitCommand(`cat-file -e ${shellQuote(`:${selectedStage}:${item.path}`)}`)} 2>/dev/null; then ${gitCommand(`checkout --${side} -- ${quotedPath}`)} && ${gitCommand(`add -- ${quotedPath}`)}; else ${gitCommand(`rm --ignore-unmatch -- ${quotedPath}`)}; fi`
    : gitCommand(`add -- ${quotedPath}`);
  await runOperation(
    `resolve:${item.path}`,
    () => command(checkout).then(() => undefined),
    `${item.path} 已标记为解决`,
  );
}

async function continueMerge() {
  if (conflicts.value.length) return ElMessage.warning("请先解决全部冲突");
  await runOperation(
    "merge-continue",
    () => command(gitCommand("-c core.editor=true merge --continue")).then(() => undefined),
    "合并已完成",
  );
}

async function abortMerge() {
  try {
    await ElMessageBox.confirm("中止本次合并并恢复合并前的状态？", "中止合并", {
      confirmButtonText: "中止合并",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  await runOperation("merge-abort", () => command(gitCommand("merge --abort")).then(() => undefined), "已中止合并");
}

async function analyzeConflict(item: GitChange) {
  if (!status.value?.aiConfigured) return ElMessage.warning("请先在设置中配置 AI 模型");
  conflictAnalysisOpen.value = true;
  conflictAnalysisLoading.value = true;
  conflictAnalysisPath.value = item.path;
  conflictAnalysis.value = "";
  try {
    const content = await command(gitCommand(`diff --no-ext-diff --no-color --cc -- ${shellQuote(item.path)}`));
    const result = await api<{ message: string; model: string }>("/api/agent/git-assist", {
      method: "POST",
      body: json({ kind: "conflict_analysis", branch: branch.value, path: item.path, content: truncateGitAIContent(content) }),
    });
    conflictAnalysis.value = result.message;
  } catch (cause) {
    conflictAnalysis.value = cause instanceof Error ? cause.message : "冲突分析失败";
  } finally {
    conflictAnalysisLoading.value = false;
  }
}

async function initializeRepository() {
  try {
    await ElMessageBox.confirm(`在“${repoPath.value}”初始化 Git 仓库？`, "初始化 Git", {
      confirmButtonText: "初始化",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  await runOperation("init", () => command(gitCommand("init")).then(() => undefined), "Git 仓库已初始化");
}

async function fetchRepository() {
  await runOperation("fetch", () => command(gitCommand("fetch --all --prune")).then(() => undefined), "远程信息已更新");
}

async function pullRepository() {
  try {
    await ElMessageBox.confirm("拉取远程更新可能产生合并或修改工作区，继续吗？", "Git Pull", {
      confirmButtonText: "拉取",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  await runOperation("pull", () => command(gitCommand("pull --ff-only")).then(() => undefined), "远程更新已拉取");
}

async function pushRepository() {
  const remote = currentRemote.value;
  if (!remote) return ElMessage.warning("请先配置远程仓库");
  if (branch.value === "HEAD") return ElMessage.warning("游离 HEAD 状态下不能直接推送，请先切换或创建分支");
  const establishesTracking = !tracking.value || !tracking.value.startsWith(`${remote}/`);
  try {
    await ElMessageBox.confirm(
      `推送分支“${branch.value}”到“${remote}”？${establishesTracking ? "同时设置为上游分支。" : ""}`,
      "Git Push",
      {
      confirmButtonText: "推送",
      cancelButtonText: "取消",
      type: "warning",
      },
    );
  } catch {
    return;
  }
  const args = establishesTracking
    ? `push --set-upstream ${shellQuote(remote)} ${shellQuote(branch.value)}`
    : "push";
  await runOperation("push", () => command(gitCommand(args)).then(() => undefined), "代码已推送");
}

async function switchBranch(item: GitBranchInfo): Promise<void> {
  if (item.current) return;
  if (item.remote) {
    await switchRemoteBranch(item);
    return;
  }
  try {
    await ElMessageBox.confirm(`切换到分支“${item.name}”？未提交变更可能阻止切换。`, "切换分支", {
      confirmButtonText: "切换",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  await runOperation("switch", () => command(gitCommand(`switch -- ${shellQuote(item.name)}`)).then(() => undefined), `已切换到 ${item.name}`);
}

async function switchRemoteBranch(item: GitBranchInfo): Promise<void> {
  const parts = remoteBranchParts(item);
  if (!parts) {
    ElMessage.warning("无法识别远程分支");
    return;
  }
  const existing = localBranches.value.find((branchItem) => branchItem.name === parts.branch);
  if (existing) {
    await switchBranch(existing);
    return;
  }
  try {
    await ElMessageBox.confirm(`基于远程分支“${item.name}”创建并切换本地分支“${parts.branch}”？`, "切换远程分支", {
      confirmButtonText: "创建并切换",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  await runOperation("switch-remote", () => command(gitCommand(`switch --track ${shellQuote(item.name)}`)).then(() => undefined), `已切换到 ${parts.branch}`);
}

function remoteBranchParts(item: GitBranchInfo) {
  const remote = [...remotes.value]
    .sort((left, right) => right.name.length - left.name.length)
    .find((candidate) => item.name.startsWith(`${candidate.name}/`));
  if (!remote) return undefined;
  return { remote: remote.name, branch: item.name.slice(remote.name.length + 1) };
}

function remoteBranchButton(item: GitBranchInfo) {
  const parts = remoteBranchParts(item);
  if (!parts) return "创建本地分支";
  const local = localBranches.value.find((branchItem) => branchItem.name === parts.branch);
  if (local?.current) return "当前分支";
  return local ? "切换本地分支" : "创建本地分支";
}

function remoteBranchDisabled(item: GitBranchInfo) {
  const parts = remoteBranchParts(item);
  return Boolean(busy.value) || !parts || localBranches.value.some(
    (branchItem) => branchItem.name === parts.branch && branchItem.current,
  );
}

async function deleteRemoteBranch(item: GitBranchInfo) {
  const parts = remoteBranchParts(item);
  if (!parts) return ElMessage.warning("无法识别远程分支");
  try {
    await ElMessageBox.confirm(`从“${parts.remote}”永久删除远程分支“${parts.branch}”？`, "删除远程分支", {
      confirmButtonText: "删除",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  await runOperation(
    "delete-remote-branch",
    () => command(gitCommand(`push ${shellQuote(parts.remote)} --delete ${shellQuote(parts.branch)}`)).then(() => undefined),
    "远程分支已删除",
  );
}

async function createBranch() {
  const name = newBranchName.value.trim();
  if (!name) return ElMessage.warning("请输入分支名称");
  await runOperation("create-branch", () => command(gitCommand(`switch -c ${shellQuote(name)}`)).then(() => undefined), `分支 ${name} 已创建并切换`);
  newBranchName.value = "";
}

async function deleteBranch(item: GitBranchInfo) {
  if (item.current) return;
  try {
    await ElMessageBox.confirm(`删除本地分支“${item.name}”？未合并提交不会被删除。`, "删除分支", {
      confirmButtonText: "删除",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  await runOperation("delete-branch", () => command(gitCommand(`branch -d ${shellQuote(item.name)}`)).then(() => undefined), "本地分支已删除");
}

async function addRemote() {
  const name = remoteName.value.trim();
  const url = remoteURL.value.trim();
  if (!name || !url) return ElMessage.warning("请输入远程名称和地址");
  await runOperation("add-remote", () => command(gitCommand(`remote add ${shellQuote(name)} ${shellQuote(url)}`)).then(() => undefined), "远程仓库已添加");
  remoteName.value = "";
  remoteURL.value = "";
}

async function removeRemote(item: GitRemote) {
  try {
    await ElMessageBox.confirm(`删除远程仓库“${item.name}”？`, "删除远程仓库", {
      confirmButtonText: "删除",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  await runOperation("remove-remote", () => command(gitCommand(`remote remove ${shellQuote(item.name)}`)).then(() => undefined), "远程仓库已删除");
}

function showChangeDiff(item: GitChange) {
  previewChange.value = item;
  previewMerge.value = isConflict(item);
  changePreviewOpen.value = true;
}

async function showCommit(item: GitCommit) {
  commitDetailOpen.value = true;
  commitDetailLoading.value = true;
  commitDetailHash.value = item.hash;
  commitDetail.value = "";
  try {
    commitDetail.value = await command(gitCommand(`show --stat --decorate --format=fuller ${shellQuote(item.hash)}`));
  } catch (cause) {
    commitDetail.value = cause instanceof Error ? cause.message : "提交详情读取失败";
  } finally {
    commitDetailLoading.value = false;
  }
}

watch(
  () => props.modelValue,
  (open) => {
    window.clearInterval(remoteRefreshTimer);
    remoteRefreshTimer = undefined;
    refreshEpoch++; loading.value = false; refreshing.value = false;
    if (!open) { changePreviewOpen.value = false; commitDetailOpen.value = false; conflictAnalysisOpen.value = false; return; }
    activeTab.value = props.initialTab || "status";
    repoPath.value = props.initialPath?.trim() || props.host?.initialDirectory || ".";
    repoRoot.value = ""; changes.value = []; commits.value = []; branches.value = []; remotes.value = [];
    operationError.value = ""; expandedChangePaths.value = []; selectedChangePath.value = "";
    try { commitMessage.value = localStorage.getItem(draftKey.value) || ""; } catch { commitMessage.value = ""; }
    lastRemoteRefreshAttempt = 0;
    error.value = "";
    status.value = undefined;
    previewChange.value = props.initialConflict;
    previewMerge.value = Boolean(props.initialConflict);
    changePreviewOpen.value = Boolean(props.initialConflict);
    void refresh();
    remoteRefreshTimer = window.setInterval(() => {
      if (!busy.value && !loading.value) void refreshRemoteStatus(true, true);
    }, REMOTE_REFRESH_INTERVAL);
  },
  { immediate: true },
);
onBeforeUnmount(() => window.clearInterval(remoteRefreshTimer));
</script>

<template>
  <div :class="{ 'git-workspace': embedded }">
  <GitSurface
    :model-value="modelValue"
    :embedded="embedded"
    v-show="!embedded || (!(changePreviewOpen && previewMerge) && !commitDetailOpen && !conflictAnalysisOpen)"
    class="git-dialog"
    :title="embedded ? undefined : `Git 管理 · ${host?.name || '当前终端'}`"
    width="min(1080px, calc(100vw - 28px))"
    append-to-body
    :close-on-click-modal="false"
    :before-close="async (done: () => void) => { if (await confirmClose()) done(); }"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="git-heading">
      <div class="git-repo-identity">
        <span class="git-repo-mark"><GitBranch :size="18" /></span>
        <div class="git-repo-label">
          <strong>{{ repoRoot ? repositoryName : 'Git 工作区' }}</strong>
          <span class="git-repo-location" :title="repoRoot || repoPath">{{ repoRoot || `${repoPath} · 当前终端目录` }}</span>
        </div>
      </div>
      <div class="git-heading-actions">
        <el-select v-if="remotes.length" v-model="selectedRemote" class="git-remote-select" placeholder="远程仓库" size="small">
          <el-option v-for="item in remotes" :key="item.name" :label="item.name" :value="item.name" />
        </el-select>
        <el-button size="small" :icon="RefreshCw" :disabled="Boolean(busy)" :loading="loading" @click="refresh()">刷新</el-button>
        <span class="git-action-divider" aria-hidden="true"></span>
        <el-button size="small" :icon="Download" :disabled="!repoRoot || !remotes.length || Boolean(busy)" :loading="busy === 'fetch'" @click="fetchRepository">抓取</el-button>
        <el-button size="small" :icon="GitMerge" :disabled="!repoRoot || !tracking || Boolean(busy)" :loading="busy === 'pull'" :title="tracking ? `从 ${tracking} 拉取` : '当前分支没有上游分支'" @click="pullRepository">拉取</el-button>
        <el-button size="small" :icon="Upload" :disabled="!repoRoot || !remotes.length || Boolean(busy)" :loading="busy === 'push'" @click="pushRepository">推送</el-button>
      </div>
    </div>

    <div v-if="repoRoot" class="git-repo-meta">
      <span class="git-branch-pill"><GitBranch :size="13" />{{ branch }}</span>
      <span v-if="tracking">跟踪 {{ tracking }}</span>
      <span v-if="ahead">领先 {{ ahead }}</span>
      <span v-if="behind">落后 {{ behind }}</span>
      <span v-if="!ahead && !behind && tracking" class="git-clean-label"><Check :size="13" />已同步</span>
    </div>

    <div v-if="!repoRoot" class="git-no-repository" v-loading="loading">
      <GitBranch :size="30" />
      <strong>{{ error ? 'Git 仓库暂不可用' : '正在检查 Git 仓库' }}</strong>
      <span v-if="error">{{ error }}</span>
      <el-button v-if="!loading" @click="refresh()">重新连接</el-button>
      <el-button v-if="!loading" type="primary" :icon="GitCommitHorizontal" @click="initializeRepository">初始化 Git 仓库</el-button>
    </div>

    <OperationFeedback :pending="busy ? operationProgress || operationLabel(busy) : undefined" :error="operationError || (repoRoot ? error : undefined)" retry-label="刷新 Git 状态" @retry="refresh()" @dismiss="operationError = ''; error = ''" />
    <template v-if="repoRoot">
      <el-tabs v-model="activeTab" class="git-tabs">
        <el-tab-pane label="状态与提交" name="status">
          <div class="git-status-summary">
            <span>{{ changes.length }} 个变更</span>
            <span v-if="tracking">跟踪 {{ tracking }}</span>
            <span v-if="ahead">领先 {{ ahead }}</span>
            <span v-if="behind" class="git-behind-label">落后 {{ behind }}</span>
            <span v-if="tracking && !ahead && !behind" class="git-clean-label"><Check :size="13" />已同步</span>
          </div>
          <section v-if="mergeInProgress" class="git-conflicts">
            <div class="git-conflict-heading">
              <CircleAlert :size="18" />
              <div>
                <strong>正在合并分支</strong>
                <span v-if="conflicts.length">{{ conflicts.length }} 个文件需要处理</span>
                <span v-else>冲突已全部标记为解决，可以继续完成合并</span>
              </div>
              <el-button size="small" :disabled="Boolean(busy)" :loading="busy === 'merge-abort'" @click="abortMerge">中止合并</el-button>
              <el-button size="small" type="primary" :disabled="Boolean(busy) || Boolean(conflicts.length)" :loading="busy === 'merge-continue'" @click="continueMerge">继续合并</el-button>
            </div>
            <div v-for="item in conflicts" :key="`conflict-${item.path}`" class="git-conflict-row">
              <div><FileDiff :size="15" /><code>{{ item.path }}</code></div>
              <div class="git-conflict-actions">
                <el-button text size="small" :icon="FilePenLine" :disabled="Boolean(busy)" @click="editConflict(item)">三栏合并</el-button>
                <el-button text size="small" :icon="Sparkles" :disabled="Boolean(busy) || !status?.aiConfigured" @click="analyzeConflict(item)">AI 分析</el-button>
                <el-button text size="small" :disabled="Boolean(busy)" @click="resolveConflict(item, 'ours')">使用当前</el-button>
                <el-button text size="small" :disabled="Boolean(busy)" @click="resolveConflict(item, 'theirs')">使用传入</el-button>
                <el-button text size="small" :disabled="Boolean(busy)" @click="resolveConflict(item)">标记已解决</el-button>
              </div>
            </div>
          </section>
          <div class="git-status-layout">
            <section class="git-status-changes" aria-label="变动文件">
              <header class="git-status-panel-heading">
                <div><strong>变动</strong><span>{{ changes.length }} 个文件</span></div>
                <el-radio-group v-model="changeView" size="small" class="git-view-switch" aria-label="变动文件显示方式">
                  <el-radio-button value="list"><List :size="14" />列表</el-radio-button>
                  <el-radio-button value="tree"><FolderTree :size="14" />目录</el-radio-button>
                </el-radio-group>
              </header>
              <div class="git-status-change-scroll">
                <div v-if="changeView === 'list' && changes.length" class="git-changes">
                  <div v-for="item in changes" :key="item.path" class="git-change-row" :class="{ 'is-selected': selectedChangePath === item.path }" @click="selectedChangePath = item.path">
                    <span class="git-change-code">{{ item.index }}{{ item.worktree }}</span>
                    <span class="git-change-path" :title="item.originalPath ? `${item.originalPath} → ${item.path}` : item.path">{{ item.originalPath ? `${item.originalPath} → ${item.path}` : item.path }}</span>
                    <span class="git-change-state">{{ changeSource(item) }}</span>
                    <el-button text size="small" :icon="FileDiff" :disabled="Boolean(busy)" @click.stop="showChangeDiff(item)">对比</el-button>
                  </div>
                </div>
                <el-tree v-else-if="changeView === 'tree' && changes.length" class="git-change-tree" :data="changeTree" node-key="id" :expand-on-click-node="true" :default-expanded-keys="expandedChangePaths" :current-node-key="selectedChangePath" highlight-current @node-expand="(data: { id: string }) => { if (!expandedChangePaths.includes(data.id)) expandedChangePaths.push(data.id); }" @node-collapse="(data: { id: string }) => expandedChangePaths = expandedChangePaths.filter(id => id !== data.id)" @node-click="(data: { id: string }) => selectedChangePath = data.id">
                  <template #default="{ data }">
                    <div class="git-tree-node">
                      <Folder v-if="data.directory" :size="15" />
                      <FileText v-else :size="15" />
                      <span class="git-tree-label">{{ data.label }}</span>
                      <template v-if="data.change">
                        <span class="git-change-code">{{ data.change.index }}{{ data.change.worktree }}</span>
                        <span class="git-change-state">{{ changeSource(data.change) }}</span>
                        <el-button text size="small" :icon="FileDiff" :disabled="Boolean(busy)" @click.stop="showChangeDiff(data.change)">对比</el-button>
                      </template>
                    </div>
                  </template>
                </el-tree>
                <div v-else class="git-empty"><Check :size="24" />工作区干净</div>
              </div>
            </section>
            <section class="git-status-commit" aria-label="提交操作">
              <header class="git-status-panel-heading"><div><strong>提交</strong><span>提交时自动包含全部变更</span></div></header>
              <div class="git-commit-editor">
                <el-input v-model="commitMessage" type="textarea" :rows="7" :disabled="Boolean(busy)" placeholder="提交说明，例如：修复登录超时处理" />
                <div class="git-commit-actions">
                  <el-button :icon="Sparkles" :disabled="Boolean(busy) || loading || !changes.length || mergeInProgress || Boolean(conflicts.length)" :loading="aiCommitLoading" :title="mergeInProgress || conflicts.length ? '请先完成合并并处理冲突' : !changes.length ? '没有可分析的代码变更' : '根据全部代码变更生成提交说明'" @click="generateCommitMessage">AI 生成说明</el-button>
                  <el-button type="primary" :icon="behind ? GitMerge : GitCommitHorizontal" :disabled="Boolean(busy) || loading || mergeInProgress || Boolean(conflicts.length) || (!behind && (!changes.length || !commitMessage.trim()))" :loading="busy === 'commit' || busy === 'pull'" @click="commitChanges">{{ behind ? '拉取最新代码' : '提交全部变更' }}</el-button>
                </div>
              </div>
            </section>
          </div>
        </el-tab-pane>

        <el-tab-pane label="提交历史" name="history">
          <div class="git-history-list">
            <article v-for="item in commits" :key="item.hash" class="git-commit-row" @click="showCommit(item)">
              <GitCommitHorizontal :size="16" />
              <div class="git-commit-main"><strong>{{ item.subject || '无提交说明' }}</strong><span>{{ item.author }} · {{ item.date }}</span></div>
              <code>{{ item.shortHash }}</code>
              <ChevronRight :size="15" />
            </article>
            <div v-if="!commits.length" class="git-empty"><History :size="24" />暂无提交历史</div>
          </div>
        </el-tab-pane>

        <el-tab-pane label="分支与远程" name="branches">
          <div class="git-branch-create">
            <el-input v-model="newBranchName" placeholder="新分支名称" @keyup.enter="createBranch" />
            <el-button :icon="CirclePlus" :disabled="Boolean(busy)" :loading="busy === 'create-branch'" @click="createBranch">创建并切换</el-button>
          </div>
          <section class="git-section">
            <div class="git-section-heading"><strong>本地分支</strong><span>{{ localBranches.length }}</span></div>
            <div v-for="item in localBranches" :key="item.name" class="git-branch-row">
              <GitBranch :size="16" />
              <div class="git-branch-main">
                <strong>{{ item.name }}</strong>
                <span>{{ item.hash }} · {{ item.subject || '无提交说明' }}</span>
                <span v-if="item.upstream" class="git-upstream">跟踪 {{ item.upstream }}{{ item.tracking ? ` · ${item.tracking}` : '' }}</span>
              </div>
              <span v-if="item.current" class="git-current-label">当前</span>
              <el-button v-if="!item.current" text size="small" :icon="GitMerge" :disabled="Boolean(busy) || mergeInProgress" :loading="busy === `merge:${item.name}`" @click="mergeBranch(item)">合并</el-button>
              <el-button v-if="!item.current" text size="small" :disabled="Boolean(busy)" @click="switchBranch(item)">切换</el-button>
              <el-button v-if="!item.current" text size="small" class="git-danger" :disabled="Boolean(busy)" :icon="Trash2" @click="deleteBranch(item)">删除</el-button>
            </div>
            <div v-if="!localBranches.length" class="git-subempty">暂无本地分支；新仓库会在首次提交后创建分支引用</div>
          </section>
          <section class="git-section">
            <div class="git-section-heading"><strong>远程分支</strong><span>{{ remoteBranches.length }}</span></div>
            <div v-for="item in remoteBranches" :key="item.name" class="git-branch-row">
              <GitBranch :size="16" />
              <div class="git-branch-main"><strong>{{ item.name }}</strong><span>{{ item.hash }} · {{ item.subject || '无提交说明' }}</span></div>
              <el-button text size="small" :icon="GitMerge" :disabled="Boolean(busy) || mergeInProgress" :loading="busy === `merge:${item.name}`" @click="mergeBranch(item)">合并</el-button>
              <el-button text size="small" :disabled="remoteBranchDisabled(item)" @click="switchBranch(item)">{{ remoteBranchButton(item) }}</el-button>
              <el-button text size="small" class="git-danger" :disabled="Boolean(busy)" :icon="Trash2" @click="deleteRemoteBranch(item)">删除</el-button>
            </div>
            <div v-if="!remoteBranches.length" class="git-subempty">{{ remotes.length ? '暂无远程分支，请先执行 Fetch' : '请先添加远程仓库' }}</div>
          </section>
          <section class="git-section">
            <div class="git-section-heading"><strong>远程仓库</strong><span>{{ remotes.length }}</span></div>
            <div v-for="item in remotes" :key="item.name" class="git-remote-row">
              <div><strong>{{ item.name }}</strong><span>{{ item.url }}</span></div>
              <span v-if="selectedRemote === item.name" class="git-current-label">已选择</span>
              <el-button text size="small" class="git-danger" :disabled="Boolean(busy)" @click="removeRemote(item)">删除</el-button>
            </div>
            <div class="git-remote-form">
              <el-input v-model="remoteName" placeholder="名称，例如 origin" />
              <el-input v-model="remoteURL" placeholder="地址，例如 git@github.com:user/repo.git" />
              <el-button :icon="Plus" :disabled="Boolean(busy)" :loading="busy === 'add-remote'" @click="addRemote">添加远程</el-button>
            </div>
          </section>
        </el-tab-pane>
      </el-tabs>
    </template>


  </GitSurface>

  <GitCompareDialog
    ref="compareRef"
    v-model="changePreviewOpen"
    :embedded="embedded"
    @dirty="mergeDirty = $event"
    @busy="mergeSaving = $event"
    :host="host"
    :session-id="sessionId"
    :repository="repoRoot || repoPath"
    :change="previewChange"
    :merge="previewMerge"
    :ai-configured="status?.aiConfigured"
    @saved="() => { refresh(); emit('changed'); }"
  />

  <GitSurface :embedded="embedded" v-model="commitDetailOpen" class="git-commit-detail" title="提交详情" width="min(860px, calc(100vw - 28px))" append-to-body>
    <div class="git-commit-hash">{{ commitDetailHash }}</div>
    <pre v-loading="commitDetailLoading">{{ commitDetail }}</pre>
    <template #footer><el-button @click="commitDetailOpen = false">关闭</el-button></template>
  </GitSurface>

  <GitSurface :embedded="embedded" v-model="conflictAnalysisOpen" class="git-conflict-analysis" title="AI 冲突分析" width="min(820px, calc(100vw - 28px))" append-to-body>
    <div class="git-conflict-analysis-path">{{ conflictAnalysisPath }}</div>
    <pre v-loading="conflictAnalysisLoading">{{ conflictAnalysis }}</pre>
    <template #footer>
      <el-button @click="conflictAnalysisOpen = false">关闭</el-button>
    </template>
  </GitSurface>

  </div>
</template>

<style scoped>
.git-workspace { height: 100%; min-height: 0; overflow: hidden; position: relative; }
.git-workspace .git-heading { align-items: flex-start; flex-wrap: wrap; }
.git-workspace .git-heading-actions { flex-wrap: wrap; }
.git-workspace .git-conflict-row { flex-wrap: wrap; }
.git-workspace .git-conflict-actions { flex-wrap: wrap; }
.git-workspace .git-commit-detail pre, .git-workspace .git-conflict-analysis pre { max-height: none; }
.git-workspace .git-commit-row:hover { background: var(--surface-2); }

.git-dialog { color: var(--text); font-family: Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; letter-spacing: 0; }
.git-dialog :deep(.el-button), .git-dialog :deep(.el-input__inner), .git-dialog :deep(.el-textarea__inner) { font-family: inherit; }
.git-dialog :deep(.el-button) { font-size: 12px; }
.git-heading, .git-repo-identity, .git-heading-actions, .git-repo-meta, .git-status-summary, .git-change-row, .git-tree-node, .git-commit-row, .git-branch-create, .git-section-heading, .git-branch-row, .git-remote-row, .git-remote-form, .git-conflict-heading, .git-conflict-row, .git-conflict-row > div, .git-conflict-actions, .git-commit-actions { display: flex; align-items: center; }
.git-heading { justify-content: space-between; gap: 16px; margin-bottom: 12px; padding: 12px 14px; border: 1px solid var(--line-strong); border-radius: 9px; background: var(--surface-2); }
.git-repo-identity { min-width: 0; flex: 1; gap: 11px; }
.git-repo-mark { display: grid; width: 36px; height: 36px; flex: 0 0 36px; place-items: center; border: 1px solid var(--line-strong); border-radius: 8px; background: var(--surface); color: var(--accent-strong); }
.git-repo-label { display: grid; min-width: 0; gap: 3px; }
.git-repo-label strong { overflow: hidden; color: var(--text); font-size: 13px; font-weight: 620; text-overflow: ellipsis; white-space: nowrap; }
.git-repo-location { overflow: hidden; color: var(--muted); font: 10px var(--font-mono, monospace); text-overflow: ellipsis; white-space: nowrap; }
.git-heading-actions { flex-wrap: wrap; justify-content: flex-end; gap: 6px; }
.git-action-divider { width: 1px; height: 21px; margin: 0 2px; background: var(--line-strong); }
.git-remote-select { width: 132px; }
.git-repo-meta { flex-wrap: wrap; gap: 7px; margin: 0 2px 12px; color: var(--text-soft); font-size: 11px; }
.git-repo-meta > span:not(.git-branch-pill):not(.git-clean-label) { padding: 4px 8px; border: 1px solid var(--line-strong); border-radius: 5px; background: var(--surface); }
.git-branch-pill { display: inline-flex; align-items: center; gap: 4px; padding: 3px 7px; border: 1px solid #46566f; border-radius: 10px; background: #202d47; color: var(--accent-strong); }
.git-clean-label { display: inline-flex; align-items: center; gap: 3px; color: #87c7a1; }
.git-tabs { margin-top: 0; }
.git-tabs :deep(.el-tabs__item) { height: 40px; font-size: 13px; font-weight: 560; }
.git-status-summary { gap: 14px; padding: 7px 0 10px; color: var(--text-soft); font-size: 12px; }
.git-behind-label { color: #e5ae75; }
.git-status-layout { display: grid; grid-template-columns: minmax(0, 1.3fr) minmax(280px, .82fr); gap: 12px; min-height: 320px; height: min(44vh, 440px); }
.git-status-changes, .git-status-commit { display: flex; min-width: 0; min-height: 0; flex-direction: column; overflow: hidden; border: 1px solid var(--line-strong); background: var(--surface); }
.git-status-panel-heading { display: flex; flex: 0 0 auto; align-items: center; justify-content: space-between; gap: 9px; min-height: 42px; padding: 6px 10px; border-bottom: 1px solid var(--line-strong); }
.git-status-panel-heading > div { display: grid; min-width: 0; gap: 2px; }
.git-status-panel-heading strong { color: var(--text); font-size: 12px; font-weight: 600; }
.git-status-panel-heading span { color: var(--muted); font-size: 10px; }
.git-status-change-scroll { flex: 1; min-height: 0; overflow: auto; }
.git-commit-editor { display: flex; flex: 1; min-height: 0; flex-direction: column; gap: 10px; padding: 11px; }
.git-commit-editor > .el-textarea { flex: 1; min-height: 120px; }
.git-commit-editor :deep(.el-textarea__inner) { height: 100%; min-height: 120px !important; resize: none; }
.git-commit-editor .git-commit-actions { display: grid; flex: 0 0 auto; grid-template-columns: 1fr; gap: 7px; }
.git-commit-editor .git-commit-actions .el-button { width: 100%; margin: 0; }
.git-conflicts { margin: 0 0 12px; border: 1px solid #7a5940; background: #2c241d; }
.git-conflict-heading { gap: 9px; padding: 10px; color: #e2bd8c; }
.git-conflict-heading > div { min-width: 0; flex: 1; display: grid; gap: 2px; }
.git-conflict-heading strong { color: #f0d1a8; font-size: 13px; }
.git-conflict-heading span { color: #c3a783; font-size: 11px; }
.git-conflict-row { justify-content: space-between; gap: 12px; min-height: 42px; padding: 5px 10px; border-top: 1px solid #624a37; }
.git-conflict-row > div:first-child { min-width: 0; flex: 1; gap: 7px; color: #e0b77f; }
.git-conflict-row code { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font: 12px var(--font-mono, monospace); }
.git-conflict-actions { flex: 0 0 auto; gap: 2px; }
.git-view-switch { flex: 0 0 auto; }
.git-view-switch :deep(.el-radio-button__inner) { display: inline-flex; align-items: center; gap: 5px; }
.git-changes, .git-change-tree { min-height: 100%; }
.git-change-row { justify-content: space-between; gap: 12px; min-height: 43px; padding: 4px 8px; border-bottom: 1px solid var(--line-strong); }
.git-change-row .git-change-path { flex: 1; }
.git-change-code { flex: 0 0 20px; color: #d0af7d; font: 12px var(--font-mono, monospace); }
.git-change-path { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font: 12px var(--font-mono, monospace); }
.git-change-state { flex: 0 0 auto; color: var(--muted); font-size: 11px; }
.git-change-tree { background: transparent; color: var(--text); }
.git-change-tree :deep(.el-tree-node__content) { min-height: 30px; height: 30px; background: transparent; }
.git-change-tree :deep(.el-tree-node__content:hover), .git-change-tree :deep(.el-tree-node:focus > .el-tree-node__content) { background: #1d283d; }
.git-change-tree :deep(.el-button) { min-height: 24px; padding-top: 2px; padding-bottom: 2px; }
.git-tree-node { min-width: 0; width: 100%; gap: 6px; padding-right: 4px; color: var(--text-soft); }
.git-tree-label { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font: 12px var(--font-mono, monospace); }
.git-commit-actions { align-self: stretch; flex-direction: column; justify-content: flex-end; gap: 7px; }
.git-commit-actions .el-button { width: 100%; margin-left: 0; }
.git-empty, .git-no-repository { display: grid; place-items: center; align-content: center; gap: 8px; min-height: 180px; color: var(--text-soft); font-size: 12px; }
.git-status-change-scroll > .git-empty { min-height: 100%; }
.git-no-repository { border: 1px dashed var(--line-strong); background: var(--surface); }
.git-no-repository strong { color: var(--text); font-size: 14px; }
.git-no-repository span { max-width: 760px; overflow-wrap: anywhere; text-align: center; }
.git-history-list { min-height: 250px; border-top: 1px solid var(--line-strong); }
.git-commit-row { gap: 10px; min-height: 63px; padding: 8px 9px; border-bottom: 1px solid var(--line-strong); color: var(--muted); cursor: pointer; }
.git-commit-row:hover { background: #1d283d; }
.git-commit-main, .git-branch-main, .git-remote-row > div { min-width: 0; flex: 1; display: grid; gap: 4px; }
.git-commit-main strong, .git-branch-main strong, .git-remote-row strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font-size: 13px; }
.git-commit-main span, .git-branch-main span, .git-remote-row span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--muted); font-size: 11px; }
.git-branch-main .git-upstream { color: var(--accent-strong); }
.git-commit-row code, .git-commit-hash { color: var(--accent-strong); font: 11px var(--font-mono, monospace); }
.git-branch-create, .git-remote-form { gap: 8px; margin: 7px 0 15px; }
.git-branch-create .el-input { max-width: 360px; }
.git-section { margin-top: 14px; border-top: 1px solid var(--line-strong); }
.git-section-heading { justify-content: space-between; padding: 11px 8px 7px; color: var(--text); font-size: 13px; }
.git-section-heading span { color: var(--muted); font-size: 11px; }
.git-branch-row, .git-remote-row { gap: 9px; min-height: 53px; padding: 6px 8px; border-bottom: 1px solid var(--line-strong); color: var(--muted); }
.git-current-label { flex: 0 0 auto; color: #87c7a1; font-size: 11px; }
.git-remote-row > div { gap: 3px; }
.git-remote-form .el-input:first-child { flex: 0 0 150px; }
.git-remote-form .el-input:nth-child(2) { flex: 1; }
.git-danger:not(:disabled) { color: #d98781; }
.git-danger:not(:disabled):hover { color: #ef8e86; }
.git-subempty { padding: 10px 8px; color: var(--muted); font-size: 12px; }
.git-error { display: grid; gap: 3px; margin-top: 10px; padding: 9px 11px; border: 1px solid #784b47; background: #321f1e; color: #e6aaa4; font-size: 12px; }
.git-error strong { color: #f1c0ba; }
.git-commit-detail :deep(.el-dialog__body) { padding-top: 4px; }
.git-commit-detail pre { max-height: 65vh; min-height: 220px; margin: 10px 0 0; overflow: auto; padding: 14px; background: var(--field-bg); color: var(--text); font: 12px/1.55 var(--font-mono, monospace); white-space: pre-wrap; word-break: break-word; }
.git-conflict-analysis-path { overflow-wrap: anywhere; color: var(--accent-strong); font: 12px var(--font-mono, monospace); }
.git-conflict-analysis pre { min-height: 260px; max-height: 62vh; overflow: auto; margin: 10px 0 0; padding: 14px; border: 1px solid var(--line-strong); background: var(--field-bg); color: var(--text); font: 13px/1.65 var(--font-mono, monospace); white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 720px) {
  .git-heading { align-items: stretch; flex-direction: column; padding: 12px; }
  .git-heading-actions { justify-content: flex-start; }
  .git-repo-identity { width: 100%; }
  .git-heading-actions > .el-button { flex: 1 1 auto; }
  .git-remote-select { width: min(180px, 100%); }
  .git-status-layout { grid-template-columns: minmax(0, 1fr); height: auto; }
  .git-status-changes { min-height: 220px; max-height: 45vh; }
  .git-status-commit { min-height: 250px; }
  .git-view-switch { align-self: flex-start; }
  .git-conflict-heading, .git-conflict-row { align-items: stretch; flex-direction: column; }
  .git-conflict-actions { flex-wrap: wrap; }
  .git-remote-form { flex-wrap: wrap; }
  .git-remote-form .el-input:first-child, .git-remote-form .el-input:nth-child(2) { flex: 1 1 100%; }
  .git-change-row .git-change-state, .git-tree-node .git-change-state { display: none; }
}
</style>
