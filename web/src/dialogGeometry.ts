import { nextTick, onBeforeUnmount, watch } from 'vue';
export function useDialogGeometry(open: () => boolean, className: () => string) {
  let element: HTMLElement | undefined;
  let stopDrag: (() => void) | undefined;
  function setSize(width: number, height: number) {
    element?.style.setProperty('width', `${Math.max(300, Math.min(window.innerWidth - 24, width))}px`, 'important');
    element?.style.setProperty('height', `${Math.max(240, Math.min(window.innerHeight * .9, height))}px`, 'important');
  }
  function persist() {
    if (!element || !open()) return;
    const rect = element.getBoundingClientRect();
    try { localStorage.setItem(`velin-dialog-size:${className()}`, JSON.stringify({ width: rect.width, height: rect.height })); } catch { /* Optional preference. */ }
  }
  function begin(event: PointerEvent) {
    if (event.button !== 0 || !element) return;
    event.preventDefault(); event.stopPropagation();
    stopDrag?.();
    const rect = element.getBoundingClientRect(), startX = event.clientX, startY = event.clientY;
    const handle = event.currentTarget as HTMLElement; handle.setPointerCapture(event.pointerId);
    const move = (next: PointerEvent) => setSize(rect.width + next.clientX - startX, rect.height + next.clientY - startY);
    const stop = () => { handle.removeEventListener('pointermove', move); handle.removeEventListener('pointerup', stop); handle.removeEventListener('pointercancel', stop); handle.removeEventListener('lostpointercapture', stop); stopDrag = undefined; persist(); };
    handle.addEventListener('pointermove', move); handle.addEventListener('pointerup', stop); handle.addEventListener('pointercancel', stop); handle.addEventListener('lostpointercapture', stop); stopDrag = stop;
  }
  function apply() {
    const name = className();
    if (!name || !open()) return;
    element = [...document.querySelectorAll<HTMLElement>(`.el-dialog.${name}`)].at(-1);
    if (!element) return;
    try {
      const size = JSON.parse(localStorage.getItem(`velin-dialog-size:${name}`) || 'null');
      if (size && Number.isFinite(size.width) && Number.isFinite(size.height)) setSize(size.width, size.height);
    } catch { /* Optional preference. */ }
    if (!element.querySelector('.code-dialog-resizer')) {
      const handle = document.createElement('button'); handle.type = 'button'; handle.className = 'code-dialog-resizer'; handle.setAttribute('aria-label', '调整弹窗大小'); handle.title = '拖动调整大小；方向键微调';
      handle.addEventListener('pointerdown', begin);
      handle.addEventListener('keydown', event => {
        if (!element || !['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) return;
        event.preventDefault(); event.stopPropagation(); const rect = element.getBoundingClientRect();
        setSize(rect.width + (event.key === 'ArrowLeft' ? -20 : event.key === 'ArrowRight' ? 20 : 0), rect.height + (event.key === 'ArrowUp' ? -20 : event.key === 'ArrowDown' ? 20 : 0)); persist();
      });
      element.append(handle);
    }
  }
  watch(open, async value => { stopDrag?.(); if (value) { await nextTick(); apply(); } else element = undefined; }, { immediate: true });
  onBeforeUnmount(() => stopDrag?.());
  return { apply };
}
