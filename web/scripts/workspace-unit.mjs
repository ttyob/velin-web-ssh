import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import { EditorState } from '@codemirror/state'
import { Chunk } from '@codemirror/merge'
const load = async name => {
  const source = await readFile(new URL('../src/' + name, import.meta.url), 'utf8')
  return import('data:text/javascript;base64,' + Buffer.from(ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 } }).outputText).toString('base64'))
}
const { replaceCodeHunk } = await load('codeHunks.ts')
for (const [left, right] of [
  ['a\nb\nc\n', 'a\nB\nc\n'],
  ['a\nb', 'a\nB'],
  ['a\nb', 'a\nb\n'],
  ['', 'new file\n'],
  ['all removed\n', ''],
  ['first\r\nlast\r\n', 'first\r\nmodified\r\n'],
  ['漢字😀\nlast', '漢字😀 added\nlast'],
]) {
  const a = EditorState.create({ doc: left }).doc, b = EditorState.create({ doc: right }).doc
  const chunks = Chunk.build(a, b)
  assert.equal(chunks.length, 1)
  const hunk = { ...chunks[0], index: 0, total: 1 }
  assert.equal(replaceCodeHunk(left, right, hunk), right)
  assert.equal(replaceCodeHunk(left, right, hunk, true), left)
}
const base = Array.from({length: 50}, (_, index) => `line ${index}`).join('\n') + '\n'
const work = base.replace('line 2\n', 'first change\n').replace('line 42\n', 'second change\n')
const chunks = Chunk.build(EditorState.create({doc: base}).doc, EditorState.create({doc: work}).doc)
assert.equal(chunks.length, 2)
assert.equal(replaceCodeHunk(base, work, { ...chunks[0], index: 0, total: 2 }), base.replace('line 2\n', 'first change\n'))
assert.equal(replaceCodeHunk(base, work, { ...chunks[1], index: 1, total: 2 }, true), base.replace('line 2\n', 'first change\n'))
console.log('Workspace hunk checks passed: partial replacements, CRLF, missing newline, empty files and Unicode.')
