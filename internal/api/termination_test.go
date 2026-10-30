package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttyob/velin-web-ssh/internal/store"
	"github.com/ttyob/velin-web-ssh/internal/terminal"
)

func TestTerminateSessionReportsLocalCloseAfterRemoteFailure(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "termination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.CreateUser("owner", "owner", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	host := store.Host{ID: "host", UserID: "owner", Name: "stale host", Address: "192.0.2.1", Port: 22, Username: "test"}
	if err = database.SaveHost(host); err != nil {
		t.Fatal(err)
	}
	meta := store.TerminalSession{ID: "session", UserID: "owner", HostID: host.ID, Name: "terminal", SessionMode: "tmux", Status: "unreachable"}
	if err = database.SaveTerminal(meta); err != nil {
		t.Fatal(err)
	}
	a := &API{store: database, terminals: terminal.NewManager(database, nil, "test")}
	// No credentials exist: remote cleanup fails before any outbound dial.
	request := shareRouteRequest(http.MethodDelete, "/api/sessions/session", "id", meta.ID, `{}`)
	request = request.WithContext(context.WithValue(request.Context(), userKey, store.User{ID: "owner"}))
	recorder := httptest.NewRecorder()
	a.terminateSession(recorder, request)
	var result terminal.TerminationResult
	if err = json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || !result.OK || result.RemoteTerminated || result.Warning == "" {
		t.Fatalf("status=%d termination result=%+v", recorder.Code, result)
	}
	stored, err := database.Terminal("owner", meta.ID)
	if err != nil || stored.Status != "ended" || !strings.Contains(stored.LastError, "SSH credential required") {
		t.Fatalf("stored terminal=%+v error=%v", stored, err)
	}
	// Reloading session management must see the persisted ended state.
	sessions, err := database.Terminals("owner")
	if err != nil || len(sessions) != 1 || sessions[0].Status != "ended" {
		t.Fatalf("session list=%+v error=%v", sessions, err)
	}
}
