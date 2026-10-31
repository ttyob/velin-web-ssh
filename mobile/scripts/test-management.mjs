import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(new URL('../terminal/package.json', import.meta.url));
const { chromium } = require('playwright-core');
const base = process.env.VELIN_MOBILE_PREVIEW_URL || 'http://127.0.0.1:8388/';
const shots = new URL('../design/screenshots/', import.meta.url);
const browser = await chromium.launch({executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox']});
let page;
try {
  page = await browser.newPage({viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true, deviceScaleFactor: 2});
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('console', m => {if (/overflowed|RenderFlex|EXCEPTION CAUGHT/.test(m.text())) errors.push(m.text());});
  const button = name => page.getByRole('button', {name, exact: true});
  const field = name => page.getByRole('textbox', {name, exact: false});
  const settle = () => page.waitForTimeout(500);
  async function fill(name, value) {
    const editor = field(name);
    await editor.tap();
    await settle(); // Let Flutter attach the active DOM editing connection.
    await editor.fill(value);
    await settle();
  }
  const back = async () => {await button('Back').tap(); await settle();};
  const shot = async name => {await settle(); await page.screenshot({path: new URL(name + '.png', shots).pathname});};
  const menu = async name => {await button('列表选项').tap(); await page.getByRole('menuitem', {name, exact: true}).tap(); await settle();};

  await page.goto(base);
  await page.locator('flt-semantics-placeholder').waitFor({state: 'attached'});
  await page.evaluate(() => document.querySelector('flt-semantics-placeholder')?.click());
  await button('主机').tap();
  await button('添加 SSH 主机').tap();
  await fill('主机名称', '手机测试机');
  await fill('主机地址', '192.0.2.20');
  await fill('端口', '2222');
  await fill('SSH 密码', 'sample-only');
  await shot('host-editor');
  await button('保存主机').tap();
  await settle();
  const host = page.getByRole('button', {name: /^手机测试机 root@192.0.2.20:2222/});
  await host.waitFor();
  await host.getByRole('button', {name: '主机操作'}).tap();
  await button('编辑主机').tap();
  await fill('主机名称', '手机开发机');
  await button('保存主机').tap();
  await settle();
  await page.getByRole('button', {name: /^手机开发机 root@192.0.2.20:2222/}).tap();
  await fill('会话名称', '修复登录问题');
  await button('创建并连接').tap();
  await button('展开输入').waitFor();
  await back();
  await button('会话 · 示例').tap();
  await page.getByRole('button', {name: /^修复登录问题 手机开发机 /}).waitFor();
  assert.equal(await page.getByRole('button', {name: /^开发服务器 · .*开发服务器/}).count(), 2, 'Existing host-named sessions need distinct identities');

  await menu('反向代理');
  await shot('web-services');
  await button('添加反向代理').tap();
  await fill('服务名称', '后台管理');
  await fill('目标地址', 'http://127.0.0.1:3000');
  await shot('web-service-editor');
  await button('保存代理').tap();
  await settle();
  let service = page.getByRole('button', {name: /^后台管理 http:\/\/127.0.0.1:3000/});
  await service.waitFor();
  await service.getByRole('button', {name: '代理操作'}).tap();
  await page.getByRole('menuitem', {name: '编辑', exact: true}).tap();
  await fill('服务名称', '管理预览');
  await button('保存代理').tap();
  await settle();
  service = page.getByRole('button', {name: /^管理预览 http:\/\/127.0.0.1:3000/});
  await service.tap();
  await page.getByText('示例网页', {exact: true}).waitFor();
  await back();
  await service.getByRole('button', {name: '代理操作'}).tap();
  await page.getByRole('menuitem', {name: '删除', exact: true}).tap();
  await button('删除').tap();
  await settle();
  assert.equal(await service.count(), 0);
  await back();

  await menu('Velin 服务管理');
  await button('添加 Velin 服务').tap();
  await fill('备注名称（可选）', '开发服务');
  await fill('服务地址', 'https://dev.example.com/velin');
  await fill('账户', 'dev');
  await button('保存').tap();
  await settle();
  let profile = page.getByRole('button', {name: /^开发服务 dev · https:\/\/dev.example.com\/velin\//});
  await profile.waitFor();
  await profile.getByRole('button', {name: '服务操作'}).tap();
  await page.getByRole('menuitem', {name: '编辑', exact: true}).tap();
  await fill('备注名称（可选）', '远端开发');
  await button('保存').tap();
  await settle();
  profile = page.getByRole('button', {name: /^远端开发 dev · https:\/\/dev.example.com\/velin\//});
  await profile.waitFor();
  await shot('service-profiles');
  assert.deepEqual(errors, [], 'Browser/layout errors');
  console.log('Mobile management flows passed: host add/edit, named session, duplicate labels, proxy add/edit/open/delete, service profile add/edit.');
} catch (e) {
  if (page) {console.log(await page.locator('body').ariaSnapshot()); await page.screenshot({path: '/tmp/velin-mobile-management-failure.png'});}
  throw e;
} finally {await browser.close();}
