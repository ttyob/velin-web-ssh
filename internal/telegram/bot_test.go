package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/sessionbridge"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

const fixtureToken = "123456:abcdefghijklmnopqrstuvwxyz_123456789"

type fakeSessions struct {
	mu       sync.Mutex
	sessions []sessionbridge.Session
	writes   [][]byte
	offset   uint64
	output   string
	last     time.Time
	captures int
}

func (f *fakeSessions) Sessions(_ context.Context, user string) ([]sessionbridge.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if user != "owner" {
		return []sessionbridge.Session{{ID: "other-session", HostID: "other-host", Name: "Other"}}, nil
	}
	return append([]sessionbridge.Session{}, f.sessions...), nil
}
func (f *fakeSessions) Hosts(_ context.Context, user string) ([]sessionbridge.Host, error) {
	if user != "owner" {
		return []sessionbridge.Host{{ID: "other-host"}}, nil
	}
	return []sessionbridge.Host{{ID: "host", Name: "开发服务器"}}, nil
}
func (f *fakeSessions) Snapshot(_ context.Context, _, _ string, lines int) (sessionbridge.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := f.output
	snapshot := sessionbridge.Snapshot{Cursor: sessionbridge.Cursor{StreamID: "stream", Offset: f.offset}, LastOutputAt: f.last, Source: "tmux-capture"}
	if lines > 0 {
		f.captures++
		snapshot.Capture = &value
	}
	return snapshot, nil
}
func (f *fakeSessions) WriteInput(ctx context.Context, _, _ string, data []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte{}, data...))
	return len(data), nil
}
func (f *fakeSessions) Create(_ context.Context, _, host, name, mode string) (sessionbridge.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := sessionbridge.Session{ID: "new-session", HostID: host, Name: name, Mode: mode, Attached: true}
	f.sessions = append(f.sessions, value)
	return value, nil
}
func (f *fakeSessions) Restore(context.Context, string, string) (sessionbridge.Session, error) {
	return sessionbridge.Session{}, nil
}
func (f *fakeSessions) Rename(_ context.Context, _, id, name string) (sessionbridge.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			f.sessions[i].Name = name
			return f.sessions[i], nil
		}
	}
	return sessionbridge.Session{}, errors.New("missing")
}
func (f *fakeSessions) Terminate(context.Context, string, string) (sessionbridge.Termination, error) {
	return sessionbridge.Termination{OK: true, RemoteTerminated: true}, nil
}
func (f *fakeSessions) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.writes) }

type fakeFiles struct {
	mu      sync.Mutex
	uploads int
	body    []byte
}

