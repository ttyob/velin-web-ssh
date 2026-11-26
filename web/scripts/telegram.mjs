import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

const main = await readFile(new URL('../src/main.ts',import.meta.url),'utf8')
const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)].map(([,path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`).join('\n')
const entry = `
import {createApp,h} from 'vue'
import {createPinia} from 'pinia'
import ElementPlus from 'element-plus'
import TelegramPanel from '/src/features/telegram/TelegramPanel.vue'
import {useAuthStore} from '/src/stores/auth'
${styles}
const layout=document.createElement('style');layout.textContent='html,body,#app{height:auto;min-height:100%;overflow:visible}';document.head.appendChild(layout)
const pinia=createPinia()
const auth=useAuthStore(pinia)
auth.user={id:'owner',username:'owner',role:'admin',forcePasswordChange:false}
window.harnessRole=role=>{auth.user.role=role}
createApp({render:()=>h('main',{style:'max-width:680px;margin:28px auto;padding:16px'},[h(TelegramPanel)])}).use(pinia).use(ElementPlus).mount('#app')
`
const server = await createServer({root:fileURLToPath(new URL('..',import.meta.url)),server:{host:'127.0.0.1',port:0,proxy:{}},cacheDir:'node_modules/.vite-telegram',
  optimizeDeps:{include:['vue','pinia','element-plus'],esbuildOptions:{target:'esnext'}},plugins:[{
    name:'telegram-harness',resolveId(id){if(id==='/__telegram__.js')return id},load(id){if(id==='/__telegram__.js')return entry},
    configureServer(vite){vite.middlewares.use('/__telegram__.html',(_req,res)=>{res.setHeader('Content-Type','text/html');res.end('<meta name="viewport" content="width=device-width, initial-scale=1"><div id="app"></div><script type="module" src="/__telegram__.js"></script>')})},
  }]})
let browser
try {
  await server.listen()
  browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH||'/usr/bin/chromium',headless:true,args:['--no-sandbox']})
  const page=await browser.newPage({viewport:{width:1200,height:1000}})
  await page.context().grantPermissions(['clipboard-read','clipboard-write'])
  page.setDefaultTimeout(10000)
  const errors=[];page.on('pageerror',error=>errors.push(error.message))
  const changes=[]
  let failLoad=false,failTest=false
  const profile={bot:{enabled:false,hasToken:false,hasProxy:false,username:'',publicURL:'',idleSeconds:90,state:'stopped',lastError:''},binding:null,access:null,subscriptions:[]}
  const pair={url:'https://t.me/velin_fixture_bot?start=fixture_binding_code',command:'/start fixture_binding_code',expiresAt:new Date(Date.now()+600000).toISOString()}
  await page.route('**/api/**',async route=>{
    const req=route.request(),path=new URL(req.url()).pathname,method=req.method()
    let status=200,data={}
    if(path==='/api/csrf')data={token:'fixture-csrf'}
    else if(path==='/api/hosts')data=[{id:'host',name:'开发服务器',protocol:'ssh'},{id:'desktop',name:'远程桌面',protocol:'rdp'}]
    else if(path==='/api/sessions')data=[{id:'session-1',name:'后端开发',hostID:'host',sessionMode:'tmux'}]
    else if(path==='/api/telegram'&&method==='GET'){if(failLoad){status=503;data={message:'测试加载失败'}}else data=profile}
    else {
      const body=req.postData()?req.postDataJSON():null
      changes.push({method,path,body,csrf:req.headers()['x-csrf-token']})
      if(path==='/api/admin/telegram/test'){if(failTest){status=400;data={message:'无法连接 Telegram，请检查代理'}}else data={username:'velin_fixture_bot'}}
      else if(path==='/api/admin/telegram'){
        Object.assign(profile.bot,{enabled:body.enabled,hasToken:true,username:'velin_fixture_bot',publicURL:body.publicURL,idleSeconds:body.idleSeconds,state:body.enabled?'running':'stopped'})
        if(Object.hasOwn(body,'proxyURL'))profile.bot.hasProxy=!!body.proxyURL
        data=profile.bot
      }
      else if(path==='/api/telegram/access'){
        profile.access={...body,enabled:true};profile.binding={...(profile.binding||{active:false,telegramUserID:0,name:'',boundAt:''}),allowFiles:body.allowFiles};data={ok:true}
      }
      else if(path==='/api/telegram/pair')data=pair
      else if(path.startsWith('/api/telegram/subscriptions/')){profile.subscriptions=[];data={ok:true}}
      else if(path==='/api/telegram/binding'){profile.binding=null;profile.access=null;profile.subscriptions=[];data={ok:true}}
      else throw new Error(`Unexpected API ${method} ${path}`)
    }
    await route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)})
  })
  const origin=`http://127.0.0.1:${server.httpServer.address().port}`
  await page.goto(origin+'/__telegram__.html')
  await page.getByText('机器人尚未配置',{exact:true}).waitFor()
  await page.getByRole('button',{name:'保存会话授权',exact:true}).click()
  await page.getByText('请选择至少一个会话或 SSH 主机',{exact:true}).waitFor()
  await page.getByLabel('Bot Token',{exact:true}).fill('123456:fixture_bot_token_123456789012345')
  await page.locator('.tg-config .el-switch').click()
  await page.getByLabel('手机访问的工作台地址',{exact:false}).fill('https://ssh.example.com')
  await page.getByLabel('Telegram 代理',{exact:false}).fill('socks5://127.0.0.1:1080')
  failTest=true
  await page.getByRole('button',{name:'测试连接',exact:true}).click()
  await page.getByText('无法连接 Telegram，请检查代理',{exact:true}).waitFor()
  failTest=false
  await page.getByRole('button',{name:'测试连接',exact:true}).click()
  await page.getByText('已连接 @velin_fixture_bot，保存后生效',{exact:true}).waitFor()
  await page.getByRole('button',{name:'保存机器人配置',exact:true}).click()
  await page.getByText('运行中',{exact:true}).waitFor()
  assert.equal(await page.getByLabel('Bot Token',{exact:true}).inputValue(),'','Saved secret is cleared')
  const savedConfig=changes.find(c=>c.path==='/api/admin/telegram')
  assert.equal(savedConfig.body.proxyURL,'socks5://127.0.0.1:1080')
  assert.equal(savedConfig.csrf,'fixture-csrf')
  await page.locator('.tg-config summary').click()
  await page.getByRole('button',{name:'保存机器人配置',exact:true}).click()
  await page.getByText('机器人已保存并启动',{exact:true}).last().waitFor()
  assert(!Object.hasOwn(changes.filter(c=>c.path==='/api/admin/telegram').at(-1).body,'proxyURL'),'Unedited proxy is preserved')
  await page.locator('.el-select').filter({has:page.getByRole('combobox',{name:'Telegram 允许的会话'})}).click()
  await page.getByRole('option').filter({hasText:'后端开发'}).click()
  await page.locator('.tg-heading').filter({hasText:'会话授权'}).click()
  await page.locator('.tg-permissions .el-checkbox').filter({hasText:'发送需求 / 按键'}).click()
  await page.locator('.tg-permissions .el-checkbox').filter({hasText:'项目文件传输'}).click()
  assert(await page.getByRole('button',{name:'生成绑定链接',exact:true}).isDisabled(),'Unsaved scopes cannot be paired')
  await page.getByRole('button',{name:'保存会话授权',exact:true}).click()
  await page.getByRole('button',{name:'生成绑定链接',exact:true}).waitFor({state:'visible'})
  await page.getByRole('button',{name:'生成绑定链接',exact:true}).click()
  await page.getByText('用手机扫描，或打开 Telegram 完成绑定',{exact:true}).waitFor()
  await page.getByRole('button',{name:'复制链接',exact:true}).click()
  assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),pair.url)
  assert.deepEqual(profile.access.sessionIDs,['session-1'])
  assert.deepEqual(profile.access.permissions,['read','input'])
  assert(profile.binding.allowFiles)
  profile.binding={...profile.binding,active:true,telegramUserID:42,name:'手机开发 (@fixture)',boundAt:new Date().toISOString()}
  profile.subscriptions=[{sessionID:'session-1',name:'后端开发',hostName:'开发服务器'}]
  await page.getByRole('button',{name:'刷新 Telegram 状态',exact:true}).click()
  await page.getByText('已绑定',{exact:true}).waitFor()
  assert.equal(await page.locator('.tg-pair').count(),0,'Consumed link is cleared')
  const artifacts=new URL('../../artifacts/telegram/',import.meta.url);await mkdir(artifacts,{recursive:true})
  await page.locator('.el-message').last().waitFor({state:'hidden'})
  await page.screenshot({path:fileURLToPath(new URL('settings-desktop.png',artifacts)),fullPage:true})
  await page.setViewportSize({width:390,height:844})
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Telegram settings overflow on mobile')
  await page.screenshot({path:fileURLToPath(new URL('settings-mobile.png',artifacts)),fullPage:true})
  await page.getByRole('button',{name:'关闭通知',exact:true}).click()
  await page.getByText('尚未订阅会话',{exact:true}).waitFor()
  await page.evaluate(()=>window.harnessRole('user'))
  assert.equal(await page.locator('.tg-config').count(),0,'Normal user cannot edit bot configuration')
  await page.getByRole('button',{name:'解除绑定',exact:true}).click()
  await page.getByRole('button',{name:'解除绑定',exact:true}).last().click()
  await page.getByRole('button',{name:'生成绑定链接',exact:true}).waitFor()
  assert(await page.getByRole('button',{name:'生成绑定链接',exact:true}).isDisabled())
  failLoad=true
  await page.getByRole('button',{name:'刷新 Telegram 状态',exact:true}).click()
  await page.getByText('测试加载失败',{exact:true}).waitFor()
  assert.deepEqual(errors,[])
  console.log('Telegram settings passed: config test/save, proxy preservation, scope validation, CSRF, pairing, binding, subscriptions, user permissions, unbind, errors and mobile layout.')
} finally {await browser?.close();await server.close()}
