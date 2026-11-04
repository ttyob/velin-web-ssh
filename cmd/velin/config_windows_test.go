package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsServerConfig(t *testing.T) {
	for _, external := range []bool{false, true} {
		name := "portable defaults"
		if external {
			name = "explicit configuration"
		}
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			for _, key := range []string{"VELIN_ADDR", "VELIN_DATA_DIR", "VELIN_COOKIE_SECURE", "VELIN_WEB_DIST", "VELIN_RDP_DRIVE_DIR", "VELIN_HOST_PORT_ADDR", "VELIN_DESKTOP_PROXY_ADDR", "VELIN_RDP_RESIZE_METHOD", "VELIN_EMBED_ORIGINS", "VELIN_TRUSTED_PROXY_CIDRS"} {
				t.Setenv(key, "")
			}
			directory := filepath.Join(t.TempDir(), "portable server")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if external {
				// An empty environment variable takes precedence in godotenv,
				// so unset these to exercise loading the EXE's .env file.
				for _, key := range []string{"VELIN_DATA_DIR", "VELIN_COOKIE_SECURE", "VELIN_WEB_DIST"} {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
				env := "VELIN_ADDR=0.0.0.0:9000\nVELIN_DATA_DIR=custom-data\nVELIN_COOKIE_SECURE=true\nVELIN_WEB_DIST=external-web\n"
				if err := os.WriteFile(filepath.Join(directory, ".env"), []byte(env), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("VELIN_ADDR", "127.0.0.1:9001")
			}
			cfg, err := loadWindowsServerConfig(directory)
			if err != nil {
				t.Fatal(err)
			}
			if external {
				if cfg.Addr != "127.0.0.1:9001" || cfg.DataDir != "custom-data" || !cfg.CookieSecure || cfg.WebDist != "external-web" || cfg.WebFS != nil {
					t.Fatalf("explicit settings were not preserved: addr=%q data=%q secure=%v web=%q embedded=%v", cfg.Addr, cfg.DataDir, cfg.CookieSecure, cfg.WebDist, cfg.WebFS != nil)
				}
				return
			}
			if cfg.Addr != "0.0.0.0:8377" || cfg.DataDir != directory || cfg.CookieSecure || cfg.DatabasePath != filepath.Join(directory, "velin.db") {
				t.Fatalf("unexpected portable defaults: addr=%q data=%q secure=%v database=%q", cfg.Addr, cfg.DataDir, cfg.CookieSecure, cfg.DatabasePath)
			}
			index, err := fs.ReadFile(cfg.WebFS, "index.html")
			if err != nil || !strings.Contains(string(index), "<html") {
				t.Fatalf("embedded frontend unavailable: %v", err)
			}
			if _, err := os.Stat(filepath.Join(directory, "deployment_id")); err != nil {
				t.Fatalf("data was not stored beside the EXE: %v", err)
			}
		})
	}
}
