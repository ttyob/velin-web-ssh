package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/html"
	"velin-webssh/internal/security"
	"velin-webssh/internal/store"
	"velin-webssh/internal/terminal"
)

const (
	webProxyIdleTTL      = 2 * time.Hour
	webProxyMaxTTL       = 12 * time.Hour
	maxRewriteBody       = 8 << 20
	maxWebProxies        = 8
	webServiceQuery      = "__velin_web_service"
	hostPortAccessQuery  = "__velin_access"
	hostPortAccessCookie = "velin_host_port_"
)

var (
	cssURLPattern    = regexp.MustCompile(`(?i)url\(\s*[^)]*\)`)
	cssImportPattern = regexp.MustCompile(`(?i)@import\s+["'][^"']+["']`)
)

type webProxyManager struct {
	terminals      *terminal.Manager
	listenAddress  string
	mu             sync.Mutex
	sessions       map[string]*webProxySession
	stableSessions map[string]*webProxySession
	stableCreating map[string]*webProxyCreation
	hostPorts      map[string]*hostPortWebProxy
	hostPortAccess map[string]*hostPortAccess
}

type hostPortAccess struct {
	userID, serviceID, authTokenHash string
	expiresAt                        time.Time
	activated                        bool
}

type hostPortWebProxy struct {
	port     int
	listener net.Listener
	server   *http.Server
}

type webProxyCreation struct {
	ready chan struct{}
	err   error
}

type webProxySession struct {
	token        string
	userID       string
	hostID       string
	target       *url.URL
	upstream     string
	client       *ssh.Client
	transport    *http.Transport
	proxy        *httputil.ReverseProxy
	createdAt    time.Time
	lastUsedAt   time.Time
	active       int
	routePrefix  string
	stable       bool
	rootProxy    bool
	viteDevMode  atomic.Bool
	cookieMu     sync.Mutex
	cookies      map[string]*http.Cookie
	onProxyError func()
}

type webProxyInput struct {
	HostID        string `json:"hostID"`
	TargetURL     string `json:"targetURL"`
	UpstreamHost  string `json:"upstreamHost"`
	SkipTLSVerify bool   `json:"skipTLSVerify"`
}

func newWebProxyManager(terminals *terminal.Manager, listenAddress string) *webProxyManager {
	m := &webProxyManager{
		terminals: terminals, listenAddress: listenAddress, sessions: make(map[string]*webProxySession),
		stableSessions: make(map[string]*webProxySession),
		stableCreating: make(map[string]*webProxyCreation),
		hostPorts:      make(map[string]*hostPortWebProxy),
		hostPortAccess: make(map[string]*hostPortAccess),
	}
	go m.cleanupLoop()
	return m
}

func (m *webProxyManager) issueHostPortAccess(userID, serviceID, authTokenHash string) (string, error) {
	token, err := security.RandomToken(24)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.hostPortAccess[token] = &hostPortAccess{userID: userID, serviceID: serviceID, authTokenHash: authTokenHash, expiresAt: time.Now().Add(12 * time.Hour)}
	m.mu.Unlock()
	return token, nil
}

func (m *webProxyManager) hostPortAuthorization(token, userID, serviceID string, activate bool) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	access := m.hostPortAccess[token]
	if access == nil || access.userID != userID || access.serviceID != serviceID || time.Now().After(access.expiresAt) || (!activate && !access.activated) {
		delete(m.hostPortAccess, token)
		return "", false
	}
	if activate {
		if access.activated {
			return "", false
		}
		access.activated = true
	}
	return access.authTokenHash, true
}

func (m *webProxyManager) activeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions) + len(m.stableSessions)
}

func validateWebProxyInput(in webProxyInput) (*url.URL, string, error) {
	if strings.TrimSpace(in.HostID) == "" {
		return nil, "", errors.New("请选择代理主机")
	}
	target, err := url.Parse(strings.TrimSpace(in.TargetURL))
	if err != nil || target.Hostname() == "" || target.User != nil || target.Fragment != "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, "", errors.New("目标必须是有效的 HTTP 或 HTTPS 地址")
	}
	if target.Port() != "" {
		port, portErr := strconv.Atoi(target.Port())
		if portErr != nil || port < 1 || port > 65535 {
			return nil, "", errors.New("目标端口无效")
		}
	}
	upstream := strings.TrimSpace(in.UpstreamHost)
	if upstream == "" {
		upstream = target.Host
	} else {
		parsed, parseErr := url.Parse("//" + upstream)
		if parseErr != nil || parsed.Host != upstream || parsed.Hostname() == "" || strings.ContainsAny(upstream, "\r\n/\\") {
			return nil, "", errors.New("上游 Host 无效")
		}
		if parsed.Port() != "" {
			port, portErr := strconv.Atoi(parsed.Port())
			if portErr != nil || port < 1 || port > 65535 {
				return nil, "", errors.New("上游 Host 端口无效")
			}
		}
	}
	return target, upstream, nil
}

func (m *webProxyManager) create(ctx context.Context, userID string, in webProxyInput) (*webProxySession, error) {
	target, upstream, err := validateWebProxyInput(in)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	active := 0
	for _, session := range m.sessions {
		if session.userID == userID && !session.expired(time.Now()) {
			active++
		}
	}
	for _, session := range m.stableSessions {
		if session.userID == userID && !session.expired(time.Now()) {
			active++
		}
	}
	m.mu.Unlock()
	if active >= maxWebProxies {
		return nil, errors.New("同时打开的 Web 代理已达到上限")
	}
	client, _, err := m.terminals.DialSaved(ctx, userID, in.HostID)
	if err != nil {
		return nil, err
	}
	token, err := security.RandomToken(18)
	if err != nil {
		client.Close()
		return nil, err
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           sshDialContext(client),
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 45 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         target.Hostname(),
			InsecureSkipVerify: in.SkipTLSVerify, // User must opt in for private services with self-signed certificates.
		},
	}
	now := time.Now()
	session := &webProxySession{
		token: token, userID: userID, hostID: in.HostID, target: target,
		upstream: upstream, client: client, transport: transport,
		createdAt: now, lastUsedAt: now,
		cookies: make(map[string]*http.Cookie),
	}
	session.proxy = session.reverseProxy()
	m.mu.Lock()
	active = 0
	for _, existing := range m.sessions {
		if existing.userID == userID && !existing.expired(time.Now()) {
			active++
		}
	}
	for _, existing := range m.stableSessions {
		if existing.userID == userID && !existing.expired(time.Now()) {
			active++
		}
	}
	if active >= maxWebProxies {
		m.mu.Unlock()
		session.close()
		return nil, errors.New("同时打开的 Web 代理已达到上限")
	}
	m.sessions[token] = session
	m.mu.Unlock()
	return session, nil
}

