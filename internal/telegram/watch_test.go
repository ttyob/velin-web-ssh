package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWatchNotificationsCardsAndRevocation(t *testing.T) {
	m, sessions, api, binding, token := setup(t)
	ctx := context.Background()
	client := clientFor(t, m, api)
	if err := m.card(ctx, client, m.cfg, binding, token, "session-1", 0, false); err != nil {
		t.Fatal(err)
	}
	card := api.all()[0]
	if err := m.toggleWatch(ctx, binding, token, sessions.sessions[0], card.ID); err != nil {
		t.Fatal(err)
	}
	load := func() watchState {
		t.Helper()
		var raw string
		if err := m.store.DB.QueryRow("SELECT state FROM telegram_subscriptions WHERE user_id=? AND session_id=?", binding.UserID, "session-1").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var state watchState
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	save := func(state watchState) {
		t.Helper()
		raw, _ := json.Marshal(state)
		if _, err := m.store.DB.Exec("UPDATE telegram_subscriptions SET state=? WHERE user_id=? AND session_id=?", string(raw), binding.UserID, "session-1"); err != nil {
			t.Fatal(err)
		}
	}
	advance := func() {
		sessions.mu.Lock()
		sessions.offset++
		sessions.last = time.Now()
		sessions.mu.Unlock()
	}
	m.watchOnce(ctx, client, m.cfg)
	advance()
	m.watchOnce(ctx, client, m.cfg)
	if len(api.all()) != 1 || sessions.captures != 1 {
		t.Fatal("frequent polling captured output or edited the card")
	}
	state := load()
	state.LastCard = time.Now().Add(-31 * time.Second)
	save(state)
	advance()
	m.watchOnce(ctx, client, m.cfg)
	if messages := api.all(); len(messages) != 2 || messages[1].Method != "editMessageText" || messages[1].ID != card.ID || sessions.captures != 2 {
		t.Fatal("progress did not update the same card", messages)
	}
	state = load()
	state.LastOutput = time.Now().Add(-91 * time.Second)
	save(state)
	m.watchOnce(ctx, client, m.cfg)
	messages := api.all()
	if len(messages) != 3 || !strings.Contains(messages[2].Text, "静默不代表任务完成") {
		t.Fatal("missing accurate idle notification", messages)
	}
	var route string
	if err := m.store.DB.QueryRow("SELECT session_id FROM telegram_routes WHERE chat_id=? AND message_id=?", binding.ChatID, messages[2].ID).Scan(&route); err != nil || route != "session-1" {
		t.Fatal("notification replies have no session route", route, err)
	}
	m.watchOnce(ctx, client, m.cfg)
	if len(api.all()) != 3 {
		t.Fatal("duplicate idle notification")
	}
	if _, err := m.store.DB.Exec("UPDATE session_bridge_tokens SET enabled=0 WHERE id=?", token.ID); err != nil {
		t.Fatal(err)
	}
	advance()
	m.watchOnce(ctx, client, m.cfg)
	if len(api.all()) != 3 || sessions.captures != 2 {
		t.Fatal("revoked token continued reading or notifying")
	}
	if _, err := m.store.DB.Exec(`UPDATE session_bridge_tokens SET enabled=1,host_ids='[]',session_ids='["session-2"]' WHERE id=?`, token.ID); err != nil {
		t.Fatal(err)
	}
	m.watchOnce(ctx, client, m.cfg)
	if m.subscribed(binding, "session-1") || len(api.all()) != 3 {
		t.Fatal("inaccessible session subscription was not removed")
	}
}
