package services

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerStatesRouteAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name, body, state string
		status            int
	}{
		{"running", `{"State":{"Running":true,"Status":"running"}}`, "up", 200},
		{"healthy", `{"State":{"Running":true,"Health":{"Status":"healthy"}}}`, "up", 200},
		{"starting", `{"State":{"Running":true,"Health":{"Status":"starting"}}}`, "pending", 200},
		{"restarting", `{"State":{"Running":false,"Restarting":true}}`, "pending", 200},
		{"stopped", `{"State":{"Running":false}}`, "down", 200},
		{"paused", `{"State":{"Running":true,"Paused":true}}`, "down", 200},
		{"unhealthy", `{"State":{"Running":true,"Health":{"Status":"unhealthy"}}}`, "down", 200},
		{"malformed", `{"State":{"Running":"true"}}`, "down", 200},
		{"missing", `{}`, "down", 200}, {"unknown-health", `{"State":{"Running":true,"Health":{"Status":"private-value"}}}`, "down", 200},
		{"missing-container", `{"message":"fixture-secret"}`, "down", 404}, {"refused", `fixture-secret`, "down", 403}, {"redirect", ``, "down", 302},
		{"oversized", strings.Repeat(" ", maxBody+1), "down", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				if r.Method != "GET" || r.URL.RequestURI() != "/v1.51/containers/monitor_1/json" || r.ContentLength != 0 || r.Header.Get("Accept") != "application/json" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
				}
				w.Header().Set("Location", "http://127.0.0.1:1/secret")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			expectState(t, Config{Type: Docker, Timeout: time.Second, Docker: &DockerConfig{Endpoint: srv.URL, Container: "monitor_1", APIVersion: "v1.51"}}, tc.state)
			if called != 1 {
				t.Fatalf("requests=%d", called)
			}
		})
	}
}
func TestDockerTLS(t *testing.T) {
	tc, trust := testTLS(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"State":{"Running":true}}`) }))
	srv.TLS = tc
	srv.StartTLS()
	defer srv.Close()
	c := Config{Type: Docker, Timeout: time.Second, Docker: &DockerConfig{Endpoint: srv.URL, Container: "monitor", TLS: trust}}
	expectState(t, c, "up")
	c.Docker.TLS = nil
	expectState(t, c, "down")
}

func TestDockerUnixSocket(t *testing.T) {
	socket := filepath.ToSlash(filepath.Join(t.TempDir(), "docker.sock"))
	// Windows AF_UNIX also accepts a root-relative slash path on the current
	// volume, allowing the same canonical unix:///path endpoint contract.
	if volume := filepath.VolumeName(socket); volume != "" {
		socket = strings.TrimPrefix(socket, volume)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("AF_UNIX fixture unavailable: %v", err)
	}
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/containers/monitor/json" {
			t.Error("incorrect socket request")
		}
		fmt.Fprint(w, `{"State":{"Running":true}}`)
	})}
	go httpServer.Serve(ln)
	defer httpServer.Close()
	expectState(t, Config{Type: Docker, Timeout: time.Second, Docker: &DockerConfig{Endpoint: "unix://" + socket, Container: "monitor"}}, "up")
}
func TestDockerCancellationAndValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	r := Check(context.Background(), Config{Type: Docker, Timeout: 50 * time.Millisecond, Docker: &DockerConfig{Endpoint: srv.URL, Container: "monitor"}})
	if r.State != "down" || r.LatencyMs > 1000 {
		t.Fatal(r)
	}
	for _, d := range []DockerConfig{{Endpoint: "unix:///a/../docker.sock", Container: "a"}, {Endpoint: "http://user:secret@localhost", Container: "a"}, {Endpoint: "http://localhost/base", Container: "a"}, {Endpoint: "http://localhost", Container: "../a"}, {Endpoint: "http://localhost", Container: "a", APIVersion: "../v"}, {Endpoint: "http://localhost:99999", Container: "a"}} {
		if d.validate() == nil {
			t.Fatal("accepted unsafe Docker config")
		}
	}
}
