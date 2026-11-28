import { api, json } from "./api";
import type { DiffFileInfo } from "./codeDiff";

export interface RemoteFileEntry {
  name: string;
  path: string;
  size: number;
  mode: string;
  directory: boolean;
  symlink: boolean;
  modifiedAt: string;
}

export interface RemoteText {
  content: string;
  version: string;
  size: number;
}

export function createRemoteFiles(hostID: string, sessionID: string) {
  function url(operation: string, path: string) {
    return `/api/sftp/${encodeURIComponent(hostID)}/${operation}?${new URLSearchParams({ path, session: sessionID })}`;
  }
  return {
    list: (path: string, signal?: AbortSignal) => api<{ path: string; entries: RemoteFileEntry[] }>(url("list", path), { signal }),
    read: (path: string) => api<RemoteText>(url("text", path)),
    info: (path: string) => api<DiffFileInfo>(`${url("text", path)}&mode=info`),
    imageURL: (path: string) => url("preview-image", path),
    downloadURL: (path: string) => url("download", path),
    mkdir: (path: string) => api(url("mkdir", path), { method: "POST", body: json({ path }) }),
    create: (path: string) => api(url("upload", path), { method: "PUT", body: new Blob([]) }),
    upload: (path: string, content: Blob, signal?: AbortSignal) => api(url("upload", path), { method: "PUT", body: content, signal }),
    copy: (root: string, source: string, destination: string) => api(url("copy", source), { method: "POST", body: json({ root, source, destination }) }),
    rename: (source: string, destination: string) => api(url("rename", source), { method: "POST", body: json({ source, destination }) }),
    remove: (path: string, recursive: boolean) => api(url("delete", path), { method: "POST", body: json({ path, recursive }) }),
    save: (path: string, content: string, version: string, allowEmpty = false) => api<{ version: string; bytes: number }>(url("text", path), {
      method: "PUT",
      headers: { "Content-Type": "text/plain; charset=utf-8", "If-Match": `"${version}"`, ...(allowEmpty ? { "X-Velin-Allow-Empty": "true" } : {}) },
      // A Blob keeps the shared API helper from treating source code as JSON.
      body: new Blob([content], { type: "text/plain; charset=utf-8" }),
    }),
  };
}

export function fileName(path: string) { return path.split("/").at(-1) || path; }
export function parentRemotePath(path: string) { return path.replace(/\/+$/, "").split("/").slice(0, -1).join("/") || "/"; }
export function withinRemotePath(root: string, path: string) { return root === path || path.startsWith(`${root.replace(/\/$/, "")}/`); }
export function joinRemotePath(root: string, path: string) { return `${root.replace(/\/$/, "")}/${path}`; }
export function relativeRemotePath(root: string, path: string) {
  const prefix = `${root.replace(/\/$/, "")}/`;
  return path.startsWith(prefix) ? path.slice(prefix.length) : path;
}
