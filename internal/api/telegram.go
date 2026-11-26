package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"github.com/ttyob/velin-web-ssh/internal/sessionbridge"
	"github.com/ttyob/velin-web-ssh/internal/telegram"
)

func (a *API) initTelegram() {
	if a.sessionBridge == nil || a.vault == nil {
		return
	}
	module, err := telegram.New(a.store, a.vault, a.sessionBridge, telegramFiles{a}, telegram.Options{})
	if err != nil {
		slog.Error("initialize Telegram integration", "error", err)
		return
	}
	a.telegram = module
}

// StartIntegrations is called after binding the HTTP listener. Failed or
// competing server instances never start a second Telegram update consumer.
func (a *API) StartIntegrations() {
	if a.telegram != nil {
		a.telegram.Start()
	}
}
func (a *API) Close() {
	if a.telegram != nil {
		a.telegram.Close()
	}
}
func (a *API) registerTelegramRoutes(r chi.Router) {
	r.Get("/api/telegram", a.telegramProfile)
	r.Put("/api/telegram/access", a.telegramAccess)
	r.Post("/api/telegram/pair", a.telegramPair)
	r.Delete("/api/telegram/binding", a.telegramUnbind)
	r.Delete("/api/telegram/subscriptions/{sessionID}", a.telegramUnsubscribe)
	r.Group(func(r chi.Router) {
		r.Use(a.adminOnly)
		r.Put("/api/admin/telegram", a.telegramConfigure)
		r.Post("/api/admin/telegram/test", a.telegramTest)
	})
}
func (a *API) telegramAvailable(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if a.telegram == nil {
		writeError(w, 503, "telegram_unavailable", "Telegram 模块暂不可用，请查看服务日志")
		return false
	}
	return true
}
func telegramError(w http.ResponseWriter, err error) {
	writeError(w, 400, "telegram_error", err.Error())
}
func (a *API) telegramProfile(w http.ResponseWriter, r *http.Request) {
	if !a.telegramAvailable(w) {
		return
	}
	userID := currentUser(r).ID
	var binding *telegram.Binding
	var access *sessionbridge.Token
	if value, err := a.telegram.Binding(userID); err == nil {
		binding = &value
		tokens, err := a.sessionBridge.Tokens(userID)
		if err != nil {
			telegramError(w, errors.New("无法读取会话授权"))
			return
		}
		for _, token := range tokens {
			if token.ID == value.TokenID {
				copy := token
				access = &copy
				break
			}
		}
	} else if !errors.Is(err, telegram.ErrNotBound) {
		telegramError(w, errors.New("无法读取绑定信息"))
		return
	}
	subscriptions, err := a.telegram.Subscriptions(userID)
	if err != nil {
		telegramError(w, errors.New("无法读取通知订阅"))
		return
	}
	writeJSON(w, 200, map[string]any{"bot": a.telegram.Status(), "binding": binding, "access": access, "subscriptions": subscriptions})
}
func (a *API) telegramAccess(w http.ResponseWriter, r *http.Request) {
	if !a.telegramAvailable(w) {
		return
	}
	var input struct {
		sessionbridge.TokenInput
		AllowFiles bool `json:"allowFiles"`
	}
	if !decode(w, r, &input) {
		return
	}
	user := currentUser(r)
	input.Name = "Telegram 私聊"
	input.Enabled = true
	var token sessionbridge.Token
	var err error
	if binding, lookupErr := a.telegram.Binding(user.ID); lookupErr == nil {
		token, err = a.sessionBridge.UpdateToken(user.ID, binding.TokenID, input.TokenInput)
	} else if errors.Is(lookupErr, telegram.ErrNotBound) {
		token, _, err = a.sessionBridge.CreateToken(user.ID, input.TokenInput)
	} else {
		err = errors.New("无法读取绑定信息")
	}
	if err != nil {
		telegramError(w, err)
		return
	}
	if err = a.telegram.SaveAccess(user.ID, token.ID, input.AllowFiles); err != nil {
		telegramError(w, err)
		return
	}
	_ = a.store.Audit(user.ID, "telegram.access", "telegram", user.ID, a.clientIP(r), map[string]any{"tokenID": token.ID, "allowFiles": input.AllowFiles})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) telegramPair(w http.ResponseWriter, r *http.Request) {
	if !a.telegramAvailable(w) {
		return
	}
	pair, err := a.telegram.CreatePair(currentUser(r).ID)
	if err != nil {
		telegramError(w, err)
		return
	}
	writeJSON(w, 201, pair)
}
func (a *API) telegramUnbind(w http.ResponseWriter, r *http.Request) {
	if !a.telegramAvailable(w) {
		return
	}
	if err := a.telegram.Unbind(currentUser(r).ID); err != nil {
		telegramError(w, errors.New("解除绑定失败"))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) telegramUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if !a.telegramAvailable(w) {
		return
	}
	if err := a.telegram.Unsubscribe(currentUser(r).ID, chi.URLParam(r, "sessionID")); err != nil {
		telegramError(w, errors.New("关闭通知失败"))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) telegramConfigure(w http.ResponseWriter, r *http.Request) {
	if !a.telegramAvailable(w) {
		return
	}
	var input telegram.ConfigInput
	if !decode(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, err := a.telegram.Configure(ctx, input)
	if err != nil {
		telegramError(w, err)
		return
	}
	_ = a.store.Audit(currentUser(r).ID, "telegram.configure", "telegram", "", a.clientIP(r), map[string]any{"enabled": status.Enabled, "hasProxy": status.HasProxy})
	writeJSON(w, 200, status)
}
func (a *API) telegramTest(w http.ResponseWriter, r *http.Request) {
	if !a.telegramAvailable(w) {
		return
	}
	var input telegram.ConfigInput
	if !decode(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	bot, err := a.telegram.Test(ctx, input)
	if err != nil {
		telegramError(w, err)
		return
	}
	writeJSON(w, 200, bot)
}

type telegramFiles struct{ api *API }

func (b telegramFiles) authorize(ctx context.Context, userID, id string) error {
	binding, err := b.api.telegram.Binding(userID)
	if err != nil || !binding.AllowFiles || !binding.Active {
		return errors.New("文件传输授权已失效")
	}
	token, err := b.api.sessionBridge.AccessToken(userID, binding.TokenID)
	if err != nil {
		return errors.New("会话授权已失效")
	}
	_, err = b.api.sessionBridge.CheckSession(ctx, token, id, sessionbridge.Read)
	return err
}
func (b telegramFiles) open(ctx context.Context, userID, id string) (*sftp.Client, string, func(), error) {
	if err := b.authorize(ctx, userID, id); err != nil {
		return nil, "", nil, err
	}
	live, err := b.api.terminals.Get(userID, id)
	if err != nil {
		return nil, "", nil, errors.New("请先连接 tmux 会话")
	}
	directory, err := live.BridgeDirectory(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	if directory == "" || directory == "/" || !path.IsAbs(directory) {
		return nil, "", nil, errors.New("无法确定项目目录，请在 tmux 终端进入项目后重试")
	}
	ssh := live.SSHClient()
	if ssh == nil {
		return nil, "", nil, errors.New("终端连接已断开")
	}
	type opened struct {
		client *sftp.Client
		err    error
	}
	ready := make(chan opened)
	go func() {
		client, err := sftp.NewClient(ssh)
		select {
		case ready <- opened{client, err}:
		case <-ctx.Done():
			if client != nil {
				_ = client.Close()
			}
		}
	}()
	var client *sftp.Client
	select {
	case <-ctx.Done():
		return nil, "", nil, ctx.Err()
	case value := <-ready:
		if value.err != nil {
			return nil, "", nil, errors.New("无法打开文件传输通道")
		}
		client = value.client
	}
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	cleanup := func() { stop(); _ = client.Close() }
	base, err := client.RealPath(directory)
	if err != nil || base == "/" || !path.IsAbs(base) {
		cleanup()
		return nil, "", nil, errors.New("无法访问项目目录")
	}
	return client, path.Clean(base), cleanup, nil
}
func safeTelegramFilename(value string) string {
	value = path.Base(strings.ReplaceAll(value, "\\", "/"))
	var out strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
		if out.Len() > 150 {
			break
		}
	}
	name := strings.Trim(out.String(), "._-")
	if name == "" {
		return "file"
	}
	return name
}
func (b telegramFiles) Upload(ctx context.Context, userID, id, name string, body io.Reader) (string, error) {
	client, base, cleanup, err := b.open(ctx, userID, id)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return uploadTelegramFile(ctx, client, base, name, body, func() error { return b.authorize(ctx, userID, id) })
}
func uploadTelegramFile(ctx context.Context, client *sftp.Client, base, name string, body io.Reader, authorize func() error) (string, error) {
	var err error
	directory := path.Join(base, "clipboard")
	if _, err = client.Lstat(directory); os.IsNotExist(err) {
		if err = client.Mkdir(directory); err != nil {
			return "", errors.New("无法创建项目 clipboard 目录")
		}
		_ = client.Chmod(directory, 0700)
	}
	info, err := client.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("clipboard 须为项目中的普通目录")
	}
	real, err := client.RealPath(directory)
	if err != nil || path.Clean(real) != directory {
		return "", errors.New("clipboard 目录指向项目之外")
	}
	prefix := time.Now().UTC().Format("20060102-150405") + "-" + uuid.NewString()[:8]
	final := path.Join(directory, prefix+"-"+safeTelegramFilename(name))
	partial := path.Join(directory, ".upload-"+uuid.NewString())
	file, err := client.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return "", errors.New("无法创建上传文件")
	}
	defer client.Remove(partial)
	_ = file.Chmod(0600)
	written, copyErr := io.Copy(file, io.LimitReader(body, telegram.MaxFileSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || ctx.Err() != nil {
		return "", errors.New("文件上传中断，请检查连接后重试")
	}
	if written > telegram.MaxFileSize {
		return "", errors.New("文件超过 20 MiB")
	}
	if err = authorize(); err != nil {
		return "", err
	}
	if err = client.Rename(partial, final); err != nil {
		return "", errors.New("无法保存上传文件")
	}
	return final, nil
}
func (b telegramFiles) Download(ctx context.Context, userID, id, relative string) (string, []byte, error) {
	if !validTelegramRelativePath(relative) {
		return "", nil, errors.New("请提供项目目录内的相对文件路径")
	}
	client, base, cleanup, err := b.open(ctx, userID, id)
	if err != nil {
		return "", nil, err
	}
	defer cleanup()
	return readTelegramFile(ctx, client, base, relative, func() error { return b.authorize(ctx, userID, id) })
}
func readTelegramFile(ctx context.Context, client *sftp.Client, base, relative string, authorize func() error) (string, []byte, error) {
	if !validTelegramRelativePath(relative) {
		return "", nil, errors.New("请提供项目目录内的相对文件路径")
	}
	// Some SFTP servers implement realpath as a lexical cleanup. Check every
	// component as well, so a parent symlink cannot escape the project.
	current := base
	for _, component := range strings.Split(path.Clean(relative), "/") {
		current = path.Join(current, component)
		info, err := client.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", nil, errors.New("文件路径不存在或包含符号链接")
		}
	}
	real, err := client.RealPath(path.Join(base, relative))
	if err != nil || !strings.HasPrefix(path.Clean(real), base+"/") {
		return "", nil, errors.New("文件不存在或不在当前项目目录中")
	}
	info, err := client.Lstat(real)
	if err != nil || !info.Mode().IsRegular() || info.Size() > telegram.MaxFileSize {
		return "", nil, errors.New("只能下载项目内不超过 20 MiB 的普通文件")
	}
	file, err := client.Open(real)
	if err != nil {
		return "", nil, errors.New("无法读取项目文件")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, telegram.MaxFileSize+1))
	if err != nil || ctx.Err() != nil {
		return "", nil, errors.New("文件读取中断")
	}
	if len(data) > telegram.MaxFileSize {
		return "", nil, errors.New("文件超过 20 MiB")
	}
	if err = authorize(); err != nil {
		return "", nil, err
	}
	return safeTelegramFilename(info.Name()), data, nil
}

func validTelegramRelativePath(relative string) bool {
	return !(relative == "" || len(relative) > 4096 || strings.ContainsAny(relative, "\\\x00\r\n") || path.IsAbs(relative) || path.Clean(relative) == ".." || strings.HasPrefix(path.Clean(relative), "../"))
}
