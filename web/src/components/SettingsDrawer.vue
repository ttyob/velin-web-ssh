<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  Bot,
  Check,
  Code2,
  Database,
  Download,
  HardDriveDownload,
  FlaskConical,
  KeyRound,
  LockKeyhole,
  LogOut,
  MonitorCog,
  Network,
  Pencil,
  Plus,
  ListTodo,
  RefreshCw,
  Save,
  ServerCog,
  Shield,
  ShieldCheck,
  Trash2,
  Upload,
  UserPlus,
  Users,
} from "@lucide/vue";
import { api, json } from "../api";
import { useAuthStore } from "../stores/auth";
import type {
  AIModelConfig,
  Host,
  Credential,
  LoginDevice,
  Preferences,
  SecurityPolicy,
  TailscaleConfig,
  TerminalSession,
  UpdateCheck,
  User,
} from "../types";
import ToolsDrawer from "./ToolsDrawer.vue";
import CredentialsPanel from "./CredentialsPanel.vue";
import TaskPanel from "./TaskPanel.vue";
import {
  accentPresets,
  findInterfaceTheme,
  findTerminalTheme,
  interfaceThemePresets,
  terminalThemePresets,
} from "../themePresets";

const props = defineProps<{
  modelValue: boolean;
  preferences: Preferences;
  hosts: Host[];
  credentials: Credential[];
  sessions: TerminalSession[];
}>();
const emit = defineEmits<{
  "update:modelValue": [boolean];
  preferences: [Preferences];
  logout: [];
  insert: [string];
  execute: [string];
  batchExecute: [string, string[]];
  credentialSaved: [Credential];
  credentialDeleted: [string];
}>();
const auth = useAuthStore();
const router = useRouter();
const tab = ref("terminal"),
  users = ref<User[]>([]),
  devices = ref<LoginDevice[]>([]),
  stats = ref<any>({}),
  backups = ref<any[]>([]),
  tailscale = ref<TailscaleConfig>({
    enabled: false,
    hostname: "velin",
    controlURL: "",
    authKeyConfigured: false,
    status: { enabled: false, state: "disabled", tun: false },
  }),
  backupKey = ref(""),
  backupBusy = ref<"backup" | "restore">(),
  backupFileInput = ref<HTMLInputElement | null>(null);
const userDialogOpen = ref(false),
  userPasswordDialogOpen = ref(false),
  savingUser = ref(false),
  savingUserPassword = ref(false),
  userForm = ref({
    id: "",
    username: "",
    password: "",
    role: "user" as "admin" | "user",
    disabled: false,
  }),
  userPasswordForm = ref({ userID: "", username: "", password: "" }),
  passwordForm = ref({
    currentPassword: "",
    newPassword: "",
    confirmPassword: "",
  });
const lockPINConfigured = ref(false),
  lockPINSetupOpen = ref(false),
  savingLockPIN = ref(false),
  lockPINForm = ref({ pin: "", confirm: "" });
const updateCheck = ref<UpdateCheck>({
  currentVersion: "dev",
  latestVersion: "",
  versionKnown: false,
  updateAvailable: false,
  releaseURL: "",
  releaseName: "",
  publishedAt: "",
  checkedAt: "",
});
const updateCheckBusy = ref(false);
const updateCheckError = ref("");
let preferenceSaveTimer: number | undefined;
let preferenceSaveInFlight = false;
let preferenceSaveQueued = false;
let preferenceSaveNotice = false;
let savedPreferenceSnapshot = "";
const policy = ref<SecurityPolicy>({
  passwordMinLength: 10,
  loginFailureThreshold: 5,
  lockMinutes: 15,
  rememberDays: 7,
  forceChangeOnCreate: true,
});
const aiModel = ref<AIModelConfig>({
  baseURL: "",
  model: "",
  apiKeyConfigured: false,
  configured: false,
});
const aiModelForm = ref({
  baseURL: "",
  model: "",
  apiKey: "",
  clearAPIKey: false,
});
const savingAIModel = ref(false),
  testingAIModel = ref(false);
