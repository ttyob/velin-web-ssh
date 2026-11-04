import type { Host } from "./types";

type NativeRDPBridge = {
  LaunchRDP: (
    address: string,
    port: number,
    username: string,
    domain: string,
    password: string,
  ) => Promise<void>;
  GetClipboardText?: () => Promise<string>;
  SetClipboardText?: (text: string) => Promise<void>;
};

type WailsWindow = typeof globalThis & {
  go?: { main?: { App?: Partial<NativeRDPBridge>; desktopApp?: Partial<NativeRDPBridge> } };
};

function nativeRDPBridge() {
  const main = (globalThis as WailsWindow).go?.main;
  return main?.desktopApp || main?.App;
}

export function nativeRDPAvailable() {
  return typeof nativeRDPBridge()?.LaunchRDP === "function";
}

export async function getNativeClipboardText() {
  const getClipboardText = nativeRDPBridge()?.GetClipboardText;
  return typeof getClipboardText === "function"
    ? getClipboardText()
    : undefined;
}

export async function setNativeClipboardText(text: string) {
  const setClipboardText = nativeRDPBridge()?.SetClipboardText;
  if (typeof setClipboardText !== "function") return false;
  await setClipboardText(text);
  return true;
}

function cleanRDPValue(value: string) {
  return value.replace(/[\r\n]/g, " ").trim();
}

export function rdpTarget(host: Pick<Host, "address" | "port">) {
  const address = cleanRDPValue(host.address);
  return address.includes(":") && !address.startsWith("[")
    ? `[${address}]:${host.port}`
    : `${address}:${host.port}`;
}

export function buildRDPFile(host: Host) {
  const lines = [
    "screen mode id:i:2",
    `use multimon:i:${host.rdpMultiMonitor ? 1 : 0}`,
    "session bpp:i:32",
    `full address:s:${rdpTarget(host)}`,
    `username:s:${cleanRDPValue(
      host.username,
    )}`,
    `domain:s:${cleanRDPValue(host.desktopDomain)}`,
    "prompt for credentials:i:1",
    "authentication level:i:2",
    "enablecredsspsupport:i:1",
    `redirectclipboard:i:${host.rdpClipboard ? 1 : 0}`,
    `redirectprinters:i:${host.rdpPrinting ? 1 : 0}`,
    "redirectcomports:i:0",
    "redirectsmartcards:i:0",
    `drivestoredirect:s:${host.rdpDrive ? "*" : ""}`,
    `audiomode:i:${host.rdpAudio ? 0 : 2}`,
    "autoreconnection enabled:i:1",
  ];
  return `${lines.join("\r\n")}\r\n`;
}

function rdpFileName(name: string) {
  const safe = name
    .replace(/[<>:"/\\|?*\u0000-\u001f]/g, "-")
    .trim()
    .replace(/[. ]+$/, "");
  return `${safe || "remote-desktop"}.rdp`;
}

function downloadRDPFile(host: Host) {
  const blob = new Blob([buildRDPFile(host)], {
    type: "application/rdp",
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = rdpFileName(host.name);
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export async function launchNativeRDP(host: Host, secret = "") {
  const bridge = nativeRDPBridge();
  if (typeof bridge?.LaunchRDP === "function") {
    await bridge.LaunchRDP(
      cleanRDPValue(host.address),
      host.port,
      cleanRDPValue(host.username),
      cleanRDPValue(host.desktopDomain),
      secret,
    );
    return "launched" as const;
  }
  downloadRDPFile(host);
  return "downloaded" as const;
}
