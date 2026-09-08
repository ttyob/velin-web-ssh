<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { Copy, ExternalLink, Eye, Keyboard, Link2, Square } from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import type { TerminalSession, TerminalShare } from "../types";

const props = defineProps<{ modelValue: boolean; session?: TerminalSession }>();
const emit = defineEmits<{ "update:modelValue": [boolean] }>();
const password = ref("");
const permission = ref<"view" | "operate">("view");
const expiresInMinutes = ref(60);
const record = ref(false);
const shares = ref<TerminalShare[]>([]);
const loading = ref(false);
const creating = ref(false);
let refreshTimer: number | undefined;

const activeShares = computed(() => shares.value.filter((item) => item.active));
const previousShares = computed(() => shares.value.filter((item) => !item.active));

watch(
  [() => props.modelValue, () => props.session?.id],
  ([open]) => {
    stopRefresh();
    if (!open || !props.session) return;
    password.value = "";
    void loadShares();
    refreshTimer = window.setInterval(loadShares, 3000);
  },
  { immediate: true },
);

onBeforeUnmount(stopRefresh);

function stopRefresh() {
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer);
  refreshTimer = undefined;
}

async function loadShares() {
  if (!props.session) return;
  if (!shares.value.length) loading.value = true;
  try {
    shares.value = await api<TerminalShare[]>(
      `/api/sessions/${props.session.id}/shares`,
    );
  } catch (error) {
    if (!shares.value.length)
      ElMessage.error(error instanceof Error ? error.message : "读取分享失败");
  } finally {
    loading.value = false;
  }
}

async function createShare() {
  if (!props.session || creating.value) return;
  creating.value = true;
  try {
    const value = await api<TerminalShare>(
      `/api/sessions/${props.session.id}/shares`,
      {
        method: "POST",
        body: json({
          password: password.value,
          permission: permission.value,
          expiresInMinutes: expiresInMinutes.value,
          record: record.value,
        }),
      },
    );
    shares.value.unshift(value);
    password.value = "";
    await copyURL(value);
    ElMessage.success("分享已创建，链接已复制");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "创建分享失败");
  } finally {
    creating.value = false;
  }
}

function absoluteURL(item: TerminalShare) {
  return new URL(item.url || "", location.origin).href;
}

async function copyURL(item: TerminalShare) {
  const value = absoluteURL(item);
  try {
    await navigator.clipboard.writeText(value);
  } catch {
    const input = document.createElement("textarea");
    input.value = value;
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    document.execCommand("copy");
    input.remove();
  }
}

function preview(item: TerminalShare) {
  window.open(absoluteURL(item), "_blank", "noopener,noreferrer");
}

