# 外部 Agent 会话接入（OpenClaw / MCP）

## 目标与边界

让 OpenClaw 等 MCP 客户端复用 Velin 已打开的 SSH/tmux 会话，查看最近输出、发送输入，并按授权创建、恢复、重命名、终止会话。入口放在原有设置中的“外部 Agent”，Web 与 Windows 共用。会话输出仍是终端内容，不把静默视为任务完成，也不假装获得 CLI 的结构化内部状态。

模块独立实现为 `internal/sessionbridge/`、`internal/api/session_bridge.go`、`internal/terminal/bridge.go` 与 `web/src/features/session-bridge/`。API 只保留初始化、路由与 Bearer 传输的 CSRF 分流接入点；模块自行管理附加数据库表。删除这些目录、文件和接入点即可整块移除，不改变既有会话表和浏览器登录逻辑。

## 开发任务

- [x] 建立独立模块、权限模型与数据库迁移。
- [x] 实现专用令牌：仅返回一次原文、哈希存储、过期、停用、删除、按用户隔离。
- [x] 实现会话 / 主机授权范围；读取为基础权限，输入、创建恢复、管理分别授权。
- [x] 通过官方 Go MCP SDK 提供 Streamable HTTP 入口 `/mcp/sessions`，支持标准初始化、工具发现与调用。
- [x] 实现工具：列出授权主机和会话、读取输出、发送输入、创建、恢复、重命名、终止会话。
- [x] 旁路读取：tmux 捕获真实屏幕及有限历史；普通 SSH 通过终端解析生成文本；保持原有大小、滚动位置和控制权。
- [x] 返回输出游标、截断标识及连接状态，支持有上限的异步等待新输出。
- [x] 输入与网页输入串行，支持文本、回车和常用按键；持久化请求编号去重，明确“写入终端”与“执行完成”的区别。
- [x] 创建只使用已保存凭据；指纹和补充认证沿用现有交互，不自动信任新指纹。
- [x] 设置页提供令牌创建、权限范围、停用 / 删除、一次性复制令牌和 OpenClaw 配置示例。
- [x] 补齐权限、跨用户访问、过期撤销、重复输入、终端文本与真实 MCP 客户端测试。
- [x] 完成浏览器交互验证、Go 测试、Web 生产构建及 Windows 编译。
- [x] 补齐使用说明和移除方法，核对最终交付。

## 接口约定

浏览器管理接口位于 `/api/session-bridge/`，沿用登录 Cookie、CSRF 和锁屏保护。MCP 入口仅接受专用 `Authorization: Bearer <token>`，每次请求重新校验令牌和用户状态；浏览器 Cookie 不授予 MCP 访问权。

令牌范围是选定会话与选定主机的并集。选定主机包含其现有及后续会话；只有主机授权且启用创建权限才能新建。所有资源仍须属于令牌所有者。输入、创建恢复、管理权限相互独立，终止另需显式确认参数。

发送输入须携带唯一 `requestID`。同一令牌、工具、编号和参数只执行一次；编号与不同参数冲突则拒绝。中断期间未确认的写入返回不确定状态，不自动补发。审计记录只保存操作、目标和字节数等信息，不保存令牌或终端输入正文。

读取仅返回有界文本，保留中文及终端重绘语义。等待新输出的调用有上限，不限制远程 CLI 本身运行时长。断线恢复使用现有 tmux 恢复逻辑；普通 SSH 结束后无法恢复原进程。

## 验收与接入说明

实际工具参数、OpenClaw 配置和验证结果见下文。Telegram 通道直接由 OpenClaw 配置；主动事件推送与 webhook 通知是后续扩展，本次交付覆盖由 Agent 主动查询和操作会话。

## 使用方法

1. 更新并启动包含本模块的服务，进入工作台 **设置 → 工具 → 外部 Agent**。
2. 创建令牌，选择指定会话或 SSH 主机。默认只有读取权限；按需开启发送输入、创建 / 恢复、重命名 / 终止。
3. 保存一次性显示的令牌，确认配置中的服务地址能被 OpenClaw 访问，复制完整配置到 OpenClaw。
4. 执行 `openclaw mcp doctor velin --probe`，确认能发现工具。
5. 在 OpenClaw 中先让它列出会话，再按会话 ID 读取或操作。例如：“查看名为 Codex 开发的会话最后输出；确认内容后再发送我的下一条需求。”

只授权指定会话时，Agent 看不到其他会话，也不能创建新会话。授权主机后，会覆盖该主机的当前和未来会话。创建和恢复只使用已保存的认证信息；新指纹、缺少凭据等情况需要所有者在工作台处理。

Windows 内置服务也提供相同接口。配置示例中若显示 `127.0.0.1`，应改成 OpenClaw 实际能连接到的 Windows 服务地址。

```json
{
  "mcp": {
    "servers": {
      "velin": {
        "url": "http://你的服务地址:8377/mcp/sessions",
        "transport": "streamable-http",
        "enabled": true,
        "headers": {
          "Authorization": "Bearer <专用令牌>"
        },
        "requestTimeoutMs": 30000
      }
    }
  }
}
```

接入方式以 OpenClaw 官方文档为准：<https://docs.openclaw.ai/tools/mcp>。本次使用官方 Go MCP 客户端做协议集成验证，未在安装 OpenClaw 的机器上运行端到端测试。

## 工具与行为

