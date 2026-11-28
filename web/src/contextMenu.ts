export function contextMenuPosition(point: { x: number; y: number }, element: HTMLElement) {
  const rect = element.getBoundingClientRect();
  const viewport = window.visualViewport;
  const left = viewport?.offsetLeft || 0, top = viewport?.offsetTop || 0;
  const width = viewport?.width || window.innerWidth, height = viewport?.height || window.innerHeight;
  return { x: Math.max(left + 6, Math.min(point.x, left + width - (element.offsetWidth || rect.width) - 6)), y: Math.max(top + 6, Math.min(point.y, top + height - (element.offsetHeight || rect.height) - 6)) };
}
export function contextMenuKey(event: KeyboardEvent, element: HTMLElement | undefined, close: () => void) {
  event.stopPropagation();
  if (event.key === 'Escape' || event.key === 'Tab') { event.preventDefault(); close(); return; }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const buttons = [...(element?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') || [])];
  if (!buttons.length) return;
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
  const target = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowUp' ? -1 : 1) + buttons.length) % buttons.length;
  buttons[target]?.focus({ preventScroll: true });
  buttons[target]?.scrollIntoView({ block: 'nearest' });
}
