package canary

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessDistinguishesFaultFromLiveness(t *testing.T) {
	n := cr2New(t, cr2Options(t, false))
	request := func(path string, code int, body string) {
		t.Helper()
		w := httptest.NewRecorder()
		n.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != code || strings.TrimSpace(w.Body.String()) != body {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	request("/readyz", 200, `{"ready":true}`)
	// Missing peers do not disable independent monitoring/readiness.
	n.fault = errors.New("private persistence diagnostic")
	request("/readyz", 503, `{"ready":false}`)
	request("/healthz", 200, `{"ok":true}`)
	n.fault = nil
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	request("/readyz", 503, `{"ready":false}`)
}
