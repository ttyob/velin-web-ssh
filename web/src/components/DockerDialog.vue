<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import {
  Box,
  ChevronRight,
  CirclePlay,
  CircleStop,
  Download,
  FileText,
  FolderOpen,
  Info,
  LogIn,
  Pencil,
  RefreshCw,
  RotateCw,
  Save,
  TerminalSquare,
  Trash2,
  X,
} from "@lucide/vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, json } from "../api";
import type { AgentStatus, Host } from "../types";
import CodeEditor from "./CodeEditor.vue";

type DockerTab = "containers" | "compose" | "images" | "networks" | "config" | "monitor";
type DockerResource = {
  cpu: string;
  memory: string;
  memoryPercent: string;
  network: string;
  block: string;
  pids: string;
};
type DockerContainer = {
  id: string;
  name: string;
  image: string;
  status: string;
  ports: string;
  networks: string;
  networkMode: string;
  resource?: DockerResource;
};
type ComposeProject = {
  name: string;
  status: string;
  configFiles: string;
};
type ComposeContainer = DockerContainer & { service: string };
type DockerImage = {
  id: string;
  repository: string;
  tag: string;
  size: string;
  created: string;
  containerNames: string[];
  inUse: boolean;
};
type DockerNetwork = {
  id: string;
  name: string;
  driver: string;
  scope: string;
};
type DockerDiskUsage = {
  type: string;
  total: string;
  active: string;
  size: string;
  reclaimable: string;
};
type DockerMonitorOverview = {
  serverVersion: string;
  driver: string;
  totalContainers: string;
  runningContainers: string;
  pausedContainers: string;
  stoppedContainers: string;
  images: string;
  disk: DockerDiskUsage[];
};
type DockerContainerDetail = {
  id: string;
  name: string;
  created: string;
  image: string;
  command: string;
  entrypoint: string;
  env: string[];
  mounts: Array<{ source: string; destination: string; mode: string }>;
  labels: Array<{ key: string; value: string }>;
  networks: Array<{ name: string; ip: string; gateway: string; mac: string }>;
  restartPolicy: string;
};
type DockerNetworkDetail = {
  id: string;
  name: string;
  driver: string;
  scope: string;
  subnet: string;
  gateway: string;
  dns: string[];
  containers: Array<{ id: string; name: string; ip: string; mac: string }>;
};
type DockerImageDetail = {
  id: string;
  created: string;
  architecture: string;
  os: string;
  size: string;
  entrypoint: string;
  command: string;
  workingDir: string;
  env: string[];
  layers: string[];
  repoDigests: string[];
};

const props = defineProps<{
  modelValue: boolean;
  host?: Host;
  sessionId?: string;
}>();
const emit = defineEmits<{
  "update:modelValue": [boolean];
  terminal: [target: { id: string; name: string }];
}>();

const activeTab = ref<DockerTab>("containers");
const status = ref<AgentStatus>();
const loading = ref(false);
const busy = ref("");
const error = ref("");
const containers = ref<DockerContainer[]>([]);
const projects = ref<ComposeProject[]>([]);
const composeServices = reactive<Record<string, ComposeContainer[]>>({});
const composeExpanded = reactive<Record<string, boolean>>({});
const composeLoading = reactive<Record<string, boolean>>({});
const images = ref<DockerImage[]>([]);
const networks = ref<DockerNetwork[]>([]);
const monitorOverview = ref<DockerMonitorOverview>();
const monitorUpdatedAt = ref("");
let monitorTimer: number | undefined;
const imageToPull = ref("");
const dockerLoginOpen = ref(false);
const dockerLoginLoading = ref(false);
const dockerLoginRegistry = ref("");
const dockerLoginUsername = ref("");
const dockerLoginPassword = ref("");
const logsOpen = ref(false);
const logsTitle = ref("");
const logs = ref("");
const logsLoading = ref(false);
const logsTarget = ref<DockerContainer>();
const logsFollow = ref(false);
let logsTimer: number | undefined;
const containerDetailOpen = ref(false);
const containerDetailLoading = ref(false);
const containerDetail = ref<DockerContainerDetail>();
const containerDetailPolicy = ref("no");
const networkDetailOpen = ref(false);
const networkDetailLoading = ref(false);
const networkDetail = ref<DockerNetworkDetail>();
const networkContainerName = ref("");
const imageDetailOpen = ref(false);
const imageDetailLoading = ref(false);
const imageDetail = ref<DockerImageDetail>();
const composeEditorOpen = ref(false);
const composeEditorPath = ref("");
const composeEditorText = ref("");
const composeEditorProject = ref<ComposeProject>();
const composeEditorLoading = ref(false);
const composeEditorSaving = ref(false);
const configPath = ref("/etc/docker/daemon.json");
const configRaw = ref("{}");
const configLoading = ref(false);
const configSaving = ref(false);
const configRestarting = ref(false);
const registryMirrors = ref("");
const insecureRegistries = ref("");
const dockerHTTPProxy = ref("");
const dockerHTTPSProxy = ref("");
const dockerNoProxy = ref("");
const logDriver = ref("");
const logMaxSize = ref("");
const liveRestore = ref(false);

const connected = computed(() => status.value?.state === "connected");
const runningCount = computed(
  () => containers.value.filter((item) => isRunning(item.status)).length,
);
const tabLoading = computed(() => loading.value || Boolean(busy.value));
const monitoredContainers = computed(() => containers.value.filter((item) => item.resource));
const totalCPU = computed(() =>
  monitoredContainers.value.reduce((total, item) => total + parseFloatValue(item.resource?.cpu), 0).toFixed(2),
);

function parseFloatValue(value = "") {
  const result = Number.parseFloat(value.replace(/,/g, ""));
  return Number.isFinite(result) ? result : 0;
}

function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

function isRunning(statusText: string) {
  return /^up\b/i.test(statusText.trim());
}

function base64Encode(value: string) {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function base64Decode(value: string) {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index);
  return new TextDecoder().decode(bytes);
}

function parseJson<T>(output: string, fallback: T): T {
  const value = output.trim();
  if (!value) return fallback;
  try {
    return JSON.parse(value) as T;
  } catch {
    const start = Math.min(...[value.indexOf("["), value.indexOf("{")].filter((item) => item >= 0));
    const end = Math.max(value.lastIndexOf("]"), value.lastIndexOf("}"));
    if (start >= 0 && end > start) {
      try {
        return JSON.parse(value.slice(start, end + 1)) as T;
      } catch {}
    }
    return fallback;
  }
}

async function loadStatus() {
  if (!props.host) return;
  status.value = await api<AgentStatus>(`/api/hosts/${props.host.id}/agent`);
}

async function ensureConnected() {
  if (!props.host) throw new Error("当前终端没有可用的主机信息");
  await loadStatus();
  if (connected.value) return;
  status.value = await api<AgentStatus>(
    `/api/hosts/${props.host.id}/agent/connect`,
    { method: "POST", body: json({}) },
  );
}

async function command(commandText: string) {
  if (!props.host) throw new Error("当前终端没有可用的主机信息");
  const result = await api<{ output: string; success: boolean; error?: string }>(
    `/api/hosts/${props.host.id}/agent/command`,
    { method: "POST", body: json({ command: commandText }) },
  );
  if (!result.success)
    throw new Error(result.error || result.output || "Docker 命令执行失败");
  return result.output || "";
}

async function loginDocker() {
  const username = dockerLoginUsername.value.trim();
  const password = dockerLoginPassword.value;
  if (!username) return ElMessage.warning("请输入 Docker 用户名");
  if (!password) return ElMessage.warning("请输入密码或访问令牌");
  if (!props.host) return ElMessage.error("当前终端没有可用的主机信息");
  dockerLoginLoading.value = true;
  try {
    await ensureConnected();
    const result = await api<{ output: string; success: boolean; error?: string }>(
      `/api/hosts/${props.host.id}/docker/login`,
      {
        method: "POST",
        body: json({
          registry: dockerLoginRegistry.value.trim(),
          username,
          password,
        }),
      },
    );
    if (!result.success) throw new Error(result.error || result.output || "Docker 登录失败");
    dockerLoginOpen.value = false;
    dockerLoginPassword.value = "";
    ElMessage.success("Docker 登录成功");
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "Docker 登录失败");
  } finally {
    dockerLoginLoading.value = false;
  }
}

function parseContainers(output: string) {
  const sections = output.split(/\r?\nMODES\r?\n/);
  const modeAndStats = (sections[1] || "").split(/\r?\nSTATS\r?\n/);
  const modeByID = new Map<string, string>();
  for (const line of (modeAndStats[0] || "").split(/\r?\n/).filter(Boolean)) {
    const [id = "", mode = ""] = line.split("\t");
    if (id) modeByID.set(id.slice(0, 12), mode || "default");
  }
  const resourceByID = parseContainerStats(modeAndStats[1] || "");
  return output
    .split(/\r?\nMODES\r?\n/)[0]
    .split(/\r?\n/)
    .filter(Boolean)
    .map((line) => {
      const [id = "", name = "", image = "", statusText = "", ports = "", networksText = ""] = line.split("\t");
      return {
        id,
        name,
        image,
        status: statusText,
        ports,
        networks: networksText,
        networkMode: modeByID.get(id) || "default",
        resource: resourceByID.get(id) || resourceByID.get(name),
      };
    })
    .filter((item) => item.id && item.name);
}

function parseContainerStats(output: string) {
  const resourceByID = new Map<string, DockerResource>();
  for (const line of output.split(/\r?\n/).filter(Boolean)) {
    const [id = "", name = "", cpu = "", memory = "", memoryPercent = "", network = "", block = "", pids = ""] = line.split("\t");
    if (!id || !name) continue;
    const resource = { cpu, memory, memoryPercent, network, block, pids };
    resourceByID.set(id, resource);
    resourceByID.set(id.slice(0, 12), resource);
    resourceByID.set(name, resource);
  }
  return resourceByID;
}

