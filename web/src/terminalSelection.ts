import type { Terminal } from '@xterm/xterm';

export type SelectionPoint = { row: number; column: number };
export type SelectionLine = { cells: string[]; wrapped: boolean; trimWrappedIndent?: boolean };
export type SelectionScreen = SelectionLine[];

export function selectionScreen(
  terminal: Terminal,
  tmuxHistory = false,
  applicationScreen = false,
): SelectionScreen {
  const buffer = terminal.buffer.active;
  const applicationRows = applicationScreen
    ? applicationWrapRows(terminal, buffer.viewportY, buffer.viewportY + terminal.rows - 1)
    : new Set<number>();
  return Array.from({ length: terminal.rows }, (_, row) => {
    const absoluteRow = buffer.viewportY + row;
    const line = buffer.getLine(absoluteRow);
    const cells = Array.from({ length: terminal.cols }, (_, col) => {
      const cell = line?.getCell(col);
      return cell?.getWidth() === 0 ? '' : cell?.getChars() || ' ';
    });
    // tmux draws its copy-mode position over the right end of the first row.
    // It is navigation chrome, not part of the selected shell output.
    if (tmuxHistory && row === 0) {
      const marker = /\[\d+\/\d+\]\s*$/.exec(line?.translateToString(false) || '');
      if (marker) {
        const column = terminal.cols - marker[0].length;
        cells.fill(' ', column);
      }
    }
    return {
      wrapped: Boolean(line?.isWrapped || applicationRows.has(absoluteRow)),
      trimWrappedIndent: Boolean(!line?.isWrapped && applicationRows.has(absoluteRow)),
      cells,
    };
  });
}

function lineText(line: SelectionLine) { return line.cells.join('').trimEnd(); }
function applicationLineWidth(terminal: Terminal, row: number) {
  const line = terminal.buffer.active.getLine(row);
  if (!line) return 0;
  for (let column = terminal.cols - 1; column >= 0; column--) {
    const cell = line.getCell(column);
    if (cell?.getChars()) return column + Math.max(1, cell.getWidth());
  }
  return 0;
}

function fillsTerminalWidth(terminal: Terminal, row: number) {
  return applicationLineWidth(terminal, row) >= Math.max(1, terminal.cols - 2);
}

function applicationLineText(terminal: Terminal, row: number) {
  return terminal.buffer.active.getLine(row)?.translateToString(true).trimEnd() || '';
}

