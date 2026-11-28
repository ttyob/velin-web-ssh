<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import {
  CheckSquare,
  ChevronsDownUp,
  ChevronsUpDown,
  Copy,
  Eye,
  EyeOff,
  FilePenLine,
  FlaskConical,
  FolderInput,
  FolderOpen,
  Plus,
  Server,
  Tags,
  Trash2,
  X,
} from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import { contextMenuPosition, contextMenuKey } from "../contextMenu";
import { maskHostAddress } from "../hostAddress";
import type { Host } from "../types";
import HostGroupNode, {
  type HostDropTarget,
  type HostTreeGroup,
} from "./HostGroupNode.vue";

const props = defineProps<{
  hosts: Host[];
  testing?: string;
  activeHostId?: string;
  connectedHostIds?: Set<string>;
}>();
const emit = defineEmits<{
  connect: [Host];
  files: [Host];
  test: [Host];
  edit: [Host];
  duplicate: [Host];
  delete: [Host];
  add: [];
  refresh: [];
  reorder: [{ hostID: string; target: HostDropTarget }];
}>();
type HostMenuState = { host: Host; x: number; y: number };
const selecting = ref(false),
  selected = ref(new Set<string>()),
  collapsed = ref(new Set<string>()),
  busy = ref(false),
  draggingHostID = ref(""),
  dropTarget = ref<HostDropTarget | null>(null),
  hostMenu = ref<HostMenuState>(),
  hostMenuElement = ref<HTMLElement>();
const maskAddressStorageKey = "velin-mask-host-address";
const maskAddresses = ref(loadMaskAddresses());
const groups = computed(() => buildGroups(props.hosts));
const allGroupPaths = computed(() => collectGroupPaths(groups.value));
const allGroupsCollapsed = computed(
  () =>
    allGroupPaths.value.length > 0 &&
    allGroupPaths.value.every((path) => collapsed.value.has(path)),
);

function loadMaskAddresses() {
  try {
    return localStorage.getItem(maskAddressStorageKey) === "1";
  } catch {
    return false;
  }
}
function toggleMaskAddresses() {
  maskAddresses.value = !maskAddresses.value;
  try {
    localStorage.setItem(maskAddressStorageKey, maskAddresses.value ? "1" : "0");
  } catch {}
}
function collectGroupPaths(items: HostTreeGroup[]): string[] {
  const paths: string[] = [];
  for (const item of items) {
    paths.push(item.path);
    if (item.children.length) paths.push(...collectGroupPaths(item.children));
  }
  return paths;
}
function toggleAllGroups() {
  collapsed.value = allGroupsCollapsed.value
    ? new Set()
    : new Set(allGroupPaths.value);
}

function closeHostMenu() {
  hostMenu.value = undefined;
}
async function openHostMenu(state: HostMenuState) {
  closeHostMenu();
  hostMenu.value = { ...state };
  await nextTick();
  if (!hostMenu.value || !hostMenuElement.value) return;
  Object.assign(hostMenu.value, contextMenuPosition(hostMenu.value, hostMenuElement.value));
  hostMenuElement.value
    .querySelector<HTMLButtonElement>("button:not(:disabled)")
    ?.focus({ preventScroll: true });
}
function outsideHostMenu(event: PointerEvent) {
  if (!hostMenuElement.value?.contains(event.target as Node)) closeHostMenu();
}
function wheelHostMenu(event: WheelEvent) {
  if (!hostMenuElement.value?.contains(event.target as Node)) closeHostMenu();
}
function hostMenuKey(event: KeyboardEvent) { contextMenuKey(event, hostMenuElement.value, closeHostMenu); }
function runHostMenu(action: (host: Host) => void) {
  const host = hostMenu.value?.host;
  closeHostMenu();
  if (host) action(host);
}
function canOpenHostFiles(host: Host) {
  return (
    (host.protocol || "ssh") === "ssh" &&
    (Boolean(host.credentialID) || Boolean(host.hasPassword))
  );
}
watch(
  () => Boolean(hostMenu.value),
  (open) => {
    if (open) {
      document.addEventListener("pointerdown", outsideHostMenu, true);
      document.addEventListener("wheel", wheelHostMenu, {
        capture: true,
        passive: true,
      });
      window.addEventListener("resize", closeHostMenu);
      window.addEventListener("blur", closeHostMenu);
    } else {
      document.removeEventListener("pointerdown", outsideHostMenu, true);
      document.removeEventListener("wheel", wheelHostMenu, true);
      window.removeEventListener("resize", closeHostMenu);
      window.removeEventListener("blur", closeHostMenu);
    }
  },
);
watch(() => props.hosts, closeHostMenu);
onBeforeUnmount(() => {
  closeHostMenu();
  document.removeEventListener("pointerdown", outsideHostMenu, true);
  document.removeEventListener("wheel", wheelHostMenu, true);
  window.removeEventListener("resize", closeHostMenu);
  window.removeEventListener("blur", closeHostMenu);
});

