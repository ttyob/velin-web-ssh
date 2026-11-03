package sessionbridge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (m *Module) session(ctx context.Context, token Token, id string) (Session, error) {
	sessions, err := m.backend.Sessions(ctx, token.UserID)
	if err != nil {
		return Session{}, errors.New("无法读取会话列表")
	}
	for _, session := range sessions {
		if session.ID == id && token.AllowsSession(session) {
			return session, nil
		}
	}
	return Session{}, errors.New("会话不存在或未授权")
}

func (m *Module) listSessions(ctx context.Context, token Token) ([]Session, error) {
	sessions, err := m.backend.Sessions(ctx, token.UserID)
	if err != nil {
		return nil, errors.New("无法读取会话列表")
	}
	result := make([]Session, 0)
	for _, session := range sessions {
		if token.AllowsSession(session) {
			result = append(result, session)
		}
	}
	return result, nil
}

func (m *Module) listHosts(ctx context.Context, token Token) ([]Host, error) {
	hosts, err := m.backend.Hosts(ctx, token.UserID)
	if err != nil {
		return nil, errors.New("无法读取主机列表")
	}
	result := make([]Host, 0)
	for _, host := range hosts {
		if contains(token.HostIDs, host.ID) {
			result = append(result, host)
		}
	}
	return result, nil
}

type readArgs struct {
	SessionID   string  `json:"sessionID" jsonschema:"ID returned by velin_list_sessions"`
	MaxLines    int     `json:"maxLines,omitempty" jsonschema:"Recent terminal lines, default 200, maximum 1000"`
	Cursor      *Cursor `json:"cursor,omitempty" jsonschema:"Cursor from the previous read, used to detect new output"`
	WaitSeconds int     `json:"waitSeconds,omitempty" jsonschema:"Wait up to 20 seconds for output after cursor; this does not limit the remote task"`
}

type readResult struct {
	Session      Session    `json:"session"`
	Text         string     `json:"text"`
	Cursor       Cursor     `json:"cursor"`
	Changed      bool       `json:"changed"`
	Truncated    bool       `json:"truncated"`
	Source       string     `json:"source"`
	LastOutputAt *time.Time `json:"lastOutputAt,omitempty"`
	TaskState    string     `json:"taskState"`
}

