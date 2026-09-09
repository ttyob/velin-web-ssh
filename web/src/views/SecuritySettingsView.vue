<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import QrcodeVue from "qrcode.vue";
import { ArrowLeft, Copy, Download, KeyRound, ShieldCheck, Smartphone } from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import { useAuthStore } from "../stores/auth";

const router = useRouter();
const auth = useAuthStore();
const loading = ref(true);
const setupBusy = ref(false);
const actionBusy = ref(false);
const totp = ref({ enabled: false, recoveryCodesRemaining: 0 });
const setup = ref<{ secret: string; uri: string } | null>(null);
const setupForm = ref({ password: "", code: "" });
const disableForm = ref({ password: "", code: "" });
const recoveryCodes = ref<string[]>([]);

async function load() {
  try {
    totp.value = await api<{ enabled: boolean; recoveryCodesRemaining: number }>(
      "/api/auth/totp",
    );
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "无法加载安全设置");
  } finally {
    loading.value = false;
  }
}

async function beginSetup() {
  setupBusy.value = true;
  try {
    setup.value = await api<{ secret: string; uri: string }>(
      "/api/auth/totp/setup",
      { method: "POST" },
    );
    setupForm.value = { password: "", code: "" };
    recoveryCodes.value = [];
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "无法生成认证设置");
  } finally {
    setupBusy.value = false;
  }
}

async function enableTOTP() {
  if (!setupForm.value.password || !/^\d{6}$/.test(setupForm.value.code.trim())) {
    ElMessage.warning("请输入当前密码和 6 位动态码");
    return;
  }
  actionBusy.value = true;
  try {
    const result = await api<{ enabled: boolean; recoveryCodes: string[] }>(
      "/api/auth/totp/enable",
      { method: "POST", body: json(setupForm.value) },
    );
    totp.value = { enabled: true, recoveryCodesRemaining: result.recoveryCodes.length };
    recoveryCodes.value = result.recoveryCodes;
    setup.value = null;
    setupForm.value = { password: "", code: "" };
    ElMessage.success("双因素认证已启用，请立即保存恢复码");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "启用失败");
  } finally {
    actionBusy.value = false;
  }
}

async function disableTOTP() {
  if (!/^\d{6}$/.test(disableForm.value.code.trim()) || !disableForm.value.password) {
    ElMessage.warning("请输入当前密码和 6 位动态码");
    return;
  }
  try {
    await ElMessageBox.confirm(
      "关闭后，登录将不再要求验证器动态码。确认继续吗？",
      "关闭双因素认证",
      { confirmButtonText: "确认关闭", cancelButtonText: "取消", type: "warning" },
    );
  } catch {
    return;
  }
  actionBusy.value = true;
  try {
    await api("/api/auth/totp", {
      method: "DELETE",
      body: json({ password: disableForm.value.password, code: disableForm.value.code.trim() }),
    });
    totp.value = { enabled: false, recoveryCodesRemaining: 0 };
    disableForm.value = { password: "", code: "" };
    ElMessage.success("双因素认证已关闭");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "关闭失败");
  } finally {
    actionBusy.value = false;
  }
}

async function copyValue(value: string, label: string) {
  try {
    await navigator.clipboard.writeText(value);
    ElMessage.success(`${label}已复制`);
  } catch {
    ElMessage.warning("浏览器未允许复制，请手动选择文本");
  }
}

