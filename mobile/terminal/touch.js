// Keep display magnification in the WebView. Never resize the PTY on a pinch.
export function attachTouchControls({ term, viewport, post, cellGeometry, positionAt, state, pauseFollowing, zoom, knob }) {
  let selecting = false, start = null, moved = false, multiTouch = false;
  let longPress = null, drag = null, remainder = 0, scrollFrame = 0;
  let lastTouchAt = -Infinity;
  let pinch = null;
  // One-finger scrolling is applied once per frame, then continues as inertia.
  let scrollQueued = 0, flingFrame = 0, samples = [], lastPoint = { x: 0, y: 0 };
  const handles = ['start', 'end'].map(edge => {
    const handle = document.createElement('button');
    handle.className = 'selection-handle';
    handle.dataset.edge = edge;
    handle.setAttribute('aria-label', edge === 'start' ? '调整选择起点' : '调整选择终点');
    handle.hidden = true;
    viewport.append(handle);
    return handle;
  });
  const index = point => point.y * term.cols + point.x;
  const cancelHold = () => { clearTimeout(longPress); longPress = null; };
  function stopDrag() {
    cancelAnimationFrame(scrollFrame); scrollFrame = 0; drag = null;
  }
  function stopFling() {
    cancelAnimationFrame(flingFrame); flingFrame = 0;
    cancelAnimationFrame(scrollQueued); scrollQueued = 0;
  }
  // Applies whole cells accumulated in `remainder`. Returns false when a local
  // scroll hit an edge, so inertia can stop instead of spinning against it.
  function flushScroll() {
    const cellH = cellGeometry().height, lines = Math.trunc(remainder / cellH);
    if (!lines) return true;
    remainder -= lines * cellH;
    const remote = state().remoteHistory || term.buffer.active.type === 'alternate';
    // A remote screen taller than the phone must not swallow the gesture by
    // panning the local container: full-screen apps (Codex) and tmux history
    // scroll remotely. While magnified, panning the view still comes first.
    if (!remote || zoom?.scale() > 1.02) {
      const oldTop = viewport.scrollTop;
      viewport.scrollTop += lines * cellH;
      if (viewport.scrollTop !== oldTop) return true;
    }
    if (remote) {
      // Full-screen apps need wheel events over their transcript, not under the
      // finger: the composer at the bottom ignores them.
      post('scroll', { lines, ...positionAt(lastPoint.x, lastPoint.y),
        anchorRow: Math.max(1, Math.floor(term.rows / 3)), anchorColumn: Math.max(1, Math.floor(term.cols / 2)),
        mouse: term.modes.mouseTrackingMode });
      return true;
    }
    const oldRow = term.buffer.active.viewportY;
    term.scrollLines(lines);
    return term.buffer.active.viewportY !== oldRow;
  }
  function queueScroll() {
    if (scrollQueued) return;
    scrollQueued = requestAnimationFrame(() => { scrollQueued = 0; flushScroll(); });
  }
  function fling(velocity) {
    stopFling();
    let v = Math.max(-5, Math.min(5, velocity)), last = performance.now();
    const step = time => {
      const dt = Math.min(32, time - last); last = time;
      remainder += v * dt;
      v *= Math.exp(-dt / 360);
      if (!flushScroll() || Math.abs(v) < .02) { flingFrame = 0; return; }
      flingFrame = requestAnimationFrame(step);
    };
    flingFrame = requestAnimationFrame(step);
  }
  function pointAt(x, y) {
    const cell = cellGeometry();
    const row = term.buffer.active.viewportY + Math.max(0, Math.min(term.rows - 1, Math.floor((y - cell.rect.top) / cell.height)));
    const col = Math.max(0, Math.min(term.cols - 1, Math.floor((x - cell.rect.left) / cell.width)));
    return { x: col, y: Math.min(term.buffer.active.length - 1, row) };
  }
  function endpointPosition(point) {
    const cell = cellGeometry(), rect = viewport.getBoundingClientRect();
    return {
      x: cell.rect.left - rect.left + viewport.scrollLeft + point.x * cell.width,
      y: cell.rect.top - rect.top + viewport.scrollTop + (point.y - term.buffer.active.viewportY + 1) * cell.height,
      clientX: cell.rect.left + point.x * cell.width,
      clientY: cell.rect.top + (point.y - term.buffer.active.viewportY + 1) * cell.height,
      height: cell.height,
    };
  }
  function refresh() {
    const range = selecting && term.getSelectionPosition();
    for (const handle of handles) {
      const point = range && range[handle.dataset.edge];
      // Hidden endpoints stay available by scrolling back to their buffer row.
      handle.hidden = !point || point.y < term.buffer.active.viewportY || point.y >= term.buffer.active.viewportY + term.rows;
      if (handle.hidden) continue;
      const position = endpointPosition(point);
      const bounds = visibleBounds();
      handle.classList.toggle('inset-left', position.clientX < bounds.left + 16);
      handle.classList.toggle('inset-right', position.clientX > bounds.right - 16);
      handle.style.left = `${position.x}px`;
      handle.style.top = `${position.y}px`;
      handle.style.setProperty('--cell-height', `${position.height}px`);
    }
  }
  function setSelection(enabled, notify = false) {
    selecting = !!enabled;
    document.body.classList.toggle('select', selecting);
    document.body.classList.toggle('browse', !selecting);
    cancelHold();
    if (!selecting) { stopDrag(); term.clearSelection(); }
    if (notify) post('selectionMode', { enabled: selecting });
    refresh();
  }
  function selectBetween(a, b) {
    const low = Math.max(0, Math.min(a, b)), high = Math.min(term.buffer.active.length * term.cols, Math.max(a, b));
    term.select(low % term.cols, Math.floor(low / term.cols), Math.max(1, high - low));
    refresh();
  }
  function selectWord(x, y) {
    const point = pointAt(x, y), line = term.buffer.active.getLine(point.y);
    let first = point.x;
    if (line?.getCell(first)?.getWidth() === 0 && first > 0) first--;
    let last = first + Math.max(1, line?.getCell(first)?.getWidth() || 1);
    const isWord = cell => cell && (cell.getWidth() === 0 || /[\p{L}\p{N}_./~:@+-]/u.test(cell.getChars()));
    if (isWord(line?.getCell(first))) {
      while (first > 0 && isWord(line.getCell(first - 1))) first--;
      while (last < term.cols && isWord(line.getCell(last))) last++;
    }
    const anchor = point.y * term.cols + first;
    selectBetween(anchor, point.y * term.cols + last);
    return anchor;
  }
  function moveSelection() {
    if (!drag) return;
    const { x, y } = drag.latest;
    if (drag.handle) {
      const cell = cellGeometry();
      const row = Math.max(0, Math.min(term.rows - 1, Math.floor((y - drag.offsetY - cell.rect.top - cell.height / 2) / cell.height)));
      const col = Math.max(0, Math.min(term.cols, Math.round((x - drag.offsetX - cell.rect.left) / cell.width)));
      selectBetween(drag.anchor, (term.buffer.active.viewportY + row) * term.cols + col);
    } else {
      const point = pointAt(x, y), cell = term.buffer.active.getLine(point.y)?.getCell(point.x);
      const target = index(point);
      selectBetween(drag.anchor, target < drag.anchor ? target : target + Math.max(1, cell?.getWidth() || 1));
    }
  }
  function visibleBounds() {
    const rect = viewport.getBoundingClientRect(), visual = window.visualViewport;
    return {
      left: Math.max(rect.left, visual?.offsetLeft || 0),
      right: Math.min(rect.right, (visual?.offsetLeft || 0) + (visual?.width || innerWidth)),
      top: Math.max(rect.top, visual?.offsetTop || 0),
      bottom: Math.min(rect.bottom, (visual?.offsetTop || 0) + (visual?.height || innerHeight)),
    };
  }
  function autoScroll(time) {
    scrollFrame = 0;
    if (!drag) return;
    if (drag.moved && (!drag.lastScroll || time - drag.lastScroll >= 60)) {
      drag.lastScroll = time;
      const bounds = visibleBounds(), { x, y } = drag.latest;
      const dx = x < bounds.left + 24 ? -16 : x > bounds.right - 24 ? 16 : 0;
      const dy = y < bounds.top + 24 ? -1 : y > bounds.bottom - 28 ? 1 : 0;
      const oldLeft = viewport.scrollLeft, oldTop = viewport.scrollTop, oldRow = term.buffer.active.viewportY;
      viewport.scrollLeft += dx;
      if (dy) {
        viewport.scrollTop += dy * cellGeometry().height;
        // Remote history is intentionally not requested during a local selection:
        // replacing a tmux screen would replace the text being copied.
        if (viewport.scrollTop === oldTop && term.buffer.active.type !== 'alternate') term.scrollLines(dy);
      }
      if (oldLeft !== viewport.scrollLeft || oldTop !== viewport.scrollTop || oldRow !== term.buffer.active.viewportY) moveSelection();
    }
    scrollFrame = requestAnimationFrame(autoScroll);
  }
  function beginDrag(value) {
    stopDrag(); drag = value;
    scrollFrame = requestAnimationFrame(autoScroll);
  }
  // Two-finger pinch zooms the page content around the fingers and pans with
  // them; the terminal itself keeps its size, so nothing reflows.
  function pinchState(touches) {
    const a = touches[0], b = touches[1];
    return { distance: Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY) || 1, x: (a.clientX + b.clientX) / 2, y: (a.clientY + b.clientY) / 2 };
  }
  function beginPinch(touches) {
    stopFling();
    const now = pinchState(touches), rect = viewport.getBoundingClientRect(), style = getComputedStyle(viewport);
    const padLeft = parseFloat(style.paddingLeft) || 0, padTop = parseFloat(style.paddingTop) || 0, scale = zoom.scale();
    pinch = { distance: now.distance, scale, padLeft, padTop,
      anchorX: (viewport.scrollLeft + now.x - rect.left - padLeft) / scale,
      anchorY: (viewport.scrollTop + now.y - rect.top - padTop) / scale };
  }
  function movePinch(touches) {
    const now = pinchState(touches), rect = viewport.getBoundingClientRect();
    const scale = zoom.apply(Math.max(zoom.min(), Math.min(zoom.max, pinch.scale * now.distance / pinch.distance)));
    viewport.scrollLeft = pinch.anchorX * scale + pinch.padLeft - (now.x - rect.left);
    viewport.scrollTop = pinch.anchorY * scale + pinch.padTop - (now.y - rect.top);
    refresh();
  }

  // Floating knob: hold and drag up/down; it follows the finger and scrolls at a
  // speed that grows with the drag distance, then springs back on release.
  const KNOB_RANGE = 74, KNOB_DEAD = 7, KNOB_PX_PER_MS = 2.6;
  let knobTouch = null, knobOffset = 0, knobFrame = 0, knobLast = 0;
  function setKnob(offset) {
    knobOffset = offset;
    knob.style.setProperty('--knob-y', `${offset}px`);
    knob.classList.toggle('up', offset < -KNOB_DEAD);
    knob.classList.toggle('down', offset > KNOB_DEAD);
  }
  function knobTick(time) {
    knobFrame = 0;
    if (knobTouch === null) return;
    const dt = Math.min(40, time - knobLast); knobLast = time;
    const distance = Math.abs(knobOffset);
    if (distance > KNOB_DEAD) {
      const level = (distance - KNOB_DEAD) / (KNOB_RANGE - KNOB_DEAD);
      remainder += Math.sign(knobOffset) * Math.pow(level, 1.6) * KNOB_PX_PER_MS * dt;
      if (knobOffset < 0) pauseFollowing();
      const rect = viewport.getBoundingClientRect();
      lastPoint = { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
      queueScroll();
    }
    knobFrame = requestAnimationFrame(knobTick);
  }
  const knobFinger = event => [...event.changedTouches].find(touch => touch.identifier === knobTouch?.id);
  function knobStart(event) {
    event.preventDefault(); event.stopPropagation();
    if (knobTouch) return;
    const touch = event.changedTouches[0];
    stopFling(); remainder = 0;
    knobTouch = { id: touch.identifier, y: touch.clientY };
    knob.classList.add('active'); knobLast = performance.now();
    knobFrame = requestAnimationFrame(knobTick);
  }
  function knobMove(event) {
    const touch = knobFinger(event);
    if (!touch) return;
    event.preventDefault(); event.stopPropagation();
    setKnob(Math.max(-KNOB_RANGE, Math.min(KNOB_RANGE, touch.clientY - knobTouch.y)));
  }
  function knobEnd(event) {
    if (!knobFinger(event)) return;
    event.stopPropagation();
    cancelAnimationFrame(knobFrame); knobFrame = 0; knobTouch = null; remainder = 0;
    knob.classList.remove('active'); setKnob(0);
  }
  if (knob) {
    knob.addEventListener('touchstart', knobStart, { passive: false });
    knob.addEventListener('touchmove', knobMove, { passive: false });
    knob.addEventListener('touchend', knobEnd);
    knob.addEventListener('touchcancel', knobEnd);
  }
  function onStart(event) {
    lastTouchAt = performance.now();
    event.stopPropagation(); // xterm's built-in touch scroller must not compete.
    cancelHold(); stopFling(); samples = [];
    if (event.touches.length !== 1) {
      multiTouch = true; moved = true; start = null; stopDrag();
      if (event.touches.length === 2 && !selecting && zoom) beginPinch(event.touches);
      return;
    }
    if (multiTouch) return;
    const touch = event.touches[0];
    start = { x: touch.clientX, y: touch.clientY };
    moved = false; remainder = 0;
    const handle = event.target.closest?.('.selection-handle');
    const range = selecting && term.getSelectionPosition();
    if (handle && range) {
      const edge = handle.dataset.edge, position = endpointPosition(range[edge]);
      moved = true;
      beginDrag({ anchor: index(range[edge === 'start' ? 'end' : 'start']), handle: true,
        offsetX: start.x - position.clientX, offsetY: start.y - position.clientY, latest: start });
      return;
    }
    if (selecting) {
      pauseFollowing();
      beginDrag({ anchor: index(pointAt(start.x, start.y)), latest: start });
      return;
    }
    longPress = setTimeout(() => {
      if (!start || moved || multiTouch) return;
      setSelection(true, true); pauseFollowing(); moved = true;
      const anchor = selectWord(start.x, start.y);
      beginDrag({ anchor, latest: start });
    }, 500);
  }
  function onMove(event) {
    event.stopPropagation();
    // Let the WebView handle both fingers, including pinch and pan while zoomed.
    if (event.touches.length !== 1 || multiTouch) {
      cancelHold(); stopDrag(); start = null;
      if (pinch && event.touches.length === 2) { event.preventDefault(); movePinch(event.touches); }
      return;
    }
    if (!start) return;
    const touch = event.touches[0], x = touch.clientX, y = touch.clientY;
    const dx = start.x - x, dy = start.y - y;
    if (!moved && Math.hypot(dx, dy) < 8) return;
    cancelHold();
    if (!selecting && Math.abs(dx) > Math.abs(dy)) {
      moved = true; pauseFollowing(); start = { x, y };
      return; // Preserve native horizontal panning, including the zoomed viewport.
    }
    event.preventDefault(); moved = true;
    if (selecting && drag) {
      drag.moved = true; drag.latest = { x, y }; moveSelection(); return;
    }
    pauseFollowing();
    remainder += dy; lastPoint = { x, y };
    const now = performance.now();
    samples.push({ t: now, y }); while (samples.length > 1 && now - samples[0].t > 100) samples.shift();
    queueScroll();
    start = { x, y };
  }
  function onEnd(event) {
    cancelHold(); stopDrag();
    if (event.touches.length < 2) pinch = null;
    if (!event.touches.length) {
      const first = samples[0], last = samples[samples.length - 1];
      if (!selecting && moved && !multiTouch && first && last && last !== first && performance.now() - last.t < 80) {
        const velocity = (first.y - last.y) / (last.t - first.t);
        if (Math.abs(velocity) > .25) fling(velocity);
      }
      samples = [];
      if (start && !moved && !multiTouch && !selecting && state().controlled && state().applicationAlternate) {
        const touch = event.changedTouches[0];
        post('click', positionAt(touch.clientX, touch.clientY));
      }
      start = null; multiTouch = false;
    }
  }
  function onCancel() { cancelHold(); stopDrag(); stopFling(); samples = []; start = null; multiTouch = false; pinch = null; }
  const suppressMenu = event => {
    event.preventDefault(); event.stopPropagation();
    if (event.pointerType === 'touch' || event.sourceCapabilities?.firesTouchEvents || start || multiTouch || performance.now() - lastTouchAt < 1200) return;
    post('contextMenu');
  };
  viewport.addEventListener('touchstart', onStart, { passive: true, capture: true });
  viewport.addEventListener('touchmove', onMove, { passive: false, capture: true });
  viewport.addEventListener('touchend', onEnd);
  viewport.addEventListener('touchcancel', onCancel);
  viewport.addEventListener('contextmenu', suppressMenu, true);
  viewport.addEventListener('scroll', refresh, { passive: true });
  window.visualViewport?.addEventListener('resize', refresh);
  window.visualViewport?.addEventListener('scroll', refresh);
  const resizeObserver = new ResizeObserver(refresh);
  resizeObserver.observe(viewport);
  return {
    setSelection, refresh,
    dispose() {
      onCancel(); resizeObserver.disconnect();
      if (knob) { knob.removeEventListener('touchstart', knobStart); knob.removeEventListener('touchmove', knobMove); knob.removeEventListener('touchend', knobEnd); knob.removeEventListener('touchcancel', knobEnd); }
      viewport.removeEventListener('touchstart', onStart, true);
      viewport.removeEventListener('touchmove', onMove, true);
      viewport.removeEventListener('touchend', onEnd);
      viewport.removeEventListener('touchcancel', onCancel);
      viewport.removeEventListener('contextmenu', suppressMenu, true);
      viewport.removeEventListener('scroll', refresh);
      window.visualViewport?.removeEventListener('resize', refresh);
      window.visualViewport?.removeEventListener('scroll', refresh);
      handles.forEach(handle => handle.remove());
    },
  };
}
