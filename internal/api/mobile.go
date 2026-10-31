package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
)

func (a *API) registerMobileRoutes(r chi.Router) {
	r.Get("/api/mobile/v1/capabilities", a.mobileCapabilities)
}

func (a *API) mobileCapabilities(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"protocolVersion":     1,
		"viewAttach":          true,
		"geometry":            true,
		"inputAck":            true,
		"maxInputBytes":       terminal.MobileInputMaxBytes,
		"inputDedupeSeconds":  600,
		"inputDedupeCapacity": 512,
	})
}
