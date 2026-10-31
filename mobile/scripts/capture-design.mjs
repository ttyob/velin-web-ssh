import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir } from 'node:fs/promises';

const require = createRequire(new URL('../terminal/package.json', import.meta.url));
const { chromium } = require('playwright-core');
const base = process.env.VELIN_MOBILE_PREVIEW_URL || 'http://127.0.0.1:8388/';
const target = new URL('../design/screenshots/', import.meta.url);
await mkdir(target, { recursive: true });
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
  args: ['--no-sandbox'],
  headless: true,
});

try {
  const page = await browser.newPage({
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
  });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('console', m => {
    if (/overflowed|RenderFlex|EXCEPTION CAUGHT/.test(m.text())) errors.push(m.text());
  });
  const button = name => page.getByRole('button', { name, exact: true });
  const settle = () => page.waitForTimeout(450);
  async function open(path = '') {
    await page.goto(new URL(path, base).href);
    await page.locator('flt-semantics-placeholder').waitFor({ state: 'attached' });
    await page.evaluate(() => document.querySelector('flt-semantics-placeholder')?.click());
    await settle();
  }
  async function capture(name) {
    await page.mouse.move(0, 0);
    await page.waitForTimeout(1600);
    await page.screenshot({ path: new URL(name + '.png', target).pathname });
  }
  async function terminalArea(minimum) {
    await settle();
    const box = await page.locator('iframe').boundingBox();
    const size = page.viewportSize();
    assert(box && box.height / size.height >= minimum,
      `Terminal occupies too little space: ${JSON.stringify({ box, size })}`);
    assert(box.x >= -1 && box.y >= -1 && box.x + box.width <= size.width + 1 &&
      box.y + box.height <= size.height + 1, 'Terminal extends outside viewport');
    return Math.round(box.height / size.height * 100);
  }

  await open('?screen=login');
  await button('进入设计预览').waitFor();
  await capture('login');
  await open();
  await button('会话 · 示例').waitFor();
  assert.equal(await page.getByRole('textbox').count(), 0, 'Search must start collapsed');
  await capture('sessions');
  await button('搜索').tap();
  await page.getByRole('textbox').fill('API');
  await settle();
  assert.equal(await page.getByRole('button', { name: /^API 联调 / }).count(), 1);
  assert.equal(await page.getByRole('button', { name: /^控制台界面优化 / }).count(), 0);
  await button('收起搜索').tap();
  await page.getByRole('button', { name: /^Velin · 移动端开发 / }).tap();
  await button('展开输入').waitFor();
  const frame = page.frames().find(f => f.url().endsWith('/terminal/index.html'));
  assert(frame, 'Terminal iframe was not created');
  await frame.waitForFunction(() => document.querySelector('.xterm-rows')?.textContent.includes('/workspace/velin'));
  assert.equal(await page.getByRole('textbox').count(), 0, 'Composer must start collapsed');
  const portraitRatio = await terminalArea(.85);
  await capture('terminal-view');

  await button('全屏终端').tap();
  const fullscreenRatio = await terminalArea(.98);
  assert.equal(frame.isDetached(), false, 'Fullscreen recreated the terminal');
  assert((await frame.locator('.xterm-rows').textContent()).includes('/workspace/velin'));
  assert.equal(await button('终端操作').count(), 0);
  await capture('terminal-fullscreen');
  await button('退出全屏').tap();
  assert.equal(frame.isDetached(), false, 'Exiting fullscreen recreated the terminal');
  await button('接管').tap();
  await button('展开输入').tap();
  const draft = '继续完善终端交互，补充回归测试。';
  await page.getByRole('textbox').fill(draft);
  await capture('terminal-input');
  await button('收起输入').tap();
  await terminalArea(.85);
  await button('展开输入').tap();
  assert.equal(await page.getByRole('textbox').inputValue(), draft, 'Collapsing input lost draft');
  await button('收起输入').tap();
  await button('选择文本').tap();
  await frame.waitForFunction(() => document.body.className === 'select');
  await frame.evaluate(() => {
    const area = document.querySelector('#viewport');
    const touch = (x, y) => new Touch({ identifier: 1, target: area, clientX: x, clientY: y });
    area.dispatchEvent(new TouchEvent('touchstart', { touches: [touch(15, 15)], bubbles: true }));
    area.dispatchEvent(new TouchEvent('touchmove', { touches: [touch(135, 15)], bubbles: true, cancelable: true }));
    area.dispatchEvent(new TouchEvent('touchend', { touches: [], changedTouches: [touch(135, 15)], bubbles: true }));
  });
  await settle();
  assert.equal(await button('复制').isEnabled(), true, 'Touch selection did not enable copy');
  await capture('terminal-selection');
  await button('退出选择').tap();
  await frame.waitForFunction(() => document.body.className === 'browse');
  await button('展开输入').tap();
  await button('输入选项').tap();
  await page.getByRole('menuitem', { name: '上传附件' }).tap();
  await button('加入示例引用').waitFor();
  await capture('attachment');
  await button('加入示例引用').tap();
  await settle();
  // Flutter exposes the editing value through the DOM only while focused.
  await page.getByRole('textbox').tap();
  await settle();
  const attachedDraft = await page.getByRole('textbox').inputValue();
  assert(attachedDraft.includes('/clipboard/design-reference.png'), `Attachment missing from draft: ${attachedDraft}`);
  await button('收起输入').tap();

  // Browser viewports check available space and real interactions. OS keyboard,
  // font scaling and system safe areas still require physical-device testing.
  for (const size of [{ width: 360, height: 780 }, { width: 430, height: 932 }, { width: 844, height: 390 }]) {
    await page.setViewportSize(size);
    await terminalArea(size.width > size.height ? .7 : .85);
    await button('展开输入').tap();
    await settle();
    assert.equal(await button('发送').isVisible(), true);
    await button('收起输入').tap();
    await button('全屏终端').tap();
    await terminalArea(.98);
    await button('退出全屏').tap();
  }
  assert.deepEqual(errors, [], 'Browser/layout errors');
  console.log(`Terminal area: portrait ${portraitRatio}%, fullscreen ${fullscreenRatio}%`);
  console.log(`Design screenshots: ${target.pathname}`);
} finally {
  await browser.close();
}
