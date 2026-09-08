<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from "vue";
import {
  Bot,
  ChevronRight,
  CircleUserRound,
  History as HistoryIcon,
  Plus,
  Play,
  PlugZap,
  RefreshCw,
  SendHorizontal,
  ShieldAlert,
  SquareTerminal,
  Trash2,
  Unplug,
  X,
} from "@lucide/vue";
import DOMPurify from "dompurify";
import { marked } from "marked";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import type {
  AgentModel,
  AgentStatus,
  Host,
} from "../types";

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  suspended?: boolean;
}>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();

const status = ref<AgentStatus>();
const models = ref<AgentModel[]>([]);
const defaultContextWindow = ref(0);
const selectedModel = ref("");
const reasoningEffort = ref<"" | "low" | "medium" | "high">("");
const modelsLoading = ref(false);
const modelsError = ref("");
const modelServiceAvailable = ref(false);
const fallbackAgentModelIDs = [
  "gpt-5.4",
  "gpt-5.4-mini",
  "gpt-5.5",
  "gpt-5.6-luna",
  "gpt-5.6-sol",
  "gpt-5.6-terra",
];
const lastUsage = ref<{
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
}>();
const busy = ref<"connect" | "disconnect">();
const chatInput = ref("");
const chatting = ref(false);
const executingIDs = ref<Set<string>>(new Set());
let aiRequestQueue: Promise<void> = Promise.resolve();
let commandFollowupTimer: number | undefined;
type AgentChatMessage = {
  id: string;
  role: "user" | "assistant" | "result";
  content: string;
  success?: boolean;
  reason?: string;
};
type AgentCommandProposal = {
  id: string;
  command: string;
  reason: string;
  requiresApproval: boolean;
};
type AgentConversation = {
  id: string;
  title: string;
  updatedAt: string;
  messages: AgentChatMessage[];
  history: Array<{ role: "user" | "assistant"; content: string }>;
  proposals: AgentCommandProposal[];
};
const chatMessages = ref<AgentChatMessage[]>([]);
const chatHistory = ref<Array<{ role: "user" | "assistant"; content: string }>>([]);
const commandProposals = ref<AgentCommandProposal[]>([]);
const expandedResultIDs = ref<Set<string>>(new Set());
const chatLog = ref<HTMLElement>();
const approvalConfirmButton = ref<HTMLButtonElement>();
const conversations = ref<AgentConversation[]>([]);
const activeConversationID = ref("");
const historyOpen = ref(false);
const windowRect = reactive({ left: 24, top: 48, width: 1040, height: 820 });
type ResizeEdge = "n" | "ne" | "e" | "se" | "s" | "sw" | "w" | "nw";
let pointerCleanup: (() => void) | undefined;
let chatScrollFrame: number | undefined;
let followChatOutput = true;

const windowStyle = computed(() => ({
  left: `${windowRect.left}px`,
  top: `${windowRect.top}px`,
  width: `${windowRect.width}px`,
  height: `${windowRect.height}px`,
}));

const connected = computed(() => status.value?.state === "connected");
const aiReady = computed(
  () => status.value?.aiConfigured === true || modelServiceAvailable.value,
);
const statusLabel = computed(
  () =>
    ({
      disconnected: "未连接",
      connecting: "连接中",
      connected: "已连接",
      error: "异常",
    })[status.value?.state || "disconnected"],
);
const modelOptions = computed(() => {
  const options = [...models.value];
  if (selectedModel.value && !options.some((item) => item.id === selectedModel.value))
    options.unshift({ id: selectedModel.value });
  return options;
});
const modelSyncLabel = computed(() => {
  if (modelsLoading.value) return "同步模型中";
  if (modelsError.value) return "模型同步失败";
  return "";
});
const selectedModelInfo = computed(() =>
  modelOptions.value.find((item) => item.id === selectedModel.value),
);
const contextSummary = computed(() => {
  const contextWindow = selectedModelInfo.value?.contextWindow || defaultContextWindow.value;
  const reportedUsed = lastUsage.value?.promptTokens || 0;
  const estimatedUsed = Math.ceil(
    chatHistory.value.reduce((total, item) => total + item.content.length, 0) / 2.5,
  );
  const used = reportedUsed || estimatedUsed;
  const usagePrefix = reportedUsed ? "上下文" : "上下文约";
  if (contextWindow && used)
    return `${usagePrefix} ${formatTokenCount(used)} / ${formatTokenCount(contextWindow)}`;
  if (contextWindow) return `上下文上限 ${formatTokenCount(contextWindow)}`;
  if (used) return `已用上下文 ${formatTokenCount(used)} · 上限未提供`;
  return modelsLoading.value ? "正在同步上下文" : "上下文用量 0 · 上限未提供";
});
const approvalProposal = computed(() => commandProposals.value[0]);

watch(
  () => props.modelValue,
  async (open) => {
    stopPointerInteraction();
    clearCommandFollowupTimer();
    if (!open || !props.host) {
      window.removeEventListener("keydown", handleWindowKeydown);
      return;
    }
    resetWindowRect();
    window.addEventListener("keydown", handleWindowKeydown);
    chatInput.value = "";
    historyOpen.value = false;
    chatMessages.value = [];
    chatHistory.value = [];
    commandProposals.value = [];
    expandedResultIDs.value = new Set();
    models.value = [];
    defaultContextWindow.value = 0;
    modelsError.value = "";
    modelServiceAvailable.value = false;
    status.value = undefined;
    lastUsage.value = undefined;
    loadModelPreferences();
    loadConversations();
    await loadStatus();
    const loadedStatus = status.value as AgentStatus | undefined;
    if (!selectedModel.value) selectedModel.value = loadedStatus?.model || "";
    void loadAgentModels();
    if (!connected.value && (props.host.credentialID || props.host.hasPassword)) {
      await connect();
    }
  },
);

watch(
  () => props.suspended,
  async (suspended, previous) => {
    if (suspended || previous === undefined || !props.modelValue) return;
    const wasConnected =
      status.value?.state === "connected" || status.value?.state === "connecting";
    if (!wasConnected || (!props.host?.credentialID && !props.host?.hasPassword)) return;
    await loadStatus();
    if (status.value?.state !== "connected") await connect();
  },
);

watch(
  [chatMessages, commandProposals, chatting],
  () => scheduleChatScroll(),
  { deep: true, flush: "post" },
);

watch(historyOpen, () => scheduleChatScroll());

watch(
  approvalProposal,
  async (proposal) => {
    if (!proposal) return;
    await nextTick();
    approvalConfirmButton.value?.focus();
  },
  { flush: "post" },
);

watch([selectedModel, reasoningEffort], persistModelPreferences);

onBeforeUnmount(() => {
  stopPointerInteraction();
  clearCommandFollowupTimer();
  window.removeEventListener("keydown", handleWindowKeydown);
  if (chatScrollFrame !== undefined) cancelAnimationFrame(chatScrollFrame);
});

