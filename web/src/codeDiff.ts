import { Change, diff } from "@codemirror/merge";

// Keep source offsets intact so block operations still use the original files.
function trimLines(source: string) {
  const positions = new Uint32Array(source.length + 1);
  const lines: string[] = [];
  let offset = 0, length = 0;
  for (const line of source.split("\n")) {
    const start = line.search(/[^\t ]/);
    const text = start < 0 ? "" : line.slice(start).replace(/[\t ]+$/, "");
    lines.push(text);
    for (let i = 0; i < text.length; i++) positions[length++] = offset + start + i;
    offset += line.length;
    if (offset < source.length) positions[length++] = offset++;
  }
  positions[length] = source.length;
  return { text: lines.join("\n"), positions };
}

export function diffIgnoringWhitespace(a: string, b: string) {
  const left = trimLines(a), right = trimLines(b);
  return diff(left.text, right.text, { scanLimit: 500, timeout: 100 }).map(change => {
    const fromA = left.positions[change.fromA]!, fromB = right.positions[change.fromB]!;
    return new Change(fromA, change.toA === change.fromA ? fromA : left.positions[change.toA - 1]! + 1,
      fromB, change.toB === change.fromB ? fromB : right.positions[change.toB - 1]! + 1);
  });
}

export interface DiffFileInfo { exists: boolean; size: number; kind: "text" | "image" | "binary" | "unsupported" }
export interface DiffFileSide extends DiffFileInfo { imageURL?: string; downloadURL?: string }
export interface DiffFilePreview { left: DiffFileSide; right: DiffFileSide }
export const emptyDiffFile = (): DiffFileSide => ({ exists: false, size: 0, kind: "text" });
export function canLoadDiff(preview: DiffFilePreview) { return [preview.left, preview.right].every(side => !side.exists || side.kind === "text" && side.size <= 2 * 1024 * 1024); }
export function needsDiffPreview(preview: DiffFilePreview) { return [preview.left, preview.right].some(side => side.exists && (side.kind !== "text" || side.size >= 256 * 1024)); }

export interface DiffReadingState {
  scroll: number; horizontal: number; anchorA: number; anchorB: number; top: number; active: number;
  inline: boolean; wrap: boolean; collapsed: boolean; ignoreWhitespace: boolean; expandedA: number[]; expandedB: number[];
}
const readings = new Map<string, DiffReadingState>();
export function readDiffState(key: string): DiffReadingState | undefined { return readings.get(key); }
export function saveDiffState(key: string, value: DiffReadingState) {
  readings.delete(key); readings.set(key, value);
  if (readings.size > 100) readings.delete(readings.keys().next().value!);
}
