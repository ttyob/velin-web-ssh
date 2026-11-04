package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/ttyob/velin-web-ssh/internal/config"
	webassets "github.com/ttyob/velin-web-ssh/web"
)

func loadServerConfig() (config.Config, error) {
	executable, err := os.Executable()
	if err != nil {
		return config.Config{}, fmt.Errorf("find server executable: %w", err)
	}
	return loadWindowsServerConfig(filepath.Dir(executable))
}

func loadWindowsServerConfig(directory string) (config.Config, error) {
	if err := os.Chdir(directory); err != nil {
		return config.Config{}, fmt.Errorf("set server working directory: %w", err)
	}
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, fmt.Errorf("load server .env: %w", err)
	}
	defaults := map[string]string{
		"VELIN_DATA_DIR":      directory,
		"VELIN_COOKIE_SECURE": "false",
		"VELIN_RDP_DRIVE_DIR": filepath.Join(os.TempDir(), "velin-rdp-drives"),
	}
	for key, value := range defaults {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			if err := os.Setenv(key, value); err != nil {
				return config.Config{}, fmt.Errorf("set %s: %w", key, err)
			}
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, err
	}
	if strings.TrimSpace(os.Getenv("VELIN_WEB_DIST")) == "" {
		cfg.WebFS, err = webassets.Frontend()
		if err != nil {
			return config.Config{}, fmt.Errorf("load embedded server frontend: %w", err)
		}
	}
	return cfg, nil
}
