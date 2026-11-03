import type { Terminal } from '@xterm/xterm';

export type SelectionPoint = { row: number; column: number };
export type SelectionLine = { cells: string[]; wrapped: boolean };
export type SelectionScreen = SelectionLine[];

export function selectionScreen(terminal: Terminal, tmuxHistory = false): SelectionScreen {
  const buffer = terminal.buffer.active;
  return Array.from({ length: terminal.rows }, (_, row) => {
    const line = buffer.getLine(buffer.viewportY + row);
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
      wrapped: line?.isWrapped || false,
      cells,
    };
  });
}

function lineText(line: SelectionLine) { return line.cells.join('').trimEnd(); }
function before(a: SelectionPoint, b: SelectionPoint) {
  return a.row < b.row || a.row === b.row && a.column <= b.column;
}

// Alternate buffers are redrawn in place. Retain rows crossed by a drag rather
// than reading the last screen again at mouseup. Only overlapping scroll frames
// are joined; arbitrary TUI redraws must never become conversation history.
export class ScrollingSelection {
  private screen: SelectionScreen;
  private lines = new Map<number, SelectionLine>();
  private top = 0;
  private header = 0;
  private footer = 0;
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
    if (!shift) return false;
    // Fixed CLI headers and composers stay outside the scrolling transcript.
    const header = Math.max(this.header, first);
    const footer = Math.max(this.footer, old.length - last);
    for (let row = this.header; row < header; row++) this.lines.delete(this.top + row);
    for (let row = old.length - footer; row < old.length - this.footer; row++) this.lines.delete(this.top + row);
    this.header = header;
    this.footer = footer;
    this.start.row = Math.max(header, Math.min(old.length - footer - 1, this.start.row));
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
      const line = this.lines.get(row);
      if (!line) return ''; // Never silently copy a range containing unseen rows.
      let from = row === start.row ? start.column : 0;
      const to = row === end.row ? end.column : this.columns;
      if (from > 0 && line.cells[from] === '') from--;
      const text = line.cells.slice(from, to).join('').trimEnd().replace(/\u00a0/g, ' ');
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
  canScroll: () => boolean;
  scroll: (lines: number) => void;
  copy: (text: string) => void;
  visible: () => boolean;
  tmuxHistory: () => boolean;
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
    options.scroll(lines);
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
        terminal.modes.mouseTrackingMode !== 'none' && !event.shiftKey || !options.canScroll()) return;
    const box = rect();
    if (event.clientX < box.left || event.clientX >= box.right || event.clientY < box.top || event.clientY >= box.bottom) return;
    origin = pointer = { x: event.clientX, y: event.clientY };
    model = new ScrollingSelection(selectionScreen(terminal, options.tmuxHistory()), point());
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
    wheel(lines: number) {
      if (!down || !model) {
        if (active) { clear(); terminal.clearSelection(); }
        return false;
      }
      scroll(lines, true);
      return true;
    },
    changed() {
      if (!model || !active || !pending) return;
      if (!options.visible() || terminal.rows !== model.rows || terminal.cols !== model.columns) { clear(); return; }
      if (!model.scroll(selectionScreen(terminal, options.tmuxHistory()), pending)) return;
      pending = 0;
      clearTimeout(settleTimer);
      repaint();
      if (queued) {
        const lines = Math.max(-3, Math.min(3, queued));
        queued -= lines;
        scroll(lines);
      }
      finish();
    },
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
