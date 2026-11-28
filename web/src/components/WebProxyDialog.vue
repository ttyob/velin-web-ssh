<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from "vue";
import {
  ChevronRight,
  ExternalLink,
  Globe,
  Link2,
  Network,
  Server,
} from "@lucide/vue";
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
  saveAndOpen: [WebService, Window | null];
}>();
const saving = ref(false);
const extraOpen = ref(false);
const targetInput = ref<{ focus?: () => void }>();
const form = reactive<WebService>({
  id: "",
  hostID: "",
  name: "",
  proxyMode: "path",
  pageMode: "html",
  listenPort: 18080,
  targetURL: "",
  upstreamHost: "",
  skipTLSVerify: false,
});
const errors = reactive({
  targetURL: "",
  listenPort: "",
  hostID: "",
});
const availableHosts = computed(() =>
  props.hosts.filter(
    (item) =>
      (!item.protocol || item.protocol === "ssh") &&
      (item.credentialID || item.hasPassword),
  ),
);
const isHTTPS = computed(() => /^https:\/\//i.test(form.targetURL.trim()));
const extraSummary = computed(() => {
  const items: string[] = [];
  if (form.pageMode === "vite") items.push("Vite 开发服务");
  if (form.upstreamHost.trim()) items.push("自定义 Host");
  return items.join(" · ");
});

function clearErrors() {
  errors.targetURL = "";
  errors.listenPort = "";
  errors.hostID = "";
}

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
      targetURL: props.service?.targetURL || "",
      upstreamHost: props.service?.upstreamHost || "",
      skipTLSVerify: props.service?.skipTLSVerify || false,
    });
    extraOpen.value = false;
    clearErrors();
    if (!props.service?.id) {
      void nextTick(() => targetInput.value?.focus?.());
    }
  },
);
watch(
  () => form.proxyMode,
  () => {
    errors.listenPort = "";
  },
);

function close() {
  emit("update:modelValue", false);
}

function normalizeTargetURL(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return "";
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(trimmed)) return trimmed;
  return "http://" + trimmed;
}

function validateForm(): { normalizedURL: string; defaultName: string } | null {
  clearErrors();
  const normalizedURL = normalizeTargetURL(form.targetURL);
  if (!normalizedURL) {
    errors.targetURL = "请输入目标 URL";
    void nextTick(() => targetInput.value?.focus?.());
    return null;
  }
  let parsed: URL;
  try {
    parsed = new URL(normalizedURL);
    if (!(parsed.protocol === "http:" || parsed.protocol === "https:")) {
      throw new Error();
    }
    if (!parsed.hostname) throw new Error();
  } catch {
    errors.targetURL = "请输入有效的 HTTP 或 HTTPS 地址";
    void nextTick(() => targetInput.value?.focus?.());
    return null;
  }
  if (
    form.proxyMode === "host_port" &&
    (!Number.isInteger(form.listenPort) ||
      form.listenPort < 1 ||
      form.listenPort > 65535)
  ) {
    errors.listenPort = "端口范围 1–65535";
    return null;
  }
  if (!form.hostID) {
    errors.hostID = availableHosts.value.length
      ? "请选择代理主机"
      : "请先添加已保存密码或凭据的 SSH 主机";
    return null;
  }
  return { normalizedURL, defaultName: parsed.host };
}

