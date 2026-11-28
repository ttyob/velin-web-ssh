<script setup lang="ts">
import { computed } from "vue";
import {
  Apple,
  ChevronRight,
  Monitor,
  MoreHorizontal,
  Server,
  SquareTerminal,
} from "@lucide/vue";
import type { Host } from "../types";
import { distributionLogos } from "../distributionLogos";
import { formatHostEndpoint } from "../hostAddress";

defineOptions({ name: "HostGroupNode" });

export interface HostTreeGroup {
  name: string;
  path: string;
  hosts: Host[];
  children: HostTreeGroup[];
}
export type HostDropTarget =
  | { kind: "host"; groupPath: string; hostID: string; after: boolean }
  | { kind: "group"; groupPath: string };

const props = defineProps<{
  group: HostTreeGroup;
  depth: number;
  selecting: boolean;
  selected: Set<string>;
  collapsed: Set<string>;
  testing?: string;
  draggingHostID?: string;
  dropTarget?: HostDropTarget | null;
  maskAddresses?: boolean;
  activeHostId?: string;
  connectedHostIds?: Set<string>;
}>();
const emit = defineEmits<{
  connect: [Host];
  context: [{ host: Host; x: number; y: number }];
  test: [Host];
  edit: [Host];
  duplicate: [Host];
  delete: [Host];
  toggleSelection: [string, boolean];
  toggleGroup: [string];
  dragStart: [Host];
  dragEnd: [];
  dragOver: [HostDropTarget];
  drop: [HostDropTarget];
}>();

const count = computed(() => countHosts(props.group));
const platformIcons = {
  linux: SquareTerminal,
  windows: Monitor,
  macos: Apple,
  bsd: Server,
  unix: SquareTerminal,
};
const platformColors: Record<string, string> = {
  linux: "#69b4e8",
  windows: "#5ca8f8",
  macos: "#d6dce6",
  bsd: "#ef7f55",
  unix: "#67c99a",
};
const distributionBadges: Record<
  string,
  { label: string; color: string }
