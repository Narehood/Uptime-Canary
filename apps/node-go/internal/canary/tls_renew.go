package canary

import (
	"encoding/json"
	"errors"
	"time"
	"uptime-canary/apps/node-go/internal/nodetls"
	"uptime-canary/apps/node-go/internal/origin"
)

// Offline trust changes require the existing identity and its lifetime lock.
// They never open SQLite or start a listener, scheduler or notification worker.
func offlineTLSIdentity(get func(string) string) (*origin.Reservation, string, error) {
	directory, id := get("CANARY_IDENTITY_DIRECTORY"), get("CANARY_NODE_ID")
	if directory == "" || id == "" {
		return nil, "", errors.New("Set CANARY_IDENTITY_DIRECTORY and CANARY_NODE_ID; stop the service first")
	}
	r, err := origin.Open(origin.Options{Directory: directory, NodeID: id, RequireExisting: true})
	return r, id, err
}

// RenewTLS changes the server key only after the operator names its current
// public fingerprint. Repeating an uncertain operation with the old pin fails.
func RenewTLS(get func(string) string, expected string) (any, error) {
	pin, err := nodetls.Fingerprint(expected)
	if err != nil || pin == "" {
		return nil, errors.New("Name the complete current TLS certificate fingerprint")
	}
	r, id, err := offlineTLSIdentity(get)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	previous, err := r.TLSState()
	if err != nil {
		return nil, err
	}
	var state retainedTLSIdentity
	if previous == nil || connectionDecode(previous, &state) != nil || state.Version != 1 || state.NodeID != id {
		return nil, errors.New("A valid retained TLS identity is required")
	}
	_, leaf, err := nodetls.Inspect([]byte(state.Certificate), []byte(state.Key))
	if err != nil || nodetls.CertificateFingerprint(leaf) != pin {
		return nil, errors.New("Current TLS fingerprint does not match; inspect tls-info before trying again")
	}
	certificate, key, err := nodetls.Generate(id, time.Now())
	if err != nil {
		return nil, err
	}
	state.Certificate, state.Key = string(certificate), string(key)
	next, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if err = r.ReplaceTLSState(previous, next); err != nil {
		return nil, err
	}
	_, leaf, err = nodetls.Inspect(certificate, key)
	if err != nil {
		return nil, err
	}
	return map[string]any{"nodeId": id, "fingerprint": nodetls.CertificateFingerprint(leaf), "notAfter": leaf.NotAfter, "message": "Certificate renewed. Update each peer and dashboard trust fingerprint over a trusted channel before reconnecting. Node identity and data are unchanged."}, nil
}

// TrustDashboard explicitly changes an outbound connection's trust anchor,
// retaining its origin, device secret and registration. It does not discover or
// accept a certificate over the network. 'system' selects normal CA/host trust.
func TrustDashboard(get func(string) string, previousPin, nextPin string) (any, error) {
	parse := func(value string) (string, error) {
		if value == "system" {
			return "", nil
		}
		pin, err := nodetls.Fingerprint(value)
		if err == nil && pin == "" {
			err = nodetls.ErrCertificate
		}
		return pin, err
	}
	old, err := parse(previousPin)
	if err != nil {
		return nil, err
	}
	next, err := parse(nextPin)
	if err != nil {
		return nil, err
	}
	r, id, err := offlineTLSIdentity(get)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	raw, err := r.ConnectionState()
	if err != nil {
		return nil, err
	}
	var state nodeConnectionIdentity
	if raw == nil || connectionDecode(raw, &state) != nil || state.Version != 1 || state.NodeID != id || state.TLSFingerprint != old {
		return nil, errors.New("Current dashboard trust does not match the retained connection")
	}
	if _, err = dashboardConnectionOrigin(state.Origin); err != nil {
		return nil, err
	}
	state.TLSFingerprint = next
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if err = r.SaveConnectionState(raw, encoded); err != nil {
		return nil, err
	}
	return map[string]any{"nodeId": id, "tlsFingerprint": next, "message": "Dashboard trust updated explicitly. Set CANARY_DASHBOARD_TLS_FINGERPRINT to this fingerprint (unset for system trust) before restarting; retain the same connection URL."}, nil
}
