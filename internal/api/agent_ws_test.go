package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"velin-webssh/internal/agent"
	"velin-webssh/internal/store"
)

func TestAgentWSReusesConnectionForMultipleRequests(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "agent-ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.CreateUser("user-1", "user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	const tokenHash = "agent-ws-token-hash"
	if err = database.CreateAuthSession("session-1", "user-1", tokenHash, "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	a := &API{store: database, agents: agent.NewManager(nil, agent.AIConfig{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), userKey, store.User{ID: "user-1"})
		ctx = context.WithValue(ctx, authTokenHashKey, tokenHash)
		a.agentWS(w, r.WithContext(ctx))
	}))
	defer server.Close()

	header := http.Header{"Origin": {server.URL}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/hosts/host-1/agent", header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var event agentWSEvent
	if err = conn.ReadJSON(&event); err != nil || event.Type != "ready" {
		t.Fatalf("ready event=%+v err=%v", event, err)
	}
	for index := 1; index <= 2; index++ {
		requestID := fmt.Sprintf("command-%d", index)
		if err = conn.WriteJSON(agentWSRequest{Type: "command", RequestID: requestID, Command: "pwd"}); err != nil {
			t.Fatal(err)
		}
		if err = conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "done" || event.RequestID != requestID || event.Kind != "command" || event.Success {
			t.Fatalf("command event=%+v", event)
		}
	}
	if err = conn.WriteJSON(agentWSRequest{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if err = conn.ReadJSON(&event); err != nil || event.Type != "pong" {
		t.Fatalf("pong event=%+v err=%v", event, err)
	}
	if got := a.wsTotal.Load(); got != 1 {
		t.Fatalf("WebSocket connections=%d, want 1", got)
	}
	if got := a.websockets.Load(); got != 1 {
		t.Fatalf("active WebSocket connections=%d, want 1", got)
	}
}