function resetWindowRect() {
  const width = Math.min(1040, Math.max(minWindowWidth(), window.innerWidth - 24));
  const height = Math.min(820, Math.max(minWindowHeight(), window.innerHeight - 32));
  windowRect.width = width;
  windowRect.height = height;
  windowRect.left = Math.max(12, Math.round((window.innerWidth - width) / 2));
  windowRect.top = Math.max(12, Math.round((window.innerHeight - height) / 2));
}

function minWindowWidth() {
  return Math.min(320, Math.max(240, window.innerWidth - 24));
}

function minWindowHeight() {
  return Math.min(420, Math.max(240, window.innerHeight - 24));
}

function handleWindowKeydown(event: KeyboardEvent) {
  if (approvalProposal.value && event.key === "Enter") {
    event.preventDefault();
    event.stopPropagation();
    confirmApproval();
    return;
  }
  if (approvalProposal.value && event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    cancelProposal(approvalProposal.value);
    return;
  }
  if (event.key === "Escape") emit("update:modelValue", false);
}

function clampWindowRect() {
  const minWidth = minWindowWidth();
  const minHeight = minWindowHeight();
  const maxWidth = Math.max(minWidth, window.innerWidth - 24);
  const maxHeight = Math.max(minHeight, window.innerHeight - 24);
  windowRect.width = Math.min(Math.max(minWidth, windowRect.width), maxWidth);
  windowRect.height = Math.min(Math.max(minHeight, windowRect.height), maxHeight);
  windowRect.left = Math.min(
    Math.max(12, windowRect.left),
    Math.max(12, window.innerWidth - windowRect.width - 12),
  );
  windowRect.top = Math.min(
    Math.max(12, windowRect.top),
    Math.max(12, window.innerHeight - windowRect.height - 12),
  );
}

function startDrag(event: PointerEvent) {
  if (event.button !== 0) return;
  startPointerInteraction(event, undefined);
}

function startResize(event: PointerEvent, edge: ResizeEdge) {
  if (event.button !== 0) return;
  event.preventDefault();
  startPointerInteraction(event, edge);
}

function startPointerInteraction(event: PointerEvent, edge?: ResizeEdge) {
  stopPointerInteraction();
  const origin = {
    x: event.clientX,
    y: event.clientY,
    ...windowRect,
  };
  document.body.classList.add("is-agent-interacting");
  const move = (next: PointerEvent) => {
    const dx = next.clientX - origin.x;
    const dy = next.clientY - origin.y;
    if (!edge) {
      windowRect.left = origin.left + dx;
      windowRect.top = origin.top + dy;
      clampWindowRect();
      return;
    }
    const minWidth = minWindowWidth();
    const minHeight = minWindowHeight();
    if (edge.includes("e"))
      windowRect.width = Math.max(minWidth, origin.width + dx);
    if (edge.includes("s"))
      windowRect.height = Math.max(minHeight, origin.height + dy);
    if (edge.includes("w")) {
      windowRect.width = Math.max(minWidth, origin.width - dx);
      windowRect.left = origin.left + (origin.width - windowRect.width);
    }
    if (edge.includes("n")) {
      windowRect.height = Math.max(minHeight, origin.height - dy);
      windowRect.top = origin.top + (origin.height - windowRect.height);
    }
    clampWindowRect();
  };
  const stop = () => stopPointerInteraction();
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", stop, { once: true });
  pointerCleanup = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", stop);
    document.body.classList.remove("is-agent-interacting");
  };
}

function stopPointerInteraction() {
  pointerCleanup?.();
  pointerCleanup = undefined;
  document.body.classList.remove("is-agent-interacting");
}

async function loadStatus() {
  if (!props.host) return;
  try {
    status.value = await api<AgentStatus>(
      `/api/hosts/${props.host.id}/agent`,
    );
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "状态获取失败");
  }
}

function modelPreferencesStorageKey() {
  return props.host ? `velin-agent-preferences:${props.host.id}` : "";
}

function loadModelPreferences() {
  selectedModel.value = "";
  reasoningEffort.value = "";
  const key = modelPreferencesStorageKey();
  if (!key) return;
  try {
    const parsed = JSON.parse(localStorage.getItem(key) || "{}");
    if (parsed && typeof parsed.model === "string") selectedModel.value = parsed.model;
    if (["", "low", "medium", "high"].includes(parsed?.reasoningEffort))
      reasoningEffort.value = parsed.reasoningEffort;
  } catch {}
}

function persistModelPreferences() {
  const key = modelPreferencesStorageKey();
  if (!key) return;
  try {
    localStorage.setItem(
      key,
      JSON.stringify({
        model: selectedModel.value,
        reasoningEffort: reasoningEffort.value,
      }),
    );
  } catch {}
}

async function loadAgentModels() {
  modelsLoading.value = true;
  modelsError.value = "";
  try {
    const result = await api<{
      defaultModel: string;
      models: AgentModel[];
      defaultContextWindow?: number;
      warning?: string;
    }>(
      "/api/agent/models",
    );
    models.value = Array.isArray(result.models) ? result.models : [];
    defaultContextWindow.value = result.defaultContextWindow || 0;
    if (result.warning) modelsError.value = result.warning;
    const defaultModel = result.defaultModel || status.value?.model || "";
    if (!selectedModel.value) selectedModel.value = defaultModel;
    if (!models.value.length)
      models.value = fallbackModels(defaultModel);
    modelServiceAvailable.value = true;
    if (status.value && !status.value.aiConfigured)
      status.value = { ...status.value, aiConfigured: true };
  } catch (error) {
    modelsError.value = error instanceof Error ? error.message : "模型同步失败";
    const fallbackModel = selectedModel.value || status.value?.model || "";
    if (!models.value.length) models.value = fallbackModels(fallbackModel);
  } finally {
    modelsLoading.value = false;
  }
}

function fallbackModels(defaultModel: string): AgentModel[] {
  const ids = defaultModel.startsWith("gpt-5")
    ? fallbackAgentModelIDs
    : defaultModel
      ? [defaultModel]
      : [];
  return ids.map((id) => ({ id }));
}

async function connect() {
  if (!props.host) return;
  busy.value = "connect";
  try {
    status.value = await api<AgentStatus>(
      `/api/hosts/${props.host.id}/agent/connect`,
      { method: "POST" },
    );
    ElMessage.success("Agent 已连接");
  } catch (error) {
    await loadStatus();
    ElMessage.error(error instanceof Error ? error.message : "连接失败");
  } finally {
    busy.value = undefined;
  }
}

async function disconnect() {
  if (!props.host) return;
  busy.value = "disconnect";
  try {
    status.value = await api<AgentStatus>(
      `/api/hosts/${props.host.id}/agent`,
      { method: "DELETE" },
    );
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "断开失败");
  } finally {
    busy.value = undefined;
  }
}

async function sendChat() {
  const content = chatInput.value.trim();
  if (!content || !connected.value || !aiReady.value || chatting.value)
    return;
  chatInput.value = "";
  chatMessages.value.push({ id: messageID(), role: "user", content });
  chatHistory.value.push({ role: "user", content });
  scheduleChatScroll(true);
  persistCurrentConversation(content);
  await requestAI();
}

