package canary

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSteamEscapedOptionsSurviveAPIReplicationAndRestart(t *testing.T) {
	directory := t.TempDir()
	options := func(id, other string) Options {
		return Options{NodeID: id, DBPath: filepath.Join(directory, id+".db"), Port: Ptr(0),
			AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: other, URL: "http://127.0.0.1:1"}}}
	}
	open := func(o Options) *Node {
		n, err := New(o)
		if err != nil {
			t.Fatal("private database startup failed", err)
		}
		t.Cleanup(func() { _ = n.Stop() })
		return n
	}
	a, b := open(options("a", "b")), open(options("b", "a"))
	input := steamInput([]string{"a", "b"})
	input.Steam.APICA = strings.Repeat(string(rune(0x2028)), 32768)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	// Valid JSON may carry U+2028 literally. Go's own encoder escapes it later.
	raw = bytes.ReplaceAll(raw, []byte(`\u2028`), []byte(string(rune(0x2028))))
	if len(raw) >= 140<<10 {
		t.Fatal("fixture must fit the original ingress options cap")
	}
	r := httptest.NewRequest("POST", "/api/monitors", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("admitted compact options failed API creation: %d", w.Code)
	}
	var monitor Monitor
	if err := json.Unmarshal(w.Body.Bytes(), &monitor); err != nil {
		t.Fatal("node could not decode its own API response", err)
	}
	server := httptest.NewServer(b)
	t.Cleanup(server.Close)
	if code, _ := capacityReviewPush(t, a, b, server.URL); code != http.StatusOK {
		t.Fatal("encoded options did not replicate", code)
	}
	server.Close()
	for _, entry := range []struct {
		n *Node
		o Options
	}{{a, options("a", "b")}, {b, options("b", "a")}} {
		if err := entry.n.Stop(); err != nil {
			t.Fatal(err)
		}
		reopened := open(entry.o)
		saved := reopened.current(monitor.ID)
		if saved == nil || saved.Revision != monitor.Revision || !equalJSON(saved.Steam, input.Steam) {
			t.Fatal("restart changed admitted options or readiness")
		}
	}
}
