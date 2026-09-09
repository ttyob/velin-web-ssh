package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pquerna/otp/totp"
	"velin-webssh/internal/agent"
	"velin-webssh/internal/config"
	"velin-webssh/internal/forward"
	"velin-webssh/internal/netdial"
	"velin-webssh/internal/remotedesktop"
	"velin-webssh/internal/security"
	"velin-webssh/internal/store"
	"velin-webssh/internal/tailnet"
	"velin-webssh/internal/terminal"
)

const cookieName = "velin_session"
const csrfCookieName = "velin_csrf"

type API struct {
	cfg             config.Config
	store           *store.Store
	vault           *security.Vault
	terminals       *terminal.Manager
	forwards        *forward.Manager
	agents          *agent.Manager
	webProxies      *webProxyManager
	desktops        *remotedesktop.Manager
	tailscale       *tailnet.Manager
	started         time.Time
	requests        atomic.Int64
	websockets      atomic.Int64
	httpTotal       atomic.Uint64
	httpErrors      atomic.Uint64
	httpNanos       atomic.Uint64
	http2xx         atomic.Uint64
	http3xx         atomic.Uint64
	http4xx         atomic.Uint64
	http5xx         atomic.Uint64
	wsTotal         atomic.Uint64
	taskQueue       chan commandTaskRequest
	backupMu        sync.Mutex
	captchaMu       sync.Mutex
	captchas        map[string]loginCaptcha
	updateMu        sync.Mutex
	updateCache     updateInfo
	updateCheckedAt time.Time
	updateClient    *http.Client
	updateURL       string
	shareMu         sync.Mutex
	shareViewers    map[string]map[string]*shareViewerConnection
	shareFailures   map[string]shareFailure
}

type contextKey string

const userKey contextKey = "user"
const authTokenHashKey contextKey = "auth_token_hash"

type securityPolicy struct {
	PasswordMinLength     int  `json:"passwordMinLength"`
	LoginFailureThreshold int  `json:"loginFailureThreshold"`
	LockMinutes           int  `json:"lockMinutes"`
	RememberDays          int  `json:"rememberDays"`
	ForceChangeOnCreate   bool `json:"forceChangeOnCreate"`
}

func defaultSecurityPolicy() securityPolicy {
	return securityPolicy{PasswordMinLength: 10, LoginFailureThreshold: 5, LockMinutes: 15, RememberDays: 7, ForceChangeOnCreate: true}
}
func (a *API) securityPolicy() securityPolicy {
	policy := defaultSecurityPolicy()
	_ = a.store.SystemSetting("security_policy", &policy)
	return policy
}

func New(cfg config.Config, s *store.Store, v *security.Vault, t *terminal.Manager, forwards *forward.Manager, agents *agent.Manager) *API {
	return newAPI(cfg, s, v, t, forwards, agents, netdial.Direct{}, nil)
}

func NewWithTailnet(cfg config.Config, s *store.Store, v *security.Vault, t *terminal.Manager, forwards *forward.Manager, agents *agent.Manager, tailscale *tailnet.Manager) *API {
	return newAPI(cfg, s, v, t, forwards, agents, tailscale, tailscale)
}

func newAPI(cfg config.Config, s *store.Store, v *security.Vault, t *terminal.Manager, forwards *forward.Manager, agents *agent.Manager, dialer netdial.Dialer, tailscale *tailnet.Manager) *API {
	desktops := remotedesktop.NewManager(s, v, t, cfg.GuacdAddr, cfg.DesktopProxyAddr, cfg.RDPDriveDir, cfg.RDPResizeMethod, cfg.RDPDisableGFX)
	desktops.SetDialer(dialer)
	forwards.SetDialer(dialer)
	a := &API{cfg: cfg, store: s, vault: v, terminals: t, forwards: forwards, agents: agents, tailscale: tailscale, webProxies: newWebProxyManager(t, cfg.HostPortAddr), desktops: desktops, started: time.Now(), taskQueue: make(chan commandTaskRequest, 100), captchas: make(map[string]loginCaptcha), updateClient: &http.Client{Timeout: 10 * time.Second}, updateURL: defaultReleaseURL, shareViewers: make(map[string]map[string]*shareViewerConnection), shareFailures: make(map[string]shareFailure)}
	a.restoreAIModelConfig()
	a.restoreHostPortWebServices()
	go a.commandTaskWorker()
	go a.expireTerminalShares()
	return a
}

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(a.requestID, a.recoverer, a.requestLog, a.securityHeaders, a.csrfProtection)
	r.Use(a.markedWebServiceProxy)
	r.Get("/api/health", a.ready)
	r.Get("/api/health/live", a.live)
	r.Get("/api/health/ready", a.ready)
	r.Get("/api/csrf", a.csrfToken)
	r.Post("/api/auth/login", a.login)
	r.Get("/api/auth/captcha", a.loginCaptcha)
	r.Get("/api/public/shares/{token}", a.publicTerminalShare)
	r.Post("/api/public/shares/{token}/access", a.accessTerminalShare)
	r.Get("/ws/shares/{token}", a.terminalShareWS)
	r.Group(func(r chi.Router) {
		r.Use(a.authenticate)
		r.Use(a.requirePasswordChange)
		r.Use(a.requireUnlocked)
		r.Get("/api/auth/me", a.me)
		r.Post("/api/auth/lock", a.lockSession)
		r.Post("/api/auth/unlock", a.unlockSession)
		r.Get("/api/auth/lock-pin", a.lockPINStatus)
		r.Put("/api/auth/lock-pin", a.setLockPIN)
		r.Delete("/api/auth/lock-pin", a.disableLockPIN)
		r.Post("/api/auth/password", a.changePassword)
		r.Get("/api/auth/totp", a.totpStatus)
		r.Post("/api/auth/totp/setup", a.setupTOTP)
		r.Post("/api/auth/totp/enable", a.enableTOTP)
		r.Delete("/api/auth/totp", a.disableTOTP)
		r.Post("/api/auth/logout", a.logout)
		r.Get("/api/auth/devices", a.devices)
		r.Post("/api/auth/devices/revoke-all", a.revokeAllDevices)
		r.Delete("/api/auth/devices/{id}", a.deleteDevice)
		r.Get("/api/hosts", a.hosts)
		r.Post("/api/hosts", a.saveHost)
		r.Post("/api/hosts/batch", a.batchHosts)
		r.Post("/api/hosts/reorder", a.reorderHosts)
		r.Put("/api/hosts/{id}", a.saveHost)
		r.Post("/api/hosts/{id}/test", a.testHost)
		r.Post("/api/desktop/sessions", a.createDesktopSession)
		r.Get("/ws/desktop/vnc/{token}", a.desktopVNC)
		r.Get("/ws/desktop/rdp/{token}", a.desktopRDP)
		r.Get("/api/hosts/{id}/sessions", a.hostSessions)
		r.Get("/api/hosts/{id}/agent", a.agentStatus)
		r.Post("/api/hosts/{id}/agent/connect", a.connectAgent)
		r.Get("/api/hosts/{id}/agent/snapshot", a.agentSnapshot)
		r.Get("/api/hosts/{id}/agent/processes", a.agentProcesses)
		r.Post("/api/hosts/{id}/agent/ssh-sessions/terminate", a.agentTerminateSSHSession)
		r.Post("/api/hosts/{id}/agent/ssh-sessions/ban", a.agentBanSSHAddress)
		r.Post("/api/hosts/{id}/agent/ssh-sessions/unban", a.agentUnbanSSHAddress)
		r.Get("/api/agent/models", a.agentModels)
		r.Get("/api/agent/backends", a.agentBackends)
		r.Post("/api/hosts/{id}/agent/chat", a.agentChat)
		r.Post("/api/hosts/{id}/agent/command", a.agentCommand)
		r.Get("/ws/hosts/{id}/agent", a.agentWS)
		r.Post("/api/hosts/{id}/docker/login", a.dockerLogin)
		r.Delete("/api/hosts/{id}/agent", a.disconnectAgent)
		r.Delete("/api/hosts/{id}", a.deleteHost)
		r.Get("/api/credentials", a.credentials)
		r.Post("/api/credentials", a.saveCredential)
		r.Delete("/api/credentials/{id}", a.deleteCredential)
		r.Get("/api/sessions", a.sessions)
		r.Post("/api/sessions", a.createSession)
		r.Post("/api/sessions/{id}/restore", a.restoreSession)
		r.Get("/api/sessions/{id}/directory", a.sessionDirectory)
		r.Get("/api/sessions/{id}/foreground", a.sessionForeground)
		r.Get("/api/sessions/{id}/docker/status", a.sessionDockerStatus)
		r.Patch("/api/sessions/{id}", a.updateSession)
		r.Delete("/api/sessions/{id}", a.terminateSession)
		r.Get("/api/sessions/{id}/shares", a.terminalShares)
		r.Post("/api/sessions/{id}/shares", a.createTerminalShare)
		r.Delete("/api/session-shares/{id}", a.revokeTerminalShare)
		r.Get("/api/tasks", a.tasks)
		r.Post("/api/tasks", a.createTask)
		r.Get("/api/tasks/{id}", a.task)
		r.Get("/api/workspace", a.workspace)
		r.Put("/api/workspace", a.saveWorkspace)
		r.Get("/api/preferences", a.preferences)
		r.Put("/api/preferences", a.savePreferences)
		r.Get("/api/system/update", a.update)
		r.Get("/api/data/export", a.exportData)
		r.Post("/api/data/import/openssh", a.importOpenSSH)
		r.Get("/api/snippets", a.snippets)
		r.Post("/api/snippets", a.saveSnippet)
		r.Put("/api/snippets/{id}", a.saveSnippet)
		r.Delete("/api/snippets/{id}", a.deleteSnippet)
		r.Get("/api/sftp/{hostID}/list", a.sftpList)
		r.Get("/api/sftp/{hostID}/download", a.sftpDownload)
		r.Get("/api/sftp/{hostID}/preview-image", a.sftpPreviewImage)
		r.Get("/api/sftp/{hostID}/text", a.sftpReadText)
		r.Put("/api/sftp/{hostID}/text", a.sftpWriteText)
		r.Put("/api/sftp/{hostID}/upload", a.sftpUpload)
		r.Get("/api/sftp/{hostID}/transfer-status", a.sftpTransferStatus)
		r.Post("/api/sftp/{hostID}/mkdir", a.sftpMkdir)
		r.Post("/api/sftp/{hostID}/rename", a.sftpRename)
		r.Post("/api/sftp/{hostID}/delete", a.sftpDelete)
		r.Get("/api/forwards", a.portForwards)
		r.Post("/api/forwards", a.savePortForward)
		r.Put("/api/forwards/{id}", a.savePortForward)
		r.Post("/api/forwards/{id}/start", a.startPortForward)
		r.Post("/api/forwards/{id}/stop", a.stopPortForward)
		r.Delete("/api/forwards/{id}", a.deletePortForward)
		r.Post("/api/web-proxies", a.createWebProxy)
		r.Delete("/api/web-proxies/{token}", a.deleteWebProxy)
		r.Get("/api/web-services", a.webServices)
		r.Post("/api/web-services", a.saveWebService)
		r.Put("/api/web-services/{id}", a.saveWebService)
		r.Post("/api/web-services/{id}/open", a.openWebService)
		r.Delete("/api/web-services/{id}", a.deleteWebService)
		r.Handle("/web-proxy/{token}", http.HandlerFunc(a.serveWebProxy))
		r.Handle("/web-proxy/{token}/*", http.HandlerFunc(a.serveWebProxy))
		r.Handle("/web-service-proxy/{serviceID}", http.HandlerFunc(a.serveStableWebProxy))
		r.Handle("/web-service-proxy/{serviceID}/*", http.HandlerFunc(a.serveStableWebProxy))
		r.Get("/api/recordings", a.recordings)
		r.Post("/api/sessions/{id}/recording", a.startRecording)
		r.Delete("/api/sessions/{id}/recording", a.stopRecording)
		r.Post("/api/recordings/{id}/upload", a.uploadRecording)
		r.Get("/api/recordings/{id}/download", a.downloadRecording)
		r.Group(func(r chi.Router) {
			r.Use(a.adminOnly)
			r.Get("/api/admin/users", a.users)
			r.Post("/api/admin/users", a.createUser)
			r.Patch("/api/admin/users/{id}", a.updateUser)
			r.Delete("/api/admin/users/{id}", a.deleteUser)
			r.Get("/api/admin/security-policy", a.getSecurityPolicy)
			r.Put("/api/admin/security-policy", a.saveSecurityPolicy)
			r.Get("/api/admin/ai-model", a.getAIModelConfig)
			r.Put("/api/admin/ai-model", a.saveAIModelConfig)
			r.Post("/api/admin/ai-model/test", a.testAIModelConfig)
			r.Get("/api/admin/tailscale", a.tailscaleStatus)
			r.Put("/api/admin/tailscale", a.saveTailscale)
			r.Get("/api/admin/stats", a.stats)
			r.Get("/api/admin/metrics", a.metrics)
			r.Post("/api/admin/backup", a.backup)
			r.Get("/api/admin/backups", a.backups)
			r.Post("/api/admin/backups/upload", a.uploadBackup)
			r.Get("/api/admin/backups/{file}/download", a.downloadBackup)
			r.Post("/api/admin/backups/{file}/restore", a.restoreBackup)
		})
	})
	r.Get("/ws/sessions/{id}", a.terminalWS)
	r.Handle("/*", a.staticHandler())
	return r
}

