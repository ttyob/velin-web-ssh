import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Real xterm and panel, isolated SSH peer with delayed geometry ACKs. Run a
// regular terminal concurrently to detect accidental global resize suspension.
const entry = `
import { createApp, h, ref } from 'vue'
import ElementPlus from 'element-plus'
import Floating from '/src/components/FloatingTerminalPanel.vue'
import TerminalPane from '/src/components/TerminalPane.vue'
import '/src/styles.css'
import '/src/styles-refresh.css'
import '/src/styles-polish.css'
import '/src/styles-code-workspace.css'
import '@xterm/xterm/css/xterm.css'
window.records=[]; window.resizeDebug={}; window.terminals={}; window.sockets={}; window.connections=0;
window.autoAck=true; window.streaming=true;
class Socket {
  static OPEN=1; static CONNECTING=0; readyState=1; cols=80; rows=24; version=1;
  constructor(url) {
    this.id=new URL(url).pathname.split('/').at(-1); window.sockets[this.id]=this; window.connections++;
    queueMicrotask(()=>{this.onopen?.();this.receive({type:'hello',clientID:'test',controller:'test',status:'attached',geometryVersion:this.version});this.receive({type:'replay_end'});this.output('history-sentinel\\r\\n');if(this.id==='floating')this.output('\\x1b[?1049h');});
  }
  receive(message) { this.onmessage?.({data:JSON.stringify(message)}); }
  output(text) { this.receive({type:'output',data:btoa(text)}); }
  ack(message) { this.cols=message.cols;this.rows=message.rows;this.receive({type:'geometry',cols:this.cols,rows:this.rows,geometryVersion:++this.version}); }
  send(data) {
    const m=JSON.parse(data);
    if(m.type==='resize') {
      window.records.push({...m,type:'remote',id:this.id,at:performance.now()});
      this.pending=m;
      if(window.autoAck)setTimeout(()=>{if(this.readyState===1)this.ack(m)},160);
    }
  }
  close(){this.readyState=3}
}
window.WebSocket=Socket;
const bounds=ref({width:640,height:300,right:12,bottom:12}), resizing=ref(false), open=ref(true), active=ref(true), normalWidth=ref(450);
window.setNormalWidth=width=>normalWidth.value=width; window.setOpen=value=>open.value=value;window.setActive=value=>active.value=value;
const preferences={terminalTheme:'velin',fontSize:14,lineHeight:1.2,fontWeight:400,letterSpacing:0,background:'#111318',cursorStyle:'block',cursorBlink:false};
const session=id=>({id,name:id,hostID:'fixture',sessionMode:'normal',status:'attached'});
createApp({render:()=>h('div',{style:'position:relative;width:1000px;height:650px'},[
 h('div',{style:{width:normalWidth.value+'px',height:'250px'},class:'normal'},[h(TerminalPane,{session:session('normal'),preferences,visible:true})]),
 h(Floating,{modelValue:open.value,active:active.value,bounds:bounds.value,hostName:'fixture','onUpdate:modelValue':v=>open.value=v,'onUpdate:bounds':v=>bounds.value=v,onResizing:v=>resizing.value=v},()=>h('div',{class:'code-terminal-mount'},[
 h(TerminalPane,{session:session('floating'),preferences,visible:open.value,floating:true,resizeSuspended:resizing.value})]))
])}).use(ElementPlus).mount('#app');
window.draw=()=>{const s=window.sockets.floating;if(!s)return;const width=s.cols-2;s.output('\\x1b[H\\x1b[2K'+ '-'.repeat(width)+'\\r\\n\\x1b[2KCodex frame at '+s.cols+' columns\\r\\n\\x1b[2K'+ 'x'.repeat(width)+'\\r\\n\\x1b[2Kready> ')};
setInterval(()=>{if(window.streaming)window.draw()},25);
`
const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  cacheDir: 'node_modules/.vite-floating-resize' + (process.argv.includes('--windows') ? '-windows' : ''), optimizeDeps: { esbuildOptions: { target: 'esnext' } },
  server: { host: '127.0.0.1', port: 0 },
  plugins: [{ name: 'floating-terminal-test', enforce: 'pre',
    resolveId: id => id === '/__floating__.js' ? id : undefined,
    load: id => id === '/__floating__.js' ? entry : undefined,
    transform(source, id) {
      if (id.endsWith('/src/components/TerminalPane.vue')) return source.replace('terminal.open(container.value!);',
        `terminal.open(container.value!);window.terminals[props.session.id]=terminal;window.resizeDebug[props.session.id]=()=>({pendingWrites,resizeAfterWrite,suspended:props.resizeSuspended,cols:terminal.cols,rows:terminal.rows,proposed:fit.proposeDimensions(),bounds:container.value.getBoundingClientRect().toJSON()});terminal.onResize(({cols,rows})=>window.records.push({type:'local',id:props.session.id,at:performance.now(),cols,rows}));`)
    },
    configureServer(vite) { vite.middlewares.use('/__floating__.html', (_, res) => { res.setHeader('Content-Type','text/html');res.end('<div id="app"></div><script type="module" src="/__floating__.js"></script>') }) },
  }],
})
const windowsRendering = process.argv.includes('--windows')
let browser, page
try {
  await server.listen()
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox'] })
  page = await browser.newPage({ viewport: { width: 1150, height: 850 } })
  if (windowsRendering) await page.addInitScript(() => Object.defineProperty(navigator, 'platform', { get: () => 'Win32' }))
  page.setDefaultTimeout(8000)
  const errors=[]
  page.on('pageerror', error=>errors.push(error.message))
  await page.route('**/api/**', route=>route.fulfill({json:route.request().url().endsWith('/csrf')?{token:'fixture'}:{}}))
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__floating__.html`)
  await page.waitForFunction(()=>window.records.filter(r=>r.type==='remote').length>=2)
  await page.waitForTimeout(1350)
  const float=page.locator('.code-floating-terminal')
  const begin=async()=>{const b=await float.locator('.handle-nw').boundingBox();const point={x:b.x+6,y:b.y+6};await page.mouse.move(point.x,point.y);await page.mouse.down();return point}
  const count=()=>page.evaluate(()=>window.records.filter(r=>r.id==='floating').length)
  const clear=()=>page.evaluate(()=>{window.records.length=0})
  await clear()
  const start=await begin()
  for(let i=1;i<=5;i++){await page.mouse.move(start.x+i*24,start.y+i*8);await page.waitForTimeout(145)}
  assert.equal(await count(),0,'slow drag must not resize either grid, even during pauses and continuous output')
  await page.evaluate(()=>window.setNormalWidth(520))
  await page.waitForTimeout(260)
  assert.equal(await count(),0,'ordinary terminal layout must not resize the floating grid during drag')
  assert(await page.evaluate(()=>window.records.some(r=>r.id==='normal'&&r.type==='remote')),'ordinary terminal still resizes during a floating gesture')
  await page.mouse.up()
  await page.waitForFunction(()=>window.records.some(r=>r.id==='floating'&&r.type==='remote'),undefined,{timeout:1000})
  const records=await page.evaluate(()=>window.records.filter(r=>r.id==='floating'))
  assert.equal(records.filter(r=>r.type==='local').length,1)
  assert.equal(records.filter(r=>r.type==='remote').length,1)
  assert(records[1].at-records[0].at<20,'send the PTY size in the same turn as the local grid change')
  await page.waitForTimeout(1200)
  const screen=await page.evaluate(()=>{const t=window.terminals.floating;return {cols:t.cols,lines:Array.from({length:4},(_,i)=>t.buffer.active.getLine(i)?.translateToString(true,0,t.cols).trimEnd())}})
  assert.equal(screen.lines[0],'-'.repeat(screen.cols-2))
  assert.equal(screen.lines[1],`Codex frame at ${screen.cols} columns`)
  assert.equal(screen.lines[2],'x'.repeat(screen.cols-2))
  assert.equal(screen.lines[3],'ready>')

  // Arbitrary old-width output and stale/mismatched ACKs cannot remove preview.
  await page.evaluate(()=>{window.autoAck=false;window.records.length=0})
  const again=await begin()
  await page.mouse.move(again.x-75,again.y-35);await page.mouse.up()
  await float.locator('.terminal-resize-snapshot').waitFor()
  await page.waitForTimeout(350)
  assert.equal(await float.locator('.terminal-resize-snapshot').count(),1)
  await page.evaluate(()=>{const s=window.sockets.floating;s.receive({...s.pending,type:'geometry',cols:s.pending.cols+1,geometryVersion:s.version+1});s.receive({...s.pending,type:'geometry',geometryVersion:s.version})})
  await page.waitForTimeout(160)
  assert.equal(await float.locator('.terminal-resize-snapshot').count(),1,'incorrect or stale geometry must not acknowledge the new grid')
  await page.evaluate(()=>{const s=window.sockets.floating;window.streaming=false;s.ack(s.pending);window.draw()})
  await float.locator('.terminal-resize-snapshot').waitFor({state:'detached',timeout:400})

  // Keyboard resize and silent/no-ACK peers have a bounded preview fallback.
  await clear()
  await float.getByRole('separator',{name:'调整悬浮终端大小',exact:true}).focus()
  await page.keyboard.press('ArrowLeft')
  await page.waitForFunction(()=>window.records.some(r=>r.id==='floating'&&r.type==='remote'),undefined,{timeout:1000})
  await float.locator('.terminal-resize-snapshot').waitFor({state:'detached',timeout:1800})

  // New/cancelled gestures cancel pending fits; returning to the initial bounds
  // must not leave a snapshot or suspended state behind.
  await page.evaluate(()=>{window.autoAck=true;window.streaming=true})
  await clear()
  const cancelled=await begin()
  await page.mouse.move(cancelled.x+40,cancelled.y+16)
  await float.locator('.handle-nw').dispatchEvent('pointercancel')
  await page.mouse.up()
  await page.waitForFunction(()=>window.records.some(r=>r.id==='floating'&&r.type==='remote'),undefined,{timeout:1000})
  await page.waitForTimeout(1200)
  await clear()
  const rapid=await begin()
  await page.mouse.move(rapid.x+30,rapid.y+12);await page.mouse.up()
  const rapid2=await begin()
  await page.mouse.move(rapid2.x+30,rapid2.y+12)
  await page.waitForTimeout(180)
  assert.equal(await count(),0,'a second gesture cancels the first pending fit')
  await page.mouse.up()
  await page.waitForTimeout(1300)
  assert.equal(await page.evaluate(()=>window.records.filter(r=>r.id==='floating'&&r.type==='remote').length),1)
  await clear()
  const unchanged=await begin()
  await page.mouse.move(unchanged.x+25,unchanged.y+10)
  await page.waitForTimeout(150)
  await page.mouse.move(unchanged.x,unchanged.y);await page.mouse.up()
  await page.waitForTimeout(300)
  assert.equal(await count(),0,'returning to the original bounds needs no grid change')
  assert.equal(await float.locator('.terminal-resize-snapshot').count(),0)
  const hidden=await begin()
  await page.mouse.move(hidden.x-20,hidden.y-10)
  await page.evaluate(()=>window.setActive(false));await page.mouse.up()
  await page.waitForTimeout(1300)
  await page.evaluate(()=>window.setActive(true))
  await clear()
  await float.getByRole('button',{name:'重置悬浮终端',exact:true}).click()
  await page.waitForFunction(()=>window.records.some(r=>r.id==='floating'&&r.type==='remote'),undefined,{timeout:1000})
  await page.waitForTimeout(1200)
  await page.evaluate(()=>{window.streaming=false;window.sockets.floating.output('\x1b[?1049l')})
  await page.waitForFunction(()=>window.terminals.floating.buffer.active.type==='normal')
  assert(await page.evaluate(()=>{const b=window.terminals.floating.buffer.active;return Array.from({length:b.length},(_,i)=>b.getLine(i)?.translateToString(true)).some(l=>l?.includes('history-sentinel'))}),'normal buffer history survives TUI resize')
  await page.evaluate(()=>window.setOpen(false));await page.evaluate(()=>window.setOpen(true))
  await page.waitForTimeout(300)
  assert.equal(await page.evaluate(()=>window.connections),2,'resize and reopening must preserve both sockets')
  assert.deepEqual(errors,[])
  console.log('Floating resize: one final grid, immediate PTY request, ACK/render preview, TUI output, ordinary pane isolation, keyboard/cancel/rapid gestures and history/socket preservation passed')
} catch (error) {
  console.error(JSON.stringify(await page?.evaluate(()=>({records:window.records,debug:Object.fromEntries(Object.entries(window.resizeDebug||{}).map(([id,read])=>[id,read()]))})),null,2))
  throw error
} finally { await browser?.close();await server.close() }
