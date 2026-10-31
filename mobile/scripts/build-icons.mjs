import { readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
const require = createRequire(new URL('../terminal/package.json', import.meta.url));
const { chromium } = require('playwright-core');
const app = new URL('../app/', import.meta.url);
const svg = await readFile(new URL('../design/icon.svg', import.meta.url), 'utf8');
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox'], headless: true });
try {
  const page = await browser.newPage({ deviceScaleFactor: 2 });
  async function icon(path, size) {
    const data = await page.evaluate(async ({ svg, size }) => {
      const image = new Image();
      const ready = new Promise((resolve, reject) => { image.onload = resolve; image.onerror = reject; });
      image.src = `data:image/svg+xml;base64,${btoa(svg)}`;
      await ready;
      const canvas = document.createElement('canvas');
      canvas.width = canvas.height = size;
      canvas.getContext('2d').drawImage(image, 0, 0, size, size);
      return canvas.toDataURL('image/png').split(',')[1];
    }, { svg, size });
    await writeFile(new URL(path, app), Buffer.from(data, 'base64'));
  }
  const manifest = JSON.parse(await readFile(new URL('ios/Runner/Assets.xcassets/AppIcon.appiconset/Contents.json', app), 'utf8'));
  for (const entry of manifest.images) {
    await icon(`ios/Runner/Assets.xcassets/AppIcon.appiconset/${entry.filename}`, Math.round(parseFloat(entry.size) * parseFloat(entry.scale)));
  }
  for (const [density, size] of [['mdpi',48],['hdpi',72],['xhdpi',96],['xxhdpi',144],['xxxhdpi',192]]) {
    await icon(`android/app/src/main/res/mipmap-${density}/ic_launcher.png`, size);
  }
  for (const size of [192,512]) {
    await icon(`web/icons/Icon-${size}.png`, size);
    await icon(`web/icons/Icon-maskable-${size}.png`, size);
  }
  await icon('web/favicon.png',32);
  console.log('Mobile platform icons generated.');
} finally { await browser.close(); }
