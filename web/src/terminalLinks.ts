import type { ILink, ILinkProvider, Terminal } from "@xterm/xterm";

const imageExtension = /\.(?:avif|bmp|gif|jpe?g|png|webp)$/i;
const urlPrefix = /^https?:\/\//i;
const tokenPattern = /[^\s"'`<>]+/g;
const visualLinkSearchRows = 32;
const marginToken = /^[│┃║┆┊]+$/;

type LinkKind = "url" | "image";
type TerminalLinkHandler = (event: MouseEvent, value: string, kind: LinkKind) => void;

type RowText = {
  text: string;
  columns: number[];
  row: number;
};

function rowText(terminal: Terminal, row: number): RowText | undefined {
  const line = terminal.buffer.active.getLine(row);
  if (!line) return;
  let text = "";
  const columns: number[] = [];
  for (let column = 0; column < terminal.cols; column++) {
    const cell = line.getCell(column);
    if (!cell || cell.getWidth() === 0) continue;
    const chars = cell.getChars() || " ";
    for (let index = 0; index < chars.length; index++) {
      columns.push(column);
      text += chars[index];
    }
  }
  return { text, columns, row };
}
function logicalRows(terminal: Terminal, row: number) {
  let first = row;
  while (first > 0 && terminal.buffer.active.getLine(first)?.isWrapped) first--;
  const rows: RowText[] = [];
  for (let current = first; current < terminal.buffer.active.length; current++) {
    const value = rowText(terminal, current);
    if (!value) break;
    rows.push(value);
    if (!terminal.buffer.active.getLine(current + 1)?.isWrapped) break;
  }
  return { first, rows };
}

function trimToken(raw: string, offset: number) {
  let value = raw;
  let start = offset;
  while (/^[([<{!]/.test(value)) {
    value = value.slice(1);
    start++;
  }
  while (/[.,!?;:)}>]+$/.test(value) || value.endsWith("]")) value = value.slice(0, -1);
  return { value, start, end: start + value.length };
}

function leadingToken(row: RowText) {
  tokenPattern.lastIndex = 0;
  for (const match of row.text.matchAll(tokenPattern)) {
    if (marginToken.test(match[0])) continue;
    const start = match.index || 0;
    return { raw: match[0], start, end: start + match[0].length };
  }
}

function hasOpenContainer(value: string) {
  const pairs: Array<[string, string]> = [["(", ")"], ["[", "]"], ["{", "}"]];
  return pairs.some(([open, close]) => {
    let depth = 0;
    for (const character of value) {
      if (character === open) depth++;
      else if (character === close && depth > 0) depth--;
    }
    return depth > 0;
  });
}

function canContinueVisualURL(raw: string, row: RowText, tokenEnd: number, columns: number) {
  if (hasOpenContainer(raw) || /[=&?\/#%]$/.test(raw)) return true;
  const endColumn = row.columns[Math.max(0, tokenEnd - 1)] ?? 0;
  return raw.length >= 24 && endColumn >= columns - 2;
}

function visualURLLinks(terminal: Terminal, requestedRow: number, handler: TerminalLinkHandler) {
  const links: ILink[] = [];
  const firstRow = Math.max(0, requestedRow - visualLinkSearchRows);
  for (let startRow = firstRow; startRow <= requestedRow; startRow++) {
    const row = rowText(terminal, startRow);
    if (!row) continue;
    for (const match of row.text.matchAll(tokenPattern)) {
      const initial = trimToken(match[0], match.index || 0);
      if (!urlPrefix.test(initial.value)) continue;

      const pieces: RowText[] = [{
        text: match[0],
        columns: row.columns.slice(match.index || 0, (match.index || 0) + match[0].length),
        row: startRow,
      }];
      let raw = match[0];
      let currentRow = row;
      let tokenEnd = (match.index || 0) + match[0].length;
      for (let nextRow = startRow + 1;
        nextRow < terminal.buffer.active.length && nextRow <= startRow + visualLinkSearchRows;
        nextRow++) {
        if (!canContinueVisualURL(raw, currentRow, tokenEnd, terminal.cols)) break;
        // Native xterm wrapping is already handled by logicalRows(). This path
        // covers applications such as Codex that repaint a continuation line.
        if (terminal.buffer.active.getLine(nextRow)?.isWrapped) break;
        const next = rowText(terminal, nextRow);
        if (!next) break;
        const continuation = leadingToken(next);
        if (!continuation) break;
        const candidate = trimToken(continuation.raw, 0).value;
        if (!candidate || urlPrefix.test(candidate)) break;
        pieces.push({
          text: continuation.raw,
          columns: next.columns.slice(continuation.start, continuation.end),
          row: nextRow,
        });
        raw += continuation.raw;
        currentRow = next;
        tokenEnd = continuation.end;
      }
      if (pieces.length < 2 || pieces.at(-1)!.row < requestedRow) continue;

      const token = trimToken(raw, 0);
      const start = pointAt(pieces, token.start);
      const end = pointAt(pieces, Math.max(token.start, token.end - 1));
      links.push({
        text: token.value,
        range: {
          start: { x: start.x + 1, y: start.y + 1 },
          end: { x: end.x + 1, y: end.y + 1 },
        },
        activate: (event, value) => handler(event, value, "url"),
      });
    }
  }
  return links;
}

function pointAt(rows: RowText[], offset: number) {
  let remaining = offset;
  for (const row of rows) {
    if (remaining < row.text.length) {
      return { x: row.columns[remaining] ?? 0, y: row.row };
    }
    remaining -= row.text.length;
  }
  const last = rows[rows.length - 1];
  return { x: last?.columns.at(-1) ?? 0, y: last?.row ?? 0 };
}

function linksFor(terminal: Terminal, lineNumber: number, handler: TerminalLinkHandler): ILink[] {
  const requestedRow = lineNumber - 1;
  const { rows } = logicalRows(terminal, requestedRow);
  if (!rows.length) return [];
  const text = rows.map((row) => row.text).join("");
  const links: ILink[] = [];
  for (const match of text.matchAll(tokenPattern)) {
    const token = trimToken(match[0], match.index || 0);
    let kind: LinkKind | undefined;
    if (urlPrefix.test(token.value)) kind = "url";
    else if (imageExtension.test(token.value.split(/[?#]/, 1)[0] || "")) kind = "image";
    if (!kind || !token.value) continue;
    const start = pointAt(rows, token.start);
    const end = pointAt(rows, Math.max(token.start, token.end - 1));
    links.push({
      text: token.value,
      range: {
        start: { x: start.x + 1, y: start.y + 1 },
        end: { x: end.x + 1, y: end.y + 1 },
      },
      activate: (event, value) => handler(event, value, kind),
    });
  }
  let matching = links.filter((link) =>
    link.range.start.y <= lineNumber && link.range.end.y >= lineNumber,
  );
  for (const link of visualURLLinks(terminal, requestedRow, handler)) {
    matching = matching.filter((existing) => !(link.text.startsWith(existing.text) &&
      existing.range.start.x === link.range.start.x &&
      existing.range.start.y === link.range.start.y));
    if (!matching.some((existing) => existing.text === link.text &&
      existing.range.start.x === link.range.start.x &&
      existing.range.start.y === link.range.start.y)) matching.push(link);
  }
  return matching;
}

export function createTerminalLinkProvider(
  terminal: Terminal,
  handler: TerminalLinkHandler,
): ILinkProvider {
  return {
    provideLinks(lineNumber, callback) {
      callback(linksFor(terminal, lineNumber, handler));
    },
  };
}
