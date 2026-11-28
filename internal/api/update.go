package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ttyob/velin-web-ssh/internal/version"
)

const (
	defaultReleaseURL = "https://api.github.com/repos/" + version.Repository + "/releases/latest"
	updateCacheTTL    = 15 * time.Minute
)

type releaseVersion struct {
	major int
	minor int
	patch int
	pre   string
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

type updateInfo struct {
	CurrentVersion  string         `json:"currentVersion"`
	LatestVersion   string         `json:"latestVersion"`
	VersionKnown    bool           `json:"versionKnown"`
	UpdateAvailable bool           `json:"updateAvailable"`
	ReleaseURL      string         `json:"releaseURL"`
	ReleaseName     string         `json:"releaseName"`
	PublishedAt     string         `json:"publishedAt"`
	CheckedAt       string         `json:"checkedAt"`
	SelfUpdate      selfUpdateInfo `json:"selfUpdate"`
}

type selfUpdateInfo struct {
	Enabled       bool   `json:"enabled"`
	RequestID     string `json:"requestID,omitempty"`
	State         string `json:"state,omitempty"`
	TargetVersion string `json:"targetVersion,omitempty"`
	Message       string `json:"message,omitempty"`
	RequestedAt   string `json:"requestedAt,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type selfUpdateRequest struct {
	RequestID     string `json:"requestID"`
	TargetVersion string `json:"targetVersion"`
	RequestedAt   string `json:"requestedAt"`
}

func parseReleaseVersion(raw string) (releaseVersion, bool) {
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "v"), "V"))
	if value == "" {
		return releaseVersion{}, false
	}
	core := strings.SplitN(value, "-", 2)
	parts := strings.Split(strings.SplitN(core[0], "+", 2)[0], ".")
	if len(parts) != 3 {
		return releaseVersion{}, false
	}
	values := [3]int{}
	for i, part := range parts {
		if part == "" {
			return releaseVersion{}, false
		}
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return releaseVersion{}, false
		}
		values[i] = parsed
	}
	pre := ""
	if len(core) == 2 {
		pre = strings.TrimSpace(core[1])
	}
	return releaseVersion{major: values[0], minor: values[1], patch: values[2], pre: pre}, true
}

func validReleaseImageTag(raw string) bool {
	if len(raw) < 6 || len(raw) > 129 || raw[0] != 'v' {
		return false
	}
	for _, char := range raw {
		if !(char >= '0' && char <= '9' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char == '.' || char == '-') {
			return false
		}
	}
	_, ok := parseReleaseVersion(raw)
	return ok
}

func compareReleaseVersions(left, right releaseVersion) int {
	for _, pair := range [][2]int{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if left.pre == right.pre {
		return 0
	}
	if left.pre == "" {
		return 1
	}
	if right.pre == "" {
		return -1
	}
	if left.pre < right.pre {
		return -1
	}
	return 1
}

func fetchLatestRelease(ctx context.Context, client *http.Client, endpoint, current string) (updateInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return updateInfo{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "JeexTerminal/"+current)
	response, err := client.Do(request)
	if err != nil {
		return updateInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return updateInfo{}, fmt.Errorf("GitHub 返回 HTTP %d", response.StatusCode)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 256*1024)).Decode(&release); err != nil {
		return updateInfo{}, fmt.Errorf("解析 GitHub release 失败: %w", err)
	}
	latest, ok := parseReleaseVersion(release.TagName)
	if !ok || !validReleaseImageTag(release.TagName) {
		return updateInfo{}, fmt.Errorf("GitHub release 版本号无效: %q", release.TagName)
	}
	if release.HTMLURL != "" {
		parsedURL, err := url.Parse(release.HTMLURL)
		if err != nil || parsedURL.Scheme != "https" || parsedURL.Host != "github.com" {
			release.HTMLURL = ""
		}
	}
	currentVersion, currentKnown := parseReleaseVersion(current)
	return updateInfo{
		CurrentVersion:  current,
		LatestVersion:   release.TagName,
		VersionKnown:    currentKnown,
		UpdateAvailable: currentKnown && compareReleaseVersions(currentVersion, latest) < 0,
		ReleaseURL:      release.HTMLURL,
		ReleaseName:     release.Name,
		PublishedAt:     release.PublishedAt,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (a *API) update(w http.ResponseWriter, r *http.Request) {
	a.updateMu.Lock()
	if r.URL.Query().Get("refresh") != "1" && !a.updateCheckedAt.IsZero() && time.Since(a.updateCheckedAt) < updateCacheTTL {
		cached := a.updateCache
		a.updateMu.Unlock()
		writeJSON(w, http.StatusOK, a.attachSelfUpdate(cached))
		return
	}
	client := a.updateClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	endpoint := a.updateURL
	if endpoint == "" {
		endpoint = defaultReleaseURL
	}
	a.updateMu.Unlock()

	info, err := fetchLatestRelease(r.Context(), client, endpoint, version.Current)
	if err != nil {
		writeError(w, http.StatusBadGateway, "update_check_failed", "无法检查版本更新")
		return
	}
	a.updateMu.Lock()
	a.updateCache = info
	a.updateCheckedAt = time.Now()
	a.updateMu.Unlock()
	writeJSON(w, http.StatusOK, a.attachSelfUpdate(info))
}

func (a *API) selfUpdateDir() string {
	return filepath.Join(a.cfg.DataDir, "self-update")
}

func (a *API) attachSelfUpdate(info updateInfo) updateInfo {
	status := selfUpdateInfo{Enabled: a.cfg.SelfUpdate}
	path := filepath.Join(a.selfUpdateDir(), "status.json")
	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		if decodeErr := json.NewDecoder(io.LimitReader(file, 64*1024)).Decode(&status); decodeErr != nil {
			status = selfUpdateInfo{Enabled: a.cfg.SelfUpdate, State: "failed", Message: "无法读取更新状态"}
		}
	}
	status.Enabled = a.cfg.SelfUpdate
	info.SelfUpdate = status
	return info
}

func writeUpdateJSON(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".update-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err = temp.Chmod(0o600); err == nil {
		err = json.NewEncoder(temp).Encode(value)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func selfUpdateInProgress(state string) bool {
	switch state {
	case "queued", "pulling", "restarting", "verifying", "rolling_back":
		return true
	default:
		return false
	}
}

func (a *API) startUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.SelfUpdate {
		writeError(w, http.StatusConflict, "self_update_unavailable", "当前部署未启用一键更新")
		return
	}
	current := a.attachSelfUpdate(updateInfo{}).SelfUpdate
	if selfUpdateInProgress(current.State) {
		writeError(w, http.StatusConflict, "self_update_in_progress", "版本更新正在进行")
		return
	}
	client := a.updateClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	endpoint := a.updateURL
	if endpoint == "" {
		endpoint = defaultReleaseURL
	}
	info, err := fetchLatestRelease(r.Context(), client, endpoint, version.Current)
	if err != nil {
		writeError(w, http.StatusBadGateway, "update_check_failed", "无法检查版本更新")
		return
	}
	if !info.VersionKnown {
		writeError(w, http.StatusConflict, "development_version", "开发版本不能执行一键更新")
		return
	}
	if !info.UpdateAvailable {
		writeError(w, http.StatusConflict, "already_latest", "当前已经是最新版本")
		return
	}
	if !validReleaseImageTag(info.LatestVersion) {
		writeError(w, http.StatusBadGateway, "invalid_release_version", "发布版本号无法用于镜像更新")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	request := selfUpdateRequest{RequestID: uuid.NewString(), TargetVersion: info.LatestVersion, RequestedAt: now}
	status := selfUpdateInfo{Enabled: true, RequestID: request.RequestID, State: "queued", TargetVersion: request.TargetVersion, Message: "更新任务已提交", RequestedAt: now, UpdatedAt: now}
	statusPath := filepath.Join(a.selfUpdateDir(), "status.json")
	requestPath := filepath.Join(a.selfUpdateDir(), "request.json")
	a.updateMu.Lock()
	current = a.attachSelfUpdate(updateInfo{}).SelfUpdate
	if selfUpdateInProgress(current.State) {
		a.updateMu.Unlock()
		writeError(w, http.StatusConflict, "self_update_in_progress", "版本更新正在进行")
		return
	}
	if err = writeUpdateJSON(statusPath, status); err == nil {
		err = writeUpdateJSON(requestPath, request)
	}
	if err != nil {
		failed := status
		failed.State = "failed"
		failed.Message = "无法创建更新任务"
		failed.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		_ = writeUpdateJSON(statusPath, failed)
		a.updateMu.Unlock()
		if !errors.Is(err, os.ErrPermission) {
			writeError(w, http.StatusInternalServerError, "self_update_failed", "无法创建更新任务")
			return
		}
		writeError(w, http.StatusForbidden, "self_update_permission_denied", "更新目录不可写")
		return
	}
	a.updateCache = info
	a.updateCheckedAt = time.Now()
	a.updateMu.Unlock()
	info.SelfUpdate = status
	writeJSON(w, http.StatusAccepted, info)
}
