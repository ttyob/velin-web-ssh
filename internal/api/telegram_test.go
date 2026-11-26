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
	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
)

func TestTelegramBrowserPermissionsAndScope(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "telegram-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"owner", "other"} {
		if err = db.CreateUser(id, id, "unused", "user"); err != nil {
			t.Fatal(err)
		}
		if err = db.SaveHost(store.Host{ID: id + "-host", UserID: id, Name: id, Address: "127.0.0.1", Port: 22, Username: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.CreateAuthSession("browser", "owner", security.TokenHash("owner-cookie"), "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	vault, err := security.LoadVault(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	secret := "123456:private_bot_token_never_expose_this"
	encrypted, _ := vault.Encrypt(secret)
	if err = db.SaveSystemSetting("telegram_bot", map[string]any{"enabled": false, "tokenEncrypted": encrypted, "idleSeconds": 90}); err != nil {
		t.Fatal(err)
	}
	a := &API{store: db, vault: vault, terminals: terminal.NewManager(db, vault, "test")}
	defer a.terminals.CloseAll()
	a.initSessionBridge()
	a.initTelegram()
	defer a.Close()
	if a.telegram == nil {
		t.Fatal("Telegram module not initialized")
	}
	router := a.Router()
	csrfRequest := httptest.NewRequest("GET", "/api/csrf", nil)
	csrfResponse := httptest.NewRecorder()
	router.ServeHTTP(csrfResponse, csrfRequest)
	var csrf struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(csrfResponse.Body.Bytes(), &csrf); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, authenticated, withCSRF bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: a.sessionCookieName(req), Value: "owner-cookie"})
		}
		if withCSRF {
			req.Header.Set("X-CSRF-Token", csrf.Token)
			for _, cookie := range csrfResponse.Result().Cookies() {
				req.AddCookie(cookie)
			}
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	if res := request("GET", "/api/telegram", "", false, false); res.Code != 401 {
		t.Fatal("profile allows anonymous", res.Code)
	}
	body := `{"permissions":["read","input"],"hostIDs":["owner-host"],"sessionIDs":[],"allowFiles":true}`
	if res := request("PUT", "/api/telegram/access", body, true, false); res.Code != 403 {
		t.Fatal("CSRF bypass", res.Code)
	}
	if res := request("PUT", "/api/admin/telegram", `{"enabled":false}`, true, true); res.Code != 403 {
		t.Fatal("user changed bot config", res.Code)
	}
	if res := request("POST", "/api/admin/telegram/test", `{}`, true, true); res.Code != 403 {
		t.Fatal("user tested secret bot config", res.Code)
	}
	if res := request("PUT", "/api/telegram/access", strings.ReplaceAll(body, "owner-host", "other-host"), true, true); res.Code != 400 {
		t.Fatal("foreign host authorization accepted", res.Code, res.Body.String())
	}
	if res := request("PUT", "/api/telegram/access", body, true, true); res.Code != 200 {
		t.Fatal("save access", res.Code, res.Body.String())
	}
	res := request("GET", "/api/telegram", "", true, false)
	if res.Code != 200 || strings.Contains(res.Body.String(), secret) || strings.Contains(res.Body.String(), encrypted) {
		t.Fatal("profile leaks configuration", res.Code, res.Body.String())
	}
	var profile struct {
		Binding struct {
			AllowFiles bool   `json:"allowFiles"`
			TokenID    string `json:"tokenID"`
		} `json:"binding"`
		Access struct {
			Permissions []string `json:"permissions"`
			HostIDs     []string `json:"hostIDs"`
		} `json:"access"`
	}
	if err = json.Unmarshal(res.Body.Bytes(), &profile); err != nil || !profile.Binding.AllowFiles || len(profile.Access.HostIDs) != 1 || profile.Access.HostIDs[0] != "owner-host" {
		t.Fatal("wrong saved scope", err, res.Body.String())
	}
	if res := request("POST", "/api/telegram/pair", "", true, true); res.Code != 400 {
		t.Fatal("disabled bot generated link", res.Code)
	}
	if res := request("DELETE", "/api/telegram/binding", "", true, true); res.Code != 200 {
		t.Fatal("unbind", res.Code, res.Body.String())
	}
	if _, err = a.sessionBridge.AccessToken("owner", profile.Binding.TokenID); err == nil {
		t.Fatal("unbind retained access token")
	}
}
