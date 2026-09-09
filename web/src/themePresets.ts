export interface AccentPreset {
  id: string;
  name: string;
  value: string;
  strong: string;
}

export interface InterfaceThemePreset {
  id: "dark" | "vscode-dark" | "graphite" | "light";
  name: string;
  description: string;
  accent: string;
  accentStrong: string;
  colors: {
    bg: string;
    surface: string;
    surface2: string;
    surface3: string;
    hover: string;
    line: string;
    lineStrong: string;
    text: string;
    textSoft: string;
    muted: string;
    sidebar: string;
    sidebarHeader: string;
    tabbar: string;
    tabHover: string;
    tabActive: string;
    workspace: string;
    terminalToolbar: string;
    terminalToolbarFocus: string;
    split: string;
    field: string;
    fieldHover: string;
    fieldLine: string;
    fieldLineHover: string;
    menu: string;
    footer: string;
    overlay: string;
    shadow: string;
    placeholder: string;
  };
}

export const interfaceThemePresets: InterfaceThemePreset[] = [
  {
    id: "dark",
    name: "Slate Blue",
    description: "石墨黑底与冷蓝焦点",
    accent: "#5b8cff",
    accentStrong: "#9bb7ff",
    colors: {
      bg: "#111318",
      surface: "#181b22",
      surface2: "#22262f",
      surface3: "#2b303a",
      hover: "#252a34",
      line: "#303641",
      lineStrong: "#4b5568",
      text: "#e3e7ee",
      textSoft: "#bbc3d1",
      muted: "#8d96a5",
      sidebar: "#171a21",
      sidebarHeader: "#1b1f27",
      tabbar: "#15181e",
      tabHover: "#20252e",
      tabActive: "#252a34",
      workspace: "#0e1117",
      terminalToolbar: "#151922",
      terminalToolbarFocus: "#1c222d",
      split: "#303844",
      field: "#12161d",
      fieldHover: "#181e27",
      fieldLine: "#515c70",
      fieldLineHover: "#707c92",
      menu: "#1c2028",
      footer: "#15181e",
      overlay: "#05070bb8",
      shadow: "#05070b8c",
      placeholder: "#737d8d",
    },
  },
  {
    id: "vscode-dark",
    name: "VS Code Dark+",
    description: "默认深色工作台与经典蓝色焦点",
    accent: "#007acc",
    accentStrong: "#4daafc",
    colors: {
      bg: "#1e1e1e",
      surface: "#252526",
      surface2: "#2d2d30",
      surface3: "#333333",
      hover: "#2a2d2e",
      line: "#3c3c3c",
      lineStrong: "#565656",
      text: "#cccccc",
      textSoft: "#b9b9b9",
      muted: "#858585",
      sidebar: "#252526",
      sidebarHeader: "#252526",
      tabbar: "#181818",
      tabHover: "#2a2d2e",
      tabActive: "#1e1e1e",
      workspace: "#1e1e1e",
      terminalToolbar: "#252526",
      terminalToolbarFocus: "#2d2d30",
      split: "#3c3c3c",
      field: "#3c3c3c",
      fieldHover: "#454545",
      fieldLine: "#555555",
      fieldLineHover: "#707070",
      menu: "#252526",
      footer: "#181818",
      overlay: "#00000099",
      shadow: "#00000080",
      placeholder: "#8b8b8b",
    },
  },
  {
    id: "graphite",
    name: "Graphite",
    description: "中性灰黑，克制的高对比界面",
    accent: "#a0c4ff",
    accentStrong: "#bfd7ff",
    colors: {
      bg: "#151617",
      surface: "#1d1f21",
      surface2: "#24272a",
      surface3: "#2d3034",
      hover: "#292c2f",
      line: "#34383c",
      lineStrong: "#50555a",
      text: "#e4e6e8",
      textSoft: "#bec2c6",
      muted: "#858b91",
      sidebar: "#1b1d1f",
      sidebarHeader: "#202225",
      tabbar: "#191b1d",
      tabHover: "#26292c",
      tabActive: "#2a2d30",
      workspace: "#111213",
      terminalToolbar: "#1b1d1f",
      terminalToolbarFocus: "#24272a",
      split: "#34383c",
      field: "#17191b",
      fieldHover: "#202326",
      fieldLine: "#494e53",
      fieldLineHover: "#686e74",
      menu: "#222426",
      footer: "#191b1d",
      overlay: "#050505b8",
      shadow: "#0000008c",
      placeholder: "#737980",
    },
  },
  {
    id: "light",
    name: "Paper Light",
    description: "清爽浅色框架，终端配色保持独立",
    accent: "#2879b9",
    accentStrong: "#1769aa",
    colors: {
      bg: "#f3f5f8",
      surface: "#ffffff",
      surface2: "#f5f7fb",
      surface3: "#e9edf5",
      hover: "#edf1f8",
      line: "#d8dee9",
      lineStrong: "#b7c1d1",
      text: "#20242b",
      textSoft: "#46505e",
      muted: "#758092",
      sidebar: "#f7f9fc",
      sidebarHeader: "#ffffff",
      tabbar: "#e9edf4",
      tabHover: "#f3f5f8",
      tabActive: "#ffffff",
      workspace: "#d8dee8",
      terminalToolbar: "#242a33",
      terminalToolbarFocus: "#2c3542",
      split: "#aeb8c5",
      field: "#ffffff",
      fieldHover: "#f8faff",
      fieldLine: "#b9c3d0",
      fieldLineHover: "#8491a4",
      menu: "#ffffff",
      footer: "#f4f6fa",
      overlay: "#18202c66",
      shadow: "#27324733",
      placeholder: "#8993a3",
    },
  },
];