function structuralApplicationLine(text: string) {
  return /^(?:[•*-]|\d+[.)]|[›>$#]|(?:OpenAI Codex|Claude Code|Aider|Gemini CLI|OpenCode)\b)/i.test(text.trimStart());
}

function applicationWrapRows(terminal: Terminal, start: number, end: number) {
  const rows = new Set<number>();
  const first = Math.max(0, start - 1);
  const texts = new Map<number, string>();
  for (let row = first; row <= end; row++) texts.set(row, applicationLineText(terminal, row));
  const contentWidth = Math.max(
    20,
    ...Array.from(texts.entries())
      .filter(([, text]) => text.length >= 20 && !structuralApplicationLine(text))
      .map(([row]) => applicationLineWidth(terminal, row)),
  );
  for (let row = start; row <= end; row++) {
    const line = terminal.buffer.active.getLine(row);
    if (!line || line.isWrapped) continue;
    const previous = texts.get(row - 1) || '';
    const current = texts.get(row) || '';
    if (!previous || !current || structuralApplicationLine(current)) continue;
    // A redraw-based wrap has no xterm flag. Codex's prose rows share the
    // same content edge, while the final continuation may be short.
    const reachesContentEdge = applicationLineWidth(terminal, row - 1) >= contentWidth - 3;
    const previousEndsSentence = /[。！？!?；;：:]$/.test(previous);
    const indentedContinuation = /^\s+\S/.test(current);
    if ((indentedContinuation && reachesContentEdge && !previousEndsSentence) ||
        fillsTerminalWidth(terminal, row - 1)) rows.add(row);
  }
  return rows;
}

function before(a: SelectionPoint, b: SelectionPoint) {
  return a.row < b.row || a.row === b.row && a.column <= b.column;
}

function isWrappedRow(terminal: Terminal, row: number, applicationScreen = false) {
  const line = terminal.buffer.active.getLine(row);
  if (!line) return false;
  if (line.isWrapped) return true;
  // Alternate-screen TUIs such as Codex often redraw soft-wrapped rows with
  // cursor movement instead of terminal autowrap, so xterm cannot mark them
  // with isWrapped. A full preceding row is the reliable screen-level signal.
  return (applicationScreen || terminal.buffer.active.type === 'alternate') &&
    fillsTerminalWidth(terminal, row - 1);
}

export function normalizeTerminalSelection(
  terminal: Terminal,
  selected: string,
  applicationScreen = terminal.buffer.active.type === 'alternate',
) {
  const position = terminal.getSelectionPosition();
  if (!position) return selected;
  const points = [position.start, position.end].sort((a, b) =>
    a.y - b.y || a.x - b.x,
  );
  const [start, end] = points;
  const applicationRows = applicationScreen
    ? applicationWrapRows(terminal, start.y, end.y)
    : new Set<number>();
  let containsWrappedLine = false;
  for (let row = start.y; row <= end.y; row++) {
    if (isWrappedRow(terminal, row, false) || applicationRows.has(row)) {
      containsWrappedLine = true;
      break;
    }
  }
  if (!containsWrappedLine) return selected;
  const result: string[] = [];
  for (let row = start.y; row <= end.y; row++) {
    const line = terminal.buffer.active.getLine(row);
    if (!line) return selected;
    const from = row === start.y ? start.x : 0;
    const to = row === end.y ? end.x : terminal.cols;
    let text = line.translateToString(true, from, to);
    if (row > start.y && (isWrappedRow(terminal, row, false) || applicationRows.has(row))) {
      // Codex-style redraws indent visual continuation rows. That indentation
      // belongs to the renderer, not to the selected logical input line.
      if (!line.isWrapped) text = text.replace(/^\s+/, '');
      result[result.length - 1] += text;
    }
    else result.push(text);
  }
  const normalized = result.join("\n");
  return normalized || selected;
}

// Alternate buffers are redrawn in place. Retain rows crossed by a drag rather
// than reading the last screen again at mouseup. Matching rows preserve the
// transcript; a full redraw follows the explicit scroll request as a fallback.
export class ScrollingSelection {
  private screen: SelectionScreen;
  private lines = new Map<number, SelectionLine>();
  private observed = new Map<number, SelectionLine>();
  private top = 0;
  private header = 0;
  private footer = 0;
  private minimumHeader = Number.POSITIVE_INFINITY;
  private start: SelectionPoint;
  private end: SelectionPoint;
  readonly columns: number;
  readonly rows: number;

  constructor(screen: SelectionScreen, start: SelectionPoint) {
    this.screen = screen;
    this.columns = screen[0]?.cells.length || 0;
    this.rows = screen.length;
    this.start = { ...start };
    this.end = { ...start };
    this.remember(screen);
  }

  private remember(screen: SelectionScreen) {
    for (let row = 0; row < screen.length; row++) {
      const key = this.top + row;
      if (!this.observed.has(key)) this.observed.set(key, screen[row]);
    }
    for (let row = this.header; row < screen.length - this.footer; row++) {
      this.lines.set(this.top + row, screen[row]);
    }
  }

  move(point: SelectionPoint) {
    this.end = {
      row: this.top + Math.max(this.header, Math.min(this.screen.length - this.footer - 1, point.row)),
      column: Math.max(0, Math.min(this.columns, point.column)),
    };
  }

  scroll(next: SelectionScreen, direction: number): boolean {
    if (next.length !== this.screen.length || next[0]?.cells.length !== this.columns) return false;
    const old = this.screen.map(lineText), current = next.map(lineText);
    let first = 0, last = old.length;
    while (first < last && old[first] === current[first]) first++;
    while (last > first && old[last - 1] === current[last - 1]) last--;
    if (first === last) return false;
    let shift = 0, bestScore = 0;
    for (let delta = 1 - old.length; delta < old.length; delta++) {
      if (!delta || Math.sign(delta) !== Math.sign(direction)) continue;
      let run = 0, longest = 0, nonempty = 0;
      for (let row = first; row < last; row++) {
        const previous = row + delta;
        if (previous >= first && previous < last && current[row] === old[previous]) {
          longest = Math.max(longest, ++run);
          if (current[row].trim()) nonempty++;
        } else run = 0;
      }
      // Blank margins or a single repeated border are insufficient evidence.
      if (longest < 2 || nonempty < 2) continue;
      const score = longest * 1000 + nonempty * 10 - Math.abs(delta - direction);
      if (score > bestScore) { bestScore = score; shift = delta; }
    }
    if (!shift) {
      // Some TUIs repaint the entire viewport, so there is no shared row to
      // match. The scroll request still gives us the exact viewport movement.
      shift = Math.max(1 - old.length, Math.min(old.length - 1, direction));
    }
    // Fixed CLI headers and composers stay outside the scrolling transcript.
    const detectedFooter = old.length - last;
    if (first > 0) this.minimumHeader = Math.min(this.minimumHeader, first);
    const header = first > 0 ? this.minimumHeader : 0;
    const footer = detectedFooter;
    for (let row = this.header; row < header; row++) this.lines.delete(this.top + row);
    for (let row = old.length - footer; row < old.length - this.footer; row++) this.lines.delete(this.top + row);
    this.header = header;
    this.footer = footer;
    // start.row is an absolute buffer coordinate. Keep the original anchor
    // fixed even after the viewport has moved past it or a fixed header grows.
    this.top += shift;
    this.screen = next;
    this.remember(next);
    return true;
  }

  private range(): [SelectionPoint, SelectionPoint] {
    return before(this.start, this.end) ? [this.start, this.end] : [this.end, this.start];
  }

  text(): string {
    const [start, end] = this.range();
    const result: string[] = [];
    for (let row = start.row; row <= end.row; row++) {
      const line = this.lines.get(row) || this.observed.get(row);
      if (!line) return ''; // Never silently copy a range containing unseen rows.
      let from = row === start.row ? start.column : 0;
      const to = row === end.row ? end.column : this.columns;
      if (from > 0 && line.cells[from] === '') from--;
      let text = line.cells.slice(from, to).join('').trimEnd().replace(/\u00a0/g, ' ');
      if (row > start.row && line.wrapped && line.trimWrappedIndent) text = text.replace(/^\s+/, '');
      if (row > start.row && line.wrapped) result[result.length - 1] += text;
      else result.push(text);
    }
    return result.join('\n');
  }

  paint(terminal: Terminal) {
    const [start, end] = this.range();
    const first = this.top + this.header;
    const last = this.top + this.screen.length - this.footer - 1;
    if (end.row < first || start.row > last) { terminal.clearSelection(); return; }
    const row = Math.max(start.row, first);
    const endRow = Math.min(end.row, last);
    const col = row === start.row ? start.column : 0;
    const endCol = endRow === end.row ? end.column : this.columns;
    terminal.select(col, terminal.buffer.active.viewportY + row - this.top, (endRow - row) * this.columns + endCol - col);
  }
}

export function attachScrollingSelection(terminal: Terminal, options: {
  selectable?: () => boolean;
  canScroll: () => boolean;
  scroll: (lines: number) => boolean | void;
  copy: (text: string) => void;
  visible: () => boolean;
  tmuxHistory: () => boolean;
  applicationScreen?: () => boolean;
  newline?: string;
}) {
  const host = terminal.element!;
  let model: ScrollingSelection | undefined;
  let active = false, down = false, moved = false;
  let origin = { x: 0, y: 0 }, pointer = { x: 0, y: 0 };
  let pending = 0, queued = 0;
  let scrollTimer: number | undefined, settleTimer: number | undefined;
  const rect = () => host.querySelector('.xterm-screen')!.getBoundingClientRect();
  const point = (): SelectionPoint => {
    const box = rect();
    return {
      row: Math.floor((pointer.y - box.top) * terminal.rows / box.height),
      column: Math.max(0, Math.min(terminal.cols, Math.round((pointer.x - box.left) * terminal.cols / box.width))),
    };
  };
  function text() {
    return active ? model!.text().replace(/\n/g, options.newline || '\n') : undefined;
  }
  function clear() {
    model = undefined;
    active = down = moved = false;
    pending = queued = 0;
    clearInterval(scrollTimer);
    clearTimeout(settleTimer);
  }
  function finish() {
    if (!model || down || pending || queued || !active || !options.visible()) return;
    const selected = text();
    if (selected) options.copy(selected);
  }
  function repaint() {
    if (!model || !active) return;
    model.move(point());
    model.paint(terminal);
  }
  function scroll(lines: number, queue = false) {
    if (!lines || !model || !options.canScroll()) return;
    if (!active) {
      active = true;
      // Remove xterm's own drag timer, which can only scroll its current buffer.
      terminal.clearSelection();
      repaint();
    }
    if (pending) {
      if (queue) queued += lines;
      return;
    }
    pending = lines;
    const localScroll = options.scroll(lines) === true;
    if (localScroll) queueMicrotask(changed);
    clearTimeout(settleTimer);
    settleTimer = window.setTimeout(() => { pending = queued = 0; finish(); }, 1500);
  }
  function start(event: MouseEvent) {
    if (event.button === 2 && active) {
      // Keep a completed local drag available to the workspace context menu.
      // Applications with mouse capture must not consume the right click.
      event.preventDefault();
      event.stopImmediatePropagation();
      return;
    }
    if (event.button !== 0) return;
    clear();
    if (event.detail !== 1 || event.altKey || event.ctrlKey || event.metaKey ||
        !(options.selectable?.() ?? options.canScroll())) return;
    const box = rect();
    if (event.clientX < box.left || event.clientX >= box.right || event.clientY < box.top || event.clientY >= box.bottom) return;
    origin = pointer = { x: event.clientX, y: event.clientY };
    model = new ScrollingSelection(selectionScreen(
      terminal,
      options.tmuxHistory(),
      options.applicationScreen?.(),
    ), point());
    down = true;
    scrollTimer = window.setInterval(() => {
      if (!down || !moved || !options.visible()) return;
      const box = rect();
      const direction = pointer.y < box.top ? -1 : pointer.y >= box.bottom ? 1 : 0;
      if (direction) scroll(direction);
    }, 100);
  }
  function move(event: MouseEvent) {
    if (!down || !model) return;
    pointer = { x: event.clientX, y: event.clientY };
    moved ||= Math.hypot(pointer.x - origin.x, pointer.y - origin.y) > 3;
    if (active) repaint();
    const box = rect();
    if (moved && (pointer.y < box.top || pointer.y >= box.bottom)) {
      scroll(pointer.y < box.top ? -1 : 1);
    }
  }
  function end(event: MouseEvent) {
    if (event.button !== 0 || !down) return;
    pointer = { x: event.clientX, y: event.clientY };
    repaint();
    down = false;
    clearInterval(scrollTimer);
    if (!active) { model = undefined; return; }
    if (!pending) queueMicrotask(finish);
  }
  function copy(event: ClipboardEvent) {
    const selected = text();
    if (selected === undefined || !event.clipboardData) return;
    event.clipboardData.setData('text/plain', selected);
    event.preventDefault();
    event.stopImmediatePropagation();
  }
  function changed() {
    if (!model || !active || !pending) return;
    if (!options.visible() || terminal.rows !== model.rows || terminal.cols !== model.columns) { clear(); return; }
    if (!model.scroll(selectionScreen(
      terminal,
      options.tmuxHistory(),
      options.applicationScreen?.(),
    ), pending)) {
      pending = 0;
      clearTimeout(settleTimer);
      finish();
      return;
    }
    pending = 0;
    clearTimeout(settleTimer);
    repaint();
    if (queued) {
      const lines = Math.max(-3, Math.min(3, queued));
      queued -= lines;
      scroll(lines);
    }
    finish();
  }
  const blur = () => {
    if (active && down) { down = false; clearInterval(scrollTimer); }
    else if (!active) clear();
  };
  host.addEventListener('mousedown', start, true);
  host.addEventListener('copy', copy, true);
  document.addEventListener('mousemove', move);
  document.addEventListener('mouseup', end);
  window.addEventListener('blur', blur);
  return {
    text,
    clear,
    get dragging() { return down && model !== undefined; },
    get waiting() { return pending !== 0; },
    wheel(lines: number) {
      if (!down || !model) {
        if (active) { clear(); terminal.clearSelection(); }
        return false;
      }
      scroll(lines, true);
      return true;
    },
    changed,
    dispose() {
      clear();
      host.removeEventListener('mousedown', start, true);
      host.removeEventListener('copy', copy, true);
      document.removeEventListener('mousemove', move);
      document.removeEventListener('mouseup', end);
      window.removeEventListener('blur', blur);
    },
  };
}
