export type EditorIndent = 'tab' | '2' | '4' | '8';
export function detectIndent(content: string): EditorIndent {
  const lines = content.split(/\r?\n/).filter(line => /^\s+\S/.test(line)).slice(0, 200);
  if (lines.filter(line => line.startsWith('\t')).length > lines.length / 2) return 'tab';
  const widths = lines.map(line => /^ +(?=\S)/.exec(line)?.[0].length || 0).filter(width => width > 0);
  return widths.some(width => width % 4 !== 0) ? '2' : widths.length && Math.min(...widths) >= 8 ? '8' : '4';
}
export function withLineEnding(content: string, ending: 'LF' | 'CRLF') { return content.replace(/\r\n|\r|\n/g, ending === 'CRLF' ? '\r\n' : '\n'); }
