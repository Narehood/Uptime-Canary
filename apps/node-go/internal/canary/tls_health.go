package canary

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"uptime-canary/apps/node-go/internal/nodetls"
	"uptime-canary/apps/node-go/internal/origin"
)

// HealthCheck uses no management credential and never disables verification.
// Its identity read is local/read-only while the node holds the lifetime lock.
func HealthCheck(ctx context.Context, get func(string) string) error {
	port := get("CANARY_PORT")
	if port == "" {
		port = "3100"
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return errors.New("Invalid healthcheck port")
	}
	host := get("CANARY_HOST")
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	scheme := "http://"
	pin := ""
	mode := get("CANARY_TLS_MODE")
	if mode == "" && get("CANARY_NODE_ID") != "" && get("CANARY_IDENTITY_DIRECTORY") != "" {
		_, err := origin.ReadTLSState(get("CANARY_IDENTITY_DIRECTORY"), get("CANARY_NODE_ID"))
		if err == nil {
			mode = "auto"
		} else if !errors.Is(err, origin.ErrMissingState) && !errors.Is(err, os.ErrNotExist) {
			return errors.New("Cannot inspect retained healthcheck TLS mode")
		}
	}
	if mode != "" && mode != "off" {
		if mode != "auto" {
			return errors.New("Invalid healthcheck TLS mode")
		}
		directory := get("CANARY_IDENTITY_DIRECTORY")
		if directory == "" {
			database := get("CANARY_DB")
			if database == "" {
				database = ".canary/canary.db"
			}
			directory = filepath.Join(filepath.Dir(database), ".canary-identities")
		}
		raw, err := origin.ReadTLSState(directory, get("CANARY_NODE_ID"))
		if err != nil {
			return errors.New("Cannot read the healthcheck TLS identity")
		}
		var state retainedTLSIdentity
		if connectionDecode(raw, &state) != nil || state.Version != 1 || state.NodeID != get("CANARY_NODE_ID") {
			return errors.New("Invalid healthcheck TLS identity")
		}
		_, leaf, err := nodetls.Server([]byte(state.Certificate), []byte(state.Key), time.Now())
		if err != nil {
			return err
		}
		pin = nodetls.CertificateFingerprint(leaf)
		scheme = "https://"
	}
	config, err := nodetls.Client(pin)
	if err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: config, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, "GET", scheme+net.JoinHostPort(host, port)+"/readyz", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("Node healthcheck could not authenticate or reach the node")
	}
	defer response.Body.Close()
	var ready struct {
		Ready bool `json:"ready"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&ready) != nil || !ready.Ready {
		return errors.New("Node is not ready")
	}
	return nil
}

// TLSCertificateInfo reads the retained public trust anchor without starting a
// node, opening SQLite, taking another lifetime lock or printing private keys.
func TLSCertificateInfo(get func(string) string) (any, error) {
	directory := get("CANARY_IDENTITY_DIRECTORY")
	if directory == "" {
		return nil, errors.New("Set CANARY_IDENTITY_DIRECTORY and CANARY_NODE_ID")
	}
	raw, err := origin.ReadTLSState(directory, get("CANARY_NODE_ID"))
	if err != nil {
		return nil, err
	}
	var state retainedTLSIdentity
	if connectionDecode(raw, &state) != nil || state.Version != 1 || state.NodeID != get("CANARY_NODE_ID") {
		return nil, errors.New("Invalid retained TLS identity")
	}
	_, leaf, err := nodetls.Inspect([]byte(state.Certificate), []byte(state.Key))
	if err != nil {
		return nil, err
	}
	return map[string]any{"nodeId": state.NodeID, "fingerprint": nodetls.CertificateFingerprint(leaf), "certificatePem": state.Certificate, "notAfter": leaf.NotAfter, "validNow": !time.Now().Before(leaf.NotBefore) && time.Now().Before(leaf.NotAfter)}, nil
}

// InitializeTLS is an explicit offline preparation step. It keeps peer
// configuration, data and identity, but starts no listener, checks or alerts.
func InitializeTLS(get func(string) string) (any, error) {
	options, err := OptionsFromEnvironment(get)
	if err != nil {
		return nil, err
	}
	options.TLSMode = "off"
	options.DashboardURL, options.DashboardEnrollToken = "", ""
	node, err := New(options)
	if err != nil {
		return nil, err
	}
	defer node.Stop()
	_, pin, err := node.retainedTLS()
	if err != nil {
		return nil, err
	}
	return map[string]any{"nodeId": node.ID, "fingerprint": pin, "message": "No listener started. Keep CANARY_TLS_MODE=off until all peer URLs and fingerprints are ready, then enable auto together."}, nil
}
