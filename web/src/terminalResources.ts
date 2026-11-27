import { computed, reactive, shallowRef, watch } from 'vue';
import { api } from './api';

type Usage = { totalBytes: number; usedBytes: number; usedPercent: number };
type Snapshot = { cpu: { total: number; idle: number } | null; memory: Usage | null; disk: Usage | null; collectedAt: string };
type ResourceState = { snapshot?: Snapshot; cpu?: number; unavailable: boolean };
type Subscription = { sessionID: string };
type HostPoll = { state: ResourceState; subscribers: Set<Subscription>; timer?: number; controller?: AbortController; busy: boolean; next: number };
const hosts = new Map<string, HostPoll>();

function schedule(hostID: string, entry: HostPoll, delay = 5000) {
  if (hosts.get(hostID) !== entry || !entry.subscribers.size) return;
  window.clearTimeout(entry.timer);
  entry.timer = window.setTimeout(() => void poll(hostID, entry), delay);
}
async function poll(hostID: string, entry: HostPoll) {
  if (hosts.get(hostID) !== entry || !entry.subscribers.size || entry.busy) return;
  window.clearTimeout(entry.timer);
  if (document.visibilityState === 'hidden') { schedule(hostID, entry); return; }
  const subscribers = [...entry.subscribers];
  const subscriber = subscribers[entry.next % subscribers.length]!;
  entry.busy = true; entry.controller = new AbortController();
  const timeout = window.setTimeout(() => entry.controller?.abort(), 6500);
  let delay = 5000;
  try {
    const snapshot = await api<Snapshot>(`/api/sessions/${encodeURIComponent(subscriber.sessionID)}/resources`, { signal: entry.controller.signal });
    if (hosts.get(hostID) !== entry) return;
    const previous = entry.state.snapshot?.cpu, current = snapshot.cpu;
    const total = current && previous ? current.total - previous.total : 0;
    const idle = current && previous ? current.idle - previous.idle : -1;
    entry.state.cpu = total > 0 && idle >= 0 && idle <= total ? Math.max(0, Math.min(100, (total - idle) / total * 100)) : undefined;
    entry.state.snapshot = snapshot; entry.state.unavailable = false;
    if (current && entry.state.cpu === undefined) delay = 1000;
  } catch {
    if (hosts.get(hostID) === entry) { entry.state.unavailable = true; entry.state.snapshot = undefined; entry.state.cpu = undefined; entry.next++; }
  } finally {
    window.clearTimeout(timeout); entry.busy = false;
    schedule(hostID, entry, delay);
  }
}

// Visible panes on the same host share one poll and reuse an existing terminal
// transport. Hiding or closing the last pane stops and cancels the request.
export function useTerminalResources(hostID: () => string, sessionID: () => string, active: () => boolean) {
  const state = shallowRef<ResourceState>();
  watch([hostID, sessionID, active], ([host, session, enabled], _, cleanup) => {
    state.value = undefined;
    if (!host || !session || !enabled) return;
    let entry = hosts.get(host);
    if (!entry) { entry = { state: reactive<ResourceState>({ unavailable: false }), subscribers: new Set(), busy: false, next: 0 }; hosts.set(host, entry); }
    const shared = entry, subscriber = { sessionID: session };
    shared.subscribers.add(subscriber); state.value = shared.state;
    if (!shared.timer && !shared.busy) void poll(host, shared);
    const visible = () => { if (document.visibilityState !== 'hidden') void poll(host, shared); };
    document.addEventListener('visibilitychange', visible);
    cleanup(() => {
      document.removeEventListener('visibilitychange', visible);
      shared.subscribers.delete(subscriber);
      if (!shared.subscribers.size) { hosts.delete(host); window.clearTimeout(shared.timer); shared.controller?.abort(); }
    });
  }, { immediate: true });
  return computed(() => state.value);
}