func (a *API) requirePasswordChange(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		if u.ForcePasswordChange && r.URL.Path != "/api/auth/me" && r.URL.Path != "/api/auth/password" && r.URL.Path != "/api/auth/logout" {
			writeError(w, http.StatusForbidden, "password_change_required", "继续使用前必须修改初始密码")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) requireUnlocked(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/me", "/api/auth/lock", "/api/auth/unlock", "/api/auth/logout":
			next.ServeHTTP(w, r)
			return
		}
		if currentUser(r).SessionLocked {
			writeError(w, http.StatusLocked, "session_locked", "当前工作区已锁定")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set("X-Request-ID", id)
		a.requests.Add(1)
		defer a.requests.Add(-1)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey("request_id"), id)))
	})
}

func (a *API) csrfProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || isWebProxyPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
			writeError(w, http.StatusForbidden, "cross_site_rejected", "不允许跨站写请求")
			return
		}
		cookie, cookieErr := r.Cookie(csrfCookieName)
		header := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		if cookieErr != nil || header == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
			writeError(w, http.StatusForbidden, "csrf_rejected", "安全令牌无效，请刷新页面重试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func isWebProxyPath(path string) bool {
	return strings.HasPrefix(path, "/web-proxy/") || strings.HasPrefix(path, "/web-service-proxy/")
}

func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "error", v, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal_error", "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (a *API) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		tracked := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(tracked, r)
		a.httpTotal.Add(1)
		a.httpNanos.Add(uint64(time.Since(start)))
		if tracked.status >= http.StatusBadRequest {
			a.httpErrors.Add(1)
		}
		switch tracked.status / 100 {
		case 2:
			a.http2xx.Add(1)
		case 3:
			a.http3xx.Add(1)
		case 4:
			a.http4xx.Add(1)
		case 5:
			a.http5xx.Add(1)
		}
		slog.Info("request", "request_id", r.Context().Value(contextKey("request_id")), "method", r.Method, "path", logRequestPath(r.URL.Path), "duration", time.Since(start).String())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking not supported")
	}
	return hijacker.Hijack()
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequestPath(path string) string {
	for _, prefix := range []string{"/web-proxy/", "/web-service-proxy/", "/ws/desktop/vnc/", "/ws/desktop/rdp/", "/share/", "/api/public/shares/", "/ws/shares/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			return prefix + "[redacted]" + rest[slash:]
		}
		return prefix + "[redacted]"
	}
	return path
}
func (a *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if len(a.cfg.EmbedOrigins) == 0 {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		w.Header().Set("Permissions-Policy", "clipboard-read=(self), clipboard-write=(self)")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// API responses opened as documents get an opaque origin. Fetch clients are unaffected.
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'none'")
		} else {
			csp := "default-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:; img-src 'self' data:; font-src 'self' data:"
			if len(a.cfg.EmbedOrigins) > 0 {
				csp += "; frame-ancestors " + strings.Join(a.cfg.EmbedOrigins, " ")
			} else {
				csp += "; frame-ancestors 'none'"
			}
			w.Header().Set("Content-Security-Policy", csp)
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, tokenHash, err := a.currentAuthSession(r)
		if err != nil || u.Disabled {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请重新登录")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, authTokenHashKey, tokenHash)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (a *API) adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).Role != "admin" {
			writeError(w, http.StatusForbidden, "forbidden", "需要管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) currentUser(r *http.Request) (store.User, error) {
	u, _, err := a.currentAuthSession(r)
	return u, err
}
func (a *API) currentAuthSession(r *http.Request) (store.User, string, error) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return store.User{}, "", err
	}
	tokenHash := security.TokenHash(cookie.Value)
	u, err := a.store.UserByToken(tokenHash)
	if err != nil {
		return store.User{}, "", err
	}
	u.SessionLocked, err = a.store.AuthSessionLocked(tokenHash)
	return u, tokenHash, err
}
func currentUser(r *http.Request) store.User {
	u, _ := r.Context().Value(userKey).(store.User)
	return u
}
func currentAuthTokenHash(r *http.Request) string {
	hash, _ := r.Context().Value(authTokenHashKey).(string)
	return hash
}
func ipOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	return r.RemoteAddr
}

