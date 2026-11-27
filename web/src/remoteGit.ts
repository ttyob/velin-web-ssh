import { api, json } from "./api";
import { parseGitBranches, parseGitStatus } from "./git";
import { truncateGitAIContent } from "./gitAI";
import type { AgentStatus } from "./types";

export function shellQuote(value: string) { return `'${value.replace(/'/g, `'\\''`)}'`; }

export function createRemoteGit(hostID: string) {
  const base = `/api/hosts/${encodeURIComponent(hostID)}/agent`;
  async function execute(command: string) {
    const result = await api<{ success: boolean; output?: string; error?: string }>(`${base}/command`, {
      method: "POST", body: json({ command }),
    });
    if (!result.success) throw new Error(result.error || result.output || "Git 命令执行失败");
    return result.output || "";
  }
  function command(repository: string, args: string) {
    return execute(`git --no-optional-locks -C ${shellQuote(repository)} --literal-pathspecs ${args}`);
  }
  return {
    async connect() {
      const status = await api<AgentStatus>(base);
      return status.state === "connected" ? status : await api<AgentStatus>(`${base}/connect`, { method: "POST", body: json({}) });
    },
    async generateCommitMessage(root: string, branch: string) {
      const repository = shellQuote(root);
      const prefix = `GIT_INDEX_FILE="$temporary_index" git -C ${repository} --literal-pathspecs`;
      const diff = await execute(`temporary_index=$(mktemp) && rm -f "$temporary_index" && trap 'rm -f "$temporary_index"' EXIT && if git -C ${repository} rev-parse --verify HEAD >/dev/null 2>&1; then GIT_INDEX_FILE="$temporary_index" git -C ${repository} read-tree HEAD; else GIT_INDEX_FILE="$temporary_index" git -C ${repository} read-tree --empty; fi && ${prefix} add -A && ${prefix} diff --cached --no-ext-diff --no-color --stat && printf '\\n--- PATCH ---\\n' && ${prefix} diff --cached --no-ext-diff --no-color`);
      return api<{ message: string; model: string }>("/api/agent/git-assist", {
        method: "POST", body: json({ kind: "commit_message", branch, content: truncateGitAIContent(diff) }),
      });
    },
    editIndex: (repository: string, path: string, expected: string, exists: boolean, content: string, remove: boolean) => api(`${base}/git-index`, { method: "POST", body: json({ repository, path, expected, exists, content, remove }) }),
    root: async (path: string) => (await command(path, "rev-parse --show-toplevel")).trim(),
    status: async (root: string) => {
      const output = await command(root, "status --porcelain=v1 -z --branch --untracked-files=all");
      if (output.endsWith("\n[output truncated]")) throw new Error("仓库变更过多，状态输出不完整，请在终端缩小范围后处理");
      return parseGitStatus(output);
    },
    blob: (repository: string, path: string, revision: "HEAD" | "0") => api<{ content: string; exists: boolean }>(`${base}/git-text?${new URLSearchParams({ repository, path, revision })}`),
    stage: (root: string, path?: string) => command(root, path ? `add -- ${shellQuote(path)}` : "add -A"),
    async unstage(root: string, path?: string) {
      const spec = path ? shellQuote(path) : ".";
      // An initial repository has an index but no HEAD to restore from.
      try { await command(root, "rev-parse --verify HEAD"); }
      catch { return command(root, `rm -r --cached -- ${spec}`); }
      return command(root, `restore --staged -- ${spec}`);
    },
    commit: (root: string, message: string, amend = false) => command(root, `commit ${amend ? "--amend " : ""}-m ${shellQuote(message)}`),
    branches: async (root: string) => parseGitBranches(await command(root, "for-each-ref --format='%(HEAD)%09%(refname)%09%(objectname:short)%09%(upstream:short)%09%(upstream:track)%09%(subject)' refs/heads refs/remotes")),
    switchBranch: (root: string, name: string) => command(root, `switch -- ${shellQuote(name)}`),
    switchRemoteBranch: (root: string, name: string) => command(root, `switch --track ${shellQuote(name)}`),
    pull: (root: string) => command(root, "pull --ff-only"),
    push: (root: string) => command(root, "push"),
  };
}
