<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, type Component } from "vue";
import { api } from "../api";
import type { Host, TerminalSession } from "../types";

type Tool = "files" | "agent" | "share" | "monitor" | "git" | "docker";
interface Context { tool: Tool; host: Host; session: TerminalSession; serviceURL: string }
declare global { interface Window { VelinTools?: { postMessage(message: string): void } } }

const loaders = {
  files: () => import("../components/FileManagerDialog.vue"),
  agent: () => import("../components/AgentDialog.vue"),
  share: () => import("../components/SessionShareDialog.vue"),
  monitor: () => import("../components/HostMonitorDialog.vue"),
  git: () => import("../components/GitDialog.vue"),
  docker: () => import("../components/DockerDialog.vue"),
};
const context = ref<Context>();
const panel = shallowRef<Component>();
const open = ref(false), loading = ref(true), error = ref("");
const directory = ref(".");
function post(type: string, payload: Record<string, unknown> = {}) {
  window.VelinTools?.postMessage(JSON.stringify({ type, ...payload }));
}
const panelProps = computed(() => {
  const c = context.value;
  if (!c) return {};
  if (c.tool === "share") return { session: c.session, baseURL: c.serviceURL };
  if (c.tool === "files" || c.tool === "git") return { host: c.host, sessionId: c.session.id, initialPath: directory.value };
  if (c.tool === "docker") return { host: c.host, sessionId: c.session.id };
  return { host: c.host };
});
function changed(value: boolean) {
  open.value = value;
  if (!value) post("close");
}
function dockerTerminal(target: { id: string; name: string }) { post("dockerTerminal", target); }
function locked() { post("accessRequired", { code: "session_locked" }); }

onMounted(async () => {
  document.body.classList.add("mobile-tool-mode");
  window.addEventListener("velin:session-locked", locked);
  try {
    const response = await fetch("/native/context", { credentials: "same-origin", cache: "no-store" });
    if (!response.ok) throw new Error("请从手机客户端的终端操作菜单打开工具");
    const c = await response.json() as Context;
    if (!c.host?.id || !c.session?.id || !Object.prototype.hasOwnProperty.call(loaders, c.tool)) throw new Error("终端工具参数无效");
    context.value = c;
    directory.value = c.host.initialDirectory || ".";
    if (c.tool === "files" || c.tool === "git") {
      try { directory.value = (await api<{ path: string }>(`/api/sessions/${encodeURIComponent(c.session.id)}/directory`)).path; } catch {}
    }
    panel.value = (await loaders[c.tool]()).default;
    await nextTick();
    open.value = true;
  } catch (e) { error.value = e instanceof Error ? e.message : "工具加载失败"; }
  finally { loading.value = false; }
});
onBeforeUnmount(() => {
  document.body.classList.remove("mobile-tool-mode");
  window.removeEventListener("velin:session-locked", locked);
});
</script>

<template>
  <main class="mobile-tool-page">
    <p v-if="loading">正在打开工具…</p>
    <p v-else-if="error" role="alert">{{ error }}</p>
    <component v-if="panel" :is="panel" :model-value="open" v-bind="panelProps"
      @update:model-value="changed" @terminal="dockerTerminal" @open="post('openLink', { url: $event })" />
  </main>
</template>

<style>
.mobile-tool-page { min-height: 100dvh; padding: 16px; background: #101113; color: #dedee3; box-sizing: border-box; }
body.mobile-tool-mode { overflow: hidden; }
.mobile-tool-mode .el-overlay-dialog { padding: 0; overflow: hidden; }
.mobile-tool-mode .el-dialog { margin: 0 !important; width: 100% !important; max-width: 100% !important; height: 100dvh; max-height: 100dvh !important; display: flex; flex-direction: column; border-radius: 0; box-sizing: border-box; }
.mobile-tool-mode .el-dialog__header { flex: 0 0 auto; padding-right: 30px; }
.mobile-tool-mode .el-dialog__body { flex: 1 1 auto; min-height: 0; overflow: auto; }
.mobile-tool-mode .el-dialog__footer { flex: 0 0 auto; }
.mobile-tool-mode .el-message-box { width: calc(100vw - 24px); max-width: 420px; }
.mobile-tool-mode .el-table { width: 100%; }
.mobile-tool-mode .el-input, .mobile-tool-mode .el-select { max-width: 100%; }
body.mobile-tool-mode .agent-window { inset: 0 !important; width: 100% !important; height: 100dvh !important; max-width: 100%; max-height: 100dvh; min-height: 0; border-radius: 0; --aw: 100vw !important; --ah: 100dvh !important; }
.mobile-tool-mode .agent-window-header { cursor: default; }
.mobile-tool-mode .agent-resize-handle { display: none; }
</style>
