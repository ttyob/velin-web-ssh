package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/agent"
)

const aiModelSettingKey = "ai_model_config"

type storedAIModelConfig struct {
	BaseURL         string `json:"baseURL"`
	Model           string `json:"model"`
	APIKeyEncrypted string `json:"apiKeyEncrypted"`
}

type aiModelConfigResponse struct {
	BaseURL          string `json:"baseURL"`
	Model            string `json:"model"`
	APIKeyConfigured bool   `json:"apiKeyConfigured"`
	Configured       bool   `json:"configured"`
}

func (a *API) restoreAIModelConfig() {
	var stored storedAIModelConfig
	if err := a.store.SystemSetting(aiModelSettingKey, &stored); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("load AI model configuration", "error", err)
		}
		return
	}
	apiKey := ""
	if stored.APIKeyEncrypted != "" {
		var err error
		apiKey, err = a.vault.Decrypt(stored.APIKeyEncrypted)
		if err != nil {
			slog.Warn("decrypt AI model API key", "error", err)
			return
		}
	}
	a.agents.UpdateAI(agent.AIConfig{BaseURL: stored.BaseURL, Model: stored.Model, APIKey: apiKey})
}

func (a *API) getAIModelConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, aiModelResponse(a.agents.AIConfig()))
}

func (a *API) saveAIModelConfig(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BaseURL     string `json:"baseURL"`
		Model       string `json:"model"`
		APIKey      string `json:"apiKey"`
		ClearAPIKey bool   `json:"clearAPIKey"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	input.Model = strings.TrimSpace(input.Model)
	input.APIKey = strings.TrimSpace(input.APIKey)
	if err := validateAIModelConfig(input.BaseURL, input.Model, input.APIKey); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_ai_model_config", err.Error())
		return
	}
	apiKey := a.agents.AIConfig().APIKey
	if input.ClearAPIKey {
		apiKey = ""
	}
	if input.APIKey != "" {
		apiKey = input.APIKey
	}
	encrypted := ""
	var err error
	if apiKey != "" {
		encrypted, err = a.vault.Encrypt(apiKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption_error", "API Key 加密失败")
			return
		}
	}
	stored := storedAIModelConfig{BaseURL: input.BaseURL, Model: input.Model, APIKeyEncrypted: encrypted}
	if err = a.store.SaveSystemSetting(aiModelSettingKey, stored); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "AI 模型配置保存失败")
		return
	}
	value := agent.AIConfig{BaseURL: input.BaseURL, Model: input.Model, APIKey: apiKey}
	a.agents.UpdateAI(value)
	writeJSON(w, http.StatusOK, aiModelResponse(value))
}

func (a *API) testAIModelConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithAgentTimeout(r, 30*time.Second)
	defer cancel()
	result, err := a.agents.Chat(ctx, []agent.ChatMessage{{Role: "user", Content: "This is a connection test. Reply with OK only and do not call tools."}}, "configuration test")
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"model": result.Model, "message": result.Message})
}

func aiModelResponse(value agent.AIConfig) aiModelConfigResponse {
	return aiModelConfigResponse{BaseURL: value.BaseURL, Model: value.Model, APIKeyConfigured: value.APIKey != "", Configured: value.Configured()}
}

func validateAIModelConfig(baseURL, model, apiKey string) error {
	if len(baseURL) > 2048 || len(model) > 256 || len(apiKey) > 16384 {
		return errors.New("AI 模型配置内容过长")
	}
	if baseURL == "" && model == "" {
		return nil
	}
	if baseURL == "" || model == "" {
		return errors.New("API 地址和模型名称必须同时填写")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return errors.New("请输入有效的 HTTP 或 HTTPS 模型 API 地址")
	}
	return nil
}
