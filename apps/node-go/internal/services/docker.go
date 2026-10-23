package services

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
)

type DockerConfig struct {
	Endpoint   string     `json:"endpoint"` // unix:///absolute/socket, http://host:port, or https://host:port
	Container  string     `json:"container"`
	APIVersion string     `json:"apiVersion,omitempty"` // optional v1.51, no negotiation request
	TLS        *TLSConfig `json:"tls,omitempty"`
}

var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var dockerVersion = regexp.MustCompile(`^v[0-9]{1,2}\.[0-9]{1,3}$`)

func (c DockerConfig) validate() error {
	if !containerName.MatchString(c.Container) {
		return errors.New("invalid Docker container ID or name")
	}
	if c.APIVersion != "" && !dockerVersion.MatchString(c.APIVersion) {
		return errors.New("invalid Docker API version")
	}
	u, e := url.Parse(c.Endpoint)
	if e != nil || len(c.Endpoint) > 2048 || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("invalid Docker endpoint")
	}
	if u.Scheme == "unix" {
		if u.Host != "" || u.Path == "" || u.Path[0] != '/' || path.Clean(u.Path) != u.Path || u.RawPath != "" || c.TLS != nil {
			return errors.New("Docker socket must be an absolute canonical Unix path without TLS")
		}
		return nil
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return errors.New("Docker endpoint must be an HTTP origin or Unix socket")
	}
	if u.Port() != "" {
		if validAddress(u.Host) != nil {
			return errors.New("invalid Docker endpoint port")
		}
	}
	if u.Scheme == "http" && c.TLS != nil {
		return errors.New("Docker TLS requires HTTPS")
	}
	_, e = tlsSettings(c.TLS, u.Hostname())
	return e
}
func checkDocker(ctx context.Context, c DockerConfig) Result {
	u, _ := url.Parse(c.Endpoint)
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 64 << 10}
	defer tr.CloseIdleConnections()
	if u.Scheme == "unix" {
		socket := u.Path
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx, "unix", socket) }
		u = &url.URL{Scheme: "http", Host: "docker.local"}
	} else {
		tr.DialContext = dial
		tr.TLSClientConfig, _ = tlsSettings(c.TLS, u.Hostname())
	}
	u.Path = "/containers/" + c.Container + "/json"
	if c.APIVersion != "" {
		u.Path = "/" + c.APIVersion + u.Path
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return down("Docker inspection request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return down("Docker container not found")
	}
	if resp.StatusCode != http.StatusOK {
		return down("Docker inspection rejected")
	}
	b, err := boundedRead(resp.Body, maxBody)
	if err != nil {
		return down("Docker inspection response exceeds limit or failed")
	}
	var body struct {
		State *struct {
			Running    *bool
			Paused     bool
			Restarting bool
			Dead       bool
			Status     string
			Health     *struct{ Status string }
		}
	}
	if decodeOne(b, &body) != nil || body.State == nil || body.State.Running == nil {
		return down("invalid Docker inspection response")
	}
	s := body.State
	if s.Paused || s.Dead {
		return down("Docker container is paused or dead")
	}
	if s.Restarting || s.Status == "restarting" {
		return Result{State: "pending", Message: "Docker container is restarting"}
	}
	if !*s.Running {
		return down("Docker container is not running")
	}
	if s.Status != "" && s.Status != "running" {
		return down("inconsistent Docker container state")
	}
	if s.Health != nil {
		switch s.Health.Status {
		case "healthy":
			return up("Docker container is running and healthy")
		case "starting":
			return Result{State: "pending", Message: "Docker container health is starting"}
		case "unhealthy":
			return down("Docker container is unhealthy")
		default:
			return down("invalid Docker health state")
		}
	}
	return up("Docker container is running")
}