async function revoke(item: TerminalShare) {
  try {
    await ElMessageBox.confirm(
      `中止后 ${item.viewerCount || 0} 个在线访客会立即断开，分享链接永久失效。`,
      "中止会话分享",
      {
        confirmButtonText: "立即中止",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    await api(`/api/session-shares/${item.id}`, { method: "DELETE" });
    await loadShares();
    ElMessage.success("分享已中止");
  } catch (error) {
    if (error !== "cancel" && error !== "close")
      ElMessage.error(error instanceof Error ? error.message : "中止分享失败");
  }
}

function expiresLabel(value: string) {
  const date = new Date(value);
  return date.toLocaleString();
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    title="分享终端会话"
    width="min(680px, calc(100vw - 24px))"
    append-to-body
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="share-session-heading">
      <Link2 :size="20" />
      <div><strong>{{ session?.name }}</strong><small>访客只能访问这一条终端会话</small></div>
    </div>

    <el-form label-position="top" class="share-create-form" @submit.prevent="createShare">
      <div class="share-form-grid">
        <el-form-item label="访问权限">
          <el-segmented
            v-model="permission"
            :options="[
              { label: '仅观看', value: 'view' },
              { label: '可操作', value: 'operate' },
            ]"
          />
        </el-form-item>
        <el-form-item label="有效期">
          <el-select v-model="expiresInMinutes">
            <el-option label="15 分钟" :value="15" />
            <el-option label="1 小时" :value="60" />
            <el-option label="8 小时" :value="480" />
            <el-option label="24 小时" :value="1440" />
            <el-option label="7 天" :value="10080" />
            <el-option label="30 天" :value="43200" />
          </el-select>
        </el-form-item>
      </div>
      <el-form-item label="访问密码">
        <el-input
          v-model="password"
          type="password"
          maxlength="128"
          show-password
          autocomplete="new-password"
          placeholder="可选，设置后至少 4 个字符"
        />
      </el-form-item>
      <div class="share-create-actions">
        <label><el-switch v-model="record" /><span>分享期间录制终端输出</span></label>
        <el-button type="primary" native-type="submit" :loading="creating">创建并复制链接</el-button>
      </div>
    </el-form>

    <div class="share-list-title"><strong>当前分享</strong><small>{{ activeShares.length }} 个有效</small></div>
    <el-skeleton v-if="loading" :rows="2" animated />
    <div v-else-if="activeShares.length" class="share-list">
      <article v-for="item in activeShares" :key="item.id" class="share-row">
        <div class="share-row-main">
          <span class="share-permission">
            <Keyboard v-if="item.permission === 'operate'" :size="15" />
            <Eye v-else :size="15" />
            {{ item.permission === "operate" ? "可操作" : "仅观看" }}
          </span>
          <strong>{{ item.viewerCount }} 人在线</strong>
          <small>有效至 {{ expiresLabel(item.expiresAt) }} · {{ item.passwordRequired ? "有密码" : "无密码" }}<template v-if="item.record"> · 正在录屏</template></small>
          <div v-if="item.viewers?.length" class="share-viewers">
            <span v-for="viewer in item.viewers" :key="viewer.id">{{ viewer.name }} · {{ viewer.ip }}</span>
          </div>
        </div>
        <div class="share-row-actions">
          <el-tooltip content="复制链接"><button @click="copyURL(item)"><Copy :size="16" /></button></el-tooltip>
          <el-tooltip content="打开预览"><button @click="preview(item)"><ExternalLink :size="16" /></button></el-tooltip>
          <el-tooltip content="中止分享"><button class="danger" @click="revoke(item)"><Square :size="15" /></button></el-tooltip>
        </div>
      </article>
    </div>
    <el-empty v-else :image-size="52" description="暂无有效分享" />

    <details v-if="previousShares.length" class="share-history">
      <summary>已结束分享（{{ previousShares.length }}）</summary>
      <p v-for="item in previousShares" :key="item.id">
        {{ item.permission === "operate" ? "可操作" : "仅观看" }} · {{ item.revokedAt ? "已中止" : "已过期" }} · {{ expiresLabel(item.createdAt) }}
      </p>
    </details>
  </el-dialog>
</template>

<style scoped>
.share-session-heading { display: flex; gap: 10px; align-items: center; padding-bottom: 14px; border-bottom: 1px solid var(--line); }
.share-session-heading div, .share-row-main { display: grid; min-width: 0; gap: 3px; }
.share-session-heading small, .share-row small, .share-list-title small { color: var(--muted); }
.share-create-form { padding: 16px 0 18px; border-bottom: 1px solid var(--line); }
.share-form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.share-create-actions { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.share-create-actions label { display: flex; align-items: center; gap: 8px; }
.share-list-title { display: flex; justify-content: space-between; margin: 18px 0 10px; }
.share-list { display: grid; gap: 8px; }
.share-row { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; padding: 12px; border: 1px solid var(--line); border-radius: 6px; background: var(--surface-2); }
.share-permission { display: inline-flex; align-items: center; gap: 6px; color: var(--accent); }
.share-viewers { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 4px; }
.share-viewers span { padding: 2px 6px; border: 1px solid var(--line); border-radius: 4px; color: var(--muted); font-size: 11px; }
.share-row-actions { display: flex; flex: none; gap: 4px; }
.share-row-actions button { display: grid; place-items: center; width: 32px; height: 32px; border: 0; border-radius: 4px; color: var(--text); background: transparent; cursor: pointer; }
.share-row-actions button:hover { background: var(--surface-3); }
.share-row-actions .danger { color: var(--danger); }
.share-history { margin-top: 14px; color: var(--muted); font-size: 12px; }
.share-history summary { cursor: pointer; }
@media (max-width: 560px) { .share-form-grid { grid-template-columns: 1fr; gap: 0; } .share-create-actions { align-items: stretch; flex-direction: column; } }
</style>
