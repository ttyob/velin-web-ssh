package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"velin-webssh/internal/security"
)

type Store struct{ DB *sql.DB }

type User struct {
	ID                  string    `json:"id"`
	Username            string    `json:"username"`
	Role                string    `json:"role"`
	Disabled            bool      `json:"disabled"`
	ForcePasswordChange bool      `json:"forcePasswordChange"`
	SessionLocked       bool      `json:"sessionLocked,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
}
type Host struct {
	ID                string     `json:"id"`
	UserID            string     `json:"userID"`
	Name              string     `json:"name"`
	Address           string     `json:"address"`
	Protocol          string     `json:"protocol"`
	Username          string     `json:"username"`
	GroupName         string     `json:"groupName"`
	SortOrder         int        `json:"sortOrder"`
	Tags              string     `json:"tags"`
	Notes             string     `json:"notes"`
	CredentialID      string     `json:"credentialID"`
	PasswordEnc       string     `json:"-"`
	HasPassword       bool       `json:"hasPassword"`
	Port              int        `json:"port"`
	InitialDir        string     `json:"initialDirectory"`
	ConnectTimeout    int        `json:"connectTimeout"`
	KeepaliveInterval int        `json:"keepaliveInterval"`
	MaxRetries        int        `json:"maxRetries"`
	TerminalType      string     `json:"terminalType"`
	SessionMode       string     `json:"sessionMode"`
	JumpHostID        string     `json:"jumpHostID"`
	RDPMode           string     `json:"rdpMode"`
	RDPQuality        string     `json:"rdpQuality"`
	RDPClipboard      bool       `json:"rdpClipboard"`
	RDPAudio          bool       `json:"rdpAudio"`
	RDPDrive          bool       `json:"rdpDrive"`
	RDPPrinting       bool       `json:"rdpPrinting"`
	RDPMultiMonitor   bool       `json:"rdpMultiMonitor"`
	DesktopDomain     string     `json:"desktopDomain"`
	DesktopSecurity   string     `json:"desktopSecurity"`
	IgnoreCertificate bool       `json:"ignoreCertificate"`
	DesktopReadOnly   bool       `json:"desktopReadOnly"`
	Platform          string     `json:"platform"`
	Distribution      string     `json:"distribution"`
	LastStatus        string     `json:"lastStatus"`
	LastLatency       int        `json:"lastLatencyMs"`
	LastConnectedAt   *time.Time `json:"lastConnectedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}
type Credential struct {
	ID         string    `json:"id"`
	UserID     string    `json:"userID"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Secret     string    `json:"-"`
	Passphrase string    `json:"-"`
	CreatedAt  time.Time `json:"createdAt"`
}
type TerminalSession struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userID"`
	HostID       string    `json:"hostID"`
	CredentialID string    `json:"credentialID"`
	Name         string    `json:"name"`
	RemoteUser   string    `json:"remoteUser"`
	SessionMode  string    `json:"sessionMode"`
	TmuxSocket   string    `json:"tmuxSocket"`
	TmuxName     string    `json:"tmuxName"`
	OwnerMarker  string    `json:"-"`
	Status       string    `json:"status"`
	LastError    string    `json:"lastError"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
type Snippet struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userID"`
	Name        string    `json:"name"`
	GroupName   string    `json:"groupName"`
	Tags        string    `json:"tags"`
	Command     string    `json:"command"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type PortForward struct {
	ID            string    `json:"id"`
	UserID        string    `json:"userID"`
	HostID        string    `json:"hostID"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	ListenAddress string    `json:"listenAddress"`
	ListenPort    int       `json:"listenPort"`
	TargetHost    string    `json:"targetHost"`
	TargetPort    int       `json:"targetPort"`
	Status        string    `json:"status"`
	LastError     string    `json:"lastError"`
	BytesIn       int64     `json:"bytesIn"`
	BytesOut      int64     `json:"bytesOut"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
type WebService struct {
	ID            string    `json:"id"`
	UserID        string    `json:"userID"`
	HostID        string    `json:"hostID"`
	Name          string    `json:"name"`
	ProxyMode     string    `json:"proxyMode"`
	PageMode      string    `json:"pageMode"`
	ListenPort    int       `json:"listenPort"`
	TargetURL     string    `json:"targetURL"`
	UpstreamHost  string    `json:"upstreamHost"`
	SkipTLSVerify bool      `json:"skipTLSVerify"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type CommandTask struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userID"`
	Command    string     `json:"command"`
	SessionIDs []string   `json:"sessionIDs"`
	Status     string     `json:"status"`
	Output     string     `json:"output,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type TerminalRecording struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userID"`
	SessionID   string     `json:"sessionID"`
	SessionName string     `json:"sessionName"`
	Path        string     `json:"-"`
	Status      string     `json:"status"`
	Bytes       int64      `json:"bytes"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

type TerminalShare struct {
	ID               string     `json:"id"`
	UserID           string     `json:"-"`
	SessionID        string     `json:"sessionID"`
	SessionName      string     `json:"sessionName"`
	TokenHash        string     `json:"-"`
	TokenEnc         string     `json:"-"`
	PasswordHash     string     `json:"-"`
	PasswordRequired bool       `json:"passwordRequired"`
	Permission       string     `json:"permission"`
	Record           bool       `json:"record"`
	RecordingID      string     `json:"recordingID,omitempty"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	RevokedAt        *time.Time `json:"revokedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	SessionStatus    string     `json:"-"`
	OwnerDisabled    bool       `json:"-"`
}

type TerminalShareAccess struct {
	TokenHash   string
	ShareID     string
	DisplayName string
	IP          string
	UserAgent   string
	ExpiresAt   time.Time
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		return nil, err
	}
	s := &Store{DB: db}
	return s, s.migrate()
}

func (s *Store) migrate() error {
	_, err := s.DB.Exec(`
CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user', disabled INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS auth_sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, token_hash TEXT NOT NULL UNIQUE, user_agent TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '', expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_auth_token ON auth_sessions(token_hash);
CREATE TABLE IF NOT EXISTS credentials (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, kind TEXT NOT NULL, secret_enc TEXT NOT NULL, passphrase_enc TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS hosts (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, address TEXT NOT NULL, port INTEGER NOT NULL DEFAULT 22, username TEXT NOT NULL, protocol TEXT NOT NULL DEFAULT 'ssh', rdp_mode TEXT NOT NULL DEFAULT 'web', rdp_quality TEXT NOT NULL DEFAULT 'crisp', rdp_clipboard INTEGER NOT NULL DEFAULT 1, rdp_audio INTEGER NOT NULL DEFAULT 0, rdp_drive INTEGER NOT NULL DEFAULT 0, rdp_printing INTEGER NOT NULL DEFAULT 0, rdp_multi_monitor INTEGER NOT NULL DEFAULT 0, credential_id TEXT REFERENCES credentials(id) ON DELETE SET NULL, password_enc TEXT NOT NULL DEFAULT '', group_name TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0, tags TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '', favorite INTEGER NOT NULL DEFAULT 0, initial_directory TEXT NOT NULL DEFAULT '', connect_timeout INTEGER NOT NULL DEFAULT 12, keepalive_interval INTEGER NOT NULL DEFAULT 30, max_retries INTEGER NOT NULL DEFAULT 5, terminal_type TEXT NOT NULL DEFAULT 'xterm-256color', session_mode TEXT NOT NULL DEFAULT 'tmux', jump_host_id TEXT NOT NULL DEFAULT '', desktop_domain TEXT NOT NULL DEFAULT '', desktop_security TEXT NOT NULL DEFAULT 'any', desktop_ignore_certificate INTEGER NOT NULL DEFAULT 0, desktop_read_only INTEGER NOT NULL DEFAULT 0, platform TEXT NOT NULL DEFAULT '', distribution TEXT NOT NULL DEFAULT '', last_status TEXT NOT NULL DEFAULT '', last_latency_ms INTEGER NOT NULL DEFAULT 0, last_connected_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_hosts_user ON hosts(user_id, name);
CREATE TABLE IF NOT EXISTS known_host_keys (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL, address TEXT NOT NULL, port INTEGER NOT NULL, fingerprint TEXT NOT NULL, public_key TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE(user_id,address,port));
CREATE TABLE IF NOT EXISTS terminal_sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, host_id TEXT NOT NULL, credential_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, remote_user TEXT NOT NULL, session_mode TEXT NOT NULL DEFAULT 'tmux', tmux_socket TEXT NOT NULL, tmux_name TEXT NOT NULL, owner_marker TEXT NOT NULL, status TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_terminal_user ON terminal_sessions(user_id, updated_at DESC);
CREATE TABLE IF NOT EXISTS workspaces (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, layout_json TEXT NOT NULL DEFAULT '{}', version INTEGER NOT NULL DEFAULT 1, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS user_preferences (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, preferences_json TEXT NOT NULL DEFAULT '{}', updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE TABLE IF NOT EXISTS audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL DEFAULT '', event_type TEXT NOT NULL, resource_type TEXT NOT NULL DEFAULT '', resource_id TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '', details TEXT NOT NULL DEFAULT '{}', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE TABLE IF NOT EXISTS login_attempts (identity TEXT NOT NULL, ip TEXT NOT NULL, failures INTEGER NOT NULL DEFAULT 0, locked_until DATETIME, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(identity,ip));
	CREATE TABLE IF NOT EXISTS system_settings (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE TABLE IF NOT EXISTS snippets (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, group_name TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '', command TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE INDEX IF NOT EXISTS idx_snippets_user ON snippets(user_id,group_name,name);
	CREATE TABLE IF NOT EXISTS notifications (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, session_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'info', read INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id,read,created_at DESC);
	CREATE TABLE IF NOT EXISTS port_forwards (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, host_id TEXT NOT NULL, name TEXT NOT NULL, kind TEXT NOT NULL, listen_address TEXT NOT NULL DEFAULT '127.0.0.1', listen_port INTEGER NOT NULL, target_host TEXT NOT NULL DEFAULT '', target_port INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'stopped', last_error TEXT NOT NULL DEFAULT '', bytes_in INTEGER NOT NULL DEFAULT 0, bytes_out INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE INDEX IF NOT EXISTS idx_forwards_user ON port_forwards(user_id,updated_at DESC);
	CREATE TABLE IF NOT EXISTS user_totp (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, secret_enc TEXT NOT NULL, recovery_hashes TEXT NOT NULL DEFAULT '[]', enabled INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE TABLE IF NOT EXISTS web_services (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE, name TEXT NOT NULL, proxy_mode TEXT NOT NULL DEFAULT 'path', page_mode TEXT NOT NULL DEFAULT 'html', listen_port INTEGER NOT NULL DEFAULT 0, target_url TEXT NOT NULL, upstream_host TEXT NOT NULL DEFAULT '', skip_tls_verify INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE INDEX IF NOT EXISTS idx_web_services_user ON web_services(user_id,name);
	CREATE TABLE IF NOT EXISTS host_monitor_policies (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE, enabled INTEGER NOT NULL DEFAULT 0, interval_seconds INTEGER NOT NULL DEFAULT 30, cpu_threshold REAL NOT NULL DEFAULT 90, memory_threshold REAL NOT NULL DEFAULT 90, disk_threshold REAL NOT NULL DEFAULT 90, cooldown_seconds INTEGER NOT NULL DEFAULT 900, last_alert_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE(user_id,host_id));
	CREATE INDEX IF NOT EXISTS idx_monitor_policy_user ON host_monitor_policies(user_id,host_id);
	CREATE TABLE IF NOT EXISTS command_tasks (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, command TEXT NOT NULL, session_ids TEXT NOT NULL DEFAULT '[]', status TEXT NOT NULL DEFAULT 'queued', output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, started_at DATETIME, finished_at DATETIME);
	CREATE INDEX IF NOT EXISTS idx_command_tasks_user ON command_tasks(user_id,created_at DESC);
	CREATE TABLE IF NOT EXISTS terminal_recordings (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, session_id TEXT NOT NULL, session_name TEXT NOT NULL DEFAULT '', path TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'recording', bytes INTEGER NOT NULL DEFAULT 0, started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, finished_at DATETIME);
	CREATE INDEX IF NOT EXISTS idx_recordings_user ON terminal_recordings(user_id,started_at DESC);
	CREATE TABLE IF NOT EXISTS terminal_shares (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, session_id TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, token_enc TEXT NOT NULL, password_hash TEXT NOT NULL DEFAULT '', permission TEXT NOT NULL DEFAULT 'view', record INTEGER NOT NULL DEFAULT 0, recording_id TEXT NOT NULL DEFAULT '', expires_at DATETIME NOT NULL, revoked_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE INDEX IF NOT EXISTS idx_terminal_shares_owner ON terminal_shares(user_id,session_id,created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_terminal_shares_expiry ON terminal_shares(expires_at,revoked_at);
	CREATE TABLE IF NOT EXISTS terminal_share_access (token_hash TEXT PRIMARY KEY, share_id TEXT NOT NULL REFERENCES terminal_shares(id) ON DELETE CASCADE, display_name TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '', user_agent TEXT NOT NULL DEFAULT '', expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE INDEX IF NOT EXISTS idx_terminal_share_access_share ON terminal_share_access(share_id,last_seen_at DESC);
	CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
`)
	if err != nil {
		return err
	}
	columns := []struct{ name, definition string }{
		{"initial_directory", "TEXT NOT NULL DEFAULT ''"}, {"connect_timeout", "INTEGER NOT NULL DEFAULT 12"},
		{"keepalive_interval", "INTEGER NOT NULL DEFAULT 30"}, {"max_retries", "INTEGER NOT NULL DEFAULT 5"},
		{"terminal_type", "TEXT NOT NULL DEFAULT 'xterm-256color'"}, {"password_enc", "TEXT NOT NULL DEFAULT ''"}, {"sort_order", "INTEGER NOT NULL DEFAULT 0"}, {"last_status", "TEXT NOT NULL DEFAULT ''"},
		{"session_mode", "TEXT NOT NULL DEFAULT 'tmux'"},
		{"jump_host_id", "TEXT NOT NULL DEFAULT ''"},
		{"protocol", "TEXT NOT NULL DEFAULT 'ssh'"},
		{"rdp_mode", "TEXT NOT NULL DEFAULT 'web'"},
		{"rdp_quality", "TEXT NOT NULL DEFAULT 'crisp'"}, {"rdp_clipboard", "INTEGER NOT NULL DEFAULT 1"},
		{"rdp_audio", "INTEGER NOT NULL DEFAULT 0"}, {"rdp_drive", "INTEGER NOT NULL DEFAULT 0"},
		{"rdp_printing", "INTEGER NOT NULL DEFAULT 0"}, {"rdp_multi_monitor", "INTEGER NOT NULL DEFAULT 0"},
		{"desktop_domain", "TEXT NOT NULL DEFAULT ''"},
		{"desktop_security", "TEXT NOT NULL DEFAULT 'any'"},
		{"desktop_ignore_certificate", "INTEGER NOT NULL DEFAULT 0"},
		{"desktop_read_only", "INTEGER NOT NULL DEFAULT 0"},
		{"platform", "TEXT NOT NULL DEFAULT ''"},
		{"distribution", "TEXT NOT NULL DEFAULT ''"},
		{"last_latency_ms", "INTEGER NOT NULL DEFAULT 0"}, {"last_connected_at", "DATETIME"},
	}
	for _, column := range columns {
		if err = s.ensureColumn("hosts", column.name, column.definition); err != nil {
			return err
		}
	}
	if err = s.ensureColumn("users", "force_password_change", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err = s.ensureColumn("auth_sessions", "locked", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err = s.ensureColumn("auth_sessions", "unlock_failures", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err = s.ensureColumn("auth_sessions", "unlock_blocked_until", "DATETIME"); err != nil {
		return err
	}
	if err = s.ensureColumn("users", "lock_pin_hash", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err = s.DB.Exec(`UPDATE auth_sessions SET locked=0,unlock_failures=0,unlock_blocked_until=NULL WHERE locked=1 AND user_id IN (SELECT id FROM users WHERE lock_pin_hash='')`); err != nil {
		return err
	}
	if err = s.ensureColumn("web_services", "proxy_mode", "TEXT NOT NULL DEFAULT 'path'"); err != nil {
		return err
	}
	if err = s.ensureColumn("web_services", "listen_port", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err = s.ensureColumn("web_services", "page_mode", "TEXT NOT NULL DEFAULT 'html'"); err != nil {
		return err
	}
	if err = s.ensureColumn("terminal_sessions", "session_mode", "TEXT NOT NULL DEFAULT 'tmux'"); err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES(1),(2),(3),(4),(5),(6),(7),(8),(9),(10),(11),(12),(13),(14),(15),(16); PRAGMA user_version=16;`)
	return err
}

func (s *Store) ensureColumn(table, name, definition string) error {
	rows, err := s.DB.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var column, kind string
		var notNull, pk int
		var defaultValue any
		if err = rows.Scan(&cid, &column, &kind, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if column == name {
			found = true
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.DB.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + definition)
	return err
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) CreateUser(id, username, hash, role string) error {
	_, err := s.DB.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES(?,?,?,?)`, id, username, hash, role)
	return err
}
func (s *Store) SetForcePasswordChange(userID string, force bool) error {
	result, err := s.DB.Exec(`UPDATE users SET force_password_change=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, force, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) UserCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}
func (s *Store) UserByUsername(username string) (User, string, error) {
	var u User
	var hash string
	err := s.DB.QueryRow(`SELECT id,username,password_hash,role,disabled,force_password_change,created_at FROM users WHERE username=?`, username).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.Disabled, &u.ForcePasswordChange, &u.CreatedAt)
	return u, hash, err
}
func (s *Store) UserByID(id string) (User, error) {
	var u User
	err := s.DB.QueryRow(`SELECT id,username,role,disabled,force_password_change,created_at FROM users WHERE id=?`, id).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.ForcePasswordChange, &u.CreatedAt)
	return u, err
}
func (s *Store) CreateAuthSession(id, userID, tokenHash, agent, ip string, expires time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO auth_sessions(id,user_id,token_hash,user_agent,ip,expires_at) VALUES(?,?,?,?,?,?)`, id, userID, tokenHash, agent, ip, expires)
	return err
}
func (s *Store) UserByToken(hash string) (User, error) {
	var u User
	err := s.DB.QueryRow(`SELECT u.id,u.username,u.role,u.disabled,u.force_password_change,u.created_at FROM auth_sessions a JOIN users u ON u.id=a.user_id WHERE a.token_hash=? AND a.expires_at>CURRENT_TIMESTAMP`, hash).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.ForcePasswordChange, &u.CreatedAt)
	if err == nil {
		_, _ = s.DB.Exec(`UPDATE auth_sessions SET last_seen_at=CURRENT_TIMESTAMP WHERE token_hash=?`, hash)
	}
	return u, err
}
func (s *Store) UserByTokenState(hash string) (User, bool, error) {
	var u User
	var locked bool
	err := s.DB.QueryRow(`SELECT u.id,u.username,u.role,u.disabled,u.force_password_change,u.created_at,a.locked FROM auth_sessions a JOIN users u ON u.id=a.user_id WHERE a.token_hash=? AND a.expires_at>CURRENT_TIMESTAMP`, hash).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.ForcePasswordChange, &u.CreatedAt, &locked)
	return u, locked, err
}
func (s *Store) DeleteAuthSession(hash string) error {
	_, err := s.DB.Exec(`DELETE FROM auth_sessions WHERE token_hash=?`, hash)
	return err
}
func (s *Store) AuthSessionLocked(hash string) (bool, error) {
	var locked bool
	err := s.DB.QueryRow(`SELECT locked FROM auth_sessions WHERE token_hash=? AND expires_at>CURRENT_TIMESTAMP`, hash).Scan(&locked)
	return locked, err
}
func (s *Store) SetAuthSessionLocked(hash string, locked bool) error {
	result, err := s.DB.Exec(`UPDATE auth_sessions SET locked=?,unlock_failures=0,unlock_blocked_until=NULL WHERE token_hash=? AND expires_at>CURRENT_TIMESTAMP`, locked, hash)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) UserLockPINHash(userID string) (string, error) {
	var hash string
	err := s.DB.QueryRow(`SELECT lock_pin_hash FROM users WHERE id=?`, userID).Scan(&hash)
	return hash, err
}
func (s *Store) SetUserLockPIN(userID, hash string) error {
	result, err := s.DB.Exec(`UPDATE users SET lock_pin_hash=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, hash, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) DisableUserLockPIN(userID string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE users SET lock_pin_hash='',updated_at=CURRENT_TIMESTAMP WHERE id=?`, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE auth_sessions SET locked=0,unlock_failures=0,unlock_blocked_until=NULL WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) AuthSessionUnlockBlockedUntil(hash string) (time.Time, error) {
	var blocked sql.NullTime
	err := s.DB.QueryRow(`SELECT unlock_blocked_until FROM auth_sessions WHERE token_hash=? AND expires_at>CURRENT_TIMESTAMP`, hash).Scan(&blocked)
	if err != nil || !blocked.Valid {
		return time.Time{}, err
	}
	return blocked.Time, nil
}
func (s *Store) RecordAuthSessionUnlockFailure(hash string) error {
	_, err := s.DB.Exec(`UPDATE auth_sessions SET
		unlock_failures=CASE WHEN unlock_blocked_until IS NOT NULL AND unlock_blocked_until<CURRENT_TIMESTAMP THEN 1 ELSE unlock_failures+1 END,
		unlock_blocked_until=CASE
			WHEN unlock_blocked_until IS NOT NULL AND unlock_blocked_until<CURRENT_TIMESTAMP THEN NULL
			WHEN unlock_failures+1>=5 THEN datetime('now','+30 seconds')
			ELSE unlock_blocked_until END
		WHERE token_hash=? AND expires_at>CURRENT_TIMESTAMP`, hash)
	return err
}
func (s *Store) ChangePassword(userID, currentPassword, newPassword, currentTokenHash string) error {
	var hash string
	if err := s.DB.QueryRow(`SELECT password_hash FROM users WHERE id=?`, userID).Scan(&hash); err != nil {
		return err
	}
	if !security.VerifyPassword(hash, currentPassword) {
		return errors.New("current password is incorrect")
	}
	newHash, err := security.HashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE users SET password_hash=?,force_password_change=0,updated_at=CURRENT_TIMESTAMP WHERE id=?`, newHash, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM auth_sessions WHERE user_id=? AND token_hash<>?`, userID, currentTokenHash); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

func (s *Store) ResetUserPassword(userID, newHash string, forceChange bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE users SET password_hash=?,force_password_change=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, newHash, forceChange, userID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return sql.ErrNoRows
	}
	if _, err = tx.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM login_attempts WHERE identity=(SELECT lower(trim(username)) FROM users WHERE id=?)`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LoginLock(identity, ip string) (time.Time, error) {
	keys := loginRateLimitKeys(identity, ip)
	rows, err := s.DB.Query(`SELECT locked_until FROM login_attempts WHERE (identity=? AND ip=?) OR (identity=? AND ip=?) OR (identity=? AND ip=?)`,
		keys[0].identity, keys[0].ip, keys[1].identity, keys[1].ip, keys[2].identity, keys[2].ip)
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	var latest time.Time
	for rows.Next() {
		var locked sql.NullTime
		if err = rows.Scan(&locked); err != nil {
			return time.Time{}, err
		}
		if locked.Valid && locked.Time.After(latest) {
			latest = locked.Time
		}
	}
	return latest, rows.Err()
}
func (s *Store) LoginFailureCount(identity, ip string) (int, error) {
	keys := loginRateLimitKeys(identity, ip)
	rows, err := s.DB.Query(`SELECT failures FROM login_attempts WHERE (identity=? AND ip=?) OR (identity=? AND ip=?) OR (identity=? AND ip=?)`,
		keys[0].identity, keys[0].ip, keys[1].identity, keys[1].ip, keys[2].identity, keys[2].ip)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	maxFailures := 0
	for rows.Next() {
		var failures int
		if err = rows.Scan(&failures); err != nil {
			return 0, err
		}
		if failures > maxFailures {
			maxFailures = failures
		}
	}
	return maxFailures, rows.Err()
}
func (s *Store) RecordLoginFailure(identity, ip string, threshold, lockMinutes int) error {
	keys := loginRateLimitKeys(identity, ip)
	thresholds := []int{threshold, maxInt(threshold*2, 10), maxInt(threshold*4, 20)}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for index, key := range keys {
		if _, err = tx.Exec(`INSERT INTO login_attempts(identity,ip,failures,locked_until) VALUES(?,?,1,NULL)
			ON CONFLICT(identity,ip) DO UPDATE SET failures=CASE WHEN locked_until IS NOT NULL AND locked_until<CURRENT_TIMESTAMP THEN 1 ELSE failures+1 END,
			locked_until=CASE WHEN (CASE WHEN locked_until IS NOT NULL AND locked_until<CURRENT_TIMESTAMP THEN 1 ELSE failures+1 END)>=? THEN datetime('now',?) ELSE locked_until END,updated_at=CURRENT_TIMESTAMP`, key.identity, key.ip, thresholds[index], fmt.Sprintf("+%d minutes", lockMinutes)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) ClearLoginFailures(identity, ip string) error {
	keys := loginRateLimitKeys(identity, ip)
	_, err := s.DB.Exec(`DELETE FROM login_attempts WHERE (identity=? AND ip=?) OR (identity=? AND ip=?) OR (identity=? AND ip=?)`,
		keys[0].identity, keys[0].ip, keys[1].identity, keys[1].ip, keys[2].identity, keys[2].ip)
	return err
}

type loginRateLimitKey struct{ identity, ip string }

func loginRateLimitKeys(identity, ip string) [3]loginRateLimitKey {
	identity = strings.ToLower(strings.TrimSpace(identity))
	return [3]loginRateLimitKey{
		{identity: identity, ip: ip},
		{identity: identity, ip: "*"},
		{identity: "*", ip: ip},
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (s *Store) SystemSetting(key string, dst any) error {
	var raw string
	err := s.DB.QueryRow(`SELECT value_json FROM system_settings WHERE key=?`, key).Scan(&raw)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), dst)
}
func (s *Store) SaveSystemSetting(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO system_settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=CURRENT_TIMESTAMP`, key, string(raw))
	return err
}

const hostCols = `id,user_id,name,address,port,username,COALESCE(protocol,'ssh'),COALESCE(rdp_mode,'web'),COALESCE(rdp_quality,'crisp'),COALESCE(rdp_clipboard,1),COALESCE(rdp_audio,0),COALESCE(rdp_drive,0),COALESCE(rdp_printing,0),COALESCE(rdp_multi_monitor,0),COALESCE(credential_id,''),COALESCE(password_enc,''),group_name,COALESCE(sort_order,0),tags,notes,initial_directory,connect_timeout,keepalive_interval,max_retries,terminal_type,session_mode,jump_host_id,COALESCE(desktop_domain,''),COALESCE(desktop_security,'any'),COALESCE(desktop_ignore_certificate,0),COALESCE(desktop_read_only,0),platform,distribution,last_status,last_latency_ms,last_connected_at,created_at,updated_at`

func scanHost(row interface{ Scan(...any) error }) (Host, error) {
	var h Host
	var connected sql.NullTime
	err := row.Scan(&h.ID, &h.UserID, &h.Name, &h.Address, &h.Port, &h.Username, &h.Protocol, &h.RDPMode, &h.RDPQuality, &h.RDPClipboard, &h.RDPAudio, &h.RDPDrive, &h.RDPPrinting, &h.RDPMultiMonitor, &h.CredentialID, &h.PasswordEnc, &h.GroupName, &h.SortOrder, &h.Tags, &h.Notes, &h.InitialDir, &h.ConnectTimeout, &h.KeepaliveInterval, &h.MaxRetries, &h.TerminalType, &h.SessionMode, &h.JumpHostID, &h.DesktopDomain, &h.DesktopSecurity, &h.IgnoreCertificate, &h.DesktopReadOnly, &h.Platform, &h.Distribution, &h.LastStatus, &h.LastLatency, &connected, &h.CreatedAt, &h.UpdatedAt)
	h.HasPassword = h.PasswordEnc != ""
	if connected.Valid {
		h.LastConnectedAt = &connected.Time
	}
	return h, err
}
func (s *Store) Hosts(userID string) ([]Host, error) {
	rows, err := s.DB.Query(`SELECT `+hostCols+` FROM hosts WHERE user_id=? ORDER BY sort_order,name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
func (s *Store) Host(userID, id string) (Host, error) {
	return scanHost(s.DB.QueryRow(`SELECT `+hostCols+` FROM hosts WHERE user_id=? AND id=?`, userID, id))
}
func (s *Store) SaveHost(h Host) error {
	if h.Protocol == "" {
		h.Protocol = "ssh"
	}
	if h.SessionMode == "" {
		h.SessionMode = "tmux"
	}
	if h.DesktopSecurity == "" {
		h.DesktopSecurity = "any"
	}
	if h.RDPMode == "" {
		h.RDPMode = "web"
	}
	if h.RDPQuality == "" {
		h.RDPQuality = "crisp"
	}
	_, err := s.DB.Exec(`INSERT INTO hosts(id,user_id,name,address,port,username,protocol,rdp_mode,rdp_quality,rdp_clipboard,rdp_audio,rdp_drive,rdp_printing,rdp_multi_monitor,credential_id,password_enc,group_name,sort_order,tags,notes,initial_directory,connect_timeout,keepalive_interval,max_retries,terminal_type,session_mode,jump_host_id,desktop_domain,desktop_security,desktop_ignore_certificate,desktop_read_only) VALUES(?,?,?,?,?,?,?, ?,?,?,?,?,?, ?,NULLIF(?,''),?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,address=excluded.address,port=excluded.port,username=excluded.username,protocol=excluded.protocol,rdp_mode=excluded.rdp_mode,rdp_quality=excluded.rdp_quality,rdp_clipboard=excluded.rdp_clipboard,rdp_audio=excluded.rdp_audio,rdp_drive=excluded.rdp_drive,rdp_printing=excluded.rdp_printing,rdp_multi_monitor=excluded.rdp_multi_monitor,credential_id=excluded.credential_id,password_enc=excluded.password_enc,group_name=excluded.group_name,sort_order=excluded.sort_order,tags=excluded.tags,notes=excluded.notes,initial_directory=excluded.initial_directory,connect_timeout=excluded.connect_timeout,keepalive_interval=excluded.keepalive_interval,max_retries=excluded.max_retries,terminal_type=excluded.terminal_type,session_mode=excluded.session_mode,jump_host_id=excluded.jump_host_id,desktop_domain=excluded.desktop_domain,desktop_security=excluded.desktop_security,desktop_ignore_certificate=excluded.desktop_ignore_certificate,desktop_read_only=excluded.desktop_read_only,updated_at=CURRENT_TIMESTAMP WHERE user_id=excluded.user_id`, h.ID, h.UserID, h.Name, h.Address, h.Port, h.Username, h.Protocol, h.RDPMode, h.RDPQuality, h.RDPClipboard, h.RDPAudio, h.RDPDrive, h.RDPPrinting, h.RDPMultiMonitor, h.CredentialID, h.PasswordEnc, h.GroupName, h.SortOrder, h.Tags, h.Notes, h.InitialDir, h.ConnectTimeout, h.KeepaliveInterval, h.MaxRetries, h.TerminalType, h.SessionMode, h.JumpHostID, h.DesktopDomain, h.DesktopSecurity, h.IgnoreCertificate, h.DesktopReadOnly)
	return err
}

type HostOrder struct {
	ID        string
	GroupName string
	SortOrder int
}

func (s *Store) ReorderHosts(userID string, items []HostOrder) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.ID == "" {
			return sql.ErrNoRows
		}
		if _, ok := seen[item.ID]; ok {
			return errors.New("duplicate host in reorder request")
		}
		seen[item.ID] = struct{}{}
		var owner string
		if err = tx.QueryRow(`SELECT user_id FROM hosts WHERE id=?`, item.ID).Scan(&owner); err != nil || owner != userID {
			return sql.ErrNoRows
		}
		if _, err = tx.Exec(`UPDATE hosts SET group_name=?,sort_order=?,updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, item.GroupName, item.SortOrder, userID, item.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) UpdateHostConnection(userID, id, status string, latency int) error {
	_, err := s.DB.Exec(`UPDATE hosts SET last_status=?,last_latency_ms=?,last_connected_at=CASE WHEN ?='online' THEN CURRENT_TIMESTAMP ELSE last_connected_at END,updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, status, latency, status, userID, id)
	return err
}
func (s *Store) UpdateHostSystem(userID, id, platform, distribution string) error {
	_, err := s.DB.Exec(`UPDATE hosts SET platform=?,distribution=?,updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, platform, distribution, userID, id)
	return err
}
func (s *Store) ActiveSessionsForHost(userID, id string) ([]TerminalSession, error) {
	rows, err := s.DB.Query(`SELECT `+terminalCols+` FROM terminal_sessions WHERE user_id=? AND host_id=? AND status<>'ended' ORDER BY updated_at DESC`, userID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TerminalSession
	for rows.Next() {
		item, e := scanTerminal(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) DeleteHost(userID, id string) error {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM terminal_sessions WHERE user_id=? AND host_id=? AND status NOT IN ('ended')`, userID, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("host has %d active sessions", n)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM hosts WHERE user_id=? AND jump_host_id=?`, userID, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("host is used as a jump host by %d hosts", n)
	}
	_, err := s.DB.Exec(`DELETE FROM hosts WHERE user_id=? AND id=?`, userID, id)
	return err
}
func (s *Store) BatchHosts(userID string, ids []string, action, groupName, tags string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return sql.ErrNoRows
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
		var owner string
		if err = tx.QueryRow(`SELECT user_id FROM hosts WHERE id=?`, id).Scan(&owner); err != nil || owner != userID {
			return sql.ErrNoRows
		}
	}
	if action == "delete" {
		for _, id := range unique {
			var count int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM terminal_sessions WHERE user_id=? AND host_id=? AND status<>'ended'`, userID, id).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("host %s has %d active sessions", id, count)
			}
			rows, queryErr := tx.Query(`SELECT id FROM hosts WHERE user_id=? AND jump_host_id=?`, userID, id)
			if queryErr != nil {
				return queryErr
			}
			for rows.Next() {
				var dependentID string
				if err = rows.Scan(&dependentID); err != nil {
					rows.Close()
					return err
				}
				if _, deleting := seen[dependentID]; !deleting {
					rows.Close()
					return fmt.Errorf("host %s is used as a jump host", id)
				}
			}
			if err = rows.Close(); err != nil {
				return err
			}
		}
	}
	for _, id := range unique {
		switch action {
		case "group":
			_, err = tx.Exec(`UPDATE hosts SET group_name=?,updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, groupName, userID, id)
		case "tags":
			var current string
			if err = tx.QueryRow(`SELECT tags FROM hosts WHERE user_id=? AND id=?`, userID, id).Scan(&current); err == nil {
				merged := make([]string, 0)
				known := map[string]bool{}
				for _, raw := range strings.Split(current+","+tags, ",") {
					value := strings.TrimSpace(raw)
					key := strings.ToLower(value)
					if value != "" && !known[key] {
						known[key] = true
						merged = append(merged, value)
					}
				}
				_, err = tx.Exec(`UPDATE hosts SET tags=?,updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, strings.Join(merged, ","), userID, id)
			}
		case "delete":
			_, err = tx.Exec(`DELETE FROM hosts WHERE user_id=? AND id=?`, userID, id)
		default:
			return errors.New("unsupported batch action")
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Credentials(userID string) ([]Credential, error) {
	rows, err := s.DB.Query(`SELECT id,user_id,name,kind,'','',created_at FROM credentials WHERE user_id=? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.Kind, &c.Secret, &c.Passphrase, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) Credential(userID, id string) (Credential, error) {
	var c Credential
	err := s.DB.QueryRow(`SELECT id,user_id,name,kind,secret_enc,passphrase_enc,created_at FROM credentials WHERE user_id=? AND id=?`, userID, id).Scan(&c.ID, &c.UserID, &c.Name, &c.Kind, &c.Secret, &c.Passphrase, &c.CreatedAt)
	return c, err
}
func (s *Store) SaveCredential(c Credential) error {
	_, err := s.DB.Exec(`INSERT INTO credentials(id,user_id,name,kind,secret_enc,passphrase_enc) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind,secret_enc=excluded.secret_enc,passphrase_enc=excluded.passphrase_enc,updated_at=CURRENT_TIMESTAMP WHERE user_id=excluded.user_id`, c.ID, c.UserID, c.Name, c.Kind, c.Secret, c.Passphrase)
	return err
}
func (s *Store) DeleteCredential(userID, id string) error {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM hosts WHERE user_id=? AND credential_id=?`, userID, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("credential is used by %d hosts", n)
	}
	_, err := s.DB.Exec(`DELETE FROM credentials WHERE user_id=? AND id=?`, userID, id)
	return err
}

func (s *Store) SaveTerminal(t TerminalSession) error {
	if t.SessionMode == "" {
		t.SessionMode = "tmux"
	}
	_, err := s.DB.Exec(`INSERT INTO terminal_sessions(id,user_id,host_id,credential_id,name,remote_user,session_mode,tmux_socket,tmux_name,owner_marker,status,last_error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,last_error=excluded.last_error,updated_at=CURRENT_TIMESTAMP`, t.ID, t.UserID, t.HostID, t.CredentialID, t.Name, t.RemoteUser, t.SessionMode, t.TmuxSocket, t.TmuxName, t.OwnerMarker, t.Status, t.LastError)
	return err
}
func scanTerminal(row interface{ Scan(...any) error }) (TerminalSession, error) {
	var t TerminalSession
	err := row.Scan(&t.ID, &t.UserID, &t.HostID, &t.CredentialID, &t.Name, &t.RemoteUser, &t.SessionMode, &t.TmuxSocket, &t.TmuxName, &t.OwnerMarker, &t.Status, &t.LastError, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

const terminalCols = `id,user_id,host_id,credential_id,name,remote_user,session_mode,tmux_socket,tmux_name,owner_marker,status,last_error,created_at,updated_at`

func (s *Store) Terminal(userID, id string) (TerminalSession, error) {
	return scanTerminal(s.DB.QueryRow(`SELECT `+terminalCols+` FROM terminal_sessions WHERE user_id=? AND id=?`, userID, id))
}
func (s *Store) Terminals(userID string) ([]TerminalSession, error) {
	rows, err := s.DB.Query(`SELECT `+terminalCols+` FROM terminal_sessions WHERE user_id=? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TerminalSession
	for rows.Next() {
		t, e := scanTerminal(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) UpdateTerminalStatus(userID, id, status, msg string) error {
	_, err := s.DB.Exec(`UPDATE terminal_sessions SET status=?,last_error=?,updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, status, msg, userID, id)
	return err
}
func (s *Store) EndStaleNormalSessions(message string) error {
	_, err := s.DB.Exec(`UPDATE terminal_sessions SET status='ended',last_error=?,updated_at=CURRENT_TIMESTAMP WHERE session_mode='normal' AND status<>'ended'`, message)
	return err
}
func (s *Store) RenameTerminal(userID, id, name string) error {
	result, err := s.DB.Exec(`UPDATE terminal_sessions SET name=?,updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, name, userID, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) DeleteTerminal(userID, id string) error {
	_, err := s.DB.Exec(`DELETE FROM terminal_sessions WHERE user_id=? AND id=?`, userID, id)
	return err
}

func (s *Store) Workspace(userID string) (json.RawMessage, int, error) {
	var raw string
	var version int
	err := s.DB.QueryRow(`SELECT layout_json,version FROM workspaces WHERE user_id=?`, userID).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return json.RawMessage(`{"tabs":[],"panes":[]}`), 0, nil
	}
	return json.RawMessage(raw), version, err
}
func (s *Store) SaveWorkspace(userID string, raw json.RawMessage, version int) error {
	if !json.Valid(raw) {
		return errors.New("invalid workspace")
	}
	res, err := s.DB.Exec(`INSERT INTO workspaces(user_id,layout_json,version) VALUES(?,?,1) ON CONFLICT(user_id) DO UPDATE SET layout_json=excluded.layout_json,version=workspaces.version+1,updated_at=CURRENT_TIMESTAMP WHERE workspaces.version=?`, userID, string(raw), version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("workspace version conflict")
	}
	return nil
}
func (s *Store) Preferences(userID string) (json.RawMessage, error) {
	var raw string
	err := s.DB.QueryRow(`SELECT preferences_json FROM user_preferences WHERE user_id=?`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(raw), err
}
func (s *Store) SavePreferences(userID string, raw json.RawMessage) error {
	if !json.Valid(raw) {
		return errors.New("invalid preferences")
	}
	_, err := s.DB.Exec(`INSERT INTO user_preferences(user_id,preferences_json)VALUES(?,?) ON CONFLICT(user_id)DO UPDATE SET preferences_json=excluded.preferences_json,updated_at=CURRENT_TIMESTAMP`, userID, string(raw))
	return err
}
func (s *Store) Snippets(userID string) ([]Snippet, error) {
	rows, err := s.DB.Query(`SELECT id,user_id,name,group_name,tags,command,description,created_at,updated_at FROM snippets WHERE user_id=? ORDER BY group_name,name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snippet
	for rows.Next() {
		var value Snippet
		if err = rows.Scan(&value.ID, &value.UserID, &value.Name, &value.GroupName, &value.Tags, &value.Command, &value.Description, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (s *Store) SaveSnippet(value Snippet) error {
	_, err := s.DB.Exec(`INSERT INTO snippets(id,user_id,name,group_name,tags,command,description)VALUES(?,?,?,?,?,?,?) ON CONFLICT(id)DO UPDATE SET name=excluded.name,group_name=excluded.group_name,tags=excluded.tags,command=excluded.command,description=excluded.description,updated_at=CURRENT_TIMESTAMP WHERE user_id=excluded.user_id`, value.ID, value.UserID, value.Name, value.GroupName, value.Tags, value.Command, value.Description)
	return err
}
func (s *Store) DeleteSnippet(userID, id string) error {
	_, err := s.DB.Exec(`DELETE FROM snippets WHERE user_id=? AND id=?`, userID, id)
	return err
}

const forwardCols = `id,user_id,host_id,name,kind,listen_address,listen_port,target_host,target_port,status,last_error,bytes_in,bytes_out,created_at,updated_at`

func scanForward(row interface{ Scan(...any) error }) (PortForward, error) {
	var value PortForward
	err := row.Scan(&value.ID, &value.UserID, &value.HostID, &value.Name, &value.Kind, &value.ListenAddress, &value.ListenPort, &value.TargetHost, &value.TargetPort, &value.Status, &value.LastError, &value.BytesIn, &value.BytesOut, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}
func (s *Store) PortForwards(userID string) ([]PortForward, error) {
	rows, err := s.DB.Query(`SELECT `+forwardCols+` FROM port_forwards WHERE user_id=? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PortForward
	for rows.Next() {
		value, e := scanForward(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (s *Store) PortForward(userID, id string) (PortForward, error) {
	return scanForward(s.DB.QueryRow(`SELECT `+forwardCols+` FROM port_forwards WHERE user_id=? AND id=?`, userID, id))
}
func (s *Store) SavePortForward(value PortForward) error {
	_, err := s.DB.Exec(`INSERT INTO port_forwards(id,user_id,host_id,name,kind,listen_address,listen_port,target_host,target_port,status,last_error)VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id)DO UPDATE SET name=excluded.name,kind=excluded.kind,listen_address=excluded.listen_address,listen_port=excluded.listen_port,target_host=excluded.target_host,target_port=excluded.target_port,updated_at=CURRENT_TIMESTAMP WHERE user_id=excluded.user_id`, value.ID, value.UserID, value.HostID, value.Name, value.Kind, value.ListenAddress, value.ListenPort, value.TargetHost, value.TargetPort, value.Status, value.LastError)
	return err
}
func (s *Store) UpdatePortForward(userID, id, status, lastError string) error {
	_, err := s.DB.Exec(`UPDATE port_forwards SET status=?,last_error=?,updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, status, lastError, userID, id)
	return err
}
func (s *Store) DeletePortForward(userID, id string) error {
	_, err := s.DB.Exec(`DELETE FROM port_forwards WHERE user_id=? AND id=?`, userID, id)
	return err
}

const webServiceCols = `id,user_id,host_id,name,proxy_mode,page_mode,listen_port,target_url,upstream_host,skip_tls_verify,created_at,updated_at`

func scanWebService(row interface{ Scan(...any) error }) (WebService, error) {
	var value WebService
	err := row.Scan(&value.ID, &value.UserID, &value.HostID, &value.Name, &value.ProxyMode, &value.PageMode, &value.ListenPort, &value.TargetURL, &value.UpstreamHost, &value.SkipTLSVerify, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}
func (s *Store) WebServices(userID string) ([]WebService, error) {
	rows, err := s.DB.Query(`SELECT `+webServiceCols+` FROM web_services WHERE user_id=? ORDER BY name COLLATE NOCASE,updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebService
	for rows.Next() {
		value, scanErr := scanWebService(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (s *Store) WebService(userID, id string) (WebService, error) {
	return scanWebService(s.DB.QueryRow(`SELECT `+webServiceCols+` FROM web_services WHERE user_id=? AND id=?`, userID, id))
}
func (s *Store) SaveWebService(value WebService) error {
	if value.ProxyMode == "" {
		value.ProxyMode = "path"
	}
	if value.PageMode == "" {
		value.PageMode = "html"
	}
	_, err := s.DB.Exec(`INSERT INTO web_services(id,user_id,host_id,name,proxy_mode,page_mode,listen_port,target_url,upstream_host,skip_tls_verify) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET host_id=excluded.host_id,name=excluded.name,proxy_mode=excluded.proxy_mode,page_mode=excluded.page_mode,listen_port=excluded.listen_port,target_url=excluded.target_url,upstream_host=excluded.upstream_host,skip_tls_verify=excluded.skip_tls_verify,updated_at=CURRENT_TIMESTAMP WHERE user_id=excluded.user_id`, value.ID, value.UserID, value.HostID, value.Name, value.ProxyMode, value.PageMode, value.ListenPort, value.TargetURL, value.UpstreamHost, value.SkipTLSVerify)
	return err
}
func (s *Store) HostPortWebServices() ([]WebService, error) {
	rows, err := s.DB.Query(`SELECT ` + webServiceCols + ` FROM web_services WHERE proxy_mode='host_port' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebService
	for rows.Next() {
		value, scanErr := scanWebService(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (s *Store) DeleteWebService(userID, id string) error {
	result, err := s.DB.Exec(`DELETE FROM web_services WHERE user_id=? AND id=?`, userID, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CommandTasks(userID string) ([]CommandTask, error) {
	rows, err := s.DB.Query(`SELECT id,command,session_ids,status,output,error,created_at,started_at,finished_at FROM command_tasks WHERE user_id=? ORDER BY created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CommandTask, 0)
	for rows.Next() {
		var value CommandTask
		var raw string
		var started, finished sql.NullTime
		if err := rows.Scan(&value.ID, &value.Command, &raw, &value.Status, &value.Output, &value.Error, &value.CreatedAt, &started, &finished); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &value.SessionIDs)
		if started.Valid {
			value.StartedAt = &started.Time
		}
		if finished.Valid {
			value.FinishedAt = &finished.Time
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) CommandTask(userID, id string) (CommandTask, error) {
	var value CommandTask
	var raw string
	var started, finished sql.NullTime
	err := s.DB.QueryRow(`SELECT id,command,session_ids,status,output,error,created_at,started_at,finished_at FROM command_tasks WHERE user_id=? AND id=?`, userID, id).Scan(&value.ID, &value.Command, &raw, &value.Status, &value.Output, &value.Error, &value.CreatedAt, &started, &finished)
	if err != nil {
		return value, err
	}
	_ = json.Unmarshal([]byte(raw), &value.SessionIDs)
	if started.Valid {
		value.StartedAt = &started.Time
	}
	if finished.Valid {
		value.FinishedAt = &finished.Time
	}
	return value, nil
}

func (s *Store) CreateCommandTask(value CommandTask) error {
	raw, err := json.Marshal(value.SessionIDs)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO command_tasks(id,user_id,command,session_ids,status) VALUES(?,?,?,?,?)`, value.ID, value.UserID, value.Command, string(raw), "queued")
	return err
}

func (s *Store) UpdateCommandTask(userID, id, status, output, message string, started, finished *time.Time) error {
	_, err := s.DB.Exec(`UPDATE command_tasks SET status=?,output=?,error=?,started_at=?,finished_at=? WHERE user_id=? AND id=?`, status, output, message, started, finished, userID, id)
	return err
}

func (s *Store) CreateRecording(value TerminalRecording) error {
	_, err := s.DB.Exec(`INSERT INTO terminal_recordings(id,user_id,session_id,session_name,path,status,bytes,started_at) VALUES(?,?,?,?,?,?,?,?)`, value.ID, value.UserID, value.SessionID, value.SessionName, value.Path, value.Status, value.Bytes, value.StartedAt)
	return err
}

func (s *Store) Recording(userID, id string) (TerminalRecording, error) {
	var value TerminalRecording
	var finished sql.NullTime
	err := s.DB.QueryRow(`SELECT id,user_id,session_id,session_name,path,status,bytes,started_at,finished_at FROM terminal_recordings WHERE user_id=? AND id=?`, userID, id).Scan(&value.ID, &value.UserID, &value.SessionID, &value.SessionName, &value.Path, &value.Status, &value.Bytes, &value.StartedAt, &finished)
	if finished.Valid {
		value.FinishedAt = &finished.Time
	}
	return value, err
}

func (s *Store) Recordings(userID string) ([]TerminalRecording, error) {
	rows, err := s.DB.Query(`SELECT id,user_id,session_id,session_name,path,status,bytes,started_at,finished_at FROM terminal_recordings WHERE user_id=? ORDER BY started_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TerminalRecording, 0)
	for rows.Next() {
		var value TerminalRecording
		var finished sql.NullTime
		if err := rows.Scan(&value.ID, &value.UserID, &value.SessionID, &value.SessionName, &value.Path, &value.Status, &value.Bytes, &value.StartedAt, &finished); err != nil {
			return nil, err
		}
		if finished.Valid {
			value.FinishedAt = &finished.Time
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) FinishRecording(userID, id, status string, bytes int64, finished time.Time) error {
	_, err := s.DB.Exec(`UPDATE terminal_recordings SET status=?,bytes=?,finished_at=? WHERE user_id=? AND id=?`, status, bytes, finished, userID, id)
	return err
}

const terminalShareColumns = `s.id,s.user_id,s.session_id,t.name,s.token_hash,s.token_enc,s.password_hash,s.permission,s.record,s.recording_id,s.expires_at,s.revoked_at,s.created_at,t.status,u.disabled`

func scanTerminalShare(row interface{ Scan(...any) error }) (TerminalShare, error) {
	var value TerminalShare
	var revoked sql.NullTime
	err := row.Scan(
		&value.ID, &value.UserID, &value.SessionID, &value.SessionName, &value.TokenHash, &value.TokenEnc,
		&value.PasswordHash, &value.Permission, &value.Record, &value.RecordingID,
		&value.ExpiresAt, &revoked, &value.CreatedAt, &value.SessionStatus, &value.OwnerDisabled,
	)
	value.PasswordRequired = value.PasswordHash != ""
	if revoked.Valid {
		value.RevokedAt = &revoked.Time
	}
	return value, err
}

func (s *Store) CreateTerminalShare(value TerminalShare) error {
	_, err := s.DB.Exec(
		`INSERT INTO terminal_shares(id,user_id,session_id,token_hash,token_enc,password_hash,permission,record,recording_id,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.UserID, value.SessionID, value.TokenHash, value.TokenEnc, value.PasswordHash,
		value.Permission, value.Record, value.RecordingID, value.ExpiresAt, value.CreatedAt,
	)
	return err
}

func (s *Store) TerminalShareByTokenHash(tokenHash string) (TerminalShare, error) {
	return scanTerminalShare(s.DB.QueryRow(
		`SELECT `+terminalShareColumns+` FROM terminal_shares s JOIN terminal_sessions t ON t.id=s.session_id JOIN users u ON u.id=s.user_id WHERE s.token_hash=?`,
		tokenHash,
	))
}

func (s *Store) TerminalShare(userID, id string) (TerminalShare, error) {
	return scanTerminalShare(s.DB.QueryRow(
		`SELECT `+terminalShareColumns+` FROM terminal_shares s JOIN terminal_sessions t ON t.id=s.session_id JOIN users u ON u.id=s.user_id WHERE s.user_id=? AND s.id=?`,
		userID, id,
	))
}

func (s *Store) TerminalShares(userID, sessionID string) ([]TerminalShare, error) {
	rows, err := s.DB.Query(
		`SELECT `+terminalShareColumns+` FROM terminal_shares s JOIN terminal_sessions t ON t.id=s.session_id JOIN users u ON u.id=s.user_id WHERE s.user_id=? AND s.session_id=? ORDER BY s.created_at DESC LIMIT 100`,
		userID, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]TerminalShare, 0)
	for rows.Next() {
		value, scanErr := scanTerminalShare(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) RevokeTerminalShare(userID, id string, revokedAt time.Time) (TerminalShare, error) {
	value, err := s.TerminalShare(userID, id)
	if err != nil {
		return TerminalShare{}, err
	}
	result, err := s.DB.Exec(`UPDATE terminal_shares SET revoked_at=? WHERE user_id=? AND id=? AND revoked_at IS NULL`, revokedAt, userID, id)
	if err != nil {
		return TerminalShare{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return TerminalShare{}, sql.ErrNoRows
	}
	_, _ = s.DB.Exec(`DELETE FROM terminal_share_access WHERE share_id=?`, id)
	value.RevokedAt = &revokedAt
	return value, nil
}

func (s *Store) TerminalSharesNeedingClosure(now time.Time) ([]TerminalShare, error) {
	rows, err := s.DB.Query(
		`SELECT `+terminalShareColumns+` FROM terminal_shares s JOIN terminal_sessions t ON t.id=s.session_id JOIN users u ON u.id=s.user_id WHERE s.revoked_at IS NULL AND (s.expires_at<=? OR t.status='ended' OR u.disabled=1)`,
		now,
	)
	if err != nil {
		return nil, err
	}
	values := make([]TerminalShare, 0)
	for rows.Next() {
		value, scanErr := scanTerminalShare(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return values, rows.Close()
}

func (s *Store) CreateTerminalShareAccess(value TerminalShareAccess) error {
	_, err := s.DB.Exec(
		`INSERT INTO terminal_share_access(token_hash,share_id,display_name,ip,user_agent,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?,?,?,?)`,
		value.TokenHash, value.ShareID, value.DisplayName, value.IP, value.UserAgent,
		value.ExpiresAt, value.CreatedAt, value.LastSeenAt,
	)
	return err
}

func (s *Store) TerminalShareAccess(tokenHash, shareID string, now time.Time) (TerminalShareAccess, error) {
	var value TerminalShareAccess
	err := s.DB.QueryRow(
		`SELECT token_hash,share_id,display_name,ip,user_agent,expires_at,created_at,last_seen_at FROM terminal_share_access WHERE token_hash=? AND share_id=? AND expires_at>?`,
		tokenHash, shareID, now,
	).Scan(&value.TokenHash, &value.ShareID, &value.DisplayName, &value.IP, &value.UserAgent, &value.ExpiresAt, &value.CreatedAt, &value.LastSeenAt)
	return value, err
}

func (s *Store) TouchTerminalShareAccess(tokenHash string, now time.Time) error {
	_, err := s.DB.Exec(`UPDATE terminal_share_access SET last_seen_at=? WHERE token_hash=?`, now, tokenHash)
	return err
}

func (s *Store) Audit(userID, eventType, resourceType, resourceID, ip string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO audit_events(user_id,event_type,resource_type,resource_id,ip,details) VALUES(?,?,?,?,?,?)`, userID, eventType, resourceType, resourceID, ip, string(raw))
	return err
}
func (s *Store) TOTP(userID string) (secret string, recovery []string, enabled bool, err error) {
	var raw string
	err = s.DB.QueryRow(`SELECT secret_enc,recovery_hashes,enabled FROM user_totp WHERE user_id=?`, userID).Scan(&secret, &raw, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &recovery)
	}
	return
}
func (s *Store) SaveTOTP(userID, secret string, recovery []string, enabled bool) error {
	raw, err := json.Marshal(recovery)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO user_totp(user_id,secret_enc,recovery_hashes,enabled)VALUES(?,?,?,?) ON CONFLICT(user_id)DO UPDATE SET secret_enc=excluded.secret_enc,recovery_hashes=excluded.recovery_hashes,enabled=excluded.enabled,updated_at=CURRENT_TIMESTAMP`, userID, secret, string(raw), enabled)
	return err
}
func (s *Store) DeleteTOTP(userID string) error {
	_, err := s.DB.Exec(`DELETE FROM user_totp WHERE user_id=?`, userID)
	return err
}
func (s *Store) KnownHost(userID, address string, port int) (string, string, error) {
	var fp, key string
	err := s.DB.QueryRow(`SELECT fingerprint,public_key FROM known_host_keys WHERE user_id=? AND address=? AND port=?`, userID, address, port).Scan(&fp, &key)
	return fp, key, err
}
func (s *Store) TrustHost(userID, address string, port int, fp, key string) error {
	_, err := s.DB.Exec(`INSERT INTO known_host_keys(user_id,address,port,fingerprint,public_key)VALUES(?,?,?,?,?) ON CONFLICT(user_id,address,port)DO UPDATE SET fingerprint=excluded.fingerprint,public_key=excluded.public_key,created_at=CURRENT_TIMESTAMP`, userID, address, port, fp, key)
	return err
}
func (s *Store) Backup(ctx context.Context, path string) error {
	_, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// Restore copies the compatible tables from a verified backup into the open
// database connection. Keeping the connection open avoids invalidating all
// API dependencies that reference Store, while the transaction keeps the
// restore atomic from other requests.
func (s *Store) Restore(ctx context.Context, path string) error {
	if _, err := s.DB.ExecContext(ctx, `ATTACH DATABASE ? AS velin_restore`, path); err != nil {
		return err
	}
	defer s.DB.Exec(`DETACH DATABASE velin_restore`)
	if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.DB.Exec(`PRAGMA foreign_keys=ON`)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, table := range tables {
		var exists string
		if err = tx.QueryRow(`SELECT name FROM velin_restore.sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		mainColumns, err := tableColumns(tx, "main", table)
		if err != nil {
			return err
		}
		restoreColumns, err := tableColumns(tx, "velin_restore", table)
		if err != nil {
			return err
		}
		available := make(map[string]bool, len(restoreColumns))
		for _, column := range restoreColumns {
			available[column] = true
		}
		columns := make([]string, 0, len(mainColumns))
		for _, column := range mainColumns {
			if available[column] {
				columns = append(columns, column)
			}
		}
		if len(columns) == 0 {
			continue
		}
		qTable := quoteSQLiteIdentifier(table)
		if _, err = tx.Exec(`DELETE FROM main.` + qTable); err != nil {
			return err
		}
		quoted := make([]string, len(columns))
		for i, column := range columns {
			quoted[i] = quoteSQLiteIdentifier(column)
		}
		list := strings.Join(quoted, ",")
		if _, err = tx.Exec(`INSERT INTO main.` + qTable + ` (` + list + `) SELECT ` + list + ` FROM velin_restore.` + qTable); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM main.auth_sessions`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM main.terminal_share_access`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.migrate()
}

func tableColumns(tx *sql.Tx, schema, table string) ([]string, error) {
	rows, err := tx.Query(`PRAGMA ` + schema + `.table_info(` + quoteSQLiteIdentifier(table) + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}
func VerifyBackup(path string) error {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err = db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version < 1 || version > 16 {
		return fmt.Errorf("unsupported database version %d", version)
	}
	requiredTables := []string{"users", "hosts", "workspaces", "terminal_sessions"}
	if version >= 7 {
		requiredTables = append(requiredTables, "web_services")
	}
	if version >= 16 {
		requiredTables = append(requiredTables, "terminal_shares", "terminal_share_access")
	}
	for _, table := range requiredTables {
		var found string
		if err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil {
			return fmt.Errorf("missing required table %s", table)
		}
	}
	return nil
}
