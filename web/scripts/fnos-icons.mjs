import { copyFile, readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const fnosDir = path.resolve(webDir, '../packaging/fnos')

const svg = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256">
  <defs>
    <linearGradient id="card" x1="0" y1="0" x2="0.7" y2="1">
      <stop offset="0" stop-color="#ff8276"/>
      <stop offset="1" stop-color="#ed565d"/>
    </linearGradient>
    <filter id="card-shadow" x="-20%" y="-20%" width="140%" height="150%">
      <feDropShadow dx="0" dy="7" stdDeviation="6" flood-color="#211c39" flood-opacity="0.28"/>
    </filter>
    <filter id="window-shadow" x="-20%" y="-20%" width="140%" height="150%">
      <feDropShadow dx="0" dy="5" stdDeviation="4" flood-color="#211c39" flood-opacity="0.3"/>
    </filter>
    <clipPath id="terminal-clip">
      <rect x="52" y="61" width="152" height="125" rx="20"/>
    </clipPath>
  </defs>

  <rect x="24" y="20" width="208" height="208" rx="50" fill="url(#card)" filter="url(#card-shadow)"/>
  <path d="M57 43c30-17 86-18 139-2" fill="none" stroke="#ffaaa0" stroke-width="5" stroke-linecap="round" opacity="0.42"/>

  <g filter="url(#window-shadow)">
    <rect x="52" y="61" width="152" height="125" rx="20" fill="#2b274e"/>
    <path d="M52 99h152" stroke="#4f4877" stroke-width="6" clip-path="url(#terminal-clip)"/>
  </g>

  <circle cx="71" cy="79" r="6" fill="#ffd477"/>
  <circle cx="89" cy="79" r="6" fill="#66d8cc"/>
  <circle cx="107" cy="79" r="6" fill="#ff9f8b"/>

  <path d="M75 124l21 17-21 17" fill="none" stroke="#ffd884" stroke-width="9" stroke-linecap="round" stroke-linejoin="round"/>
  <path d="M108 158h48" fill="none" stroke="#ffd884" stroke-width="9" stroke-linecap="round"/>
  <rect x="162" y="149" width="9" height="18" rx="3" fill="#66d8cc"/>
</svg>`

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
  headless: true,
  args: ['--no-sandbox'],
})

try {
  for (const size of [64, 256]) {
    const context = await browser.newContext({
      viewport: { width: size, height: size },
      deviceScaleFactor: 1,
    })
    const page = await context.newPage()
    await page.setContent(`<style>html,body{margin:0;width:100%;height:100%;background:transparent}svg{display:block;width:100%;height:100%}</style>${svg}`)
    const png = await page.screenshot({ omitBackground: true, type: 'png' })
    const packageIcon = path.join(fnosDir, size === 64 ? 'ICON.PNG' : 'ICON_256.PNG')
    await writeFile(packageIcon, addSRGBChunk(png))
    await copyFile(packageIcon, path.join(fnosDir, `app/ui/images/icon_${size}.png`))
    await context.close()
  }
} finally {
  await browser.close()
}

for (const relativePath of ['ICON.PNG', 'ICON_256.PNG']) {
  const data = await readFile(path.join(fnosDir, relativePath))
  if (!hasChunk(data, 'sRGB')) throw new Error(`${relativePath} is missing its sRGB PNG chunk`)
}

function addSRGBChunk(data) {
  if (hasChunk(data, 'sRGB')) return data
  const chunkType = Buffer.from('sRGB')
  const payload = Buffer.from([0])
  const chunk = Buffer.alloc(4 + 4 + payload.length + 4)
  chunk.writeUInt32BE(payload.length, 0)
  chunkType.copy(chunk, 4)
  payload.copy(chunk, 8)
  chunk.writeUInt32BE(crc32(Buffer.concat([chunkType, payload])), chunk.length - 4)
  return Buffer.concat([data.subarray(0, 33), chunk, data.subarray(33)])
}

function hasChunk(data, expected) {
  let offset = 8
  while (offset + 12 <= data.length) {
    const length = data.readUInt32BE(offset)
    const type = data.toString('ascii', offset + 4, offset + 8)
    if (type === expected) return true
    offset += 12 + length
  }
  return false
}

function crc32(data) {
  let crc = 0xffffffff
  for (const byte of data) {
    crc ^= byte
    for (let bit = 0; bit < 8; bit++) {
      crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1))
    }
  }
  return (crc ^ 0xffffffff) >>> 0
}
