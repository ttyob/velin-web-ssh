<div align="center">

# Velin Web SSH

轻量、自托管的浏览器 SSH 工作区。

在一个界面中管理主机、终端、文件、端口转发和内网 Web 服务。

[![Build](https://github.com/ttyob/VelinWebSsh/actions/workflows/container.yml/badge.svg)](https://github.com/ttyob/VelinWebSsh/actions/workflows/container.yml)
[![Release](https://img.shields.io/github/v/release/ttyob/VelinWebSsh?display_name=tag&sort=semver)](https://github.com/ttyob/VelinWebSsh/releases)
[![GHCR](https://img.shields.io/badge/GHCR-amd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white)](https://github.com/ttyob/VelinWebSsh/pkgs/container/velinwebssh)

</div>

![Velin 终端工作区](.github/assets/velin-workspace.png)

## 功能

- 主机分组、搜索、拖动排序、跳板机和 OpenSSH 配置导入
- 多标签与分屏终端，支持普通 SSH、tmux 持久会话和断线恢复
- 页面内 VNC（noVNC）和 RDP（Guacamole）远程桌面，RDP 也支持调用 Windows 本地远程桌面客户端
- 连接过程直接显示在终端中，并显示连接状态与网络延迟
- SFTP 文件管理、文本编辑、上传和下载
- 本地、远程与 SOCKS5 端口转发
- 通过 SSH 访问内网 HTTP/HTTPS 服务
- 独立保存的主机密码和可复用 SSH 凭据
- TOTP、PIN 锁屏、主机指纹校验和加密备份
- 主机资源监控、Docker/Git 工具和可选 AI Agent
- 终端会话限时分享，支持密码、只读/可操作权限、在线预览和分享期录制
- Linux Web 版和 Windows Wails GUI

## 快速安装

需要 Linux、Docker Engine 和 Docker Compose v2：

```bash
curl -fsSL https://raw.githubusercontent.com/ttyob/VelinWebSsh/main/install.sh | sh
```

当前用户没有 Docker 权限时：

```bash
curl -fsSL https://raw.githubusercontent.com/ttyob/VelinWebSsh/main/install.sh | sudo sh
```

安装脚本会拉取 `ghcr.io/ttyob/velinwebssh:latest`，创建数据目录和随机管理员密码。完成后会输出访问地址、管理员账号、密码和安装目录。

默认端口为 `8377`，默认安装目录为：

- root 用户：`/opt/velin`
- 普通用户：`~/.local/share/velin`

升级已安装实例：

```bash
/opt/velin/update.sh
```

## 其他运行方式

### Docker Compose

```bash
git clone https://github.com/ttyob/VelinWebSsh.git
cd VelinWebSsh
cp .env.example .env
docker compose build
docker compose run --rm --no-deps --user root --entrypoint sh velin \
  -c 'chown -R velin:velin /app/data && chmod 700 /app/data'
docker compose up -d
```

启动前请修改 `.env` 中的 `VELIN_ADMIN_PASSWORD`。数据保存在项目的 `data/` 目录中。

常用命令：

```bash
docker compose ps
docker compose logs -f velin
docker compose restart velin
docker compose down
```

忘记管理员密码时，可保留现有数据并重置密码。命令会撤销该管理员的旧登录会话，输出临时密码，并要求登录后修改：

```bash
docker compose run --rm --no-deps velin --reset-admin-password
```

若要指定新密码和管理员用户名：

```bash
docker compose run --rm --no-deps \
  -e VELIN_ADMIN_USER=admin \
  -e VELIN_ADMIN_PASSWORD='替换为新密码' \
  velin --reset-admin-password
```

使用安装脚本部署在 `/opt/velin` 时，先进入该目录再执行上述命令。仅修改 `.env` 中的 `VELIN_ADMIN_PASSWORD` 不会重置已有账户。

### GitHub Release

[Releases](https://github.com/ttyob/VelinWebSsh/releases) 提供以下运行包：

- `velin-linux-web-amd64.tar.gz`
- `velin-linux-web-arm64.tar.gz`
- `velin-windows-gui-amd64.zip`

Linux Release 包可在解压目录中直接重置管理员密码：

```bash
./velin-web --reset-admin-password
```

### 飞牛 fnOS 原生应用包

仓库提供不依赖 Docker 的飞牛 fnOS `.fpk` 原生应用包。包内直接运行 Velin Go 服务和 guacd，数据保存到 fnOS 应用数据目录，不影响现有 Compose 部署：

```bash
packaging/fnos/build.sh
```

构建脚本默认构建当前主机架构，也可以显式指定架构：

```bash
VELIN_FNOS_VERSION=0.3.27 VELIN_FNOS_ARCH=amd64 packaging/fnos/build.sh
VELIN_FNOS_VERSION=0.3.27 VELIN_FNOS_ARCH=arm64 packaging/fnos/build.sh
```

忘记 fnOS 原生应用的管理员密码时，通过 SSH 登录 NAS 后执行：

```bash
sudo -u velin env \
  TRIM_APPDEST=/var/apps/velin-web-ssh/target \
  TRIM_PKGVAR=/var/apps/velin-web-ssh/var \
  /var/apps/velin-web-ssh/target/cmd/main reset-admin-password
```

命令会短暂停止 Velin、生成并输出临时密码，然后恢复原有运行状态。指定新密码时增加 `VELIN_RESET_PASSWORD='替换为新密码'`；数据、主机和凭据不会被删除。

卸载 fnOS 原生应用时可选择保留或删除应用数据，默认保留以便重新安装。选择删除会永久清除数据库、主密钥、凭据、备份和日志。

将生成的 `dist/fnos/velin-fnos-native-*.fpk` 在飞牛应用中心手动安装。首次安装向导会设置管理员账号和密码。构建脚本需要 Docker 和 GitHub 网络来提取对应架构的 guacd 运行库；安装后的 NAS 不需要 Docker。

Linux 包解压后复制配置并运行：

```bash
cp .env.example .env
chmod +x velin-web
./velin-web
```

Windows GUI 需要 Microsoft Edge WebView2。解压后在 PowerShell 中执行：

```powershell
.\Velin-GUI.exe
```

Windows GUI 无需配置，默认自动选择空闲本机端口。数据库、主密钥、录制文件和启动日志均写入 `Velin-GUI.exe` 所在目录；需要覆盖默认值时才从 `.env.example` 创建 `.env`。VNC 可直接使用，RDP 需要在本机 Docker 或其他受信任服务器上单独运行 guacd，并通过 `VELIN_GUACD_ADDR` 指定地址。

## 配置

Velin 默认读取运行目录中的 `.env`，同名系统环境变量优先。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VELIN_ADDR` | `0.0.0.0:8377` | Web 服务监听地址 |
| `VELIN_ADMIN_USER` | `admin` | 首次启动创建的管理员账号 |
| `VELIN_ADMIN_PASSWORD` | 空 | 首次启动管理员密码 |
| `VELIN_DATA_DIR` | `data` | 数据库、主密钥和录制目录 |
| `VELIN_COOKIE_SECURE` | `true` | 会话 Cookie 仅通过 HTTPS 发送；纯 HTTP 内网部署必须显式设置为 `false` |
| `VELIN_EMBED_ORIGINS` | 空 | 允许嵌入 Velin 的可信来源，多个来源用逗号分隔，例如 `https://nas.example.com` |
| `VELIN_TRUSTED_PROXY_CIDRS` | 空 | 可信反向代理网段，只有来自这些网段的请求才会读取 `X-Real-IP` |
| `VELIN_HOST_PORT_ADDR` | `127.0.0.1` | 主机端口代理监听地址 |
| `VELIN_GUACD_ADDR` | `127.0.0.1:4822` | RDP 使用的 guacd 地址 |
| `VELIN_DESKTOP_PROXY_ADDR` | `127.0.0.1` | RDP 跳板代理监听地址 |
| `VELIN_RDP_DRIVE_DIR` | `/tmp/velin-rdp-drives` | RDP 磁盘映射目录；使用外部 guacd 时需与 guacd 共享此路径 |
| `VELIN_AI_BASE_URL` | 空 | OpenAI 兼容 API 地址 |
| `VELIN_AI_MODEL` | 空 | AI Agent 模型名称 |
| `VELIN_AI_API_KEY` | 空 | AI API Key |
| `VELIN_FFMPEG_BINARY` | `ffmpeg` | 终端录制使用的 FFmpeg 路径 |

公网部署请使用 Caddy、Nginx 等反向代理提供 HTTPS；同时通过防火墙限制外部直接访问 `8377` 端口。配置内嵌页面时只填写明确的 `VELIN_EMBED_ORIGINS`，不要使用通配来源。反向代理必须覆盖而不是追加 `X-Real-IP`，并将代理网段写入 `VELIN_TRUSTED_PROXY_CIDRS`。guacd 没有认证能力，Compose 已将其限制在 `127.0.0.1:4822`，不要将该端口暴露到公网。Compose 会自动为 Velin 和 guacd 共享 RDP 磁盘映射目录。

### 内嵌 Tailscale

管理员可在“设置 -> 系统管理 -> 内嵌 Tailscale”中配置节点名称、控制面地址和 Auth Key，并通过服务状态开关启停。默认关闭；只有开启并保存后，Velin 才会在当前进程中启动 `tsnet` 用户态网络。节点身份保存在 `data/tailscale`，升级和重启时必须保留。

启用后，在 Velin 主机列表中直接填写 Tailnet IP 或 MagicDNS 名称；SSH、SFTP、RDP、Web 代理和端口转发的远端出站连接都会通过内嵌节点。Velin 不安装 `tailscaled`、不创建系统 TUN，也不要求容器增加 `NET_ADMIN`。Auth Key 只保存在服务端加密数据中。

## 数据与备份

`data/` 目录包含：

- `velin.db`：用户、主机、会话和设置
- `master.key`：SSH 敏感数据的加密主密钥
- `recordings/`：终端录制

管理员可以在设置中创建密钥加密备份。备份包含数据库和 `master.key`，不包含 `.env` 和终端录制。恢复时必须使用创建备份时输入的同一密钥。

## 开发

需要 Go 1.25.13、Node.js 18.20.4 或更高版本，以及 npm。

```bash
cd web
npm ci
npm run dev
```

检查和构建：

```bash
cd web
npm test
npm run typecheck
npm run build
```

Windows GUI 使用 Wails 构建：

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.2
wails build -platform windows/amd64 -clean -m -s -skipbindings
```

Windows GUI 默认为便携模式。数据库、主密钥、终端录制和 `velin-gui.log` 均保存在 `Velin-GUI.exe` 所在目录。首次启动会显示管理员凭据对话框，密码可通过按钮复制并同时写入 `velin-gui.log`。

如果初始密码已经丢失，在 PowerShell 中执行以下命令。程序会保留现有数据、重置管理员密码、清除旧登录会话，并通过弹窗和日志显示新密码：

```powershell
.\Velin-GUI.exe --reset-admin-password
```

## 常见问题

- `ssh: unable to authenticate`：检查用户名、密码、私钥和远端 SSH 认证策略。
- `tmux session not found`：远端会话已经不存在；也可以把主机会话模式改为普通 SSH。
- 无法启动：检查端口占用、数据目录权限以及 `docker compose logs -f velin`。
- Windows GUI 异常：查看 `Velin-GUI.exe` 同目录下的 `velin-gui.log`。

发布版本与校验文件见 [GitHub Releases](https://github.com/ttyob/VelinWebSsh/releases)。
