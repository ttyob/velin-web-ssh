package remotedesktop

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

func testManager(t *testing.T) (*Manager, *store.Store, *security.Vault) {
	t.Helper()
	directory := t.TempDir()
	database, err := store.Open(filepath.Join(directory, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	vault, err := security.LoadVault(filepath.Join(directory, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(database, vault, nil, "127.0.0.1:4822", "127.0.0.1", "", "display-update", false), database, vault
}

func TestRDPResizeMethodDefaultsAndOverrides(t *testing.T) {
	manager, _, _ := testManager(t)
	if manager.rdpResizeMethod != "display-update" {
		t.Fatalf("default resize method=%q", manager.rdpResizeMethod)
	}
	manager = NewManager(manager.store, manager.vault, nil, "127.0.0.1:4822", "127.0.0.1", "", "reconnect", true)
	if manager.rdpResizeMethod != "reconnect" {
		t.Fatalf("configured resize method=%q", manager.rdpResizeMethod)
	}
	if !manager.rdpDisableGFX {
		t.Fatal("configured GFX disable flag was ignored")
	}
}

func TestDrivePathIsolatedAndCleaned(t *testing.T) {
	manager, _, _ := testManager(t)
	manager.rdpDriveDir = t.TempDir()

	path, cleanup, err := manager.prepareDrivePath(store.Host{RDPDrive: true})
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("drive path is empty")
	}
	if err = os.WriteFile(filepath.Join(path, "probe.txt"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("drive path still exists after cleanup: %v", err)
	}
}

func TestDesktopSessionIsOneTimeAndOwned(t *testing.T) {
	manager, database, vault := testManager(t)
	if err := database.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	encrypted, err := vault.Encrypt("vnc-password")
	if err != nil {
		t.Fatal(err)
	}
	host := store.Host{ID: "vnc-host", UserID: "u1", Name: "VNC", Address: "127.0.0.1", Port: 5900, Protocol: "vnc", PasswordEnc: encrypted, ConnectTimeout: 3}
	if err = database.SaveHost(host); err != nil {
		t.Fatal(err)
	}
	session, err := manager.Create(context.Background(), "u1", CreateRequest{HostID: host.ID, Width: 1920, Height: 1080})
	if err != nil {
		t.Fatal(err)
	}
	if session.Protocol != "vnc" || session.Password != "vnc-password" || !strings.HasPrefix(session.WebSocketPath, "/ws/desktop/vnc/") {
		t.Fatalf("unexpected session: %+v", session)
	}
	if _, err = manager.consume("u2", session.ID, "vnc"); err == nil {
		t.Fatal("another user consumed desktop session")
	}
	if _, err = manager.consume("u1", session.ID, "vnc"); err != nil {
		t.Fatalf("owner could not consume session: %v", err)
	}
	if _, err = manager.consume("u1", session.ID, "vnc"); err == nil {
		t.Fatal("desktop session token was reused")
	}
}

func TestDesktopSessionExpiryAndCredentialKind(t *testing.T) {
	manager, database, vault := testManager(t)
	if err := database.CreateUser("u1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	encrypted, err := vault.Encrypt("private-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = database.SaveCredential(store.Credential{ID: "key", UserID: "u1", Name: "key", Kind: "key", Secret: encrypted}); err != nil {
		t.Fatal(err)
	}
	host := store.Host{ID: "rdp-host", UserID: "u1", Name: "RDP", Address: "127.0.0.1", Port: 3389, Protocol: "rdp", Username: "admin", CredentialID: "key", ConnectTimeout: 3}
	if err = database.SaveHost(host); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Create(context.Background(), "u1", CreateRequest{HostID: host.ID}); err == nil || !strings.Contains(err.Error(), "password credential") {
		t.Fatalf("key credential error=%v", err)
	}
	host.CredentialID = ""
	host.PasswordEnc, err = vault.Encrypt("rdp-password")
	if err != nil || database.SaveHost(host) != nil {
		t.Fatal(err)
	}
	now := time.Now()
	manager.now = func() time.Time { return now }
	session, err := manager.Create(context.Background(), "u1", CreateRequest{HostID: host.ID})
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return now.Add(pendingLifetime + time.Second) }
	if _, err = manager.consume("u1", session.ID, "rdp"); err == nil {
		t.Fatal("expired desktop session was accepted")
	}
}

func TestDesktopTargetAndOrigin(t *testing.T) {
	manager, _, _ := testManager(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = connection.Close()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	if _, err = manager.Test(context.Background(), "u1", store.Host{Address: address.IP.String(), Port: address.Port, ConnectTimeout: 3}); err != nil {
		t.Fatalf("test target: %v", err)
	}

	request := httptest.NewRequest("GET", "http://velin.example/ws", nil)
	request.Host = "velin.example"
	request.Header.Set("Origin", "https://velin.example")
	if !sameOrigin(request) {
		t.Fatal("same origin was rejected")
	}
	request.Header.Set("Origin", "https://attacker.example")
	if sameOrigin(request) {
		t.Fatal("cross origin was accepted")
	}
}
