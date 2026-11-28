import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Exercise the real workspace, terminal, fingerprint prompt and tmux guide.
// Only HTTP and WebSocket peers are simulated; no production credentials are used.
const entry = `
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import '@xterm/xterm/css/xterm.css'
import '/src/styles.css'
import '/src/styles-refresh.css'
import '/src/styles-polish.css'
import '/src/styles-dialogs.css'
import WorkspaceView from '/src/views/WorkspaceView.vue'
window.testSockets = []
class TestSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 3
  readyState = 0
  messages = []
  attached = false
  constructor(url) {
    this.url = url
    window.testSockets.push(this)
    queueMicrotask(() => {
      this.readyState = 1
      this.onopen?.()
      this.receive({ type: 'hello', status: window.socketStatus || 'attached', clientID: 'test', controller: 'test' })
      this.receive({ type: 'replay_end' })
    })
  }
  receive(message) {
    if (message.status === 'attached') this.attached = true
    this.onmessage?.({ data: JSON.stringify(message) })
  }
  send(data) {
    const message = JSON.parse(data)
    this.messages.push(message)
    if (message.type === 'ping') this.receive({ type: 'pong' })
    // Like SSH, discard geometry sent before the remote PTY is attached.
    if (window.socketStatus === 'creating' && this.attached && message.type === 'resize') {
      this.receive({ type: 'output', data: btoa('\x1b[2J\x1b[' + message.rows + ';1Hssh-ready-bottom$ ') })
    }
  }
  close() {
    if (this.readyState === 3) return
    this.readyState = 3
    queueMicrotask(() => this.onclose?.())
  }
}
window.WebSocket = TestSocket
const router = createRouter({ history: createMemoryHistory(), routes: [] })
createApp(WorkspaceView).use(createPinia()).use(router).use(ElementPlus).mount('#app')
`
const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  cacheDir: 'node_modules/.vite-connection-onboarding',
  server: { host: '127.0.0.1', port: 0, open: false },
  optimizeDeps: {
    noDiscovery: true,
    include: [
      'vue', 'pinia', 'vue-router', 'element-plus', '@lucide/vue', 'qrcode.vue',
      '@xterm/xterm', '@xterm/addon-canvas', '@xterm/addon-fit',
      '@xterm/addon-search', '@xterm/addon-web-links',
    ],
    esbuildOptions: { target: 'esnext' },
  },
  plugins: [{
    name: 'connection-onboarding-test',
    enforce: 'pre',
    resolveId(id) { if (id === '/__connection_onboarding__.js') return id },
    load(id) { if (id === '/__connection_onboarding__.js') return entry },
    transform(source, id) {
      if (id.endsWith('/src/components/TerminalPane.vue')) {
        return source.replace('terminal.open(container.value!);',
          'terminal.open(container.value!); window.testTerminal = terminal;')
      }
    },
    configureServer(vite) {
      vite.middlewares.use('/__connection_onboarding__.html', (_req, res) => {
        res.setHeader('Content-Type', 'text/html')
        res.end('<div id="app"></div><script type="module" src="/__connection_onboarding__.js"></script>')
      })
    },
  }],
})