function buildGroups(hosts: Host[]): HostTreeGroup[] {
  const roots: HostTreeGroup[] = [];
  const groupsByPath = new Map<string, HostTreeGroup>();
  const ungrouped: Host[] = [];
  for (const host of hosts) {
    const parts = host.groupName
      .split("/")
      .map((part) => part.trim())
      .filter(Boolean);
    if (!parts.length) {
      ungrouped.push(host);
      continue;
    }
    let path = "";
    let parent: HostTreeGroup | undefined;
    let group: HostTreeGroup | undefined;
    for (const name of parts) {
      path = path ? `${path}/${name}` : name;
      group = groupsByPath.get(path);
      if (!group) {
        group = { name, path, hosts: [], children: [] };
        groupsByPath.set(path, group);
        if (parent) parent.children.push(group);
        else roots.push(group);
      }
      parent = group;
    }
    group?.hosts.push(host);
  }
  const sortGroups = (items: HostTreeGroup[]): HostTreeGroup[] =>
    items
      .map((item) => ({
        ...item,
        hosts: [...item.hosts].sort(compareHosts),
        children: sortGroups(item.children),
      }))
      .sort((a, b) => a.name.localeCompare(b.name));
  const result = sortGroups(roots);
  if (ungrouped.length)
    result.push({
      name: "未分组",
      path: "__ungrouped__",
      hosts: [...ungrouped].sort(compareHosts),
      children: [],
    });
  return result;
}
function compareHosts(a: Host, b: Host) {
  return (
    Number(a.sortOrder ?? 0) - Number(b.sortOrder ?? 0) ||
    a.name.localeCompare(b.name)
  );
}
function toggleGroup(path: string) {
  const next = new Set(collapsed.value);
  if (next.has(path)) next.delete(path);
  else next.add(path);
  collapsed.value = next;
}
function toggle(id: string, value: boolean) {
  const next = new Set(selected.value);
  if (value) next.add(id);
  else next.delete(id);
  selected.value = next;
}
function toggleAll() {
  selected.value =
    selected.value.size === props.hosts.length
      ? new Set()
      : new Set(props.hosts.map((host) => host.id));
}
function stopSelecting() {
  selecting.value = false;
  selected.value = new Set();
}
function beginDrag(host: Host) {
  if (selecting.value) return;
  draggingHostID.value = host.id;
}
function clearDrag() {
  draggingHostID.value = "";
  dropTarget.value = null;
}
function updateDropTarget(target: HostDropTarget) {
  dropTarget.value = target;
}
function completeDrop(target: HostDropTarget) {
  const hostID = draggingHostID.value;
  if (!hostID) return clearDrag();
  emit("reorder", { hostID, target });
  clearDrag();
}
async function batch(action: "group" | "tags" | "delete") {
  if (!selected.value.size) return ElMessage.warning("请先选择主机");
  try {
    let body: any = { ids: [...selected.value], action };
    if (action === "group") {
      const result = await ElMessageBox.prompt(
        "输入目标分组名称，留空可移出当前分组。",
        `移动 ${selected.value.size} 台主机`,
        { confirmButtonText: "移动", cancelButtonText: "取消" },
      );
      body.groupName = result.value.trim();
    }
    if (action === "tags") {
      const result = await ElMessageBox.prompt(
        "输入要添加的标签，多个标签使用逗号分隔。",
        `标记 ${selected.value.size} 台主机`,
        {
          confirmButtonText: "添加",
          cancelButtonText: "取消",
          inputValidator: (v) => Boolean(v.trim()) || "请输入标签",
        },
      );
      body.tags = result.value.trim();
    }
    if (action === "delete")
      await ElMessageBox.confirm(
        `删除选中的 ${selected.value.size} 台主机？若任一主机存在活动会话，整批操作都会取消。`,
        "批量删除",
        {
          confirmButtonText: "全部删除",
          cancelButtonText: "取消",
          type: "warning",
        },
      );
    busy.value = true;
    await api("/api/hosts/batch", { method: "POST", body: json(body) });
    ElMessage.success(`已更新 ${selected.value.size} 台主机`);
    stopSelecting();
    emit("refresh");
  } catch (e: any) {
    if (e !== "cancel" && e !== "close")
      ElMessage.error(e instanceof Error ? e.message : "批量操作失败");
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="host-list-tools">
    <template v-if="selecting"
      ><el-checkbox
        :model-value="selected.size === hosts.length && hosts.length > 0"
        :indeterminate="selected.size > 0 && selected.size < hosts.length"
        @change="toggleAll"
        >已选 {{ selected.size }}</el-checkbox
      >
      <div class="host-batch-actions">
        <el-tooltip content="移动分组"
          ><button class="icon-btn" :disabled="busy" @click="batch('group')">
            <FolderInput :size="15" /></button></el-tooltip
        ><el-tooltip content="添加标签"
          ><button class="icon-btn" :disabled="busy" @click="batch('tags')">
            <Tags :size="15" /></button></el-tooltip
        ><el-tooltip content="批量删除"
          ><button
            class="icon-btn danger"
            :disabled="busy"
            @click="batch('delete')"
          >
            <Trash2 :size="15" /></button></el-tooltip
        ><button class="icon-btn" title="退出选择" @click="stopSelecting">
          <X :size="15" />
        </button></div
    ></template>
    <template v-else
      ><span>{{ hosts.length }} 台主机</span>
      <div>
        <button
          v-if="allGroupPaths.length > 1"
          class="icon-btn"
          :title="allGroupsCollapsed ? '展开全部分组' : '折叠全部分组'"
          :aria-label="allGroupsCollapsed ? '展开全部分组' : '折叠全部分组'"
          @click="toggleAllGroups"
        >
          <ChevronsUpDown v-if="allGroupsCollapsed" :size="15" />
          <ChevronsDownUp v-else :size="15" />
        </button>
        <button
          class="icon-btn"
          :class="{ active: maskAddresses }"
          :title="maskAddresses ? '显示主机 IP/地址' : '隐藏主机 IP/地址（打码）'"
          :aria-label="maskAddresses ? '显示主机 IP/地址' : '隐藏主机 IP/地址（打码）'"
          :aria-pressed="maskAddresses"
          @click="toggleMaskAddresses"
        >
          <EyeOff v-if="maskAddresses" :size="15" />
          <Eye v-else :size="15" />
        </button>
        <button class="icon-btn" title="批量选择" @click="selecting = true">
          <CheckSquare :size="15" /></button
        ><button class="icon-btn" title="新增主机" @click="emit('add')">
          <Plus :size="15" />
        </button></div
    ></template>
  </div>
  <div class="resource-list host-resource-list">
    <div v-if="!hosts.length" class="empty-small">
      <Server :size="28" /><span>暂无主机</span>
    </div>
    <HostGroupNode
      v-for="group in groups"
      :key="group.path"
      :group="group"
      :depth="0"
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
      @context="openHostMenu($event)"
      @test="emit('test', $event)"
      @edit="emit('edit', $event)"
      @duplicate="emit('duplicate', $event)"
      @delete="emit('delete', $event)"
      @toggle-selection="toggle"
      @toggle-group="toggleGroup"
      @drag-start="beginDrag"
      @drag-end="clearDrag"
      @drag-over="updateDropTarget"
      @drop="completeDrop"
    />
  </div>
  <Teleport to="body">
    <nav
      v-if="hostMenu"
      ref="hostMenuElement"
      class="host-context-menu"
      role="menu"
      aria-label="主机操作"
      :style="{ left: `${hostMenu.x}px`, top: `${hostMenu.y}px` }"
      @pointerdown.stop
      @contextmenu.prevent.stop
      @keydown="hostMenuKey"
    >
      <div class="host-context-heading">
        <strong>{{ hostMenu.host.name }}</strong>
        <small>{{ hostMenu.host.username }}@{{ maskAddresses ? maskHostAddress(hostMenu.host.address) : hostMenu.host.address }}</small>
      </div>
      <button
        class="action-files"
        role="menuitem"
        :disabled="!canOpenHostFiles(hostMenu.host)"
        :title="
          canOpenHostFiles(hostMenu.host)
            ? '打开 SFTP 文件管理'
            : '请先为这台主机保存 SSH 密码或密钥'
        "
        @click="runHostMenu((host) => emit('files', host))"
      >
        <FolderOpen :size="14" /><span>SFTP</span>
      </button>
      <button
        class="action-test"
        role="menuitem"
        :disabled="testing === hostMenu.host.id"
        @click="runHostMenu((host) => emit('test', host))"
      >
        <FlaskConical :size="14" />
        <span>{{ testing === hostMenu.host.id ? "正在测试…" : "测试连接" }}</span>
      </button>
      <button
        class="action-edit"
        role="menuitem"
        @click="runHostMenu((host) => emit('edit', host))"
      >
        <FilePenLine :size="14" /><span>编辑</span>
      </button>
      <button
        class="action-copy"
        role="menuitem"
        @click="runHostMenu((host) => emit('duplicate', host))"
      >
        <Copy :size="14" /><span>复制主机</span>
      </button>
      <div class="host-context-separator" role="separator" />
      <button
        class="danger"
        role="menuitem"
        @click="runHostMenu((host) => emit('delete', host))"
      >
        <Trash2 :size="14" /><span>删除</span>
      </button>
    </nav>
  </Teleport>
</template>
