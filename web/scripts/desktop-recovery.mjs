import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Exercise the real Guacamole client and pane against an isolated WebSocket
// peer. Simulated time covers idle expiry and WebView suspension quickly.
const entry = `
import { createApp, h, reactive } from 'vue'
import ElementPlus from 'element-plus'
import Guacamole from 'guacamole-common-js'
import RemoteDesktopPane from '/src/components/RemoteDesktopPane.vue'

window.sockets = []
window.instructions = []
const encode = values => values.map(value => {
  const text = String(value)
  return text.length + '.' + text
}).join(',') + ';'
class TestSocket {
  static OPEN = 1
  readyState = 0
  responding = true
  id = window.sockets.length
  constructor() {
    window.sockets.push(this)
    this.parser = new Guacamole.Parser()
    this.parser.oninstruction = (opcode, parameters) => {
      const args = [...parameters]
      window.instructions.push({ socket: this.id, opcode, parameters: args })
      if (opcode === '' && args[0] === 'ping' && this.responding)
        queueMicrotask(() => this.receive('', ...args))
    }
    queueMicrotask(() => {
      this.readyState = 1
      this.onopen?.()
      this.receive('', 'test-' + this.id)
      this.receive('size', 0, 1024, 768)
      this.receive('sync', 1)
    })
  }
  receive(...values) {
    if (this.readyState === 1)
      this.onmessage?.({ data: encode(values) })
  }
  send(data) { this.parser.receive(data) }
  close(code = 1000) {
    if (this.readyState === 3) return
    this.readyState = 3
    queueMicrotask(() => this.onclose?.({ code }))
  }
}
window.WebSocket = TestSocket
window.blockFrame = timestamp => {
  const decode = window.createImageBitmap
  window.createImageBitmap = () => new Promise(() => {})
  const socket = window.sockets.at(-1)
  socket.receive('img', 0, 14, 0, 'image/png', 0, 0)
  socket.receive('end', 0)
  socket.receive('sync', timestamp)
  window.createImageBitmap = decode
}
window.desktopProps = reactive({ visible: true })
window.app = createApp({ render: () => h(RemoteDesktopPane, {
  host: { id: 'rdp-test', name: 'RDP test', protocol: 'rdp', rdpQuality: 'crisp' },
  visible: window.desktopProps.visible,
}) }).use(ElementPlus)
window.app.mount('#app')
`

const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  server: { host: '127.0.0.1', port: 0, open: false },
  optimizeDeps: { esbuildOptions: { target: 'esnext' } },
  plugins: [{
    name: 'desktop-recovery-test',
    resolveId(id) { if (id === '/__desktop_recovery__.js') return id },
    load(id) { if (id === '/__desktop_recovery__.js') return entry },
    configureServer(vite) {
      vite.middlewares.use('/__desktop_recovery__.html', (_req, res) => {
        res.setHeader('Content-Type', 'text/html')
        res.end('<div id="app" style="height:600px;width:1000px"></div><script type="module" src="/__desktop_recovery__.js"></script>')
      })
    },
  }],
})

