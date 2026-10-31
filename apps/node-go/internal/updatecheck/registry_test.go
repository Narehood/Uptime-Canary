package updatecheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type doer func(*http.Request) (*http.Response, error)

func (f doer) Do(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body []byte, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(string(body))), ContentLength: int64(len(body))}
}
func sum(raw []byte) string { d := sha256.Sum256(raw); return "sha256:" + hex.EncodeToString(d[:]) }
func imageFixture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	config, _ := json.Marshal(map[string]any{"config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.source": sourceRepository, "org.opencontainers.image.revision": strings.Repeat("a", 40)}}})
	image, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": map[string]any{"digest": sum(config), "size": len(config)}})
	return image, config
}

func TestRegistryBuildTimeIsAuthenticatedMetadataAndRetainedOnConditionalReads(t *testing.T) {
	for _, stamp := range []string{"2025-02-02T12:34:56Z", "", "not-a-time", "2999-01-01T00:00:00Z"} {
		t.Run(stamp, func(t *testing.T) {
			config, _ := json.Marshal(map[string]any{"config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.source": sourceRepository, "org.opencontainers.image.revision": strings.Repeat("a", 40), "org.opencontainers.image.created": stamp, "org.opencontainers.image.version": "0.1.0-dev.42"}}})
			image, _ := json.Marshal(map[string]any{"schemaVersion": 2, "config": map[string]any{"digest": sum(config), "size": len(config)}})
			unchanged := false
			c := New(doer(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/token" {
					return response(200, []byte(`{"token":"metadata-only"}`), nil), nil
				}
				if strings.HasSuffix(r.URL.Path, "/manifests/latest") {
					if unchanged {
						return response(304, nil, nil), nil
					}
					return response(200, image, http.Header{"Etag": []string{`"fixture"`}, "Docker-Content-Digest": []string{sum(image)}}), nil
				}
				if strings.HasSuffix(r.URL.Path, "/blobs/"+sum(config)) {
					return response(200, config, nil), nil
				}
				t.Fatal("unexpected image layer or endpoint")
				return nil, ErrUnavailable
			}))
			first, err := c.Registry(context.Background(), NodeRepository, Cache{})
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if stamp == "2025-02-02T12:34:56Z" {
				want = stamp
			}
			if first.CreatedAt != want {
				t.Fatal("invalid or missing publisher time was treated as reliable", first.CreatedAt)
			}
			if first.Version != "0.1.0-dev.42" {
				t.Fatal("publisher version was not retained")
			}
			unchanged = true
			second, err := c.Registry(context.Background(), NodeRepository, first)
			if err != nil || second != first {
				t.Fatal("conditional check lost publisher time", err)
			}
		})
	}
}

func TestRegistryReadsOnlyMetadataAndReusesConditionalManifestCache(t *testing.T) {
	image, config := imageFixture(t)
	requests := 0
	unchanged := false
	c := New(doer(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Scheme != "https" || r.URL.Host != "ghcr.io" {
			t.Fatal("unexpected network origin")
		}
		if r.URL.Path == "/token" {
			if r.Header.Get("Authorization") != "" || r.URL.Query().Get("scope") != "repository:"+NodeRepository+":pull" {
				t.Fatal("wrong anonymous auth scope")
			}
			return response(200, []byte(`{"token":"anonymous-read-token"}`), nil), nil
		}
		if r.Header.Get("Authorization") != "Bearer anonymous-read-token" {
			t.Fatal("missing pull token")
		}
		switch r.URL.Path {
		case "/v2/" + NodeRepository + "/manifests/latest":
			if unchanged {
				if r.Header.Get("If-None-Match") != `"manifest-one"` {
					t.Fatal("cached ETag missing")
				}
				return response(304, nil, nil), nil
			}
			return response(200, image, http.Header{"Etag": []string{`"manifest-one"`}, "Docker-Content-Digest": []string{sum(image)}}), nil
		case "/v2/" + NodeRepository + "/blobs/" + sum(config):
			return response(200, config, nil), nil
		default:
			t.Fatal("unexpected manifest or image-layer request", r.URL.Path)
			return nil, ErrUnavailable
		}
	}))
	first, err := c.Registry(context.Background(), NodeRepository, Cache{})
	if err != nil || first.Revision != strings.Repeat("a", 40) || requests != 3 {
		t.Fatal("metadata read failed", err, requests)
	}
	unchanged = true
	second, err := c.Registry(context.Background(), NodeRepository, first)
	if err != nil || second != first || requests != 5 {
		t.Fatal("conditional check downloaded config again", err, requests)
	}
}

