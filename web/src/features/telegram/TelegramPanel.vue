<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Copy, ExternalLink, Link, RefreshCw, Send, Trash2 } from '@lucide/vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import QrcodeVue from 'qrcode.vue'
import { api, json } from '../../api'
import { useAuthStore } from '../../stores/auth'
import type { Host, TerminalSession } from '../../types'

interface BotStatus { enabled:boolean; hasToken:boolean; hasProxy:boolean; username:string; publicURL:string; idleSeconds:number; state:string; lastError:string }
interface Access { permissions:string[]; sessionIDs:string[]; hostIDs:string[]; enabled:boolean; expiresAt:string|null }
interface Binding { active:boolean; telegramUserID:number; name:string; allowFiles:boolean; boundAt:string }
interface Subscription { sessionID:string; name:string; hostName:string }
interface Profile { bot:BotStatus; binding:Binding|null; access:Access|null; subscriptions:Subscription[] }
interface Pair { url:string; command:string; expiresAt:string }
const auth = useAuthStore()
const admin = computed(() => auth.user?.role === 'admin')
const profile = ref<Profile|null>(null), hosts = ref<Host[]>([]), sessions = ref<TerminalSession[]>([])
const loading = ref(false), busy = ref(false), error = ref(''), accessError = ref(''), pair = ref<Pair|null>(null)
const enabled = ref(false), botToken = ref(''), proxyURL = ref(''), proxyChanged = ref(false), publicURL = ref(window.location.origin), idleSeconds = ref(90)
const permissions = ref(['read']), sessionIDs = ref<string[]>([]), hostIDs = ref<string[]>([]), allowFiles = ref(false), expiresAt = ref<string|null>(null)
const now = ref(Date.now()), polling = ref(false)
let timer:ReturnType<typeof setInterval>|undefined
const sshHosts = computed(() => hosts.value.filter(host => !host.protocol || host.protocol === 'ssh'))
const hostName = (id:string) => hosts.value.find(host => host.id === id)?.name || '主机'
const bound = computed(() => !!profile.value?.binding?.active)
const pairExpired = computed(() => !!pair.value && new Date(pair.value.expiresAt).getTime() <= now.value)
const statusLabel = computed(() => ({running:'运行中',connecting:'连接中',error:'连接异常',stopped:'未运行'}[profile.value?.bot.state || 'stopped']))
const accessInactive = computed(() => profile.value?.access && (!profile.value.access.enabled || (!!profile.value.access.expiresAt && new Date(profile.value.access.expiresAt).getTime() <= now.value)))
const accessDirty = computed(() => {
  const saved = profile.value?.access
  const equal = (a:string[],b:string[]) => JSON.stringify([...a].sort()) === JSON.stringify([...b].sort())
  return !saved || !equal(permissions.value,saved.permissions) || !equal(sessionIDs.value,saved.sessionIDs) || !equal(hostIDs.value,saved.hostIDs)
    || allowFiles.value !== !!profile.value?.binding?.allowFiles || (expiresAt.value ? new Date(expiresAt.value).getTime() : 0) !== (saved.expiresAt ? new Date(saved.expiresAt).getTime() : 0)
})
const message = (e:unknown) => e instanceof Error ? e.message : '操作失败，请稍后重试'

