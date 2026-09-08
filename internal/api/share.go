package api

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"velin-webssh/internal/security"
	"velin-webssh/internal/store"
	"velin-webssh/internal/terminal"
)

type shareViewerConnection struct {
	ID          string
	Name        string
	IP          string
	UserAgent   string
	Permission  string
	ConnectedAt time.Time
	conn        *websocket.Conn
	writeMu     sync.Mutex
}

func (v *shareViewerConnection) writeJSON(value any) error {
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	_ = v.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return v.conn.WriteJSON(value)
}

func (v *shareViewerConnection) close(code int, reason string) {
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	_ = v.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = v.conn.Close()
}

type shareFailure struct {
	Count        int
	BlockedUntil time.Time
}

type terminalShareView struct {
	ID               string                `json:"id"`
	SessionID        string                `json:"sessionID"`
	SessionName      string                `json:"sessionName"`
	Permission       string                `json:"permission"`
	PasswordRequired bool                  `json:"passwordRequired"`
	Record           bool                  `json:"record"`
	RecordingID      string                `json:"recordingID,omitempty"`
	ExpiresAt        time.Time             `json:"expiresAt"`
	RevokedAt        *time.Time            `json:"revokedAt,omitempty"`
	CreatedAt        time.Time             `json:"createdAt"`
	Active           bool                  `json:"active"`
	Authorized       bool                  `json:"authorized,omitempty"`
	CanManage        bool                  `json:"canManage,omitempty"`
	URL              string                `json:"url,omitempty"`
	ViewerCount      int                   `json:"viewerCount"`
	Viewers          []terminalShareViewer `json:"viewers,omitempty"`
}

type terminalShareViewer struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	IP          string    `json:"ip"`
	Permission  string    `json:"permission"`
	ConnectedAt time.Time `json:"connectedAt"`
}

