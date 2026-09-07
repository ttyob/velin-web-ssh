package adminreset

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"velin-webssh/internal/security"
	"velin-webssh/internal/store"
)

func TestResetAdministratorPassword(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "velin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateUser("admin-id", "admin", "old-hash", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuthSession("session", "admin-id", "token", "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLoginFailure("admin", "127.0.0.1", 1, 15); err != nil {
		t.Fatal(err)
	}

	result, err := Reset(s, "admin", "new-password")
	if err != nil {
		t.Fatal(err)
	}
	if result.Username != "admin" || result.Password != "new-password" || result.Generated {
		t.Fatalf("unexpected result: %+v", result)
	}
	user, hash, err := s.UserByUsername("admin")
	if err != nil || !user.ForcePasswordChange || !security.VerifyPassword(hash, "new-password") {
		t.Fatalf("password was not reset: user=%+v err=%v", user, err)
	}
	if _, err := s.UserByToken("token"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old session still exists: %v", err)
	}
	if failures, err := s.LoginFailureCount("admin", "192.0.2.1"); err != nil || failures != 0 {
		t.Fatalf("account login failures were not cleared: failures=%d err=%v", failures, err)
	}
}

func TestResetGeneratesPasswordAndRejectsNonAdmin(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "velin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateUser("admin-id", "admin", "old-hash", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser("user-id", "member", "old-hash", "user"); err != nil {
		t.Fatal(err)
	}

	result, err := Reset(s, "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Generated || strings.TrimSpace(result.Password) == "" {
		t.Fatalf("temporary password was not generated: %+v", result)
	}
	if _, err := Reset(s, "member", "new-password"); err == nil {
		t.Fatal("non-administrator password reset succeeded")
	}
}
