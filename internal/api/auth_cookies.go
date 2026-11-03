package api

import (
	"net/http"
	"strings"

	"github.com/ttyob/velin-web-ssh/internal/security"
)

const httpCookieName = "velin_session_http"
const httpCSRFCookieName = "velin_csrf_http"

// Browsers share cookies across ports. Scope names to both the installation and
// the request authority, including its port, without setting a cookie Domain.
// The v2 namespace also leaves historical path and Secure cookies behind.
func (a *API) sessionCookieName(r *http.Request) string {
	if !a.cfg.CookieSecure {
		return a.scopedCookieName(httpCookieName, r)
	}
	return a.scopedCookieName(cookieName, r)
}

func (a *API) csrfCookieName(r *http.Request) string {
	name := csrfCookieName
	if !a.cfg.CookieSecure {
		name = httpCSRFCookieName
	}
	return a.scopedCookieName(name, r)
}

func (a *API) scopedCookieName(name string, r *http.Request) string {
	// Host is present on bootstrap, API and WebSocket requests alike. Do not use
	// Origin (absent on GET) or untrusted forwarded headers to select a scope.
	scope := a.cfg.DeploymentID + "\x00" + strings.ToLower(r.Host)
	return name + "_v2_" + security.TokenHash(scope)[:22]
}

func (a *API) sessionCookie(r *http.Request) (*http.Cookie, error) {
	// Older cookies cannot identify their original host/port. Require one new
	// login after upgrading instead of inheriting another address's session.
	return r.Cookie(a.sessionCookieName(r))
}

func (a *API) csrfCookie(r *http.Request) (*http.Cookie, error) {
	return r.Cookie(a.csrfCookieName(r))
}

func (a *API) revokeBrowserSessions(r *http.Request) {
	for _, cookie := range r.Cookies() {
		if cookie.Name == a.sessionCookieName(r) {
			_ = a.store.DeleteAuthSession(security.TokenHash(cookie.Value))
		}
	}
}

func (a *API) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	// Never clear legacy cookies or cookies owned by another address/instance.
	http.SetCookie(w, &http.Cookie{Name: a.sessionCookieName(r), Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
}
