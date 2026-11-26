// Only a session identifier is carried through login. External URLs and
// authentication tokens are never accepted as navigation destinations.
export function sessionLinkQuery(value: unknown): Record<string, string> {
  return typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value)
    ? { session: value }
    : {}
}
