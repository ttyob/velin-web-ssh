import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

const root = fileURLToPath(new URL('..', import.meta.url))
const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8')
const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)].map(([, path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`).join('\n')
const source = Array.from({ length: 400 }, (_, i) => `const setting${i} = "value ${i}";`)
source[7] = 'const longLine = "' + 'long value '.repeat(80) + '";'
const edited = [...source]
edited[8] = 'const setting8 = "changed";'
edited[90] = '    ' + source[90] + '  '
edited[199] = 'const setting199 = "changed";'
edited[380] = 'const setting380 = "changed";'
const entry = `
import {createApp,h,reactive} from 'vue'
import ElementPlus from 'element-plus'
import {EditorView} from '@codemirror/view'
import Dialog from '/src/components/CodeDiffDialog.vue'
import GitDialog from '/src/components/GitCompareDialog.vue'
import {diffIgnoringWhitespace} from '/src/codeDiff.ts'
${styles}
const data = reactive({open:true,path:'sample.ts',left:${JSON.stringify(source.join('\n'))},right:${JSON.stringify(edited.join('\n'))},git:false})
window.fixture = {data, diffIgnoringWhitespace, view:()=>EditorView.findFromDOM(document.querySelector('.cm-merge-b') || document.querySelector('.cm-editor'))}
createApp({render:()=>data.git ? h(GitDialog,{modelValue:data.open,'onUpdate:modelValue':v=>data.open=v,host:{id:'fixture'},sessionId:'session',repository:'/repo',change:{path:data.path,index:'M',worktree:'M'}}) : h(Dialog,{modelValue:data.open,'onUpdate:modelValue':v=>data.open=v,stateKey:'fixture:'+data.path,path:data.path,left:data.left,right:data.right,leftLabel:'提交版本',rightLabel:'当前文件',loading:false,error:'',canEdit:false,refreshable:false})}).use(ElementPlus).mount('#app')
`
let browser, server
try {
  server = await createServer({ root, server: { host: '127.0.0.1', port: 0, proxy: {} }, cacheDir: 'node_modules/.vite-diff-reading', optimizeDeps: { include: ['vue', 'element-plus'], esbuildOptions: { target: 'esnext' } }, plugins: [{ name: 'diff-reading-test', resolveId: id => id === '/__diff.js' ? id : undefined, load: id => id === '/__diff.js' ? entry : undefined, configureServer(vite) { vite.middlewares.use('/__diff.html', (_, response) => { response.setHeader('Content-Type', 'text/html'); response.end('<div id="app"></div><script type="module" src="/__diff.js"></script>') }) } }] })
  await server.listen()
  browser = await chromium.launch({ executablePath: '/usr/bin/chromium', args: ['--no-sandbox'] })
  const page = await browser.newPage({ viewport: { width: 1500, height: 1000 } })
  page.setDefaultTimeout(15000)
  const errors = [], reads = []
  page.on('pageerror', error => errors.push(error.message))
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=', 'base64')
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url()), mode = url.searchParams.get('mode'), path = url.searchParams.get('path')
    const image = path.endsWith('.png'), binary = path.endsWith('.bin'), huge = path.endsWith('huge.txt')
    if (mode === 'image' || url.pathname.endsWith('/preview-image')) return route.fulfill({ contentType: 'image/png', body: png })
    if (mode === 'info') return route.fulfill({ json: { exists: true, kind: image ? 'image' : binary ? 'binary' : 'text', size: image ? png.length : huge ? 3 * 1024 * 1024 : 300 * 1024 } })
    reads.push(path)
    return route.fulfill({ json: { content: url.pathname.includes('/sftp/') ? 'changed\n' : 'original\n', exists: true, version: 'v1' } })
  })
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__diff.html`)
  const view = page.locator('.code-diff-view')
  await view.locator('.cm-collapsedLines').first().waitFor()
  assert.equal(await view.locator('.code-diff-overview button').count(), 4)
  const foldedCount = await view.locator('.cm-collapsedLines').count()
  await view.locator('.cm-collapsedLines').first().focus(); await page.keyboard.press('Enter')
  assert(await view.locator('.cm-collapsedLines').count() < foldedCount, 'collapsed context expands from the keyboard')
  await view.getByRole('button', { name: '折叠未修改代码', exact: true }).click()
  await page.waitForFunction(() => !document.querySelector('.cm-collapsedLines'))
  assert(await view.locator('.cm-mergeView').evaluate(el => el.scrollHeight > el.clientHeight * 5))
  await view.getByRole('button', { name: '忽略空白差异', exact: true }).click()
  await page.waitForFunction(() => document.querySelectorAll('.code-diff-overview button').length === 3)
  const applied = await page.evaluate(() => {
    const a = '  keep 😀\n    before\n tail ', b = 'keep 😀  \n\t after \n tail'
    let result = a
    for (const c of [...window.fixture.diffIgnoringWhitespace(a,b)].reverse()) result = result.slice(0,c.fromA) + b.slice(c.fromB,c.toB) + result.slice(c.toA)
    return result.split('\n').map(line => line.trim()).join('\n')
  })
  assert.equal(applied, 'keep 😀\nafter\ntail', 'whitespace filtering retains original source offsets')
  assert.deepEqual(await page.evaluate(() => window.fixture.diffIgnoringWhitespace('  same\nnext  ', 'same\n\tnext')), [])
  assert((await page.evaluate(() => window.fixture.diffIgnoringWhitespace('"a b"', '"ab"'))).length > 0, 'spaces inside strings are significant')
  await view.getByRole('button', { name: '忽略空白差异', exact: true }).click()
  const divider = page.getByRole('separator', { name: '调整对比左右宽度' })
  const bounds = await divider.boundingBox()
  await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + 50); await page.mouse.down(); await page.mouse.move(bounds.x + 210, bounds.y + 50, { steps: 10 }); await page.mouse.up()
  assert(Number(await divider.getAttribute('aria-valuenow')) > 60)
  const ratio = await view.locator('.cm-mergeViewEditor').first().evaluate(el => el.clientWidth / el.parentElement.clientWidth)
  assert(ratio > .6 && ratio < .8)
  await divider.dblclick({ position: { x: 3, y: 20 } })
  assert.equal(await divider.getAttribute('aria-valuenow'), '50')
  await view.getByRole('button', { name: '自动换行', exact: true }).click()
  await page.waitForFunction(() => getComputedStyle(document.querySelector('.cm-merge-b .cm-content')).whiteSpace === 'pre-wrap')
  assert(await view.locator('.cm-merge-b .cm-scroller').evaluate(el => el.scrollWidth <= el.clientWidth + 2), 'long lines wrap inside their pane')
  await view.getByRole('button', { name: '自动换行', exact: true }).click()
  await view.getByRole('combobox', { name: '跳转到修改' }).selectOption('3')
  await page.waitForFunction(() => document.querySelector('.cm-mergeView').scrollTop > 5000)
  const scroll = await view.locator('.cm-mergeView').evaluate(el => el.scrollTop)
  await page.evaluate(() => { window.fixture.data.open = false })
  await view.waitFor({ state: 'detached' })
  await page.evaluate(() => { window.fixture.data.path = 'other.ts'; window.fixture.data.open = true })
  await view.locator('.cm-mergeView').waitFor()
  await page.evaluate(() => { window.fixture.data.open = false })
  await view.waitFor({ state: 'detached' })
  await page.evaluate(() => { window.fixture.data.path = 'sample.ts'; window.fixture.data.open = true })
  await page.waitForFunction(previous => Math.abs(document.querySelector('.cm-mergeView')?.scrollTop - previous) < 3, scroll)
  assert.equal(await view.getByRole('combobox', { name: '跳转到修改' }).inputValue(), '3')
  await page.getByRole('button', { name: '切换为行内对比', exact: true }).click()
  await view.locator('.cm-content[aria-label="行内变更对比"]').waitFor()
  await page.evaluate(() => { window.fixture.data.open = false })
  await view.waitFor({ state: 'detached' })
  await page.evaluate(() => { window.fixture.data.open = true })
  await view.locator('.cm-content[aria-label="行内变更对比"]').waitFor()
  assert.equal(await view.getByRole('combobox', { name: '跳转到修改' }).inputValue(), '3')
  await mkdir(fileURLToPath(new URL('../../artifacts/diff-reading/', import.meta.url)), { recursive: true })
  await page.screenshot({ path: fileURLToPath(new URL('../../artifacts/diff-reading/review-dark.png', import.meta.url)) })
  await page.evaluate(() => { document.documentElement.dataset.interfaceTheme = 'light' })
  await page.screenshot({ path: fileURLToPath(new URL('../../artifacts/diff-reading/review-light.png', import.meta.url)) })
  await page.setViewportSize({ width: 390, height: 844 })
  assert(await view.evaluate(el => el.scrollWidth <= el.clientWidth + 1), 'compact toolbar fits a narrow screen')
  await page.screenshot({ path: fileURLToPath(new URL('../../artifacts/diff-reading/review-mobile.png', import.meta.url)) })
  await page.setViewportSize({ width: 1500, height: 1000 })
  console.log('PASS: collapsible context; whitespace filtering and real offsets; draggable/resettable split; wrapping; per-file scroll, hunk and layout memory')
  await page.evaluate(() => { window.fixture.data.open = false })
  await view.waitFor({ state: 'detached' })
  await page.evaluate(() => { window.fixture.data.path = 'large.txt'; window.fixture.data.git = true; window.fixture.data.open = true })
  await page.getByRole('button', { name: '加载完整对比', exact: true }).waitFor()
  assert.equal(reads.length, 0, 'large files do not transfer full content before confirmation')
  assert.equal(await page.locator('.cm-editor').count(), 0)
  await page.getByRole('button', { name: '加载完整对比', exact: true }).click()
  await view.locator('.cm-editor').waitFor()
  assert.equal(reads.length, 2)
  for (const file of ['preview.png', 'binary.bin', 'huge.txt']) {
    await page.evaluate(() => { window.fixture.data.open = false })
    await page.locator('.code-diff-dialog').waitFor({ state: 'hidden' })
    await page.evaluate(path => { window.fixture.data.path = path; window.fixture.data.open = true }, file)
    await page.getByRole('region', { name: '文件预览', exact: true }).waitFor()
    assert.equal(await page.locator('.cm-editor').count(), 0)
    assert.equal(reads.length, 2, 'non-text files never reach the text reader')
    if (file.endsWith('.png')) {
      await page.waitForFunction(() => [...document.querySelectorAll('.diff-preview-media img')].length === 2 && [...document.querySelectorAll('.diff-preview-media img')].every(img => img.complete && img.naturalWidth === 1))
      await mkdir(fileURLToPath(new URL('../../artifacts/diff-reading/', import.meta.url)), { recursive: true })
      await page.screenshot({ path: fileURLToPath(new URL('../../artifacts/diff-reading/images.png', import.meta.url)) })
    } else assert.equal(await page.getByRole('button', { name: '加载完整对比', exact: true }).count(), 0)
  }
  assert.deepEqual(errors, [])
  console.log('PASS: metadata-first loading; explicit large text load; before/after image previews; binary and oversized file information')
} finally { await browser?.close(); await server?.close() }
