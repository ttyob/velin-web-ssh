import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { createServer } from 'vite';
import { chromium } from 'playwright-core';

// Use the production components with isolated host/session APIs and a native bridge.
const server = await createServer({ root: fileURLToPath(new URL('..', import.meta.url)),
  server: { host: '127.0.0.1', port: 0, proxy: {} },
  optimizeDeps: { esbuildOptions: { target: 'esnext' } },
});
await server.listen();
const base = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox'] });
const output = new URL('../../artifacts/mobile-context-menu-015/tools/', import.meta.url);
await mkdir(output, { recursive: true });
try {
  for (const tool of ['files', 'agent', 'share', 'monitor', 'git', 'docker']) {
    const page = await browser.newPage({ viewport: { width: 390, height: 780 }, isMobile: true, hasTouch: true });
    page.setDefaultTimeout(15000);
    const errors = [], calls = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.addInitScript(() => {
      window.nativeEvents = [];
      window.VelinTools = { postMessage: value => window.nativeEvents.push(JSON.parse(value)) };
    });
    await page.route('**/native/context', route => route.fulfill({ json: {
      tool, serviceURL: 'https://service.example/velin/',
      host: { id: 'h', name: '测试主机', address: 'test.invalid', username: 'dev', hasPassword: true, initialDirectory: '/workspace' },
      session: { id: 's', hostID: 'h', name: '开发会话', status: 'attached', sessionMode: 'tmux' },
    } }));
    await page.route('**/api/**', route => {
      const path = new URL(route.request().url()).pathname;
      calls.push(path);
      let body = {};
      if (path === '/api/csrf') body = { token: 'test-csrf' };
      else if (path.endsWith('/directory')) body = { path: '/workspace' };
      else if (path.endsWith('/list')) body = { path: '/workspace', entries: [{ name: 'hello.txt', path: '/workspace/hello.txt', size: 20, directory: false }] };
      else if (path.endsWith('/shares')) body = [{ id: 'share1', url: '/share/public-token', active: true, permission: 'view', expiresAt: '2030-01-01T00:00:00Z', createdAt: '2026-11-01T00:00:00Z' }];
      else if (path === '/api/agent/models') body = { models: [{ id: 'test-model' }], configured: true };
      else if (path.endsWith('/agent')) body = { state: 'connected', aiConfigured: true };
      else if (path.endsWith('/snapshot')) body = { system: { hostname: 'test', os: 'Linux', arch: 'arm64', kernel: '6.6' }, load1: 0, load5: 0, load15: 0, memory: { total: 10000, used: 1000, usedPercent: 10 }, disks: [], uptimeSeconds: 3600, loadAverage: [0,0,0], network: [] };
      else if (path.endsWith('/command')) {
        const cmd = route.request().postDataJSON().command;
        let output = '';
        if (cmd.includes('rev-parse')) output = '/workspace';
        else if (cmd.includes('status --porcelain')) output = '## main\0 M hello.txt\0';
        else if (cmd.startsWith('docker ps ')) output = 'abcdef012345\ttest-container\talpine\tUp 2 hours\t\tbridge\n';
        else if (cmd.startsWith('docker logs')) output = 'hello container\n';
        body = { success: true, output };
      }
      else throw new Error(`Unexpected API: ${path}`);
      return route.fulfill({ json: body });
    });
    await page.routeWebSocket('**/ws/**', socket => { socket.send(JSON.stringify({ type: 'ready' })); });
    await page.goto(base + '/mobile-tools');
    const dialog = page.getByRole('dialog').first();
    await dialog.waitFor();
    await page.waitForTimeout(900);
    const box = await dialog.boundingBox();
    assert(box && box.x >= 0 && box.y >= 0 && box.width <= 391 && box.height <= 781, `${tool} must fit viewport: ${JSON.stringify(box)}`);
    assert(calls.some(path => path.includes(tool === 'files' ? '/sftp/h/list' : tool === 'share' ? '/sessions/s/shares' : '/hosts/h/agent')), `${tool} API never loaded`);
    if (tool === 'files') {
      await page.getByText('hello.txt', { exact: true }).waitFor();
      await page.getByRole('button', { name: '文件操作' }).click();
      await page.getByText('下载', { exact: true }).click();
      await page.waitForFunction(() => nativeEvents.some(e => e.type === 'download'));
      assert.match(await page.evaluate(() => nativeEvents.find(e => e.type === 'download').path), /^\/api\/sftp\/h\/download\?.*session=s/);
    }
    if (tool === 'share') {
      await page.locator('.share-row-actions button').nth(1).click();
      await page.waitForFunction(() => nativeEvents.some(e => e.type === 'openLink'));
      assert.equal(await page.evaluate(() => nativeEvents.find(e => e.type === 'openLink').url), 'https://service.example/velin/share/public-token');
    }
    if (tool === 'docker') {
      await page.getByText('test-container', { exact: true }).first().waitFor();
      await page.getByRole('button', { name: '终端', exact: true }).first().click();
      await page.waitForFunction(() => nativeEvents.some(e => e.type === 'dockerTerminal'));
      assert.equal(await page.evaluate(() => nativeEvents.find(e => e.type === 'dockerTerminal').id), 'abcdef012345');
    }
    await page.screenshot({ path: new URL(tool + '.png', output).pathname });
    assert.deepEqual(errors, [], `${tool} JavaScript errors`);
    await page.close();
    console.log(`Mobile tool passed: ${tool}`);
  }
} finally { await browser.close(); await server.close(); }
