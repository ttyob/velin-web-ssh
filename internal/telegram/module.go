// Package telegram implements the optional, scoped Telegram interface to live
// sessions. Its tables and transport are independent of browser authentication.
package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/sessionbridge"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

var ErrNotBound = errors.New("尚未绑定 Telegram")

type Files interface {
	Upload(context.Context, string, string, string, io.Reader) (string, error)
	Download(context.Context, string, string, string) (string, []byte, error)
}
type Options struct {
	APIBaseURL   string
	PollInterval time.Duration
}
type ConfigInput struct {
	Enabled     bool    `json:"enabled"`
	BotToken    string  `json:"botToken"`
	ProxyURL    *string `json:"proxyURL"`
	PublicURL   string  `json:"publicURL"`
	IdleSeconds int     `json:"idleSeconds"`
}
type config struct {
	Enabled     bool   `json:"enabled"`
	Token       string `json:"tokenEncrypted"`
	Proxy       string `json:"proxyEncrypted"`
	Bot         Bot    `json:"bot"`
	PublicURL   string `json:"publicURL"`
	IdleSeconds int    `json:"idleSeconds"`
}
type Status struct {
	Enabled     bool   `json:"enabled"`
	HasToken    bool   `json:"hasToken"`
	HasProxy    bool   `json:"hasProxy"`
	Username    string `json:"username"`
	PublicURL   string `json:"publicURL"`
	IdleSeconds int    `json:"idleSeconds"`
	State       string `json:"state"`
	LastError   string `json:"lastError"`
}
type Binding struct {
	UserID         string `json:"-"`
	TokenID        string `json:"tokenID"`
	TelegramUserID int64  `json:"telegramUserID"`
	ChatID         int64  `json:"-"`
	Name           string `json:"name"`
	BotID          int64  `json:"-"`
	AllowFiles     bool   `json:"allowFiles"`
	BoundAt        string `json:"boundAt"`
	Active         bool   `json:"active"`
}
type Pair struct {
	URL       string    `json:"url"`
	Command   string    `json:"command"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type runtime struct {
	cancel context.CancelFunc
	done   chan struct{}
	client *Client
}
type Module struct {
	store            *store.Store
	vault            *security.Vault
	bridge           *sessionbridge.Module
	files            Files
	options          Options
	control          sync.Mutex
	mu               sync.RWMutex
	cfg              config
	state, lastError string
	run              *runtime
	closed           bool
}

func New(s *store.Store, vault *security.Vault, bridge *sessionbridge.Module, files Files, options Options) (*Module, error) {
	_, err := s.DB.Exec(`
CREATE TABLE IF NOT EXISTS telegram_bindings (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 token_id TEXT NOT NULL REFERENCES session_bridge_tokens(id) ON DELETE CASCADE,
 telegram_user_id INTEGER NOT NULL DEFAULT 0, chat_id INTEGER NOT NULL DEFAULT 0,
 name TEXT NOT NULL DEFAULT '', bot_id INTEGER NOT NULL DEFAULT 0,
 allow_files INTEGER NOT NULL DEFAULT 0, bound_at TEXT NOT NULL DEFAULT '',
 pair_hash TEXT NOT NULL DEFAULT '', pair_expires TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS telegram_account ON telegram_bindings(telegram_user_id) WHERE telegram_user_id != 0;
CREATE TABLE IF NOT EXISTS telegram_routes (
 user_id TEXT NOT NULL REFERENCES telegram_bindings(user_id) ON DELETE CASCADE,
 chat_id INTEGER NOT NULL, message_id INTEGER NOT NULL, session_id TEXT NOT NULL,
 kind TEXT NOT NULL, expires_at TEXT NOT NULL, PRIMARY KEY(chat_id,message_id)
);
CREATE TABLE IF NOT EXISTS telegram_actions (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES telegram_bindings(user_id) ON DELETE CASCADE,
 chat_id INTEGER NOT NULL, payload TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS telegram_subscriptions (
 user_id TEXT NOT NULL REFERENCES telegram_bindings(user_id) ON DELETE CASCADE,
 session_id TEXT NOT NULL, state TEXT NOT NULL DEFAULT '{}', PRIMARY KEY(user_id,session_id)
);
CREATE TABLE IF NOT EXISTS telegram_runtime (bot_id INTEGER PRIMARY KEY, next_update INTEGER NOT NULL DEFAULT 0);
`)
	if err != nil {
		return nil, err
	}
	m := &Module{store: s, vault: vault, bridge: bridge, files: files, options: options, state: "stopped", cfg: config{IdleSeconds: 90}}
	if err = s.SystemSetting("telegram_bot", &m.cfg); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if m.cfg.IdleSeconds < 30 {
		m.cfg.IdleSeconds = 90
	}
	return m, nil
}
func (m *Module) Start() {
	m.control.Lock()
	defer m.control.Unlock()
	if m.closed || m.run != nil || !m.configuration().Enabled {
		return
	}
	if err := m.start(m.configuration()); err != nil {
		m.setState("error", err.Error())
	}
}
func (m *Module) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Status{Enabled: m.cfg.Enabled, HasToken: m.cfg.Token != "", HasProxy: m.cfg.Proxy != "", Username: m.cfg.Bot.Username, PublicURL: m.cfg.PublicURL, IdleSeconds: m.cfg.IdleSeconds, State: m.state, LastError: m.lastError}
}
func (m *Module) setState(state, message string) {
	m.mu.Lock()
	m.state, m.lastError = state, message
	m.mu.Unlock()
}
func (m *Module) configuration() config { m.mu.RLock(); defer m.mu.RUnlock(); return m.cfg }
func (m *Module) credentials(cfg config) (string, string, error) {
	token, err := m.vault.Decrypt(cfg.Token)
	if err != nil {
		return "", "", errors.New("无法读取 Bot Token，请重新配置")
	}
	proxy := ""
	if cfg.Proxy != "" {
		proxy, err = m.vault.Decrypt(cfg.Proxy)
		if err != nil {
			return "", "", errors.New("无法读取 Telegram 代理，请重新配置")
		}
	}
	return token, proxy, nil
}
func validToken(token string) bool {
	left, right, ok := strings.Cut(token, ":")
	if !ok || len(left) < 1 || len(right) < 20 || len(token) > 256 {
		return false
	}
	for _, r := range left {
		if r < '0' || r > '9' {
			return false
		}
	}
	for _, r := range right {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func (m *Module) candidate(input ConfigInput) (config, string, string, error) {
	cfg := m.configuration()
	cfg.Enabled = input.Enabled
	cfg.PublicURL = strings.TrimRight(strings.TrimSpace(input.PublicURL), "/")
	cfg.IdleSeconds = input.IdleSeconds
	if cfg.IdleSeconds == 0 {
		cfg.IdleSeconds = 90
	}
	if cfg.IdleSeconds < 30 || cfg.IdleSeconds > 3600 {
		return cfg, "", "", errors.New("无输出提醒时间须为 30～3600 秒")
	}
	if cfg.PublicURL != "" {
		u, err := url.Parse(cfg.PublicURL)
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			return cfg, "", "", errors.New("工作台地址须为 HTTP/HTTPS 地址，不包含查询参数或登录令牌")
		}
		cfg.PublicURL = strings.TrimSuffix(cfg.PublicURL, "/workspace")
	}
	token, proxy := "", ""
	if cfg.Token != "" {
		var err error
		token, proxy, err = m.credentials(cfg)
		if err != nil && strings.TrimSpace(input.BotToken) == "" {
			return cfg, "", "", err
		}
	}
	if input.BotToken != "" {
		token = strings.TrimSpace(input.BotToken)
	}
	if token != "" && !validToken(token) {
		return cfg, "", "", errors.New("Bot Token 格式无效")
	}
	if input.ProxyURL != nil {
		proxy = strings.TrimSpace(*input.ProxyURL)
	}
	if len(proxy) > 4096 {
		return cfg, "", "", errors.New("代理地址过长")
	}
	return cfg, token, proxy, nil
}
func (m *Module) Test(ctx context.Context, input ConfigInput) (Bot, error) {
	_, token, proxy, err := m.candidate(input)
	if err != nil {
		return Bot{}, err
	}
	if token == "" {
		return Bot{}, errors.New("请先填写 Bot Token")
	}
	client, err := newClient(token, proxy, m.options.APIBaseURL)
	if err != nil {
		return Bot{}, err
	}
	defer client.close()
	return client.validate(ctx)
}
func (m *Module) Configure(ctx context.Context, input ConfigInput) (Status, error) {
	m.control.Lock()
	defer m.control.Unlock()
	if m.closed {
		return m.Status(), errors.New("服务正在关闭")
	}
	cfg, token, proxy, err := m.candidate(input)
	if err != nil {
		return m.Status(), err
	}
	if cfg.Enabled && token == "" {
		return m.Status(), errors.New("请先填写 Bot Token")
	}
	if token != "" {
		client, err := newClient(token, proxy, m.options.APIBaseURL)
		if err != nil {
			return m.Status(), err
		}
		defer client.close()
		if cfg.Enabled || input.BotToken != "" {
			bot, err := client.validate(ctx)
			if err != nil {
				return m.Status(), err
			}
			cfg.Bot = bot
		}
		cfg.Token, err = m.vault.Encrypt(token)
		if err != nil {
			return m.Status(), err
		}
	}
	cfg.Proxy = ""
	if proxy != "" {
		cfg.Proxy, err = m.vault.Encrypt(proxy)
		if err != nil {
			return m.Status(), err
		}
	}
	if err = m.store.SaveSystemSetting("telegram_bot", cfg); err != nil {
		return m.Status(), errors.New("无法保存 Telegram 配置")
	}
	m.stop()
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	if cfg.Enabled {
		if err = m.start(cfg); err != nil {
			m.setState("error", err.Error())
			return m.Status(), err
		}
	} else {
		m.setState("stopped", "")
	}
	return m.Status(), nil
}
func (m *Module) start(cfg config) error {
	token, proxy, err := m.credentials(cfg)
	if err != nil {
		return err
	}
	client, err := newClient(token, proxy, m.options.APIBaseURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &runtime{cancel: cancel, done: make(chan struct{}), client: client}
	m.run = r
	m.setState("connecting", "")
	go func() {
		defer close(r.done)
		defer client.close()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); m.poll(ctx, client, cfg) }()
		go func() { defer wg.Done(); m.watch(ctx, client, cfg) }()
		wg.Wait()
	}()
	return nil
}
func (m *Module) stop() {
	if m.run != nil {
		m.run.cancel()
		<-m.run.done
		m.run = nil
	}
}
func (m *Module) Close() {
	m.control.Lock()
	defer m.control.Unlock()
	m.closed = true
	m.stop()
	m.setState("stopped", "")
}

const bindingColumns = "user_id,token_id,telegram_user_id,chat_id,name,bot_id,allow_files,bound_at"

func scanBinding(row interface{ Scan(...any) error }) (Binding, error) {
	var b Binding
	err := row.Scan(&b.UserID, &b.TokenID, &b.TelegramUserID, &b.ChatID, &b.Name, &b.BotID, &b.AllowFiles, &b.BoundAt)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotBound
	}
	return b, err
}
func (m *Module) Binding(userID string) (Binding, error) {
	b, err := scanBinding(m.store.DB.QueryRow("SELECT "+bindingColumns+" FROM telegram_bindings WHERE user_id=?", userID))
	b.Active = b.TelegramUserID != 0 && b.BotID == m.configuration().Bot.ID
	return b, err
}
func (m *Module) SaveAccess(userID, tokenID string, files bool) error {
	if _, err := m.bridge.AccessToken(userID, tokenID); err != nil {
		return errors.New("会话授权无效")
	}
	_, err := m.store.DB.Exec(`INSERT INTO telegram_bindings(user_id,token_id,allow_files) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET token_id=excluded.token_id,allow_files=excluded.allow_files`, userID, tokenID, files)
	return err
}
func randomID() string {
	var data [24]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(data[:])
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func (m *Module) CreatePair(userID string) (Pair, error) {
	cfg := m.configuration()
	if !cfg.Enabled || cfg.Bot.Username == "" {
		return Pair{}, errors.New("请先由管理员配置并开启机器人")
	}
	b, err := m.Binding(userID)
	if err != nil {
		return Pair{}, errors.New("请先保存会话授权范围")
	}
	if _, err = m.bridge.AccessToken(userID, b.TokenID); err != nil {
		return Pair{}, errors.New("会话授权已失效，请先保存授权")
	}
	code := randomID()
	expires := time.Now().Add(10 * time.Minute)
	_, err = m.store.DB.Exec(`UPDATE telegram_bindings SET pair_hash=?,pair_expires=? WHERE user_id=?`, hash(code), stamp(expires), userID)
	return Pair{URL: "https://t.me/" + cfg.Bot.Username + "?start=" + code, Command: "/start " + code, ExpiresAt: expires}, err
}
func (m *Module) pair(message Message, code string, bot Bot) (Binding, error) {
	if len(code) != 32 {
		return Binding{}, errors.New("绑定链接无效或已过期，请在工作台重新生成")
	}
	prior, err := scanBinding(m.store.DB.QueryRow("SELECT "+bindingColumns+" FROM telegram_bindings WHERE pair_hash=? AND pair_expires>?", hash(code), stamp(time.Now())))
	if err != nil {
		return Binding{}, errors.New("绑定链接无效或已过期，请在工作台重新生成")
	}
	if _, err = m.bridge.AccessToken(prior.UserID, prior.TokenID); err != nil {
		return Binding{}, errors.New("会话授权已失效，请重新保存授权")
	}
	tx, err := m.store.DB.Begin()
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()
	b, err := scanBinding(tx.QueryRow("SELECT "+bindingColumns+" FROM telegram_bindings WHERE pair_hash=? AND pair_expires>?", hash(code), stamp(time.Now())))
	if err != nil {
		return Binding{}, errors.New("绑定链接无效或已过期，请在工作台重新生成")
	}
	if b.TokenID != prior.TokenID || b.UserID != prior.UserID {
		return Binding{}, errors.New("绑定信息已更新，请重新生成链接")
	}
	// Authorization is checked outside this transaction to avoid occupying a
	// SQLite connection while re-entering the shared authorization service.
	userID := b.UserID
	if err = tx.QueryRow("SELECT user_id FROM session_bridge_tokens WHERE id=? AND user_id=? AND enabled=1", b.TokenID, userID).Scan(&userID); err != nil {
		return Binding{}, errors.New("会话授权已失效")
	}
	var disabled, force bool
	if err = tx.QueryRow("SELECT disabled,force_password_change FROM users WHERE id=?", userID).Scan(&disabled, &force); err != nil || disabled || force {
		return Binding{}, errors.New("请先在工作台完成账户验证")
	}
	name := message.From.FirstName
	if message.From.Username != "" {
		name += " (@" + message.From.Username + ")"
	}
	_, err = tx.Exec(`UPDATE telegram_bindings SET telegram_user_id=?,chat_id=?,name=?,bot_id=?,bound_at=?,pair_hash='',pair_expires='' WHERE user_id=?`, message.From.ID, message.Chat.ID, name, bot.ID, stamp(time.Now()), b.UserID)
	if err != nil {
		return Binding{}, errors.New("此 Telegram 账号已经绑定其他用户，请先解除原绑定")
	}
	for _, table := range []string{"telegram_routes", "telegram_actions", "telegram_subscriptions"} {
		if _, err = tx.Exec("DELETE FROM "+table+" WHERE user_id=?", b.UserID); err != nil {
			return Binding{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Binding{}, err
	}
	_ = m.store.Audit(b.UserID, "telegram.bind", "telegram", b.UserID, "", map[string]any{"telegramUserID": message.From.ID})
	return m.Binding(b.UserID)
}
func (m *Module) Unbind(userID string) error {
	b, err := m.Binding(userID)
	if errors.Is(err, ErrNotBound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = m.store.DB.Exec("DELETE FROM telegram_bindings WHERE user_id=?", userID)
	if err != nil {
		return err
	}
	_ = m.bridge.DeleteToken(userID, b.TokenID)
	_ = m.store.Audit(userID, "telegram.unbind", "telegram", userID, "", nil)
	return nil
}
func (m *Module) authenticate(user, chat, bot int64) (Binding, sessionbridge.Token, error) {
	b, err := scanBinding(m.store.DB.QueryRow("SELECT "+bindingColumns+" FROM telegram_bindings WHERE telegram_user_id=? AND chat_id=? AND bot_id=?", user, chat, bot))
	if err != nil {
		return b, sessionbridge.Token{}, ErrNotBound
	}
	token, err := m.bridge.AccessToken(b.UserID, b.TokenID)
	return b, token, err
}
func (m *Module) authorized(b Binding) (sessionbridge.Token, error) {
	current, err := m.Binding(b.UserID)
	if err != nil || current.TokenID != b.TokenID || current.ChatID != b.ChatID || current.TelegramUserID != b.TelegramUserID || current.BotID != b.BotID {
		return sessionbridge.Token{}, ErrNotBound
	}
	return m.bridge.AccessToken(b.UserID, b.TokenID)
}
