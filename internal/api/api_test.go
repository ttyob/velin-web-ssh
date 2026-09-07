package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pquerna/otp/totp"
	"velin-webssh/internal/config"
	"velin-webssh/internal/security"
	"velin-webssh/internal/store"
	"velin-webssh/internal/terminal"
)

func TestForwardTerminalEventDuringReplay(t *testing.T) {
	var written []terminal.Event
	writeJSON := func(value any) error {
		written = append(written, value.(terminal.Event))
		return nil
	}
	var pending []terminal.Event
	output := terminal.Event{Type: "output", Data: "encoded", Offset: 42}
	if err := forwardTerminalEventDuringReplay(writeJSON, output, &pending); err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].Type != "replay_live" || written[0].Data != output.Data || written[0].Offset != output.Offset {
		t.Fatalf("preview event=%+v", written)
	}
	if len(pending) != 1 || pending[0] != output {
		t.Fatalf("pending output=%+v", pending)
	}

	controller := terminal.Event{Type: "controller", Controller: "client"}
	if err := forwardTerminalEventDuringReplay(writeJSON, controller, &pending); err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 || written[1] != controller {
		t.Fatalf("control event=%+v", written)
	}
	if len(pending) != 1 {
		t.Fatalf("control event was delayed: %+v", pending)
	}
}

func TestParseOpenSSH(t *testing.T) {
	config := `
Host *.internal
  User ignored
Host production
  HostName 192.0.2.10
  User deploy
  Port 2222
  IdentityFile ~/.ssh/id_ed25519
  ProxyJump bastion
  Compression yes
Host broken
  HostName example.invalid
  Port 70000
`
	hosts := parseOpenSSH(config)
	if len(hosts) != 2 {
		t.Fatalf("hosts=%d: %+v", len(hosts), hosts)
	}
	production := hosts[0]
	if production.Alias != "production" || production.HostName != "192.0.2.10" || production.User != "deploy" || production.Port != 2222 || production.IdentityFile != "~/.ssh/id_ed25519" || production.ProxyJump != "bastion" {
		t.Fatalf("production=%+v", production)
	}
	if len(production.Warnings) != 1 || !strings.Contains(production.Warnings[0], "Compression") {
		t.Fatalf("warnings=%v", production.Warnings)
	}
	if hosts[1].Port != 22 || len(hosts[1].Warnings) != 1 || !strings.Contains(hosts[1].Warnings[0], "端口无效") {
		t.Fatalf("broken=%+v", hosts[1])
	}
}

func TestVerifySecondFactor(t *testing.T) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Velin", AccountName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	hashes := []string{security.TokenHash("RECOVERY-ONE"), security.TokenHash("RECOVERY-TWO")}
	valid, remaining, used := verifySecondFactor(key.Secret(), hashes, code)
	if !valid || used || len(remaining) != 2 {
		t.Fatalf("totp valid=%v used=%v remaining=%d", valid, used, len(remaining))
	}
	valid, remaining, used = verifySecondFactor(key.Secret(), hashes, " recovery-one ")
	if !valid || !used || len(remaining) != 1 || remaining[0] != hashes[1] {
		t.Fatalf("recovery valid=%v used=%v remaining=%v", valid, used, remaining)
	}
	valid, _, used = verifySecondFactor(key.Secret(), remaining, "RECOVERY-ONE")
	if valid || used {
		t.Fatal("consumed recovery code was accepted")
	}
}

func TestValidLockPIN(t *testing.T) {
	for _, value := range []string{"123456", "000000", "987654"} {
		if !validLockPIN(value) {
			t.Fatalf("valid PIN %q rejected", value)
		}
	}
	for _, value := range []string{"", "12345", "1234567", "12345a", " 123456", "１２３４５６"} {
		if validLockPIN(value) {
			t.Fatalf("invalid PIN %q accepted", value)
		}
	}
}

func TestNormalizeHostGroup(t *testing.T) {
	for input, expected := range map[string]string{
		"":                        "",
		"  production / east/db ": "production/east/db",
		"/one//two/":              "one/two",
	} {
		if actual := normalizeHostGroup(input); actual != expected {
			t.Fatalf("normalizeHostGroup(%q)=%q, want %q", input, actual, expected)
		}
	}
}

