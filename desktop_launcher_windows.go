package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ttyob/velin-web-ssh/internal/desktopservice"
	"github.com/ttyob/velin-web-ssh/internal/store"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows/registry"
)

type desktopStartup struct {
	Mode      string `json:"mode"`
	Minimized bool   `json:"minimized"`
	AutoStart bool   `json:"autoStart"`
	Username  string `json:"username"`
	DataDir   string `json:"dataDir"`
}

func (d *desktopApp) workerCommand(mode string) *exec.Cmd {
	args := []string{"--service-worker"}
	address := d.cfg.Addr
	if mode == "window" {
		args = append(args, "--worker-window")
		if !d.explicitAddress {
			address = "127.0.0.1:0"
		}
	} else if !d.explicitAddress {
		address = "0.0.0.0:8377"
	}
	if d.resetOnStart.Load() {
		args = append(args, "--reset-admin-password")
	}
	cmd := exec.Command(d.executable, args...)
	cmd.Dir = filepath.Dir(d.executable)
	cmd.Env = append(withoutEnvironment(os.Environ(), "VELIN_ADDR"), "VELIN_ADDR="+address)
	cmd.Stderr = log.Writer()
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd
}

func withoutEnvironment(values []string, key string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		name, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(name, key) {
			result = append(result, value)
		}
	}
	return result
}

func (d *desktopApp) servePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/", "/index.html":
		_, _ = w.Write(desktopLauncherHTML)
	case "/window.html":
		status := d.manager.Status()
		if status.State != "running" || status.Mode != "window" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		credentials, err := d.readInitialCredentials()
		if err != nil {
			slog.Warn("read desktop initial credentials", "error", err)
		}
		var notice *desktopCredentialNotice
		if credentials != nil {
			notice = &desktopCredentialNotice{username: credentials.Username, password: credentials.Password, reset: credentials.Reset}
		}
		page, err := buildDesktopPage(status.URL, notice)
		if err != nil {
			http.Error(w, "无法加载工作台", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(page)
	default:
		http.NotFound(w, r)
	}
}

func (d *desktopApp) GetStartup() desktopStartup {
	mode := ""
	if desktopArgumentPresent("--service") {
		mode = "service"
	} else if desktopArgumentPresent("--window") || d.resetOnStart.Load() {
		mode = "window"
	}
	return desktopStartup{Mode: mode, Minimized: desktopArgumentPresent("--minimized"), AutoStart: d.autoStartEnabled(), Username: d.cfg.AdminUser, DataDir: d.cfg.DataDir}
}

func (d *desktopApp) GetServiceStatus() desktopservice.Status {
	return d.manager.Status()
}

func (d *desktopApp) StartMode(mode string) (desktopservice.Status, error) {
	if err := d.requireLauncher(); err != nil {
		return d.manager.Status(), err
	}
	status, err := d.manager.Start(mode)
	if err != nil {
		return status, err
	}
	d.resetOnStart.Store(false)
	if mode == "window" {
		wailsruntime.WindowSetMinSize(d.ctx, 980, 640)
		wailsruntime.WindowSetSize(d.ctx, 1440, 900)
	} else {
		wailsruntime.WindowSetMinSize(d.ctx, 480, 520)
		wailsruntime.WindowSetSize(d.ctx, 560, 600)
	}
	wailsruntime.WindowCenter(d.ctx)
	return status, nil
}

func (d *desktopApp) StopService() (desktopservice.Status, error) {
	if d.manager.Status().Mode == "window" {
		return d.manager.Status(), errors.New("请在服务模式下管理服务")
	}
	return d.manager.Stop()
}

func (d *desktopApp) RestartService() (desktopservice.Status, error) {
	if _, err := d.StopService(); err != nil {
		return d.manager.Status(), err
	}
	return d.manager.Start("service")
}

func (d *desktopApp) SelectStartupMode() error {
	if err := d.requireLauncher(); err != nil {
		return err
	}
	if _, err := d.manager.Stop(); err != nil {
		return err
	}
	wailsruntime.WindowSetMinSize(d.ctx, 480, 440)
	wailsruntime.WindowSetSize(d.ctx, 560, 440)
	wailsruntime.WindowCenter(d.ctx)
	return nil
}

func (d *desktopApp) GetInitialCredentials() (*desktopservice.Credentials, error) {
	if err := d.requireLauncher(); err != nil {
		return nil, err
	}
	return d.readInitialCredentials()
}

func (d *desktopApp) readInitialCredentials() (*desktopservice.Credentials, error) {
	s, err := d.openAdministratorStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return desktopservice.InitialCredentials(s, d.cfg.AdminUser, d.cfg.DataDir, d.manager.Credentials())
}

func (d *desktopApp) ChangeAdminPassword(password, confirmation string) error {
	if err := d.requireLauncher(); err != nil {
		return err
	}
	s, err := d.openAdministratorStore()
	if err != nil {
		return err
	}
	defer s.Close()
	if err = desktopservice.ChangePassword(s, d.cfg.AdminUser, password, confirmation); err != nil {
		return err
	}
	d.manager.ClearCredentials()
	if err = os.Remove(filepath.Join(d.cfg.DataDir, "initial-admin-credentials.txt")); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("remove obsolete initial credentials", "error", err)
	}
	return nil
}

func (d *desktopApp) openAdministratorStore() (*store.Store, error) {
	if _, err := os.Stat(d.cfg.DatabasePath); err != nil {
		return nil, errors.New("请先启动一次服务以创建管理员账户")
	}
	return store.Open(d.cfg.DatabasePath)
}

func (d *desktopApp) OpenBrowser() error {
	status := d.manager.Status()
	if status.State != "running" {
		return errors.New("请先启动服务")
	}
	wailsruntime.BrowserOpenURL(d.ctx, status.URL)
	return nil
}

func (d *desktopApp) Minimize() {
	wailsruntime.WindowMinimise(d.ctx)
}

func (d *desktopApp) QuitService() {
	d.quitting.Store(true)
	wailsruntime.Quit(d.ctx)
}

func (d *desktopApp) autoStartName() string {
	directory := d.cfg.DataDir
	if directory == "" {
		directory = filepath.Dir(d.executable)
	}
	if absolute, err := filepath.Abs(directory); err == nil {
		directory = absolute
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(directory))))
	return "VelinWebSSH_" + hex.EncodeToString(digest[:6])
}

func (d *desktopApp) autoStartCommand() string {
	return syscall.EscapeArg(d.executable) + " --service --minimized"
}

const desktopAutoStartKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func (d *desktopApp) autoStartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, desktopAutoStartKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(d.autoStartName())
	if err != nil || value == "" {
		return false
	}
	if value != d.autoStartCommand() {
		// A versioned EXE may have been replaced in the same data directory.
		return d.SetAutoStart(true) == nil
	}
	return true
}

func (d *desktopApp) SetAutoStart(enabled bool) error {
	if err := d.requireLauncher(); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, desktopAutoStartKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("设置开机自启失败: %w", err)
	}
	defer key.Close()
	if enabled {
		return key.SetStringValue(d.autoStartName(), d.autoStartCommand())
	}
	err = key.DeleteValue(d.autoStartName())
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func (d *desktopApp) requireLauncher() error {
	if d.manager != nil && d.manager.Status().Mode == "window" {
		return errors.New("请在启动选择页或服务管理窗口中执行此操作")
	}
	return nil
}
