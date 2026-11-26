package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const MaxFileSize = 20 << 20

type Bot struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsBot    bool   `json:"is_bot"`
}
type Person struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type File struct {
	ID   string `json:"file_id"`
	Name string `json:"file_name"`
	Size int64  `json:"file_size"`
	Path string `json:"file_path"`
}
type Message struct {
	ID       int64    `json:"message_id"`
	Date     int64    `json:"date"`
	From     Person   `json:"from"`
	Chat     Chat     `json:"chat"`
	Text     string   `json:"text"`
	Caption  string   `json:"caption"`
	Reply    *Message `json:"reply_to_message"`
	Document *File    `json:"document"`
	Photo    []File   `json:"photo"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    Person   `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}
type Keyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}
type apiError struct {
	Code, Retry int
	Unchanged   bool
}

func (e *apiError) Error() string {
	switch e.Code {
	case 401:
		return "Bot Token 无效，请重新配置"
	case 403:
		return "机器人无法发送消息，请在 Telegram 中启动或解除屏蔽机器人"
	case 409:
		return "同一机器人正在被其他程序接收，请只保留一个运行实例并检查 webhook"
	case 429:
		return "Telegram 请求过于频繁，稍后重试"
	default:
		return fmt.Sprintf("Telegram 请求失败（%d）", e.Code)
	}
}

type Client struct {
	token, base string
	http        *http.Client
}

func newClient(token, proxy, base string) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
			return nil, errors.New("请输入有效的 HTTP、HTTPS 或 SOCKS5 代理地址")
		}
		switch parsed.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, errors.New("代理协议须为 HTTP、HTTPS 或 SOCKS5")
		}
		if port := parsed.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return nil, errors.New("代理端口无效")
			}
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	if base == "" {
		base = "https://api.telegram.org"
	}
	return &Client{token: token, base: strings.TrimRight(base, "/"), http: &http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Telegram 不接受重定向") }}}, nil
}
func (c *Client) close() { c.http.CloseIdleConnections() }
func (c *Client) request(ctx context.Context, method, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, body)
	if err != nil {
		return errors.New("无法创建 Telegram 请求")
	}
	req.Header.Set("Content-Type", contentType)
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("无法连接 Telegram，请检查网络或 Telegram 代理")
	}
	defer res.Body.Close()
	var packet struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&packet) != nil {
		return errors.New("Telegram 返回了无效响应")
	}
	if !packet.OK {
		return &apiError{Code: packet.Code, Retry: packet.Parameters.Retry, Unchanged: strings.Contains(packet.Description, "message is not modified")}
	}
	if out != nil {
		if json.Unmarshal(packet.Result, out) != nil {
			return errors.New("无法解析 Telegram 响应")
		}
	}
	return nil
}
func (c *Client) call(ctx context.Context, method string, value, out any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return errors.New("Telegram 请求内容无效")
	}
	return c.request(ctx, method, "application/json", bytes.NewReader(data), out)
}
func (c *Client) validate(ctx context.Context) (Bot, error) {
	var bot Bot
	if err := c.call(ctx, "getMe", struct{}{}, &bot); err != nil {
		return bot, err
	}
	if !bot.IsBot || bot.ID == 0 || bot.Username == "" {
		return bot, errors.New("Telegram 返回的机器人信息无效")
	}
	var webhook struct {
		URL string `json:"url"`
	}
	if err := c.call(ctx, "getWebhookInfo", struct{}{}, &webhook); err != nil {
		return bot, err
	}
	if webhook.URL != "" {
		return bot, errors.New("此机器人已配置 webhook，请使用独立 Bot 或先移除原 webhook")
	}
	return bot, nil
}
func (c *Client) updates(ctx context.Context, offset int64) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "limit": 20, "allowed_updates": []string{"message", "callback_query"}}, &updates)
	return updates, err
}
func (c *Client) send(ctx context.Context, chat int64, text string, markup any) (Message, error) {
	var message Message
	err := c.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "reply_markup": markup, "link_preview_options": map[string]bool{"is_disabled": true}}, &message)
	return message, err
}
func (c *Client) edit(ctx context.Context, chat, id int64, text string, markup any) error {
	err := c.call(ctx, "editMessageText", map[string]any{"chat_id": chat, "message_id": id, "text": text, "parse_mode": "HTML", "reply_markup": markup, "link_preview_options": map[string]bool{"is_disabled": true}}, nil)
	var api *apiError
	if errors.As(err, &api) && api.Unchanged {
		return nil
	}
	return err
}
func (c *Client) answer(ctx context.Context, id, text string) {
	_ = c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}
func (c *Client) document(ctx context.Context, chat int64, name string, data []byte) (Message, error) {
	var message Message
	if len(data) > MaxFileSize {
		return message, errors.New("文件超过 20 MiB")
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", strconv.FormatInt(chat, 10))
	part, err := w.CreateFormFile("document", name)
	if err != nil {
		return message, errors.New("文件名无效")
	}
	_, _ = part.Write(data)
	_ = w.Close()
	err = c.request(ctx, "sendDocument", w.FormDataContentType(), &body, &message)
	return message, err
}
func (c *Client) download(ctx context.Context, file File) (io.ReadCloser, error) {
	if file.Size > MaxFileSize {
		return nil, errors.New("文件超过 20 MiB")
	}
	var remote File
	if err := c.call(ctx, "getFile", map[string]string{"file_id": file.ID}, &remote); err != nil {
		return nil, err
	}
	if remote.Size > MaxFileSize {
		return nil, errors.New("文件超过 20 MiB")
	}
	if remote.Path == "" || strings.ContainsAny(remote.Path, "\\?#") || path.Clean(remote.Path) != remote.Path || strings.HasPrefix(remote.Path, "/") || strings.HasPrefix(remote.Path, "../") {
		return nil, errors.New("Telegram 文件路径无效")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/file/bot"+c.token+"/"+remote.Path, nil)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("Telegram 文件下载失败，请检查网络")
	}
	if res.StatusCode != 200 || res.ContentLength > MaxFileSize {
		res.Body.Close()
		return nil, errors.New("Telegram 文件无法下载或超过 20 MiB")
	}
	return res.Body, nil
}
