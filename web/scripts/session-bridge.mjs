import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8')
const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)].map(([, path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`).join('\n')
const entry = `
import {createApp,h} from 'vue'
import ElementPlus from 'element-plus'
import SessionBridgePanel from '/src/features/session-bridge/SessionBridgePanel.vue'
${styles}
createApp({render:()=>h('main',{style:'max-width:680px;margin:32px auto;padding:16px'},[h(SessionBridgePanel)])}).use(ElementPlus).mount('#app')
`
const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  server: { host: '127.0.0.1', port: 0, proxy: {} },
  cacheDir: 'node_modules/.vite-session-bridge',
  optimizeDeps: { include: ['vue', 'element-plus'], esbuildOptions: { target: 'esnext' } },
  plugins: [{
    name: 'session-bridge-harness',
    resolveId(id) { if(id === '/__bridge__.js') return id },
    load(id) { if(id === '/__bridge__.js') return entry },
    configureServer(vite) { vite.middlewares.use('/__bridge__.html',(_req,res) => {res.setHeader('Content-Type','text/html');res.end('<meta name="viewport" content="width=device-width, initial-scale=1"><div id="app"></div><script type="module" src="/__bridge__.js"></script>')}) },
  }],
})
let browser
try {
  await server.listen()
  browser = await chromium.launch({executablePath:process.env.CHROMIUM_PATH || '/usr/bin/chromium',headless:true,args:['--no-sandbox']})
  const page = await browser.newPage({viewport:{width:1200,height:900}})
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  const errors = []; page.on('pageerror',error => errors.push(error.message))
  let tokens = [], failLoad = false
  const mutations = []
  const secret = 'velin_mcp_' + 'x'.repeat(43)
  await page.route('**/api/**',async route => {
    const request = route.request(), path = new URL(request.url()).pathname, method = request.method()
    let status = 200, data = {}
    if(path === '/api/csrf') data = {token:'test-csrf'}
    else if(path === '/api/hosts') data = [{id:'h1',name:'开发服务器',address:'192.0.2.1',protocol:'ssh'},{id:'rdp',name:'Windows桌面',address:'192.0.2.2',protocol:'rdp'}]
    else if(path === '/api/sessions') data = [{id:'session-1',hostID:'h1',name:'Codex 开发',sessionMode:'tmux',status:'attached'}]
    else if(path.startsWith('/api/session-bridge/tokens')) {
      if(method === 'GET') { if(failLoad) {status=503;data={message:'测试加载失败'}} else data = {tokens,endpoint:'/mcp/sessions',tools:[]} }
      else {
        const body = method === 'DELETE' ? null : request.postDataJSON()
        mutations.push({method,path,body,csrf:request.headers()['x-csrf-token']})
        if(method === 'POST') { const token={...body,id:'token-1',hint:'…xxxxxx',lastUsedAt:null,createdAt:new Date().toISOString()};tokens.push(token);status=201;data={token,secret} }
        if(method === 'PUT') { tokens=tokens.map(token => token.id === path.split('/').at(-1) ? {...token,...body} : token);data=tokens[0] }
        if(method === 'DELETE') { tokens=[];data={ok:true} }
      }
    } else throw new Error(`Unexpected API ${method} ${path}`)
    await route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)})
  })
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`
  await page.goto(origin + '/__bridge__.html')
  await page.getByText('尚未创建访问令牌').waitFor()
  await page.getByRole('button',{name:'创建令牌',exact:true}).click()
  let dialog = page.getByRole('dialog').filter({has:page.locator('.bridge-form')})
  await dialog.getByRole('button',{name:'创建令牌',exact:true}).click()
  await dialog.getByText('请填写令牌名称').waitFor()
  await dialog.locator('input').first().fill('手机开发助手')
  await dialog.getByRole('button',{name:'创建令牌',exact:true}).click()
  await dialog.getByText('请选择至少一个会话或主机').waitFor()
  await dialog.locator('.el-select').first().click()
  await page.getByRole('option').filter({hasText:'Codex 开发'}).click()
  await dialog.locator('input').first().click()
  await dialog.locator('.el-checkbox').filter({hasText:'发送输入'}).click()
  await dialog.getByRole('button',{name:'创建令牌',exact:true}).click()
  const secretDialog = page.getByRole('dialog').filter({has:page.locator('.bridge-secret')})
  await secretDialog.getByText('令牌仅在此展示一次，请立即保存。').waitFor()
  assert.equal(await secretDialog.getByTestId('agent-secret').innerText(),secret)
  await secretDialog.getByRole('button',{name:'复制令牌',exact:true}).click()
  assert.equal(await page.evaluate(() => navigator.clipboard.readText()),secret)
  const config=JSON.parse(await secretDialog.locator('pre').innerText())
  assert.equal(config.mcp.servers.velin.headers.Authorization,'Bearer '+secret)
  assert.equal(config.mcp.servers.velin.url,origin+'/mcp/sessions')
  await secretDialog.getByRole('button',{name:'已保存，关闭'}).click()
  await secretDialog.waitFor({state:'hidden'})
  await page.waitForFunction(value => !document.body.textContent.includes(value),secret)
  assert.deepEqual(tokens[0].sessionIDs,['session-1'])
  assert.deepEqual(tokens[0].permissions,['read','input'])
  assert.equal(mutations[0].csrf,'test-csrf')
  await page.getByRole('button',{name:'编辑令牌'}).click()
  dialog=page.getByRole('dialog').filter({has:page.locator('.bridge-form')})
  await dialog.locator('input').first().fill('随身开发')
  await dialog.locator('.el-select').nth(1).click()
  await page.getByRole('option').filter({hasText:'开发服务器'}).click()
  assert.equal(await page.getByRole('option').filter({hasText:'Windows桌面'}).count(),0)
  await dialog.locator('input').first().click()
  await dialog.locator('.el-checkbox').filter({hasText:'创建 / 恢复'}).click()
  await dialog.getByRole('button',{name:'保存授权'}).click()
  await dialog.waitFor({state:'hidden'})
  assert.equal(tokens[0].name,'随身开发')
  await page.locator('.bridge-token strong').filter({hasText:'随身开发'}).waitFor()
  await page.getByRole('button',{name:'停用',exact:true}).click()
  await page.locator('.bridge-token').getByText('已停用',{exact:true}).waitFor()
  assert.equal(tokens[0].enabled,false)
  assert.deepEqual(tokens[0].hostIDs,['h1'])
  assert.deepEqual(Object.keys(mutations.at(-1).body).sort(),['enabled','expiresAt','hostIDs','name','permissions','sessionIDs'])
  await page.locator('.el-message').last().waitFor({state:'hidden'})
  const artifactDir = new URL('../../artifacts/session-bridge/',import.meta.url)
  await mkdir(artifactDir,{recursive:true})
  await page.screenshot({path:fileURLToPath(new URL('settings-desktop.png',artifactDir)),fullPage:true})
  await page.setViewportSize({width:390,height:844})
  await page.getByRole('button',{name:'编辑令牌'}).click()
  await page.getByRole('dialog').filter({has:page.locator('.bridge-form')}).waitFor()
  await page.locator('.bridge-dialog').evaluate(async panel => {
    const overlay=panel.closest('.el-overlay')
    await Promise.all([panel,...overlay.querySelectorAll('*'),overlay].flatMap(el => el.getAnimations()).map(animation => animation.finished.catch(() => {})))
  })
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),'narrow layout overflows')
  await page.screenshot({path:fileURLToPath(new URL('settings-narrow.png',artifactDir)),fullPage:true})
  await page.getByRole('button',{name:'取消',exact:true}).click()
  await page.getByRole('button',{name:'删除令牌'}).click()
  await page.getByRole('button',{name:'删除',exact:true}).click()
  await page.getByText('尚未创建访问令牌').waitFor()
  failLoad=true
  await page.getByRole('button',{name:'刷新令牌列表'}).click()
  await page.getByRole('alert').filter({hasText:'测试加载失败'}).waitFor()
  assert.deepEqual(errors,[])
  console.log('Session bridge UI: create, one-time secret, scoped grants, CSRF, edit, disable, delete, error and narrow layout passed')
} finally { await browser?.close();await server.close() }
