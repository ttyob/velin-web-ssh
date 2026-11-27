import assert from 'node:assert/strict'
import { mkdtemp, readFile, writeFile, mkdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

const repo = await mkdtemp(join(tmpdir(), 'velin-merge-'))
const file = 'complete file.txt'
const git = (...args) => execFileSync('git', ['-C', repo, ...args], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
const digest = text => createHash('sha256').update(text).digest('hex')
const base = Array.from({ length: 320 }, (_, i) => `line ${i + 1}: unchanged`)
base[1] = 'long line ' + '0123456789'.repeat(220)
const ours = [...base], theirs = [...base]
for (const i of [11, 139, 269]) { ours[i] = `ours ${i}`; theirs[i] = `theirs ${i}` }
let server, browser
try {
  git('init', '-b', 'main'); git('config', 'user.email', 'test@example.invalid'); git('config', 'user.name', 'Merge test')
  await writeFile(join(repo, file), base.join('\n') + '\n'); git('add', '.'); git('commit', '-m', 'base'); git('branch', 'incoming')
  await writeFile(join(repo, file), ours.join('\n') + '\n'); git('commit', '-am', 'ours')
  git('switch', 'incoming'); await writeFile(join(repo, file), theirs.join('\n') + '\n'); git('commit', '-am', 'theirs'); git('switch', 'main')
  assert.throws(() => git('merge', '--no-edit', 'incoming'))
  let working = await readFile(join(repo, file), 'utf8')
  assert.equal((working.match(/^<<<<<<< /gm) || []).length, 3)
  const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8')
  const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)].map(([, path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`).join('\n')
  const entry = `
import {createApp,h,ref,nextTick} from 'vue'
import ElementPlus from 'element-plus'
import {EditorView} from '@codemirror/view'
import Dialog from '/src/components/GitCompareDialog.vue'
import {parseMergeConflicts,joinConflictSides} from '/src/gitMerge.ts'
${styles}
const open=ref(true), merge=ref(true), saved=ref(0)
window.mergeHelpers={parseMergeConflicts,joinConflictSides}
window.compareTest={
 view(side){return EditorView.findFromDOM(document.querySelector('.git-code-pane.'+side+' .cm-editor'))},
 text(side){return this.view(side).state.doc.toString()},
 readonly(){return EditorView.findFromDOM(document.querySelector('.cm-merge-b')).state.doc.toString()},
 async compare(){open.value=false;await nextTick();merge.value=false;await nextTick();open.value=true},
 saved(){return saved.value},
}
createApp({render:()=>h(Dialog,{modelValue:open.value,'onUpdate:modelValue':v=>open.value=v,host:{id:'fixture'},repository:${JSON.stringify(repo)},change:{path:${JSON.stringify(file)},index:'U',worktree:'U'},merge:merge.value,aiConfigured:false,onSaved:()=>saved.value++})}).use(ElementPlus).mount('#app')
`
  server = await createServer({ root: fileURLToPath(new URL('..', import.meta.url)), server: { host: '127.0.0.1', port: 0, proxy: {} }, cacheDir: 'node_modules/.vite-git-compare', optimizeDeps: { esbuildOptions: { target: 'esnext' } }, plugins: [{ name: 'merge-test', resolveId: id => id === '/__compare.js' ? id : undefined, load: id => id === '/__compare.js' ? entry : undefined, configureServer(vite) { vite.middlewares.use('/__compare.html', (_, response) => { response.setHeader('Content-Type', 'text/html'); response.end('<div id="app"></div><script type="module" src="/__compare.js"></script>') }) } }] })
  await server.listen()
  browser = await chromium.launch({ executablePath: '/usr/bin/chromium', args: ['--no-sandbox'] })
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })
  page.setDefaultTimeout(15000)
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url())
    let body = {}, status = 200
    try {
      if (url.pathname === '/api/csrf') body = { token: 'fixture-token' }
      else if (url.pathname.endsWith('/agent/git-text')) {
        const revision = url.searchParams.get('revision'), spec = revision === 'HEAD' ? `HEAD:${file}` : `:${revision}:${file}`
        try { body = { content: git('show', spec), exists: true } } catch { body = { content: '', exists: false } }
      } else if (url.pathname.includes('/sftp/')) {
        working = await readFile(join(repo, file), 'utf8')
        if (request.method() === 'PUT') {
          if (request.headers()['if-match'] !== `"${digest(working)}"`) { status = 409; body = { message: '远程文件已被其他操作修改' } }
          else { working = request.postData(); await writeFile(join(repo, file), working); body = { version: digest(working), bytes: Buffer.byteLength(working) } }
        } else body = { content: working, version: digest(working) }
      } else if (url.pathname.endsWith('/agent/command')) { git('add', '--', file); body = { success: true, output: '' } }
      else throw new Error(url.pathname)
    } catch (error) { status = 500; body = { message: error.message } }
    await route.fulfill({ status, json: body })
  })
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__compare.html`)
  await page.locator('.git-code-pane.result .cm-content').waitFor()
  await page.waitForFunction(() => document.querySelectorAll('.git-code-pane .git-line-conflict').length > 0)
  assert.equal(await page.locator('.git-code-pane').count(), 3)
  assert.equal(await page.evaluate(() => window.compareTest.text('result')), working)
  assert.equal(await page.getByRole('button', { name: '保存并标记已解决', exact: true }).isDisabled(), true)
  const shots = fileURLToPath(new URL('../../artifacts/git-compare/', import.meta.url))
  await mkdir(shots, { recursive: true })
  await page.screenshot({ path: join(shots, 'three-pane.png') })
  const parsed = await page.evaluate(() => window.mergeHelpers.parseMergeConflicts('keep\n<<<<<<<<<< ours\nleft\n|||||||||| base\nold\n==========\nright\n>>>>>>>>>> theirs\ntail'))
  assert.equal(parsed.length, 1); assert.equal(parsed[0].from, 5); assert.equal(parsed[0].left, 'left\n'); assert.equal(parsed[0].right, 'right\n')
  assert.equal(parsed[0].to, 'keep\n<<<<<<<<<< ours\nleft\n|||||||||| base\nold\n==========\nright\n>>>>>>>>>> theirs\n'.length)
  console.log('three-pane loaded')
  await page.getByRole('button', { name: '使用左侧冲突', exact: true }).first().click()
  await page.waitForFunction(() => (window.compareTest.text('result').match(/^<<<<<<< /gm) || []).length === 2)
  await page.getByRole('button', { name: '使用右侧', exact: true }).click()
  await page.waitForFunction(() => (window.compareTest.text('result').match(/^<<<<<<< /gm) || []).length === 1)
  await page.getByRole('button', { name: '合并两侧（左后右）', exact: true }).click()
  await page.waitForFunction(() => !window.compareTest.text('result').includes('<<<<<<<'))
  const resolved = [...base]; resolved[11] = 'ours 11'; resolved[139] = 'theirs 139'; resolved[269] = 'ours 269\ntheirs 269'
  assert.equal(await page.evaluate(() => window.compareTest.text('result')), resolved.join('\n') + '\n')
  console.log('left/right/both selected')
  const content = page.locator('.git-code-pane.result .cm-content')
  await content.click(); await page.keyboard.press('Control+Home'); await page.keyboard.type('// edited outside conflicts\n')
  await page.keyboard.press('Control+End'); await page.keyboard.type('// edited end\n')
  const expected = '// edited outside conflicts\n' + resolved.join('\n') + '\n// edited end\n'
  assert.equal(await page.evaluate(() => window.compareTest.text('result')), expected)
  await page.keyboard.press('Control+z')
  assert.notEqual(await page.evaluate(() => window.compareTest.text('result')), expected)
  await page.keyboard.press('Control+y')
  assert.equal(await page.evaluate(() => window.compareTest.text('result')), expected)
  console.log('whole file edited; undo/redo preserved')
  const vertical = page.getByRole('scrollbar', { name: '全局纵向滚动条' })
  await vertical.focus(); await page.keyboard.press('Home')
  const vr = await vertical.boundingBox()
  await page.mouse.move(vr.x + vr.width / 2, vr.y + 25); await page.mouse.down(); await page.mouse.move(vr.x + vr.width / 2, vr.y + vr.height * .7, { steps: 12 }); await page.mouse.up()
  await page.waitForFunction(() => [...document.querySelectorAll('.git-code-pane .cm-scroller')].every(el => el.scrollTop > 500))
  console.log('vertical dragged')
  const horizontal = page.getByRole('scrollbar', { name: '全局横向滚动条' })
  await horizontal.focus(); await page.keyboard.press('Home')
  const hr = await horizontal.boundingBox()
  await page.mouse.move(hr.x + 40, hr.y + hr.height / 2); await page.mouse.down(); await page.mouse.move(hr.x + 700, hr.y + hr.height / 2, { steps: 14 }); await page.mouse.up()
  await page.waitForFunction(() => [...document.querySelectorAll('.git-code-pane .cm-scroller')].every(el => el.scrollLeft > 50))
  console.log('horizontal dragged')
  await page.getByRole('button', { name: '保存完整文件', exact: true }).click()
  await page.waitForFunction(() => window.compareTest.saved() === 1)
  assert.equal(await readFile(join(repo, file), 'utf8'), expected)
  await writeFile(join(repo, file), expected + 'external writer\n')
  await page.getByRole('button', { name: '保存完整文件', exact: true }).click()
  await page.getByText('远程文件已被其他操作修改', { exact: true }).waitFor()
  assert.equal(await readFile(join(repo, file), 'utf8'), expected + 'external writer\n')
  await writeFile(join(repo, file), expected)
  await page.getByRole('button', { name: '保存并标记已解决', exact: true }).click()
  await page.waitForFunction(() => window.compareTest.saved() === 2)
  assert.equal(git('ls-files', '-u'), ''); assert.equal(git('show', `:${file}`), expected)
  await page.evaluate(() => window.compareTest.compare())
  await page.locator('.git-full-compare-dialog .cm-mergeView').waitFor()
  assert.equal(await page.evaluate(() => window.compareTest.readonly()), expected)
  assert.equal(await page.locator('.git-full-compare-dialog .cm-content[contenteditable="true"]').count(), 0)
  assert(await page.locator('.git-full-compare-dialog .cm-mergeView').evaluate(el => el.scrollHeight > el.clientHeight * 2))
  await page.screenshot({ path: join(shots, 'two-pane.png') })
  assert.deepEqual(errors, [])
  console.log('PASS: real Git conflicts; left/right/both; edit outside conflicts and undo; full save/stage; concurrent write protection; global scrollbar dragging; shared read-only comparison dialog')
} finally { await browser?.close(); await server?.close(); await rm(repo, { recursive: true, force: true }) }
