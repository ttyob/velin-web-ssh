import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Mount the real workspace and dialogs. Replace terminal rendering only, so
// transport recovery cannot change the connection states under test.
const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8')
const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)]
  .map(([, path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`)
  .join('\n')
const entry = `
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ElementPlus from 'element-plus'
${styles}
import WorkspaceView from '/src/views/WorkspaceView.vue'
const router = createRouter({ history: createMemoryHistory(), routes: [] })
createApp(WorkspaceView).use(createPinia()).use(router).use(ElementPlus).mount('#app')
`
const pane = `
import { h } from 'vue'
export default {
  props: ['node', 'sessions'],
  emits: ['close', 'context', 'title'],
  setup(props, { emit, expose }) {
    expose({ resize() {}, focusLeaf() {} })
    const leaves = node => node.type === 'leaf' ? [node] : [...leaves(node.first), ...leaves(node.second)]
    return () => h('div', leaves(props.node).map(leaf => h('div', {
      'data-leaf': leaf.id,
      onContextmenu: event => { event.preventDefault(); emit('context', event, leaf.id) },
    }, [
      h('button', { class: 'test-pane-close', 'data-session': leaf.sessionID, onClick: () => emit('close', leaf.id) }, 'Close pane ' + leaf.sessionID),
      h('button', { 'data-title-session': leaf.sessionID, onClick: () => emit('title', leaf.sessionID, 'codex: remote program title') }, 'Update remote title'),
    ])))
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
const renamed = []
let rejectRename = false
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
    else if (path.startsWith('/api/sessions/') && request.method() === 'PATCH') {
      const id = path.split('/').at(-1)
      renamed.push({ id, ...request.postDataJSON() })
      if (rejectRename) return route.fulfill({ status: 503, json: { code: 'save_failed', message: '保存会话名称失败，请重试' } })
      const session = sessions.find(item => item.id === id)
      session.name = request.postDataJSON().name
      session.updatedAt = new Date().toISOString()
      body = session
    }
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

  // Give both tabs and a second split the same host label, as real connections
  // initially do. Each rename must target the selected session, not its host.
  for (const id of ['pane-live', 'dismiss', 'background']) sessions.find(item => item.id === id).name = '开发主机'
  workspace.trees.panes = split('pane-live', 'background')
  workspace.active = 'panes'
  await page.reload()
  await page.locator('.terminal-tab .tab-title').filter({ hasText: /^开发主机$/ }).first().waitFor()
  assert.equal(await page.locator('.terminal-tab .tab-title').filter({ hasText: /^开发主机$/ }).count(), 2)
  const prompt = page.locator('.el-message-box')
  const nameInput = prompt.locator('input')
  const saveName = () => prompt.getByRole('button', { name: '保存', exact: true }).click()
  await page.locator('.terminal-tab .tab-title').first().dblclick()
  await nameInput.fill('   ')
  await saveName()
  await prompt.getByText('会话名称需为 1 到 80 个字符', { exact: true }).waitFor()
  await nameInput.fill('名'.repeat(81))
  await saveName()
  assert.equal(renamed.length, 0)
  await nameInput.fill('  前端开发  ')
  await saveName()
  await tab('前端开发').waitFor()
  assert.deepEqual(renamed.at(-1), { id: 'pane-live', name: '前端开发' })
  await page.locator('[data-title-session="pane-live"]:visible').click()
  assert.equal(await tab('前端开发').count(), 1, 'Remote OSC titles must not overwrite the saved name')
  assert.match(await tab('前端开发').locator('.tab-title').getAttribute('title'), /codex: remote program title/)

  // Right-clicking an inactive tab must target that tab without switching away.
  rejectRename = true
  await tab('开发主机').click({ button: 'right' })
  await page.getByRole('navigation', { name: '标签操作' }).getByRole('button', { name: '重命名会话' }).click()
  await nameInput.fill('后端开发')
  await saveName()
  await page.getByText('保存会话名称失败，请重试', { exact: true }).waitFor()
  assert.equal(await tab('开发主机').count(), 1, 'Failed persistence must preserve the original label')
  rejectRename = false
  await tab('开发主机').click({ button: 'right' })
  await page.getByRole('navigation', { name: '标签操作' }).getByRole('button', { name: '重命名会话' }).click()
  await nameInput.fill('后端开发')
  await saveName()
  await tab('后端开发').waitFor()
  assert.equal(renamed.at(-1).id, 'dismiss')
  assert.equal(await tab('前端开发').evaluate(el => el.classList.contains('active')), true)

  // The terminal menu targets the clicked split, independently of its tab name.
  await page.locator('[data-leaf="leaf-background"]:visible').click({ button: 'right' })
  await page.locator('.pane-context-menu:not(.tab-context-menu)').getByRole('button', { name: '重命名会话' }).click()
  await nameInput.fill('日志排查')
  await saveName()
  await prompt.waitFor({ state: 'hidden' })
  assert.deepEqual(renamed.at(-1), { id: 'background', name: '日志排查' })
  assert.equal(await tab('前端开发').count(), 1)
  await tab('前端开发').locator('.tab-title').dblclick()
  await prompt.getByRole('button', { name: '取消', exact: true }).click()
  assert.equal(renamed.length, 4, 'Cancelling must not send a rename request')
  await page.reload()
  await tab('前端开发').waitFor()
  await tab('后端开发').waitFor()
  assert.equal(sessions.find(item => item.id === 'background').name, '日志排查')
  await page.locator('[data-title-session="pane-live"]:visible').click()
  assert.equal(await tab('前端开发').count(), 1, 'Replayed remote titles must preserve names after reload')
  const shots = new URL('../../artifacts/session-renaming-20261031/', import.meta.url)
  await mkdir(shots, { recursive: true })
  await page.screenshot({ path: new URL('renamed-tabs.png', shots).pathname })
  assert.deepEqual(errors, [])
  console.log('Session checks passed: tab/split closing, cleanup failures, persisted layout, duplicate host labels, tab double-click/context menus, split rename, validation, cancellation, save errors, remote title isolation and persisted names.')
} finally {
  await browser?.close()
  await server.close()
}
