package tailnet

import (
	"context"
	"testing"

	"github.com/ttyob/velin-web-ssh/internal/config"
)

func TestManagerIsDisabledUntilApplied(t *testing.T) {
	manager, err := New(config.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if manager.Enabled() {
		t.Fatal("Tailscale manager started before settings were applied")
	}
	if err := manager.Apply(Settings{}); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.State != "disabled" {
		t.Fatalf("status=%+v", status)
	}
}