function parseMonitorOverview(output: string) {
  const sections = output.split(/\r?\nDF\r?\n/);
  const [serverVersion = "", driver = "", totalContainers = "", runningContainers = "", pausedContainers = "", stoppedContainers = "", images = ""] = (sections[0] || "").trim().split("\t");
  const disk = (sections[1] || "")
    .split(/\r?\n/)
    .filter(Boolean)
    .map((line) => {
      const [type = "", total = "", active = "", size = "", reclaimable = ""] = line.split("\t");
      return { type, total, active, size, reclaimable };
    })
    .filter((item) => item.type);
  return { serverVersion, driver, totalContainers, runningContainers, pausedContainers, stoppedContainers, images, disk };
}

function parseImages(output: string) {
  const sections = output.split(/\r?\nUSAGE\r?\n/);
  const usageByImage = new Map<string, string[]>();
  for (const line of (sections[1] || "").split(/\r?\n/).filter(Boolean)) {
    const [imageID = "", containerName = ""] = line.split("\t");
    if (!imageID || !containerName) continue;
    const names = usageByImage.get(imageID) || [];
    const normalizedID = imageID.replace(/^sha256:/, "");
    names.push(containerName.replace(/^\//, ""));
    usageByImage.set(imageID, names);
    usageByImage.set(normalizedID, names);
    usageByImage.set(normalizedID.slice(0, 12), names);
  }
  return output
    .split(/\r?\nUSAGE\r?\n/)[0]
    .split(/\r?\n/)
    .filter(Boolean)
    .map((line) => {
      const [id = "", repository = "", tag = "", size = "", created = ""] = line.split("\t");
      const normalizedID = id.replace(/^sha256:/, "");
      const containerNames = usageByImage.get(id) || usageByImage.get(normalizedID) || usageByImage.get(normalizedID.slice(0, 12)) || [];
      return { id, repository, tag, size, created, containerNames, inUse: containerNames.length > 0 };
    })
    .filter((item) => item.id && item.repository);
}

function imageLabel(image: DockerImage) {
  return image.repository === "<none>" && image.tag === "<none>"
    ? "未标记镜像"
    : `${image.repository}:${image.tag}`;
}

function parseNetworks(output: string) {
  return output
    .split(/\r?\n/)
    .filter(Boolean)
    .map((line) => {
      const [id = "", name = "", driver = "", scope = ""] = line.split("\t");
      return { id, name, driver, scope };
    })
    .filter((item) => item.id && item.name);
}

function parseProjects(output: string) {
  const value = parseJson<unknown>(output, []);
  const list = Array.isArray(value) ? value : [];
  return list
    .map((item: any) => ({
      name: String(item.Name || item.name || ""),
      status: String(item.Status || item.status || ""),
      configFiles: String(item.ConfigFiles || item.configFiles || ""),
    }))
    .filter((item) => item.name);
}

function parseComposeContainers(output: string) {
  const sections = output.split(/\r?\nMODES\r?\n/);
  const modeByID = new Map<string, string>();
  for (const line of (sections[1] || "").split(/\r?\n/).filter(Boolean)) {
    const [id = "", mode = ""] = line.split("\t");
    if (id) {
      modeByID.set(id, mode || "default");
      modeByID.set(id.slice(0, 12), mode || "default");
    }
  }
  return output
    .split(/\r?\nMODES\r?\n/)[0]
    .split(/\r?\n/)
    .filter(Boolean)
    .map((line) => {
      const [id = "", name = "", service = "", image = "", statusText = "", ports = "", networksText = ""] = line.split("\t");
      return {
        id,
        name,
        service: service || name,
        image,
        status: statusText,
        ports,
        networks: networksText,
        networkMode: modeByID.get(id) || modeByID.get(id.slice(0, 12)) || "default",
      };
    })
    .filter((item) => item.id && item.name);
}

async function refreshContainers() {
  const output = await command(
    "docker ps -a --format '{{.ID}}\\t{{.Names}}\\t{{.Image}}\\t{{.Status}}\\t{{.Ports}}\\t{{.Networks}}'; printf '\\nMODES\\n'; for id in $(docker ps -aq); do mode=$(docker inspect -f '{{.HostConfig.NetworkMode}}' \"$id\" 2>/dev/null) || continue; printf '%s\\t%s\\n' \"$id\" \"$mode\"; done",
  );
  containers.value = parseContainers(output);
}

async function refreshContainerStats() {
  const output = await command(
    "docker stats --no-stream --format '{{.ID}}\\t{{.Name}}\\t{{.CPUPerc}}\\t{{.MemUsage}}\\t{{.MemPerc}}\\t{{.NetIO}}\\t{{.BlockIO}}\\t{{.PIDs}}' 2>/dev/null",
  );
  const resourceByID = parseContainerStats(output);
  containers.value = containers.value.map((item) => ({
    ...item,
    resource: resourceByID.get(item.id) || resourceByID.get(item.name),
  }));
  monitorUpdatedAt.value = new Date().toLocaleTimeString("zh-CN", { hour12: false });
}

async function refreshMonitorOverview() {
  const output = await command(
    "docker info --format '{{.ServerVersion}}\\t{{.Driver}}\\t{{.Containers}}\\t{{.ContainersRunning}}\\t{{.ContainersPaused}}\\t{{.ContainersStopped}}\\t{{.Images}}'; printf '\\nDF\\n'; docker system df --format '{{.Type}}\\t{{.TotalCount}}\\t{{.Active}}\\t{{.Size}}\\t{{.Reclaimable}}' 2>/dev/null",
  );
  monitorOverview.value = parseMonitorOverview(output);
}

async function refreshMonitoring() {
  if (!containers.value.length) await refreshContainers();
  else await refreshContainerStats();
  await refreshMonitorOverview();
}

async function refreshCompose() {
  const output = await command("docker compose ls --all --format json");
  projects.value = parseProjects(output);
  for (const project of projects.value) {
    if (composeExpanded[project.name]) await loadComposeServices(project);
  }
}

async function loadComposeServices(project: ComposeProject) {
  composeLoading[project.name] = true;
  try {
    const output = await command(
      `${composeCommand(project)} ps -a --format '{{.ID}}\\t{{.Name}}\\t{{.Service}}\\t{{.Image}}\\t{{.Status}}\\t{{.Ports}}\\t{{.Networks}}'; printf '\\nMODES\\n'; for id in $(${composeCommand(project)} ps -aq); do mode=$(docker inspect -f '{{.HostConfig.NetworkMode}}' "$id" 2>/dev/null) || continue; printf '%s\\t%s\\n' "$id" "$mode"; done`,
    );
    composeServices[project.name] = parseComposeContainers(output);
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : `读取 ${project.name} 容器失败`);
  } finally {
    composeLoading[project.name] = false;
  }
}

async function toggleComposeProject(project: ComposeProject) {
  composeExpanded[project.name] = !composeExpanded[project.name];
  if (composeExpanded[project.name] && !composeServices[project.name])
    await loadComposeServices(project);
}

async function refreshImages() {
  const output = await command(
    "docker images --format '{{.ID}}\\t{{.Repository}}\\t{{.Tag}}\\t{{.Size}}\\t{{.CreatedSince}}'; printf '\\nUSAGE\\n'; for id in $(docker ps -aq); do image_id=$(docker inspect -f '{{.Image}}' \"$id\" 2>/dev/null) || continue; container_name=$(docker inspect -f '{{.Name}}' \"$id\" 2>/dev/null) || continue; printf '%s\\t%s\\n' \"$image_id\" \"$container_name\"; done",
  );
  images.value = parseImages(output);
}

async function refreshNetworks() {
  const output = await command(
    "docker network ls --format '{{.ID}}\\t{{.Name}}\\t{{.Driver}}\\t{{.Scope}}'",
  );
  networks.value = parseNetworks(output);
}

function parseNetworkDetail(raw: any): DockerNetworkDetail {
  const config = raw.IPAM?.Config?.[0] || {};
  return {
    id: String(raw.Id || ""),
    name: String(raw.Name || ""),
    driver: String(raw.Driver || ""),
    scope: String(raw.Scope || ""),
    subnet: String(config.Subnet || ""),
    gateway: String(config.Gateway || ""),
    dns: String(raw.Options?.dns || "").split(/[ ,]+/).filter(Boolean),
    containers: Object.entries(raw.Containers || {}).map(([id, value]: [string, any]) => ({
      id,
      name: String(value?.Name || "").replace(/^\//, ""),
      ip: String(value?.IPv4Address || "").split("/")[0],
      mac: String(value?.MacAddress || ""),
    })),
  };
}

async function showNetworkDetails(item: DockerNetwork) {
  networkDetailOpen.value = true;
  networkDetailLoading.value = true;
  networkDetail.value = undefined;
  networkContainerName.value = "";
  try {
    const raw = parseJson<any[]>(await command(`docker network inspect ${shellQuote(item.id)}`), []);
    if (!raw[0]) throw new Error("网络详情为空");
    networkDetail.value = parseNetworkDetail(raw[0]);
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "网络详情读取失败");
  } finally {
    networkDetailLoading.value = false;
  }
}

async function connectNetworkContainer() {
  const detail = networkDetail.value;
  const target = networkContainerName.value.trim();
  if (!detail || !target) return ElMessage.warning("请输入容器名称或 ID");
  busy.value = `network-connect:${detail.id}`;
  try {
    await command(`docker network connect ${shellQuote(detail.id)} ${shellQuote(target)}`);
    ElMessage.success("容器已连接到网络");
    await showNetworkDetails({ id: detail.id, name: detail.name, driver: detail.driver, scope: detail.scope });
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "连接网络失败");
  } finally {
    busy.value = "";
  }
}

