package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"uptime-canary/apps/node-go/internal/nodetls"
)

func nativeTLSOptions(t *testing.T, id string) Options {
	t.Helper()
	directory := t.TempDir()
	return Options{NodeID: id, AdminToken: adminToken, ClusterToken: clusterToken, Host: "127.0.0.1", Port: Ptr(0), DBPath: filepath.Join(directory, "node.db"), IdentityDirectory: filepath.Join(directory, "identity"), TLSMode: "auto"}
}

func TestNativeTLSRetainsIdentityAndHealthAuthenticatesWithoutAdminToken(t *testing.T) {
	options := nativeTLSOptions(t, "tls-retained")
	n, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	first := n.TLSFingerprint()
	if len(first) != 64 || n.Start() != nil {
		t.Fatal("TLS node did not start")
	}
	address, _ := url.Parse(n.URL())
	values := map[string]string{"CANARY_NODE_ID": options.NodeID, "CANARY_IDENTITY_DIRECTORY": options.IdentityDirectory, "CANARY_PORT": address.Port(), "CANARY_TLS_MODE": "auto"}
	get := func(key string) string {
		if strings.Contains(key, "TOKEN") {
			t.Fatal("health read an administrator credential")
		}
		return values[key]
	}
	if err = HealthCheck(context.Background(), get); err != nil {
		t.Fatal(err)
	}
	info, err := TLSCertificateInfo(get)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(info)
	if bytes.Contains(raw, []byte("PRIVATE KEY")) || !bytes.Contains(raw, []byte(first)) {
		t.Fatal("certificate inspection exposed private material or omitted fingerprint")
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	next, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Stop()
	if next.TLSFingerprint() != first {
		t.Fatal("restart changed the trust anchor")
	}
}

func TestNativeTLSVerifyStoresTrustBeforeRoleEnrollmentAndRefusesWrongPin(t *testing.T) {
	n, err := New(nativeTLSOptions(t, "tls-registry"))
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	var calls atomic.Int32
	n.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); n.ServeHTTP(w, r) })
	if err = n.Start(); err != nil {
		t.Fatal(err)
	}
	d, owner, _ := dashboardRegistryFixture(t)
	input := map[string]string{"label": "Private TLS node", "url": n.URL(), "token": adminToken, "tlsFingerprint": strings.Repeat("0", 64)}
	failed := dashboardHTTP(d, "POST", "/api/dashboard/verify", owner, "", input)
	if failed.Code != 502 || calls.Load() != 0 {
		t.Fatal("incorrect certificate pin sent a credential or was accepted")
	}
	input["tlsFingerprint"] = n.TLSFingerprint()
	verified := dashboardHTTP(d, "POST", "/api/dashboard/verify", owner, "", input)
	var ticket nodeVerification
	if verified.Code != 200 || json.Unmarshal(verified.Body.Bytes(), &ticket) != nil || ticket.TLSFingerprint != n.TLSFingerprint() {
		t.Fatalf("TLS verification failed: %d", verified.Code)
	}
	added := dashboardHTTP(d, "POST", "/api/dashboard/nodes", owner, "", map[string]string{"ticket": ticket.Ticket})
	var row RegisteredNode
	if added.Code != 201 || json.Unmarshal(added.Body.Bytes(), &row) != nil || row.TLSFingerprint != n.TLSFingerprint() {
		t.Fatalf("pinned enrollment failed: %d", added.Code)
	}
	if dashboardHTTP(d, "GET", "/api/overview", owner, row.ID, nil).Code != 200 {
		t.Fatal("saved pin not used for later reads")
	}
	d.mu.Lock()
	saved := d.registry[row.ID]
	d.mu.Unlock()
	for _, role := range []string{"viewer", "editor"} {
		public := d.publicNode(saved, role)
		if public.TLSFingerprint != "" || public.URL != "" {
			t.Fatal("private trust configuration exposed to nonadmin")
		}
	}
	if response := dashboardHTTP(d, "PATCH", "/api/dashboard/nodes/"+row.ID, owner, "", map[string]string{"label": "Wrong trust", "tlsFingerprint": strings.Repeat("0", 64)}); response.Code != 502 {
		t.Fatal("pin replacement accepted an unverified server")
	}
	d.mu.Lock()
	unchanged := d.registry[row.ID]
	d.mu.Unlock()
	if unchanged.TLSFingerprint != saved.TLSFingerprint || unchanged.Credentials != saved.Credentials {
		t.Fatal("failed trust change mutated saved credentials")
	}
}