func sshDialContext(client *ssh.Client) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		type result struct {
			conn net.Conn
			err  error
		}
		ready := make(chan result, 1)
		go func() {
			conn, err := client.Dial(network, address)
			ready <- result{conn: conn, err: err}
		}()
		select {
		case <-ctx.Done():
			go func() {
				value := <-ready
				if value.conn != nil {
					_ = value.conn.Close()
				}
			}()
			return nil, ctx.Err()
		case value := <-ready:
			return value.conn, value.err
		}
	}
}

func (m *webProxyManager) get(userID, token string) *webProxySession {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[token]
	if session == nil || session.userID != userID || session.expired(time.Now()) {
		if session != nil && session.expired(time.Now()) {
			delete(m.sessions, token)
			go session.close()
		}
		return nil
	}
	session.lastUsedAt = time.Now()
	session.active++
	return session
}

func (m *webProxyManager) release(session *webProxySession) {
	m.mu.Lock()
	if session.active > 0 {
		session.active--
	}
	session.lastUsedAt = time.Now()
	m.mu.Unlock()
}

func stableWebProxyKey(userID, serviceID string) string {
	return userID + "\x00" + serviceID
}

func stableWebProxyPrefix(serviceID string) string {
	return "/web-service-proxy/" + serviceID
}

func (m *webProxyManager) getOrCreateStable(ctx context.Context, userID, serviceID string, in webProxyInput, rootProxy bool) (*webProxySession, error) {
	key := stableWebProxyKey(userID, serviceID)
	for {
		var expired *webProxySession
		m.mu.Lock()
		if session := m.stableSessions[key]; session != nil {
			if !session.expired(time.Now()) && session.rootProxy == rootProxy {
				session.lastUsedAt = time.Now()
				session.active++
				m.mu.Unlock()
				return session, nil
			}
			delete(m.stableSessions, key)
			expired = session
		}
		if creation := m.stableCreating[key]; creation != nil {
			m.mu.Unlock()
			if expired != nil {
				expired.close()
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-creation.ready:
				if creation.err != nil {
					return nil, creation.err
				}
				continue
			}
		}
		creation := &webProxyCreation{ready: make(chan struct{})}
		m.stableCreating[key] = creation
		m.mu.Unlock()
		if expired != nil {
			expired.close()
		}

		session, err := m.create(ctx, userID, in)
		m.mu.Lock()
		if err == nil {
			delete(m.sessions, session.token)
			if !rootProxy {
				session.routePrefix = stableWebProxyPrefix(serviceID)
			}
			session.stable = true
			session.rootProxy = rootProxy
			session.onProxyError = func() { m.invalidateStable(userID, serviceID, session) }
			m.stableSessions[key] = session
		}
		creation.err = err
		delete(m.stableCreating, key)
		close(creation.ready)
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
}

func (m *webProxyManager) checkHostPort(serviceID string, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("主机端口范围应为 1 到 65535")
	}
	m.mu.Lock()
	for id, current := range m.hostPorts {
		if id != serviceID && current.port == port {
			m.mu.Unlock()
			return fmt.Errorf("端口 %d 已被其他内网 Web 使用", port)
		}
	}
	current := m.hostPorts[serviceID]
	m.mu.Unlock()
	if current != nil && current.port == port {
		return nil
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(m.hostPortAddress(), strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("无法监听主机端口 %d：%w", port, err)
	}
	return listener.Close()
}

func (m *webProxyManager) setHostPort(serviceID string, port int, handler http.Handler) error {
	m.mu.Lock()
	if current := m.hostPorts[serviceID]; current != nil && current.port == port {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	listener, err := net.Listen("tcp4", net.JoinHostPort(m.hostPortAddress(), strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("无法监听主机端口 %d：%w", port, err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	next := &hostPortWebProxy{port: port, listener: listener, server: server}
	m.mu.Lock()
	previous := m.hostPorts[serviceID]
	m.hostPorts[serviceID] = next
	m.mu.Unlock()
	if previous != nil {
		_ = previous.server.Close()
		_ = previous.listener.Close()
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			m.mu.Lock()
			if m.hostPorts[serviceID] == next {
				delete(m.hostPorts, serviceID)
			}
			m.mu.Unlock()
		}
	}()
	return nil
}

func (m *webProxyManager) hostPortAddress() string {
	if strings.TrimSpace(m.listenAddress) == "" {
		return "127.0.0.1"
	}
	return m.listenAddress
}

func (m *webProxyManager) deleteHostPort(serviceID string) {
	m.mu.Lock()
	current := m.hostPorts[serviceID]
	delete(m.hostPorts, serviceID)
	m.mu.Unlock()
	if current != nil {
		_ = current.server.Close()
		_ = current.listener.Close()
	}
}

func (m *webProxyManager) invalidateStable(userID, serviceID string, session *webProxySession) {
	key := stableWebProxyKey(userID, serviceID)
	m.mu.Lock()
	if m.stableSessions[key] != session {
		m.mu.Unlock()
		return
	}
	delete(m.stableSessions, key)
	m.mu.Unlock()
	session.close()
}

func (m *webProxyManager) deleteStable(userID, serviceID string) {
	key := stableWebProxyKey(userID, serviceID)
	m.mu.Lock()
	session := m.stableSessions[key]
	delete(m.stableSessions, key)
	m.mu.Unlock()
	if session != nil {
		session.close()
	}
}

func (m *webProxyManager) delete(userID, token string) bool {
	m.mu.Lock()
	session := m.sessions[token]
	if session == nil || session.userID != userID {
		m.mu.Unlock()
		return false
	}
	delete(m.sessions, token)
	m.mu.Unlock()
	session.close()
	return true
}

func (m *webProxyManager) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for now := range ticker.C {
		var expired []*webProxySession
		m.mu.Lock()
		for token, session := range m.sessions {
			if session.expired(now) {
				delete(m.sessions, token)
				expired = append(expired, session)
			}
		}
		for key, session := range m.stableSessions {
			if session.expired(now) {
				delete(m.stableSessions, key)
				expired = append(expired, session)
			}
		}
		for token, access := range m.hostPortAccess {
			if now.After(access.expiresAt) {
				delete(m.hostPortAccess, token)
			}
		}
		m.mu.Unlock()
		for _, session := range expired {
			session.close()
		}
	}
}

func (s *webProxySession) expired(now time.Time) bool {
	return (!s.stable && now.Sub(s.createdAt) > webProxyMaxTTL) || (s.active == 0 && now.Sub(s.lastUsedAt) > webProxyIdleTTL)
}

func (s *webProxySession) close() {
	s.transport.CloseIdleConnections()
	_ = s.client.Close()
}

func (s *webProxySession) prefix() string {
	if s.rootProxy {
		return ""
	}
	if s.routePrefix != "" {
		return s.routePrefix
	}
	return "/web-proxy/" + s.token
}

func (s *webProxySession) reverseProxy() *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Transport:     s.transport,
		FlushInterval: -1,
		Director: func(request *http.Request) {
			prefix := s.prefix()
			forwardedHost := request.Host
			forwardedProto := requestProto(request)
			path := request.URL.Path
			if !s.rootProxy {
				path = strings.TrimPrefix(path, prefix)
			}
			if path == "" {
				path = "/"
			}
			request.URL.Scheme = s.target.Scheme
			request.URL.Host = s.target.Host
			request.URL.Path = joinURLPath(s.target.Path, path)
			request.URL.RawPath = ""
			request.URL.RawQuery = joinRawQuery(s.target.RawQuery, request.URL.RawQuery)
			request.Host = s.upstream
			request.Header.Del("Accept-Encoding")
			if !s.rootProxy {
				request.Header.Del("If-Modified-Since")
				request.Header.Del("If-None-Match")
			}
			request.Header.Set("Cookie", s.upstreamCookieHeader(request))
			request.Header.Set("X-Forwarded-Host", forwardedHost)
			request.Header.Set("X-Forwarded-Proto", forwardedProto)
			if prefix == "" {
				request.Header.Del("X-Forwarded-Prefix")
			} else {
				request.Header.Set("X-Forwarded-Prefix", prefix)
			}
			rewriteRequestOrigin(request, prefix, s.target, s.upstream)
		},
		ModifyResponse: s.modifyResponse,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if s.onProxyError != nil {
				go s.onProxyError()
			}
			writeError(w, http.StatusBadGateway, "web_proxy_failed", fmt.Sprintf("内网 Web 服务访问失败：%v", err))
		},
	}
	return proxy
}

