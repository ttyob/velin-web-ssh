package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestFetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.36","name":"Release v0.3.36","html_url":"https://github.com/ttyob/VelinWebSsh/releases/tag/v0.3.36","published_at":"2026-09-07T00:00:00Z"}`))
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
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.36","html_url":"https://github.com/ttyob/VelinWebSsh/releases/tag/v0.3.36"}`))
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
