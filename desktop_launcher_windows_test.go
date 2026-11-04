package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttyob/velin-web-ssh/internal/config"
	"github.com/ttyob/velin-web-ssh/internal/desktopservice"
)

func TestDesktopWorkerModeAndEnvironment(t *testing.T) {
	t.Setenv("VELIN_ADDR", "0.0.0.0:9999")
	app := &desktopApp{executable: filepath.Join(t.TempDir(), "directory with spaces", "velin.exe"), cfg: config.Config{Addr: "0.0.0.0:9000"}}
	for _, test := range []struct {
		mode, address string
		explicit      bool
	}{
		{"window", "127.0.0.1:0", false},
		{"service", "0.0.0.0:8377", false},
		{"window", "0.0.0.0:9000", true},
		{"service", "0.0.0.0:9000", true},
	} {
		app.explicitAddress = test.explicit
		cmd := app.workerCommand(test.mode)
		count := 0
		for _, value := range cmd.Env {
			if strings.HasPrefix(value, "VELIN_ADDR=") {
				count++
				if value != "VELIN_ADDR="+test.address {
					t.Fatalf("unexpected worker address: %q", value)
				}
			}
		}
		if count != 1 || cmd.Stdin != nil || cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
			t.Fatal("invalid worker isolation configuration")
		}
	}
}

func TestDesktopAutoStart(t *testing.T) {
	app := &desktopApp{executable: filepath.Join(t.TempDir(), "directory with spaces", "velin.exe")}
	defer app.SetAutoStart(false)
	if err := app.SetAutoStart(true); err != nil {
		t.Fatal(err)
	}
	if !app.autoStartEnabled() {
		t.Fatal("auto-start registration was not saved")
	}
	if command := app.autoStartCommand(); !strings.HasPrefix(command, `"`) || !strings.HasSuffix(command, " --service --minimized") {
		t.Fatalf("invalid auto-start command: %s", command)
	}
	other := &desktopApp{executable: filepath.Join(t.TempDir(), "velin.exe")}
	if other.autoStartName() == app.autoStartName() || other.autoStartEnabled() {
		t.Fatal("different installations share auto-start settings")
	}
	upgraded := &desktopApp{executable: filepath.Join(filepath.Dir(app.executable), "velin-new-version.exe")}
	if upgraded.autoStartName() != app.autoStartName() || !upgraded.autoStartEnabled() {
		t.Fatal("upgrading the EXE lost the enabled auto-start setting")
	}
	if err := app.SetAutoStart(false); err != nil {
		t.Fatal(err)
	}
	if app.autoStartEnabled() {
		t.Fatal("auto-start registration was not removed")
	}
}

func TestDesktopLauncherRouting(t *testing.T) {
	app := &desktopApp{manager: desktopservice.New(desktopservice.Options{})}
	response := httptest.NewRecorder()
	app.servePage(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "启动方式") || !strings.Contains(response.Body.String(), "服务管理") {
		t.Fatal("launcher was not served")
	}
	response = httptest.NewRecorder()
	app.servePage(response, httptest.NewRequest("GET", "/window.html", nil))
	if response.Code != 303 || response.Header().Get("Location") != "/" {
		t.Fatal("window page opened before the service was ready")
	}
}

func TestDesktopSingleInstance(t *testing.T) {
	directory := t.TempDir()
	release, owned, err := desktopInstance(directory)
	if err != nil || !owned {
		t.Fatalf("cannot own application instance: %v", err)
	}
	defer release()
	otherRelease, owned, err := desktopInstance(directory)
	if err != nil || owned {
		t.Fatalf("duplicate instance was not detected: %v", err)
	}
	otherRelease()
	separateRelease, owned, err := desktopInstance(t.TempDir())
	if err != nil || !owned {
		t.Fatalf("separate data directory was blocked: %v", err)
	}
	separateRelease()
}
