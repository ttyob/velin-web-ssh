package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ttyob/velin-web-ssh/internal/security"
	"github.com/ttyob/velin-web-ssh/internal/store"
)

func TestMobileCapabilitiesRequireAuthentication(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mobile.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.CreateUser("mobile-user", "mobile", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	if err = db.CreateAuthSession("mobile-session", "mobile-user", security.TokenHash("token"), "test", "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a := &API{store: db}
	handler := a.Router()
	for _, authenticated := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/mobile/v1/capabilities", nil)
		if authenticated {
			r.Header.Set("Cookie", a.sessionCookieName()+"=token")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if !authenticated {
			if w.Code != 401 {
				t.Fatalf("public capabilities: %d", w.Code)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatalf("capabilities: %d %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["viewAttach"] != true || body["inputAck"] != true || body["geometry"] != true || body["maxInputBytes"] != float64(49152) {
			t.Fatalf("capabilities: %+v", body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("capabilities may be cached")
		}
	}
}
