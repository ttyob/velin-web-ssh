package api

import (
	"net/http"

	"github.com/ttyob/velin-web-ssh/internal/security"
)

const httpCookieName = "velin_session_http"
const httpCSRFCookieName = "velin_csrf_http"

// HTTP cannot replace a Secure cookie left by an earlier configuration.
// Use separate names and accept readable legacy cookies during migration.
func (a *API) sessionCookieName() string {
	if !a.cfg.CookieSecure {
		return httpCookieName
	}
	return cookieName
}

func (a *API) csrfCookieName() string {
	if !a.cfg.CookieSecure {
		return httpCSRFCookieName
	}
	return csrfCookieName
}

func preferredCookie(r *http.Request, name, legacyName string) (*http.Cookie, error) {
	cookie, err := r.Cookie(name)
	if err == http.ErrNoCookie && name != legacyName {
		return r.Cookie(legacyName)
	}
	return cookie, err
}

func (a *API) sessionCookie(r *http.Request) (*http.Cookie, error) {
	return preferredCookie(r, a.sessionCookieName(), cookieName)
}

func (a *API) csrfCookie(r *http.Request) (*http.Cookie, error) {
	return preferredCookie(r, a.csrfCookieName(), csrfCookieName)
}

func (a *API) revokeBrowserSessions(r *http.Request) {
	// Revoke both cookies so clearing the preferred cookie cannot revive an
	// older session through the migration fallback.
	for _, cookie := range r.Cookies() {
		if cookie.Name == cookieName || cookie.Name == httpCookieName {
			_ = a.store.DeleteAuthSession(security.TokenHash(cookie.Value))
		}
	}
}

func (a *API) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{cookieName, httpCookieName} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	}
}