async function disconnectNetworkContainer(item: { name: string }) {
  const detail = networkDetail.value;
  if (!detail) return;
  try {
    await ElMessageBox.confirm(`从网络“${detail.name}”断开容器“${item.name}”？`, "断开网络", {
      confirmButtonText: "断开",
      cancelButtonText: "取消",
      type: "warning",
    });
    busy.value = `network-disconnect:${detail.id}`;
    await command(`docker network disconnect ${shellQuote(detail.id)} ${shellQuote(item.name)}`);
    ElMessage.success("容器已断开网络");
    await showNetworkDetails({ id: detail.id, name: detail.name, driver: detail.driver, scope: detail.scope });
  } catch (cause) {
    if (cause !== "cancel" && cause !== "close")
      ElMessage.error(cause instanceof Error ? cause.message : "断开网络失败");
  } finally {
    busy.value = "";
  }
}

function parseImageDetail(raw: any): DockerImageDetail {
  return {
    id: String(raw.Id || "").replace(/^sha256:/, ""),
    created: String(raw.Created || ""),
    architecture: String(raw.Architecture || ""),
    os: String(raw.Os || ""),
    size: `${Math.round(Number(raw.Size || 0) / 1024 / 1024)} MB`,
    entrypoint: valueList(raw.Config?.Entrypoint).join(" "),
    command: valueList(raw.Config?.Cmd).join(" "),
    workingDir: String(raw.Config?.WorkingDir || ""),
    env: valueList(raw.Config?.Env),
    layers: valueList(raw.RootFS?.Layers),
    repoDigests: valueList(raw.RepoDigests),
  };
}

async function showImageDetails(item: DockerImage) {
  imageDetailOpen.value = true;
  imageDetailLoading.value = true;
  imageDetail.value = undefined;
  try {
    const raw = parseJson<any[]>(await command(`docker image inspect ${shellQuote(item.id)}`), []);
    if (!raw[0]) throw new Error("镜像详情为空");
    imageDetail.value = parseImageDetail(raw[0]);
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "镜像详情读取失败");
  } finally {
    imageDetailLoading.value = false;
  }
}

async function refresh() {
  if (!props.host) return;
  loading.value = true;
  error.value = "";
  try {
    await ensureConnected();
    if (activeTab.value === "containers") await refreshContainers();
    else if (activeTab.value === "compose") await refreshCompose();
    else if (activeTab.value === "images") await refreshImages();
    else if (activeTab.value === "networks") await refreshNetworks();
    else if (activeTab.value === "config") await loadDockerConfig();
    else await refreshMonitoring();
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "Docker 连接失败";
  } finally {
    loading.value = false;
  }
}

async function runContainerAction(item: DockerContainer) {
  const action = isRunning(item.status) ? "stop" : "start";
  busy.value = `${action}:${item.id}`;
  try {
    await command(`docker ${action} ${shellQuote(item.id)}`);
    ElMessage.success(`${item.name} 已${action === "start" ? "启动" : "停止"}`);
    await refreshContainers();
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "操作失败");
  } finally {
    busy.value = "";
  }
}

async function restartContainer(item: DockerContainer) {
  busy.value = `restart:${item.id}`;
  try {
    await command(`docker restart ${shellQuote(item.id)}`);
    ElMessage.success(`${item.name} 已重启`);
    await refreshContainers();
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "重启失败");
  } finally {
    busy.value = "";
  }
}

function valueList(value: unknown) {
  return Array.isArray(value) ? value.map((item) => String(item)) : [];
}

function parseContainerDetail(raw: any): DockerContainerDetail {
  const networks = Object.entries(raw.NetworkSettings?.Networks || {}).map(([name, value]: [string, any]) => ({
    name,
    ip: String(value?.IPAddress || ""),
    gateway: String(value?.Gateway || ""),
    mac: String(value?.MacAddress || ""),
  }));
  return {
    id: String(raw.Id || ""),
    name: String(raw.Name || "").replace(/^\//, ""),
    created: String(raw.Created || ""),
    image: String(raw.Image || ""),
    command: [raw.Path, ...valueList(raw.Args)].filter(Boolean).join(" "),
    entrypoint: valueList(raw.Config?.Entrypoint).join(" "),
    env: valueList(raw.Config?.Env),
    mounts: Array.isArray(raw.Mounts)
      ? raw.Mounts.map((item: any) => ({
          source: String(item.Source || ""),
          destination: String(item.Destination || ""),
          mode: String(item.Mode || "rw"),
        }))
      : [],
    labels: Object.entries(raw.Config?.Labels || {}).map(([key, value]) => ({ key, value: String(value) })),
    networks,
    restartPolicy: String(raw.HostConfig?.RestartPolicy?.Name || "no"),
  };
}

async function showContainerDetails(item: DockerContainer) {
  containerDetailOpen.value = true;
  containerDetailLoading.value = true;
  containerDetail.value = undefined;
  try {
    const raw = parseJson<any[]>(await command(`docker inspect ${shellQuote(item.id)}`), []);
    if (!raw[0]) throw new Error("容器详情为空");
    containerDetail.value = parseContainerDetail(raw[0]);
    containerDetailPolicy.value = containerDetail.value.restartPolicy;
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "容器详情读取失败");
  } finally {
    containerDetailLoading.value = false;
  }
}

async function renameContainer() {
  const detail = containerDetail.value;
  if (!detail) return;
  try {
    const { value } = await ElMessageBox.prompt("输入新的容器名称", "重命名容器", {
      confirmButtonText: "保存",
      cancelButtonText: "取消",
      inputValue: detail.name,
      inputPattern: /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/,
      inputErrorMessage: "只能使用字母、数字、点、下划线和短横线",
    });
    if (value.trim() === detail.name) return;
    busy.value = `rename:${detail.id}`;
    await command(`docker rename ${shellQuote(detail.id)} ${shellQuote(value.trim())}`);
    ElMessage.success("容器已重命名");
    await refreshContainers();
    detail.name = value.trim();
  } catch (cause) {
    if (cause !== "cancel" && cause !== "close")
      ElMessage.error(cause instanceof Error ? cause.message : "容器重命名失败");
  } finally {
    busy.value = "";
  }
}

async function updateContainerPolicy() {
  const detail = containerDetail.value;
  if (!detail) return;
  busy.value = `policy:${detail.id}`;
  try {
    await command(`docker update --restart=${shellQuote(containerDetailPolicy.value)} ${shellQuote(detail.id)}`);
    detail.restartPolicy = containerDetailPolicy.value;
    ElMessage.success("重启策略已更新");
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "重启策略更新失败");
  } finally {
    busy.value = "";
  }
}

async function removeContainer(item: DockerContainer) {
  try {
    await ElMessageBox.confirm(`删除容器“${item.name}”？`, "删除 Docker 容器", {
      confirmButtonText: "删除",
      cancelButtonText: "取消",
      type: "warning",
    });
    busy.value = `remove:${item.id}`;
    await command(`docker rm ${shellQuote(item.id)}`);
    ElMessage.success("容器已删除");
    await refreshContainers();
  } catch (cause) {
    if (cause !== "cancel" && cause !== "close")
      ElMessage.error(cause instanceof Error ? cause.message : "删除失败");
  } finally {
    busy.value = "";
  }
}

async function showLogs(item: DockerContainer) {
  logsTitle.value = `日志 · ${item.name}`;
  logs.value = "";
  logsTarget.value = item;
  logsFollow.value = true;
  logsOpen.value = true;
  startLogsPolling();
  await refreshLogs();
}

async function refreshLogs() {
  const item = logsTarget.value;
  if (!item) return;
  logsLoading.value = true;
  try {
    logs.value = await command(`docker logs --tail 300 ${shellQuote(item.id)} 2>&1`);
  } catch (cause) {
    logs.value = cause instanceof Error ? cause.message : "日志读取失败";
  } finally {
    logsLoading.value = false;
  }
}

function stopLogsPolling() {
  if (logsTimer !== undefined) window.clearInterval(logsTimer);
  logsTimer = undefined;
}

function startLogsPolling() {
  stopLogsPolling();
  if (!logsFollow.value) return;
  logsTimer = window.setInterval(() => void refreshLogs(), 2000);
}

function downloadLogs() {
  const name = logsTarget.value?.name || "container";
  const blob = new Blob([logs.value], { type: "text/plain;charset=utf-8" });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `${name}-logs.txt`;
  link.click();
  URL.revokeObjectURL(link.href);
}

async function pullImage() {
  const image = imageToPull.value.trim();
  if (!image) return ElMessage.warning("请输入镜像名称");
  busy.value = `pull:${image}`;
  try {
    await command(`docker pull ${shellQuote(image)}`);
    imageToPull.value = "";
    ElMessage.success("镜像拉取完成");
    await refreshImages();
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "镜像拉取失败");
  } finally {
    busy.value = "";
  }
}

async function pruneImages() {
  try {
    await ElMessageBox.confirm(
      "仅清理未被容器使用的未标记镜像，不会删除正在使用的镜像。继续吗？",
      "清理未标记镜像",
      { confirmButtonText: "清理", cancelButtonText: "取消", type: "warning" },
    );
    busy.value = "prune-images";
    const output = await command("docker image prune -f");
    ElMessage.success(output.includes("Total reclaimed space: 0B") ? "没有可清理的未标记镜像" : "未标记镜像清理完成");
    await refreshImages();
  } catch (cause) {
    if (cause !== "cancel" && cause !== "close")
      ElMessage.error(cause instanceof Error ? cause.message : "镜像清理失败");
  } finally {
    busy.value = "";
  }
}