const fingerprint = 'SHA256:test-fingerprint'
const host = {
  id: 'host-test', name: 'First connection', address: 'test.invalid', port: 22,
  username: 'test', protocol: 'ssh', sessionMode: 'tmux', credentialID: '',
  hasPassword: true, groupName: '', tags: '', platform: 'linux', distribution: 'debian',
}
let browser
let baseURL
async function scenario(options, verify) {
  const fixture = { trusted: false, tmux: true, secretRequired: false, ...options }
  const context = await browser.newContext({ viewport: { width: 1400, height: 900 } })
  const page = await context.newPage()
  const errors = []
  const probes = [], creates = [], restores = [], deletions = [], fileRequests = [], hostSaves = [], credentialSaves = [], webSaves = []
  const credentials = [...(fixture.credentials || [])]
  const webServices = [...(fixture.webServices || [])]
  const sessions = fixture.restore ? [{
    id: 'restored', hostID: host.id, name: 'Restored terminal',
    sessionMode: 'tmux', status: 'background', lastError: '',
  }] : []
  const layout = fixture.restore ? {
    tabs: ['restored'], active: 'restored',
    trees: { restored: { type: 'leaf', id: 'restored-leaf', sessionID: 'restored' } },
  } : { tabs: [] }
  page.on('pageerror', error => { errors.push(error.message); console.error(error) })
  await page.addInitScript(status => { window.socketStatus = status }, fixture.creating ? 'creating' : 'attached')
  await page.addInitScript(() => {
    window.open = url => {
      window.__openedWebService = url
      return { opener: null, close() {}, location: { replace(next) { window.__openedWebService = next } } }
    }
  })
  await page.clock.install()
  await page.route('**/api/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const body = request.postDataJSON() || {}
    const reject = (status, code, message, extra = {}) => route.fulfill({ status, json: { code, message, ...extra } })
    const keyError = () => reject(409, 'unknown_host_key', '请确认远程主机指纹', {
      fingerprint, hostName: host.name, hostAddress: 'test.invalid:22',
    })
    if (path === '/api/csrf') return route.fulfill({ json: { token: 'onboarding-test' } })
    if (path === '/api/hosts' && request.method() === 'GET') return route.fulfill({ json: fixture.hosts || [host] })
    if (path === '/api/hosts' && request.method() === 'POST') {
      hostSaves.push(body)
      return route.fulfill({ status: 201, json: { ...body, id: 'host-saved-' + hostSaves.length, hasPassword: Boolean(body.password) } })
    }
    if (/^\/api\/hosts\/[^/]+$/.test(path) && request.method() === 'PUT') {
      hostSaves.push(body)
      return route.fulfill({ json: { ...body, hasPassword: Boolean(body.password || body.hasPassword) } })
    }
    if (path === '/api/credentials' && request.method() === 'GET') return route.fulfill({ json: credentials })
    if (path === '/api/credentials' && request.method() === 'POST') {
      const created = { id: 'cred-' + (credentials.length + 1), name: body.name, kind: body.kind }
      credentialSaves.push(body)
      credentials.push(created)
      return route.fulfill({ status: 201, json: created })
    }
    if (/^\/api\/web-services\/[^/]+\/open$/.test(path) && request.method() === 'POST') {
      return route.fulfill({ json: { url: '/proxy/web-test' } })
    }
    if (path.startsWith('/api/sftp/')) {
      fileRequests.push({ path, session: new URL(request.url()).searchParams.get('session') })
      return route.fulfill({ json: { path: '/srv/project', entries: [] } })
    }
    if (/^\/api\/hosts\/[^/]+\/test$/.test(path)) {
      probes.push(body)
      if (fixture.holdProbe) await new Promise(resolve => { fixture.releaseProbe = resolve })
      if (fixture.probeFailure) return reject(502, "connection_failed", fixture.probeFailure)
      if (fixture.secretRequired && !body.secret) return reject(502, 'connection_failed', 'SSH credential required')
      if (!fixture.trusted) {
        if (body.trustFingerprint !== fingerprint) return keyError()
        fixture.trusted = true
      }
      if (!fixture.tmux) return reject(502, 'tmux_missing', 'tmux is required on the remote host')
      return route.fulfill({ json: { latencyMs: 1, sessionMode: 'tmux', tmuxVersion: 'tmux 3.3' } })
    }
    if (path === '/api/sessions' && request.method() === 'GET') return route.fulfill({ json: sessions })
    if (path === '/api/sessions' && request.method() === 'POST') {
      creates.push(body)
      const session = { id: 'created-' + creates.length, hostID: body.hostID, name: body.name || host.name, sessionMode: body.sessionMode || 'tmux', status: fixture.creating ? 'creating' : 'attached' }
      sessions.push(session)
      if (fixture.holdCreate) await new Promise(resolve => { fixture.releaseCreate = resolve })
      return route.fulfill({ status: 201, json: session })
    }
    if (path.startsWith('/api/sessions/') && request.method() === 'DELETE') {
      deletions.push(path); return route.fulfill({ json: { ok: true } })
    }
    if (path.endsWith('/restore')) {
      restores.push(body)
      if (fixture.challengeRestore && !fixture.trusted) {
        if (body.trustFingerprint !== fingerprint) return keyError()
        fixture.trusted = true
        if (fixture.failAfterTrust) return reject(502, 'restore_failed', 'Remote retry failed')
      }
      return route.fulfill({ json: {} })
    }
    if (path === '/api/workspace') return route.fulfill({ json: { layout, version: 1 } })
    if (path === '/api/auth/lock-pin') return route.fulfill({ json: { configured: false } })
    if (path === '/api/web-services' && request.method() === 'GET') return route.fulfill({ json: webServices })
    if (path === '/api/web-services' && request.method() === 'POST') {
      const saved = { ...body, id: 'web-' + (webServices.length + 1) }
      webSaves.push(body)
      webServices.push(saved)
      return route.fulfill({ status: 201, json: saved })
    }
    if (/^\/api\/web-services\/[^/]+$/.test(path) && request.method() === 'PUT') {
      const id = path.split('/').pop()
      const saved = { ...body, id }
      webSaves.push(body)
      const idx = webServices.findIndex(item => item.id === id)
      if (idx >= 0) webServices[idx] = saved
      return route.fulfill({ json: saved })
    }
    return route.fulfill({ json: {} })
  })
  try {
    await page.goto(baseURL)
    await page.locator('.host-row').first().waitFor()
    await verify({ page, fixture, probes, creates, restores, deletions, fileRequests, hostSaves, credentialSaves, webSaves })
    assert.deepEqual(errors, [])
  } finally {
    await context.close()
  }
}
const fingerprintDialog = page => page.getByRole('dialog', { name: '确认主机指纹', exact: true }).or(page.getByRole('region', { name: '确认主机指纹', exact: true }))
async function assertWaiting(page) {
  await fingerprintDialog(page).waitFor()
  await page.clock.runFor(5000)
  assert.equal(await page.locator('.terminal-error:visible').count(), 0, 'waiting must not be covered by a failure overlay')
  const button = fingerprintDialog(page).getByRole('button', { name: '信任并连接' })
  assert(await button.evaluate(element => {
    const rect = element.getBoundingClientRect()
    return element.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2))
  }), 'fingerprint confirmation must remain clickable')
}
async function connected(page) {
  await page.locator('.terminal-statusbar .status-dot.attached').last().waitFor()
  assert.equal(await page.locator('.terminal-error:visible').count(), 0)
}

