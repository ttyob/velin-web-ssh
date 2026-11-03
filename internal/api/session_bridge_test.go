package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/sessionbridge"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
)

func TestSessionBridgeRoutingAndBrowserProtection(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.CreateUser("owner", "owner", "unused", "user"); err != nil {
		t.Fatal(err)
	}
	if err = db.CreateAuthSession("browser", "owner", security.TokenHash("browser-cookie"), "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveHost(store.Host{ID: "host", UserID: "owner", Name: "host", Address: "127.0.0.1", Port: 22, Username: "test"}); err != nil {
		t.Fatal(err)
	}
	a := &API{store: db, terminals: terminal.NewManager(db, nil, "test")}
	a.initSessionBridge()
	if a.sessionBridge == nil {
		t.Fatal("bridge initialization failed")
	}
	router := a.Router()
	csrfReq := httptest.NewRequest("GET", "/api/csrf", nil)
	csrfRes := httptest.NewRecorder()
	router.ServeHTTP(csrfRes, csrfReq)
	var csrf struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(csrfRes.Body.Bytes(), &csrf); err != nil || csrf.Token == "" {
		t.Fatal(err)
	}
	request := func(method, path, body, bearer string, browser, withCSRF bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if browser {
			req.AddCookie(&http.Cookie{Name: a.sessionCookieName(req), Value: "browser-cookie"})
		}
		if withCSRF {
			req.Header.Set("X-CSRF-Token", csrf.Token)
			for _, cookie := range csrfRes.Result().Cookies() {
				req.AddCookie(cookie)
			}
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	body := `{"name":"OpenClaw","permissions":["read"],"hostIDs":["host"],"sessionIDs":[],"enabled":true,"expiresAt":null}`
	if res := request("GET", "/api/session-bridge/tokens", "", "", false, false); res.Code != 401 {
		t.Fatal(res.Code, res.Body.String())
	}
	if res := request("POST", "/api/session-bridge/tokens", body, "", true, false); res.Code != 403 {
		t.Fatal("CSRF bypassed", res.Code)
	}
	res := request("POST", "/api/session-bridge/tokens", body, "", true, true)
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	var created struct {
		Token  sessionbridge.Token `json:"token"`
		Secret string              `json:"secret"`
	}
	if err = json.Unmarshal(res.Body.Bytes(), &created); err != nil || created.Secret == "" {
		t.Fatal(err)
	}
	res = request("GET", "/api/session-bridge/tokens", "", "", true, false)
	if res.Code != 200 || strings.Contains(res.Body.String(), created.Secret) {
		t.Fatal("list leaks token", res.Code)
	}
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"api-test","version":"1"}}}`
	res = request("POST", sessionbridge.Endpoint, initialize, created.Secret, false, false)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "velin-sessions") {
		t.Fatal("MCP requires browser CSRF", res.Code, res.Body.String())
	}
	if res = request("POST", sessionbridge.Endpoint, initialize, "invalid", false, false); res.Code != 401 {
		t.Fatal(res.Code, res.Body.String())
	}
	if res = request("POST", sessionbridge.Endpoint, initialize, "", true, true); res.Code != 401 {
		t.Fatal("cookie granted MCP access", res.Code)
	}
	if res = request("POST", "/api/session-bridge/tokens", body, created.Secret, false, true); res.Code != 401 {
		t.Fatal("MCP token granted management access", res.Code)
	}
	// Locking protects token administration, even with valid browser CSRF.
	_, _ = db.DB.Exec(`UPDATE auth_sessions SET locked=1 WHERE id='browser'`)
	if res = request("GET", "/api/session-bridge/tokens", "", "", true, false); res.Code != 423 {
		t.Fatal("locked browser accessed tokens", res.Code, res.Body.String())
	}
}
