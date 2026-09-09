export const terminalWheelPixelsPerLine = 64;
export const terminalWheelMaxLines = 3;

export type TerminalWheelDelta = {
  lines: number;
  remainder: number;
};

export function normalizeTerminalWheelDelta(
  deltaY: number,
  deltaMode: number,
  remainder = 0,
): TerminalWheelDelta {
  if (!Number.isFinite(deltaY) || deltaY === 0)
    return { lines: 0, remainder: 0 };

  const direction = Math.sign(deltaY);
  const carried = Math.sign(remainder) === direction ? remainder : 0;
  let deltaLines: number;
  if (deltaMode === 1) deltaLines = deltaY;
  else if (deltaMode === 2) deltaLines = direction * terminalWheelMaxLines;
  else deltaLines = deltaY / terminalWheelPixelsPerLine;

  const accumulated = carried + deltaLines;
  const wholeLines = Math.trunc(accumulated) || 0;
  const lines = Math.max(
    -terminalWheelMaxLines,
    Math.min(terminalWheelMaxLines, wholeLines),
  );
  return {
    lines,
    remainder: Math.abs(wholeLines) > terminalWheelMaxLines
      ? 0
      : accumulated - wholeLines,
  };
}