> = {
  ubuntu: { label: "Ubuntu", color: "#e95420" },
  debian: { label: "Debian", color: "#e8437a" },
  linuxmint: { label: "Linux Mint", color: "#87cf3e" },
  pop: { label: "Pop!_OS", color: "#48b9c7" },
  elementary: { label: "elementary OS", color: "#64baff" },
  rhel: { label: "Red Hat Enterprise Linux", color: "#f05252" },
  centos: { label: "CentOS", color: "#c26eb0" },
  rocky: { label: "Rocky Linux", color: "#10b981" },
  almalinux: { label: "AlmaLinux", color: "#4f6bff" },
  fedora: { label: "Fedora", color: "#51a2da" },
  arch: { label: "Arch Linux", color: "#1793d1" },
  manjaro: { label: "Manjaro", color: "#35bf5c" },
  endeavouros: { label: "EndeavourOS", color: "#7f5af0" },
  alpine: { label: "Alpine Linux", color: "#3b9ed8" },
  opensuse: { label: "openSUSE", color: "#73ba25" },
  gentoo: { label: "Gentoo", color: "#8b7bb5" },
  kali: { label: "Kali Linux", color: "#6ea5c7" },
  raspbian: { label: "Raspberry Pi OS", color: "#e34877" },
  nixos: { label: "NixOS", color: "#5277c3" },
  void: { label: "Void Linux", color: "#5cb88a" },
  amazon: { label: "Amazon Linux", color: "#ff9900" },
  oracle: { label: "Oracle Linux", color: "#e0624f" },
  proxmox: { label: "Proxmox VE", color: "#e57000" },
};
function distributionBadge(value?: string) {
  const id = value?.trim().toLowerCase() || "";
  return (
    distributionBadges[id] || {
      label: id || "Linux",
      color: "#6f9cff",
    }
  );
}
function systemLabel(host: Host) {
  if (host.protocol === "vnc") return "VNC 桌面";
  if (host.protocol === "rdp") return "RDP 桌面";
  if (host.distribution) return distributionBadge(host.distribution).label;
  const labels: Record<string, string> = {
    linux: "Linux",
    windows: "Windows",
    macos: "macOS",
    bsd: "BSD",
    unix: "Unix",
  };
  return labels[host.platform || ""] || "尚未识别系统";
}
function hostIconColor(host: Host) {
  if (host.protocol === "vnc") return "#62c9a2";
  if (host.protocol === "rdp") return "#69aefc";
  if (host.distribution) return distributionBadge(host.distribution).color;
  return platformColors[host.platform || ""] || "#7aa2ff";
}
function hostInitial(name: string) {
  return Array.from(name.trim())[0]?.toUpperCase() || "?";
}
function hostProtocol(host: Host) {
  return (host.protocol || "ssh").toLowerCase();
}
function hostDisplayAddress(host: Host) {
  return formatHostEndpoint(host, { masked: props.maskAddresses });
}
function hostSubtitleTitle(host: Host) {
  const proto = hostProtocol(host).toUpperCase();
  const endpoint = formatHostEndpoint(host, {
    masked: props.maskAddresses,
    includeDefaultPort: true,
  });
  return `${proto} · ${endpoint}`;
}
function countHosts(group: HostTreeGroup): number {
  return (
    group.hosts.length +
    group.children.reduce((total, child) => total + countHosts(child), 0)
  );
}
function forwardSelection(id: string, value: boolean) {
  emit("toggleSelection", id, value);
}
function openHostMenu(event: MouseEvent, host: Host) {
  if (props.selecting) return;
  emit("context", {
    host,
    x: event.clientX,
    y: event.clientY,
  });
}
function openHostMenuFromButton(event: MouseEvent, host: Host) {
  if (props.selecting) return;
  const button = event.currentTarget;
  const rect = button instanceof HTMLElement
    ? button.getBoundingClientRect()
    : undefined;
  emit("context", {
    host,
    x: rect ? Math.min(rect.right, window.innerWidth - 12) : event.clientX,
    y: rect ? rect.bottom + 3 : event.clientY,
  });
}
function dragStart(event: DragEvent, host: Host) {
  if (props.selecting) return;
  event.dataTransfer?.setData("text/plain", host.id);
  if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
  emit("dragStart", host);
}
function dragOverHost(event: DragEvent, host: Host) {
  event.preventDefault();
  event.stopPropagation();
  const row = event.currentTarget;
  const after =
    row instanceof HTMLElement &&
    event.clientY > row.getBoundingClientRect().top + row.offsetHeight / 2;
  emit("dragOver", {
    kind: "host",
    groupPath: props.group.path,
    hostID: host.id,
    after,
  });
}
function dragOverGroup(event: DragEvent) {
  event.preventDefault();
  event.stopPropagation();
  emit("dragOver", { kind: "group", groupPath: props.group.path });
}
function emitDrop(event: DragEvent, target: HostDropTarget) {
  event.preventDefault();
  event.stopPropagation();
  emit("drop", target);
}
function dropHost(event: DragEvent, host: Host) {
  const row = event.currentTarget;
  const after =
    row instanceof HTMLElement &&
    event.clientY > row.getBoundingClientRect().top + row.offsetHeight / 2;
  emitDrop(event, {
    kind: "host",
    groupPath: props.group.path,
    hostID: host.id,
    after,
  });
}
</script>

