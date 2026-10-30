// Keep Windows text in the browser's native renderer. Rasterizing glyphs to a
// canvas atlas can lose ClearType smoothing, especially at fractional DPI.
export const nativeTerminalText = typeof navigator !== "undefined" &&
  /Win32|Win64|Windows/i.test(`${navigator.platform} ${navigator.userAgent}`);

export const terminalFontFamily = nativeTerminalText
  ? 'Consolas, "Cascadia Mono", "Cascadia Code", "Microsoft YaHei", monospace'
  : '"JetBrains Mono", "Cascadia Mono", "Cascadia Code", Menlo, Consolas, "Sarasa Mono SC", "Noto Sans Mono CJK SC", "Source Han Mono SC", "Microsoft YaHei", monospace';
