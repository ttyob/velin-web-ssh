<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from "vue";
import {
  ExternalLink,
  FilePenLine,
  Globe2,
  MoreHorizontal,
  Plus,
  Trash2,
} from "@lucide/vue";
import { formatWebServiceTarget, isHttpsTargetURL } from "../hostAddress";
import type { Host, WebService } from "../types";

const props = defineProps<{
  services: WebService[];
  hosts: Host[];
  opening?: string;
}>();
const emit = defineEmits<{
  add: [];
  open: [WebService];
  edit: [WebService];
  delete: [WebService];
}>();
type ServiceMenuState = { service: WebService; x: number; y: number };
const menu = ref<ServiceMenuState>(),
  menuElement = ref<HTMLElement>();

function serviceHostName(service: WebService) {
  return props.hosts.find((host) => host.id === service.hostID)?.name || "";
}
function serviceTargetSummary(service: WebService) {
  return formatWebServiceTarget(service.targetURL);
}
function serviceTooltip(service: WebService) {
  const hostName = serviceHostName(service) || "主机已删除";
  const proxyLabel =
    service.proxyMode === "host_port" ? `端口 ${service.listenPort}` : "路径代理";
  const pageLabel = service.pageMode === "vite" ? "Vite" : "HTML";
  return `${service.name}\n${hostName} · ${proxyLabel} · ${pageLabel}\n${service.targetURL}`;
}
function serviceIconColor(service: WebService) {
  return isHttpsTargetURL(service.targetURL) ? "#4cc39b" : "#5caef0";
}

function closeMenu() {
  menu.value = undefined;
}
async function openMenu(event: MouseEvent, service: WebService) {
  event.preventDefault();
  event.stopPropagation();
  closeMenu();
  menu.value = { service, x: event.clientX, y: event.clientY };
  await nextTick();
  if (!menu.value || !menuElement.value) return;
  const rect = menuElement.value.getBoundingClientRect();
  menu.value.x = Math.max(
    6,
    Math.min(event.clientX, window.innerWidth - rect.width - 6),
  );
  menu.value.y = Math.max(
    6,
    Math.min(event.clientY, window.innerHeight - rect.height - 6),
  );
  menuElement.value
    .querySelector<HTMLButtonElement>("button:not(:disabled)")
    ?.focus({ preventScroll: true });
}
async function openFromButton(event: MouseEvent, service: WebService) {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
  closeMenu();
  menu.value = {
    service,
    x: Math.min(rect.right, window.innerWidth - 12),
    y: rect.bottom + 3,
  };
  await nextTick();
  if (!menu.value || !menuElement.value) return;
  const box = menuElement.value.getBoundingClientRect();
  menu.value.x = Math.max(
    6,
    Math.min(menu.value.x, window.innerWidth - box.width - 6),
  );
  menu.value.y = Math.max(
    6,
    Math.min(menu.value.y, window.innerHeight - box.height - 6),
  );
  menuElement.value
    .querySelector<HTMLButtonElement>("button:not(:disabled)")
    ?.focus({ preventScroll: true });
}
function outsideMenu(event: PointerEvent) {
  if (!menuElement.value?.contains(event.target as Node)) closeMenu();
}
function wheelMenu(event: WheelEvent) {
  if (!menuElement.value?.contains(event.target as Node)) closeMenu();
}
function menuKey(event: KeyboardEvent) {
  if (event.key === "Escape") event.preventDefault();
  const buttons = [
    ...(menuElement.value?.querySelectorAll<HTMLButtonElement>(
      "button:not(:disabled)",
    ) || []),
  ];
  if (event.key === "Escape" || event.key === "Tab") {
    closeMenu();
    return;
  }
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
  const target =
    event.key === "Home"
      ? buttons[0]
      : event.key === "End"
        ? buttons.at(-1)
        : buttons[
            (current + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) %
              buttons.length
          ];
  target?.focus({ preventScroll: true });
}
function runMenu(action: (service: WebService) => void) {
  const service = menu.value?.service;
  closeMenu();
  if (service) action(service);
}
watch(
  () => Boolean(menu.value),
  (open) => {
    if (open) {
      document.addEventListener("pointerdown", outsideMenu, true);
      document.addEventListener("wheel", wheelMenu, {
        capture: true,
        passive: true,
      });
      window.addEventListener("resize", closeMenu);
      window.addEventListener("blur", closeMenu);
    } else {
      document.removeEventListener("pointerdown", outsideMenu, true);
      document.removeEventListener("wheel", wheelMenu, true);
      window.removeEventListener("resize", closeMenu);
      window.removeEventListener("blur", closeMenu);
    }
  },
);
watch(() => props.services, closeMenu);
onBeforeUnmount(() => {
  closeMenu();
  document.removeEventListener("pointerdown", outsideMenu, true);
  document.removeEventListener("wheel", wheelMenu, true);
  window.removeEventListener("resize", closeMenu);
  window.removeEventListener("blur", closeMenu);
});
</script>

