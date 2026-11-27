package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Complete sends a focused model request without exposing remote command tools.
func (m *Manager) Complete(ctx context.Context, systemPrompt, userPrompt string) (ChatResponse, error) {
	systemPrompt = strings.TrimSpace(systemPrompt)
	userPrompt = strings.TrimSpace(userPrompt)
	if systemPrompt == "" || userPrompt == "" || len(systemPrompt) > 16000 || len(userPrompt) > 128*1024 {
		return ChatResponse{}, errors.New("invalid AI completion prompt")
	}
	config, client, err := m.aiHTTPClient(90 * time.Second)
	if err != nil {
		return ChatResponse{}, err
	}
	if !config.Configured() {
		return ChatResponse{}, errors.New("AI model service is not configured")
	}
	body := map[string]any{
		"model": config.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ChatResponse{}, err
	}
	endpoint := strings.TrimRight(config.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return ChatResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	response, err := client.Do(request)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("AI model request: %w", err)
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return ChatResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ChatResponse{}, modelHTTPError(response.StatusCode, responseRaw)
	}
	return decodeChatResponse(responseRaw, config.Model)
}