let browser
try {
  await server.listen()
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
    headless: true, args: ['--no-sandbox'],
  })
  const page = await browser.newPage()
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.clock.install()
  let sessions = 0
  await page.route('**/api/**', route => {
    if (route.request().url().endsWith('/csrf'))
      return route.fulfill({ json: { token: 'desktop-test' } })
    sessions++
    return route.fulfill({ json: {
      id: String(sessions), protocol: 'rdp', websocketPath: '/ws/desktop/rdp/test-' + sessions,
    } })
  })
  const address = server.httpServer.address()
  await page.goto(`http://127.0.0.1:${address.port}/__desktop_recovery__.html`)
  const waitConnected = () => page.locator('.desktop-status.connected').waitFor()
  const socketCount = () => page.evaluate(() => window.sockets.length)
  const flush = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  await waitConnected()
  await flush()

  // A healthy static desktop must remain connected while idle.
  await page.clock.runFor(60_000)
  assert.equal(await socketCount(), 1, 'healthy idle connections must not be recycled')
  await waitConnected()

  // Repeated server/display resize notifications must not generate a size loop.
  const sizesBefore = await page.evaluate(() => window.instructions.filter(i => i.opcode === 'size').length)
  await page.evaluate(() => {
    for (let i = 0; i < 5; i++) {
      window.sockets.at(-1).receive('size', 0, 1024, 768)
      window.sockets.at(-1).receive('sync', 10 + i)
    }
  })
  await flush()
  assert.equal(await page.evaluate(() => window.instructions.filter(i => i.opcode === 'size').length), sizesBefore)

  // An idle half-open socket times out without a client-level disconnect.
  await page.evaluate(() => { window.sockets.at(-1).responding = false })
  await page.clock.runFor(17_000)
  await page.waitForFunction(() => window.sockets.length === 2)
  await waitConnected()
  assert.equal(await page.evaluate(() => window.sockets[0].readyState), 3)

  // A clean WebSocket close also needs recovery even without a protocol error.
  await page.evaluate(() => window.sockets.at(-1).close(1000))
  await page.clock.runFor(1_100)
  await page.waitForFunction(() => window.sockets.length === 3)
  await waitConnected()

  // A blocked image decode freezes frame ACKs while tunnel pings still work.
  await page.evaluate(() => window.blockFrame(99))
  await page.clock.runFor(24_000)
  await page.waitForFunction(() => window.sockets.length === 4)
  await waitConnected()

  // Hidden workspace tabs must not resize the remote machine to zero/minimum
  // dimensions, or reconnect solely because their frame queue is paused.
  await page.evaluate(() => { window.desktopProps.visible = false })
  await flush()
  const hiddenSizesBefore = await page.evaluate(() => window.instructions.filter(i => i.opcode === 'size').length)
  await page.evaluate(() => {
    window.sockets.at(-1).receive('size', 0, 800, 600)
    window.blockFrame(100)
  })
  await page.clock.runFor(30_000)
  assert.equal(await socketCount(), 4)
  assert.equal(await page.evaluate(() => window.instructions.filter(i => i.opcode === 'size').length), hiddenSizesBefore)
  await page.evaluate(() => { window.desktopProps.visible = true })
  await page.waitForFunction(() => window.sockets.length === 5)
  await waitConnected()

  // Key state must be released across backgrounding, and reconnects must not
  // leave multiple keyboard handlers sending the same key.
  await page.keyboard.down('Control')
  await page.evaluate(() => window.dispatchEvent(new Event('blur')))
  assert.ok(await page.evaluate(() => window.instructions.some(i => i.socket === 4 && i.opcode === 'key' && i.parameters[1] === '0')),
    JSON.stringify(await page.evaluate(() => window.instructions.filter(i => i.opcode === 'key'))))
  await page.keyboard.up('Control')
  await page.evaluate(() => { window.instructions.length = 0 })
  await page.keyboard.press('a')
  const keyEvents = await page.evaluate(() => window.instructions.filter(i => i.opcode === 'key'))
  assert.deepEqual(keyEvents.map(i => i.parameters), [['97', '1'], ['97', '0']])

  // Returning to the window probes a healthy connection without replacing it.
  await page.evaluate(() => { window.instructions.length = 0; window.dispatchEvent(new Event('focus')) })
  assert.ok(await page.evaluate(() => window.instructions.some(i => i.opcode === 'nop')))
  assert.equal(await socketCount(), 5)

  // Removing a pane must cancel timers, including when the WebSocket's close
  // callback fires after the pane has gone away.
  await page.evaluate(() => window.app.unmount())
  await page.clock.runFor(60_000)
  assert.equal(await socketCount(), 5)
  assert.deepEqual(errors, [])
  console.log('RDP idle, reconnect, tab resume and keyboard browser checks passed')
} finally {
  await browser?.close()
  await server.close()
}
