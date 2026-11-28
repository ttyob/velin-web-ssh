import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { deflateSync } from 'node:zlib';
import { chromium } from 'playwright-core';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const source = name => path.join(root, 'assets/brand', name);

// Keep all platform assets derived from the supplied artwork, including the
// transparent wordmarks. Regeneration must never bring the old icons back.
export async function generateBrandAssets(targets = ['web', 'desktop', 'fnos', 'mobile']) {
  const browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
    args: ['--no-sandbox'],
  });
  try {
    const page = await browser.newPage();
    const images = {};
    for (const name of ['app-icon', 'platform-icon', 'logo-white', 'logo-black']) {
      images[name] = `data:image/png;base64,${(await readFile(source(`${name}.png`))).toString('base64')}`;
    }
    const cache = new Map();
    async function render(name, size, { flat = false, maskable = false } = {}) {
      const key = `${name}:${size}:${flat}:${maskable}`;
      if (cache.has(key)) return cache.get(key);
      const data = await page.evaluate(async ({ url, size, flat, maskable, mark, edgeToEdge }) => {
        const image = new Image();
        image.src = url;
        await image.decode();
        const probe = document.createElement('canvas');
        probe.width = image.width; probe.height = image.height;
        const ctx = probe.getContext('2d');
        ctx.drawImage(image, 0, 0);
        const pixels = ctx.getImageData(0, 0, image.width, image.height).data;
        let left = image.width, top = image.height, right = 0, bottom = 0;
        for (let y = 0; y < image.height; y++) {
          for (let x = 0; x < image.width; x++) {
            if (pixels[(y * image.width + x) * 4 + 3] > 12) {
              left = Math.min(left, x); top = Math.min(top, y);
              right = Math.max(right, x + 1); bottom = Math.max(bottom, y + 1);
            }
          }
        }
        // This supplied platform artwork already includes its intended spacing.
        if (edgeToEdge) { left = 0; top = 0; right = image.width; bottom = image.height; }
        const canvas = document.createElement('canvas');
        canvas.width = canvas.height = size;
        const output = canvas.getContext('2d');
        if (flat || maskable) { output.fillStyle = '#ffffff'; output.fillRect(0, 0, size, size); }
        output.imageSmoothingEnabled = true; output.imageSmoothingQuality = 'high';
        const padding = edgeToEdge ? 0 : maskable ? .17 : mark ? .04 : .03;
        const scale = size * (1 - padding * 2) / Math.max(right - left, bottom - top);
        const width = (right - left) * scale, height = (bottom - top) * scale;
        output.drawImage(image, left, top, right - left, bottom - top,
          (size - width) / 2, (size - height) / 2, width, height);
        if (flat) {
          const rgba = output.getImageData(0, 0, size, size).data;
          const rgb = new Uint8Array(size * (size * 3 + 1));
          for (let y = 0; y < size; y++) {
            for (let x = 0; x < size; x++) {
              const from = (y * size + x) * 4, to = y * (size * 3 + 1) + 1 + x * 3;
              rgb[to] = rgba[from]; rgb[to + 1] = rgba[from + 1]; rgb[to + 2] = rgba[from + 2];
            }
          }
          let binary = '';
          for (let offset = 0; offset < rgb.length; offset += 32768) binary += String.fromCharCode(...rgb.subarray(offset, offset + 32768));
          return { rgb: btoa(binary) };
        }
        return { png: canvas.toDataURL('image/png').split(',')[1] };
      }, { url: images[name], size, flat, maskable, mark: name.startsWith('logo-'), edgeToEdge: name === 'platform-icon' });
      // iOS requires a real RGB PNG, even an unused alpha channel is rejected.
      const png = addSRGBChunk(data.rgb
        ? encodeRGB(Buffer.from(data.rgb, 'base64'), size)
        : Buffer.from(data.png, 'base64'));
      cache.set(key, png);
      return png;
    }
    async function save(relative, data) {
      const destination = path.join(root, relative);
      await mkdir(path.dirname(destination), { recursive: true });
      await writeFile(destination, data);
    }
    async function icon(relative, size, options) { await save(relative, await render('app-icon', size, options)); }
    async function platformIcon(relative, size) { await save(relative, await render('platform-icon', size)); }

    if (targets.includes('web')) {
      for (const name of ['logo-white', 'logo-black']) await save(`web/src/assets/brand/${name}.png`, await render(name, 256));
      await icon('web/public/favicon-32.png', 32);
      await icon('web/public/favicon-192.png', 192);
      await icon('web/public/brand/app-icon-512.png', 512);
      await icon('web/public/apple-touch-icon.png', 180, { flat: true });
      await save('web/public/favicon.ico', await ico(render, [16, 32, 48]));
    }
    if (targets.includes('desktop')) {
      await platformIcon('build/appicon.png', 1024);
      await save('build/windows/icon.ico', await ico(render, [16, 20, 24, 32, 40, 48, 64, 128, 256], 'platform-icon'));
      const logo = `data:image/png;base64,${(await render('logo-white', 128)).toString('base64')}`;
      const favicon = `data:image/png;base64,${(await render('platform-icon', 32)).toString('base64')}`;
      for (const name of ['index', 'launcher']) {
        const relative = `desktop/dist/${name}.html`;
        const html = await readFile(path.join(root, relative), 'utf8');
        await save(relative, html
          .replace(/(data-brand-logo src=")[^"]*(")/g, `$1${logo}$2`)
          .replace(/(data-brand-favicon href=")[^"]*(")/g, `$1${favicon}$2`));
      }
    }
    if (targets.includes('fnos')) {
      for (const size of [64, 256]) {
        const data = await render('platform-icon', size);
        await save(`packaging/fnos/${size === 64 ? 'ICON.PNG' : 'ICON_256.PNG'}`, data);
        await save(`packaging/fnos/app/ui/images/icon_${size}.png`, data);
      }
    }
    if (targets.includes('mobile')) {
      for (const name of ['logo-white', 'logo-black']) await save(`mobile/app/assets/brand/${name}.png`, await render(name, 256));
      const app = 'mobile/app/';
      const ios = `${app}ios/Runner/Assets.xcassets/AppIcon.appiconset/`;
      const manifest = JSON.parse(await readFile(path.join(root, ios, 'Contents.json'), 'utf8'));
      for (const entry of manifest.images) {
        await icon(ios + entry.filename, Math.round(parseFloat(entry.size) * parseFloat(entry.scale)), { flat: true });
      }
      for (const size of [192, 512]) {
        await icon(`${app}web/icons/Icon-${size}.png`, size);
        await icon(`${app}web/icons/Icon-maskable-${size}.png`, size, { maskable: true });
      }
      await icon(`${app}web/favicon.png`, 32);
    }
    if (targets.includes('mobile') || targets.includes('android')) {
      for (const [density, size] of [['mdpi', 48], ['hdpi', 72], ['xhdpi', 96], ['xxhdpi', 144], ['xxxhdpi', 192]]) {
        await platformIcon(`mobile/app/android/app/src/main/res/mipmap-${density}/ic_launcher.png`, size);
      }
    }
    console.log(`Jeex Terminal brand assets generated: ${targets.join(', ')}.`);
  } finally { await browser.close(); }
}

async function ico(render, sizes, name = 'app-icon') {
  const frames = await Promise.all(sizes.map(size => render(name, size)));
  const header = Buffer.alloc(6 + sizes.length * 16);
  header.writeUInt16LE(1, 2); header.writeUInt16LE(sizes.length, 4);
  let offset = header.length;
  sizes.forEach((size, i) => {
    const position = 6 + i * 16;
    header[position] = header[position + 1] = size === 256 ? 0 : size;
    header.writeUInt16LE(1, position + 4); header.writeUInt16LE(32, position + 6);
    header.writeUInt32LE(frames[i].length, position + 8); header.writeUInt32LE(offset, position + 12);
    offset += frames[i].length;
  });
  return Buffer.concat([header, ...frames]);
}

function addSRGBChunk(data) {
  for (let position = 8; position + 12 <= data.length;) {
    if (data.toString('ascii', position + 4, position + 8) === 'sRGB') return data;
    position += data.readUInt32BE(position) + 12;
  }
  return Buffer.concat([data.subarray(0, 33), pngChunk('sRGB', Buffer.from([0])), data.subarray(33)]);
}

function encodeRGB(rows, size) {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0); header.writeUInt32BE(size, 4);
  header[8] = 8; header[9] = 2;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk('IHDR', header), pngChunk('IDAT', deflateSync(rows)), pngChunk('IEND', Buffer.alloc(0)),
  ]);
}

function pngChunk(name, payload) {
  const type = Buffer.from(name), chunk = Buffer.alloc(12 + payload.length);
  chunk.writeUInt32BE(payload.length, 0); type.copy(chunk, 4); payload.copy(chunk, 8);
  let crc = 0xffffffff;
  for (const byte of Buffer.concat([type, payload])) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  chunk.writeUInt32BE((crc ^ 0xffffffff) >>> 0, chunk.length - 4);
  return chunk;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await generateBrandAssets(process.argv.length > 2 ? process.argv.slice(2) : undefined);
}