function requestAI() {
  const conversationID = activeConversationID.value;
  const next = aiRequestQueue.then(() => requestAIOnce(conversationID));
  aiRequestQueue = next.catch(() => undefined);
  return next;
}

async function requestAIOnce(conversationID: string) {
  if (!props.host || conversationID !== activeConversationID.value) return;
  chatting.value = true;
  try {
    const response = await api<{
      message: string;
      commands: Array<{
        id: string;
        command: string;
        reason: string;
        requiresApproval?: boolean;
      }>;
      model: string;
      promptTokens?: number;
      completionTokens?: number;
      totalTokens?: number;
    }>(`/api/hosts/${props.host.id}/agent/chat`, {
      method: "POST",
      body: json({
        messages: chatHistory.value.slice(-40),
        model: selectedModel.value || undefined,
        reasoningEffort: reasoningEffort.value || undefined,
        backend: "native",
        conversationID,
      }),
    });
    if (conversationID !== activeConversationID.value) return;
    lastUsage.value = {
      promptTokens: response.promptTokens || 0,
      completionTokens: response.completionTokens || 0,
      totalTokens: response.totalTokens || 0,
    };
    const assistantContent =
      response.message ||
      (response.commands.length
        ? `建议执行：${response.commands.map((item) => item.command).join("；")}`
        : "");
    if (response.message)
      chatMessages.value.push({
        id: messageID(),
        role: "assistant",
        content: response.message,
      });
    if (assistantContent)
      chatHistory.value.push({ role: "assistant", content: assistantContent });
    const proposals = response.commands.map((item) => ({
      ...item,
      requiresApproval: item.requiresApproval !== false,
    }));
    commandProposals.value.push(
      ...proposals.filter((item) => item.requiresApproval),
    );
    persistCurrentConversation();
    await Promise.all(
      proposals
        .filter((item) => !item.requiresApproval)
        .map((item) => executeProposal(item)),
    );
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "Agent 请求失败");
  } finally {
    chatting.value = false;
  }
}

async function executeProposal(proposal: AgentCommandProposal) {
  if (!props.host || executingIDs.value.has(proposal.id)) return;
  executingIDs.value = new Set(executingIDs.value).add(proposal.id);
  let completed = false;
  try {
    const result = await api<{ output: string; success: boolean; error?: string }>(
      `/api/hosts/${props.host.id}/agent/command`,
      { method: "POST", body: json({ command: proposal.command }) },
    );
    const display = result.output.trim() || result.error || "命令已完成，无输出";
    chatMessages.value.push({
      id: messageID(),
      role: "result",
      content: `$ ${proposal.command}\n${display}`,
      success: result.success,
      reason: proposal.reason,
    });
    commandProposals.value = commandProposals.value.filter(
      (item) => item.id !== proposal.id,
    );
    const modelOutput = display.slice(0, 24000);
    chatHistory.value.push({
      role: "user",
      content: `用户已确认并执行 SSH 命令：\n${proposal.command}\n\n执行结果（success=${result.success}）：\n${modelOutput}\n\n请根据结果继续处理用户任务。`,
    });
    completed = true;
    persistCurrentConversation();
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "命令执行失败");
  } finally {
    const next = new Set(executingIDs.value);
    next.delete(proposal.id);
    executingIDs.value = next;
    if (completed && executingIDs.value.size === 0 && commandProposals.value.length === 0)
      scheduleCommandFollowup();
  }
}

function scheduleCommandFollowup() {
  if (
    commandFollowupTimer !== undefined ||
    executingIDs.value.size > 0 ||
    commandProposals.value.length > 0
  ) return;
  commandFollowupTimer = window.setTimeout(() => {
    commandFollowupTimer = undefined;
    if (executingIDs.value.size === 0 && commandProposals.value.length === 0)
      void requestAI();
  }, 250);
}

function clearCommandFollowupTimer() {
  if (commandFollowupTimer === undefined) return;
  window.clearTimeout(commandFollowupTimer);
  commandFollowupTimer = undefined;
}

function isExecuting(proposalID: string) {
  return executingIDs.value.has(proposalID);
}

function confirmApproval() {
  const proposal = approvalProposal.value;
  if (!proposal || !connected.value) return;
  commandProposals.value = commandProposals.value.filter(
    (item) => item.id !== proposal.id,
  );
  persistCurrentConversation();
  void executeProposal(proposal);
}

function cancelProposal(proposal: AgentCommandProposal) {
  commandProposals.value = commandProposals.value.filter(
    (item) => item.id !== proposal.id,
  );
  chatMessages.value.push({
    id: messageID(),
    role: "result",
    content: `已取消执行：\n\`\`\`sh\n${proposal.command}\n\`\`\``,
    success: false,
    reason: proposal.reason,
  });
  persistCurrentConversation();
}

function renderMarkdown(content: string) {
  const html = marked.parse(content, { gfm: true, breaks: true, async: false });
  return DOMPurify.sanitize(String(html));
}

function conversationStorageKey() {
  return props.host ? `velin-agent-conversations:${props.host.id}` : "";
}

function loadConversations() {
  conversations.value = [];
  const key = conversationStorageKey();
  if (key) {
    try {
      const parsed = JSON.parse(localStorage.getItem(key) || "[]");
      if (Array.isArray(parsed)) {
        conversations.value = parsed
          .filter((item) => item && typeof item.id === "string")
          .slice(0, 30)
          .map((item) => ({
            id: item.id,
            title: String(item.title || "新会话").slice(0, 80),
            updatedAt: String(item.updatedAt || new Date().toISOString()),
            messages: Array.isArray(item.messages) ? item.messages.slice(-200) : [],
            history: Array.isArray(item.history) ? item.history.slice(-40) : [],
            proposals: Array.isArray(item.proposals) ? item.proposals.slice(-20) : [],
          }));
      }
    } catch {}
  }
  const latest = conversations.value[0];
  if (latest) selectConversation(latest.id, false);
  else startConversation(false);
}

function startConversation(closeHistory = true) {
  clearCommandFollowupTimer();
  persistCurrentConversation();
  const conversation: AgentConversation = {
    id: messageID(),
    title: "新会话",
    updatedAt: new Date().toISOString(),
    messages: [],
    history: [],
    proposals: [],
  };
  conversations.value.unshift(conversation);
  activeConversationID.value = conversation.id;
  chatMessages.value = [];
  chatHistory.value = [];
  commandProposals.value = [];
  expandedResultIDs.value = new Set();
  lastUsage.value = undefined;
  if (closeHistory) historyOpen.value = false;
  persistConversations();
  scheduleChatScroll(true);
}

