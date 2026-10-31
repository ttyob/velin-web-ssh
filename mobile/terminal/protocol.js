export const VERSION = 1;
export const MAX_INPUT_BYTES = 48 * 1024;
export function decodeBytes(encoded) {
  return Uint8Array.from(atob(encoded), c => c.charCodeAt(0));
}
export function encodeBytes(bytes) {
  let result = '';
  for (let i = 0; i < bytes.length; i += 8192) result += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return btoa(result);
}
export function accepted(message, sessionID, generation) {
  return message?.version === VERSION && message.sessionID === sessionID && message.generation === generation;
}
export function encodePaste(text, bracketed, submit) {
  const bytes = new TextEncoder().encode(text);
  if (bytes.length > MAX_INPUT_BYTES) throw new RangeError('input_too_large');
  // Raw escape sequences from a clipboard must never become terminal keys.
  if (/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(text)) throw new Error('control_characters');
  const body = bracketed ? `\x1b[200~${text}\x1b[201~` : text;
  const encoded = new TextEncoder().encode(body + (submit ? '\r' : ''));
  if (encoded.length > MAX_INPUT_BYTES) throw new RangeError('input_too_large');
  return encoded;
}
export function suffixAtOffset(bytes, end, applied) {
  if (!Number.isSafeInteger(end) || end < bytes.length) throw new Error('invalid_offset');
  if (end <= applied) return new Uint8Array();
  const start = end - bytes.length;
  if (start > applied) throw new Error('output_gap');
  return bytes.subarray(Math.max(0, applied - start));
}