function apply(value:Profile, reset:boolean) {
  if (profile.value?.binding?.boundAt !== value.binding?.boundAt) pair.value = null
  profile.value = value
  if (!reset) return
  enabled.value = value.bot.enabled; publicURL.value = value.bot.publicURL || window.location.origin; idleSeconds.value = value.bot.idleSeconds || 90
  botToken.value = ''; proxyURL.value = ''; proxyChanged.value = false
  permissions.value = [...(value.access?.permissions || ['read'])]; sessionIDs.value = [...(value.access?.sessionIDs || [])]; hostIDs.value = [...(value.access?.hostIDs || [])]
  allowFiles.value = value.binding?.allowFiles || false; expiresAt.value = value.access?.expiresAt || null
}
async function load() {
  loading.value = true; error.value = ''
  try {
    const [value,h,s] = await Promise.all([api<Profile>('/api/telegram'),api<Host[]>('/api/hosts'),api<TerminalSession[]>('/api/sessions')])
    hosts.value = h; sessions.value = s; apply(value,true)
  } catch (e) { error.value = message(e) } finally { loading.value = false }
}
async function refresh() {
  if (polling.value || busy.value || loading.value) return
  polling.value = true
  try { apply(await api<Profile>('/api/telegram'),false) } catch {} finally { polling.value = false }
}
function configuration() {
  return { enabled:enabled.value,botToken:botToken.value.trim(),...(proxyChanged.value ? {proxyURL:proxyURL.value.trim()} : {}),publicURL:publicURL.value.trim(),idleSeconds:idleSeconds.value }
}
async function botAction(test:boolean) {
  busy.value = true; error.value = ''
  try {
    if (test) {
      const result = await api<{username:string}>('/api/admin/telegram/test',{method:'POST',body:json(configuration())})
      ElMessage.success(`已连接 @${result.username}，保存后生效`)
    } else {
      await api('/api/admin/telegram',{method:'PUT',body:json(configuration())})
      await load(); ElMessage.success(enabled.value ? '机器人已保存并启动' : '机器人已停用')
    }
  } catch (e) { error.value = message(e) } finally { busy.value = false }
}
async function saveAccess() {
  accessError.value = ''
  if (!sessionIDs.value.length && !hostIDs.value.length) { accessError.value = '请选择至少一个会话或 SSH 主机'; return }
  if (permissions.value.includes('create') && !hostIDs.value.length) { accessError.value = '创建会话需要授权至少一台主机'; return }
  busy.value = true
  try {
    await api('/api/telegram/access',{method:'PUT',body:json({permissions:permissions.value,sessionIDs:sessionIDs.value,hostIDs:hostIDs.value,allowFiles:allowFiles.value,expiresAt:expiresAt.value ? new Date(expiresAt.value).toISOString() : null})})
    apply(await api<Profile>('/api/telegram'),false); ElMessage.success('会话授权已保存')
  } catch (e) { accessError.value = message(e) } finally { busy.value = false }
}
async function createPair() {
  busy.value = true; error.value = ''
  try { pair.value = await api<Pair>('/api/telegram/pair',{method:'POST'}); now.value = Date.now() }
  catch (e) { error.value = message(e) } finally { busy.value = false }
}
async function unbind() {
  try { await ElMessageBox.confirm('解除后，Telegram 将无法操作会话，也不会继续收到通知。','解除 Telegram 绑定',{confirmButtonText:'解除绑定',cancelButtonText:'取消'}); busy.value = true
    await api('/api/telegram/binding',{method:'DELETE'}); pair.value = null; await load(); ElMessage.success('已解除绑定')
  } catch (e) { if (e !== 'cancel' && e !== 'close') error.value = message(e) } finally { busy.value = false }
}
async function unsubscribe(item:Subscription) {
  busy.value = true
  try { await api(`/api/telegram/subscriptions/${encodeURIComponent(item.sessionID)}`,{method:'DELETE'}); apply(await api<Profile>('/api/telegram'),false) }
  catch (e) { error.value = message(e) } finally { busy.value = false }
}
async function copy(value:string) {
  try {
    if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(value)
    else { const field = document.createElement('textarea'); field.value = value; field.style.cssText = 'position:fixed;opacity:0'; document.body.appendChild(field); try { field.select(); if (!document.execCommand('copy')) throw new Error('请手动复制') } finally { field.remove() } }
    ElMessage.success('已复制')
  } catch (e) { ElMessage.error(message(e)) }
}
function openPair() { if (pair.value && !pairExpired.value) window.open(pair.value.url,'_blank','noopener,noreferrer') }
onMounted(() => { load(); timer = setInterval(() => { now.value = Date.now(); refresh() },5000) })
onBeforeUnmount(() => { clearInterval(timer); botToken.value = ''; proxyURL.value = ''; pair.value = null })
</script>

