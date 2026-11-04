import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { mkdir, readFile } from 'node:fs/promises'
import { chromium } from 'playwright-core'

const html = await readFile(new URL('../../desktop/dist/launcher.html', import.meta.url), 'utf8')
const artifacts = new URL('../../artifacts/desktop-launcher/', import.meta.url)
await mkdir(artifacts, { recursive:true })
const server = createServer((req, res) => {
  res.setHeader('Content-Type', 'text/html; charset=utf-8')
  res.end(req.url === '/window.html' ? '<h1>桌面工作台</h1>' : html)
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const base = `http://127.0.0.1:${server.address().port}`
let browser
try {
  browser = await chromium.launch({ executablePath:process.env.CHROMIUM_PATH || '/usr/bin/chromium', args:['--no-sandbox'] })
  const context = await browser.newContext({ viewport:{width:560,height:600} })
  await context.addInitScript(() => {
    const fixture = window.fixture = { calls:[], state:{mode:'',state:'stopped',url:''}, failStart:false, failAutoStart:false, changed:false, close:null }
    const record = (name, ...args) => fixture.calls.push([name, ...args])
    window.runtime = { EventsOn(name, callback) { fixture.close = callback } }
    window.go = { main:{ desktopApp:{
      GetStartup:async () => ({mode:sessionStorage.getItem('startup') || '',minimized:true,autoStart:false,username:'admin'}),
      GetServiceStatus:async () => ({...fixture.state}),
      StartMode:async mode => {
        record('StartMode',mode)
        if (fixture.failStart) { fixture.state = {mode,state:'error',lastError:'8377 端口已被占用'}; throw new Error('8377 端口已被占用') }
        fixture.state = {mode,state:'running',url:'http://127.0.0.1:8377/',startedAt:new Date().toISOString()}
        return {...fixture.state}
      },
      StopService:async () => { record('StopService'); fixture.state = {mode:'service',state:'stopped',url:''}; return {...fixture.state} },
      RestartService:async () => { record('RestartService'); return {...fixture.state} },
      SelectStartupMode:async () => { record('SelectStartupMode'); fixture.state = {mode:'service',state:'stopped',url:''} },
      OpenBrowser:async () => record('OpenBrowser'),
      SetClipboardText:async text => record('SetClipboardText',text),
      SetAutoStart:async enabled => { record('SetAutoStart',enabled); if (fixture.failAutoStart) throw new Error('无法保存自启设置') },
      GetInitialCredentials:async () => fixture.changed ? null : {username:'admin',password:'temporary-password'},
      ChangeAdminPassword:async (password, confirmation) => {
        if (password.length < 10) throw new Error('新密码至少 10 位')
        fixture.changed = true; record('ChangeAdminPassword',password === confirmation)
      },
      Minimize:async () => record('Minimize'),
      QuitService:async () => record('QuitService'),
    } } }
  })
  const page = await context.newPage(), errors = []
  page.on('pageerror', error => errors.push(error.message))
  const calls = name => page.evaluate(name => window.fixture.calls.filter(call => call[0] === name), name)
  await page.goto(base)
  await page.getByRole('button',{name:'服务模式',exact:false}).waitFor()
  assert.equal((await calls('StartMode')).length,0,'Manual launch waits for the selected mode')
  await page.screenshot({path:new URL('startup.png',artifacts).pathname})
  await page.getByRole('button',{name:'服务模式',exact:false}).click()
  await page.getByRole('status').filter({hasText:'运行中'}).waitFor()
  assert.deepEqual(await calls('StartMode'),[['StartMode','service']])
  await page.getByRole('button',{name:'在浏览器打开',exact:true}).click()
  assert.equal((await calls('OpenBrowser')).length,1)
  await page.getByRole('button',{name:'复制',exact:true}).click()
  assert.deepEqual(await calls('SetClipboardText'),[['SetClipboardText','http://127.0.0.1:8377/']])
  await page.getByLabel('开机自启',{exact:true}).check()
  assert.deepEqual(await calls('SetAutoStart'),[['SetAutoStart',true]])
  await page.evaluate(() => window.fixture.failAutoStart = true)
  await page.getByLabel('开机自启',{exact:true}).click()
  await page.getByText('无法保存自启设置',{exact:true}).waitFor()
  assert(await page.getByLabel('开机自启',{exact:true}).isChecked(),'Failed setting restores the actual state')
  await page.locator('#account-details summary').click()
  await page.getByLabel('初始密码',{exact:true}).waitFor()
  assert.equal(await page.getByLabel('初始密码',{exact:true}).getAttribute('type'),'password')
  await page.getByRole('button',{name:'显示密码',exact:true}).click()
  assert.equal(await page.getByLabel('初始密码',{exact:true}).getAttribute('type'),'text')
  await page.getByRole('button',{name:'复制密码',exact:true}).click()
  assert.equal((await calls('SetClipboardText')).at(-1)[1],'temporary-password')
  await page.getByRole('button',{name:'修改密码',exact:true}).click()
  await page.getByLabel('新密码',{exact:true}).fill('short')
  await page.getByLabel('确认新密码',{exact:true}).fill('mismatch')
  await page.getByRole('button',{name:'保存密码',exact:true}).click()
  await page.getByText('两次输入的密码不一致',{exact:true}).waitFor()
  await page.getByLabel('确认新密码',{exact:true}).fill('short')
  await page.getByRole('button',{name:'保存密码',exact:true}).click()
  await page.getByText('新密码至少 10 位',{exact:true}).waitFor()
  await page.getByLabel('新密码',{exact:true}).fill('new-password-12345')
  await page.getByLabel('确认新密码',{exact:true}).fill('new-password-12345')
  await page.getByRole('button',{name:'保存密码',exact:true}).click()
  await page.getByText('密码已更新，请使用新密码重新登录',{exact:true}).waitFor()
  assert.equal(await page.locator('#initial-password').inputValue(),'')
  assert.equal(await page.locator('#new-password').inputValue(),'')
  await page.getByRole('button',{name:'重启',exact:true}).click()
  await page.getByRole('button',{name:'取消',exact:true}).last().click()
  assert.equal((await calls('RestartService')).length,0)
  await page.getByRole('button',{name:'重启',exact:true}).click()
  await page.locator('#confirm-action').click()
  assert.equal((await calls('RestartService')).length,1)
  await page.getByRole('button',{name:'最小化',exact:true}).click()
  assert.equal((await calls('Minimize')).length,1)
  assert.equal((await calls('StopService')).length,0,'Minimize keeps the service running')
  await page.evaluate(() => window.fixture.close())
  await page.getByRole('heading',{name:'退出并停止服务？',exact:true}).waitFor()
  await page.locator('#confirm-minimize').click()
  assert.equal((await calls('Minimize')).length,2)
  assert.equal((await calls('QuitService')).length,0)
  await page.locator('#account-details summary').click()
  await page.setViewportSize({width:480,height:520})
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),'Small manager has no horizontal overflow')
  await page.screenshot({path:new URL('service-manager.png',artifacts).pathname})
  await page.getByRole('button',{name:'停止服务',exact:true}).click()
  await page.locator('#confirm-action').click()
  await page.getByRole('status').filter({hasText:'已停止'}).waitFor()
  assert(await page.getByRole('button',{name:'在浏览器打开',exact:true}).isDisabled())
  await page.evaluate(() => window.fixture.failStart = true)
  await page.getByRole('button',{name:'启动服务',exact:true}).click()
  await page.getByText('8377 端口已被占用',{exact:true}).waitFor()
  await page.evaluate(() => window.fixture.failStart = false)
  await page.getByRole('button',{name:'启动服务',exact:true}).click()
  await page.getByRole('status').filter({hasText:'运行中'}).waitFor()
  await page.getByRole('button',{name:'切换模式',exact:true}).click()
  await page.locator('#confirm-action').click()
  await page.getByRole('heading',{name:'启动方式',exact:true}).waitFor()
  await page.getByRole('button',{name:'窗口模式',exact:false}).click()
  await page.waitForURL('**/window.html')
  await page.getByRole('heading',{name:'桌面工作台',exact:true}).waitFor()
  await page.goto(base)
  await page.evaluate(() => sessionStorage.setItem('startup','service'))
  await page.reload()
  await page.getByRole('status').filter({hasText:'运行中'}).waitFor()
  await page.waitForFunction(() => window.fixture.calls.some(call => call[0] === 'Minimize'))
  await page.getByRole('button',{name:'退出',exact:true}).click()
  await page.locator('#confirm-action').click()
  assert.equal((await calls('QuitService')).length,1)
  assert.deepEqual(errors,[])
  console.log('Desktop launcher passed: mode choice, management, port errors, recovery, browser, auto-start, credentials, password errors, restart, minimize, close confirmation and automatic service startup.')
} finally {
  await browser?.close()
  await new Promise(resolve => server.close(resolve))
}