function selectConversation(id: string, closeHistory = true) {
  const conversation = conversations.value.find((item) => item.id === id);
  if (!conversation) return;
  clearCommandFollowupTimer();
  if (activeConversationID.value !== id) persistCurrentConversation();
  activeConversationID.value = id;
  chatMessages.value = conversation.messages.map((item) => ({ ...item }));
  chatHistory.value = conversation.history.map((item) => ({ ...item }));
  commandProposals.value = conversation.proposals.map((item) => ({ ...item }));
  expandedResultIDs.value = new Set();
  lastUsage.value = undefined;
  if (closeHistory) historyOpen.value = false;
  scheduleChatScroll(true);
}

async function deleteConversation(id: string) {
  const conversation = conversations.value.find((item) => item.id === id);
  if (!conversation) return;
  try {
    await ElMessageBox.confirm(
      `确定删除会话“${conversation.title}”？删除后无法恢复。`,
      "删除历史会话",
      {
        type: "warning",
        confirmButtonText: "删除",
        cancelButtonText: "取消",
        modalClass: "agent-delete-confirm-overlay",
      },
    );
  } catch {
    return;
  }
  const wasActive = activeConversationID.value === id;
  conversations.value = conversations.value.filter((item) => item.id !== id);
  if (wasActive) {
    const next = conversations.value[0];
    if (next) selectConversation(next.id, false);
    else startConversation(false);
  }
  persistConversations();
}

function persistCurrentConversation(firstMessage = "") {
  const conversation = conversations.value.find(
    (item) => item.id === activeConversationID.value,
  );
  if (!conversation) return;
  conversation.messages = chatMessages.value.slice(-200).map((item) => ({ ...item }));
  conversation.history = chatHistory.value.slice(-40).map((item) => ({ ...item }));
  conversation.proposals = commandProposals.value.slice(-20).map((item) => ({ ...item }));
  if (firstMessage && conversation.title === "新会话")
    conversation.title = firstMessage.replace(/\s+/g, " ").slice(0, 80);
  conversation.updatedAt = new Date().toISOString();
  conversations.value.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  persistConversations();
}

function persistConversations() {
  const key = conversationStorageKey();
  if (!key) return;
  try {
    localStorage.setItem(key, JSON.stringify(conversations.value.slice(0, 30)));
  } catch {}
}

function handleChatKeydown(event: KeyboardEvent) {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    void sendChat();
  }
}

function handleChatScroll() {
  const element = chatLog.value;
  if (!element) return;
  followChatOutput =
    element.scrollHeight - element.scrollTop - element.clientHeight < 72;
}

function scheduleChatScroll(force = false) {
  if (force) followChatOutput = true;
  if (!followChatOutput) return;
  if (chatScrollFrame !== undefined) cancelAnimationFrame(chatScrollFrame);
  nextTick(() => {
    chatScrollFrame = requestAnimationFrame(() => {
      chatScrollFrame = undefined;
      const element = chatLog.value;
      if (element && followChatOutput)
        element.scrollTop = Math.max(0, element.scrollHeight - element.clientHeight);
    });
  });
}