func (s *webProxySession) modifyResponse(response *http.Response) error {
	prefix := s.prefix()
	response.Header.Del("Content-Security-Policy")
	response.Header.Del("Content-Security-Policy-Report-Only")
	response.Header.Del("Clear-Site-Data")
	response.Header.Set("Referrer-Policy", "same-origin")
	response.Header.Set("Service-Worker-Allowed", prefix+"/")
	if location := response.Header.Get("Location"); location != "" {
		response.Header.Set("Location", rewriteProxyResponseURL(location, prefix, s.target, response.Request))
	}
	if refresh := response.Header.Get("Refresh"); refresh != "" {
		response.Header.Set("Refresh", rewriteRefresh(refresh, prefix, s.target))
	}
	setCookies := response.Header.Values("Set-Cookie")
	s.captureUpstreamCookies(setCookies)
	response.Header.Del("Set-Cookie")
	for _, raw := range setCookies {
		if cookie, err := http.ParseSetCookie(raw); err == nil {
			if cookie.Name == cookieName || cookie.Name == csrfCookieName || strings.HasPrefix(cookie.Name, hostPortAccessCookie) {
				continue
			}
			cookie.Domain = ""
			cookie.Path = prefix + "/"
			cookie.Secure = response.Request.Header.Get("X-Forwarded-Proto") == "https"
			response.Header.Add("Set-Cookie", cookie.String())
		}
	}
	if s.rootProxy {
		return nil
	}
	response.Header.Set("Cache-Control", "no-store")
	response.Header.Del("ETag")
	response.Header.Del("Last-Modified")
	mediaType, inferredMediaType := proxyRewriteMediaType(response)
	rewriteJavaScript := s.viteDevMode.Load() && isJavaScriptMediaType(response.Header.Get("Content-Type"))
	if mediaType == "" && !rewriteJavaScript || response.Body == nil || response.ContentLength > maxRewriteBody {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRewriteBody+1))
	if err != nil {
		return err
	}
	if len(body) > maxRewriteBody {
		response.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), response.Body))
		return nil
	}
	_ = response.Body.Close()
	if mediaType == "text/html" {
		if isViteDevelopmentHTML(body) {
			s.viteDevMode.Store(true)
		}
		documentPath := "/"
		if response.Request != nil && response.Request.URL != nil {
			documentPath = proxyDocumentPath(response.Request.URL.Path, s.target)
		}
		body = rewriteHTMLAtPath(body, prefix, s.target, documentPath)
	} else if mediaType == "text/css" {
		body = rewriteCSS(body, prefix, s.target)
	} else if rewriteJavaScript {
		body = rewriteViteJavaScript(body, prefix, s.target)
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	if inferredMediaType {
		response.Header.Set("Content-Type", mediaType+"; charset=utf-8")
	}
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	response.Header.Del("Content-Encoding")
	return nil
}

func proxyRewriteMediaType(response *http.Response) (string, bool) {
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaType == "text/html" || mediaType == "text/css" {
		return mediaType, false
	}
	if mediaType != "" && mediaType != "text/plain" && mediaType != "application/octet-stream" {
		return "", false
	}
	if response.Request == nil || response.Request.URL == nil {
		return "", false
	}
	path := strings.ToLower(response.Request.URL.Path)
	switch {
	case strings.HasSuffix(path, ".html"), strings.HasSuffix(path, ".htm"):
		return "text/html", true
	case strings.HasSuffix(path, ".css"):
		return "text/css", true
	default:
		return "", false
	}
}

func upstreamCookies(request *http.Request) string {
	values := make([]string, 0)
	for _, cookie := range request.Cookies() {
		if cookie.Name != cookieName && cookie.Name != csrfCookieName && !strings.HasPrefix(cookie.Name, hostPortAccessCookie) {
			values = append(values, cookie.String())
		}
	}
	return strings.Join(values, "; ")
}

