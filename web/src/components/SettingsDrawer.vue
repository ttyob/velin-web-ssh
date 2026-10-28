<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
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
  X,
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
import SecurityPanel from "./SecurityPanel.vue";
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
const tabMetaMap: Record<string, { title: string; desc: string }> = {
  terminal: { title: "外观与终端", desc: "界面主题、强调色与终端显示效果" },
  security: { title: "安全设置", desc: "管理登录保护和双因素认证" },
  devices: { title: "账户与设备", desc: "管理登录设备与账户会话" },
  credentials: { title: "凭据管理", desc: "集中保存 SSH 密码与私钥，供多台主机复用" },
  users: { title: "用户管理", desc: "添加、停用用户并分配角色" },
  admin: { title: "系统管理", desc: "查看运行状态，配置登录安全策略" },
  network: { title: "组网", desc: "通过 Tailscale 接入内网主机" },
  ai: { title: "AI 模型", desc: "配置终端助手使用的模型与密钥" },
  snippets: { title: "命令片段", desc: "保存常用命令，一键发送到终端" },
  forwards: { title: "端口转发", desc: "管理本地与远程端口转发" },
  data: { title: "数据", desc: "加密备份、导出与导入" },
  tasks: { title: "任务队列", desc: "查看后台任务与执行进度" },
  updates: { title: "版本更新", desc: "检查并获取新版本" },
};
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
  proxyURL: "",
  apiKeyConfigured: false,
  configured: false,
});
const aiModelForm = ref({
  baseURL: "",
  model: "",
  proxyURL: "",
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
const tabMeta = computed(() => tabMetaMap[tab.value] || tabMetaMap.terminal);
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
      proxyURL: aiModel.value.proxyURL || "",
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
      proxyURL: aiModel.value.proxyURL || "",
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
    class="sp"
    title="设置"
    width="min(1040px, calc(100vw - 32px))"
    align-center
    :show-close="false"
    append-to-body
    @open="
      tab = 'terminal';
      load();
    "
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="sp-shell">

      <button
        class="sp-close"
        type="button"
        title="关闭 (Esc)"
        aria-label="关闭设置"
        @click="emit('update:modelValue', false)"
      >
        <X :size="18" />
      </button>
      <div class="sp-body">
        <nav class="sp-nav" aria-label="设置分类">
          <h2 class="sp-nav-title">设置</h2>
          <div class="sp-nav-scroll">
            <span class="sp-nav-group">个人</span>
            <button :class="{ active: tab === 'terminal' }" @click="tab = 'terminal'">
              <MonitorCog :size="15" /><span>外观与终端</span>
            </button>
            <button :class="{ active: tab === 'devices' }" @click="tab = 'devices'">
              <Shield :size="15" /><span>账户与设备</span>
            </button>
            <button :class="{ active: tab === 'security' }" @click="tab = 'security'">
              <ShieldCheck :size="15" /><span>安全设置</span>
            </button>
            <button :class="{ active: tab === 'credentials' }" @click="tab = 'credentials'">
              <KeyRound :size="15" /><span>凭据管理</span>
            </button>
            <template v-if="auth.user?.role === 'admin'">
              <span class="sp-nav-group">管理</span>
              <button :class="{ active: tab === 'users' }" @click="tab = 'users'">
                <Users :size="15" /><span>用户管理</span>
              </button>
              <button :class="{ active: tab === 'admin' }" @click="tab = 'admin'">
                <ServerCog :size="15" /><span>系统管理</span>
              </button>
              <button :class="{ active: tab === 'network' }" @click="tab = 'network'">
                <Network :size="15" /><span>组网</span>
              </button>
              <button :class="{ active: tab === 'ai' }" @click="tab = 'ai'">
                <Bot :size="15" /><span>AI 模型</span>
              </button>
            </template>
            <span class="sp-nav-group">工具</span>
            <button :class="{ active: tab === 'snippets' }" @click="tab = 'snippets'">
              <Code2 :size="15" /><span>命令片段</span>
            </button>
            <button :class="{ active: tab === 'forwards' }" @click="tab = 'forwards'">
              <Network :size="15" /><span>端口转发</span>
            </button>
            <button :class="{ active: tab === 'data' }" @click="tab = 'data'">
              <Database :size="15" /><span>数据</span>
            </button>
            <button :class="{ active: tab === 'tasks' }" @click="tab = 'tasks'">
              <ListTodo :size="15" /><span>任务队列</span>
            </button>
            <button :class="{ active: tab === 'updates' }" @click="tab = 'updates'">
              <Download :size="15" /><span>版本更新</span>
              <i v-if="updateCheck.updateAvailable" class="update-dot" aria-label="有新版本" />
            </button>
          </div>
        </nav>

        <main class="sp-main">
          <div class="sp-col" :key="tab">
            <header class="sp-title">
              <h1>{{ tabMeta.title }}</h1>
              <p>{{ tabMeta.desc }}</p>
            </header>

            <!-- ============ 外观与终端 ============ -->
            <template v-if="tab === 'terminal'">
              <section class="sx">
                <h3 class="sx-h">界面主题</h3>
                <div class="theme-grid" role="radiogroup" aria-label="界面主题">
                  <button
                    v-for="preset in interfaceThemePresets"
                    :key="preset.id"
                    class="theme-card"
                    :class="{ active: preferences.theme === preset.id }"
                    :aria-checked="preferences.theme === preset.id"
                    role="radio"
                    @click="selectInterfaceTheme(preset.id)"
                  >
                    <span
                      class="theme-preview"
                      :style="{
                        backgroundColor: preset.colors.bg,
                        borderColor: preset.colors.lineStrong,
                      }"
                    >
                      <i :style="{ backgroundColor: preset.colors.sidebar }"></i>
                      <b :style="{ backgroundColor: preset.colors.tabbar }"></b>
                      <em :style="{ backgroundColor: preset.colors.surface2 }"></em>
                      <small :style="{ backgroundColor: preset.accent }"></small>
                    </span>
                    <span class="theme-name">
                      <strong>{{ preset.name }}</strong>
                      <small>{{ preset.description }}</small>
                    </span>
                    <Check v-if="preferences.theme === preset.id" :size="14" class="theme-check" />
                  </button>
                </div>
                <div class="sx-list">
                  <div class="sx-row">
                    <div class="sx-info"><strong>强调色</strong><small>按钮、选中态与焦点的颜色</small></div>
                    <div class="sx-ctl">
                      <div class="accent-palette" role="radiogroup" aria-label="强调色">
                        <el-tooltip v-for="preset in accentPresets" :key="preset.id" :content="preset.name">
                          <button
                            class="accent-swatch"
                            :class="{ active: preferences.accentColor === preset.value }"
                            :style="{ backgroundColor: preset.value }"
                            :aria-label="preset.name"
                            :aria-checked="preferences.accentColor === preset.value"
                            role="radio"
                            @click="preferences.accentColor = preset.value"
                          >
                            <Check v-if="preferences.accentColor === preset.value" :size="13" />
                          </button>
                        </el-tooltip>
                      </div>
                    </div>
                  </div>
                </div>
              </section>

              <section class="sx">
                <h3 class="sx-h">终端配色</h3>
                <div class="swatch-grid" role="radiogroup" aria-label="终端配色">
                  <button
                    v-for="preset in terminalThemePresets"
                    :key="preset.id"
                    class="swatch-card"
                    :class="{ active: preferences.terminalTheme === preset.id }"
                    :style="{ backgroundColor: preset.background, color: preset.foreground }"
                    :aria-checked="preferences.terminalTheme === preset.id"
                    role="radio"
                    @click="selectTerminalTheme(preset.id)"
                  >
                    <span class="swatch-dots">
                      <i :style="{ backgroundColor: preset.colors[1] }"></i>
                      <i :style="{ backgroundColor: preset.colors[2] }"></i>
                      <i :style="{ backgroundColor: preset.colors[3] }"></i>
                      <i :style="{ backgroundColor: preset.colors[4] }"></i>
                    </span>
                    <span class="swatch-name">{{ preset.name }}</span>
                    <Check v-if="preferences.terminalTheme === preset.id" :size="13" class="theme-check" />
                  </button>
                </div>
              </section>

              <section class="sx">
                <h3 class="sx-h">字体</h3>
                <div class="sx-list">
                  <div class="sx-row">
                    <div class="sx-info"><strong>字号</strong></div>
                    <div class="sx-ctl wide"><el-slider v-model="preferences.fontSize" :min="10" :max="24" show-input /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>行高</strong></div>
                    <div class="sx-ctl wide"><el-slider v-model="preferences.lineHeight" :min="1" :max="2" :step="0.05" show-input /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>字重</strong></div>
                    <div class="sx-ctl wide"><el-slider v-model="preferences.fontWeight" :min="300" :max="700" :step="100" show-input /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>字间距</strong></div>
                    <div class="sx-ctl wide"><el-slider v-model="preferences.letterSpacing" :min="0" :max="3" :step="0.25" show-input /></div>
                  </div>
                </div>
              </section>

              <section class="sx">
                <h3 class="sx-h">光标与提示</h3>
                <div class="sx-list">
                  <div class="sx-row">
                    <div class="sx-info"><strong>光标样式</strong></div>
                    <div class="sx-ctl">
                      <el-segmented
                        v-model="preferences.cursorStyle"
                        :options="[
                          { label: '方块', value: 'block' },
                          { label: '竖线', value: 'bar' },
                          { label: '下划线', value: 'underline' },
                        ]"
                      />
                    </div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>光标闪烁</strong></div>
                    <div class="sx-ctl"><el-switch v-model="preferences.cursorBlink" /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>视觉响铃</strong><small>收到终端响铃时闪烁画面</small></div>
                    <div class="sx-ctl"><el-switch v-model="preferences.visualBell" /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>声音响铃</strong><small>收到终端响铃时播放提示音</small></div>
                    <div class="sx-ctl"><el-switch v-model="preferences.soundBell" /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>多行粘贴保护</strong><small>粘贴多行内容前先确认，避免误执行</small></div>
                    <div class="sx-ctl"><el-switch v-model="preferences.pasteGuard" /></div>
                  </div>
                </div>
              </section>

              <div class="sp-savebar">
                <el-button @click="resetPreferences">恢复默认</el-button>
                <el-button type="primary" @click="savePreferences">保存设置</el-button>
              </div>
            </template>

            <!-- ============ 账户与设备 ============ -->
            <template v-else-if="tab === 'devices'">
              <section class="sx">
                <h3 class="sx-h">工作区锁屏</h3>
                <div class="sx-list">
                  <div class="sx-row">
                    <div class="sx-info"><strong>启用锁屏</strong><small>仅在空闲超时、手动锁屏或 Win/Meta + L 时锁定，切换页面不会锁定</small></div>
                    <div class="sx-ctl">
                      <el-switch
                        :model-value="preferences.lockEnabled && lockPINConfigured"
                        @change="toggleLockFeature"
                      />
                    </div>
                  </div>
                  <template v-if="preferences.lockEnabled && lockPINConfigured">
                    <div class="sx-row">
                      <div class="sx-info"><strong>空闲自动锁屏</strong></div>
                      <div class="sx-ctl">
                        <el-select v-model="preferences.autoLockMinutes">
                          <el-option label="不按空闲锁屏" :value="0" />
                          <el-option label="1 分钟" :value="1" />
                          <el-option label="5 分钟" :value="5" />
                          <el-option label="15 分钟" :value="15" />
                          <el-option label="30 分钟" :value="30" />
                          <el-option label="1 小时" :value="60" />
                        </el-select>
                      </div>
                    </div>
                    <div class="sx-row">
                      <div class="sx-info"><strong>Win/Meta + L</strong><small>捕获到该快捷键时锁定</small></div>
                      <div class="sx-ctl"><el-switch v-model="preferences.lockOnShortcut" /></div>
                    </div>
                    <div class="sx-row">
                      <div class="sx-info"><strong>锁屏 PIN</strong><small>6 位数字</small></div>
                      <div class="sx-ctl"><el-button @click="openLockPINSetup">修改 PIN</el-button></div>
                    </div>
                  </template>
                </div>
                <div v-if="lockPINSetupOpen" class="sx-form lock-pin-setup">
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
                  <div class="sx-actions">
                    <el-button @click="lockPINSetupOpen = false">取消</el-button>
                    <el-button type="primary" :loading="savingLockPIN" @click="saveLockPIN">保存 PIN</el-button>
                  </div>
                </div>
              </section>

              <section class="sx">
                <h3 class="sx-h">登录密码</h3>
                <div class="sx-form">
                  <el-input v-model="passwordForm.currentPassword" type="password" show-password placeholder="当前密码" />
                  <el-input
                    v-model="passwordForm.newPassword"
                    type="password"
                    show-password
                    :placeholder="`新密码，至少 ${policy.passwordMinLength} 位`"
                  />
                  <el-input v-model="passwordForm.confirmPassword" type="password" show-password placeholder="确认新密码" />
                  <div class="sx-actions"><el-button type="primary" @click="changePassword">修改密码</el-button></div>
                </div>
              </section>

              <section class="sx">
                <div class="sx-h-row">
                  <h3 class="sx-h">登录设备</h3>
                  <el-button :icon="LogOut" type="danger" plain size="small" @click="revokeAll">退出全部设备</el-button>
                </div>
                <div class="sx-list">
                  <div v-for="d in devices" :key="d.id" class="sx-row">
                    <div class="sx-info">
                      <strong>
                        {{ d.userAgent || "未知设备" }}
                        <span v-if="d.current" class="sx-badge">当前设备</span>
                      </strong>
                      <small>
                        {{ d.ip }} · 最后活动 {{ new Date(d.lastSeenAt).toLocaleString() }} · 到期 {{ new Date(d.expiresAt).toLocaleString() }}
                      </small>
                    </div>
                    <div class="sx-ctl"><el-button :icon="Trash2" text @click="revoke(d.id)">撤销</el-button></div>
                  </div>
                  <div v-if="!devices.length" class="sx-empty">暂无登录设备</div>
                </div>
              </section>
            </template>

            <!-- ============ 凭据 / 任务 ============ -->
            <SecurityPanel v-else-if="tab === 'security'" />
            <CredentialsPanel
              v-else-if="tab === 'credentials'"
              :credentials="credentials"
              @saved="emit('credentialSaved', $event)"
              @deleted="emit('credentialDeleted', $event)"
            />
            <TaskPanel v-else-if="tab === 'tasks'" :sessions="sessions" />

            <!-- ============ 用户管理 ============ -->
            <template v-else-if="tab === 'users' && auth.user?.role === 'admin'">
              <section class="sx">
                <div class="sx-h-row">
                  <h3 class="sx-h">{{ users.length }} 个账户</h3>
                  <el-button :icon="UserPlus" type="primary" size="small" @click="openCreateUser">添加用户</el-button>
                </div>
                <div v-if="users.length" class="sx-list">
                  <div v-for="u in users" :key="u.id" class="sx-row user-row">
                    <span class="user-initial">{{ u.username.slice(0, 1).toUpperCase() }}</span>
                    <div class="sx-info">
                      <strong>
                        {{ u.username }}
                        <span class="sx-badge" :class="{ off: u.disabled }">{{ u.disabled ? "已禁用" : "已启用" }}</span>
                      </strong>
                      <small>
                        {{ u.role === "admin" ? "管理员" : "普通用户" }}
                        <template v-if="u.forcePasswordChange"> · 登录后需改密</template>
                      </small>
                    </div>
                    <div class="sx-ctl">
                      <el-button :icon="Pencil" text @click="openEditUser(u)">编辑</el-button>
                      <el-button :icon="LockKeyhole" text @click="openUserPassword(u)">改密</el-button>
                      <el-button v-if="u.id !== auth.user?.id" :icon="Trash2" text type="danger" @click="deleteUser(u)">删除</el-button>
                    </div>
                  </div>
                </div>
                <div v-else class="sx-empty">暂无用户</div>
              </section>
            </template>

            <!-- ============ 系统管理 ============ -->
            <template v-else-if="tab === 'admin' && auth.user?.role === 'admin'">
              <section class="sx">
                <div class="stat-strip">
                  <div><strong>{{ stats.users || 0 }}</strong><span>用户 · {{ stats.activeUsers || 0 }} 活跃</span></div>
                  <div><strong>{{ stats.hosts || 0 }}</strong><span>主机</span></div>
                  <div><strong>{{ stats.sessions || 0 }}</strong><span>SSH 会话</span></div>
                  <div><strong>{{ stats.websockets || 0 }}</strong><span>WebSocket</span></div>
                  <div><strong>{{ formatBytes(stats.databaseBytes || 0) }}</strong><span>数据库</span></div>
                  <div><strong>{{ stats.backups || 0 }}</strong><span>备份</span></div>
                </div>
                <p class="sx-meta">
                  <span>运行 {{ formatDuration(stats.uptimeSeconds || 0) }}</span>
                  <span v-if="stats.goVersion">{{ stats.goVersion }}</span>
                  <span v-if="stats.deploymentID">部署 {{ stats.deploymentID }}</span>
                  <span v-if="stats.latestBackupAt">最近备份 {{ new Date(stats.latestBackupAt).toLocaleString() }}</span>
                </p>
              </section>

              <section class="sx">
                <h3 class="sx-h">登录安全策略</h3>
                <div class="sx-list">
                  <div class="sx-row">
                    <div class="sx-info"><strong>密码最小长度</strong></div>
                    <div class="sx-ctl"><el-input-number v-model="policy.passwordMinLength" :min="10" :max="128" /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>失败锁定阈值</strong><small>连续失败达到该次数后锁定账户</small></div>
                    <div class="sx-ctl"><el-input-number v-model="policy.loginFailureThreshold" :min="3" :max="20" /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>锁定分钟数</strong></div>
                    <div class="sx-ctl"><el-input-number v-model="policy.lockMinutes" :min="1" :max="1440" /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>保持登录天数</strong></div>
                    <div class="sx-ctl"><el-input-number v-model="policy.rememberDays" :min="1" :max="90" /></div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>新用户强制改密</strong><small>首次登录后必须修改初始密码</small></div>
                    <div class="sx-ctl"><el-switch v-model="policy.forceChangeOnCreate" /></div>
                  </div>
                </div>
              </section>
              <div class="sp-savebar">
                <el-button :icon="ShieldCheck" type="primary" @click="savePolicy">保存安全策略</el-button>
              </div>
            </template>

            <!-- ============ 组网 ============ -->
            <template v-else-if="tab === 'network' && auth.user?.role === 'admin'">
              <section class="sx">
                <div class="sx-h-row">
                  <h3 class="sx-h">内嵌 Tailscale</h3>
                  <span class="sx-badge" :class="{ ok: tailscale.status.state === 'Running' }">
                    {{ tailscale.status.state === "Running" ? "运行中" : tailscale.enabled ? tailscale.status.state : "已关闭" }}
                  </span>
                </div>
                <div class="sx-list">
                  <div class="sx-row">
                    <div class="sx-info"><strong>启用服务</strong><small>默认关闭，仅在开启并保存后启动 Go 网络服务</small></div>
                    <div class="sx-ctl"><el-switch v-model="tailscaleForm.enabled" /></div>
                  </div>
                </div>
                <el-form label-position="top" class="sx-fields">
                  <el-form-item label="节点名称">
                    <el-input v-model="tailscaleForm.hostname" maxlength="63" />
                  </el-form-item>
                  <el-form-item label="控制面地址">
                    <el-input v-model="tailscaleForm.controlURL" placeholder="官方 Tailscale 留空，Headscale 填写地址" clearable />
                  </el-form-item>
                  <el-form-item label="Auth Key">
                    <el-input
                      v-model="tailscaleForm.authKey"
                      type="password"
                      show-password
                      autocomplete="new-password"
                      :placeholder="tailscale.authKeyConfigured ? '已配置，留空保持不变' : '输入 Auth Key'"
                      :disabled="tailscaleForm.clearAuthKey"
                    />
                  </el-form-item>
                  <el-checkbox v-if="tailscale.authKeyConfigured" v-model="tailscaleForm.clearAuthKey">清除已保存的 Auth Key</el-checkbox>
                </el-form>
                <p v-if="tailscale.status.ips?.length || tailscale.status.authUrl" class="sx-meta">
                  <span v-if="tailscale.status.ips?.length">IP {{ tailscale.status.ips.join(", ") }}</span>
                  <a v-if="tailscale.status.authUrl" :href="tailscale.status.authUrl" target="_blank" rel="noreferrer">打开登录链接</a>
                </p>
              </section>
              <div class="sp-savebar">
                <el-button :icon="Save" type="primary" :loading="savingTailscale" @click="saveTailscale">保存并应用</el-button>
              </div>
            </template>

            <!-- ============ AI 模型 ============ -->
            <template v-else-if="tab === 'ai' && auth.user?.role === 'admin'">
              <section class="sx">
                <div class="sx-h-row">
                  <h3 class="sx-h">Agent 模型服务</h3>
                  <span class="sx-badge" :class="{ ok: aiModel.configured }">{{ aiModel.configured ? "已启用" : "未配置" }}</span>
                </div>
                <p class="sx-desc">兼容 Chat Completions 工具调用的模型接口。</p>
                <el-form label-position="top" class="sx-fields">
                  <el-form-item label="API 地址">
                    <el-input v-model="aiModelForm.baseURL" placeholder="https://api.example.com/v1" clearable />
                  </el-form-item>
                  <el-form-item label="模型名称">
                    <el-input v-model="aiModelForm.model" placeholder="输入模型标识" clearable />
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
                  <el-checkbox v-if="aiModel.apiKeyConfigured" v-model="aiModelForm.clearAPIKey">清除已保存的 API Key</el-checkbox>
                  <el-form-item label="Agent 代理地址（可选）">
                    <el-input
                      v-model="aiModelForm.proxyURL"
                      type="password"
                      show-password
                      autocomplete="new-password"
                      placeholder="http://127.0.0.1:7890 或 socks5://127.0.0.1:1080"
                      clearable
                    />
                    <p class="sx-hint">仅用于 AI Agent 的模型请求和连接测试，留空直连。支持 HTTP、HTTPS、SOCKS5，可在地址中填写账号密码。</p>
                  </el-form-item>
                </el-form>
              </section>
              <div class="sp-savebar">
                <el-button :icon="FlaskConical" :loading="testingAIModel" :disabled="savingAIModel" @click="testAIModel">测试连接</el-button>
                <el-button type="primary" :icon="Save" :loading="savingAIModel" :disabled="testingAIModel" @click="saveAIModel()">保存配置</el-button>
              </div>
            </template>

            <!-- ============ 命令片段 / 端口转发 ============ -->
            <ToolsDrawer
              v-else-if="tab === 'snippets' || tab === 'forwards'"
              :section="tab"
              :hosts="hosts"
              :sessions="sessions"
              @insert="emit('insert', $event)"
              @execute="emit('execute', $event)"
              @batch-execute="forwardBatch"
            />

            <!-- ============ 数据 ============ -->
            <template v-else-if="tab === 'data'">
              <section v-if="auth.user?.role === 'admin'" class="sx">
                <div class="sx-h-row">
                  <h3 class="sx-h">加密备份与恢复</h3>
                  <span class="sx-badge mono">AES-GCM</span>
                </div>
                <p class="sx-desc">备份包含应用数据库和主密钥，使用输入的密钥整体加密。密钥不会保存到服务器，忘记密钥无法恢复备份。</p>
                <div class="sx-form backup-key-form">
                  <el-input
                    v-model="backupKey"
                    type="password"
                    show-password
                    autocomplete="new-password"
                    placeholder="备份密钥，至少 12 个字符"
                  />
                  <input
                    ref="backupFileInput"
                    class="visually-hidden"
                    type="file"
                    accept=".enc,application/octet-stream"
                    @change="uploadBackup"
                  />
                  <div class="sx-actions left">
                    <el-button :icon="HardDriveDownload" type="primary" :loading="backupBusy === 'backup'" :disabled="backupBusy === 'restore'" @click="backup">创建加密备份</el-button>
                    <el-button :icon="Upload" :loading="backupBusy === 'restore'" :disabled="backupBusy === 'backup'" @click="chooseBackupFile">上传备份文件</el-button>
                  </div>
                </div>
                <p class="sx-hint">上传时会先使用备份密钥解密并校验完整性，校验通过后才会加入下方列表。</p>
                <div class="sx-list">
                  <div v-for="item in backups" :key="item.file" class="sx-row">
                    <div class="sx-info">
                      <strong class="mono">{{ item.file }}</strong>
                      <small>{{ formatBytes(item.size) }} · {{ new Date(item.createdAt).toLocaleString() }} · {{ item.sha256?.slice(0, 12) }}</small>
                    </div>
                    <div class="sx-ctl">
                      <el-button text :icon="Download" @click="downloadBackup(item.file)">下载</el-button>
                      <el-button text type="warning" :icon="RefreshCw" :loading="backupBusy === 'restore'" :disabled="backupBusy === 'backup'" @click="restoreBackup(item.file)">恢复</el-button>
                    </div>
                  </div>
                  <div v-if="!backups.length" class="sx-empty">暂无加密备份</div>
                </div>
              </section>
              <ToolsDrawer
                section="data"
                :hosts="hosts"
                :sessions="sessions"
                @insert="emit('insert', $event)"
                @execute="emit('execute', $event)"
                @batch-execute="forwardBatch"
              />
            </template>

            <!-- ============ 版本更新 ============ -->
            <template v-else-if="tab === 'updates'">
              <section class="sx">
                <div class="sx-h-row">
                  <h3 class="sx-h">检查 GitHub 最新稳定 Release</h3>
                  <span
                    class="sx-badge"
                    :class="{
                      ok: updateCheck.versionKnown && !updateCheck.updateAvailable,
                      warn: updateCheck.updateAvailable,
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
                <div class="sx-list">
                  <div class="sx-row">
                    <div class="sx-info"><strong>当前版本</strong></div>
                    <div class="sx-ctl mono">{{ updateCheck.currentVersion }}</div>
                  </div>
                  <div class="sx-row">
                    <div class="sx-info"><strong>最新版本</strong>
                      <small v-if="updateCheckError" class="err">{{ updateCheckError }}</small>
                      <small v-else-if="updateCheck.releaseName">
                        {{ updateCheck.releaseName }}<template v-if="updateCheck.publishedAt"> · {{ new Date(updateCheck.publishedAt).toLocaleDateString() }}</template>
                      </small>
                    </div>
                    <div class="sx-ctl mono">{{ updateCheck.latestVersion || "--" }}</div>
                  </div>
                </div>
                <div class="sx-actions left">
                  <el-button :icon="RefreshCw" :loading="updateCheckBusy" @click="checkForUpdate(true)">检查更新</el-button>
                  <a v-if="updateCheck.releaseURL" class="el-button el-button--primary" :href="updateCheck.releaseURL" target="_blank" rel="noreferrer">查看 Release</a>
                </div>
              </section>
            </template>
          </div>
        </main>
      </div>
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
