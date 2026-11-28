<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import {
  ChevronRight,
  FlaskConical,
  KeyRound,
  Monitor,
  Play,
  Plus,
  SquareTerminal,
} from "@lucide/vue";
import { api, json } from "../api";
import type { Credential, Host } from "../types";
import CredentialDialog from "./CredentialDialog.vue";

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  hosts: Host[];
  credentials: Credential[];
  testing?: boolean;
}>();
const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  saved: [host: Host];
  saveAndConnect: [host: Host];
  credentialCreated: [credential: Credential];
  test: [host: Host];
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
  // New hosts start as a plain SSH session; tmux persistence is opt-in.
  sessionMode: "normal",
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
const extraOpen = ref(false);
const authMode = ref<"password" | "credential" | "prompt">("password");
const password = ref("");
const saving = ref(false);
const credentialDialogOpen = ref(false);
const localCredentials = ref<Credential[]>([]);
const addressInput = ref<{ focus?: () => void }>();
const usernameInput = ref<{ focus?: () => void }>();
const passwordInput = ref<{ focus?: () => void }>();
const errors = reactive({
  address: "",
  port: "",
  username: "",
  auth: "",
});

const mergedCredentials = computed(() => {
  const byID = new Map<string, Credential>();
  for (const item of props.credentials) byID.set(item.id, item);
  for (const item of localCredentials.value) byID.set(item.id, item);
  return [...byID.values()];
});
const liveHost = computed(() =>
  form.id ? props.hosts.find((item) => item.id === form.id) || props.host : undefined,
);
const availableJumpHosts = computed(() =>
  props.hosts.filter(
    (host) => host.id !== form.id && (!host.protocol || host.protocol === "ssh"),
  ),
);
const availableCredentials = computed(() =>
  form.protocol === "ssh"
    ? mergedCredentials.value
    : mergedCredentials.value.filter((credential) => credential.kind === "password"),
);
const groupOptions = computed(() => {
  const paths = new Set<string>();
  for (const host of props.hosts) {
    const parts = (host.groupName || "")
      .split("/")
      .map((part) => part.trim())
      .filter(Boolean);
    let acc = "";
    for (const part of parts) {
      acc = acc ? `${acc}/${part}` : part;
      paths.add(acc);
    }
  }
  return [...paths].sort((a, b) => a.localeCompare(b));
});
const tagSuggestions = computed(() => {
  const items = new Set<string>();
  for (const host of props.hosts) {
    for (const tag of (host.tags || "").split(/[,，]/)) {
      const trimmed = tag.trim();
      if (trimmed) items.add(trimmed);
    }
  }
  return [...items].sort((a, b) => a.localeCompare(b));
});
const tagList = computed<string[]>({
  get() {
    return (form.tags || "")
      .split(/[,，]/)
      .map((item) => item.trim())
      .filter(Boolean);
  },
  set(values) {
    const unique = [...new Set(values.map((item) => item.trim()).filter(Boolean))];
    form.tags = unique.join(", ");
  },
});
const extraSummary = computed(() => {
  const items: string[] = [];
  if (form.protocol === "ssh" && form.sessionMode === "tmux") items.push("tmux");
  if (tagList.value.length) items.push(`${tagList.value.length} 个标签`);
  if (form.notes.trim()) items.push("有备注");
  if (form.jumpHostID) items.push("跳板机");
  if (form.protocol === "ssh" && form.initialDirectory.trim()) items.push("启动目录");
  if (form.connectTimeout !== 12) items.push(`超时 ${form.connectTimeout}s`);
  if (form.protocol === "ssh") {
    if (
      form.keepaliveInterval !== 30 ||
      form.maxRetries !== 5 ||
      form.terminalType !== "xterm-256color"
    ) {
      items.push("已自定义连接参数");
    }
  } else if (form.protocol === "rdp") {
    if (
      form.desktopDomain ||
      form.desktopSecurity !== "any" ||
      !form.ignoreCertificate ||
      form.desktopReadOnly ||
      (form.rdpMode === "web" &&
        (form.rdpQuality !== "crisp" ||
          !form.rdpClipboard ||
          form.rdpAudio ||
          form.rdpDrive ||
          form.rdpPrinting ||
          form.rdpMultiMonitor))
    ) {
      items.push("已自定义 RDP");
    }
  } else if (form.desktopReadOnly) {
    items.push("只读");
  }
  return items.join(" · ");
});
const protocolAccent = computed(() => {
  if (form.protocol === "vnc") return "#62c9a2";
  if (form.protocol === "rdp") return "#69aefc";
  return "#6aa8f5";
});

