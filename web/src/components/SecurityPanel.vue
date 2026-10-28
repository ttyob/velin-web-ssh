<script setup lang="ts">
import { onMounted, ref } from "vue";
import QrcodeVue from "qrcode.vue";
import { Copy, Download, KeyRound, ShieldCheck, Smartphone } from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import { useAuthStore } from "../stores/auth";

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
  <section v-loading="loading" class="sec">
    <div class="sx">
      <div class="sx-h-row">
        <h3 class="sx-h">双因素认证</h3>
        <span class="sx-badge" :class="{ ok: totp.enabled }">{{ totp.enabled ? "已启用" : "未启用" }}</span>
      </div>
      <p class="sx-desc">使用验证器应用生成动态码，降低密码泄露后的登录风险。</p>

      <template v-if="totp.enabled">
        <div class="sx-list">
          <div class="sx-row">
            <Smartphone :size="18" class="sec-icon" />
            <div class="sx-info">
              <strong>验证器已绑定</strong>
              <small>剩余恢复码 {{ totp.recoveryCodesRemaining }} 个</small>
            </div>
          </div>
        </div>
      </template>

      <template v-else>
        <div v-if="!setup" class="sec-empty">
          <KeyRound :size="26" />
          <strong>尚未设置验证器</strong>
          <p>使用 Google Authenticator、Microsoft Authenticator 或其他兼容应用扫描二维码。</p>
          <el-button type="primary" :icon="ShieldCheck" :loading="setupBusy" @click="beginSetup">开始设置</el-button>
        </div>
        <div v-else class="sec-setup">
          <div class="sec-qr">
            <div class="sec-qr-frame">
              <QrcodeVue :value="setup.uri" :size="188" level="M" render-as="svg" background="#ffffff" foreground="#101817" />
            </div>
            <strong>用验证器扫描二维码</strong>
            <p>确认应用中显示 VelinWebSSH 后，再输入动态码完成绑定。</p>
          </div>
          <div class="sec-form">
            <label>手动密钥</label>
            <div class="sec-copy"><code>{{ setup.secret }}</code><el-button text :icon="Copy" @click="copyValue(setup.secret, '密钥')">复制</el-button></div>
            <label>当前密码</label>
            <el-input v-model="setupForm.password" type="password" show-password placeholder="当前密码" autocomplete="current-password" />
            <label>验证器动态码</label>
            <el-input v-model="setupForm.code" maxlength="6" inputmode="numeric" placeholder="6 位动态码" @keyup.enter="enableTOTP" />
            <div class="sx-actions"><el-button type="primary" :loading="actionBusy" @click="enableTOTP">确认启用</el-button></div>
          </div>
        </div>
      </template>
    </div>

    <div v-if="totp.enabled" class="sx">
      <h3 class="sx-h">关闭双因素认证</h3>
      <p class="sx-desc">需要当前密码和验证器动态码才能关闭。</p>
      <div class="sx-form">
        <el-input v-model="disableForm.password" type="password" show-password placeholder="当前密码" autocomplete="current-password" />
        <el-input v-model="disableForm.code" maxlength="6" inputmode="numeric" placeholder="6 位动态码" />
        <div class="sx-actions"><el-button type="danger" plain :loading="actionBusy" @click="disableTOTP">关闭认证</el-button></div>
      </div>
    </div>

    <div v-if="recoveryCodes.length" class="sx">
      <h3 class="sx-h">请立即保存恢复码</h3>
      <p class="sx-desc">恢复码只显示这一次，每个恢复码只能使用一次。</p>
      <div class="sec-codes"><code v-for="code in recoveryCodes" :key="code">{{ code }}</code></div>
      <div class="sx-actions left">
        <el-button :icon="Copy" @click="copyValue(recoveryCodes.join('\n'), '恢复码')">复制全部</el-button>
        <el-button :icon="Download" @click="downloadRecoveryCodes">下载文本</el-button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.sec-icon { flex: 0 0 auto; color: var(--accent); }
.sec-empty {
  display: grid;
  justify-items: center;
  gap: 8px;
  padding: 36px 16px;
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-md);
  color: var(--accent);
  text-align: center;
}
.sec-empty strong { color: var(--text); font-size: 14px; }
.sec-empty p, .sec-qr p { margin: 0 0 6px; max-width: 460px; color: var(--muted); font-size: 12.5px; line-height: 1.6; }
.sec-setup { display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 28px; padding-top: 4px; }
.sec-qr { display: grid; justify-items: center; align-content: start; gap: 8px; text-align: center; }
.sec-qr strong { color: var(--text); font-size: 13px; }
.sec-qr-frame { padding: 12px; border-radius: var(--radius-md); background: #fff; }
.sec-form { display: grid; align-content: start; gap: 8px; }
.sec-form label { margin-top: 4px; color: var(--muted); font-size: 12px; }
.sec-copy {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 2px 4px 2px 10px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  background: var(--field-bg);
}
.sec-copy code { flex: 1; overflow-wrap: anywhere; color: var(--text); font: 12px var(--font-mono); user-select: all; }
.sec-codes { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 8px; }
.sec-codes code {
  padding: 8px 10px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  background: var(--field-bg);
  color: var(--text);
  font: 12.5px var(--font-mono);
  text-align: center;
  user-select: all;
}
@media (max-width: 680px) {
  .sec-setup { grid-template-columns: minmax(0, 1fr); gap: 20px; }
}
</style>
