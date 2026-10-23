package canary

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagementReauthorizesAfterBlockedRequestBody(t *testing.T) {
	for _, change := range []string{"demotion", "deletion", "token rotation", "session logout", "session expiry"} {
		t.Run(change, func(t *testing.T) {
			clock, now := clock()
			n := identityNode(t, func(o *Options) { o.Now = now })
			a := createIdentityAccount(t, n, "editor", "editor", editorToken, accountPassword)
			token := editorToken
			if strings.HasPrefix(change, "session") {
				token = passwordLogin(t, n, "editor", accountPassword, "")
			}
			encoded, err := json.Marshal(input([]string{n.ID}, "http://127.0.0.1:1", ""))
			if err != nil {
				t.Fatal(err)
			}
			body := &pausedIdentityBody{Reader: strings.NewReader(string(encoded)), started: make(chan struct{}), release: make(chan struct{})}
			r := httptest.NewRequest("POST", "/api/monitors", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			r.Body = body
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); n.ServeHTTP(w, r) }()
			select {
			case <-body.started:
			case <-time.After(3 * time.Second):
				close(body.release)
				t.Fatal("request never reached body reader")
			}
			want := 401
			var changed *httptest.ResponseRecorder
			switch change {
			case "demotion":
				changed = identityCall(n, "PUT", "/api/accounts/"+a.ID, `{"role":"viewer"}`, adminToken)
				want = 403
			case "deletion":
				changed = identityCall(n, "DELETE", "/api/accounts/"+a.ID, "{}", adminToken)
			case "token rotation":
				changed = identityCall(n, "PUT", "/api/accounts/"+a.ID, `{"token":"rotated-editor-secret-123456789"}`, adminToken)
			case "session logout":
				changed = identityCall(n, "POST", "/api/auth/logout", "{}", token)
			case "session expiry":
				clock.Add(int64(13 * time.Hour / time.Millisecond))
			}
			close(body.release)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not finish")
			}
			if changed != nil && changed.Code != 200 && changed.Code != 204 {
				t.Fatalf("account change: %d %s", changed.Code, changed.Body.String())
			}
			if w.Code != want {
				t.Fatalf("stale request: got %d, want %d: %s", w.Code, want, w.Body.String())
			}
			n.mu.Lock()
			count := len(n.heads)
			n.mu.Unlock()
			if count != 0 {
				t.Fatal("unauthorized request created a monitor")
			}
		})
	}
}
