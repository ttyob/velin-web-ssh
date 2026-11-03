// Native tools keep authenticated downloads in the app and use Android's save picker.
declare global {
  interface Window { VelinTools?: { postMessage(message: string): void } }
}

function nativePost(type: string, payload: Record<string, unknown>): boolean {
  if (location.pathname !== "/mobile-tools" || !window.VelinTools) return false;
  window.VelinTools.postMessage(JSON.stringify({ type, ...payload }));
  return true;
}

export function downloadToolFile(path: string) {
  if (nativePost("download", { path })) return;
  const link = document.createElement("a");
  link.href = path;
  link.click();
}

export async function downloadToolBlob(blob: Blob, name: string) {
  if (location.pathname === "/mobile-tools" && window.VelinTools) {
    if (blob.size > 16 * 1024 * 1024) throw new Error("日志过大，请先缩小日志范围");
    const data = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onerror = () => reject(new Error("无法读取下载内容"));
      reader.onload = () => resolve(String(reader.result).split(",")[1]);
      reader.readAsDataURL(blob);
    });
    nativePost("downloadData", { name, data });
    return;
  }
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}