function messageID() {
  return globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`;
}

function formatTokenCount(value = 0) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1024) return `${Math.round(value / 1024)}K`;
  return String(value);
}

function isResultExpanded(messageID: string) {
  return expandedResultIDs.value.has(messageID);
}

function toggleResult(messageID: string) {
  const next = new Set(expandedResultIDs.value);
  if (next.has(messageID)) next.delete(messageID);
  else next.add(messageID);
  expandedResultIDs.value = next;
}

function resultCommand(content: string) {
  const firstLine = content.split(/\r?\n/, 1)[0]?.trim() || "SSH 命令结果";
  return firstLine.startsWith("$ ") ? firstLine.slice(2) : firstLine;
}

</script>

<template>
  <Teleport to="body">
    <div v-if="modelValue" v-show="!suspended" class="agent-floating-layer">
      <section
        class="agent-window"
        role="dialog"
        aria-modal="false"
        :aria-label="`后台 Agent${host ? ` · ${host.name}` : ''}`"
        :style="windowStyle"
        @pointerdown.stop
      >
        <header class="agent-window-header" @pointerdown="startDrag">
          <span class="agent-window-title">
            <Bot :size="16" />后台 Agent<span v-if="host"> · {{ host.name }}</span>
            <i
              class="agent-header-status"
              :class="status?.state || 'disconnected'"
              :title="statusLabel"
              :aria-label="statusLabel"
            ></i>
          </span>
          <div class="agent-window-header-actions">
            <el-button
              text
              :icon="HistoryIcon"
              :type="historyOpen ? 'primary' : undefined"
              title="历史会话"
              aria-label="历史会话"
              @pointerdown.stop
              @click="historyOpen = !historyOpen"
            />
            <el-button
              text
              :icon="Plus"
              title="新建会话"
              aria-label="新建会话"
              @pointerdown.stop
              @click="startConversation()"
            />
            <el-button
              v-if="!connected"
              text
              :icon="PlugZap"
              :loading="busy === 'connect'"
              :disabled="Boolean(busy) || (!host?.credentialID && !host?.hasPassword)"
              title="连接 Agent"
              aria-label="连接 Agent"
              @pointerdown.stop
              @click="connect"
            />
            <el-button
              v-else
              text
              :icon="Unplug"
              :loading="busy === 'disconnect'"
              :disabled="Boolean(busy)"
              title="断开 Agent"
              aria-label="断开 Agent"
              @pointerdown.stop
              @click="disconnect"
            />
            <button
              class="agent-window-close"
              type="button"
              title="关闭 Agent"
              aria-label="关闭 Agent"
              @pointerdown.stop
              @click="emit('update:modelValue', false)"
            >
              <X :size="16" />
            </button>
          </div>
        </header>
        <div
          ref="chatLog"
          class="agent-window-body"
          @scroll="handleChatScroll"
        >
    <el-alert
      v-if="status?.lastError"
      class="agent-error"
      type="error"
      :closable="false"
      :title="status.lastError"
    />

          <el-alert
            v-if="status && !aiReady"
            type="warning"
            :closable="false"
            title="AI 模型服务尚未配置"
          />
          <div class="agent-chat-log">
            <div v-if="!chatMessages.length && !chatting" class="agent-empty agent-chat-empty">
              <Bot :size="30" />
              <span>{{ connected ? "开始一个任务" : "请先连接主机" }}</span>
            </div>
            <article
              v-for="message in chatMessages"
              :key="message.id"
              class="agent-chat-message"
              :class="[message.role, { failed: message.success === false }]"
            >
              <span class="agent-message-avatar" aria-hidden="true">
                <CircleUserRound v-if="message.role === 'user'" :size="17" />
                <SquareTerminal v-else-if="message.role === 'result'" :size="17" />
                <Bot v-else :size="17" />
              </span>
              <div class="agent-message-body">
                <span class="agent-message-author">{{ message.role === "user" ? "你" : message.role === "assistant" ? "Agent" : "SSH" }}</span>
                <template v-if="message.role === 'result'">
                  <button
                    type="button"
                    class="agent-result-summary"
                    :aria-expanded="isResultExpanded(message.id)"
                    @click="toggleResult(message.id)"
                  >
                    <ChevronRight
                      :size="15"
                      :class="{ expanded: isResultExpanded(message.id) }"
                    />
                    <span class="agent-result-summary-copy">
                      <span class="agent-result-purpose">{{ message.reason || "命令说明未提供" }}</span>
                      <span class="agent-result-command">$ {{ resultCommand(message.content) }}</span>
                    </span>
                    <small>{{ message.success === false ? "失败" : "已完成" }}</small>
                  </button>
                  <pre
                    v-if="isResultExpanded(message.id)"
                    class="agent-result-output"
                  >{{ message.content }}</pre>
                </template>
                <div v-else class="agent-markdown" v-html="renderMarkdown(message.content)"></div>
              </div>
            </article>
            <div v-if="chatting" class="agent-thinking"><i></i><i></i><i></i></div>
          </div>
          <div class="agent-composer-dock">
            <div v-if="commandProposals.length" class="agent-command-pending">
              <ShieldAlert :size="15" />
              <span>等待确认 {{ commandProposals.length }} 条命令</span>
            </div>
            <div class="agent-chat-input">
              <el-input
                v-model="chatInput"
                type="textarea"
                :autosize="{ minRows: 2, maxRows: 5 }"
                resize="none"
                placeholder="输入要在主机上完成的任务"
                :disabled="!connected || !aiReady || chatting"
                @keydown="handleChatKeydown"
              />
              <el-button
                type="primary"
                :icon="SendHorizontal"
                :loading="chatting"
                :disabled="!chatInput.trim() || !connected || !aiReady"
                title="发送"
                aria-label="发送"
                @click="sendChat"
              />
            <div class="agent-ai-controls">
              <span class="agent-context-size" :title="modelsError || contextSummary">
                {{ contextSummary }}
              </span>
              <div class="agent-ai-control agent-model-control">
                <el-select
                  v-model="selectedModel"
                  size="small"
                  filterable
                  popper-class="agent-select-popper"
                  :loading="modelsLoading"
                  :disabled="!modelOptions.length || chatting"
                  placeholder="选择模型"
                  no-data-text="暂无可用模型"
                  aria-label="选择模型"
                >
                  <el-option
                    v-for="model in modelOptions"
                    :key="model.id"
                    :label="model.id"
                    :value="model.id"
                  >
                    <span class="agent-model-option">
                      <span>{{ model.id }}</span>
                      <small v-if="model.contextWindow">{{ formatTokenCount(model.contextWindow) }}</small>
                    </span>
                  </el-option>
                </el-select>
              </div>
              <div class="agent-ai-control agent-reasoning-control">
                <el-select
                  v-model="reasoningEffort"
                  size="small"
                  popper-class="agent-select-popper"
                  :disabled="chatting"
                  placeholder="默认"
                  aria-label="选择推理强度"
                >
                  <el-option label="默认" value="" />
                  <el-option label="低" value="low" />
                  <el-option label="中" value="medium" />
                  <el-option label="高" value="high" />
                </el-select>
              </div>
              <el-button
                class="agent-model-refresh"
                text
                :icon="RefreshCw"
                :loading="modelsLoading"
                :disabled="chatting"
                title="同步可用模型"
                aria-label="同步可用模型"
                @click="loadAgentModels"
              />
              <small
                v-if="modelSyncLabel"
                class="agent-model-sync-status"
                :title="modelsError"
              >{{ modelSyncLabel }}</small>
            </div>
            </div>
          </div>
        </div>
        <div
          v-if="approvalProposal"
          class="agent-command-approval-layer"
          role="presentation"
          @click.self="cancelProposal(approvalProposal)"
        >
          <section
            class="agent-command-approval"
            role="dialog"
            aria-modal="true"
            aria-labelledby="agent-command-approval-title"
          >
            <div class="agent-command-approval-header">
              <span class="agent-command-approval-icon"><ShieldAlert :size="18" /></span>
              <div>
                <strong id="agent-command-approval-title">需要确认执行</strong>
                <small>确认后立即后台执行，不等待命令结果</small>
              </div>
              <span class="agent-command-approval-count">1 / {{ commandProposals.length }}</span>
            </div>
            <div class="agent-command-approval-reason">
              {{ approvalProposal.reason || "请检查命令内容后决定是否执行" }}
            </div>
            <pre class="agent-command-approval-command">{{ approvalProposal.command }}</pre>
            <div class="agent-command-approval-actions">
              <button
                type="button"
                class="agent-command-approval-cancel"
                @click="cancelProposal(approvalProposal)"
              >取消</button>
              <button
                ref="approvalConfirmButton"
                type="button"
                class="agent-command-approval-confirm"
                :disabled="!connected"
                @click="confirmApproval"
              >
                <Play :size="14" />确认执行
              </button>
            </div>
          </section>
        </div>
        <div
          v-if="historyOpen"
          class="agent-history-modal"
          @click="historyOpen = false"
        >
          <section
            class="agent-history-modal-card"
            role="dialog"
            aria-modal="true"
            aria-label="历史会话"
            @click.stop
          >
            <header class="agent-history-modal-header">
              <div>
                <strong>历史会话</strong>
                <small>{{ host?.name || "当前主机" }}</small>
              </div>
              <button
                type="button"
                class="agent-window-close"
                title="关闭历史会话"
                aria-label="关闭历史会话"
                @click="historyOpen = false"
              >
                <X :size="16" />
              </button>
            </header>
            <div class="agent-history-list">
              <div
                v-for="conversation in conversations"
                :key="conversation.id"
                class="agent-history-entry"
              >
                <button
                  type="button"
                  class="agent-history-item"
                  :class="{ active: conversation.id === activeConversationID }"
                  @click="selectConversation(conversation.id)"
                >
                  <strong>{{ conversation.title }}</strong>
                  <small>{{ new Date(conversation.updatedAt).toLocaleString() }}</small>
                </button>
                <button
                  type="button"
                  class="agent-history-delete"
                  title="删除会话"
                  aria-label="删除会话"
                  @click="deleteConversation(conversation.id)"
                >
                  <Trash2 :size="15" />
                </button>
              </div>
              <div v-if="!conversations.length" class="agent-history-empty">暂无历史会话</div>
            </div>
          </section>
        </div>
        <span class="agent-resize-handle agent-resize-n" @pointerdown.stop="startResize($event, 'n')"></span>
        <span class="agent-resize-handle agent-resize-ne" @pointerdown.stop="startResize($event, 'ne')"></span>
        <span class="agent-resize-handle agent-resize-e" @pointerdown.stop="startResize($event, 'e')"></span>
        <span class="agent-resize-handle agent-resize-se" @pointerdown.stop="startResize($event, 'se')"></span>
        <span class="agent-resize-handle agent-resize-s" @pointerdown.stop="startResize($event, 's')"></span>
        <span class="agent-resize-handle agent-resize-sw" @pointerdown.stop="startResize($event, 'sw')"></span>
        <span class="agent-resize-handle agent-resize-w" @pointerdown.stop="startResize($event, 'w')"></span>
        <span class="agent-resize-handle agent-resize-nw" @pointerdown.stop="startResize($event, 'nw')"></span>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
:global(.agent-delete-confirm-overlay) {
  z-index: 6000 !important;
}

:global(.agent-select-popper) {
  z-index: 5000 !important;
}

.agent-floating-layer {
  position: fixed;
  inset: 0;
  z-index: 3200;
  pointer-events: none;
}
.agent-window {
  position: fixed;
  display: flex;
  flex-direction: column;
  min-width: min(320px, calc(100vw - 24px));
  min-height: min(420px, calc(100vh - 24px));
  max-width: calc(100vw - 24px);
  max-height: calc(100vh - 24px);
  overflow: hidden;
  pointer-events: auto;
  border: 1px solid color-mix(in srgb, var(--line-strong, #4b5568) 88%, transparent);
  border-radius: 10px;
  background: var(--surface, #181b22);
  box-shadow: 0 24px 80px #05070bc7, 0 0 0 1px #ffffff05;
}
.agent-window-header {
  height: 44px;
  flex: 0 0 44px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 10px 0 15px;
  border-bottom: 1px solid var(--line, #303641);
  background: var(--sidebar-header-bg, #1b1f27);
  color: var(--text, #e3e7ee);
  cursor: move;
  user-select: none;
}
.agent-window-title {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  gap: 8px;
  overflow: hidden;
  color: var(--text-soft, #bbc3d1);
  font-size: 13px;
  font-weight: 620;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-window-title svg {
  flex: 0 0 auto;
  color: var(--accent-strong, #9bb7ff);
}
.agent-header-status {
  width: 7px;
  height: 7px;
  flex: 0 0 7px;
  margin-left: 2px;
  border-radius: 50%;
  background: #8a9490;
  box-shadow: 0 0 0 2px #8a949022;
}
.agent-header-status.connected { background: #5b8cff; box-shadow: 0 0 0 3px #5b8cff22; }
.agent-header-status.connecting { background: #d6a84d; box-shadow: 0 0 0 3px #d6a84d22; }
.agent-header-status.error { background: #df6a6a; box-shadow: 0 0 0 3px #df6a6a22; }
.agent-window-header-actions {
  display: flex;
  align-items: center;
  gap: 3px;
  flex: 0 0 auto;
}
.agent-window-header-actions :deep(.el-button) {
  width: 28px;
  height: 28px;
  min-height: 28px;
  margin: 0;
  padding: 0;
  color: var(--muted, #86938d);
}
.agent-window-header-actions :deep(.el-button:hover) {
  color: var(--accent-strong, #9bb7ff);
  background: var(--accent-surface, #5b8cff14);
}
.agent-window-close {
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
  display: grid;
  place-items: center;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--muted, #86938d);
  cursor: pointer;
  font-size: 22px;
  line-height: 1;
}
.agent-window-close:hover {
  background: #3b2928;
  color: #f29a93;
}
.agent-window-body {
  display: flex;
  flex-direction: column;
  min-height: 0;
  flex: 1 1 auto;
  position: relative;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
  padding: 16px 18px 18px;
}
.agent-resize-handle {
  position: absolute;
  z-index: 3;
  display: block;
}
.agent-resize-n,
.agent-resize-s {
  left: 10px;
  right: 10px;
  height: 7px;
}
.agent-resize-n { top: -3px; cursor: ns-resize; }
.agent-resize-s { bottom: -3px; cursor: ns-resize; }
.agent-resize-e,
.agent-resize-w {
  top: 10px;
  bottom: 10px;
  width: 7px;
}
.agent-resize-e { right: -3px; cursor: ew-resize; }
.agent-resize-w { left: -3px; cursor: ew-resize; }
.agent-resize-ne,
.agent-resize-se,
.agent-resize-sw,
.agent-resize-nw {
  width: 12px;
  height: 12px;
}
.agent-resize-ne { top: -4px; right: -4px; cursor: nesw-resize; }
.agent-resize-se { right: -4px; bottom: -4px; cursor: nwse-resize; }
.agent-resize-sw { bottom: -4px; left: -4px; cursor: nesw-resize; }
.agent-resize-nw { top: -4px; left: -4px; cursor: nwse-resize; }
.agent-error { flex: 0 0 auto; margin-top: 12px; }
.agent-ai-controls {
  display: flex;
  align-items: center;
  gap: 6px;
  min-height: 30px;
  flex: 0 0 auto;
  margin: 0;
  padding: 0;
  background: transparent;
}
.agent-ai-control {
  display: inline-flex;
  align-items: center;
  min-width: 0;
}
.agent-model-control { flex: 0 1 auto; }
.agent-reasoning-control { flex: 0 0 auto; }
.agent-ai-controls :deep(.el-select) { min-width: 0; }
.agent-model-control :deep(.el-select) { width: min(230px, 30vw); }
.agent-reasoning-control :deep(.el-select) { width: 72px; }
.agent-ai-controls :deep(.el-select__wrapper) {
  min-height: 28px;
  height: 28px;
  padding: 0 7px;
  border-radius: 4px !important;
  border-color: transparent !important;
  background: color-mix(in srgb, var(--surface-3, #2b303a) 76%, #151619) !important;
  box-shadow: none !important;
  font-size: 11px;
}
.agent-ai-controls :deep(.el-select__wrapper:hover),
.agent-ai-controls :deep(.el-select__wrapper.is-focused) {
  border-color: color-mix(in srgb, var(--line-strong, #4b5568) 76%, transparent) !important;
  background: var(--surface-3, #2b303a) !important;
  box-shadow: none !important;
}
.agent-model-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
}
.agent-model-option span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-model-option small {
  flex: 0 0 auto;
  color: var(--text-muted);
  font-size: 10px;
}
.agent-model-refresh {
  width: 30px;
  height: 30px;
  flex: 0 0 30px;
  margin: 0;
  padding: 0;
}
.agent-model-sync-status {
  flex: 0 0 auto;
  max-width: 72px;
  overflow: hidden;
  color: var(--warning, #dfb864);
  font-size: 10px;
  line-height: 28px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-context-size {
  flex: 0 1 auto;
  max-width: 210px;
  margin-right: auto;
  overflow: hidden;
  color: var(--text-soft);
  font-size: 10px;
  line-height: 28px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-history-modal {
  position: absolute;
  inset: 44px 0 0;
  z-index: 10;
  display: grid;
  place-items: center;
  padding: 20px;
  background: #05070bb8;
}
.agent-history-modal-card {
  display: flex;
  flex-direction: column;
  width: min(520px, 100%);
  max-height: min(70vh, 560px);
  overflow: hidden;
  border: 1px solid var(--line-strong);
  border-radius: 10px;
  background: var(--surface);
  box-shadow: 0 24px 70px #05070bc7;
}
.agent-history-modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-height: 52px;
  padding: 6px 10px 6px 16px;
  border-bottom: 1px solid var(--line);
  color: var(--text);
}
.agent-history-modal-header > div {
  display: grid;
  gap: 2px;
}
.agent-history-modal-header strong {
  font-size: 14px;
}
.agent-history-modal-header small {
  color: var(--text-muted);
  font-size: 10px;
}
.agent-history-list {
  display: grid;
  gap: 4px;
  min-height: 0;
  padding: 10px;
  overflow: auto;
}
.agent-history-entry {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 34px;
  gap: 4px;
  min-width: 0;
}
.agent-history-item {
  display: grid;
  gap: 3px;
  min-width: 0;
  padding: 9px 10px;
  border: 1px solid transparent;
  border-radius: 5px;
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
  text-align: left;
}
.agent-history-item:hover,
.agent-history-item.active {
  border-color: var(--accent-border);
  background: var(--accent-surface);
}
.agent-history-item strong {
  overflow: hidden;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-history-item small,
.agent-history-empty { color: var(--text-muted); font-size: 11px; }
.agent-history-empty { padding: 12px; text-align: center; }
.agent-history-delete {
  display: grid;
  place-items: center;
  min-width: 0;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 5px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}
.agent-history-delete:hover,
.agent-history-delete:focus-visible {
  border-color: #df6a6a66;
  background: #3b2928;
  color: #f29a93;
}
.agent-window-body > .el-alert {
  box-sizing: border-box;
  flex: 0 0 auto;
  margin: 10px 10px 0;
  width: calc(100% - 20px);
}
.agent-chat-log {
  display: flex;
  flex-direction: column;
  gap: 20px;
  min-height: 0;
  flex: 0 0 auto;
  padding: 20px 16px 28px;
  overflow: visible;
}
.agent-chat-message {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  width: 100%;
  align-self: flex-start;
}
.agent-chat-message.user { flex-direction: row-reverse; }
.agent-message-avatar {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  border: 1px solid var(--border-subtle);
  border-radius: 50%;
  background: var(--surface, #181b22);
  color: var(--text-soft);
}
.agent-chat-message.user .agent-message-avatar {
  border-color: color-mix(in srgb, var(--el-color-primary) 38%, var(--border-subtle));
  color: var(--el-color-primary);
}
.agent-chat-message.result .agent-message-avatar { color: #d6a84d; }
.agent-message-body {
  display: grid;
  gap: 6px;
  min-width: 0;
  max-width: min(78%, 680px);
}
.agent-chat-message.user .agent-message-body { justify-items: end; }
.agent-chat-message.result .agent-message-body {
  width: min(78%, 680px);
  max-width: min(78%, 680px);
}
.agent-result-summary {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  width: 100%;
  min-height: 48px;
  padding: 7px 10px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: var(--surface-soft, #22262f);
  color: var(--text-soft);
  cursor: pointer;
  font: inherit;
  text-align: left;
}
.agent-result-summary:hover,
.agent-result-summary:focus-visible {
  border-color: var(--accent-border);
  background: var(--accent-surface);
}
.agent-result-summary svg {
  flex: 0 0 auto;
  margin-top: 2px;
  color: var(--accent-strong);
  transition: transform 0.16s ease;
}
.agent-result-summary svg.expanded {
  transform: rotate(90deg);
}
.agent-result-summary-copy {
  display: grid;
  min-width: 0;
  flex: 1 1 auto;
  gap: 2px;
}
.agent-result-purpose {
  min-width: 0;
  overflow: hidden;
  color: var(--text-soft);
  font-size: 11px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-result-command {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  color: var(--muted);
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-result-summary small {
  flex: 0 0 auto;
  align-self: center;
  color: var(--text-muted);
  font-size: 10px;
}
.agent-message-author {
  color: var(--text-muted);
  padding: 0 2px;
  font-size: 11px;
  font-weight: 600;
}
.agent-chat-message pre {
  width: 100%;
  box-sizing: border-box;
  margin: 0;
  padding: 11px 13px;
  color: var(--text-primary);
  border: 1px solid var(--border-subtle);
  border-radius: 4px 9px 9px 9px;
  background: var(--surface-soft, #22262f);
  font-family: inherit;
  font-size: 13px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.agent-markdown {
  padding: 11px 13px;
  color: var(--text-primary);
  border: 1px solid var(--border-subtle);
  border-radius: 4px 9px 9px 9px;
  background: var(--surface-soft, #22262f);
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}
.agent-chat-message.user .agent-markdown {
  border-radius: 9px 4px 9px 9px;
  border-color: color-mix(in srgb, var(--el-color-primary) 34%, var(--border-subtle));
  background: color-mix(in srgb, var(--el-color-primary) 10%, var(--surface-soft));
}
.agent-markdown :deep(:first-child) { margin-top: 0; }
.agent-markdown :deep(:last-child) { margin-bottom: 0; }
.agent-markdown :deep(p) { margin: 0 0 8px; }
.agent-markdown :deep(ul),
.agent-markdown :deep(ol) { margin: 5px 0 8px; padding-left: 21px; }
.agent-markdown :deep(blockquote) {
  margin: 7px 0;
  padding-left: 10px;
  border-left: 3px solid var(--accent-border);
  color: var(--text-soft);
}
.agent-markdown :deep(code) {
  padding: 1px 4px;
  border-radius: 3px;
  background: var(--surface-deep, #0d1210);
  font-family: var(--font-mono, monospace);
  font-size: 12px;
}
.agent-markdown :deep(pre) {
  margin: 8px 0;
  padding: 9px;
  overflow: auto;
  border-radius: 4px;
  background: var(--surface-deep, #0d1210);
}
.agent-markdown :deep(pre code) { padding: 0; background: transparent; }
.agent-markdown :deep(a) { color: var(--accent-strong); }
.agent-chat-message.user pre {
  border-color: color-mix(in srgb, var(--el-color-primary) 34%, var(--border-subtle));
  background: color-mix(in srgb, var(--el-color-primary) 10%, var(--surface-soft));
}
.agent-result-output {
  max-height: min(320px, 42vh);
  overflow: auto;
  font-family: "JetBrains Mono", "Cascadia Mono", Menlo, Consolas, monospace;
  font-size: 12px;
  white-space: pre;
  overflow-wrap: normal;
  word-break: normal;
}
.agent-chat-message.result.failed pre { border-color: color-mix(in srgb, #df6a6a 55%, var(--border-subtle)); }
.agent-composer-dock {
  position: sticky;
  bottom: 10px;
  z-index: 4;
  flex: 0 0 auto;
  align-self: center;
  box-sizing: border-box;
  width: min(920px, 100%);
  margin: auto 0 2px;
  padding: 8px 10px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  box-shadow: none;
}
.agent-command-flyout {
  position: absolute;
  right: 0;
  bottom: calc(100% + 10px);
  left: 0;
  z-index: 8;
  display: grid;
  gap: 8px;
  max-height: min(310px, 46vh);
  padding: 11px;
  overflow: auto;
  border: 1px solid color-mix(in srgb, #d6a84d 48%, var(--border-subtle));
  border-radius: 9px;
  background: color-mix(in srgb, var(--surface-deep, #0d1210) 94%, #d6a84d);
  box-shadow: 0 14px 36px #050807a8;
}
.agent-command-pending {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-height: 28px;
  margin-bottom: 7px;
  padding: 0 4px;
  color: #d6a84d;
  font-size: 11px;
}
.agent-command-approval-layer {
  position: fixed;
  inset: 0;
  z-index: 7000;
  display: grid;
  place-items: center;
  padding: 20px;
  background: #05070b62;
  pointer-events: auto;
}
.agent-command-approval {
  width: min(620px, calc(100vw - 40px));
  box-sizing: border-box;
  padding: 18px;
  border: 1px solid color-mix(in srgb, #d6a84d 58%, var(--border-subtle));
  border-radius: 10px;
  background: var(--surface, #181b22);
  box-shadow: 0 24px 70px #05070bd9, 0 0 0 1px #ffffff08;
}
.agent-command-approval-header {
  display: flex;
  align-items: center;
  gap: 10px;
}
.agent-command-approval-icon {
  display: grid;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  place-items: center;
  border-radius: 7px;
  color: #f0c56a;
  background: #d6a84d1c;
}
.agent-command-approval-header > div {
  display: grid;
  gap: 3px;
  min-width: 0;
}
.agent-command-approval-header strong { color: var(--text-primary); font-size: 14px; }
.agent-command-approval-header small { color: var(--text-muted); font-size: 11px; }
.agent-command-approval-count {
  margin-left: auto;
  color: var(--text-muted);
  font-size: 11px;
  white-space: nowrap;
}
.agent-command-approval-reason {
  margin-top: 15px;
  color: var(--text-soft);
  font-size: 12px;
  line-height: 1.5;
}
.agent-command-approval-command {
  max-height: 180px;
  margin: 10px 0 0;
  padding: 11px 12px;
  overflow: auto;
  color: var(--text-primary);
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: var(--surface-deep, #0e1117);
  font: 12px/1.55 "JetBrains Mono", "Cascadia Mono", Menlo, Consolas, monospace;
  white-space: pre;
}
.agent-command-approval-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}
.agent-command-approval-actions button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 32px;
  gap: 6px;
  padding: 0 13px;
  border-radius: 5px;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}
.agent-command-approval-cancel {
  border: 1px solid var(--border-subtle);
  background: transparent;
  color: var(--text-soft);
}
.agent-command-approval-confirm {
  border: 1px solid color-mix(in srgb, var(--el-color-primary) 72%, #fff);
  background: var(--el-color-primary);
  color: #fff;
}
.agent-command-approval-confirm:focus-visible,
.agent-command-approval-cancel:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--el-color-primary) 78%, #fff);
  outline-offset: 2px;
}
.agent-command-approval-confirm:disabled {
  cursor: not-allowed;
  opacity: .55;
}
.agent-command-flyout-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  color: var(--text-soft);
  font-size: 12px;
}
.agent-command-flyout-heading span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
}
.agent-command-flyout-heading svg { color: #d6a84d; }
.agent-command-flyout-heading small { color: var(--text-muted); }
.agent-command-proposal {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 11px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: var(--surface-soft);
}
.agent-command-proposal > div {
  display: grid;
  gap: 7px;
  min-width: 0;
  flex: 1;
}
.agent-command-proposal > div:first-child { align-self: stretch; }
.agent-command-actions {
  display: flex;
  flex: 0 0 auto;
  gap: 6px;
}
.agent-command-proposal strong { font-size: 12px; }
.agent-command-proposal code {
  display: block;
  padding: 7px 8px;
  overflow: auto;
  color: var(--text-primary);
  background: var(--surface-deep, #0e1117);
  font-size: 12px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.agent-chat-input {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 42px;
  grid-template-rows: minmax(44px, auto) 30px;
  align-items: end;
  gap: 0 6px;
  min-height: 82px;
  padding: 8px 9px 7px 11px;
  border: 1px solid color-mix(in srgb, var(--line-strong, #4b5568) 86%, transparent);
  border-radius: 13px;
  background: color-mix(in srgb, var(--surface-2, #22262f) 88%, #0d0e10);
  box-shadow: 0 8px 24px #05070b36, inset 0 1px 0 #ffffff08;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}
.agent-chat-input:focus-within {
  border-color: color-mix(in srgb, var(--accent, #5b8cff) 72%, var(--line-strong, #4b5568));
  box-shadow: 0 8px 24px #05070b36, 0 0 0 2px color-mix(in srgb, var(--accent, #5b8cff) 16%, transparent);
}
.agent-chat-input > :deep(.el-textarea) {
  grid-column: 1 / -1;
  grid-row: 1;
  width: 100%;
  align-self: stretch;
}
.agent-chat-input :deep(.el-textarea__inner) {
  min-height: 42px !important;
  max-height: 140px;
  padding: 8px 2px 6px;
  border: 0 !important;
  border-radius: 6px;
  background: transparent !important;
  box-shadow: none !important;
  line-height: 1.45;
}
.agent-chat-input > .el-button {
  grid-column: 2;
  grid-row: 2;
  width: 34px;
  height: 34px;
  align-self: end;
  justify-self: end;
  margin: 0;
  padding: 0;
  border-radius: 50%;
  z-index: 1;
}
.agent-chat-input > .agent-ai-controls {
  grid-column: 1 / -1;
  grid-row: 2;
  min-width: 0;
  padding-right: 48px;
}
.agent-thinking {
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 30px;
  padding-left: 40px;
}
.agent-thinking i {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--text-muted);
  animation: agent-thinking 1s ease-in-out infinite;
}
.agent-thinking i:nth-child(2) { animation-delay: 0.15s; }
.agent-thinking i:nth-child(3) { animation-delay: 0.3s; }
@keyframes agent-thinking { 50% { opacity: 0.25; transform: translateY(-2px); } }
.agent-empty { display: grid; place-items: center; gap: 8px; min-height: 170px; color: var(--text-muted); }
.agent-chat-empty { flex: 1 1 auto; }
@media (max-width: 720px) {
  .agent-window-body { padding: 12px 12px 18px; }
  .agent-message-body { max-width: 84%; }
  .agent-chat-message.result .agent-message-body {
    width: calc(100% - 42px);
    max-width: calc(100% - 42px);
  }
  .agent-chat-log { padding: 16px 10px 24px; }
  .agent-composer-dock { margin-top: auto; padding: 8px 8px 9px; }
  .agent-chat-input { grid-template-rows: minmax(44px, auto) auto; min-height: 82px; }
  .agent-ai-controls { flex-wrap: wrap; gap: 6px; }
  .agent-model-control { flex: 1 1 auto; }
  .agent-model-control :deep(.el-select) { width: min(180px, 42vw); }
  .agent-context-size { margin-left: 0; }
  .agent-command-flyout { max-height: 38vh; }
  .agent-command-proposal { align-items: stretch; flex-direction: column; }
  .agent-command-actions { align-self: flex-end; }
}
</style>
