import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { execFileSync, spawn, spawnSync } from 'node:child_process'
import { createInterface } from 'node:readline'
import { once } from 'node:events'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Mount the real pane with an isolated transport; no SSH credentials or live
// sessions are needed. Expose xterm only in this test server's transformed SFC.
const entry = `
import { createApp, h, reactive } from 'vue'
import ElementPlus from 'element-plus'
import TerminalPane from '/src/components/TerminalPane.vue'
import '/src/styles.css'
import '@xterm/xterm/css/xterm.css'

window.messages = []
class TestSocket {
  static CONNECTING = 0
  static OPEN = 1
  readyState = 1
  constructor() {
    window.testSocket = this
    queueMicrotask(() => {
      this.onopen?.()
      window.receive({ type: 'hello', clientID: 'test', controller: 'test', historySize: 200 })
      window.receive({ type: 'replay_end' })
    })
  }
  send(data) { window.messages.push(JSON.parse(data)) }
  close() { this.readyState = 3 }
}
window.WebSocket = TestSocket
window.receive = message => window.testSocket.onmessage({ data: JSON.stringify(message) })
window.output = data => window.receive({
  type: 'output', data: btoa(String.fromCharCode(...new TextEncoder().encode(data))),
})
window.session = reactive({
  id: 'terminal-history-test', name: 'Terminal history test',
  sessionMode: 'tmux', status: 'attached',
})
createApp({ render: () => h(TerminalPane, {
  session: window.session, visible: true,
  preferences: {
    terminalTheme: 'default', fontSize: 14, lineHeight: 1.2, fontWeight: 400,
    letterSpacing: 0, background: '#111614', cursorStyle: 'block', cursorBlink: false,
  },
}) }).use(ElementPlus).mount('#app')
`

const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  server: { host: '127.0.0.1', port: 0, open: false },
  optimizeDeps: { esbuildOptions: { target: 'esnext' } },
  plugins: [{
    name: 'terminal-history-test',
    enforce: 'pre',
    resolveId(id) { if (id === '/__terminal_history__.js') return id },
    load(id) { if (id === '/__terminal_history__.js') return entry },
    transform(source, id) {
      if (id.endsWith('/src/components/TerminalPane.vue')) {
        return source.replace('terminal.open(container.value!);',
          'terminal.open(container.value!); window.testTerminal = terminal;')
      }
    },
    configureServer(vite) {
      vite.middlewares.use('/__terminal_history__.html', (_req, res) => {
        res.setHeader('Content-Type', 'text/html')
        res.end('<div id="app" style="height:600px;width:900px"></div><script type="module" src="/__terminal_history__.js"></script>')
      })
    },
  }],
})