<template>
  <div class="tg-panel" v-loading="loading">
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <section class="tg-card tg-overview">
      <div class="tg-heading"><div class="tg-brand"><Send :size="20" /><strong>Telegram</strong></div><span class="tg-state" :class="{ 'is-error':profile?.bot.state === 'error' }">{{ statusLabel }}</span></div>
      <p>查看进度、回复需求、传递文件，让正在运行的开发会话随身可用。</p>
      <div class="tg-bottom"><span class="tg-muted">{{ profile?.bot.username ? `@${profile.bot.username}` : '机器人尚未配置' }}</span><el-button :icon="RefreshCw" text :disabled="busy" aria-label="刷新 Telegram 状态" @click="load">刷新</el-button></div>
      <el-alert v-if="profile?.bot.lastError" :title="profile.bot.lastError" type="warning" :closable="false" />
    </section>

    <details v-if="admin" class="tg-card tg-config" :open="!profile?.bot.hasToken">
      <summary>机器人配置<span>管理员</span></summary>
      <div class="tg-fields">
        <div class="tg-bottom"><span>启用机器人</span><el-switch v-model="enabled" :disabled="busy" aria-label="启用 Telegram 机器人" /></div>
        <label class="tg-field">Bot Token<el-input v-model="botToken" type="password" show-password autocomplete="off" :placeholder="profile?.bot.hasToken ? '已保存，留空保持原值' : '从 @BotFather 创建机器人后获取'" /></label>
        <label class="tg-field">手机访问的工作台地址<el-input v-model="publicURL" placeholder="https://ssh.example.com" /><small>用于卡片上的“打开终端”，请填写手机可以访问的服务地址。</small></label>
        <label class="tg-field">Telegram 代理<el-input v-model="proxyURL" type="password" show-password autocomplete="off" :placeholder="profile?.bot.hasProxy ? '已保存；输入新地址替换，清空后保存可移除' : '可选，例如 socks5://127.0.0.1:1080'" @input="proxyChanged = true" /><small>仅用于 Telegram 请求，与 AI 模型代理独立。<button v-if="profile?.bot.hasProxy" class="tg-text-button" type="button" @click="proxyURL = ''; proxyChanged = true">移除已保存代理</button><span v-if="proxyChanged && !proxyURL">保存后将使用直连。</span></small></label>
        <div class="tg-bottom"><label for="tg-idle">持续无输出提醒</label><div class="tg-duration"><el-input-number id="tg-idle" v-model="idleSeconds" :min="30" :max="3600" :step="30" controls-position="right" /><span>秒</span></div></div>
        <div class="tg-actions"><el-button :loading="busy" @click="botAction(true)">测试连接</el-button><el-button type="primary" :loading="busy" @click="botAction(false)">保存机器人配置</el-button></div>
      </div>
    </details>
    <p v-else-if="!profile?.bot.enabled" class="tg-muted">请先联系管理员配置并启用机器人。</p>

    <section class="tg-card">
      <div class="tg-heading"><strong>会话授权</strong><span class="tg-muted">仅访问你选择的范围</span></div>
      <p>选择主机将包含其当前与后续会话；选择单个会话可精确限定范围。</p>
      <el-alert v-if="accessInactive" title="授权已停用或过期，重新保存后可恢复使用。" type="warning" :closable="false" />
      <div class="tg-fields">
        <label class="tg-field">允许的会话<el-select v-model="sessionIDs" multiple filterable collapse-tags collapse-tags-tooltip placeholder="选择指定会话" aria-label="Telegram 允许的会话"><el-option v-for="session in sessions" :key="session.id" :value="session.id" :label="`${session.name} · ${hostName(session.hostID)}`" /></el-select></label>
        <label class="tg-field">允许的 SSH 主机<el-select v-model="hostIDs" multiple filterable collapse-tags collapse-tags-tooltip placeholder="选择主机，包含后续会话" aria-label="Telegram 允许的主机"><el-option v-for="host in sshHosts" :key="host.id" :value="host.id" :label="host.name" /></el-select></label>
        <div class="tg-permissions"><el-checkbox-group v-model="permissions"><el-checkbox value="read" disabled>读取输出</el-checkbox><el-checkbox value="input">发送需求 / 按键</el-checkbox><el-checkbox value="create">创建 / 恢复</el-checkbox><el-checkbox value="manage">重命名 / 终止</el-checkbox></el-checkbox-group><el-checkbox v-model="allowFiles">项目文件传输</el-checkbox></div>
        <label class="tg-field">授权有效期<el-date-picker v-model="expiresAt" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" placeholder="不设置则长期有效" clearable /></label>
        <small class="tg-muted">文件传输需要已连接的 tmux 会话。上传保存在项目 clipboard 目录，单个文件最多 20 MiB。</small>
        <el-alert v-if="accessError" :title="accessError" type="error" :closable="false" />
        <div class="tg-actions"><el-button type="primary" :loading="busy" @click="saveAccess">保存会话授权</el-button></div>
      </div>
    </section>

    <section class="tg-card">
      <div class="tg-heading"><strong>绑定账号</strong><span v-if="bound" class="tg-state">已绑定</span></div>
      <template v-if="bound"><p>{{ profile?.binding?.name }} <span class="tg-muted">· ID {{ profile?.binding?.telegramUserID }}</span></p><small class="tg-muted">{{ profile?.binding?.boundAt ? new Date(profile.binding.boundAt).toLocaleString() : '' }}</small></template>
      <p v-else>先保存会话授权，再通过限时链接绑定你的 Telegram 私聊账号。</p>
      <div class="tg-actions"><el-button :icon="Link" :disabled="!profile?.bot.enabled || accessDirty || !!accessInactive" :loading="busy" @click="createPair">{{ bound ? '重新生成绑定链接' : '生成绑定链接' }}</el-button><el-button v-if="profile?.binding" :icon="Trash2" text :disabled="busy" @click="unbind">{{ bound ? '解除绑定' : '清除授权' }}</el-button></div>
      <small v-if="profile?.access && accessDirty" class="tg-muted">授权有未保存的修改，请先保存会话授权。</small>
      <div v-if="pair && !accessDirty" class="tg-pair">
        <div class="tg-qr"><QrcodeVue :value="pair.url" :size="116" level="M" render-as="svg" /></div>
        <div class="tg-pair-content"><strong>{{ pairExpired ? '绑定链接已过期' : '用手机扫描，或打开 Telegram 完成绑定' }}</strong><p>链接 10 分钟内有效，仅可使用一次。请使用你自己的 Telegram 账号。</p><div class="tg-actions"><el-button :icon="ExternalLink" :disabled="pairExpired" @click="openPair">打开 Telegram</el-button><el-button :icon="Copy" :disabled="pairExpired" @click="copy(pair!.url)">复制链接</el-button><el-button :icon="Copy" text :disabled="pairExpired" @click="copy(pair!.command)">复制绑定命令</el-button></div></div>
      </div>
    </section>

    <section class="tg-card">
      <div class="tg-heading"><strong>通知订阅</strong><span class="tg-muted">{{ profile?.subscriptions.length || 0 }} / 20</span></div>
      <p>在 Telegram 会话卡片中开启通知。连接变化和持续无输出会提醒，进度卡片最多每 30 秒更新一次。</p>
      <div v-for="item in profile?.subscriptions" :key="item.sessionID" class="tg-subscription"><div><strong>{{ item.name || item.sessionID }}</strong><small>{{ item.hostName }}</small></div><el-button text :disabled="busy" @click="unsubscribe(item)">关闭通知</el-button></div>
      <div v-if="!profile?.subscriptions.length" class="tg-empty">尚未订阅会话</div>
      <small class="tg-muted">持续无输出不代表任务完成。第一版不通过文本猜测 CLI 的完成或批准状态。</small>
    </section>
    <details class="tg-help"><summary>使用方式</summary><p>在机器人中发送 /sessions 选择会话；回复卡片发送需求或上传文件。没有关联会话的消息会先让你选择目标。复杂终端操作可点击“打开终端”。</p><p>服务使用长轮询接收消息，无需配置 webhook。同一个 Bot Token 请只在一个服务实例启用。</p></details>
  </div>
