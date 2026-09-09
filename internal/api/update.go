package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	VersionKnown    bool   `json:"versionKnown"`
	UpdateAvailable bool   `json:"updateAvailable"`
	ReleaseURL      string `json:"releaseURL"`
	ReleaseName     string `json:"releaseName"`
	PublishedAt     string `json:"publishedAt"`
	CheckedAt       string `json:"checkedAt"`
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
	request.Header.Set("User-Agent", "VelinWebSSH/"+current)
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
	if !ok {
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
		writeJSON(w, http.StatusOK, cached)
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
	writeJSON(w, http.StatusOK, info)
}
