# 开发与构建

[返回首页](../README.md)

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
npm run test:sessions
npm run test:connections
```

## Windows 打包

Windows GUI 使用 Wails 构建，在项目根目录先构建要内嵌的网页资源：

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