async function pruneUnusedImages() {
  try {
    await ElMessageBox.confirm(
      "将清理所有未被容器使用的镜像，包括带标签的旧镜像。继续吗？",
      "清理未使用镜像",
      { confirmButtonText: "清理", cancelButtonText: "取消", type: "warning" },
    );
    busy.value = "prune-unused-images";
    const output = await command("docker image prune -a -f");
    ElMessage.success(output.includes("Total reclaimed space: 0B") ? "没有可清理的未使用镜像" : "未使用镜像清理完成");
    await refreshImages();
  } catch (cause) {
    if (cause !== "cancel" && cause !== "close")
      ElMessage.error(cause instanceof Error ? cause.message : "镜像清理失败");
  } finally {
    busy.value = "";
  }
}

async function pruneBuildCache() {
  try {
    await ElMessageBox.confirm(
      "将清理 Docker 当前未使用的构建缓存，不会删除镜像和容器。继续吗？",
      "清理构建缓存",
      { confirmButtonText: "清理", cancelButtonText: "取消", type: "warning" },
    );
    busy.value = "prune-build-cache";
    const output = await command("docker builder prune -f");
    ElMessage.success(output.includes("Total reclaimed space: 0B") ? "没有可清理的构建缓存" : "构建缓存清理完成");
  } catch (cause) {
    if (cause !== "cancel" && cause !== "close")
      ElMessage.error(cause instanceof Error ? cause.message : "构建缓存清理失败");
  } finally {
    busy.value = "";
  }
}

async function removeImage(image: DockerImage) {
  if (image.inUse)
    return ElMessage.warning(`镜像正在被容器使用：${image.containerNames.join("、")}`);
  try {
    await ElMessageBox.confirm(`删除镜像“${image.repository}:${image.tag}”？`, "删除 Docker 镜像", {
      confirmButtonText: "删除",
      cancelButtonText: "取消",
      type: "warning",
    });
    busy.value = `image:${image.id}`;
    await command(`docker rmi ${shellQuote(image.id)}`);
    ElMessage.success("镜像已删除");
    await refreshImages();
  } catch (cause) {
    if (cause !== "cancel" && cause !== "close")
      ElMessage.error(cause instanceof Error ? cause.message : "删除失败");
  } finally {
    busy.value = "";
  }
}

async function runCompose(project: ComposeProject, action: "restart" | "stop" | "update" | "rebuild") {
  const labels = { restart: "重启", stop: "停止", update: "更新", rebuild: "重建" };
  const compose = composeCommand(project);
  const commands = {
    restart: `${compose} restart`,
    stop: `${compose} stop`,
    update: `${compose} pull && ${compose} up -d --build`,
    rebuild: `${compose} up -d --build --force-recreate`,
  };
  if (action === "update" || action === "rebuild") {
    try {
      await ElMessageBox.confirm(
        `${labels[action]}编排项目“${project.name}”，这可能会重建并替换运行中的容器，继续吗？`,
        `${labels[action]}编排`,
        { confirmButtonText: "继续", cancelButtonText: "取消", type: "warning" },
      );
    } catch {
      return;
    }
  }
  busy.value = `${action}:${project.name}`;
  try {
    await command(commands[action]);
    ElMessage.success(`编排项目已${labels[action]}`);
    await refreshCompose();
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : `${labels[action]}失败`);
  } finally {
    busy.value = "";
  }
}

async function runComposeService(project: ComposeProject, item: ComposeContainer, action: "toggle" | "restart") {
  const service = item.service || item.name;
  const running = isRunning(item.status);
  const commandText =
    action === "restart"
      ? `${composeCommand(project)} restart ${shellQuote(service)}`
      : running
        ? `${composeCommand(project)} stop ${shellQuote(service)}`
        : `${composeCommand(project)} up -d ${shellQuote(service)}`;
  const label = action === "restart" ? "重启" : running ? "停止" : "启动";
  busy.value = `service:${project.name}:${service}`;
  try {
    await command(commandText);
    ElMessage.success(`${service} 已${label}`);
    await loadComposeServices(project);
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : `${label}失败`);
  } finally {
    busy.value = "";
  }
}

function composeCommand(project: ComposeProject) {
  const files = project.configFiles
    .split(/[,;]/)
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => `-f ${shellQuote(item)}`)
    .join(" ");
  return `docker compose ${files} -p ${shellQuote(project.name)}`;
}

function projectConfigPath(project: ComposeProject) {
  return project.configFiles.split(/[,;]/)[0]?.trim() || "";
}

function composeCommandWithFirstFile(project: ComposeProject, firstFile: string) {
  const files = project.configFiles
    .split(/[,;]/)
    .map((item) => item.trim())
    .filter(Boolean);
  files[0] = firstFile;
  return `docker compose ${files.map((item) => `-f ${shellQuote(item)}`).join(" ")} -p ${shellQuote(project.name)}`;
}

async function editCompose(project: ComposeProject) {
  const path = projectConfigPath(project);
  if (!path) return ElMessage.warning("当前编排没有可识别的配置文件");
  composeEditorProject.value = project;
  composeEditorPath.value = path;
  composeEditorText.value = "";
  composeEditorOpen.value = true;
  composeEditorLoading.value = true;
  try {
    const encoded = await command(`base64 < ${shellQuote(path)} | tr -d '\\n'`);
    composeEditorText.value = base64Decode(encoded);
  } catch (cause) {
    composeEditorText.value = cause instanceof Error ? cause.message : "配置读取失败";
  } finally {
    composeEditorLoading.value = false;
  }
}

async function saveCompose() {
  if (!composeEditorPath.value) return;
  composeEditorSaving.value = true;
  try {
    const data = base64Encode(composeEditorText.value);
    const path = shellQuote(composeEditorPath.value);
    const validationPath = `${composeEditorPath.value}.velin-validate`;
    const validationCommand = composeEditorProject.value
      ? composeCommandWithFirstFile(composeEditorProject.value, validationPath)
      : `docker compose -f ${shellQuote(validationPath)}`;
    await command(`TMP=${shellQuote(validationPath)}; DATA=${shellQuote(data)}; printf '%s' "$DATA" | base64 -d > "$TMP"; CODE=0; ${validationCommand} config --quiet || CODE=$?; rm -f "$TMP"; exit $CODE`);
    await command(`DATA=${shellQuote(data)}; printf '%s' "$DATA" | base64 -d | (tee ${path} >/dev/null || sudo tee ${path} >/dev/null)`);
    ElMessage.success("编排配置已保存");
    composeEditorOpen.value = false;
    await refreshCompose();
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? `配置校验或保存失败：${cause.message}` : "配置校验或保存失败");
  } finally {
    composeEditorSaving.value = false;
  }
}

function configValue(value: unknown) {
  return Array.isArray(value) ? value.join("\n") : typeof value === "string" ? value : "";
}

function syncConfigFields() {
  const parsed = parseJson<Record<string, any>>(configRaw.value, {});
  registryMirrors.value = configValue(parsed["registry-mirrors"]);
  insecureRegistries.value = configValue(parsed["insecure-registries"]);
  dockerHTTPProxy.value = typeof parsed.proxies?.["http-proxy"] === "string" ? parsed.proxies["http-proxy"] : "";
  dockerHTTPSProxy.value = typeof parsed.proxies?.["https-proxy"] === "string" ? parsed.proxies["https-proxy"] : "";
  dockerNoProxy.value = configValue(parsed.proxies?.["no-proxy"]);
  logDriver.value = typeof parsed["log-driver"] === "string" ? parsed["log-driver"] : "";
  logMaxSize.value = String(parsed["log-opts"]?.["max-size"] || "");
  liveRestore.value = parsed["live-restore"] === true;
}

function applyConfigFields() {
  let parsed: Record<string, any>;
  try {
    parsed = JSON.parse(configRaw.value) as Record<string, any>;
  } catch {
    ElMessage.error("Docker 配置不是有效 JSON");
    return;
  }
  const lines = (value: string) => value.split(/\r?\n|,/).map((item) => item.trim()).filter(Boolean);
  const mirrors = lines(registryMirrors.value);
  const insecure = lines(insecureRegistries.value);
  if (mirrors.length) parsed["registry-mirrors"] = mirrors;
  else delete parsed["registry-mirrors"];
  if (insecure.length) parsed["insecure-registries"] = insecure;
  else delete parsed["insecure-registries"];
  const httpProxy = dockerHTTPProxy.value.trim();
  const httpsProxy = dockerHTTPSProxy.value.trim();
  const noProxy = lines(dockerNoProxy.value);
  if (httpProxy || httpsProxy || noProxy.length) {
    parsed.proxies = { ...(parsed.proxies || {}) };
    if (httpProxy) parsed.proxies["http-proxy"] = httpProxy;
    else delete parsed.proxies["http-proxy"];
    if (httpsProxy) parsed.proxies["https-proxy"] = httpsProxy;
    else delete parsed.proxies["https-proxy"];
    if (noProxy.length) parsed.proxies["no-proxy"] = noProxy.join(",");
    else delete parsed.proxies["no-proxy"];
    if (!Object.keys(parsed.proxies).length) delete parsed.proxies;
  } else {
    delete parsed.proxies;
  }
  if (logDriver.value.trim()) parsed["log-driver"] = logDriver.value.trim();
  else delete parsed["log-driver"];
  if (logMaxSize.value.trim()) parsed["log-opts"] = { ...(parsed["log-opts"] || {}), "max-size": logMaxSize.value.trim() };
  else if (parsed["log-opts"]) {
    delete parsed["log-opts"]["max-size"];
    if (!Object.keys(parsed["log-opts"]).length) delete parsed["log-opts"];
  }
  if (liveRestore.value) parsed["live-restore"] = true;
  else delete parsed["live-restore"];
  configRaw.value = JSON.stringify(parsed, null, 2);
}

