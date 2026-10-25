package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"uptime-canary/apps/node-go/internal/jsonshape"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

type nodeConnectionIdentity struct {
	Version        int    `json:"version"`
	NodeID         string `json:"nodeId"`
	Origin         string `json:"origin"`
	RegistrationID string `json:"registrationId,omitempty"`
	Secret         string `json:"secret"`
}

type nodeConnector struct {
	identity          nodeConnectionIdentity
	fingerprint       string // Immutable after construction; safe for startup logging.
	saved             []byte
	invitation, admin string
	client            *http.Client
	transport         *http.Transport
}

func connectionDecode(raw []byte, value any) error {
	if jsonshape.Validate(raw) != nil {
		return nodeconnection.ErrMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return nodeconnection.ErrMessage
	}
	return nil
}

func (n *Node) prepareConnector(o Options) error {
	if o.DashboardURL == "" {
		if o.DashboardEnrollToken != "" {
			return errors.New("CANARY_DASHBOARD_URL is required for a node invitation")
		}
		return nil
	}
	origin, err := dashboardConnectionOrigin(o.DashboardURL)
	if err != nil {
		return err
	}
	if n.store.origin == nil {
		return errors.New("Node connections require a retained identity directory")
	}
	c := &nodeConnector{identity: nodeConnectionIdentity{Version: 1, NodeID: n.ID, Origin: origin}}
	c.saved, err = n.store.origin.ConnectionState()
	if err != nil {
		return err
	}
	if c.saved != nil {
		if connectionDecode(c.saved, &c.identity) != nil || c.identity.Version != 1 || c.identity.NodeID != n.ID || c.identity.Origin != origin || !nodeconnection.Identifier(c.identity.Secret, 64) || c.identity.RegistrationID != "" && !nodeconnection.Identifier(c.identity.RegistrationID, 32) {
			return errors.New("Retained node connection identity does not match this node and dashboard")
		}
	} else {
		c.identity.Secret, err = nodeconnection.RandomKey()
		if err != nil {
			return err
		}
	}
	parts := strings.Split(o.DashboardEnrollToken, ".")
	if o.DashboardEnrollToken != "" && (len(parts) != 2 || !nodeconnection.Identifier(parts[0], 32) || !nodeconnection.Identifier(parts[1], 64)) {
		return errors.New("Invalid CANARY_DASHBOARD_ENROLL_TOKEN")
	}
	if c.identity.RegistrationID == "" || o.DashboardEnrollToken != "" && parts[0] != c.identity.RegistrationID {
		if o.DashboardEnrollToken == "" {
			return errors.New("A new node connection requires CANARY_DASHBOARD_ENROLL_TOKEN")
		}
		// Never hand a remote dashboard an unproved administrator credential.
		request, _ := http.NewRequest("GET", "http://node.invalid/api/auth/me", nil)
		request.Header.Set("Authorization", "Bearer "+o.AdminToken)
		actor, authErr := n.authenticate(request)
		if authErr != nil || actor.Role != "admin" {
			return errors.New("The node invitation requires a current local administrator token")
		}
		c.invitation, c.admin = o.DashboardEnrollToken, o.AdminToken
	}
	if c.saved == nil {
		data, _ := json.Marshal(c.identity)
		if err = n.store.origin.SaveConnectionState(nil, data); err != nil {
			return err
		}
		c.saved = data
	}
	c.transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 4,
		IdleConnTimeout: 30 * time.Second, MaxResponseHeaderBytes: 32 * 1024, DisableCompression: true}
	c.client = &http.Client{Transport: c.transport, Timeout: 22 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c.fingerprint = digest(c.identity.Secret)
	n.connector = c
	return nil
}

// ConnectionFingerprint contains no credential and is safe to compare with the
// wizard before the administrator approves the first pairing.
func (n *Node) ConnectionFingerprint() string {
	if n.connector == nil {
		return ""
	}
	return n.connector.fingerprint
}

func (c *nodeConnector) post(ctx context.Context, route, authorization string, body any, limit int) (int, []byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, nil, nodeconnection.ErrMessage
	}
	request, err := http.NewRequestWithContext(ctx, "POST", c.identity.Origin+route, bytes.NewReader(data))
	if err != nil {
		return 0, nil, nodeconnection.ErrMessage
	}
	request.Header.Set("Authorization", "Bearer "+authorization)
	request.Header.Set("Content-Type", "application/json")
	if route != "/connect/v1/enroll" {
		request.Header.Set("X-Canary-Registration", c.identity.RegistrationID)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return 0, nil, nodeconnection.ErrDisconnected
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, nil, nodeconnection.ErrSession
	}
	if response.StatusCode == 204 {
		return 204, nil, nil
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return response.StatusCode, nil, nodeconnection.ErrMessage
	}
	raw, err := readBounded(response.Body, int64(limit))
	if err != nil {
		return response.StatusCode, nil, nodeconnection.ErrMessage
	}
	return response.StatusCode, raw, nil
}

