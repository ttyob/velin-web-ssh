# Velin Mobile

独立手机客户端：Flutter 界面、原生 HTTP / WebSocket、本地 WebView 中的 xterm.js，复用现有 Go 服务。

当前为 **0.1.3 开发内测版**。黑灰配色、紧凑列表、可展开输入与全屏终端。支持服务地址管理、SSH 主机增改、命名会话、反向代理管理与访问，以及终端查看 / 接管、触摸选择、附件和恢复。登录与首次改密采用统一样式，锁屏轻触后展开 PIN，解锁保留当前页面。Android 调试构建已通过；真机输入法、代理网页、长任务和 iOS 尚待验收。完整结果见[任务计划](../docs/mobile-app-development-plan.md)。

[需求](../docs/mobile-app-requirements.md) · [界面与截图](design/README.md) · [协议 v1](contracts/README.md)

## 使用

1. 服务端需包含本次移动扩展。登录后 `GET /api/mobile/v1/capabilities` 应返回协议版本 1，且 `viewAttach`、`geometry`、`inputAck` 均为 `true`。旧服务会显示升级提示。
2. 在 App 中输入 Velin 服务的 HTTP / HTTPS 地址、用户名和密码；也支持带路径前缀的服务地址。沿用验证码、双因素认证、首次改密和 PIN 规则。
3. 打开已有会话先查看；点击“接管”申请输入控制。电脑正在控制时需要其批准。同名会话显示短编号和创建时间；点击主机新建时填写任务名称，也可从会话“⋯”重命名。
4. 点击底部键盘图标展开输入，收起保留草稿。Enter 换行；向上箭头发送正文并追加一次 Enter，“＋”中的“只粘贴正文，不回车”只写入正文。中文候选尚未确认时不会发送。Esc / Ctrl+C 在底栏，其余快捷键按需打开。
5. 选择模式中划选文本，然后点击“复制”。普通模式可拖动浏览终端，宽终端可横向移动。tmux 远端历史需接管，会改变该会话的共享视图；TUI 手势走应用鼠标协议。
6. 附件先确认远端项目目录，上传至其 `clipboard/`，路径加入当前会话草稿。不会直接执行草稿；不会自动清理远端文件。可在项目中自行删除不用的附件。
7. 返回、切应用和退出账户不会终止远端 tmux。菜单中的“终止远端会话”需要单独确认。普通 SSH 会话不具备 tmux 的持久恢复能力。

首页顶部切换会话 / 主机；搜索点放大镜，设置与筛选在“⋯”中。主机页“＋”添加 SSH 主机，主机“⋯”编辑、删除或新建命名会话。密码、已存凭据和连接时输入均可选择，启动目录、跳板机等收入“更多设置”。编辑保留原有高级参数，密码留空保留已存密码。

首页“⋯”提供“Velin 服务管理”和“反向代理”，设置中也有入口。服务档案支持备注名称、地址和账户的新增 / 编辑 / 删除；切换时保留各服务独立的登录状态。登录页可直接添加和管理服务。

反向代理支持新增、编辑、删除及 App 内打开，复用 Web 的配置。目标地址由 SSH 主机访问，支持路径代理、独立端口及 Vite 模式；SSH 主机需保存密码或绑定凭据。本机服务已支持密码主机；其他部署需更新对应服务端。代理 WebView 只接收服务专用票据，账户 Cookie 留在原生客户端。独立端口需手机可达；跳出当前代理域名的顶层导航会被拦截。

终端底部可开启全屏，连接详情点击顶部会话标题查看；附件在终端“⋯”或输入框“＋”中打开。

## 工具链

已验证 Flutter **3.35.7** / Dart **3.9.2**、Node **18.20.4**、Java **17**、Android SDK **36** / Build Tools **35.0.0** / NDK **27.0.12077973**。Gradle 8.12、AGP 8.9.1 和 Kotlin 2.1.0 随工程声明。

Android 最低 **7.0 / API 24**。iOS 工程暂设最低 **13.0**，最终支持范围须在 macOS / Xcode 和真机上确认。原 Go / Web 构建不需要这些移动工具链。

从仓库根目录执行：

```bash
npm --prefix mobile/terminal ci
npm --prefix mobile/terminal run build
cd mobile/app
flutter pub get
flutter analyze
flutter test
flutter build apk --debug --target-platform android-arm64
```

