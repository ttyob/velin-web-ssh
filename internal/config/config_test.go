package config

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReadsDotEnvAndPreservesEnvironment(t *testing.T) {
	workDir := t.TempDir()
	changeWorkingDirectory(t, workDir)
	preserveAndUnsetEnv(t, "VELIN_DATA_DIR")
	preserveAndUnsetEnv(t, "VELIN_ADMIN_USER")
	preserveAndUnsetEnv(t, "VELIN_ADDR")
	t.Setenv("VELIN_ADMIN_USER", "environment-admin")

	content := "VELIN_DATA_DIR=state\nVELIN_ADMIN_USER=file-admin\n"
	if err := os.WriteFile(filepath.Join(workDir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "state" {
		t.Fatalf("data directory=%q", cfg.DataDir)
	}
	if cfg.AdminUser != "environment-admin" {
		t.Fatalf("admin user=%q", cfg.AdminUser)
	}
	if cfg.Addr != "0.0.0.0:8377" {
		t.Fatalf("listen address=%q", cfg.Addr)
	}
}

func TestLoadRejectsInvalidDotEnv(t *testing.T) {
	workDir := t.TempDir()
	changeWorkingDirectory(t, workDir)
	if err := os.WriteFile(filepath.Join(workDir, ".env"), []byte("invalid line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "load .env") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadRDPResizeMethod(t *testing.T) {
	workDir := t.TempDir()
	changeWorkingDirectory(t, workDir)
	t.Setenv("VELIN_DATA_DIR", filepath.Join(workDir, "data"))
	t.Setenv("VELIN_RDP_RESIZE_METHOD", "reconnect")
	t.Setenv("VELIN_RDP_DISABLE_GFX", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RDPResizeMethod != "reconnect" {
		t.Fatalf("RDP resize method=%q", cfg.RDPResizeMethod)
	}
	if !cfg.RDPDisableGFX {
		t.Fatal("RDP GFX disable flag was ignored")
	}

	t.Setenv("VELIN_RDP_RESIZE_METHOD", "invalid")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "VELIN_RDP_RESIZE_METHOD") {
		t.Fatalf("invalid resize method error=%v", err)
	}
}

func TestParseOriginsRejectsWildcardsAndPaths(t *testing.T) {
	origins, err := parseOrigins("https://nas.example.com, http://127.0.0.1:8080")
	if err != nil || len(origins) != 2 {
		t.Fatalf("origins=%v err=%v", origins, err)
	}
	for _, invalid := range []string{"*", "https://nas.example.com/path", "javascript:alert(1)"} {
		if _, err = parseOrigins(invalid); err == nil {
			t.Fatalf("invalid origin %q was accepted", invalid)
		}
	}
}

func TestParseCIDRs(t *testing.T) {
	networks, err := parseCIDRs("127.0.0.1, 10.0.0.0/8")
	if err != nil || len(networks) != 2 || !networks[0].Contains(net.ParseIP("127.0.0.1")) || !networks[1].Contains(net.ParseIP("10.1.2.3")) {
		t.Fatalf("networks=%v err=%v", networks, err)
	}
	if _, err = parseCIDRs("not-a-network"); err == nil {
		t.Fatal("invalid proxy network was accepted")
	}
}

func changeWorkingDirectory(t *testing.T, path string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

func preserveAndUnsetEnv(t *testing.T, key string) {
	t.Helper()
	value, exists := os.LookupEnv(key)
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
	_ = os.Unsetenv(key)
}
