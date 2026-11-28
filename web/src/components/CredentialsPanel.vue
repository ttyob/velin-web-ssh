<script setup lang="ts">
import { ref } from "vue";
import { KeyRound, Plus, Trash2 } from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api } from "../api";
import type { Credential } from "../types";
import CredentialDialog from "./CredentialDialog.vue";

defineProps<{ credentials: Credential[] }>();
const emit = defineEmits<{
  saved: [Credential];
  deleted: [string];
}>();
const dialogOpen = ref(false);

async function remove(credential: Credential) {
  try {
    await ElMessageBox.confirm(
      `删除凭据“${credential.name}”？使用该凭据的主机需要先改用其他认证方式。`,
      "删除凭据",
      {
        confirmButtonText: "删除",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    await api(`/api/credentials/${credential.id}`, { method: "DELETE" });
    emit("deleted", credential.id);
    ElMessage.success("凭据已删除");
  } catch (error: any) {
    if (error !== "cancel" && error !== "close")
      ElMessage.error(error instanceof Error ? error.message : "凭据删除失败");
  }
}
</script>

<template>
  <div class="tool-heading">
    <span>已保存凭据 · {{ credentials.length }}</span>
    <el-button :icon="Plus" type="primary" size="small" @click="dialogOpen = true"
      >新增凭据</el-button
    >
  </div>
  <div v-if="credentials.length" class="list-stack">
    <div
      v-for="credential in credentials"
      :key="credential.id"
      class="data-row credential-row"
    >
      <KeyRound :size="17" />
      <div>
        <strong>{{ credential.name }}</strong>
        <small
          >{{ credential.kind === "key" ? "SSH 私钥" : "密码" }} ·
          {{ new Date(credential.createdAt || "").toLocaleString() }}</small
        >
      </div>
      <el-button
        :icon="Trash2"
        text
        type="danger"
        @click="remove(credential)"
      />
    </div>
  </div>
  <div v-else class="empty-small">
    <KeyRound :size="28" /><span>暂无已保存凭据</span>
  </div>
  <CredentialDialog v-model="dialogOpen" @saved="emit('saved', $event)" />
</template>
