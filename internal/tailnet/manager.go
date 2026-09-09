package tailnet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ttyob/velin-web-ssh/internal/config"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"tailscale.com/tsnet"
)

const settingKey = "tailscale_config"

type Settings struct {
	Enabled    bool
	Hostname   string
	ControlURL string
	AuthKey    string `json:"-"`
}

type Update struct {
	Enabled      bool   `json:"enabled"`
	Hostname     string `json:"hostname"`
	ControlURL   string `json:"controlURL"`
	AuthKey      string `json:"authKey"`
	ClearAuthKey bool   `json:"clearAuthKey"`
}

type storedSettings struct {
	Enabled          bool   `json:"enabled"`
	Hostname         string `json:"hostname"`
	ControlURL       string `json:"controlURL"`
	AuthKeyEncrypted string `json:"authKeyEncrypted"`
}

type Manager struct {
	mu      sync.RWMutex
	dir     string
	server  *tsnet.Server
	enabled bool
}

type Status struct {
	Enabled        bool     `json:"enabled"`
	State          string   `json:"state"`
	TUN            bool     `json:"tun"`
	IPs            []string `json:"ips,omitempty"`
	MagicDNSSuffix string   `json:"magicDnsSuffix,omitempty"`
	Version        string   `json:"version,omitempty"`
	AuthURL        string   `json:"authUrl,omitempty"`
	Health         []string `json:"health,omitempty"`
}

func New(cfg config.Config) (*Manager, error) {
	return &Manager{dir: filepath.Join(cfg.DataDir, "tailscale")}, nil
}

func LoadSettings(s *store.Store, vault *security.Vault) (Settings, error) {
	var stored storedSettings
	if err := s.SystemSetting(settingKey, &stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Settings{Hostname: "velin"}, nil
		}
		return Settings{}, err
	}
	settings := Settings{Enabled: stored.Enabled, Hostname: stored.Hostname, ControlURL: stored.ControlURL}
	if settings.Hostname == "" {
		settings.Hostname = "velin"
	}
	if stored.AuthKeyEncrypted != "" {
		var err error
		settings.AuthKey, err = vault.Decrypt(stored.AuthKeyEncrypted)
		if err != nil {
			return Settings{}, fmt.Errorf("decrypt Tailscale Auth Key: %w", err)
		}
	}
	return settings, nil
}

func SaveSettings(s *store.Store, vault *security.Vault, settings Settings) error {
	stored := storedSettings{Enabled: settings.Enabled, Hostname: settings.Hostname, ControlURL: settings.ControlURL}
	if strings.TrimSpace(settings.AuthKey) != "" {
		encrypted, err := vault.Encrypt(strings.TrimSpace(settings.AuthKey))
		if err != nil {
			return fmt.Errorf("encrypt Tailscale Auth Key: %w", err)
		}
		stored.AuthKeyEncrypted = encrypted
	}
	return s.SaveSystemSetting(settingKey, stored)
}

func (m *Manager) Apply(settings Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		_ = m.server.Close()
		m.server = nil
	}
	m.enabled = false
	if !settings.Enabled {
		return nil
	}
	hostname := strings.TrimSpace(settings.Hostname)
	if hostname == "" {
		hostname = "velin"
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return fmt.Errorf("create Tailscale state directory: %w", err)
	}
	if err := os.Chmod(m.dir, 0o700); err != nil {
		return fmt.Errorf("protect Tailscale state directory: %w", err)
	}
	server := &tsnet.Server{
		Dir:        m.dir,
		Hostname:   hostname,
		AuthKey:    strings.TrimSpace(settings.AuthKey),
		ControlURL: strings.TrimSpace(settings.ControlURL),
		UserLogf: func(format string, args ...any) {
			slog.Info(fmt.Sprintf(format, args...))
		},
		Logf: func(format string, args ...any) {
			slog.Debug(fmt.Sprintf(format, args...))
		},
	}
	if err := server.Start(); err != nil {
		_ = server.Close()
		return err
	}
	m.server = server
	m.enabled = true
	return nil
}

func (m *Manager) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled && m.server != nil
}

func (m *Manager) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.enabled || m.server == nil {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return m.server.Dial(ctx, network, address)
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.enabled || m.server == nil {
		return Status{State: "disabled"}, nil
	}
	lc, err := m.server.LocalClient()
	if err != nil {
		return Status{Enabled: true, State: "error"}, err
	}
	value, err := lc.Status(ctx)
	if err != nil {
		return Status{Enabled: true, State: "error"}, err
	}
	out := Status{Enabled: true, State: value.BackendState, TUN: value.TUN, Version: value.Version, AuthURL: value.AuthURL}
	for _, ip := range value.TailscaleIPs {
		out.IPs = append(out.IPs, ip.String())
	}
	if value.CurrentTailnet != nil {
		out.MagicDNSSuffix = value.CurrentTailnet.MagicDNSSuffix
	}
	out.Health = append([]string(nil), value.Health...)
	return out, nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == nil {
		return nil
	}
	err := m.server.Close()
	m.server = nil
	m.enabled = false
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
