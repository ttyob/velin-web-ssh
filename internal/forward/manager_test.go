package forward

import (
	"path/filepath"
	"testing"

	"github.com/ttyob/velin-web-ssh/internal/store"
)

func TestValidateForward(t *testing.T) {
	base := store.PortForward{Name: "ssh", HostID: "host", Kind: "local", ListenAddress: "127.0.0.1", ListenPort: 8080, TargetHost: "127.0.0.1", TargetPort: 22}
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	dynamic := base
	dynamic.Kind, dynamic.TargetHost, dynamic.TargetPort = "dynamic", "", 0
	if err := validate(dynamic); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*store.PortForward){
		"public listener":     func(value *store.PortForward) { value.ListenAddress = "0.0.0.0" },
		"invalid listen port": func(value *store.PortForward) { value.ListenPort = 65536 },
		"invalid target port": func(value *store.PortForward) { value.TargetPort = 0 },
		"unknown kind":        func(value *store.PortForward) { value.Kind = "reverse-dynamic" },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			if err := validate(value); err == nil {
				t.Fatal("invalid forward accepted")
			}
		})
	}
}

func TestSaveForwardRejectsForeignHost(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "forward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, id := range []string{"u1", "u2"} {
		if err = database.CreateUser(id, id, "hash", "user"); err != nil {
			t.Fatal(err)
		}
	}
	if err = database.SaveHost(store.Host{ID: "foreign", UserID: "u2", Name: "foreign", Address: "127.0.0.1", Port: 22, Username: "root"}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(database, nil)
	value := store.PortForward{Name: "ssh", HostID: "foreign", Kind: "local", ListenAddress: "127.0.0.1", ListenPort: 8080, TargetHost: "127.0.0.1", TargetPort: 22}
	if _, err = manager.Save("u1", value); err == nil {
		t.Fatal("foreign host forward was saved")
	}
}
