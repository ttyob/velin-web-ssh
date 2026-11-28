<script setup lang="ts">
import { onMounted, ref } from "vue";
import {
  Download,
  Network,
  Play,
  Plus,
  Save,
  Send,
  Square,
  Trash2,
  Upload,
} from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import type {
  Host,
  PortForward,
  Snippet,
  TerminalSession,
} from "../types";

const props = defineProps<{
  section: string;
  hosts: Host[];
  sessions: TerminalSession[];
}>();
const emit = defineEmits<{
  insert: [string];
  execute: [string];
  batchExecute: [string, string[]];
}>();
const snippets = ref<Snippet[]>([]),
  editing = ref<Snippet>(),
  snippetDialog = ref(false),
  importDialog = ref(false),
  importText = ref(""),
  preview = ref<any[]>([]);
const forwards = ref<PortForward[]>([]),
  forwardDialog = ref(false),
  editingForward = ref<PortForward>();
const batchMode = ref(false),
  batchTargets = ref<string[]>([]);
const blank = (): Snippet => ({
  id: "",
  name: "",
  groupName: "",
  tags: "",
  command: "",
  description: "",
});
async function load() {
  [snippets.value, forwards.value] = await Promise.all([
    api<Snippet[]>("/api/snippets"),
    api<PortForward[]>("/api/forwards"),
  ]);
}
onMounted(load);
function edit(value?: Snippet) {
  editing.value = value ? { ...value } : blank();
  snippetDialog.value = true;
}
async function save() {
  if (!editing.value) return;
  const method = editing.value.id ? "PUT" : "POST",
    path = editing.value.id
      ? `/api/snippets/${editing.value.id}`
      : "/api/snippets";
  const value = await api<Snippet>(path, { method, body: json(editing.value) });
  const index = snippets.value.findIndex((item) => item.id === value.id);
  if (index >= 0) snippets.value[index] = value;
  else snippets.value.push(value);
  snippetDialog.value = false;
  ElMessage.success("命令片段已保存");
}
async function remove(value: Snippet) {
  try {
    await ElMessageBox.confirm(`删除片段“${value.name}”？`, "删除片段", {
      type: "warning",
    });
    await api(`/api/snippets/${value.id}`, { method: "DELETE" });
    snippets.value = snippets.value.filter((item) => item.id !== value.id);
  } catch {}
}
function variables(command: string) {
  return [
    ...new Set(
      [...command.matchAll(/\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}/g)].map(
        (match) => match[1],
      ),
    ),
  ];
}
async function resolve(value: Snippet) {
  let command = value.command;
  for (const variable of variables(command)) {
    try {
      const result = await ElMessageBox.prompt(
        `输入变量 ${variable}`,
        "填充命令变量",
        {
          confirmButtonText: "继续",
          cancelButtonText: "取消",
          inputValidator: (v) => Boolean(v) || "请输入变量值",
        },
      );
      command = command.replaceAll(`\${${variable}}`, result.value);
    } catch {
      return "";
    }
  }
  return command;
}
async function insert(value: Snippet) {
  const command = await resolve(value);
  if (command) emit("insert", command);
}
async function execute(value: Snippet) {
  const command = await resolve(value);
  if (!command) return;
  const risky =
    /\n|\r|\brm\s+-rf\b|\bmkfs\b|\bshutdown\b|\breboot\b|:\(\)\s*\{/.test(
      command,
    );
  if (batchMode.value) {
    const targets = props.sessions.filter((item) =>
      batchTargets.value.includes(item.id),
    );
    if (!targets.length) return ElMessage.warning("请选择至少一个目标终端");
    try {
      await ElMessageBox.confirm(
        `命令将立即发送到 ${targets.length} 个终端：\n${targets.map((item) => `• ${item.name}`).join("\n")}\n\n请确认所有目标均正确。`,
        "批量执行命令",
        {
          confirmButtonText: "发送并执行",
          cancelButtonText: "取消",
          type: "warning",
        },
      );
      emit(
        "batchExecute",
        command,
        targets.map((item) => item.id),
      );
    } catch {}
    return;
  }
  if (risky)
    try {
      await ElMessageBox.confirm(
        "该命令包含多行或高风险模式，发送后会立即执行。",
        "确认执行",
        {
          confirmButtonText: "发送并执行",
          cancelButtonText: "取消",
          type: "warning",
        },
      );
    } catch {
      return;
    }
  emit("execute", command);
}
async function exportData() {
  const response = await fetch("/api/data/export", {
    credentials: "same-origin",
  });
  if (!response.ok) return ElMessage.error("导出失败");
  const blob = await response.blob(),
    link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `jeex-terminal-export-${new Date().toISOString().slice(0, 10)}.json`;
  link.click();
  setTimeout(() => URL.revokeObjectURL(link.href), 0);
}
async function previewImport() {
  const result = await api<{ hosts: any[] }>("/api/data/import/openssh", {
    method: "POST",
    body: json({ content: importText.value, commit: false }),
  });
  preview.value = result.hosts;
}
async function commitImport() {
  const result = await api<{ created: number }>("/api/data/import/openssh", {
    method: "POST",
    body: json({ content: importText.value, commit: true }),
  });
  ElMessage.success(`已导入 ${result.created} 台主机`);
  importDialog.value = false;
  location.reload();
}
const blankForward = (): PortForward => ({
  id: "",
  hostID: props.hosts.find((host) => host.credentialID)?.id || "",
  name: "",
  kind: "local",
  listenAddress: "127.0.0.1",
  listenPort: 8080,
  targetHost: "127.0.0.1",
  targetPort: 80,
  status: "stopped",
  lastError: "",
  bytesIn: 0,
  bytesOut: 0,
});
function editForward(value?: PortForward) {
  editingForward.value = value ? { ...value } : blankForward();
  forwardDialog.value = true;
}
async function saveForward() {
  if (!editingForward.value) return;
  const method = editingForward.value.id ? "PUT" : "POST",
    path = editingForward.value.id
      ? `/api/forwards/${editingForward.value.id}`
      : "/api/forwards";
  const saved = await api<PortForward>(path, {
      method,
      body: json(editingForward.value),
    }),
    index = forwards.value.findIndex((item) => item.id === saved.id);
  if (index >= 0) forwards.value[index] = saved;
  else forwards.value.unshift(saved);
  forwardDialog.value = false;
}
async function toggleForward(value: PortForward) {
  try {
    const action = value.status === "running" ? "stop" : "start",
      saved = await api<PortForward>(`/api/forwards/${value.id}/${action}`, {
        method: "POST",
      });
    Object.assign(value, saved);
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "操作失败");
  }
}
async function deleteForward(value: PortForward) {
  try {
    await ElMessageBox.confirm(`删除转发“${value.name}”？`, "删除转发", {
      type: "warning",
    });
    await api(`/api/forwards/${value.id}`, { method: "DELETE" });
    forwards.value = forwards.value.filter((item) => item.id !== value.id);
  } catch {}
}
</script>
<template>
  <div class="settings-tools-panel">
    <el-tabs :model-value="section" class="embedded-tool-tabs">
      <el-tab-pane label="命令片段" name="snippets"
        ><div class="tool-heading">
          <span>个人命令片段 · {{ snippets.length }}</span
          ><el-button :icon="Plus" type="primary" size="small" @click="edit()"
            >新增</el-button
          >
        </div>
        <div class="batch-command-panel" :class="{ enabled: batchMode }">
          <div>
            <strong>多终端发送</strong>
            <small>关闭时“执行”仅发送到当前聚焦终端。</small>
          </div>
          <el-switch v-model="batchMode" />
          <el-checkbox-group v-if="batchMode" v-model="batchTargets">
            <el-checkbox
              v-for="session in sessions"
              :key="session.id"
              :value="session.id"
            >
              {{ session.name }} ·
              {{
                hosts.find((host) => host.id === session.hostID)?.name ||
                session.remoteUser
              }}
            </el-checkbox>
          </el-checkbox-group>
          <span v-if="batchMode && !sessions.length" class="muted"
            >当前工作区没有可选终端</span
          >
        </div>
        <div class="list-stack">
          <div v-for="item in snippets" :key="item.id" class="snippet-row">
            <div>
              <strong>{{ item.name }}</strong
              ><small
                >{{ item.groupName
                }}<template v-if="item.description">
                  · {{ item.description }}</template
                ></small
              ><code>{{ item.command }}</code>
            </div>
            <div class="row-actions">
              <el-button :icon="Send" text @click="insert(item)">插入</el-button
              ><el-button :icon="Play" text @click="execute(item)"
                >执行</el-button
              ><el-button text @click="edit(item)">编辑</el-button
              ><el-button
                :icon="Trash2"
                text
                type="danger"
                @click="remove(item)"
              />
            </div>
          </div></div
      ></el-tab-pane>
      <el-tab-pane label="端口转发" name="forwards"
        ><div class="tool-heading">
          <span>本地、远程与 SOCKS 转发 · {{ forwards.length }}</span
          ><el-button :icon="Plus" type="primary" size="small" @click="editForward()"
            >新增</el-button
          >
        </div>
        <div class="list-stack">
          <div v-for="item in forwards" :key="item.id" class="forward-row">
            <Network :size="18" />
            <div>
              <strong>{{ item.name }}</strong
              ><small
                >{{ item.kind }} · {{ item.listenAddress }}:{{ item.listenPort
                }}<template v-if="item.kind !== 'dynamic'">
                  → {{ item.targetHost }}:{{ item.targetPort }}</template
                >
                · {{ item.status }}</small
              ><span v-if="item.lastError">{{ item.lastError }}</span>
            </div>
            <div class="row-actions">
              <el-button
                :icon="item.status === 'running' ? Square : Play"
                :type="item.status === 'running' ? 'warning' : 'primary'"
                plain
                @click="toggleForward(item)"
                >{{ item.status === "running" ? "停止" : "启动" }}</el-button
              ><el-button
                text
                :disabled="item.status === 'running'"
                @click="editForward(item)"
                >编辑</el-button
              ><el-button
                :icon="Trash2"
                text
                type="danger"
                @click="deleteForward(item)"
              />
            </div>
          </div></div
      ></el-tab-pane>
      <el-tab-pane label="数据" name="data"
        ><div class="data-tool">
          <Download :size="24" />
          <div>
            <strong>脱敏数据导出</strong
            ><small
              >包含主机、分组、标签、终端偏好和命令片段，不包含任何凭据。</small
            >
          </div>
          <el-button :icon="Download" @click="exportData">导出 JSON</el-button>
        </div>
        <div class="data-tool">
          <Upload :size="24" />
          <div>
            <strong>OpenSSH config 导入</strong
            ><small>先预览识别结果和不支持字段，再确认写入。</small>
          </div>
          <el-button :icon="Upload" @click="importDialog = true"
            >导入</el-button
          >
        </div></el-tab-pane
      >
    </el-tabs>
    <el-dialog
      v-model="snippetDialog"
      class="tool-form-dialog snippet-dialog"
      :title="editing?.id ? '编辑片段' : '新增片段'"
      width="min(600px, 90vw)"
      append-to-body
      ><el-form v-if="editing" label-position="top"
        ><div class="form-grid">
          <el-form-item label="名称"
            ><el-input v-model="editing.name" /></el-form-item
          ><el-form-item label="分组"
            ><el-input v-model="editing.groupName" /></el-form-item
          ><el-form-item label="标签" class="span-2"
            ><el-input v-model="editing.tags" /></el-form-item
          ><el-form-item label="命令" class="span-2"
            ><el-input
              v-model="editing.command"
              type="textarea"
              :rows="6"
              placeholder="支持 ${variable} 占位符" /></el-form-item
          ><el-form-item label="说明" class="span-2"
            ><el-input v-model="editing.description"
          /></el-form-item></div></el-form
      ><template #footer
        ><el-button @click="snippetDialog = false">取消</el-button
        ><el-button :icon="Save" type="primary" @click="save"
          >保存</el-button
        ></template
      ></el-dialog
    >
    <el-dialog
      v-model="importDialog"
      class="tool-form-dialog import-dialog"
      title="导入 OpenSSH config"
      width="min(700px, 94vw)"
      append-to-body
      ><el-input
        v-model="importText"
        type="textarea"
        :rows="12"
        placeholder="粘贴 ~/.ssh/config 内容"
      /><el-button class="import-preview" @click="previewImport"
        >预览</el-button
      >
      <div v-if="preview.length" class="import-list">
        <div v-for="item in preview" :key="item.name">
          <strong>{{ item.name }}</strong
          ><span
            >{{ item.username || "root" }}@{{ item.address }}:{{
              item.port
            }}</span
          ><small v-for="warning in item.warnings" :key="warning">{{
            warning
          }}</small>
        </div>
      </div>
      <template #footer
        ><el-button @click="importDialog = false">取消</el-button
        ><el-button
          type="primary"
          :disabled="!preview.length"
          @click="commitImport"
          >确认导入 {{ preview.length }} 台</el-button
        ></template
      ></el-dialog
    >
    <el-dialog
      v-model="forwardDialog"
      class="tool-form-dialog forward-dialog"
      :title="editingForward?.id ? '编辑端口转发' : '新增端口转发'"
      width="min(620px, 94vw)"
      append-to-body
      ><el-form v-if="editingForward" label-position="top"
        ><div class="form-grid">
          <el-form-item label="名称"
            ><el-input v-model="editingForward.name" /></el-form-item
          ><el-form-item label="主机"
            ><el-select v-model="editingForward.hostID"
              ><el-option
                v-for="host in hosts.filter((item) => item.credentialID)"
                :key="host.id"
                :label="host.name"
                :value="host.id" /></el-select></el-form-item
          ><el-form-item label="类型"
            ><el-select v-model="editingForward.kind"
              ><el-option label="本地转发" value="local" /><el-option
                label="远程转发"
                value="remote" /><el-option
                label="动态 SOCKS5"
                value="dynamic" /></el-select></el-form-item
          ><el-form-item label="监听地址"
            ><el-input
              v-model="editingForward.listenAddress"
              disabled /></el-form-item
          ><el-form-item label="监听端口"
            ><el-input-number
              v-model="editingForward.listenPort"
              :min="1"
              :max="65535" /></el-form-item
          ><template v-if="editingForward.kind !== 'dynamic'"
            ><el-form-item label="目标地址"
              ><el-input v-model="editingForward.targetHost" /></el-form-item
            ><el-form-item label="目标端口"
              ><el-input-number
                v-model="editingForward.targetPort"
                :min="1"
                :max="65535" /></el-form-item
          ></template></div></el-form
      ><template #footer
        ><el-button @click="forwardDialog = false">取消</el-button
        ><el-button type="primary" @click="saveForward"
          >保存</el-button
        ></template
      ></el-dialog
    >
  </div>
</template>
