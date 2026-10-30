# Windows 终端配色排查（2026-10-30）

Windows 桌面版与 Docker Web 使用相同的终端组件和用户配色设置。桌面窗口的 `windows.Dark` 只设置原生窗口外观，不会固定终端配色。

用户已确认是在 Win11 客户端连接 Linux，并补充 tmux 模式背景不随主题变化。源码中的 SSH 会话由 Go SSH 客户端建立，没有调用本地 PowerShell。已发现 `SetTerminalColors` 将 RGB 写入 tmux 的 `window-style` / `window-active-style`，使远端默认文字与背景变为显式颜色，客户端配色切换无法独立改变这些已有单元格。

## 待确认的 Win11 配色联动

用户进一步反馈终端配色会随 PowerShell 主题变化。这一现场现象尚未复现或确认原因，不能将下面的 tmux 或对比度主题修复视为已解决这条反馈。

当前源码检查结果：

- `terminalXtermTheme` 为 xterm 提供默认前景、背景、光标及完整 ANSI 16 色；没有读取 PowerShell 配色或跟随系统深浅色的逻辑。
- 桌面版 Wails 配置原生窗口为深色；未开启窗口或 WebView 透明，也未指定浏览器颜色覆盖参数。所用 Wails 版本的浏览器参数配置仅涉及安全功能及可选 GPU 设置。
- SSH PTY 使用主机配置的终端类型（默认 `xterm-256color`），没有将本机 PowerShell 配色环境转发给 Linux。
- xterm 支持远端 OSC 调色板修改，这条标准功能本身不能证明本地 PowerShell 是颜色变化来源；尚未为这次反馈禁用它。

需要区分：使用独立 EXE 或浏览器；修改的是 PowerShell 配色还是 Windows 系统/对比度主题；已打开的窗口立即变化，还是重启或重连后变化。已向用户询问这些信息，等待现场补充。

## 已确认问题的修复任务

修复任务：

- [x] tmux 使用默认前景/背景，创建及恢复时清理旧版本写入的固定 RGB。
- [x] 兼容原有主题通知协议，避免切换主题重新写入固定 RGB。
- [x] 回归真实 tmux 输出及已有文字的即时换色，保留 ANSI 和 RGB 输出。
- [x] 更新相关构建并记录验证边界。

另外复现了系统对比度主题触发的 `forced-colors` 问题：浏览器会替换 DOM 背景和配色卡片的颜色，Canvas 内的像素仍保持终端配色，两者不一致。已在 `.xterm` 与终端配色卡片限定 `forced-color-adjust: none`，使终端和颜色预览保持选择的配色。

验证使用 Linux Chromium 的 `forcedColors` 模拟，挂载真实 `TerminalPane`、`SettingsDrawer` 和应用样式，使用隔离 API/WebSocket，没有连接生产 SSH 主机：

- 普通模式与系统强制颜色模式分别点击全部 7 个终端配色。
- 检查终端 Canvas 背景像素、viewport 背景及配色预览与选中主题一致。
- 检查实际设置操作提交的用户偏好包含选中的终端主题。
- 前端类型检查和生产构建通过。

这不是原生 Win11 实机验证，尚未确认用户现场是否开启对比度主题。若背景能切换而命令输出没有高亮，还需检查远端 shell / CLI 的颜色设置；终端主题不会为纯文本添加语法高亮，也不会重新映射应用指定的 RGB 真彩色。

桌面 EXE 内嵌构建时的前端资源，更新 Docker Web 不会改变已下载 EXE 的界面代码。

Docker Web 已更新到镜像 `sha256:96865465288c77932dffdca316b1cb6310f613dc1fda431f763636617aea7a55`。健康检查通过，在线首页及入口 JS/CSS 与本次构建一致，已确认在线 CSS 包含两处修复。更新前现有工作流处于 blocked，运行记录保留；guacd 未重建。

## tmux 修复及验证

