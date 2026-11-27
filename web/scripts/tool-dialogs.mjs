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
import DockerDialog from '/src/components/DockerDialog.vue'
import HostMonitorDialog from '/src/components/HostMonitorDialog.vue'
const host={id:'tool-host',name:'开发服务器',address:'192.0.2.10',port:22,username:'developer',protocol:'ssh',credentialID:'',groupName:'',tags:'',notes:'',initialDirectory:'',connectTimeout:10,keepaliveInterval:20,maxRetries:3,terminalType:'xterm-256color',sessionMode:'tmux',jumpHostID:'',rdpMode:'web',rdpQuality:'crisp',rdpClipboard:true,rdpAudio:false,rdpDrive:false,rdpPrinting:false,rdpMultiMonitor:false,desktopDomain:'',desktopSecurity:'any',ignoreCertificate:false,desktopReadOnly:false}
const fixture=reactive({tool:''})
window.dialogFixture={show:tool=>fixture.tool=tool}
createApp({render:()=>h('main',{style:'min-height:100vh;background:var(--bg)'},[
  h(DockerDialog,{modelValue:fixture.tool==='docker',host,sessionId:'tool-session','onUpdate:modelValue':value=>{if(!value&&fixture.tool==='docker')fixture.tool=''}}),
  h(HostMonitorDialog,{modelValue:fixture.tool==='monitor',host,'onUpdate:modelValue':value=>{if(!value&&fixture.tool==='monitor')fixture.tool=''}}),
])}).use(ElementPlus).mount('#app')
setTimeout(()=>fixture.tool='docker',0)
`
const server = await createServer({ root: fileURLToPath(new URL('..', import.meta.url)), server: { host: '127.0.0.1', port: 0, proxy: {} }, cacheDir: 'node_modules/.vite-tool-dialogs', optimizeDeps: { esbuildOptions: { target: 'esnext' } }, plugins: [{ name: 'tool-dialog-fixture', resolveId: id => id === '/__tool_dialogs.js' ? id : undefined, load: id => id === '/__tool_dialogs.js' ? entry : undefined, configureServer(vite) { vite.middlewares.use('/__tool_dialogs.html', (_, response) => { response.setHeader('Content-Type', 'text/html'); response.end('<div id="app"></div><script type="module" src="/__tool_dialogs.js"></script>') }) } }] })
const snapshot = { system: { hostname: 'dev-app-01', os: 'Linux', arch: 'x86_64', kernel: '6.8.0' }, uptimeSeconds: 345678, load1: 0.42, load5: 0.55, load15: 0.61, memory: { totalBytes: 17179869184, availableBytes: 9663676416, usedBytes: 7516192768, usedPercent: 43.8 }, disks: [{ path: '/', totalBytes: 536870912000, freeBytes: 322122547200, usedBytes: 214748364800, usedPercent: 40 }], collectedAt: new Date().toISOString() }
const shots = fileURLToPath(new URL('../../artifacts/tool-dialogs/', import.meta.url))
let browser
try {
  await mkdir(shots, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ executablePath: '/usr/bin/chromium', args: ['--no-sandbox'] })
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } })
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  await page.route('**/api/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname
    if (path.endsWith('/csrf')) return route.fulfill({ json: { token: 'tool-dialog-fixture' } })
    if (path.endsWith('/agent/snapshot')) return route.fulfill({ json: snapshot })
    if (path.endsWith('/agent/processes')) return route.fulfill({ json: [] })
    if (path.endsWith('/agent')) return route.fulfill({ json: { hostID: 'tool-host', state: 'connected', backend: 'velin', aiConfigured: false } })
    if (path.endsWith('/agent/command')) {
      const command = request.postDataJSON()?.command || ''
      let output = ''
      if (command.includes('cores=$(getconf')) output = [
        ['cores','8'].join(String.fromCharCode(9)), ['cpu','24000','18000'].join(String.fromCharCode(9)),
        ['network','104857600','52428800'].join(String.fromCharCode(9)), '__LOGS__',
      ].join(String.fromCharCode(10))
      else output = [
        ['5ad310c8f246','web-api','registry.example/web-api:latest','Up 2 hours','0.0.0.0:8080->8080/tcp','app-net'].join(String.fromCharCode(9)),
        ['3b37b34a719d','postgres','postgres:16-alpine','Exited (0) 3 hours ago','5432/tcp','app-net'].join(String.fromCharCode(9)),
        'MODES', ['5ad310c8f246','bridge'].join(String.fromCharCode(9)), ['3b37b34a719d','bridge'].join(String.fromCharCode(9)),
      ].join(String.fromCharCode(10))
      return route.fulfill({ json: { output, success: true } })
    }
    return route.fulfill({ json: {} })
  })
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__tool_dialogs.html`)
  const docker = page.locator('.el-dialog.docker-dialog')
  await docker.locator('.docker-row').first().waitFor()
  assert((await docker.boundingBox()).width <= 1001)
  assert((await docker.locator('.el-dialog__header').boundingBox()).height <= 44)
  assert((await docker.locator('.docker-row').first().boundingBox()).height <= 58)
  assert.equal(await docker.evaluate(el => Math.round(parseFloat(getComputedStyle(el).borderTopWidth))), 1)
  await docker.screenshot({ path: shots + 'docker-desktop.png' })
  await page.setViewportSize({ width: 430, height: 820 })
  await page.waitForTimeout(100)
  assert.equal(await docker.locator('.el-dialog__body').evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
  const mobileMain = await docker.locator('.docker-row').first().locator('.docker-row-main').boundingBox()
  const mobileActions = await docker.locator('.docker-row').first().locator('.docker-row-actions').boundingBox()
  assert(mobileActions.y >= mobileMain.y + mobileMain.height, 'mobile Docker actions must sit below resource details')
  await docker.screenshot({ path: shots + 'docker-mobile.png' })
  await page.setViewportSize({ width: 1280, height: 860 })
  await page.evaluate(() => window.dialogFixture.show('monitor'))
  const monitor = page.locator('.el-dialog.host-monitor-dialog')
  await monitor.locator('.monitor-metric').first().waitFor()
  assert((await monitor.boundingBox()).width <= 1001)
  assert((await monitor.locator('.el-dialog__header').boundingBox()).height <= 44)
  assert.equal(await monitor.evaluate(el => Math.round(parseFloat(getComputedStyle(el).borderTopWidth))), 1)
  await monitor.screenshot({ path: shots + 'monitor-desktop.png' })
  await page.setViewportSize({ width: 430, height: 820 })
  await page.waitForTimeout(100)
  assert.equal(await monitor.locator('.el-dialog__body').evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
  await monitor.screenshot({ path: shots + 'monitor-mobile.png' })
  assert.deepEqual(errors, [])
  console.log('PASS: compact Docker and host monitor dialogs render with thin frames, dense rows and no mobile overflow')
} catch (error) {
  const page = browser?.contexts()[0]?.pages()[0]
  if (page) await page.screenshot({ path: shots + 'failure.png' })
  throw error
} finally { await browser?.close(); await server.close() }