func (a *API) createTerminalShare(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	sessionID := chi.URLParam(r, "id")
	if _, err := a.terminals.Get(user.ID, sessionID); err != nil {
		writeError(w, http.StatusConflict, "session_not_attached", "只能分享当前已连接的终端会话")
		return
	}
	var input struct {
		Password         string `json:"password"`
		Permission       string `json:"permission"`
		ExpiresInMinutes int    `json:"expiresInMinutes"`
		Record           bool   `json:"record"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Permission != "view" && input.Permission != "operate" {
		writeError(w, http.StatusBadRequest, "invalid_share_permission", "分享权限无效")
		return
	}
	if input.ExpiresInMinutes < 5 || input.ExpiresInMinutes > 30*24*60 {
		writeError(w, http.StatusBadRequest, "invalid_share_expiry", "分享有效期必须在 5 分钟至 30 天之间")
		return
	}
	if len([]rune(input.Password)) > 128 || (input.Password != "" && len([]rune(input.Password)) < 4) {
		writeError(w, http.StatusBadRequest, "invalid_share_password", "分享密码至少 4 个字符且不能超过 128 个字符")
		return
	}
	passwordHash := ""
	var err error
	if input.Password != "" {
		passwordHash, err = security.HashPassword(input.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "share_create_failed", "无法保护分享密码")
			return
		}
	}
	token, err := security.RandomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "share_create_failed", "无法生成分享链接")
		return
	}
	tokenEnc, err := a.vault.Encrypt(token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "share_create_failed", "无法保护分享链接")
		return
	}
	share := store.TerminalShare{
		ID: uuid.NewString(), UserID: user.ID, SessionID: sessionID,
		TokenHash: security.TokenHash(token), TokenEnc: tokenEnc, PasswordHash: passwordHash,
		Permission: input.Permission, Record: input.Record,
		ExpiresAt: time.Now().UTC().Add(time.Duration(input.ExpiresInMinutes) * time.Minute),
		CreatedAt: time.Now().UTC(),
	}
	if input.Record {
		recording, recordErr := a.terminals.StartShareRecording(user.ID, sessionID)
		if recordErr != nil {
			writeError(w, http.StatusConflict, "share_recording_failed", recordErr.Error())
			return
		}
		share.RecordingID = recording.ID
	}
	if err = a.store.CreateTerminalShare(share); err != nil {
		if share.RecordingID != "" {
			a.terminals.StopRecordingIfID(user.ID, sessionID, share.RecordingID)
		}
		writeError(w, http.StatusInternalServerError, "share_create_failed", "创建分享失败")
		return
	}
	_ = a.store.Audit(user.ID, "terminal_share_created", "terminal_session", sessionID, a.clientIP(r), map[string]any{
		"shareID": share.ID, "permission": share.Permission, "record": share.Record, "expiresAt": share.ExpiresAt,
	})
	view := a.terminalShareView(share, true)
	view.URL = "/share/" + token
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) terminalShares(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	values, err := a.store.TerminalShares(user.ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "shares_unavailable", "读取分享记录失败")
		return
	}
	result := make([]terminalShareView, 0, len(values))
	for _, value := range values {
		view := a.terminalShareView(value, true)
		if token, decryptErr := a.vault.Decrypt(value.TokenEnc); decryptErr == nil {
			view.URL = "/share/" + token
		}
		result = append(result, view)
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) revokeTerminalShare(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	share, err := a.store.RevokeTerminalShare(user.ID, chi.URLParam(r, "id"), time.Now().UTC())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "share_not_found", "分享不存在")
		} else {
			writeError(w, http.StatusInternalServerError, "share_revoke_failed", "中止分享失败")
		}
		return
	}
	a.finishTerminalShare(share, "分享已由创建者中止")
	_ = a.store.Audit(user.ID, "terminal_share_revoked", "terminal_session", share.SessionID, a.clientIP(r), map[string]any{"shareID": share.ID})
	writeJSON(w, http.StatusOK, a.terminalShareView(share, true))
}

func (a *API) publicTerminalShare(w http.ResponseWriter, r *http.Request) {
	share, err := a.loadTerminalShare(chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, http.StatusNotFound, "share_not_found", "分享链接不存在")
		return
	}
	owner := a.isTerminalShareOwner(r, share)
	_, authorized := a.terminalShareAccess(r, share)
	view := a.terminalShareView(share, owner)
	view.Authorized = owner || authorized
	view.CanManage = owner
	if share.PasswordRequired && !view.Authorized {
		view.SessionName = "共享终端"
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) accessTerminalShare(w http.ResponseWriter, r *http.Request) {
	share, err := a.loadActiveTerminalShare(chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, http.StatusGone, "share_inactive", "分享已中止或过期")
		return
	}
	var input struct {
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		input.DisplayName = "访客"
	}
	if len([]rune(input.DisplayName)) > 40 || strings.ContainsRune(input.DisplayName, '\x00') {
		writeError(w, http.StatusBadRequest, "invalid_display_name", "访客名称无效")
		return
	}
	failureKey := share.ID + ":" + a.clientIP(r)
	if wait := a.shareFailureWait(failureKey); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "share_access_locked", "密码错误次数过多，请稍后重试")
		return
	}
	if share.PasswordRequired && !security.VerifyPassword(share.PasswordHash, input.Password) {
		a.recordShareFailure(failureKey)
		writeError(w, http.StatusUnauthorized, "invalid_share_password", "分享密码错误")
		return
	}
	a.clearShareFailure(failureKey)
	accessToken, err := security.RandomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "share_access_failed", "无法建立分享访问会话")
		return
	}
	now := time.Now().UTC()
	access := store.TerminalShareAccess{
		TokenHash: security.TokenHash(accessToken), ShareID: share.ID, DisplayName: input.DisplayName,
		IP: a.clientIP(r), UserAgent: r.UserAgent(), ExpiresAt: share.ExpiresAt, CreatedAt: now, LastSeenAt: now,
	}
	if err = a.store.CreateTerminalShareAccess(access); err != nil {
		writeError(w, http.StatusInternalServerError, "share_access_failed", "无法建立分享访问会话")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: shareCookieName(share.ID), Value: accessToken, Path: "/", HttpOnly: true,
		Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, Expires: share.ExpiresAt,
	})
	view := a.terminalShareView(share, false)
	view.Authorized = true
	writeJSON(w, http.StatusOK, view)
}

func (a *API) terminalShareWS(w http.ResponseWriter, r *http.Request) {
	share, err := a.loadActiveTerminalShare(chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, http.StatusGone, "share_inactive", "分享已中止或过期")
		return
	}
	owner := a.isTerminalShareOwner(r, share)
	access, authorized := a.terminalShareAccess(r, share)
	if !owner && !authorized {
		writeError(w, http.StatusUnauthorized, "share_access_required", "请先验证分享密码")
		return
	}
	session, err := a.terminals.Get(share.UserID, share.SessionID)
	if err != nil {
		writeError(w, http.StatusConflict, "shared_session_unavailable", "共享终端当前未连接")
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: sameOrigin, ReadBufferSize: 4096, WriteBufferSize: 32768}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	a.websockets.Add(1)
	a.wsTotal.Add(1)
	defer a.websockets.Add(-1)
	defer conn.Close()
	conn.SetReadLimit(128 * 1024)

	permission := share.Permission
	name := access.DisplayName
	if owner {
		permission = "view"
		name = "创建者预览"
	}
	viewerID := uuid.NewString()
	viewer := &shareViewerConnection{ID: viewerID, Name: name, IP: a.clientIP(r), UserAgent: r.UserAgent(), Permission: permission, ConnectedAt: time.Now().UTC(), conn: conn}
	a.addShareViewer(share.ID, viewer)
	defer a.removeShareViewer(share.ID, viewerID)
	if access.TokenHash != "" {
		_ = a.store.TouchTerminalShareAccess(access.TokenHash, time.Now().UTC())
	}

	clientID := "share:" + viewerID
	resumeStreamID := r.URL.Query().Get("stream")
	resumeOffset, _ := strconv.ParseUint(r.URL.Query().Get("offset"), 10, 64)
	var events <-chan terminal.Event
	var subscriptionDone <-chan struct{}
	var replay terminal.Replay
	if permission == "operate" {
		events, subscriptionDone, replay = session.Subscribe(clientID, resumeStreamID, resumeOffset)
	} else {
		events, subscriptionDone, replay = session.SubscribeReadOnly(clientID, resumeStreamID, resumeOffset)
	}
	defer session.Unsubscribe(clientID)

	writeJSON := viewer.writeJSON
	controller := ""
	if permission == "operate" && session.IsController(clientID) {
		controller = clientID
	}
	meta := session.Meta()
	if err = writeJSON(terminal.Event{Type: "hello", ClientID: clientID, Controller: controller, Status: meta.Status, Message: meta.LastError, Truncated: replay.Truncated, StreamID: replay.StreamID, Offset: replay.Offset}); err != nil {
		return
	}
	for index, segment := range replay.Segments {
		if writeJSON(terminal.Event{Type: "replay", Data: base64.StdEncoding.EncodeToString(segment), ReplayFinal: index+1 == len(replay.Segments)}) != nil {
			return
		}
	}
	if len(replay.Segments) == 0 {
		if writeJSON(terminal.Event{Type: "replay_end"}) != nil {
			return
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case event, ok := <-events:
				if !ok {
					viewer.close(4003, "共享终端已结束")
					return
				}
				if writeJSON(event) != nil {
					return
				}
			case <-subscriptionDone:
				viewer.close(4003, "共享终端已结束")
				return
			case <-ticker.C:
				current, loadErr := a.store.TerminalShareByTokenHash(share.TokenHash)
				if loadErr != nil || !terminalShareActive(current, time.Now()) {
					reason := "分享已中止或不可用"
					if loadErr == nil {
						reason = terminalShareEndReason(current, time.Now())
					}
					viewer.close(4003, reason)
					return
				}
			}
		}
	}()

	for {
		var message terminalWSMessage
		if err = conn.ReadJSON(&message); err != nil {
			return
		}
		if permission == "operate" {
			switch message.Type {
			case "input":
				raw, decodeErr := base64.StdEncoding.DecodeString(message.Data)
				if decodeErr == nil {
					_ = session.Write(clientID, raw)
				}
			case "resize":
				_ = session.Resize(clientID, message.Rows, message.Cols)
			case "request_control":
				if session.RequestControl(clientID) {
					_ = writeJSON(terminal.Event{Type: "control_granted", Controller: clientID})
				} else {
					_ = writeJSON(terminal.Event{Type: "control_pending"})
				}
			case "release_control":
				session.ReleaseControl(clientID)
			}
		}
		if message.Type == "ping" {
			session.TouchControl(clientID)
			_ = writeJSON(terminal.Event{Type: "pong"})
		}
		select {
		case <-done:
			return
		default:
		}
	}
}

func (a *API) loadTerminalShare(token string) (store.TerminalShare, error) {
	if len(token) < 32 || len(token) > 128 || strings.ContainsRune(token, '\x00') {
		return store.TerminalShare{}, sql.ErrNoRows
	}
	return a.store.TerminalShareByTokenHash(security.TokenHash(token))
}

func (a *API) loadActiveTerminalShare(token string) (store.TerminalShare, error) {
	share, err := a.loadTerminalShare(token)
	if err != nil {
		return share, err
	}
	now := time.Now().UTC()
	if !terminalShareActive(share, now) {
		a.closeInactiveTerminalShare(share, now, "")
		return share, errors.New("share inactive")
	}
	return share, nil
}

func terminalShareActive(share store.TerminalShare, now time.Time) bool {
	return share.RevokedAt == nil && share.ExpiresAt.After(now) && share.SessionStatus != "ended" && !share.OwnerDisabled
}

func terminalShareEndReason(share store.TerminalShare, now time.Time) string {
	if !share.ExpiresAt.After(now) {
		return "分享已过期"
	}
	if share.OwnerDisabled {
		return "分享创建者账户已停用"
	}
	if share.SessionStatus == "ended" {
		return "共享终端已结束"
	}
	return "分享已中止"
}

func terminalShareEndEvent(share store.TerminalShare, now time.Time) string {
	if !share.ExpiresAt.After(now) {
		return "terminal_share_expired"
	}
	return "terminal_share_invalidated"
}

func (a *API) closeInactiveTerminalShare(share store.TerminalShare, now time.Time, ip string) {
	if share.RevokedAt != nil {
		return
	}
	revoked, err := a.store.RevokeTerminalShare(share.UserID, share.ID, now)
	if err != nil {
		return
	}
	reason := terminalShareEndReason(share, now)
	a.finishTerminalShare(revoked, reason)
	_ = a.store.Audit(share.UserID, terminalShareEndEvent(share, now), "terminal_session", share.SessionID, ip, map[string]any{"shareID": share.ID, "reason": reason})
}

func (a *API) terminalShareAccess(r *http.Request, share store.TerminalShare) (store.TerminalShareAccess, bool) {
	cookie, err := r.Cookie(shareCookieName(share.ID))
	if err != nil || cookie.Value == "" {
		return store.TerminalShareAccess{}, false
	}
	access, err := a.store.TerminalShareAccess(security.TokenHash(cookie.Value), share.ID, time.Now().UTC())
	return access, err == nil
}

func (a *API) isTerminalShareOwner(r *http.Request, share store.TerminalShare) bool {
	user, _, err := a.currentAuthSession(r)
	return err == nil && !user.Disabled && !user.SessionLocked && user.ID == share.UserID
}

func shareCookieName(shareID string) string {
	return "velin_share_" + strings.ReplaceAll(shareID, "-", "")
}

func (a *API) terminalShareView(share store.TerminalShare, includeViewers bool) terminalShareView {
	viewers := a.shareViewerList(share.ID)
	view := terminalShareView{
		ID: share.ID, SessionID: share.SessionID, SessionName: share.SessionName,
		Permission: share.Permission, PasswordRequired: share.PasswordRequired,
		Record: share.Record, RecordingID: share.RecordingID, ExpiresAt: share.ExpiresAt,
		RevokedAt: share.RevokedAt, CreatedAt: share.CreatedAt,
		Active: terminalShareActive(share, time.Now()), ViewerCount: len(viewers),
	}
	if includeViewers {
		view.Viewers = viewers
	}
	return view
}

func (a *API) addShareViewer(shareID string, viewer *shareViewerConnection) {
	a.shareMu.Lock()
	if a.shareViewers == nil {
		a.shareViewers = make(map[string]map[string]*shareViewerConnection)
	}
	if a.shareViewers[shareID] == nil {
		a.shareViewers[shareID] = make(map[string]*shareViewerConnection)
	}
	a.shareViewers[shareID][viewer.ID] = viewer
	a.shareMu.Unlock()
}

func (a *API) removeShareViewer(shareID, viewerID string) {
	a.shareMu.Lock()
	delete(a.shareViewers[shareID], viewerID)
	if len(a.shareViewers[shareID]) == 0 {
		delete(a.shareViewers, shareID)
	}
	a.shareMu.Unlock()
}

func (a *API) shareViewerList(shareID string) []terminalShareViewer {
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	values := make([]terminalShareViewer, 0, len(a.shareViewers[shareID]))
	for _, viewer := range a.shareViewers[shareID] {
		values = append(values, terminalShareViewer{ID: viewer.ID, Name: viewer.Name, IP: viewer.IP, Permission: viewer.Permission, ConnectedAt: viewer.ConnectedAt})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ConnectedAt.Before(values[j].ConnectedAt) })
	return values
}

func (a *API) finishTerminalShare(share store.TerminalShare, reason string) {
	if share.RecordingID != "" && a.terminals != nil {
		a.terminals.StopRecordingIfID(share.UserID, share.SessionID, share.RecordingID)
	}
	a.shareMu.Lock()
	connections := make([]*shareViewerConnection, 0, len(a.shareViewers[share.ID]))
	for _, viewer := range a.shareViewers[share.ID] {
		connections = append(connections, viewer)
	}
	delete(a.shareViewers, share.ID)
	a.shareMu.Unlock()
	for _, viewer := range connections {
		viewer.close(4003, reason)
	}
}

func (a *API) expireTerminalShares() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		shares, err := a.store.TerminalSharesNeedingClosure(now.UTC())
		if err != nil {
			continue
		}
		for _, share := range shares {
			a.closeInactiveTerminalShare(share, now.UTC(), "")
		}
	}
}

func (a *API) shareFailureWait(key string) time.Duration {
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	value := a.shareFailures[key]
	if value.BlockedUntil.After(time.Now()) {
		return time.Until(value.BlockedUntil)
	}
	if !value.BlockedUntil.IsZero() {
		delete(a.shareFailures, key)
	}
	return 0
}

func (a *API) recordShareFailure(key string) {
	a.shareMu.Lock()
	if a.shareFailures == nil {
		a.shareFailures = make(map[string]shareFailure)
	}
	value := a.shareFailures[key]
	value.Count++
	if value.Count >= 5 {
		value.BlockedUntil = time.Now().Add(10 * time.Minute)
	}
	a.shareFailures[key] = value
	a.shareMu.Unlock()
}

func (a *API) clearShareFailure(key string) {
	a.shareMu.Lock()
	delete(a.shareFailures, key)
	a.shareMu.Unlock()
}
