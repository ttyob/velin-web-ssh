package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ttyob/velin-web-ssh/internal/agent"
)

func (a *API) agentGitText(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithAgentTimeout(r, 30*time.Second)
	defer cancel()
	q := r.URL.Query()
	if q.Get("mode") == "info" {
		info, err := a.agents.GitFileInfo(ctx, currentUser(r).ID, chi.URLParam(r, "id"), q.Get("repository"), q.Get("path"), q.Get("revision"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, info)
		return
	}
	if q.Get("mode") == "image" {
		content, contentType, err := a.agents.GitPreviewImage(ctx, currentUser(r).ID, chi.URLParam(r, "id"), q.Get("repository"), q.Get("path"), q.Get("revision"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		_, _ = w.Write([]byte(content))
		return
	}
	content, exists, err := a.agents.GitText(ctx, currentUser(r).ID, chi.URLParam(r, "id"), q.Get("repository"), q.Get("path"), q.Get("revision"))
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": content, "exists": exists})
}

func (a *API) agentGitIndexEdit(w http.ResponseWriter, r *http.Request) {
	var in agent.GitIndexEdit
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 26<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeError(w, 400, "invalid_json", "请求格式无效")
		return
	}
	ctx, cancel := contextWithAgentTimeout(r, 30*time.Second)
	defer cancel()
	if err := a.agents.EditGitIndex(ctx, currentUser(r).ID, chi.URLParam(r, "id"), in); err != nil {
		writeError(w, 409, "git_index_changed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
