export type CodeHunk = { fromA: number; toA: number; fromB: number; toB: number; index: number; total: number };
export function replaceCodeHunk(left: string, right: string, hunk: CodeHunk, reverse = false) {
  const a = left.replace(/\r\n/g, '\n'), b = right.replace(/\r\n/g, '\n');
  const original = reverse ? right : left;
  const next = reverse ? b.slice(0, hunk.fromB) + a.slice(hunk.fromA, hunk.toA) + b.slice(hunk.toB) : a.slice(0, hunk.fromA) + b.slice(hunk.fromB, hunk.toB) + a.slice(hunk.toA);
  return original.includes('\r\n') ? next.replace(/\n/g, '\r\n') : next;
}
