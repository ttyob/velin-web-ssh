import type { Terminal } from "@xterm/xterm";

const imageExtension = "(?:avif|bmp|gif|jpe?g|png|webp)";
const imageToken = new RegExp("(?:file://)?[^\\s\\\"'`<>│┃║┆┊]+?\\." + imageExtension, "gi");
const panelTitle = new RegExp(`[^\\s│┃║┆┊×]+?\\.${imageExtension}`, "i");
const panelFallback = "Image preview unavailable in this terminal";
const dividers = new Set(["│", "┃", "║", "┆", "┊"]);

type ScreenRow = {
  text: string;
  columns: number[];
  row: number;
};

export type TerminalImagePanel = {
  path: string;
  name: string;
  startColumn: number;
  startRow: number;
  endColumn: number;
  endRow: number;
};

function screenRow(terminal: Terminal, row: number): ScreenRow | undefined {
  const line = terminal.buffer.active.getLine(terminal.buffer.active.viewportY + row);
  if (!line) return;
  let text = "";
  const columns: number[] = [];
  for (let column = 0; column < terminal.cols; column++) {
    const cell = line.getCell(column);
    if (!cell || cell.getWidth() === 0) continue;
    const chars = cell.getChars() || " ";
    for (let index = 0; index < chars.length; index++) {
      text += chars[index];
      columns.push(column);
    }
  }
  return { text, columns, row };
}

function basename(path: string) {
  return path.split(/[\\/]/).pop() || path;
}

function titleMatches(name: string, title: string) {
  if (!title.includes("…")) return name === title;
  const [prefix, suffix] = title.split("…", 2);
  return name.startsWith(prefix) && name.endsWith(suffix || "");
}

function cleanPath(value: string) {
  value = value.replace(/[),.;:!?]+$/g, "");
  if (!value.startsWith("file://")) return value;
  try {
    return decodeURIComponent(new URL(value).pathname);
  } catch {
    return value.slice("file://".length);
  }
}

export function detectTerminalImagePanel(terminal: Terminal): TerminalImagePanel | undefined {
  const rows = Array.from({ length: terminal.rows }, (_, row) => screenRow(terminal, row));
  const marker = rows.find((row) => row?.text.includes(panelFallback));
  if (!marker) return;
  const markerOffset = marker.text.indexOf(panelFallback);
  const markerColumn = marker.columns[markerOffset] ?? 0;
  const markerLine = terminal.buffer.active.getLine(terminal.buffer.active.viewportY + marker.row);
  let divider = -1;
  for (let column = markerColumn - 1; column >= 0; column--) {
    if (dividers.has(markerLine?.getCell(column)?.getChars() || "")) {
      divider = column;
      break;
    }
  }
  const startColumn = divider >= 0 ? divider + 1 : Math.max(0, markerColumn - 1);

  let title = "";
  for (let row = marker.row; row >= Math.max(0, marker.row - 4); row--) {
    const current = rows[row];
    if (!current) continue;
    const offset = current.columns.findIndex(column => column >= startColumn);
    const right = offset >= 0 ? current.text.slice(offset) : "";
    const match = panelTitle.exec(right);
    if (match) {
      title = match[0];
      break;
    }
  }

  const candidates: Array<{ path: string; row: number; score: number }> = [];
  for (const row of rows) {
    if (!row) continue;
    for (const match of row.text.matchAll(imageToken)) {
      const path = cleanPath(match[0]);
      if (path.includes("…")) continue;
      const name = basename(path);
      let score = path.startsWith("/") || path.startsWith("~/") ? 100 : 0;
      if (title && titleMatches(name, title)) score += 1000;
      score -= Math.abs(row.row - marker.row);
      candidates.push({ path, row: row.row, score });
    }
  }
  candidates.sort((a, b) => b.score - a.score || b.row - a.row);
  const selected = candidates[0];
  if (!selected) return;

  let endRow = terminal.rows;
  for (let row = marker.row + 1; row < terminal.rows; row++) {
    const current = rows[row];
    if (!current) continue;
    const offset = current.columns.findIndex(column => column >= startColumn);
    const right = offset >= 0 ? current.text.slice(offset) : "";
    if (/\bEsc\s+close\b/i.test(right)) {
      endRow = row;
      break;
    }
  }
  return {
    path: selected.path,
    name: basename(selected.path),
    startColumn,
    startRow: marker.row,
    endColumn: terminal.cols,
    endRow: Math.max(marker.row + 1, endRow),
  };
}
