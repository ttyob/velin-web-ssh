package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttyob/velin-web-ssh/internal/agent"
	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

func TestSaveAIModelConfigEncryptsAPIKey(t *testing.T) {
	dir := t.TempDir()
	database, err := store.Open(filepath.Join(dir, "velin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	vault, err := security.LoadVault(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	manager := agent.NewManager(nil, agent.AIConfig{})
	api := &API{store: database, vault: vault, agents: manager}
	request := httptest.NewRequest(http.MethodPut, "/api/admin/ai-model", strings.NewReader(`{"baseURL":"https://models.example/v1","model":"agent-model","apiKey":"secret-key","proxyURL":" socks5://proxy-user:proxy-password@127.0.0.1:1080 "}`))
	request = request.WithContext(context.WithValue(request.Context(), userKey, store.User{ID: "admin", Role: "admin"}))
	recorder := httptest.NewRecorder()
	api.saveAIModelConfig(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	var stored storedAIModelConfig
	if err = database.SystemSetting(aiModelSettingKey, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.APIKeyEncrypted == "" || strings.Contains(stored.APIKeyEncrypted, "secret-key") {
		t.Fatalf("API key was not encrypted: %#v", stored)
	}
	if stored.ProxyURLEncrypted == "" || strings.Contains(stored.ProxyURLEncrypted, "proxy-password") {
		t.Fatalf("proxy credentials were not encrypted: %#v", stored)
	}
	proxyURL := "socks5://proxy-user:proxy-password@127.0.0.1:1080"
	if value := manager.AIConfig(); value.APIKey != "secret-key" || value.Model != "agent-model" || value.ProxyURL != proxyURL {
		t.Fatalf("runtime configuration was not updated: %#v", value)
	}
	restored := agent.NewManager(nil, agent.AIConfig{})
	(&API{store: database, vault: vault, agents: restored}).restoreAIModelConfig()
	if value := restored.AIConfig(); value.APIKey != "secret-key" || value.BaseURL != "https://models.example/v1" || value.ProxyURL != proxyURL {
		t.Fatalf("stored configuration was not restored: %#v", value)
	}
	for _, test := range []struct {
		body   string
		status int
		proxy  string
	}{
		{`{"baseURL":"https://models.example/v1","model":"agent-model"}`, http.StatusOK, proxyURL},
		{`{"baseURL":"https://models.example/v1","model":"agent-model","proxyURL":"ftp://proxy"}`, http.StatusBadRequest, proxyURL},
		{`{"baseURL":"https://models.example/v1","model":"agent-model","proxyURL":""}`, http.StatusOK, ""},
	} {
		recorder := httptest.NewRecorder()
		api.saveAIModelConfig(recorder, httptest.NewRequest(http.MethodPut, "/api/admin/ai-model", strings.NewReader(test.body)))
		if recorder.Code != test.status || manager.AIConfig().ProxyURL != test.proxy || manager.AIConfig().APIKey != "secret-key" {
			t.Fatalf("proxy update: status=%d config=%+v", recorder.Code, manager.AIConfig())
		}
		if test.status == http.StatusOK {
			var response aiModelConfigResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.ProxyURL != test.proxy {
				t.Fatalf("proxy response: %+v err=%v", response, err)
			}
		}
	}
	stored = storedAIModelConfig{}
	if err := database.SystemSetting(aiModelSettingKey, &stored); err != nil || stored.ProxyURLEncrypted != "" {
		t.Fatalf("cleared proxy was still stored: %+v err=%v", stored, err)
	}
}

func TestValidateAIModelConfig(t *testing.T) {
	if err := validateAIModelConfig("https://models.example/v1", "model", ""); err != nil {
		t.Fatal(err)
	}
	for _, test := range [][2]string{{"models.example/v1", "model"}, {"https://user@models.example/v1", "model"}, {"https://models.example/v1", ""}} {
		if err := validateAIModelConfig(test[0], test[1], ""); err == nil {
			t.Fatalf("expected invalid configuration for %#v", test)
		}
	}
}
