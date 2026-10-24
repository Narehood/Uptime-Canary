package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

type identityDoer func(*http.Request) (*http.Response, error)

func (f identityDoer) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestBinaryIdentityVersionCLIRequiresNoEnvironmentNetworkOrNode(t *testing.T) {
	get := func(key string) string {
		t.Fatalf("version queried environment %s", key)
		return ""
	}
	client := identityDoer(func(*http.Request) (*http.Response, error) {
		t.Fatal("version attempted an HTTP request")
		return nil, nil
	})
	for _, command := range []string{"version", "--version"} {
		value, err := RunCLI(context.Background(), []string{command}, get, client)
		if err != nil {
			t.Fatal(err)
		}
		identity := value.(BinaryIdentity)
		if identity.Name != "Uptime-Canary" || identity.PeerProtocol != peerProtocol ||
			identity.OS != runtime.GOOS || identity.Arch != runtime.GOARCH || identity.GoVersion == "" || identity.Version == "" {
			t.Fatalf("missing binary identity: %+v", identity)
		}
		if _, err := RunCLI(context.Background(), []string{command, "unused-fixture.db"}, get, client); err == nil {
			t.Fatal("version accepted an unexpected argument")
		}
	}
	// Nil adapters are also valid: the command cannot accidentally fall back to
	// account authentication or the default HTTP client.
	if _, err := RunCLI(context.Background(), []string{"version"}, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestBinaryIdentityDistinguishesReleaseLabelModuleVersionAndDirtySource(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, modified := range []string{"false", "true"} {
		info := &debug.BuildInfo{GoVersion: "go1.27.3", Main: debug.Module{Path: "uptime-canary", Version: "v0.0.0-20261023211217-aaaaaaaaaaaa"},
			Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: commit}, {Key: "vcs.modified", Value: modified}}}
		result := binaryIdentity(info, true, "0.1.0-rc.1")
		if result.Version != "0.1.0-rc.1" || result.ModuleVersion != info.Main.Version ||
			result.SourceCommit != commit || result.Modified == nil || *result.Modified != (modified == "true") {
			t.Fatalf("release or source identity lost: %+v", result)
		}
		if binaryIdentity(info, true, "").Version != info.Main.Version {
			t.Fatal("an unstamped build must report the actual Go module version")
		}
	}
}

func TestBinaryIdentityUnknownOrMalformedProvenanceNeverClaimsCleanSource(t *testing.T) {
	settings := []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: strings.Repeat("a", 40)}, {Key: "vcs.modified", Value: "false"}}
	cases := []*debug.BuildInfo{nil, {}, {Settings: settings[:2]}}
	for _, invalid := range []debug.BuildSetting{{Key: "vcs", Value: "other"}, {Key: "vcs.revision", Value: "not-a-commit"},
		{Key: "vcs.revision", Value: strings.Repeat("A", 40)}, {Key: "vcs.modified", Value: "0"}} {
		copy := append([]debug.BuildSetting(nil), settings...)
		for index := range copy {
			if copy[index].Key == invalid.Key {
				copy[index] = invalid
			}
		}
		cases = append(cases, &debug.BuildInfo{Settings: copy})
	}
	cases = append(cases, &debug.BuildInfo{Settings: append(append([]debug.BuildSetting(nil), settings...), settings[2])})
	for _, info := range cases {
		result := binaryIdentity(info, true, "")
		if result.SourceCommit != "" || result.Modified != nil || result.Version != "unknown" {
			t.Fatalf("incomplete provenance presented as authoritative: %+v", result)
		}
		raw, err := json.Marshal(result)
		if err != nil || !strings.Contains(string(raw), `"modified":null`) {
			t.Fatalf("unknown modification state must remain explicit in JSON: %s %v", raw, err)
		}
	}
	if result := binaryIdentity(&debug.BuildInfo{Main: debug.Module{Version: "untrusted-unused"}}, false, "0.1.0"); result.ModuleVersion != "" || result.Version != "0.1.0" {
		t.Fatal("unavailable metadata was used")
	}
}
