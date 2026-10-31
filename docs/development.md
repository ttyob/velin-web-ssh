# 开发与构建

[返回首页](../README.md)

手机 App 已有独立 Flutter 第一版与 Android 调试构建，真机验收待完成：[构建与使用](../mobile/README.md) · [需求与技术设计](mobile-app-requirements.md) · [开发任务与验收计划](mobile-app-development-plan.md)。

需要 Go 1.25.13、Node.js 18.20.4 或更高版本，以及 npm。

## 本地运行

在项目根目录准备配置并启动后端：

```bash
cp .env.example .env
VELIN_ADDR=127.0.0.1:8080 go run ./cmd/velin
```

在另一个终端启动前端，访问 Vite 输出的地址：

```bash
npm --prefix web ci
npm --prefix web run dev
```

纯 HTTP 本地开发需在 `.env` 中设置 `VELIN_COOKIE_SECURE=false`。首次启动的管理员凭据由启动日志说明，首次登录需修改密码。

## 检查与构建

在项目根目录执行：

```bash
go test ./...
go vet ./...
npm --prefix web test
npm --prefix web run build
```

生产构建包含 TypeScript 类型检查。

## 浏览器回归

终端历史、远程桌面恢复、标签关闭和首次连接的浏览器回归检查（需要 Chromium，可用 `CHROMIUM_PATH` 指定路径）：

安装 `tmux` 和 Python 3 后，终端检查还会验证真实 tmux 输出在浏览器中的历史行序和反复滚动；缺少这两项时会提示跳过该部分。

```bash
cd web
npm run test:terminal
npm run test:terminal -- --windows
npm run test:desktop
npm run test:dialogs
npm run test:auth
npm run test:sessions
npm run test:connections
```

## Windows 打包

Windows GUI 使用 Wails 构建，在项目根目录先构建要内嵌的网页资源：

登录、首次改密与锁屏在浏览器和 Windows WebView2 中共用同一套 Vue 页面。认证样式集中于 `web/src/styles-auth.css`，使用独立的黑灰配色，工作区主题偏好不覆盖认证页面。锁屏中央显示简洁指针钟表、24 小时制时间、锁定状态和点击解锁提示；时间取自当前设备，每秒更新，切回页面时立即校准，退出锁屏后清理计时器。点击提示以轻微呼吸和圆环波纹循环引导，点击屏幕或按 Enter / Space 通过上移、淡入与模糊过渡展开 PIN，Esc 反向收起并清空输入。PIN 验证成功后，遮罩向上揭开、表单淡出，露出工作区；验证失败保持锁屏。减少动态效果偏好会关闭过渡和循环动效。切换账号入口位于 PIN 界面，焦点限制在遮罩内，底层工作区使用 `inert`。验证、限流、自动锁定及跨标签页信号继续复用原服务端流程。Windows 首次启动的管理员凭据提示位于 `desktop/dist/index.html`，保持同样的表单和配色。现有项目没有开放注册接口，账户仍由管理员创建。

登录会话与安全令牌 Cookie 使用部署 ID 区分同一主机上的不同服务，并隔离历史 Cookie 的 Secure 属性及路径冲突。升级时兼容可读取的旧登录会话；登录后通过 `/api/auth/me` 确认 Cookie 已生效，再进入工作区或首次改密页。改密页遇到会话失效会返回登录页。

`npm --prefix web run test:auth` 使用隔离的 API 数据验证验证码、双因素认证、改密确认、手动 / 闲置 / 刷新恢复锁屏、错误 PIN、焦点限制、键盘和窄屏布局，并验证 Windows 初始账户提示、登录 Cookie 未保存及改密会话失效的处理。后端测试覆盖存在历史 Cookie 时的登录、首次改密、访问工作区和退出流程。截图写入 `artifacts/auth-redesign-20261031/`；浏览器检查不能替代 Windows 真机 WebView2 验收。

```powershell
npm --prefix web ci
npm --prefix web run build
go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.2
wails build -platform windows/amd64 -clean -m -s -skipbindings
```

产物为 `build/bin/velin-web-ssh.exe`。fnOS 原生包构建见 [fnOS 打包说明](../packaging/fnos/README.md)。

## 更新 README 截图

先在独立演示实例中连接真实 SSH 会话、设置分屏并运行要展示的命令。截图脚本登录该实例，直接截取现有工作区，使用真实 API 和终端输出。

在项目根目录执行（Bash）：

```bash
export VELIN_SCREENSHOT_URL=http://127.0.0.1:8377
export VELIN_SCREENSHOT_USER=demo
read -rs -p '演示账号密码: ' VELIN_SCREENSHOT_PASSWORD
export VELIN_SCREENSHOT_PASSWORD
npm --prefix web run screenshot:readme
unset VELIN_SCREENSHOT_PASSWORD
```

需要 Chromium，可用 `CHROMIUM_PATH` 指定路径。默认生成 `.github/assets/velin-workspace.png`（1600 × 960）；用 `VELIN_SCREENSHOT_OUTPUT` 可修改输出位置。

## 发布

[GitHub Actions](../.github/workflows/container.yml) 在 `main` 更新后执行测试并发布 Docker `edge` 镜像。推送 `v*` 版本标签后，执行测试、构建双架构 Docker 镜像及 fnOS 安装包、构建 Windows 单 EXE，最后统一创建 GitHub Release 和校验文件。

正式镜像的 `latest` 跟随版本标签，发行版同时提供固定版本镜像地址。
