package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (a *API) agentGitAssist(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Kind    string `json:"kind"`
		Content string `json:"content"`
		Branch  string `json:"branch"`
		Path    string `json:"path"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Kind = strings.TrimSpace(input.Kind)
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" {
		writeError(w, http.StatusBadRequest, "empty_git_ai_content", "没有可供 Git AI 分析的变更内容")
		return
	}
	if len(input.Content) > 120*1024 {
		writeError(w, http.StatusRequestEntityTooLarge, "git_ai_content_too_large", "Git AI 分析内容超过 120 KiB")
		return
	}
	if len(input.Branch) > 512 || len(input.Path) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_git_ai_request", "Git AI 请求参数无效")
		return
	}
	config := a.agents.AIConfig()
	var systemPrompt, userPrompt string
	switch input.Kind {
	case "commit_message":
		systemPrompt = promptOrDefault(config.GitCommitPrompt, defaultGitCommitPrompt)
		userPrompt = fmt.Sprintf("当前分支：%s\n\n以下是待提交变更：\n<git-diff>\n%s\n</git-diff>", strings.TrimSpace(input.Branch), input.Content)
	case "conflict_analysis":
		systemPrompt = promptOrDefault(config.GitConflictPrompt, defaultGitConflictPrompt)
		userPrompt = fmt.Sprintf("当前分支：%s\n冲突文件：%s\n\n以下是冲突差异：\n<git-conflict>\n%s\n</git-conflict>", strings.TrimSpace(input.Branch), strings.TrimSpace(input.Path), input.Content)
	default:
		writeError(w, http.StatusBadRequest, "invalid_git_ai_request", "不支持的 Git AI 操作")
		return
	}
	ctx, cancel := contextWithAgentTimeout(r, 95*time.Second)
	defer cancel()
	result, err := a.agents.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		if strings.Contains(err.Error(), "not configured") {
			writeError(w, http.StatusServiceUnavailable, "ai_not_configured", "AI 模型服务尚未配置")
			return
		}
		writeAgentError(w, err)
		return
	}
	message := strings.TrimSpace(result.Message)
	if input.Kind == "commit_message" {
		message = cleanGitCommitMessage(message)
	}
	if message == "" {
		writeError(w, http.StatusBadGateway, "empty_ai_response", "AI 未返回有效内容")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": message, "model": result.Model})
}

func cleanGitCommitMessage(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") {
		lines := strings.Split(value, "\n")
		if len(lines) > 1 {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			lines = lines[:len(lines)-1]
		}
		value = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	if len(value) > 4000 {
		value = strings.TrimSpace(value[:4000])
	}
	return value
}