func TestNativeTLSLocalPlaceholderCanCorrectTrustButEstablishedTrustDoesNotDrift(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	d.localURL, d.localToken, d.localTLSFingerprint = "https://127.0.0.1:3100", adminToken, strings.Repeat("a", 64)
	d.ensureLocalNode(context.Background())
	var id string
	for key := range d.registry {
		id = key
	}
	first := d.registry[id]
	secret, err := d.openSecrets(first)
	if err != nil {
		t.Fatal(err)
	}
	d.localTLSFingerprint = strings.Repeat("b", 64)
	d.ensureLocalNode(context.Background())
	corrected := d.registry[id]
	after, err := d.openSecrets(corrected)
	if err != nil || corrected.TLSFingerprint != d.localTLSFingerprint || after != secret || corrected.ID != first.ID {
		t.Fatal("pending trust correction lost its journal or role bundle", err)
	}
	corrected.Unproved = false
	corrected.NodeID = "proved-local"
	after.NodeID = corrected.NodeID
	corrected.Credentials, err = d.sealSecrets(corrected, after)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.saveRegistry(corrected); err != nil {
		t.Fatal(err)
	}
	d.localTLSFingerprint = strings.Repeat("c", 64)
	d.ensureLocalNode(context.Background())
	if d.registry[id].TLSFingerprint != strings.Repeat("b", 64) {
		t.Fatal("deployment environment silently replaced established trust")
	}
}

func TestNativeTLSBundledMigrationRetainsRegistrationAndProvesAllRoles(t *testing.T) {
	options := nativeTLSOptions(t, "bundled-tls")
	options.TLSMode = "off"
	node, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.Start(); err != nil {
		t.Fatal(err)
	}
	d, owner, _ := dashboardRegistryFixture(t)
	oldURL := node.URL()
	row := enrollDashboardNode(t, d, owner, oldURL)
	before := d.registry[row.ID]
	before.Local = true
	if err = d.saveRegistry(before); err != nil {
		t.Fatal(err)
	}
	original, err := d.openSecrets(before)
	if err != nil {
		t.Fatal(err)
	}
	d.localURL, d.localToken = oldURL, adminToken
	node.Stop()
	options.TLSMode = "auto"
	secure, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer secure.Stop()
	if err = secure.Start(); err != nil {
		t.Fatal(err)
	}
	d.localURL, d.localTLSFingerprint = secure.URL(), secure.TLSFingerprint()
	path := "/api/dashboard/nodes/" + row.ID
	wrong := dashboardHTTP(d, "PATCH", path, owner, "", map[string]string{"label": row.Label, "url": secure.URL(), "tlsFingerprint": strings.Repeat("0", 64)})
	if wrong.Code != 502 || d.registry[row.ID].URL != oldURL {
		t.Fatal("wrong pin moved a bundled registration")
	}
	moved := dashboardHTTP(d, "PATCH", path, owner, "", map[string]string{"label": row.Label, "url": secure.URL(), "tlsFingerprint": secure.TLSFingerprint()})
	if moved.Code != 200 {
		t.Fatalf("explicit bundled TLS migration failed: %d %s", moved.Code, moved.Body.String())
	}
	d.ensureLocalNode(context.Background())
	current := d.registry[row.ID]
	retained, err := d.openSecrets(current)
	if err != nil || !current.Local || current.ID != row.ID || current.NodeID != row.NodeID || retained != original || len(secure.accounts) != 4 || len(d.registry) != 1 {
		t.Fatal("TLS migration replaced identity, delegation or registration", err)
	}
}

