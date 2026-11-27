import { ChangeSet } from "@codemirror/state";
import { diff } from "@codemirror/merge";

export type TextRange = { from: number; to: number };
export type MergeConflict = TextRange & { left: string; right: string };

export function normalizeGitText(text: string) {
  return text.replace(/\r\n/g, "\n");
}

// Keep complete offsets, including the newline after a closing marker. This
// allows choosing either side without dropping the following unchanged line.
export function parseMergeConflicts(text: string): MergeConflict[] {
  const lines = [...text.matchAll(/[^\n]*(?:\n|$)/g)].filter(m => m[0].length);
  const result: MergeConflict[] = [];
  for (let i = 0; i < lines.length; i++) {
    const start = lines[i];
    const marker = start[0].trimEnd().match(/^(<{7,})(?: .*)?$/);
    if (!marker) continue;
    const width = marker[1].length;
    let base = -1, middle = -1, end = -1;
    for (let j = i + 1; j < lines.length; j++) {
      const line = lines[j][0].trimEnd();
      if (line.startsWith("|".repeat(width)) && base < 0 && middle < 0) base = j;
      if (line === "=".repeat(width) && middle < 0) middle = j;
      if (line.startsWith(">".repeat(width)) && middle >= 0) { end = j; break; }
    }
    if (middle < 0 || end < 0) continue;
    const offset = (n: number) => lines[n].index!;
    result.push({
      from: start.index!, to: offset(end) + lines[end][0].length,
      left: text.slice(offset(i + 1), offset(base >= 0 ? base : middle)),
      right: text.slice(offset(middle + 1), offset(end)),
    });
    i = end;
  }
  return result;
}

export function mapTextRanges(from: string, to: string, ranges: TextRange[]): TextRange[] {
  const changes = ChangeSet.of(diff(from, to, { timeout: 1500 }).map(c => ({
    from: c.fromA, to: c.toA, insert: to.slice(c.fromB, c.toB),
  })), from.length);
  return ranges.map(r => ({ from: changes.mapPos(r.from, -1), to: changes.mapPos(r.to, 1) }));
}

export function joinConflictSides(left: string, right: string) {
  if (left === right) return left;
  return left + (left && right && !left.endsWith("\n") ? "\n" : "") + right;
}

export function overlaps(a: TextRange, b: TextRange) {
  if (a.from === a.to) return a.from >= b.from && a.from < b.to;
  return a.from < b.to && a.to > b.from;
}
