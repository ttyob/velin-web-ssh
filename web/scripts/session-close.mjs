import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Mount the real workspace and dialogs. Replace terminal rendering only, so
// transport recovery cannot change the connection states under test.
const entry = `
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import WorkspaceView from '/src/views/WorkspaceView.vue'
const router = createRouter({ history: createMemoryHistory(), routes: [] })
createApp(WorkspaceView).use(createPinia()).use(router).use(ElementPlus).mount('#app')
`
const pane = `
import { h } from 'vue'
export default {
  props: ['node', 'sessions'],
  emits: ['close'],
  setup(props, { emit, expose }) {
    expose({ resize() {}, focusLeaf() {} })
    const leaves = node => node.type === 'leaf' ? [node] : [...leaves(node.first), ...leaves(node.second)]
    return () => h('div', leaves(props.node).map(leaf => h('button', {
      class: 'test-pane-close', 'data-session': leaf.sessionID,
      onClick: () => emit('close', leaf.id),
    }, 'Close pane ' + leaf.sessionID)))
  },
}
`
const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  server: { host: '127.0.0.1', port: 0, open: false, proxy: {} },
  cacheDir: 'node_modules/.vite-session-close',
  optimizeDeps: {
    noDiscovery: true,
    include: ['vue', 'pinia', 'vue-router', 'element-plus', '@lucide/vue'],
    esbuildOptions: { target: 'esnext' },
  },
  plugins: [{
    name: 'session-close-test',
    enforce: 'pre',
    resolveId(id) {
      if (id === '/__session_close__.js') return id
      if (id.endsWith('/SplitPaneNode.vue')) return '/__session_close_pane__.js'
    },
    load(id) {
      if (id === '/__session_close__.js') return entry
      if (id === '/__session_close_pane__.js') return pane
    },
    configureServer(vite) {
      vite.middlewares.use('/__session_close__.html', (_req, res) => {
        res.setHeader('Content-Type', 'text/html')
        res.end('<div id="app"></div><script type="module" src="/__session_close__.js"></script>')
      })
    },
  }],
})

