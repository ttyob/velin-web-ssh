package desktopservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

// InitialCredentials only returns a temporary password that is still valid.
func InitialCredentials(s *store.Store, username, directory string, notice *Credentials) (*Credentials, error) {
	u, hash, err := s.UserByUsername(username)
	if err != nil {
		return nil, err
	}
	if u.Role != "admin" || u.Disabled || !u.ForcePasswordChange {
		return nil, nil
	}
	if notice != nil && notice.Username == u.Username && security.VerifyPassword(hash, notice.Password) {
		value := *notice
		return &value, nil
	}
	content, err := os.ReadFile(filepath.Join(directory, "initial-admin-credentials.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
		if ok {
			values[key] = value
		}
	}
	if values["username"] != u.Username || !security.VerifyPassword(hash, values["password"]) {
		return nil, nil
	}
	return &Credentials{Username: u.Username, Password: values["password"]}, nil
}

func ChangePassword(s *store.Store, username, password, confirmation string) error {
	if password != confirmation {
		return errors.New("两次输入的密码不一致")
	}
	policy := struct {
		PasswordMinLength int `json:"passwordMinLength"`
	}{PasswordMinLength: 10}
	_ = s.SystemSetting("security_policy", &policy)
	if len(password) < policy.PasswordMinLength {
		return fmt.Errorf("新密码至少 %d 位", policy.PasswordMinLength)
	}
	if len(password) > 1024 {
		return errors.New("密码过长")
	}
	u, _, err := s.UserByUsername(username)
	if err != nil {
		return fmt.Errorf("读取管理员账户: %w", err)
	}
	if u.Role != "admin" || u.Disabled {
		return errors.New("只能修改已启用的管理员账户密码")
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	return s.ResetUserPassword(u.ID, hash, false)
}
