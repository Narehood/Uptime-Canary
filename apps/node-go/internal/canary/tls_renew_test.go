package canary

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/nodetls"
	"uptime-canary/apps/node-go/internal/origin"
)

func TestNativeTLSInternalDashboardAccountsDoNotInheritConnectionListenerTLS(t *testing.T) {
	accounts, options := dashboardAccountFixture(t)
	dir := filepath.Join(options.Directory, "auth-origin")
	if _, _, err := accounts.node.retainedTLS(); err != nil {
		t.Fatal(err)
	}
	accounts.Close()
	r, err := origin.Open(origin.Options{Directory: dir, NodeID: "dashboard-accounts", RequireExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	old, err := r.TLSState()
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := nodetls.Generate("dashboard-accounts", time.Now().AddDate(-11, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(retainedTLSIdentity{1, "dashboard-accounts", string(cert), string(key)})
	if err = r.ReplaceTLSState(old, raw); err != nil {
		t.Fatal(err)
	}
	r.Close()
	reopened, err := NewDashboardAccounts(options)
	if err != nil {
		t.Fatal("disabled optional connection TLS blocked dashboard accounts", err)
	}
	defer reopened.Close()
	if reopened.node.TLSFingerprint() != "" || reopened.node.server.TLSConfig != nil {
		t.Fatal("internal account runtime inherited node listener TLS")
	}
	if response := dashboardAccountRequest(reopened, "GET", "/api/auth/status", "", nil); response.Code != 200 {
		t.Fatal("account status unavailable", response.Code)
	}
	if _, _, err = reopened.node.retainedTLS(); err == nil {
		t.Fatal("explicitly enabling the expired native listener did not fail closed")
	}
}

func TestNativeTLSExplicitRenewalKeepsNodeIdentityAndRequiresStoppedService(t *testing.T) {
	o := nativeTLSOptions(t, "renewable")
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	first := n.TLSFingerprint()
	values := map[string]string{"CANARY_NODE_ID": o.NodeID, "CANARY_IDENTITY_DIRECTORY": o.IdentityDirectory}
	get := func(k string) string { return values[k] }
	if _, err = RenewTLS(get, first); err == nil {
		t.Fatal("running service identity replaced")
	}
	n.Stop()
	if _, err = RenewTLS(get, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong current fingerprint accepted")
	}
	if _, err = RenewTLS(get, first); err != nil {
		t.Fatal(err)
	}
	if _, err = RenewTLS(get, first); err == nil {
		t.Fatal("uncertain renewal replayed")
	}
	next, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	next.Stop()
	if next.ID != o.NodeID || next.TLSFingerprint() == first {
		t.Fatal("renewal changed node identity or kept old certificate")
	}
	// A stale SQLite marker must not roll the private identity back after renewal.
	reopened, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if reopened.TLSFingerprint() != next.TLSFingerprint() {
		t.Fatal("restart changed renewed identity")
	}
}

func TestNativeTLSExpiredIdentityCanBeInspectedAndRenewedExplicitly(t *testing.T) {
	o := nativeTLSOptions(t, "expired")
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	n.Stop()
	r, err := origin.Open(origin.Options{Directory: o.IdentityDirectory, NodeID: o.NodeID, RequireExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := r.TLSState()
	cert, key, err := nodetls.Generate(o.NodeID, time.Now().AddDate(-11, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(retainedTLSIdentity{1, o.NodeID, string(cert), string(key)})
	if err = r.ReplaceTLSState(old, raw); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if bad, err := New(o); err == nil {
		bad.Stop()
		t.Fatal("expired certificate started")
	}
	get := func(k string) string {
		if k == "CANARY_NODE_ID" {
			return o.NodeID
		}
		if k == "CANARY_IDENTITY_DIRECTORY" {
			return o.IdentityDirectory
		}
		return ""
	}
	info, err := TLSCertificateInfo(get)
	if err != nil || info.(map[string]any)["validNow"] != false {
		t.Fatal("expired certificate cannot be inspected", err)
	}
	if _, err = RenewTLS(get, info.(map[string]any)["fingerprint"].(string)); err != nil {
		t.Fatal(err)
	}
	good, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	good.Stop()
}

func TestNativeTLSOutboundRetrustRetainsDeviceAndRegistration(t *testing.T) {
	o := nativeTLSOptions(t, "retrust")
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	n.Stop()
	r, err := origin.Open(origin.Options{Directory: o.IdentityDirectory, NodeID: o.NodeID, RequireExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	state := nodeConnectionIdentity{Version: 1, NodeID: o.NodeID, Origin: "https://dashboard.test", TLSFingerprint: strings.Repeat("a", 64), RegistrationID: strings.Repeat("b", 32), Secret: strings.Repeat("c", 64)}
	raw, _ := json.Marshal(state)
	if err = r.SaveConnectionState(nil, raw); err != nil {
		t.Fatal(err)
	}
	r.Close()
	get := func(k string) string {
		if k == "CANARY_NODE_ID" {
			return o.NodeID
		}
		if k == "CANARY_IDENTITY_DIRECTORY" {
			return o.IdentityDirectory
		}
		return ""
	}
	if _, err = TrustDashboard(get, strings.Repeat("0", 64), strings.Repeat("d", 64)); err == nil {
		t.Fatal("stale trust replaced")
	}
	if _, err = TrustDashboard(get, state.TLSFingerprint, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	r, err = origin.Open(origin.Options{Directory: o.IdentityDirectory, NodeID: o.NodeID, RequireExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	next, _ := r.ConnectionState()
	var actual nodeConnectionIdentity
	if json.Unmarshal(next, &actual) != nil {
		t.Fatal("unreadable connection")
	}
	actual.TLSFingerprint = state.TLSFingerprint
	check, _ := json.Marshal(actual)
	if !bytes.Equal(check, raw) {
		t.Fatal("retrust changed device identity, origin or registration")
	}
}