export interface TerminalThemePreset {
  id: string;
  name: string;
  background: string;
  foreground: string;
  cursor: string;
  selectionBackground: string;
  colors: string[];
}

export const accentPresets: AccentPreset[] = [
  { id: "blue", name: "冷蓝", value: "#5b8cff", strong: "#9bb7ff" },
  { id: "cyan", name: "青蓝", value: "#55bfc5", strong: "#78d7da" },
  { id: "sky", name: "海蓝", value: "#6da7d9", strong: "#91c1e8" },
  { id: "amber", name: "琥珀", value: "#d6aa5f", strong: "#e7c27d" },
  { id: "coral", name: "珊瑚", value: "#dd7d73", strong: "#ee9b91" },
  { id: "violet", name: "柔紫", value: "#a58ad8", strong: "#bea8e8" },
];

export const terminalThemePresets: TerminalThemePreset[] = [
  {
    id: "velin",
    name: "VelinWebSSH Dark",
    background: "#111318",
    foreground: "#d8deea",
    cursor: "#8eafff",
    selectionBackground: "#30466f",
    colors: [
      "#202426",
      "#e27770",
      "#6f9cff",
      "#deb96e",
      "#73a8d4",
      "#bd8fcb",
      "#66bcc2",
      "#d8ded9",
      "#626b67",
      "#ef9189",
      "#a5bcff",
      "#e8cb8c",
      "#91bde0",
      "#cea9da",
      "#85d0d4",
      "#f2f5f3",
    ],
  },
  {
    id: "dracula",
    name: "Dracula",
    background: "#282a36",
    foreground: "#f8f8f2",
    cursor: "#f8f8f2",
    selectionBackground: "#44475a",
    colors: [
      "#21222c",
      "#ff5555",
      "#50fa7b",
      "#f1fa8c",
      "#bd93f9",
      "#ff79c6",
      "#8be9fd",
      "#f8f8f2",
      "#6272a4",
      "#ff6e6e",
      "#69ff94",
      "#ffffa5",
      "#d6acff",
      "#ff92df",
      "#a4ffff",
      "#ffffff",
    ],
  },
  {
    id: "one-dark",
    name: "One Dark",
    background: "#282c34",
    foreground: "#abb2bf",
    cursor: "#528bff",
    selectionBackground: "#3e4451",
    colors: [
      "#282c34",
      "#e06c75",
      "#98c379",
      "#e5c07b",
      "#61afef",
      "#c678dd",
      "#56b6c2",
      "#abb2bf",
      "#5c6370",
      "#e06c75",
      "#98c379",
      "#e5c07b",
      "#61afef",
      "#c678dd",
      "#56b6c2",
      "#ffffff",
    ],
  },
  {
    id: "tokyo-night",
    name: "Tokyo Night",
    background: "#1a1b26",
    foreground: "#c0caf5",
    cursor: "#c0caf5",
    selectionBackground: "#33467c",
    colors: [
      "#15161e",
      "#f7768e",
      "#9ece6a",
      "#e0af68",
      "#7aa2f7",
      "#bb9af7",
      "#7dcfff",
      "#a9b1d6",
      "#414868",
      "#f7768e",
      "#9ece6a",
      "#e0af68",
      "#7aa2f7",
      "#bb9af7",
      "#7dcfff",
      "#c0caf5",
    ],
  },
  {
    id: "nord",
    name: "Nord",
    background: "#2e3440",
    foreground: "#d8dee9",
    cursor: "#88c0d0",
    selectionBackground: "#434c5e",
    colors: [
      "#3b4252",
      "#bf616a",
      "#a3be8c",
      "#ebcb8b",
      "#81a1c1",
      "#b48ead",
      "#88c0d0",
      "#e5e9f0",
      "#4c566a",
      "#bf616a",
      "#a3be8c",
      "#ebcb8b",
      "#81a1c1",
      "#b48ead",
      "#8fbcbb",
      "#eceff4",
    ],
  },
  {
    id: "solarized-dark",
    name: "Solarized Dark",
    background: "#002b36",
    foreground: "#839496",
    cursor: "#93a1a1",
    selectionBackground: "#073642",
    colors: [
      "#073642",
      "#dc322f",
      "#859900",
      "#b58900",
      "#268bd2",
      "#d33682",
      "#2aa198",
      "#eee8d5",
      "#002b36",
      "#cb4b16",
      "#586e75",
      "#657b83",
      "#839496",
      "#6c71c4",
      "#93a1a1",
      "#fdf6e3",
    ],
  },
  {
    id: "github-dark",
    name: "GitHub Dark",
    background: "#0d1117",
    foreground: "#c9d1d9",
    cursor: "#58a6ff",
    selectionBackground: "#264f78",
    colors: [
      "#484f58",
      "#ff7b72",
      "#3fb950",
      "#d29922",
      "#58a6ff",
      "#bc8cff",
      "#39c5cf",
      "#b1bac4",
      "#6e7681",
      "#ffa198",
      "#56d364",
      "#e3b341",
      "#79c0ff",
      "#d2a8ff",
      "#56d4dd",
      "#f0f6fc",
    ],
  },
];

