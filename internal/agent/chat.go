package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
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

type modelToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (m *Manager) Backends() []BackendInfo {
	return []BackendInfo{{ID: "native", Label: "VelinWebSSH", Available: true}}
}

func (m *Manager) Chat(ctx context.Context, history []ChatMessage, hostContext string, options ...ChatOptions) (ChatResponse, error) {
	request, model, err := m.chatRequest(ctx, history, hostContext, false, options...)
	if err != nil {
		return ChatResponse{}, err
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
		return ChatResponse{}, modelHTTPError(response.StatusCode, responseRaw)
	}
	return decodeChatResponse(responseRaw, model)
}

func (m *Manager) ChatStream(ctx context.Context, history []ChatMessage, hostContext string, onDelta func(string) error, options ...ChatOptions) (ChatResponse, error) {
	request, model, err := m.chatRequest(ctx, history, hostContext, true, options...)
	if err != nil {
		return ChatResponse{}, err
	}
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("AI model request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseRaw, readErr := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
		if readErr != nil {
			return ChatResponse{}, readErr
		}
		return ChatResponse{}, modelHTTPError(response.StatusCode, responseRaw)
	}
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		responseRaw, readErr := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
		if readErr != nil {
			return ChatResponse{}, readErr
		}
		result, decodeErr := decodeChatResponse(responseRaw, model)
		if decodeErr == nil && result.Message != "" && onDelta != nil {
			decodeErr = onDelta(result.Message)
		}
		return result, decodeErr
	}
	return decodeChatStream(response.Body, model, onDelta)
}

func (m *Manager) chatRequest(ctx context.Context, history []ChatMessage, hostContext string, stream bool, options ...ChatOptions) (*http.Request, string, error) {
	selected := ChatOptions{}
	if len(options) > 0 {
		selected = options[0]
	}
	switch strings.TrimSpace(selected.Backend) {
	case "", "native":
	default:
		return nil, "", errors.New("invalid agent backend")
	}
	config := m.AIConfig()
	if !config.Configured() {
		return nil, "", errors.New("AI model service is not configured")
	}
	if len(history) == 0 || len(history) > 40 {
		return nil, "", errors.New("invalid agent conversation")
	}
	model := strings.TrimSpace(selected.Model)
	if model == "" {
		model = config.Model
	}
	if len(model) > 256 || strings.ContainsRune(model, '\x00') {
		return nil, "", errors.New("invalid agent model")
	}
	reasoningEffort := strings.TrimSpace(selected.ReasoningEffort)
	if reasoningEffort != "" && reasoningEffort != "low" && reasoningEffort != "medium" && reasoningEffort != "high" {
		return nil, "", errors.New("invalid reasoning effort")
	}
	messages := []map[string]string{{
		"role":    "system",
		"content": "You are VelinWebSSH Agent. Help the user operate a remote host. Reply in the user's language. Use run_ssh_command when host inspection or an operation is needed. The application automatically executes ordinary non-sensitive read-only commands and displays an approval dialog for writes, sensitive reads, dangerous commands, and commands it cannot classify. Call run_ssh_command directly without first asking the user to approve or narrating that approval is needed. Never claim an unexecuted command has run. Prefer small, auditable commands and avoid destructive operations unless the user explicitly requested them. Connected host: " + hostContext,
	}}
	for _, item := range history {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if (role != "user" && role != "assistant") || content == "" || len(content) > 32000 {
			return nil, "", errors.New("invalid agent message")
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
	if stream {
		body["stream"] = true
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	endpoint := strings.TrimRight(config.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	return request, model, nil
}

func decodeChatResponse(responseRaw []byte, model string) (ChatResponse, error) {
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
		Usage chatUsage `json:"usage"`
	}
	if err := json.Unmarshal(responseRaw, &decoded); err != nil || len(decoded.Choices) == 0 {
		return ChatResponse{}, errors.New("AI model returned an invalid response")
	}
	calls := make([]modelToolCall, 0, len(decoded.Choices[0].Message.ToolCalls))
	for _, call := range decoded.Choices[0].Message.ToolCalls {
		calls = append(calls, modelToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	return finishChatResponse(decoded.Choices[0].Message.Content, calls, model, decoded.Usage)
}

func decodeChatStream(body io.Reader, model string, onDelta func(string) error) (ChatResponse, error) {
	type streamToolCall struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type streamMessage struct {
		Content   string           `json:"content"`
		ToolCalls []streamToolCall `json:"tool_calls"`
	}
	var content strings.Builder
	toolCalls := make(map[int]*modelToolCall)
	usage := chatUsage{}
	process := func(data string) (bool, error) {
		if data == "[DONE]" {
			return true, nil
		}
		var chunk struct {
			Choices []struct {
				Delta   streamMessage `json:"delta"`
				Message streamMessage `json:"message"`
			} `json:"choices"`
			Usage chatUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return false, errors.New("AI model returned an invalid stream event")
		}
		if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 || chunk.Usage.TotalTokens != 0 {
			usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			delta := choice.Delta
			if delta.Content == "" && len(delta.ToolCalls) == 0 {
				delta = choice.Message
			}
			if delta.Content != "" {
				content.WriteString(delta.Content)
				if onDelta != nil {
					if err := onDelta(delta.Content); err != nil {
						return false, err
					}
				}
			}
			for _, fragment := range delta.ToolCalls {
				call := toolCalls[fragment.Index]
				if call == nil {
					call = &modelToolCall{}
					toolCalls[fragment.Index] = call
				}
				call.ID += fragment.ID
				call.Name += fragment.Function.Name
				call.Arguments += fragment.Function.Arguments
			}
		}
		return false, nil
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	dataLines := make([]string, 0, 1)
	totalBytes := 0
	done := false
	flushEvent := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		var err error
		done, err = process(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		totalBytes += len(line)
		if totalBytes > 8*1024*1024 {
			return ChatResponse{}, errors.New("AI model stream is too large")
		}
		if line == "" {
			if err := flushEvent(); err != nil {
				return ChatResponse{}, err
			}
			if done {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return ChatResponse{}, err
	}
	if !done {
		if err := flushEvent(); err != nil {
			return ChatResponse{}, err
		}
	}
	indexes := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	calls := make([]modelToolCall, 0, len(indexes))
	for _, index := range indexes {
		calls = append(calls, *toolCalls[index])
	}
	return finishChatResponse(content.String(), calls, model, usage)
}

func finishChatResponse(message string, calls []modelToolCall, model string, usage chatUsage) (ChatResponse, error) {
	result := ChatResponse{
		Message: strings.TrimSpace(message), Commands: make([]CommandProposal, 0), Model: model, Backend: "native",
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens,
	}
	for _, call := range calls {
		if call.Name != "run_ssh_command" {
			continue
		}
		var arguments struct {
			Command string `json:"command"`
			Reason  string `json:"reason"`
		}
		if json.Unmarshal([]byte(call.Arguments), &arguments) != nil {
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

func modelHTTPError(status int, responseRaw []byte) error {
	message := strings.TrimSpace(string(responseRaw))
	if len(message) > 4096 {
		message = message[:4096]
	}
	return fmt.Errorf("AI model returned HTTP %d: %s", status, message)
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