func (s *webProxySession) upstreamCookieHeader(request *http.Request) string {
	values := make(map[string]string)
	for _, cookie := range request.Cookies() {
		if cookie.Name != cookieName && cookie.Name != csrfCookieName && !strings.HasPrefix(cookie.Name, hostPortAccessCookie) {
			values[cookie.Name] = cookie.Value
		}
	}
	s.cookieMu.Lock()
	for name, cookie := range s.cookies {
		if _, present := values[name]; !present {
			values[name] = cookie.Value
		}
	}
	s.cookieMu.Unlock()
	parts := make([]string, 0, len(values))
	for name, value := range values {
		parts = append(parts, name+"="+value)
	}
	slices.Sort(parts)
	return strings.Join(parts, "; ")
}

func (s *webProxySession) captureUpstreamCookies(rawCookies []string) {
	if len(rawCookies) == 0 {
		return
	}
	now := time.Now()
	s.cookieMu.Lock()
	defer s.cookieMu.Unlock()
	if s.cookies == nil {
		s.cookies = make(map[string]*http.Cookie)
	}
	for _, raw := range rawCookies {
		cookie, err := http.ParseSetCookie(raw)
		if err != nil || cookie.Name == "" || cookie.Name == cookieName || cookie.Name == csrfCookieName || strings.HasPrefix(cookie.Name, hostPortAccessCookie) {
			continue
		}
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(now)) {
			delete(s.cookies, cookie.Name)
			continue
		}
		copy := *cookie
		s.cookies[cookie.Name] = &copy
	}
}

func rewriteRequestOrigin(request *http.Request, prefix string, target *url.URL, upstreamHost string) {
	if origin := request.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			request.Header.Set("Origin", (&url.URL{Scheme: target.Scheme, Host: upstreamHost}).String())
		}
	}

	referer := request.Header.Get("Referer")
	parsed, err := url.Parse(referer)
	if referer == "" || err != nil || !strings.HasPrefix(parsed.Path, prefix) {
		return
	}
	parsed.Scheme, parsed.Host = target.Scheme, upstreamHost
	parsed.Path = joinURLPath(target.Path, strings.TrimPrefix(parsed.Path, prefix))
	request.Header.Set("Referer", parsed.String())
}

func requestProto(request *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		return forwarded
	}
	if request.TLS != nil {
		return "https"
	}
	return "http"
}

func rewriteHTML(body []byte, prefix string, target *url.URL) []byte {
	return rewriteHTMLAtPath(body, prefix, target, "/")
}

func rewriteHTMLAtPath(body []byte, prefix string, target *url.URL, documentPath string) []byte {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return body
	}
	removeUpstreamCSPMeta(document)
	injectWebProxyBootstrap(document, prefix, target, documentPath)
	var rewrite func(*html.Node)
	rewrite = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for i := range node.Attr {
				switch strings.ToLower(node.Attr[i].Key) {
				case "href":
					if serviceIDFromPrefix(prefix) != "" && (strings.EqualFold(node.Data, "a") || strings.EqualFold(node.Data, "area")) {
						node.Attr[i].Val = rewriteBrowserRouteURL(node.Attr[i].Val, prefix, target)
					} else {
						node.Attr[i].Val = rewriteProxyURL(node.Attr[i].Val, prefix, target)
					}
				case "src", "action", "poster":
					node.Attr[i].Val = rewriteProxyURL(node.Attr[i].Val, prefix, target)
				case "srcset":
					node.Attr[i].Val = rewriteSrcset(node.Attr[i].Val, prefix, target)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			rewrite(child)
		}
	}
	rewrite(document)
	var output bytes.Buffer
	if err := html.Render(&output, document); err != nil {
		return body
	}
	return output.Bytes()
}

func removeUpstreamCSPMeta(node *html.Node) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		if isUpstreamCSPMeta(child) {
			node.RemoveChild(child)
		} else {
			removeUpstreamCSPMeta(child)
		}
		child = next
	}
}

func isUpstreamCSPMeta(node *html.Node) bool {
	if node.Type != html.ElementNode || !strings.EqualFold(node.Data, "meta") {
		return false
	}
	for _, attr := range node.Attr {
		if !strings.EqualFold(attr.Key, "http-equiv") {
			continue
		}
		value := strings.TrimSpace(attr.Val)
		return strings.EqualFold(value, "Content-Security-Policy") ||
			strings.EqualFold(value, "Content-Security-Policy-Report-Only")
	}
	return false
}

func isViteDevelopmentHTML(body []byte) bool {
	text := strings.ToLower(string(body))
	return strings.Contains(text, "@vite/client")
}

func isJavaScriptMediaType(value string) bool {
	mediaType, _, _ := mime.ParseMediaType(value)
	switch strings.ToLower(mediaType) {
	case "application/javascript", "application/x-javascript", "text/javascript", "application/ecmascript", "text/ecmascript":
		return true
	default:
		return false
	}
}

var viteImportPattern = regexp.MustCompile(`(?s)(\bfrom\s*|\bimport\s*(?:\(\s*)?)(["'])([^"']+)(["'])`)

func rewriteViteJavaScript(body []byte, prefix string, target *url.URL) []byte {
	rewritten := viteImportPattern.ReplaceAllStringFunc(string(body), func(match string) string {
		groups := viteImportPattern.FindStringSubmatch(match)
		if len(groups) != 5 || groups[2] != groups[4] {
			return match
		}
		rewritten := rewriteProxyURL(groups[3], prefix, target)
		if rewritten == groups[3] {
			return match
		}
		return groups[1] + groups[2] + rewritten + groups[4]
	})
	return []byte(rewritten)
}

func injectWebProxyBootstrap(document *html.Node, prefix string, target *url.URL, documentPath string) {
	var head *html.Node
	var findHead func(*html.Node)
	findHead = func(node *html.Node) {
		if head != nil {
			return
		}
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "head") {
			head = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			findHead(child)
		}
	}
	findHead(document)
	if head == nil {
		return
	}
	script := &html.Node{
		Type: html.ElementNode,
		Data: "script",
		Attr: []html.Attribute{{Key: "data-velin-web-proxy", Val: "runtime"}},
	}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: webProxyBootstrap(prefix, target)})
	head.InsertBefore(script, head.FirstChild)
	base := &html.Node{
		Type: html.ElementNode,
		Data: "base",
		Attr: []html.Attribute{{Key: "href", Val: proxyDocumentBase(prefix, documentPath)}, {Key: "data-velin-web-proxy", Val: "base"}},
	}
	head.InsertBefore(base, script.NextSibling)
}

