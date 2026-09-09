package api

import (
	"context"
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
	request := httptest.NewRequest(http.MethodPut, "/api/admin/ai-model", strings.NewReader(`{"baseURL":"https://models.example/v1","model":"agent-model","apiKey":"secret-key"}`))
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
	if value := manager.AIConfig(); value.APIKey != "secret-key" || value.Model != "agent-model" {
		t.Fatalf("runtime configuration was not updated: %#v", value)
	}
	restored := agent.NewManager(nil, agent.AIConfig{})
	(&API{store: database, vault: vault, agents: restored}).restoreAIModelConfig()
	if value := restored.AIConfig(); value.APIKey != "secret-key" || value.BaseURL != "https://models.example/v1" {
		t.Fatalf("stored configuration was not restored: %#v", value)
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