func (c *nodeConnector) claim(ctx context.Context, n *Node) error {
	info := LocalBinaryIdentity()
	_, raw, err := c.post(ctx, "/connect/v1/enroll", c.invitation, map[string]any{"nodeId": n.ID, "name": n.Name, "version": info.Version, "sourceCommit": info.SourceCommit, "connectionToken": c.identity.Secret, "adminToken": c.admin}, 4096)
	if err != nil {
		return err
	}
	var result struct {
		RegistrationID, NodeID, State string
		ConnectionProtocol            int
	}
	if connectionDecode(raw, &result) != nil || result.ConnectionProtocol != 1 || result.NodeID != n.ID || result.RegistrationID != strings.Split(c.invitation, ".")[0] || result.State != "awaiting-confirmation" {
		return nodeconnection.ErrMessage
	}
	next := c.identity
	next.RegistrationID = result.RegistrationID
	data, _ := json.Marshal(next)
	if err = n.store.origin.SaveConnectionState(c.saved, data); err != nil {
		return err
	}
	c.identity, c.saved, c.invitation, c.admin = next, data, "", ""
	return nil
}

func (c *nodeConnector) run(ctx context.Context, n *Node) {
	defer c.transport.CloseIdleConnections()
	delay := time.Second
	reported := false
	for ctx.Err() == nil {
		if c.invitation != "" {
			err := c.claim(ctx, n)
			if err != nil && !errors.Is(err, nodeconnection.ErrDisconnected) && !errors.Is(err, nodeconnection.ErrSession) && !errors.Is(err, nodeconnection.ErrMessage) {
				log.Print("Uptime-Canary node connection stopped after an identity persistence error; monitoring continues")
				return // Reopen retained state; publication may already be visible.
			}
			if err == nil {
				delay = time.Second
			}
		}
		if c.invitation == "" {
			if err := c.session(ctx, n); err == nil || ctx.Err() != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if !reported {
			log.Print("Uptime-Canary node connection unavailable; retrying with bounded backoff; monitoring continues")
			reported = true
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(30*time.Second, delay*2)
	}
}

func (c *nodeConnector) session(parent context.Context, n *Node) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	_, raw, err := c.post(ctx, "/connect/v1/open", c.identity.Secret, map[string]string{"registrationId": c.identity.RegistrationID, "nodeId": n.ID}, 4096)
	if err != nil {
		return err
	}
	var opened struct {
		Session, NodeID, State string
		ConnectionProtocol     int
	}
	if connectionDecode(raw, &opened) != nil || !nodeconnection.Identifier(opened.Session, 64) || opened.NodeID != n.ID || opened.ConnectionProtocol != 1 {
		return nodeconnection.ErrMessage
	}
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	slots := make(chan struct{}, nodeconnection.MaxPending)
	for ctx.Err() == nil {
		status, raw, err := c.post(ctx, "/connect/v1/poll", c.identity.Secret, map[string]string{"registrationId": c.identity.RegistrationID, "session": opened.Session}, 512*1024)
		if err != nil {
			return err
		}
		if status == 204 {
			continue
		}
		var frame nodeconnection.Request
		if connectionDecode(raw, &frame) != nil || !frame.Valid() || frame.NodeID != n.ID || !connectionNodePath(frame.Method, frame.Path, c.identity.RegistrationID) {
			return nodeconnection.ErrMessage
		}
		// Local admission/acknowledgement waits spend the received budget too.
		// Starting a fresh timeout after acquiring a slot could execute a write
		// after its caller has already exhausted that original budget.
		deadline := time.Now().Add(time.Duration(frame.TimeoutMs) * time.Millisecond)
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		workers.Add(1)
		go func(frame nodeconnection.Request, deadline time.Time) {
			defer workers.Done()
			defer func() { <-slots }()
			jobCtx, stop := context.WithDeadline(ctx, deadline)
			defer stop()
			result, err := n.connectionDispatch(jobCtx, c.identity.RegistrationID, frame)
			if err != nil {
				// Expiring this job is not a failure of the session or its sibling.
				if !errors.Is(err, context.DeadlineExceeded) {
					cancel()
				}
				return
			}
			resultCtx, finish := context.WithTimeout(ctx, 5*time.Second)
			defer finish()
			status, _, err := c.post(resultCtx, "/connect/v1/result", c.identity.Secret, struct {
				RegistrationID string `json:"registrationId"`
				Session        string `json:"session"`
				nodeconnection.Response
			}{c.identity.RegistrationID, opened.Session, result}, 4096)
			// Only 410 means this result's pending caller went away. An epoch
			// change remains 409; authentication/transport errors still reconnect.
			if err != nil && status != http.StatusGone {
				cancel()
			}
		}(frame, deadline)
	}
	return nodeconnection.ErrSession
}
