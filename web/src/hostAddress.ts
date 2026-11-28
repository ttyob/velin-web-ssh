export function defaultPortForProtocol(protocol?: string): number {
  const normalized = (protocol || "ssh").toLowerCase();
  if (normalized === "rdp") return 3389;
  if (normalized === "vnc") return 5900;
  return 22;
}

export function maskHostAddress(address: string): string {
  const value = address.trim();
  if (!value) return "";
  return "••••••";
}

export function formatHostEndpoint(
  host: { protocol?: string; address: string; port?: number },
  options?: { masked?: boolean; includeDefaultPort?: boolean },
): string {
  if (options?.masked) return maskHostAddress(host.address);
  const defaultPort = defaultPortForProtocol(host.protocol);
  const port = host.port || defaultPort;
  const showPort = Boolean(options?.includeDefaultPort || port !== defaultPort);
  return showPort ? `${host.address}:${port}` : host.address;
}

export function isHttpsTargetURL(targetURL: string): boolean {
  return /^https:\/\//i.test(targetURL.trim());
}

export function formatWebServiceTarget(targetURL: string): string {
  const raw = targetURL.trim();
  if (!raw) return "";
  try {
    const parsed = new URL(raw);
    const hostname = parsed.hostname;
    const defaultPort = parsed.protocol === "https:" ? "443" : "80";
    const port = parsed.port && parsed.port !== defaultPort ? parsed.port : "";
    const path = parsed.pathname === "/" ? "" : parsed.pathname.replace(/\/+$/, "");
    const suffix = `${path}${parsed.search}`;
    const isLoopback =
      hostname === "127.0.0.1" || hostname === "localhost" || hostname === "[::1]" || hostname === "::1";
    if (isLoopback && port) {
      return `:${port}${suffix}`;
    }
    return `${hostname}${port ? `:${port}` : ""}${suffix}`;
  } catch {
    return raw.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
  }
}