func proxyDocumentPath(requestPath string, target *url.URL) string {
	documentPath := requestPath
	if target != nil && target.Path != "" && target.Path != "/" {
		documentPath = strings.TrimPrefix(documentPath, target.Path)
	}
	if documentPath == "" || documentPath[0] != '/' {
		documentPath = "/" + documentPath
	}
	return documentPath
}

func proxyDocumentBase(prefix, documentPath string) string {
	directory := path.Dir(ensureLeadingSlash(documentPath))
	if directory == "." || directory == "/" {
		return prefix + "/"
	}
	return prefix + ensureLeadingSlash(directory) + "/"
}

func webProxyBootstrap(prefix string, target *url.URL) string {
	targetPath := target.Path
	if targetPath == "" {
		targetPath = "/"
	}
	serviceID := serviceIDFromPrefix(prefix)
	return `(function(){
  "use strict";
	const prefix=` + strconv.Quote(prefix) + `;
	const targetHost=` + strconv.Quote(target.Host) + `;
	const targetPort=` + strconv.Quote(effectiveURLPort(target)) + `;
	const targetPath=` + strconv.Quote(targetPath) + `;
  const serviceID=` + strconv.Quote(serviceID) + `;
	const serviceQuery=` + strconv.Quote(webServiceQuery) + `;
  const pageHost=location.host;
  const nativePushState=history.pushState.bind(history);
	const nativeReplaceState=history.replaceState.bind(history);
	function targetHostMatches(value){
	 if(value.host===targetHost) return true;
	 const host=value.hostname.toLowerCase();
	 const port=value.port||(value.protocol==="https:"||value.protocol==="wss:"?"443":"80");
	 const loopback=host==="localhost"||host==="::1"||host==="[::1]"||host.startsWith("127.");
	 return loopback&&port===targetPort;
	}
  function browserRoute(value){
    if(!serviceID || value==null) return value;
    let parsed;
    try{parsed=new URL(String(value),location.href);}catch(_){return value;}
    if(parsed.origin!==location.origin) return value;
    if(parsed.pathname===prefix) parsed.pathname="/";
    else if(parsed.pathname.startsWith(prefix+"/")) parsed.pathname=parsed.pathname.slice(prefix.length)||"/";
    parsed.searchParams.set(serviceQuery,serviceID);
    return parsed.href;
  }
  if(serviceID){
    nativeReplaceState(history.state,"",browserRoute(location.href));
    history.pushState=function(state,title,url){return nativePushState(state,title,browserRoute(url));};
    history.replaceState=function(state,title,url){return nativeReplaceState(state,title,browserRoute(url));};
  }
  function proxyURL(value,socket){
    if(typeof value!=="string" && !(value instanceof URL)) return value;
    const original=String(value);
    let parsed;
    try{parsed=new URL(original,location.href);}catch(_){return value;}
    if(!/^(https?|wss?):$/.test(parsed.protocol)) return value;
    const fromPage=parsed.host===pageHost;
	    const fromTarget=targetHostMatches(parsed);
    if(!fromPage && !fromTarget) return value;
    if(fromPage && (parsed.pathname===prefix || parsed.pathname.startsWith(prefix+"/"))) return original;
    if(fromTarget && targetPath!=="/" && (parsed.pathname===targetPath || parsed.pathname.startsWith(targetPath+"/"))){
      parsed.pathname=parsed.pathname.slice(targetPath.length)||"/";
    }
    parsed.host=pageHost;
    parsed.protocol=socket?(location.protocol==="https:"?"wss:":"ws:"):location.protocol;
    parsed.pathname=prefix+(parsed.pathname.startsWith("/")?parsed.pathname:"/"+parsed.pathname);
    return parsed.href;
  }
  const nativeFetch=window.fetch.bind(window);
  window.fetch=function(input,init){
    if(input instanceof Request){
      const next=proxyURL(input.url,false);
      if(next!==input.url) input=new Request(next,input);
    }else input=proxyURL(input,false);
    return nativeFetch(input,init);
  };
  const nativeOpen=XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open=function(method,url){
    const args=Array.prototype.slice.call(arguments);
    args[1]=proxyURL(url,false);
    return nativeOpen.apply(this,args);
  };
  if(window.WebSocket){
    const NativeWebSocket=window.WebSocket;
    window.WebSocket=new Proxy(NativeWebSocket,{construct(Target,args,newTarget){args[0]=proxyURL(args[0],true);return Reflect.construct(Target,args,newTarget);}});
  }
  if(window.EventSource){
    const NativeEventSource=window.EventSource;
    window.EventSource=new Proxy(NativeEventSource,{construct(Target,args,newTarget){args[0]=proxyURL(args[0],false);return Reflect.construct(Target,args,newTarget);}});
  }
  if(navigator.sendBeacon){
    const nativeBeacon=navigator.sendBeacon.bind(navigator);
    navigator.sendBeacon=function(url,data){return nativeBeacon(proxyURL(url,false),data);};
  }
	  const navigationAttributes=new Map([["A","href"],["AREA","href"]]);
	  const urlAttributes=new Map([["FORM","action"],["IFRAME","src"],["FRAME","src"],["IMG","src"],["SCRIPT","src"],["LINK","href"],["SOURCE","src"],["VIDEO","src"],["AUDIO","src"]]);
	  const nativeSetAttribute=Element.prototype.setAttribute;
	  Element.prototype.setAttribute=function(name,value){
	    const attr=String(name).toLowerCase();
	    if(navigationAttributes.get(this.tagName)===attr) value=browserRoute(value);
	    else if(urlAttributes.get(this.tagName)===attr) value=proxyURL(value,false);
	    return nativeSetAttribute.call(this,name,value);
	  };
	  for(const [tag,attr] of new Map([...navigationAttributes,...urlAttributes])){
	    const sample=document.createElement(tag.toLowerCase());
    let owner=sample;
    let descriptor;
    while(owner && !(descriptor=Object.getOwnPropertyDescriptor(owner,attr))) owner=Object.getPrototypeOf(owner);
    if(!descriptor || !descriptor.get || !descriptor.set || descriptor.configurable===false) continue;
	    const rewrite=navigationAttributes.has(tag)?browserRoute:function(value){return proxyURL(value,false);};
	    Object.defineProperty(Object.getPrototypeOf(sample),attr,{configurable:true,enumerable:descriptor.enumerable,get:descriptor.get,set:function(value){return descriptor.set.call(this,rewrite(value));}});
	  }
	  const nativeWindowOpen=window.open.bind(window);
	  window.open=function(url){
	    const args=Array.prototype.slice.call(arguments);
	    if(args.length) args[0]=browserRoute(url);
    return nativeWindowOpen.apply(window,args);
  };
  for(const name of ["Worker","SharedWorker"]){
    if(!window[name]) continue;
    const NativeWorker=window[name];
    window[name]=new Proxy(NativeWorker,{construct(Target,args,newTarget){args[0]=proxyURL(args[0],false);return Reflect.construct(Target,args,newTarget);}});
  }
})();`
}