func (f *fakeFiles) Upload(_ context.Context, _, _, _ string, body io.Reader) (string, error) {
	data, err := io.ReadAll(body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body = data
	f.uploads++
	return "/project/clipboard/image.png", err
}
func (f *fakeFiles) Download(context.Context, string, string, string) (string, []byte, error) {
	return "build.log", []byte("build passed"), nil
}

type sent struct {
	Method string
	Chat   int64
	ID     int64
	Text   string
	Markup Keyboard
	Data   []byte
}
type fakeAPI struct {
	mu       sync.Mutex
	messages []sent
	next     int64
	updates  chan []Update
	webhook  string
	download []byte
	server   *httptest.Server
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{updates: make(chan []Update, 10), download: []byte("image-data")}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		result := any(true)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/file/") {
			_, _ = w.Write(f.download)
			return
		}
		switch method {
		case "getMe":
			result = Bot{ID: 123456, Username: "velin_fixture_bot", IsBot: true}
		case "getWebhookInfo":
			f.mu.Lock()
			result = map[string]string{"url": f.webhook}
			f.mu.Unlock()
		case "getUpdates":
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case updates := <-f.updates:
				result = updates
			case <-r.Context().Done():
				return
			case <-time.After(150 * time.Millisecond):
				result = []Update{}
			}
		case "getFile":
			result = File{Path: "photos/image.png", Size: int64(len(f.download))}
		case "sendMessage", "editMessageText":
			var body struct {
				Chat   int64           `json:"chat_id"`
				ID     int64           `json:"message_id"`
				Text   string          `json:"text"`
				Markup json.RawMessage `json:"reply_markup"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var markup Keyboard
			_ = json.Unmarshal(body.Markup, &markup)
			f.mu.Lock()
			id := body.ID
			if id == 0 {
				f.next++
				id = f.next
			}
			f.messages = append(f.messages, sent{Method: method, Chat: body.Chat, ID: id, Text: body.Text, Markup: markup})
			f.mu.Unlock()
			result = Message{ID: id, Chat: Chat{ID: body.Chat, Type: "private"}}
		case "sendDocument":
			if err := r.ParseMultipartForm(MaxFileSize + 1024); err != nil {
				t.Error(err)
				return
			}
			file, _, err := r.FormFile("document")
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := io.ReadAll(file)
			file.Close()
			f.mu.Lock()
			f.next++
			id := f.next
			f.messages = append(f.messages, sent{Method: method, ID: id, Data: data})
			f.mu.Unlock()
			result = Message{ID: id}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(f.server.Close)
	return f
}
func (f *fakeAPI) all() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sent{}, f.messages...)
}
func setup(t *testing.T) (*Module, *fakeSessions, *fakeAPI, Binding, sessionbridge.Token) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, user := range []string{"owner", "other"} {
		if err = db.CreateUser(user, user, "unused", "user"); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.SaveHost(store.Host{ID: "host", UserID: "owner", Name: "开发服务器", Address: "127.0.0.1", Port: 22, Username: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveHost(store.Host{ID: "other-host", UserID: "other", Name: "Other", Address: "127.0.0.1", Port: 22, Username: "fixture"}); err != nil {
		t.Fatal(err)
	}
	f := &fakeSessions{sessions: []sessionbridge.Session{{ID: "session-1", HostID: "host", HostName: "开发服务器", Name: "后端开发", Mode: "tmux", Status: "attached", Attached: true}, {ID: "session-2", HostID: "host", Name: "前端开发", Mode: "tmux", Status: "attached", Attached: true}}, offset: 1, output: "test passed\nready", last: time.Now()}
	bridge, err := sessionbridge.New(db, f)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := bridge.CreateToken("owner", sessionbridge.TokenInput{Name: "Telegram", Permissions: []string{"read", "input", "create", "manage"}, HostIDs: []string{"host"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	vault, err := security.LoadVault(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	api := newFakeAPI(t)
	m, err := New(db, vault, bridge, &fakeFiles{}, Options{APIBaseURL: api.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	encrypted, _ := vault.Encrypt(fixtureToken)
	m.cfg = config{Enabled: true, Token: encrypted, Bot: Bot{ID: 123456, Username: "velin_fixture_bot", IsBot: true}, PublicURL: "https://ssh.example.com", IdleSeconds: 90}
	if err = m.SaveAccess("owner", token.ID, false); err != nil {
		t.Fatal(err)
	}
	pair, err := m.CreatePair("owner")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(pair.Command, "/start ")
	b, err := m.pair(Message{From: Person{ID: 42, FirstName: "Owner"}, Chat: Chat{ID: 42, Type: "private"}}, code, m.cfg.Bot)
	if err != nil {
		t.Fatal(err)
	}
	return m, f, api, b, token
}
func clientFor(t *testing.T, m *Module, api *fakeAPI) *Client {
	t.Helper()
	c, err := newClient(fixtureToken, "", api.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c
}
func callbackFor(b Binding, data string, id int64) Callback {
	return Callback{ID: "callback", From: Person{ID: b.TelegramUserID}, Message: &Message{ID: id, Chat: Chat{ID: b.ChatID, Type: "private"}}, Data: data}
}

func TestBindingExpiryIsolationAndRevocation(t *testing.T) {
	m, _, api, b, token := setup(t)
	ctx := context.Background()
	c := clientFor(t, m, api)
	pair, err := m.CreatePair(b.UserID)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(pair.Command, "/start ")
	var stored string
	if err = m.store.DB.QueryRow("SELECT pair_hash FROM telegram_bindings WHERE user_id=?", b.UserID).Scan(&stored); err != nil || stored == code || stored != hash(code) {
		t.Fatal("pair secret was not hashed", err)
	}
	if _, err = m.pair(Message{From: Person{ID: 42}, Chat: Chat{ID: 42}}, code, m.cfg.Bot); err != nil {
		t.Fatal(err)
	}
	if _, err = m.pair(Message{From: Person{ID: 42}, Chat: Chat{ID: 42}}, code, m.cfg.Bot); err == nil {
		t.Fatal("pair reused")
	}
	pair, _ = m.CreatePair(b.UserID)
	_, _ = m.store.DB.Exec("UPDATE telegram_bindings SET pair_expires=? WHERE user_id=?", stamp(time.Now().Add(-time.Minute)), b.UserID)
	if _, err = m.pair(Message{From: Person{ID: 42}, Chat: Chat{ID: 42}}, strings.TrimPrefix(pair.Command, "/start "), m.cfg.Bot); err == nil {
		t.Fatal("expired pair accepted")
	}
	if _, _, err = m.authenticate(99, b.ChatID, m.cfg.Bot.ID); err == nil {
		t.Fatal("other Telegram user accepted")
	}
	if _, _, err = m.authenticate(b.TelegramUserID, b.ChatID, 999); err == nil {
		t.Fatal("binding leaked to different bot")
	}
	if err = m.card(ctx, c, m.cfg, b, token, "other-session", 0, false); err == nil {
		t.Fatal("cross-user session read")
	}
	_, err = m.bridge.UpdateToken(b.UserID, token.ID, sessionbridge.TokenInput{Name: "Telegram", Permissions: []string{"read"}, HostIDs: []string{"host"}, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.authenticate(b.TelegramUserID, b.ChatID, m.cfg.Bot.ID); err == nil {
		t.Fatal("revoked token accepted")
	}
	if err = m.Unbind(b.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Binding(b.UserID); !errors.Is(err, ErrNotBound) {
		t.Fatal("unbind did not remove binding")
	}
}
func TestReplyRoutingPendingSelectionAndExactlyOnceInput(t *testing.T) {
	m, f, api, b, token := setup(t)
	c := clientFor(t, m, api)
	ctx := context.Background()
	if err := m.card(ctx, c, m.cfg, b, token, "session-1", 0, false); err != nil {
		t.Fatal(err)
	}
	card := api.all()[0]
	msg := Message{ID: 50, From: Person{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "继续修复登录", Reply: &Message{ID: card.ID}}
	if err := m.incoming(ctx, c, m.cfg, b, token, msg, 10); err != nil {
		t.Fatal(err)
	}
	if f.count() != 1 {
		t.Fatal("reply not forwarded")
	}
	if err := m.incoming(ctx, c, m.cfg, b, token, msg, 10); err != nil {
		t.Fatal(err)
	}
	if f.count() != 1 {
		t.Fatal("retried input duplicated")
	}
	msg.Reply = nil
	msg.Text = "sensitive private requirement"
	if err := m.incoming(ctx, c, m.cfg, b, token, msg, 11); err != nil {
		t.Fatal(err)
	}
	if f.count() != 1 {
		t.Fatal("unscoped message executed")
	}
	var id, payload string
	if err := m.store.DB.QueryRow("SELECT id,payload FROM telegram_actions").Scan(&id, &payload); err != nil || strings.Contains(payload, msg.Text) {
		t.Fatal("pending content was not encrypted", err)
	}
	cb := callbackFor(b, "a:"+id+":session-2", 0)
	if err := m.callback(ctx, c, m.cfg, b, token, cb, 12); err != nil {
		t.Fatal(err)
	}
	if f.count() != 2 {
		t.Fatal("selection not sent")
	}
	if err := m.callback(ctx, c, m.cfg, b, token, cb, 13); err == nil {
		t.Fatal("pending selection reused")
	}
	if f.count() != 2 {
		t.Fatal("selection duplicated")
	}
	msg.Text = "not authorized"
	msg.Reply = &Message{ID: card.ID}
	if _, err := m.store.DB.Exec("UPDATE session_bridge_tokens SET permissions='[\"read\"]' WHERE id=?", token.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.incoming(ctx, c, m.cfg, b, token, msg, 14); err == nil {
		t.Fatal("revoked input permission ignored")
	}
	if f.count() != 2 {
		t.Fatal("input after revocation")
	}
	current, err := m.authorized(b)
	if err != nil {
		t.Fatal(err)
	}
	msg.Reply = nil
	if err = m.incoming(ctx, c, m.cfg, b, current, msg, 15); err == nil || !strings.Contains(err.Error(), "尚未授权") {
		t.Fatal("read-only user was offered a send target", err)
	}
}
func TestFileTransferIsExplicitAndDoesNotSubmitPath(t *testing.T) {
	m, f, api, b, token := setup(t)
	c := clientFor(t, m, api)
	ctx := context.Background()
	if err := m.card(ctx, c, m.cfg, b, token, "session-1", 0, false); err != nil {
		t.Fatal(err)
	}
	card := api.all()[0]
	msg := Message{ID: 50, From: Person{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Document: &File{ID: "photo", Name: "image.png", Size: 10}, Reply: &Message{ID: card.ID}}
	if err := m.incoming(ctx, c, m.cfg, b, token, msg, 20); err == nil {
		t.Fatal("file permission ignored")
	}
	if err := m.SaveAccess(b.UserID, token.ID, true); err != nil {
		t.Fatal(err)
	}
	b, _ = m.Binding(b.UserID)
	if err := m.incoming(ctx, c, m.cfg, b, token, msg, 21); err != nil {
		t.Fatal(err)
	}
	if f.count() != 0 {
		t.Fatal("upload typed into CLI without clicking")
	}
	var id string
	if err := m.store.DB.QueryRow("SELECT id FROM telegram_actions").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := m.callback(ctx, c, m.cfg, b, token, callbackFor(b, "a:"+id, 0), 22); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	data := string(f.writes[0])
	f.mu.Unlock()
	if data != "/project/clipboard/image.png" {
		t.Fatalf("path submitted or mangled: %q", data)
	}
	msg.Caption = "请参考这张图修复页面"
	if err := m.incoming(ctx, c, m.cfg, b, token, msg, 23); err != nil {
		t.Fatal(err)
	}
	last := api.all()[len(api.all())-1]
	if !strings.Contains(last.Text, msg.Caption) || last.Markup.Rows[0][0].Text != "粘贴说明和路径" {
		t.Fatal("file caption was lost", last)
	}
	if err := m.callback(ctx, c, m.cfg, b, token, callbackFor(b, last.Markup.Rows[0][0].Data, 0), 24); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	data = string(f.writes[1])
	f.mu.Unlock()
	if data != "\x1b[200~"+msg.Caption+"\n/project/clipboard/image.png\x1b[201~" {
		t.Fatalf("caption paste was submitted or mangled: %q", data)
	}
	if err := m.execute(ctx, c, m.cfg, b, token, action{Kind: "download", SessionID: "session-1", Text: "logs/build.log"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sent := range api.all() {
		if string(sent.Data) == "build passed" {
			found = true
		}
	}
	if !found {
		t.Fatal("download missing")
	}
}
func TestNotificationTransitionsDoNotClaimCompletion(t *testing.T) {
	now := time.Now()
	session := sessionbridge.Session{Attached: true, Status: "attached"}
	initial := watchState{Connected: true, Status: "连接正常", Cursor: sessionbridge.Cursor{StreamID: "s", Offset: 1}, LastOutput: now.Add(-time.Minute)}
	active, event := transition(initial, session, sessionbridge.Snapshot{Cursor: sessionbridge.Cursor{StreamID: "s", Offset: 2}, LastOutputAt: now}, now, 90*time.Second)
	if event != "" || !active.Active {
		t.Fatal("activity generated noisy notification", event)
	}
	idle, event := transition(active, session, sessionbridge.Snapshot{Cursor: active.Cursor}, now.Add(91*time.Second), 90*time.Second)
	if !idle.Idle || !strings.Contains(event, "不代表任务完成") {
		t.Fatal("quiet misclassified", event)
	}
	_, event = transition(idle, session, sessionbridge.Snapshot{Cursor: idle.Cursor}, now.Add(100*time.Second), 90*time.Second)
	if event != "" {
		t.Fatal("idle spammed")
	}
	_, event = transition(idle, session, sessionbridge.Snapshot{Cursor: sessionbridge.Cursor{StreamID: "s", Offset: 3}, LastOutputAt: now.Add(130 * time.Second)}, now.Add(130*time.Second), 90*time.Second)
	if event != "会话收到新输出" {
		t.Fatal("resume not notified", event)
	}
	session.Attached = false
	session.Status = "error"
	disconnected, event := transition(idle, session, sessionbridge.Snapshot{}, now.Add(140*time.Second), 90*time.Second)
	if event != "连接状态：连接异常" {
		t.Fatal(event)
	}
	session.Attached = true
	session.Status = "attached"
	_, event = transition(disconnected, session, sessionbridge.Snapshot{Cursor: idle.Cursor}, now.Add(150*time.Second), 90*time.Second)
	if event != "连接状态：连接正常" {
		t.Fatal(event)
	}
}
func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
func TestPollingPersistsAdmissionAndStopsOnClose(t *testing.T) {
	m, f, api, b, _ := setup(t)
	c := clientFor(t, m, api)
	if err := m.route(b, 101, "session-1", "input"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SaveSystemSetting("telegram_bot", m.cfg); err != nil {
		t.Fatal(err)
	}
	m.Start()
	update := Update{ID: 100, Message: &Message{ID: 102, Date: time.Now().Unix(), From: Person{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "continue", Reply: &Message{ID: 101}}}
	api.updates <- []Update{update, update}
	waitFor(t, func() bool { return f.count() == 1 })
	m.Close()
	restarted, err := New(m.store, m.vault, m.bridge, m.files, m.options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.Start()
	api.updates <- []Update{update}
	time.Sleep(100 * time.Millisecond)
	if f.count() != 1 {
		t.Fatal("update replayed after restart")
	}
	var offset int64
	if err = m.store.DB.QueryRow("SELECT next_update FROM telegram_runtime WHERE bot_id=?", m.cfg.Bot.ID).Scan(&offset); err != nil || offset != 101 {
		t.Fatal("admission not persisted", offset, err)
	}
	_ = c
}
func TestConfigEncryptionProxyAndSafeErrors(t *testing.T) {
	m, _, api, _, _ := setup(t)
	input := ConfigInput{BotToken: fixtureToken, PublicURL: "https://ssh.example.com", IdleSeconds: 90}
	if _, err := m.Configure(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := m.store.DB.QueryRow("SELECT value_json FROM system_settings WHERE key='telegram_bot'").Scan(&stored); err != nil || strings.Contains(stored, fixtureToken) {
		t.Fatal("token leaked", err)
	}
	encoded, _ := json.Marshal(m.Status())
	if strings.Contains(string(encoded), fixtureToken) {
		t.Fatal("status leaks token")
	}
	for _, proxy := range []string{"file:///tmp/proxy", "https://proxy/x?secret=1", "socks5://127.0.0.1:999999"} {
		if _, err := newClient(fixtureToken, proxy, api.server.URL); err == nil {
			t.Fatal("invalid proxy accepted", proxy)
		}
	}
	client, err := newClient(fixtureToken, "", api.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	if client.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("Telegram inherited process proxy")
	}
	api.mu.Lock()
	api.webhook = "https://other.example/webhook"
	api.mu.Unlock()
	if _, err = m.Test(context.Background(), input); err == nil || !strings.Contains(err.Error(), "webhook") {
		t.Fatal("existing webhook silently replaced", err)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error_code":401,"description":"`+fixtureToken+`"}`)
	}))
	defer failing.Close()
	bad, _ := newClient(fixtureToken, "", failing.URL)
	defer bad.close()
	_, err = bad.validate(context.Background())
	if err == nil || strings.Contains(err.Error(), fixtureToken) {
		t.Fatal("upstream error leaked secret", err)
	}
}

func TestTimeoutReportsWithoutRepeatingInputAndPaginationBounds(t *testing.T) {
	m, sessions, api, b, token := setup(t)
	client := clientFor(t, m, api)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	m.report(ctx, client, b.ChatID, context.DeadlineExceeded)
	if messages := api.all(); len(messages) != 1 || !strings.Contains(messages[0].Text, "远端任务不会因此被终止") || sessions.count() != 0 {
		t.Fatal("timeout was silent or repeated a mutation", messages)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	m.report(canceled, client, b.ChatID, context.Canceled)
	if len(api.all()) != 1 {
		t.Fatal("shutdown sent another request")
	}
	if err := m.list(context.Background(), client, b, token, int(^uint(0)>>1), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.hosts(context.Background(), client, b, token, "9223372036854775807"); err != nil {
		t.Fatal(err)
	}
}
