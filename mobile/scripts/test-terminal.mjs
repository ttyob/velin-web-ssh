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
  await frame.waitForFunction(() => document.querySelector('.xterm-rows').innerText.includes('hello 中文🙂'));
  assert.match(await frame.locator('.xterm-rows').innerText(), /hello 中文🙂/);
  await send({type:'write',id:'late',generation:'old',data:Buffer.from('WRONG-PAGE').toString('base64'),bytes:10});
  await send({type:'write',id:'large',data:Buffer.from('old output\r\n'.repeat(1000)).toString('base64'),bytes:12000});
  await send({type:'reset'});
  await send({type:'write',id:'after-reset',data:Buffer.from('hello world\r\n<script>window.pwned=true</script>\r\n').toString('base64'),bytes:55});
  await page.waitForFunction(() => events.some(e=>e.type==='rendered'&&e.id==='after-reset'));
  await frame.waitForFunction(() => document.querySelector('.xterm-rows').innerText.includes('hello world'));
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

  // A top-level mobile document exercises the WebView's viewport magnification.
  const touchPage = await browser.newPage({ viewport: { width: 390, height: 600 }, isMobile: true, hasTouch: true });
  touchPage.on('pageerror', error => errors.push(error.message));
  await touchPage.addInitScript(() => {
    window.events = [];
    window.VelinTerminal = { postMessage: message => window.events.push(JSON.parse(message)) };
  });
  await touchPage.goto(`http://127.0.0.1:${server.address().port}/index.html`);
  await touchPage.waitForFunction(() => events.some(e => e.type === 'ready'));
  const touchSend = message => touchPage.evaluate(value => window.Velin.receive(value), { version: 1, sessionID: 'touch', generation: 'one', ...message });
  await touchSend({ type: 'configure', rows: 20, cols: 120 });
  const sample = 'hello world\r\nnext 中文🙂 text\r\n' + 'wide output '.repeat(9) + '\r\n';
  await touchSend({ type: 'write', id: 'touch', data: Buffer.from(sample).toString('base64'), bytes: Buffer.byteLength(sample) });
  await touchPage.waitForFunction(() => events.some(e => e.type === 'rendered' && e.id === 'touch'));
  await touchPage.waitForFunction(() => document.querySelector('.xterm-rows').innerText.includes('hello world'));
  const cdp = await touchPage.context().newCDPSession(touchPage);
  const cell = await touchPage.locator('.xterm-screen').boundingBox();
  const w = cell.width / 120, h = cell.height / 20;
  const touch = (type, points, changed = points, target = '#viewport') => touchPage.evaluate(({ type, points, changed, target }) => {
    const element = document.querySelector(target);
    const make = (point, i) => new Touch({ identifier: i + 1, target: element, clientX: point.x, clientY: point.y });
    const event = new TouchEvent(type, { touches: points.map(make), changedTouches: changed.map(make), bubbles: true, cancelable: true });
    element.dispatchEvent(event);
    return event.defaultPrevented;
  }, { type, points, changed, target });
  const first = { x: cell.x + w * 2, y: cell.y + h / 2 };
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ ...first, id: 1 }] });
  await touchPage.waitForFunction(() => events.some(e => e.type === 'selectionMode' && e.enabled));
  await touchPage.waitForFunction(() => events.some(e => e.type === 'selection' && e.text === 'hello'));
  assert.equal(await touchPage.locator('.selection-handle:visible').count(), 2, 'Long press must expose both selection handles');
  for (const x of [45, 60, 75, 90]) {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: first.y, id: 1 }] });
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await touchSend({ type: 'copy' });
  assert.match(await touchPage.evaluate(() => events.filter(e => e.type === 'selection').at(-1).text), /^hello world/, 'Long press must allow dragging immediately without a second touch');

  const handle = await touchPage.locator('[data-edge="end"]').boundingBox();
  const grab = { x: handle.x + handle.width / 2, y: handle.y + 8 };
  await touch('touchstart', [grab], [grab], '[data-edge="end"]');
  await touch('touchmove', [{ x: cell.x + w * 12 + 12, y: cell.y + h * 2 + 8 }]);
  await touch('touchend', [], [grab]);
  await touchSend({ type: 'copy' });
  const copied = await touchPage.evaluate(() => events.filter(e => e.type === 'selection').at(-1).text);
  assert.match(copied, /^hello world\nnext 中文🙂/, 'Dragging the handle must include multiple lines and wide Unicode cells');
  assert(!await touchPage.evaluate(() => events.some(e => e.type === 'scroll')), 'Selection must not scroll the remote application');
  assert(!await touchPage.evaluate(() => events.some(e => e.type === 'contextMenu')), 'Touch selection must not open the mouse menu');

  await touchSend({ type: 'selectAll' });
  await touchPage.waitForFunction(() => events.filter(e => e.type === 'selection').at(-1)?.text.includes('next 中文🙂'));
  assert.match(await touchPage.evaluate(() => events.filter(e => e.type === 'selection').at(-1).text), /hello world/);

  await touchSend({ type: 'selection', enabled: false });
  assert.equal(await touchPage.locator('.selection-handle:visible').count(), 0);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 320, y: 200 }] });
  for (const x of [290, 260, 230, 200, 170, 140]) {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: 200 }] });
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await touchPage.waitForFunction(() => document.querySelector('#viewport').scrollLeft > 100);
  assert(await touchPage.locator('#viewport').evaluate(element => element.scrollLeft) > 100, 'One-finger horizontal browsing must still work');
  await touchPage.locator('#viewport').evaluate(element => { element.scrollLeft = 0; });

  await touchSend({ type: 'control', enabled: true });
  await touchSend({ type: 'application', remoteHistory: true, alternate: true });
  // Application scrolling targets the transcript (not the finger) and coasts after release.
  const cellNow = await touchPage.locator('.xterm-screen').boundingBox();
  const scrollFrom = await touchPage.evaluate(() => events.length);
  const press = { x: cellNow.x + w * 4, y: cellNow.y + h * 12 };
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ ...press, id: 1 }] });
  for (let i = 1; i <= 6; i++) {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: press.x, y: press.y + i * 22, id: 1 }] });
    await touchPage.waitForTimeout(16);
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await touchPage.waitForTimeout(450);
  const scrolls = await touchPage.evaluate(from => events.slice(from).filter(e => e.type === 'scroll'), scrollFrom);
  assert(scrolls.length > 0 && scrolls.every(e => e.lines < 0), 'Dragging down must scroll the application up');
  assert(scrolls.every(e => e.anchorRow === 6 && e.anchorColumn === 60), 'Wheel steps must target the transcript, not the composer under the finger');
  assert(scrolls.reduce((sum, e) => sum - e.lines, 0) > 8, 'Release must continue with inertia');

  // A remote screen taller than the phone must still scroll the application, not the local container.
  await touchSend({ type: 'setViewport', rows: 60, cols: 120 });
  await touchPage.waitForFunction(() => document.querySelector('#viewport').scrollHeight > document.querySelector('#viewport').clientHeight + 100);
  const tallTop = await touchPage.evaluate(() => { const v = document.querySelector('#viewport'); v.scrollTop = 120; return v.scrollTop; });
  const tallFrom = await touchPage.evaluate(() => events.length);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 200, y: 200, id: 1 }] });
  for (let i = 1; i <= 6; i++) { await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 200, y: 200 + i * 24, id: 1 }] }); await touchPage.waitForTimeout(16); }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await touchPage.waitForTimeout(300);
  assert((await touchPage.evaluate(from => events.slice(from).filter(e => e.type === 'scroll').length, tallFrom)) > 0, 'Swiping a tall remote screen must scroll the application');
  assert.equal(await touchPage.evaluate(() => document.querySelector('#viewport').scrollTop), tallTop, 'The local container must not pan while the application scrolls');
  await touchSend({ type: 'setViewport', rows: 20, cols: 120 });
  // A second finger cancels the hold; lifting it must not send a TUI click.
  await touchSend({ type: 'control', enabled: true });
  await touchSend({ type: 'application', remoteHistory: true, alternate: true });
  const beforeGesture = await touchPage.evaluate(() => events.length);
  await touch('touchstart', [first]);
  await touch('touchstart', [first, { x: 220, y: 200 }]);
  assert.equal(await touch('touchmove', [{ x: 60, y: 100 }, { x: 280, y: 250 }]), true, 'The page must handle pinch itself');
  await touch('touchend', [first], [{ x: 280, y: 250 }]);
  await touch('touchmove', [{ x: 100, y: 130 }]);
  await touch('touchend', [], [first]);
  assert.deepEqual(await touchPage.evaluate(from => events.slice(from).filter(e => ['click', 'scroll', 'selectionMode', 'fit'].includes(e.type)), beforeGesture), []);

  await touchSend({ type: 'zoomReset' });
  const before = await touchPage.locator('.xterm-rows').innerText();
  const layoutBefore = await touchPage.evaluate(() => document.querySelector('.xterm-screen').offsetWidth);
  const zoomState = () => touchPage.evaluate(() => {
    const viewport = document.querySelector('#viewport'), screen = document.querySelector('.xterm-screen').getBoundingClientRect(), box = viewport.getBoundingClientRect();
    return { transform: document.querySelector('#terminal').style.transform, screen: { left: screen.left, right: screen.right, top: screen.top, bottom: screen.bottom }, box: { left: box.left, right: box.right, top: box.top, bottom: box.bottom }, scrollWidth: viewport.scrollWidth, clientWidth: viewport.clientWidth };
  });
  const pinch = async (centre, from, to) => {
    const fingers = gap => [{ x: centre.x - gap, y: centre.y }, { x: centre.x + gap, y: centre.y }];
    await touch('touchstart', [fingers(from)[0]]);
    await touch('touchstart', fingers(from), [fingers(from)[1]]);
    for (let i = 1; i <= 8; i++) await touch('touchmove', fingers(from + (to - from) * i / 8));
    await touch('touchend', [fingers(to)[0]], [fingers(to)[1]]);
    await touch('touchend', [], [fingers(to)[0]]);
  };
  // Pinch out: the page zooms in around the fingers and every edge stays reachable.
  await pinch({ x: 195, y: 300 }, 30, 110);
  const zoomedIn = await zoomState();
  const factor = Number(/scale\(([\d.]+)\)/.exec(zoomedIn.transform)?.[1]);
  assert(factor > 2, `Pinch out must magnify the terminal: ${zoomedIn.transform}`);
  assert.equal(await touchPage.locator('.xterm-rows').innerText(), before, 'Magnification must not reflow terminal contents');
  assert.equal(await touchPage.evaluate(() => document.querySelector('.xterm-screen').offsetWidth), layoutBefore, 'Zoom must preserve the terminal width in layout pixels');
  await touchPage.evaluate(() => { const v = document.querySelector('#viewport'); v.scrollLeft = 1e6; v.scrollTop = 1e6; });
  const corner = await zoomState();
  assert(corner.screen.right <= corner.box.right + 1 && corner.screen.bottom <= corner.box.bottom + 1, 'The far right and bottom edges must be reachable while zoomed');
  await touchPage.evaluate(() => { const v = document.querySelector('#viewport'); v.scrollLeft = 0; v.scrollTop = 0; });
  const origin = await zoomState();
  assert(origin.screen.left >= origin.box.left - 1 && origin.screen.top >= origin.box.top - 1, 'The top-left edge must be reachable while zoomed');
  // Pinch in far: stops at the fit scale, where the whole terminal is visible at once.
  await pinch({ x: 195, y: 300 }, 140, 8);
  const fitted = await zoomState();
  assert(fitted.screen.right <= fitted.box.right + 1 && fitted.screen.bottom <= fitted.box.bottom + 1 && fitted.screen.left >= fitted.box.left - 1 && fitted.screen.top >= fitted.box.top - 1, `Zooming out must fit the whole terminal on screen: ${JSON.stringify(fitted)}`);
  assert(Number(/scale\(([\d.]+)\)/.exec(fitted.transform)?.[1]) < .6, 'Fit scale must be well below 1 for a 120-column terminal on a phone');
  assert.equal(await touchPage.evaluate(from => events.slice(from).some(e => ['fit', 'geometry', 'click', 'scroll'].includes(e.type)), beforeGesture), false, 'Pinch must not resize or send input to the PTY');
  await touchSend({ type: 'zoomReset' });

  // Scroll knob follows the finger and scrolls while held away from centre.
  await touchSend({ type: 'knob', enabled: true });
  const knobBox = await touchPage.locator('#scroll-knob').boundingBox();
  const knobAt = { x: knobBox.x + knobBox.width / 2, y: knobBox.y + knobBox.height / 2 };
  const knobFrom = await touchPage.evaluate(() => events.length);
  await touch('touchstart', [knobAt], [knobAt], '#scroll-knob');
  await touch('touchmove', [{ x: knobAt.x, y: knobAt.y - 50 }], [{ x: knobAt.x, y: knobAt.y - 50 }], '#scroll-knob');
  await touchPage.waitForTimeout(350);
  assert.equal(await touchPage.locator('#scroll-knob').evaluate(element => element.style.getPropertyValue('--knob-y')), '-50px', 'Knob must follow the finger');
  assert(await touchPage.locator('#scroll-knob').evaluate(element => element.classList.contains('active') && element.classList.contains('up')));
  await touch('touchend', [], [{ x: knobAt.x, y: knobAt.y - 50 }], '#scroll-knob');
  const knobScrolls = await touchPage.evaluate(from => events.slice(from).filter(e => e.type === 'scroll'), knobFrom);
  assert(knobScrolls.length > 0 && knobScrolls.every(e => e.lines < 0), 'Dragging the knob up must scroll up');
  assert.equal(await touchPage.locator('#scroll-knob').evaluate(element => element.style.getPropertyValue('--knob-y')), '0px', 'Knob must spring back on release');
  const settled = await touchPage.evaluate(() => events.length);
  await touchPage.waitForTimeout(250);
  assert.equal(await touchPage.evaluate(from => events.slice(from).filter(e => e.type === 'scroll').length, settled), 0, 'Scrolling must stop after release');
  await touchPage.waitForTimeout(1300);
  await touchPage.locator('#viewport').dispatchEvent('contextmenu', { button: 2, pointerType: 'mouse' });
  await touchPage.waitForFunction(() => events.some(e => e.type === 'contextMenu'));
  await touchSend({ type: 'clear' });
  await touchPage.waitForFunction(() => document.body.className === 'browse');
  await touchPage.waitForFunction(() => !document.querySelector('.xterm-rows').textContent.includes('hello world'));
  // History replay: hidden while rendering, every chunk acknowledged, revealed once at the end.
  await touchSend({ type: 'reset' });
  await touchPage.waitForFunction(() => document.body.classList.contains('restoring'));
  const replayFrom = await touchPage.evaluate(() => events.length);
  const replayChunk = Buffer.from('history line\r\n'.repeat(200)).toString('base64');
  for (let i = 0; i < 6; i++) await touchSend({ type: 'write', id: `replay-${i}`, data: replayChunk, bytes: 2800, offset: (i + 1) * 2800 });
  assert(await touchPage.evaluate(() => document.body.classList.contains('restoring')), 'Replay must stay hidden until the final chunk');
  await touchSend({ type: 'write', id: 'replay-final', data: '', bytes: 0, offset: 6 * 2800 });
  await touchPage.waitForFunction(() => !document.body.classList.contains('restoring'));
  const acked = await touchPage.evaluate(from => events.slice(from).filter(e => e.type === 'rendered').map(e => e.id), replayFrom);
  assert.deepEqual(acked, [...Array.from({ length: 6 }, (_, i) => `replay-${i}`), 'replay-final'], 'Batched replay must still acknowledge every chunk in order');
  assert((await touchPage.locator('.xterm-rows').innerText()).includes('history line'), 'Replay content must be rendered');
  await touchPage.close();
  assert.deepEqual(errors,[]);
  console.log('Mobile xterm browser checks passed: UTF-8 fragments, page isolation, ordered reset, long press, draggable Unicode selection, copy, horizontal browsing, pinch zoom to every edge and to fit, scroll knob, no reflow, output escaping.');
} finally { await browser.close();await new Promise(resolve=>server.close(resolve)); }
