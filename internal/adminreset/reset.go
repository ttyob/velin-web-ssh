package adminreset

import (
	"fmt"
	"strings"

	"velin-webssh/internal/security"
	"velin-webssh/internal/store"
)

type Result struct {
	Username  string
	Password  string
	Generated bool
}

// Reset changes an existing administrator password and revokes all of that
// account's login sessions. An empty password generates a temporary password.
func Reset(s *store.Store, username, password string) (Result, error) {
	username = strings.TrimSpace(username)
	user, _, err := s.UserByUsername(username)
	if err != nil {
		return Result{}, fmt.Errorf("find administrator %q: %w", username, err)
	}
	if user.Role != "admin" {
		return Result{}, fmt.Errorf("user %q is not an administrator", user.Username)
	}
	generated := password == ""
	if generated {
		password, err = security.RandomToken(12)
		if err != nil {
			return Result{}, fmt.Errorf("generate temporary password: %w", err)
		}
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return Result{}, fmt.Errorf("hash administrator password: %w", err)
	}
	if err := s.ResetUserPassword(user.ID, hash, true); err != nil {
		return Result{}, fmt.Errorf("reset administrator password: %w", err)
	}
	return Result{Username: user.Username, Password: password, Generated: generated}, nil
}
