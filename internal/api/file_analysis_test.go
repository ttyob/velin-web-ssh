package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ttyob/velin-web-ssh/internal/agent"
)

func TestFileAnalysisPreservesEditorSnapshot(t *testing.T) {
	content := "\n  const value = missing;\n"
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role    string
				Content string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input.Messages) != 2 || !strings.Contains(input.Messages[1].Content, "<file>\n"+content+"\n</file>") {
			t.Error("editor snapshot was trimmed or replaced")
		}
		if !strings.Contains(input.Messages[0].Content, "不要执行") {
			t.Error("missing review-only instruction")
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "```json\n{\"summary\":\"发现未定义变量\",\"diagnostics\":[{\"line\":2,\"severity\":\"error\",\"message\":\"missing 未定义\",\"suggestion\":\"声明该变量\"},{\"line\":99,\"message\":\"无效定位\"}]}\n```"}}}})
	}))
	defer model.Close()
	a := &API{agents: agent.NewManager(nil, agent.AIConfig{BaseURL: model.URL, Model: "fixture"})}
	body, _ := json.Marshal(map[string]string{"path": "/src/app.ts", "content": content})
	response := httptest.NewRecorder()
	a.analyzeEditorFile(response, httptest.NewRequest(http.MethodPost, "/api/agent/file-analysis", strings.NewReader(string(body))))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var result fileAnalysis
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Summary != "发现未定义变量" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Line != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFileAnalysisValidation(t *testing.T) {
	a := &API{agents: agent.NewManager(nil, agent.AIConfig{})}
	for _, test := range []struct {
		path, content string
		status        int
	}{
		{"", "hello", http.StatusBadRequest},
		{"/src/app.ts", strings.Repeat("中", 41*1024), http.StatusBadRequest},
		{"/src/app.ts", "hello", http.StatusServiceUnavailable},
	} {
		body, _ := json.Marshal(map[string]string{"path": test.path, "content": test.content})
		response := httptest.NewRecorder()
		a.analyzeEditorFile(response, httptest.NewRequest(http.MethodPost, "/api/agent/file-analysis", strings.NewReader(string(body))))
		if response.Code != test.status {
			t.Fatalf("status %d, expected %d: %s", response.Code, test.status, response.Body.String())
		}
	}
	for _, content := range []string{"plain text", `{}`, `{"summary":"ok","diagnostics":"invalid"}`} {
		if _, err := parseFileAnalysis(content, 10); err == nil {
			t.Fatalf("accepted malformed analysis: %s", content)
		}
	}
	result, err := parseFileAnalysis(`{"summary":"未发现明确问题","diagnostics":[]}`, 1)
	if err != nil || len(result.Diagnostics) != 0 {
		t.Fatalf("valid empty result: %+v %v", result, err)
	}
}
