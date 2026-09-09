import { chromium, devices } from 'playwright-core'
import { mkdir } from 'node:fs/promises'

const baseURL = process.env.VELIN_TEST_URL || 'http://127.0.0.1:8377'
const password = process.env.VELIN_TEST_PASSWORD
if (!password) throw new Error('VELIN_TEST_PASSWORD is required')
await mkdir('../artifacts', { recursive: true })
const browser = await chromium.launch({ executablePath: '/usr/bin/chromium', headless: true, args: ['--no-sandbox'] })

async function verify(name, contextOptions, expectedMobile = false) {
  const context = await browser.newContext(contextOptions)
  const page = await context.newPage()
  const errors = []
  const httpErrors = []
  page.on('console', msg => {
    if (msg.type() === 'error' && !msg.text().startsWith('Failed to load resource: the server responded with a status of 401')) errors.push(msg.text())
  })
  page.on('pageerror', err => errors.push(err.message))
  page.on('response', response => {
    if (response.status() >= 400 && !(response.status() === 401 && response.url().endsWith('/api/auth/me'))) httpErrors.push(`${response.status()} ${response.url()}`)
  })
  await page.goto(`${baseURL}/login`, { waitUntil: 'networkidle' })
  await page.getByLabel('用户名').fill('admin')
  await page.getByLabel('密码').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
  await page.waitForURL('**/workspace')
  await page.getByText('没有打开的终端').waitFor()
  const rejected = await context.request.put(`${baseURL}/api/preferences`, {
    data: {},
    headers: { 'Sec-Fetch-Site': 'cross-site' },
  })
  const csrf = await page.evaluate(async () => {
    const token = await fetch('/api/csrf').then(response => response.json())
    const live = await fetch('/api/health/live').then(response => response.status)
    const ready = await fetch('/api/health/ready').then(response => response.status)
    const metrics = await fetch('/api/admin/metrics').then(async response => ({ status: response.status, body: await response.text() }))
    return { tokenLength: token.token?.length || 0, live, ready, metrics }
  })
  if (csrf.tokenLength < 32 || rejected.status() !== 403) throw new Error(`${name}: CSRF enforcement failed ${JSON.stringify({ ...csrf, rejected: rejected.status() })}`)
  if (csrf.live !== 200 || csrf.ready !== 200 || csrf.metrics.status !== 200 || !csrf.metrics.body.includes('velin_websockets')) throw new Error(`${name}: health or metrics failed ${JSON.stringify(csrf)}`)
  const viewport = page.viewportSize()
  if (viewport && viewport.width <= 760) await page.getByTitle('打开侧栏').click()
  await page.getByText('设置', { exact: true }).click()
  await page.getByRole('dialog', { name: '设置' }).waitFor()
  await page.keyboard.press('Escape')
  const metrics = await page.evaluate(() => ({
    width: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
    title: document.title,
    userAgent: navigator.userAgent,
    touchPoints: navigator.maxTouchPoints,
  }))
  if (metrics.scrollWidth > metrics.width) throw new Error(`${name}: horizontal overflow ${metrics.scrollWidth} > ${metrics.width}`)
  if (metrics.title !== 'VelinWebSSH') throw new Error(`${name}: unexpected title ${metrics.title}`)
  if (expectedMobile && (!metrics.userAgent.includes('Android') || !metrics.userAgent.includes('Mobile') || metrics.touchPoints < 1)) {
    throw new Error(`${name}: Android mobile emulation failed ${JSON.stringify(metrics)}`)
  }
  if (errors.length) throw new Error(`${name}: console errors: ${errors.join(' | ')}`)
  if (httpErrors.length) throw new Error(`${name}: HTTP errors: ${httpErrors.join(' | ')}`)
  await page.screenshot({ path: `../artifacts/${name}.png`, fullPage: true })
  console.log(JSON.stringify({ name, ...metrics, errors: errors.length }))
  await context.close()
}

try {
  await verify('desktop', { viewport: { width: 1440, height: 900 } })
  await verify('mobile', { ...devices['Pixel 7'] }, true)
} finally {
  await browser.close()
}