func TestNativeTLSLocalAliasAdoptionCarriesVerifiedTrustAtomically(t *testing.T) {
	for _, failure := range []string{"", "wrong-pin", "viewer-refused", "commit-failed"} {
		name := failure
		if name == "" {
			name = "adopted"
		}
		t.Run(name, func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			n := dashboardRemoteFixture(t, "pinned-local-alias")
			plain := httptest.NewServer(n)
			defer plain.Close()
			registered := enrollDashboardNode(t, d, owner, plain.URL)
			original := d.registry[registered.ID]
			secret, err := d.openSecrets(original)
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			var roles [3]atomic.Int32
			secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				for i, role := range dashboardRoles {
					if bearer(r) == secret.token(role) {
						roles[i].Add(1)
					}
				}
				if failure == "viewer-refused" && bearer(r) == secret.Viewer {
					w.Header().Set("X-Canary-Node-Id", n.ID)
					w.WriteHeader(401)
					return
				}
				n.ServeHTTP(w, r)
			}))
			defer secure.Close()
			pin := nodetls.CertificateFingerprint(secure.Certificate())
			configuredPin := pin
			if failure == "wrong-pin" {
				configuredPin = strings.Repeat("0", 64)
			}
			options := DashboardOptions{Accounts: d.accounts, Key: dashboardKeyFixture, LocalNodeURL: secure.URL, LocalNodeToken: adminToken, LocalNodeTLSFingerprint: configuredPin}
			d, err = NewDashboard(options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(d.Close)
			d.ensureLocalNode(context.Background())
			var pending registeredNodeRecord
			for _, row := range d.registry {
				if row.Unproved {
					pending = row
				}
			}
			if pending.ID == "" || pending.TLSFingerprint != configuredPin {
				t.Fatal("configured trust was not retained on the local placeholder")
			}
			if failure == "commit-failed" {
				if _, err = d.accounts.node.store.db.Exec(`CREATE TRIGGER tls_adoption_failure BEFORE DELETE ON records WHEN OLD.category='dashboardRegistry' BEGIN SELECT RAISE(ABORT, 'private fixture failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, err = d.finishEnrollment(context.Background(), pending.ID)
			if failure != "" {
				if err == nil {
					t.Fatal("unverified or uncommitted TLS alias was adopted")
				}
				persisted, readErr := loadAll[registeredNodeRecord](d.accounts.node.store, "dashboardRegistry")
				if readErr != nil || len(d.registry) != 2 || len(persisted) != 2 || d.registry[original.ID] != original || persisted[original.ID] != original || len(n.accounts) != 4 {
					t.Fatal("failed adoption changed established trust, identity or role credentials", readErr)
				}
				if failure == "wrong-pin" && requests.Load() != 0 {
					t.Fatal("incorrect pin sent an HTTP request or credential")
				}
				if failure == "commit-failed" {
					for i := range roles {
						if roles[i].Load() == 0 {
							t.Fatal("commit failure did not exercise every existing role")
						}
					}
				}
				return
			}
			if err != nil {
				t.Fatal("configured pinned alias could not adopt the existing registration", err)
			}
			adopted := onlyDashboardRecord(t, d)
			retained, err := d.openSecrets(adopted)
			if err != nil || adopted.ID != original.ID || adopted.NodeID != original.NodeID || adopted.URL != secure.URL || adopted.TLSFingerprint != pin || !adopted.Local || retained != secret || len(n.accounts) != 4 {
				t.Fatal("TLS alias adoption lost trust, identity or the existing delegation", err)
			}
			for i := range roles {
				if roles[i].Load() == 0 {
					t.Fatal("TLS alias was adopted without proving every existing role")
				}
			}
			reloaded, err := NewDashboard(options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(reloaded.Close)
			stored := onlyDashboardRecord(t, reloaded)
			restored, err := reloaded.openSecrets(stored)
			if err != nil || stored.TLSFingerprint != pin || restored != secret || dashboardHTTP(reloaded, "GET", "/api/overview", owner, stored.ID, nil).Code != 200 {
				t.Fatal("adopted pin and encrypted role bundle did not survive restart", err)
			}
		})
	}
}

func TestNativeTLSPeersReplicateWithExplicitCertificateTrust(t *testing.T) {
	optionsA, optionsB := nativeTLSOptions(t, "tls-a"), nativeTLSOptions(t, "tls-b")
	firstA, err := New(optionsA)
	if err != nil {
		t.Fatal(err)
	}
	pinA := firstA.TLSFingerprint()
	if err = firstA.Stop(); err != nil {
		t.Fatal(err)
	}
	firstB, err := New(optionsB)
	if err != nil {
		t.Fatal(err)
	}
	pinB := firstB.TLSFingerprint()
	if err = firstB.Stop(); err != nil {
		t.Fatal(err)
	}
	optionsA.Peers = []Peer{{ID: "tls-b", URL: "https://127.0.0.1:1", TLSFingerprint: pinB}}
	optionsB.Peers = []Peer{{ID: "tls-a", URL: "https://127.0.0.1:1", TLSFingerprint: pinA}}
	a, err := New(optionsA)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	b, err := New(optionsB)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	// Configure addresses before the first scheduled replication round. The
	// production peer configuration is immutable after startup.
	a.nextSync = a.now() + 3600000
	b.nextSync = b.now() + 3600000
	if err = a.Start(); err != nil {
		t.Fatal(err)
	}
	if err = b.Start(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.settings.Peers[0].URL = b.URL()
	a.mu.Unlock()
	b.mu.Lock()
	b.settings.Peers[0].URL = a.URL()
	b.mu.Unlock()
	in := input([]string{"tls-a", "tls-b"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	// The public API canonicalizes optional monitor fields before committing
	// the immutable revision. A raw writeMonitor(input(...)) bypasses that
	// admission step and creates a fixture with a different payload commitment.
	body, _ := json.Marshal(in)
	request, _ := http.NewRequest("POST", a.URL()+"/api/monitors", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := b.peerTLSClients[a.ID].Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var monitor Monitor
	decodeErr := json.NewDecoder(response.Body).Decode(&monitor)
	response.Body.Close()
	if response.StatusCode != 201 || decodeErr != nil || monitor.ID == "" {
		t.Fatal("HTTPS monitor creation failed", decodeErr)
	}
	b.exchangePage(Peer{ID: "tls-a", URL: a.URL(), TLSFingerprint: pinA})
	b.mu.Lock()
	copy := b.current(monitor.ID)
	b.mu.Unlock()
	if copy == nil || copy.Name != monitor.Name {
		t.Fatal("TLS peer exchange did not replicate configuration")
	}
	if _, err = nodetls.Client(pinA); err != nil {
		t.Fatal(err)
	}
}