func (a *API) clientIP(r *http.Request) string {
	remote := ipOf(r)
	parsedRemote := net.ParseIP(remote)
	for _, network := range a.cfg.TrustedProxyCIDRs {
		if parsedRemote != nil && network.Contains(parsedRemote) {
			forwarded := strings.TrimSpace(r.Header.Get("X-Real-IP"))
			if net.ParseIP(forwarded) != nil {
				return forwarded
			}
			break
		}
	}
	return remote
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		TOTPCode  string `json:"totpCode"`
		CaptchaID string `json:"captchaID"`
		Captcha   string `json:"captcha"`
		Remember  bool   `json:"remember"`
	}
	if !decode(w, r, &in) {
		return
	}
	identity, ip := strings.TrimSpace(in.Username), a.clientIP(r)
	if identity == "" || len([]rune(identity)) > 128 {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	policy := a.securityPolicy()
	locked, lockErr := a.store.LoginLock(identity, ip)
	if lockErr != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "登录失败，请稍后重试")
		return
	}
	if locked.After(time.Now()) {
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{"code": "login_locked", "message": "登录尝试过多，请稍后再试", "lockedUntil": locked})
		return
	}
	failures, failureErr := a.store.LoginFailureCount(identity, ip)
	if failureErr != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "登录失败，请稍后重试")
		return
	}
	captchaRequired := failures > 0
	if captchaRequired {
		valid, captchaCode := a.validateLoginCaptcha(identity, ip, in.CaptchaID, in.Captcha)
		if !valid {
			if captchaCode == "" {
				captchaCode = "captcha_required"
			}
			writeJSON(w, http.StatusUnauthorized, map[string]any{"code": captchaCode, "message": "请输入正确的图形验证码", "captchaRequired": true})
			return
		}
	}
	u, hash, err := a.store.UserByUsername(identity)
	if err != nil || u.Disabled || !security.VerifyPassword(hash, in.Password) {
		if recordErr := a.store.RecordLoginFailure(identity, ip, policy.LoginFailureThreshold, policy.LockMinutes); recordErr != nil {
			slog.Error("record login failure", "error", recordErr)
		}
		if captchaRequired {
			a.deleteLoginCaptcha(in.CaptchaID)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "invalid_credentials", "message": "用户名或密码错误", "captchaRequired": true})
		return
	}
	secretEnc, recoveryHashes, totpEnabled, totpErr := a.store.TOTP(u.ID)
	if totpErr != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "登录失败，请稍后重试")
		return
	}
	if totpEnabled {
		if in.TOTPCode == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "totp_required", "message": "请输入双因素验证码或恢复码", "captchaRequired": captchaRequired})
			return
		}
		secret, decryptErr := a.vault.Decrypt(secretEnc)
		valid, remaining, recoveryUsed := verifySecondFactor(secret, recoveryHashes, in.TOTPCode)
		valid = decryptErr == nil && valid
		if valid && recoveryUsed {
			if err = a.store.SaveTOTP(u.ID, secretEnc, remaining, true); err != nil {
				writeError(w, 500, "database_error", "无法消费恢复码，请重试")
				return
			}
		}
		if !valid {
			if recordErr := a.store.RecordLoginFailure(identity, ip, policy.LoginFailureThreshold, policy.LockMinutes); recordErr != nil {
				slog.Error("record login failure", "error", recordErr)
			}
			writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "invalid_totp", "message": "双因素验证码无效", "captchaRequired": true})
			return
		}
	}
	_ = a.store.ClearLoginFailures(identity, ip)
	token, err := security.RandomToken(32)
	if err != nil {
		writeError(w, 500, "internal_error", "无法创建登录会话")
		return
	}
	ttl := 24 * time.Hour
	if in.Remember {
		ttl = time.Duration(policy.RememberDays) * 24 * time.Hour
	}
	sid := uuid.NewString()
	if err = a.store.CreateAuthSession(sid, u.ID, security.TokenHash(token), r.UserAgent(), ip, time.Now().Add(ttl)); err != nil {
		writeError(w, 500, "database_error", "登录失败")
		return
	}
	if captchaRequired {
		a.deleteLoginCaptcha(in.CaptchaID)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(ttl.Seconds())})
	writeJSON(w, 200, map[string]any{"user": u})
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = a.store.DeleteAuthSession(security.TokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) me(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, currentUser(r)) }
func (a *API) lockSession(w http.ResponseWriter, r *http.Request) {
	var in struct{ Reason string }
	if !decode(w, r, &in) {
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if reason != "manual" && reason != "idle" && reason != "shortcut" {
		reason = "manual"
	}
	u := currentUser(r)
	pinHash, err := a.store.UserLockPINHash(u.ID)
	if err != nil || pinHash == "" {
		writeError(w, http.StatusConflict, "lock_pin_not_configured", "请先设置锁屏 PIN")
		return
	}
	if err := a.store.SetAuthSessionLocked(currentAuthTokenHash(r), true); err != nil {
		writeError(w, 500, "lock_failed", "无法锁定当前工作区")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) unlockSession(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct{ PIN string }
	if !decode(w, r, &in) {
		return
	}
	tokenHash := currentAuthTokenHash(r)
	if blockedUntil, err := a.store.AuthSessionUnlockBlockedUntil(tokenHash); err == nil && blockedUntil.After(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "unlock_rate_limited", "尝试次数过多，请稍后再试")
		return
	}
	hash, err := a.store.UserLockPINHash(u.ID)
	if err != nil || hash == "" {
		writeError(w, http.StatusConflict, "lock_pin_not_configured", "锁屏 PIN 未设置")
		return
	}
	if !validLockPIN(in.PIN) || !security.VerifyPassword(hash, in.PIN) {
		_ = a.store.RecordAuthSessionUnlockFailure(tokenHash)
		writeError(w, http.StatusUnauthorized, "invalid_lock_pin", "锁屏 PIN 错误")
		return
	}
	if err = a.store.SetAuthSessionLocked(tokenHash, false); err != nil {
		writeError(w, 500, "unlock_failed", "无法解锁当前工作区")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func validLockPIN(pin string) bool {
	if len(pin) != 6 {
		return false
	}
	for _, value := range pin {
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}
func (a *API) lockPINStatus(w http.ResponseWriter, r *http.Request) {
	hash, err := a.store.UserLockPINHash(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "lock_pin_status_failed", "无法读取锁屏设置")
		return
	}
	writeJSON(w, 200, map[string]bool{"configured": hash != ""})
}
func (a *API) setLockPIN(w http.ResponseWriter, r *http.Request) {
	var in struct{ PIN string }
	if !decode(w, r, &in) {
		return
	}
	if !validLockPIN(in.PIN) {
		writeError(w, http.StatusBadRequest, "invalid_lock_pin", "PIN 必须是 6 位数字")
		return
	}
	hash, err := security.HashPassword(in.PIN)
	if err != nil {
		writeError(w, 500, "lock_pin_hash_failed", "无法保存锁屏 PIN")
		return
	}
	u := currentUser(r)
	if err = a.store.SetUserLockPIN(u.ID, hash); err != nil {
		writeError(w, 500, "lock_pin_save_failed", "无法保存锁屏 PIN")
		return
	}
	writeJSON(w, 200, map[string]bool{"configured": true})
}
func (a *API) disableLockPIN(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if err := a.store.DisableUserLockPIN(u.ID); err != nil {
		writeError(w, 500, "lock_pin_disable_failed", "无法关闭锁屏")
		return
	}
	writeJSON(w, 200, map[string]bool{"configured": false})
}
func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct{ CurrentPassword, NewPassword string }
	if !decode(w, r, &in) {
		return
	}
	minLength := a.securityPolicy().PasswordMinLength
	if len(in.NewPassword) < minLength {
		writeError(w, 400, "weak_password", fmt.Sprintf("新密码至少 %d 位", minLength))
		return
	}
	cookie, _ := r.Cookie(cookieName)
	currentTokenHash := ""
	if cookie != nil {
		currentTokenHash = security.TokenHash(cookie.Value)
	}
	if err := a.store.ChangePassword(u.ID, in.CurrentPassword, in.NewPassword, currentTokenHash); err != nil {
		writeError(w, 400, "password_change_failed", "当前密码错误")
		return
	}
	_ = os.Remove(filepath.Join(a.cfg.DataDir, "initial-admin-credentials.txt"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) totpStatus(w http.ResponseWriter, r *http.Request) {
	_, recovery, enabled, err := a.store.TOTP(currentUser(r).ID)
	if err != nil {
		enabled = false
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled, "recoveryCodesRemaining": len(recovery)})
}
func (a *API) setupTOTP(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if _, _, enabled, err := a.store.TOTP(u.ID); err == nil && enabled {
		writeError(w, 409, "totp_already_enabled", "双因素认证已启用，请先验证并关闭现有设置")
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Velin Web SSH", AccountName: u.Username})
	if err != nil {
		writeError(w, 500, "totp_setup_failed", "无法生成 TOTP 密钥")
		return
	}
	enc, err := a.vault.Encrypt(key.Secret())
	if err != nil {
		writeError(w, 500, "encryption_error", "无法加密 TOTP 密钥")
		return
	}
	if err = a.store.SaveTOTP(u.ID, enc, nil, false); err != nil {
		writeError(w, 500, "database_error", "无法保存 TOTP 设置")
		return
	}
	writeJSON(w, 200, map[string]string{"secret": key.Secret(), "uri": key.URL()})
}
func (a *API) enableTOTP(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct{ Password, Code string }
	if !decode(w, r, &in) {
		return
	}
	_, hash, err := a.store.UserByUsername(u.Username)
	if err != nil || !security.VerifyPassword(hash, in.Password) {
		writeError(w, 401, "invalid_password", "当前密码错误")
		return
	}
	secretEnc, _, _, err := a.store.TOTP(u.ID)
	if err != nil {
		writeError(w, 400, "totp_not_initialized", "请先生成 TOTP 设置")
		return
	}
	secret, err := a.vault.Decrypt(secretEnc)
	if err != nil || !totp.Validate(in.Code, secret) {
		writeError(w, 400, "invalid_totp", "验证码无效")
		return
	}
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for index := range codes {
		raw, randomErr := security.RandomToken(7)
		if randomErr != nil {
			writeError(w, 500, "internal_error", "无法生成恢复码")
			return
		}
		codes[index] = strings.ToUpper(raw[:10])
		hashes[index] = security.TokenHash(codes[index])
	}
	if err = a.store.SaveTOTP(u.ID, secretEnc, hashes, true); err != nil {
		writeError(w, 500, "database_error", "启用 TOTP 失败")
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "recoveryCodes": codes})
}
func (a *API) disableTOTP(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct{ Password, Code string }
	if !decode(w, r, &in) {
		return
	}
	_, hash, err := a.store.UserByUsername(u.Username)
	if err != nil || !security.VerifyPassword(hash, in.Password) {
		writeError(w, 401, "invalid_password", "当前密码错误")
		return
	}
	secretEnc, _, enabled, err := a.store.TOTP(u.ID)
	if err != nil || !enabled {
		writeError(w, 400, "totp_not_enabled", "TOTP 未启用")
		return
	}
	secret, err := a.vault.Decrypt(secretEnc)
	if err != nil || !totp.Validate(in.Code, secret) {
		writeError(w, 400, "invalid_totp", "验证码无效")
		return
	}
	if err = a.store.DeleteTOTP(u.ID); err != nil {
		writeError(w, 500, "database_error", "关闭双因素认证失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func verifySecondFactor(secret string, recoveryHashes []string, code string) (valid bool, remaining []string, recoveryUsed bool) {
	normalized := strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if secret != "" && totp.Validate(normalized, secret) {
		return true, recoveryHashes, false
	}
	candidate := security.TokenHash(strings.ToUpper(normalized))
	for index, value := range recoveryHashes {
		if candidate == value {
			remaining = append([]string(nil), recoveryHashes[:index]...)
			remaining = append(remaining, recoveryHashes[index+1:]...)
			return true, remaining, true
		}
	}
	return false, recoveryHashes, false
}
func (a *API) devices(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	cookie, _ := r.Cookie(cookieName)
	currentHash := ""
	if cookie != nil {
		currentHash = security.TokenHash(cookie.Value)
	}
	rows, err := a.store.DB.Query(`SELECT id,token_hash,user_agent,ip,created_at,last_seen_at,expires_at FROM auth_sessions WHERE user_id=? ORDER BY last_seen_at DESC`, u.ID)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, tokenHash, agent, ip string
		var created, seen, expires time.Time
		_ = rows.Scan(&id, &tokenHash, &agent, &ip, &created, &seen, &expires)
		out = append(out, map[string]any{"id": id, "userAgent": agent, "ip": ip, "createdAt": created, "lastSeenAt": seen, "expiresAt": expires, "current": tokenHash == currentHash})
	}
	writeJSON(w, 200, nonNil(out))
}
func (a *API) revokeAllDevices(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	_, err := a.store.DB.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, u.ID)
	if err != nil {
		writeError(w, 500, "database_error", "撤销设备失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) deleteDevice(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	_, err := a.store.DB.Exec(`DELETE FROM auth_sessions WHERE id=? AND user_id=?`, chi.URLParam(r, "id"), u.ID)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) hosts(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.Hosts(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, nonNil(out))
}

func (a *API) reorderHosts(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct {
		Items []struct {
			ID        string `json:"id"`
			GroupName string `json:"groupName"`
			SortOrder int    `json:"sortOrder"`
		} `json:"items"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Items) == 0 || len(in.Items) > 10000 {
		writeError(w, 400, "invalid_host_order", "排序主机数量无效")
		return
	}
	items := make([]store.HostOrder, 0, len(in.Items))
	seen := make(map[string]struct{}, len(in.Items))
	for _, item := range in.Items {
		item.ID = strings.TrimSpace(item.ID)
		item.GroupName = normalizeHostGroup(item.GroupName)
		if item.ID == "" || item.SortOrder < 0 || len([]rune(item.GroupName)) > 1024 {
			writeError(w, 400, "invalid_host_order", "主机排序参数无效")
			return
		}
		if _, ok := seen[item.ID]; ok {
			writeError(w, 400, "invalid_host_order", "排序请求包含重复主机")
			return
		}
		seen[item.ID] = struct{}{}
		items = append(items, store.HostOrder{ID: item.ID, GroupName: item.GroupName, SortOrder: item.SortOrder})
	}
	if err := a.store.ReorderHosts(u.ID, items); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "host_not_found", "主机不存在")
		} else {
			writeError(w, 500, "database_error", err.Error())
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func normalizeHostGroup(value string) string {
	parts := strings.Split(value, "/")
	clean := parts[:0]
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, "/")
}

func (a *API) validateJumpHost(userID, targetID, jumpHostID string) error {
	visited := map[string]bool{targetID: true}
	for depth := 0; jumpHostID != ""; depth++ {
		if depth >= 8 {
			return errors.New("跳板机链路不能超过 8 层")
		}
		if visited[jumpHostID] {
			return errors.New("跳板机不能循环引用")
		}
		visited[jumpHostID] = true
		jumpHost, err := a.store.Host(userID, jumpHostID)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("所选跳板机不存在或无权访问")
		}
		if err != nil {
			return err
		}
		if jumpHost.Protocol != "" && jumpHost.Protocol != "ssh" {
			return fmt.Errorf("主机“%s”不是 SSH 主机，不能作为跳板机", jumpHost.Name)
		}
		if jumpHost.CredentialID == "" && !jumpHost.HasPassword {
			return fmt.Errorf("跳板机“%s”需要保存主机密码或绑定已保存的凭据", jumpHost.Name)
		}
		jumpHostID = jumpHost.JumpHostID
	}
	return nil
}

func (a *API) batchHosts(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct {
		IDs       []string `json:"ids"`
		Action    string   `json:"action"`
		GroupName string   `json:"groupName"`
		Tags      string   `json:"tags"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 500 {
		writeError(w, 400, "invalid_batch", "请选择 1 到 500 台主机")
		return
	}
	in.GroupName, in.Tags = normalizeHostGroup(in.GroupName), strings.TrimSpace(in.Tags)
	if len([]rune(in.GroupName)) > 1024 || len([]rune(in.Tags)) > 500 {
		writeError(w, 400, "invalid_batch", "分组或标签内容过长")
		return
	}
	if in.Action != "group" && in.Action != "tags" && in.Action != "delete" {
		writeError(w, 400, "invalid_batch_action", "不支持的批量操作")
		return
	}
	services := []store.WebService(nil)
	if in.Action == "delete" {
		services, _ = a.store.WebServices(u.ID)
	}
	if err := a.store.BatchHosts(u.ID, in.IDs, in.Action, in.GroupName, in.Tags); err != nil {
		status, code := http.StatusBadRequest, "batch_failed"
		if strings.Contains(err.Error(), "active sessions") {
			status, code = http.StatusConflict, "host_in_use"
		} else if errors.Is(err, sql.ErrNoRows) {
			status, code = http.StatusNotFound, "host_not_found"
		}
		writeError(w, status, code, err.Error())
		return
	}
	if in.Action == "delete" {
		deleted := make(map[string]bool, len(in.IDs))
		for _, id := range in.IDs {
			deleted[strings.TrimSpace(id)] = true
		}
		for _, service := range services {
			if deleted[service.HostID] {
				a.webProxies.deleteStable(u.ID, service.ID)
				a.webProxies.deleteHostPort(service.ID)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "count": len(in.IDs)})
}
func (a *API) saveHost(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct {
		store.Host
		Password string `json:"password"`
		AuthMode string `json:"authMode"`
	}
	if !decode(w, r, &in) {
		return
	}
	h := in.Host
	h.ID = chi.URLParam(r, "id")
	if h.ID == "" {
		h.ID = uuid.NewString()
	}
	h.UserID = u.ID
	h.Name = strings.TrimSpace(h.Name)
	h.Address = strings.TrimSpace(h.Address)
	h.Protocol = strings.ToLower(strings.TrimSpace(h.Protocol))
	h.Username = strings.TrimSpace(h.Username)
	h.RDPMode = strings.ToLower(strings.TrimSpace(h.RDPMode))
	h.DesktopDomain = strings.TrimSpace(h.DesktopDomain)
	h.DesktopSecurity = strings.ToLower(strings.TrimSpace(h.DesktopSecurity))
	h.GroupName = normalizeHostGroup(h.GroupName)
	h.JumpHostID = strings.TrimSpace(h.JumpHostID)
	if len([]rune(h.GroupName)) > 1024 {
		writeError(w, 400, "invalid_host_group", "分组路径不能超过 1024 个字符")
		return
	}
	if h.Protocol == "" {
		h.Protocol = "ssh"
	}
	if h.Protocol != "ssh" && h.Protocol != "vnc" && h.Protocol != "rdp" {
		writeError(w, 400, "invalid_host_protocol", "不支持的主机协议")
		return
	}
	if h.Port == 0 {
		switch h.Protocol {
		case "vnc":
			h.Port = 5900
		case "rdp":
			h.Port = 3389
		default:
			h.Port = 22
		}
	}
	if h.ConnectTimeout == 0 {
		h.ConnectTimeout = 12
	}
	if h.KeepaliveInterval == 0 {
		h.KeepaliveInterval = 30
	}
	if h.MaxRetries == 0 {
		h.MaxRetries = 5
	}
	if h.TerminalType == "" {
		h.TerminalType = "xterm-256color"
	}
	if h.SessionMode == "" {
		h.SessionMode = "tmux"
	}
	if h.DesktopSecurity == "" {
		h.DesktopSecurity = "any"
	}
	if h.RDPMode == "" {
		h.RDPMode = "web"
	}
	if h.RDPQuality == "" {
		h.RDPQuality = "crisp"
	}
	if h.Protocol != "rdp" {
		h.RDPMode = "web"
		h.RDPQuality = "crisp"
		h.RDPClipboard = false
		h.RDPAudio = false
		h.RDPDrive = false
		h.RDPPrinting = false
		h.RDPMultiMonitor = false
	}
	if h.ConnectTimeout < 3 || h.ConnectTimeout > 120 || h.KeepaliveInterval < 0 || h.KeepaliveInterval > 300 || h.MaxRetries < 0 || h.MaxRetries > 20 || (h.TerminalType != "xterm-256color" && h.TerminalType != "xterm" && h.TerminalType != "screen-256color") || (h.SessionMode != "tmux" && h.SessionMode != "normal") {
		writeError(w, 400, "invalid_host_options", "连接参数超出允许范围")
		return
	}
	if h.Name == "" || h.Address == "" || (h.Protocol != "vnc" && h.Username == "") || h.Port < 1 || h.Port > 65535 {
		writeError(w, 400, "invalid_host", "请完整填写主机名称、地址、端口和用户名")
		return
	}
	if h.Protocol == "rdp" && h.DesktopSecurity != "any" && h.DesktopSecurity != "nla" && h.DesktopSecurity != "tls" && h.DesktopSecurity != "rdp" {
		writeError(w, 400, "invalid_desktop_security", "不支持的 RDP 安全模式")
		return
	}
	if h.Protocol == "rdp" && h.RDPMode != "web" && h.RDPMode != "native" {
		writeError(w, 400, "invalid_rdp_mode", "不支持的 RDP 连接方式")
		return
	}
	if h.Protocol == "rdp" && h.RDPQuality != "crisp" && h.RDPQuality != "smooth" {
		writeError(w, 400, "invalid_rdp_quality", "不支持的 RDP 画质模式")
		return
	}
	if h.Protocol == "rdp" && h.RDPMode == "native" && h.JumpHostID != "" {
		writeError(w, 400, "invalid_rdp_mode", "本地 RDP 客户端不支持 SSH 跳板机")
		return
	}
	if err := a.validateJumpHost(u.ID, h.ID, h.JumpHostID); err != nil {
		writeError(w, 400, "invalid_jump_host", err.Error())
		return
	}
	existingPassword := ""
	if h.ID != "" {
		if existing, err := a.store.Host(u.ID, h.ID); err == nil {
			existingPassword = existing.PasswordEnc
		}
	}
	authMode := strings.TrimSpace(in.AuthMode)
	if authMode == "" {
		switch {
		case in.Password != "":
			authMode = "password"
		case h.CredentialID != "":
			authMode = "credential"
		default:
			authMode = "prompt"
		}
	}
	switch authMode {
	case "password":
		if in.Password != "" {
			enc, err := a.vault.Encrypt(in.Password)
			if err != nil {
				writeError(w, 500, "encryption_error", "密码加密失败")
				return
			}
			h.PasswordEnc = enc
		} else if existingPassword != "" {
			h.PasswordEnc = existingPassword
		} else {
			writeError(w, 400, "invalid_password", "请填写连接密码")
			return
		}
		h.CredentialID = ""
	case "credential":
		h.PasswordEnc = ""
		if h.CredentialID == "" {
			writeError(w, 400, "invalid_credential", "请选择凭据")
			return
		}
		credential, err := a.store.Credential(u.ID, h.CredentialID)
		if err != nil {
			writeError(w, 400, "invalid_credential", "凭据不存在")
			return
		}
		if h.Protocol != "ssh" && credential.Kind != "password" {
			writeError(w, 400, "invalid_credential", "VNC/RDP 只能使用密码凭据")
			return
		}
	case "prompt":
		h.CredentialID = ""
		h.PasswordEnc = ""
	default:
		writeError(w, 400, "invalid_auth_mode", "不支持的认证方式")
		return
	}
	if err := a.store.SaveHost(h); err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	saved, _ := a.store.Host(u.ID, h.ID)
	writeJSON(w, 200, saved)
}
func (a *API) hostSessions(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	out, err := a.store.ActiveSessionsForHost(u.ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, nonNil(out))
}
func (a *API) testHost(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	host, err := a.store.Host(u.ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 404, "host_not_found", "主机不存在")
		return
	}
	if host.Protocol == "vnc" || host.Protocol == "rdp" {
		latency, testErr := a.desktops.Test(r.Context(), u.ID, host)
		if testErr != nil {
			_ = a.store.UpdateHostConnection(u.ID, host.ID, "offline", 0)
			writeError(w, 502, connectionErrorCode(testErr), testErr.Error())
			return
		}
		latencyMS := int(latency.Milliseconds())
		_ = a.store.UpdateHostConnection(u.ID, host.ID, "online", latencyMS)
		writeJSON(w, 200, map[string]any{"latencyMs": latencyMS, "protocol": host.Protocol})
		return
	}
	var in sessionInput
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	credentialID := in.CredentialID
	if credentialID == "" {
		credentialID = host.CredentialID
	}
	var credential store.Credential
	if credentialID != "" {
		credential, err = a.store.Credential(u.ID, credentialID)
		if err != nil {
			writeError(w, 400, "credential_not_found", "凭据不存在")
			return
		}
	}
	result, err := a.terminals.Test(r.Context(), u.ID, host, credential, in.Secret, in.Passphrase, in.TrustFingerprint)
	if err != nil {
		_ = a.store.UpdateHostConnection(u.ID, host.ID, "offline", 0)
		var hk *terminal.HostKeyError
		if errors.As(err, &hk) {
			writeJSONStatus(w, 409, hostKeyErrorBody(hk))
			return
		}
		writeError(w, 502, connectionErrorCode(err), err.Error())
		return
	}
	_ = a.store.UpdateHostConnection(u.ID, host.ID, "online", int(result.LatencyMS))
	writeJSON(w, 200, result)
}
func connectionErrorCode(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "tmux is required"), strings.Contains(message, "tmux: command not found"):
		return "tmux_missing"
	case strings.Contains(message, "unable to authenticate"), strings.Contains(message, "permission denied"):
		return "authentication_failed"
	case strings.Contains(message, "no such host"):
		return "dns_failed"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return "network_timeout"
	case strings.Contains(message, "connection refused"):
		return "connection_refused"
	default:
		return "connection_failed"
	}
}
func hostKeyErrorBody(err *terminal.HostKeyError) map[string]any {
	return map[string]any{
		"code": err.Kind, "message": "请确认远程主机指纹", "fingerprint": err.Fingerprint,
		"hostName": err.HostName, "hostAddress": err.Address,
	}
}
func (a *API) deleteHost(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id := chi.URLParam(r, "id")
	services, _ := a.store.WebServices(u.ID)
	if err := a.store.DeleteHost(u.ID, id); err != nil {
		writeError(w, 409, "host_in_use", err.Error())
		return
	}
	a.agents.Disconnect(u.ID, id)
	for _, service := range services {
		if service.HostID == id {
			a.webProxies.deleteStable(u.ID, service.ID)
			a.webProxies.deleteHostPort(service.ID)
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) credentials(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.Credentials(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, nonNil(out))
}
func (a *API) saveCredential(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct{ ID, Name, Kind, Secret, Passphrase string }
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" || (in.Kind != "password" && in.Kind != "key") || in.Secret == "" {
		writeError(w, 400, "invalid_credential", "请填写凭据名称和内容")
		return
	}
	enc, err := a.vault.Encrypt(in.Secret)
	if err != nil {
		writeError(w, 500, "encryption_error", "凭据加密失败")
		return
	}
	pass := ""
	if in.Passphrase != "" {
		pass, err = a.vault.Encrypt(in.Passphrase)
		if err != nil {
			writeError(w, 500, "encryption_error", "凭据加密失败")
			return
		}
	}
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	c := store.Credential{ID: in.ID, UserID: u.ID, Name: in.Name, Kind: in.Kind, Secret: enc, Passphrase: pass}
	if err = a.store.SaveCredential(c); err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	saved, _ := a.store.Credential(u.ID, c.ID)
	saved.Secret = ""
	saved.Passphrase = ""
	writeJSON(w, 200, saved)
}
func (a *API) deleteCredential(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id := chi.URLParam(r, "id")
	if err := a.store.DeleteCredential(u.ID, id); err != nil {
		writeError(w, 409, "credential_in_use", err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type sessionInput struct{ HostID, CredentialID, Secret, Passphrase, TrustFingerprint, Name, SessionMode string }

func (a *API) createSession(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in sessionInput
	if !decode(w, r, &in) {
		return
	}
	host, err := a.store.Host(u.ID, in.HostID)
	if err != nil {
		writeError(w, 404, "host_not_found", "主机不存在")
		return
	}
	if host.Protocol != "" && host.Protocol != "ssh" {
		writeError(w, 400, "invalid_terminal_host", "VNC/RDP 主机只能打开远程桌面")
		return
	}
	var cred store.Credential
	if in.CredentialID == "" {
		in.CredentialID = host.CredentialID
	}
	if in.CredentialID != "" {
		cred, err = a.store.Credential(u.ID, in.CredentialID)
		if err != nil {
			writeError(w, 400, "credential_not_found", "凭据不存在")
			return
		}
	}
	if in.SessionMode != "" && in.SessionMode != "tmux" && in.SessionMode != "normal" {
		writeError(w, 400, "invalid_session_mode", "不支持的终端会话模式")
		return
	}
	meta, err := a.terminals.Create(r.Context(), u.ID, terminal.CreateRequest{Host: host, Credential: cred, Secret: in.Secret, Passphrase: in.Passphrase, TrustFingerprint: in.TrustFingerprint, Name: in.Name, SessionMode: in.SessionMode})
	if err != nil {
		var hk *terminal.HostKeyError
		if errors.As(err, &hk) {
			writeJSONStatus(w, 409, hostKeyErrorBody(hk))
			return
		}
		writeError(w, 502, connectionErrorCode(err), err.Error())
		return
	}
	writeJSON(w, 201, meta)
}
func (a *API) sessions(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.Terminals(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, nonNil(out))
}
func (a *API) sessionDirectory(w http.ResponseWriter, r *http.Request) {
	session, err := a.terminals.Get(currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 404, "session_not_attached", "终端当前未连接")
		return
	}
	directory, err := session.CurrentDirectory()
	if err != nil || directory == "" {
		writeError(w, 502, "directory_unavailable", "无法读取终端当前目录")
		return
	}
	writeJSON(w, 200, map[string]string{"path": directory})
}

func (a *API) sessionForeground(w http.ResponseWriter, r *http.Request) {
	session, err := a.terminals.Get(currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 404, "session_not_attached", "终端当前未连接")
		return
	}
	command, err := session.ForegroundCommand()
	if err != nil {
		writeError(w, 502, "foreground_unavailable", "无法读取终端当前进程")
		return
	}
	writeJSON(w, 200, map[string]string{"command": command})
}

func (a *API) sessionDockerStatus(w http.ResponseWriter, r *http.Request) {
	installed, err := a.terminals.DockerInstalled(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 502, "docker_status_unavailable", "无法检测远端 Docker")
		return
	}
	writeJSON(w, 200, map[string]bool{"installed": installed})
}
func (a *API) updateSession(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct{ Name string }
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		writeError(w, 400, "invalid_session_name", "会话名称需为 1 到 80 个字符")
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.terminals.Rename(u.ID, id, in.Name); err != nil {
		writeError(w, 404, "session_not_found", "会话不存在")
		return
	}
	updated, _ := a.store.Terminal(u.ID, id)
	writeJSON(w, 200, updated)
}
func (a *API) restoreSession(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in sessionInput
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	s, err := a.terminals.Restore(r.Context(), u.ID, chi.URLParam(r, "id"), in.Secret, in.Passphrase, in.TrustFingerprint)
	if err != nil {
		if errors.Is(err, terminal.ErrNormalSessionEnded) {
			writeError(w, http.StatusGone, "normal_session_ended", "普通 SSH 会话已结束，无法在服务重启后恢复")
			return
		}
		var hk *terminal.HostKeyError
		if errors.As(err, &hk) {
			writeJSONStatus(w, 409, hostKeyErrorBody(hk))
			return
		}
		writeError(w, 502, "restore_failed", err.Error())
		return
	}
	writeJSON(w, 200, s.Meta())
}
func (a *API) terminateSession(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in sessionInput
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.terminals.Terminate(r.Context(), u.ID, id, in.Secret, in.Passphrase); err != nil {
		writeError(w, 502, "terminate_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) workspace(w http.ResponseWriter, r *http.Request) {
	raw, version, err := a.store.Workspace(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"layout": raw, "version": version})
}
func (a *API) saveWorkspace(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in struct {
		Layout  json.RawMessage `json:"layout"`
		Version int             `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Layout) > 256*1024 {
		writeError(w, 413, "workspace_too_large", "工作区数据过大")
		return
	}
	if err := a.store.SaveWorkspace(u.ID, in.Layout, in.Version); err != nil {
		writeError(w, 409, "workspace_conflict", err.Error())
		return
	}
	raw, ver, _ := a.store.Workspace(u.ID)
	writeJSON(w, 200, map[string]any{"layout": raw, "version": ver})
}
func (a *API) preferences(w http.ResponseWriter, r *http.Request) {
	raw, err := a.store.Preferences(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, raw)
}
func (a *API) savePreferences(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
	if err != nil || !json.Valid(raw) {
		writeError(w, 400, "invalid_preferences", "设置格式无效")
		return
	}
	if err = a.store.SavePreferences(u.ID, raw); err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, json.RawMessage(raw))
}

func (a *API) exportData(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	hosts, err := a.store.Hosts(u.ID)
	if err != nil {
		writeError(w, 500, "database_error", "导出失败")
		return
	}
	for i := range hosts {
		hosts[i].UserID = ""
		hosts[i].CredentialID = ""
	}
	preferences, _ := a.store.Preferences(u.ID)
	snippets, _ := a.store.Snippets(u.ID)
	webServices, _ := a.store.WebServices(u.ID)
	for i := range webServices {
		webServices[i].UserID = ""
	}
	w.Header().Set("Content-Disposition", `attachment; filename="velin-export.json"`)
	writeJSON(w, 200, map[string]any{"format": "velin-export", "version": 1, "exportedAt": time.Now().UTC(), "hosts": hosts, "preferences": preferences, "snippets": snippets, "webServices": webServices})
}

type openSSHImport struct {
	Content string `json:"content"`
	Commit  bool   `json:"commit"`
}
type importedHost struct {
	Alias, HostName, User, IdentityFile, ProxyJump string
	Port                                           int
	Warnings                                       []string
}

func parseOpenSSH(content string) []importedHost {
	var out []importedHost
	var current *importedHost
	flush := func() {
		if current != nil && current.Alias != "" && current.HostName != "" {
			if current.Port == 0 {
				current.Port = 22
			}
			out = append(out, *current)
		}
		current = nil
	}
	for number, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key, value := strings.ToLower(fields[0]), strings.Join(fields[1:], " ")
		if key == "host" {
			flush()
			if strings.ContainsAny(value, "*?!") {
				continue
			}
			current = &importedHost{Alias: value, Port: 22}
			continue
		}
		if current == nil {
			continue
		}
		switch key {
		case "hostname":
			current.HostName = value
		case "user":
			current.User = value
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				current.Warnings = append(current.Warnings, fmt.Sprintf("第 %d 行端口无效", number+1))
			} else {
				current.Port = port
			}
		case "identityfile":
			current.IdentityFile = value
		case "proxyjump":
			current.ProxyJump = value
		default:
			current.Warnings = append(current.Warnings, fmt.Sprintf("未导入字段 %s", fields[0]))
		}
	}
	flush()
	return out
}
func (a *API) importOpenSSH(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var in openSSHImport
	if !decode(w, r, &in) {
		return
	}
	if len(in.Content) > 512*1024 {
		writeError(w, 413, "import_too_large", "OpenSSH 配置超过 512 KiB")
		return
	}
	parsed := parseOpenSSH(in.Content)
	preview := make([]map[string]any, 0, len(parsed))
	created := 0
	for _, item := range parsed {
		warnings := append([]string{}, item.Warnings...)
		if item.IdentityFile != "" {
			warnings = append(warnings, "IdentityFile 仅记录路径提示，浏览器服务无法读取客户端本地私钥")
		}
		if item.ProxyJump != "" {
			warnings = append(warnings, "ProxyJump 已识别，请导入后在主机编辑中选择对应跳板机并绑定凭据")
		}
		preview = append(preview, map[string]any{"name": item.Alias, "address": item.HostName, "port": item.Port, "username": item.User, "identityFile": item.IdentityFile, "proxyJump": item.ProxyJump, "warnings": warnings})
		if !in.Commit {
			continue
		}
		username := item.User
		if username == "" {
			username = "root"
		}
		host := store.Host{ID: uuid.NewString(), UserID: u.ID, Name: item.Alias, Address: item.HostName, Port: item.Port, Username: username, GroupName: "OpenSSH 导入", ConnectTimeout: 12, KeepaliveInterval: 30, MaxRetries: 5, TerminalType: "xterm-256color", Notes: strings.TrimSpace(strings.Join([]string{optionalNote("IdentityFile", item.IdentityFile), optionalNote("ProxyJump", item.ProxyJump)}, "\n"))}
		if a.store.SaveHost(host) == nil {
			created++
		}
	}
	if in.Commit {
	}
	writeJSON(w, 200, map[string]any{"hosts": preview, "created": created})
}
func optionalNote(label, value string) string {
	if value == "" {
		return ""
	}
	return label + ": " + value
}

func (a *API) snippets(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.Snippets(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", "加载命令片段失败")
		return
	}
	writeJSON(w, 200, nonNil(out))
}
func (a *API) saveSnippet(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var value store.Snippet
	if !decode(w, r, &value) {
		return
	}
	value.ID = chi.URLParam(r, "id")
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	value.UserID = u.ID
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" || value.Command == "" || len(value.Command) > 64*1024 {
		writeError(w, 400, "invalid_snippet", "请填写名称和 64 KiB 以内的命令正文")
		return
	}
	if err := a.store.SaveSnippet(value); err != nil {
		writeError(w, 500, "database_error", "保存片段失败")
		return
	}
	writeJSON(w, 200, value)
}
func (a *API) deleteSnippet(w http.ResponseWriter, r *http.Request) {
	_ = a.store.DeleteSnippet(currentUser(r).ID, chi.URLParam(r, "id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) portForwards(w http.ResponseWriter, r *http.Request) {
	out, err := a.forwards.List(currentUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", "加载转发失败")
		return
	}
	writeJSON(w, 200, nonNil(out))
}
func (a *API) savePortForward(w http.ResponseWriter, r *http.Request) {
	var value store.PortForward
	if !decode(w, r, &value) {
		return
	}
	if id := chi.URLParam(r, "id"); id != "" {
		value.ID = id
	}
	saved, err := a.forwards.Save(currentUser(r).ID, value)
	if err != nil {
		writeError(w, 400, "invalid_forward", err.Error())
		return
	}
	writeJSON(w, 200, saved)
}
func (a *API) startPortForward(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id := chi.URLParam(r, "id")
	if err := a.forwards.Start(r.Context(), u.ID, id); err != nil {
		writeError(w, 502, "forward_start_failed", err.Error())
		return
	}
	value, _ := a.store.PortForward(u.ID, id)
	writeJSON(w, 200, value)
}
func (a *API) stopPortForward(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id := chi.URLParam(r, "id")
	if err := a.forwards.Stop(u.ID, id); err != nil {
		writeError(w, 404, "forward_not_found", "转发不存在")
		return
	}
	value, _ := a.store.PortForward(u.ID, id)
	writeJSON(w, 200, value)
}
func (a *API) deletePortForward(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id := chi.URLParam(r, "id")
	if err := a.forwards.Delete(u.ID, id); err != nil {
		writeError(w, 404, "forward_not_found", "转发不存在")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) users(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.DB.Query(`SELECT id,username,role,disabled,force_password_change,created_at FROM users ORDER BY created_at`)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer rows.Close()
	var out []store.User
	for rows.Next() {
		var u store.User
		_ = rows.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.ForcePasswordChange, &u.CreatedAt)
		out = append(out, u)
	}
	writeJSON(w, 200, nonNil(out))
}
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password, Role string }
	if !decode(w, r, &in) {
		return
	}
	policy := a.securityPolicy()
	if len(in.Username) < 3 || len(in.Password) < policy.PasswordMinLength {
		writeError(w, 400, "weak_account", fmt.Sprintf("用户名至少 3 位，密码至少 %d 位", policy.PasswordMinLength))
		return
	}
	if in.Role != "admin" {
		in.Role = "user"
	}
	hash, err := security.HashPassword(in.Password)
	if err != nil {
		writeError(w, 500, "internal_error", err.Error())
		return
	}
	id := uuid.NewString()
	if err = a.store.CreateUser(id, in.Username, hash, in.Role); err != nil {
		writeError(w, 409, "user_exists", "用户名已存在")
		return
	}
	if policy.ForceChangeOnCreate {
		if err = a.store.SetForcePasswordChange(id, true); err != nil {
			writeError(w, http.StatusInternalServerError, "database_error", "创建用户失败")
			return
		}
	}
	u, _ := a.store.UserByID(id)
	writeJSON(w, 201, u)
}
func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	var in struct {
		Username *string
		Role     *string
		Disabled *bool
		Password string
	}
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := a.store.UserByID(id); err != nil {
		writeError(w, 404, "not_found", "用户不存在")
		return
	}
	if id == actor.ID && in.Disabled != nil && *in.Disabled {
		writeError(w, 400, "cannot_disable_self", "不能禁用当前账户")
		return
	}
	if id == actor.ID && in.Role != nil && *in.Role != "admin" {
		writeError(w, 400, "cannot_demote_self", "不能降低当前账户的管理员权限")
		return
	}
	username := ""
	if in.Username != nil {
		username = strings.TrimSpace(*in.Username)
		if len(username) < 3 {
			writeError(w, 400, "weak_account", "用户名至少 3 位")
			return
		}
	}
	role := ""
	if in.Role != nil {
		role = strings.TrimSpace(*in.Role)
		if role != "admin" && role != "user" {
			writeError(w, 400, "invalid_role", "用户角色无效")
			return
		}
	}
	var passwordHash string
	if in.Password != "" {
		policy := a.securityPolicy()
		if len(in.Password) < policy.PasswordMinLength {
			writeError(w, 400, "weak_password", fmt.Sprintf("密码至少 %d 位", policy.PasswordMinLength))
			return
		}
		var err error
		passwordHash, err = security.HashPassword(in.Password)
		if err != nil {
			writeError(w, 500, "internal_error", err.Error())
			return
		}
	}
	tx, err := a.store.DB.Begin()
	if err != nil {
		writeError(w, 500, "database_error", "更新用户失败")
		return
	}
	defer tx.Rollback()
	if in.Username != nil {
		if _, err = tx.Exec(`UPDATE users SET username=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, username, id); err != nil {
			writeError(w, 409, "user_exists", "用户名已存在")
			return
		}
	}
	if in.Role != nil {
		if _, err = tx.Exec(`UPDATE users SET role=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, role, id); err != nil {
			writeError(w, 500, "database_error", "更新用户角色失败")
			return
		}
	}
	if in.Disabled != nil {
		if _, err = tx.Exec(`UPDATE users SET disabled=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, *in.Disabled, id); err != nil {
			writeError(w, 500, "database_error", "更新用户状态失败")
			return
		}
	}
	if passwordHash != "" {
		if _, err = tx.Exec(`UPDATE users SET password_hash=?,force_password_change=1,updated_at=CURRENT_TIMESTAMP WHERE id=?`, passwordHash, id); err != nil {
			writeError(w, 500, "database_error", "修改用户密码失败")
			return
		}
		if _, err = tx.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, id); err != nil {
			writeError(w, 500, "database_error", "撤销用户登录失败")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "database_error", "更新用户失败")
		return
	}
	u, err := a.store.UserByID(id)
	if err != nil {
		writeError(w, 404, "not_found", "用户不存在")
		return
	}
	writeJSON(w, 200, u)
}
func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	id := chi.URLParam(r, "id")
	if id == actor.ID {
		writeError(w, 400, "cannot_delete_self", "不能删除当前账户")
		return
	}
	var active int
	_ = a.store.DB.QueryRow(`SELECT COUNT(*) FROM terminal_sessions WHERE user_id=? AND status<>'ended'`, id).Scan(&active)
	if active > 0 {
		writeJSONStatus(w, 409, map[string]any{"code": "user_has_active_sessions", "message": "该用户仍有远程活动会话，不能删除", "activeSessions": active})
		return
	}
	result, err := a.store.DB.Exec(`DELETE FROM users WHERE id=?`, id)
	if err != nil {
		writeError(w, 500, "database_error", "删除用户失败")
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		writeError(w, 404, "not_found", "用户不存在")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) getSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.securityPolicy())
}
func (a *API) saveSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	var policy securityPolicy
	if !decode(w, r, &policy) {
		return
	}
	if policy.PasswordMinLength < 10 || policy.PasswordMinLength > 128 || policy.LoginFailureThreshold < 3 || policy.LoginFailureThreshold > 20 || policy.LockMinutes < 1 || policy.LockMinutes > 1440 || policy.RememberDays < 1 || policy.RememberDays > 90 {
		writeError(w, 400, "invalid_security_policy", "安全策略参数超出允许范围")
		return
	}
	if err := a.store.SaveSystemSetting("security_policy", policy); err != nil {
		writeError(w, 500, "database_error", "保存安全策略失败")
		return
	}
	writeJSON(w, 200, policy)
}
func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	var users, activeUsers, hosts, sessions int
	_ = a.store.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	_ = a.store.DB.QueryRow(`SELECT COUNT(DISTINCT user_id) FROM auth_sessions WHERE expires_at>CURRENT_TIMESTAMP`).Scan(&activeUsers)
	_ = a.store.DB.QueryRow(`SELECT COUNT(*) FROM hosts`).Scan(&hosts)
	_ = a.store.DB.QueryRow(`SELECT COUNT(*) FROM terminal_sessions WHERE status NOT IN ('ended')`).Scan(&sessions)
	var dbSize int64
	if info, err := os.Stat(a.cfg.DatabasePath); err == nil {
		dbSize = info.Size()
	}
	entries, _ := filepath.Glob(filepath.Join(a.cfg.DataDir, "velin-*.db.enc"))
	latestBackup := ""
	for _, file := range entries {
		if info, err := os.Stat(file); err == nil && (latestBackup == "" || info.ModTime().Format(time.RFC3339) > latestBackup) {
			latestBackup = info.ModTime().Format(time.RFC3339)
		}
	}
	writeJSON(w, 200, map[string]any{"users": users, "activeUsers": activeUsers, "hosts": hosts, "sessions": sessions, "websockets": a.websockets.Load(), "httpRequests": a.requests.Load(), "databaseBytes": dbSize, "backups": len(entries), "latestBackupAt": latestBackup, "uptimeSeconds": int(time.Since(a.started).Seconds()), "deploymentID": a.cfg.DeploymentID, "goVersion": runtime.Version()})
}
func (a *API) backup(w http.ResponseWriter, r *http.Request) {
	a.backupMu.Lock()
	defer a.backupMu.Unlock()
	var input struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := security.ValidateBackupKey(input.Key); err != nil {
		writeError(w, 400, "invalid_backup_key", "备份密钥至少 12 个字符且不能超过 256 个字符")
		return
	}
	name := "velin-" + time.Now().UTC().Format("20060102-150405") + "-" + uuid.NewString()[:8] + ".db.enc"
	path := filepath.Join(a.cfg.DataDir, name)
	temp, err := os.CreateTemp(a.cfg.DataDir, ".velin-backup-*.db")
	if err != nil {
		writeError(w, 500, "backup_failed", err.Error())
		return
	}
	tempPath := temp.Name()
	if err = temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		writeError(w, 500, "backup_failed", err.Error())
		return
	}
	defer os.Remove(tempPath)
	if err = a.store.Backup(r.Context(), tempPath); err != nil {
		writeError(w, 500, "backup_failed", err.Error())
		return
	}
	if err = store.VerifyBackup(tempPath); err != nil {
		writeError(w, 500, "backup_verify_failed", err.Error())
		return
	}
	if err = security.EncryptBackupBundle(tempPath, a.cfg.MasterKeyPath, path, input.Key); err != nil {
		_ = os.Remove(path)
		writeError(w, 500, "backup_encrypt_failed", "备份加密失败")
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		_ = os.Remove(path)
		writeError(w, 500, "backup_read_failed", err.Error())
		return
	}
	sum := sha256.Sum256(raw)
	checksum := hex.EncodeToString(sum[:])
	_ = os.WriteFile(path+".sha256", []byte(checksum+"  "+name+"\n"), 0o600)
	entries, _ := filepath.Glob(filepath.Join(a.cfg.DataDir, "velin-*.db.enc"))
	if len(entries) > 10 {
		sort.Strings(entries)
		for _, old := range entries[:len(entries)-10] {
			_ = os.Remove(old)
			_ = os.Remove(old + ".sha256")
		}
	}
	writeJSON(w, 201, map[string]any{"file": name, "sha256": checksum, "verified": true, "encrypted": true, "schemaVersion": 13})
}
func (a *API) backups(w http.ResponseWriter, r *http.Request) {
	entries, _ := filepath.Glob(filepath.Join(a.cfg.DataDir, "velin-*.db.enc"))
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))
	out := make([]map[string]any, 0, len(entries))
	for _, file := range entries {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		sum := ""
		if raw, e := os.ReadFile(file + ".sha256"); e == nil {
			sum = strings.Fields(string(raw))[0]
		}
		out = append(out, map[string]any{"file": filepath.Base(file), "size": info.Size(), "createdAt": info.ModTime(), "sha256": sum})
	}
	writeJSON(w, 200, out)
}
func (a *API) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "uptime_seconds": int(time.Since(a.started).Seconds())})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DB.PingContext(r.Context()); err != nil {
		writeError(w, 503, "not_ready", "数据库不可用")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "uptime_seconds": int(time.Since(a.started).Seconds())})
}

