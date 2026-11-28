package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ttyob/velin-web-ssh/internal/config"
	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/version"
)

func TestParseReleaseVersion(t *testing.T) {
	tests := []struct {
		input string
		ok    bool
		want  releaseVersion
	}{
		{input: "v0.3.35", ok: true, want: releaseVersion{major: 0, minor: 3, patch: 35}},
		{input: "1.2.3-beta.1", ok: true, want: releaseVersion{major: 1, minor: 2, patch: 3, pre: "beta.1"}},
		{input: "v1.2", ok: false},
		{input: "release", ok: false},
	}
	for _, test := range tests {
		got, ok := parseReleaseVersion(test.input)
		if ok != test.ok || ok && got != test.want {
			t.Errorf("parseReleaseVersion(%q) = %#v, %v; want %#v, %v", test.input, got, ok, test.want, test.ok)
		}
	}
}

func TestValidReleaseImageTag(t *testing.T) {
	for _, test := range []struct {
		tag  string
		want bool
	}{
		{tag: "v1.2.3", want: true},
		{tag: "v1.2.3-beta.1", want: true},
		{tag: "v1.2.3+build.1", want: false},
		{tag: "v1.2.3$(touch-bad)", want: false},
		{tag: "1.2.3", want: false},
	} {
		if got := validReleaseImageTag(test.tag); got != test.want {
			t.Errorf("validReleaseImageTag(%q) = %v, want %v", test.tag, got, test.want)
		}
	}
}

func TestFetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.36","name":"Release v0.3.36","html_url":"https://github.com/ttyob/velin-web-ssh/releases/tag/v0.3.36","published_at":"2026-09-07T00:00:00Z"}`))
	}))
	defer server.Close()

	got, err := fetchLatestRelease(context.Background(), server.Client(), server.URL, "v0.3.35")
	if err != nil {
		t.Fatal(err)
	}
	if !got.VersionKnown || !got.UpdateAvailable || got.LatestVersion != "v0.3.36" || got.ReleaseURL == "" {
		t.Fatalf("unexpected update result: %+v", got)
	}
}

func TestUpdateCachesReleaseCheck(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.36","html_url":"https://github.com/ttyob/velin-web-ssh/releases/tag/v0.3.36"}`))
	}))
	defer server.Close()

	a := &API{updateURL: server.URL, updateClient: server.Client()}
	for i := 0; i < 2; i++ {
		recording := httptest.NewRecorder()
		a.update(recording, httptest.NewRequest(http.MethodGet, "/api/system/update", nil))
		if recording.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i, recording.Code)
		}
	}
	if requests != 1 {
		t.Fatalf("release endpoint requests = %d, want 1", requests)
	}
}

func TestStartUpdateCreatesRestrictedRequest(t *testing.T) {
	previousVersion := version.Current
	version.Current = "v0.3.57"
	t.Cleanup(func() { version.Current = previousVersion })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0","html_url":"https://github.com/ttyob/velin-web-ssh/releases/tag/v99.0.0"}`))
	}))
	defer server.Close()
	directory := t.TempDir()
	a := &API{cfg: config.Config{DataDir: directory, SelfUpdate: true}, updateURL: server.URL, updateClient: server.Client()}
	recording := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/system/update", nil)
	request = request.WithContext(context.WithValue(request.Context(), userKey, store.User{ID: "admin", Role: "admin"}))
	a.startUpdate(recording, request)
	if recording.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", recording.Code, recording.Body.String())
	}
	var queued selfUpdateRequest
	requestPath := filepath.Join(directory, "self-update", "request.json")
	content, err := os.ReadFile(requestPath)
	if err != nil || json.Unmarshal(content, &queued) != nil {
		t.Fatalf("read update request: %v content=%s", err, content)
	}
	if queued.RequestID == "" || queued.TargetVersion != "v99.0.0" || queued.RequestedAt == "" {
		t.Fatalf("unexpected request: %+v", queued)
	}
	if mode := fileMode(t, requestPath); mode != 0o600 {
		t.Fatalf("request mode = %o", mode)
	}
}

func TestStartUpdateRequiresEnabledUpdater(t *testing.T) {
	a := &API{}
	recording := httptest.NewRecorder()
	a.startUpdate(recording, httptest.NewRequest(http.MethodPost, "/api/admin/system/update", nil))
	if recording.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", recording.Code, recording.Body.String())
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
