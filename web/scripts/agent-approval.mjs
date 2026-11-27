import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Use the real Agent and transport with isolated API/WebSocket peers. No SSH
// commands leave this browser fixture.
const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8')
const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)]
  .map(([, path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`)
  .join('\n')
const entry = `
import { createApp, h, ref } from 'vue'
import ElementPlus from 'element-plus'
import AgentDialog from '/src/components/AgentDialog.vue'
import { applyAccent, applyInterfaceTheme } from '/src/themePresets.ts'
${styles}
applyAccent('#5b8cff')
applyInterfaceTheme('dark')
const open = ref(false), suspended = ref(false)
window.agentHarness = {
  open() { open.value = true },
  suspend(value) { suspended.value = value },
  theme(value) { applyInterfaceTheme(value) },
}
createApp({ render: () => h('main', [
  h('button', { id: 'outside', style: 'position:fixed;top:0;left:0' }, 'Outside Agent'),
  h('input', { id: 'outside-input', 'aria-label': 'Outside input', style: 'position:fixed;top:24px;left:0;width:90px' }),
  h(AgentDialog, {
    modelValue: open.value,
    'onUpdate:modelValue': value => { open.value = value },
    suspended: suspended.value,
    host: { id: 'approval-host', name: '测试主机', address: 'test.invalid', username: 'dev', credentialID: 'saved' },
  }),
]) }).use(ElementPlus).mount('#app')
`
const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  server: { host: '127.0.0.1', port: 0, proxy: {} },
  optimizeDeps: { esbuildOptions: { target: 'esnext' } },
  plugins: [{
    name: 'agent-approval-fixture',
    resolveId(id) { if (id === '/__agent_approval__.js') return id },
    load(id) { if (id === '/__agent_approval__.js') return entry },
    configureServer(vite) {
      vite.middlewares.use('/__agent_approval__.html', (_req, res) => {
        res.setHeader('Content-Type', 'text/html')
        res.end('<div id="app"></div><script type="module" src="/__agent_approval__.js"></script>')
      })
    },
  }],
})
const commands = [
  { id: 'first', command: "printf '%s\\n' ready > /tmp/agent-approval-example", reason: '写入示例文件，需要确认后执行', requiresApproval: true },
  { id: 'second', command: Array.from({ length: 24 }, (_, i) => `printf '%s\\n' '${i}: ${'long command content '.repeat(10)}'`).join('\n'), reason: '检查较长命令的换行与滚动', requiresApproval: true },
]
const artifacts = new URL('../../artifacts/agent-approval/', import.meta.url)
await mkdir(artifacts, { recursive: true })
let browser
try {
  await server.listen()
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox'] })
  for (const [name, viewport] of [['desktop', { width: 1440, height: 900 }], ['mobile', { width: 390, height: 780 }]]) {
    const context = await browser.newContext({ viewport })
    const page = await context.newPage()
    const errors = [], chats = [], executions = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', route => {
      const path = new URL(route.request().url()).pathname
      let json = {}
      if (path === '/api/csrf') json = { token: 'approval-fixture' }
      if (path.endsWith('/agent')) json = { state: 'connected', aiConfigured: true, model: 'fixture-model' }
      if (path === '/api/agent/models') json = { models: [{ id: 'fixture-model', contextWindow: 128000 }], defaultModel: 'fixture-model' }
      return route.fulfill({ json })
    })
    await page.routeWebSocket('**/ws/hosts/*/agent', socket => {
      socket.send(JSON.stringify({ type: 'ready' }))
      socket.onMessage(raw => {
        const request = JSON.parse(String(raw))
        if (request.type === 'chat') {
          chats.push(request)
          socket.send(JSON.stringify({ type: 'done', requestID: request.requestID, response: {
            message: chats.length === 1 ? '请检查下方命令。\n\n' + '任务说明\n\n'.repeat(40) : '已收到补充说明。',
            commands: chats.length === 1 ? commands : [], model: 'fixture-model',
          } }))
        } else if (request.type === 'command') {
          executions.push(request.command)
          socket.send(JSON.stringify({ type: 'done', requestID: request.requestID, success: true, output: 'fixture result' }))
        }
      })
    })
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__agent_approval__.html`)
    await page.evaluate(() => window.agentHarness.open())
    const agent = page.getByRole('dialog', { name: '后台 Agent · 测试主机', exact: true })
    const input = agent.getByPlaceholder('输入要在主机上完成的任务')
    const approval = agent.getByRole('region', { name: '需要确认执行', exact: true })
    await input.fill('部署示例任务')
    await input.press('Enter')
    await approval.waitFor()
    assert(await approval.evaluate(element => !element.contains(document.activeElement)), 'a proposal must not move focus to an approval button')
    assert.equal(await page.locator('.agent-command-approval-layer').count(), 0)

    const fits = async () => {
      const [bar, composer, window] = await Promise.all([
        approval.boundingBox(), agent.locator('.agent-chat-input').boundingBox(), agent.boundingBox(),
      ])
      assert(bar.x >= window.x && bar.x + bar.width <= window.x + window.width, `${name}: confirmation stays inside the Agent`)
      assert(bar.y >= window.y && bar.y + bar.height <= composer.y, `${name}: confirmation floats above the input`)
      assert(bar.height < window.height / 2, `${name}: confirmation stays compact`)
      assert(await approval.evaluate(element => element.scrollWidth <= element.clientWidth + 1), 'long commands must not widen the confirmation')
    }
    await fits()
    await page.screenshot({ path: new URL(`${name}-pending.png`, artifacts).pathname })

    // A click inside or outside the Agent, unrelated Enter, and Escape must
    // preserve the pending command without approving or cancelling it.
    if (name === 'desktop') {
      await page.locator('#outside').click()
      await page.locator('#outside-input').press('Enter')
      await page.locator('#outside-input').press('Escape')
    }
    await agent.locator('.agent-window-title').click()
    await input.press('Escape')
    assert.equal(await approval.locator('pre').innerText(), commands[0].command)
    assert.deepEqual(executions, [])
    await input.fill('补充说明，不执行待确认命令')
    await input.press('Enter')
    await page.waitForFunction(() => document.querySelector('.agent-chat-log')?.textContent.includes('已收到补充说明'))
    assert.equal(chats.length, 2)
    assert.deepEqual(executions, [], 'Enter in the draft must send a message, never approve a command')

    // Suspended Agent windows must not respond to keys on the active page.
    await page.evaluate(() => window.agentHarness.suspend(true))
    await page.locator('#outside-input').press('Enter')
    assert.deepEqual(executions, [])
    await page.evaluate(() => window.agentHarness.suspend(false))
    await approval.waitFor()
    await approval.getByRole('button', { name: '确认执行', exact: true }).click()
    await page.waitForFunction(() => document.querySelector('.agent-command-approval pre')?.textContent.startsWith("printf '%s\\n' '0:"))
    assert.deepEqual(executions, [commands[0].command], 'only the explicitly confirmed command executes')
    await fits()

    await page.evaluate(() => { document.querySelector('.agent-window-body').scrollTop = 0 })
    await fits()
    await page.evaluate(() => window.agentHarness.theme('light'))
    await page.screenshot({ path: new URL(`${name}-long-command.png`, artifacts).pathname })
    await approval.getByRole('button', { name: '取消', exact: true }).click()
    await approval.waitFor({ state: 'hidden' })
    assert.deepEqual(executions, [commands[0].command], 'cancelling the next command must not execute it')
    assert.deepEqual(errors, [])
    await context.close()
    console.log(`${name}: inline approval, outside clicks, draft Enter, suspension, queue, long commands and cancellation passed`)
  }
} finally {
  await browser?.close()
  await server.close()
}
