package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/ttyob/velin-web-ssh/internal/sessionbridge"
)

type action struct {
	Kind      string `json:"kind"`
	SessionID string `json:"sessionID,omitempty"`
	Text      string `json:"text,omitempty"`
	File      *File  `json:"file,omitempty"`
	RequestID string `json:"requestID,omitempty"`
}

func text(value string, limit int) string {
	var out strings.Builder
	units := 0
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		n := 1
		if r > 0xffff {
			n = 2
		}
		if units+n > limit {
			out.WriteString("…")
			break
		}
		out.WriteRune(r)
		units += n
	}
	return out.String()
}
func esc(value string) string { return html.EscapeString(value) }
func requestID(bot, update int64, kind string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("telegram:%d:%d:%s", bot, update, kind))).String()
}
func sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (m *Module) poll(ctx context.Context, client *Client, cfg config) {
	backoff := time.Second
	probed := false
	for ctx.Err() == nil {
		if !probed {
			probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			bot, err := client.validate(probeCtx)
			cancel()
			if err != nil || bot.ID != cfg.Bot.ID {
				if ctx.Err() != nil {
					return
				}
				message := "机器人身份已变化，请重新保存配置"
				if err != nil {
					message = err.Error()
				}
				m.setState("error", message)
				if !sleep(ctx, backoff) {
					return
				}
				backoff = min(30*time.Second, backoff*2)
				continue
			}
			probed = true
			m.setState("running", "")
			commandsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = client.call(commandsCtx, "setMyCommands", map[string]any{"commands": []map[string]string{{"command": "sessions", "description": "查看开发会话"}, {"command": "help", "description": "使用说明"}, {"command": "unbind", "description": "解除账号绑定"}}}, nil)
			cancel()
		}
		var offset int64
		err := m.store.DB.QueryRow("SELECT next_update FROM telegram_runtime WHERE bot_id=?", cfg.Bot.ID).Scan(&offset)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			m.setState("error", "无法读取 Telegram 接收进度")
			if !sleep(ctx, 5*time.Second) {
				return
			}
			continue
		}
		updates, err := client.updates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.setState("error", err.Error())
			delay := backoff
			var api *apiError
			if errors.As(err, &api) && api.Retry > 0 {
				delay = time.Duration(min(api.Retry, 60)) * time.Second
			}
			if !sleep(ctx, delay) {
				return
			}
			backoff = min(30*time.Second, backoff*2)
			continue
		}
		m.setState("running", "")
		backoff = time.Second
		for _, update := range updates {
			if ctx.Err() != nil {
				return
			}
			// Persist admission before any PTY/file side effect. An interrupted
			// update is never automatically replayed after restarting the bot.
			result, err := m.store.DB.Exec(`INSERT INTO telegram_runtime(bot_id,next_update) VALUES(?,?) ON CONFLICT(bot_id) DO UPDATE SET next_update=excluded.next_update WHERE telegram_runtime.next_update<excluded.next_update`, cfg.Bot.ID, update.ID+1)
			if err != nil {
				m.setState("error", "无法保存 Telegram 接收进度")
				break
			}
			if n, _ := result.RowsAffected(); n == 0 {
				continue
			}
			opCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
			m.handle(opCtx, client, cfg, update)
			cancel()
		}
		_, _ = m.store.DB.Exec("DELETE FROM telegram_actions WHERE expires_at<?", stamp(time.Now()))
		_, _ = m.store.DB.Exec("DELETE FROM telegram_routes WHERE expires_at<?", stamp(time.Now()))
		if len(updates) == 0 {
			if !sleep(ctx, 300*time.Millisecond) {
				return
			}
		}
	}
}
func (m *Module) handle(ctx context.Context, c *Client, cfg config, update Update) {
	if update.Callback != nil {
		cb := update.Callback
		if cb.Message == nil || cb.Message.Chat.Type != "private" {
			c.answer(ctx, cb.ID, "请在私聊中操作")
			return
		}
		b, token, err := m.authenticate(cb.From.ID, cb.Message.Chat.ID, cfg.Bot.ID)
		if err != nil {
			c.answer(ctx, cb.ID, "绑定或授权已失效，请在工作台重新设置")
			return
		}
		c.answer(ctx, cb.ID, "")
		if err = m.callback(ctx, c, cfg, b, token, *cb, update.ID); err != nil {
			m.report(ctx, c, b.ChatID, err)
		}
		return
	}
	msg := update.Message
	if msg == nil || msg.Chat.Type != "private" || msg.From.ID == 0 || msg.Chat.ID != msg.From.ID {
		return
	}
	if msg.Date > 0 && time.Since(time.Unix(msg.Date, 0)) > 5*time.Minute {
		return
	}
	parts := strings.Fields(msg.Text)
	if len(parts) == 2 && strings.Split(parts[0], "@")[0] == "/start" {
		b, err := m.pair(*msg, parts[1], cfg.Bot)
		if err != nil {
			m.report(ctx, c, msg.Chat.ID, err)
			return
		}
		_, _ = c.send(ctx, b.ChatID, "<b>绑定成功</b>\n选择一个会话查看进度。回复会话卡片即可发送需求。", nil)
		token, err := m.authorized(b)
		if err == nil {
			err = m.list(ctx, c, b, token, 0, "", "")
		}
		if err != nil {
			m.report(ctx, c, b.ChatID, err)
		}
		return
	}
	b, token, err := m.authenticate(msg.From.ID, msg.Chat.ID, cfg.Bot.ID)
	if err != nil {
		_, _ = c.send(ctx, msg.Chat.ID, "请在 Jeex Terminal 的「设置 → Telegram」生成绑定链接，再点击链接完成绑定。", nil)
		return
	}
	command := ""
	if len(parts) > 0 {
		command = strings.Split(parts[0], "@")[0]
	}
	if msg.Reply != nil {
		command = ""
	}
	switch command {
	case "/start", "/sessions":
		err = m.list(ctx, c, b, token, 0, "", "")
	case "/help":
		_, err = c.send(ctx, b.ChatID, "<b>随身会话控制台</b>\n/sessions 查看会话\n/unbind 解除绑定\n\n回复会话卡片发送需求；图片和文档也可以回复卡片上传。直接发送的内容会先让你选择会话。\n\n通知描述连接和输出变化，持续无输出并不代表任务完成。", nil)
	case "/unbind":
		var id string
		id, err = m.putAction(b, action{Kind: "unbind"})
		if err == nil {
			_, err = c.send(ctx, b.ChatID, "解除绑定后将停止本账号的 Telegram 操作和通知。", Keyboard{Rows: [][]Button{{{Text: "确认解除绑定", Data: "a:" + id}}}})
		}
	default:
		err = m.incoming(ctx, c, cfg, b, token, *msg, update.ID)
	}
	if err != nil {
		m.report(ctx, c, b.ChatID, err)
	}
}
func (m *Module) report(ctx context.Context, c *Client, chat int64, err error) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	// A bounded operation may time out after writing to the terminal. Use a
	// separate short request to report this without replaying its side effects.
	message := err.Error()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		message = "操作响应超时，请先查看会话和文件状态，再决定是否重试。远端任务不会因此被终止。"
	}
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = c.send(reportCtx, chat, esc(text(message, 1200)), nil)
}
func (m *Module) route(b Binding, messageID int64, sessionID, kind string) error {
	_, err := m.store.DB.Exec(`INSERT INTO telegram_routes(user_id,chat_id,message_id,session_id,kind,expires_at) VALUES(?,?,?,?,?,?) ON CONFLICT(chat_id,message_id) DO UPDATE SET session_id=excluded.session_id,kind=excluded.kind,expires_at=excluded.expires_at`, b.UserID, b.ChatID, messageID, sessionID, kind, stamp(time.Now().Add(7*24*time.Hour)))
	return err
}
func (m *Module) putAction(b Binding, value action) (string, error) {
	if _, err := m.authorized(b); err != nil {
		return "", err
	}
	id := randomID()[:16]
	raw, _ := json.Marshal(value)
	encrypted, err := m.vault.Encrypt(string(raw))
	if err != nil {
		return "", err
	}
	_, err = m.store.DB.Exec(`INSERT INTO telegram_actions(id,user_id,chat_id,payload,expires_at) VALUES(?,?,?,?,?)`, id, b.UserID, b.ChatID, encrypted, stamp(time.Now().Add(10*time.Minute)))
	return id, err
}
func (m *Module) getAction(b Binding, id string, consume bool) (action, error) {
	var value action
	var encrypted string
	query := "SELECT payload FROM telegram_actions WHERE id=? AND user_id=? AND chat_id=? AND expires_at>?"
	if consume {
		query = "DELETE FROM telegram_actions WHERE id=? AND user_id=? AND chat_id=? AND expires_at>? RETURNING payload"
	}
	if err := m.store.DB.QueryRow(query, id, b.UserID, b.ChatID, stamp(time.Now())).Scan(&encrypted); err != nil {
		return value, errors.New("此操作已处理或已过期，请重新发送消息或刷新卡片")
	}
	raw, err := m.vault.Decrypt(encrypted)
	if err != nil {
		return value, errors.New("无法读取临时消息，请重新发送")
	}
	err = json.Unmarshal([]byte(raw), &value)
	return value, err
}
func (m *Module) list(ctx context.Context, c *Client, b Binding, token sessionbridge.Token, page int, pending, title string) error {
	sessions, err := m.bridge.ListSessions(ctx, token)
	if err != nil {
		return err
	}
	if page < 0 || page > max(0, len(sessions)-1)/8 {
		page = 0
	}
	if title == "" {
		title = "选择会话"
	}
	rows := [][]Button{}
	for _, s := range sessions[page*8 : min(len(sessions), page*8+8)] {
		callback := "v:" + s.ID
		if pending != "" {
			callback = "a:" + pending + ":" + s.ID
		}
		rows = append(rows, []Button{{Text: text(s.Name+" · "+s.HostName, 60), Data: callback}})
	}
	nav := []Button{}
	prefix := "p:"
	if pending != "" {
		prefix = "z:" + pending + ":"
	}
	if page > 0 {
		nav = append(nav, Button{Text: "上一页", Data: prefix + strconv.Itoa(page-1)})
	}
	if (page+1)*8 < len(sessions) {
		nav = append(nav, Button{Text: "下一页", Data: prefix + strconv.Itoa(page+1)})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	if pending == "" && token.Allows(sessionbridge.Create) {
		rows = append(rows, []Button{{Text: "新建 tmux 会话", Data: "h:0"}})
	}
	if len(sessions) == 0 {
		title = "当前没有可访问的会话，请检查授权范围或先创建会话。"
	}
	_, err = c.send(ctx, b.ChatID, "<b>"+esc(title)+"</b>", Keyboard{Rows: rows})
	return err
}
func workspaceURL(cfg config, id string) string {
	if cfg.PublicURL == "" {
		return ""
	}
	target, err := url.Parse(cfg.PublicURL + "/workspace")
	if err != nil {
		return ""
	}
	query := target.Query()
	query.Set("session", id)
	target.RawQuery = query.Encode()
	return target.String()
}
func sessionLabel(s sessionbridge.Session) string {
	return esc(text(s.Name, 100)) + " · " + esc(text(s.HostName, 80))
}
func stateLabel(s sessionbridge.Session) string {
	if s.Attached {
		return "连接正常"
	}
	switch s.Status {
	case "connecting", "pending", "restoring", "creating", "reconnecting":
		return "正在连接"
	case "error", "failed", "unreachable":
		return "连接异常"
	case "closed", "terminated", "ended":
		return "已结束"
	case "auth_required":
		return "需要身份验证"
	case "host_key_required":
		return "需要确认主机指纹"
	case "ownership_error":
		return "会话归属异常"
	case "background":
		return "后台会话，未连接"
	default:
		return "未连接"
	}
}
func (m *Module) subscribed(b Binding, id string) bool {
	var count int
	_ = m.store.DB.QueryRow("SELECT COUNT(*) FROM telegram_subscriptions WHERE user_id=? AND session_id=?", b.UserID, id).Scan(&count)
	return count > 0
}
func (m *Module) card(ctx context.Context, c *Client, cfg config, b Binding, token sessionbridge.Token, id string, edit int64, more bool) error {
	s, err := m.bridge.CheckSession(ctx, token, id, sessionbridge.Read)
	if err != nil {
		return err
	}
	output := ""
	recent := ""
	if s.Attached {
		result, readErr := m.bridge.ReadSession(ctx, token, id, 35)
		if readErr != nil {
			output = readErr.Error()
		} else {
			output = result.Text
			if result.LastOutputAt != nil {
				recent = " · 最近输出：" + result.LastOutputAt.Local().Format("15:04:05")
			}
		}
	} else {
		output = s.LastError
	}
	if output == "" {
		output = "暂无可读取的输出"
	}
	content := "<b>" + sessionLabel(s) + "</b>\n" + stateLabel(s) + recent + "\n\n<pre>" + esc(text(output, 2300)) + "</pre>\n回复此卡片可发送需求。"
	rows := [][]Button{{{Text: "刷新进度", Data: "r:" + id}, {Text: "更多输出", Data: "o:" + id}}}
	watchLabel := "开启通知"
	if m.subscribed(b, id) {
		watchLabel = "关闭通知"
	}
	rows = append(rows, []Button{{Text: watchLabel, Data: "w:" + id}, {Text: "所有会话", Data: "p:0"}})
	if token.Allows(sessionbridge.Input) {
		rows = append(rows, []Button{{Text: "发送需求", Data: "i:" + id}, {Text: "中断", Data: "c:" + id}, {Text: "更多操作", Data: "m:" + id}})
	} else {
		rows = append(rows, []Button{{Text: "更多操作", Data: "m:" + id}})
	}
	if link := workspaceURL(cfg, id); link != "" {
		rows = append(rows, []Button{{Text: "打开终端", URL: link}})
	}
	if more {
		if token.Allows(sessionbridge.Input) {
			rows = append(rows, []Button{{Text: "回车", Data: "k:Enter:" + id}, {Text: "取消 Esc", Data: "k:Escape:" + id}, {Text: "Tab", Data: "k:Tab:" + id}}, []Button{{Text: "↑", Data: "k:Up:" + id}, {Text: "↓", Data: "k:Down:" + id}, {Text: "←", Data: "k:Left:" + id}, {Text: "→", Data: "k:Right:" + id}})
		}
		if token.Allows(sessionbridge.Create) && s.Mode != "normal" && !s.Attached {
			rows = append(rows, []Button{{Text: "恢复 tmux 连接", Data: "u:" + id}})
		}
		if b.AllowFiles {
			rows = append(rows, []Button{{Text: "下载项目文件", Data: "g:" + id}})
		}
		if token.Allows(sessionbridge.Manage) {
			rows = append(rows, []Button{{Text: "重命名", Data: "j:" + id}, {Text: "终止会话", Data: "t:" + id}})
		}
	}
	if _, err = m.authorized(b); err != nil {
		return err
	}
	if edit != 0 {
		if err = c.edit(ctx, b.ChatID, edit, content, Keyboard{Rows: rows}); err != nil {
			return err
		}
		return m.route(b, edit, id, "input")
	}
	message, err := c.send(ctx, b.ChatID, content, Keyboard{Rows: rows})
	if err != nil {
		return err
	}
	return m.route(b, message.ID, id, "input")
}
func (m *Module) prompt(ctx context.Context, c *Client, b Binding, id, kind, prompt string) error {
	message, err := c.send(ctx, b.ChatID, esc(prompt), map[string]any{"force_reply": true, "selective": true, "input_field_placeholder": text(prompt, 60)})
	if err != nil {
		return err
	}
	return m.route(b, message.ID, id, kind)
}
func (m *Module) incoming(ctx context.Context, c *Client, cfg config, b Binding, token sessionbridge.Token, msg Message, update int64) error {
	value := action{Kind: "input", Text: msg.Text, RequestID: requestID(cfg.Bot.ID, update, "input")}
	if msg.Document != nil {
		file := *msg.Document
		value.File = &file
	}
	if len(msg.Photo) > 0 {
		file := msg.Photo[len(msg.Photo)-1]
		file.Name = "photo.jpg"
		value.File = &file
	}
	if value.File != nil {
		value.Kind = "upload"
		value.Text = msg.Caption
		if !b.AllowFiles {
			return errors.New("文件传输尚未授权，请在工作台 Telegram 设置中开启")
		}
	}
	if value.File == nil && value.Text == "" {
		return errors.New("目前支持文本、图片和文档；语音消息尚未接入")
	}
	if len(value.Text) > 48<<10 {
		return errors.New("消息内容过长，请缩短后发送")
	}
	var target, kind string
	if msg.Reply != nil {
		_ = m.store.DB.QueryRow("SELECT session_id,kind FROM telegram_routes WHERE user_id=? AND chat_id=? AND message_id=? AND expires_at>?", b.UserID, b.ChatID, msg.Reply.ID, stamp(time.Now())).Scan(&target, &kind)
	}
	if target != "" {
		value.SessionID = target
		if kind != "input" {
			if value.File != nil {
				return errors.New("请回复会话卡片上传文件")
			}
			value.Kind = kind
		}
		return m.execute(ctx, c, cfg, b, token, value)
	}
	if value.Kind == "input" && !token.Allows(sessionbridge.Input) {
		return errors.New("发送需求尚未授权，请在工作台 Telegram 设置中开启")
	}
	id, err := m.putAction(b, value)
	if err != nil {
		return err
	}
	return m.list(ctx, c, b, token, 0, id, "将这条消息发送到哪个会话？（10 分钟内有效）")
}
func (m *Module) callback(ctx context.Context, c *Client, cfg config, b Binding, token sessionbridge.Token, cb Callback, update int64) error {
	parts := strings.Split(cb.Data, ":")
	if len(parts) < 2 {
		return errors.New("按钮无效，请重新打开会话列表")
	}
	op, id := parts[0], parts[1]
	if op == "p" {
		page, _ := strconv.Atoi(id)
		return m.list(ctx, c, b, token, page, "", "")
	}
	if op == "z" {
		if _, err := m.getAction(b, id, false); err != nil {
			return err
		}
		page := 0
		if len(parts) > 2 {
			page, _ = strconv.Atoi(parts[2])
		}
		return m.list(ctx, c, b, token, page, id, "选择这条消息的目标会话")
	}
	if op == "h" {
		return m.hosts(ctx, c, b, token, id)
	}
	if op == "n" {
		hosts, err := m.bridge.ListHosts(ctx, token)
		if err != nil {
			return err
		}
		for _, h := range hosts {
			if h.ID == id && token.Allows(sessionbridge.Create) {
				return m.prompt(ctx, c, b, id, "create", "为「"+h.Name+"」的新 tmux 会话取个名字")
			}
		}
		return errors.New("主机未授权创建会话")
	}
	if op == "a" {
		value, err := m.getAction(b, id, true)
		if err != nil {
			return err
		}
		if value.SessionID == "" && len(parts) > 2 {
			value.SessionID = parts[2]
		}
		return m.execute(ctx, c, cfg, b, token, value)
	}
	if op == "k" {
		if len(parts) != 3 {
			return errors.New("按键参数无效")
		}
		return m.execute(ctx, c, cfg, b, token, action{Kind: "key", SessionID: parts[2], Text: id, RequestID: requestID(cfg.Bot.ID, update, "key")})
	}
	s, err := m.bridge.CheckSession(ctx, token, id, sessionbridge.Read)
	if err != nil {
		return err
	}
	switch op {
	case "v":
		return m.card(ctx, c, cfg, b, token, id, 0, false)
	case "r", "m":
		return m.card(ctx, c, cfg, b, token, id, cb.Message.ID, op == "m")
	case "o":
		result, err := m.bridge.ReadSession(ctx, token, id, 1000)
		if err != nil {
			return err
		}
		message, err := c.document(ctx, b.ChatID, "terminal-output.txt", []byte(result.Text))
		if err != nil {
			return err
		}
		return m.route(b, message.ID, id, "input")
	case "i":
		if !token.Allows(sessionbridge.Input) {
			return errors.New("未授权发送输入")
		}
		return m.prompt(ctx, c, b, id, "input", "回复此消息，发送需求到「"+s.Name+"」")
	case "g":
		if !b.AllowFiles {
			return errors.New("文件传输尚未授权")
		}
		return m.prompt(ctx, c, b, id, "download", "回复项目内的相对文件路径，例如 logs/build.log")
	case "j":
		if !token.Allows(sessionbridge.Manage) {
			return errors.New("未授权管理会话")
		}
		return m.prompt(ctx, c, b, id, "rename", "回复「"+s.Name+"」的新名称")
	case "u":
		result, err := m.bridge.RestoreSession(ctx, token, sessionbridge.RestoreArgs{SessionID: id, RequestID: requestID(cfg.Bot.ID, update, "restore")})
		if err != nil {
			return err
		}
		if result.Status != "restored" {
			return errors.New(result.Message)
		}
		return m.card(ctx, c, cfg, b, token, id, 0, false)
	case "w":
		if err = m.toggleWatch(ctx, b, token, s, cb.Message.ID); err != nil {
			return err
		}
		return m.card(ctx, c, cfg, b, token, id, cb.Message.ID, false)
	case "c", "t":
		permission := sessionbridge.Input
		kind := "interrupt"
		label := "确认中断"
		description := "向当前会话发送 Ctrl+C，中断前台任务？"
		if op == "t" {
			permission = sessionbridge.Manage
			kind = "terminate"
			label = "确认终止会话"
			description = "终止会话及远端 tmux 进程？当前任务会结束。"
		}
		if _, err = m.bridge.CheckSession(ctx, token, id, permission); err != nil {
			return err
		}
		actionID, err := m.putAction(b, action{Kind: kind, SessionID: id, RequestID: requestID(cfg.Bot.ID, update, kind)})
		if err != nil {
			return err
		}
		_, err = c.send(ctx, b.ChatID, "<b>"+sessionLabel(s)+"</b>\n"+description, Keyboard{Rows: [][]Button{{{Text: label, Data: "a:" + actionID}, {Text: "返回", Data: "v:" + id}}}})
		return err
	}
	return errors.New("按钮已失效，请重新打开会话")
}
func (m *Module) hosts(ctx context.Context, c *Client, b Binding, token sessionbridge.Token, pageText string) error {
	if !token.Allows(sessionbridge.Create) {
		return errors.New("未授权创建会话")
	}
	hosts, err := m.bridge.ListHosts(ctx, token)
	if err != nil {
		return err
	}
	page, _ := strconv.Atoi(pageText)
	if page < 0 || page > max(0, len(hosts)-1)/8 {
		page = 0
	}
	rows := [][]Button{}
	for _, h := range hosts[page*8 : min(len(hosts), page*8+8)] {
		rows = append(rows, []Button{{Text: text(h.Name, 70), Data: "n:" + h.ID}})
	}
	nav := []Button{}
	if page > 0 {
		nav = append(nav, Button{Text: "上一页", Data: "h:" + strconv.Itoa(page-1)})
	}
	if (page+1)*8 < len(hosts) {
		nav = append(nav, Button{Text: "下一页", Data: "h:" + strconv.Itoa(page+1)})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []Button{{Text: "返回会话", Data: "p:0"}})
	_, err = c.send(ctx, b.ChatID, "<b>选择主机创建 tmux 会话</b>", Keyboard{Rows: rows})
	return err
}
func (m *Module) execute(ctx context.Context, c *Client, cfg config, b Binding, token sessionbridge.Token, value action) error {
	current, err := m.authorized(b)
	if err != nil {
		return err
	}
	token = current
	if value.Kind == "unbind" {
		if err = m.Unbind(b.UserID); err != nil {
			return err
		}
		_, err = c.send(ctx, b.ChatID, "已解除绑定，操作和通知已停止。", nil)
		return err
	}
	if value.Kind == "create" {
		result, err := m.bridge.CreateSession(ctx, token, sessionbridge.CreateArgs{HostID: value.SessionID, Name: strings.TrimSpace(value.Text), Mode: "tmux", RequestID: value.RequestID})
		if err != nil {
			return err
		}
		if result.Session == nil {
			return errors.New(result.Message)
		}
		return m.card(ctx, c, cfg, b, token, result.Session.ID, 0, false)
	}
	s, err := m.bridge.CheckSession(ctx, token, value.SessionID, sessionbridge.Read)
	if err != nil {
		return err
	}
	switch value.Kind {
	case "input", "key", "interrupt", "path":
		args := sessionbridge.InputArgs{SessionID: s.ID, RequestID: value.RequestID, Text: value.Text, Submit: true}
		if value.Kind == "key" || value.Kind == "interrupt" {
			args.Text = ""
			args.Submit = false
			args.Key = value.Text
			if value.Kind == "interrupt" {
				args.Key = "Ctrl+C"
			}
		}
		if value.Kind == "path" {
			args.Submit = false
		}
		result, err := m.bridge.SendInput(ctx, token, args)
		if err != nil {
			return err
		}
		message := "<b>" + sessionLabel(s) + "</b>\n" + esc(result.Message)
		if result.Status == "written" {
			message = "<b>" + sessionLabel(s) + "</b>\n已送达终端，执行结果请查看进度。"
			if value.Kind == "path" {
				message = "<b>" + sessionLabel(s) + "</b>\n内容已粘贴到终端，尚未发送回车。"
			}
		}
		msg, err := c.send(ctx, b.ChatID, message, Keyboard{Rows: [][]Button{{{Text: "查看进度", Data: "v:" + s.ID}}}})
		if err != nil {
			return err
		}
		return m.route(b, msg.ID, s.ID, "input")
	case "rename":
		_, err = m.bridge.RenameSession(ctx, token, sessionbridge.RenameArgs{SessionID: s.ID, Name: strings.TrimSpace(value.Text)})
		if err != nil {
			return err
		}
		return m.card(ctx, c, cfg, b, token, s.ID, 0, false)
	case "terminate":
		result, err := m.bridge.TerminateSession(ctx, token, sessionbridge.TerminateArgs{SessionID: s.ID, RequestID: value.RequestID, Confirm: true})
		if err != nil {
			return err
		}
		if result.Status != "terminated" {
			return errors.New(result.Message)
		}
		message := "已终止「" + s.Name + "」"
		if result.Termination != nil && result.Termination.Warning != "" {
			message += "\n" + result.Termination.Warning
		}
		_, err = c.send(ctx, b.ChatID, esc(message), nil)
		return err
	case "upload":
		binding, err := m.Binding(b.UserID)
		if err != nil || !binding.AllowFiles || m.files == nil {
			return errors.New("文件传输尚未授权")
		}
		if value.File == nil {
			return errors.New("文件信息无效")
		}
		body, err := c.download(ctx, *value.File)
		if err != nil {
			return err
		}
		defer body.Close()
		remote, err := m.files.Upload(ctx, b.UserID, s.ID, value.File.Name, io.LimitReader(body, MaxFileSize+1))
		if err != nil {
			return err
		}
		_ = m.store.Audit(b.UserID, "telegram.upload", "terminal_session", s.ID, "", map[string]any{"tokenID": token.ID})
		buttons := [][]Button{{{Text: "查看会话", Data: "v:" + s.ID}}}
		caption := strings.TrimSpace(value.Text)
		content := "<b>" + sessionLabel(s) + "</b>\n文件已上传：\n<code>" + esc(remote) + "</code>"
		if caption != "" {
			content += "\n说明：" + esc(text(caption, 1000))
		}
		if token.Allows(sessionbridge.Input) {
			paste, label := remote, "粘贴路径到终端"
			if caption != "" {
				paste, label = caption+"\n"+remote, "粘贴说明和路径"
			}
			pending, err := m.putAction(b, action{Kind: "path", SessionID: s.ID, Text: paste, RequestID: value.RequestID})
			if err != nil {
				return err
			}
			buttons = append([][]Button{{{Text: label, Data: "a:" + pending}}}, buttons...)
		}
		_, err = c.send(ctx, b.ChatID, content+"\n点击粘贴后不会发送回车，请按具体 CLI 的文件输入方式使用。", Keyboard{Rows: buttons})
		return err
	case "download":
		binding, err := m.Binding(b.UserID)
		if err != nil || !binding.AllowFiles || m.files == nil {
			return errors.New("文件传输尚未授权")
		}
		name, data, err := m.files.Download(ctx, b.UserID, s.ID, strings.TrimSpace(value.Text))
		if err != nil {
			return err
		}
		if _, err = m.authorized(b); err != nil {
			return err
		}
		message, err := c.document(ctx, b.ChatID, name, data)
		if err != nil {
			return err
		}
		return m.route(b, message.ID, s.ID, "input")
	}
	return errors.New("操作已过期，请刷新会话卡片")
}