</template>

<style scoped>
.tg-panel { display:flex; flex-direction:column; gap:16px; color:var(--text); min-width:0; }
.tg-card { border:1px solid var(--line); border-radius:12px; padding:18px; background:var(--surface); min-width:0; }
.tg-heading,.tg-bottom { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.tg-heading strong,.tg-subscription strong { font-size:13px; font-weight:600; }
.tg-brand { display:flex; align-items:center; gap:10px; }.tg-brand strong { font-size:17px; }.tg-brand svg { color:var(--muted); }
.tg-panel p { font-size:12px; color:var(--muted); line-height:1.75; margin:10px 0; }
.tg-muted { font-size:11px; color:var(--muted); line-height:1.65; overflow-wrap:anywhere; }
.tg-state { font-size:11px; border:1px solid var(--line); padding:3px 9px; border-radius:20px; color:var(--text); white-space:nowrap; }.tg-state.is-error { color:var(--el-color-danger); }
.tg-config { padding:0; }.tg-config summary { display:flex; justify-content:space-between; gap:12px; padding:17px 18px; cursor:pointer; font-size:13px; font-weight:600; }.tg-config summary span { font-size:11px; font-weight:400; color:var(--muted); }.tg-config .tg-fields { padding:0 18px 18px; }
.tg-fields { display:flex; flex-direction:column; gap:17px; margin-top:16px; }.tg-config .tg-fields { margin-top:0; }
.tg-field { display:flex; flex-direction:column; gap:8px; font-size:12px; }.tg-field small { font-size:11px; color:var(--muted); line-height:1.7; }.tg-field :deep(.el-select),.tg-field :deep(.el-date-editor) { width:100%; }
.tg-actions { display:flex; justify-content:flex-end; align-items:center; flex-wrap:wrap; gap:8px; margin-top:12px; }.tg-actions :deep(.el-button + .el-button) { margin-left:0; }
.tg-permissions :deep(.el-checkbox-group) { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:0 8px; }.tg-permissions :deep(.el-checkbox) { margin-right:0; }.tg-permissions :deep(.el-checkbox__label) { font-size:12px; }
.tg-duration { display:flex; align-items:center; gap:8px; font-size:12px; color:var(--muted); }.tg-duration :deep(.el-input-number) { width:112px; }
.tg-bottom { font-size:12px; }.tg-text-button { background:none; border:0; color:var(--accent); cursor:pointer; padding:0 4px; font-size:11px; }
.tg-pair { display:flex; gap:18px; align-items:center; margin-top:18px; padding-top:18px; border-top:1px solid var(--line); }.tg-qr { padding:8px; border-radius:8px; background:white; flex-shrink:0; line-height:0; }.tg-pair-content { min-width:0; }.tg-pair-content strong { font-size:12px; }.tg-pair-content .tg-actions { justify-content:flex-start; }
.tg-subscription { display:flex; align-items:center; justify-content:space-between; gap:10px; padding:13px 0; border-top:1px solid var(--line); }.tg-subscription > div { min-width:0; display:flex; flex-direction:column; gap:5px; }.tg-subscription strong { overflow-wrap:anywhere; }.tg-subscription small { color:var(--muted); font-size:11px; }.tg-empty { padding:20px 0; text-align:center; font-size:12px; color:var(--muted); }
.tg-help { color:var(--muted); font-size:12px; padding:0 4px; }.tg-help summary { cursor:pointer; }.tg-overview :deep(.el-alert) { margin-top:10px; }
@media(max-width:600px) { .tg-card { padding:14px; }.tg-config { padding:0; }.tg-config summary { padding:15px 14px; }.tg-config .tg-fields { padding:0 14px 14px; }.tg-pair { align-items:flex-start; flex-direction:column; }.tg-pair .tg-qr { align-self:center; }.tg-actions { justify-content:flex-start; }.tg-permissions :deep(.el-checkbox-group) { grid-template-columns:1fr; } }
</style>