func serviceIDFromPrefix(prefix string) string {
	if !strings.HasPrefix(prefix, "/web-service-proxy/") {
		return ""
	}
	return strings.TrimPrefix(prefix, "/web-service-proxy/")
}

func serviceIDFromReferer(r *http.Request) string {
	referer, err := url.Parse(strings.TrimSpace(r.Referer()))
	if err != nil || referer.Host == "" || !strings.EqualFold(referer.Host, r.Host) {
		return ""
	}
	if serviceID := strings.TrimSpace(referer.Query().Get(webServiceQuery)); serviceID != "" {
		return serviceID
	}
	const prefix = "/web-service-proxy/"
	if !strings.HasPrefix(referer.Path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(referer.Path, prefix)
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		rest = rest[:slash]
	}
	return strings.TrimSpace(rest)
}

func rewriteBrowserRouteURL(value, prefix string, target *url.URL) string {
	if serviceIDFromPrefix(prefix) == "" {
		return rewriteProxyURL(value, prefix, target)
	}
	serviceID := serviceIDFromPrefix(prefix)
	rewritten := rewriteProxyURL(value, prefix, target)
	parsed, err := url.Parse(rewritten)
	if err != nil || (!strings.HasPrefix(parsed.Path, prefix+"/") && parsed.Path != prefix) {
		return value
	}
	parsed.Path = strings.TrimPrefix(parsed.Path, prefix)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	query := parsed.Query()
	query.Set(webServiceQuery, serviceID)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func rewriteSrcset(value, prefix string, target *url.URL) string {
	parts := strings.Split(value, ",")
	for i, part := range parts {
		fields := strings.Fields(part)
		if len(fields) > 0 {
			fields[0] = rewriteProxyURL(fields[0], prefix, target)
			parts[i] = strings.Join(fields, " ")
		}
	}
	return strings.Join(parts, ", ")
}

func rewriteCSS(body []byte, prefix string, target *url.URL) []byte {
	text := cssURLPattern.ReplaceAllStringFunc(string(body), func(match string) string {
		start, end := strings.IndexByte(match, '('), strings.LastIndexByte(match, ')')
		if start < 0 || end <= start {
			return match
		}
		value := strings.TrimSpace(match[start+1 : end])
		quote := ""
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
			quote, value = value[:1], value[1:len(value)-1]
		}
		return match[:start+1] + quote + rewriteProxyURL(value, prefix, target) + quote + ")"
	})
	text = cssImportPattern.ReplaceAllStringFunc(text, func(match string) string {
		quoteIndex := strings.IndexAny(match, "\"'")
		if quoteIndex < 0 || len(match) <= quoteIndex+1 {
			return match
		}
		quote := match[quoteIndex : quoteIndex+1]
		return match[:quoteIndex+1] + rewriteProxyURL(match[quoteIndex+1:len(match)-1], prefix, target) + quote
	})
	return []byte(text)
}

func rewriteProxyURL(value, prefix string, target *url.URL) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "javascript:") || strings.HasPrefix(trimmed, "mailto:") || strings.HasPrefix(trimmed, "tel:") {
		return value
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return value
	}
	if parsed.Path == prefix || strings.HasPrefix(parsed.Path, prefix+"/") {
		return value
	}
	if parsed.IsAbs() {
		if !isProxyTargetHost(parsed, target) || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss") {
			return value
		}
		return prefix + ensureLeadingSlash(strings.TrimPrefix(parsed.Path, target.Path)) + queryAndFragment(parsed)
	}
	if strings.HasPrefix(trimmed, "//") {
		if !isProxyTargetHost(parsed, target) {
			return value
		}
		return prefix + ensureLeadingSlash(strings.TrimPrefix(parsed.Path, target.Path)) + queryAndFragment(parsed)
	}
	if strings.HasPrefix(parsed.Path, "/") {
		return prefix + parsed.Path + queryAndFragment(parsed)
	}
	return value
}

func rewriteProxyResponseURL(value, prefix string, target *url.URL, request *http.Request) string {
	rewritten := rewriteProxyURL(value, prefix, target)
	if rewritten != value || request == nil {
		return rewritten
	}
	proxyHost := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Host"), ",")[0])
	if proxyHost == "" {
		proxyHost = request.Host
	}
	if proxyHost == "" {
		return value
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Host, proxyHost) {
		return value
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return value
	}
	return prefix + ensureLeadingSlash(strings.TrimPrefix(parsed.Path, target.Path)) + queryAndFragment(parsed)
}

func isProxyTargetHost(candidate, target *url.URL) bool {
	if strings.EqualFold(candidate.Host, target.Host) {
		return true
	}
	if candidate.Port() == "" && strings.EqualFold(candidate.Hostname(), target.Hostname()) {
		return true
	}
	if !isLoopbackHost(candidate.Hostname()) {
		return false
	}
	return effectiveURLPort(candidate) == effectiveURLPort(target)
}

func effectiveURLPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if value.Scheme == "https" || value.Scheme == "wss" {
		return "443"
	}
	return "80"
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func queryAndFragment(value *url.URL) string {
	out := ""
	if value.RawQuery != "" {
		out += "?" + value.RawQuery
	}
	if value.Fragment != "" {
		out += "#" + value.Fragment
	}
	return out
}

func rewriteRefresh(value, prefix string, target *url.URL) string {
	parts := strings.SplitN(value, ";", 2)
	if len(parts) != 2 {
		return value
	}
	assignment := strings.SplitN(parts[1], "=", 2)
	if len(assignment) != 2 || !strings.EqualFold(strings.TrimSpace(assignment[0]), "url") {
		return value
	}
	return parts[0] + "; url=" + rewriteProxyURL(strings.Trim(strings.TrimSpace(assignment[1]), "\"'"), prefix, target)
}

func joinURLPath(base, path string) string {
	if base == "" || base == "/" {
		return ensureLeadingSlash(path)
	}
	return strings.TrimSuffix(base, "/") + ensureLeadingSlash(path)
}