async function save(openAfter = false) {
  if (saving.value) return;
  const validated = validateForm();
  if (!validated) return;
  form.targetURL = validated.normalizedURL;
  let popup: Window | null = null;
  if (openAfter) {
    popup = window.open("about:blank", "_blank");
    if (popup) popup.opener = null;
  }
  saving.value = true;
  try {
    const path = form.id ? `/api/web-services/${form.id}` : "/api/web-services";
    const saved = await api<WebService>(path, {
      method: form.id ? "PUT" : "POST",
      body: json({
        ...form,
        name: form.name.trim() || validated.defaultName,
        targetURL: validated.normalizedURL,
        upstreamHost: form.upstreamHost.trim(),
        skipTLSVerify:
          /^https:\/\//i.test(validated.normalizedURL) && form.skipTLSVerify,
      }),
    });
    emit("saved", saved);
    close();
    if (openAfter) {
      emit("saveAndOpen", saved, popup);
    } else {
      ElMessage.success(form.id ? "内网 Web 已更新" : "内网 Web 已添加");
    }
  } catch (error) {
    popup?.close();
    ElMessage.error(error instanceof Error ? error.message : "保存失败");
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="web-proxy-dialog"
    width="min(540px, calc(100vw - 24px))"
    append-to-body
    :close-on-click-modal="false"
    align-center
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div class="hd-header">
        <span class="hd-header-icon" style="--hd-proto-color: #62c9a2">
          <Globe :size="15" />
        </span>
        <strong>{{ form.id ? "编辑内网 Web" : "添加内网 Web" }}</strong>
      </div>
    </template>

    <el-form label-position="top" class="hd-form" @submit.prevent="save(false)">
      <div class="hd-mode-wrap">
        <div
          class="hd-proto-strip cols-2"
          role="radiogroup"
          aria-label="访问模式"
        >
          <button
            type="button"
            role="radio"
            :aria-checked="form.proxyMode === 'path'"
            class="proto-vnc"
            :class="{ active: form.proxyMode === 'path' }"
            @click="form.proxyMode = 'path'"
          >
            <Link2 :size="14" />
            <span>路径代理</span>
          </button>
          <button
            type="button"
            role="radio"
            :aria-checked="form.proxyMode === 'host_port'"
            class="proto-ssh"
            :class="{ active: form.proxyMode === 'host_port' }"
            @click="form.proxyMode = 'host_port'"
          >
            <Network :size="14" />
            <span>主机端口</span>
          </button>
        </div>
        <p class="hd-plain-note">
          {{
            form.proxyMode === "path"
              ? "复用当前站点端口与登录态；依赖根路径的应用建议使用主机端口。"
              : "默认仅监听本机并校验登录态；若开放局域网监听请配合防火墙限制来源。"
          }}
        </p>
      </div>

      <div
        class="hd-row"
        :class="form.proxyMode === 'host_port' ? 'hd-addr' : 'single'"
      >
        <el-form-item :class="{ 'is-error': Boolean(errors.targetURL) }">
          <template #label>
            目标 URL <em class="hd-req">*</em>
          </template>
          <el-input
            ref="targetInput"
            v-model="form.targetURL"
            class="hd-mono"
            placeholder="http://127.0.0.1:8080"
            @input="errors.targetURL = ''"
          />
          <span v-if="errors.targetURL" class="hd-field-error">
            {{ errors.targetURL }}
          </span>
        </el-form-item>
        <el-form-item
          v-if="form.proxyMode === 'host_port'"
          :class="{ 'is-error': Boolean(errors.listenPort) }"
        >
          <template #label>
            监听端口 <em class="hd-req">*</em>
          </template>
          <el-input-number
            v-model="form.listenPort"
            class="hd-mono"
            :min="1"
            :max="65535"
            :controls="false"
            @change="errors.listenPort = ''"
          />
          <span v-if="errors.listenPort" class="hd-field-error">
            {{ errors.listenPort }}
          </span>
        </el-form-item>
      </div>

      <div class="hd-row">
        <el-form-item label="显示名称">
          <el-input
            v-model="form.name"
            placeholder="留空默认使用目标地址"
          />
        </el-form-item>
        <el-form-item :class="{ 'is-error': Boolean(errors.hostID) }">
          <template #label>
            代理主机 <em class="hd-req">*</em>
          </template>
          <el-select
            v-model="form.hostID"
            filterable
            placeholder="选择 SSH 主机"
            @change="errors.hostID = ''"
          >
            <el-option
              v-for="item in availableHosts"
              :key="item.id"
              :label="item.name"
              :value="item.id"
            >
              <span class="hd-cred-option">
                <Server :size="12" />
                <span>{{ item.name }}</span>
                <small>{{ item.address }}</small>
              </span>
            </el-option>
          </el-select>
          <span v-if="errors.hostID" class="hd-field-error">
            {{ errors.hostID }}
          </span>
          <span v-else-if="!availableHosts.length" class="hd-plain-note">
            需先添加已保存密码或凭据的 SSH 主机。
          </span>
        </el-form-item>
      </div>

      <label v-if="isHTTPS" class="hd-switch-item">
        <span>允许自签 HTTPS 证书</span>
        <el-switch v-model="form.skipTLSVerify" size="small" />
      </label>

      <button
        type="button"
        class="hd-more-toggle"
        :class="{ open: extraOpen }"
        :aria-expanded="extraOpen"
        @click="extraOpen = !extraOpen"
      >
        <ChevronRight :size="14" />
        <span>更多设置</span>
        <small>{{ extraSummary || "网页类型 · 上游 Host" }}</small>
      </button>

      <div v-if="extraOpen" class="hd-more-body">
        <div class="hd-inline-row">
          <span class="hd-inline-label">网页类型</span>
          <div class="hd-inline-tabs" role="radiogroup" aria-label="网页类型">
            <button
              type="button"
              role="radio"
              :aria-checked="form.pageMode === 'html'"
              :class="{ active: form.pageMode === 'html' }"
              @click="form.pageMode = 'html'"
            >
              普通 HTML
            </button>
            <button
              type="button"
              role="radio"
              :aria-checked="form.pageMode === 'vite'"
              :class="{ active: form.pageMode === 'vite' }"
              @click="form.pageMode = 'vite'"
            >
              Vite 开发服务
            </button>
          </div>
        </div>

        <el-form-item label="上游 Host">
          <el-input
            v-model="form.upstreamHost"
            class="hd-mono"
            placeholder="默认使用目标 URL 中的主机和端口（可选）"
          />
        </el-form-item>
      </div>
    </el-form>

    <template #footer>
      <div class="hd-footer">
        <div class="hd-footer-left" />
        <div class="hd-footer-right">
          <el-button :disabled="saving" @click="close">取消</el-button>
          <el-button
            :loading="saving"
            :disabled="!availableHosts.length"
            @click="save(false)"
          >
            保存
          </el-button>
          <el-button
            type="primary"
            :loading="saving"
            :disabled="!availableHosts.length"
            @click="save(true)"
          >
            <ExternalLink v-if="!saving" :size="14" />
            <span>保存并打开</span>
          </el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>
