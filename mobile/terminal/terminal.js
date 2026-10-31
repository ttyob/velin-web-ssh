import { VERSION, accepted, decodeBytes, encodeBytes } from './protocol.js';
let sessionID = '', generation = '', selectionMode = false, controlled = false;
let remoteHistory = false, applicationAlternate = false;
let pending = [], writing = false, epoch = 0, following = true;
const viewport = document.querySelector('#viewport');
const mount = document.querySelector('#terminal');
const term = new Terminal({
  cols: 120, rows: 30, fontSize: 13, lineHeight: 1.25,
  fontFamily: '"SFMono-Regular", Consolas, "Noto Sans Mono", monospace',
  scrollback: 10000, convertEol: false, cursorBlink: false,
  allowProposedApi: false, disableStdin: true,
  theme: { background: '#101113', foreground: '#dedee3', cursor: '#d4d8e1', selectionBackground: '#393d49' },
});
const fit = new FitAddon.FitAddon();
term.loadAddon(fit);
term.open(mount);
function post(type, payload = {}) {
  const message = { version: VERSION, type, sessionID, generation, ...payload };
  if (window.VelinTerminal) window.VelinTerminal.postMessage(JSON.stringify(message));
  else if (window.parent !== window) window.parent.postMessage(message, location.origin);
}
function modes() {
  post('modes', { bracketedPaste: !!term.modes.bracketedPasteMode, alternate: term.buffer.active.type === 'alternate', mouse: term.modes.mouseTrackingMode });
}
function drain() {
  if (writing || !pending.length) return;
  const item = pending.shift(), current = epoch;
  writing = true;
  term.write(decodeBytes(item.data), () => {
    if (current !== epoch) return;
    writing = false;
    if (following) term.scrollToBottom();
    modes();
    post('rendered', { id: item.id, bytes: item.bytes, offset: item.offset, connection: item.connection });
    drain();
  });
}
function geometry(message) {
  const cols = Math.max(2, Math.min(1000, Number(message.cols) || term.cols));
  const rows = Math.max(2, Math.min(500, Number(message.rows) || term.rows));
  mount.style.width = `${Math.max(viewport.clientWidth - 16, cols * (term.options.fontSize * .64))}px`;
  mount.style.height = `${rows * term.options.fontSize * term.options.lineHeight}px`;
  term.resize(cols, rows);
  const screen = term.element.querySelector('.xterm-screen');
  if (screen?.clientWidth) mount.style.width = `${Math.max(viewport.clientWidth - 16, screen.clientWidth)}px`;
  if (following) viewport.scrollTop = viewport.scrollHeight;
  post('geometry', { rows: term.rows, cols: term.cols });
}
function cellGeometry() {
  const rect = term.element.querySelector('.xterm-screen').getBoundingClientRect();
  return { rect, width: rect.width / term.cols, height: rect.height / term.rows };
}
function positionAt(x, y) {
  const cell = cellGeometry();
  return { row: Math.max(1, Math.min(term.rows, Math.floor((y-cell.rect.top)/cell.height)+1)), column: Math.max(1, Math.min(term.cols, Math.floor((x-cell.rect.left)/cell.width)+1)) };
}
function receive(message) {
  if (message?.type === 'configure' && message.version === VERSION) {
    sessionID = message.sessionID; generation = message.generation;
    document.body.className = 'browse';
    geometry(message); post('configured'); return;
  }
  if (!accepted(message, sessionID, generation)) return;
  switch (message.type) {
    case 'reset':
      epoch++; pending = []; writing = true; following = true;
      { const current = epoch;
        term.write('', () => {
          if (current !== epoch) return;
          term.reset(); writing = false; drain();
        });
      } break;
    case 'write': pending.push(message); drain(); break;
    case 'setViewport': geometry(message); break;
    case 'fit':
      mount.style.width = '100%'; mount.style.height = '100%'; fit.fit(); post('fit', { rows: term.rows, cols: term.cols }); break;
    case 'control': controlled = message.enabled; break;
    case 'application': remoteHistory = message.remoteHistory; applicationAlternate = message.alternate; break;
    case 'font': term.options.fontSize = Math.max(10, Math.min(24, message.size)); geometry({}); break;
    case 'theme':
      if (message.colors && ['background','foreground','cursor','selectionBackground'].every(key => /^#[0-9a-f]{6}$/i.test(message.colors[key]))) {
        const {background, foreground, cursor, selectionBackground} = message.colors;
        term.options.theme = {background, foreground, cursor, selectionBackground};
        document.documentElement.style.backgroundColor = background;
      } break;
    case 'selection':
      selectionMode = message.enabled; document.body.className = selectionMode ? 'select' : 'browse';
      if (!selectionMode) term.clearSelection(); break;
    case 'bottom': following = true; term.scrollToBottom(); viewport.scrollTop = viewport.scrollHeight; post('following', { enabled: true }); break;
    case 'copy': post('selection', { text: term.getSelection() }); break;
    case 'dispose': epoch++; pending = []; term.dispose(); break;
  }
}
window.Velin = { receive };
new ResizeObserver(() => { if (following) viewport.scrollTop = viewport.scrollHeight; }).observe(viewport);
window.addEventListener('message', event => {
  if (event.source === window.parent && event.origin === location.origin) receive(event.data);
});
// Native composer is the only keyboard input source in this first version.
term.textarea?.setAttribute('readonly', 'readonly');
term.onData(data => { if (controlled) post('input', { data: encodeBytes(new TextEncoder().encode(data)) }); });
term.onSelectionChange(() => { following = false; post('selection', { text: term.getSelection() }); });
term.onBell(() => post('bell'));
term.onScroll(() => {
  if (term.buffer.active.viewportY < term.buffer.active.baseY) following = false;
  post('following', { enabled: following });
});
let start = null, moved = false, remainder = 0;
viewport.addEventListener('touchstart', event => {
  if (event.touches.length !== 1) return;
  start = { x: event.touches[0].clientX, y: event.touches[0].clientY };
  moved = false; remainder = 0;
}, { passive: true });
viewport.addEventListener('touchmove', event => {
  if (!start || event.touches.length !== 1) return;
  const touch = event.touches[0], dy = start.y - touch.clientY, dx = start.x - touch.clientX;
  if (selectionMode) {
    event.preventDefault(); moved = true;
    const cell = cellGeometry(), rect = cell.rect;
    const cellW = cell.width, cellH = cell.height;
    const firstCol = Math.max(0, Math.min(term.cols - 1, Math.floor((start.x - rect.left) / cellW)));
    const firstRow = term.buffer.active.viewportY + Math.max(0, Math.floor((start.y - rect.top) / cellH));
    const lastCol = Math.max(0, Math.min(term.cols - 1, Math.floor((touch.clientX - rect.left) / cellW)));
    const lastRow = term.buffer.active.viewportY + Math.max(0, Math.floor((touch.clientY - rect.top) / cellH));
    const a = firstRow * term.cols + firstCol, b = lastRow * term.cols + lastCol;
    const low = Math.min(a, b), high = Math.max(a, b);
    term.select(low % term.cols, Math.floor(low / term.cols), high - low + 1);
    return;
  }
  if (Math.abs(dy) < 5 && !moved || Math.abs(dx) > Math.abs(dy)) return;
  event.preventDefault(); moved = true; following = false;
  remainder += dy;
  const cellH = cellGeometry().height;
  const lines = Math.trunc(remainder / cellH);
  if (lines) {
    remainder -= lines * cellH;
    const oldTop = viewport.scrollTop;
    viewport.scrollTop += lines * cellH;
    if (viewport.scrollTop !== oldTop) { start = { x: touch.clientX, y: touch.clientY }; return; }
    if (remoteHistory || term.buffer.active.type === 'alternate') post('scroll', { lines, ...positionAt(touch.clientX,touch.clientY), mouse: term.modes.mouseTrackingMode });
    else term.scrollLines(lines);
  }
  start = { x: touch.clientX, y: touch.clientY };
  post('following', { enabled: false });
}, { passive: false });
viewport.addEventListener('touchend', event => {
  if (start && !moved && !selectionMode && controlled && applicationAlternate) {
    const touch = event.changedTouches[0];
    post('click', positionAt(touch.clientX,touch.clientY));
  }
  start = null;
});
post('ready');
