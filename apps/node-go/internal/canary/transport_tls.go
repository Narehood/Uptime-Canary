package canary

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"uptime-canary/apps/node-go/internal/nodetls"
)

type retainedTLSIdentity struct {
	Version     int    `json:"version"`
	NodeID      string `json:"nodeId"`
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

func (n *Node) retainedTLS() (*tls.Config, string, error) {
	if n.store.origin == nil {
		return nil, "", errors.New("Native TLS requires a retained identity directory")
	}
	raw, err := n.store.origin.TLSState()
	if err != nil {
		return nil, "", err
	}
	expected, found, err := loadOne[string](n.store, "settings", "tlsFingerprint")
	if err != nil {
		return nil, "", errors.New("Cannot load TLS identity fingerprint")
	}
	var identity retainedTLSIdentity
	if raw == nil {
		if found {
			return nil, "", errors.New("Retained TLS identity is missing; restore the original identity volume")
		}
		certificate, key, err := nodetls.Generate(n.ID, time.Now())
		if err != nil {
			return nil, "", err
		}
		identity = retainedTLSIdentity{1, n.ID, string(certificate), string(key)}
		raw, err = json.Marshal(identity)
		if err != nil {
			return nil, "", err
		}
		if err = n.store.origin.CreateTLSState(raw); err != nil {
			return nil, "", err
		}
	} else if connectionDecode(raw, &identity) != nil || identity.Version != 1 || identity.NodeID != n.ID {
		return nil, "", errors.New("Retained TLS identity does not match this node")
	}
	config, leaf, err := nodetls.Server([]byte(identity.Certificate), []byte(identity.Key), time.Now())
	if err != nil {
		return nil, "", errors.New("Retained TLS certificate is invalid or expired; explicit certificate replacement is required")
	}
	fingerprint := nodetls.CertificateFingerprint(leaf)
	// The private retained file is authoritative across explicit renewal and
	// SQLite restores. The DB marker detects loss, never rolls back a certificate.
	// Remote peers still require the exact independently configured fingerprint.
	if !found || expected != fingerprint {
		if err = put(n.store.db, "settings", "tlsFingerprint", fingerprint); err != nil {
			return nil, "", err
		}
	}
	return config, fingerprint, nil
}

func (n *Node) prepareTLS(mode string) error {
	if mode == "" {
		_, retained, err := loadOne[string](n.store, "settings", "tlsFingerprint")
		if err != nil {
			return errors.New("Cannot load retained TLS mode")
		}
		if retained {
			mode = "auto"
		}
		if !retained && n.store.origin != nil {
			state, err := n.store.origin.TLSState()
			if err != nil {
				return err
			}
			if state != nil {
				mode = "auto"
			}
		}
	}
	if mode == "" || mode == "off" {
		return nil
	}
	if mode != "auto" {
		return errors.New("CANARY_TLS_MODE must be auto or off")
	}
	for _, peer := range n.settings.Peers {
		if strings.HasPrefix(peer.URL, "http://") {
			return errors.New("Native TLS nodes require HTTPS peer addresses; migrate peer URLs and trust together")
		}
	}
	config, fingerprint, err := n.retainedTLS()
	if err != nil {
		return err
	}
	n.server.TLSConfig, n.tlsFingerprint = config, fingerprint
	return nil
}

// TLSFingerprint is a public trust anchor; it contains no private key material.
func (n *Node) TLSFingerprint() string { return n.tlsFingerprint }

func validateTLSOrigin(origin, pin string) (string, error) {
	normalized, err := nodetls.Fingerprint(pin)
	if err != nil {
		return "", errors.New("Use a complete 64-character SHA256 TLS certificate fingerprint")
	}
	if normalized != "" && !strings.HasPrefix(origin, "https://") {
		return "", errors.New("A TLS fingerprint requires an HTTPS address")
	}
	return normalized, nil
}

func pinnedHTTPClient(base *http.Client, transport *http.Transport, pin string) (*http.Client, error) {
	config, err := nodetls.Client(pin)
	if err != nil {
		return nil, err
	}
	copy := *base
	cloned := transport.Clone()
	cloned.TLSClientConfig = config
	copy.Transport = cloned
	return &copy, nil
}

// Each pool is scoped to a certificate pin; changing trust cannot reuse a TLS
// connection authenticated under another pin. The cache has a fixed ceiling.
func (d *Dashboard) tlsClient(pin string) (*http.Client, error) {
	if pin == "" {
		return d.client, nil
	}
	d.tlsMu.Lock()
	defer d.tlsMu.Unlock()
	if client := d.tlsClients[pin]; client != nil {
		return client, nil
	}
	client, err := pinnedHTTPClient(d.client, d.transport, pin)
	if err != nil {
		return nil, err
	}
	if len(d.tlsClients) >= 96 {
		for key, old := range d.tlsClients {
			old.CloseIdleConnections()
			delete(d.tlsClients, key)
			break
		}
	}
	d.tlsClients[pin] = client
	return client, nil
}
