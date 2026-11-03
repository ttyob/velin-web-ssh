package sessionbridge

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

var ErrUnauthorized = errors.New("invalid, disabled or expired agent token")
var ErrNotFound = errors.New("token not found")
var ErrInvalid = errors.New("invalid token configuration")

type Module struct {
	store   *store.Store
	backend Backend
	slots   chan struct{}
}

func New(s *store.Store, backend Backend) (*Module, error) {
	_, err := s.DB.Exec(`
CREATE TABLE IF NOT EXISTS session_bridge_tokens (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, hint TEXT NOT NULL,
 permissions TEXT NOT NULL, session_ids TEXT NOT NULL, host_ids TEXT NOT NULL,
 enabled INTEGER NOT NULL, expires_at TEXT, last_used_at TEXT, created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_bridge_user ON session_bridge_tokens(user_id);
CREATE TABLE IF NOT EXISTS session_bridge_receipts (
 token_id TEXT NOT NULL REFERENCES session_bridge_tokens(id) ON DELETE CASCADE,
 tool TEXT NOT NULL, request_id TEXT NOT NULL, digest TEXT NOT NULL,
 result TEXT, created_at TEXT NOT NULL, PRIMARY KEY(token_id,tool,request_id)
);`)
	if err != nil {
		return nil, fmt.Errorf("initialize session bridge: %w", err)
	}
	return &Module{store: s, backend: backend, slots: make(chan struct{}, 16)}, nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

const tokenColumns = `id,user_id,name,hint,permissions,session_ids,host_ids,enabled,expires_at,last_used_at,created_at`

type scanner interface{ Scan(...any) error }

func scanToken(row scanner) (Token, error) {
	var t Token
	var permissions, sessions, hosts, created string
	var expires, used sql.NullString
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Hint, &permissions, &sessions, &hosts, &t.Enabled, &expires, &used, &created)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(permissions), &t.Permissions); err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(sessions), &t.SessionIDs); err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(hosts), &t.HostIDs); err != nil {
		return t, err
	}
	t.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return t, err
	}
	for _, field := range []struct {
		raw sql.NullString
		dst **time.Time
	}{{expires, &t.ExpiresAt}, {used, &t.LastUsedAt}} {
		if field.raw.Valid {
			parsed, parseErr := time.Parse(time.RFC3339Nano, field.raw.String)
			if parseErr != nil {
				return t, parseErr
			}
			*field.dst = &parsed
		}
	}
	return t, nil
}

