package sessionbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

type fakeBackend struct {
	mu       sync.Mutex
	sessions []Session
	snapshot Snapshot
	writes   [][]byte
	captures int
	onCursor func()
}

func (b *fakeBackend) Sessions(context.Context, string) ([]Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Session(nil), b.sessions...), nil
}
func (b *fakeBackend) Hosts(context.Context, string) ([]Host, error) {
	return []Host{{ID: "h1"}, {ID: "h2"}}, nil
}
func (b *fakeBackend) Snapshot(ctx context.Context, _ string, _ string, lines int) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	b.mu.Lock()
	snapshot, callback := b.snapshot, b.onCursor
	if lines > 0 {
		b.captures++
	}
	b.mu.Unlock()
	if lines == 0 && callback != nil {
		callback()
	}
	return snapshot, nil
}
func (b *fakeBackend) WriteInput(_ context.Context, _, _ string, data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, append([]byte(nil), data...))
	return len(data), nil
}
func (b *fakeBackend) Create(_ context.Context, _, host, name, mode string) (Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Session{ID: uuid.NewString(), HostID: host, Name: name, Mode: mode, Status: "creating"}
	b.sessions = append(b.sessions, s)
	return s, nil
}
func (b *fakeBackend) Restore(_ context.Context, _, id string) (Session, error) {
	return Session{ID: id, HostID: "h1", Status: "attached"}, nil
}
func (b *fakeBackend) Rename(_ context.Context, _, id, name string) (Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.sessions {
		if b.sessions[i].ID == id {
			b.sessions[i].Name = name
			return b.sessions[i], nil
		}
	}
	return Session{}, errors.New("missing")
}
func (b *fakeBackend) Terminate(context.Context, string, string) (Termination, error) {
	return Termination{OK: true, RemoteTerminated: false, Warning: "remote cleanup pending"}, nil
}
func fixture(t *testing.T) (*Module, *fakeBackend) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range []string{"u1", "u2"} {
		if err = db.CreateUser(id, id, "unused", "user"); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []store.Host{{ID: "h1", UserID: "u1", Name: "host 1", Address: "127.0.0.1", Port: 22, Username: "test"}, {ID: "h2", UserID: "u1", Name: "host 2", Address: "127.0.0.2", Port: 22, Username: "test"}, {ID: "foreign", UserID: "u2", Name: "foreign", Address: "127.0.0.3", Port: 22, Username: "test"}} {
		if err = db.SaveHost(host); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []store.TerminalSession{{ID: "s1", UserID: "u1", HostID: "h1", Name: "Codex", SessionMode: "tmux", Status: "attached"}, {ID: "s2", UserID: "u1", HostID: "h2", Name: "Claude", SessionMode: "normal", Status: "attached"}} {
		if err = db.SaveTerminal(s); err != nil {
			t.Fatal(err)
		}
	}
	backend := &fakeBackend{sessions: []Session{{ID: "s1", HostID: "h1", Name: "Codex", Mode: "tmux", Status: "attached"}, {ID: "s2", HostID: "h2", Name: "Claude", Mode: "normal", Status: "attached"}}, snapshot: Snapshot{Cursor: Cursor{StreamID: "stream", Offset: 5}, Raw: []byte("\x1b[31m中文输出\x1b[0m\r\n$ "), Rows: 4, Cols: 40, Source: "terminal-replay"}}
	module, err := New(db, backend)
	if err != nil {
		t.Fatal(err)
	}
	return module, backend
}
func fullInput() TokenInput {
	return TokenInput{Name: "OpenClaw", Permissions: []string{Read, Input, Create, Manage}, HostIDs: []string{"h1"}, Enabled: true}
}
func makeToken(t *testing.T, m *Module, in TokenInput) (Token, string) {
	t.Helper()
	token, secret, err := m.CreateToken("u1", in)
	if err != nil {
		t.Fatal(err)
	}
	return token, secret
}

func TestTokenStorageScopesAndRevocation(t *testing.T) {
	m, _ := fixture(t)
	token, secret := makeToken(t, m, fullInput())
	var hash string
	if err := m.store.DB.QueryRow(`SELECT token_hash FROM session_bridge_tokens WHERE id=?`, token.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == secret || hash != hashToken(secret) {
		t.Fatal("secret not hashed")
	}
	listed, err := m.Tokens("u1")
	if err != nil || len(listed) != 1 {
		t.Fatal(listed, err)
	}
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "token_hash") {
		t.Fatal("secret leaked")
	}
	if _, err = m.Authenticate(secret); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Authenticate(secret + "x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err = m.UpdateToken("u2", token.ID, fullInput()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = m.DeleteToken("u2", token.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	tests := []TokenInput{
		{Name: "foreign", Permissions: []string{Read}, HostIDs: []string{"foreign"}, Enabled: true},
		{Name: "empty", Permissions: []string{Read}, Enabled: true},
		{Name: "no read", Permissions: []string{Input}, HostIDs: []string{"h1"}, Enabled: true},
		{Name: "create", Permissions: []string{Read, Create}, SessionIDs: []string{"s1"}, Enabled: true},
		{Name: "invalid", Permissions: []string{Read, "admin"}, HostIDs: []string{"h1"}, Enabled: true},
	}
	for _, in := range tests {
		if _, _, err = m.CreateToken("u1", in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", in.Name, err)
		}
	}
	in := fullInput()
	in.Enabled = false
	if _, err = m.UpdateToken("u1", token.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Authenticate(secret); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled", err)
	}
	in.Enabled = true
	if _, err = m.UpdateToken("u1", token.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err = m.store.DB.Exec(`UPDATE users SET disabled=1 WHERE id='u1'`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Authenticate(secret); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled owner", err)
	}
	_, _ = m.store.DB.Exec(`UPDATE users SET disabled=0,force_password_change=1 WHERE id='u1'`)
	if _, err = m.Authenticate(secret); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("password reset", err)
	}
	_, _ = m.store.DB.Exec(`UPDATE users SET force_password_change=0 WHERE id='u1'`)
	past := time.Now().Add(-time.Hour).UTC()
	_, _ = m.store.DB.Exec(`UPDATE session_bridge_tokens SET expires_at=? WHERE id=?`, past.Format(time.RFC3339Nano), token.ID)
	if _, err = m.Authenticate(secret); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired", err)
	}
	in.ExpiresAt = &past
	in.Enabled = false
	if _, err = m.UpdateToken("u1", token.ID, in); err != nil {
		t.Fatal("cannot disable expired token", err)
	}
	if err = m.DeleteToken("u1", token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Authenticate(secret); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("deleted", err)
	}
}

