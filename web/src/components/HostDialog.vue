<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { ChevronRight } from "@lucide/vue";
import { api, json } from "../api";
import type { Credential, Host } from "../types";

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  hosts: Host[];
  credentials: Credential[];
}>();
const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  saved: [host: Host];
}>();
const defaults = (): Host => ({
  id: "",
  name: "",
  address: "",
  protocol: "ssh",
  port: 22,
  username: "root",
  credentialID: "",
  groupName: "",
  tags: "",
  notes: "",
  initialDirectory: "",
  connectTimeout: 12,
  keepaliveInterval: 30,
  maxRetries: 5,
  terminalType: "xterm-256color",
  sessionMode: "tmux",
  jumpHostID: "",
  rdpMode: "web",
  rdpQuality: "crisp",
  rdpClipboard: true,
  rdpAudio: false,
  rdpDrive: false,
  rdpPrinting: false,
  rdpMultiMonitor: false,
  desktopDomain: "",
  desktopSecurity: "any",
  ignoreCertificate: true,
  desktopReadOnly: false,
});
const form = reactive<Host>(defaults());
const availableJumpHosts = computed(() =>
  props.hosts.filter(
    (host) => host.id !== form.id && (!host.protocol || host.protocol === "ssh"),
  ),
);
const availableCredentials = computed(() =>
  form.protocol === "ssh"
    ? props.credentials
    : props.credentials.filter((credential) => credential.kind === "password"),
);
const moreOpen = ref(false),
  advOpen = ref(false),
  authMode = ref<"password" | "credential" | "prompt">("password"),
  password = ref("");
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      Object.assign(form, defaults(), props.host || {});
      authMode.value = props.host
        ? props.host.credentialID
          ? "credential"
          : props.host.hasPassword
            ? "password"
          : "prompt"
        : "password";
      password.value = "";
      moreOpen.value = !!(
        props.host &&
        (props.host.groupName || props.host.tags || props.host.notes)
      );
      advOpen.value = false;
    }
  },
);
watch(
  () => form.protocol,
  (protocol, previous) => {
    const ports = { ssh: 22, vnc: 5900, rdp: 3389 } as const;
    if (!form.port || form.port === ports[previous || "ssh"])
      form.port = ports[protocol];
    if (protocol !== "ssh") {
      form.sessionMode = "normal";
      form.terminalType = "xterm-256color";
      if (
        form.credentialID &&
        !props.credentials.some(
          (credential) =>
            credential.id === form.credentialID && credential.kind === "password",
        )
      )
        form.credentialID = "";
    }
  },
);
async function save() {
  try {
    if (
      authMode.value === "password" &&
      !password.value &&
      !form.hasPassword
    )
      return ElMessage.warning(`请输入 ${form.protocol.toUpperCase()} 密码`);
    if (authMode.value === "credential" && !form.credentialID)
      return ElMessage.warning("请选择凭据");
    const method = form.id ? "PUT" : "POST";
    const path = form.id ? `/api/hosts/${form.id}` : "/api/hosts";
    const saved = await api<Host>(path, {
      method,
      body: json({
        ...form,
        credentialID: authMode.value === "credential" ? form.credentialID : "",
        password: authMode.value === "password" ? password.value : "",
        authMode: authMode.value,
      }),
    });
    emit("saved", saved);
    emit("update:modelValue", false);
    ElMessage.success("主机已保存");
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "保存失败");
  }
}
</script>
<template>
  <el-dialog
    :model-value="modelValue"
    class="host-dialog"
    :title="form.id ? '编辑主机' : '新增主机'"
    width="min(520px, calc(100vw - 24px))"
    align-center
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-form
      label-position="top"
      class="hd-form"
      @submit.prevent="save"
    >
      <el-segmented
        v-model="form.protocol"
        class="hd-proto"
        :options="[
          { label: 'SSH 终端', value: 'ssh' },
          { label: 'VNC 桌面', value: 'vnc' },
          { label: 'RDP 桌面', value: 'rdp' },
        ]"
      />
      <el-segmented
        v-if="form.protocol === 'rdp'"
        v-model="form.rdpMode"
        class="hd-proto hd-sub"
        :options="[
          { label: '浏览器内连接', value: 'web' },
          { label: '调用本地远程桌面', value: 'native' },
        ]"
      />

      <div class="hd-row hd-addr">
        <el-form-item label="主机地址">
          <el-input
            v-model="form.address"
            placeholder="server.example.com"
            autofocus
          />
        </el-form-item>
        <el-form-item label="端口">
          <el-input-number
            v-model="form.port"
            :min="1"
            :max="65535"
            :controls="false"
          />
        </el-form-item>
      </div>
      <div class="hd-row" :class="{ single: form.protocol === 'vnc' }">
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="例如 生产 API" />
        </el-form-item>
        <el-form-item v-if="form.protocol !== 'vnc'" label="用户名">
          <el-input v-model="form.username" />
        </el-form-item>
      </div>

      <div class="hd-auth">
        <div class="hd-auth-head">
          <span class="hd-label">认证方式</span>
          <el-segmented
            v-model="authMode"
            size="small"
            :options="[
              { label: '密码', value: 'password' },
              { label: '已存凭据', value: 'credential' },
              { label: '连接时输入', value: 'prompt' },
            ]"
          />
        </div>
        <el-input
          v-if="authMode === 'password'"
          v-model="password"
          type="password"
          show-password
          autocomplete="new-password"
          :placeholder="
            form.hasPassword
              ? '留空保留当前密码'
              : `${form.protocol.toUpperCase()} 密码，保存后加密存储`
          "
        />
        <el-select
          v-else-if="authMode === 'credential'"
          v-model="form.credentialID"
          placeholder="选择已保存凭据"
        >
          <el-option
            v-for="credential in availableCredentials"
            :key="credential.id"
            :label="credential.name"
            :value="credential.id"
          />
        </el-select>
        <p v-else class="hd-note">每次连接时会弹出输入框，不保存密码。</p>
      </div>

      <button
        type="button"
        class="hd-disclosure"
        :class="{ open: moreOpen }"
        :aria-expanded="moreOpen"
        @click="moreOpen = !moreOpen"
      >
        <ChevronRight :size="14" />
        <span>更多信息</span>
        <small>分组 · 标签 · 备注{{ form.protocol === "ssh" ? " · 会话模式" : "" }}</small>
      </button>
      <div v-if="moreOpen" class="hd-panel">
        <div class="hd-row">
          <el-form-item label="分组">
            <el-input v-model="form.groupName" placeholder="生产/华东/数据库" />
          </el-form-item>
          <el-form-item label="标签">
            <el-input v-model="form.tags" placeholder="逗号分隔" />
          </el-form-item>
        </div>
        <el-form-item v-if="form.protocol === 'ssh'" label="终端会话">
          <el-segmented
            v-model="form.sessionMode"
            size="small"
            :options="[
              { label: 'tmux 持久模式', value: 'tmux' },
              { label: '普通 SSH', value: 'normal' },
            ]"
          />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="form.notes" type="textarea" :rows="2" resize="none" />
        </el-form-item>
      </div>

      <button
        type="button"
        class="hd-disclosure"
        :class="{ open: advOpen }"
        :aria-expanded="advOpen"
        @click="advOpen = !advOpen"
      >
        <ChevronRight :size="14" />
        <span>高级连接</span>
        <small>跳板机 · 超时 · 重试{{ form.protocol === "rdp" ? " · 安全模式" : "" }}</small>
      </button>
      <div v-if="advOpen" class="hd-panel">
        <div class="form-grid hd-adv">
            <el-form-item label="跳板机" class="span-2">
              <el-select
                v-model="form.jumpHostID"
                filterable
                placeholder="直连（不使用跳板机）"
                clearable
              >
                <el-option label="直连（不使用跳板机）" value="" />
                <el-option
                  v-for="jumpHost in availableJumpHosts"
                  :key="jumpHost.id"
                  :value="jumpHost.id"
                  :disabled="!jumpHost.credentialID && !jumpHost.hasPassword"
                  :label="`${jumpHost.name} · ${jumpHost.username}@${jumpHost.address}:${jumpHost.port}${jumpHost.credentialID || jumpHost.hasPassword ? '' : '（需先保存密码或凭据）'}`"
                />
              </el-select>
            </el-form-item>
            <el-form-item
              v-if="form.protocol === 'ssh'"
              label="启动目录"
              class="span-2"
              ><el-input
                v-model="form.initialDirectory"
                placeholder="例如 /srv/app，留空使用远程默认目录"
            /></el-form-item>
            <el-form-item label="连接超时（秒）"
              ><el-input-number
                v-model="form.connectTimeout"
                :min="3"
                :max="120"
                controls-position="right"
            /></el-form-item>
            <el-form-item v-if="form.protocol === 'ssh'" label="Keepalive（秒）"
              ><el-input-number
                v-model="form.keepaliveInterval"
                :min="0"
                :max="300"
                controls-position="right"
            /></el-form-item>
            <el-form-item v-if="form.protocol === 'ssh'" label="最大重试次数"
              ><el-input-number
                v-model="form.maxRetries"
                :min="0"
                :max="20"
                controls-position="right"
            /></el-form-item>
            <el-form-item v-if="form.protocol === 'ssh'" label="终端类型"
              ><el-select v-model="form.terminalType"
                ><el-option
                  label="xterm-256color"
                  value="xterm-256color" /><el-option
                  label="xterm"
                  value="xterm" /><el-option
                  label="screen-256color"
                  value="screen-256color" /></el-select
            ></el-form-item>
            <el-form-item
              v-if="form.protocol === 'rdp'"
              label="Windows 域"
            >
              <el-input v-model="form.desktopDomain" placeholder="可选" />
            </el-form-item>
            <el-form-item
              v-if="form.protocol === 'rdp'"
              label="RDP 安全模式"
            >
              <el-select v-model="form.desktopSecurity">
                <el-option label="自动协商" value="any" />
                <el-option label="NLA" value="nla" />
                <el-option label="TLS" value="tls" />
                <el-option label="RDP" value="rdp" />
              </el-select>
            </el-form-item>
            <el-form-item
              v-if="form.protocol === 'rdp'"
              label="忽略服务器证书"
            >
              <el-switch v-model="form.ignoreCertificate" />
            </el-form-item>
            <el-form-item
              v-if="form.protocol !== 'ssh'"
              label="只读模式"
            >
              <el-switch v-model="form.desktopReadOnly" />
            </el-form-item>
            <template v-if="form.protocol === 'rdp' && form.rdpMode === 'web'">
              <el-form-item label="画质模式" class="span-2">
                <el-segmented
                  v-model="form.rdpQuality"
                  :options="[
                    { label: '清晰', value: 'crisp' },
                    { label: '流畅', value: 'smooth' },
                  ]"
                />
              </el-form-item>
              <el-form-item label="剪贴板">
                <el-switch v-model="form.rdpClipboard" />
              </el-form-item>
              <el-form-item label="音频">
                <el-switch v-model="form.rdpAudio" />
              </el-form-item>
              <el-form-item label="磁盘映射">
                <el-switch v-model="form.rdpDrive" />
              </el-form-item>
              <el-form-item label="打印机映射">
                <el-switch v-model="form.rdpPrinting" />
              </el-form-item>
              <el-form-item label="多显示器">
                <el-switch v-model="form.rdpMultiMonitor" />
              </el-form-item>
            </template>
          </div>
      </div>
    </el-form>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>