| 工具 | 权限 | 参数与结果 |
| --- | --- | --- |
| `velin_list_sessions` | read | 返回授权会话 ID、标题、主机、模式、连接状态、错误和是否附加 |
| `velin_list_hosts` | read | 仅返回明确授权的 SSH 主机，不包含凭据 |
| `velin_read_session` | read | `sessionID`，可选 `maxLines`（默认 200，最大 1000）、`cursor`、`waitSeconds`（0～20） |
| `velin_send_input` | input | `sessionID`、UUID `requestID`；选择 `text` + 可选 `submit`，或单独 `key` |
| `velin_create_session` | create | `hostID`、`name`、UUID `requestID`、可选 `mode`（默认 tmux）；异步返回 `accepted` 和会话 |
| `velin_restore_session` | create | `sessionID`、UUID `requestID`；恢复已有 tmux 连接 |
| `velin_rename_session` | manage | `sessionID`、`name`；只修改显示标题 |
| `velin_terminate_session` | manage | `sessionID`、UUID `requestID`、`confirm: true`；返回本地终止与远程清理结果 |

支持按键：`Enter`、`Ctrl+C`、`Ctrl+D`、`Tab`、`Escape`、`Up`、`Down`、`Left`、`Right`、`Backspace`。

读取返回 `text`、`cursor`（`streamID` / `offset`）、`changed`、`truncated`、`source`、最近输出时间和 `taskState: "unknown"`。它是最近终端屏幕与有限历史的快照，不是 Codex/Claude 的结构化消息记录。默认仅最近 200 行，最终文本最多 96 KiB。tmux 优先真实捕获；不支持安全捕获的远端 tmux 和普通 SSH 使用有界回放及 VT 解析，普通回放最多解析最近 2 MiB。历史裁剪与文本长度限制通过 `truncated` 表达。

携带上次的 `cursor` 与 `waitSeconds` 可以等待新输出；轮询只读取游标，结束时才捕获一次文本。20 秒是单次读取等待的上限，远程开发进程可继续运行数小时。网页滚动产生的终端重绘也可能改变游标；`changed` 和静默都不能独立证明开发任务完成。

`written` 表示输入写入终端。`rejected` 表示确认未写入。`uncertain` 表示写入过程或持久化未能确认；应先读取会话并由使用者判断，不能换请求编号自动补发。多行文本仅在终端已启用 bracketed paste 时允许，以免意外逐行执行；`submit: true` 才会在文本后附加回车。单独的按键通过 `key` 发送，正文中的其他终端控制字符会拒绝。

## 实现与撤销

- `internal/sessionbridge/`：令牌、权限、回执、VT 文本、MCP 传输与工具。
- `internal/api/session_bridge.go`：沿用浏览器认证的令牌管理接口、终端适配。
- `internal/terminal/bridge.go`：旁路快照、独立 SSH 命令通道、串行外部输入。
- `web/src/features/session-bridge/SessionBridgePanel.vue`：设置面板、一次性令牌、配置复制。
- 接入点仅在 `internal/api/api.go`（模块初始化、两组路由、精确 MCP Bearer CSRF 分流）、`internal/terminal/manager.go`（最近输出时间）与 `SettingsDrawer.vue`（入口 / 面板）。

每次 MCP 请求均重新验证专用令牌，工具执行前及等待期间继续检查权限与范围。停用、删除、缩小范围或停用账户后，后续操作立即受限；已经写入远端的命令仍由远端继续执行。令牌管理继续使用浏览器登录、CSRF 和锁屏保护，专用令牌不能调用管理 API，浏览器 Cookie 也不能代替 MCP 令牌。

令牌原文只返回一次，数据库保存 SHA-256。操作回执在写入前持久化，重启后继续识别相同请求编号。回执和审计不保存输入正文或令牌原文；审计记录令牌 ID、目标、请求编号、状态、字节数。令牌删除时级联删除回执。

整块移除时删除上述独立文件、测试、设置入口与 API 接入点，移除 `lastOutputAt` 字段 / 写入钩子，再运行 `go mod tidy` 清理依赖。附加表与既有会话表独立，可保留；若需清理数据，应先删 `session_bridge_receipts`，再删 `session_bridge_tokens`。无需改动主机、会话、凭据、浏览器认证表。

## 验证记录（2026-11-03）

- Go 全量测试与 `go vet ./...` 通过。
- 会话模块、终端和 API 的竞态测试通过，覆盖权限 / 用户隔离、过期 / 停用 / 撤销、真实 MCP 初始化 / 发现 / 调用、范围变更、输入去重、中文 / emoji / 光标重绘 / 备用屏幕。
- 使用独立 SSH 测试端与独立 tmux socket 验证捕获文本、不改变滚动位置 / 尺寸 / 控制权，以及取消读取后共享 SSH 仍可使用。
- 浏览器自动化覆盖创建、必填验证、一次性令牌、复制、编辑、主机过滤、停用、删除、加载错误与 390px 宽度；截图为测试数据。
- 前端单元测试、类型检查与生产构建通过。
- Linux 服务和 Windows Wails 程序编译通过；Windows 原生窗口运行情况仍需在 Windows 机器验证。

复现命令：

```sh
go test ./...
go vet ./...
go test -race ./internal/sessionbridge ./internal/terminal ./internal/api
cd web
npm test
npm run test:session-bridge
npm run build
```

开发构建位于 `artifacts/session-bridge/velin`、`build/bin/velin-session-bridge-dev.exe`；界面截图位于 `artifacts/session-bridge/`。当前生产 Docker 未重启或替换，现有会话继续由原服务维护。使用新接口需要部署新服务版本。
