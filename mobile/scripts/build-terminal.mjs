import { mkdir, copyFile, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
const source = new URL('../terminal/', import.meta.url);
const target = new URL('../app/assets/terminal/', import.meta.url);
await mkdir(target, { recursive: true });
for (const name of ['index.html', 'terminal.js', 'terminal.css', 'protocol.js']) {
  await copyFile(new URL(name, source), new URL(name, target));
}
// Local file WebViews must not depend on module fetch/CORS support.
const protocol = (await readFile(new URL('protocol.js', source), 'utf8')).replace(/^export /gm, '');
const entry = (await readFile(new URL('terminal.js', source), 'utf8')).replace(/^import .* from '\.\/protocol\.js';\n/, '');
await writeFile(new URL('terminal.js', target), `(() => {\n'use strict';\n${protocol}\n${entry}\n})();\n`);
for (const [from, to] of [
  ['@xterm/xterm/lib/xterm.js', 'xterm.js'],
  ['@xterm/xterm/css/xterm.css', 'xterm.css'],
  ['@xterm/addon-fit/lib/addon-fit.js', 'addon-fit.js'],
]) await copyFile(new URL(`node_modules/${from}`, source), new URL(to, target));
console.log(`Terminal assets: ${fileURLToPath(target)}`);
