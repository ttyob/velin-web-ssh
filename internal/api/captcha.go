package api

import (
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/security"
)

const (
	loginCaptchaTTL = 5 * time.Minute
	loginCaptchaMax = 256
)

type loginCaptcha struct {
	Identity  string
	IP        string
	Code      string
	ExpiresAt time.Time
}

func (a *API) loginCaptcha(w http.ResponseWriter, r *http.Request) {
	identity := strings.TrimSpace(r.URL.Query().Get("username"))
	if identity == "" || len([]rune(identity)) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_username", "请输入有效用户名")
		return
	}
	challengeID, err := security.RandomToken(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "captcha_failed", "验证码生成失败")
		return
	}
	code, err := randomCaptchaCode(5)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "captcha_failed", "验证码生成失败")
		return
	}
	a.captchaMu.Lock()
	if a.captchas == nil {
		a.captchas = make(map[string]loginCaptcha)
	}
	now := time.Now()
	for id, item := range a.captchas {
		if item.ExpiresAt.Before(now) {
			delete(a.captchas, id)
		}
	}
	if len(a.captchas) >= loginCaptchaMax {
		writeError(w, http.StatusTooManyRequests, "captcha_busy", "验证码请求过于频繁，请稍后重试")
		a.captchaMu.Unlock()
		return
	}
	a.captchas[challengeID] = loginCaptcha{Identity: strings.ToLower(identity), IP: a.clientIP(r), Code: code, ExpiresAt: now.Add(loginCaptchaTTL)}
	a.captchaMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"id": challengeID, "image": captchaSVG(code)})
}

func (a *API) validateLoginCaptcha(identity, ip, challengeID, answer string) (bool, string) {
	if challengeID == "" || answer == "" {
		return false, "captcha_required"
	}
	a.captchaMu.Lock()
	defer a.captchaMu.Unlock()
	item, ok := a.captchas[challengeID]
	if !ok || item.ExpiresAt.Before(time.Now()) {
		if ok {
			delete(a.captchas, challengeID)
		}
		return false, "captcha_invalid"
	}
	if item.Identity != strings.ToLower(strings.TrimSpace(identity)) || item.IP != ip || !strings.EqualFold(item.Code, strings.TrimSpace(answer)) {
		delete(a.captchas, challengeID)
		return false, "captcha_invalid"
	}
	return true, ""
}

func (a *API) deleteLoginCaptcha(challengeID string) {
	if challengeID == "" {
		return
	}
	a.captchaMu.Lock()
	delete(a.captchas, challengeID)
	a.captchaMu.Unlock()
}

func randomCaptchaCode(length int) (string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	code := make([]byte, length)
	for i := range code {
		value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		code[i] = alphabet[value.Int64()]
	}
	return string(code), nil
}

func captchaSVG(code string) string {
	var lines strings.Builder
	for index := 0; index < 6; index++ {
		x := 8 + index*27
		y := 8 + (index*17)%32
		x2 := 145 - index*11
		y2 := 12 + (index*23)%32
		fmt.Fprintf(&lines, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#9aa8c7" stroke-width="1" opacity=".55"/>`, x, y, x2, y2)
	}
	var text strings.Builder
	for index, char := range code {
		x := 15 + index*27
		rotation := -9 + index*5
		fmt.Fprintf(&text, `<text x="%d" y="33" transform="rotate(%d %d 28)">%c</text>`, x, rotation, x, char)
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="160" height="48" viewBox="0 0 160 48" role="img" aria-label="登录验证码"><rect width="160" height="48" rx="5" fill="#eef2f8"/>%s<g fill="#1f3156" font-family="Arial,sans-serif" font-size="23" font-weight="700" letter-spacing="1">%s</g></svg>`, lines.String(), text.String())
}
