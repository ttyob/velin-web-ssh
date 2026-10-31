import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
const require = createRequire(new URL('../terminal/package.json', import.meta.url));
const { chromium } = require('playwright-core');
const assets = new URL('../app/assets/terminal/', import.meta.url);
const server = createServer(async (request, response) => {
  try {
    const name = request.url === '/' ? null : request.url.slice(1);
    if (!name) {
      response.setHeader('Content-Type', 'text/html');
      response.end('<!doctype html><style>body{margin:0}iframe{width:390px;height:500px;border:0}</style><iframe src="/index.html"></iframe><script>window.events=[];addEventListener("message",e=>events.push(e.data))</script>');
    } else {
      if (!/^[a-z.-]+$/.test(name)) throw new Error('invalid path');
      response.setHeader('Content-Type', name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html');
      response.end(await readFile(new URL(name, assets)));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox'], headless: true });
try {
  const page = await browser.newPage({ viewport: { width:390, height:600 }, isMobile:true, hasTouch:true });
  const errors=[]; page.on('pageerror',error=>errors.push(error.message));
  await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.waitForFunction(() => events.some(e=>e.type==='ready'));
  const frame = page.frames()[1];
  const send = message => frame.evaluate(value=>window.Velin.receive(value), { version:1, sessionID:'test', generation:'one', ...message });
  await send({type:'configure',rows:20,cols:40});
  const text = new TextEncoder().encode('hello 中文🙂\r\n');
  await send({type:'write',id:'split-one',data:Buffer.from(text.subarray(0,8)).toString('base64'),bytes:8});
  await send({type:'write',id:'split-two',data:Buffer.from(text.subarray(8)).toString('base64'),bytes:text.length-8});
  await page.waitForFunction(() => events.some(e=>e.type==='rendered'&&e.id==='split-two'));
  assert.match(await frame.locator('.xterm-rows').innerText(), /hello 中文🙂/);
  await send({type:'write',id:'late',generation:'old',data:Buffer.from('WRONG-PAGE').toString('base64'),bytes:10});
  await send({type:'write',id:'large',data:Buffer.from('old output\r\n'.repeat(1000)).toString('base64'),bytes:12000});
  await send({type:'reset'});
  await send({type:'write',id:'after-reset',data:Buffer.from('hello world\r\n<script>window.pwned=true</script>\r\n').toString('base64'),bytes:55});
  await page.waitForFunction(() => events.some(e=>e.type==='rendered'&&e.id==='after-reset'));
  const visible = await frame.locator('.xterm-rows').innerText();
  assert.match(visible,/hello world/);assert.doesNotMatch(visible,/old output|WRONG-PAGE/);
  assert.equal(await frame.evaluate(()=>!!window.pwned),false);
  await send({type:'selection',enabled:true});
  await frame.evaluate(()=>{
    const area=document.querySelector('#viewport');
    const touch=(x,y)=>new Touch({identifier:1,target:area,clientX:x,clientY:y});
    area.dispatchEvent(new TouchEvent('touchstart',{touches:[touch(10,15)],bubbles:true}));
    area.dispatchEvent(new TouchEvent('touchmove',{touches:[touch(65,15)],bubbles:true,cancelable:true}));
    area.dispatchEvent(new TouchEvent('touchend',{touches:[],changedTouches:[touch(65,15)],bubbles:true}));
  });
  await send({type:'copy'});
  await page.waitForFunction(()=>events.some(e=>e.type==='selection'&&e.text.startsWith('hello')));
  assert.deepEqual(errors,[]);
  console.log('Mobile xterm browser checks passed: UTF-8 fragments, page isolation, ordered reset, touch selection, copy, output escaping.');
} finally { await browser.close();await new Promise(resolve=>server.close(resolve)); }
