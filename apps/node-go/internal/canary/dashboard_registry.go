package canary

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

type RegisteredNode struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	NodeID       string `json:"nodeId"`
	NodeName     string `json:"nodeName"`
	URL          string `json:"url,omitempty"`
	Local        bool   `json:"local"`
	State        string `json:"state"`
	LastReadAt   int64  `json:"lastReadAt,omitempty"`
	MonitorCount *int   `json:"monitorCount,omitempty"`
	Error        string `json:"error,omitempty"`
}

type registeredNodeRecord struct {
	RegisteredNode
	// Authenticated against ID, URL and proved node identity; never public JSON.
	Credentials string `json:"credentials"`
	// Distinguish an undiscovered placeholder from a real node named pending.
	Unproved bool `json:"unproved,omitempty"`
}

type dashboardNodeSecrets struct {
	dashboardRoleTokens
	// Bootstrap while enrolling, or the previous/recovery administrator
	// credential while rotating. Cleared after role verification succeeds.
	EnrollmentToken string `json:"enrollmentToken,omitempty"`
}

func dashboardOperationPending(state string) bool {
	return state == "enrolling" || state == "removing" || state == "rotating"
}

type nodeVerification struct {
	Ticket     string   `json:"ticket"`
	ExpiresAt  int64    `json:"expiresAt"`
	Node       Identity `json:"node"`
	Label      string   `json:"label"`
	URL        string   `json:"url"`
	PlainHTTP  bool     `json:"plainHttp"`
	actorID    string
	bearerHash string
	token      string
}

type DashboardOptions struct {
	Accounts *DashboardAccounts
	// Retain outside database backups. A missing/wrong key is a startup error.
	Key            []byte
	CORSOrigins    []string
	LocalNodeURL   string
	LocalNodeToken string
}

type Dashboard struct {
	accounts     *DashboardAccounts
	mu           sync.Mutex
	registry     map[string]registeredNodeRecord
	tickets      map[string]nodeVerification
	busy         map[string]bool
	box          cipher.AEAD
	client       *http.Client
	transport    *http.Transport
	networkSlots chan struct{}
	origins      []string
	localURL     string
	localToken   string
	start        sync.Once
	done         chan struct{}
}

func NewDashboard(o DashboardOptions) (*Dashboard, error) {
	if o.Accounts == nil || len(o.Key) != 32 {
		return nil, errors.New("Dashboard accounts and a retained 32-byte key are required")
	}
	block, err := aes.NewCipher(o.Key)
	if err != nil {
		return nil, errors.New("Invalid dashboard key")
	}
	box, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxIdleConns: 8, MaxIdleConnsPerHost: 2,
		MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, MaxResponseHeaderBytes: 32 * 1024, DisableCompression: true}
	d := &Dashboard{accounts: o.Accounts, registry: map[string]registeredNodeRecord{}, tickets: map[string]nodeVerification{}, busy: map[string]bool{}, box: box,
		transport: transport, client: &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		networkSlots: make(chan struct{}, 8), origins: slices.Clone(o.CORSOrigins), done: make(chan struct{})}
	if o.LocalNodeURL != "" {
		d.localURL, err = dashboardNodeURL(o.LocalNodeURL)
		if err != nil || tokenValue(o.LocalNodeToken) != nil {
			return nil, errors.New("Local node auto-enrollment requires a valid private origin and admin token")
		}
		d.localToken = o.LocalNodeToken
	}
	n := d.accounts.node
	n.mu.Lock()
	keyHash, found, err := loadOne[string](n.store, "settings", "dashboardKeyFingerprint")
	if err == nil && found && keyHash != digest(string(o.Key)) {
		err = errors.New("Dashboard key does not match this database")
	}
	if err == nil && !found {
		err = n.identityTransactionLocked(func(e dbExec) error { return put(e, "settings", "dashboardKeyFingerprint", digest(string(o.Key))) })
	}
	if err != nil {
		n.mu.Unlock()
		return nil, errors.New("Dashboard key does not match this database or could not be recorded")
	}
	loaded, err := loadAll[registeredNodeRecord](n.store, "dashboardRegistry")
	n.mu.Unlock()
	if err != nil || len(loaded) > 64 {
		return nil, errors.New("Cannot load dashboard node registry")
	}
	for id, row := range loaded {
		if id != row.ID || row.NodeID == "" || !slices.Contains([]string{"enrolling", "removing", "rotating", "starting", "answering", "not-answering", "credential-refused", "different-node"}, row.State) {
			return nil, errors.New("Invalid dashboard registry record")
		}
		if row.Unproved && (!row.Local || row.NodeID != "pending" || (row.State != "enrolling" && row.State != "removing")) {
			return nil, errors.New("Invalid unproved local registration")
		}
		canonical, e := dashboardNodeURL(row.URL)
		if e != nil || canonical != row.URL {
			return nil, errors.New("Invalid saved dashboard node address")
		}
		if _, e = d.openSecrets(row); e != nil {
			return nil, errors.New("Cannot decrypt saved node credentials; restore the matching dashboard key")
		}
		if !dashboardOperationPending(row.State) {
			row.State = "starting"
			row.Error = ""
		}
		d.registry[id] = row
	}
	return d, nil
}