func TestValidateJumpHost(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "jump-host.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	credential := store.Credential{ID: "credential", UserID: "u1", Name: "jump", Kind: "password", Secret: "encrypted"}
	if err = s.SaveCredential(credential); err != nil {
		t.Fatal(err)
	}
	jump := store.Host{ID: "jump", UserID: "u1", Name: "bastion", Address: "192.0.2.1", Port: 22, Username: "root", CredentialID: credential.ID}
	target := store.Host{ID: "target", UserID: "u1", Name: "private", Address: "10.0.0.2", Port: 22, Username: "root"}
	if err = s.SaveHost(jump); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveHost(target); err != nil {
		t.Fatal(err)
	}
	a := &API{store: s}
	if err = a.validateJumpHost("u1", target.ID, jump.ID); err != nil {
		t.Fatalf("valid jump host rejected: %v", err)
	}
	jump.CredentialID = ""
	jump.PasswordEnc = "encrypted-password"
	if err = s.SaveHost(jump); err != nil {
		t.Fatal(err)
	}
	if err = a.validateJumpHost("u1", target.ID, jump.ID); err != nil {
		t.Fatalf("host password jump host rejected: %v", err)
	}
	jump.JumpHostID = target.ID
	if err = s.SaveHost(jump); err != nil {
		t.Fatal(err)
	}
	if err = a.validateJumpHost("u1", target.ID, jump.ID); err == nil || !strings.Contains(err.Error(), "循环") {
		t.Fatalf("jump host cycle error=%v", err)
	}
}

func TestWritableRemotePath(t *testing.T) {
	for _, value := range []string{"", ".", "/", "..", "../", " /../ ", "bad\x00path"} {
		if _, err := writableRemotePath(value); err == nil {
			t.Fatalf("dangerous path %q accepted", value)
		}
	}
	for input, expected := range map[string]string{"/srv/data/file.txt": "/srv/data/file.txt", "reports/../today.txt": "today.txt", " ./safe.txt ": "safe.txt"} {
		actual, err := writableRemotePath(input)
		if err != nil || actual != expected {
			t.Fatalf("path %q=%q err=%v", input, actual, err)
		}
	}
}

func TestEditableTextVersion(t *testing.T) {
	first := editableTextVersion([]byte("key=value\n"))
	if first == "" || len(first) != 64 {
		t.Fatalf("unexpected version %q", first)
	}
	if first != editableTextVersion([]byte("key=value\n")) {
		t.Fatal("same content produced different versions")
	}
	if first == editableTextVersion([]byte("key=changed\n")) {
		t.Fatal("different content produced the same version")
	}
}

