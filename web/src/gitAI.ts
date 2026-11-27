export const gitAIContentMaxBytes = 120 * 1024;

export function truncateGitAIContent(value: string, maxBytes = gitAIContentMaxBytes) {
  const encoder = new TextEncoder();
  const bytes = encoder.encode(value);
  if (bytes.length <= maxBytes) return value;

  const suffix = "\n\n[差异过长，已截断]";
  const suffixBytes = encoder.encode(suffix);
  let end = Math.max(0, maxBytes - suffixBytes.length);
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
  return new TextDecoder().decode(bytes.subarray(0, end)).trimEnd() + suffix;
}
