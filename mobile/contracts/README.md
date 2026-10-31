# 移动协议 v1

本次实现的契约，日期 2026-10-31。服务端认证、CSRF、WebSocket Origin 和用户会话授权沿用现有规则。能力声明表示接口可用，不代表真机验收完成。

## 能力发现与订阅

登录、改密、解锁完成后请求 `GET /api/mobile/v1/capabilities`，响应不允许缓存：

```json
{
  "protocolVersion": 1,
  "viewAttach": true,
  "geometry": true,
  "inputAck": true,
  "maxInputBytes": 49152,
  "inputDedupeSeconds": 600,
  "inputDedupeCapacity": 512
}
```

缺少关键能力则停止连接，要求升级服务端。登录态失效、PIN 锁屏和首次改密保持对应错误流程。

恢复 HTTP 会话后连接 `/ws/sessions/{id}?attach=view&device=<UUID>`。`device` 是设备本地安装 UUID，不是认证凭据。原生层提供 Cookie 和服务地址的 Origin；不能把 Cookie、密码或完整 API 客户端放进 WebView。

`SubscribeView` 允许明确的后续 `request_control`，连接和重连本身均不取得控制。普通 Web 的默认订阅和分享的永久只读订阅保持原语义。只有控制者可以输入、resize、远端鼠标及操作 tmux 历史。心跳为原有 `ping` / `pong`；申请、批准、拒绝、释放复用原有消息。

## 网格与重放

`hello` 增加 `rows`、`cols`、`geometryVersion`，零版本可省略并按 0 处理。初始 PTY 为 30 行、120 列；成功 resize 后增加版本并广播：

```json
{"type":"geometry","rows":38,"cols":84,"geometryVersion":2}
```

同一次 resize 的尺寸事件在后续输出广播前排入订阅。查看者按服务端网格显示，屏幕不足时本地平移；键盘开合不自动改变远端网格。用户接管后可通过菜单手动适配手机宽度。

App 每次建立连接重置渲染器，从保留历史全量同步；暂不发送 stream / offset 增量续传参数。`hello.offset` 为订阅快照结束偏移；只有 `replay` 写入渲染器，`replay_preview` / `replay_live` 不重复追加。最终 replay 或 `replay_end` 使用渲染回执作为同步屏障。之后 `output.offset` 为字节结束偏移，重叠部分剔除，缺口停止同步并提供重连入口。

几何元数据目前只记录当前网格，历史字节环不携带各段 resize 边界。跨尺寸历史或截断后的 TUI 重建仍是待解决的验收项；`geometry` 不是完整屏幕快照协议。

## 输入回执

仅 `attach=view` 且带有效 device UUID 的连接可使用确认输入：

```json
{"type":"input","inputID":"<UUID>","data":"aGVsbG8N"}
```

```json
{"type":"input_ack","inputID":"<UUID>","result":"written","bytes":6}
```

| 字段 | 含义 |
| --- | --- |
| result=written | SSH 写入返回全部字节，不能据此判断命令执行结果 |
| result=rejected | 拒绝写入；code 可为 invalid_input、not_controller、input_id_conflict、input_cache_full |
| result=uncertain | 写入失败或部分写入，bytes 为返回的已写入数量；code=write_failed |
| duplicate=true | 返回同一有效编号的原回执，没有再次写入 |

输入须为 1～49152 原始字节，inputID 最长 80 字符。去重作用域为当前服务进程、终端 session、认证 token hash、device 和 inputID，保存负载 SHA256。相同编号但不同负载拒绝。每会话最多 512 条有效回执，保留 600 秒；满容量时拒绝新输入，不驱逐仍有效的回执。过期及服务重启后不承诺去重。

App 一次等待一条输入回执，15 秒未确认则保留草稿和“未确认”提示。断线、回执丢失、退后台均不自动重发。正文采用原始 UTF-8；启用 bracketed paste 时加包裹序列，“发送”最后增加一个 CR。正文控制字符拒绝，无 bracketed paste 的多行 / Tab 内容需要确认。

## WebView 桥

资源随包离线加载，CSP 禁止网络请求和内联脚本。外部导航被阻止；远端 ANSI 字节只能进入 xterm，不能作为 HTML 执行。

启动 `ready` 后原生绑定并发送 `configure`。后续消息统一携带 `version=1`、`sessionID`、随机 `generation`，不匹配的消息丢弃。原生连接另有 `connection` 计数，重连前的渲染回执不改变当前队列。

| 方向 | 消息 |
| --- | --- |
| 原生 → 页面 | configure、reset、write、setViewport、fit、control、application、font、theme、selection、bottom、copy、dispose |
| 页面 → 原生 | ready、configured、rendered、modes、geometry、selection、following、fit、scroll、click、bell |

`write` 含 base64 `data`、`bytes`、`id`、可选 `offset`、`connection`。xterm 的写入 callback 后才发 `rendered`；UTF-8 解码由 xterm 连续处理。reset 是有序屏障，以 epoch 丢弃旧回调。

`application` 区分 tmux 外层 alternate buffer 与真实 pane 的 alternateScreen：tmux 使用服务器 `history_state`，普通终端使用 xterm modes。查看模式仅浏览本地保留内容；接管后的 tmux 历史使用带 sequence 的绝对位置队列，TUI 滚动每次最多 3 行，点击带实际单元格坐标。远端滚动会影响共享的 tmux 视图。

渲染未回执达到 512 KiB 时暂停 WS 消费，低于 128 KiB 恢复，渲染队列超过 4 MiB 时要求重新同步。该策略限制应用渲染队列，仍需真机高输出内存测试。

## 生命周期与附件

退后台保存草稿、释放控制并关闭 WS，恢复时重新认证 / restore / 连接，仍从查看模式开始。退出页面释放连接，不 DELETE 会话。终止需要用户单独确认。

附件流式 PUT 使用当前页面固定的 hostID 和 sessionID。确认项目绝对目录后创建 `clipboard/`；文件名带时间及随机后缀防止覆盖。只有响应 offset 等于文件长度才插入经过 shell 引号处理的路径。取消和退出时关闭传输并清理本地选择器临时文件；远端部分文件和成功附件由用户管理，不自动删除。
