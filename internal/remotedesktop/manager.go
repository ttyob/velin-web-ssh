package remotedesktop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ttyob/velin-web-ssh/internal/netdial"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
	guac "github.com/wwt/guac"
)

var ErrCredentialRequired = errors.New("desktop credential required")

const pendingLifetime = time.Minute

type Manager struct {
	store           *store.Store
	vault           *security.Vault
	terminals       *terminal.Manager
	guacdAddr       string
	proxyAddr       string
	rdpDriveDir     string
	rdpResizeMethod string
	rdpDisableGFX   bool
	dialer          netdial.Dialer
	mu              sync.Mutex
	pending         map[string]pendingSession
	now             func() time.Time
}

type CreateRequest struct {
	HostID string `json:"hostID"`
	Secret string `json:"secret"`
	Native bool   `json:"native"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	DPI    int    `json:"dpi"`
}

type Session struct {
	ID            string `json:"id"`
	HostID        string `json:"hostID"`
	Protocol      string `json:"protocol"`
	WebSocketPath string `json:"websocketPath"`
	Password      string `json:"password,omitempty"`
	ReadOnly      bool   `json:"readOnly"`
}

type pendingSession struct {
	userID    string
	host      store.Host
	secret    string
	width     int
	height    int
	dpi       int
	expiresAt time.Time
}

func NewManager(s *store.Store, vault *security.Vault, terminals *terminal.Manager, guacdAddr, proxyAddr, driveDir, resizeMethod string, disableGFX bool) *Manager {
	resizeMethod = strings.TrimSpace(resizeMethod)
	if resizeMethod == "" {
		resizeMethod = "display-update"
	}
	return &Manager{
		store: s, vault: vault, terminals: terminals,
		guacdAddr: guacdAddr, proxyAddr: proxyAddr,
		rdpDriveDir: strings.TrimSpace(driveDir), rdpResizeMethod: resizeMethod,
		rdpDisableGFX: disableGFX, dialer: netdial.Direct{},
		pending: make(map[string]pendingSession), now: time.Now,
	}
}

func (m *Manager) SetDialer(dialer netdial.Dialer) {
	if dialer != nil {
		m.dialer = dialer
	}
}

func (m *Manager) Create(ctx context.Context, userID string, request CreateRequest) (Session, error) {
	host, err := m.store.Host(userID, request.HostID)
	if err != nil {
		return Session{}, err
	}
	if host.Protocol != "vnc" && host.Protocol != "rdp" {
		return Session{}, errors.New("host is not a remote desktop host")
	}
	if request.Native && host.Protocol != "rdp" {
		return Session{}, errors.New("native desktop client is only supported for RDP")
	}
	secret, err := m.resolveSecret(userID, host, request.Secret)
	if err != nil {
		return Session{}, err
	}
	if request.Native {
		return Session{HostID: host.ID, Protocol: host.Protocol, Password: secret}, nil
	}
	request.Width = clampDefault(request.Width, 320, 7680, 1280)
	request.Height = clampDefault(request.Height, 200, 4320, 720)
	request.DPI = clampDefault(request.DPI, 72, 240, 96)
	token, err := security.RandomToken(32)
	if err != nil {
		return Session{}, err
	}
	now := m.now()
	m.mu.Lock()
	m.pruneLocked(now)
	m.pending[token] = pendingSession{
		userID: userID, host: host, secret: secret,
		width: request.Width, height: request.Height, dpi: request.DPI,
		expiresAt: now.Add(pendingLifetime),
	}
	m.mu.Unlock()
	path := "/ws/desktop/" + host.Protocol + "/" + url.PathEscape(token)
	result := Session{ID: token, HostID: host.ID, Protocol: host.Protocol, WebSocketPath: path, ReadOnly: host.DesktopReadOnly}
	if host.Protocol == "vnc" {
		result.Password = secret
	}
	return result, nil
}

func (m *Manager) resolveSecret(userID string, host store.Host, temporary string) (string, error) {
	if temporary != "" {
		return temporary, nil
	}
	if host.CredentialID != "" {
		credential, err := m.store.Credential(userID, host.CredentialID)
		if err != nil {
			return "", err
		}
		if credential.Kind != "password" {
			return "", errors.New("remote desktop requires a password credential")
		}
		return m.vault.Decrypt(credential.Secret)
	}
	if host.PasswordEnc != "" {
		return m.vault.Decrypt(host.PasswordEnc)
	}
	return "", ErrCredentialRequired
}

func (m *Manager) consume(userID, token, protocol string) (pendingSession, error) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(now)
	session, ok := m.pending[token]
	if !ok || session.userID != userID || session.host.Protocol != protocol {
		return pendingSession{}, errors.New("desktop session is invalid or expired")
	}
	delete(m.pending, token)
	return session, nil
}

func (m *Manager) pruneLocked(now time.Time) {
	for token, session := range m.pending {
		if !session.expiresAt.After(now) {
			delete(m.pending, token)
		}
	}
}

func clampDefault(value, minimum, maximum, fallback int) int {
	if value == 0 {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func (m *Manager) Test(ctx context.Context, userID string, host store.Host) (time.Duration, error) {
	started := time.Now()
	connection, err := m.dialTarget(ctx, userID, host)
	if err != nil {
		return 0, err
	}
	_ = connection.Close()
	return time.Since(started), nil
}

func (m *Manager) ServeVNC(w http.ResponseWriter, r *http.Request, userID, token string) error {
	if !sameOrigin(r) {
		err := errors.New("websocket origin is not allowed")
		http.Error(w, "WebSocket origin is not allowed", http.StatusForbidden)
		return err
	}
	session, err := m.consume(userID, token, "vnc")
	if err != nil {
		http.Error(w, "Remote desktop session is invalid or expired", http.StatusGone)
		return err
	}
	target, err := m.dialTarget(r.Context(), userID, session.host)
	if err != nil {
		http.Error(w, "Remote desktop target is unavailable", http.StatusBadGateway)
		return err
	}
	upgrader := websocket.Upgrader{ReadBufferSize: 32 * 1024, WriteBufferSize: 32 * 1024, CheckOrigin: sameOrigin}
	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		_ = target.Close()
		return err
	}
	defer client.Close()
	defer target.Close()
	client.SetReadLimit(4 << 20)
	return bridgeWebSocket(client, target)
}

func (m *Manager) ServeRDP(w http.ResponseWriter, r *http.Request, userID, token string) error {
	if !sameOrigin(r) {
		err := errors.New("websocket origin is not allowed")
		http.Error(w, "WebSocket origin is not allowed", http.StatusForbidden)
		return err
	}
	session, err := m.consume(userID, token, "rdp")
	if err != nil {
		http.Error(w, "Remote desktop session is invalid or expired", http.StatusGone)
		return err
	}
	server := guac.NewWebsocketServer(func(*http.Request) (guac.Tunnel, error) {
		return m.openGuacamoleTunnel(r.Context(), session)
	})
	server.ServeHTTP(w, r)
	return nil
}

func (m *Manager) openGuacamoleTunnel(ctx context.Context, session pendingSession) (guac.Tunnel, error) {
	drivePath, cleanupDrive, err := m.prepareDrivePath(session.host)
	if err != nil {
		return nil, err
	}
	target, err := m.dialTarget(ctx, session.userID, session.host)
	if err != nil {
		cleanupDrive()
		return nil, err
	}
	proxy, err := newOneShotProxy(m.proxyAddr, target)
	if err != nil {
		cleanupDrive()
		_ = target.Close()
		return nil, err
	}
	guacd, err := net.DialTimeout("tcp", m.guacdAddr, 5*time.Second)
	if err != nil {
		cleanupDrive()
		_ = proxy.Close()
		return nil, fmt.Errorf("connect guacd at %s: %w", m.guacdAddr, err)
	}
	config := guac.NewGuacamoleConfiguration()
	config.Protocol = "rdp"
	config.OptimalScreenWidth = session.width
	config.OptimalScreenHeight = session.height
	config.OptimalResolution = session.dpi
	if session.host.RDPQuality == "smooth" {
		config.ImageMimetypes = []string{"image/webp", "image/jpeg", "image/png"}
	} else {
		config.ImageMimetypes = []string{"image/png"}
	}
	config.AudioMimetypes = []string{"audio/ogg", "audio/webm", "audio/L16"}
	config.Parameters = map[string]string{
		"hostname":              m.proxyAddr,
		"port":                  strconv.Itoa(proxy.Port()),
		"username":              session.host.Username,
		"password":              session.secret,
		"domain":                session.host.DesktopDomain,
		"security":              session.host.DesktopSecurity,
		"ignore-cert":           strconv.FormatBool(session.host.IgnoreCertificate),
		"read-only":             strconv.FormatBool(session.host.DesktopReadOnly),
		"disable-gfx":           strconv.FormatBool(m.rdpDisableGFX),
		"color-depth":           "32",
		"enable-font-smoothing": "true",
		"force-lossless":        strconv.FormatBool(session.host.RDPQuality != "smooth"),
		"disable-copy":          strconv.FormatBool(!session.host.RDPClipboard),
		"disable-paste":         strconv.FormatBool(!session.host.RDPClipboard),
		"disable-audio":         strconv.FormatBool(!session.host.RDPAudio),
		"enable-drive":          strconv.FormatBool(session.host.RDPDrive),
		"enable-printing":       strconv.FormatBool(session.host.RDPPrinting),
		"enable-multimon":       strconv.FormatBool(session.host.RDPMultiMonitor),
	}
	if m.rdpResizeMethod != "none" {
		config.Parameters["resize-method"] = m.rdpResizeMethod
	}
	if drivePath != "" {
		config.Parameters["drive-path"] = drivePath
		config.Parameters["create-drive-path"] = "true"
	}
	stream := guac.NewStream(guacd, guac.SocketTimeout)
	if err = stream.Handshake(config); err != nil {
		cleanupDrive()
		_ = guacd.Close()
		_ = proxy.Close()
		return nil, fmt.Errorf("configure guacd RDP connection: %w", err)
	}
	return &managedTunnel{Tunnel: guac.NewSimpleTunnel(stream), proxy: proxy, cleanup: cleanupDrive}, nil
}

func (m *Manager) prepareDrivePath(host store.Host) (string, func(), error) {
	if !host.RDPDrive {
		return "", func() {}, nil
	}
	if m.rdpDriveDir == "" {
		return "", func() {}, errors.New("RDP drive redirection is not configured")
	}
	if err := os.MkdirAll(m.rdpDriveDir, 0o777); err != nil {
		return "", func() {}, fmt.Errorf("create RDP drive directory: %w", err)
	}
	if err := os.Chmod(m.rdpDriveDir, 0o777); err != nil {
		return "", func() {}, fmt.Errorf("prepare RDP drive directory: %w", err)
	}
	drivePath, err := os.MkdirTemp(m.rdpDriveDir, "session-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create RDP drive session: %w", err)
	}
	if err := os.Chmod(drivePath, 0o777); err != nil {
		_ = os.RemoveAll(drivePath)
		return "", func() {}, fmt.Errorf("prepare RDP drive session: %w", err)
	}
	return filepath.Clean(drivePath), func() { _ = os.RemoveAll(drivePath) }, nil
}

func (m *Manager) dialTarget(ctx context.Context, userID string, host store.Host) (net.Conn, error) {
	timeout := time.Duration(host.ConnectTimeout) * time.Second
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	target := net.JoinHostPort(host.Address, strconv.Itoa(host.Port))
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if host.JumpHostID == "" {
		return m.dialer.DialContext(dialCtx, "tcp", target)
	}
	if m.terminals == nil {
		return nil, errors.New("SSH jump host support is unavailable")
	}
	jump, _, err := m.terminals.DialSaved(dialCtx, userID, host.JumpHostID)
	if err != nil {
		return nil, fmt.Errorf("connect SSH jump host: %w", err)
	}
	connection, err := dialSSHContext(dialCtx, jump, target)
	if err != nil {
		_ = jump.Close()
		return nil, err
	}
	return &jumpConnection{Conn: connection, jump: jump}, nil
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Host, r.Host)
}
