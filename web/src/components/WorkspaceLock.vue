<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { LockKeyhole } from "@lucide/vue";

const props = defineProps<{ busy: boolean; error: string }>();
const emit = defineEmits<{ unlock: [pin: string]; logout: []; 'clear-error': [] }>();
const root = ref<HTMLElement>();
const input = ref<{ focus: () => void }>();
const expanded = ref(false);
const pin = ref("");
const now = ref(new Date());
const clock = computed(() => {
  const date = now.value;
  const seconds = date.getSeconds();
  const minutes = date.getMinutes() + seconds / 60;
  return {
    hour: (date.getHours() % 12 + minutes / 60) * 30,
    minute: minutes * 6,
    second: seconds * 6,
    time: `${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`,
    iso: date.toISOString(),
  };
});
let clockTimer: ReturnType<typeof setInterval> | undefined;
function updateTime() {
  now.value = new Date();
}

function expand() {
  if (expanded.value) return;
  emit('clear-error');
  expanded.value = true;
  root.value?.focus({ preventScroll: true });
}
function collapse() {
  if (props.busy) return;
  expanded.value = false;
  pin.value = "";
  emit('clear-error');
  root.value?.focus({ preventScroll: true });
}
function focusStage() {
  if (expanded.value) input.value?.focus();
  else root.value?.focus({ preventScroll: true });
}
function activateStage(element: Element) {
  element.removeAttribute("inert");
}
function leaveStage(element: Element) {
  // Outgoing controls must not receive focus while the new stage appears.
  element.setAttribute("inert", "");
}
function submit() {
  if (!props.busy && /^\d{6}$/.test(pin.value)) emit("unlock", pin.value);
}
function handleKey(event: KeyboardEvent) {
  if (event.key === "Escape" && expanded.value) {
    event.preventDefault();
    void collapse();
  } else if (!expanded.value && (event.key === "Enter" || event.key === " ")) {
    event.preventDefault();
    void expand();
  } else if (event.key === "Tab") {
    // Keep keyboard navigation inside the modal, including Shift+Tab.
    const focusable = [...root.value!.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)')]
      .filter(element => !element.closest('[inert]') && element.getClientRects().length > 0);
    if (!focusable.length) {
      event.preventDefault();
      return;
    }
    const first = focusable[0], last = focusable.at(-1);
    if (event.shiftKey && (document.activeElement === first || document.activeElement === root.value)) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && (document.activeElement === last || document.activeElement === root.value)) {
      event.preventDefault();
      first?.focus();
    }
  }
}
async function resetPIN() {
  pin.value = "";
  await nextTick();
  if (expanded.value) input.value?.focus();
}
defineExpose({ resetPIN });
onMounted(() => {
  root.value?.focus({ preventScroll: true });
  updateTime();
  clockTimer = setInterval(updateTime, 1000);
  document.addEventListener("visibilitychange", updateTime);
  window.addEventListener("focus", updateTime);
});
onBeforeUnmount(() => {
  clearInterval(clockTimer);
  document.removeEventListener("visibilitychange", updateTime);
  window.removeEventListener("focus", updateTime);
});
</script>

<template>
  <section ref="root" class="system-lock auth-surface" :class="{ 'is-expanded': expanded }"
    aria-modal="true" role="dialog" aria-label="工作区已锁定" tabindex="-1"
    @keydown="handleKey" @click.self="!expanded && expand()">
    <Transition name="lock-stage" mode="out-in" @before-enter="activateStage"
      @before-leave="leaveStage" @leave-cancelled="activateStage" @after-enter="focusStage">
      <button v-if="!expanded" key="cover" class="system-lock-cover" type="button" aria-label="点击解锁" aria-describedby="workspace-lock-time" @click="expand">
        <svg class="system-lock-clock" viewBox="0 0 240 240" aria-hidden="true">
          <circle class="lock-clock-rim" cx="120" cy="120" r="110" />
          <g class="lock-clock-ticks" stroke-linecap="round">
            <line v-for="tick in 60" :key="tick" x1="120" y1="18" x2="120"
              :y2="tick % 5 === 0 ? 29 : 22" :class="{ 'is-hour': tick % 5 === 0 }"
              :transform="`rotate(${tick * 6} 120 120)`" />
          </g>
          <g class="lock-clock-numerals" text-anchor="middle" dominant-baseline="central">
            <text x="120" y="45">12</text><text x="196" y="120">3</text>
            <text x="120" y="195">6</text><text x="44" y="120">9</text>
          </g>
          <line class="lock-clock-hour" x1="120" y1="126" x2="120" y2="66" :transform="`rotate(${clock.hour} 120 120)`" />
          <line class="lock-clock-minute" x1="120" y1="128" x2="120" y2="45" :transform="`rotate(${clock.minute} 120 120)`" />
          <line class="lock-clock-second" x1="120" y1="138" x2="120" y2="36" :transform="`rotate(${clock.second} 120 120)`" />
          <circle class="lock-clock-pivot" cx="120" cy="120" r="3" />
        </svg>
        <time id="workspace-lock-time" class="system-lock-time" :datetime="clock.iso">{{ clock.time }}</time>
        <span class="system-lock-status">
          <LockKeyhole class="system-lock-symbol" :size="14" :stroke-width="1.5" aria-hidden="true" />
          <span>工作区已锁定</span>
        </span>
        <span class="system-lock-prompt">
          <span class="lock-prompt-indicator" aria-hidden="true"></span>
          <span>点击屏幕解锁</span>
        </span>
      </button>
      <div v-else key="pin" class="system-lock-entry">
        <form class="system-lock-panel" aria-labelledby="workspace-unlock-title" @submit.prevent="submit">
          <LockKeyhole class="system-lock-symbol" :size="28" :stroke-width="1.4" aria-hidden="true" />
          <h1 id="workspace-unlock-title" class="system-lock-title">解锁工作区</h1>
          <p class="system-lock-caption">输入 6 位 PIN 继续</p>
          <label class="system-lock-input-label" for="workspace-unlock-pin">锁屏 PIN</label>
          <el-input id="workspace-unlock-pin" ref="input" :model-value="pin" type="password"
            maxlength="6" inputmode="numeric" autocomplete="off" :readonly="busy"
            @update:model-value="pin = $event.replace(/\D/g, '').slice(0, 6); emit('clear-error')" />
          <p v-if="error" class="system-lock-error" role="alert">{{ error }}</p>
          <el-button native-type="submit" type="primary" :loading="busy" :disabled="pin.length !== 6 || busy">解锁工作台</el-button>
          <div class="system-lock-actions">
            <button type="button" class="auth-text-button" :disabled="busy" @click="collapse">返回锁屏</button>
            <button type="button" class="auth-text-button" :disabled="busy" @click="emit('logout')">切换账号</button>
          </div>
        </form>
      </div>
    </Transition>
  </section>
</template>
