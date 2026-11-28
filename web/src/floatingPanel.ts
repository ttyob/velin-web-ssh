export interface FloatingPanelBounds { width: number; height: number; right: number; bottom: number }
export function clampPanel(bounds: FloatingPanelBounds, width: number, height: number): FloatingPanelBounds {
  const margin = 8, maxWidth = Math.max(1, width - margin * 2), maxHeight = Math.max(1, height - margin * 2);
  const panelWidth = Math.max(Math.min(320, maxWidth), Math.min(maxWidth, bounds.width));
  const panelHeight = Math.max(Math.min(160, maxHeight), Math.min(maxHeight, bounds.height));
  return { width: panelWidth, height: panelHeight, right: Math.max(margin, Math.min(width - panelWidth - margin, bounds.right)), bottom: Math.max(margin, Math.min(height - panelHeight - margin, bounds.bottom)) };
}
export function transformPanel(bounds: FloatingPanelBounds, width: number, height: number, dx: number, dy: number, edge = 'move'): FloatingPanelBounds {
  if (edge === 'move') return clampPanel({ ...bounds, right: bounds.right - dx, bottom: bounds.bottom - dy }, width, height);
  const margin = 8, minWidth = Math.min(320, width - 16), minHeight = Math.min(160, height - 16);
  let left = width - bounds.right - bounds.width, right = width - bounds.right;
  let top = height - bounds.bottom - bounds.height, bottom = height - bounds.bottom;
  if (edge.includes('w')) left = Math.max(margin, Math.min(right - minWidth, left + dx));
  if (edge.includes('e')) right = Math.max(left + minWidth, Math.min(width - margin, right + dx));
  if (edge.includes('n')) top = Math.max(margin, Math.min(bottom - minHeight, top + dy));
  if (edge.includes('s')) bottom = Math.max(top + minHeight, Math.min(height - margin, bottom + dy));
  return clampPanel({ width: right - left, height: bottom - top, right: width - right, bottom: height - bottom }, width, height);
}