const leaf = id => ({ type: 'leaf', id: 'leaf-' + id, sessionID: id })
const split = (first, second) => ({ type: 'split', id: 'split-' + first, direction: 'horizontal', ratio: 0.5, first: leaf(first), second: leaf(second) })
const sessions = [
  ['unreachable', 'unreachable'], ['auth', 'auth_required'],
  ['connecting', 'reconnecting'], ['ended', 'ended'],
  ['mixed-ended', 'ended'], ['mixed-live', 'attached'], ['mixed-failed', 'unreachable'],
  ['pane-failed', 'ownership_error'], ['pane-live', 'attached'],
  ['background', 'attached'], ['dismiss', 'attached'], ['local-only', 'unreachable'],
].map(([id, status]) => ({
  id, status, name: id, hostID: 'test', sessionMode: 'tmux',
  updatedAt: new Date().toISOString(),
}))
let workspace = {
  tabs: ['unreachable', 'auth', 'connecting', 'ended', 'mixed', 'panes', 'background', 'dismiss', 'local-only'],
  active: 'unreachable',
  trees: {
    unreachable: leaf('unreachable'), auth: leaf('auth'), connecting: leaf('connecting'),
    ended: leaf('ended'),
    mixed: { ...split('mixed-ended', 'mixed-live'), second: split('mixed-live', 'mixed-failed') },
    panes: split('pane-failed', 'pane-live'),
    background: leaf('background'), dismiss: leaf('dismiss'), 'local-only': leaf('local-only'),
  },
}
const deleted = []
const pending = new Map()
let version = 0
let browser
try {
  await server.listen()
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
    headless: true, args: ['--no-sandbox'],
  })
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })
  const errors = []
  page.on('pageerror', error => { errors.push(error.message); console.error(error) })
  await page.route('**/api/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    let body = {}
    if (path === '/api/csrf') body = { token: 'session-close-test' }
    else if (path === '/api/sessions' && request.method() === 'GET') body = sessions
    else if (path.startsWith('/api/sessions/') && request.method() === 'DELETE') {
      const id = path.split('/').at(-1)
      deleted.push(id)
      if (id === 'local-only') {
        sessions.find(session => session.id === id).status = 'ended'
        return route.fulfill({ json: { ok: true, remoteTerminated: false, warning: '本地会话已关闭，但无法确认远程任务已终止。' } })
      }
      if (id === 'unreachable') await new Promise(resolve => pending.set(id, resolve))
      if (['unreachable', 'auth', 'mixed-failed', 'pane-failed'].includes(id))
        return route.fulfill({ status: 502, json: { code: 'terminate_failed', message: id === 'auth' ? 'credential required' : 'SSH connection unavailable' } })
      if (id === 'connecting') return route.abort('failed')
      sessions.find(session => session.id === id).status = 'ended'
      body = { ok: true }
    } else if (path === '/api/workspace') {
      if (request.method() === 'PUT') {
        workspace = request.postDataJSON().layout
        version++
      }
      body = { layout: workspace, version }
    } else if (['/api/hosts', '/api/credentials', '/api/web-services'].includes(path)) body = []
    else if (path === '/api/auth/lock-pin') body = { configured: false }
    return route.fulfill({ json: body })
  })
  const address = server.httpServer.address()
  const url = `http://127.0.0.1:${address.port}/__session_close__.html`
  await page.goto(url)
  const tab = name => page.locator('.terminal-tab').filter({ has: page.locator('.tab-title', { hasText: new RegExp('^' + name + '$') }) })
  const confirm = async (name, button = '全部终止并关闭') => {
    await tab(name).locator('.tab-close').click()
    await page.getByRole('button', { name: button, exact: true }).click()
    await tab(name).waitFor({ state: 'hidden' })
  }
  await page.locator('.terminal-tab').first().waitFor()

  // Remote deletion can hang or fail, but the tab closes immediately.
  await confirm('unreachable')
  assert(pending.has('unreachable'), 'remote cleanup request was not started')
  assert(!deleted.includes('auth'))
  pending.get('unreachable')()
  await page.getByText('标签已关闭，但有 1 个会话未能保存终止状态，请检查服务连接后重试。', { exact: true }).first().waitFor()

  // A reachable backend closes stale local state even when the remote host
  // is unreachable, and reports that remote tasks could not be confirmed.
  await confirm('local-only')
  await page.getByText('会话已关闭，其中 1 个远程会话无法确认任务已终止。', { exact: true }).first().waitFor()
  assert.equal(sessions.find(session => session.id === 'local-only').status, 'ended')

  // Missing credentials and network failures must not block closing or open
  // another authentication dialog after the pane is already gone.
  await confirm('auth')
  await confirm('connecting')
  assert.equal(await page.locator('.el-message-box__input:visible').count(), 0)

  // Ended tabs require no remote request or confirmation.
  await tab('ended').locator('.tab-close').click()
  await tab('ended').waitFor({ state: 'hidden' })
  assert(!deleted.includes('ended'))

  // A mixed tab must close completely, including ended leaves, even when
  // remote cleanup succeeds for only some of its sessions.
  await confirm('mixed-ended')
  assert(deleted.includes('mixed-live'))
  assert(deleted.includes('mixed-failed'))
  assert(!deleted.includes('mixed-ended'))

  // Closing one failed split must keep the other pane and its focus usable.
  await tab('pane-failed').click()
  await page.locator('[data-session="pane-failed"]').click()
  await page.getByRole('button', { name: '终止并关闭', exact: true }).click()
  await page.locator('[data-session="pane-failed"]').waitFor({ state: 'hidden' })
  await tab('pane-live').waitFor()
  assert.equal(await page.locator('[data-session="pane-live"]:visible').count(), 1)

  // Background remains an explicit option. Dismissing the dialog keeps the
  // tab open and must never issue a termination request.
  await confirm('background', '全部移到后台')
  assert(!deleted.includes('background'))
  await tab('dismiss').locator('.tab-close').click()
  await page.locator('.el-message-box__headerbtn').click()
  await page.locator('.el-message-box').waitFor({ state: 'hidden' })
  assert.equal(await tab('dismiss').count(), 1)
  assert(!deleted.includes('dismiss'))

  // Verify the actual save path: closed tabs must stay closed after reload,
  // while unsuccessfully terminated sessions keep their truthful status.
  await page.waitForFunction(async () => {
    const saved = await fetch('/api/workspace').then(response => response.json())
    return JSON.stringify(saved.layout.tabs) === '["panes","dismiss"]'
  }, undefined, { polling: 100 })
  assert.deepEqual(workspace.tabs, ['panes', 'dismiss'])
  assert.equal(sessions.find(session => session.id === 'auth').status, 'auth_required')
  await page.reload()
  await tab('pane-live').waitFor()
  assert.equal(await page.locator('.terminal-tab').count(), 2)
  assert.deepEqual(errors, [])
  console.log('Session tab and split closing, cleanup failures and persisted layout browser checks passed')
} finally {
  await browser?.close()
  await server.close()
}