func (m *Module) readSession(ctx context.Context, token Token, args readArgs) (readResult, error) {
	if args.MaxLines == 0 {
		args.MaxLines = 200
	}
	if args.MaxLines < 1 || args.MaxLines > 1000 || args.WaitSeconds < 0 || args.WaitSeconds > 20 {
		return readResult{}, errors.New("maxLines 须为 1～1000，waitSeconds 须为 0～20")
	}
	if args.WaitSeconds > 0 && args.Cursor == nil {
		return readResult{}, errors.New("等待新输出需要提供上次读取的 cursor")
	}
	session, err := m.session(ctx, token, args.SessionID)
	if err != nil {
		return readResult{}, err
	}
	deadline := time.Now().Add(time.Duration(args.WaitSeconds) * time.Second)
	var snapshot Snapshot
	for {
		// A read waiting for output must respect revocation and scope changes too.
		if err = m.revalidate(token, Read, &session); err != nil {
			return readResult{}, err
		}
		snapshot, err = m.backend.Snapshot(ctx, token.UserID, session.ID, 0)
		if err != nil {
			return readResult{}, err
		}
		if args.Cursor == nil || snapshot.Cursor != *args.Cursor || !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return readResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	snapshot, err = m.backend.Snapshot(ctx, token.UserID, session.ID, args.MaxLines)
	if err != nil {
		return readResult{}, err
	}
	if err = m.revalidate(token, Read, &session); err != nil {
		return readResult{}, err
	}
	if current, lookupErr := m.session(ctx, token, session.ID); lookupErr == nil {
		session = current
	}
	text := ""
	lineLimited := false
	if snapshot.Capture != nil {
		text = tailText(*snapshot.Capture, args.MaxLines)
		lineLimited = strings.Count(strings.TrimRight(*snapshot.Capture, "\r\n"), "\n")+1 > args.MaxLines
	} else {
		text, lineLimited = renderText(snapshot.Raw, snapshot.Cols, snapshot.Rows, args.MaxLines)
	}
	text, bounded := boundText(text)
	result := readResult{Session: session, Text: text, Cursor: snapshot.Cursor, Changed: args.Cursor == nil || snapshot.Cursor != *args.Cursor,
		Truncated: snapshot.Truncated || bounded || lineLimited, Source: snapshot.Source, TaskState: "unknown"}
	if !snapshot.LastOutputAt.IsZero() {
		result.LastOutputAt = &snapshot.LastOutputAt
	}
	return result, nil
}

func (m *Module) revalidate(prior Token, permission string, session *Session) error {
	t, err := scanToken(m.store.DB.QueryRow(`SELECT `+tokenColumns+` FROM session_bridge_tokens WHERE id=? AND user_id=?`, prior.ID, prior.UserID))
	if err != nil || !t.Enabled || t.ExpiresAt != nil && !time.Now().Before(*t.ExpiresAt) {
		return ErrUnauthorized
	}
	u, err := m.store.UserByID(t.UserID)
	if err != nil || u.Disabled || u.ForcePasswordChange {
		return ErrUnauthorized
	}
	if !t.Allows(permission) || session != nil && !t.AllowsSession(*session) {
		return errors.New("令牌未授权此操作或会话")
	}
	return nil
}

type mutationResult struct {
	Status      string       `json:"status"`
	Bytes       int          `json:"bytes,omitempty"`
	Duplicate   bool         `json:"duplicate,omitempty"`
	Session     *Session     `json:"session,omitempty"`
	Termination *Termination `json:"termination,omitempty"`
	Message     string       `json:"message,omitempty"`
}

// mutation persists admission before touching a PTY. A crash cannot make a
// retried request execute twice. No input text or token is stored in receipts.
func (m *Module) mutation(ctx context.Context, token Token, tool, requestID string, payload any, effect func() mutationResult) (mutationResult, error) {
	if _, err := uuid.Parse(requestID); err != nil {
		return mutationResult{}, errors.New("requestID 必须是 UUID；重试同一操作时保留原编号")
	}
	raw, _ := json.Marshal(payload)
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	result, err := m.store.DB.ExecContext(ctx, `INSERT INTO session_bridge_receipts(token_id,tool,request_id,digest,created_at) VALUES(?,?,?,?,?) ON CONFLICT(token_id,tool,request_id) DO NOTHING`, token.ID, tool, requestID, hash, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return mutationResult{}, errors.New("无法记录操作，请稍后重试")
	}
	if count, _ := result.RowsAffected(); count == 0 {
		var existing string
		var receipt sql.NullString
		if err = m.store.DB.QueryRowContext(ctx, `SELECT digest,result FROM session_bridge_receipts WHERE token_id=? AND tool=? AND request_id=?`, token.ID, tool, requestID).Scan(&existing, &receipt); err != nil {
			return mutationResult{}, errors.New("无法读取操作记录")
		}
		if existing != hash {
			return mutationResult{}, errors.New("requestID 已用于不同参数，操作被拒绝")
		}
		cached := mutationResult{Status: "uncertain", Message: "先前操作仍在进行或其结果未确认。请先读取会话，勿自动生成新编号补发。"}
		if receipt.Valid {
			if err = json.Unmarshal([]byte(receipt.String), &cached); err != nil {
				return mutationResult{}, err
			}
		}
		cached.Duplicate = true
		return cached, nil
	}
	if err := ctx.Err(); err != nil {
		return mutationResult{}, err
	}
	value := effect()
	encoded, err := json.Marshal(value)
	if err != nil {
		return mutationResult{}, err
	}
	// Persist even if the client disconnected after the effect completed.
	if _, err = m.store.DB.Exec(`UPDATE session_bridge_receipts SET result=? WHERE token_id=? AND tool=? AND request_id=?`, string(encoded), token.ID, tool, requestID); err != nil {
		return mutationResult{Status: "uncertain", Bytes: value.Bytes, Message: "操作结果未持久化。请读取会话确认，勿自动补发。"}, nil
	}
	var target struct {
		SessionID string `json:"sessionID"`
		HostID    string `json:"hostID"`
	}
	_ = json.Unmarshal(raw, &target)
	_ = m.store.Audit(token.UserID, "session_bridge."+tool, "session_bridge_token", token.ID, "", map[string]any{"requestID": requestID, "sessionID": target.SessionID, "hostID": target.HostID, "status": value.Status, "bytes": value.Bytes})
	return value, nil
}

type inputArgs struct {
	SessionID string `json:"sessionID"`
	RequestID string `json:"requestID" jsonschema:"Unique UUID for this input. Reuse exactly for retries; never retry uncertain results with a new ID."`
	Text      string `json:"text,omitempty" jsonschema:"Text to type or paste. Does not submit unless submit is true."`
	Submit    bool   `json:"submit,omitempty" jsonschema:"Append Enter after text"`
	Key       string `json:"key,omitempty" jsonschema:"One named key instead of text: Enter, Ctrl+C, Ctrl+D, Tab, Escape, Up, Down, Left, Right, Backspace"`
}

func inputBytes(args inputArgs) ([]byte, error) {
	if args.Key != "" {
		if args.Text != "" || args.Submit {
			return nil, errors.New("key 与 text/submit 不能同时使用")
		}
		keys := map[string]string{"Enter": "\r", "Ctrl+C": "\x03", "Ctrl+D": "\x04", "Tab": "\t", "Escape": "\x1b", "Up": "\x1b[A", "Down": "\x1b[B", "Right": "\x1b[C", "Left": "\x1b[D", "Backspace": "\x7f"}
		if value, ok := keys[args.Key]; ok {
			return []byte(value), nil
		}
		return nil, errors.New("不支持的按键")
	}
	if len(args.Text) > 48<<10 || strings.ContainsRune(args.Text, 0) {
		return nil, errors.New("输入须少于 48 KiB，且不能包含 NUL")
	}
	text := args.Text
	for _, r := range text {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' || r == 127 {
			return nil, errors.New("text 不能包含终端控制字符；请通过 key 参数发送按键")
		}
	}
	// Multiline data is pasted atomically instead of submitting each line.
	if strings.ContainsAny(text, "\r\n") {
		text = "\x1b[200~" + strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n") + "\x1b[201~"
	}
	if args.Submit {
		text += "\r"
	}
	if text == "" {
		return nil, errors.New("请提供 text、submit 或 key")
	}
	return []byte(text), nil
}

func failed(err error) mutationResult {
	return mutationResult{Status: "failed", Message: fmt.Sprint(err)}
}
