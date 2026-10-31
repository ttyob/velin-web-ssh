import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'

// Run the actual router, store, views and styles against isolated HTTP fixtures.
// This never logs into, locks or changes credentials on a deployed service.
const server = await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  cacheDir: 'node_modules/.vite-auth-interface',
  server: { host: '127.0.0.1', port: 0, open: false },
  optimizeDeps: { esbuildOptions: { target: 'esnext' } },
})
const shots = new URL('../../artifacts/auth-redesign-20261031/', import.meta.url)
await mkdir(shots, { recursive: true })
let browser
try {
  await server.listen()
  const base = `http://127.0.0.1:${server.httpServer.address().port}`
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox'] })
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await context.newPage()
  const errors = [], unlocks = []
  let user, locked = false, passwordChanges = 0, loseLoginSession = false
  const protectedWhileLocked = []
  page.on('pageerror', e => errors.push(e.message))
  await page.route('**/api/**', async route => {
    const req = route.request(), path = new URL(req.url()).pathname
    const data = req.postDataJSON() || {}
    const ok = json => route.fulfill({ json })
    const fail = (status, code, message, extra = {}) => route.fulfill({ status, json: { code, message, ...extra } })
    if (path === '/api/csrf') return ok({ token: 'isolated-auth-fixture' })
    if (path === '/api/auth/me') return user ? ok({ ...user, sessionLocked: locked }) : fail(401, 'unauthorized', '请登录')
    if (path === '/api/auth/captcha') return ok({ id: 'captcha', image: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="48"><rect width="160" height="48" fill="#ececf0"/><text x="24" y="32" font-size="24">AB123</text></svg>' })
    if (path === '/api/auth/login') {
      if (loseLoginSession) return ok({ user: { id: 'fixture', username: '开发者', role: 'user', forcePasswordChange: true } })
      if (!data.captchaID) return fail(401, 'invalid_credentials', '用户名或密码错误', { captchaRequired: true })
      if (data.captcha !== 'AB123') return fail(401, 'captcha_invalid', '验证码错误')
      if (!data.totpCode) return fail(401, 'totp_required', '需要双因素验证')
      if (data.totpCode !== '123456') return fail(401, 'invalid_totp', '验证码无效')
      user = { id: 'fixture', username: '开发者', role: 'user', forcePasswordChange: true }
      return ok({ user })
    }
    if (path === '/api/auth/password') {
      if (!user) return fail(401, 'unauthorized', '请重新登录')
      passwordChanges++; user.forcePasswordChange = false; return ok({ ok: true })
    }
    if (path === '/api/auth/lock') { locked = true; return ok({ ok: true }) }
    if (path === '/api/auth/unlock') {
      unlocks.push(data.pin)
      if (data.pin === '999999') return fail(429, 'unlock_rate_limited', '尝试次数过多，请稍后再试')
      if (data.pin !== '123456') return fail(401, 'invalid_lock_pin', '锁屏 PIN 错误')
      locked = false
      return ok({ ok: true })
    }
    if (path === '/api/auth/logout') { user = undefined; locked = false; return ok({ ok: true }) }
    if (locked) { protectedWhileLocked.push(path); return fail(423, 'session_locked', '当前工作区已锁定') }
    if (path === '/api/auth/lock-pin') return ok({ configured: true })
    if (path === '/api/auth/devices') return ok([])
    if (path === '/api/preferences') return ok({ lockEnabled: true, autoLockMinutes: 1 })
    if (path === '/api/workspace') return ok({ layout: { tabs: [] }, version: 1 })
    if (['/api/hosts', '/api/credentials', '/api/sessions', '/api/web-services'].includes(path)) return ok([])
    return ok({})
  })
  const shot = async name => {
    await page.waitForTimeout(250)
    await page.screenshot({ path: new URL(`${name}.png`, shots).pathname })
  }
  const waitForLockStage = () => page.waitForFunction(() => !document.querySelector('.lock-stage-enter-active, .lock-stage-leave-active'))
  await page.goto(`${base}/login`)
  await page.getByRole('heading', { name: '登录', exact: true }).waitFor()
  assert.equal(await page.getByRole('button', { name: '登录', exact: true }).evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(236, 236, 240)')
  await shot('web-login')
  await page.getByLabel('用户名', { exact: true }).fill('fixture')
  await page.getByLabel('密码', { exact: true }).fill('fixture-password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByLabel('图形验证码', { exact: true }).fill('WRONG')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByText('图形验证码错误，请重新输入').waitFor()
  await page.getByLabel('图形验证码', { exact: true }).fill('AB123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByLabel('双因素验证码', { exact: true }).fill('000000')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByText('双因素验证码无效').waitFor()
  await page.getByLabel('双因素验证码', { exact: true }).fill('123456')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.waitForURL('**/change-password')
  await shot('web-password-setup')
  await page.getByLabel('当前密码', { exact: true }).fill('fixture-password')
  await page.getByLabel('新密码', { exact: true }).fill('fixture-new-password')
  await page.getByLabel('确认新密码', { exact: true }).fill('mismatch')
  await page.getByRole('button', { name: '修改并进入工作区' }).click()
  await page.getByText('两次输入的新密码不一致').waitFor()
  assert.equal(passwordChanges, 0)
  await page.getByLabel('确认新密码', { exact: true }).fill('fixture-new-password')
  await page.getByRole('button', { name: '修改并进入工作区' }).click()
  await page.waitForURL('**/workspace')
  await page.getByRole('heading', { name: '没有打开的终端' }).waitFor()
  await page.locator('.workspace-lock-button').click()
  const cover = page.getByRole('dialog', { name: '工作区已锁定' })
  await cover.waitFor()
  assert.equal(await page.locator('#workspace-unlock-pin').count(), 0)
  const coverText = await cover.innerText()
  assert(coverText.includes('工作区已锁定') && coverText.includes('点击屏幕解锁'))
  assert(!coverText.includes('开发者') && !coverText.includes('velin') && !coverText.includes('远端任务'))
  await shot('web-lock-screen')
  assert(await page.locator('.system-lock-prompt').evaluate(el =>
    el.getAnimations({ subtree: true }).filter(animation => animation.playState === 'running' && animation.effect.getTiming().iterations === Infinity).length >= 2
  ), 'The unlock hint continuously breathes and ripples')
  // An inert workspace cannot reclaim focus behind the mask.
  await page.getByRole('button', { name: '新增主机', includeHidden: true }).first().evaluate(el => el.focus())
  assert(await page.evaluate(() => document.activeElement.closest('.system-lock') !== null))
  await page.keyboard.press('Tab')
  assert.equal(await page.getByRole('button', { name: '点击解锁' }).evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Shift+Tab')
  assert.equal(await page.getByRole('button', { name: '点击解锁' }).evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Enter')
  const pin = page.getByLabel('锁屏 PIN', { exact: true })
  await pin.waitFor()
  await page.waitForFunction(() => document.querySelector('.lock-stage-enter-active')?.getAnimations().some(animation => animation.playState === 'running'))
  await waitForLockStage()
  assert.equal(await pin.evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Shift+Tab')
  assert.equal(await page.getByRole('button', { name: '切换账号', exact: true }).evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Tab')
  assert.equal(await pin.evaluate(el => el === document.activeElement), true)
  await shot('web-lock-pin')
  await pin.fill('ab12')
  assert.equal(await pin.inputValue(), '12')
  assert.equal(await page.getByRole('button', { name: '解锁工作台' }).isEnabled(), false)
  for (const [value, message] of [['000000', '锁屏 PIN 错误'], ['999999', '尝试次数过多，请稍后再试']]) {
    await pin.fill(value)
    await page.getByRole('button', { name: '解锁工作台' }).click()
    await page.getByRole('alert').filter({ hasText: message }).waitFor()
    assert.equal(await pin.inputValue(), '')
    assert.equal(await cover.count(), 1)
    assert.equal(await page.locator('.system-lock-leave-active').count(), 0, 'An invalid PIN must not reveal the workspace')
  }
  await page.keyboard.press('Escape')
  await pin.waitFor({ state: 'detached' })
  await waitForLockStage()
  await page.keyboard.press('Space')
  await pin.waitFor()
  await waitForLockStage()
  assert.equal(await page.locator('.system-lock-error').count(), 0)
  await pin.fill('123456')
  await page.getByRole('button', { name: '解锁工作台' }).click()
  await page.waitForFunction(() => document.querySelector('.system-lock-leave-active')?.getAnimations()
    .some(animation => animation.transitionProperty === 'clip-path' && animation.playState === 'running' && animation.effect.getComputedTiming().progress > .2))
  assert.equal(locked, false, 'The opening animation starts after PIN verification')
  assert.notEqual(await cover.evaluate(el => getComputedStyle(el).clipPath), 'none')
  await page.screenshot({ path: new URL('web-unlock-reveal.png', shots).pathname })
  await cover.waitFor({ state: 'hidden' })
  assert.equal(locked, false)
  assert.deepEqual(unlocks, ['000000', '999999', '123456'])

  await page.clock.install()
  await page.mouse.click(700, 200)
  await page.clock.fastForward(61_000)
  await cover.waitFor()
  assert.equal(locked, true, 'Idle locking remains enabled')
  // Advance the device clock across a minute and verify the displayed time and
  // hands follow it, including when the tab becomes visible again after sleep.
  await page.clock.pauseAt(new Date(await page.evaluate(() => Date.now() + 1000)))
  const readClock = () => page.locator('.system-lock-time').getAttribute('datetime')
  const clockBefore = await readClock()
  const timeBefore = await page.locator('.system-lock-time').innerText()
  await page.clock.fastForward(61_000)
  const clockAfter = await readClock()
  const elapsed = new Date(clockAfter) - new Date(clockBefore)
  assert(elapsed >= 60_000 && elapsed <= 62_000, 'Clock updates within its one second sampling interval')
  assert.notEqual(await page.locator('.system-lock-time').innerText(), timeBefore)
  assert(await page.evaluate(() => {
    const date = new Date(document.querySelector('.system-lock-time').getAttribute('datetime'))
    const seconds = date.getSeconds(), minutes = date.getMinutes() + seconds / 60
    return [['hour', (date.getHours() % 12 + minutes / 60) * 30], ['minute', minutes * 6], ['second', seconds * 6]]
      .every(([hand, angle]) => document.querySelector(`.lock-clock-${hand}`).getAttribute('transform') === `rotate(${angle} 120 120)`)
  }))
  await page.clock.setSystemTime(new Date(new Date(clockAfter).getTime() + 3_600_000))
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  assert.equal(new Date(await readClock()) - new Date(clockAfter), 3_600_000)
  protectedWhileLocked.length = 0
  await page.reload()
  await cover.waitFor()
  assert.equal(await pin.count(), 0, 'Restored locks start collapsed')
  assert.deepEqual(protectedWhileLocked, [], 'Locked restoration must not load private workspace data')
  await page.clock.resume()
  for (const viewport of [{ width: 360, height: 780 }, { width: 844, height: 390 }]) {
    await page.setViewportSize(viewport)
    const bounds = await page.locator('.system-lock-clock').boundingBox()
    assert(bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= viewport.width && bounds.y + bounds.height <= viewport.height, 'Clock must fit the viewport')
    await shot(viewport.width === 360 ? 'web-lock-clock-mobile' : 'web-lock-clock-landscape')
    if (viewport.width === 360) await page.mouse.click(8, 8)
    else await page.getByRole('button', { name: '点击解锁' }).click()
    await pin.waitFor()
    await waitForLockStage()
    const width = await cover.evaluate(el => ({ scroll: el.scrollWidth, client: el.clientWidth }))
    assert(width.scroll <= width.client + 1, `Lock overflows horizontally: ${JSON.stringify(width)}`)
    await shot(viewport.width === 360 ? 'web-lock-pin-mobile' : 'web-lock-pin-landscape')
    await page.getByRole('button', { name: '返回锁屏' }).click()
    await pin.waitFor({ state: 'detached' })
    await waitForLockStage()
  }
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForFunction(() => {
    const prompt = document.querySelector('.system-lock-prompt')
    return getComputedStyle(prompt).animationName === 'none' && getComputedStyle(prompt.querySelector('.lock-prompt-indicator'), '::before').animationName === 'none'
  })
  assert.equal(await page.locator('.system-lock-prompt').evaluate(el =>
    el.getAnimations({ subtree: true }).filter(animation => animation.playState === 'running' && animation.effect.getTiming().iterations === Infinity).length
  ), 0, 'Reduced motion disables the looping hint')
  await page.getByRole('button', { name: '点击解锁' }).click()
  await pin.waitFor()
  await waitForLockStage()
  assert.equal(await pin.evaluate(el => el === document.activeElement), true)
  await page.getByRole('button', { name: '切换账号', exact: true }).click()
  await page.waitForURL('**/login')
  await page.emulateMedia({ reducedMotion: 'no-preference' })
  await page.setViewportSize({ width: 390, height: 844 })
  await shot('web-login-mobile')
  const width = await page.locator('.auth-page').evaluate(el => ({ scroll: el.scrollWidth, client: el.clientWidth }))
  assert(width.scroll <= width.client + 1)

  // The Windows bootstrap uses only inline assets, so it can be rendered here.
  const bootstrap = (await readFile(new URL('../../desktop/dist/index.html', import.meta.url), 'utf8'))
    .replace('__VELIN_DESKTOP_URL_JSON__', JSON.stringify(`${base}/login`))
    .replace('__VELIN_DESKTOP_CREDENTIALS_JSON__', JSON.stringify({ username: 'fixture', password: 'fixture-password', reset: false }))
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.setContent(bootstrap)
  await page.getByRole('heading', { name: '初始管理员凭据' }).waitFor()
  await shot('windows-initial-account')
  await page.getByRole('button', { name: '进入登录' }).click()
  await page.getByRole('heading', { name: '登录', exact: true }).waitFor()

  // A successful login response must not open setup if its cookie was blocked.
  loseLoginSession = true
  await page.getByLabel('用户名', { exact: true }).fill('fixture')
  await page.getByLabel('密码', { exact: true }).fill('fixture-password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByText('无法保存登录会话，请允许本站使用 Cookie 后重试', { exact: true }).waitFor()
  assert.equal(new URL(page.url()).pathname, '/login')
  loseLoginSession = false

  // An expired session on setup returns to login instead of trapping the user.
  user = { id: 'fixture', username: '开发者', role: 'user', forcePasswordChange: true }
  await page.goto(`${base}/change-password`)
  await page.getByLabel('当前密码', { exact: true }).fill('fixture-password')
  await page.getByLabel('新密码', { exact: true }).fill('fixture-new-password')
  await page.getByLabel('确认新密码', { exact: true }).fill('fixture-new-password')
  const changesBeforeExpiry = passwordChanges
  user = undefined
  await page.getByRole('button', { name: '修改并进入工作区' }).click()
  await page.waitForURL('**/login')
  await page.getByText('登录已失效，请重新登录后修改密码', { exact: true }).waitFor()
  assert.equal(passwordChanges, changesBeforeExpiry)
  assert.deepEqual(errors, [])
  console.log('Auth UI passed: captcha, TOTP, password confirmation, analog clock, time advancement and wake calibration, looping unlock hint, animated PIN transition and successful unlock reveal, manual/idle/restored lock, PIN errors, focus isolation, keyboard, full-screen click, reduced motion, logout, responsive layout, Windows bootstrap, blocked login cookies and expired setup sessions.')
} finally {
  await browser?.close()
  await server.close()
}