type bearerTransport struct{ secret string }

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header.Set("Authorization", "Bearer "+t.secret)
	return http.DefaultTransport.RoundTrip(copy)
}
func mcpClient(t *testing.T, m *Module, secret string) (*mcp.ClientSession, context.Context) {
	t.Helper()
	server := httptest.NewServer(m)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerTransport{secret}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, ctx
}
func call(t *testing.T, client *mcp.ClientSession, ctx context.Context, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(name, err)
	}
	return result
}
func decodeResult[T any](t *testing.T, result *mcp.CallToolResult) T {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool error: %+v", result.Content)
	}
	var value T
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestMCPToolsAuthorizationAndInputReceipts(t *testing.T) {
	m, b := fixture(t)
	token, secret := makeToken(t, m, fullInput())
	client, ctx := mcpClient(t, m, secret)
	tools, err := client.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 8 {
		t.Fatal(tools, err)
	}
	listed := decodeResult[sessionsResult](t, call(t, client, ctx, "velin_list_sessions", emptyArgs{}))
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != "s1" {
		t.Fatal(listed)
	}
	denied := call(t, client, ctx, "velin_read_session", readArgs{SessionID: "s2"})
	if !denied.IsError {
		t.Fatal("out of scope read succeeded")
	}
	read := decodeResult[readResult](t, call(t, client, ctx, "velin_read_session", readArgs{SessionID: "s1"}))
	if !strings.Contains(read.Text, "中文输出") || strings.Contains(read.Text, "\x1b") || read.TaskState != "unknown" || !read.Changed {
		t.Fatal(read)
	}
	args := inputArgs{SessionID: "s1", RequestID: uuid.NewString(), Text: "echo secret-input-for-test", Submit: true}
	first := decodeResult[mutationResult](t, call(t, client, ctx, "velin_send_input", args))
	if first.Status != "written" {
		t.Fatal(first)
	}
	duplicate := decodeResult[mutationResult](t, call(t, client, ctx, "velin_send_input", args))
	if !duplicate.Duplicate || duplicate.Status != "written" {
		t.Fatal(duplicate)
	}
	args.Text = "different"
	if !call(t, client, ctx, "velin_send_input", args).IsError {
		t.Fatal("conflicting requestID accepted")
	}
	b.mu.Lock()
	writes := len(b.writes)
	b.mu.Unlock()
	if writes != 1 {
		t.Fatal(writes)
	}
	var audit string
	if err = m.store.DB.QueryRow(`SELECT details FROM audit_events WHERE event_type='session_bridge.input'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, "secret-input-for-test") || !strings.Contains(audit, "s1") {
		t.Fatal("incorrect audit", audit)
	}
	created := decodeResult[mutationResult](t, call(t, client, ctx, "velin_create_session", createArgs{HostID: "h1", Name: "new Codex", RequestID: uuid.NewString()}))
	if created.Status != "accepted" || created.Session == nil {
		t.Fatal(created)
	}
	renamed := decodeResult[Session](t, call(t, client, ctx, "velin_rename_session", renameArgs{SessionID: "s1", Name: "renamed"}))
	if renamed.Name != "renamed" {
		t.Fatal(renamed)
	}
	restored := decodeResult[mutationResult](t, call(t, client, ctx, "velin_restore_session", restoreArgs{SessionID: "s1", RequestID: uuid.NewString()}))
	if restored.Status != "restored" {
		t.Fatal(restored)
	}
	if !call(t, client, ctx, "velin_terminate_session", terminateArgs{SessionID: "s1", RequestID: uuid.NewString()}).IsError {
		t.Fatal("termination lacked confirmation")
	}
	terminated := decodeResult[mutationResult](t, call(t, client, ctx, "velin_terminate_session", terminateArgs{SessionID: "s1", RequestID: uuid.NewString(), Confirm: true}))
	if terminated.Termination == nil || terminated.Termination.RemoteTerminated || terminated.Termination.Warning == "" {
		t.Fatal(terminated)
	}
	in := fullInput()
	in.Permissions = []string{Read}
	in.HostIDs = nil
	in.SessionIDs = []string{"s1"}
	if _, err = m.UpdateToken("u1", token.ID, in); err != nil {
		t.Fatal(err)
	}
	tools, err = client.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 3 {
		t.Fatal("live permission change", tools, err)
	}
	if result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "velin_send_input", Arguments: inputArgs{SessionID: "s1", RequestID: uuid.NewString(), Key: "Enter"}}); err == nil && !result.IsError {
		t.Fatal("revoked permission remained accessible")
	}
	if err = m.DeleteToken("u1", token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ListTools(ctx, nil); err == nil {
		t.Fatal("deleted token remained connected")
	}
}

func TestPersistentReceiptAndConcurrentDuplicate(t *testing.T) {
	m, _ := fixture(t)
	token, _ := makeToken(t, m, fullInput())
	id := uuid.NewString()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan mutationResult, 1)
	payload := inputArgs{SessionID: "s1", Text: "do once", RequestID: id}
	go func() {
		value, err := m.mutation(context.Background(), token, "input", id, payload, func() mutationResult { close(started); <-release; return mutationResult{Status: "written", Bytes: 7} })
		if err != nil {
			done <- failed(err)
		} else {
			done <- value
		}
	}()
	<-started
	value, err := m.mutation(context.Background(), token, "input", id, payload, func() mutationResult { t.Error("duplicate executed"); return mutationResult{} })
	if err != nil || value.Status != "uncertain" || !value.Duplicate {
		t.Fatal(value, err)
	}
	close(release)
	<-done
	restarted, err := New(m.store, m.backend)
	if err != nil {
		t.Fatal(err)
	}
	value, err = restarted.mutation(context.Background(), token, "input", id, payload, func() mutationResult { t.Error("retry after restart executed"); return mutationResult{} })
	if err != nil || value.Status != "written" || !value.Duplicate {
		t.Fatal(value, err)
	}
	// Simulate a crash after admission but before an observable result.
	id = uuid.NewString()
	payload.RequestID = id
	raw, _ := json.Marshal(payload)
	if _, err = m.store.DB.Exec(`INSERT INTO session_bridge_receipts(token_id,tool,request_id,digest,created_at) VALUES(?,?,?,?,?)`, token.ID, "input", id, hashToken(string(raw)), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	crashed, err := New(m.store, m.backend)
	if err != nil {
		t.Fatal(err)
	}
	value, err = crashed.mutation(context.Background(), token, "input", id, payload, func() mutationResult { t.Error("unconfirmed write replayed after restart"); return mutationResult{} })
	if err != nil || value.Status != "uncertain" || !value.Duplicate {
		t.Fatal(value, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = m.mutation(ctx, token, "input", uuid.NewString(), payload, func() mutationResult { t.Error("cancelled call executed"); return mutationResult{} })
}

func TestWaitReturnsNewOutputAndUnchangedCursor(t *testing.T) {
	m, b := fixture(t)
	token, _ := makeToken(t, m, fullInput())
	cursor := b.snapshot.Cursor
	var once sync.Once
	b.onCursor = func() {
		once.Do(func() { b.mu.Lock(); b.snapshot.Offset++; b.snapshot.Raw = []byte("finished output"); b.mu.Unlock() })
	}
	value, err := m.readSession(context.Background(), token, readArgs{SessionID: "s1", Cursor: &cursor, WaitSeconds: 2})
	if err != nil || !value.Changed || value.Cursor.Offset != cursor.Offset+1 || !strings.Contains(value.Text, "finished output") || value.TaskState != "unknown" {
		t.Fatal(value, err)
	}
	b.mu.Lock()
	captures := b.captures
	b.mu.Unlock()
	if captures != 1 {
		t.Fatal("waiting captured on every poll", captures)
	}
	value, err = m.readSession(context.Background(), token, readArgs{SessionID: "s1", Cursor: &value.Cursor})
	if err != nil || value.Changed || value.TaskState != "unknown" {
		t.Fatal(value, err)
	}
}
func TestWaitIsCheapAndRevocationStopsIt(t *testing.T) {
	m, b := fixture(t)
	token, _ := makeToken(t, m, fullInput())
	b.onCursor = func() { _ = m.DeleteToken("u1", token.ID) }
	_, err := m.readSession(context.Background(), token, readArgs{SessionID: "s1", Cursor: &b.snapshot.Cursor, WaitSeconds: 2})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	b.mu.Lock()
	captures := b.captures
	b.mu.Unlock()
	if captures != 0 {
		t.Fatal("waiting ran capture", captures)
	}
}
func TestMCPHTTPRejectsCookieOnlyAndCrossOrigin(t *testing.T) {
	m, _ := fixture(t)
	_, secret := makeToken(t, m, fullInput())
	for _, tc := range []struct {
		auth, origin string
		status       int
	}{{"", "", 401}, {"Bearer wrong", "", 401}, {"Bearer " + secret, "https://evil.example", 403}, {"Bearer " + secret, "null", 403}} {
		req := httptest.NewRequest("POST", "http://localhost/mcp/sessions", strings.NewReader(`{}`))
		req.Header.Set("Cookie", "velin_session=anything")
		req.Header.Set("Authorization", tc.auth)
		req.Header.Set("Origin", tc.origin)
		res := httptest.NewRecorder()
		m.ServeHTTP(res, req)
		if res.Code != tc.status {
			t.Fatal(tc.status, res.Code, res.Body.String())
		}
	}
}
func TestTerminalTextAndInput(t *testing.T) {
	for _, tc := range []struct{ name, raw, want, absent string }{
		{"cursor redraw", "first\r\n\x1b[2K\rold\r\x1b[2Knew", "new", "old"},
		{"wide unicode", "\x1b[32m中文🙂\x1b[0m", "中文🙂", "\x1b"},
		{"alternate", "main\x1b[?1049h\x1b[2J\x1b[HCLI", "CLI", "main"},
		{"status queries", "hello\x1b[6n\x1b[c\x1b]11;?\x07", "hello", "\x1b"},
		{"indentation", "  code\r\n    next", "    next", "\x1b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := RenderText([]byte(tc.raw), 40, 5, 20)
			if !strings.Contains(value, tc.want) || strings.Contains(value, tc.absent) {
				t.Fatalf("%q", value)
			}
		})
	}
	value, truncated := boundText(strings.Repeat("中", maxTextBytes))
	if !truncated || !utf8.ValidString(value) || len(value) > maxTextBytes {
		t.Fatal("invalid bounded Unicode")
	}
	for _, args := range []inputArgs{{Text: "\x1b[201~evil\n"}, {Text: "abc\x03"}, {Key: "Enter", Text: "conflict"}, {Text: strings.Repeat("a", 49<<10)}} {
		if _, err := inputBytes(args); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	valueBytes, err := inputBytes(inputArgs{Text: "a\r\nb", Submit: true})
	if err != nil || string(valueBytes) != "\x1b[200~a\nb\x1b[201~\r" {
		t.Fatal(string(valueBytes), err)
	}
}
