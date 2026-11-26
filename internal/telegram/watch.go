package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/sessionbridge"
)

type watchState struct {
	Connected  bool                 `json:"connected"`
	Status     string               `json:"status"`
	Cursor     sessionbridge.Cursor `json:"cursor"`
	LastOutput time.Time            `json:"lastOutput"`
	Active     bool                 `json:"active"`
	Idle       bool                 `json:"idle"`
	LastPush   time.Time            `json:"lastPush"`
	CardID     int64                `json:"cardID"`
	LastCard   time.Time            `json:"lastCard"`
}
type Subscription struct {
	SessionID string `json:"sessionID"`
	Name      string `json:"name"`
	HostName  string `json:"hostName"`
}

func (m *Module) Subscriptions(userID string) ([]Subscription, error) {
	rows, err := m.store.DB.Query("SELECT session_id FROM telegram_subscriptions WHERE user_id=? ORDER BY session_id", userID)
	if err != nil {
		return nil, err
	}
	items := []Subscription{}
	for rows.Next() {
		var item Subscription
		if err = rows.Scan(&item.SessionID); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if session, err := m.store.Terminal(userID, items[i].SessionID); err == nil {
			items[i].Name = session.Name
			if host, err := m.store.Host(userID, session.HostID); err == nil {
				items[i].HostName = host.Name
			}
		}
	}
	return items, nil
}
func (m *Module) Unsubscribe(userID, id string) error {
	_, err := m.store.DB.Exec("DELETE FROM telegram_subscriptions WHERE user_id=? AND session_id=?", userID, id)
	return err
}
func (m *Module) toggleWatch(ctx context.Context, b Binding, token sessionbridge.Token, s sessionbridge.Session, cardID int64) error {
	if m.subscribed(b, s.ID) {
		return m.Unsubscribe(b.UserID, s.ID)
	}
	var count int
	if err := m.store.DB.QueryRow("SELECT COUNT(*) FROM telegram_subscriptions WHERE user_id=?", b.UserID).Scan(&count); err != nil {
		return err
	}
	if count >= 20 {
		return errors.New("最多同时订阅 20 个会话，请先关闭部分通知")
	}
	state := watchState{Connected: s.Attached, Status: stateLabel(s), CardID: cardID, LastCard: time.Now()}
	if s.Attached {
		snapshot, err := m.bridge.SessionCursor(ctx, token, s.ID)
		if err != nil {
			return err
		}
		state.Cursor = snapshot.Cursor
		state.LastOutput = snapshot.LastOutputAt
	}
	encoded, _ := json.Marshal(state)
	_, err := m.store.DB.Exec("INSERT INTO telegram_subscriptions(user_id,session_id,state) VALUES(?,?,?)", b.UserID, s.ID, string(encoded))
	return err
}

// transition reports observations, never completion or an inferred CLI state.
func transition(previous watchState, session sessionbridge.Session, snapshot sessionbridge.Snapshot, now time.Time, idle time.Duration) (watchState, string) {
	next := previous
	next.Connected = session.Attached
	next.Status = stateLabel(session)
	notice := ""
	if next.Connected != previous.Connected || next.Status != previous.Status {
		notice = "连接状态：" + next.Status
		next.Active = false
		next.Idle = false
	}
	if next.Connected {
		if snapshot.Cursor != previous.Cursor {
			if previous.Idle && notice == "" && now.Sub(previous.LastPush) >= 30*time.Second {
				notice = "会话收到新输出"
			}
			next.Active = true
			next.Idle = false
			next.Cursor = snapshot.Cursor
			next.LastOutput = snapshot.LastOutputAt
			if next.LastOutput.IsZero() {
				next.LastOutput = now
			}
		}
		if next.Active && !next.Idle && !next.LastOutput.IsZero() && now.Sub(next.LastOutput) >= idle {
			next.Idle = true
			next.Active = false
			if notice == "" {
				notice = "持续一段时间无新输出，可查看当前进度。静默不代表任务完成。"
			}
		}
	}
	if notice != "" {
		next.LastPush = now
	}
	return next, notice
}
func (m *Module) watch(ctx context.Context, c *Client, cfg config) {
	interval := m.options.PollInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.watchOnce(ctx, c, cfg)
		}
	}
}
func (m *Module) watchOnce(ctx context.Context, c *Client, cfg config) {
	type watched struct{ user, id, raw string }
	rows, err := m.store.DB.Query("SELECT user_id,session_id,state FROM telegram_subscriptions")
	if err != nil {
		return
	}
	items := []watched{}
	for rows.Next() {
		var item watched
		if err = rows.Scan(&item.user, &item.id, &item.raw); err != nil {
			break
		}
		items = append(items, item)
	}
	rows.Close()
	if err != nil {
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		opCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		m.watchSession(opCtx, c, cfg, item.user, item.id, item.raw)
		cancel()
	}
}
func (m *Module) watchSession(ctx context.Context, c *Client, cfg config, userID, id, raw string) {
	b, err := m.Binding(userID)
	if err != nil || b.ChatID == 0 || b.BotID != cfg.Bot.ID {
		return
	}
	token, err := m.authorized(b)
	if err != nil {
		return
	}
	s, err := m.bridge.CheckSession(ctx, token, id, sessionbridge.Read)
	if err != nil {
		_ = m.Unsubscribe(userID, id)
		return
	}
	var previous watchState
	if json.Unmarshal([]byte(raw), &previous) != nil {
		return
	}
	var snapshot sessionbridge.Snapshot
	if s.Attached {
		snapshot, err = m.bridge.SessionCursor(ctx, token, id)
		if err != nil {
			return
		}
	}
	now := time.Now()
	next, notice := transition(previous, s, snapshot, now, time.Duration(cfg.IdleSeconds)*time.Second)
	if notice != "" {
		if _, err = m.authorized(b); err != nil {
			return
		}
		buttons := [][]Button{{{Text: "查看进度", Data: "v:" + id}, {Text: "关闭通知", Data: "w:" + id}}}
		if link := workspaceURL(cfg, id); link != "" {
			buttons = append(buttons, []Button{{Text: "打开终端", URL: link}})
		}
		message, err := c.send(ctx, b.ChatID, "<b>"+sessionLabel(s)+"</b>\n"+esc(notice), Keyboard{Rows: buttons})
		if err != nil {
			return
		}
		_ = m.route(b, message.ID, id, "input")
	}
	if next.CardID != 0 && s.Attached && snapshot.Cursor != previous.Cursor && now.Sub(previous.LastCard) >= 30*time.Second {
		if err = m.card(ctx, c, cfg, b, token, id, next.CardID, false); err != nil {
			next.CardID = 0
		}
		next.LastCard = now
	}
	encoded, _ := json.Marshal(next)
	// A concurrent unsubscribe or re-subscribe must not be overwritten by an
	// older poll that was waiting on SSH or Telegram.
	_, _ = m.store.DB.Exec("UPDATE telegram_subscriptions SET state=? WHERE user_id=? AND session_id=? AND state=?", string(encoded), userID, id, raw)
}
