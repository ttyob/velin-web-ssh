import test from 'node:test';
import assert from 'node:assert/strict';
import { encodeBytes, decodeBytes, encodePaste, suffixAtOffset, accepted } from '../protocol.js';

test('Unicode bytes survive bridge boundaries without intermediate text decoding', () => {
  const source = new TextEncoder().encode('中文🙂\x1b[32mhello\x1b[0m');
  const joined = new Uint8Array([...decodeBytes(encodeBytes(source.subarray(0, 2))), ...decodeBytes(encodeBytes(source.subarray(2)))]);
  assert.deepEqual(joined, source);
});
test('paste separates body, bracketed mode and one submit key', () => {
  assert.equal(new TextDecoder().decode(encodePaste('中文\nsecond', true, true)), '\x1b[200~中文\nsecond\x1b[201~\r');
  assert.equal(new TextDecoder().decode(encodePaste('hello', false, false)), 'hello');
  assert.throws(() => encodePaste('\x1b[201~bad', true, false), /control_characters/);
  assert.throws(() => encodePaste('x'.repeat(48*1024), true, true), /input_too_large/);
});
test('offsets reject gaps and strip only overlapping bytes', () => {
  const bytes = new Uint8Array([1,2,3,4]);
  assert.deepEqual(suffixAtOffset(bytes, 8, 6), new Uint8Array([3,4]));
  assert.equal(suffixAtOffset(bytes, 8, 8).length, 0);
  assert.throws(() => suffixAtOffset(bytes, 8, 3), /output_gap/);
});
test('late pages cannot address a replacement renderer', () => {
  assert.equal(accepted({version:1,sessionID:'a',generation:'old'},'a','new'), false);
  assert.equal(accepted({version:1,sessionID:'b',generation:'new'},'a','new'), false);
  assert.equal(accepted({version:1,sessionID:'a',generation:'new'},'a','new'), true);
});