func TestCSRFProtectionRejectsExplicitCrossSiteRequest(t *testing.T) {
	a := &API{}
	handler := a.csrfProtection(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPut, "http://velin.example/api/preferences", strings.NewReader(`{}`))
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "cross_site_rejected") {
		t.Fatalf("cross-site status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "http://velin.example/api/preferences", strings.NewReader(`{}`))
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-test-token-123456789012345678901234567890"})
	request.Header.Set("X-CSRF-Token", "csrf-test-token-123456789012345678901234567890")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("valid CSRF status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCSRFProtectionDoesNotConsumeProxiedApplicationRequests(t *testing.T) {
	a := &API{}
	handler := a.csrfProtection(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPost, "http://velin.example/web-proxy/token/api/save", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("proxy POST status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSameOriginRejectsCrossOriginWebSocket(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://velin.example/ws/sessions/id", nil)
	request.Header.Set("Origin", "https://evil.example")
	if sameOrigin(request) {
		t.Fatal("cross-origin WebSocket request was accepted")
	}
	request.Header.Set("Origin", "http://velin.example")
	if !sameOrigin(request) {
		t.Fatal("same-origin WebSocket request was rejected")
	}
}

func TestClientIPDoesNotTrustUnconfiguredForwardedHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://velin.example/api/auth/login", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Real-IP", "203.0.113.20")
	api := &API{}
	if got := api.clientIP(request); got != "127.0.0.1" {
		t.Fatalf("client IP=%q, want proxy address", got)
	}
}

func TestClientIPUsesForwardedHeaderOnlyForTrustedProxy(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://velin.example/api/auth/login", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Real-IP", "203.0.113.20")
	_, network, err := net.ParseCIDR("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	api := &API{cfg: config.Config{TrustedProxyCIDRs: []*net.IPNet{network}}}
	if got := api.clientIP(request); got != "203.0.113.20" {
		t.Fatalf("client IP=%q, want forwarded address", got)
	}
}

func TestLoginRequiresCaptchaAfterCredentialFailure(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "captcha-login.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	hash, err := security.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = database.CreateUser("admin", "admin", hash, "admin"); err != nil {
		t.Fatal(err)
	}
	a := &API{cfg: config.Config{CookieSecure: false}, store: database, captchas: make(map[string]loginCaptcha)}

	bad := httptest.NewRequest(http.MethodPost, "http://velin.example/api/auth/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
	bad.RemoteAddr = "192.0.2.10:1234"
	badRecorder := httptest.NewRecorder()
	a.login(badRecorder, bad)
	if badRecorder.Code != http.StatusUnauthorized || !strings.Contains(badRecorder.Body.String(), `"captchaRequired":true`) {
		t.Fatalf("first failed login status=%d body=%s", badRecorder.Code, badRecorder.Body.String())
	}

	captchaRequest := httptest.NewRequest(http.MethodGet, "http://velin.example/api/auth/captcha?username=admin", nil)
	captchaRequest.RemoteAddr = bad.RemoteAddr
	captchaRecorder := httptest.NewRecorder()
	a.loginCaptcha(captchaRecorder, captchaRequest)
	if captchaRecorder.Code != http.StatusOK {
		t.Fatalf("captcha status=%d body=%s", captchaRecorder.Code, captchaRecorder.Body.String())
	}
	var challenge struct {
		ID string `json:"id"`
	}
	if err = json.NewDecoder(captchaRecorder.Body).Decode(&challenge); err != nil {
		t.Fatal(err)
	}
	code := a.captchas[challenge.ID].Code

	goodBody := fmt.Sprintf(`{"username":"admin","password":"correct-password","captchaID":%q,"captcha":%q}`, challenge.ID, code)
	good := httptest.NewRequest(http.MethodPost, "http://velin.example/api/auth/login", strings.NewReader(goodBody))
	good.RemoteAddr = bad.RemoteAddr
	goodRecorder := httptest.NewRecorder()
	a.login(goodRecorder, good)
	if goodRecorder.Code != http.StatusOK {
		t.Fatalf("captcha login status=%d body=%s", goodRecorder.Code, goodRecorder.Body.String())
	}
}

func TestUpdateUserProfile(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateUser("admin", "admin", "hash", "admin"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateUser("member", "member", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	a := &API{store: s}

	body := []byte(`{"username":"operator","role":"admin","disabled":true}`)
	req := httptest.NewRequest("PATCH", "/api/admin/users/member", bytes.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add("id", "member")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, route)
	ctx = context.WithValue(ctx, userKey, store.User{ID: "admin", Role: "admin"})
	recorder := httptest.NewRecorder()
	a.updateUser(recorder, req.WithContext(ctx))
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var updated store.User
	if err = json.NewDecoder(recorder.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Username != "operator" || updated.Role != "admin" || !updated.Disabled {
		t.Fatalf("updated user=%+v", updated)
	}
}

func TestUpdateUserRejectsSelfDemotion(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateUser("admin", "admin", "hash", "admin"); err != nil {
		t.Fatal(err)
	}
	a := &API{store: s}

	req := httptest.NewRequest("PATCH", "/api/admin/users/admin", strings.NewReader(`{"role":"user"}`))
	route := chi.NewRouteContext()
	route.URLParams.Add("id", "admin")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, route)
	ctx = context.WithValue(ctx, userKey, store.User{ID: "admin", Role: "admin"})
	recorder := httptest.NewRecorder()
	a.updateUser(recorder, req.WithContext(ctx))
	if recorder.Code != 400 || !strings.Contains(recorder.Body.String(), "cannot_demote_self") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestConnectionErrorCodeDetectsMissingTmux(t *testing.T) {
	err := fmt.Errorf("tmux is required on the remote host: exit status 1")
	if code := connectionErrorCode(err); code != "tmux_missing" {
		t.Fatalf("code=%q", code)
	}
}