<template>
  <section class="host-tree-group" :style="{ '--host-group-depth': depth }">
    <button
      class="host-group-heading"
      :title="group.path"
      @dragover="dragOverGroup"
      @drop="emitDrop($event, { kind: 'group', groupPath: group.path })"
      :class="{
        collapsed: collapsed.has(group.path),
        'drop-target':
          dropTarget?.kind === 'group' && dropTarget.groupPath === group.path,
      }"
      @click="emit('toggleGroup', group.path)"
    >
      <ChevronRight :size="14" />
      <span>{{ group.name }}</span>
      <small>{{ count }}</small>
    </button>
    <div
      v-show="!collapsed.has(group.path)"
      class="host-group-content"
      :class="{
        'drop-target':
          dropTarget?.kind === 'group' && dropTarget.groupPath === group.path,
      }"
      @dragover="dragOverGroup"
      @drop="emitDrop($event, { kind: 'group', groupPath: group.path })"
    >
      <article
        v-for="host in group.hosts"
        :key="host.id"
        class="host-row"
        :class="{
          selected: selected.has(host.id),
          'is-active': !selecting && activeHostId === host.id,
          'is-connected': Boolean(connectedHostIds?.has(host.id)),
          dragging: draggingHostID === host.id,
          'drop-target':
            dropTarget?.kind === 'host' && dropTarget.hostID === host.id,
        }"
        :draggable="!selecting"
        @dragstart="dragStart($event, host)"
        @dragend="emit('dragEnd')"
        @dragover="dragOverHost($event, host)"
        @drop="dropHost($event, host)"
        @dblclick="!selecting && emit('connect', host)"
        @contextmenu.prevent.stop="openHostMenu($event, host)"
      >
        <el-checkbox
          v-if="selecting"
          :model-value="selected.has(host.id)"
          @change="
            (value: boolean | string | number) =>
              emit('toggleSelection', host.id, Boolean(value))
          "
          @click.stop
        />
        <span
          class="host-icon"
          :class="{
            detected:
              (host.protocol || 'ssh') === 'ssh' && Boolean(host.platform),
            distribution:
              (host.protocol || 'ssh') === 'ssh' && Boolean(host.distribution),
          }"
          :title="systemLabel(host)"
          :style="{
            '--host-icon-color': hostIconColor(host),
          }"
        >
          <Monitor
            v-if="host.protocol === 'vnc' || host.protocol === 'rdp'"
            :size="17"
            :stroke-width="1.8"
          />
          <svg
            v-else-if="host.distribution && distributionLogos[host.distribution]"
            class="distribution-logo"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path :d="distributionLogos[host.distribution]" />
          </svg>
          <component
            :is="platformIcons[host.platform as keyof typeof platformIcons]"
            v-else-if="host.platform && platformIcons[host.platform as keyof typeof platformIcons]"
            :size="17"
            :stroke-width="1.8"
          />
          <span v-else>{{ hostInitial(host.name) }}</span>
          <span
            v-if="connectedHostIds?.has(host.id)"
            class="host-live-dot"
            title="已有活跃会话"
            aria-hidden="true"
          ></span>
        </span>
        <div class="host-copy">
          <strong>{{ host.name }}</strong>
          <small :title="hostSubtitleTitle(host)">
            <span class="host-proto" :class="`proto-${hostProtocol(host)}`">{{
              hostProtocol(host).toUpperCase()
            }}</span><span class="host-meta-sep" aria-hidden="true">·</span><span
              class="host-addr"
              :class="{ masked: maskAddresses }"
            >{{ hostDisplayAddress(host) }}</span>
          </small>
        </div>
        <span v-if="!selecting" class="host-row-actions">
          <button
            class="icon-btn row-menu"
            title="主机操作"
            aria-label="主机操作"
            aria-haspopup="menu"
            @pointerdown.stop
            @click.stop="openHostMenuFromButton($event, host)"
            @dblclick.stop
          >
            <MoreHorizontal :size="15" />
          </button>
        </span>
      </article>
      <HostGroupNode
        v-for="child in group.children"
        :key="child.path"
        :group="child"
        :depth="depth + 1"
        :selecting="selecting"
        :selected="selected"
        :collapsed="collapsed"
        :testing="testing"
        :dragging-host-id="draggingHostID"
        :drop-target="dropTarget"
        :mask-addresses="maskAddresses"
        :active-host-id="activeHostId"
        :connected-host-ids="connectedHostIds"
        @connect="emit('connect', $event)"
        @context="emit('context', $event)"
        @test="emit('test', $event)"
        @edit="emit('edit', $event)"
        @duplicate="emit('duplicate', $event)"
        @delete="emit('delete', $event)"
        @toggle-selection="forwardSelection"
        @toggle-group="emit('toggleGroup', $event)"
        @drag-start="emit('dragStart', $event)"
        @drag-end="emit('dragEnd')"
        @drag-over="emit('dragOver', $event)"
        @drop="emit('drop', $event)"
      />
    </div>
  </section>
</template>
