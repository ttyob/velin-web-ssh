import { mkdir } from 'node:fs/promises'
import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'

// Capture a running instance with real SSH sessions. Prepare an isolated demo
// workspace first: this script does not replace API responses or terminal output.
const baseURL = process.env.VELIN_SCREENSHOT_URL
const username = process.env.VELIN_SCREENSHOT_USER
const password = process.env.VELIN_SCREENSHOT_PASSWORD
const output = process.env.VELIN_SCREENSHOT_OUTPUT ||
  fileURLToPath(new URL('../../.github/assets/velin-workspace.png', import.meta.url))
if (!baseURL || !username || !password) {
  throw new Error('Set VELIN_SCREENSHOT_URL, VELIN_SCREENSHOT_USER and VELIN_SCREENSHOT_PASSWORD for a prepared demo workspace')
}

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
  headless: true,
  args: ['--no-sandbox'],
})

try {
  const context = await browser.newContext({
    viewport: { width: 1600, height: 960 },
    deviceScaleFactor: 1,
    colorScheme: 'dark',
  })
  const page = await context.newPage()
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(new URL('/login', baseURL).href)
  await page.getByLabel('用户名', { exact: true }).fill(username)
  await page.getByLabel('密码', { exact: true }).fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.waitForURL('**/workspace')
  await page.locator('.terminal-pane .terminal-canvas > .xterm .xterm-screen').first().waitFor()
  await page.waitForFunction(() => {
    const panes = [...document.querySelectorAll('.terminal-pane')]
    return panes.length > 0 && panes.every(pane =>
      pane.querySelector('.connection-icon:not(.disconnected)') &&
      !pane.querySelector('.terminal-resize-snapshot'))
  })
  await page.evaluate(() => document.fonts.ready)
  await page.mouse.move(1590, 950)
  // Allow cursor blinking and transient connection messages to settle.
  await page.waitForTimeout(1500)
  if (errors.length) throw new Error(`Browser errors: ${errors.join(' | ')}`)
  await mkdir(dirname(output), { recursive: true })
  await page.screenshot({ path: output, fullPage: false, animations: 'disabled' })
  console.log(`Saved real workspace screenshot: ${output}`)
  await context.close()
} finally {
  await browser.close()
}
