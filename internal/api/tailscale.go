package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/ttyob/velin-web-ssh/internal/tailnet"
)

type tailscaleConfigResponse struct {
	Enabled           bool           `json:"enabled"`
	Hostname          string         `json:"hostname"`
	ControlURL        string         `json:"controlURL"`
	AuthKeyConfigured bool           `json:"authKeyConfigured"`
	Status            tailnet.Status `json:"status"`
}

func (a *API) tailscaleStatus(w http.ResponseWriter, r *http.Request) {
	settings, err := tailnet.LoadSettings(a.store, a.vault)
	if err != nil {
		slog.Warn("load Tailscale settings", "error", err)
		writeError(w, http.StatusInternalServerError, "tailscale_settings_failed", "无法读取 Tailscale 设置")
		return
	}
	status := tailnet.Status{State: "disabled"}
	if a.tailscale != nil {
		status, err = a.tailscale.Status(r.Context())
		if err != nil {
			slog.Warn("read embedded Tailscale status", "error", err)
			status = tailnet.Status{State: "error"}
		}
	}
	response := tailscaleConfigResponse{Enabled: settings.Enabled, Hostname: settings.Hostname, ControlURL: settings.ControlURL, AuthKeyConfigured: settings.AuthKey != "", Status: status}
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) saveTailscale(w http.ResponseWriter, r *http.Request) {
	var input tailnet.Update
	if !decode(w, r, &input) {
		return
	}
	input.Hostname = strings.TrimSpace(input.Hostname)
	input.ControlURL = strings.TrimRight(strings.TrimSpace(input.ControlURL), "/")
	input.AuthKey = strings.TrimSpace(input.AuthKey)
	if len(input.Hostname) > 63 || strings.ContainsAny(input.Hostname, " \t\r\n/\\") {
		writeError(w, http.StatusBadRequest, "invalid_tailscale_hostname", "Tailscale 节点名称无效")
		return
	}
	if input.ControlURL != "" {
		parsed, err := url.Parse(input.ControlURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
			writeError(w, http.StatusBadRequest, "invalid_tailscale_control_url", "控制面地址必须是有效的 HTTP 或 HTTPS 地址")
			return
		}
	}
	if len(input.AuthKey) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_tailscale_auth_key", "Auth Key 过长")
		return
	}
	settings, err := tailnet.LoadSettings(a.store, a.vault)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tailscale_settings_failed", "无法读取 Tailscale 设置")
		return
	}
	settings.Enabled = input.Enabled
	settings.Hostname = input.Hostname
	settings.ControlURL = input.ControlURL
	if input.ClearAuthKey {
		settings.AuthKey = ""
	}
	if input.AuthKey != "" {
		settings.AuthKey = input.AuthKey
	}
	if err = tailnet.SaveSettings(a.store, a.vault, settings); err != nil {
		writeError(w, http.StatusInternalServerError, "tailscale_settings_failed", "无法保存 Tailscale 设置")
		return
	}
	if a.tailscale != nil {
		err = a.tailscale.Apply(settings)
	}
	if err != nil {
		slog.Warn("apply Tailscale settings", "error", err)
		writeError(w, http.StatusServiceUnavailable, "tailscale_start_failed", err.Error())
		return
	}
	status := tailnet.Status{State: "disabled"}
	var statusErr error
	if a.tailscale != nil {
		status, statusErr = a.tailscale.Status(r.Context())
	}
	if statusErr != nil {
		slog.Warn("read Tailscale status after update", "error", statusErr)
	}
	writeJSON(w, http.StatusOK, tailscaleConfigResponse{Enabled: settings.Enabled, Hostname: settings.Hostname, ControlURL: settings.ControlURL, AuthKeyConfigured: settings.AuthKey != "", Status: status})
}
