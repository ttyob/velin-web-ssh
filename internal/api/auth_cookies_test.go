package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/config"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

func TestCSRFCookieBootstrapAndProtection(t *testing.T) {
	legacyToken := strings.Repeat("a", 32)
	preferredToken := strings.Repeat("b", 32)
	scope := httptest.NewRequest(http.MethodGet, "/api/csrf", nil)
	for _, secure := range []bool{false, true} {
		a := &API{cfg: config.Config{CookieSecure: secure}}
		for _, scenario := range []struct {
			name    string
			cookies []*http.Cookie
			want    string
		}{
			{name: "new browser"},
			{name: "legacy ignored", cookies: []*http.Cookie{{Name: csrfCookieName, Value: legacyToken}}},
			{name: "preferred wins", cookies: []*http.Cookie{{Name: a.csrfCookieName(scope), Value: preferredToken}, {Name: csrfCookieName, Value: legacyToken}}, want: preferredToken},
			{name: "invalid preferred cannot fall back", cookies: []*http.Cookie{{Name: a.csrfCookieName(scope), Value: "short"}, {Name: csrfCookieName, Value: legacyToken}}},
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
				if body.Token == legacyToken {
					t.Fatal("bootstrap inherited a legacy token with unknown scope")
				}
				if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Vary") != "Cookie" {
					t.Fatal("CSRF bootstrap must not be cached")
				}
				post := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
				for _, cookie := range scenario.cookies {
					post.AddCookie(cookie)
				}
				for _, cookie := range w.Result().Cookies() {
					if cookie.Name != a.csrfCookieName(r) || cookie.Secure != secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
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

func TestCSRFDeploymentCookieAvoidsLegacyConflicts(t *testing.T) {
	scope := httptest.NewRequest(http.MethodGet, "/api/csrf", nil)
	for _, secure := range []bool{false, true} {
		a := &API{cfg: config.Config{CookieSecure: secure, DeploymentID: "deployment-a"}}
		other := &API{cfg: config.Config{CookieSecure: secure, DeploymentID: "deployment-b"}}
		if a.csrfCookieName(scope) == other.csrfCookieName(scope) {
			t.Fatal("independent installations share the same CSRF cookie")
		}
		if a.sessionCookieName(scope) == other.sessionCookieName(scope) {
			t.Fatal("independent installations share the same session cookie")
		}
		if a.csrfCookieName(scope) == csrfCookieName || a.csrfCookieName(scope) == httpCSRFCookieName {
			t.Fatal("scoped cookie conflicts with a legacy cookie")
		}
		legacy := []*http.Cookie{
			{Name: httpCSRFCookieName, Value: strings.Repeat("a", 32), Path: "/api/auth"},
			{Name: httpCSRFCookieName, Value: strings.Repeat("b", 32), Secure: true, Path: "/"},
			{Name: csrfCookieName, Value: strings.Repeat("c", 32)},
			{Name: other.csrfCookieName(scope), Value: strings.Repeat("d", 32)},
			{Name: httpCSRFCookieName + "_" + security.TokenHash(a.cfg.DeploymentID)[:16], Value: strings.Repeat("e", 32), Path: "/api/auth"},
		}
		r := httptest.NewRequest(http.MethodGet, "/api/csrf", nil)
		for _, cookie := range legacy {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.csrfToken(w, r)
		var body struct{ Token string }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		cookies := w.Result().Cookies()
		if w.Code != http.StatusOK || len(cookies) != 1 || cookies[0].Name != a.csrfCookieName(r) || cookies[0].Path != "/" || cookies[0].Secure != secure || len(body.Token) < 32 {
			t.Fatal("bootstrap did not create an independent root cookie")
		}
		for _, cookie := range legacy {
			if body.Token == cookie.Value {
				t.Fatal("bootstrap reused a legacy or another installation's token")
			}
		}
		post := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		for _, cookie := range legacy {
			post.AddCookie(cookie)
		}
		post.AddCookie(cookies[0])
		handler := a.csrfProtection(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		for _, token := range []string{body.Token, legacy[0].Value, legacy[3].Value, ""} {
			post.Header.Set("X-CSRF-Token", token)
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, post)
			want := http.StatusForbidden
			if token == body.Token {
				want = http.StatusNoContent
			}
			if result.Code != want {
				t.Fatalf("scoped cookie CSRF status=%d, want %d", result.Code, want)
			}
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

func TestCookieScopesFollowRequestAuthority(t *testing.T) {
	a := &API{cfg: config.Config{DeploymentID: "deployment-a"}}
	base := httptest.NewRequest(http.MethodGet, "http://velin.example:8377/api/csrf", nil)
	for _, target := range []string{
		"http://velin.example:8378/api/auth/login",
		"http://other.example:8377/api/auth/login",
	} {
		r := httptest.NewRequest(http.MethodPost, target, nil)
		if a.sessionCookieName(r) == a.sessionCookieName(base) || a.csrfCookieName(r) == a.csrfCookieName(base) {
			t.Fatal("different request authorities share cookies")
		}
		r.AddCookie(&http.Cookie{Name: a.csrfCookieName(base), Value: strings.Repeat("a", 32)})
		r.Header.Set("X-CSRF-Token", strings.Repeat("a", 32))
		w := httptest.NewRecorder()
		a.csrfProtection(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("another address's CSRF token accepted") })).ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("another address's CSRF status=%d", w.Code)
		}
	}
	// GET bootstrap, POST and WebSocket requests must choose the same scope,
	// even behind TLS termination or with inconsistent forwarded headers.
	post := httptest.NewRequest(http.MethodPost, "http://VELIN.EXAMPLE:8377/api/auth/login", nil)
	post.Header.Set("Origin", "https://other.example")
	post.Header.Set("X-Forwarded-Host", "other.example:9000")
	post.Header.Set("X-Forwarded-Proto", "https")
	ws := httptest.NewRequest(http.MethodGet, "http://velin.example:8377/ws/sessions/test", nil)
	for _, r := range []*http.Request{post, ws} {
		if a.sessionCookieName(r) != a.sessionCookieName(base) || a.csrfCookieName(r) != a.csrfCookieName(base) {
			t.Fatal("request method/path/forwarded headers changed the cookie scope")
		}
	}
}

func TestAuthCookiesCoexistAcrossPorts(t *testing.T) {
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
	a := &API{store: database, cfg: config.Config{DeploymentID: "same-deployment"}}
	first := httptest.NewServer(a.Router())
	defer first.Close()
	second := httptest.NewServer(a.Router())
	defer second.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	legacyName := httpCSRFCookieName + "_" + security.TokenHash(a.cfg.DeploymentID)[:16]
	for _, base := range []string{first.URL, second.URL} {
		address, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		jar.SetCookies(address, []*http.Cookie{{Name: legacyName, Value: strings.Repeat("x", 32), Path: "/api/auth"}})
	}
	request := func(base, method, path, body, csrf string, want int) map[string]any {
		t.Helper()
		r, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result map[string]any
		if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s status=%d, want %d: %v", method, path, response.StatusCode, want, result)
		}
		return result
	}
	csrfFirst := request(first.URL, http.MethodGet, "/api/csrf", "", "", http.StatusOK)["token"].(string)
	request(first.URL, http.MethodPost, "/api/auth/login", `{"username":"tester","password":"test-password"}`, csrfFirst, http.StatusOK)
	csrfSecond := request(second.URL, http.MethodGet, "/api/csrf", "", "", http.StatusOK)["token"].(string)
	request(second.URL, http.MethodPost, "/api/auth/login", `{"username":"tester","password":"test-password"}`, csrfSecond, http.StatusOK)
	request(first.URL, http.MethodGet, "/api/auth/me", "", "", http.StatusOK)
	request(second.URL, http.MethodGet, "/api/auth/me", "", "", http.StatusOK)
	if request(first.URL, http.MethodGet, "/api/csrf", "", "", http.StatusOK)["token"] != csrfFirst {
		t.Fatal("opening another port replaced the original CSRF cookie")
	}
	request(second.URL, http.MethodPost, "/api/auth/logout", "{}", csrfFirst, http.StatusForbidden)
	request(first.URL, http.MethodPost, "/api/auth/logout", "{}", csrfFirst, http.StatusOK)
	request(first.URL, http.MethodGet, "/api/auth/me", "", "", http.StatusUnauthorized)
	request(second.URL, http.MethodGet, "/api/auth/me", "", "", http.StatusOK)
	request(second.URL, http.MethodPost, "/api/auth/logout", "{}", csrfSecond, http.StatusOK)
}

func TestSessionCookieLifecycleIsolatesOtherAddresses(t *testing.T) {
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
		for _, action := range []string{"login", "logout"} {
			t.Run(action+map[bool]string{true: "/secure", false: "/http"}[secure], func(t *testing.T) {
				a := &API{cfg: config.Config{CookieSecure: secure, DeploymentID: "deployment-a"}, store: database}
				r := httptest.NewRequest(http.MethodPost, "http://velin.example/api/auth/"+action, strings.NewReader(`{"username":"tester","password":"test-password"}`))
				otherAddress := httptest.NewRequest(http.MethodGet, "http://velin.example:8378/api/auth/me", nil)
				foreignCookies := []*http.Cookie{
					{Name: cookieName, Value: "legacy-session"},
					{Name: httpCookieName, Value: "http-session"},
					{Name: a.sessionCookieName(otherAddress), Value: "other-address-session"},
				}
				for _, cookie := range foreignCookies {
					token := cookie.Value
					if err := database.CreateAuthSession(token, "user", security.TokenHash(token), "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
					r.AddCookie(cookie)
				}
				if _, _, err := a.currentAuthSession(r); err == nil {
					t.Fatal("inherited a legacy or another address's session")
				}
				if err := database.CreateAuthSession("current-session", "user", security.TokenHash("current-session"), "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				r.AddCookie(&http.Cookie{Name: a.sessionCookieName(r), Value: "current-session"})
				user, tokenHash, err := a.currentAuthSession(r)
				if err != nil || tokenHash != security.TokenHash("current-session") {
					t.Fatal("current session not accepted")
				}
				r = r.WithContext(context.WithValue(r.Context(), userKey, user))
				w := httptest.NewRecorder()
				switch action {
				case "login":
					a.login(w, r)
				case "logout":
					a.logout(w, r)
				}
				if w.Code != http.StatusOK {
					t.Fatalf("status=%d: %s", w.Code, w.Body.String())
				}
				if _, err := database.UserByToken(security.TokenHash("current-session")); err == nil {
					t.Fatal("current browser session was not revoked")
				}
				for _, cookie := range foreignCookies {
					if _, err := database.UserByToken(security.TokenHash(cookie.Value)); err != nil {
						t.Fatal("another address's session was revoked")
					}
				}
				cleared := map[string]bool{}
				for _, cookie := range w.Result().Cookies() {
					if cookie.Name != a.sessionCookieName(r) || cookie.Secure != secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
						t.Fatal("incorrect session cookie attributes")
					}
					if cookie.MaxAge < 0 {
						cleared[cookie.Name] = true
						continue
					}
					if action != "login" {
						t.Fatal("unexpected session cookie")
					}
					if _, err := database.UserByToken(security.TokenHash(cookie.Value)); err != nil {
						t.Fatal("new login session is invalid")
					}
				}
				if len(cleared) != 1 || !cleared[a.sessionCookieName(r)] {
					t.Fatal("only the current session cookie should be cleared")
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
	for _, deploymentID := range []string{"", "deployment-a"} {
		a := &API{store: database, cfg: config.Config{DeploymentID: deploymentID}}
		r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: "valid"})
		r.AddCookie(&http.Cookie{Name: a.sessionCookieName(r), Value: "invalid"})
		if _, _, err = a.currentAuthSession(r); err == nil {
			t.Fatal("invalid preferred cookie must not fall back to legacy")
		}
	}
}

func TestScopedSessionInitialPasswordChangeLifecycle(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "https"}[secure], func(t *testing.T) {
			dataDir := t.TempDir()
			database, err := store.Open(filepath.Join(dataDir, "auth.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			hash, err := security.HashPassword("initial-test-password")
			if err != nil {
				t.Fatal(err)
			}
			if err = database.CreateUser("user", "tester", hash, "admin"); err != nil {
				t.Fatal(err)
			}
			if err = database.SetForcePasswordChange("user", true); err != nil {
				t.Fatal(err)
			}
			a := &API{store: database, cfg: config.Config{CookieSecure: secure, DeploymentID: "deployment-a", DataDir: dataDir}}
			handler := a.Router()
			scope := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			// Legacy path cookies appear first in a browser's Cookie header.
			cookies := []*http.Cookie{
				{Name: httpCookieName, Value: "stale-path-session"},
				{Name: cookieName, Value: "stale-secure-session"},
			}
			csrf := ""
			request := func(method, path, body string, want int) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				for _, cookie := range cookies {
					r.AddCookie(cookie)
				}
				r.Header.Set("X-CSRF-Token", csrf)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s %s status=%d, want %d: %s", method, path, w.Code, want, w.Body.String())
				}
				return w
			}
			bootstrap := request(http.MethodGet, "/api/csrf", "", http.StatusOK)
			var token struct{ Token string }
			if err = json.Unmarshal(bootstrap.Body.Bytes(), &token); err != nil {
				t.Fatal(err)
			}
			csrf = token.Token
			cookies = append(cookies, bootstrap.Result().Cookies()...)
			login := request(http.MethodPost, "/api/auth/login", `{"username":"tester","password":"initial-test-password"}`, http.StatusOK)
			for _, cookie := range login.Result().Cookies() {
				if cookie.MaxAge > 0 {
					if cookie.Name != a.sessionCookieName(scope) || cookie.Name == cookieName || cookie.Name == httpCookieName || !cookie.HttpOnly || cookie.Secure != secure {
						t.Fatal("login did not set an independent session cookie")
					}
					cookies = append(cookies, cookie)
				}
			}
			request(http.MethodGet, "/api/auth/me", "", http.StatusOK)
			request(http.MethodGet, "/api/hosts", "", http.StatusForbidden)
			request(http.MethodPost, "/api/auth/password", `{"currentPassword":"initial-test-password","newPassword":"updated-test-password"}`, http.StatusOK)
			me := request(http.MethodGet, "/api/auth/me", "", http.StatusOK)
			var user store.User
			if err = json.Unmarshal(me.Body.Bytes(), &user); err != nil || user.ForcePasswordChange {
				t.Fatal("password change did not preserve an authenticated, initialized session")
			}
			_, updatedHash, err := database.UserByUsername("tester")
			if err != nil || !security.VerifyPassword(updatedHash, "updated-test-password") {
				t.Fatal("new password was not saved")
			}
			request(http.MethodGet, "/api/hosts", "", http.StatusOK)
			request(http.MethodPost, "/api/auth/logout", "{}", http.StatusOK)
			request(http.MethodGet, "/api/auth/me", "", http.StatusUnauthorized)
		})
	}
}
