<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Copy, KeyRound, Pencil, Plus, RefreshCw, Trash2 } from '@lucide/vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, json } from '../../api'
import type { Host, TerminalSession } from '../../types'

type Permission = 'read' | 'input' | 'create' | 'manage'
interface AgentToken {
  id: string; name: string; hint: string; permissions: Permission[]
  sessionIDs: string[]; hostIDs: string[]; enabled: boolean
  expiresAt: string | null; lastUsedAt: string | null; createdAt: string
}
interface TokenForm {
  name: string; permissions: Permission[]; sessionIDs: string[]; hostIDs: string[]
  enabled: boolean; expiresAt: string | null
}
const permissionLabels: Record<Permission, string> = { read: '读取输出', input: '发送输入', create: '创建 / 恢复', manage: '重命名 / 终止' }
const tokens = ref<AgentToken[]>([])
const hosts = ref<Host[]>([])
const sessions = ref<TerminalSession[]>([])
const endpoint = ref('/mcp/sessions')
const loading = ref(false), saving = ref(false), error = ref(''), busyID = ref('')
const formOpen = ref(false), editingID = ref(''), formError = ref('')
const created = ref<{ token: AgentToken; secret: string } | null>(null)
const secretOpen = ref(false)
const serverOrigin = ref(window.location.origin)
const endpointURL = computed(() => `${serverOrigin.value.trim().replace(/\/+$/, '')}${endpoint.value}`)
const form = ref<TokenForm>(emptyForm())
const sshHosts = computed(() => hosts.value.filter(host => !host.protocol || host.protocol === 'ssh'))
const config = computed(() => JSON.stringify({ mcp: { servers: { velin: {
  url: endpointURL.value, transport: 'streamable-http', enabled: true,
  headers: { Authorization: `Bearer ${created.value?.secret || '<在此填写专用令牌>'}` },
  requestTimeoutMs: 30000,
} } } }, null, 2))

function emptyForm(): TokenForm { return { name: '', permissions: ['read'], sessionIDs: [], hostIDs: [], enabled: true, expiresAt: null } }
function dateLabel(value: string | null) { return value ? new Date(value).toLocaleString() : '—' }
function scopeLabel(token: AgentToken) {
  return [token.sessionIDs.length ? `${token.sessionIDs.length} 个会话` : '', token.hostIDs.length ? `${token.hostIDs.length} 台主机` : ''].filter(Boolean).join(' · ')
}
function expired(token: AgentToken) { return !!token.expiresAt && new Date(token.expiresAt).getTime() <= Date.now() }
function payload(value: TokenForm) {
  return { name: value.name, permissions: value.permissions, sessionIDs: value.sessionIDs,
    hostIDs: value.hostIDs, enabled: value.enabled,
    expiresAt: value.expiresAt ? new Date(value.expiresAt).toISOString() : null }
}
async function load() {
  loading.value = true; error.value = ''
  try {
    const [data, hostList, sessionList] = await Promise.all([
      api<{ tokens: AgentToken[]; endpoint: string }>('/api/session-bridge/tokens'),
      api<Host[]>('/api/hosts'), api<TerminalSession[]>('/api/sessions'),
    ])
    tokens.value = data.tokens; endpoint.value = data.endpoint
    hosts.value = hostList; sessions.value = sessionList
  } catch (e) { error.value = e instanceof Error ? e.message : '加载失败' }
  finally { loading.value = false }
}
function openForm(token?: AgentToken) {
  editingID.value = token?.id || ''; formError.value = ''
  form.value = token ? { name: token.name, permissions: [...token.permissions], sessionIDs: [...token.sessionIDs], hostIDs: [...token.hostIDs], enabled: token.enabled, expiresAt: token.expiresAt } : emptyForm()
  formOpen.value = true
}
async function save() {
  if (!form.value.name.trim()) { formError.value = '请填写令牌名称'; return }
  if (!form.value.sessionIDs.length && !form.value.hostIDs.length) { formError.value = '请选择至少一个会话或主机'; return }
  if (form.value.permissions.includes('create') && !form.value.hostIDs.length) { formError.value = '创建会话需要授权至少一台主机'; return }
  if (form.value.expiresAt && new Date(form.value.expiresAt).getTime() <= Date.now()) { formError.value = '请选择未来的过期时间，或清空以长期有效'; return }
  saving.value = true; formError.value = ''
  try {
    if (editingID.value) {
      await api(`/api/session-bridge/tokens/${editingID.value}`, { method: 'PUT', body: json(payload(form.value)) })
      ElMessage.success('授权已更新')
    } else {
      created.value = await api<{ token: AgentToken; secret: string }>('/api/session-bridge/tokens', { method: 'POST', body: json(payload(form.value)) })
      secretOpen.value = true
    }
    formOpen.value = false
    await load()
  } catch (e) { formError.value = e instanceof Error ? e.message : '保存失败' }
  finally { saving.value = false }
}
async function toggle(token: AgentToken) {
  busyID.value = token.id
  try {
    const updated = await api<AgentToken>(`/api/session-bridge/tokens/${token.id}`, { method: 'PUT', body: json(payload({ ...token, enabled: !token.enabled })) })
    tokens.value = tokens.value.map(item => item.id === token.id ? updated : item)
    ElMessage.success(updated.enabled ? '令牌已启用' : '令牌已停用')
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : '更新失败') }
  finally { busyID.value = '' }
}
async function remove(token: AgentToken) {
  try {
    await ElMessageBox.confirm(`删除“${token.name}”后，使用此令牌的 Agent 将失去访问权限。`, '删除令牌', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    busyID.value = token.id
    await api(`/api/session-bridge/tokens/${token.id}`, { method: 'DELETE' })
    tokens.value = tokens.value.filter(item => item.id !== token.id)
    ElMessage.success('令牌已删除')
  } catch (e) { if (e !== 'cancel' && e !== 'close') ElMessage.error(e instanceof Error ? e.message : '删除失败') }
  finally { busyID.value = '' }
}
async function copy(value: string) {
  try {
    if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(value)
    else {
      const field = document.createElement('textarea')
      field.value = value; field.style.cssText = 'position:fixed;left:0;top:0;opacity:0;pointer-events:none'
      document.body.appendChild(field)
      try { field.focus(); field.select(); if (!document.execCommand('copy')) throw new Error('复制未成功，请手动选中文本复制') }
      finally { field.remove() }
    }
    ElMessage.success('已复制')
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : '复制失败，请手动选中文本复制') }
}
onMounted(load)
</script>