func (d *Dashboard) Close() { d.transport.CloseIdleConnections() }

func dashboardNodeURL(raw string) (string, error) {
	if len(raw) > 512 || strings.TrimSpace(raw) != raw {
		return "", fail(400, "Use a complete HTTP or HTTPS node origin")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || strings.ContainsAny(raw, "\\\r\n\t") {
		return "", fail(400, "Use a node origin without credentials, a path or query")
	}
	canonical, err := baseURL(raw, "node address")
	if err != nil {
		return "", err
	}
	u, _ = url.Parse(canonical)
	if u.Port() == "0" || u.Hostname() == "" || strings.Contains(u.Hostname(), "%") {
		return "", fail(400, "Invalid node address")
	}
	return canonical, nil
}

func registryAAD(row registeredNodeRecord) []byte {
	return []byte("canary-dashboard-v1\x00" + row.ID + "\x00" + row.NodeID + "\x00" + row.URL)
}

func (d *Dashboard) sealSecrets(row registeredNodeRecord, secret dashboardNodeSecrets) (string, error) {
	b, err := json.Marshal(secret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, d.box.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", fail(503, "Cryptographic randomness unavailable")
	}
	return base64.RawStdEncoding.EncodeToString(d.box.Seal(nonce, nonce, b, registryAAD(row))), nil
}

func (d *Dashboard) openSecrets(row registeredNodeRecord) (dashboardNodeSecrets, error) {
	var secret dashboardNodeSecrets
	b, err := base64.RawStdEncoding.DecodeString(row.Credentials)
	if err != nil || len(b) < d.box.NonceSize()+d.box.Overhead() {
		return secret, errors.New("Invalid encrypted node credentials")
	}
	b, err = d.box.Open(nil, b[:d.box.NonceSize()], b[d.box.NonceSize():], registryAAD(row))
	if err != nil || json.Unmarshal(b, &secret) != nil || secret.NodeID != row.NodeID {
		return secret, errors.New("Invalid encrypted node credentials")
	}
	for _, role := range dashboardRoles {
		if tokenValue(secret.token(role)) != nil {
			return secret, errors.New("Invalid role credential")
		}
	}
	return secret, nil
}

// Caller holds d.mu. Use the identity store transaction so failures fault the
// service and cannot publish a registry mutation that was not durably written.
func (d *Dashboard) saveRegistry(row registeredNodeRecord) error {
	return d.commitRegistry(row, nil, AccountView{})
}

func (d *Dashboard) saveAuthorizedRegistry(row registeredNodeRecord, r *http.Request, actor AccountView) error {
	return d.commitRegistry(row, r, actor)
}

func (d *Dashboard) commitRegistry(row registeredNodeRecord, r *http.Request, actor AccountView) error {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	if r != nil {
		current, err := n.authenticate(r)
		if err != nil {
			return err
		}
		if current.ID != actor.ID || current.Role != "admin" || actor.Role != "admin" {
			return fail(403, "Dashboard administrator permission is required")
		}
		if r.Context().Err() != nil {
			return fail(408, "Request cancelled")
		}
	}
	if err := n.identityTransactionLocked(func(e dbExec) error { return put(e, "dashboardRegistry", row.ID, row) }); err != nil {
		return err
	}
	d.registry[row.ID] = row
	return nil
}

func (d *Dashboard) publicNode(row registeredNodeRecord, role string) RegisteredNode {
	v := row.RegisteredNode
	// Local means managed by the current deployment, rather than an immutable
	// reason to hide removal after that deployment was disabled or moved.
	v.Local = row.Local && d.localURL != "" && row.URL == d.localURL
	if role != "admin" {
		v.URL = ""
	}
	return v
}

// Caller holds d.mu. Only use after remote revocation, or for an unproved
// placeholder which has never sent any role-enrollment mutation.
func (d *Dashboard) deleteRegistry(id string) error {
	n := d.accounts.node
	n.mu.Lock()
	err := n.identityTransactionLocked(func(e dbExec) error { return remove(e, "dashboardRegistry", id) })
	n.mu.Unlock()
	if err == nil {
		delete(d.registry, id)
	}
	return err
}

func (d *Dashboard) nodeRequest(ctx context.Context, row registeredNodeRecord, token, method, path string, body []byte) (*http.Response, []byte, error) {
	authorization := ""
	if token != "" {
		authorization = "Bearer " + token
	}
	return d.nodeCall(ctx, row, authorization, method, path, body)
}

func (d *Dashboard) nodeCall(ctx context.Context, row registeredNodeRecord, authorization, method, path string, body []byte) (*http.Response, []byte, error) {
	select {
	case d.networkSlots <- struct{}{}:
		defer func() { <-d.networkSlots }()
	case <-ctx.Done():
		return nil, nil, fail(504, "The dashboard node request timed out")
	}
	req, err := http.NewRequestWithContext(ctx, method, row.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fail(502, "Cannot reach this node from the dashboard server")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if row.NodeID != "" {
		req.Header.Set("X-Canary-Expected-Node", row.NodeID)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, nil, fail(502, "Cannot reach this node from the dashboard server; check its address, certificate and permissions")
	}
	defer resp.Body.Close()
	if row.NodeID != "" && resp.Header.Get("X-Canary-Node-Id") != row.NodeID {
		return nil, nil, fail(502, "Different node at this address")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(b) > 4*1024*1024 {
		return nil, nil, fail(502, "Node response exceeds the dashboard limit or could not be read")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, nil, fail(502, "The node redirected the dashboard; redirects are refused")
	}
	return resp, b, nil
}

func (d *Dashboard) nodeActor(ctx context.Context, row registeredNodeRecord, token, role string) error {
	resp, b, err := d.nodeRequest(ctx, row, token, "GET", "/api/auth/me", nil)
	if err != nil {
		return err
	}
	var a AccountView
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fail(502, "Node credential was refused")
	}
	if resp.StatusCode != 200 || json.Unmarshal(b, &a) != nil || a.Role != role {
		return fail(502, "Node credential has an unexpected role")
	}
	if row.ID != "" && a.ID != dashboardDelegateID(row.ID, role) {
		return fail(502, "Node delegation identity changed")
	}
	return nil
}

func (d *Dashboard) readNode(ctx context.Context, row registeredNodeRecord, token string) (Overview, error) {
	var out Overview
	resp, b, err := d.nodeRequest(ctx, row, token, "GET", "/api/overview", nil)
	if err != nil {
		return out, err
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return out, fail(502, "Node credential was refused")
	}
	if resp.StatusCode != 200 || json.Unmarshal(b, &out) != nil || identifier(out.Node.ID, "node identity") != nil || out.Node.ID == "" {
		return out, fail(502, "This address did not return an Uptime-Canary node overview")
	}
	if row.NodeID != "" && out.Node.ID != row.NodeID {
		return out, fail(502, "Different node at this address")
	}
	return out, nil
}

func dashboardErrorMessage(err error) string {
	var api *apiError
	if errors.As(err, &api) {
		return api.Message
	}
	return "The dashboard could not complete this node operation"
}
