package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type AIConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

func (c AIConfig) Configured() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.Model) != ""
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CommandProposal struct {
	ID               string `json:"id"`
	Command          string `json:"command"`
	Reason           string `json:"reason"`
	RequiresApproval bool   `json:"requiresApproval"`
}

type ChatResponse struct {
	Message          string            `json:"message"`
	Commands         []CommandProposal `json:"commands"`
	Model            string            `json:"model"`
	Backend          string            `json:"backend"`
	PromptTokens     int               `json:"promptTokens,omitempty"`
	CompletionTokens int               `json:"completionTokens,omitempty"`
	TotalTokens      int               `json:"totalTokens,omitempty"`
}

type ChatOptions struct {
	Model           string
	ReasoningEffort string
	Backend         string
	WorkspaceKey    string
	ConversationID  string
}

type BackendInfo struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type ModelInfo struct {
	ID              string `json:"id"`
	OwnedBy         string `json:"ownedBy,omitempty"`
	ContextWindow   int    `json:"contextWindow,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
}

func (m *Manager) Backends() []BackendInfo {
	return []BackendInfo{{ID: "native", Label: "Velin", Available: true}}
}

func (m *Manager) Chat(ctx context.Context, history []ChatMessage, hostContext string, options ...ChatOptions) (ChatResponse, error) {
	selected := ChatOptions{}
	if len(options) > 0 {
		selected = options[0]
	}
	switch strings.TrimSpace(selected.Backend) {
	case "", "native":
	default:
		return ChatResponse{}, errors.New("invalid agent backend")
	}
	config := m.AIConfig()
	if !config.Configured() {
		return ChatResponse{}, errors.New("AI model service is not configured")
	}
	if len(history) == 0 || len(history) > 40 {
		return ChatResponse{}, errors.New("invalid agent conversation")
	}
	model := strings.TrimSpace(selected.Model)
	if model == "" {
		model = config.Model
	}
	if len(model) > 256 || strings.ContainsRune(model, '\x00') {
		return ChatResponse{}, errors.New("invalid agent model")
	}
	reasoningEffort := strings.TrimSpace(selected.ReasoningEffort)
	if reasoningEffort != "" && reasoningEffort != "low" && reasoningEffort != "medium" && reasoningEffort != "high" {
		return ChatResponse{}, errors.New("invalid reasoning effort")
	}
	messages := []map[string]string{{
		"role":    "system",
		"content": "You are Velin SSH Agent. Help the user operate a remote host. Reply in the user's language. Use run_ssh_command when host inspection or an operation is needed. The application automatically executes ordinary non-sensitive read-only commands and displays an approval dialog for writes, sensitive reads, dangerous commands, and commands it cannot classify. Call run_ssh_command directly without first asking the user to approve or narrating that approval is needed. Never claim an unexecuted command has run. Prefer small, auditable commands and avoid destructive operations unless the user explicitly requested them. Connected host: " + hostContext,
	}}
	for _, item := range history {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if (role != "user" && role != "assistant") || content == "" || len(content) > 32000 {
			return ChatResponse{}, errors.New("invalid agent message")
		}
		messages = append(messages, map[string]string{"role": role, "content": content})
	}
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "run_ssh_command",
				"description": "Submit a shell command for the connected SSH host. The application automatically executes safe inspection commands and requests approval for sensitive or state-changing commands.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]string{"type": "string", "description": "The exact shell command"},
						"reason":  map[string]string{"type": "string", "description": "A short reason shown to the user"},
					},
					"required":             []string{"command", "reason"},
					"additionalProperties": false,
				},
			},
		}},
		"tool_choice": "auto",
	}
	if reasoningEffort != "" {
		body["reasoning_effort"] = reasoningEffort
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
	client := &http.Client{Timeout: 90 * time.Second}
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
		message := strings.TrimSpace(string(responseRaw))
		if len(message) > 4096 {
			message = message[:4096]
		}
		return ChatResponse{}, fmt.Errorf("AI model returned HTTP %d: %s", response.StatusCode, message)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err = json.Unmarshal(responseRaw, &decoded); err != nil || len(decoded.Choices) == 0 {
		return ChatResponse{}, errors.New("AI model returned an invalid response")
	}
	result := ChatResponse{
		Message:          strings.TrimSpace(decoded.Choices[0].Message.Content),
		Commands:         make([]CommandProposal, 0),
		Model:            model,
		Backend:          "native",
		PromptTokens:     decoded.Usage.PromptTokens,
		CompletionTokens: decoded.Usage.CompletionTokens,
		TotalTokens:      decoded.Usage.TotalTokens,
	}
	for _, call := range decoded.Choices[0].Message.ToolCalls {
		if call.Function.Name != "run_ssh_command" {
			continue
		}
		var arguments struct {
			Command string `json:"command"`
			Reason  string `json:"reason"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil {
			continue
		}
		arguments.Command = strings.TrimSpace(arguments.Command)
		if arguments.Command == "" || len(arguments.Command) > 6000 {
			continue
		}
		result.Commands = append(result.Commands, CommandProposal{
			ID: call.ID, Command: arguments.Command, Reason: strings.TrimSpace(arguments.Reason),
			RequiresApproval: commandRequiresApproval(arguments.Command),
		})
	}
	if len(result.Commands) > 0 {
		// The command card provides its reason and approval controls. Suppress the
		// model's duplicate preamble so users see only one approval request.
		result.Message = ""
	}
	if result.Message == "" && len(result.Commands) == 0 {
		return ChatResponse{}, errors.New("AI model returned an empty response")
	}
	return result, nil
}

func (m *Manager) Models(ctx context.Context) ([]ModelInfo, error) {
	config := m.AIConfig()
	if !config.Configured() {
		return nil, errors.New("AI model service is not configured")
	}
	endpoint := strings.TrimRight(config.BaseURL, "/")
	if strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint = strings.TrimSuffix(endpoint, "/chat/completions")
	}
	if !strings.HasSuffix(endpoint, "/models") {
		endpoint += "/models"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("AI model list request: %w", err)
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseRaw))
		if len(message) > 4096 {
			message = message[:4096]
		}
		return nil, fmt.Errorf("AI model list returned HTTP %d: %s", response.StatusCode, message)
	}
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err = json.Unmarshal(responseRaw, &envelope); err != nil {
		return nil, errors.New("AI model list returned an invalid response")
	}
	models := make([]ModelInfo, 0, len(envelope.Data))
	seen := make(map[string]struct{}, len(envelope.Data))
	for _, item := range envelope.Data {
		id, _ := item["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 256 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ownedBy, _ := item["owned_by"].(string)
		models = append(models, ModelInfo{
			ID:              id,
			OwnedBy:         strings.TrimSpace(ownedBy),
			ContextWindow:   modelNumber(item, "context_window", "context_length", "max_context_length", "max_model_len", "max_input_tokens", "input_token_limit"),
			MaxOutputTokens: modelNumber(item, "max_output_tokens", "output_token_limit", "max_tokens"),
		})
	}
	return models, nil
}

func modelNumber(item map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := item[key]
		if !ok {
			continue
		}
		switch number := value.(type) {
		case float64:
			if number > 0 && number <= float64(^uint(0)>>1) {
				return int(number)
			}
		case json.Number:
			parsed, err := number.Int64()
			if err == nil && parsed > 0 && parsed <= int64(^uint(0)>>1) {
				return int(parsed)
			}
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(number))
			if err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}
