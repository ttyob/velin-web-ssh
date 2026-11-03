package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/ttyob/velin-web-ssh/internal/sessionbridge"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
)

func (a *API) initSessionBridge() {
	module, err := sessionbridge.New(a.store, sessionBridgeBackend{a})
	if err != nil {
		slog.Error("session bridge unavailable", "error", err)
		return
	}
	a.sessionBridge = module
}

func (a *API) registerSessionBridgeRoutes(r chi.Router) {
	r.Get("/api/session-bridge/tokens", a.agentTokens)
	r.Post("/api/session-bridge/tokens", a.createAgentToken)
	r.Put("/api/session-bridge/tokens/{id}", a.updateAgentToken)
	r.Delete("/api/session-bridge/tokens/{id}", a.deleteAgentToken)
}

func (a *API) serveSessionBridge(w http.ResponseWriter, r *http.Request) {
	if a.bridgeAvailable(w) {
		a.sessionBridge.ServeHTTP(w, r)
	}
}
func (a *API) bridgeAvailable(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if a.sessionBridge == nil {
		writeError(w, 503, "session_bridge_unavailable", "外部 Agent 接入暂不可用，请查看服务日志")
		return false
	}
	return true
}
func bridgeTokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sessionbridge.ErrInvalid):
		writeError(w, 400, "invalid_agent_token", err.Error())
	case errors.Is(err, sessionbridge.ErrNotFound):
		writeError(w, 404, "agent_token_not_found", "令牌不存在")
	default:
		slog.Error("session bridge token operation failed", "error", err)
		writeError(w, 500, "agent_token_error", "令牌操作失败，请稍后重试")
	}
}
func (a *API) agentTokens(w http.ResponseWriter, r *http.Request) {
	if !a.bridgeAvailable(w) {
		return
	}
	tokens, err := a.sessionBridge.Tokens(currentUser(r).ID)
	if err != nil {
		bridgeTokenError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": tokens, "endpoint": sessionbridge.Endpoint, "tools": sessionbridge.ToolNames()})
}
func (a *API) createAgentToken(w http.ResponseWriter, r *http.Request) {
	if !a.bridgeAvailable(w) {
		return
	}
	var input sessionbridge.TokenInput
	if !decode(w, r, &input) {
		return
	}
	user := currentUser(r)
	token, secret, err := a.sessionBridge.CreateToken(user.ID, input)
	if err != nil {
		bridgeTokenError(w, err)
		return
	}
	_ = a.store.Audit(user.ID, "session_bridge.token_created", "session_bridge_token", token.ID, a.clientIP(r), token)
	writeJSON(w, 201, map[string]any{"token": token, "secret": secret})
}
func (a *API) updateAgentToken(w http.ResponseWriter, r *http.Request) {
	if !a.bridgeAvailable(w) {
		return
	}
	var input sessionbridge.TokenInput
	if !decode(w, r, &input) {
		return
	}
	user := currentUser(r)
	token, err := a.sessionBridge.UpdateToken(user.ID, chi.URLParam(r, "id"), input)
	if err != nil {
		bridgeTokenError(w, err)
		return
	}
	_ = a.store.Audit(user.ID, "session_bridge.token_updated", "session_bridge_token", token.ID, a.clientIP(r), token)
	writeJSON(w, 200, token)
}
func (a *API) deleteAgentToken(w http.ResponseWriter, r *http.Request) {
	if !a.bridgeAvailable(w) {
		return
	}
	user, id := currentUser(r), chi.URLParam(r, "id")
	if err := a.sessionBridge.DeleteToken(user.ID, id); err != nil {
		bridgeTokenError(w, err)
		return
	}
	_ = a.store.Audit(user.ID, "session_bridge.token_deleted", "session_bridge_token", id, a.clientIP(r), nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type sessionBridgeBackend struct{ api *API }

func (b sessionBridgeBackend) session(userID string, meta store.TerminalSession) sessionbridge.Session {
	result := sessionbridge.Session{ID: meta.ID, HostID: meta.HostID, Name: meta.Name, Mode: meta.SessionMode, Status: meta.Status, LastError: meta.LastError}
	if host, err := b.api.store.Host(userID, meta.HostID); err == nil {
		result.HostName = host.Name
	}
	if live, err := b.api.terminals.Get(userID, meta.ID); err == nil {
		current := live.Meta()
		result.Status, result.LastError = current.Status, current.LastError
		result.Attached = current.Status == "attached"
	}
	return result
}
func (b sessionBridgeBackend) Sessions(ctx context.Context, userID string) ([]sessionbridge.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := b.api.store.Terminals(userID)
	if err != nil {
		return nil, err
	}
	result := make([]sessionbridge.Session, 0, len(records))
	for _, meta := range records {
		result = append(result, b.session(userID, meta))
	}
	return result, nil
}
func (b sessionBridgeBackend) Hosts(ctx context.Context, userID string) ([]sessionbridge.Host, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hosts, err := b.api.store.Hosts(userID)
	if err != nil {
		return nil, err
	}
	result := make([]sessionbridge.Host, 0, len(hosts))
	for _, host := range hosts {
		if host.Protocol == "" || host.Protocol == "ssh" {
			result = append(result, sessionbridge.Host{ID: host.ID, Name: host.Name, Address: net.JoinHostPort(host.Address, strconv.Itoa(host.Port))})
		}
	}
	return result, nil
}
func (b sessionBridgeBackend) Snapshot(ctx context.Context, userID, id string, maxLines int) (sessionbridge.Snapshot, error) {
	live, err := b.api.terminals.Get(userID, id)
	if err != nil {
		return sessionbridge.Snapshot{}, errors.New("会话当前未附加到此服务，请先在 Velin 中连接或恢复 tmux 会话")
	}
	value, err := live.BridgeSnapshot(ctx, maxLines)
	if err != nil {
		return sessionbridge.Snapshot{}, err
	}
	return sessionbridge.Snapshot{Cursor: sessionbridge.Cursor{StreamID: value.StreamID, Offset: value.Offset}, Raw: value.Raw, Capture: value.Capture, Truncated: value.Truncated, Rows: value.Rows, Cols: value.Cols, LastOutputAt: value.LastOutputAt, Source: value.Source}, nil
}
func (b sessionBridgeBackend) WriteInput(ctx context.Context, userID, id string, data []byte) (int, error) {
	live, err := b.api.terminals.Get(userID, id)
	if err != nil {
		return 0, sessionbridge.ErrInputRejected
	}
	n, err := live.WriteExternal(ctx, data)
	if errors.Is(err, terminal.ErrExternalInputRejected) {
		return n, fmt.Errorf("%w: %s", sessionbridge.ErrInputRejected, err)
	}
	return n, err
}
func (b sessionBridgeBackend) Create(ctx context.Context, userID, hostID, name, mode string) (sessionbridge.Session, error) {
	host, err := b.api.store.Host(userID, hostID)
	if err != nil || host.Protocol != "" && host.Protocol != "ssh" {
		return sessionbridge.Session{}, errors.New("SSH 主机不存在")
	}
	var credential store.Credential
	if host.CredentialID != "" {
		credential, err = b.api.store.Credential(userID, host.CredentialID)
		if err != nil {
			return sessionbridge.Session{}, errors.New("保存的凭据不可用，请在 Velin 中检查主机认证设置")
		}
	}
	meta, err := b.api.terminals.Create(ctx, userID, terminal.CreateRequest{Host: host, Credential: credential, Name: name, SessionMode: mode})
	if err != nil {
		return sessionbridge.Session{}, err
	}
	return b.session(userID, meta), nil
}
func (b sessionBridgeBackend) Restore(ctx context.Context, userID, id string) (sessionbridge.Session, error) {
	live, err := b.api.terminals.Restore(ctx, userID, id, "", "", "")
	if err != nil {
		return sessionbridge.Session{}, err
	}
	return b.session(userID, live.Meta()), nil
}
func (b sessionBridgeBackend) Rename(ctx context.Context, userID, id, name string) (sessionbridge.Session, error) {
	if err := ctx.Err(); err != nil {
		return sessionbridge.Session{}, err
	}
	if err := b.api.terminals.Rename(userID, id, name); err != nil {
		return sessionbridge.Session{}, err
	}
	meta, err := b.api.store.Terminal(userID, id)
	if err != nil {
		return sessionbridge.Session{}, err
	}
	return b.session(userID, meta), nil
}
func (b sessionBridgeBackend) Terminate(ctx context.Context, userID, id string) (sessionbridge.Termination, error) {
	value, err := b.api.terminals.Terminate(ctx, userID, id, "", "")
	return sessionbridge.Termination{OK: value.OK, RemoteTerminated: value.RemoteTerminated, Warning: value.Warning}, err
}
