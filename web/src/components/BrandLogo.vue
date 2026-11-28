<script setup lang="ts">
import whiteLogo from '../assets/brand/logo-white.png';
import blackLogo from '../assets/brand/logo-black.png';

withDefaults(defineProps<{ size?: number; tone?: 'auto' | 'white' | 'black' }>(), { size: 24, tone: 'auto' });
</script>

<template>
  <span class="jeex-logo" :class="`jeex-logo--${tone}`" :style="{ width: `${size}px`, height: `${size}px` }" aria-hidden="true">
    <img class="jeex-logo-white" :src="whiteLogo" alt="" draggable="false" />
    <img class="jeex-logo-black" :src="blackLogo" alt="" draggable="false" />
  </span>
</template>

<style scoped>
.jeex-logo { display: inline-grid; flex: 0 0 auto; place-items: center; vertical-align: middle; }
.jeex-logo img { display: block; grid-area: 1 / 1; width: 100%; height: 100%; object-fit: contain; }
.jeex-logo .jeex-logo-black { display: none; }
:root[data-interface-theme="light"] .jeex-logo--auto .jeex-logo-white,
.jeex-logo--black .jeex-logo-white { display: none; }
:root[data-interface-theme="light"] .jeex-logo--auto .jeex-logo-black,
.jeex-logo--black .jeex-logo-black { display: block; }
</style>
