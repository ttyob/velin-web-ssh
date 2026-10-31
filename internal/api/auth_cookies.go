package api

import (
	"net/http"

	"github.com/ttyob/velin-web-ssh/internal/security"
)

const httpCookieName = "velin_session_http"
const httpCSRFCookieName = "velin_csrf_http"

// HTTP cannot replace a Secure cookie left by an earlier configuration.
// New sessions also use an installation scope to avoid stale path cookies and
// other servers on the same host, since browsers share cookies across ports.
func (a *API) sessionCookieName() string {
	if !a.cfg.CookieSecure {
		return a.deploymentCookieName(httpCookieName)
	}
	return a.deploymentCookieName(cookieName)
}

func (a *API) csrfCookieName() string {
	name := csrfCookieName
	if !a.cfg.CookieSecure {
		name = httpCSRFCookieName
	}
	return a.deploymentCookieName(name)
}

func (a *API) deploymentCookieName(name string) string {
	if a.cfg.DeploymentID != "" {
		name += "_" + security.TokenHash(a.cfg.DeploymentID)[:16]
	}
	return name
}

func preferredCookie(r *http.Request, name, legacyName string) (*http.Cookie, error) {
	cookie, err := r.Cookie(name)
	if err == http.ErrNoCookie && name != legacyName {
		return r.Cookie(legacyName)
	}
	return cookie, err
}

func (a *API) sessionCookie(r *http.Request) (*http.Cookie, error) {
	if cookie, err := r.Cookie(a.sessionCookieName()); err != http.ErrNoCookie {
		return cookie, err
	}
	// Keep existing readable sessions working during the upgrade, but never
	// fall back when a scoped cookie is present and its session has expired.
	if !a.cfg.CookieSecure {
		return preferredCookie(r, httpCookieName, cookieName)
	}
	return r.Cookie(cookieName)
}

func (a *API) csrfCookie(r *http.Request) (*http.Cookie, error) {
	if a.cfg.DeploymentID != "" {
		// Bootstrap a fresh scoped cookie instead of migrating a potentially
		// shadowed legacy cookie. Clients automatically retry stale tokens.
		return r.Cookie(a.csrfCookieName())
	}
	return preferredCookie(r, a.csrfCookieName(), csrfCookieName)
}

func (a *API) revokeBrowserSessions(r *http.Request) {
	// Revoke both cookies so clearing the preferred cookie cannot revive an
	// older session through the migration fallback.
	for _, cookie := range r.Cookies() {
		if cookie.Name == a.sessionCookieName() || cookie.Name == cookieName || cookie.Name == httpCookieName {
			_ = a.store.DeleteAuthSession(security.TokenHash(cookie.Value))
		}
	}
}

func (a *API) clearSessionCookies(w http.ResponseWriter) {
	cleared := make(map[string]bool)
	for _, name := range []string{a.sessionCookieName(), cookieName, httpCookieName} {
		if cleared[name] {
			continue
		}
		cleared[name] = true
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	}
}