<template>
  <div class="bridge-panel">
    <section class="bridge-connect">
      <div class="bridge-section-heading"><strong>MCP 接入</strong><span class="bridge-chip">OpenClaw</span></div>
      <p>让 Agent 查看终端输出、发送输入。每个令牌分别选择会话范围与操作权限。</p>
      <div class="bridge-endpoint"><code>{{ endpointURL }}</code><el-button :icon="Copy" text aria-label="复制接入地址" @click="copy(endpointURL)" /></div>
      <details class="bridge-details">
        <summary>OpenClaw 配置与接入说明</summary>
        <label class="bridge-field">服务地址<el-input v-model="serverOrigin" placeholder="OpenClaw 能访问的 Velin 地址" /></label>
        <p>将配置合并到 OpenClaw 的配置文件。创建令牌后，可复制包含令牌的完整配置。</p>
        <pre>{{ config }}</pre>
        <el-button :icon="Copy" size="small" @click="copy(config)">复制配置模板</el-button>
        <p>保存后执行 <code>openclaw mcp doctor velin --probe</code> 检查连接。</p>
        <p>读取保留原终端的控制权和尺寸。终端静默不代表任务完成，Agent 需要结合输出判断进度。</p>
      </details>
    </section>

    <div class="bridge-section-heading bridge-token-heading"><strong>访问令牌 <span class="bridge-count">{{ tokens.length }}</span></strong><div><el-button :icon="RefreshCw" text :loading="loading" aria-label="刷新令牌列表" @click="load" /><el-button :icon="Plus" type="primary" size="small" @click="openForm()">创建令牌</el-button></div></div>
    <div v-if="error" class="bridge-error" role="alert">{{ error }}<el-button text size="small" @click="load">重试</el-button></div>
    <div v-if="loading && !tokens.length" class="bridge-empty">正在加载…</div>
    <div v-else-if="!error && !tokens.length" class="bridge-empty"><KeyRound :size="26" /><strong>尚未创建访问令牌</strong><span>创建一个令牌，在 OpenClaw 中连接此工作台。</span></div>
    <div v-for="token in tokens" :key="token.id" class="bridge-token" :data-token-id="token.id">
      <div class="bridge-section-heading"><strong>{{ token.name }} <small>{{ token.hint }}</small></strong><span class="bridge-status" :class="{ off: !token.enabled || expired(token) }">{{ expired(token) ? '已过期' : token.enabled ? '已启用' : '已停用' }}</span></div>
      <div class="bridge-grants"><span v-for="permission in token.permissions" :key="permission">{{ permissionLabels[permission] }}</span></div>
      <p>{{ scopeLabel(token) }}</p>
      <div class="bridge-token-footer"><div><span>最近使用 {{ dateLabel(token.lastUsedAt) }}</span><span>{{ token.expiresAt ? `到期 ${dateLabel(token.expiresAt)}` : '长期有效' }}</span></div><div class="bridge-token-actions"><el-button size="small" text :disabled="!!busyID" @click="toggle(token)">{{ token.enabled ? '停用' : '启用' }}</el-button><el-button :icon="Pencil" size="small" text aria-label="编辑令牌" :disabled="!!busyID" @click="openForm(token)" /><el-button :icon="Trash2" size="small" text type="danger" aria-label="删除令牌" :disabled="!!busyID" @click="remove(token)" /></div></div>
    </div>

    <el-dialog v-model="formOpen" :title="editingID ? '编辑授权' : '创建访问令牌'" width="540px" append-to-body destroy-on-close :close-on-click-modal="false" class="bridge-dialog">
      <form class="bridge-form" @submit.prevent="save">
        <label class="bridge-field">名称<el-input v-model="form.name" maxlength="80" placeholder="例如：随身开发助手" autocomplete="off" /></label>
        <label class="bridge-field">允许访问的会话<el-select v-model="form.sessionIDs" multiple filterable collapse-tags collapse-tags-tooltip placeholder="选择指定会话"><el-option v-for="session in sessions" :key="session.id" :value="session.id" :label="`${session.name || session.id.slice(0, 8)} · ${hosts.find(host => host.id === session.hostID)?.name || session.hostID} · ${session.id.slice(0, 6)}`" /><el-option v-for="id in form.sessionIDs.filter(id => !sessions.some(item => item.id === id))" :key="id" :value="id" :label="`已移除的会话 ${id.slice(0, 8)}`" /></el-select></label>
        <label class="bridge-field">允许访问的主机<el-select v-model="form.hostIDs" multiple filterable collapse-tags collapse-tags-tooltip placeholder="按需选择 SSH 主机"><el-option v-for="host in sshHosts" :key="host.id" :value="host.id" :label="`${host.name} · ${host.address}`" /><el-option v-for="id in form.hostIDs.filter(id => !sshHosts.some(item => item.id === id))" :key="id" :value="id" :label="`已移除的主机 ${id.slice(0, 8)}`" /></el-select><small>主机授权包含该主机现有和后续创建的会话，与指定会话取并集。</small></label>
        <div class="bridge-field"><span>操作权限</span><el-checkbox-group v-model="form.permissions" class="bridge-permissions"><el-checkbox value="read" disabled>读取输出</el-checkbox><el-checkbox value="input">发送输入</el-checkbox><el-checkbox value="create">创建 / 恢复</el-checkbox><el-checkbox value="manage">重命名 / 终止</el-checkbox></el-checkbox-group><small v-if="form.permissions.includes('input')">发送输入可以执行终端命令，请仅授权给你信任的 Agent。</small></div>
        <label class="bridge-field">到期时间<el-date-picker v-model="form.expiresAt" type="datetime" placeholder="长期有效" value-format="YYYY-MM-DDTHH:mm:ssZ" clearable /></label>
        <div class="bridge-section-heading"><span>启用令牌</span><el-switch v-model="form.enabled" aria-label="启用令牌" /></div>
        <p v-if="formError" class="bridge-error" role="alert">{{ formError }}</p>
      </form>
      <template #footer><el-button :disabled="saving" @click="formOpen = false">取消</el-button><el-button type="primary" :loading="saving" @click="save">{{ editingID ? '保存授权' : '创建令牌' }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="secretOpen" title="令牌已创建" width="580px" append-to-body :close-on-click-modal="false" destroy-on-close @closed="created = null">
      <div v-if="created" class="bridge-secret">
        <p>令牌仅在此展示一次，请立即保存。</p>
        <div class="bridge-endpoint"><code data-testid="agent-secret">{{ created.secret }}</code><el-button :icon="Copy" text aria-label="复制令牌" @click="copy(created.secret)" /></div>
        <label class="bridge-field">OpenClaw 能访问的服务地址<el-input v-model="serverOrigin" /></label>
        <pre>{{ config }}</pre>
        <el-button :icon="Copy" type="primary" @click="copy(config)">复制 OpenClaw 配置</el-button>
        <small>保存配置后执行 openclaw mcp doctor velin --probe 检查连接。</small>
      </div>
      <template #footer><el-button @click="secretOpen = false">已保存，关闭</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.bridge-panel { display:flex; flex-direction:column; gap:16px; min-width:0; color:var(--text); }
