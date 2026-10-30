import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Use the application's full stylesheet order and real Vue/Element Plus
// transitions. Sampling frames catches a panel disappearing after it entered,
// including animations that restart when Vue removes its transition classes.
const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8')
const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)]
  .map(([, path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`)
  .join('\n')
const entry = `
import { createApp, h, ref } from 'vue'
import ElementPlus, { ElDialog } from 'element-plus'
${styles}
const open = ref(false)
const nested = ref(false)
const settings = ref(false)
const destroy = ref(false)
window.dialogEvents = { opened: 0, closed: 0 }
window.dialogHarness = {
  open(options) { settings.value = options.settings; destroy.value = options.destroy; open.value = true },
  close() { open.value = false },
  openNested() { nested.value = true },
}
createApp({ render: () => h('main', [
  h(ElDialog, {
    modelValue: open.value,
    'onUpdate:modelValue': value => { open.value = value },
    class: ['motion-test-dialog', { sp: settings.value }],
    title: 'Dialog motion regression',
    appendToBody: true,
    destroyOnClose: destroy.value,
    onOpened: () => { window.dialogEvents.opened++ },
    onClosed: () => { window.dialogEvents.closed++ },
  }, { default: () => h('p', 'The dialog must stay visible after entering.') }),
  h(ElDialog, {
    modelValue: nested.value,
    'onUpdate:modelValue': value => { nested.value = value },
    class: 'motion-nested-dialog',
    title: 'Nested dialog',
    appendToBody: true,
    destroyOnClose: true,
  }, { default: () => h('p', 'Closing this dialog must preserve the parent.') }),
]) }).use(ElementPlus).mount('#app')
window.dialogHarnessReady = true
`
const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  server: { host: '127.0.0.1', port: 0, open: false, proxy: {} },
  cacheDir: 'node_modules/.vite-dialog-motion',
  optimizeDeps: { include: ['vue', 'element-plus'], esbuildOptions: { target: 'esnext' } },
  plugins: [{
    name: 'dialog-motion-test',
    resolveId(id) { if (id === '/__dialog_motion__.js') return id },
    load(id) { if (id === '/__dialog_motion__.js') return entry },
    configureServer(vite) {
      vite.middlewares.use('/__dialog_motion__.html', (_req, res) => {
        res.setHeader('Content-Type', 'text/html')
        res.end('<div id="app"></div><script type="module" src="/__dialog_motion__.js"></script>')
      })
    },
  }],
})

let browser
try {
  await server.listen()
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
    headless: true,
    args: ['--no-sandbox'],
  })
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } })
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__dialog_motion__.html`)
  await page.waitForFunction(() => window.dialogHarnessReady)

  for (const reducedMotion of ['no-preference', 'reduce']) {
    await page.emulateMedia({ reducedMotion })
    for (const options of [
      { settings: false, destroy: false },
      { settings: true, destroy: false },
      { settings: false, destroy: true },
    ]) {
      for (let repeat = 0; repeat < 2; repeat++) {
        const frames = await page.evaluate(async options => {
          window.dialogHarness.open(options)
          const started = performance.now()
          return await new Promise(resolve => {
            const frames = []
            const sample = () => {
              const panel = document.querySelector('.motion-test-dialog')
              if (panel) {
                const overlay = panel.closest('.el-overlay')
                if (getComputedStyle(overlay).display !== 'none') {
                  const ancestors = [panel, panel.closest('.el-overlay-dialog'), overlay]
                  frames.push({
                    t: Math.round(performance.now() - started),
                    opacity: ancestors.reduce((value, el) => value * Number(getComputedStyle(el).opacity), 1),
                  })
                }
              }
              if (performance.now() - started < 800) requestAnimationFrame(sample)
              else resolve(frames)
            }
            requestAnimationFrame(sample)
          })
        }, options)
        const label = JSON.stringify({ reducedMotion, ...options, repeat })
        assert(frames.length > 2, `${label}: dialog was not rendered`)
        let peak = 0
        for (const frame of frames) {
          assert(frame.opacity >= peak - 0.03,
            `${label}: dialog opacity fell from ${peak.toFixed(3)} to ${frame.opacity.toFixed(3)} at ${frame.t} ms`)
          peak = Math.max(peak, frame.opacity)
        }
        assert(frames.at(-1).opacity > 0.99, `${label}: dialog did not finish entering`)
        assert.equal(await page.locator('.motion-test-dialog').evaluate(panel => panel.getAnimations().filter(animation => animation.playState === 'running').length), 0,
          `${label}: panel animation outlived its enter transition`)

        if (repeat === 0) {
          await page.evaluate(() => window.dialogHarness.openNested())
          await page.locator('.motion-nested-dialog').waitFor()
          await page.keyboard.press('Escape')
          await page.locator('.motion-nested-dialog').waitFor({ state: 'hidden' })
          assert.equal(await page.locator('.motion-test-dialog:visible').count(), 1)
        }
        await page.evaluate(() => window.dialogHarness.close())
        await page.locator('.motion-test-dialog').waitFor({ state: 'hidden' })
      }
    }
  }
  const events = await page.evaluate(() => window.dialogEvents)
  assert.deepEqual(events, { opened: 12, closed: 12 })
  assert.deepEqual(errors, [])
  console.log('Dialog opacity stays continuous on first and repeated opens, including settings, destroy-on-close, nested dialogs and reduced motion')
} finally {
  await browser?.close()
  await server.close()
}