func (a *API) csrfToken(w http.ResponseWriter, r *http.Request) {
	token := ""
	if cookie, err := r.Cookie(csrfCookieName); err == nil && len(cookie.Value) >= 32 {
		token = cookie.Value
	}
	if token == "" {
		var err error
		token, err = security.RandomToken(24)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "csrf_failed", "无法建立安全会话")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: token, Path: "/", Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (a *API) metrics(w http.ResponseWriter, _ *http.Request) {
	total, nanos := a.httpTotal.Load(), a.httpNanos.Load()
	average := float64(0)
	if total > 0 {
		average = float64(nanos) / float64(total) / float64(time.Second)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# TYPE velin_uptime_seconds gauge\nvelin_uptime_seconds %d\n", int(time.Since(a.started).Seconds()))
	fmt.Fprintf(w, "# TYPE velin_http_requests_total counter\nvelin_http_requests_total %d\n", total)
	fmt.Fprintf(w, "# TYPE velin_http_errors_total counter\nvelin_http_errors_total %d\n", a.httpErrors.Load())
	fmt.Fprintf(w, "# TYPE velin_http_responses_total counter\nvelin_http_responses_total{class=\"2xx\"} %d\nvelin_http_responses_total{class=\"3xx\"} %d\nvelin_http_responses_total{class=\"4xx\"} %d\nvelin_http_responses_total{class=\"5xx\"} %d\n", a.http2xx.Load(), a.http3xx.Load(), a.http4xx.Load(), a.http5xx.Load())
	fmt.Fprintf(w, "# TYPE velin_http_request_duration_seconds_average gauge\nvelin_http_request_duration_seconds_average %.6f\n", average)
	fmt.Fprintf(w, "# TYPE velin_websockets gauge\nvelin_websockets %d\n", a.websockets.Load())
	fmt.Fprintf(w, "# TYPE velin_websocket_connections_total counter\nvelin_websocket_connections_total %d\n", a.wsTotal.Load())
	fmt.Fprintf(w, "# TYPE velin_terminal_sessions gauge\nvelin_terminal_sessions %d\n", a.terminals.ActiveCount())
	fmt.Fprintf(w, "# TYPE velin_port_forwards gauge\nvelin_port_forwards %d\n", a.forwards.ActiveCount())
	fmt.Fprintf(w, "# TYPE velin_web_proxy_sessions gauge\nvelin_web_proxy_sessions %d\n", a.webProxies.activeCount())
}

type terminalWSMessage struct {
	Type, Data, Requester  string
	Foreground, Background string
	Rows, Cols             int
	Position, HistorySize  int
	Sequence               uint64
	Approved               bool
}

func forwardTerminalEventDuringReplay(writeJSON func(any) error, ev terminal.Event, pendingOutput *[]terminal.Event) error {
	if ev.Type != "output" {
		return writeJSON(ev)
	}
	previewEvent := ev
	previewEvent.Type = "replay_live"
	if err := writeJSON(previewEvent); err != nil {
		return err
	}
	*pendingOutput = append(*pendingOutput, ev)
	return nil
}

func (a *API) terminalWS(w http.ResponseWriter, r *http.Request) {
	u, tokenHash, err := a.currentAuthSession(r)
	if err != nil || u.Disabled {
		writeError(w, 401, "unauthorized", "请重新登录")
		return
	}
	if u.SessionLocked {
		writeError(w, http.StatusLocked, "session_locked", "当前工作区已锁定")
		return
	}
	id := chi.URLParam(r, "id")
	s, err := a.terminals.Get(u.ID, id)
	if err != nil {
		writeError(w, 409, "session_not_attached", "请先恢复会话")
		return
	}
	up := websocket.Upgrader{CheckOrigin: sameOrigin, ReadBufferSize: 4096, WriteBufferSize: 32768}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	a.websockets.Add(1)
	a.wsTotal.Add(1)
	defer a.websockets.Add(-1)
	defer conn.Close()
	conn.SetReadLimit(128 * 1024)
	var writeMu sync.Mutex
	writeJSON := func(value any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(value)
	}
	clientID := uuid.NewString()
	reconnectKey := r.URL.Query().Get("reconnect")
	if _, parseErr := uuid.Parse(reconnectKey); parseErr != nil {
		reconnectKey = ""
	}
	resumeStreamID := r.URL.Query().Get("stream")
	resumeOffset, _ := strconv.ParseUint(r.URL.Query().Get("offset"), 10, 64)
	events, subscriptionDone, replay := s.Subscribe(clientID, resumeStreamID, resumeOffset, reconnectKey)
	defer s.Unsubscribe(clientID)
	controller := ""
	if s.IsController(clientID) {
		controller = clientID
	}
	meta := s.Meta()
	if err = writeJSON(terminal.Event{Type: "hello", ClientID: clientID, Controller: controller, Status: meta.Status, Message: meta.LastError, Truncated: replay.Truncated, StreamID: replay.StreamID, Offset: replay.Offset, HistorySize: s.CachedHistorySize()}); err != nil {
		return
	}
	go func() {
		size := s.HistorySize()
		_ = writeJSON(terminal.Event{Type: "history_state", HistorySize: size, Position: size})
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer conn.Close()
		lockTicker := time.NewTicker(time.Second)
		defer lockTicker.Stop()
		pendingOutput := make([]terminal.Event, 0, 16)
		forwardDuringReplay := func(ev terminal.Event) bool {
			return forwardTerminalEventDuringReplay(writeJSON, ev, &pendingOutput) == nil
		}
		drainReplayEvents := func(limit int) bool {
			for count := 0; limit < 0 || count < limit; count++ {
				select {
				case ev := <-events:
					if !forwardDuringReplay(ev) {
						return false
					}
				case <-subscriptionDone:
					return false
				case <-lockTicker.C:
					locked, lockErr := a.store.AuthSessionLocked(tokenHash)
					if lockErr != nil || locked {
						return false
					}
				default:
					return true
				}
			}
			return true
		}
		fullReplay := resumeStreamID == "" || resumeStreamID != replay.StreamID || replay.Truncated
		if fullReplay {
			if preview := s.Snapshot(200); len(preview) > 0 {
				if writeJSON(terminal.Event{Type: "replay_preview", Data: base64.StdEncoding.EncodeToString(preview)}) != nil {
					return
				}
			}
		}
		for index, segment := range replay.Segments {
			if writeJSON(terminal.Event{Type: "replay", Data: base64.StdEncoding.EncodeToString(segment), ReplayFinal: index+1 == len(replay.Segments)}) != nil {
				return
			}
			if !drainReplayEvents(16) {
				return
			}
		}
		if len(replay.Segments) == 0 {
			if writeJSON(terminal.Event{Type: "replay_end"}) != nil {
				return
			}
		}
		if !drainReplayEvents(-1) {
			return
		}
		for _, ev := range pendingOutput {
			if writeJSON(ev) != nil {
				return
			}
		}
		for {
			select {
			case ev, ok := <-events:
				if !ok || writeJSON(ev) != nil {
					return
				}
			case <-subscriptionDone:
				return
			case <-lockTicker.C:
				locked, lockErr := a.store.AuthSessionLocked(tokenHash)
				if lockErr != nil || locked {
					return
				}
			}
		}
	}()
	handleMessage := func(msg terminalWSMessage) {
		position := msg.Position
		if position == 0 && msg.Rows != 0 {
			position = msg.Rows
		}
		historySize := msg.HistorySize
		if historySize == 0 && msg.Cols != 0 {
			historySize = msg.Cols
		}
		switch msg.Type {
		case "input":
			raw, decodeErr := base64.StdEncoding.DecodeString(msg.Data)
			if decodeErr == nil {
				_ = s.Write(clientID, raw)
			}
		case "resize":
			_ = s.Resize(clientID, msg.Rows, msg.Cols)
		case "scroll_history":
			_ = s.ScrollHistory(clientID, msg.Rows)
		case "scroll_history_to":
			s.QueueHistoryPosition(clientID, position, historySize, msg.Sequence)
		case "history_state":
			go s.SendHistoryState(clientID)
		case "terminal_theme":
			_ = s.SetTerminalColors(clientID, msg.Foreground, msg.Background)
		case "request_control":
			if s.RequestControl(clientID) {
				_ = writeJSON(terminal.Event{Type: "control_granted", Controller: clientID})
			} else {
				_ = writeJSON(terminal.Event{Type: "control_pending"})
			}
		case "control_response":
			s.RespondControl(clientID, msg.Requester, msg.Approved)
		case "release_control":
			s.ReleaseControl(clientID)
		case "ping":
			s.TouchControl(clientID)
			_ = writeJSON(terminal.Event{Type: "pong"})
		}
	}
	for {
		var msg terminalWSMessage
		if err = conn.ReadJSON(&msg); err != nil {
			return
		}
		if locked, lockErr := a.store.AuthSessionLocked(tokenHash); lockErr != nil || locked {
			return
		}
		handleMessage(msg)
		select {
		case <-done:
			return
		default:
		}
	}
}

func (a *API) staticHandler() http.Handler {
	dist := a.cfg.WebDist
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(dist, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			http.ServeFile(w, r, path)
			return
		}
		index := filepath.Join(dist, "index.html")
		if _, err := os.Stat(index); err == nil {
			http.ServeFile(w, r, index)
			return
		}
		http.Error(w, "Velin frontend has not been built", http.StatusServiceUnavailable)
	})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, 400, "invalid_json", "请求格式无效")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeJSONStatus(w http.ResponseWriter, status int, v any) { writeJSON(w, status, v) }
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

var _ fs.FS
var _ = strconv.Itoa