.bridge-connect,.bridge-token { border:1px solid var(--line); border-radius:12px; padding:18px; background:var(--surface); min-width:0; }
.bridge-section-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.bridge-section-heading strong { font-size:14px; min-width:0; overflow-wrap:anywhere; }
.bridge-panel p,.bridge-secret p { color:var(--muted); font-size:12px; line-height:1.7; margin:10px 0; }
.bridge-chip,.bridge-status,.bridge-grants span { font-size:11px; border:1px solid var(--line); padding:3px 8px; border-radius:6px; white-space:nowrap; color:var(--muted); }
.bridge-status { color:var(--accent); } .bridge-status.off { color:var(--muted); opacity:.7; }
.bridge-count,.bridge-token small { font-weight:400; color:var(--muted); margin-left:6px; font-size:11px; }
.bridge-endpoint { display:flex; align-items:center; gap:8px; border:1px solid var(--line); border-radius:8px; padding:8px 10px; background:var(--bg); }
.bridge-endpoint code { flex:1; min-width:0; overflow-wrap:anywhere; font-size:12px; user-select:text; }
.bridge-endpoint .el-button { flex-shrink:0; }
.bridge-details { margin-top:14px; color:var(--muted); font-size:12px; }
.bridge-details summary { cursor:pointer; user-select:none; }
.bridge-details .bridge-field { margin-top:14px; }
.bridge-panel pre,.bridge-secret pre { font-size:11px; line-height:1.65; overflow:auto; max-height:300px; padding:14px; border-radius:8px; background:var(--bg); border:1px solid var(--line); user-select:text; color:var(--text); }
.bridge-token-heading { margin-top:8px; }
.bridge-token-heading > div { display:flex; align-items:center; gap:6px; }
.bridge-grants { display:flex; flex-wrap:wrap; gap:6px; margin-top:14px; }
.bridge-token-footer { display:flex; align-items:flex-end; justify-content:space-between; gap:10px; }
.bridge-token-footer > div:first-child { display:flex; flex-direction:column; gap:6px; color:var(--muted); font-size:11px; }
.bridge-token-actions { display:flex; align-items:center; flex-shrink:0; }
.bridge-token-actions .el-button + .el-button { margin-left:2px; }
.bridge-empty { display:flex; align-items:center; gap:10px; flex-direction:column; padding:38px 16px; color:var(--muted); text-align:center; font-size:12px; }
.bridge-empty strong { font-size:14px; font-weight:500; }
.bridge-form,.bridge-secret { display:flex; flex-direction:column; gap:18px; }
.bridge-field { display:flex; flex-direction:column; gap:8px; font-size:12px; color:var(--text); }
.bridge-field small,.bridge-secret small { font-size:11px; line-height:1.6; color:var(--muted); }
.bridge-field :deep(.el-select),.bridge-field :deep(.el-date-editor) { width:100%; }
.bridge-permissions { display:grid; grid-template-columns:1fr 1fr; }
.bridge-permissions .el-checkbox { margin-right:8px; }
.bridge-error { font-size:12px; line-height:1.6; color:var(--el-color-danger); margin:0; }
@media(max-width:600px) { .bridge-connect,.bridge-token { padding:14px; } .bridge-token-footer { align-items:flex-start; flex-direction:column; } .bridge-token-actions { align-self:flex-end; } }
</style>
