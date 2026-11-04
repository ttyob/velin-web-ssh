//go:build windows

package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/ttyob/velin-web-ssh/internal/adminreset"
	"github.com/ttyob/velin-web-ssh/internal/agent"
	"github.com/ttyob/velin-web-ssh/internal/api"
	"github.com/ttyob/velin-web-ssh/internal/config"
	"github.com/ttyob/velin-web-ssh/internal/desktopservice"
	"github.com/ttyob/velin-web-ssh/internal/forward"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/tailnet"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed desktop/dist/index.html
var desktopIndexHTML string

//go:embed desktop/dist/launcher.html
var desktopLauncherHTML []byte

//go:embed all:web/dist
var desktopWebAssets embed.FS

type desktopServer struct {
	server    *http.Server
	store     *store.Store
	agent     *agent.Manager
	terminal  *terminal.Manager
	tailscale *tailnet.Manager
	done      chan error
	once      sync.Once
}

type desktopApp struct {
	ctx             context.Context
	cfg             config.Config
	executable      string
	explicitAddress bool
	manager         *desktopservice.Manager
	resetOnStart    atomic.Bool
	quitting        atomic.Bool
}

func (d *desktopApp) LaunchRDP(address string, port int, username, domain, password string) error {
	address = strings.TrimSpace(address)
	if address == "" || port < 1 || port > 65535 {
		return errors.New("invalid RDP target")
	}
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		address = strings.TrimSuffix(strings.TrimPrefix(address, "["), "]")
	}
	for _, value := range []string{address, username, domain} {
		if strings.ContainsAny(value, "\r\n") {
			return errors.New("invalid RDP connection value")
		}
	}

	target := net.JoinHostPort(address, strconv.Itoa(port))
	credentialTarget := "TERMSRV/" + target
	if password != "" {
		windowsUser := strings.TrimSpace(username)
		if strings.TrimSpace(domain) != "" {
			windowsUser = strings.TrimSpace(domain) + `\` + windowsUser
		}
		if err := exec.Command("cmdkey.exe", "/generic:"+credentialTarget, "/user:"+windowsUser, "/pass:"+password).Run(); err != nil {
			return fmt.Errorf("save Windows RDP credential: %w", err)
		}
	}
	process := exec.Command("mstsc.exe", "/v:"+target)
	if err := process.Start(); err != nil {
		if password != "" {
			_ = exec.Command("cmdkey.exe", "/delete:"+credentialTarget).Run()
		}
		return fmt.Errorf("start Windows Remote Desktop: %w", err)
	}
	if password != "" {
		go func() {
			_ = process.Wait()
			_ = exec.Command("cmdkey.exe", "/delete:"+credentialTarget).Run()
		}()
	}
	return nil
}

func (d *desktopApp) GetClipboardText() (string, error) {
	return wailsruntime.ClipboardGetText(d.ctx)
}

func (d *desktopApp) SetClipboardText(text string) error {
	return wailsruntime.ClipboardSetText(d.ctx, text)
}

type desktopCredentialNotice struct {
	username string
	password string
	reset    bool
}

func (d *desktopServer) close(ctx context.Context) {
	d.once.Do(func() {
		if d.server != nil {
			_ = d.server.Shutdown(ctx)
		}
		if d.agent != nil {
			d.agent.Close()
		}
		if d.terminal != nil {
			d.terminal.CloseAll()
		}
		if d.tailscale != nil {
			_ = d.tailscale.Close()
		}
		if d.store != nil {
			_ = d.store.Close()
		}
	})
}

func main() {
	if err := runDesktop(); err != nil {
		log.Fatal(err)
	}
}

func runDesktop() (runErr error) {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find desktop executable: %w", err)
	}
	directory := filepath.Dir(executable)
	closeLog, err := configureDesktopLogging(directory)
	if err != nil {
		return fmt.Errorf("configure desktop logging: %w", err)
	}
	defer func() {
		if runErr != nil {
			slog.Error("desktop initialization failed", "error", runErr)
		}
		closeLog()
	}()
	if err = os.Chdir(directory); err != nil {
		return err
	}
	if err = godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	explicitAddress := strings.TrimSpace(os.Getenv("VELIN_ADDR")) != ""
	for key, value := range map[string]string{
		"VELIN_DATA_DIR":      directory,
		"VELIN_COOKIE_SECURE": "false",
		"VELIN_RDP_DRIVE_DIR": filepath.Join(os.TempDir(), "velin-rdp-drives"),
	} {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			if err = os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("VELIN_WEB_DIST")) == "" {
		cfg.WebFS, err = fs.Sub(desktopWebAssets, "web/dist")
		if err != nil {
			return err
		}
	}
	if desktopArgumentPresent("--service-worker") {
		return runDesktopWorker(cfg)
	}
	releaseInstance, owned, err := desktopInstance(cfg.DataDir)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	defer releaseInstance()
	app := &desktopApp{cfg: cfg, executable: executable, explicitAddress: explicitAddress}
	app.resetOnStart.Store(desktopArgumentPresent("--reset-admin-password"))
	app.manager = desktopservice.New(desktopservice.Options{Command: app.workerCommand})
	defer app.manager.Close()
	return wails.Run(&options.App{
		Title: "VelinWebSSH", Width: 560, Height: 440, MinWidth: 480, MinHeight: 440,
		BackgroundColour: options.NewRGB(16, 18, 22),
		AssetServer:      &assetserver.Options{Handler: http.HandlerFunc(app.servePage)},
		Windows:          &windows.Options{Theme: windows.Dark},
		Bind:             []interface{}{app},
		OnStartup:        func(ctx context.Context) { app.ctx = ctx },
		OnBeforeClose: func(ctx context.Context) bool {
			status := app.manager.Status()
			if !app.quitting.Load() && status.Mode == "service" && status.State == "running" {
				wailsruntime.EventsEmit(ctx, "service:close-request")
				return true
			}
			return false
		},
		OnShutdown: func(ctx context.Context) { app.manager.Close() },
	})
}

func startDesktopServer(cfg config.Config, allowFallback bool) (*desktopServer, *desktopCredentialNotice, string, error) {
	s, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return nil, nil, "", err
	}
	backend := &desktopServer{store: s, done: make(chan error, 1)}
	fail := func(err error) (*desktopServer, *desktopCredentialNotice, string, error) {
		backend.close(context.Background())
		return nil, nil, "", err
	}
	vault, err := security.LoadVault(cfg.MasterKeyPath)
	if err != nil {
		return fail(err)
	}
	count, err := s.UserCount()
	if err != nil {
		return fail(err)
	}
	var notice *desktopCredentialNotice
	if count == 0 {
		password := cfg.AdminPassword
		if password == "" {
			password, err = security.RandomToken(12)
			if err != nil {
				return fail(err)
			}
		}
		hash, err := security.HashPassword(password)
		if err != nil {
			return fail(err)
		}
		if err = s.CreateUser(uuid.NewString(), cfg.AdminUser, hash, "admin"); err != nil {
			return fail(err)
		}
		user, _, err := s.UserByUsername(cfg.AdminUser)
		if err != nil {
			return fail(err)
		}
		if err = s.SetForcePasswordChange(user.ID, true); err != nil {
			return fail(err)
		}
		notice = &desktopCredentialNotice{username: user.Username, password: password}
	} else if desktopArgumentPresent("--reset-admin-password") {
		result, err := adminreset.Reset(s, cfg.AdminUser, cfg.AdminPassword)
		if err != nil {
			return fail(err)
		}
		notice = &desktopCredentialNotice{username: result.Username, password: result.Password, reset: true}
	}
	if notice != nil {
		content := fmt.Sprintf("username=%s\npassword=%s\n", notice.username, notice.password)
		if err = os.WriteFile(filepath.Join(cfg.DataDir, "initial-admin-credentials.txt"), []byte(content), 0o600); err != nil {
			return fail(err)
		}
	}
	settings, err := tailnet.LoadSettings(s, vault)
	if err != nil {
		return fail(err)
	}
	backend.tailscale, err = tailnet.New(cfg)
	if err != nil {
		return fail(err)
	}
	if err = backend.tailscale.Apply(settings); err != nil {
		return fail(err)
	}
	backend.terminal = terminal.NewManager(s, vault, cfg.DeploymentID, backend.tailscale)
	forwards := forward.NewManager(s, backend.terminal)
	backend.agent = agent.NewManager(backend.terminal, agent.AIConfig{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIAPIKey, Model: cfg.AIModel})
	handler := api.NewWithTailnet(cfg, s, vault, backend.terminal, forwards, backend.agent, backend.tailscale).Router()
	backend.server = &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	listener, err := net.Listen("tcp4", cfg.Addr)
	if err != nil && allowFallback && errors.Is(err, syscall.EADDRINUSE) {
		slog.Warn("desktop port is in use, selecting a free loopback port", "addr", cfg.Addr)
		listener, err = net.Listen("tcp4", "127.0.0.1:0")
	}
	if err != nil {
		return fail(fmt.Errorf("监听 %s 失败: %w", cfg.Addr, err))
	}
	url, err := desktopServiceURL(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return fail(err)
	}
	go func() { backend.done <- backend.server.Serve(listener) }()
	if err = waitForDesktopServer(url); err != nil {
		return fail(err)
	}
	slog.Info("desktop web service ready", "configured_addr", cfg.Addr, "service_url", url)
	return backend, notice, url, nil
}

func runDesktopWorker(cfg config.Config) error {
	backend, notice, url, err := startDesktopServer(cfg, desktopArgumentPresent("--worker-window"))
	if err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(desktopservice.Ready{Error: err.Error()})
		return err
	}
	defer backend.close(context.Background())
	ready := desktopservice.Ready{URL: url}
	if notice != nil {
		ready.Credentials = &desktopservice.Credentials{Username: notice.username, Password: notice.password, Reset: notice.reset}
	}
	if err = json.NewEncoder(os.Stdout).Encode(ready); err != nil {
		return err
	}
	stop := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if scanner.Text() == "stop" {
				break
			}
		}
		close(stop)
	}()
	select {
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		backend.close(ctx)
		return nil
	case err := <-backend.done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func desktopArgumentPresent(name string) bool {
	for _, argument := range os.Args[1:] {
		if argument == name {
			return true
		}
	}
	return false
}

func buildDesktopPage(serviceURL string, notice *desktopCredentialNotice) ([]byte, error) {
	serviceJSON, err := json.Marshal(serviceURL)
	if err != nil {
		return nil, fmt.Errorf("encode desktop service URL: %w", err)
	}
	credentialsJSON := []byte("null")
	if notice != nil {
		credentialsJSON, err = json.Marshal(map[string]any{
			"reset":    notice.reset,
			"username": notice.username,
			"password": notice.password,
		})
		if err != nil {
			return nil, fmt.Errorf("encode desktop credentials: %w", err)
		}
	}
	page := strings.Replace(desktopIndexHTML, "__VELIN_DESKTOP_URL_JSON__", string(serviceJSON), 1)
	page = strings.Replace(page, "__VELIN_DESKTOP_CREDENTIALS_JSON__", string(credentialsJSON), 1)
	return []byte(page), nil
}

func copyDesktopCredential(ctx context.Context, notice *desktopCredentialNotice) {
	if notice == nil {
		return
	}
	if err := wailsruntime.ClipboardSetText(ctx, notice.password); err != nil {
		slog.Error("copy desktop administrator password", "error", err)
	}
}

func desktopServiceURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid desktop service address %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/", nil
}

func waitForDesktopServer(serviceURL string) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for attempt := 0; attempt < 40; attempt++ {
		response, err := client.Get(serviceURL + "api/health/live")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("desktop web service did not become ready at %s", serviceURL)
}

func configureDesktopLogging(logDir string) (func(), error) {
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return func() {}, err
	}
	file, err := os.OpenFile(filepath.Join(logDir, "velin-web-ssh.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return func() {}, err
	}
	log.SetOutput(file)
	slog.SetDefault(slog.New(slog.NewTextHandler(file, nil)))
	return func() {
		_ = file.Sync()
		_ = file.Close()
	}, nil
}
