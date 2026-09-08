<script setup lang="ts">
import { ExternalLink, Globe2, MoreHorizontal, Plus } from "@lucide/vue";
import type { Host, WebService } from "../types";

defineProps<{
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
</script>

<template>
  <section class="web-service-resource">
    <header class="web-service-heading">
      <span><Globe2 :size="14" />内网 Web</span>
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
        :title="`${service.name}\n${service.targetURL}`"
        @dblclick="emit('open', service)"
      >
        <Globe2 :size="15" />
        <div>
          <strong>{{ service.name }}</strong>
          <small
            >{{ hosts.find((host) => host.id === service.hostID)?.name || "主机已删除" }}
            · {{ service.proxyMode === "host_port" ? `端口 ${service.listenPort}` : "路径代理" }}
            · {{ service.pageMode === "vite" ? "Vite" : "HTML" }}
            · {{ service.targetURL }}</small
          >
        </div>
        <el-dropdown trigger="click" @click.stop>
          <button class="icon-btn row-menu"><MoreHorizontal :size="15" /></button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item :icon="ExternalLink" @click="emit('open', service)"
                >打开</el-dropdown-item
              >
              <el-dropdown-item @click="emit('edit', service)">编辑</el-dropdown-item>
              <el-dropdown-item divided @click="emit('delete', service)"
                >删除</el-dropdown-item
              >
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </article>
      <button v-if="!services.length" class="web-service-empty" @click="emit('add')">
        <Plus :size="14" />添加内网 Web
      </button>
    </div>
  </section>
</template>
