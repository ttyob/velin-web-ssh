package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatReturnsCommandProposal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected model request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var request struct {
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
			Messages        []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || request.ReasoningEffort != "high" {
			t.Fatalf("unexpected chat options: %#v", request)
		}
		if len(request.Messages) == 0 || !strings.Contains(request.Messages[0].Content, "Call run_ssh_command directly without first asking the user to approve") {
			t.Fatalf("system prompt does not suppress duplicate approval narration: %#v", request.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"我需要先查看磁盘。","tool_calls":[{"id":"call-1","type":"function","function":{"name":"run_ssh_command","arguments":"{\"command\":\"df -h\",\"reason\":\"查看磁盘空间\"}"}}]}}],"usage":{"prompt_tokens":123,"completion_tokens":45,"total_tokens":168}}`)
	}))
	defer server.Close()
	manager := NewManager(nil, AIConfig{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"})
	result, err := manager.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "检查磁盘"}}, "server-1 linux amd64", ChatOptions{ReasoningEffort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "test-model" || result.TotalTokens != 168 || result.Message != "" || len(result.Commands) != 1 || result.Commands[0].Command != "df -h" || result.Commands[0].RequiresApproval {
		t.Fatalf("unexpected chat result: %#v", result)
	}
}

func TestChatStreamReturnsDeltasAndReassemblesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.Stream {
			t.Fatalf("stream options=%+v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"正在\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"检查\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"run_ssh_command\",\"arguments\":\"{\\\"command\\\":\\\"df \"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"-h\\\",\\\"reason\\\":\\\"查看磁盘\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	manager := NewManager(nil, AIConfig{BaseURL: server.URL, Model: "stream-model"})
	var deltas strings.Builder
	result, err := manager.ChatStream(context.Background(), []ChatMessage{{Role: "user", Content: "检查磁盘"}}, "server", func(delta string) error {
		deltas.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if deltas.String() != "正在检查" || result.Message != "" || result.TotalTokens != 15 || len(result.Commands) != 1 || result.Commands[0].Command != "df -h" || result.Commands[0].RequiresApproval {
		t.Fatalf("deltas=%q result=%+v", deltas.String(), result)
	}
}

func TestChatStreamFallsBackToJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"完成"}}],"usage":{"total_tokens":7}}`)
	}))
	defer server.Close()
	manager := NewManager(nil, AIConfig{BaseURL: server.URL, Model: "json-model"})
	var delta string
	result, err := manager.ChatStream(context.Background(), []ChatMessage{{Role: "user", Content: "hello"}}, "server", func(value string) error {
		delta += value
		return nil
	})
	if err != nil || delta != "完成" || result.Message != "完成" || result.TotalTokens != 7 {
		t.Fatalf("delta=%q result=%+v err=%v", delta, result, err)
	}
}

func TestModelsReturnsContextMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected model list request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"model-a","owned_by":"local","context_window":131072},{"id":"model-a"},{"id":"model-b","max_model_len":"32768"}]}`)
	}))
	defer server.Close()
	manager := NewManager(nil, AIConfig{BaseURL: server.URL, APIKey: "test-key", Model: "model-a"})
	models, err := manager.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "model-a" || models[0].ContextWindow != 131072 || models[1].ContextWindow != 32768 {
		t.Fatalf("unexpected model metadata: %#v", models)
	}
}

func TestChatRequiresConfiguration(t *testing.T) {
	manager := NewManager(nil, AIConfig{})
	if _, err := manager.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hello"}}, "test host"); err == nil {
		t.Fatal("expected missing model configuration error")
	}
}