func unique(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (m *Module) validate(userID string, in TokenInput, previous *Token) (TokenInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 80 {
		return in, errors.New("令牌名称须为 1～80 个字符")
	}
	if len(in.SessionIDs) > 200 || len(in.HostIDs) > 200 || len(in.Permissions) > 4 {
		return in, errors.New("授权范围过大")
	}
	in.Permissions, in.SessionIDs, in.HostIDs = unique(in.Permissions), unique(in.SessionIDs), unique(in.HostIDs)
	if !contains(in.Permissions, Read) {
		return in, errors.New("须保留读取权限")
	}
	for _, permission := range in.Permissions {
		if !contains([]string{Read, Input, Create, Manage}, permission) {
			return in, errors.New("未知权限")
		}
	}
	if len(in.SessionIDs)+len(in.HostIDs) == 0 {
		return in, errors.New("请选择允许访问的会话或主机")
	}
	if contains(in.Permissions, Create) && len(in.HostIDs) == 0 {
		return in, errors.New("创建会话须授权至少一台主机")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) && (previous == nil || previous.ExpiresAt == nil || !in.ExpiresAt.Equal(*previous.ExpiresAt)) {
		return in, errors.New("过期时间须晚于当前时间")
	}
	if !in.Enabled {
		return in, nil
	}
	for _, id := range in.SessionIDs {
		if _, err := m.store.Terminal(userID, id); err != nil {
			return in, errors.New("选择的会话不存在或无权访问")
		}
	}
	for _, id := range in.HostIDs {
		host, err := m.store.Host(userID, id)
		if err != nil || host.Protocol != "" && host.Protocol != "ssh" {
			return in, errors.New("选择的 SSH 主机不存在或无权访问")
		}
	}
	return in, nil
}

func encodeStrings(values []string) string { raw, _ := json.Marshal(values); return string(raw) }
func timeValue(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (m *Module) CreateToken(userID string, in TokenInput) (Token, string, error) {
	in, err := m.validate(userID, in, nil)
	if err != nil {
		return Token{}, "", fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return Token{}, "", err
	}
	secret := "velin_mcp_" + base64.RawURLEncoding.EncodeToString(random[:])
	t := Token{ID: uuid.NewString(), UserID: userID, Name: in.Name, Hint: "…" + secret[len(secret)-6:], Permissions: in.Permissions, SessionIDs: in.SessionIDs, HostIDs: in.HostIDs, Enabled: in.Enabled, ExpiresAt: in.ExpiresAt, CreatedAt: time.Now().UTC()}
	_, err = m.store.DB.Exec(`INSERT INTO session_bridge_tokens(id,user_id,name,token_hash,hint,permissions,session_ids,host_ids,enabled,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, userID, t.Name, hashToken(secret), t.Hint, encodeStrings(t.Permissions), encodeStrings(t.SessionIDs), encodeStrings(t.HostIDs), t.Enabled, timeValue(t.ExpiresAt), t.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return Token{}, "", err
	}
	return t, secret, nil
}

func (m *Module) Tokens(userID string) ([]Token, error) {
	rows, err := m.store.DB.Query(`SELECT `+tokenColumns+` FROM session_bridge_tokens WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := make([]Token, 0)
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

func (m *Module) UpdateToken(userID, id string, in TokenInput) (Token, error) {
	previous, err := scanToken(m.store.DB.QueryRow(`SELECT `+tokenColumns+` FROM session_bridge_tokens WHERE id=? AND user_id=?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	if err != nil {
		return Token{}, err
	}
	in, err = m.validate(userID, in, &previous)
	if err != nil {
		return Token{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	result, err := m.store.DB.Exec(`UPDATE session_bridge_tokens SET name=?,permissions=?,session_ids=?,host_ids=?,enabled=?,expires_at=? WHERE id=? AND user_id=?`,
		in.Name, encodeStrings(in.Permissions), encodeStrings(in.SessionIDs), encodeStrings(in.HostIDs), in.Enabled, timeValue(in.ExpiresAt), id, userID)
	if err != nil {
		return Token{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return Token{}, ErrNotFound
	}
	return scanToken(m.store.DB.QueryRow(`SELECT `+tokenColumns+` FROM session_bridge_tokens WHERE id=? AND user_id=?`, id, userID))
}

func (m *Module) DeleteToken(userID, id string) error {
	result, err := m.store.DB.Exec(`DELETE FROM session_bridge_tokens WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

func (m *Module) Authenticate(secret string) (Token, error) {
	if !strings.HasPrefix(secret, "velin_mcp_") || len(secret) != 53 {
		return Token{}, ErrUnauthorized
	}
	t, err := scanToken(m.store.DB.QueryRow(`SELECT `+tokenColumns+` FROM session_bridge_tokens WHERE token_hash=?`, hashToken(secret)))
	if err != nil || !t.Enabled || t.ExpiresAt != nil && !time.Now().Before(*t.ExpiresAt) {
		return Token{}, ErrUnauthorized
	}
	u, err := m.store.UserByID(t.UserID)
	if err != nil || u.Disabled || u.ForcePasswordChange {
		return Token{}, ErrUnauthorized
	}
	now := time.Now().UTC()
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > time.Minute {
		_, _ = m.store.DB.Exec(`UPDATE session_bridge_tokens SET last_used_at=? WHERE id=?`, now.Format(time.RFC3339Nano), t.ID)
	}
	return t, nil
}
