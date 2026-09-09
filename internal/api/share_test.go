package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ttyob/velin-web-ssh/internal/config"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
)

func TestTerminalSharePasswordAccess(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "share.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.CreateUser("user-1", "owner", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	if err = database.SaveTerminal(store.TerminalSession{ID: "session-1", UserID: "user-1", Name: "Production", RemoteUser: "root", TmuxSocket: "velin", TmuxName: "ws_1", OwnerMarker: "owner", Status: "attached"}); err != nil {
		t.Fatal(err)
	}
	vault, err := security.LoadVault(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := security.HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	const rawToken = "abcdefghijklmnopqrstuvwxyz1234567890TOKEN"
	share := store.TerminalShare{ID: "share-1", UserID: "user-1", SessionID: "session-1", TokenHash: security.TokenHash(rawToken), TokenEnc: "encrypted", PasswordHash: passwordHash, Permission: "view", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err = database.CreateTerminalShare(share); err != nil {
		t.Fatal(err)
	}
	a := &API{cfg: config.Config{CookieSecure: false}, store: database, vault: vault, terminals: terminal.NewManager(database, vault, "test"), shareViewers: make(map[string]map[string]*shareViewerConnection), shareFailures: make(map[string]shareFailure)}

	request := shareRouteRequest(http.MethodPost, "/api/public/shares/token/access", "token", rawToken, `{"displayName":"Alice","password":"wrong"}`)
	recorder := httptest.NewRecorder()
	a.accessTerminalShare(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = shareRouteRequest(http.MethodPost, "/api/public/shares/token/access", "token", rawToken, `{"displayName":"Alice","password":"secret"}`)
	recorder = httptest.NewRecorder()
	a.accessTerminalShare(recorder, request)
	if recorder.Code != http.StatusOK || len(recorder.Result().Cookies()) != 1 {
		t.Fatalf("access status=%d cookies=%d body=%s", recorder.Code, len(recorder.Result().Cookies()), recorder.Body.String())
	}
	var response terminalShareView
	if err = json.NewDecoder(recorder.Body).Decode(&response); err != nil || !response.Authorized || response.SessionName != "Production" {
		t.Fatalf("response=%+v err=%v", response, err)
	}

	request = shareRouteRequest(http.MethodGet, "/api/public/shares/token", "token", rawToken, "")
	request.AddCookie(recorder.Result().Cookies()[0])
	recorder = httptest.NewRecorder()
	a.publicTerminalShare(recorder, request)
	if err = json.NewDecoder(recorder.Body).Decode(&response); err != nil || !response.Authorized {
		t.Fatalf("metadata response=%+v err=%v", response, err)
	}
}

func TestTerminalShareTokenPathsAreRedacted(t *testing.T) {
	for _, path := range []string{"/share/secret-token", "/api/public/shares/secret-token/access", "/ws/shares/secret-token"} {
		if value := logRequestPath(path); strings.Contains(value, "secret-token") {
			t.Fatalf("share token leaked in log path %q", value)
		}
	}
}

func TestTerminalShareStateAndViewerOrder(t *testing.T) {
	now := time.Now().UTC()
	share := store.TerminalShare{ExpiresAt: now.Add(time.Hour), SessionStatus: "attached"}
	if !terminalShareActive(share, now) {
		t.Fatal("attached share should be active")
	}
	share.SessionStatus = "ended"
	if terminalShareActive(share, now) || terminalShareEndReason(share, now) != "共享终端已结束" {
		t.Fatal("ended terminal share remained active")
	}
	share.SessionStatus = "attached"
	share.OwnerDisabled = true
	if terminalShareActive(share, now) || terminalShareEndReason(share, now) != "分享创建者账户已停用" {
		t.Fatal("disabled owner share remained active")
	}

	a := &API{shareViewers: map[string]map[string]*shareViewerConnection{
		"share": {
			"late":  {ID: "late", ConnectedAt: now.Add(time.Minute)},
			"early": {ID: "early", ConnectedAt: now},
		},
	}}
	viewers := a.shareViewerList("share")
	if len(viewers) != 2 || viewers[0].ID != "early" || viewers[1].ID != "late" {
		t.Fatalf("viewer order=%+v", viewers)
	}
}

func shareRouteRequest(method, path, key, value, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add(key, value)
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))
}
