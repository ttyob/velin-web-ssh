import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir } from 'node:fs/promises';

const require = createRequire(new URL('../terminal/package.json', import.meta.url));
const { chromium } = require('playwright-core');
const base = process.env.VELIN_MOBILE_PREVIEW_URL || 'http://127.0.0.1:8388/';
const shots = new URL('../design/screenshots/', import.meta.url);
await mkdir(shots, { recursive: true });
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
  args: ['--no-sandbox'],
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
  const settle = () => page.waitForTimeout(500);
  async function open(screen) {
    await page.goto(new URL(screen ? `?screen=${screen}` : '', base).href);
    await page.locator('flt-semantics-placeholder').waitFor({ state: 'attached' });
    await page.evaluate(() => document.querySelector('flt-semantics-placeholder')?.click());
    await settle();
  }
  async function shot(name) {
    await settle();
    await page.screenshot({ path: new URL(`${name}.png`, shots).pathname });
  }
  async function enterPIN(value) {
    const pin = page.getByRole('textbox', { name: /锁屏 PIN/ });
    await pin.tap();
    await settle();
    await pin.fill(value);
    await settle();
  }

  await open('login');
  await button('进入设计预览').waitFor();
  await shot('login');
  await open('setup');
  await button('保存并继续').waitFor();
  await shot('account-setup');
  await open('lock');
  assert.equal(await page.getByRole('textbox').count(), 0, 'PIN starts hidden');
  await shot('lock-screen');
  await page.getByRole('button', { name: /点击解锁/ }).tap();
  await button('解锁工作台').waitFor();
  assert.equal(await button('解锁工作台').isEnabled(), false);
  await shot('lock-pin');
  await enterPIN('000000');
  await button('解锁工作台').tap();
  await settle();
  assert.equal(await button('解锁工作台').isEnabled(), false, 'Wrong PIN is cleared');
  assert.equal(await button('会话 · 示例').count(), 0, 'Wrong PIN cannot open workspace');
  await button('返回锁屏').tap();
  await settle();
  assert.equal(await page.getByRole('textbox').count(), 0);
  await page.getByRole('button', { name: /点击解锁/ }).tap();
  await enterPIN('123456'); // Preview fixture only; real PINs are server-verified.
  await button('解锁工作台').tap();
  await button('会话 · 示例').waitFor();
  await button('列表选项').tap();
  await page.getByRole('menuitem', { name: '锁定工作台', exact: true }).tap();
  await page.getByRole('button', { name: /点击解锁/ }).waitFor();
  assert.equal(await button('列表选项').count(), 0, 'Underlying controls are hidden');
  await page.getByRole('button', { name: /点击解锁/ }).tap();
  await enterPIN('123456');
  await button('解锁工作台').tap();
  await button('会话 · 示例').waitFor();

  for (const size of [{ width: 360, height: 780 }, { width: 844, height: 390 }]) {
    await page.setViewportSize(size);
    for (const screen of ['login', 'setup', 'lock']) {
      await open(screen);
      if (screen === 'lock') {
        await page.getByRole('button', { name: /点击解锁/ }).tap();
        await button('解锁工作台').waitFor();
      }
      await settle();
    }
  }
  assert.deepEqual(errors, [], 'Authentication browser errors');
  console.log('Auth preview: login, setup, lock, wrong PIN, unlock, relock and responsive layout passed.');
} finally {
  await browser.close();
}