产物：`mobile/app/build/app/outputs/flutter-apk/app-debug.apk`。这是使用本机构建工具调试密钥签名的 arm64 安装包，仅供内测；升级要求签名一致。正式发布需要另行配置生产签名，工程不自动使用调试密钥发布 Release。

iOS 在 macOS 上生成终端资源后执行 `flutter build ios --no-codesign`，再由 Xcode 配置签名和设备。当前没有 IPA 或 iOS 验收结论。

## 界面预览

```bash
npm --prefix mobile/terminal run build
cd mobile/app
flutter build web --release --no-web-resources-cdn
cd ../..
python3 -m http.server 8388 --bind 0.0.0.0 --directory mobile/app/build/web
```

浏览器打开 `http://<本机IP>:8388/`；`?screen=login` 查看登录页。**Web 构建始终为明确标注的示例预览，只展示界面和本地终端，不进行真实登录、SSH 或文件上传。** 原生 APK 默认连接真实服务，不包含账户、服务地址或 SSH 密钥。

`?screen=setup` 查看首次改密，`?screen=lock` 查看系统式锁屏；预览专用 PIN 为 `123456`。真实 App 使用服务端账户及其已配置的 6 位锁屏 PIN，首页“⋯ → 锁定工作台”可手动锁屏。项目没有开放注册接口，本次统一登录与首次账户设置的视觉，不新增自助注册。

## 验证

```bash
npm --prefix mobile/terminal test
npm --prefix mobile/terminal run test:browser
node mobile/scripts/capture-design.mjs
node mobile/scripts/test-management.mjs
node mobile/scripts/test-auth.mjs
go test ./...
go test -race ./internal/terminal ./internal/api
go vet ./...
npm --prefix web test
npm --prefix web run build
npm --prefix web run test:terminal
```

浏览器检查需要 Chromium；可用 `CHROMIUM_PATH` 指定路径。截图脚本读取 `VELIN_MOBILE_PREVIEW_URL`，默认 `http://127.0.0.1:8388/`。图标由 `design/icon.svg` 生成：`node mobile/scripts/build-icons.mjs`。

## 当前边界

- 真机中文 / 语音输入法、系统键盘、文件选择、前后台、弱网和 4 小时 CLI 任务待验证，浏览器截图不能代替这些结果。
- 首版复用服务端保留的字节历史；跨历史尺寸变化或缓冲截断时不保证完整重建 TUI。界面会提示截断，仍需真实 Codex / Claude 冷启动与热重连验证。
- App 重启保留服务档案和会话草稿，回到会话列表后手动打开目标会话。重连从保留历史重新同步，输入永不自动重发。
- 每条持久化草稿上限 48 KiB；超过上限的草稿只保留在当前页面内存中。删除服务档案可清理其认证、草稿、收藏和最近记录；设置中也提供缓存清理。
- 文件在原生层通过 SFTP 接口流式上传；单文件上限 2 GiB，实际传输还受网络、服务端限制及 HTTP 超时影响。不具备断点续传，取消可能留下远端部分文件。
- Git / Diff、文件编辑、通知与推送尚未实现；网页预览支持通过已配置的 SSH 反向代理打开。
- 2026-10-31 已将本机 Docker 服务更新为包含移动扩展的版本，并通过带认证的能力接口验证。App 服务地址为 `http://192.168.0.111:8377`；`8388` 仍为示例界面预览。其他部署需自行更新包含扩展的服务端。

## 隔离与移除

客户端代码和专用依赖均在 `mobile/`。移动端没有单独数据库表，也不写入原 Web 的布局和终端偏好。后端兼容扩展默认注册，无独立配置开关；仅声明 `attach=view` 的连接采用移动查看行为。

整体移除时删除 `mobile/`、`internal/api/mobile*.go`、`internal/terminal/mobile*.go`，撤销 `api.go` 中的移动注册、查看订阅分支、尺寸及输入回执适配，再撤销 `manager.go` 中对应字段、订阅参数、尺寸广播和写入适配。用本次变更的 diff 定位这些兼容扩展，随后运行原 Go / Web 回归。不要删除账户、主机、远端 tmux 或项目附件。

移动构建独立于现有 Docker / fnOS / Windows Release。