- `configureTmuxTerminal` 在新建/恢复时将两项窗口样式恢复到 `fg=default,bg=default`；原有 `terminal_theme` 消息继续验证颜色与控制权，但只恢复默认样式，用户配色在浏览器本地生效。
- 终端将字体尺寸、光标与主题监听分开。换色只更新并重绘主终端及连接恢复预览，不触发尺寸重排。
- 真实 tmux 恢复测试预置两个不同的旧 RGB 样式，连续恢复两次后都回到默认色；原有全屏重绘与历史不重复断言继续通过。
- 浏览器使用真实 tmux PTY 输出：旧样式确实产生显式背景单元格，恢复默认样式后单元格回到默认前景/背景。只读窗口在没有后续远端输出时切换 Dracula、Solarized Dark 和默认主题，Canvas 背景像素即时匹配，80×24 尺寸保持不变；ANSI 与显式 RGB 前景色保留。原有滚动、选择复制、剪贴板文件及按钮回归通过。
- `go test ./internal/terminal ./internal/api`、相关 `go vet`、`npm --prefix web run test:terminal`、前端类型检查/生产构建及 `git diff --check` 通过。

最终 Docker Web 镜像为 `sha256:0b54fd6afa8c418ae0ce9a78fe8c890b32e6976dcc5289c48ac9bb9befa521eb`，容器 healthy，在线入口资源与构建一致，guacd 未重建。更新前工作流仍为 blocked，没有重试任务。

Windows 单文件修复版：`artifacts/windows-v0.3.48/velin-web-ssh-windows-gui-amd64-v0.3.48-theme-fix.exe`，版本 `v0.3.48+theme.20261030`，38.86 MiB，SHA-256 `c928f50c896c958614f7a5ca2a7d61ff559c0074abc032dc2ead4a9c65d6c4ad`。已验证 PE 为 x64 Windows GUI，内嵌本次前端与 tmux 默认色修复；没有打入应用数据或实际环境配置。此处是交叉编译与 Chromium/tmux 回归，未进行原生 Win11 实机运行。

## 扩展灰阶适配

用户截图中主背景已为 Solarized Dark 的 `#002b36`，输入区和表格线仍为 `#3a3a3a`，与标准 256 色调色板的 237 号灰色一致。仅凭截图无法区分该颜色来自索引色还是相同值的 RGB。原先的 `terminalXtermTheme` 只配置默认色和基础 16 色，没有配置扩展灰阶。

已在统一的主题生成函数中配置 xterm 公开的 `extendedAnsi`：232–255 号灰阶以原有的灰度比例，在当前终端主题背景和前景之间生成。16–231 号颜色立方体保留标准颜色；显式 RGB（包括 RGB 灰色）保留。该设置同时供主终端、恢复预览和分享终端使用，不修改 SSH 输出或单元格内容。

原有主题监听已会重绘当前主终端和恢复预览，因此历史内容可即时换色，无须远端输出或重连。以 237 号色为例，Solarized Dark 变为 `#1e434c`，Dracula 变为 `#575961`。

新增真实终端 Canvas 像素回归：只写入一次模拟输入区、分隔线、24 级灰阶、标准颜色样本和 RGB 样本，依次切换全部 7 个主题。确认已有前景和背景随主题变色、灰阶保持明暗顺序且互不重复、标准颜色样本和 RGB 保留、文本和终端尺寸保持一致。`npm --prefix web run test:terminal`（含原有真实 tmux/滚动/复制/粘贴回归）、前端类型检查与生产构建通过。未在用户远端运行实际 Codex，也未进行 Win11 实机验证。

灰阶修复已部署到 Docker Web，镜像 `sha256:f1af43599abaee818f0e4d6f0ea492cc8ac37ec7b05014aadbfa4caa4ca512bd`，容器 healthy，readiness 为 ok。逐一核对了线上 53 个 HTML/JS/CSS 文件，与本地构建完全一致。部署前唯一工作流仍为 blocked，没有运行中的任务；guacd 保持原镜像和启动时间。

对应 Windows 单文件为 `artifacts/windows-v0.3.48/velin-web-ssh-windows-gui-amd64-v0.3.48-theme-grayscale-fix.exe`，版本 `v0.3.48+gray.20261030`，38.86 MiB，SHA-256 `5e82c530470687093ea0bddc9b52432c0de07092d542b6b04f679a11ec7500f3`。已验证 x64 Windows GUI 子系统和全部 53 个内嵌前端资源，包含前一修复版的 tmux 默认色、即时重绘及 forced-colors 修复；尚未进行 Win11 实机验证。
