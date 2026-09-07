package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"velin-webssh/internal/security"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHostOwnership(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"u1", "u2"} {
		if err := s.CreateUser(id, id, "hash", "user"); err != nil {
			t.Fatal(err)
		}
	}
	h := Host{ID: "h1", UserID: "u1", Name: "server", Address: "127.0.0.1", Port: 22, Username: "root"}
	if err := s.SaveHost(h); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Host("u2", "h1"); err == nil {
		t.Fatal("other user accessed host")
	}
	if err := s.ReorderHosts("u1", []HostOrder{{ID: "h1", GroupName: "production", SortOrder: 3}}); err != nil {
		t.Fatalf("reorder host: %v", err)
	}
	updated, err := s.Host("u1", "h1")
	if err != nil || updated.GroupName != "production" || updated.SortOrder != 3 {
		t.Fatalf("reordered host=%+v err=%v", updated, err)
	}
	if err := s.ReorderHosts("u1", []HostOrder{{ID: "h1", GroupName: "production", SortOrder: 4}}); err != nil {
		t.Fatalf("owned reorder failed: %v", err)
	}
	if err := s.ReorderHosts("u2", []HostOrder{{ID: "h1", GroupName: "other", SortOrder: 1}}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-user reorder error=%v", err)
	}
}

func TestBatchHostsIsAtomicAndOwned(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"u1", "u2"} {
		if err := s.CreateUser(id, id, "hash", "user"); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []Host{
		{ID: "h1", UserID: "u1", Name: "one", Address: "127.0.0.1", Port: 22, Username: "root"},
		{ID: "h2", UserID: "u1", Name: "two", Address: "127.0.0.2", Port: 22, Username: "root"},
		{ID: "foreign", UserID: "u2", Name: "foreign", Address: "127.0.0.3", Port: 22, Username: "root"},
	} {
		if err := s.SaveHost(host); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.BatchHosts("u1", []string{"h1", "foreign"}, "group", "production", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-user batch error=%v", err)
	}
	host, _ := s.Host("u1", "h1")
	if host.GroupName != "" {
		t.Fatal("cross-user batch partially updated an owned host")
	}
	if err := s.BatchHosts("u1", []string{"h1", "h2"}, "tags", "", "linux, production,linux"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"h1", "h2"} {
		host, _ = s.Host("u1", id)
		if host.Tags != "linux,production" {
			t.Fatalf("host %s tags=%q", id, host.Tags)
		}
	}
	if err := s.SaveTerminal(TerminalSession{ID: "s1", UserID: "u1", HostID: "h2", Name: "active", RemoteUser: "root", SessionMode: "normal", TmuxSocket: "sock", TmuxName: "tmux", OwnerMarker: "owner", Status: "attached"}); err != nil {
		t.Fatal(err)
	}
	session, err := s.Terminal("u1", "s1")
	if err != nil || session.SessionMode != "normal" {
		t.Fatalf("session mode=%q err=%v", session.SessionMode, err)
	}
	if err := s.BatchHosts("u1", []string{"h1", "h2"}, "delete", "", ""); err == nil {
		t.Fatal("batch delete with active session succeeded")
	}
	if _, err := s.Host("u1", "h1"); err != nil {
		t.Fatal("batch delete was not atomic")
	}
	if err = s.EndStaleNormalSessions("service restarted"); err != nil {
		t.Fatal(err)
	}
	session, err = s.Terminal("u1", "s1")
	if err != nil || session.Status != "ended" || session.LastError != "service restarted" {
		t.Fatalf("stale normal session=%+v err=%v", session, err)
	}
}

func TestWorkspaceVersionConflict(t *testing.T) {
	s := testStore(t)
	if err := s.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorkspace("u1", json.RawMessage(`{"tabs":[]}`), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorkspace("u1", json.RawMessage(`{"tabs":["one"]}`), 0); err == nil {
		t.Fatal("stale workspace write succeeded")
	}
	if err := s.SaveWorkspace("u1", json.RawMessage(`{"tabs":["one"]}`), 1); err != nil {
		t.Fatal(err)
	}
}

func TestAuthSessionLockState(t *testing.T) {
	s := testStore(t)
	if err := s.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuthSession("session", "u1", "token-hash", "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	locked, err := s.AuthSessionLocked("token-hash")
	if err != nil || locked {
		t.Fatalf("initial locked=%v err=%v", locked, err)
	}
	if err = s.SetAuthSessionLocked("token-hash", true); err != nil {
		t.Fatal(err)
	}
	locked, err = s.AuthSessionLocked("token-hash")
	if err != nil || !locked {
		t.Fatalf("locked=%v err=%v", locked, err)
	}
	if err = s.SetAuthSessionLocked("token-hash", false); err != nil {
		t.Fatal(err)
	}
	locked, err = s.AuthSessionLocked("token-hash")
	if err != nil || locked {
		t.Fatalf("unlocked=%v err=%v", locked, err)
	}
}

func TestTOTPDefaultsToDisabledWhenNotConfigured(t *testing.T) {
	s := testStore(t)
	if err := s.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	secret, recovery, enabled, err := s.TOTP("u1")
	if err != nil || secret != "" || recovery != nil || enabled {
		t.Fatalf("unexpected TOTP defaults: secret=%q recovery=%v enabled=%v err=%v", secret, recovery, enabled, err)
	}
}

func TestResetUserPasswordInvalidatesSessions(t *testing.T) {
	s := testStore(t)
	if err := s.CreateUser("u1", "admin", "old-hash", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuthSession("session", "u1", "token-hash", "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	newHash, err := security.HashPassword("new-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ResetUserPassword("u1", newHash, true); err != nil {
		t.Fatal(err)
	}
	user, storedHash, err := s.UserByUsername("admin")
	if err != nil || !user.ForcePasswordChange || !security.VerifyPassword(storedHash, "new-password") {
		t.Fatalf("reset user=%+v hash_valid=%v err=%v", user, security.VerifyPassword(storedHash, "new-password"), err)
	}
	if _, err = s.UserByToken("token-hash"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reset session still exists: %v", err)
	}
	if err = s.ResetUserPassword("missing", newHash, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing user reset error=%v", err)
	}
}

func TestLoginFailuresAreScopedToAccountIPAndIP(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "login.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 0; i < 5; i++ {
		if err = s.RecordLoginFailure("admin", "192.0.2.10", 5, 15); err != nil {
			t.Fatal(err)
		}
	}
	if locked, err := s.LoginLock("admin", "192.0.2.10"); err != nil || !locked.After(time.Now()) {
		t.Fatalf("account/IP lock=%v err=%v", locked, err)
	}
	for i := 0; i < 5; i++ {
		if err = s.RecordLoginFailure("admin", "192.0.2.11", 5, 15); err != nil {
			t.Fatal(err)
		}
	}
	if locked, err := s.LoginLock("admin", "192.0.2.11"); err != nil || locked.IsZero() {
		t.Fatalf("account-wide lock=%v err=%v", locked, err)
	}
	for i := 0; i < 15; i++ {
		if err = s.RecordLoginFailure(fmt.Sprintf("other-%d", i), "192.0.2.10", 5, 15); err != nil {
			t.Fatal(err)
		}
	}
	if locked, err := s.LoginLock("other", "192.0.2.10"); err != nil || locked.IsZero() {
		t.Fatalf("IP-wide lock=%v err=%v", locked, err)
	}
	if err = s.ClearLoginFailures("admin", "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if locked, err := s.LoginLock("admin", "192.0.2.10"); err != nil || !locked.IsZero() {
		t.Fatalf("cleared lock=%v err=%v", locked, err)
	}
}

func TestUserLockPINLifecycle(t *testing.T) {
	s := testStore(t)
	if err := s.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	pinHash, err := security.HashPassword("123456")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserLockPIN("u1", pinHash); err != nil {
		t.Fatal(err)
	}
	stored, err := s.UserLockPINHash("u1")
	if err != nil || stored == "" || !security.VerifyPassword(stored, "123456") {
		t.Fatalf("stored PIN hash invalid err=%v", err)
	}
	if err = s.CreateAuthSession("session", "u1", "token-hash", "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = s.SetAuthSessionLocked("token-hash", true); err != nil {
		t.Fatal(err)
	}
	if err = s.DisableUserLockPIN("u1"); err != nil {
		t.Fatal(err)
	}
	stored, err = s.UserLockPINHash("u1")
	if err != nil || stored != "" {
		t.Fatalf("PIN was not cleared: %q err=%v", stored, err)
	}
	locked, err := s.AuthSessionLocked("token-hash")
	if err != nil || locked {
		t.Fatalf("session remained locked=%v err=%v", locked, err)
	}
}

func TestBackup(t *testing.T) {
	s := testStore(t)
	if err := s.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	backup, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if n, err := backup.UserCount(); err != nil || n != 1 {
		t.Fatalf("backup user count=%d err=%v", n, err)
	}
	if err := VerifyBackup(path); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreKeepsStoreConnectionAndClearsSessions(t *testing.T) {
	s := testStore(t)
	if err := s.CreateUser("u1", "first", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser("u2", "second", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuthSession("session", "u2", "token-hash", "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UserCount(); err != nil || n != 1 {
		t.Fatalf("restored user count=%d err=%v", n, err)
	}
	if _, _, err := s.UserByUsername("first"); err != nil {
		t.Fatalf("restored user unavailable: %v", err)
	}
	if _, _, err := s.UserByUsername("second"); err == nil {
		t.Fatal("user created after backup survived restore")
	}
	if _, err := s.UserByToken("token-hash"); err == nil {
		t.Fatal("auth session survived restore")
	}
}

func TestVerifyBackupRejectsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if VerifyBackup(path) == nil {
		t.Fatal("invalid backup passed verification")
	}
}

func TestHostMigrationAndConnectionMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user', disabled INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE hosts (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL, address TEXT NOT NULL, port INTEGER NOT NULL DEFAULT 22, username TEXT NOT NULL, credential_id TEXT, group_name TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '', favorite INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO users(id,username,password_hash) VALUES('u1','user','hash');
		INSERT INTO hosts(id,user_id,name,address,username) VALUES('h1','u1','legacy','127.0.0.1','root');
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	host, err := s.Host("u1", "h1")
	if err != nil {
		t.Fatal(err)
	}
	if host.Protocol != "ssh" || host.RDPMode != "web" || host.DesktopSecurity != "any" {
		t.Fatalf("legacy host desktop defaults=%+v", host)
	}
	if host.ConnectTimeout != 12 || host.KeepaliveInterval != 30 || host.MaxRetries != 5 || host.TerminalType != "xterm-256color" || host.SessionMode != "tmux" || host.JumpHostID != "" || host.Platform != "" || host.Distribution != "" {
		t.Fatalf("unexpected migrated defaults: %+v", host)
	}
	if err = s.UpdateHostConnection("u1", "h1", "online", 37); err != nil {
		t.Fatal(err)
	}
	host, err = s.Host("u1", "h1")
	if err != nil {
		t.Fatal(err)
	}
	if host.LastStatus != "online" || host.LastLatency != 37 || host.LastConnectedAt == nil {
		t.Fatalf("connection metadata was not saved: %+v", host)
	}
	if err = s.UpdateHostSystem("u1", "h1", "linux", "ubuntu"); err != nil {
		t.Fatal(err)
	}
	host, err = s.Host("u1", "h1")
	if err != nil || host.Platform != "linux" || host.Distribution != "ubuntu" {
		t.Fatalf("platform=%q distribution=%q err=%v", host.Platform, host.Distribution, err)
	}
	host.SessionMode = "normal"
	if err = s.SaveHost(host); err != nil {
		t.Fatal(err)
	}
	host, err = s.Host("u1", "h1")
	if err != nil || host.SessionMode != "normal" {
		t.Fatalf("session mode=%q err=%v", host.SessionMode, err)
	}
	var version int
	if err = s.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
}

func TestJumpHostPersistenceAndDeleteProtection(t *testing.T) {
	s := testStore(t)
	if err := s.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	jump := Host{ID: "jump", UserID: "u1", Name: "bastion", Address: "192.0.2.1", Port: 22, Username: "root"}
	target := Host{ID: "target", UserID: "u1", Name: "private", Address: "10.0.0.2", Port: 22, Username: "root", JumpHostID: jump.ID}
	if err := s.SaveHost(jump); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHost(target); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Host("u1", target.ID)
	if err != nil || saved.JumpHostID != jump.ID {
		t.Fatalf("jumpHostID=%q err=%v", saved.JumpHostID, err)
	}
	if err = s.DeleteHost("u1", jump.ID); err == nil || !strings.Contains(err.Error(), "jump host") {
		t.Fatalf("referenced jump host deletion error=%v", err)
	}
	if err = s.BatchHosts("u1", []string{jump.ID, target.ID}, "delete", "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestWebServiceOwnershipAndHostCascade(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"u1", "u2"} {
		if err := s.CreateUser(id, id, "hash", "user"); err != nil {
			t.Fatal(err)
		}
	}
	host := Host{ID: "h1", UserID: "u1", Name: "home", Address: "127.0.0.1", Port: 22, Username: "root"}
	if err := s.SaveHost(host); err != nil {
		t.Fatal(err)
	}
	service := WebService{ID: "w1", UserID: "u1", HostID: host.ID, Name: "Router", ProxyMode: "host_port", ListenPort: 18080, TargetURL: "http://192.168.1.1"}
	if err := s.SaveWebService(service); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WebService("u2", service.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign web service error=%v", err)
	}
	saved, err := s.WebService("u1", service.ID)
	if err != nil || saved.ProxyMode != "host_port" || saved.ListenPort != 18080 {
		t.Fatalf("host port mode was not saved: %+v err=%v", saved, err)
	}
	if err := s.DeleteHost("u1", host.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WebService("u1", service.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("web service survived host deletion: %v", err)
	}
}

func TestLegacyWebServiceMigratesToPathMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-web.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user', disabled INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE hosts (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL, address TEXT NOT NULL, port INTEGER NOT NULL DEFAULT 22, username TEXT NOT NULL, credential_id TEXT, group_name TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '', favorite INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE web_services (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, host_id TEXT NOT NULL, name TEXT NOT NULL, target_url TEXT NOT NULL, upstream_host TEXT NOT NULL DEFAULT '', skip_tls_verify INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO users(id,username,password_hash) VALUES('u1','user','hash');
		INSERT INTO hosts(id,user_id,name,address,username) VALUES('h1','u1','legacy','127.0.0.1','root');
		INSERT INTO web_services(id,user_id,host_id,name,target_url) VALUES('w1','u1','h1','Router','http://192.168.1.1');
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service, err := s.WebService("u1", "w1")
	if err != nil || service.ProxyMode != "path" || service.ListenPort != 0 {
		t.Fatalf("legacy web service mode=%q port=%d err=%v", service.ProxyMode, service.ListenPort, err)
	}
}