function downloadRecoveryCodes() {
  const content = `VelinWebSSH 恢复码\n账号：${auth.user?.username || ""}\n生成时间：${new Date().toLocaleString()}\n\n${recoveryCodes.value.join("\n")}\n`;
  const url = URL.createObjectURL(new Blob([content], { type: "text/plain;charset=utf-8" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = "velin-recovery-codes.txt";
  link.click();
  URL.revokeObjectURL(url);
}

onMounted(load);
</script>

<template>
  <main class="security-page">
    <header class="security-page-header">
      <el-button text :icon="ArrowLeft" @click="router.push('/workspace')">返回工作区</el-button>
      <div>
        <h1>安全设置</h1>
        <p>管理登录保护和双因素认证</p>
      </div>
    </header>

    <section v-loading="loading" class="security-page-content">
      <div class="security-page-title">
        <div class="security-page-icon"><ShieldCheck :size="24" /></div>
        <div>
          <h2>双因素认证</h2>
          <p>使用验证器应用生成动态码，降低密码泄露后的登录风险。</p>
        </div>
        <span class="security-state" :class="{ enabled: totp.enabled }">
          {{ totp.enabled ? "已启用" : "未启用" }}
        </span>
      </div>

      <template v-if="totp.enabled">
        <div class="security-status-row">
          <Smartphone :size="18" />
          <span>验证器已绑定</span>
          <small>剩余恢复码 {{ totp.recoveryCodesRemaining }} 个</small>
        </div>
        <div class="security-form security-disable-form">
          <h3>关闭双因素认证</h3>
          <p>需要当前密码和验证器动态码才能关闭。</p>
          <el-input v-model="disableForm.password" type="password" show-password placeholder="当前密码" autocomplete="current-password" />
          <el-input v-model="disableForm.code" maxlength="6" inputmode="numeric" placeholder="6 位动态码" />
          <el-button type="danger" plain :loading="actionBusy" @click="disableTOTP">关闭认证</el-button>
        </div>
      </template>

      <template v-else>
        <div v-if="!setup" class="security-empty-state">
          <KeyRound :size="30" />
          <strong>尚未设置验证器</strong>
          <p>使用 Google Authenticator、Microsoft Authenticator 或其他兼容应用扫描二维码。</p>
          <el-button type="primary" :icon="ShieldCheck" :loading="setupBusy" @click="beginSetup">开始设置</el-button>
        </div>

        <div v-else class="totp-setup-grid">
          <div class="totp-qr-panel">
            <div class="totp-qr-frame">
              <QrcodeVue :value="setup.uri" :size="220" level="M" render-as="svg" background="#ffffff" foreground="#101817" />
            </div>
            <strong>用验证器扫描二维码</strong>
            <p>请确认应用中显示 VelinWebSSH 后，再输入动态码完成绑定。</p>
          </div>
          <div class="security-form">
            <h3>确认绑定</h3>
            <label>手动密钥</label>
            <div class="security-copy-field"><code>{{ setup.secret }}</code><el-button text :icon="Copy" @click="copyValue(setup.secret, '密钥')">复制</el-button></div>
            <label>当前密码</label>
            <el-input v-model="setupForm.password" type="password" show-password placeholder="当前密码" autocomplete="current-password" />
            <label>验证器动态码</label>
            <el-input v-model="setupForm.code" maxlength="6" inputmode="numeric" placeholder="6 位动态码" @keyup.enter="enableTOTP" />
            <el-button type="primary" :loading="actionBusy" @click="enableTOTP">确认启用</el-button>
          </div>
        </div>
      </template>

      <div v-if="recoveryCodes.length" class="recovery-panel security-recovery-panel">
        <strong>请立即保存恢复码</strong>
        <p>恢复码只显示这一次，每个恢复码只能使用一次。</p>
        <div class="recovery-grid"><code v-for="code in recoveryCodes" :key="code">{{ code }}</code></div>
        <div class="security-actions">
          <el-button :icon="Copy" @click="copyValue(recoveryCodes.join('\n'), '恢复码')">复制全部</el-button>
          <el-button :icon="Download" @click="downloadRecoveryCodes">下载文本</el-button>
        </div>
      </div>
    </section>
  </main>
</template>

<style scoped>
.security-page {
  min-height: 100vh;
  padding: 30px clamp(18px, 5vw, 72px);
  color: var(--text, #e5ebe7);
  background: var(--bg, #111318);
}
.security-page-header {
  display: flex;
  align-items: flex-start;
  gap: 18px;
  max-width: 920px;
  margin: 0 auto 24px;
}
.security-page-header h1 { margin: 6px 0 4px; font-size: 24px; }
.security-page-header p { margin: 0; color: var(--muted, #89938e); }
.security-page-content {
  max-width: 920px;
  margin: 0 auto;
  padding: 24px;
  border: 1px solid var(--line, #2a3031);
  border-radius: 8px;
  background: var(--surface, #191d1e);
}
.security-page-title { display: flex; align-items: flex-start; gap: 14px; padding-bottom: 22px; border-bottom: 1px solid var(--line, #2a3031); }
.security-page-icon { display: grid; place-items: center; width: 48px; height: 48px; color: var(--accent, #5b8cff); border: 1px solid var(--accent-border, #3b527d); border-radius: 8px; background: var(--accent-surface, #202d47); }
.security-page-title h2 { margin: 2px 0 6px; font-size: 18px; }
.security-page-title p, .security-empty-state p, .security-form p, .totp-qr-panel p { margin: 0; color: var(--muted, #89938e); font-size: 13px; line-height: 1.6; }
.security-state { margin-left: auto; padding: 5px 9px; border: 1px solid var(--line-strong, #3b4142); border-radius: 4px; color: var(--muted, #89938e); font-size: 12px; white-space: nowrap; }
.security-state.enabled { color: #9bb7ff; border-color: #49618c; background: #202d47; }
.security-status-row { display: flex; align-items: center; gap: 9px; margin-top: 22px; color: var(--accent, #5b8cff); }
.security-status-row small { margin-left: auto; color: var(--muted, #89938e); }
.security-empty-state { display: grid; justify-items: center; gap: 10px; padding: 58px 20px; text-align: center; color: var(--accent, #5b8cff); }
.security-empty-state strong { color: var(--text, #e5ebe7); }
.security-empty-state p { max-width: 520px; }
.totp-setup-grid { display: grid; grid-template-columns: minmax(240px, .9fr) minmax(280px, 1.1fr); gap: 34px; padding-top: 26px; }
.totp-qr-panel { display: grid; justify-items: center; align-content: start; gap: 10px; text-align: center; }
.totp-qr-frame { padding: 14px; border-radius: 8px; background: #fff; }
.security-form { display: grid; align-content: start; gap: 10px; }
.security-form h3 { margin: 0 0 2px; font-size: 16px; }
.security-form label { margin-top: 4px; color: var(--muted, #89938e); font-size: 12px; }
.security-copy-field { display: flex; align-items: center; gap: 8px; min-height: 34px; padding: 4px 6px 4px 10px; border: 1px solid var(--line, #2a3031); border-radius: 4px; background: var(--surface-2, #202526); }
.security-copy-field code { flex: 1; overflow-wrap: anywhere; color: var(--text, #e5ebe7); font-size: 12px; user-select: all; }
.security-disable-form { max-width: 460px; margin-top: 24px; padding-top: 22px; border-top: 1px solid var(--line, #2a3031); }
.security-recovery-panel { margin-top: 24px; }
.security-actions { display: flex; gap: 8px; margin-top: 12px; }
@media (max-width: 680px) {
  .security-page { padding: 18px 12px; }
  .security-page-content { padding: 18px; }
  .security-page-title { gap: 10px; }
  .security-page-icon { width: 40px; height: 40px; }
  .security-page-title p { font-size: 12px; }
  .security-state { font-size: 11px; }
  .totp-setup-grid { grid-template-columns: 1fr; gap: 24px; }
  .security-status-row small { margin-left: auto; }
}
</style>