func TestRegistryIndexSelectsOnlyRuntimeAndVerifiesConfigDigest(t *testing.T) {
	image, config := imageFixture(t)
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "manifests": []any{map[string]any{"digest": sum(image), "size": len(image), "platform": map[string]string{"os": "linux", "architecture": "amd64"}}, map[string]any{"digest": "sha256:" + strings.Repeat("0", 64), "size": 12, "platform": map[string]string{"os": "unknown", "architecture": "unknown"}}}})
	bad := false
	c := New(doer(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			return response(200, []byte(`{"token":"read-token"}`), nil), nil
		}
		if strings.HasSuffix(r.URL.Path, "/manifests/latest") {
			return response(200, index, nil), nil
		}
		if strings.HasSuffix(r.URL.Path, "/manifests/"+sum(image)) {
			return response(200, image, nil), nil
		}
		if strings.HasSuffix(r.URL.Path, "/blobs/"+sum(config)) {
			if bad {
				return response(200, append([]byte{}, append(config[:len(config)-1], ' ')...), nil), nil
			}
			return response(200, config, nil), nil
		}
		t.Fatal("attestation or layer fetched")
		return nil, ErrUnavailable
	}))
	if _, err := c.Registry(context.Background(), WebRepository, Cache{}); err != nil {
		t.Fatal(err)
	}
	bad = true
	if _, err := c.Registry(context.Background(), WebRepository, Cache{}); err == nil {
		t.Fatal("altered metadata passed its immutable digest")
	}
}

func TestRegistryNeverForwardsTokenToCDNOrFollowsArbitraryRedirect(t *testing.T) {
	image, config := imageFixture(t)
	location := "https://pkg-containers.githubusercontent.com/metadata?signed=public-read"
	cdnCalls := 0
	c := New(doer(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "pkg-containers.githubusercontent.com" {
			cdnCalls++
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Fatal("registry bearer or cookie leaked to CDN")
			}
			return response(200, config, nil), nil
		}
		if r.URL.Host != "ghcr.io" {
			t.Fatal("arbitrary redirect followed")
		}
		if r.URL.Path == "/token" {
			return response(200, []byte(`{"token":"read-token"}`), nil), nil
		}
		if strings.HasSuffix(r.URL.Path, "/manifests/latest") {
			return response(200, image, nil), nil
		}
		return response(307, nil, http.Header{"Location": []string{location}}), nil
	}))
	if _, err := c.Registry(context.Background(), NodeRepository, Cache{}); err != nil || cdnCalls != 1 {
		t.Fatal("allowed bounded CDN metadata failed", err)
	}
	for _, bad := range []string{"http://pkg-containers.githubusercontent.com/config", "https://pkg-containers.githubusercontent.com.evil.invalid/config", "https://127.0.0.1/config", "https://user@pkg-containers.githubusercontent.com/config", "https://pkg-containers.githubusercontent.com:443/config"} {
		location = bad
		if _, err := c.Registry(context.Background(), NodeRepository, Cache{}); err == nil {
			t.Fatal("unsafe redirect accepted")
		}
	}
	if cdnCalls != 1 {
		t.Fatal("refused redirect still made another request")
	}
}

func TestComparisonCannotRecommendDowngradeOrQueryAnArbitraryRepository(t *testing.T) {
	state := "ahead"
	calls := 0
	c := New(doer(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.github.com" || r.URL.Query().Get("page") != "2" || r.Header.Get("Authorization") != "" {
			t.Fatal("comparison origin/pagination/credential boundary changed")
		}
		body, _ := json.Marshal(map[string]string{"status": state})
		return response(200, body, nil), nil
	}))
	current, latest := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, test := range []struct{ state, want string }{{"ahead", "update-available"}, {"behind", "ahead"}, {"identical", "current"}, {"diverged", "unknown"}} {
		state = test.state
		got, err := c.Relation(context.Background(), current, latest)
		if err != nil || got != test.want {
			t.Fatal("comparison result changed", got, err)
		}
	}
	if got, err := c.Relation(context.Background(), current, current); err != nil || got != "current" {
		t.Fatal(err)
	}
	before := calls
	if _, err := c.Relation(context.Background(), "../../private", latest); err == nil {
		t.Fatal("non-SHA comparison accepted")
	}
	if _, err := c.Registry(context.Background(), "other/private", Cache{}); err == nil {
		t.Fatal("nonofficial package requested")
	}
	if calls != before {
		t.Fatal("refused input reached the network")
	}
}

func TestMetadataSizeAndRateLimitsStayBounded(t *testing.T) {
	mode := "large"
	c := New(doer(func(*http.Request) (*http.Response, error) {
		if mode == "large" {
			r := response(200, nil, nil)
			r.ContentLength = 2 * 1024 * 1024
			return r, nil
		}
		return response(429, []byte("private-remote-error-text"), http.Header{"Retry-After": []string{"9000000"}}), nil
	}))
	if _, err := c.Registry(context.Background(), NodeRepository, Cache{}); err == nil {
		t.Fatal("oversized token metadata accepted")
	}
	mode = "throttle"
	_, err := c.Registry(context.Background(), NodeRepository, Cache{})
	var throttle *Throttled
	if !errors.As(err, &throttle) || throttle.After != 24*time.Hour || strings.Contains(err.Error(), "private-remote") {
		t.Fatal("rate limit or fixed error boundary changed")
	}
}
