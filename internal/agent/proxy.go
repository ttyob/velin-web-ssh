package agent

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ParseAIProxyURL validates the optional proxy used only by model requests.
func ParseAIProxyURL(value string) (*url.URL, error) {
	if value == "" {
		return nil, nil
	}
	invalid := errors.New("请输入有效的 HTTP、HTTPS 或 SOCKS5 代理地址")
	if len(value) > 4096 {
		return nil, invalid
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" {
		return nil, invalid
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, invalid
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, invalid
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, invalid
		}
	}
	return parsed, nil
}

func newAITransport(proxyURL string) (*http.Transport, error) {
	proxy, err := ParseAIProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Never change DefaultTransport or process proxy variables: SSH, RDP,
	// downloads and other HTTP clients keep their own networking behavior.
	transport.Proxy = nil
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	return transport, nil
}

func (m *Manager) aiHTTPClient(timeout time.Duration) (AIConfig, *http.Client, error) {
	m.aiMu.RLock()
	defer m.aiMu.RUnlock()
	if m.aiTransportErr != nil {
		return AIConfig{}, nil, m.aiTransportErr
	}
	return m.ai, &http.Client{Transport: m.aiTransport, Timeout: timeout}, nil
}