try {
  await server.listen()
  baseURL = `http://127.0.0.1:${server.httpServer.address().port}/__connection_onboarding__.html`
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'] })

  await scenario({ trusted: true, restore: true }, async ({ page, fileRequests }) => {
    const row = page.locator('.host-row').first()
    const rowBox = await row.boundingBox()
    const pointer = { x: rowBox.x + rowBox.width * 0.58, y: rowBox.y + rowBox.height * 0.5 }
    await page.mouse.click(pointer.x, pointer.y, { button: 'right' })
    const menu = page.getByRole('menu', { name: '主机操作', exact: true })
    await menu.waitFor()
    const menuBox = await menu.boundingBox()
    assert(Math.abs(menuBox.x - pointer.x) <= 2 && Math.abs(menuBox.y - pointer.y) <= 2, 'host menu must open at the pointer')
    await menu.screenshot({ path: '/tmp/velin-host-menu-compact.png' })
    assert.deepEqual(await menu.getByRole('menuitem').allTextContents(), ['SFTP', '测试连接', '编辑', '复制主机', '删除'])
    assert.equal(await menu.getByRole('menuitem', { name: '连接', exact: true }).count(), 0)
    assert.equal(await menu.getByRole('menuitem', { name: '访问内网 Web', exact: true }).count(), 0)
    await page.keyboard.press('Escape')
    await menu.waitFor({ state: 'detached' })
    await page.mouse.click(pointer.x, pointer.y, { button: 'right' })
    await menu.getByRole('menuitem', { name: 'SFTP', exact: true }).click()
    const dialog = page.locator('.file-manager-dialog:visible')
    await dialog.waitFor()
    await page.waitForFunction(() => document.querySelector('.explorer-file-list'))
    assert(fileRequests.some(request => request.session === 'restored'), 'host SFTP must reuse the active host session')
    await dialog.locator('.el-dialog__headerbtn').click()
  })

  await scenario({
    trusted: true,
    hosts: [{ ...host, groupName: '生产/华东', tags: 'web, api', lastStatus: 'online', lastLatencyMs: 14 }],
  }, async ({ page, hostSaves, credentialSaves, creates }) => {
    await page.locator('.host-list-tools button[title="新增主机"]').click()
    const dialog = page.locator('.host-dialog:visible')
    await dialog.waitFor()
    // Clicking backdrop must not close the dialog.
    await page.mouse.click(12, 12)
    assert.equal(await dialog.count(), 1)
    // Inline validation on empty submit.
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    assert.equal(hostSaves.length, 0)
    assert.match(await dialog.locator('.hd-field-error').first().innerText(), /主机地址/)

    // Smart paste of ssh CLI command populates address, port and username.
    const addrInput = dialog.getByPlaceholder(/IP \/ 域名/)
    await addrInput.evaluate(element => {
      const data = new DataTransfer()
      data.setData('text/plain', 'ssh -p 2222 deploy@10.20.30.40')
      element.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
    })
    assert.equal(await addrInput.inputValue(), '10.20.30.40')
    assert.equal(await dialog.locator('.hd-addr input').nth(1).inputValue(), '2222')
    assert.equal(await dialog.getByPlaceholder('root').inputValue(), 'deploy')

    await dialog.screenshot({ path: '/tmp/velin-host-dialog-basic.png' })

    // Inline credential creation.
    await dialog.getByText('已存凭据', { exact: true }).click()
    await dialog.getByRole('button', { name: '新建凭据' }).click()
    const credDialog = page.locator('.credential-dialog:visible')
    await credDialog.waitFor()
    await credDialog.locator('input').first().fill('Prod Key')
    await credDialog.getByText('私钥', { exact: true }).click()
    await credDialog.locator('textarea').fill('-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----')
    await credDialog.getByRole('button', { name: '保存', exact: true }).click()
    await credDialog.waitFor({ state: 'hidden' })
    assert.equal(credentialSaves.length, 1)

    // Expand collapsible extra settings for SSH and verify RDP native jump-host hiding.
    const moreToggle = dialog.getByRole('button', { name: /更多设置/ })
    assert.equal(await moreToggle.getAttribute('aria-expanded'), 'false')
    await moreToggle.click()
    assert.equal(await moreToggle.getAttribute('aria-expanded'), 'true')
    await dialog.screenshot({ path: '/tmp/velin-host-dialog-adv.png' })
    await dialog.getByRole('radio', { name: /RDP 桌面/ }).click()
    await dialog.screenshot({ path: '/tmp/velin-host-dialog-rdp.png' })
    await dialog.getByText('调用本地远程桌面', { exact: true }).click()
    assert.equal(await dialog.getByText('SSH 跳板机', { exact: true }).count(), 0, 'RDP native must hide SSH jump host')
    await dialog.getByRole('radio', { name: /SSH 终端/ }).click()
    await dialog.getByText('已存凭据', { exact: true }).click()
    await dialog.locator('.hd-cred-row .el-select').click()
    await page.getByRole('option', { name: /Prod Key/ }).click()

    // Save and connect should auto-fill name from address and trigger connection.
    await dialog.getByRole('button', { name: /保存并连接/ }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(hostSaves.length, 1)
    assert.equal(hostSaves[0].name, '10.20.30.40')
    assert.equal(hostSaves[0].address, '10.20.30.40')
    assert.equal(hostSaves[0].port, 2222)
    assert.equal(hostSaves[0].username, 'deploy')
    assert.equal(hostSaves[0].credentialID, 'cred-1')
    await connected(page)
    assert.equal(creates.length, 1)

    // Edit mode exposes test button and online latency badge.
    const row = page.locator('.host-row').first()
    await row.click({ button: 'right' })
    await page.getByRole('menu', { name: '主机操作', exact: true }).getByRole('menuitem', { name: '编辑', exact: true }).click()
    await dialog.waitFor()
    assert.match(await dialog.locator('.hd-status-text.online').innerText(), /14 ms/)
    assert.equal(await dialog.getByRole('button', { name: /测试连接/ }).count(), 1)
    await dialog.screenshot({ path: '/tmp/velin-host-dialog-edit.png' })
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
  })

  const webService = {
    id: 'web-test', hostID: host.id, name: 'Local service', proxyMode: 'host_port', pageMode: 'vite',
    listenPort: 4173, targetURL: 'http://127.0.0.1:4173/a-long-path-that-must-remain-readable',
    upstreamHost: '127.0.0.1', skipTLSVerify: false,
  }
  await scenario({ trusted: true, webServices: [webService] }, async ({ page, webSaves }) => {
    await page.getByRole('button', { name: '内网 Web 面板', exact: true }).click()
    const service = page.locator('.web-service-row').first()
    await service.waitFor()
    const layout = await service.evaluate(element => {
      const copy = element.querySelector('.web-service-copy')
      const detail = copy?.querySelector('small')
      const rowRect = element.getBoundingClientRect()
      const detailRect = detail.getBoundingClientRect()
      return { rowBottom: rowRect.bottom, detailBottom: detailRect.bottom }
    })
    assert(layout.detailBottom <= layout.rowBottom - 2, 'internal Web detail must not be clipped by the row')
    await service.dblclick()
    await page.waitForFunction(() => window.__openedWebService === '/proxy/web-test')
    const box = await service.boundingBox()
    await page.mouse.click(box.x + box.width * 0.65, box.y + box.height * 0.5, { button: 'right' })
    const menu = page.getByRole('menu', { name: '内网 Web 操作', exact: true })
    await menu.waitFor()
    await menu.screenshot({ path: '/tmp/velin-web-menu-compact.png' })
    assert.deepEqual(await menu.getByRole('menuitem').allTextContents(), ['打开', '编辑', '删除'])

    // Edit existing web service: shows collapsed summary for Vite + custom Host.
    await menu.getByRole('menuitem', { name: '编辑', exact: true }).click()
    const webDialog = page.locator('.web-proxy-dialog:visible')
    await webDialog.waitFor()
    assert.match(await webDialog.getByRole('button', { name: /更多设置/ }).innerText(), /Vite 开发服务 · 自定义 Host/)
    await webDialog.screenshot({ path: '/tmp/velin-web-dialog-edit.png' })
    await webDialog.getByRole('button', { name: '取消', exact: true }).click()
    await webDialog.waitFor({ state: 'hidden' })

    // Add new web service: backdrop click stays open, inline validation works,
    // HTTPS toggle appears on https://, extra settings expand, save-and-open works.
    await page.locator('button[title="添加内网 Web"]').click()
    await webDialog.waitFor()
    await page.mouse.click(12, 12)
    assert.equal(await webDialog.count(), 1)
    await webDialog.getByRole('button', { name: '保存', exact: true }).click()
    assert.equal(webSaves.length, 0)
    assert.match(await webDialog.locator('.hd-field-error').first().innerText(), /目标 URL/)
    await webDialog.screenshot({ path: '/tmp/velin-web-dialog-add.png' })

    await webDialog.getByRole('radio', { name: /主机端口/ }).click()
    await webDialog.getByPlaceholder('http://127.0.0.1:8080').fill('https://192.168.1.1:8006')
    await webDialog.getByText('允许自签 HTTPS 证书', { exact: true }).waitFor()
    await webDialog.getByRole('button', { name: /更多设置/ }).click()
    await webDialog.screenshot({ path: '/tmp/velin-web-dialog-expanded.png' })

    await page.evaluate(() => { window.__openedWebService = '' })
    await webDialog.getByRole('button', { name: /保存并打开/ }).click()
    await webDialog.waitFor({ state: 'hidden' })
    assert.equal(webSaves.length, 1)
    assert.equal(webSaves[0].name, '192.168.1.1:8006')
    assert.equal(webSaves[0].targetURL, 'https://192.168.1.1:8006')
    assert.equal(webSaves[0].proxyMode, 'host_port')
    await page.waitForFunction(() => window.__openedWebService === '/proxy/web-test')
  })

  // Open and paint a closable tab before waiting on the remote SSH probe.
  await scenario({ trusted: true, holdProbe: true }, async ({ page, fixture, creates }) => {
    await page.locator('.host-row').dblclick()
    await page.getByRole('region', { name: 'SSH 连接信息' }).waitFor()
    assert.equal(await page.locator('.terminal-tab').count(), 1)
    assert.match(await page.locator('.connection-log').innerText(), /正在连接/)
    assert.equal(creates.length, 0)
    const bounds = await page.locator('.connection-progress').boundingBox()
    assert.ok(bounds.height > 700, 'the connection view fills its terminal window')
    await page.screenshot({ path: '/tmp/velin-connection-feedback.png' })
    fixture.holdProbe = false; fixture.releaseProbe()
    await connected(page)
    assert.equal(await page.locator('.terminal-tab').count(), 1, 'the pending tab becomes the connected terminal')
  })

  await scenario({ trusted: true, probeFailure: 'dial tcp 192.0.2.1:22: i/o timeout' }, async ({ page, fixture, creates }) => {
    await page.locator('.host-row').dblclick()
    await page.locator('.connection-failure').waitFor()
    assert.match(await page.locator('.connection-failure').innerText(), /i\/o timeout/)
    assert.equal(await page.locator('.el-message--error').count(), 0, 'connection failure stays inside its tab')
    assert.equal(creates.length, 0)
    fixture.probeFailure = ''
    await page.getByRole('button', { name: '重试连接', exact: true }).click()
    await connected(page)
    assert.equal(await page.locator('.terminal-tab').count(), 1)
  })

  await scenario({ trusted: true, holdCreate: true }, async ({ page, fixture, creates, deletions }) => {
    await page.locator('.host-row').dblclick()
    await page.waitForFunction(() => document.querySelector('.connection-log')?.textContent.includes('正在创建终端会话'))
    await page.locator('.terminal-tab .tab-close').click()
    const removed = page.waitForResponse(response => response.request().method() === 'DELETE')
    fixture.releaseCreate()
    await removed
    await page.clock.runFor(500)
    assert.equal(creates.length, 1)
    assert.equal(deletions.length, 1, 'closing during creation cleans up the returned session')
    assert.equal(await page.locator('.terminal-tab').count(), 0)
  })

  // The WebSocket opens before the real asynchronous SSH connection finishes.
  // Success must update the workspace and resend geometry without a refresh.
  await scenario({ trusted: true, creating: true }, async ({ page, creates }) => {
    await page.locator('.host-row').dblclick()
    await page.locator('.terminal-statusbar .status-dot.creating').waitFor()
    await page.clock.runFor(1500)
    assert.equal(creates.length, 1)
    const initial = await page.evaluate(() => ({
      rows: window.testTerminal.rows,
      cols: window.testTerminal.cols,
      resizes: window.testSockets.at(-1).messages.filter(message => message.type === 'resize').length,
    }))
    assert.ok(initial.rows > 30, 'fixture must expose the default 30-row remote PTY gap')
    await page.evaluate(() => window.testSockets.at(-1).receive({ type: 'status', status: 'attached' }))
    await page.clock.runFor(1500)
    await connected(page)
    const ready = await page.evaluate(() => {
      const terminal = window.testTerminal
      const buffer = terminal.buffer.active
      return {
        rows: terminal.rows, cols: terminal.cols,
        bottom: buffer.getLine(buffer.baseY + terminal.rows - 1)?.translateToString(true),
        cursorY: buffer.cursorY, viewportY: buffer.viewportY, baseY: buffer.baseY,
        resizes: window.testSockets.at(-1).messages.filter(message => message.type === 'resize'),
        sockets: window.testSockets.length,
      }
    })
    assert.ok(ready.resizes.length > initial.resizes, 'attachment must send the current geometry even if it was attempted while connecting')
    assert.deepEqual(ready.resizes.at(-1), { type: 'resize', rows: ready.rows, cols: ready.cols })
    assert.equal(ready.bottom, 'ssh-ready-bottom$ ')
    assert.equal(ready.cursorY, ready.rows - 1)
    assert.equal(ready.viewportY, ready.baseY)
    assert.equal(ready.sockets, 1, 'initial attachment must not require reconnection')
    assert.equal(await page.locator('.terminal-tab .status-dot.online').count(), 1, 'tab status must receive the attachment transition')
  })

  // Credentials -> trust -> missing tmux -> normal mode must be one ordered
  // flow, preserving the temporary password through every step.
  await scenario({ tmux: false, secretRequired: true }, async ({ page, probes, creates }) => {
    await page.locator('.host-row').dblclick()
    const credentials = page.getByRole('form', { name: '临时凭据', exact: true })
    await credentials.locator('input').first().fill('test-only-password')
    await credentials.getByRole('button', { name: '连接', exact: true }).click()
    await assertWaiting(page)
    assert.equal(creates.length, 0, 'no remote session may be created before trust')
    await fingerprintDialog(page).getByRole('button', { name: '信任并连接' }).click()
    const tmux = page.getByRole('region', { name: '需要安装 tmux', exact: true })
    await tmux.waitFor()
    assert.equal(creates.length, 0, 'missing tmux must reach its guide before creating a terminal')
    await tmux.getByRole('button', { name: '使用普通模式' }).click()
    await connected(page)
    assert.equal(creates.length, 1)
    assert.equal(creates[0].sessionMode, 'normal')
    assert.equal(creates[0].secret, 'test-only-password')
    assert.equal(creates[0].trustFingerprint, fingerprint)
    assert.equal(probes.at(-1).sessionMode, 'normal')
  })

  // Installation retry keeps the accepted fingerprint and creates tmux only
  // after the explicit "installation complete" action.
  await scenario({ tmux: false }, async ({ page, fixture, creates }) => {
    await page.locator('.host-row').dblclick()
    await assertWaiting(page)
    await fingerprintDialog(page).getByRole('button', { name: '信任并连接' }).click()
    const tmux = page.getByRole('region', { name: '需要安装 tmux', exact: true })
    await tmux.waitFor()
    fixture.tmux = true
    await tmux.getByRole('button', { name: '安装完成，重新连接' }).click()
    await connected(page)
    assert.equal(creates.length, 1)
    assert.equal(creates[0].sessionMode, 'tmux')
  })

  await scenario({}, async ({ page, creates }) => {
    await page.locator('.host-row').dblclick()
    await assertWaiting(page)
    await fingerprintDialog(page).getByRole('button', { name: '取消连接', exact: true }).click()
    await page.clock.runFor(5000)
    assert.equal(creates.length, 0)
    assert.equal(await page.locator('.terminal-tab').count(), 0)
  })

  // Restored sessions also pause automatic retry while trust is pending.
  // A failed retry must show its actual error instead of being swallowed.
  await scenario({ restore: true, challengeRestore: true, failAfterTrust: true }, async ({ page, restores }) => {
    await assertWaiting(page)
    assert.equal(restores.length, 1)
    await fingerprintDialog(page).getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('button', { name: '确认主机指纹', exact: true }).waitFor()
    await page.clock.runFor(5000)
    assert.equal(restores.length, 1, 'cancelled trust must not trigger an automatic retry')
    await page.getByRole('button', { name: '确认主机指纹', exact: true }).click()
    await assertWaiting(page)
    await fingerprintDialog(page).getByRole('button', { name: '信任并连接' }).click()
    await page.locator('.terminal-error').getByText('Remote retry failed', { exact: true }).waitFor()
    assert.equal(restores.length, 3)
  })

  // Duplicate WebSocket status events and closure share one confirmation flow.
  await scenario({ restore: true }, async ({ page, restores }) => {
    await connected(page)
    await page.evaluate(fingerprint => {
      const socket = window.testSockets.at(-1)
      const status = { type: 'status', status: 'host_key_required', message: 'Please confirm fingerprint', fingerprint }
      socket.receive(status)
      socket.receive(status)
      socket.close()
    }, fingerprint)
    await assertWaiting(page)
    assert.equal(await fingerprintDialog(page).count(), 1)
    assert.equal(restores.length, 1)
    await fingerprintDialog(page).getByRole('button', { name: '信任并连接' }).click()
    await connected(page)
    assert.equal(restores.length, 2)
    assert.equal(restores[1].trustFingerprint, fingerprint)
  })

  // Exercise the existing workspace context menu with the real terminal.
  // Right-click selection and explicit copying must coexist with auto-copy.
  await scenario({ restore: true }, async ({ page }) => {
    await connected(page)
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
    const text = 'right-click copy\r\n中文右键复制'
    await page.evaluate(data => new Promise(resolve => {
      window.testSockets.at(-1).receive({ type: 'output', data })
      window.testTerminal.write('', resolve)
    }), Buffer.from('\x1bc' + text).toString('base64'))
    const screen = page.locator('.xterm-screen')
    const menu = page.locator('.pane-context-menu')
    const copy = () => menu.getByRole('button', { name: '复制', exact: true }).click()
    const expectClipboard = text => page.waitForFunction(async text => (await navigator.clipboard.readText()) === text, text)

    await page.evaluate(() => window.testTerminal.selectAll())
    await page.evaluate(() => navigator.clipboard.writeText('before right click'))
    await screen.click({ button: 'right' })
    assert.deepEqual(await menu.locator('.context-label').allTextContents(), ['常用', '工具', '其它'])
    assert.deepEqual(await menu.getByRole('button').allTextContents(), ['重命名', '复制', '粘贴', 'SFTP', '代码编辑器', 'Agent', 'Git管理', 'Docker', '主机监控', '分屏操作', '分享会话'])
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'before right click', 'right click must preserve explicit copying')
    const all = await page.evaluate(() => window.testTerminal.getSelection())
    assert.ok(all.includes('中文右键复制'))
    await copy()
    await expectClipboard(all)

    await page.evaluate(() => window.testTerminal.clearSelection())
    const box = await screen.boundingBox()
    const dimensions = await page.evaluate(() => ({ cols: window.testTerminal.cols, rows: window.testTerminal.rows }))
    const cell = { width: box.width / dimensions.cols, height: box.height / dimensions.rows }
    await page.mouse.move(box.x + cell.width * 0.2, box.y + cell.height * 0.5)
    await page.mouse.down()
    await page.mouse.move(box.x + cell.width * 11.2, box.y + cell.height * 0.5, { steps: 8 })
    await page.mouse.up()
    await expectClipboard('right-click')
    await page.evaluate(() => navigator.clipboard.writeText('before explicit copy'))
    await page.mouse.click(box.x + cell.width * 4, box.y + cell.height * 0.5, { button: 'right' })
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'before explicit copy', 'right click must not trigger automatic copying')
    await copy()
    await expectClipboard('right-click')
  })

  // Both directions create a new session on the selected SSH host in the
  // original tab. Cancelling must leave the existing terminal untouched.
  const otherHost = { ...host, id: 'host-other', name: 'Other SSH host', address: 'other.invalid' }
  const desktopHost = { ...host, id: 'host-rdp', name: 'Desktop only', protocol: 'rdp' }
  await scenario({ restore: true, trusted: true, hosts: [host, otherHost, desktopHost] }, async ({ page, creates }) => {
    await connected(page)
    const menu = page.getByRole('menu', { name: '终端操作', exact: true })
    const dialog = page.getByRole('dialog', { name: '分屏操作', exact: true })
    const open = async () => {
      await page.locator('.xterm-screen').first().click({ button: 'right' })
      await menu.getByRole('button', { name: '分屏操作', exact: true }).click()
      await dialog.waitFor()
      assert(await dialog.getByRole('button', { name: '打开分屏', exact: true }).isDisabled())
    }
    await open()
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(creates.length, 0)
    assert.equal(await page.locator('.split-leaf').count(), 1)

    for (const [direction, name] of [['horizontal', '左右分屏'], ['vertical', '上下分屏']]) {
      await open()
      await dialog.getByText(name, { exact: true }).click()
      assert(await dialog.getByRole('radio', { name, exact: true }).isChecked())
      await dialog.getByRole('combobox').click()
      assert.equal(await page.getByRole('option', { name: /Desktop only/ }).count(), 0)
      await page.getByRole('option', { name: /Other SSH host/ }).click()
      await dialog.getByRole('button', { name: '打开分屏', exact: true }).click()
      await page.locator(`.split-node.split-${direction}`).waitFor()
      await page.clock.runFor(600)
      assert.equal(creates.at(-1).hostID, otherHost.id)
      assert.equal(await page.locator('.terminal-tab').count(), 1)
    }
    assert.equal(creates.length, 2)
    assert.equal(await page.locator('.split-leaf').count(), 3)
    await page.screenshot({ path: '/tmp/velin-context-menu-split.png' })
  })

  console.log('Connection onboarding, grouped context menu, selection/copy and split dialog browser checks passed')
} finally {
  await browser?.close()
  await server.close()
}
