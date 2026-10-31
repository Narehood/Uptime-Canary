// Package updatecheck reads bounded public image metadata, never image layers,
// credentials, Docker sockets or user-supplied registry locations.
package updatecheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const NodeRepository = "narehood/uptime-canary-node"
const WebRepository = "narehood/uptime-canary-web"
const sourceRepository = "https://github.com/Narehood/Uptime-Canary"
const acceptManifests = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

var ErrUnavailable = errors.New("Public update metadata is unavailable")

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}
type Client struct{ http Doer }
type Cache struct {
	ETag        string `json:"etag,omitempty"`
	Digest      string `json:"digest,omitempty"`
	Revision    string `json:"revision,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	Version     string `json:"version,omitempty"`
	VersionRead bool   `json:"versionRead,omitempty"`
}

type Throttled struct{ After time.Duration }

func (e *Throttled) Error() string { return "Update service requested a longer check interval" }

func New(client Doer) *Client {
	if client == nil {
		transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 * 1024, DisableCompression: true, MaxIdleConns: 4, MaxConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}
		client = &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Client{http: client}
}

func (c *Client) Close() {
	if closer, ok := c.http.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func Commit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func digest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}

func (c *Client) get(ctx context.Context, address, token, etag, accept string, limit int64) (int, http.Header, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return 0, nil, nil, ErrUnavailable
	}
	request.Header.Set("User-Agent", "Uptime-Canary/update-check")
	request.Header.Set("Accept-Encoding", "identity")
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 429 || response.StatusCode == 403 {
		after := 6 * time.Hour
		if seconds, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 32); err == nil && seconds > 0 {
			after = time.Duration(seconds) * time.Second
		}
		if until, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
			after = time.Until(until)
		}
		return response.StatusCode, nil, nil, &Throttled{After: min(24*time.Hour, max(time.Hour, after))}
	}
	if response.ContentLength > limit {
		return response.StatusCode, nil, nil, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return response.StatusCode, nil, nil, ErrUnavailable
	}
	return response.StatusCode, response.Header.Clone(), raw, nil
}

func verified(raw []byte, expected string) bool {
	actual := sha256.Sum256(raw)
	return expected == "sha256:"+hex.EncodeToString(actual[:])
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}
type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Manifests     []descriptor `json:"manifests"`
}

func (c *Client) Registry(ctx context.Context, repository string, previous Cache) (Cache, error) {
	if repository != NodeRepository && repository != WebRepository {
		return Cache{}, ErrUnavailable
	}
	// Older caches never read the version label. Refresh their config once,
	// including images that legitimately have no version label.
	if !previous.VersionRead {
		previous.ETag, previous.Digest = "", ""
	}
	status, _, raw, err := c.get(ctx, "https://ghcr.io/token?service=ghcr.io&scope="+url.QueryEscape("repository:"+repository+":pull"), "", "", "application/json", 32*1024)
	if err != nil {
		return Cache{}, err
	}
	var authorization struct {
		Token string `json:"token"`
	}
	if status != 200 || json.Unmarshal(raw, &authorization) != nil || len(authorization.Token) < 1 || len(authorization.Token) > 8192 || strings.ContainsAny(authorization.Token, "\r\n") {
		return Cache{}, ErrUnavailable
	}
	base := "https://ghcr.io/v2/" + repository
	status, headers, raw, err := c.get(ctx, base+"/manifests/latest", authorization.Token, previous.ETag, acceptManifests, 256*1024)
	if err != nil {
		return Cache{}, err
	}
	if status == 304 && Commit(previous.Revision) && digest(previous.Digest) {
		return previous, nil
	}
	if status != 200 {
		return Cache{}, ErrUnavailable
	}
	sum := sha256.Sum256(raw)
	topDigest := "sha256:" + hex.EncodeToString(sum[:])
	if serverDigest := headers.Get("Docker-Content-Digest"); serverDigest != "" && serverDigest != topDigest {
		return Cache{}, ErrUnavailable
	}
	result := Cache{Digest: topDigest}
	if etag := headers.Get("ETag"); len(etag) <= 200 && !strings.ContainsAny(etag, "\r\n") {
		result.ETag = etag
	}
	if previous.Digest == topDigest && Commit(previous.Revision) {
		result.Revision = previous.Revision
		result.CreatedAt = previous.CreatedAt
		result.Version = previous.Version
		result.VersionRead = previous.VersionRead
		return result, nil
	}
	var image manifest
	if json.Unmarshal(raw, &image) != nil || image.SchemaVersion != 2 {
		return Cache{}, ErrUnavailable
	}
	if len(image.Manifests) > 0 {
		if len(image.Manifests) > 32 {
			return Cache{}, ErrUnavailable
		}
		var selected *descriptor
		for i := range image.Manifests {
			d := &image.Manifests[i]
			if d.Platform.OS == "linux" && d.Platform.Architecture == "amd64" {
				if selected != nil {
					return Cache{}, ErrUnavailable
				}
				selected = d
			}
		}
		if selected == nil || !digest(selected.Digest) || selected.Size < 1 || selected.Size > 256*1024 {
			return Cache{}, ErrUnavailable
		}
		status, _, raw, err = c.get(ctx, base+"/manifests/"+selected.Digest, authorization.Token, "", acceptManifests, 256*1024)
		if err != nil {
			return Cache{}, err
		}
		image = manifest{}
		if status != 200 || int64(len(raw)) != selected.Size || !verified(raw, selected.Digest) || json.Unmarshal(raw, &image) != nil || image.SchemaVersion != 2 || len(image.Manifests) > 0 {
			return Cache{}, ErrUnavailable
		}
	}
	if !digest(image.Config.Digest) || image.Config.Size < 1 || image.Config.Size > 1024*1024 {
		return Cache{}, ErrUnavailable
	}
	status, headers, raw, err = c.get(ctx, base+"/blobs/"+image.Config.Digest, authorization.Token, "", "application/octet-stream", 1024*1024)
	if err != nil {
		return Cache{}, err
	}
	if status == 307 || status == 302 {
		location := headers.Get("Location")
		target, err := url.Parse(location)
		if err != nil || len(location) > 8192 || target.Scheme != "https" || target.Host != "pkg-containers.githubusercontent.com" || target.User != nil || target.Fragment != "" {
			return Cache{}, ErrUnavailable
		}
		// The registry's short-lived read token never reaches the CDN. The
		// immutable config digest authenticates its bytes independently.
		status, _, raw, err = c.get(ctx, location, "", "", "application/octet-stream", 1024*1024)
		if err != nil {
			return Cache{}, err
		}
	}
	var config struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if status != 200 || int64(len(raw)) != image.Config.Size || !verified(raw, image.Config.Digest) || json.Unmarshal(raw, &config) != nil {
		return Cache{}, ErrUnavailable
	}
	labels := config.Config.Labels
	if !strings.EqualFold(strings.TrimSuffix(labels["org.opencontainers.image.source"], "/"), sourceRepository) || !Commit(labels["org.opencontainers.image.revision"]) {
		return Cache{}, ErrUnavailable
	}
	result.Revision = labels["org.opencontainers.image.revision"]
	result.VersionRead = true
	if version := labels["org.opencontainers.image.version"]; len(version) <= 128 && !strings.ContainsAny(version, "\r\n\x00") {
		result.Version = version
	}
	if created := labels["org.opencontainers.image.created"]; len(created) <= 40 {
		if parsed, err := time.Parse(time.RFC3339, created); err == nil && parsed.UnixMilli() > 0 && parsed.Before(time.Now().Add(24*time.Hour)) {
			result.CreatedAt = parsed.UTC().Format(time.RFC3339)
		}
	}
	return result, nil
}

// Relation distinguishes a newer published source from an ahead/custom build.
// The caller caches comparisons and caps their count per scheduled check.
func (c *Client) Relation(ctx context.Context, current, latest string) (string, error) {
	if !Commit(current) || !Commit(latest) {
		return "unknown", ErrUnavailable
	}
	if current == latest {
		return "current", nil
	}
	// GitHub includes file patches only on page one. Page two retains the
	// comparison status without downloading a potentially huge source diff.
	// https://docs.github.com/en/rest/commits/commits#compare-two-commits
	status, _, raw, err := c.get(ctx, "https://api.github.com/repos/Narehood/Uptime-Canary/compare/"+current+"..."+latest+"?per_page=1&page=2", "", "", "application/vnd.github+json", 1024*1024)
	if err != nil {
		return "unavailable", err
	}
	var comparison struct {
		Status string `json:"status"`
	}
	if status != 200 || json.Unmarshal(raw, &comparison) != nil {
		return "unknown", ErrUnavailable
	}
	switch comparison.Status {
	case "ahead":
		return "update-available", nil
	case "behind":
		return "ahead", nil
	case "identical":
		return "current", nil
	default:
		return "unknown", nil
	}
}
