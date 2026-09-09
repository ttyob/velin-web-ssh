package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Addr              string
	DataDir           string
	DatabasePath      string
	MasterKeyPath     string
	WebDist           string
	CookieSecure      bool
	EmbedOrigins      []string
	TrustedProxyCIDRs []*net.IPNet
	HostPortAddr      string
	SessionTTL        time.Duration
	DeploymentID      string
	AdminUser         string
	AdminPassword     string
	AIBaseURL         string
	AIAPIKey          string
	AIModel           string
	FFmpegBinary      string
	GuacdAddr         string
	DesktopProxyAddr  string
	RDPDriveDir       string
	RDPResizeMethod   string
	RDPDisableGFX     bool
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}
	dataDir := env("VELIN_DATA_DIR", "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create data directory: %w", err)
	}
	deploymentID, err := persistedID(filepath.Join(dataDir, "deployment_id"))
	if err != nil {
		return Config{}, err
	}
	hostPortAddr := env("VELIN_HOST_PORT_ADDR", "127.0.0.1")
	if parsed := net.ParseIP(hostPortAddr); parsed == nil || parsed.To4() == nil {
		return Config{}, fmt.Errorf("VELIN_HOST_PORT_ADDR must be an IPv4 address")
	}
	desktopProxyAddr := env("VELIN_DESKTOP_PROXY_ADDR", "127.0.0.1")
	if parsed := net.ParseIP(desktopProxyAddr); parsed == nil || parsed.To4() == nil || parsed.IsUnspecified() {
		return Config{}, fmt.Errorf("VELIN_DESKTOP_PROXY_ADDR must be a specific IPv4 address")
	}
	rdpResizeMethod := strings.ToLower(env("VELIN_RDP_RESIZE_METHOD", "display-update"))
	if rdpResizeMethod != "display-update" && rdpResizeMethod != "reconnect" && rdpResizeMethod != "none" {
		return Config{}, fmt.Errorf("VELIN_RDP_RESIZE_METHOD must be display-update, reconnect, or none")
	}
	embedOrigins, err := parseOrigins(os.Getenv("VELIN_EMBED_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	trustedProxyCIDRs, err := parseCIDRs(os.Getenv("VELIN_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, err
	}
	return Config{
		Addr:              env("VELIN_ADDR", "0.0.0.0:8377"),
		DataDir:           dataDir,
		DatabasePath:      filepath.Join(dataDir, "velin.db"),
		MasterKeyPath:     filepath.Join(dataDir, "master.key"),
		WebDist:           env("VELIN_WEB_DIST", "web/dist"),
		CookieSecure:      strings.EqualFold(env("VELIN_COOKIE_SECURE", "true"), "true"),
		EmbedOrigins:      embedOrigins,
		TrustedProxyCIDRs: trustedProxyCIDRs,
		HostPortAddr:      hostPortAddr,
		SessionTTL:        7 * 24 * time.Hour,
		DeploymentID:      deploymentID,
		AdminUser:         env("VELIN_ADMIN_USER", "admin"),
		AdminPassword:     os.Getenv("VELIN_ADMIN_PASSWORD"),
		AIBaseURL:         strings.TrimRight(env("VELIN_AI_BASE_URL", ""), "/"),
		AIAPIKey:          strings.TrimSpace(os.Getenv("VELIN_AI_API_KEY")),
		AIModel:           strings.TrimSpace(os.Getenv("VELIN_AI_MODEL")),
		FFmpegBinary:      env("VELIN_FFMPEG_BINARY", "ffmpeg"),
		GuacdAddr:         env("VELIN_GUACD_ADDR", "127.0.0.1:4822"),
		DesktopProxyAddr:  desktopProxyAddr,
		RDPDriveDir:       env("VELIN_RDP_DRIVE_DIR", "/tmp/velin-rdp-drives"),
		RDPResizeMethod:   rdpResizeMethod,
		RDPDisableGFX:     strings.EqualFold(env("VELIN_RDP_DISABLE_GFX", "false"), "true"),
	}, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseOrigins(raw string) ([]string, error) {
	var origins []string
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return nil, fmt.Errorf("VELIN_EMBED_ORIGINS contains invalid origin %q", value)
		}
		origins = append(origins, parsed.Scheme+"://"+parsed.Host)
	}
	return origins, nil
}

func parseCIDRs(raw string) ([]*net.IPNet, error) {
	var networks []*net.IPNet
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if ip := net.ParseIP(value); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
				ip = ip.To4()
			}
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("VELIN_TRUSTED_PROXY_CIDRS contains invalid network %q", value)
		}
		networks = append(networks, network)
	}
	return networks, nil
}

func persistedID(path string) (string, error) {
	if value, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(value))
		if id != "" {
			return id, nil
		}
	}
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate deployment id: %w", err)
	}
	id := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("persist deployment id: %w", err)
	}
	return id, nil
}
