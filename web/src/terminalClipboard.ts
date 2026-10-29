// Let the browser dispatch a paste event without xterm sending Ctrl+V (SYN)
// to the remote application. Do not preventDefault: that would block pasting.
export function allowTerminalKeyEvent(event: KeyboardEvent): boolean {
  const paste = !event.altKey && (
    ((event.ctrlKey || event.metaKey) && (event.code === "KeyV" || event.key.toLowerCase() === "v")) ||
    (event.shiftKey && !event.ctrlKey && !event.metaKey && event.key === "Insert")
  );
  return !paste;
}

export function terminalClipboardFiles(data: DataTransfer | null): File[] {
  if (!data) return [];
  const files = Array.from(data.files);
  if (files.length) return files;
  return Array.from(data.items)
    .filter(item => item.kind === "file")
    .map(item => item.getAsFile())
    .filter((file): file is File => file !== null);
}

export function terminalUploadName(file: File): string {
  // Unique names avoid overwriting a project file or resuming an unrelated
  // upload. Strip path separators and terminal control characters.
  const characters = Array.from(file.name.replace(/[\x00-\x1f\x7f-\x9f/\\]/g, "_").slice(-180));
  while (new TextEncoder().encode(characters.join("")).length > 160) characters.shift();
  const name = characters.join("") || "file";
  const unique = globalThis.crypto?.randomUUID?.() || `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  return `clipboard-${unique}-${name}`;
}

export function terminalFileReference(path: string): string {
  return /^[a-zA-Z0-9_@%+=:,./-]+$/.test(path) ? path : `'${path.replace(/'/g, `'\\''`)}'`;
}
