import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';
const source = await readFile(new URL('../src/terminalSelection.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 } }).outputText;
const { ScrollingSelection } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`);
const line = (text, wrapped = false) => ({ cells: [...text.padEnd(30)], wrapped });
const screen = top => Array.from({ length: 8 }, (_, row) => line(`line-${String(top + row).padStart(3, '0')} text`));
const forward = new ScrollingSelection(screen(10), { row: 2, column: 5 });
for (let top = 11; top <= 40; top++) assert(forward.scroll(screen(top), 1));
forward.move({ row: 6, column: 8 });
assert.equal(forward.text(), ['line-012 text'.slice(5), ...Array.from({ length: 33 }, (_, i) => `line-${String(13 + i).padStart(3, '0')} text`), 'line-046 text'.slice(0, 8)].join('\n'));
const reverse = new ScrollingSelection(screen(40), { row: 5, column: 8 });
for (let top = 39; top >= 10; top--) assert(reverse.scroll(screen(top), -1));
reverse.move({ row: 1, column: 5 });
assert.equal(reverse.text(), ['line-011 text'.slice(5), ...Array.from({ length: 33 }, (_, i) => `line-${String(12 + i).padStart(3, '0')} text`), 'line-045 text'.slice(0, 8)].join('\n'));
// Moving back must shrink the range instead of appending rows a second time.
for (let top = 11; top <= 30; top++) assert(reverse.scroll(screen(top), 1));
reverse.move({ row: 3, column: 0 });
assert.equal(reverse.text(), [...Array.from({ length: 12 }, (_, i) => `line-${String(33 + i).padStart(3, '0')} text`), 'line-045 text'.slice(0, 8)].join('\n'));
const pinned = top => [line('CLI HEADER'), ...screen(top).slice(0, 5), line('composer >'), line('status footer')];
const tui = new ScrollingSelection(pinned(20), { row: 2, column: 0 });
for (let top = 21; top <= 40; top++) assert(tui.scroll(pinned(top), 1));
tui.move({ row: 7, column: 30 });
assert.equal(tui.text(), Array.from({ length: 24 }, (_, i) => `line-${String(21 + i).padStart(3, '0')} text`).join('\n'));
assert(!tui.scroll(pinned(40), 1), 'A scroll at the boundary must not duplicate rows');
const unicode = new ScrollingSelection([
  { cells: ['中', '', '文', '', '🙂', '', 'e\u0301', ' '], wrapped: false },
  { cells: ['w','r','a','p','p','e','d',' '], wrapped: true },
], { row: 0, column: 1 });
unicode.move({ row: 1, column: 7 });
assert.equal(unicode.text(), '中文🙂e\u0301wrapped');
console.log('Scrolling selection unit checks passed: both directions, range shrink, fixed CLI areas, boundaries, Unicode and wrapped lines.');