function clearErrors() {
  errors.address = "";
  errors.port = "";
  errors.username = "";
  errors.auth = "";
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      Object.assign(form, defaults(), props.host || {});
      extraOpen.value = false;
      authMode.value = props.host
        ? props.host.credentialID
          ? "credential"
          : props.host.hasPassword
            ? "password"
            : "prompt"
        : "password";
      password.value = "";
      localCredentials.value = [];
      clearErrors();
      if (!props.host) {
        void nextTick(() => addressInput.value?.focus?.());
      }
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
        !mergedCredentials.value.some(
          (credential) =>
            credential.id === form.credentialID && credential.kind === "password",
        )
      )
        form.credentialID = "";
    }
    if (protocol === "rdp" && form.rdpMode === "native") {
      form.jumpHostID = "";
    }
    errors.username = "";
    errors.auth = "";
  },
);
watch(
  () => form.rdpMode,
  (mode) => {
    if (form.protocol === "rdp" && mode === "native") {
      form.jumpHostID = "";
    }
  },
);
watch(authMode, () => {
  errors.auth = "";
});

function parseConnectionString(raw: string): {
  protocol?: "ssh" | "vnc" | "rdp";
  username?: string;
  address?: string;
  port?: number;
} | null {
  const text = raw.trim();
  if (!text) return null;

  // Match URI forms like ssh://user@host:2222, rdp://admin@host:3389, vnc://host:5901
  const schemeMatch = text.match(/^(ssh|vnc|rdp):\/\/(.+)$/i);
  if (schemeMatch) {
    const protocol = schemeMatch[1].toLowerCase() as "ssh" | "vnc" | "rdp";
    const parsed = parseTarget(schemeMatch[2].split(/[/?#]/)[0]);
    if (parsed) return { protocol, ...parsed };
    return null;
  }

  // Match ssh CLI command like: ssh -p 2222 user@host or ssh user@host -p 2222
  if (/^ssh\s+/i.test(text)) {
    const tokens = text.split(/\s+/).slice(1);
    let port: number | undefined;
    let targetToken = "";
    for (let i = 0; i < tokens.length; i++) {
      const token = tokens[i];
      if ((token === "-p" || token === "-P") && tokens[i + 1]) {
        const parsedPort = Number(tokens[i + 1]);
        if (Number.isInteger(parsedPort) && parsedPort >= 1 && parsedPort <= 65535) {
          port = parsedPort;
        }
        i++;
      } else if (/^-p(\d+)$/i.test(token)) {
        const parsedPort = Number(token.slice(2));
        if (Number.isInteger(parsedPort) && parsedPort >= 1 && parsedPort <= 65535) {
          port = parsedPort;
        }
      } else if (token.startsWith("-")) {
        if (["-i", "-l", "-J", "-F", "-o", "-L", "-R", "-D"].includes(token)) i++;
      } else if (!targetToken) {
        targetToken = token;
      }
    }
    if (targetToken) {
      const parsed = parseTarget(targetToken);
      if (parsed) {
        return {
          protocol: "ssh",
          username: parsed.username,
          address: parsed.address,
          port: port ?? parsed.port,
        };
      }
    }
    return null;
  }

  // Only intercept plain target strings if they contain @ or an explicit :port
  if (text.includes("@") || /^\[[^\]]+\]:\d+$/.test(text) || /^[^:\s]+:\d+$/.test(text)) {
    return parseTarget(text);
  }
  return null;
}

function parseTarget(target: string): {
  username?: string;
  address: string;
  port?: number;
} | null {
  let rest = target.trim();
  if (!rest || /\s/.test(rest)) return null;
  let username: string | undefined;
  const atIndex = rest.lastIndexOf("@");
  if (atIndex > 0) {
    username = rest.slice(0, atIndex).trim();
    rest = rest.slice(atIndex + 1).trim();
  }
  let address = rest;
  let port: number | undefined;
  const bracketMatch = rest.match(/^\[([^\]]+)\](?::(\d+))?$/);
  if (bracketMatch) {
    address = bracketMatch[1];
    if (bracketMatch[2]) port = Number(bracketMatch[2]);
  } else {
    const colonCount = (rest.match(/:/g) || []).length;
    if (colonCount === 1) {
      const [hostPart, portPart] = rest.split(":");
      if (/^\d+$/.test(portPart)) {
        address = hostPart;
        port = Number(portPart);
      }
    }
  }
  if (!address) return null;
  if (port !== undefined && (!Number.isInteger(port) || port < 1 || port > 65535)) {
    return null;
  }
  return { username, address, port };
}

function handleAddressPaste(event: ClipboardEvent) {
  const raw = event.clipboardData?.getData("text") || "";
  const parsed = parseConnectionString(raw);
  if (!parsed || !parsed.address) return;
  event.preventDefault();
  if (parsed.protocol && !form.id) form.protocol = parsed.protocol;
  form.address = parsed.address;
  errors.address = "";
  if (parsed.port) {
    form.port = parsed.port;
    errors.port = "";
  }
  if (parsed.username && form.protocol !== "vnc") {
    form.username = parsed.username;
    errors.username = "";
  }
  const filled: string[] = ["地址"];
  if (parsed.username && form.protocol !== "vnc") filled.push("用户名");
  if (parsed.port) filled.push("端口");
  ElMessage.success(`已自动识别${filled.join("、")}`);
}

function onCredentialCreated(credential: Credential) {
  localCredentials.value.push(credential);
  emit("credentialCreated", credential);
  if (form.protocol !== "ssh" && credential.kind !== "password") {
    ElMessage.info("已保存私钥凭据（VNC/RDP 仅支持密码凭据）");
    return;
  }
  authMode.value = "credential";
  form.credentialID = credential.id;
  errors.auth = "";
}

function validateForm(): boolean {
  clearErrors();
  if (!form.address.trim()) {
    errors.address = "请输入主机地址或 IP";
    void nextTick(() => addressInput.value?.focus?.());
    return false;
  }
  if (!form.port || form.port < 1 || form.port > 65535) {
    errors.port = "端口范围 1–65535";
    return false;
  }
  if (form.protocol !== "vnc" && !form.username.trim()) {
    errors.username = "请输入登录用户名";
    void nextTick(() => usernameInput.value?.focus?.());
    return false;
  }
  if (authMode.value === "password" && !password.value && !form.hasPassword) {
    errors.auth = `请输入 ${form.protocol.toUpperCase()} 密码`;
    void nextTick(() => passwordInput.value?.focus?.());
    return false;
  }
  if (authMode.value === "credential" && !form.credentialID) {
    errors.auth = "请选择已存凭据，或点击右侧新建";
    return false;
  }
  return true;
}

async function save(connectAfter = false) {
  if (saving.value) return;
  if (!validateForm()) return;
  saving.value = true;
  try {
    const payload: Host & { password: string; authMode: string } = {
      ...form,
      name: form.name.trim() || form.address.trim(),
      address: form.address.trim(),
      username: form.username.trim(),
      jumpHostID:
        form.protocol === "rdp" && form.rdpMode === "native" ? "" : form.jumpHostID,
      credentialID: authMode.value === "credential" ? form.credentialID : "",
      password: authMode.value === "password" ? password.value : "",
      authMode: authMode.value,
    };
    const method = form.id ? "PUT" : "POST";
    const path = form.id ? `/api/hosts/${form.id}` : "/api/hosts";
    const saved = await api<Host>(path, {
      method,
      body: json(payload),
    });
    emit("saved", saved);
    emit("update:modelValue", false);
    if (connectAfter) {
      emit("saveAndConnect", saved);
    } else {
      ElMessage.success(form.id ? "主机已更新" : "主机已添加");
    }
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "保存失败");
  } finally {
    saving.value = false;
  }
}
</script>
<template>
  <el-dialog
    :model-value="modelValue"
    class="host-dialog"
    width="min(540px, calc(100vw - 24px))"
    append-to-body
    :close-on-click-modal="false"
    align-center
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div class="hd-header">
        <span class="hd-header-icon" :style="{ '--hd-proto-color': protocolAccent }">
          <SquareTerminal v-if="form.protocol === 'ssh'" :size="15" />
          <Monitor v-else :size="15" />
        </span>
        <strong>{{ form.id ? "编辑主机" : "新增主机" }}</strong>
        <span
          v-if="liveHost && liveHost.lastStatus === 'online'"
          class="hd-status-text online"
        >
          在线<template v-if="liveHost.lastLatencyMs"> · {{ liveHost.lastLatencyMs }} ms</template>
        </span>
        <span
          v-else-if="liveHost && liveHost.lastStatus === 'offline'"
          class="hd-status-text offline"
        >
          离线
        </span>
      </div>
    </template>

    <el-form
      label-position="top"
      class="hd-form"
      @submit.prevent="save(false)"
    >
      <div class="hd-proto-strip" role="radiogroup" aria-label="协议类型">
        <button
          type="button"
          role="radio"
          :aria-checked="form.protocol === 'ssh'"
          class="proto-ssh"
          :class="{ active: form.protocol === 'ssh' }"
          @click="form.protocol = 'ssh'"
        >
          <SquareTerminal :size="14" />
          <span>SSH 终端</span>
        </button>
        <button
          type="button"
          role="radio"
          :aria-checked="form.protocol === 'vnc'"
          class="proto-vnc"
          :class="{ active: form.protocol === 'vnc' }"
          @click="form.protocol = 'vnc'"
        >
          <Monitor :size="14" />
          <span>VNC 桌面</span>
        </button>
        <button
          type="button"
          role="radio"
          :aria-checked="form.protocol === 'rdp'"
          class="proto-rdp"
          :class="{ active: form.protocol === 'rdp' }"
          @click="form.protocol = 'rdp'"
        >
          <Monitor :size="14" />
          <span>RDP 桌面</span>
        </button>
      </div>

      <div v-if="form.protocol === 'rdp'" class="hd-inline-row">
        <span class="hd-inline-label">连接方式</span>
        <div class="hd-inline-tabs" role="radiogroup" aria-label="RDP 连接方式">
          <button
            type="button"
            role="radio"
            :aria-checked="form.rdpMode === 'web'"
            :class="{ active: form.rdpMode === 'web' }"
            @click="form.rdpMode = 'web'"
          >
            浏览器内连接
          </button>
          <button
            type="button"
            role="radio"
            :aria-checked="form.rdpMode === 'native'"
            :class="{ active: form.rdpMode === 'native' }"
            @click="form.rdpMode = 'native'"
          >
            调用本地远程桌面
          </button>
        </div>
      </div>

      <div class="hd-row hd-addr">
        <el-form-item :class="{ 'is-error': Boolean(errors.address) }">
          <template #label>
            主机地址 <em class="hd-req">*</em>
          </template>
          <el-input
            ref="addressInput"
            v-model="form.address"
            class="hd-mono"
            :placeholder="
              form.protocol === 'ssh'
                ? 'IP / 域名，支持粘贴 user@host:port'
                : 'IP / 域名，支持粘贴 host:port'
            "
            @input="errors.address = ''"
            @paste="handleAddressPaste"
          />
          <span v-if="errors.address" class="hd-field-error">{{ errors.address }}</span>
        </el-form-item>
        <el-form-item :class="{ 'is-error': Boolean(errors.port) }">
          <template #label>
            端口 <em class="hd-req">*</em>
          </template>
          <el-input-number
            v-model="form.port"
            class="hd-mono"
            :min="1"
            :max="65535"
            :controls="false"
            @change="errors.port = ''"
          />
          <span v-if="errors.port" class="hd-field-error">{{ errors.port }}</span>
        </el-form-item>
      </div>

      <div class="hd-row" :class="{ single: form.protocol === 'vnc' }">
        <el-form-item label="显示名称">
          <el-input
            v-model="form.name"
            placeholder="留空默认使用主机地址"
          />
        </el-form-item>
        <el-form-item
          v-if="form.protocol !== 'vnc'"
          :class="{ 'is-error': Boolean(errors.username) }"
        >
          <template #label>
            用户名 <em class="hd-req">*</em>
          </template>
          <el-input
            ref="usernameInput"
            v-model="form.username"
            class="hd-mono"
            placeholder="root"
            @input="errors.username = ''"
          />
          <span v-if="errors.username" class="hd-field-error">{{ errors.username }}</span>
        </el-form-item>
      </div>

      <div class="hd-auth-block" :class="{ 'is-error': Boolean(errors.auth) }">
        <div class="hd-inline-row">
          <span class="hd-inline-label">
            认证方式
            <em
              v-if="authMode === 'password' && form.hasPassword"
              class="hd-saved-hint"
            >已保存</em>
          </span>
          <div class="hd-inline-tabs" role="radiogroup" aria-label="认证方式">
            <button
              type="button"
              role="radio"
              :aria-checked="authMode === 'password'"
              :class="{ active: authMode === 'password' }"
              @click="authMode = 'password'"
            >
              密码
            </button>
            <button
              type="button"
              role="radio"
              :aria-checked="authMode === 'credential'"
              :class="{ active: authMode === 'credential' }"
              @click="authMode = 'credential'"
            >
              已存凭据
            </button>
            <button
              type="button"
              role="radio"
              :aria-checked="authMode === 'prompt'"
              :class="{ active: authMode === 'prompt' }"
              @click="authMode = 'prompt'"
            >
              连接时输入
            </button>
          </div>
        </div>

        <el-input
          v-if="authMode === 'password'"
          ref="passwordInput"
          v-model="password"
          type="password"
          show-password
          autocomplete="new-password"
          :placeholder="
            form.hasPassword
              ? '留空保留当前密码，输入则替换'
              : `输入 ${form.protocol.toUpperCase()} 密码，保存后加密存储`
          "
          @input="errors.auth = ''"
        />
        <div v-else-if="authMode === 'credential'" class="hd-cred-row">
          <el-select
            v-model="form.credentialID"
            filterable
            clearable
            placeholder="选择已保存的密钥或密码凭据"
            @change="errors.auth = ''"
          >
            <el-option
              v-for="credential in availableCredentials"
              :key="credential.id"
              :label="credential.name"
              :value="credential.id"
            >
              <span class="hd-cred-option">
                <KeyRound :size="12" />
                <span>{{ credential.name }}</span>
                <small>{{ credential.kind === "key" ? "私钥" : "密码" }}</small>
              </span>
            </el-option>
          </el-select>
          <el-button
            class="hd-cred-add"
            title="新建密钥或密码凭据"
            @click="credentialDialogOpen = true"
          >
            <Plus :size="14" />
            <span>新建凭据</span>
          </el-button>
        </div>
        <p v-else class="hd-plain-note">每次连接时弹出临时认证框，不保存密码。</p>
        <span v-if="errors.auth" class="hd-field-error">{{ errors.auth }}</span>
      </div>

      <el-form-item label="分组">
        <el-select
          v-model="form.groupName"
          filterable
          allow-create
          default-first-option
          clearable
          placeholder="选择已有分组，或输入如 生产/华东（可选）"
        >
          <el-option
            v-for="group in groupOptions"
            :key="group"
            :label="group"
            :value="group"
          />
        </el-select>
      </el-form-item>

      <button
        type="button"
        class="hd-more-toggle"
        :class="{ open: extraOpen }"
        :aria-expanded="extraOpen"
        @click="extraOpen = !extraOpen"
      >
        <ChevronRight :size="14" />
        <span>更多设置</span>
        <small>
          {{
            extraSummary ||
              (form.protocol === "ssh"
                ? "会话模式 · 标签 · 备注 · 跳板机 · 高级参数"
                : "标签 · 备注 · 跳板机 · 桌面选项")
          }}
        </small>
      </button>

      <div v-if="extraOpen" class="hd-more-body">
        <div v-if="form.protocol === 'ssh'" class="hd-inline-row">
          <span class="hd-inline-label">终端会话模式</span>
          <div class="hd-inline-tabs" role="radiogroup" aria-label="终端会话模式">
            <button
              type="button"
              role="radio"
              :aria-checked="form.sessionMode === 'normal'"
              :class="{ active: form.sessionMode === 'normal' }"
              @click="form.sessionMode = 'normal'"
            >
              普通 SSH
            </button>
            <button
              type="button"
              role="radio"
              :aria-checked="form.sessionMode === 'tmux'"
              :class="{ active: form.sessionMode === 'tmux' }"
              @click="form.sessionMode = 'tmux'"
            >
              tmux 持久
            </button>
          </div>
        </div>

        <div class="hd-row">
          <el-form-item label="标签">
            <el-select
              v-model="tagList"
              multiple
              filterable
              allow-create
              default-first-option
              collapse-tags
              collapse-tags-tooltip
              placeholder="输入回车添加"
            >
              <el-option
                v-for="tag in tagSuggestions"
                :key="tag"
                :label="tag"
                :value="tag"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="备注">
            <el-input
              v-model="form.notes"
              placeholder="用途或说明（可选）"
            />
          </el-form-item>
        </div>

        <el-form-item
          v-if="!(form.protocol === 'rdp' && form.rdpMode === 'native')"
          label="SSH 跳板机"
        >
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
        <p v-else class="hd-plain-note">
          本地远程桌面模式会调用本机客户端连接，不支持通过 SSH 跳板机转发。
        </p>

        <div v-if="form.protocol === 'ssh'" class="hd-row">
          <el-form-item label="启动目录">
            <el-input
              v-model="form.initialDirectory"
              class="hd-mono"
              placeholder="例如 /srv/app"
            />
          </el-form-item>
          <el-form-item label="终端类型">
            <el-select v-model="form.terminalType" class="hd-mono">
              <el-option label="xterm-256color" value="xterm-256color" />
              <el-option label="xterm" value="xterm" />
              <el-option label="screen-256color" value="screen-256color" />
            </el-select>
          </el-form-item>
        </div>

        <div
          class="hd-metrics-grid"
          :class="{ 'cols-1': form.protocol !== 'ssh' }"
        >
          <el-form-item label="连接超时（秒）">
            <el-input-number
              v-model="form.connectTimeout"
              class="hd-mono"
              :min="3"
              :max="120"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item v-if="form.protocol === 'ssh'" label="Keepalive（秒）">
            <el-input-number
              v-model="form.keepaliveInterval"
              class="hd-mono"
              :min="0"
              :max="300"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item v-if="form.protocol === 'ssh'" label="最大重试次数">
            <el-input-number
              v-model="form.maxRetries"
              class="hd-mono"
              :min="0"
              :max="20"
              controls-position="right"
            />
          </el-form-item>
        </div>

        <template v-if="form.protocol !== 'ssh'">
          <div v-if="form.protocol === 'rdp'" class="hd-row">
            <el-form-item label="Windows 域">
              <el-input v-model="form.desktopDomain" placeholder="可选" />
            </el-form-item>
            <el-form-item label="RDP 安全模式">
              <el-select v-model="form.desktopSecurity">
                <el-option label="自动协商" value="any" />
                <el-option label="NLA" value="nla" />
                <el-option label="TLS" value="tls" />
                <el-option label="RDP" value="rdp" />
              </el-select>
            </el-form-item>
          </div>
          <div
            v-if="form.protocol === 'rdp' && form.rdpMode === 'web'"
            class="hd-inline-row"
          >
            <span class="hd-inline-label">画质模式</span>
            <div class="hd-inline-tabs" role="radiogroup" aria-label="画质模式">
              <button
                type="button"
                role="radio"
                :aria-checked="form.rdpQuality === 'crisp'"
                :class="{ active: form.rdpQuality === 'crisp' }"
                @click="form.rdpQuality = 'crisp'"
              >
                清晰优先
              </button>
              <button
                type="button"
                role="radio"
                :aria-checked="form.rdpQuality === 'smooth'"
                :class="{ active: form.rdpQuality === 'smooth' }"
                @click="form.rdpQuality = 'smooth'"
              >
                流畅优先
              </button>
            </div>
          </div>
          <div class="hd-switch-grid">
            <label v-if="form.protocol === 'rdp'" class="hd-switch-item">
              <span>忽略服务器证书</span>
              <el-switch v-model="form.ignoreCertificate" size="small" />
            </label>
            <label class="hd-switch-item">
              <span>只读模式</span>
              <el-switch v-model="form.desktopReadOnly" size="small" />
            </label>
            <template v-if="form.protocol === 'rdp' && form.rdpMode === 'web'">
              <label class="hd-switch-item">
                <span>剪贴板同步</span>
                <el-switch v-model="form.rdpClipboard" size="small" />
              </label>
              <label class="hd-switch-item">
                <span>音频传输</span>
                <el-switch v-model="form.rdpAudio" size="small" />
              </label>
              <label class="hd-switch-item">
                <span>磁盘映射</span>
                <el-switch v-model="form.rdpDrive" size="small" />
              </label>
              <label class="hd-switch-item">
                <span>打印机映射</span>
                <el-switch v-model="form.rdpPrinting" size="small" />
              </label>
              <label class="hd-switch-item">
                <span>多显示器</span>
                <el-switch v-model="form.rdpMultiMonitor" size="small" />
              </label>
            </template>
          </div>
        </template>
      </div>
    </el-form>

    <template #footer>
      <div class="hd-footer">
        <div class="hd-footer-left">
          <el-button
            v-if="liveHost"
            class="hd-test-btn"
            :loading="Boolean(testing)"
            :disabled="saving"
            title="测试当前已保存配置"
            @click="emit('test', liveHost)"
          >
            <FlaskConical v-if="!testing" :size="14" />
            <span>{{ testing ? "测试中…" : "测试连接" }}</span>
          </el-button>
        </div>
        <div class="hd-footer-right">
          <el-button :disabled="saving" @click="emit('update:modelValue', false)">
            取消
          </el-button>
          <el-button :loading="saving" @click="save(false)">
            保存
          </el-button>
          <el-button
            type="primary"
            :loading="saving"
            @click="save(true)"
          >
            <Play v-if="!saving" :size="13" />
            <span>保存并连接</span>
          </el-button>
        </div>
      </div>
    </template>
  </el-dialog>
  <CredentialDialog
    v-model="credentialDialogOpen"
    @saved="onCredentialCreated"
  />
</template>
