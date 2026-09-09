<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { LockKeyhole, LogIn, RefreshCw, Server, ShieldCheck, UserRound } from "@lucide/vue";
import { ElMessage } from "element-plus";
import { useAuthStore } from "../stores/auth";
import { ApiError, api } from "../api";

const auth = useAuthStore();
const router = useRouter();
const username = ref("");
const password = ref("");
const remember = ref(true);
const totpCode = ref("");
const captchaRequired = ref(false);
const captchaID = ref("");
const captchaImage = ref("");
const captchaAnswer = ref("");
const captchaUsername = ref("");
const loading = ref(false);
const captchaLoading = ref(false);
const totpRequired = ref(false);

async function loadCaptcha() {
  const value = username.value.trim();
  if (!value) return;
  captchaLoading.value = true;
  try {
    const result = await api<{ id: string; image: string }>(
      `/api/auth/captcha?username=${encodeURIComponent(value)}`,
    );
    captchaID.value = result.id;
    captchaImage.value = `data:image/svg+xml,${encodeURIComponent(result.image)}`;
    captchaAnswer.value = "";
    captchaUsername.value = value;
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "验证码加载失败");
  } finally {
    captchaLoading.value = false;
  }
}

async function requireCaptcha() {
  captchaRequired.value = true;
  if (captchaUsername.value !== username.value.trim() || !captchaID.value)
    await loadCaptcha();
}

async function submit() {
  if (!username.value || !password.value)
    return ElMessage.warning("请输入用户名和密码");
  if (captchaRequired.value && (!captchaID.value || !captchaAnswer.value.trim()))
    return ElMessage.warning("请输入图形验证码");
  loading.value = true;
  try {
    await auth.login(
      username.value.trim(),
      password.value,
      remember.value,
      totpCode.value.trim(),
      captchaID.value,
      captchaAnswer.value.trim(),
    );
    await router.replace(
      auth.user?.forcePasswordChange ? "/change-password" : "/workspace",
    );
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.body.code === "captcha_required" || error.body.code === "captcha_invalid") {
        if (error.body.code === "captcha_invalid") {
          captchaRequired.value = true;
          await loadCaptcha();
          ElMessage.error("图形验证码错误，请重新输入");
        } else await requireCaptcha();
      } else if (error.body.code === "invalid_credentials") {
        totpRequired.value = false;
        totpCode.value = "";
        if (error.body.captchaRequired) {
          await requireCaptcha();
          ElMessage.error("用户名或密码错误，请输入图形验证码后重试");
        } else ElMessage.error(error.message);
      } else if (error.body.code === "totp_required") {
        totpRequired.value = true;
        if (error.body.captchaRequired) await requireCaptcha();
        ElMessage.warning("请输入双因素验证码或恢复码");
      } else if (error.body.code === "invalid_totp") {
        totpRequired.value = true;
        totpCode.value = "";
        if (error.body.captchaRequired) await requireCaptcha();
        ElMessage.error("双因素验证码无效");
      } else ElMessage.error(error.message);
    } else ElMessage.error(error instanceof Error ? error.message : "登录失败");
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <main class="login-page">
    <section class="login-panel">
      <div class="brand-lockup">
        <span class="brand-mark"><Server :size="24" /></span>
        <div>
          <h1>VelinWebSSH</h1>
          <p>Secure terminal workspace</p>
        </div>
      </div>
      <form @submit.prevent="submit">
        <label for="velin-username">用户名</label>
        <el-input
          id="velin-username"
          v-model="username"
          size="large"
          autocomplete="username"
          autofocus
        >
          <template #prefix><UserRound :size="17" /></template>
        </el-input>
        <label for="velin-password">密码</label>
        <el-input
          id="velin-password"
          v-model="password"
          size="large"
          type="password"
          show-password
          autocomplete="current-password"
        >
          <template #prefix><LockKeyhole :size="17" /></template>
        </el-input>
        <div v-if="captchaRequired" class="login-captcha">
          <label for="velin-captcha">图形验证码</label>
          <div class="captcha-row">
            <button
              class="captcha-image-button"
              type="button"
              aria-label="刷新验证码"
              :disabled="captchaLoading"
              @click="loadCaptcha"
            >
              <img v-if="captchaImage" :src="captchaImage" alt="图形验证码" />
              <RefreshCw v-else :size="20" :class="{ spinning: captchaLoading }" />
            </button>
            <el-input
              id="velin-captcha"
              v-model="captchaAnswer"
              size="large"
              maxlength="5"
              autocomplete="off"
              placeholder="输入图片中的字符"
            />
          </div>
        </div>
        <div v-if="totpRequired" class="login-totp">
          <label for="velin-totp">双因素验证码</label>
          <el-input
            id="velin-totp"
            v-model="totpCode"
            size="large"
            maxlength="16"
            autocomplete="one-time-code"
            inputmode="numeric"
            placeholder="6 位动态码或恢复码"
          >
            <template #prefix><ShieldCheck :size="17" /></template>
          </el-input>
        </div>
        <div class="login-options">
          <el-checkbox v-model="remember">保持登录</el-checkbox>
        </div>
        <el-button
          native-type="submit"
          type="primary"
          size="large"
          :icon="LogIn"
          :loading="loading"
        >
          登录
        </el-button>
      </form>
      <p class="login-foot">支持普通 SSH 与远程 tmux 持久会话</p>
    </section>
  </main>
</template>

<style scoped>
.login-captcha,
.login-totp {
  display: grid;
  gap: 9px;
}

.captcha-row {
  display: grid;
  grid-template-columns: 160px minmax(0, 1fr);
  gap: 9px;
  align-items: stretch;
}

.captcha-image-button {
  display: grid;
  place-items: center;
  min-width: 0;
  height: 40px;
  padding: 0;
  overflow: hidden;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  color: var(--muted);
  cursor: pointer;
}

.captcha-image-button:hover {
  border-color: var(--accent);
}

.captcha-image-button:disabled {
  cursor: wait;
  opacity: 0.65;
}

.captcha-image-button img {
  display: block;
  width: 160px;
  height: 48px;
  object-fit: cover;
}

.spinning {
  animation: captcha-spin 0.9s linear infinite;
}

@keyframes captcha-spin {
  to { transform: rotate(360deg); }
}

@media (max-width: 430px) {
  .captcha-row {
    grid-template-columns: 132px minmax(0, 1fr);
  }

  .captcha-image-button img {
    width: 132px;
  }
}
</style>