export function findAccent(value: string) {
  return (
    accentPresets.find((preset) => preset.value === value) || accentPresets[0]
  );
}

export function findInterfaceTheme(id: string) {
  return (
    interfaceThemePresets.find((preset) => preset.id === id) ||
    interfaceThemePresets[0]
  );
}

export function applyInterfaceTheme(id: string) {
  const preset = findInterfaceTheme(id);
  const root = document.documentElement;
  root.dataset.interfaceTheme = preset.id;
  const style = root.style;
  const values: Record<string, string> = {
    "--bg": preset.colors.bg,
    "--surface": preset.colors.surface,
    "--surface-2": preset.colors.surface2,
    "--surface-3": preset.colors.surface3,
    "--surface-hover": preset.colors.hover,
    "--line": preset.colors.line,
    "--line-strong": preset.colors.lineStrong,
    "--text": preset.colors.text,
    "--text-soft": preset.colors.textSoft,
    "--muted": preset.colors.muted,
    "--sidebar-bg": preset.colors.sidebar,
    "--sidebar-header-bg": preset.colors.sidebarHeader,
    "--tabbar-bg": preset.colors.tabbar,
    "--tab-hover-bg": preset.colors.tabHover,
    "--tab-active-bg": preset.colors.tabActive,
    "--workspace-bg": preset.colors.workspace,
    "--terminal-toolbar-bg": preset.colors.terminalToolbar,
    "--terminal-toolbar-focus-bg": preset.colors.terminalToolbarFocus,
    "--split-bg": preset.colors.split,
    "--field-bg": preset.colors.field,
    "--field-bg-hover": preset.colors.fieldHover,
    "--field-line": preset.colors.fieldLine,
    "--field-line-hover": preset.colors.fieldLineHover,
    "--menu-bg": preset.colors.menu,
    "--dialog-footer-bg": preset.colors.footer,
    "--overlay-bg": preset.colors.overlay,
    "--theme-shadow": preset.colors.shadow,
    "--placeholder": preset.colors.placeholder,
    "--accent-contrast":
      preset.id === "vscode-dark" || preset.id === "light"
        ? "#ffffff"
        : "#111318",
  };
  for (const [name, value] of Object.entries(values))
    style.setProperty(name, value);
  style.setProperty("--shadow-float", `0 18px 50px ${preset.colors.shadow}`);
  style.setProperty("--el-mask-color", preset.colors.overlay);
  try {
    localStorage.setItem("velin-interface-theme", preset.id);
  } catch {}
}

export function findTerminalTheme(id: string) {
  return (
    terminalThemePresets.find((preset) => preset.id === id) ||
    terminalThemePresets[0]
  );
}

export function applyAccent(value: string) {
  const preset = findAccent(value);
  const root = document.documentElement.style;
  root.setProperty("--accent", preset.value);
  root.setProperty("--accent-strong", preset.strong);
  root.setProperty("--accent-soft", `${preset.value}24`);
  root.setProperty("--accent-surface", `${preset.value}14`);
  root.setProperty("--accent-border", `${preset.value}70`);
  root.setProperty("--accent-shadow", `${preset.value}38`);
  root.setProperty("--el-color-primary", preset.value);
  root.setProperty("--el-color-primary-light-3", preset.strong);
  root.setProperty("--el-color-primary-light-5", `${preset.value}b8`);
  root.setProperty("--el-color-primary-light-7", `${preset.value}70`);
  root.setProperty("--el-color-primary-light-8", `${preset.value}42`);
  root.setProperty("--el-color-primary-light-9", `${preset.value}24`);
  root.setProperty("--el-color-primary-dark-2", preset.strong);
  try {
    localStorage.setItem("velin-accent-color", preset.value);
  } catch {}
}

export function terminalXtermTheme(id: string) {
  const preset = findTerminalTheme(id);
  const [
    black,
    red,
    green,
    yellow,
    blue,
    magenta,
    cyan,
    white,
    brightBlack,
    brightRed,
    brightGreen,
    brightYellow,
    brightBlue,
    brightMagenta,
    brightCyan,
    brightWhite,
  ] = preset.colors;
  return {
    background: preset.background,
    foreground: preset.foreground,
    cursor: preset.cursor,
    cursorAccent: preset.background,
    selectionBackground: preset.selectionBackground,
    black,
    red,
    green,
    yellow,
    blue,
    magenta,
    cyan,
    white,
    brightBlack,
    brightRed,
    brightGreen,
    brightYellow,
    brightBlue,
    brightMagenta,
    brightCyan,
    brightWhite,
  };
}