<template>
  <section class="web-service-resource">
    <header class="web-service-heading">
      <span class="web-service-count">{{ services.length }} 个服务</span>
      <button class="icon-btn" title="添加内网 Web" @click="emit('add')">
        <Plus :size="14" />
      </button>
    </header>
    <div class="web-service-list">
      <article
        v-for="service in services"
        :key="service.id"
        class="web-service-row"
        :class="{ opening: opening === service.id }"
        :title="serviceTooltip(service)"
        @dblclick.stop="emit('open', service)"
        @contextmenu.prevent.stop="openMenu($event, service)"
      >
        <span
          class="web-service-icon"
          :style="{ '--web-icon-color': serviceIconColor(service) }"
          aria-hidden="true"
        >
          <Globe2 :size="15" />
        </span>
        <div class="web-service-copy">
          <div class="web-service-title-row">
            <strong>{{ service.name }}</strong>
            <span
              v-if="service.proxyMode === 'host_port' && service.listenPort"
              class="web-service-badge"
            >:{{ service.listenPort }}</span>
            <span
              v-if="service.pageMode === 'vite'"
              class="web-service-badge is-vite"
            >Vite</span>
          </div>
          <small>
            <span
              class="web-service-host"
              :class="{ missing: !serviceHostName(service) }"
            >{{ serviceHostName(service) || "主机已删除" }}</span><template
              v-if="serviceTargetSummary(service)"
            ><span class="web-service-sep" aria-hidden="true">·</span><span
                class="web-service-target"
              >{{ serviceTargetSummary(service) }}</span></template>
          </small>
        </div>
        <span class="web-service-row-actions">
          <button
            class="icon-btn row-menu"
            title="内网 Web 操作"
            aria-label="内网 Web 操作"
            aria-haspopup="menu"
            @pointerdown.stop
            @click.stop="openFromButton($event, service)"
            @dblclick.stop
          >
            <MoreHorizontal :size="15" />
          </button>
        </span>
      </article>
      <div v-if="!services.length" class="empty-small">
        <Globe2 :size="28" />
        <span>暂无内网 Web</span>
        <button type="button" @click="emit('add')">添加服务</button>
      </div>
    </div>
  </section>
  <Teleport to="body">
    <nav
      v-if="menu"
      ref="menuElement"
      class="host-context-menu web-context-menu"
      role="menu"
      aria-label="内网 Web 操作"
      :style="{ left: `${menu.x}px`, top: `${menu.y}px` }"
      @pointerdown.stop
      @contextmenu.prevent.stop
      @keydown="menuKey"
    >
      <div class="host-context-heading">
        <strong>{{ menu.service.name }}</strong>
        <small>{{ serviceHostName(menu.service) || "主机已删除" }} · {{ serviceTargetSummary(menu.service) || menu.service.targetURL }}</small>
      </div>
      <button class="action-open" role="menuitem" @click="runMenu((service) => emit('open', service))">
        <ExternalLink :size="14" /><span>打开</span>
      </button>
      <button class="action-edit" role="menuitem" @click="runMenu((service) => emit('edit', service))">
        <FilePenLine :size="14" /><span>编辑</span>
      </button>
      <div class="host-context-separator" role="separator" />
      <button
        class="danger"
        role="menuitem"
        @click="runMenu((service) => emit('delete', service))"
      >
        <Trash2 :size="14" /><span>删除</span>
      </button>
    </nav>
  </Teleport>
</template>
