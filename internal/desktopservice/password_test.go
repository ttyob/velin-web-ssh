package desktopservice

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

func TestAdministratorPasswordAndInitialCredentials(t *testing.T) {
	directory := t.TempDir()
	s, err := store.Open(filepath.Join(directory, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hash, err := security.HashPassword("temporary-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateUser("admin-id", "admin", hash, "admin"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetForcePasswordChange("admin-id", true); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "initial-admin-credentials.txt"), []byte("username=admin\npassword=temporary-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials, err := InitialCredentials(s, "admin", directory, nil)
	if err != nil || credentials == nil || credentials.Password != "temporary-password" {
		t.Fatalf("initial credentials: %v, %+v", err, credentials)
	}
	if err = s.CreateAuthSession("old-session", "admin-id", "old-token", "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveSystemSetting("security_policy", map[string]int{"passwordMinLength": 14}); err != nil {
		t.Fatal(err)
	}
	for _, passwords := range [][2]string{{"different-password", "confirmation"}, {"short", "short"}, {"twelve-chars", "twelve-chars"}} {
		if err = ChangePassword(s, "admin", passwords[0], passwords[1]); err == nil {
			t.Fatalf("invalid password accepted: %q", passwords[0])
		}
	}
	if err = ChangePassword(s, "admin", "new-administrator-password", "new-administrator-password"); err != nil {
		t.Fatal(err)
	}
	u, newHash, err := s.UserByUsername("admin")
	if err != nil || u.ForcePasswordChange || !security.VerifyPassword(newHash, "new-administrator-password") || security.VerifyPassword(newHash, "temporary-password") {
		t.Fatal("administrator password was not updated")
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE user_id='admin-id'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("old login sessions remain valid")
	}
	credentials, err = InitialCredentials(s, "admin", directory, &Credentials{Username: "admin", Password: "temporary-password"})
	if err != nil || credentials != nil {
		t.Fatal("stale initial password was displayed")
	}
	if err = s.CreateUser("user-id", "user", hash, "user"); err != nil {
		t.Fatal(err)
	}
	if err = ChangePassword(s, "user", "new-administrator-password", "new-administrator-password"); err == nil {
		t.Fatal("non-administrator password was changed")
	}
}
