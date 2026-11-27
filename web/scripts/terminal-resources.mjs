import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { mkdir, readFile } from 'node:fs/promises'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8')
const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)].map(([, path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`).join('\n')
const entry = `${styles}
import { createApp, h, reactive } from 'vue'
import ElementPlus from 'element-plus'
import TerminalPane from '/src/components/TerminalPane.vue'
window.resourceInput = []
class Socket {
  static CONNECTING=0; static OPEN=1; readyState=1
  constructor() { queueMicrotask(() => { this.onopen?.(); this.onmessage?.({data: JSON.stringify({type:'hello',clientID:'fixture',controller:'fixture',status:'attached'})}); this.onmessage?.({data: JSON.stringify({type:'replay_end'})}) }) }
  send(data) { const value=JSON.parse(data); if(value.type==='input') window.resourceInput.push(value) }
  close() { this.readyState=3 }
}
window.WebSocket=Socket
window.resourceTest=reactive({first:true,second:true})
const preferences={terminalTheme:'velin',fontSize:13,lineHeight:1.2,fontWeight:400,letterSpacing:0,background:'#111318',cursorStyle:'block',cursorBlink:false}
createApp({render:()=>h('div',{style:'display:grid;height:720px;width:100%;grid-template-rows:1fr 1fr;gap:10px'}, ['first','second'].map(id=>h(TerminalPane,{key:id,session:{id,hostID:'shared-host',name:id+' 终端',status:'attached',sessionMode:'normal'},preferences,visible:window.resourceTest[id]})))}).use(ElementPlus).mount('#app')
`
const server = await createServer({ root: fileURLToPath(new URL('..', import.meta.url)), server: { host: '127.0.0.1', port: 0, proxy: {} }, cacheDir: 'node_modules/.vite-terminal-resources',
  optimizeDeps: { esbuildOptions: { target: 'esnext' } },
  plugins: [{ name: 'terminal-resources-fixture', resolveId: id => id === '/__resources.js' ? id : undefined, load: id => id === '/__resources.js' ? entry : undefined,
    configureServer(vite) { vite.middlewares.use('/__resources.html', (_, response) => { response.setHeader('Content-Type','text/html'); response.end('<div id="app"></div><script type="module" src="/__resources.js"></script>') }) } }],
})
let browser
try {
  await server.listen()
  browser = await chromium.launch({ executablePath: '/usr/bin/chromium', args: ['--no-sandbox'] })
  const page = await browser.newPage({ viewport: { width: 1100, height: 780 } })
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  let requests = 0, failing = false, memoryUsage = 25, diskUsage = 40, releaseFirst
  const firstResponse = new Promise(resolve => { releaseFirst = resolve })
  await page.route('**/api/**', async route => {
    if (new URL(route.request().url()).pathname.endsWith('/resources')) {
      requests++
      if (requests === 1) await firstResponse
      return route.fulfill({ status: failing ? 502 : 200, json: failing ? { message: 'unavailable' } : {
        cpu: { total: 1000 + requests * 100, idle: 700 + requests * 75 },
        memory: { totalBytes: 8192, usedBytes: 2048, usedPercent: memoryUsage }, disk: { totalBytes: 10000, usedBytes: 4000, usedPercent: diskUsage }, collectedAt: new Date().toISOString(),
      } })
    }
    return route.fulfill({ json: new URL(route.request().url()).pathname.endsWith('/csrf') ? { token: 'resource-fixture' } : {} })
  })
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__resources.html`)
  await page.waitForFunction(() => document.querySelectorAll('.status-dot.attached').length === 2)
  assert.equal(await page.locator('.terminal-resource-strip').count(), 2)
  assert.equal(await page.getByRole('button', { name: '复制选中内容', exact: true }).count(), 0)
  assert.equal(await page.locator('.terminal-tools .connection-icon').count(), 0)
  assert.equal(requests, 1, 'split terminals share one host request')
  releaseFirst()
  await page.waitForFunction(() => [...document.querySelectorAll('.terminal-resource-strip')].every(el => el.textContent.includes('内存25%') && el.textContent.includes('磁盘40%')))
  await page.waitForFunction(() => [...document.querySelectorAll('.terminal-resource-strip')].every(el => el.textContent.includes('CPU25%')))
  assert.equal(requests, 2, 'CPU uses two counter samples without duplicate host polling')
  assert.deepEqual(await page.evaluate(() => window.resourceInput), [], 'sampling never injects commands into the shell')
  assert.equal(await page.locator('.terminal-resource-strip').first().locator('[data-state="正常"]').count(), 3)
  memoryUsage = 75; diskUsage = 96
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  await page.waitForFunction(() => document.querySelector('.terminal-resource-strip span:nth-child(1)')?.getAttribute('data-state') === '警告' && document.querySelector('.terminal-resource-strip span:nth-child(3)')?.getAttribute('data-state') === '报警')
  memoryUsage = 25; diskUsage = 40
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  await page.waitForFunction(() => [...document.querySelectorAll('.terminal-resource-strip span')].every(el => el.getAttribute('data-state') === '正常'))
  const strip = page.locator('.terminal-resource-strip').first(), toolbar = page.locator('.terminal-tools').first()
  const stripBox = await strip.boundingBox(), toolbarBox = await toolbar.boundingBox()
  assert(stripBox.y >= toolbarBox.y + toolbarBox.height && stripBox.width < 250)
  assert.equal(await strip.evaluate(el => getComputedStyle(el).pointerEvents), 'none', 'the overlay never intercepts terminal selections')
  const shots = fileURLToPath(new URL('../../artifacts/terminal-resources/', import.meta.url)); await mkdir(shots, {recursive:true})
  await page.screenshot({ path: shots + 'terminal-strips.png' })
  failing = true
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  await page.waitForFunction(() => [...document.querySelectorAll('.terminal-resource-strip')].every(el => el.textContent === '内存--CPU--磁盘--'))
  failing = false
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  await page.waitForFunction(() => [...document.querySelectorAll('.terminal-resource-strip')].every(el => el.textContent.includes('CPU25%')))
  await page.evaluate(() => { window.resourceTest.first = false; window.resourceTest.second = false })
  const stopped = requests
  await page.waitForTimeout(5500)
  assert.equal(requests, stopped, 'hidden terminals stop polling')
  await page.evaluate(() => { window.resourceTest.first = true })
  await page.waitForFunction(() => document.querySelector('.terminal-resource-strip')?.textContent.includes('CPU25%'))
  await page.setViewportSize({ width: 390, height: 780 })
  await page.screenshot({ path: shots + 'terminal-strips-mobile.png' })
  assert.equal(await page.locator('.terminal-pane').first().evaluate(el => el.scrollWidth > el.clientWidth), false)
  assert.deepEqual(errors, [])
  console.log('PASS: removed terminal copy/signal controls; compact status-colored resource overlays; shared host sampling; real CPU deltas; missing data; failure recovery; no shell input; hidden-pane stop/restart; mobile layout')
} catch (error) {
  const page = browser?.contexts()[0]?.pages()[0]
  if (page) {
    const shots = fileURLToPath(new URL('../../artifacts/terminal-resources/', import.meta.url)); await mkdir(shots, { recursive: true })
    await page.screenshot({ path: shots + 'failure.png' })
    console.error(await page.evaluate(() => ({ text: document.body.innerText.slice(0, 1800), dots: [...document.querySelectorAll('.status-dot')].map(el => el.className), panes: document.querySelectorAll('.terminal-pane').length })))
  }
  throw error
} finally { await browser?.close(); await server.close() }
