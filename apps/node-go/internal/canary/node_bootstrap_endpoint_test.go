package canary

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"uptime-canary/apps/node-go/internal/nodetls"
)

// A container can start its pinned listener before the user knows which host
// address a new remote node can reach. That address belongs to its invitation.
func TestNodeBootstrapPinnedListenerWithoutDefaultAddress(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	server := httptest.NewTLSServer(d.ConnectionHandler())
	t.Cleanup(server.Close)
	pin := nodetls.CertificateFingerprint(server.Certificate())
	options := DashboardOptions{Accounts: d.accounts, Key: dashboardKeyFixture, ConnectionEnabled: true, ConnectionTLSFingerprint: pin}
	ready, err := NewDashboard(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ready.Close)
	capabilities := dashboardHTTP(ready, "GET", "/api/dashboard/onboarding", owner, "", nil)
	var caps struct {
		OutboundEnabled          bool   `json:"outboundEnabled"`
		ConnectionURL            string `json:"connectionUrl"`
		ConnectionTLSFingerprint string `json:"connectionTlsFingerprint"`
	}
	if capabilities.Code != 200 || json.Unmarshal(capabilities.Body.Bytes(), &caps) != nil || !caps.OutboundEnabled || caps.ConnectionURL != "" || caps.ConnectionTLSFingerprint != pin {
		t.Fatal("ready listener without a default address lost its capability or trust")
	}
	for _, address := range []string{"", "http://192.0.2.10:18348", "https://192.0.2.10/path"} {
		input := map[string]string{"label": "Home DNS01", "nodeId": "home-dns01"}
		if address != "" {
			input["connectionUrl"] = address
		}
		if got := dashboardHTTP(ready, "POST", "/api/dashboard/invitations", owner, "", input); got.Code != 400 {
			t.Fatalf("incomplete/invalid address accepted: %q: %d", address, got.Code)
		}
	}
	invitation := connectionInvitation(t, ready, owner, "home-dns01", "https://192.0.2.10:18348")
	if invitation.ConnectionURL != "https://192.0.2.10:18348" || invitation.ConnectionTLSFingerprint != pin {
		t.Fatal("the complete invitation lost the entered address or native certificate pin")
	}
	options.ConnectionTLSFingerprint = ""
	if invalid, err := NewDashboard(options); err == nil {
		invalid.Close()
		t.Fatal("an unpinned listener without a configured address was accepted")
	}
}
