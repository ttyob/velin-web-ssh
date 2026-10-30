import { allowTerminalKeyEvent } from "./terminalClipboard";

// Keep the newline inside a paste event so line editors insert it as text
// instead of interpreting it as the Enter key that submits the current input.
export const terminalNewlineInput = "\x1b[200~\r\x1b[201~";

export function handleTerminalKeyEvent(
  event: KeyboardEvent,
  insertNewline: () => void,
): boolean {
  if (
    event.key === "Enter" && event.ctrlKey && !event.altKey && !event.metaKey
  ) {
    if (event.isComposing || event.keyCode === 229) return false;
    event.preventDefault();
    event.stopPropagation();
    if (event.type === "keydown") insertNewline();
    return false;
  }
  return allowTerminalKeyEvent(event);
}