const tailscaleForm = ref({
  enabled: false,
  hostname: "velin",
  controlURL: "",
  authKey: "",
  clearAuthKey: false,
});
const savingTailscale = ref(false);
const terminalDefaults: Preferences = {
  theme: "dark",
  accentColor: "#5b8cff",
  terminalTheme: "velin",
  fontSize: 14,
  lineHeight: 1.25,
  fontWeight: 400,
  letterSpacing: 0,
  foreground: "#d8deea",
  background: "#111318",
  cursorColor: "#8eafff",
  cursorStyle: "block",
  cursorBlink: true,
  pasteGuard: true,
  visualBell: true,
  soundBell: false,
  lockEnabled: false,
  autoLockMinutes: 15,
  lockOnShortcut: true,
};
function selectTerminalTheme(id: string) {
  const preset = findTerminalTheme(id);
  props.preferences.terminalTheme = preset.id;
  props.preferences.background = preset.background;
  props.preferences.foreground = preset.foreground;
  props.preferences.cursorColor = preset.cursor;
}
function selectInterfaceTheme(id: string) {
  const preset = findInterfaceTheme(id);
  props.preferences.theme = preset.id;
  props.preferences.accentColor = preset.accent;
}
function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB"],
    index = Math.min(
      units.length - 1,
      Math.floor(Math.log(value) / Math.log(1024)),
    );
  return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`;
}
function formatDuration(seconds: number) {
  const days = Math.floor(seconds / 86400),
    hours = Math.floor((seconds % 86400) / 3600),
    minutes = Math.floor((seconds % 3600) / 60);
  return days
    ? `${days} 天 ${hours} 小时`
    : hours
      ? `${hours} 小时 ${minutes} 分钟`
      : `${minutes} 分钟`;
}

async function load() {
  const [loadedDevices, lockPIN] = await Promise.all([
    api<LoginDevice[]>("/api/auth/devices"),
    api<{ configured: boolean }>("/api/auth/lock-pin"),
  ]);
  devices.value = loadedDevices;
  lockPINConfigured.value = lockPIN.configured;
  if (!lockPIN.configured) props.preferences.lockEnabled = false;
	if (auth.user?.role === "admin")
		[users.value, stats.value, policy.value, aiModel.value, backups.value, tailscale.value] = await Promise.all([
			api<User[]>("/api/admin/users"),
			api<any>("/api/admin/stats"),
			api<SecurityPolicy>("/api/admin/security-policy"),
			api<AIModelConfig>("/api/admin/ai-model"),
			api<any[]>("/api/admin/backups"),
			api<TailscaleConfig>("/api/admin/tailscale"),
		]);
  if (auth.user?.role === "admin")
		aiModelForm.value = {
      baseURL: aiModel.value.baseURL,
      model: aiModel.value.model,
      apiKey: "",
      clearAPIKey: false,
		};
	tailscaleForm.value = {
			enabled: tailscale.value.enabled,
			hostname: tailscale.value.hostname,
			controlURL: tailscale.value.controlURL,
			authKey: "",
			clearAuthKey: false,
	};
  void checkForUpdate();
}
async function checkForUpdate(force = false) {
  if (updateCheckBusy.value) return;
  updateCheckBusy.value = true;
  updateCheckError.value = "";
  try {
    const suffix = force ? "?refresh=1" : "";
    updateCheck.value = await api<UpdateCheck>(`/api/system/update${suffix}`);
  } catch (error) {
    updateCheckError.value = error instanceof Error ? error.message : "检查更新失败";
  } finally {
    updateCheckBusy.value = false;
  }
}
function normalizePIN(value: string) {
  return value.replace(/\D/g, "").slice(0, 6);
}
function openLockPINSetup() {
  lockPINForm.value = { pin: "", confirm: "" };
  lockPINSetupOpen.value = true;
}
async function saveLockPIN() {
  const { pin, confirm } = lockPINForm.value;
  if (!/^\d{6}$/.test(pin))
    return ElMessage.warning("请输入 6 位数字 PIN");
  if (pin !== confirm) return ElMessage.warning("两次输入的 PIN 不一致");
  savingLockPIN.value = true;
  try {
    await api("/api/auth/lock-pin", {
      method: "PUT",
      body: json({ pin }),
    });
    lockPINConfigured.value = true;
    props.preferences.lockEnabled = true;
    lockPINSetupOpen.value = false;
    lockPINForm.value = { pin: "", confirm: "" };
    await persistPreferences(false);
    ElMessage.success("锁屏 PIN 已设置");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "PIN 保存失败");
  } finally {
    savingLockPIN.value = false;
  }
}
async function toggleLockFeature(value: string | number | boolean) {
  if (Boolean(value)) {
    if (lockPINConfigured.value) {
      props.preferences.lockEnabled = true;
      return;
    }
    openLockPINSetup();
    return;
  }
  try {
    await api("/api/auth/lock-pin", { method: "DELETE" });
    lockPINConfigured.value = false;
    props.preferences.lockEnabled = false;
    lockPINSetupOpen.value = false;
    lockPINForm.value = { pin: "", confirm: "" };
    await persistPreferences(false);
    ElMessage.success("锁屏已关闭");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "无法关闭锁屏");
  }
}
onMounted(load);
onBeforeUnmount(() => {
  clearTimeout(preferenceSaveTimer);
  if (preferenceSnapshot() !== savedPreferenceSnapshot)
    void persistPreferences(false);
});
function preferenceSnapshot() {
  return JSON.stringify(props.preferences);
}
function schedulePreferenceSave() {
  if (!props.modelValue) return;
  clearTimeout(preferenceSaveTimer);
  preferenceSaveTimer = window.setTimeout(
    () => void persistPreferences(false),
    400,
  );
}
async function persistPreferences(showNotice: boolean) {
  clearTimeout(preferenceSaveTimer);
  preferenceSaveTimer = undefined;
  const snapshot = preferenceSnapshot();
  if (snapshot === savedPreferenceSnapshot) {
    if (showNotice) ElMessage.success("设置已保存");
    return;
  }
  if (preferenceSaveInFlight) {
    preferenceSaveQueued = true;
    preferenceSaveNotice ||= showNotice;
    return;
  }
  preferenceSaveInFlight = true;
  let saved = false;
  try {
    await api("/api/preferences", {
      method: "PUT",
      body: snapshot,
    });
    savedPreferenceSnapshot = snapshot;
    saved = true;
    if (preferenceSnapshot() === snapshot)
      emit("preferences", JSON.parse(snapshot) as Preferences);
    if (showNotice) ElMessage.success("设置已保存");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "设置保存失败");
  } finally {
    preferenceSaveInFlight = false;
    const needsAnotherSave =
      preferenceSaveQueued ||
      (saved && preferenceSnapshot() !== savedPreferenceSnapshot);
    const notifyNext = preferenceSaveNotice;
    preferenceSaveQueued = false;
    preferenceSaveNotice = false;
    if (needsAnotherSave) void persistPreferences(notifyNext);
  }
}
async function savePreferences() {
  await persistPreferences(true);
}
async function resetPreferences() {
  if (lockPINConfigured.value) {
    try {
      await api("/api/auth/lock-pin", { method: "DELETE" });
      lockPINConfigured.value = false;
    } catch (error) {
      return ElMessage.error(
        error instanceof Error ? error.message : "无法关闭锁屏",
      );
    }
  }
  Object.assign(props.preferences, terminalDefaults);
  await savePreferences();
}
watch(
  () => props.preferences,
  () => schedulePreferenceSave(),
  { deep: true },
);
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      if (!savedPreferenceSnapshot)
        savedPreferenceSnapshot = preferenceSnapshot();
      return;
    }
    if (preferenceSnapshot() !== savedPreferenceSnapshot)
      void persistPreferences(false);
  },
);
async function revoke(id: string) {
  const device = devices.value.find((item) => item.id === id);
  await api(`/api/auth/devices/${id}`, { method: "DELETE" });
  devices.value = devices.value.filter((v) => v.id !== id);
  if (device?.current) emit("logout");
}
async function revokeAll() {
  try {
    await ElMessageBox.confirm(
      "这会撤销当前账户在所有浏览器中的登录，正在运行的远程终端任务不会终止。",
      "退出全部设备",
      {
        confirmButtonText: "全部退出",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    await api("/api/auth/devices/revoke-all", { method: "POST" });
    emit("logout");
  } catch {}
}
function openSecuritySettings() {
  emit("update:modelValue", false);
  void router.push("/settings/security");
}
function openCreateUser() {
  userForm.value = {
    id: "",
    username: "",
    password: "",
    role: "user",
    disabled: false,
  };
  userDialogOpen.value = true;
}
function openEditUser(user: User) {
  userForm.value = {
    id: user.id,
    username: user.username,
    password: "",
    role: user.role,
    disabled: user.disabled,
  };
  userDialogOpen.value = true;
}
async function saveUser() {
  const form = userForm.value;
  if (form.username.trim().length < 3)
    return ElMessage.warning("用户名至少 3 位");
  if (!form.id && form.password.length < policy.value.passwordMinLength)
    return ElMessage.warning(`密码至少 ${policy.value.passwordMinLength} 位`);
  savingUser.value = true;
  try {
    const user = await api<User>(
      form.id ? `/api/admin/users/${form.id}` : "/api/admin/users",
      {
        method: form.id ? "PATCH" : "POST",
        body: json(
          form.id
            ? {
                username: form.username.trim(),
                role: form.role,
                disabled: form.disabled,
              }
            : {
                username: form.username.trim(),
                password: form.password,
                role: form.role,
              },
        ),
      },
    );
    const index = users.value.findIndex((item) => item.id === user.id);
    if (index >= 0) users.value.splice(index, 1, user);
    else users.value.push(user);
    if (auth.user?.id === user.id) Object.assign(auth.user, user);
    userDialogOpen.value = false;
    ElMessage.success(form.id ? "用户已更新" : "用户已创建");
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "保存失败");
  } finally {
    savingUser.value = false;
  }
}
function openUserPassword(user: User) {
  if (user.id === auth.user?.id) {
    tab.value = "devices";
    return ElMessage.info("当前账户请在账户与设备中修改登录密码");
  }
  userPasswordForm.value = {
    userID: user.id,
    username: user.username,
    password: "",
  };
  userPasswordDialogOpen.value = true;
}
async function saveUserPassword() {
  const form = userPasswordForm.value;
  if (form.password.length < policy.value.passwordMinLength)
    return ElMessage.warning(`密码至少 ${policy.value.passwordMinLength} 位`);
  savingUserPassword.value = true;
  try {
    const user = await api<User>(`/api/admin/users/${form.userID}`, {
      method: "PATCH",
      body: json({ password: form.password }),
    });
    const index = users.value.findIndex((item) => item.id === user.id);
    if (index >= 0) users.value.splice(index, 1, user);
    userPasswordDialogOpen.value = false;
    ElMessage.success("密码已修改，该用户的已有登录已撤销");
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "密码修改失败");
  } finally {
    savingUserPassword.value = false;
  }
}
async function deleteUser(user: User) {
  try {
    await ElMessageBox.confirm(
      `删除用户“${user.username}”及其主机、凭据和工作区？存在活动远程会话时系统会拒绝删除。`,
      "删除用户",
      { confirmButtonText: "删除", cancelButtonText: "取消", type: "warning" },
    );
    await api(`/api/admin/users/${user.id}`, { method: "DELETE" });
    users.value = users.value.filter((item) => item.id !== user.id);
    ElMessage.success("用户已删除");
  } catch (e: any) {
    if (e !== "cancel" && e !== "close")
      ElMessage.error(e instanceof Error ? e.message : "删除失败");
  }
}
async function savePolicy() {
  try {
    policy.value = await api<SecurityPolicy>("/api/admin/security-policy", {
      method: "PUT",
      body: json(policy.value),
    });
    ElMessage.success("登录安全策略已保存");
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "保存失败");
  }
}
async function saveAIModel(showNotice = true) {
  const form = aiModelForm.value;
  if (Boolean(form.baseURL.trim()) !== Boolean(form.model.trim())) {
    ElMessage.warning("API 地址和模型名称必须同时填写");
    return false;
  }
  savingAIModel.value = true;
  try {
    aiModel.value = await api<AIModelConfig>("/api/admin/ai-model", {
      method: "PUT",
      body: json(form),
    });
    aiModelForm.value = {
      baseURL: aiModel.value.baseURL,
      model: aiModel.value.model,
      apiKey: "",
      clearAPIKey: false,
    };
    if (showNotice) ElMessage.success("AI 模型配置已保存并生效");
    return true;
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "模型配置保存失败");
    return false;
  } finally {
    savingAIModel.value = false;
  }
}
async function testAIModel() {
  if (!(await saveAIModel(false))) return;
  testingAIModel.value = true;
  try {
    const result = await api<{ model: string; message: string }>(
      "/api/admin/ai-model/test",
      { method: "POST" },
    );
    ElMessage.success(`连接正常 · ${result.model}`);
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "模型连接测试失败");
  } finally {
    testingAIModel.value = false;
  }
}
async function saveTailscale() {
  const form = tailscaleForm.value;
  if (!form.hostname.trim()) {
    ElMessage.warning("请输入 Tailscale 节点名称");
    return;
  }
  savingTailscale.value = true;
  try {
    tailscale.value = await api<TailscaleConfig>("/api/admin/tailscale", {
      method: "PUT",
      body: json(form),
    });
    tailscaleForm.value = {
      enabled: tailscale.value.enabled,
      hostname: tailscale.value.hostname,
      controlURL: tailscale.value.controlURL,
      authKey: "",
      clearAuthKey: false,
    };
    ElMessage.success("Tailscale 设置已保存");
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "Tailscale 设置保存失败");
  } finally {
    savingTailscale.value = false;
  }
}
async function backup() {
  if (backupKey.value.length < 12)
    return ElMessage.warning("备份密钥至少需要 12 个字符");
  backupBusy.value = "backup";
  try {
    const result = await api<{ file: string; sha256: string; verified: boolean }>(
      "/api/admin/backup",
      { method: "POST", body: json({ key: backupKey.value }) },
    );
    ElMessage.success(
      `加密备份已创建并通过完整性验证：${result.file} · ${result.sha256.slice(0, 12)}…`,
    );
    backups.value = await api<any[]>("/api/admin/backups");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "备份创建失败");
  } finally {
    backupBusy.value = undefined;
  }
}
function downloadBackup(file: string) {
	const link = document.createElement("a");
	link.href = `/api/admin/backups/${encodeURIComponent(file)}/download`;
	link.click();
}
function chooseBackupFile() {
  if (backupKey.value.length < 12) {
    ElMessage.warning("请先输入创建该备份时使用的密钥");
    return;
  }
  backupFileInput.value?.click();
}
async function uploadBackup(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;
  const form = new FormData();
  form.append("file", file);
  form.append("key", backupKey.value);
  backupBusy.value = "restore";
  try {
    await api<{ file: string; sha256: string }>("/api/admin/backups/upload", {
      method: "POST",
      body: form,
    });
    backups.value = await api<any[]>("/api/admin/backups");
    ElMessage.success("备份已上传并通过密钥与完整性校验，请在列表中点击恢复");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "备份上传失败");
  } finally {
    input.value = "";
    backupBusy.value = undefined;
  }
}
async function restoreBackup(file: string) {
	if (backupKey.value.length < 12)
		return ElMessage.warning("请输入创建该备份时使用的密钥");
	try {
		await ElMessageBox.confirm(
			`恢复后当前数据库会被替换，现有登录会话会失效。系统会先自动创建恢复前备份。确认恢复“${file}”？`,
			"恢复数据库",
			{ type: "warning", confirmButtonText: "确认恢复", cancelButtonText: "取消" },
		);
		backupBusy.value = "restore";
		await api(`/api/admin/backups/${encodeURIComponent(file)}/restore`, {
			method: "POST",
			body: json({ key: backupKey.value }),
		});
		ElMessage.success("恢复完成，请重新登录");
		emit("logout");
	} catch (error: any) {
		if (error !== "cancel" && error !== "close")
			ElMessage.error(error instanceof Error ? error.message : "恢复失败");
	} finally {
		backupBusy.value = undefined;
	}
}
async function changePassword() {
  const p = passwordForm.value;
  if (p.newPassword !== p.confirmPassword)
    return ElMessage.warning("两次输入的新密码不一致");
  try {
    await api("/api/auth/password", {
      method: "POST",
      body: json({
        currentPassword: p.currentPassword,
        newPassword: p.newPassword,
      }),
    });
    passwordForm.value = {
      currentPassword: "",
      newPassword: "",
      confirmPassword: "",
    };
    if (auth.user) auth.user.forcePasswordChange = false;
    ElMessage.success("密码已修改");
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "修改失败");
  }
}
function forwardBatch(text: string, sessionIDs: string[]) {
  emit("batchExecute", text, sessionIDs);
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="settings-dialog"
    title="设置"
    width="min(900px, calc(100vw - 28px))"
    append-to-body
    @open="
      tab = 'terminal';
      load();
    "
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="settings-layout">
      <nav class="settings-menu" aria-label="设置分类">
        <button
          :class="{ active: tab === 'terminal' }"
          @click="tab = 'terminal'"
        >
          <MonitorCog :size="17" /><span>外观与终端</span>
        </button>
        <button :class="{ active: tab === 'devices' }" @click="tab = 'devices'">
          <Shield :size="17" /><span>账户与设备</span>
        </button>
        <button @click="openSecuritySettings">
          <ShieldCheck :size="17" /><span>安全设置</span>
        </button>
        <button
          :class="{ active: tab === 'credentials' }"
          @click="tab = 'credentials'"
        >
          <KeyRound :size="17" /><span>凭据管理</span>
        </button>
        <button
          v-if="auth.user?.role === 'admin'"
          :class="{ active: tab === 'users' }"
          @click="tab = 'users'"
        >
          <Users :size="17" /><span>用户管理</span>
        </button>
        <button
          v-if="auth.user?.role === 'admin'"
          :class="{ active: tab === 'admin' }"
          @click="tab = 'admin'"
        >
          <ServerCog :size="17" /><span>系统管理</span>
        </button>
        <button
          v-if="auth.user?.role === 'admin'"
          :class="{ active: tab === 'network' }"
          @click="tab = 'network'"
        >
          <Network :size="17" /><span>组网</span>
        </button>
        <button
          v-if="auth.user?.role === 'admin'"
          :class="{ active: tab === 'ai' }"
          @click="tab = 'ai'"
        >
          <Bot :size="17" /><span>AI 模型</span>
        </button>
        <span class="settings-menu-divider">工具</span>
        <button
          :class="{ active: tab === 'snippets' }"
          @click="tab = 'snippets'"
        >
          <Code2 :size="17" /><span>命令片段</span>
        </button>
        <button
          :class="{ active: tab === 'forwards' }"
          @click="tab = 'forwards'"
        >
          <Network :size="17" /><span>端口转发</span>
        </button>
        <button :class="{ active: tab === 'data' }" @click="tab = 'data'">
          <Database :size="17" /><span>数据</span>
        </button>
        <button :class="{ active: tab === 'tasks' }" @click="tab = 'tasks'">
          <ListTodo :size="17" /><span>任务队列</span>
        </button>
        <button :class="{ active: tab === 'updates' }" @click="tab = 'updates'">
          <Download :size="17" /><span>版本更新</span>
          <i v-if="updateCheck.updateAvailable" class="update-dot" aria-label="有新版本" />
        </button>
      </nav>
      <section
        class="settings-content"
        :class="{
          'has-detail-tabs': [
            'terminal',
            'devices',
            'users',
            'admin',
            'network',
            'ai',
          ].includes(tab),
        }"
      >
        <CredentialsPanel
          v-if="tab === 'credentials'"
          :credentials="credentials"
          @saved="emit('credentialSaved', $event)"
          @deleted="emit('credentialDeleted', $event)"
        />
        <TaskPanel v-if="tab === 'tasks'" :sessions="sessions" />
        <div v-if="tab === 'updates'" class="update-panel">
          <div class="settings-section">
            <div class="security-heading">
              <div>
                <h3>版本更新</h3>
                <p>检查 GitHub 上的最新稳定 Release。</p>
              </div>
              <span
                class="security-state"
                :class="{
                  enabled: updateCheck.versionKnown && !updateCheck.updateAvailable,
                  warning: updateCheck.updateAvailable,
                }"
              >
                {{
                  updateCheckBusy
                    ? "检查中"
                    : updateCheckError
                      ? "检查失败"
                      : !updateCheck.versionKnown
                        ? "开发版本"
                        : updateCheck.updateAvailable
                          ? "发现新版本"
                          : "已是最新"
                }}
              </span>
            </div>
            <div class="update-version-grid">
              <div>
                <small>当前版本</small>
                <strong>{{ updateCheck.currentVersion }}</strong>
              </div>
              <div>
                <small>最新版本</small>
                <strong>{{ updateCheck.latestVersion || "--" }}</strong>
              </div>
            </div>
            <p v-if="updateCheckError" class="setting-note update-error">
              {{ updateCheckError }}
            </p>
            <p v-else-if="updateCheck.releaseName" class="setting-note">
              {{ updateCheck.releaseName }}
              <span v-if="updateCheck.publishedAt">
                · {{ new Date(updateCheck.publishedAt).toLocaleDateString() }}
              </span>
            </p>
            <div class="row-actions update-actions">
              <el-button
                :icon="RefreshCw"
                :loading="updateCheckBusy"
                @click="checkForUpdate(true)"
              >检查更新</el-button>
              <a
                v-if="updateCheck.releaseURL"
                class="el-button el-button--primary update-release-link"
                :href="updateCheck.releaseURL"
                target="_blank"
                rel="noreferrer"
              >查看 Release</a>
            </div>
          </div>
        </div>
        <el-tabs
          v-if="
            ['terminal', 'devices', 'users', 'admin', 'network', 'ai'].includes(tab)
          "
          v-model="tab"
          class="settings-tabs settings-detail-tabs"
        >
          <el-tab-pane label="终端" name="terminal">
            <div class="settings-section">
              <h3>界面外观</h3>
              <div
                class="interface-theme-grid"
                role="radiogroup"
                aria-label="界面主题"
              >
                <button
                  v-for="preset in interfaceThemePresets"
                  :key="preset.id"
                  class="interface-theme-card"
                  :class="{ active: preferences.theme === preset.id }"
                  :aria-checked="preferences.theme === preset.id"
                  role="radio"
                  @click="selectInterfaceTheme(preset.id)"
                >
                  <span
                    class="interface-theme-preview"
                    :style="{
                      backgroundColor: preset.colors.bg,
                      borderColor: preset.colors.lineStrong,
                    }"
                  >
                    <i :style="{ backgroundColor: preset.colors.sidebar }"></i>
                    <b :style="{ backgroundColor: preset.colors.tabbar }"></b>
                    <em
                      :style="{ backgroundColor: preset.colors.surface2 }"
                    ></em>
                    <small :style="{ backgroundColor: preset.accent }"></small>
                  </span>
                  <span>
                    <strong>{{ preset.name }}</strong>
                    <small>{{ preset.description }}</small>
                  </span>
                  <Check v-if="preferences.theme === preset.id" :size="15" />
                </button>
              </div>
              <div class="setting-row palette-setting">
                <span>强调色</span>
                <div
                  class="accent-palette"
                  role="radiogroup"
                  aria-label="强调色"
                >
                  <el-tooltip
                    v-for="preset in accentPresets"
                    :key="preset.id"
                    :content="preset.name"
                  >
                    <button
                      class="accent-swatch"
                      :class="{
                        active: preferences.accentColor === preset.value,
                      }"
                      :style="{ backgroundColor: preset.value }"
                      :aria-label="preset.name"
                      :aria-checked="preferences.accentColor === preset.value"
                      role="radio"
                      @click="preferences.accentColor = preset.value"
                    >
                      <Check
                        v-if="preferences.accentColor === preset.value"
                        :size="15"
                      />
                    </button>
                  </el-tooltip>
                </div>
              </div>
            </div>
            <div class="settings-section">
              <h3>终端外观</h3>
              <div
                class="terminal-theme-grid"
                role="radiogroup"
                aria-label="终端配色"
              >
                <button
                  v-for="preset in terminalThemePresets"
                  :key="preset.id"
                  class="terminal-theme-card"
                  :class="{ active: preferences.terminalTheme === preset.id }"
                  :style="{
                    backgroundColor: preset.background,
                    color: preset.foreground,
                  }"
                  :aria-checked="preferences.terminalTheme === preset.id"
                  role="radio"
                  @click="selectTerminalTheme(preset.id)"
                >
                  <span class="terminal-theme-preview">
                    <i :style="{ backgroundColor: preset.colors[1] }"></i>
                    <i :style="{ backgroundColor: preset.colors[2] }"></i>
                    <i :style="{ backgroundColor: preset.colors[3] }"></i>
                    <i :style="{ backgroundColor: preset.colors[4] }"></i>
                  </span>
                  <span>{{ preset.name }}</span>
                  <Check
                    v-if="preferences.terminalTheme === preset.id"
                    :size="14"
                  />
                </button>
              </div>
              <div class="setting-row">
                <span>字号</span
                ><el-slider
                  v-model="preferences.fontSize"
                  :min="10"
                  :max="24"
                  show-input
                />
              </div>
              <div class="setting-row">
                <span>行高</span
                ><el-slider
                  v-model="preferences.lineHeight"
                  :min="1"
                  :max="2"
                  :step="0.05"
                  show-input
                />
              </div>
              <div class="setting-row">
                <span>字重</span
                ><el-slider
                  v-model="preferences.fontWeight"
                  :min="300"
                  :max="700"
                  :step="100"
                  show-input
                />
              </div>
              <div class="setting-row">
                <span>字间距</span
                ><el-slider
                  v-model="preferences.letterSpacing"
                  :min="0"
                  :max="3"
                  :step="0.25"
                  show-input
                />
              </div>
              <div class="setting-row">
                <span>光标</span
                ><el-select v-model="preferences.cursorStyle"
                  ><el-option label="方块" value="block" /><el-option
                    label="竖线"
                    value="bar" /><el-option label="下划线" value="underline"
                /></el-select>
              </div>
              <div class="setting-row">
                <span>光标闪烁</span
                ><el-switch v-model="preferences.cursorBlink" />
              </div>
              <div class="setting-row">
                <span>视觉响铃</span
                ><el-switch v-model="preferences.visualBell" />
              </div>
              <div class="setting-row">
                <span>声音响铃</span
                ><el-switch v-model="preferences.soundBell" />
              </div>
              <div class="setting-row">
                <span>多行粘贴保护</span
                ><el-switch v-model="preferences.pasteGuard" />
              </div>
              <div class="row-actions settings-actions">
                <el-button @click="resetPreferences">恢复默认</el-button
                ><el-button type="primary" @click="savePreferences"
                  >保存设置</el-button
                >
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane label="登录设备" name="devices">
            <div class="settings-section">
              <h3>工作区锁屏</h3>
              <div class="setting-row">
                <span>启用锁屏</span
                ><el-switch
                  :model-value="preferences.lockEnabled && lockPINConfigured"
                  @change="toggleLockFeature"
                />
              </div>
              <template v-if="preferences.lockEnabled && lockPINConfigured">
                <div class="setting-row">
                  <span>空闲自动锁屏</span
                  ><el-select v-model="preferences.autoLockMinutes">
                  <el-option label="不按空闲锁屏" :value="0" />
                  <el-option label="1 分钟" :value="1" />
                  <el-option label="5 分钟" :value="5" />
                  <el-option label="15 分钟" :value="15" />
                  <el-option label="30 分钟" :value="30" />
                  <el-option label="1 小时" :value="60" />
                  </el-select>
                </div>
                <div class="setting-row">
                  <span>Win/Meta + L</span
                  ><el-switch v-model="preferences.lockOnShortcut" />
                </div>
                <div class="setting-row">
                  <span>锁屏 PIN</span
                  ><el-button text @click="openLockPINSetup">修改 PIN</el-button>
                </div>
              </template>
              <div v-if="lockPINSetupOpen" class="lock-pin-setup">
                <el-input
                  :model-value="lockPINForm.pin"
                  maxlength="6"
                  inputmode="numeric"
                  type="password"
                  show-password
                  autocomplete="new-password"
                  placeholder="设置 6 位数字 PIN"
                  @update:model-value="lockPINForm.pin = normalizePIN($event)"
                />
                <el-input
                  :model-value="lockPINForm.confirm"
                  maxlength="6"
                  inputmode="numeric"
                  type="password"
                  show-password
                  autocomplete="new-password"
                  placeholder="再次输入 PIN"
                  @update:model-value="lockPINForm.confirm = normalizePIN($event)"
                />
                <div class="row-actions">
                  <el-button @click="lockPINSetupOpen = false">取消</el-button>
                  <el-button type="primary" :loading="savingLockPIN" @click="saveLockPIN">
                    保存 PIN
                  </el-button>
                </div>
              </div>
              <p class="setting-note">
                仅在空闲超时、手动锁屏或捕获到 Win/Meta + L 时锁定；切换页面不会锁定。
              </p>
            </div>
            <div class="settings-section">
              <h3>修改登录密码</h3>
              <div class="inline-form password-form">
                <el-input
                  v-model="passwordForm.currentPassword"
                  type="password"
                  show-password
                  placeholder="当前密码"
                /><el-input
                  v-model="passwordForm.newPassword"
                  type="password"
                  show-password
                  :placeholder="`新密码，至少 ${policy.passwordMinLength} 位`"
                /><el-input
                  v-model="passwordForm.confirmPassword"
                  type="password"
                  show-password
                  placeholder="确认新密码"
                /><el-button @click="changePassword">修改</el-button>
              </div>
            </div>
            <div class="settings-section">
              <h3>登录设备</h3>
              <div class="list-stack">
                <div v-for="d in devices" :key="d.id" class="data-row">
                  <div>
                    <strong
                      >{{ d.userAgent || "未知设备" }}
                      <span v-if="d.current" class="current-device"
                        >当前设备</span
                      ></strong
                    ><small
                      >{{ d.ip }} · 最后活动
                      {{ new Date(d.lastSeenAt).toLocaleString() }} · 到期
                      {{ new Date(d.expiresAt).toLocaleString() }}</small
                    >
                  </div>
                  <el-button :icon="Trash2" text @click="revoke(d.id)"
                    >撤销</el-button
                  >
                </div>
              </div>
              <el-button :icon="LogOut" type="danger" plain @click="revokeAll"
                >退出全部设备</el-button
              >
            </div>
          </el-tab-pane>
          <el-tab-pane
            v-if="auth.user?.role === 'admin'"
            label="用户"
            name="users"
          >
            <div class="tool-heading user-management-heading">
              <div>
                <strong>用户列表</strong>
                <small>{{ users.length }} 个账户</small>
              </div>
              <el-button :icon="UserPlus" type="primary" @click="openCreateUser"
                >添加用户</el-button
              >
            </div>
            <div v-if="users.length" class="list-stack user-list">
              <div v-for="u in users" :key="u.id" class="user-row">
                <div class="user-identity">
                  <span class="user-initial">{{
                    u.username.slice(0, 1).toUpperCase()
                  }}</span>
                  <div>
                    <strong>{{ u.username }}</strong>
                    <small>
                      {{ u.role === "admin" ? "管理员" : "普通用户" }}
                      · {{ u.disabled ? "已禁用" : "正常" }}
                      <template v-if="u.forcePasswordChange">
                        · 登录后需改密</template
                      >
                    </small>
                  </div>
                </div>
                <span class="user-state" :class="{ disabled: u.disabled }">{{
                  u.disabled ? "已禁用" : "已启用"
                }}</span>
                <div class="row-actions user-row-actions">
                  <el-button :icon="Pencil" text @click="openEditUser(u)"
                    >编辑</el-button
                  >
                  <el-button
                    :icon="LockKeyhole"
                    text
                    @click="openUserPassword(u)"
                    >修改密码</el-button
                  >
                  <el-button
                    v-if="u.id !== auth.user?.id"
                    :icon="Trash2"
                    text
                    type="danger"
                    @click="deleteUser(u)"
                    >删除</el-button
                  >
                </div>
              </div>
            </div>
            <div v-else class="empty-small">
              <Users :size="28" /><span>暂无用户</span>
            </div>
          </el-tab-pane>
          <el-tab-pane
            v-if="auth.user?.role === 'admin'"
            label="管理"
            name="admin"
          >
            <div class="stats-grid admin-stats">
              <div>
                <strong>{{ stats.users || 0 }}</strong
                ><span>用户 · {{ stats.activeUsers || 0 }} 活跃</span>
              </div>
              <div>
                <strong>{{ stats.hosts || 0 }}</strong
                ><span>主机</span>
              </div>
              <div>
                <strong>{{ stats.sessions || 0 }}</strong
                ><span>活动 SSH 会话</span>
              </div>
              <div>
                <strong>{{ stats.websockets || 0 }}</strong
                ><span>WebSocket</span>
              </div>
              <div>
                <strong>{{ formatBytes(stats.databaseBytes || 0) }}</strong
                ><span>数据库</span>
              </div>
              <div>
                <strong>{{ stats.backups || 0 }}</strong
                ><span>备份</span>
              </div>
            </div>
            <div class="system-meta">
              <span>运行 {{ formatDuration(stats.uptimeSeconds || 0) }}</span
              ><span>{{ stats.goVersion }}</span
              ><span>部署 {{ stats.deploymentID }}</span
              ><span v-if="stats.latestBackupAt"
                >最近备份
                {{ new Date(stats.latestBackupAt).toLocaleString() }}</span
              >
            </div>
            <div class="settings-section">
              <h3>登录安全策略</h3>
              <div class="setting-row">
                <span>密码最小长度</span
                ><el-input-number
                  v-model="policy.passwordMinLength"
                  :min="10"
                  :max="128"
                />
              </div>
              <div class="setting-row">
                <span>失败锁定阈值</span
                ><el-input-number
                  v-model="policy.loginFailureThreshold"
                  :min="3"
                  :max="20"
                />
              </div>
              <div class="setting-row">
                <span>锁定分钟数</span
                ><el-input-number
                  v-model="policy.lockMinutes"
                  :min="1"
                  :max="1440"
                />
              </div>
              <div class="setting-row">
                <span>保持登录天数</span
                ><el-input-number
                  v-model="policy.rememberDays"
                  :min="1"
                  :max="90"
                />
              </div>
              <div class="setting-row">
                <span>新用户强制改密</span
                ><el-switch v-model="policy.forceChangeOnCreate" />
              </div>
              <el-button :icon="ShieldCheck" type="primary" @click="savePolicy"
                >保存安全策略</el-button
              >
            </div>
            <div class="settings-section backup-section">
              <div class="security-heading">
                <div>
                  <h3>加密备份与恢复</h3>
                  <p>备份包含应用数据库和主密钥，使用输入的密钥整体加密。密钥不会保存到服务器。</p>
                </div>
                <span class="security-state">AES-GCM</span>
              </div>
              <div class="backup-key-form">
                <el-input
                  v-model="backupKey"
                  type="password"
                  show-password
                  autocomplete="new-password"
                  placeholder="备份密钥，至少 12 个字符"
                />
                <el-button
                  :icon="HardDriveDownload"
                  type="primary"
                  :loading="backupBusy === 'backup'"
                  :disabled="backupBusy === 'restore'"
                  @click="backup"
                >创建加密备份</el-button>
                <input
                  ref="backupFileInput"
                  class="visually-hidden"
                  type="file"
                  accept=".enc,application/octet-stream"
                  @change="uploadBackup"
                />
                <el-button
                  :icon="Upload"
                  :loading="backupBusy === 'restore'"
                  :disabled="backupBusy === 'backup'"
                  @click="chooseBackupFile"
                >上传并校验备份</el-button>
              </div>
              <p class="setting-note">
                恢复时请输入创建该备份时使用的密钥。忘记密钥无法恢复备份；建议通过 HTTPS 使用此功能。
              </p>
              <div v-for="item in backups" :key="item.file" class="data-row backup-row">
                <div>
                  <strong>{{ item.file }}</strong>
                  <small>{{ formatBytes(item.size) }} · {{ new Date(item.createdAt).toLocaleString() }} · {{ item.sha256?.slice(0, 12) }}</small>
                </div>
                <div class="row-actions">
                  <el-button text :icon="Download" @click="downloadBackup(item.file)">下载</el-button>
                  <el-button
                    text
                    type="warning"
                    :icon="RefreshCw"
                    :loading="backupBusy === 'restore'"
                    :disabled="backupBusy === 'backup'"
                    @click="restoreBackup(item.file)"
                  >恢复</el-button>
                </div>
              </div>
              <div v-if="!backups.length" class="empty-small backup-empty">
                <Database :size="24" /><span>暂无加密备份</span>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane
            v-if="auth.user?.role === 'admin'"
            label="组网"
            name="network"
          >
            <div class="settings-section">
              <div class="security-heading">
                <div>
                  <h3>内嵌 Tailscale</h3>
                  <p>服务默认关闭，仅在开启并保存后启动 Go 网络服务。</p>
                </div>
                <span class="security-state" :class="{ enabled: tailscale.status.state === 'Running' }">
                  {{ tailscale.status.state === "Running" ? "运行中" : tailscale.enabled ? tailscale.status.state : "已关闭" }}
                </span>
              </div>
              <div class="setting-row">
                <span>服务状态</span>
                <el-switch v-model="tailscaleForm.enabled" />
              </div>
              <el-form label-position="top" class="ai-model-form">
                <el-form-item label="节点名称">
                  <el-input v-model="tailscaleForm.hostname" maxlength="63" />
                </el-form-item>
                <el-form-item label="控制面地址">
                  <el-input v-model="tailscaleForm.controlURL" placeholder="官方 Tailscale 留空，Headscale 填写地址" clearable />
                </el-form-item>
                <el-form-item label="Auth Key">
                  <el-input v-model="tailscaleForm.authKey" type="password" show-password autocomplete="new-password" :placeholder="tailscale.authKeyConfigured ? '已配置，留空保持不变' : '输入 Auth Key'" :disabled="tailscaleForm.clearAuthKey" />
                </el-form-item>
                <el-checkbox v-if="tailscale.authKeyConfigured" v-model="tailscaleForm.clearAuthKey">清除已保存的 Auth Key</el-checkbox>
              </el-form>
              <div v-if="tailscale.status.ips?.length || tailscale.status.authUrl" class="setting-note">
                <span v-if="tailscale.status.ips?.length">IP: {{ tailscale.status.ips.join(", ") }}</span>
                <a v-if="tailscale.status.authUrl" :href="tailscale.status.authUrl" target="_blank" rel="noreferrer">打开登录链接</a>
              </div>
              <div class="row-actions settings-actions">
                <el-button :icon="Save" type="primary" :loading="savingTailscale" @click="saveTailscale">保存并应用</el-button>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane
            v-if="auth.user?.role === 'admin'"
            label="AI 模型"
            name="ai"
          >
            <div class="settings-section ai-model-section">
              <div class="security-heading">
                <div>
                  <h3>Agent 模型服务</h3>
                  <p>兼容 Chat Completions 工具调用的模型接口。</p>
                </div>
                <span class="security-state" :class="{ enabled: aiModel.configured }">
                  {{ aiModel.configured ? "已启用" : "未配置" }}
                </span>
              </div>
              <el-form label-position="top" class="ai-model-form">
                <el-form-item label="API 地址">
                  <el-input
                    v-model="aiModelForm.baseURL"
                    placeholder="https://api.example.com/v1"
                    clearable
                  />
                </el-form-item>
                <el-form-item label="模型名称">
                  <el-input
                    v-model="aiModelForm.model"
                    placeholder="输入模型标识"
                    clearable
                  />
                </el-form-item>
                <el-form-item label="API Key">
                  <el-input
                    v-model="aiModelForm.apiKey"
                    type="password"
                    show-password
                    autocomplete="new-password"
                    :disabled="aiModelForm.clearAPIKey"
                    :placeholder="aiModel.apiKeyConfigured ? '已配置，留空保持不变' : '输入 API Key（可选）'"
                  />
                </el-form-item>
                <el-checkbox
                  v-if="aiModel.apiKeyConfigured"
                  v-model="aiModelForm.clearAPIKey"
                >清除已保存的 API Key</el-checkbox>
              </el-form>
              <div class="row-actions ai-model-actions">
                <el-button
                  :icon="FlaskConical"
                  :loading="testingAIModel"
                  :disabled="savingAIModel"
                  @click="testAIModel"
                >测试连接</el-button>
                <el-button
                  type="primary"
                  :icon="Save"
                  :loading="savingAIModel"
                  :disabled="testingAIModel"
                  @click="saveAIModel()"
                >保存配置</el-button>
              </div>
            </div>
          </el-tab-pane>
        </el-tabs>
        <ToolsDrawer
          v-if="['snippets', 'forwards', 'data'].includes(tab)"
          :section="tab"
          :hosts="hosts"
          :sessions="sessions"
          @insert="emit('insert', $event)"
          @execute="emit('execute', $event)"
          @batch-execute="forwardBatch"
        />
      </section>
    </div>
  </el-dialog>
  <el-dialog
    v-model="userDialogOpen"
    class="user-dialog"
    :title="userForm.id ? '编辑用户' : '添加用户'"
    width="min(520px, calc(100vw - 28px))"
    append-to-body
  >
    <el-form label-position="top">
      <el-form-item label="用户名">
        <el-input v-model="userForm.username" autocomplete="off" />
      </el-form-item>
      <el-form-item v-if="!userForm.id" label="初始密码">
        <el-input
          v-model="userForm.password"
          type="password"
          show-password
          autocomplete="new-password"
          :placeholder="`至少 ${policy.passwordMinLength} 位`"
        />
      </el-form-item>
      <el-form-item label="角色">
        <el-segmented
          v-model="userForm.role"
          :disabled="userForm.id === auth.user?.id"
          :options="[
            { label: '普通用户', value: 'user' },
            { label: '管理员', value: 'admin' },
          ]"
        />
      </el-form-item>
      <el-form-item v-if="userForm.id" label="账户状态">
        <div class="user-status-control">
          <el-switch
            v-model="userForm.disabled"
            :disabled="userForm.id === auth.user?.id"
            active-text="禁用"
            inactive-text="启用"
          />
          <small>禁用后该用户无法继续登录。</small>
        </div>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="userDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="savingUser" @click="saveUser"
        >保存</el-button
      >
    </template>
  </el-dialog>
  <el-dialog
    v-model="userPasswordDialogOpen"
    class="user-password-dialog"
    title="修改用户密码"
    width="min(480px, calc(100vw - 28px))"
    append-to-body
  >
    <el-form label-position="top">
      <el-form-item label="用户">
        <el-input :model-value="userPasswordForm.username" disabled />
      </el-form-item>
      <el-form-item label="新密码">
        <el-input
          v-model="userPasswordForm.password"
          type="password"
          show-password
          autocomplete="new-password"
          :placeholder="`至少 ${policy.passwordMinLength} 位`"
          @keyup.enter="saveUserPassword"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="userPasswordDialogOpen = false">取消</el-button>
      <el-button
        type="primary"
        :icon="LockKeyhole"
        :loading="savingUserPassword"
        @click="saveUserPassword"
        >修改密码</el-button
      >
    </template>
  </el-dialog>
</template>
