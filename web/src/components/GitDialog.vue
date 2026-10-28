<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import {
  Archive,
  Check,
  ChevronRight,
  CirclePlus,
  Download,
  FileDiff,
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
  Trash2,
  Upload,
  X,
} from "@lucide/vue";
import { ElMessage, ElMessageBox, type TreeInstance } from "element-plus";
import { api, json } from "../api";
import {
  buildGitChangeTree,
  isStaged,
  parseGitBranches,
  parseGitCommits,
  parseGitRemotes,
  parseGitStatus,
  parseUnifiedDiff,
  type GitBranch as GitBranchInfo,
  type GitChange,
  type GitCommit,
  type GitRemote,
  type GitTreeNode,
} from "../git";
import type { AgentStatus, Host } from "../types";

type GitTab = "status" | "history" | "branches";
type ChangeView = "list" | "tree";
type ChangePreviewMode = "split" | "unified";
type DiffLineKind = "add" | "remove" | "hunk" | "meta" | "section" | "plain";

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  sessionId?: string;
  initialPath?: string;
}>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();

const activeTab = ref<GitTab>("status");
const status = ref<AgentStatus>();
const loading = ref(false);
const busy = ref("");
const error = ref("");
const repoPath = ref(".");
const repoRoot = ref("");
const branch = ref("");
const tracking = ref("");
const ahead = ref(0);
const behind = ref(0);
const changes = ref<GitChange[]>([]);
const selectedChangePaths = ref<string[]>([]);
const commits = ref<GitCommit[]>([]);
const branches = ref<GitBranchInfo[]>([]);
const remotes = ref<GitRemote[]>([]);
const commitMessage = ref("");
const newBranchName = ref("");
const remoteName = ref("");
const remoteURL = ref("");
const selectedRemote = ref("");
const changeView = ref<ChangeView>("list");
const changeTreeRef = ref<TreeInstance>();
const splitLeftRef = ref<HTMLElement>();
const splitRightRef = ref<HTMLElement>();
const changePreviewOpen = ref(false);
const changePreviewLoading = ref(false);
const changePreviewPath = ref("");
const changePreviewContent = ref("");
const changeFullDiffContent = ref("");
const changePreviewMode = ref<ChangePreviewMode>("unified");
const commitDetailOpen = ref(false);
const commitDetailLoading = ref(false);
const commitDetailHash = ref("");
const commitDetail = ref("");

const connected = computed(() => status.value?.state === "connected");
const localBranches = computed(() => branches.value.filter((item) => !item.remote));
const remoteBranches = computed(() => branches.value.filter((item) => item.remote));
const stagedCount = computed(() => changes.value.filter((item) => isStaged(item)).length);
const unstagedCount = computed(() => changes.value.filter((item) => item.worktree !== " ").length);
const currentRemote = computed(() => selectedRemote.value || remotes.value[0]?.name || "");
const changeTree = computed(() => buildGitChangeTree(changes.value));
const allChangesSelected = computed(() => changes.value.length > 0 && selectedChangePaths.value.length === changes.value.length);
const someChangesSelected = computed(() => selectedChangePaths.value.length > 0 && !allChangesSelected.value);
const changePreviewLines = computed(() => changePreviewContent.value.split("\n").map((text) => {
  let kind: DiffLineKind = "plain";
  if (text.startsWith("=== ")) kind = "section";
  else if (text.startsWith("@@")) kind = "hunk";
  else if (text.startsWith("diff --git") || text.startsWith("index ") || text.startsWith("--- ") || text.startsWith("+++ ")) kind = "meta";
  else if (text.startsWith("+")) kind = "add";
  else if (text.startsWith("-")) kind = "remove";
  return { text, kind };
}));
const fullComparisonRows = computed(() => parseUnifiedDiff(changeFullDiffContent.value));