async function loadDockerConfig() {
  configLoading.value = true;
  try {
    const path = shellQuote(configPath.value.trim() || "/etc/docker/daemon.json");
    const output = await command(`CONFIG_PATH=${path}; printf 'DATA\\t'; if [ -r "$CONFIG_PATH" ]; then base64 < "$CONFIG_PATH" | tr -d '\\n'; fi; printf '\\n'`);
    const line = output.split(/\r?\n/).find((item) => item.startsWith("DATA\t"));
    const encoded = line?.slice(5).trim() || "";
    configRaw.value = encoded ? base64Decode(encoded) : "{}";
    try {
      configRaw.value = JSON.stringify(JSON.parse(configRaw.value), null, 2);
    } catch {
      configRaw.value = configRaw.value || "{}";
    }
    syncConfigFields();
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "Docker 配置读取失败";
  } finally {
    configLoading.value = false;
  }
}

async function saveDockerConfig() {
  let parsed: unknown;
  try {
    parsed = JSON.parse(configRaw.value);
  } catch {
    return ElMessage.error("Docker 配置不是有效 JSON");
  }
  configSaving.value = true;
  try {
    const data = base64Encode(JSON.stringify(parsed, null, 2) + "\n");
    const path = shellQuote(configPath.value.trim() || "/etc/docker/daemon.json");
    await command(`DATA=${shellQuote(data)}; printf '%s' "$DATA" | base64 -d | (tee ${path} >/dev/null || sudo tee ${path} >/dev/null)`);
    configRaw.value = JSON.stringify(parsed, null, 2);
    syncConfigFields();
    ElMessage.success("Docker 配置已保存，请重启 Docker 服务使其生效");
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "Docker 配置保存失败");
  } finally {
    configSaving.value = false;
  }
}

async function restartDocker() {
  try {
    await ElMessageBox.confirm("重启 Docker 服务会中断当前容器，继续吗？", "重启 Docker", {
      confirmButtonText: "重启",
      cancelButtonText: "取消",
      type: "warning",
    });
  } catch {
    return;
  }
  configRestarting.value = true;
  try {
    await command("sudo systemctl restart docker || sudo service docker restart");
    ElMessage.success("Docker 服务已重启");
    await refresh();
  } catch (cause) {
    ElMessage.error(cause instanceof Error ? cause.message : "Docker 服务重启失败");
  } finally {
    configRestarting.value = false;
  }
}

async function removeNetwork(network: DockerNetwork) {
  if (["bridge", "host", "none"].includes(network.name))
    return ElMessage.warning("Docker 默认网络不能删除");
  try {
    await ElMessageBox.confirm(`删除网络“${network.name}”？`, "删除 Docker 网络", {
      confirmButtonText: "删除",
      cancelButtonText: "取消",
      type: "warning",
    });
    busy.value = `network:${network.id}`;
    await command(`docker network rm ${shellQuote(network.id)}`);
    ElMessage.success("网络已删除");
    await refreshNetworks();
  } catch (cause) {
    if (cause !== "cancel" && cause !== "close")
      ElMessage.error(cause instanceof Error ? cause.message : "网络删除失败");
  } finally {
    busy.value = "";
  }
}

function switchTab(tab: DockerTab) {
  activeTab.value = tab;
  if (tab === "monitor") startMonitorPolling();
  else stopMonitorPolling();
  void refresh();
}

function stopMonitorPolling() {
  if (monitorTimer !== undefined) window.clearInterval(monitorTimer);
  monitorTimer = undefined;
}

function startMonitorPolling() {
  stopMonitorPolling();
  monitorTimer = window.setInterval(() => {
    if (!props.modelValue || activeTab.value !== "monitor") return;
    void refreshMonitoring().catch(() => {});
  }, 5000);
}

onBeforeUnmount(() => {
  stopMonitorPolling();
  stopLogsPolling();
});

watch(logsFollow, (follow) => {
  if (!logsOpen.value) return;
  if (follow) startLogsPolling();
  else stopLogsPolling();
});
watch(logsOpen, (open) => {
  if (open) startLogsPolling();
  else stopLogsPolling();
});

