package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ttyob/velin-web-ssh/internal/remotedesktop"
)

func (a *API) createDesktopSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input remotedesktop.CreateRequest
	if !decode(w, r, &input) {
		return
	}
	session, err := a.desktops.Create(r.Context(), currentUser(r).ID, input)
	if err != nil {
		switch {
		case errors.Is(err, remotedesktop.ErrCredentialRequired):
			writeError(w, http.StatusBadRequest, "credential_required", "desktop credential required")
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "host_not_found", "主机不存在")
		default:
			slog.Warn("create desktop session failed", "error", err)
			writeError(w, http.StatusBadRequest, "desktop_session_failed", "无法创建远程桌面会话")
		}
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (a *API) desktopVNC(w http.ResponseWriter, r *http.Request) {
	a.websockets.Add(1)
	a.wsTotal.Add(1)
	defer a.websockets.Add(-1)
	if err := a.desktops.ServeVNC(w, r, currentUser(r).ID, chi.URLParam(r, "token")); err != nil {
		slog.Warn("VNC desktop connection closed", "error", err)
	}
}

func (a *API) desktopRDP(w http.ResponseWriter, r *http.Request) {
	a.websockets.Add(1)
	a.wsTotal.Add(1)
	defer a.websockets.Add(-1)
	if err := a.desktops.ServeRDP(w, r, currentUser(r).ID, chi.URLParam(r, "token")); err != nil {
		slog.Warn("RDP desktop connection closed", "error", err)
	}
}
