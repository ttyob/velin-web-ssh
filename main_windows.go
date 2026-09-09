//go:build windows

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/ttyob/velin-web-ssh/internal/adminreset"
	"github.com/ttyob/velin-web-ssh/internal/agent"
	"github.com/ttyob/velin-web-ssh/internal/api"
	"github.com/ttyob/velin-web-ssh/internal/config"
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

type desktopServer struct {
	server *http.Server
	store  *store.Store
	agent  *agent.Manager
	once   sync.Once
}

type desktopApp struct{}

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
	return wailsruntime.ClipboardGetText(context.Background())
}

func (d *desktopApp) SetClipboardText(text string) error {
	return wailsruntime.ClipboardSetText(context.Background(), text)
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
	const defaultDesktopAddr = "127.0.0.1:0"

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find desktop executable: %w", err)
	}
	executableDir := filepath.Dir(executable)
	logPath := filepath.Join(executableDir, "velin-web-ssh.log")
	closeLog, err := configureDesktopLogging(executableDir)
	if err != nil {
		return fmt.Errorf("configure desktop logging: %w", err)
	}
	defer func() {
		if runErr != nil {
			slog.Error("desktop initialization failed", "error", runErr)
		}
		closeLog()
	}()
	slog.Info("VelinWebSSH starting", "executable", executable, "log_file", logPath)

	if err = os.Chdir(executableDir); err != nil {
		return fmt.Errorf("set desktop working directory: %w", err)
	}
	if err = godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load desktop .env: %w", err)
	}
	if os.Getenv("VELIN_DATA_DIR") == "" {
		_ = os.Setenv("VELIN_DATA_DIR", executableDir)
	}
	if os.Getenv("VELIN_WEB_DIST") == "" {
		_ = os.Setenv("VELIN_WEB_DIST", filepath.Join(executableDir, "web", "dist"))
	}
	if os.Getenv("VELIN_ADDR") == "" {
		_ = os.Setenv("VELIN_ADDR", defaultDesktopAddr)
	}
	if os.Getenv("VELIN_COOKIE_SECURE") == "" {
		// The desktop WebView talks to the local HTTP listener.
		_ = os.Setenv("VELIN_COOKIE_SECURE", "false")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.Info("desktop configuration loaded", "data_dir", cfg.DataDir, "database", cfg.DatabasePath, "log_file", logPath)
	s, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	vault, err := security.LoadVault(cfg.MasterKeyPath)
	if err != nil {
		_ = s.Close()
		return err
	}
	count, err := s.UserCount()
	if err != nil {
		_ = s.Close()
		return err
	}
	var credentialNotice *desktopCredentialNotice
	if count == 0 {
		password := cfg.AdminPassword
		if password == "" {
			password, err = security.RandomToken(12)
			if err != nil {
				_ = s.Close()
				return err
			}
		}
		hash, hashErr := security.HashPassword(password)
		if hashErr != nil {
			_ = s.Close()
			return hashErr
		}
		if err = s.CreateUser(uuid.NewString(), cfg.AdminUser, hash, "admin"); err != nil {
			_ = s.Close()
			return err
		}
		created, _, lookupErr := s.UserByUsername(cfg.AdminUser)
		if lookupErr != nil {
			_ = s.Close()
			return lookupErr
		}
		if err = s.SetForcePasswordChange(created.ID, true); err != nil {
			_ = s.Close()
			return err
		}
		credentialNotice = &desktopCredentialNotice{username: cfg.AdminUser, password: password}
		slog.Warn("GUI administrator created; credentials are shown in the application", "username", cfg.AdminUser)
	} else if desktopArgumentPresent("--reset-admin-password") {
		result, resetErr := adminreset.Reset(s, cfg.AdminUser, cfg.AdminPassword)
		if resetErr != nil {
			_ = s.Close()
			return resetErr
		}
		credentialNotice = &desktopCredentialNotice{username: result.Username, password: result.Password, reset: true}
		slog.Warn("GUI administrator password reset; credentials are shown in the application", "username", result.Username)
	}
	tailscaleSettings, err := tailnet.LoadSettings(s, vault)
	if err != nil {
		return err
	}
	tailscaleManager, err := tailnet.New(cfg)
	if err != nil {
		return err
	}
	if err = tailscaleManager.Apply(tailscaleSettings); err != nil {
		return err
	}
	defer tailscaleManager.Close()
	manager := terminal.NewManagerWithFFmpeg(s, vault, cfg.DeploymentID, filepath.Join(cfg.DataDir, "recordings"), cfg.FFmpegBinary, tailscaleManager)
	forwardManager := forward.NewManager(s, manager)
	agentManager := agent.NewManager(manager, agent.AIConfig{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIAPIKey, Model: cfg.AIModel})
	appServer := &desktopServer{store: s, agent: agentManager}
	appServer.server = &http.Server{Addr: cfg.Addr, Handler: api.NewWithTailnet(cfg, s, vault, manager, forwardManager, agentManager, tailscaleManager).Router(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	listener, err := net.Listen("tcp4", cfg.Addr)
	if err != nil && errors.Is(err, syscall.EADDRINUSE) {
		slog.Warn("configured desktop port is in use, selecting a free loopback port", "addr", cfg.Addr)
		listener, err = net.Listen("tcp4", "127.0.0.1:0")
	}
	if err != nil {
		appServer.close(context.Background())
		return err
	}
	go func() {
		if serveErr := appServer.server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			slog.Error("desktop web server stopped", "error", serveErr)
		}
	}()
	defer appServer.close(context.Background())
	// Use the address assigned by the listener. This is important when the
	// configured port is 0, and also avoids pointing the WebView at a stale
	// configured port after address normalization.
	serviceURL, err := desktopServiceURL(listener.Addr().String())
	if err != nil {
		return err
	}
	slog.Info("desktop web service listening", "configured_addr", cfg.Addr, "service_url", serviceURL)
	if err = waitForDesktopServer(serviceURL); err != nil {
		return err
	}
	slog.Info("desktop web service ready", "service_url", serviceURL)
	page, err := buildDesktopPage(serviceURL, credentialNotice)
	if err != nil {
		return err
	}

	return wails.Run(&options.App{
		Title:            "VelinWebSSH",
		Width:            1440,
		Height:           900,
		MinWidth:         980,
		MinHeight:        640,
		BackgroundColour: options.NewRGB(16, 20, 24),
		AssetServer: &assetserver.Options{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || (r.URL.Path != "/" && r.URL.Path != "/index.html") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(page)
		})},
		Windows: &windows.Options{Theme: windows.Dark},
		Bind:    []interface{}{&desktopApp{}},
		OnStartup: func(ctx context.Context) {
			copyDesktopCredential(ctx, credentialNotice)
		},
		OnShutdown: func(ctx context.Context) {
			appServer.close(ctx)
		},
	})
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
