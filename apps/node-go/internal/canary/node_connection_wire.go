package canary

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"uptime-canary/apps/node-go/internal/jsonshape"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

func connectionJSON(raw []byte, value any, fields ...string) error {
	if jsonshape.Validate(raw) != nil {
		return fail(400, "Invalid node connection request")
	}
	return dashboardJSON(raw, value, fields...)
}

func dashboardConnectionOrigin(raw string) (string, error) {
	value, err := dashboardNodeURL(raw)
	if err != nil || !strings.HasPrefix(value, "https://") || value != raw {
		return "", errors.New("Node connection address must be a canonical HTTPS origin")
	}
	return value, nil
}

// Only the application's existing management/public capabilities travel here.
// In particular the channel cannot call peer, login/session or another
// registration's delegation endpoint, and is never a general HTTP/TCP proxy.
func connectionNodePath(method, raw, registration string) bool {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || u.RawPath != "" || path.Clean(u.Path) != u.Path || strings.ContainsAny(raw, "\\\r\n\x00#") || len(raw) > 16384 {
		return false
	}
	p := u.Path
	if p == "/api/dashboard-enrollments/"+registration {
		return (method == "POST" || method == "PATCH" || method == "DELETE") && u.RawQuery == "" && !u.ForceQuery
	}
	if p == "/api/auth/status" || p == "/api/auth/me" || p == "/api/accounts" {
		return method == "GET" && u.RawQuery == "" && !u.ForceQuery
	}
	if strings.HasPrefix(p, "/api/fleet-placements/") {
		parts := strings.Split(strings.TrimPrefix(p, "/api/fleet-placements/"), "/")
		if u.RawQuery != "" || u.ForceQuery || !fleetPlacementID(parts[0]) {
			return false
		}
		return len(parts) == 1 && (method == "GET" || method == "PUT") || len(parts) == 2 && parts[1] == "validate" && method == "POST"
	}
	if dashboardMonitoringPath(p) {
		return true
	}
	if strings.HasPrefix(p, "/public/status/") || strings.HasPrefix(p, "/public/badge/") {
		return method == "GET" || method == "HEAD"
	}
	return (p == "/api/push" && method == "POST") || (strings.HasPrefix(p, "/api/push/") && method == "GET")
}

func (d *Dashboard) outboundNodeCall(ctx context.Context, row registeredNodeRecord, authorization, method, target string, body []byte) (*http.Response, []byte, error) {
	if !connectionNodePath(method, target, row.ID) || len(body) > nodeconnection.MaxRequestBody {
		return nil, nil, fail(400, "Request is not supported by the node connection")
	}
	d.mu.Lock()
	current, exists := d.registry[row.ID]
	allowed := outboundCallAllowed(current, row, exists, method, target)
	var slots chan struct{}
	if allowed {
		if d.nodeCallSlots == nil {
			d.nodeCallSlots = make(map[string]chan struct{})
		}
		slots = d.nodeCallSlots[row.ID]
		if slots == nil {
			slots = make(chan struct{}, nodeconnection.MaxPending)
			d.nodeCallSlots[row.ID] = slots
		}
	}
	d.mu.Unlock()
	if !allowed {
		return nil, nil, fail(409, "The node registration is unavailable or an operation is pending")
	}
	// The outer global admission already bounds all waiting callers to eight.
	// Serialize bursts at this node instead of rejecting normal catalogue loads;
	// time spent waiting consumes the same end-to-end twelve-second budget.
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return nil, nil, fail(504, "The node connection timed out while waiting for capacity")
	}
	d.mu.Lock()
	current, exists = d.registry[row.ID]
	allowed = outboundCallAllowed(current, row, exists, method, target)
	d.mu.Unlock()
	if !allowed {
		return nil, nil, fail(409, "The node registration is unavailable or an operation is pending")
	}
	frame := nodeconnection.Request{Method: method, Path: target, Authorization: authorization, NodeID: row.NodeID, Body: body}
	if len(body) > 0 {
		frame.ContentType = "application/json"
	}
	result, err := d.connections.Request(ctx, row.ID, frame)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, nil, fail(504, "The node connection timed out; a dispatched request may have completed")
		}
		if errors.Is(err, nodeconnection.ErrBusy) {
			return nil, nil, fail(503, "The node connection is busy")
		}
		return nil, nil, fail(502, "Node connection unavailable; a dispatched request may have completed")
	}
	if result.NodeID != row.NodeID {
		return nil, nil, fail(502, "Different node at this connection")
	}
	if result.Status >= 300 && result.Status < 400 {
		return nil, nil, fail(502, "The node redirected the dashboard; redirects are refused")
	}
	response := &http.Response{StatusCode: result.Status, Header: make(http.Header), Body: http.NoBody}
	response.Header.Set("Content-Type", result.ContentType)
	response.Header.Set("X-Canary-Node-Id", result.NodeID)
	return response, result.Body, nil
}

func outboundCallAllowed(current, row registeredNodeRecord, exists bool, method, target string) bool {
	if !exists || current.Connection != "outbound" || current.ConnectionHash != row.ConnectionHash || current.NodeID != row.NodeID {
		return false
	}
	if !dashboardOperationPending(current.State) {
		return true
	}
	expectedMethod := "POST"
	if current.State == "rotating" {
		expectedMethod = "PATCH"
	}
	if current.State == "removing" {
		expectedMethod = "DELETE"
	}
	return target == "/api/dashboard-enrollments/"+row.ID && method == expectedMethod || current.State != "removing" && method == "GET" && (target == "/api/auth/me" || target == "/api/accounts")
}

func (n *Node) connectionDispatch(ctx context.Context, registration string, frame nodeconnection.Request) (nodeconnection.Response, error) {
	if !frame.Valid() || frame.NodeID != n.ID || !connectionNodePath(frame.Method, frame.Path, registration) {
		return nodeconnection.Response{}, nodeconnection.ErrMessage
	}
	if ctx.Err() != nil {
		return nodeconnection.Response{}, ctx.Err()
	}
	request, err := http.NewRequestWithContext(ctx, frame.Method, "http://node.invalid"+frame.Path, bytes.NewReader(frame.Body))
	if err != nil {
		return nodeconnection.Response{}, nodeconnection.ErrMessage
	}
	request.Header.Set("Authorization", frame.Authorization)
	request.Header.Set("X-Canary-Expected-Node", frame.NodeID)
	if frame.ContentType != "" {
		request.Header.Set("Content-Type", frame.ContentType)
	}
	request.RemoteAddr = "127.0.0.1:0"
	w := nodeconnection.NewWriter()
	n.ServeHTTP(w, request)
	return w.Response(frame.ID)
}