watch(
  () => props.modelValue,
  (open) => {
    if (!open) {
      stopMonitorPolling();
      return;
    }
    activeTab.value = "containers";
    containers.value = [];
    projects.value = [];
    Object.keys(composeServices).forEach((key) => delete composeServices[key]);
    Object.keys(composeExpanded).forEach((key) => delete composeExpanded[key]);
    Object.keys(composeLoading).forEach((key) => delete composeLoading[key]);
    images.value = [];
    networks.value = [];
    monitorOverview.value = undefined;
    monitorUpdatedAt.value = "";
    status.value = undefined;
    error.value = "";
    stopMonitorPolling();
    void refresh();
  },
);
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    class="docker-dialog"
    :title="`Docker 管理 · ${host?.name || '当前终端'}`"
    width="min(1120px, calc(100vw - 28px))"
    append-to-body
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="docker-heading">
      <div class="docker-summary">
        <Box :size="18" />
        <span v-if="activeTab === 'containers'">{{ containers.length }} 个容器 · {{ runningCount }} 个运行中</span>
        <span v-else-if="activeTab === 'compose'">{{ projects.length }} 个编排项目</span>
        <span v-else-if="activeTab === 'images'">{{ images.length }} 个镜像</span>
        <span v-else-if="activeTab === 'networks'">{{ networks.length }} 个网络</span>
        <span v-else-if="activeTab === 'monitor'">Docker 实时监控</span>
        <span v-else>Docker daemon 配置</span>
      </div>
      <div class="docker-heading-actions">
        <el-button :icon="LogIn" :disabled="!connected || tabLoading" @click="dockerLoginOpen = true">登录 Docker</el-button>
        <el-button :icon="RefreshCw" :loading="tabLoading" @click="refresh">刷新</el-button>
      </div>
    </div>

    <el-tabs :model-value="activeTab" class="docker-tabs" @tab-change="switchTab">
      <el-tab-pane label="容器" name="containers">
        <div v-loading="loading" class="docker-list">
          <div v-if="!loading && !error && !containers.length" class="docker-empty">
            <Box :size="28" /><span>没有 Docker 容器</span>
          </div>
          <article v-for="item in containers" :key="item.id" class="docker-row">
            <div class="docker-row-status" :class="{ running: isRunning(item.status) }"></div>
            <div class="docker-row-main">
              <strong>{{ item.name }}</strong>
              <span>{{ item.image }} · {{ item.status }}</span>
              <small>端口 {{ item.ports || '无' }} · 网络 {{ item.networks || '无' }} · 类型 {{ item.networkMode === 'default' ? 'bridge' : item.networkMode }}</small>
            </div>
            <div class="docker-row-actions">
              <el-button
                text
                size="small"
                :disabled="Boolean(busy)"
                :icon="isRunning(item.status) ? CircleStop : CirclePlay"
                @click="runContainerAction(item)"
              >{{ isRunning(item.status) ? '停止' : '启动' }}</el-button>
              <el-button text size="small" :disabled="Boolean(busy) || !isRunning(item.status)" :icon="RotateCw" @click="restartContainer(item)">重启</el-button>
              <el-button text size="small" :disabled="Boolean(busy)" :icon="FileText" @click="showLogs(item)">日志</el-button>
              <el-button text size="small" :disabled="Boolean(busy) || !isRunning(item.status)" :icon="TerminalSquare" @click="emit('terminal', { id: item.id, name: item.name })">终端</el-button>
              <el-button text size="small" :disabled="Boolean(busy)" :icon="Info" @click="showContainerDetails(item)">详情</el-button>
              <el-button text size="small" class="docker-danger" :disabled="Boolean(busy) || isRunning(item.status)" :icon="Trash2" @click="removeContainer(item)">删除</el-button>
            </div>
          </article>
        </div>
      </el-tab-pane>

      <el-tab-pane label="编排" name="compose">
        <div v-loading="loading" class="docker-list">
          <div v-if="!loading && !error && !projects.length" class="docker-empty">
            <Box :size="28" /><span>没有运行中的 Docker Compose 项目</span>
          </div>
          <div v-for="project in projects" :key="project.name" class="compose-project">
            <article class="docker-row compose-row">
              <button
                class="compose-expander"
                :class="{ expanded: composeExpanded[project.name] }"
                :aria-label="composeExpanded[project.name] ? '收起容器' : '展开容器'"
                @click="toggleComposeProject(project)"
              >
                <ChevronRight :size="16" />
              </button>
              <div class="docker-row-status" :class="{ running: /up|running/i.test(project.status) }"></div>
              <div class="docker-row-main">
                <strong>{{ project.name }}</strong>
                <span>{{ project.status || '状态未知' }}</span>
                <small>{{ project.configFiles || '未返回配置文件路径' }}</small>
              </div>
              <div class="docker-row-actions">
                <el-button text size="small" :disabled="Boolean(busy)" :icon="Pencil" @click="editCompose(project)">配置</el-button>
                <el-button text size="small" :disabled="Boolean(busy)" :icon="RefreshCw" @click="runCompose(project, 'update')">更新</el-button>
                <el-button text size="small" :disabled="Boolean(busy)" :icon="RotateCw" @click="runCompose(project, 'rebuild')">重建</el-button>
                <el-button text size="small" :disabled="Boolean(busy)" :icon="CircleStop" @click="runCompose(project, 'stop')">停止</el-button>
              </div>
            </article>
            <div v-if="composeExpanded[project.name]" v-loading="composeLoading[project.name]" class="compose-children">
              <div
                v-if="!composeLoading[project.name] && !composeServices[project.name]?.length"
                class="compose-empty"
              >暂无内部容器</div>
              <article v-for="item in composeServices[project.name] || []" :key="item.id" class="docker-subrow">
                <div class="docker-row-status" :class="{ running: isRunning(item.status) }"></div>
                <div class="docker-row-main">
                  <strong>{{ item.service }} <small>{{ item.name }}</small></strong>
                  <span>{{ item.image }} · {{ item.status || '状态未知' }}</span>
                  <small>端口 {{ item.ports || '无' }} · 网络 {{ item.networks || '无' }} · 类型 {{ item.networkMode === 'default' ? 'bridge' : item.networkMode }}</small>
                </div>
                <div class="docker-row-actions">
                  <el-button
                    text
                    size="small"
                    :disabled="Boolean(busy)"
                    :icon="isRunning(item.status) ? CircleStop : CirclePlay"
                    @click="runComposeService(project, item, 'toggle')"
                  >{{ isRunning(item.status) ? '停止' : '启动' }}</el-button>
                  <el-button text size="small" :disabled="Boolean(busy)" :icon="RotateCw" @click="runComposeService(project, item, 'restart')">重启</el-button>
                  <el-button text size="small" :disabled="Boolean(busy)" :icon="FileText" @click="showLogs(item)">日志</el-button>
                  <el-button text size="small" :disabled="Boolean(busy) || !isRunning(item.status)" :icon="TerminalSquare" @click="emit('terminal', { id: item.id, name: item.name })">终端</el-button>
                </div>
              </article>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <el-tab-pane label="镜像" name="images">
        <div class="docker-inline-form">
          <el-input v-model="imageToPull" placeholder="例如 nginx:latest" @keyup.enter="pullImage" />
          <el-button type="primary" :loading="busy.startsWith('pull:')" @click="pullImage">拉取镜像</el-button>
          <el-button
            class="docker-clean-button"
            :disabled="Boolean(busy)"
            :loading="busy === 'prune-images'"
            :icon="Trash2"
            @click="pruneImages"
          >清理未标记镜像</el-button>
          <el-button
            class="docker-clean-button"
            :disabled="Boolean(busy)"
            :loading="busy === 'prune-unused-images'"
            :icon="Trash2"
            @click="pruneUnusedImages"
          >清理未使用镜像</el-button>
          <el-button
            class="docker-clean-button"
            :disabled="Boolean(busy)"
            :loading="busy === 'prune-build-cache'"
            :icon="Trash2"
            @click="pruneBuildCache"
          >清理构建缓存</el-button>
        </div>
        <div v-loading="loading" class="docker-list">
          <div v-if="!loading && !error && !images.length" class="docker-empty">
            <Box :size="28" /><span>没有 Docker 镜像</span>
          </div>
          <article
            v-for="image in images"
            :key="`${image.id}-${image.repository}-${image.tag}`"
            class="docker-row docker-image-row"
            :class="{ 'image-in-use': image.inUse }"
          >
            <div class="docker-row-main">
              <div class="docker-image-heading">
                <strong>{{ imageLabel(image) }}</strong>
                <span class="docker-image-usage" :class="{ active: image.inUse }">
                  {{ image.inUse ? '使用中' : '未使用' }}
                </span>
              </div>
              <span>{{ image.id }} · {{ image.size }}</span>
              <small>{{ image.created }}<template v-if="image.inUse"> · 关联容器：{{ image.containerNames.join('、') }}</template></small>
            </div>
            <div class="docker-row-actions">
              <el-button text size="small" class="docker-danger" :disabled="Boolean(busy) || image.inUse" :title="image.inUse ? `关联容器：${image.containerNames.join('、')}` : '删除镜像'" :icon="Trash2" @click="removeImage(image)">{{ image.inUse ? '使用中' : '删除' }}</el-button>
              <el-button text size="small" :disabled="Boolean(busy)" :icon="Info" @click="showImageDetails(image)">详情</el-button>
            </div>
          </article>
        </div>
      </el-tab-pane>

      <el-tab-pane label="网络" name="networks">
        <div v-loading="loading" class="docker-list">
          <div v-if="!loading && !error && !networks.length" class="docker-empty">
            <Box :size="28" /><span>没有 Docker 网络</span>
          </div>
          <article v-for="network in networks" :key="network.id" class="docker-row">
            <div class="docker-row-main">
              <strong>{{ network.name }}</strong>
              <span>{{ network.driver }} · {{ network.scope }}</span>
              <small>{{ network.id }}</small>
            </div>
            <div class="docker-row-actions">
              <el-button text size="small" :disabled="Boolean(busy)" :icon="Info" @click="showNetworkDetails(network)">详情</el-button>
              <el-button text size="small" class="docker-danger" :disabled="Boolean(busy) || ['bridge', 'host', 'none'].includes(network.name)" :icon="Trash2" @click="removeNetwork(network)">删除</el-button>
            </div>
          </article>
        </div>
      </el-tab-pane>

      <el-tab-pane label="配置" name="config">
        <div class="docker-config-toolbar">
          <el-input v-model="configPath" placeholder="Docker daemon.json 路径" />
          <el-button :icon="FolderOpen" :loading="configLoading" @click="loadDockerConfig">读取</el-button>
          <el-button :icon="Save" :loading="configSaving" @click="saveDockerConfig">保存</el-button>
          <el-button type="warning" :icon="RotateCw" :loading="configRestarting" @click="restartDocker">重启 Docker</el-button>
        </div>
        <div class="docker-config-grid">
          <section class="docker-config-panel">
            <div class="docker-panel-heading"><strong>常用配置</strong><span>修改后点击“应用到 JSON”</span></div>
            <label>镜像源 <el-input v-model="registryMirrors" type="textarea" :rows="3" placeholder="每行一个，例如 https://mirror.example.com" /></label>
            <label>不安全仓库 <el-input v-model="insecureRegistries" type="textarea" :rows="2" placeholder="每行一个，例如 registry.local:5000" /></label>
            <label>HTTP 代理 <el-input v-model="dockerHTTPProxy" placeholder="例如 http://127.0.0.1:7890" /></label>
            <label>HTTPS 代理 <el-input v-model="dockerHTTPSProxy" placeholder="例如 http://127.0.0.1:7890" /></label>
            <label>不代理地址 <el-input v-model="dockerNoProxy" placeholder="例如 localhost,127.0.0.1,.internal" /></label>
            <label>日志驱动 <el-input v-model="logDriver" placeholder="json-file" /></label>
            <label>日志大小 <el-input v-model="logMaxSize" placeholder="10m" /></label>
            <el-checkbox v-model="liveRestore">启用 live-restore</el-checkbox>
            <el-button size="small" @click="applyConfigFields">应用到 JSON</el-button>
          </section>
          <section class="docker-config-panel docker-config-json">
            <div class="docker-panel-heading"><strong>daemon.json</strong><span>支持编辑其他 Docker daemon 配置</span></div>
            <CodeEditor v-model="configRaw" language="json" height="min(58vh, 560px)" />
          </section>
        </div>
      </el-tab-pane>

      <el-tab-pane label="监控" name="monitor">
        <div v-loading="loading" class="docker-monitor">
          <div class="docker-monitor-cards">
            <div class="docker-monitor-card">
              <span>运行容器</span>
              <strong>{{ monitorOverview?.runningContainers || runningCount }}</strong>
              <small>共 {{ monitorOverview?.totalContainers || containers.length }} 个</small>
            </div>
            <div class="docker-monitor-card">
              <span>容器 CPU</span>
              <strong>{{ totalCPU }}%</strong>
              <small>当前运行容器合计</small>
            </div>
            <div class="docker-monitor-card">
              <span>镜像数量</span>
              <strong>{{ monitorOverview?.images || images.length || '—' }}</strong>
              <small>Docker 本地镜像</small>
            </div>
            <div class="docker-monitor-card">
              <span>采样时间</span>
              <strong>{{ monitorUpdatedAt || '等待采样' }}</strong>
              <small>每 5 秒更新</small>
            </div>
          </div>
          <div class="docker-monitor-meta">
            <span>Docker {{ monitorOverview?.serverVersion || '—' }}</span>
            <span>存储驱动 {{ monitorOverview?.driver || '—' }}</span>
            <span>暂停 {{ monitorOverview?.pausedContainers || '0' }} · 已停止 {{ monitorOverview?.stoppedContainers || '0' }}</span>
          </div>
          <div class="docker-monitor-grid">
            <section class="docker-monitor-panel">
              <div class="docker-panel-heading"><strong>容器资源</strong><span>{{ monitoredContainers.length }} 个正在采样</span></div>
              <div v-if="!containers.length" class="compose-empty">暂无容器</div>
              <article v-for="item in containers" :key="`monitor-${item.id}`" class="docker-monitor-row">
                <div class="docker-row-status" :class="{ running: isRunning(item.status) }"></div>
                <div class="docker-row-main">
                  <strong>{{ item.name }}</strong>
                  <span>{{ item.status || '状态未知' }}</span>
                </div>
                <div v-if="item.resource" class="docker-monitor-values">
                  <span>CPU {{ item.resource.cpu }}</span>
                  <span>内存 {{ item.resource.memory }} · {{ item.resource.memoryPercent }}</span>
                  <span>网络 {{ item.resource.network }}</span>
                  <span>块 I/O {{ item.resource.block }}</span>
                </div>
                <span v-else class="docker-monitor-idle">未运行</span>
              </article>
            </section>
            <section class="docker-monitor-panel">
              <div class="docker-panel-heading"><strong>磁盘占用</strong><span>Docker system df</span></div>
              <div v-if="!monitorOverview?.disk?.length" class="compose-empty">暂无磁盘统计</div>
              <div v-for="item in monitorOverview?.disk || []" :key="item.type" class="docker-disk-row">
                <strong>{{ item.type }}</strong>
                <span>{{ item.size }} · 可回收 {{ item.reclaimable }}</span>
                <small>{{ item.active }} / {{ item.total }} 活跃</small>
              </div>
            </section>
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>

    <div v-if="error" class="docker-error">
      <strong>Docker 操作失败</strong><span>{{ error }}</span>
    </div>
  </el-dialog>

  <el-drawer v-model="containerDetailOpen" class="docker-detail-drawer" title="容器详情" size="min(560px, calc(100vw - 24px))" append-to-body>
    <div v-loading="containerDetailLoading" class="docker-detail-content">
      <template v-if="containerDetail">
        <div class="docker-detail-title"><strong>{{ containerDetail.name }}</strong><span>{{ containerDetail.id.slice(0, 12) }}</span></div>
        <div class="docker-detail-actions">
          <el-button size="small" :icon="Pencil" :disabled="Boolean(busy)" @click="renameContainer">重命名</el-button>
          <el-select v-model="containerDetailPolicy" size="small" :disabled="Boolean(busy)" class="docker-policy-select">
            <el-option label="不自动重启" value="no" />
            <el-option label="总是重启" value="always" />
            <el-option label="停止后除外" value="unless-stopped" />
            <el-option label="失败时重启" value="on-failure" />
          </el-select>
          <el-button size="small" :loading="busy === `policy:${containerDetail.id}`" @click="updateContainerPolicy">保存策略</el-button>
        </div>
        <dl class="docker-detail-list">
          <div><dt>镜像</dt><dd>{{ containerDetail.image }}</dd></div>
          <div><dt>创建时间</dt><dd>{{ containerDetail.created }}</dd></div>
          <div><dt>启动命令</dt><dd class="mono">{{ containerDetail.command || '—' }}</dd></div>
          <div><dt>入口命令</dt><dd class="mono">{{ containerDetail.entrypoint || '—' }}</dd></div>
          <div><dt>重启策略</dt><dd>{{ containerDetail.restartPolicy }}</dd></div>
        </dl>
        <section class="docker-detail-section"><h3>环境变量</h3><pre>{{ containerDetail.env.join('\n') || '—' }}</pre></section>
        <section class="docker-detail-section"><h3>挂载目录</h3><div v-for="mount in containerDetail.mounts" :key="`${mount.source}-${mount.destination}`" class="docker-detail-line"><span>{{ mount.source }} → {{ mount.destination }}</span><small>{{ mount.mode }}</small></div><p v-if="!containerDetail.mounts.length">—</p></section>
        <section class="docker-detail-section"><h3>网络</h3><div v-for="network in containerDetail.networks" :key="network.name" class="docker-detail-line"><span>{{ network.name }} · {{ network.ip || '无 IP' }}</span><small>{{ network.gateway || '无网关' }}</small></div></section>
        <section class="docker-detail-section"><h3>标签</h3><div v-for="label in containerDetail.labels" :key="label.key" class="docker-detail-line"><span>{{ label.key }}</span><small>{{ label.value }}</small></div><p v-if="!containerDetail.labels.length">—</p></section>
      </template>
    </div>
  </el-drawer>

  <el-drawer v-model="networkDetailOpen" class="docker-detail-drawer" title="网络详情" size="min(560px, calc(100vw - 24px))" append-to-body>
    <div v-loading="networkDetailLoading" class="docker-detail-content">
      <template v-if="networkDetail">
        <div class="docker-detail-title"><strong>{{ networkDetail.name }}</strong><span>{{ networkDetail.driver }} · {{ networkDetail.scope }}</span></div>
        <dl class="docker-detail-list"><div><dt>子网</dt><dd>{{ networkDetail.subnet || '—' }}</dd></div><div><dt>网关</dt><dd>{{ networkDetail.gateway || '—' }}</dd></div><div><dt>DNS</dt><dd>{{ networkDetail.dns.join('、') || 'Docker 默认' }}</dd></div></dl>
        <div class="docker-network-connect"><el-input v-model="networkContainerName" size="small" placeholder="容器名称或 ID" @keyup.enter="connectNetworkContainer" /><el-button size="small" :disabled="Boolean(busy)" @click="connectNetworkContainer">连接容器</el-button></div>
        <section class="docker-detail-section"><h3>已连接容器</h3><div v-for="item in networkDetail.containers" :key="item.id" class="docker-detail-line"><span>{{ item.name }} · {{ item.ip || '无 IP' }} · {{ item.mac || '无 MAC' }}</span><el-button text size="small" class="docker-danger" :disabled="Boolean(busy)" @click="disconnectNetworkContainer(item)">断开</el-button></div><p v-if="!networkDetail.containers.length">暂无连接容器</p></section>
      </template>
    </div>
  </el-drawer>

  <el-drawer v-model="imageDetailOpen" class="docker-detail-drawer" title="镜像详情" size="min(560px, calc(100vw - 24px))" append-to-body>
    <div v-loading="imageDetailLoading" class="docker-detail-content">
      <template v-if="imageDetail">
        <div class="docker-detail-title"><strong>{{ imageDetail.id.slice(0, 12) }}</strong><span>{{ imageDetail.os }} / {{ imageDetail.architecture }}</span></div>
        <dl class="docker-detail-list"><div><dt>大小</dt><dd>{{ imageDetail.size }}</dd></div><div><dt>创建时间</dt><dd>{{ imageDetail.created }}</dd></div><div><dt>工作目录</dt><dd class="mono">{{ imageDetail.workingDir || '—' }}</dd></div><div><dt>入口命令</dt><dd class="mono">{{ imageDetail.entrypoint || '—' }}</dd></div><div><dt>默认命令</dt><dd class="mono">{{ imageDetail.command || '—' }}</dd></div></dl>
        <section class="docker-detail-section"><h3>环境变量</h3><pre>{{ imageDetail.env.join('\n') || '—' }}</pre></section>
        <section class="docker-detail-section"><h3>镜像层（{{ imageDetail.layers.length }}）</h3><pre>{{ imageDetail.layers.join('\n') || '—' }}</pre></section>
        <section class="docker-detail-section"><h3>仓库摘要</h3><pre>{{ imageDetail.repoDigests.join('\n') || '—' }}</pre></section>
      </template>
    </div>
  </el-drawer>

  <el-dialog v-model="logsOpen" class="docker-logs-dialog" :title="logsTitle" width="min(900px, calc(100vw - 28px))" append-to-body>
    <div class="docker-logs-toolbar"><el-checkbox v-model="logsFollow">自动刷新日志</el-checkbox><el-button text size="small" :icon="Download" @click="downloadLogs">下载</el-button></div>
    <pre v-loading="logsLoading" class="docker-logs">{{ logs }}</pre>
    <template #footer><el-button :icon="X" @click="logsOpen = false">关闭</el-button></template>
  </el-dialog>

  <el-dialog v-model="composeEditorOpen" class="docker-compose-editor" title="编辑编排配置" width="min(980px, calc(100vw - 28px))" append-to-body>
    <div class="docker-editor-path">{{ composeEditorPath }}</div>
    <div v-loading="composeEditorLoading">
      <CodeEditor v-model="composeEditorText" language="yaml" height="min(68vh, 720px)" />
    </div>
    <template #footer>
      <el-button @click="composeEditorOpen = false">取消</el-button>
      <el-button type="primary" :icon="Save" :loading="composeEditorSaving" @click="saveCompose">保存配置</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="dockerLoginOpen" class="docker-login-dialog" title="登录 Docker 仓库" width="min(460px, calc(100vw - 28px))" append-to-body @closed="dockerLoginPassword = ''">
    <el-form label-position="top" @submit.prevent="loginDocker">
      <el-form-item label="仓库地址">
        <el-input v-model="dockerLoginRegistry" placeholder="留空使用 Docker Hub" autocomplete="url" />
      </el-form-item>
      <el-form-item label="用户名">
        <el-input v-model="dockerLoginUsername" autocomplete="username" @keyup.enter="loginDocker" />
      </el-form-item>
      <el-form-item label="密码或访问令牌">
        <el-input v-model="dockerLoginPassword" type="password" show-password autocomplete="current-password" @keyup.enter="loginDocker" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="dockerLoginOpen = false">取消</el-button>
      <el-button type="primary" :icon="LogIn" :loading="dockerLoginLoading" @click="loginDocker">登录</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.docker-dialog {
  color: var(--text);
  font-family: Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
  letter-spacing: 0;
}
.docker-dialog :deep(.el-button),
.docker-dialog :deep(.el-input__inner),
.docker-dialog :deep(.el-textarea__inner) {
  font-family: inherit;
}
.docker-dialog :deep(.el-button) {
  font-size: 12px;
}
.docker-dialog :deep(.el-tabs__item) {
  height: 40px;
  font-size: 13px;
  font-weight: 560;
}
.docker-heading,
.docker-summary,
.docker-heading-actions,
.docker-row,
.docker-subrow,
.docker-row-actions,
.docker-inline-form,
.docker-config-toolbar,
.docker-panel-heading {
  display: flex;
  align-items: center;
}
.docker-heading {
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 2px;
}
.docker-heading-actions {
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}
.docker-summary {
  gap: 9px;
  color: var(--text-strong, #e7eee9);
  font-size: 14px;
  font-weight: 620;
}
.docker-tabs {
  margin-top: 4px;
}
.docker-list {
  min-height: 210px;
  border-top: 1px solid var(--line, #35403a);
}
.compose-project {
  border-bottom: 1px solid var(--line, #35403a);
}
.compose-project > .docker-row {
  border-bottom: 0;
}
.compose-expander {
  width: 24px;
  height: 28px;
  display: grid;
  flex: 0 0 24px;
  place-items: center;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--text-muted, #8f9d95);
  cursor: pointer;
}
.compose-expander:hover {
  color: var(--text-strong, #e7eee9);
}
.compose-expander svg {
  transition: transform 0.16s ease;
}
.compose-expander.expanded svg {
  transform: rotate(90deg);
}
.compose-children {
  margin-left: 28px;
  border-left: 1px solid var(--line, #35403a);
  background: var(--surface);
}
.compose-empty {
  padding: 13px 12px;
  color: var(--text-muted, #8f9d95);
  font-size: 12px;
}
.docker-subrow {
  min-height: 62px;
  gap: 10px;
  padding: 8px 10px;
  border-bottom: 1px solid var(--line, #35403a);
}
.docker-subrow:last-child {
  border-bottom: 0;
}
.docker-subrow .docker-row-main strong small {
  margin-left: 5px;
  color: var(--text-muted, #8f9d95);
  font: 11px var(--font-mono, monospace);
}
.docker-row {
  min-height: 82px;
  gap: 14px;
  padding: 12px 8px;
  border-bottom: 1px solid var(--line, #35403a);
}
.docker-row-status {
  width: 8px;
  height: 8px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: #8f9d95;
}
.docker-row-status.running {
  background: #5b8cff;
  box-shadow: 0 0 0 3px #5b8cff22;
}
.docker-row-main {
  min-width: 0;
  flex: 1;
  display: grid;
  gap: 5px;
}
.docker-row-main strong,
.docker-row-main span,
.docker-row-main small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docker-row-main strong {
  color: var(--text-strong, #e7eee9);
  font-size: 14px;
  font-weight: 620;
  line-height: 1.35;
}
.docker-image-row.image-in-use {
  background: #1d283d;
}
.docker-image-heading {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}
.docker-image-heading strong {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docker-row-main .docker-image-usage {
  flex: 0 0 auto;
  overflow: visible;
  padding: 2px 7px;
  border: 1px solid #66503b;
  border-radius: 10px;
  background: #30261e;
  color: #d4b486;
  font-size: 10px;
  line-height: 1.25;
  white-space: nowrap;
}
.docker-row-main .docker-image-usage.active {
  border-color: #49658f;
  background: #202d47;
  color: var(--accent-strong);
}
.docker-row-main span,
.docker-row-main small,
.docker-editor-path,
.docker-panel-heading span {
  color: var(--text-muted, #8f9d95);
  font-size: 12px;
  line-height: 1.4;
}
.docker-row-actions {
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 4px;
}
.docker-danger:not(:disabled) {
  color: #d98781;
}
.docker-danger:not(:disabled):hover {
  color: #ef8e86;
}
.docker-clean-button {
  color: #c9aa7c !important;
}
.docker-clean-button:not(:disabled):hover {
  color: #f0cf99 !important;
  background: #3a2c22 !important;
}
.docker-clean-button.is-loading,
.docker-clean-button:disabled {
  color: #8f7c65 !important;
}
.docker-empty {
  min-height: 210px;
  display: grid;
  place-items: center;
  align-content: center;
  gap: 8px;
  color: var(--text-muted, #8f9d95);
  font-size: 12px;
}
.docker-inline-form {
  flex-wrap: wrap;
  gap: 9px;
  margin: 7px 0 16px;
}
.docker-inline-form .el-input {
  flex: 1 1 240px;
  max-width: 440px;
}
.docker-config-toolbar {
  gap: 8px;
  margin: 7px 0 16px;
}
.docker-config-toolbar .el-input {
  min-width: 260px;
  flex: 1;
}
.docker-config-grid {
  display: grid;
  grid-template-columns: minmax(240px, 0.8fr) minmax(0, 1.5fr);
  gap: 14px;
}
.docker-config-panel {
  display: grid;
  align-content: start;
  gap: 14px;
  padding: 15px;
  border: 1px solid var(--line, #35403a);
  background: var(--surface-2, #1a211e);
}
.docker-panel-heading {
  justify-content: space-between;
  gap: 10px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--line, #35403a);
}
.docker-config-panel label {
  display: grid;
  gap: 5px;
  color: var(--text-muted, #8f9d95);
  font-size: 13px;
}
.docker-monitor {
  display: grid;
  gap: 14px;
}
.docker-monitor-cards {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}
.docker-monitor-card {
  min-width: 0;
  display: grid;
  gap: 6px;
  padding: 14px;
  border: 1px solid var(--line, #35403a);
  background: #17202a;
}
.docker-monitor-card span,
.docker-monitor-card small {
  color: var(--text-muted, #8f9d95);
  font-size: 12px;
}
.docker-monitor-card strong {
  overflow: hidden;
  color: #d9bd91;
  font-size: 20px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docker-monitor-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 18px;
  color: var(--text-muted, #8f9d95);
  font-family: inherit;
  font-size: 12px;
  line-height: 1.45;
}
.docker-monitor-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.25fr) minmax(260px, 0.75fr);
  gap: 14px;
}
.docker-monitor-panel {
  min-width: 0;
  border: 1px solid var(--line, #35403a);
  background: var(--surface-2, #1a211e);
}
.docker-monitor-panel .docker-panel-heading {
  margin: 0 12px;
  padding: 12px 0 9px;
}
.docker-monitor-row {
  min-height: 58px;
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 12px;
  border-top: 1px solid var(--line, #35403a);
}
.docker-monitor-row .docker-row-main {
  flex: 0 1 170px;
}
.docker-monitor-values {
  min-width: 0;
  flex: 1;
  display: grid;
  grid-template-columns: repeat(2, minmax(100px, 1fr));
  gap: 3px 12px;
  color: var(--text-soft);
  font: 11px/1.45 var(--font-mono, monospace);
}
.docker-monitor-values span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docker-monitor-idle {
  color: #8f7c65;
  font-size: 12px;
}
.docker-disk-row {
  display: grid;
  grid-template-columns: minmax(70px, 0.7fr) minmax(0, 1.3fr);
  gap: 3px 12px;
  padding: 10px 12px;
  border-top: 1px solid var(--line, #35403a);
}
.docker-disk-row strong {
  grid-row: span 2;
  color: var(--text-strong, #e7eee9);
  font-size: 12px;
}
.docker-disk-row span,
.docker-disk-row small {
  overflow: hidden;
  color: var(--text-muted, #8f9d95);
  font: 11px/1.45 var(--font-mono, monospace);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docker-editor-path {
  margin-bottom: 8px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docker-detail-content {
  display: grid;
  gap: 16px;
  padding-bottom: 10px;
}
.docker-detail-title {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--line, #35403a);
}
.docker-detail-title strong {
  min-width: 0;
  overflow: hidden;
  color: var(--text);
  font-size: 16px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docker-detail-title span {
  flex: 0 0 auto;
  color: var(--text-soft);
  font: 11px var(--font-mono, monospace);
}
.docker-detail-actions,
.docker-network-connect {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.docker-policy-select {
  width: 150px;
}
.docker-network-connect .el-input {
  min-width: 180px;
  flex: 1;
}
.docker-detail-list {
  display: grid;
  gap: 9px;
  margin: 0;
}
.docker-detail-list > div {
  display: grid;
  grid-template-columns: 76px minmax(0, 1fr);
  gap: 12px;
  align-items: start;
}
.docker-detail-list dt {
  color: var(--muted);
  font-size: 12px;
}
.docker-detail-list dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--text);
  font-size: 12px;
  line-height: 1.45;
}
.docker-detail-list dd.mono,
.docker-detail-section pre {
  font-family: var(--font-mono, monospace);
}
.docker-detail-section {
  display: grid;
  gap: 8px;
  margin: 0;
  padding-top: 12px;
  border-top: 1px solid var(--line, #35403a);
}
.docker-detail-section h3 {
  margin: 0;
  color: #d9bd91;
  font-size: 12px;
  font-weight: 650;
}
.docker-detail-section p {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
}
.docker-detail-section pre {
  max-height: 220px;
  margin: 0;
  overflow: auto;
  padding: 10px;
  background: var(--surface);
  color: var(--text);
  font-size: 11px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.docker-detail-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-width: 0;
  padding: 7px 0;
  border-bottom: 1px solid var(--line-strong);
  color: var(--text);
  font-size: 12px;
}
.docker-detail-line > span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docker-detail-line small {
  flex: 0 0 auto;
  color: var(--muted);
  font: 11px var(--font-mono, monospace);
}
.docker-logs-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}
.docker-logs {
  min-height: 280px;
  max-height: 60vh;
  margin: 0;
  overflow: auto;
  padding: 14px;
  background: var(--field-bg);
  color: var(--text);
  font: 12px/1.55 var(--font-mono, monospace);
  white-space: pre-wrap;
  word-break: break-word;
}
.docker-error {
  display: grid;
  gap: 3px;
  margin-top: 12px;
  padding: 10px 12px;
  border: 1px solid #784b47;
  background: #321f1e;
  color: #e6aaa4;
  font-size: 12px;
}
.docker-error strong {
  color: #f1c0ba;
}
@media (max-width: 720px) {
  .docker-config-grid {
    grid-template-columns: 1fr;
  }
  .docker-config-toolbar {
    flex-wrap: wrap;
  }
  .docker-config-toolbar .el-input {
    min-width: 100%;
  }
  .docker-row {
    align-items: flex-start;
  }
  .compose-children {
    margin-left: 16px;
  }
  .docker-subrow {
    align-items: flex-start;
  }
  .docker-row-actions {
    max-width: 180px;
  }
  .docker-monitor-cards {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .docker-monitor-grid {
    grid-template-columns: 1fr;
  }
  .docker-detail-list > div {
    grid-template-columns: 68px minmax(0, 1fr);
  }
  .docker-monitor-row {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .docker-monitor-row .docker-row-main {
    flex-basis: calc(100% - 22px);
  }
  .docker-monitor-values {
    flex-basis: 100%;
    margin-left: 17px;
  }
}
</style>
