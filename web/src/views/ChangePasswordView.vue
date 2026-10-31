<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { LockKeyhole, Save } from "@lucide/vue";
import { ElMessage } from "element-plus";
import { ApiError, api, json } from "../api";
import { useAuthStore } from "../stores/auth";
import AuthShell from "../components/AuthShell.vue";

const auth = useAuthStore();
const router = useRouter();
const currentPassword = ref("");
const newPassword = ref("");
const confirmPassword = ref("");
const loading = ref(false);

async function submit() {
  if (loading.value) return;
  if (!currentPassword.value || !newPassword.value)
    return ElMessage.warning("请填写当前密码和新密码");
  if (newPassword.value !== confirmPassword.value)
    return ElMessage.warning("两次输入的新密码不一致");
  loading.value = true;
  try {
    await api("/api/auth/password", {
      method: "POST",
      body: json({
        currentPassword: currentPassword.value,
        newPassword: newPassword.value,
      }),
    });
    if (auth.user) auth.user.forcePasswordChange = false;
    ElMessage.success("密码已修改");
    await router.replace("/workspace");
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      auth.user = null;
      ElMessage.error("登录已失效，请重新登录后修改密码");
      await router.replace("/login");
      return;
    }
    ElMessage.error(error instanceof Error ? error.message : "修改失败");
  } finally {
    loading.value = false;
  }
}
async function logout() {
  try {
    await auth.logout();
  } catch {
    // Local authentication is cleared by the store even if the service is down.
  } finally {
    await router.replace("/login");
  }
}
</script>

<template>
  <AuthShell title="设置你的密码" subtitle="首次登录，先更新初始密码。">
    <form @submit.prevent="submit">
      <label for="current-password">当前密码</label>
      <el-input
        id="current-password"
        v-model="currentPassword"
        size="large"
        type="password"
        show-password
        :disabled="loading"
        autocomplete="current-password"
      >
        <template #prefix><LockKeyhole :size="17" /></template>
      </el-input>
      <label for="new-password">新密码</label>
      <el-input
        id="new-password"
        v-model="newPassword"
        size="large"
        type="password"
        show-password
        :disabled="loading"
        autocomplete="new-password"
      />
      <label for="confirm-password">确认新密码</label>
      <el-input
        id="confirm-password"
        v-model="confirmPassword"
        size="large"
        type="password"
        show-password
        :disabled="loading"
        autocomplete="new-password"
      />
      <el-button
        native-type="submit"
        type="primary"
        size="large"
        :icon="Save"
        :loading="loading"
      >
        修改并进入工作区
      </el-button>
    </form>
    <template #footer>
      <button class="auth-text-button" type="button" :disabled="loading" @click="logout">返回登录</button>
    </template>
  </AuthShell>
</template>
