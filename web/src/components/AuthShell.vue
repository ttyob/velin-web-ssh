<script setup lang="ts">
import { TerminalSquare } from "@lucide/vue";

defineProps<{ title: string; subtitle?: string }>();
</script>

<template>
  <main class="auth-page auth-surface" aria-labelledby="auth-title">
    <div class="auth-decoration" aria-hidden="true"><div class="auth-orbits"></div></div>
    <section class="auth-content">
      <div class="auth-panel">
        <header class="auth-heading">
          <div class="auth-brand">
            <TerminalSquare class="auth-brand-mark" :size="23" aria-hidden="true" />
            <span class="auth-wordmark">velin</span>
          </div>
          <h1 id="auth-title">{{ title }}</h1>
        </header>
        <p v-if="subtitle" class="auth-subtitle">{{ subtitle }}</p>
        <slot />
      </div>
      <footer v-if="$slots.footer" class="auth-footer">
        <slot name="footer" />
      </footer>
    </section>
  </main>
</template>