func joinRawQuery(base, request string) string {
	if base == "" {
		return request
	}
	if request == "" {
		return base
	}
	return base + "&" + request
}

func ensureLeadingSlash(value string) string {
	if value == "" {
		return "/"
	}
	if value[0] != '/' {
		return "/" + value
	}
	return value
}

func (a *API) createWebProxy(w http.ResponseWriter, r *http.Request) {
	var in webProxyInput
	if !decode(w, r, &in) {
		return
	}
	if _, _, err := validateWebProxyInput(in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_web_proxy", err.Error())
		return
	}
	user := currentUser(r)
	session, err := a.webProxies.create(r.Context(), user.ID, in)
	if err != nil {
		writeError(w, http.StatusBadGateway, "web_proxy_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     session.token,
		"url":       session.prefix() + "/",
		"expiresAt": session.createdAt.Add(webProxyMaxTTL),
	})
}

func (a *API) webServices(w http.ResponseWriter, r *http.Request) {
	services, err := a.store.WebServices(currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "加载内网 Web 服务失败")
		return
	}
	writeJSON(w, http.StatusOK, nonNil(services))
}

func (a *API) saveWebService(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var value store.WebService
	var previous store.WebService
	hadPrevious := false
	if !decode(w, r, &value) {
		return
	}
	value.ID = chi.URLParam(r, "id")
	if value.ID == "" {
		value.ID = uuid.NewString()
	} else {
		var err error
		previous, err = a.store.WebService(user.ID, value.ID)
		if err != nil {
			writeError(w, http.StatusNotFound, "web_service_not_found", "内网 Web 服务不存在")
			return
		}
		hadPrevious = true
	}
	value.UserID = user.ID
	value.Name = strings.TrimSpace(value.Name)
	value.TargetURL = strings.TrimSpace(value.TargetURL)
	value.UpstreamHost = strings.TrimSpace(value.UpstreamHost)
	if value.ProxyMode == "" {
		value.ProxyMode = "path"
	}
	if value.ProxyMode != "path" && value.ProxyMode != "host_port" {
		writeError(w, http.StatusBadRequest, "invalid_web_service", "不支持的 Web 代理模式")
		return
	}
	if value.ProxyMode == "path" {
		value.ListenPort = 0
	} else {
		if value.ListenPort == configuredListenPort(a.cfg.Addr) {
			writeError(w, http.StatusBadRequest, "invalid_web_service_port", "主机端口不能与 Velin HTTP 端口相同")
			return
		}
		if err := a.webProxies.checkHostPort(value.ID, value.ListenPort); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_web_service_port", err.Error())
			return
		}
	}
	if value.Name == "" || len([]rune(value.Name)) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_web_service", "名称应为 1 到 100 个字符")
		return
	}
	target, upstream, err := validateWebProxyInput(webProxyInput{
		HostID: value.HostID, TargetURL: value.TargetURL,
		UpstreamHost: value.UpstreamHost, SkipTLSVerify: value.SkipTLSVerify,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_web_service", err.Error())
		return
	}
	host, err := a.store.Host(user.ID, value.HostID)
	if err != nil || host.CredentialID == "" {
		writeError(w, http.StatusBadRequest, "invalid_web_service_host", "代理主机不存在或未绑定保存凭据")
		return
	}
	value.TargetURL = target.String()
	if value.UpstreamHost != "" {
		value.UpstreamHost = upstream
	}
	if err = a.store.SaveWebService(value); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "保存内网 Web 服务失败")
		return
	}
	a.webProxies.deleteStable(user.ID, value.ID)
	if value.ProxyMode == "host_port" {
		if err = a.webProxies.setHostPort(value.ID, value.ListenPort, a.hostPortWebServiceHandler(value.UserID, value.ID)); err != nil {
			if hadPrevious {
				_ = a.store.SaveWebService(previous)
			} else {
				_ = a.store.DeleteWebService(user.ID, value.ID)
			}
			writeError(w, http.StatusBadRequest, "web_service_port_failed", err.Error())
			return
		}
	} else {
		a.webProxies.deleteHostPort(value.ID)
	}
	saved, _ := a.store.WebService(user.ID, value.ID)
	writeJSON(w, http.StatusOK, saved)
}

func (a *API) openWebService(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	value, err := a.store.WebService(user.ID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "web_service_not_found", "内网 Web 服务不存在")
		return
	}
	openURL := "/?" + url.Values{webServiceQuery: []string{value.ID}}.Encode()
	if value.ProxyMode == "host_port" {
		access, accessErr := a.webProxies.issueHostPortAccess(user.ID, value.ID, currentAuthTokenHash(r))
		if accessErr != nil {
			writeError(w, http.StatusInternalServerError, "web_service_access_failed", "无法创建内网 Web 访问票据")
			return
		}
		openURL = hostPortWebProxyURL(r, value.ListenPort) + "?" + url.Values{hostPortAccessQuery: []string{access}}.Encode()
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"url": openURL,
	})
}

func (a *API) deleteWebService(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id := chi.URLParam(r, "id")
	if _, err := a.store.WebService(user.ID, id); err != nil {
		writeError(w, http.StatusNotFound, "web_service_not_found", "内网 Web 服务不存在")
		return
	}
	if err := a.store.DeleteWebService(user.ID, id); err != nil {
		writeError(w, http.StatusNotFound, "web_service_not_found", "内网 Web 服务不存在")
		return
	}
	a.webProxies.deleteStable(user.ID, id)
	a.webProxies.deleteHostPort(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) serveStableWebProxy(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	a.serveStableWebService(w, r, user, chi.URLParam(r, "serviceID"))
}

func (a *API) serveStableWebService(w http.ResponseWriter, r *http.Request, user store.User, serviceID string) {
	value, err := a.store.WebService(user.ID, serviceID)
	if err != nil || value.ProxyMode == "host_port" {
		writeError(w, http.StatusNotFound, "web_service_not_found", "内网 Web 服务不存在")
		return
	}
	session, err := a.webProxies.getOrCreateStable(r.Context(), user.ID, serviceID, webProxyInput{
		HostID: value.HostID, TargetURL: value.TargetURL,
		UpstreamHost: value.UpstreamHost, SkipTLSVerify: value.SkipTLSVerify,
	}, false)
	if err != nil {
		writeError(w, http.StatusBadGateway, "web_proxy_create_failed", err.Error())
		return
	}
	defer a.webProxies.release(session)
	serveWebProxySession(w, r, session)
}

func (a *API) markedWebServiceProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serviceID := strings.TrimSpace(r.URL.Query().Get(webServiceQuery))
		if serviceID == "" {
			serviceID = serviceIDFromReferer(r)
		}
		if serviceID == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			next.ServeHTTP(w, r)
			return
		}
		user, tokenHash, err := a.currentAuthSession(r)
		if err != nil || user.Disabled {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请重新登录")
			return
		}
		if user.ForcePasswordChange {
			writeError(w, http.StatusForbidden, "password_change_required", "继续使用前必须修改初始密码")
			return
		}
		if user.SessionLocked {
			writeError(w, http.StatusLocked, "session_locked", "当前工作区已锁定")
			return
		}
		query := r.URL.Query()
		query.Del(webServiceQuery)
		request := r.Clone(context.WithValue(context.WithValue(r.Context(), userKey, user), authTokenHashKey, tokenHash))
		if strings.TrimSpace(r.URL.Query().Get(webServiceQuery)) != "" {
			request.URL.RawQuery = query.Encode()
		}
		a.serveStableWebService(w, request, user, serviceID)
	})
}

