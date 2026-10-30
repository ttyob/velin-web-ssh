<script setup lang="ts">
import { ref } from "vue";
import { tmuxInstallGuide } from "../../tmuxInstall";
import type { PendingConnection } from "./types";
const props = defineProps<{ attempt: PendingConnection }>();
const emit = defineEmits<{ close: [] }>();
const secret = ref(""), passphrase = ref("");
function submitCredentials() {
  if (!secret.value) return;
  props.attempt.credentials?.(secret.value, passphrase.value);
  secret.value = ""; passphrase.value = "";
}
</script>
<template>
  <section class="connection-progress" aria-label="SSH 连接信息" aria-live="polite">
    <header><strong>{{ attempt.name }}</strong><span>{{ attempt.host.username }}@{{ attempt.host.address }}:{{ attempt.host.port }}</span></header>
    <div class="connection-progress-content">
      <ol class="connection-log"><li v-for="(message, index) in attempt.messages" :key="index">{{ message }}</li></ol>
      <p v-if="attempt.stage === 'connecting'" class="connection-waiting"><span class="connection-spinner" />正在连接，请稍候…</p>
      <section v-else-if="attempt.stage === 'trust'" class="connection-action" aria-label="确认主机指纹">
        <h3>{{ attempt.changedKey ? '主机指纹发生变化' : '首次连接需要信任主机指纹' }}</h3><p>确认指纹后继续连接。</p><pre>{{ attempt.fingerprint }}</pre>
        <el-button type="primary" @click="attempt.trust?.()">信任并连接</el-button><el-button @click="emit('close')">取消连接</el-button>
      </section>
      <form v-else-if="attempt.stage === 'credentials'" class="connection-action connection-credentials" aria-label="临时凭据" @submit.prevent="submitCredentials">
        <h3>需要 SSH 认证</h3><p>凭据仅用于本次连接。</p><el-input v-model="secret" type="password" show-password placeholder="SSH 密码或私钥内容" aria-label="SSH 密码或私钥内容" autocomplete="off" /><el-input v-model="passphrase" type="password" show-password placeholder="私钥口令（可选）" aria-label="私钥口令（可选）" autocomplete="off" /><div><el-button native-type="submit" type="primary" :disabled="!secret">连接</el-button><el-button @click="emit('close')">取消连接</el-button></div>
      </form>
      <section v-else-if="attempt.stage === 'tmux'" class="connection-action" aria-label="需要安装 tmux">
        <h3>SSH 已验证，需要安装 tmux</h3><p>{{ tmuxInstallGuide(attempt.host.platform, attempt.host.distribution).notice }}</p><pre>{{ tmuxInstallGuide(attempt.host.platform, attempt.host.distribution).command }}</pre><p>可在其他 SSH 客户端执行安装命令，或先使用普通模式。</p><el-button type="primary" @click="attempt.retry()">安装完成，重新连接</el-button><el-button @click="attempt.normal?.()">使用普通模式</el-button>
      </section>
      <section v-else class="connection-action connection-failure" role="alert"><h3>连接失败</h3><pre>{{ attempt.error }}</pre><el-button type="primary" @click="attempt.retry()">重试连接</el-button><el-button @click="emit('close')">关闭标签</el-button></section>
    </div>
    <footer>{{ attempt.stage === 'connecting' ? '连接中' : attempt.stage === 'failed' ? '连接失败' : '等待操作' }}<button type="button" @click="emit('close')">{{ attempt.stage === 'connecting' ? '取消连接' : '关闭标签' }}</button></footer>
  </section>
</template>
<style scoped>
.connection-progress { width: 100%; height: 100%; display: flex; flex: 1; min-width: 0; min-height: 0; flex-direction: column; background: var(--bg); color: var(--text); }
header { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; padding: 14px 20px; border-bottom: 1px solid var(--line); font-size: 13px; } header span { color: var(--muted); }
.connection-progress-content { flex: 1; overflow: auto; padding: 20px; }
.connection-log { padding: 0; list-style: none; margin: 0; font: 13px/1.9 var(--terminal-font-family, monospace); color: var(--muted); }
.connection-log li::before { content: '› '; color: var(--accent); }
.connection-action { margin-top: 20px; max-width: 680px; padding: 20px; background: var(--surface); border: 1px solid var(--line); border-radius: 10px; }
h3 { margin: 0 0 10px; font-size: 16px; } p { font-size: 13px; color: var(--muted); line-height: 1.7; } pre { white-space: pre-wrap; overflow-wrap: anywhere; user-select: text; font-size: 13px; line-height: 1.7; }
.connection-credentials { display: grid; gap: 10px; max-width: 440px; }
.connection-credentials p { margin: 0; }
.connection-failure h3 { color: #e89595; }
.connection-waiting { display: flex; gap: 8px; align-items: center; }
.connection-spinner { width: 12px; height: 12px; border: 2px solid var(--line); border-top-color: var(--accent); border-radius: 50%; animation: connecting-spin 1s linear infinite; }
footer { display: flex; align-items: center; gap: 12px; padding: 8px 16px; border-top: 1px solid var(--line); color: var(--muted); font-size: 12px; } footer button { margin-left: auto; background: none; border: 0; color: var(--text); cursor: pointer; }
@keyframes connecting-spin { to { transform: rotate(360deg); } }
@media (max-width: 640px) { .connection-progress-content { padding: 12px; } .connection-action { padding: 14px; } }
@media (prefers-reduced-motion: reduce) { .connection-spinner { animation: none; } }
</style>
