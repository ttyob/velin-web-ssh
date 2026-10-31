import { createApp } from "vue";
import { createPinia } from "pinia";
import ElementPlus from "element-plus";
import "element-plus/dist/index.css";
import "@xterm/xterm/css/xterm.css";
import App from "./App.vue";
import router from "./router";
import { applyAccent, applyInterfaceTheme } from "./themePresets";
import "./styles.css";
import "./styles-refresh.css";
import "./styles-polish.css";
import "./styles-settings.css";
import "./styles-dialogs.css";
import "./styles-tools.css";
import "./styles-perf.css";
import "./styles-motion.css";
import "./styles-auth.css";
import { installScrollIdle } from "./scrollIdle";

try {
  applyInterfaceTheme(localStorage.getItem("velin-interface-theme") || "dark");
  const accent = localStorage.getItem("velin-accent-color");
  if (accent) applyAccent(accent);
} catch {}

installScrollIdle();

createApp(App).use(createPinia()).use(router).use(ElementPlus).mount("#app");
