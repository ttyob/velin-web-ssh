import { VERSION, accepted, decodeBytes, encodeBytes } from './protocol.js';
import { attachTouchControls } from './touch.js';
let sessionID = '', generation = '', controlled = false;
let remoteHistory = false, applicationAlternate = false;
let pending = [], writing = false, epoch = 0, following = true;
// History replay can be megabytes: render it hidden, in large batches, and show
// the finished screen once instead of scrolling through every chunk.
let restoring = false, revealTimer = 0;
const BATCH_BYTES = 256 * 1024, REVEAL_FALLBACK_MS = 6000;
const restoringNote = document.createElement('div');
restoringNote.id = 'restoring'; restoringNote.textContent = '正在恢复历史…'; restoringNote.hidden = true;
document.body.append(restoringNote);
function setRestoring(on) {
  restoring = on;
  document.body.classList.toggle('restoring', on);
  restoringNote.hidden = !on;
  clearTimeout(revealTimer);
  if (on) revealTimer = setTimeout(() => { if (restoring) finishRestoring(); }, REVEAL_FALLBACK_MS);
}
function finishRestoring() {
  setRestoring(false);
  if (following) { term.scrollToBottom(); viewport.scrollTop = viewport.scrollHeight; }
  post('following', { enabled: following });
}
const viewport = document.querySelector('#viewport');
const mount = document.querySelector('#terminal');
const zoomer = document.querySelector('#zoomer');
const knob = document.querySelector('#scroll-knob');
const MAX_ZOOM = 4;
let zoomScale = 1;
const term = new Terminal({
  cols: 120, rows: 30, fontSize: 13, lineHeight: 1.25,
  fontFamily: '"SFMono-Regular", Consolas, "Noto Sans Mono", monospace',
  scrollback: 10000, convertEol: false, cursorBlink: false,
  allowProposedApi: false, disableStdin: true,
  theme: { background: '#101113', foreground: '#dedee3', cursor: '#d4d8e1', selectionBackground: '#393d49', selectionForeground: '#dedee3' },
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
  // Merge queued chunks into one parser pass; every chunk is still acknowledged.
  const batch = [];
  let size = 0;
  while (pending.length && (!batch.length || size + (pending[0].bytes || 0) <= BATCH_BYTES)) {
    const item = pending.shift();
    batch.push(item); size += item.bytes || 0;
    if (item.id === 'replay-final') break;
  }
  const chunks = batch.map(item => decodeBytes(item.data));
  const merged = chunks.length === 1 ? chunks[0] : new Uint8Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0));
  if (chunks.length > 1) { let at = 0; for (const chunk of chunks) { merged.set(chunk, at); at += chunk.length; } }
  const current = epoch;
  writing = true;
  term.write(merged, () => {
    if (current !== epoch) return;
    writing = false;
    const done = restoring && batch.some(item => item.id === 'replay-final' || item.id === 'demo');
    if (done) finishRestoring();
    else if (following && !restoring) term.scrollToBottom();
    modes();
    for (const item of batch) post('rendered', { id: item.id, bytes: item.bytes, offset: item.offset, connection: item.connection });
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
  applyZoom();
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
// Page-level zoom: scale the terminal as a whole (never the PTY size). The wrapper
// takes the scaled size so the viewport scrolls all the way to every edge.
function fitScale() {
  const w = mount.offsetWidth, h = mount.offsetHeight;
  if (!w || !h) return 1;
  return Math.max(.1, Math.min(1, (viewport.clientWidth - 12) / w, (viewport.clientHeight - 12) / h));
}
function applyZoom(scale = zoomScale) {
  zoomScale = Math.max(fitScale(), Math.min(MAX_ZOOM, scale));
  const zoomed = Math.abs(zoomScale - 1) > .001;
  mount.style.transform = zoomed ? `scale(${zoomScale})` : '';
  zoomer.style.width = zoomed ? `${mount.offsetWidth * zoomScale}px` : '';
  zoomer.style.height = zoomed ? `${mount.offsetHeight * zoomScale}px` : '';
  return zoomScale;
}
const touchControls = attachTouchControls({
  term, viewport, post, cellGeometry, positionAt, knob,
  zoom: { scale: () => zoomScale, min: fitScale, max: MAX_ZOOM, apply: applyZoom },
  state: () => ({ controlled, remoteHistory, applicationAlternate }),
  pauseFollowing: () => { following = false; post('following', { enabled: false }); },
});
function receive(message) {
  if (message?.type === 'configure' && message.version === VERSION) {
    sessionID = message.sessionID; generation = message.generation;
    touchControls.setSelection(false);
    geometry(message); post('configured'); return;
  }
  if (!accepted(message, sessionID, generation)) return;
  switch (message.type) {
    case 'reset':
      epoch++; pending = []; writing = true; following = true; touchControls.setSelection(false); setRestoring(true);
      { const current = epoch;
        term.write('', () => {
          if (current !== epoch) return;
          term.reset(); writing = false; drain();
        });
      } break;
    case 'write': pending.push(message); drain(); break;
    case 'setViewport': geometry(message); break;
    case 'fit':
      mount.style.width = '100%'; mount.style.height = '100%'; fit.fit(); applyZoom(); post('fit', { rows: term.rows, cols: term.cols }); break;
    case 'control': controlled = message.enabled; break;
    case 'application': remoteHistory = message.remoteHistory; applicationAlternate = message.alternate; break;
    case 'font': term.options.fontSize = Math.max(10, Math.min(24, message.size)); geometry({}); break;
    case 'theme':
      if (message.colors && ['background','foreground','cursor','selectionBackground'].every(key => /^#[0-9a-f]{6}$/i.test(message.colors[key]))) {
        const {background, foreground, cursor, selectionBackground} = message.colors;
        term.options.theme = {background, foreground, cursor, selectionBackground, selectionForeground: foreground};
        document.documentElement.style.backgroundColor = background;
        document.documentElement.style.setProperty('--knob', cursor);
      } break;
    case 'selection':
      touchControls.setSelection(message.enabled); break;
    case 'knob': knob.hidden = message.enabled === false; break;
    case 'zoomReset': applyZoom(1); touchControls.refresh(); break;
    case 'selectAll':
      touchControls.setSelection(true, true); following = false; term.selectAll(); touchControls.refresh(); break;
    case 'clear':
      touchControls.setSelection(false, true); term.clear(); following = true; post('following', { enabled: true }); break;
    case 'bottom': following = true; term.scrollToBottom(); viewport.scrollTop = viewport.scrollHeight; post('following', { enabled: true }); break;
    case 'copy': post('selection', { text: term.getSelection() }); break;
    case 'dispose': epoch++; pending = []; clearTimeout(revealTimer); touchControls.dispose(); term.dispose(); break;
  }
}
window.Velin = { receive };
new ResizeObserver(() => { applyZoom(); if (following) viewport.scrollTop = viewport.scrollHeight; }).observe(viewport);
window.addEventListener('message', event => {
  if (event.source === window.parent && event.origin === location.origin) receive(event.data);
});
// Native composer is the only keyboard input source in this first version.
term.textarea?.setAttribute('readonly', 'readonly');
term.onData(data => { if (controlled) post('input', { data: encodeBytes(new TextEncoder().encode(data)) }); });
term.onSelectionChange(() => { following = false; touchControls.refresh(); post('selection', { text: term.getSelection() }); });
term.onRender(() => touchControls.refresh());
term.onBell(() => post('bell'));
term.onScroll(() => {
  touchControls.refresh();
  if (restoring) return;
  if (term.buffer.active.viewportY < term.buffer.active.baseY) following = false;
  post('following', { enabled: following });
});
post('ready');
