<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { CornerDownLeft, Search } from "@lucide/vue";

export interface PaletteItem {
  id: string;
  group: string;
  title: string;
  hint?: string;
  keywords?: string;
  run: () => void;
}

const props = defineProps<{ modelValue: boolean; items: PaletteItem[] }>();
const emit = defineEmits<{ "update:modelValue": [value: boolean] }>();

const query = ref("");
const cursor = ref(0);
const input = ref<HTMLInputElement>();
const list = ref<HTMLElement>();

// Subsequence match with a small bonus for prefix / contiguous hits.
function score(item: PaletteItem, q: string) {
  if (!q) return 1;
  const hay = `${item.title} ${item.hint || ""} ${item.keywords || ""}`.toLowerCase();
  const direct = hay.indexOf(q);
  if (direct >= 0) return 100 - Math.min(direct, 60);
  let at = 0;
  for (const ch of q) {
    at = hay.indexOf(ch, at);
    if (at < 0) return 0;
    at++;
  }
  return 10;
}

const results = computed(() => {
  const q = query.value.trim().toLowerCase();
  const scored = props.items
    .map((item, order) => ({ item, order, s: score(item, q) }))
    .filter((entry) => entry.s > 0);
  if (q) scored.sort((a, b) => b.s - a.s || a.order - b.order);
  return scored.map((entry) => entry.item);
});

const grouped = computed(() => {
  const rows: Array<
    { type: "group"; label: string } | { type: "item"; item: PaletteItem; index: number }
  > = [];
  let last = "";
  results.value.forEach((item, index) => {
    if (item.group !== last) {
      rows.push({ type: "group", label: item.group });
      last = item.group;
    }
    rows.push({ type: "item", item, index });
  });
  return rows;
});

watch(
  () => props.modelValue,
  async (open) => {
    if (!open) return;
    query.value = "";
    cursor.value = 0;
    await nextTick();
    input.value?.focus();
  },
);
watch(query, () => (cursor.value = 0));

function close() {
  emit("update:modelValue", false);
}
function run(item?: PaletteItem) {
  if (!item) return;
  close();
  // Let the palette unmount (and return focus) before the action opens dialogs.
  setTimeout(item.run, 0);
}
function scrollToCursor() {
  nextTick(() =>
    list.value
      ?.querySelector<HTMLElement>(`[data-index="${cursor.value}"]`)
      ?.scrollIntoView({ block: "nearest" }),
  );
}
function onKey(event: KeyboardEvent) {
  const count = results.value.length;
  if (event.key === "Escape") {
    event.preventDefault();
    close();
  } else if (event.key === "ArrowDown" || (event.key === "n" && event.ctrlKey)) {
    event.preventDefault();
    if (count) cursor.value = (cursor.value + 1) % count;
    scrollToCursor();
  } else if (event.key === "ArrowUp" || (event.key === "p" && event.ctrlKey)) {
    event.preventDefault();
    if (count) cursor.value = (cursor.value - 1 + count) % count;
    scrollToCursor();
  } else if (event.key === "Enter" && !event.isComposing) {
    event.preventDefault();
    run(results.value[cursor.value]);
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="palette">
      <div
        v-if="modelValue"
        class="palette-backdrop"
        @mousedown.self="close"
      >
        <div
          class="palette"
          role="dialog"
          aria-modal="true"
          aria-label="命令面板"
          @keydown="onKey"
        >
          <label class="palette-input">
            <Search :size="16" />
            <input
              ref="input"
              v-model="query"
              placeholder="搜索主机、标签页或操作…"
              spellcheck="false"
              autocomplete="off"
              role="combobox"
              aria-expanded="true"
              aria-controls="palette-list"
            />
            <kbd>Esc</kbd>
          </label>
          <div id="palette-list" ref="list" class="palette-list" role="listbox">
            <template v-for="row in grouped" :key="row.type === 'group' ? `g-${row.label}` : row.item.id">
              <div v-if="row.type === 'group'" class="palette-group">
                {{ row.label }}
              </div>
              <button
                v-else
                class="palette-item"
                :class="{ active: row.index === cursor }"
                role="option"
                :aria-selected="row.index === cursor"
                :data-index="row.index"
                @mousemove="cursor = row.index"
                @click="run(row.item)"
              >
                <span class="palette-title">{{ row.item.title }}</span>
                <span v-if="row.item.hint" class="palette-hint">{{ row.item.hint }}</span>
                <CornerDownLeft v-if="row.index === cursor" :size="13" class="palette-enter" />
              </button>
            </template>
            <div v-if="!results.length" class="palette-empty">没有匹配的结果</div>
          </div>
          <footer class="palette-foot">
            <span><kbd>↑</kbd><kbd>↓</kbd> 选择</span>
            <span><kbd>Enter</kbd> 执行</span>
            <span><kbd>Esc</kbd> 关闭</span>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style>
.palette-backdrop {
  position: fixed;
  inset: 0;
  z-index: 4000;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: min(16vh, 120px) 16px 16px;
  background: var(--overlay-bg);
}
.palette {
  display: flex;
  flex-direction: column;
  width: min(560px, 100%);
  max-height: min(480px, 70dvh);
  overflow: hidden;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-lg);
  background: var(--surface);
  box-shadow: var(--shadow-float);
}
.palette-input {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 0 0 auto;
  padding: 0 14px;
  height: 48px;
  border-bottom: 1px solid var(--line);
  color: var(--muted);
}
.palette-input input {
  flex: 1;
  min-width: 0;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--text);
  font-size: 14px;
}
.palette-input input::placeholder {
  color: var(--placeholder);
}
.palette kbd {
  min-width: 20px;
  padding: 1px 6px;
  border: 1px solid var(--line-strong);
  border-radius: 5px;
  background: var(--surface-2);
  color: var(--muted);
  font: 10.5px/16px var(--font-mono);
  text-align: center;
}
.palette-list {
  flex: 1 1 auto;
  min-height: 0;
  padding: 6px;
  overflow: auto;
  overscroll-behavior: contain;
}
.palette-group {
  padding: 10px 10px 4px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
}
.palette-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-height: 34px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-soft);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}
.palette-item.active {
  background: var(--surface-3);
  color: var(--text);
  box-shadow: inset 2px 0 var(--accent);
}
.palette-title {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.palette-hint {
  flex: 1 1 0;
  min-width: 0;
  overflow: hidden;
  color: var(--muted);
  font: 11px var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.palette-enter {
  flex: 0 0 auto;
  margin-left: auto;
  color: var(--muted);
}
.palette-empty {
  padding: 28px 0;
  color: var(--muted);
  font-size: 13px;
  text-align: center;
}
.palette-foot {
  display: flex;
  gap: 16px;
  flex: 0 0 auto;
  padding: 8px 14px;
  border-top: 1px solid var(--line);
  color: var(--muted);
  font-size: 11px;
}
.palette-foot kbd {
  margin-right: 3px;
}
.palette-enter-active,
.palette-leave-active {
  transition: opacity 0.12s var(--ease-ui);
}
.palette-enter-active .palette {
  animation: pro-pop 0.16s var(--ease-ui) both;
}
.palette-enter-from,
.palette-leave-to {
  opacity: 0;
}
@media (max-width: 760px) {
  .palette-backdrop {
    padding-top: 8vh;
  }
  .palette-foot {
    display: none;
  }
}
</style>
