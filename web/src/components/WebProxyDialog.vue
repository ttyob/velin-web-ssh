<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { Save } from "@lucide/vue";
import { ElMessage } from "element-plus";
import { api, json } from "../api";
import type { Host, WebService } from "../types";

const props = defineProps<{
  modelValue: boolean;
  hosts: Host[];
  host?: Host;
  service?: WebService;
}>();
const emit = defineEmits<{
  "update:modelValue": [boolean];
  saved: [WebService];
}>();
const loading = ref(false);
const form = reactive<WebService>({
  id: "",
  hostID: "",
  name: "",
  proxyMode: "path",
  pageMode: "html",
  listenPort: 18080,
  targetURL: "http://127.0.0.1:80",
  upstreamHost: "",
  skipTLSVerify: false,
});
const availableHosts = computed(() =>
  props.hosts.filter((item) => item.credentialID),
);
const isHTTPS = computed(() => /^https:\/\//i.test(form.targetURL.trim()));

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return;
    Object.assign(form, {
      id: props.service?.id || "",
      hostID:
        props.service?.hostID ||
        props.host?.id ||
        availableHosts.value[0]?.id ||
        "",
      name: props.service?.name || "",
      proxyMode: props.service?.proxyMode || "path",
      pageMode: props.service?.pageMode || "html",
      listenPort: props.service?.listenPort || 18080,
      targetURL: props.service?.targetURL || "http://127.0.0.1:80",
      upstreamHost: props.service?.upstreamHost || "",
      skipTLSVerify: props.service?.skipTLSVerify || false,
    });
  },
);

function close() {
  emit("update:modelValue", false);
}

async function save() {
  if (!form.name.trim()) return ElMessage.warning("请输入名称");
  try {
    const target = new URL(form.targetURL.trim());
    if (!(target.protocol === "http:" || target.protocol === "https:"))
      throw new Error();
  } catch {
    return ElMessage.warning("请输入有效的 HTTP 或 HTTPS 地址");
  }
  if (!form.hostID) return ElMessage.warning("请选择代理主机");
  if (
    form.proxyMode === "host_port" &&
    (!Number.isInteger(form.listenPort) ||
      form.listenPort < 1 ||
      form.listenPort > 65535)
  )
    return ElMessage.warning("主机端口范围应为 1 到 65535");
  loading.value = true;
  try {
    const path = form.id ? `/api/web-services/${form.id}` : "/api/web-services";
    const saved = await api<WebService>(path, {
      method: form.id ? "PUT" : "POST",
      body: json({
        ...form,
        name: form.name.trim(),
        targetURL: form.targetURL.trim(),
        upstreamHost: form.upstreamHost.trim(),
        skipTLSVerify: isHTTPS.value && form.skipTLSVerify,
      }),
    });
    emit("saved", saved);
    close();
    ElMessage.success("内网 Web 已保存");
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "保存失败");
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="web-proxy-dialog"
    :title="service?.id ? '编辑内网 Web' : '添加内网 Web'"
    width="min(560px, calc(100vw - 28px))"
    append-to-body
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-form label-position="top" class="web-proxy-form">
      <el-form-item label="访问模式">
        <el-segmented
          v-model="form.proxyMode"
          :options="[
            { label: '路径代理', value: 'path' },
            { label: '主机端口', value: 'host_port' },
          ]"
        />
      </el-form-item>
      <el-alert
        v-if="form.proxyMode === 'path'"
        type="warning"
        :closable="false"
        title="路径代理复用 Velin 登录与站点端口；依赖根路径的应用可能不兼容。"
      />
      <el-alert
        v-else
        type="warning"
        :closable="false"
        title="主机端口默认仅监听本机并验证 Velin 登录；如配置为局域网监听，仍应使用防火墙限制来源。"
      />
      <el-form-item label="网页类型">
        <el-segmented
          v-model="form.pageMode"
          :options="[
            { label: '普通 HTML', value: 'html' },
            { label: 'Vite 开发服务', value: 'vite' },
          ]"
        />
      </el-form-item>
      <el-form-item label="名称">
        <el-input v-model="form.name" placeholder="例如：家庭路由器" />
      </el-form-item>
      <el-form-item label="代理主机">
        <el-select v-model="form.hostID" filterable>
          <el-option
            v-for="item in availableHosts"
            :key="item.id"
            :label="item.name"
            :value="item.id"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="目标 URL">
        <el-input
          v-model="form.targetURL"
          placeholder="http://192.168.1.1/"
          @keyup.enter="save"
        />
      </el-form-item>
      <el-form-item v-if="form.proxyMode === 'host_port'" label="主机监听端口">
        <el-input-number
          v-model="form.listenPort"
          :min="1"
          :max="65535"
          controls-position="right"
        />
      </el-form-item>
      <el-form-item label="上游 Host（可选）">
        <el-input
          v-model="form.upstreamHost"
          placeholder="默认使用目标 URL 中的主机和端口"
        />
      </el-form-item>
      <el-form-item v-if="isHTTPS">
        <el-checkbox v-model="form.skipTLSVerify">允许自签 HTTPS 证书</el-checkbox>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button
        type="primary"
        :icon="Save"
        :loading="loading"
        :disabled="!availableHosts.length"
        @click="save"
      >
        保存
      </el-button>
    </template>
  </el-dialog>
</template>