let browser
try {
  await server.listen()
  const address = server.httpServer.address()
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
    headless: true, args: ['--no-sandbox'],
  })
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } })
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/api/**', route => route.fulfill({
    json: route.request().url().endsWith('/csrf') ? { token: 'terminal-test' } : {},
  }))
  await page.goto(`http://127.0.0.1:${address.port}/__terminal_history__.html`)
  await page.waitForFunction(() => window.testTerminal && window.messages.some(m => m.type === 'resize'))
  // Wait for the pane's initial layout to settle before using fixed dimensions.
  await page.waitForFunction(() => !document.querySelector('.terminal-resize-snapshot'))

  const flush = () => page.evaluate(() => new Promise(resolve => window.testTerminal.write('', () => {
    requestAnimationFrame(() => requestAnimationFrame(resolve))
  })))
  const output = async data => { await page.evaluate(data => window.output(data), data); await flush() }
  const screen = () => page.evaluate(() => {
    const { active } = window.testTerminal.buffer
    return Array.from({ length: window.testTerminal.rows }, (_, row) => active.getLine(active.baseY + row).translateToString(true))
  })
  const wheel = deltaY => page.evaluate(deltaY => {
    window.testTerminal.element.querySelector('.xterm-screen').dispatchEvent(new WheelEvent('wheel', {
      deltaY, bubbles: true, cancelable: true,
    }))
  }, deltaY)

  // A TUI scrolls its body while keeping a heading and two footer rows fixed.
  const rows = await page.evaluate(() => window.testTerminal.rows)
  await output('\x1bc' + Array.from({ length: rows }, (_, row) => `\x1b[${row + 1};1Hrow-${row}`).join(''))
  await output(`\x1b[2;${rows - 2}r\x1b[${rows - 2};1H\r\nnew-body`)
  let lines = await screen()
  assert.equal(lines[0], 'row-0', 'partial scrolling must preserve the heading')
  assert.equal(lines[1], 'row-2', 'the body must scroll inside its region')
  assert.equal(lines[rows - 3], 'new-body')
  assert.deepEqual(lines.slice(-2), [`row-${rows - 2}`, `row-${rows - 1}`], 'footer rows must stay fixed')
  await output(`\x1b[r\x1b[${rows};1H\r\nregion-reset`)
  lines = await screen()
  assert.equal(lines[0], 'row-2', 'resetting the region must restore full-screen scrolling')
  assert.equal(lines.at(-1), 'region-reset')

  // Mode commands may combine screen switching and bracketed paste.
  await output('\x1bcmain-screen\x1b[?1049;2004h\x1b[Halternate-screen')
  assert.equal(await page.evaluate(() => window.testTerminal.buffer.active.type), 'alternate')
  assert.equal(await page.evaluate(() => window.testTerminal.modes.bracketedPasteMode), true)
  await output('\x1b[?1049;2004l')
  assert.equal(await page.evaluate(() => window.testTerminal.buffer.active.type), 'normal')
  assert.equal(await page.evaluate(() => window.testTerminal.modes.bracketedPasteMode), false)
  assert.equal((await screen())[0], 'main-screen', 'leaving the alternate screen must restore the shell')

  // Even with local scrollback, the first upward gesture must read tmux's
  // original history, and returning to the bottom must not create a second copy.
  await output('\x1bc' + Array.from({ length: rows * 3 }, (_, row) => `history-${row}\r\n`).join(''))
  assert.ok(await page.evaluate(() => window.testTerminal.buffer.active.baseY > 0))
  for (let cycle = 0; cycle < 3; cycle++) {
    const previous = await page.evaluate(() => window.messages.filter(m => m.type === 'scroll_history_to').length)
    await wheel(-64)
    await page.waitForFunction(previous => window.messages.filter(m => m.type === 'scroll_history_to').length > previous, previous)
    const request = await page.evaluate(() => window.messages.filter(m => m.type === 'scroll_history_to').at(-1))
    assert.equal(request.position, 199)
    assert.equal(request.historySize, 200)
    await page.waitForFunction(() => window.testTerminal.buffer.active.viewportY === window.testTerminal.buffer.active.baseY)
    await page.evaluate(request => window.receive({ ...request, type: 'history_position', position: 199 }), request)
    await wheel(64)
    await page.waitForFunction(() => window.messages.filter(m => m.type === 'scroll_history_to').at(-1)?.position === 200)
    await page.evaluate(() => {
      const request = window.messages.filter(m => m.type === 'scroll_history_to').at(-1)
      window.receive({ ...request, type: 'history_position' })
    })
  }

  await page.evaluate(() => {
    window.testTerminal.scrollToTop()
    document.querySelector('.terminal-history-scrollbar').dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Home', bubbles: true, cancelable: true,
    }))
  })
  await page.waitForFunction(() => window.messages.filter(m => m.type === 'scroll_history_to').at(-1)?.position === 0)
  const thumbAtTop = await page.locator('.terminal-history-scrollbar-thumb').boundingBox()
  await page.waitForFunction(() => window.testTerminal.buffer.active.viewportY === window.testTerminal.buffer.active.baseY)
  await page.evaluate(() => {
    const scrollbar = document.querySelector('.terminal-history-scrollbar')
    scrollbar.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true, cancelable: true }))
  })
  await page.waitForFunction(() => window.messages.filter(m => m.type === 'scroll_history_to').at(-1)?.position === 200)
  const thumbAtBottom = await page.locator('.terminal-history-scrollbar-thumb').boundingBox()
  assert.ok(thumbAtBottom.y > thumbAtTop.y + 100, 'the real scrollbar thumb must follow history position')
  await page.evaluate(() => window.receive({ type: 'history_state', historySize: 400, alternateScreen: false }))
  const smallerThumb = await page.locator('.terminal-history-scrollbar-thumb').boundingBox()
  assert.ok(smallerThumb.height < thumbAtBottom.height, 'the thumb must shrink as history grows')
  await page.evaluate(() => window.receive({ type: 'history_state', historySize: 200, alternateScreen: false }))
  await page.evaluate(() => { window.messages.length = 0 })
  await wheel(120)
  assert.equal(await page.evaluate(() => window.messages.some(m => m.type === 'input')), false, 'downward scrolling at the live tmux screen must not send TUI input')

  // Streaming must refresh the history size even when output never pauses.
  await page.evaluate(async () => {
    window.messages.length = 0
    for (let chunk = 0; chunk < 20; chunk++) {
      window.output('.')
      await new Promise(resolve => setTimeout(resolve, 75))
    }
  })
  assert.ok(await page.evaluate(() => window.messages.some(m => m.type === 'history_state')), 'continuous output must not starve history refresh')

  // The remote pane state determines scroll ownership. tmux's outer mouse
  // tracking can be enabled for an inline application with real history.
  await output('\x1b[?1049h\x1b[?1000h\x1b[?1006h')
  await page.evaluate(() => window.receive({ type: 'history_state', historySize: 200, alternateScreen: false }))
  await page.evaluate(() => { window.messages.length = 0 })
  await wheel(-64)
  await page.waitForFunction(() => window.messages.some(m => m.type === 'scroll_history_to'))
  assert.equal(await page.evaluate(() => window.messages.some(m => m.type === 'input')), false,
    'an inline mouse-aware application must keep tmux history reachable')
  await wheel(64)
  await page.waitForFunction(() => window.messages.filter(m => m.type === 'scroll_history_to').at(-1)?.position === 200)
  await page.evaluate(() => window.receive({ type: 'history_state', historySize: 0, alternateScreen: true }))
  await page.evaluate(() => { window.messages.length = 0 })
  await wheel(-120)
  assert.ok(await page.evaluate(() => window.messages.some(m => m.type === 'input')), 'tmux must retain a mouse-aware TUI\'s own scrolling')
  assert.equal(await page.evaluate(() => window.messages.some(m => m.type === 'scroll_history_to')), false)
  await output('\x1b[?1000l\x1b[?1006l')
  await page.evaluate(() => { window.messages.length = 0 })
  await wheel(-64)
  await page.waitForFunction(() => window.messages.some(m => m.type === 'scroll_application'))
  assert.equal(await page.evaluate(() => window.messages.find(m => m.type === 'scroll_application').lines), -1)
  assert.equal(await page.evaluate(() => window.messages.some(m => m.type === 'input' || m.type === 'scroll_history_to')), false,
    'a TUI without capture must receive line scrolling instead of page keys or empty tmux history')
  assert.equal(await page.evaluate(() => getComputedStyle(window.testTerminal.element.querySelector('.xterm-viewport')).scrollbarWidth), 'none',
    'fullscreen conversation history must not show an empty outer scrollbar')
  await output('\x1b[?1049l')
  await page.evaluate(() => { window.messages.length = 0 })
  await wheel(64)
  await page.waitForFunction(() => window.messages.some(m => m.type === 'scroll_application'))
  assert.equal(await page.evaluate(() => window.messages.find(m => m.type === 'scroll_application').lines), 1,
    'transcript line scrolling must also work when a replay seeded the outer normal buffer')
  await page.evaluate(() => window.receive({ type: 'history_state', historySize: 200, alternateScreen: false }))
  await wheel(-64)
  await page.waitForFunction(() => window.messages.some(m => m.type === 'scroll_history_to'))
  await page.evaluate(() => {
    const request = window.messages.filter(m => m.type === 'scroll_history_to').at(-1)
    window.receive({ ...request, type: 'history_position' })
  })
  await wheel(64)
  await page.waitForFunction(() => window.messages.filter(m => m.type === 'scroll_history_to').at(-1)?.position === 200)

  // Ordinary SSH keeps local history and native mouse reporting. A full-screen
  // conversation without mouse capture also needs native transcript wheel input.
  await page.evaluate(() => { window.session.sessionMode = 'normal' })
  await wheel(-64)
  await page.waitForFunction(() => window.testTerminal.buffer.active.viewportY < window.testTerminal.buffer.active.baseY)
  await output('\x1b[?1049h\x1b[?1000h\x1b[?1006h')
  await page.evaluate(() => { window.messages.length = 0 })
  await wheel(-120)
  assert.ok(await page.evaluate(() => window.messages.some(m => m.type === 'input')), 'normal SSH must retain native TUI mouse input')
  await output('\x1b[?1000l\x1b[?1006l')
  await page.evaluate(() => { window.messages.length = 0 })
  await wheel(-64)
  assert.equal(await page.evaluate(() => window.messages.find(m => m.type === 'scroll_application').lines), -1,
    'ordinary SSH must scroll an alternate-screen transcript without changing composer history')

  // A completed mouse selection copies automatically, including when Shift
  // forces local selection in a CLI which has enabled mouse reporting.
  const selectText = async (shift = false) => {
    const box = await page.locator('.terminal-canvas > .xterm .xterm-screen').boundingBox()
    const dimensions = await page.evaluate(() => ({ cols: window.testTerminal.cols, rows: window.testTerminal.rows }))
    const cell = { width: box.width / dimensions.cols, height: box.height / dimensions.rows }
    if (shift) await page.keyboard.down('Shift')
    await page.mouse.move(box.x + cell.width * 0.2, box.y + cell.height * 0.5)
    await page.mouse.down()
    await page.mouse.move(box.x + cell.width * 8.2, box.y + cell.height * 0.5, { steps: 8 })
    await page.mouse.up()
    if (shift) await page.keyboard.up('Shift')
  }
  const expectClipboard = text => page.waitForFunction(async text => (await navigator.clipboard.readText()) === text, text)
  await output('\x1bccopy-one text\r\n中文复制测试')
  await selectText()
  await expectClipboard('copy-one')
  await page.evaluate(() => {
    window.testTerminal.clearSelection()
    window.messages.length = 0
    return navigator.clipboard.writeText('unchanged')
  })
  await output('\x1b[?1000h\x1b[?1006h')
  await selectText(true)
  await expectClipboard('copy-one')
  assert.equal(await page.evaluate(() => window.messages.some(m => m.type === 'input')), false, 'forced selection must not send mouse input to the CLI')

  // Programmatic selection (for example search) must not overwrite clipboard.
  await page.evaluate(() => {
    window.testTerminal.selectAll()
    window.testTerminal.clearSelection()
    return navigator.clipboard.writeText('unchanged')
  })
  await flush()
  assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'unchanged')

  // Claude Code and tmux can highlight their own selection and send OSC 52.
  const remoteText = 'Claude Code 中文选区\nsecond line'
  const oscCopy = text => '\x1b]52;c;' + Buffer.from(text).toString('base64') + '\x07'
  await output(oscCopy(remoteText))
  await expectClipboard(remoteText)
  await output('\x1b]52;c;?\x07\x1b]52;c;invalid!\x07')
  assert.equal(await page.evaluate(() => navigator.clipboard.readText()), remoteText)

  // Queued history can finish parsing after replay_end arrives. It must never
  // repeat an old application's clipboard request.
  await page.evaluate(data => {
    window.receive({ type: 'hello', clientID: 'test', controller: 'test', status: 'attached', streamID: 'clipboard-replay', offset: 1 })
    window.receive({ type: 'replay', data })
    window.receive({ type: 'replay_end' })
  }, Buffer.from(oscCopy('old selection')).toString('base64'))
  await flush()
  assert.equal(await page.evaluate(() => navigator.clipboard.readText()), remoteText)

  // Browser API rejection falls back to copying during the mouse gesture and
  // restores terminal focus. If both paths are blocked, the explicit button
  // can retry the CLI selection after the user grants clipboard access.
  await page.evaluate(() => {
    window.originalClipboardWrite = navigator.clipboard.writeText
    Object.defineProperty(navigator.clipboard, 'writeText', { configurable: true, value: () => Promise.reject(new Error('clipboard blocked')) })
  })
  await output('\x1bccopy-one text')
  await selectText()
  await expectClipboard('copy-one')
  assert.equal(await page.evaluate(() => document.activeElement === window.testTerminal.textarea), true)
  await page.evaluate(() => {
    window.originalExecCommand = document.execCommand
    document.execCommand = () => false
    window.testTerminal.clearSelection()
  })
  await output(oscCopy(remoteText))
  await page.getByText('自动复制受限，请点击终端上方的复制按钮', { exact: true }).waitFor()
  await page.evaluate(() => {
    Object.defineProperty(navigator.clipboard, 'writeText', { configurable: true, value: window.originalClipboardWrite })
    document.execCommand = window.originalExecCommand
  })
  await page.getByRole('button', { name: '复制选中内容', exact: true }).click()
  await expectClipboard(remoteText)

  // Replay bytes emitted by a real tmux client on a PTY, including its
  // incremental copy-mode redraws. Protocol-only mocks cannot catch cursor
  // movement or scrolling-region corruption between tmux and xterm.
  if (spawnSync('tmux', ['-V']).status === 0 && spawnSync('python3', ['--version']).status === 0) {
    const fixture = JSON.parse(execFileSync('python3', [fileURLToPath(new URL('./fixtures/tmux-history.py', import.meta.url))], {
      timeout: 30000, maxBuffer: 2 * 1024 * 1024,
    }).toString())
    await page.evaluate(() => {
      window.session.sessionMode = 'tmux'
      window.testTerminal.reset()
      window.testTerminal.resize(80, 24)
    })
    for (const stage of fixture.stages) {
      await output(stage.data)
      if (stage.firstLine !== undefined) {
        const actual = await screen()
        for (let row = 0; row < 24; row++) {
          const line = stage.firstLine + row
          const expected = line < 80 ? `original-${String(line).padStart(3, '0')}` : ''
          assert.equal(actual[row].trim().split(/\s+/)[0], expected,
            `${stage.name}: row ${row} must appear once and in order`)
        }
        assert.equal(await page.evaluate(() => window.testTerminal.buffer.active.baseY), 0,
          'tmux redraws must not accumulate in browser scrollback')
      }
    }
    console.log('Real tmux PTY rendering and repeated history navigation checks passed')

    // Exercise a physical browser wheel all the way through a live tmux PTY
    // and back into xterm. Cover inline history and full-screen conversations
    // with and without native mouse capture.
    for (const mode of ['inline-mouse', 'alternate-keyboard', 'alternate-mouse']) {
      const peer = spawn('python3', [fileURLToPath(new URL('./fixtures/tmux-wheel.py', import.meta.url)), mode])
      const responses = createInterface({ input: peer.stdout })[Symbol.asyncIterator]()
      let stderr = ''
      peer.stderr.on('data', chunk => { stderr += chunk })
      const read = async () => {
        const result = await responses.next()
        assert.equal(result.done, false, `tmux peer exited: ${stderr}`)
        return JSON.parse(result.value)
      }
      const apply = async response => {
        if (response.data) {
          await page.evaluate(data => window.receive({ type: 'output', data }), response.data)
          await flush()
        }
        await page.evaluate(response => {
          window.receive({ type: 'history_state', ...response })
          if (response.event) window.receive(response.event)
        }, response)
      }
      try {
        await page.evaluate(() => {
          window.testTerminal.reset()
          window.testTerminal.resize(80, 24)
          window.messages.length = 0
        })
        const initial = await read()
        const alternate = mode.startsWith('alternate-')
        assert.equal(initial.alternateScreen, alternate)
        assert.equal(initial.historySize, alternate ? 0 : 57)
        await apply(initial)
        const firstLine = alternate ? 56 : 57
        const box = await page.locator('.terminal-canvas > .xterm .xterm-screen').boundingBox()
        await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
        for (let cycle = 0; cycle < 3; cycle++) {
          const distance = 1
          const wheelDelta = mode === 'alternate-mouse' ? 120 : 64
          for (const [delta, expected] of [[-wheelDelta, firstLine - distance], [wheelDelta, firstLine]]) {
            const start = await page.evaluate(() => window.messages.length)
            assert.equal(await page.evaluate(({x,y,width,height}) => Boolean(
              document.elementFromPoint(x + width / 2, y + height / 2)?.closest('.terminal-replay-preview, .terminal-resize-snapshot, .terminal-history-transition'),
            ), box), false, 'frozen transition viewports must not intercept the next physical wheel event')
            await page.mouse.wheel(0, delta)
            await page.waitForFunction(start => window.messages.slice(start).some(m => ['input', 'scroll_history_to', 'scroll_application', 'click_application'].includes(m.type)), start, { timeout: 5000 }).catch(async error => {
              throw new Error(`${mode}, cycle ${cycle}, delta ${delta}: ${JSON.stringify(await page.evaluate(start => ({
                messages: window.messages.slice(start), mouse: window.testTerminal.modes.mouseTrackingMode,
                buffer: window.testTerminal.buffer.active.type,
              }), start))}`, { cause: error })
            })
            const message = await page.evaluate(start => window.messages.slice(start).find(m => ['input', 'scroll_history_to', 'scroll_application', 'click_application'].includes(m.type)), start)
            assert.equal(message.type, mode === 'alternate-keyboard' ? 'scroll_application' : alternate ? 'input' : 'scroll_history_to')
            peer.stdin.write(JSON.stringify(message) + '\n')
            await apply(await read())
            const actual = await screen()
            for (let row = 0; row < 24; row++) {
              if (alternate && row === 23 && expected < 56) {
                assert.equal(actual[row].trim(), 'Back to bottom')
                continue
              }
              const line = expected + row
              assert.equal(actual[row].trim().split(/\s+/)[0], line < 80 ? `original-${String(line).padStart(3, '0')}` : '',
                `${mode}, cycle ${cycle}, delta ${delta}: the visible history must actually move in order`)
            }
          }
        }
        if (mode === 'alternate-keyboard') {
          const forward = async type => {
            await page.waitForFunction(type => window.messages.some(m => m.type === type), type)
            const message = await page.evaluate(type => window.messages.find(m => m.type === type), type)
            peer.stdin.write(JSON.stringify(message) + '\n')
            await apply(await read())
          }
          await page.evaluate(() => { window.messages.length = 0 })
          await page.mouse.wheel(0, -64)
          await forward('scroll_application')
          assert.equal((await screen()).at(-1).trim(), 'Back to bottom')
          await page.evaluate(() => { window.messages.length = 0 })
          await page.mouse.click(box.x + box.width * 5 / 80, box.y + box.height * 23.5 / 24)
          await forward('click_application')
          assert.equal((await screen())[0].trim(), 'original-056', 'clicking the CLI button must actually return to the latest output')
          assert.equal((await screen()).some(line => line.includes('Back to bottom')), false)

          // Selection, Shift selection, right clicks and read-only viewers
          // must never become fallback application clicks.
          await page.evaluate(() => { window.messages.length = 0 })
          await selectText()
          await expectClipboard('original')
          assert.equal(await page.evaluate(() => window.messages.some(m => m.type === 'click_application')), false)
          await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2, { button: 'right' })
          await page.keyboard.down('Shift')
          await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2)
          await page.keyboard.up('Shift')
          assert.equal(await page.evaluate(() => window.messages.some(m => m.type === 'click_application')), false)
          await page.evaluate(() => window.receive({ type: 'controller', controller: 'someone-else' }))
          await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2)
          assert.equal(await page.evaluate(() => window.messages.some(m => m.type === 'click_application')), false)
          await page.evaluate(() => window.receive({ type: 'controller', controller: 'test' }))
        }
      } finally {
        peer.stdin.end()
        await once(peer, 'exit')
        await responses.return?.()
      }
    }
    console.log('Physical browser wheel → real tmux/application → visible rows checks passed')
  } else {
    console.log('Real tmux rendering check skipped (requires tmux and Python 3)')
  }
  assert.deepEqual(errors, [])
  console.log('Terminal history, selection copying and CLI clipboard browser checks passed')
} finally {
  await browser?.close()
  await server.close()
}