func (a *API) hostPortWebServiceHandler(userID, serviceID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieName := hostPortAccessCookie + security.TokenHash(serviceID)[:12]
		accessToken := strings.TrimSpace(r.URL.Query().Get(hostPortAccessQuery))
		activate := accessToken != ""
		if !activate {
			if cookie, cookieErr := r.Cookie(cookieName); cookieErr == nil {
				accessToken = cookie.Value
			}
		}
		authTokenHash, authorized := a.webProxies.hostPortAuthorization(accessToken, userID, serviceID, activate)
		user, locked, authErr := a.store.UserByTokenState(authTokenHash)
		if !authorized || authErr != nil || user.Disabled || user.ID != userID {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先从 Velin 打开此内网 Web")
			return
		}
		if locked {
			writeError(w, http.StatusLocked, "session_locked", "当前工作区已锁定")
			return
		}
		if user.ForcePasswordChange {
			writeError(w, http.StatusForbidden, "password_change_required", "继续使用前必须修改初始密码")
			return
		}
		if activate {
			w.Header().Set("Referrer-Policy", "no-referrer")
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: accessToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 60 * 60})
			query := r.URL.Query()
			query.Del(hostPortAccessQuery)
			target := *r.URL
			target.RawQuery = query.Encode()
			http.Redirect(w, r, target.String(), http.StatusSeeOther)
			return
		}
		value, err := a.store.WebService(userID, serviceID)
		if err != nil || value.ProxyMode != "host_port" {
			http.NotFound(w, r)
			return
		}
		session, err := a.webProxies.getOrCreateStable(r.Context(), userID, serviceID, webProxyInput{
			HostID: value.HostID, TargetURL: value.TargetURL,
			UpstreamHost: value.UpstreamHost, SkipTLSVerify: value.SkipTLSVerify,
		}, true)
		if err != nil {
			writeError(w, http.StatusBadGateway, "web_proxy_create_failed", err.Error())
			return
		}
		defer a.webProxies.release(session)
		serveWebProxySession(w, r, session)
	})
}

func hostPortWebProxyURL(r *http.Request, port int) string {
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	} else {
		host = strings.Trim(host, "[]")
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
}

func (a *API) restoreHostPortWebServices() {
	services, err := a.store.HostPortWebServices()
	if err != nil {
		slog.Error("restore host-port web services", "error", err)
		return
	}
	for _, value := range services {
		if value.ListenPort == configuredListenPort(a.cfg.Addr) {
			slog.Error("restore host-port web service", "service_id", value.ID, "port", value.ListenPort, "error", "port conflicts with Velin HTTP listener")
			continue
		}
		if err = a.webProxies.setHostPort(value.ID, value.ListenPort, a.hostPortWebServiceHandler(value.UserID, value.ID)); err != nil {
			slog.Error("restore host-port web service", "service_id", value.ID, "port", value.ListenPort, "error", err)
		}
	}
}

func configuredListenPort(address string) int {
	_, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(rawPort)
	return port
}

func (a *API) deleteWebProxy(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	token := chi.URLParam(r, "token")
	if !a.webProxies.delete(user.ID, token) {
		writeError(w, http.StatusNotFound, "web_proxy_not_found", "Web 代理不存在或已过期")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) serveWebProxy(w http.ResponseWriter, r *http.Request) {
	session := a.webProxies.get(currentUser(r).ID, chi.URLParam(r, "token"))
	if session == nil {
		writeError(w, http.StatusNotFound, "web_proxy_not_found", "Web 代理不存在或已过期")
		return
	}
	defer a.webProxies.release(session)
	serveWebProxySession(w, r, session)
}

func serveWebProxySession(w http.ResponseWriter, r *http.Request, session *webProxySession) {
	// The application shell's CSP would block assets legitimately loaded by the proxied application.
	w.Header().Del("Content-Security-Policy")
	w.Header().Del("X-Frame-Options")
	w.Header().Set("Content-Security-Policy", webProxyCSP(r.Host, session.prefix()))
	w.Header().Set("Referrer-Policy", "same-origin")
	session.proxy.ServeHTTP(w, r)
}

func webProxyCSP(host, prefix string) string {
	if strings.ContainsAny(host, " \t\r\n;'\"") {
		return "default-src 'none'"
	}
	proxyPath := strings.TrimSuffix(prefix, "/") + "/"
	httpPath := "http://" + host + proxyPath
	httpsPath := "https://" + host + proxyPath
	wsPath := "ws://" + host + proxyPath
	wssPath := "wss://" + host + proxyPath
	web := strings.Join([]string{httpPath, httpsPath}, " ")
	connect := strings.Join([]string{httpPath, httpsPath, wsPath, wssPath}, " ")
	return "default-src 'none'; " +
		// The proxy page is intentionally sandboxed without allow-same-origin. This
		// keeps an untrusted upstream application from sharing the Velin origin.
		"sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads; " +
		"script-src " + web + " data: 'unsafe-inline'; " +
		"style-src " + web + " 'unsafe-inline'; img-src " + web + " data: blob:; " +
		"font-src " + web + " data:; media-src " + web + " blob:; " +
		"connect-src " + connect + "; form-action " + web + "; frame-src " + web + "; " +
		"worker-src " + web + " blob:; object-src 'none'; base-uri 'none'"
}
