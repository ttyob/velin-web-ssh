<script setup lang="ts">
import BrandLogo from './BrandLogo.vue';

defineProps<{ title: string; subtitle?: string }>();
</script>

<template>
  <main class="auth-page auth-surface" aria-labelledby="auth-title">
    <div class="auth-decoration" aria-hidden="true"><div class="auth-orbits"></div></div>
    <section class="auth-content">
      <div class="auth-panel">
        <header class="auth-heading">
          <div class="auth-brand">
            <BrandLogo class="auth-brand-mark" :size="30" tone="white" />
            <span class="auth-wordmark">Jeex Terminal</span>
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
