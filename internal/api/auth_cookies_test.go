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

	"github.com/ttyob/velin-web-ssh/internal/config"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

func TestCSRFCookieMigrationAndProtection(t *testing.T) {
	legacyToken := strings.Repeat("a", 32)
	preferredToken := strings.Repeat("b", 32)
	for _, secure := range []bool{false, true} {
		a := &API{cfg: config.Config{CookieSecure: secure}}
		for _, scenario := range []struct {
			name    string
			cookies []*http.Cookie
			want    string
		}{
			{name: "new browser"},
			{name: "readable legacy", cookies: []*http.Cookie{{Name: csrfCookieName, Value: legacyToken}}, want: legacyToken},
			{name: "preferred wins", cookies: []*http.Cookie{{Name: a.csrfCookieName(), Value: preferredToken}, {Name: csrfCookieName, Value: legacyToken}}, want: preferredToken},
			{name: "invalid preferred cannot fall back", cookies: []*http.Cookie{{Name: a.csrfCookieName(), Value: "short"}, {Name: csrfCookieName, Value: legacyToken}}},
		} {
			t.Run(scenario.name+map[bool]string{true: "/secure", false: "/http"}[secure], func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/api/csrf", nil)
				for _, cookie := range scenario.cookies {
					r.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				a.csrfToken(w, r)
				var body struct{ Token string }
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK || len(body.Token) < 32 || (scenario.want != "" && body.Token != scenario.want) {
					t.Fatalf("invalid bootstrap response: status=%d", w.Code)
				}
				if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Vary") != "Cookie" {
					t.Fatal("CSRF bootstrap must not be cached")
				}
				post := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
				for _, cookie := range scenario.cookies {
					post.AddCookie(cookie)
				}
				for _, cookie := range w.Result().Cookies() {
					if cookie.Name != a.csrfCookieName() || cookie.Secure != secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
						t.Fatal("incorrect CSRF cookie attributes")
					}
					// Simulate the browser replacing the cookie, including invalid ones.
					post.Header.Del("Cookie")
					post.AddCookie(cookie)
				}
				handler := a.csrfProtection(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
				for _, header := range []string{body.Token, "wrong", ""} {
					post.Header.Set("X-CSRF-Token", header)
					result := httptest.NewRecorder()
					handler.ServeHTTP(result, post)
					want := http.StatusForbidden
					if header == body.Token {
						want = http.StatusNoContent
					}
					if result.Code != want {
						t.Fatalf("CSRF status=%d, want %d", result.Code, want)
					}
				}
			})
		}
	}
}

func TestCSRFLegacyCannotOverridePreferredCookie(t *testing.T) {
	a := &API{}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.AddCookie(&http.Cookie{Name: httpCSRFCookieName, Value: "new"})
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "old"})
	r.Header.Set("X-CSRF-Token", "old")
	w := httptest.NewRecorder()
	a.csrfProtection(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("legacy token accepted") })).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestSessionCookieMigrationLifecycle(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	hash, err := security.HashPassword("test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = database.CreateUser("user", "tester", hash, "admin"); err != nil {
		t.Fatal(err)
	}
	for _, secure := range []bool{false, true} {
		for _, action := range []string{"login", "logout", "revoke-all"} {
			t.Run(action+map[bool]string{true: "/secure", false: "/http"}[secure], func(t *testing.T) {
				a := &API{cfg: config.Config{CookieSecure: secure}, store: database}
				for _, token := range []string{"legacy-session", "http-session"} {
					if err := database.CreateAuthSession(token, "user", security.TokenHash(token), "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
				r := httptest.NewRequest(http.MethodPost, "/api/auth/"+action, strings.NewReader(`{"username":"tester","password":"test-password"}`))
				r.AddCookie(&http.Cookie{Name: cookieName, Value: "legacy-session"})
				user, tokenHash, err := a.currentAuthSession(r)
				if err != nil || tokenHash != security.TokenHash("legacy-session") {
					t.Fatal("legacy session not accepted during migration")
				}
				r.AddCookie(&http.Cookie{Name: httpCookieName, Value: "http-session"})
				_, tokenHash, err = a.currentAuthSession(r)
				wantToken := "http-session"
				if secure {
					wantToken = "legacy-session"
				}
				if err != nil || tokenHash != security.TokenHash(wantToken) {
					t.Fatal("wrong session cookie precedence")
				}
				r = r.WithContext(context.WithValue(r.Context(), userKey, user))
				w := httptest.NewRecorder()
				switch action {
				case "login":
					a.login(w, r)
				case "logout":
					a.logout(w, r)
				case "revoke-all":
					a.revokeAllDevices(w, r)
				}
				if w.Code != http.StatusOK {
					t.Fatalf("status=%d: %s", w.Code, w.Body.String())
				}
				for _, token := range []string{"legacy-session", "http-session"} {
					if _, err := database.UserByToken(security.TokenHash(token)); err == nil {
						t.Fatal("old browser session was not revoked")
					}
				}
				cleared := map[string]bool{}
				for _, cookie := range w.Result().Cookies() {
					if cookie.Secure != secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
						t.Fatal("incorrect session cookie attributes")
					}
					if cookie.MaxAge < 0 {
						cleared[cookie.Name] = true
						continue
					}
					if action != "login" || cookie.Name != a.sessionCookieName() {
						t.Fatal("unexpected session cookie")
					}
					if _, err := database.UserByToken(security.TokenHash(cookie.Value)); err != nil {
						t.Fatal("new login session is invalid")
					}
				}
				if !cleared[cookieName] || !cleared[httpCookieName] {
					t.Fatal("both session cookie names must be cleared")
				}
				_, _ = database.DB.Exec(`DELETE FROM auth_sessions`)
			})
		}
	}
}

func TestInvalidPreferredSessionDoesNotFallBack(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.CreateUser("user", "tester", "unused", "user"); err != nil {
		t.Fatal(err)
	}
	if err = database.CreateAuthSession("legacy", "user", security.TokenHash("valid"), "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a := &API{store: database}
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: "valid"})
	r.AddCookie(&http.Cookie{Name: httpCookieName, Value: "invalid"})
	if _, _, err = a.currentAuthSession(r); err == nil {
		t.Fatal("invalid preferred cookie must not fall back to legacy")
	}
}