function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'\\''`)}'`;
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
  const result = await api<{ output: string; success: boolean; error?: string }>(
    `/api/hosts/${props.host.id}/agent/command`,
    { method: "POST", body: json({ command: commandText }) },
  );
  if (!result.success) throw new Error(result.error || result.output || "Git 命令执行失败");
  return result.output || "";
}

function gitCommand(args: string) {
  return `git -C ${shellQuote(repoPath.value.trim() || ".")} ${args}`;
}

function applyStatus(output: string) {
  const value = parseGitStatus(output);
  branch.value = value.branch;
  tracking.value = value.tracking;
  ahead.value = value.ahead;
  behind.value = value.behind;
  changes.value = value.changes;
  selectedChangePaths.value = selectedChangePaths.value.filter((path) =>
    changes.value.some((item) => item.path === path),
  );
}

async function refresh() {
  if (!props.host) return;
  loading.value = true;
  error.value = "";
  try {
    await ensureConnected();
    repoRoot.value = (await command(gitCommand("rev-parse --show-toplevel"))).trim();
    const [statusOutput, commitOutput, branchOutput, remoteOutput] = await Promise.all([
      command(gitCommand("status --porcelain=v1 -z --branch --untracked-files=all")),
      command(gitCommand("log -50 --date=iso-strict --pretty=format:'%H%x09%h%x09%an%x09%ad%x09%s'")),
      command(gitCommand("for-each-ref --format='%(HEAD)%09%(refname)%09%(objectname:short)%09%(upstream:short)%09%(upstream:track)%09%(subject)' refs/heads refs/remotes")),
      command(gitCommand("remote -v")),
    ]);
    applyStatus(statusOutput);
    commits.value = parseGitCommits(commitOutput);
    branches.value = parseGitBranches(branchOutput);
    remotes.value = parseGitRemotes(remoteOutput);
    if (!selectedRemote.value || !remotes.value.some((item) => item.name === selectedRemote.value))
      selectedRemote.value = remotes.value[0]?.name || "";
  } catch (cause) {
    repoRoot.value = "";
    changes.value = [];
    commits.value = [];
    branches.value = [];
    remotes.value = [];
    error.value = cause instanceof Error ? cause.message : "Git 仓库读取失败";
  } finally {
    loading.value = false;
  }
}

async function runOperation(key: string, operation: () => Promise<void>, message: string) {
  busy.value = key;
  try {
    await operation();
    ElMessage.success(message);
    await refresh();
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "Git 操作失败");
  } finally {
    busy.value = "";
  }
}

function selectedPaths() {
  return selectedChangePaths.value.map(shellQuote).join(" ");
}

function toggleAllChanges(value: string | number | boolean) {
  selectedChangePaths.value = value ? changes.value.map((item) => item.path) : [];
}

function updateTreeSelection() {
  const checked = (changeTreeRef.value?.getCheckedNodes(true) || []) as GitTreeNode[];
  selectedChangePaths.value = checked
    .map((item) => item.change?.path)
    .filter((path): path is string => Boolean(path));
}

function syncSplitScroll(sourceSide: "left" | "right", event: Event) {
  const source = event.currentTarget;
  if (!(source instanceof HTMLElement)) return;
  const target = sourceSide === "left" ? splitRightRef.value : splitLeftRef.value;
  if (!target) return;
  if (target.scrollLeft !== source.scrollLeft) target.scrollLeft = source.scrollLeft;
  if (target.scrollTop !== source.scrollTop) target.scrollTop = source.scrollTop;
}

async function stageSelected() {
  if (!selectedChangePaths.value.length) return ElMessage.warning("请先选择文件");
  await runOperation("stage", () => command(gitCommand(`add -- ${selectedPaths()}`)).then(() => undefined), "已暂存所选文件");
}

async function stageAll() {
  await runOperation("stage-all", () => command(gitCommand("add -A")).then(() => undefined), "已暂存全部变更");
}

async function unstageSelected() {
  if (!selectedChangePaths.value.length) return ElMessage.warning("请先选择文件");
  await runOperation("unstage", () => command(gitCommand(`restore --staged -- ${selectedPaths()}`)).then(() => undefined), "已取消暂存");
}

async function commitChanges() {
  const message = commitMessage.value.trim();
  if (!message) return ElMessage.warning("请输入提交说明");
  if (!stagedCount.value) return ElMessage.warning("请先暂存要提交的文件");
  await runOperation("commit", async () => {
    await command(gitCommand(`commit -m ${shellQuote(message)}`));
    commitMessage.value = "";
  }, "提交已创建");
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

async function showChangeDiff(item: GitChange) {
  changePreviewOpen.value = true;
  changePreviewLoading.value = true;
  changePreviewPath.value = item.path;
  changePreviewContent.value = "";
  changeFullDiffContent.value = "";
  changePreviewMode.value = "unified";
  try {
    const compactDiff = async () => {
      const sections: string[] = [];
      if (isStaged(item)) {
        const output = await command(gitCommand(`diff --cached --no-ext-diff --no-color -- ${shellQuote(item.path)}`));
        sections.push(`=== 已暂存变动 ===\n${output.trimEnd()}`);
      }
      if (item.worktree !== " ") {
        const args = item.index === "?" && item.worktree === "?"
          ? `diff --no-index --no-ext-diff --no-color -- /dev/null ${shellQuote(item.path)} || true`
          : `diff --no-ext-diff --no-color -- ${shellQuote(item.path)}`;
        const output = await command(gitCommand(args));
        sections.push(`=== 工作区变动 ===\n${output.trimEnd()}`);
      }
      return sections.filter((section) => section.trim()).join("\n\n");
    };
    const comparisonPaths = [...new Set([item.originalPath, item.path].filter((path): path is string => Boolean(path)))]
      .map(shellQuote)
      .join(" ");
    let fullArgs: string;
    if (item.index === "?" && item.worktree === "?") {
      fullArgs = `diff --no-index --no-ext-diff --no-color --unified=999999 -- /dev/null ${shellQuote(item.path)} || true`;
    } else if (commits.value.length) {
      fullArgs = `diff --no-ext-diff --no-color --unified=999999 HEAD -- ${comparisonPaths}`;
    } else if (item.worktree !== "D") {
      fullArgs = `diff --no-index --no-ext-diff --no-color --unified=999999 -- /dev/null ${shellQuote(item.path)} || true`;
    } else {
      fullArgs = `diff --cached --no-ext-diff --no-color --unified=999999 -- ${comparisonPaths}`;
    }
    const [compactOutput, fullOutput] = await Promise.all([
      compactDiff(),
      command(gitCommand(fullArgs)),
    ]);
    changePreviewContent.value = compactOutput || "没有可显示的文本差异，文件可能是二进制文件。";
    changeFullDiffContent.value = fullOutput;
  } catch (cause) {
    changePreviewContent.value = cause instanceof Error ? cause.message : "文件差异读取失败";
    changeFullDiffContent.value = "";
    changePreviewMode.value = "unified";
  } finally {
    changePreviewLoading.value = false;
  }
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
    if (!open) return;
    activeTab.value = "status";
    repoPath.value = props.initialPath?.trim() || props.host?.initialDirectory || ".";
    repoRoot.value = "";
    error.value = "";
    status.value = undefined;
    changePreviewOpen.value = false;
    void refresh();
  },
);

watch(
  [selectedChangePaths, changeView, changeTree],
  async () => {
    if (changeView.value !== "tree") return;
    await nextTick();
    changeTreeRef.value?.setCheckedKeys(
      selectedChangePaths.value.map((path) => `file:${path}`),
      true,
    );
  },
  { deep: true, flush: "post" },
);
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="git-dialog"
    :title="`Git 管理 · ${host?.name || '当前终端'}`"
    width="min(1080px, calc(100vw - 28px))"
    append-to-body
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="git-heading">
      <div class="git-path-form">
        <GitBranch :size="18" />
        <el-input v-model="repoPath" placeholder="当前终端目录或仓库路径" @keyup.enter="refresh" />
      </div>
      <div class="git-heading-actions">
        <el-select v-if="remotes.length" v-model="selectedRemote" class="git-remote-select" placeholder="远程仓库">
          <el-option v-for="item in remotes" :key="item.name" :label="item.name" :value="item.name" />
        </el-select>
        <el-button :icon="Download" :disabled="!repoRoot || !remotes.length || Boolean(busy)" :loading="busy === 'fetch'" @click="fetchRepository">Fetch</el-button>
        <el-button :icon="GitMerge" :disabled="!repoRoot || !tracking || Boolean(busy)" :loading="busy === 'pull'" :title="tracking ? `从 ${tracking} 拉取` : '当前分支没有上游分支'" @click="pullRepository">Pull</el-button>
        <el-button :icon="Upload" :disabled="!repoRoot || !remotes.length || Boolean(busy)" :loading="busy === 'push'" @click="pushRepository">Push</el-button>
        <el-button :icon="RefreshCw" :disabled="Boolean(busy)" :loading="loading" @click="refresh">刷新</el-button>
      </div>
    </div>

    <div v-if="repoRoot" class="git-repo-meta">
      <span class="git-repo-root" :title="repoRoot">{{ repoRoot }}</span>
      <span class="git-branch-pill"><GitBranch :size="13" />{{ branch }}</span>
      <span v-if="tracking">跟踪 {{ tracking }}</span>
      <span v-if="ahead">领先 {{ ahead }}</span>
      <span v-if="behind">落后 {{ behind }}</span>
      <span v-if="!ahead && !behind && tracking" class="git-clean-label"><Check :size="13" />已同步</span>
    </div>

    <div v-if="!repoRoot" class="git-no-repository" v-loading="loading">
      <GitBranch :size="30" />
      <strong>{{ error ? '当前目录不是 Git 仓库' : '正在检查 Git 仓库' }}</strong>
      <span v-if="error">{{ error }}</span>
      <el-button v-if="!loading" type="primary" :icon="GitCommitHorizontal" @click="initializeRepository">初始化 Git 仓库</el-button>
    </div>

    <template v-else>
      <el-tabs v-model="activeTab" class="git-tabs">
        <el-tab-pane label="状态与提交" name="status">
          <div class="git-status-summary">
            <span>{{ changes.length }} 个变更</span>
            <span>{{ stagedCount }} 个已暂存</span>
            <span>{{ unstagedCount }} 个未暂存</span>
          </div>
          <div class="git-toolbar">
            <div class="git-selection-tools">
              <el-checkbox :model-value="allChangesSelected" :indeterminate="someChangesSelected" :disabled="!changes.length || Boolean(busy)" @change="toggleAllChanges">全选</el-checkbox>
              <span v-if="selectedChangePaths.length" class="git-selected-count">已选 {{ selectedChangePaths.length }}</span>
              <el-button size="small" :icon="Plus" :disabled="!selectedChangePaths.length || Boolean(busy)" :loading="busy === 'stage'" @click="stageSelected">暂存所选</el-button>
              <el-button size="small" :icon="Archive" :disabled="Boolean(busy)" :loading="busy === 'stage-all'" @click="stageAll">暂存全部</el-button>
              <el-button size="small" :icon="X" :disabled="!selectedChangePaths.length || Boolean(busy)" :loading="busy === 'unstage'" @click="unstageSelected">取消暂存</el-button>
            </div>
            <el-radio-group v-model="changeView" size="small" class="git-view-switch" aria-label="变动文件显示方式">
              <el-radio-button value="list"><List :size="14" />列表</el-radio-button>
              <el-radio-button value="tree"><FolderTree :size="14" />文件夹</el-radio-button>
            </el-radio-group>
          </div>
          <el-checkbox-group v-if="changeView === 'list'" v-model="selectedChangePaths" class="git-changes">
            <div v-for="item in changes" :key="`${item.index}${item.worktree}-${item.path}`" class="git-change-row">
              <el-checkbox :label="item.path" :value="item.path">
                <span class="git-change-code">{{ item.index }}{{ item.worktree }}</span>
                <span class="git-change-path">{{ item.originalPath ? `${item.originalPath} → ${item.path}` : item.path }}</span>
              </el-checkbox>
              <span class="git-change-state">{{ isStaged(item) ? '已暂存' : '' }}{{ isStaged(item) && item.worktree !== ' ' ? ' · ' : '' }}{{ item.worktree !== ' ' ? '工作区' : '' }}</span>
              <el-button text size="small" :icon="FileDiff" :disabled="Boolean(busy)" @click.stop="showChangeDiff(item)">对比</el-button>
            </div>
          </el-checkbox-group>
          <el-tree
            v-else-if="changes.length"
            ref="changeTreeRef"
            class="git-change-tree"
            :data="changeTree"
            node-key="id"
            show-checkbox
            :expand-on-click-node="true"
            @check="updateTreeSelection"
          >
            <template #default="{ data }">
              <div class="git-tree-node">
                <Folder v-if="data.directory" :size="15" />
                <FileText v-else :size="15" />
                <span class="git-tree-label">{{ data.label }}</span>
                <template v-if="data.change">
                  <span class="git-change-code">{{ data.change.index }}{{ data.change.worktree }}</span>
                  <span class="git-change-state">{{ isStaged(data.change) ? '已暂存' : '' }}{{ isStaged(data.change) && data.change.worktree !== ' ' ? ' · ' : '' }}{{ data.change.worktree !== ' ' ? '工作区' : '' }}</span>
                  <el-button text size="small" :icon="FileDiff" :disabled="Boolean(busy)" @click.stop="showChangeDiff(data.change)">对比</el-button>
                </template>
              </div>
            </template>
          </el-tree>
          <div v-if="!changes.length" class="git-empty"><Check :size="24" />工作区干净</div>
          <div class="git-commit-form">
            <el-input v-model="commitMessage" type="textarea" :rows="3" placeholder="提交说明，例如：修复登录超时处理" />
            <el-button type="primary" :icon="GitCommitHorizontal" :disabled="!stagedCount || Boolean(busy)" :loading="busy === 'commit'" @click="commitChanges">提交已暂存内容</el-button>
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

    <div v-if="error && repoRoot" class="git-error"><strong>Git 操作失败</strong><span>{{ error }}</span></div>
  </el-dialog>

  <el-dialog v-model="changePreviewOpen" class="git-diff-dialog" title="文件变动对比" width="min(980px, calc(100vw - 28px))" append-to-body>
    <div class="git-diff-toolbar">
      <div class="git-diff-path" :title="changePreviewPath"><FileDiff :size="15" />{{ changePreviewPath }}</div>
      <el-radio-group v-model="changePreviewMode" size="small" aria-label="文件对比模式">
        <el-radio-button value="split">全量对比</el-radio-button>
        <el-radio-button value="unified">变更内容</el-radio-button>
      </el-radio-group>
    </div>
    <div v-loading="changePreviewLoading">
      <div v-if="changePreviewMode === 'split' && fullComparisonRows.length" class="git-split-diff">
        <div class="git-split-header"><span>原始版本</span><span>当前版本</span></div>
        <div class="git-split-columns">
          <div ref="splitLeftRef" class="git-split-pane git-split-left" @scroll="syncSplitScroll('left', $event)">
            <div class="git-split-pane-content">
              <div v-for="(row, index) in fullComparisonRows" :key="index" class="git-split-line" :class="`is-${row.kind}`"><span class="git-line-number">{{ row.leftNumber || '' }}</span><code>{{ row.left || ' ' }}</code></div>
            </div>
          </div>
          <div ref="splitRightRef" class="git-split-pane git-split-right" @scroll="syncSplitScroll('right', $event)">
            <div class="git-split-pane-content">
              <div v-for="(row, index) in fullComparisonRows" :key="index" class="git-split-line" :class="`is-${row.kind}`"><span class="git-line-number">{{ row.rightNumber || '' }}</span><code>{{ row.right || ' ' }}</code></div>
            </div>
          </div>
        </div>
      </div>
      <div v-else-if="changePreviewMode === 'split'" class="git-diff-unavailable">没有可显示的全量文本对比，文件可能是二进制文件。</div>
      <div v-else class="git-diff-view">
        <div v-for="(line, index) in changePreviewLines" :key="index" class="git-diff-line" :class="`is-${line.kind}`"><code>{{ line.text || ' ' }}</code></div>
      </div>
    </div>
    <template #footer><el-button @click="changePreviewOpen = false">关闭</el-button></template>
  </el-dialog>

  <el-dialog v-model="commitDetailOpen" class="git-commit-detail" title="提交详情" width="min(860px, calc(100vw - 28px))" append-to-body>
    <div class="git-commit-hash">{{ commitDetailHash }}</div>
    <pre v-loading="commitDetailLoading">{{ commitDetail }}</pre>
    <template #footer><el-button @click="commitDetailOpen = false">关闭</el-button></template>
  </el-dialog>
</template>

<style scoped>
.git-dialog { color: var(--text); font-family: Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; letter-spacing: 0; }
.git-dialog :deep(.el-button), .git-dialog :deep(.el-input__inner), .git-dialog :deep(.el-textarea__inner) { font-family: inherit; }
.git-dialog :deep(.el-button) { font-size: 12px; }
.git-heading, .git-path-form, .git-heading-actions, .git-repo-meta, .git-status-summary, .git-toolbar, .git-selection-tools, .git-change-row, .git-tree-node, .git-commit-row, .git-branch-create, .git-section-heading, .git-branch-row, .git-remote-row, .git-remote-form, .git-diff-toolbar, .git-diff-path { display: flex; align-items: center; }
.git-heading { justify-content: space-between; gap: 14px; margin-bottom: 10px; }
.git-path-form { min-width: 0; flex: 1; gap: 8px; color: var(--text-soft); }
.git-path-form .el-input { max-width: 540px; }
.git-heading-actions { flex-wrap: wrap; justify-content: flex-end; gap: 6px; }
.git-remote-select { width: 132px; }
.git-repo-meta { flex-wrap: wrap; gap: 10px; margin-bottom: 8px; color: var(--text-soft); font-size: 12px; }
.git-repo-root { min-width: 0; max-width: 44%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: var(--font-mono, monospace); }
.git-branch-pill { display: inline-flex; align-items: center; gap: 4px; padding: 3px 7px; border: 1px solid #46566f; border-radius: 10px; background: #202d47; color: var(--accent-strong); }
.git-clean-label { display: inline-flex; align-items: center; gap: 3px; color: #87c7a1; }
.git-tabs { margin-top: 2px; }
.git-tabs :deep(.el-tabs__item) { height: 40px; font-size: 13px; font-weight: 560; }
.git-status-summary { gap: 14px; padding: 7px 0 10px; color: var(--text-soft); font-size: 12px; }
.git-toolbar { justify-content: space-between; gap: 9px; margin-bottom: 9px; }
.git-selection-tools { flex-wrap: wrap; gap: 7px; }
.git-selected-count { color: var(--accent-strong); font-size: 11px; }
.git-view-switch { flex: 0 0 auto; }
.git-view-switch :deep(.el-radio-button__inner) { display: inline-flex; align-items: center; gap: 5px; }
.git-changes, .git-change-tree { min-height: 150px; border-top: 1px solid var(--line-strong); }
.git-change-row { justify-content: space-between; gap: 12px; min-height: 43px; padding: 4px 8px; border-bottom: 1px solid var(--line-strong); }
.git-change-row :deep(.el-checkbox) { min-width: 0; flex: 1; margin-right: 0; }
.git-change-row :deep(.el-checkbox__label) { display: inline-flex; min-width: 0; align-items: center; gap: 9px; }
.git-change-code { flex: 0 0 20px; color: #d0af7d; font: 12px var(--font-mono, monospace); }
.git-change-path { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font: 12px var(--font-mono, monospace); }
.git-change-state { flex: 0 0 auto; color: var(--muted); font-size: 11px; }
.git-change-tree { background: transparent; color: var(--text); }
.git-change-tree :deep(.el-tree-node__content) { min-height: 30px; height: 30px; background: transparent; }
.git-change-tree :deep(.el-tree-node__content:hover), .git-change-tree :deep(.el-tree-node:focus > .el-tree-node__content) { background: #1d283d; }
.git-change-tree :deep(.el-checkbox) { height: 28px; }
.git-change-tree :deep(.el-button) { min-height: 24px; padding-top: 2px; padding-bottom: 2px; }
.git-tree-node { min-width: 0; width: 100%; gap: 6px; padding-right: 4px; color: var(--text-soft); }
.git-tree-label { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font: 12px var(--font-mono, monospace); }
.git-commit-form { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 9px; margin-top: 14px; }
.git-empty, .git-no-repository { display: grid; place-items: center; align-content: center; gap: 8px; min-height: 180px; color: var(--text-soft); font-size: 12px; }
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
.git-diff-toolbar { justify-content: space-between; gap: 10px; margin-bottom: 9px; }
.git-diff-path { min-width: 0; flex: 1; gap: 7px; color: var(--accent-strong); font: 12px var(--font-mono, monospace); overflow-wrap: anywhere; }
.git-diff-view { min-height: 280px; max-height: 65vh; overflow: auto; border: 1px solid var(--line-strong); background: var(--field-bg); color: var(--text); font: 12px/1.55 var(--font-mono, monospace); }
.git-diff-line { width: max-content; min-width: 100%; padding: 0 12px; }
.git-diff-line code { white-space: pre; font: inherit; }
.git-diff-line.is-add { background: #173324; color: #9bd2ad; }
.git-diff-line.is-remove { background: #3a2023; color: #e6a3a8; }
.git-diff-line.is-hunk { background: #1b2b44; color: var(--accent-strong); }
.git-diff-line.is-meta { color: var(--text-soft); }
.git-diff-line.is-section { position: sticky; top: 0; z-index: 1; padding-top: 6px; padding-bottom: 6px; background: var(--surface-2); color: #e1c38d; font-weight: 650; }
.git-split-diff { min-height: 280px; overflow: hidden; border: 1px solid var(--line-strong); background: var(--field-bg); color: var(--text); font: 12px/1.55 var(--font-mono, monospace); }
.git-split-header { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
.git-split-header { position: sticky; top: 0; z-index: 2; background: var(--surface-2); color: var(--text-soft); font-weight: 650; }
.git-split-header span { padding: 7px 10px 7px 54px; }
.git-split-header span + span { border-left: 1px solid var(--line-strong); }
.git-split-columns { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); min-width: 0; height: min(60vh, 620px); min-height: 250px; overflow: hidden; }
.git-split-pane { min-width: 0; overflow-x: scroll; overflow-y: auto; scrollbar-color: #56616c #151b21; scrollbar-gutter: stable; }
.git-split-pane::-webkit-scrollbar { width: 10px; height: 10px; }
.git-split-pane::-webkit-scrollbar-track { background: var(--surface); }
.git-split-pane::-webkit-scrollbar-thumb { border: 2px solid var(--line); border-radius: 5px; background: #56616c; }
.git-split-pane::-webkit-scrollbar-thumb:hover { background: #74818c; }
.git-split-pane + .git-split-pane { border-left: 1px solid var(--line-strong); }
.git-split-pane-content { width: max-content; min-width: 100%; }
.git-split-line { display: grid; grid-template-columns: 44px minmax(max-content, 1fr); width: max-content; min-width: 100%; min-height: 19px; }
.git-split-line code { padding: 0 9px; white-space: pre; font: inherit; }
.git-line-number { padding: 0 7px; border-right: 1px solid var(--line-strong); color: var(--muted); text-align: right; user-select: none; }
.git-split-left .git-split-line.is-change, .git-split-left .git-split-line.is-remove { background: #3a2023; color: #e6a3a8; }
.git-split-right .git-split-line.is-change, .git-split-right .git-split-line.is-add { background: #173324; color: #9bd2ad; }
.git-split-left .git-split-line.is-add, .git-split-right .git-split-line.is-remove { background: var(--surface); }
.git-diff-unavailable { display: grid; min-height: 280px; place-items: center; border: 1px solid var(--line-strong); background: var(--field-bg); color: var(--text-soft); font-size: 12px; }
.git-commit-detail :deep(.el-dialog__body) { padding-top: 4px; }
.git-commit-detail pre { max-height: 65vh; min-height: 220px; margin: 10px 0 0; overflow: auto; padding: 14px; background: var(--field-bg); color: var(--text); font: 12px/1.55 var(--font-mono, monospace); white-space: pre-wrap; word-break: break-word; }
@media (max-width: 720px) {
  .git-heading { align-items: stretch; flex-direction: column; }
  .git-heading-actions { justify-content: flex-start; }
  .git-remote-select { width: min(180px, 100%); }
  .git-toolbar { align-items: stretch; flex-direction: column; }
  .git-view-switch { align-self: flex-start; }
  .git-diff-toolbar { align-items: stretch; flex-direction: column; }
  .git-diff-toolbar .el-radio-group { align-self: flex-start; }
  .git-repo-root { max-width: 100%; flex-basis: 100%; }
  .git-commit-form { grid-template-columns: 1fr; }
  .git-remote-form { flex-wrap: wrap; }
  .git-remote-form .el-input:first-child, .git-remote-form .el-input:nth-child(2) { flex: 1 1 100%; }
  .git-change-row .git-change-state, .git-tree-node .git-change-state { display: none; }
}
</style>
