package canary

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"uptime-canary/apps/node-go/internal/heartbeat"
)

// Node serializes configuration, episode reconciliation, and persistence under
// mu. Network/probe I/O always runs outside that lock with a bounded context.
type Node struct {
	ID, Name                                       string
	mu                                             sync.Mutex
	store                                          *store
	settings                                       settings
	known                                          map[string]bool
	clusterToken, clusterHash, host                string
	port                                           int
	now                                            func() int64
	client, peerClient                             Doer
	probe                                          func(context.Context, Monitor) ProbeResult
	tickInterval, historyRetention, webhookTimeout time.Duration
	historyLimit                                   int
	heads                                          map[string][]Version
	groups                                         map[string][]GroupVersion
	resources                                      map[string][]ResourceVersion
	channelTargets                                 map[string]channelTargetCache
	channelTargetBytes                             int
	revisionEdges                                  map[string]RevisionEdge
	revisionClaims                                 map[string]RevisionClaim
	graph                                          *revisionGraph
	incidentFacts                                  map[string]IncidentFact
	evidenceLinks                                  map[string]EvidenceLink
	incidentSources                                map[string]Incident
	localIncidentDeliveries                        map[string]localIncidentDelivery
	pendingIncidents                               map[string]Incident
	causalReady                                    bool
	maintenancePlans                               atomic.Pointer[compiledMaintenance]
	maintenanceCompiler                            chan struct{}
	maintenanceWindows                             *maintenanceWindowCache
	publicAvailability                             publicAvailabilityCache
	samples                                        map[string]Sample
	probePolicies                                  map[string]localCheckPolicy
	pushEvents                                     map[string]heartbeat.Event
	pushAdmission                                  map[string]int64
	pushTokenIndex                                 map[string]string
	episodes                                       map[string]Incident
	receipts                                       map[string]Receipt
	intents                                        map[string]NotificationIntent
	intentIndex                                    map[string]map[string]bool
	accounts                                       map[string]account
	sessions                                       map[string]loginSession
	loginLimits                                    map[string]loginLimit
	passwordWorkers                                chan struct{}
	notificationWorkers                            chan struct{}
	lastNotificationTest                           int64
	pendingNotificationTests                       int
	attempts                                       map[string]attempt
	peerSeen                                       map[string]int64
	peerProgress                                   map[string]syncProgress
	nextProbe                                      map[string]int64
	probing                                        map[string]context.CancelFunc
	sending                                        map[string]bool
	sequence, nextSync                             int64
	syncing, started, stopped                      bool
	fault                                          error
	server                                         *http.Server
	url                                            string
	ctx                                            context.Context
	cancel                                         context.CancelFunc
	wg                                             sync.WaitGroup
	stopDone                                       chan struct{}
}

func digest(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func matches(hash, value string) bool {
	a, err := hex.DecodeString(hash)
	b := sha256.Sum256([]byte(value))
	return err == nil && subtle.ConstantTimeCompare(a, b[:]) == 1
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("Cryptographic randomness unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func option(v *int64, saved, def int64) int64 {
	if v != nil {
		return *v
	}
	if saved != 0 {
		return saved
	}
	return def
}
func New(o Options) (*Node, error) { return newNode(o, false) }

// The private restore path can inspect a quarantined database while retaining
// the independent origin state. Public startup never bypasses quarantine.
func newNode(o Options, restoring bool) (result *Node, err error) {
	if err = tokenValue(o.ClusterToken); err != nil {
		return nil, err
	}
	if o.AdminToken != "" {
		if err = tokenValue(o.AdminToken); err != nil {
			return nil, err
		}
		if o.AdminToken == o.ClusterToken {
			return nil, errors.New("Management and cluster tokens must differ")
		}
	}
	host := o.Host
	if host == "" {
		host = "127.0.0.1"
	}
	if net.ParseIP(host) == nil && host != "localhost" {
		return nil, errors.New("host must be an IP address or localhost")
	}
	port := 3100
	if o.Port != nil {
		port = *o.Port
	}
	if err = bounded(int64(port), "port", 0, 65535); err != nil {
		return nil, err
	}
	path := o.DBPath
	if path == "" {
		path = ".canary/canary.db"
	}
	if err = validText(path, "dbPath", 4096); err != nil {
		return nil, err
	}
	s, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	schema, _, schemaErr := loadOne[int](s, "settings", "causalSchema")
	if schemaErr != nil || schema < 0 || schema > peerProtocol {
		return nil, errors.New("Database coordination version is unsupported; use a compatible node build")
	}
	quarantined, _, loadErr := loadOne[bool](s, "settings", "restoreQuarantined")
	if loadErr != nil || quarantined && !restoring {
		return nil, errors.New("Restored database requires node identity re-enrollment before monitoring")
	}
	if restoring && !quarantined {
		return nil, errors.New("Database is not a quarantined restore")
	}
	identity, exists, err := loadOne[Identity](s, "settings", "identity")
	if err != nil {
		return nil, errors.New("Cannot load persisted identity")
	}
	if o.NodeID != "" && exists && o.NodeID != identity.ID {
		return nil, errors.New("Configured node ID differs from the persisted node ID")
	}
	if o.NodeID != "" {
		identity.ID = o.NodeID
	}
	if identity.ID == "" {
		identity.ID = newID()
	}
	if o.NodeName != "" {
		identity.Name = o.NodeName
	}
	if identity.Name == "" {
		identity.Name = identity.ID
	}
	if err = identifier(identity.ID, "nodeId"); err != nil {
		return nil, err
	}
	if err = validText(identity.Name, "nodeName", 128); err != nil {
		return nil, err
	}
	saved, savedExists, err := loadOne[settings](s, "settings", "runtime")
	if err != nil {
		return nil, errors.New("Cannot load runtime settings")
	}
	peers := o.Peers
	if peers == nil {
		peers = saved.Peers
	}
	if peers == nil {
		peers = []Peer{}
	}
	if len(peers) > 31 {
		return nil, errors.New("Configure at most 31 peers")
	}
	known := map[string]bool{identity.ID: true}
	validated := []Peer{}
	for _, p := range peers {
		if err = identifier(p.ID, "peer ID"); err != nil {
			return nil, err
		}
		if known[p.ID] {
			return nil, errors.New("Peer IDs must be unique and different from this node")
		}
		known[p.ID] = true
		if p.URL, err = baseURL(p.URL, "peer URL"); err != nil {
			return nil, err
		}
		if p.Name != "" {
			if err = validText(p.Name, "peer name", 128); err != nil {
				return nil, err
			}
		}
		validated = append(validated, p)
	}
	origins := o.CORSOrigins
	if origins == nil {
		origins = saved.CORSOrigins
	}
	if origins == nil {
		origins = []string{}
	}
	if len(origins) > 32 {
		return nil, errors.New("Configure at most 32 CORS origins")
	}
	for _, v := range origins {
		var parsed string
		parsed, err = baseURL(v, "CORS origin")
		if err != nil || parsed != v {
			return nil, errors.New("CORS origins must be exact HTTP or HTTPS origins")
		}
	}
	delay := option(o.NotificationDelayMs, saved.NotificationDelayMs, 1500)
	if savedExists && o.NotificationDelayMs == nil {
		delay = saved.NotificationDelayMs
	}
	cfg := settings{validated, origins, option(o.SyncIntervalMs, saved.SyncIntervalMs, 1000), delay, option(o.PeerTimeoutMs, saved.PeerTimeoutMs, 2000)}
	for _, v := range []struct {
		n         string
		v, lo, hi int64
	}{{"syncIntervalMs", cfg.SyncIntervalMs, 50, 60000}, {"notificationDelayMs", cfg.NotificationDelayMs, 0, 300000}, {"peerTimeoutMs", cfg.PeerTimeoutMs, 25, 30000}} {
		if err = bounded(v.v, v.n, v.lo, v.hi); err != nil {
			return nil, err
		}
	}
	n := &Node{ID: identity.ID, Name: identity.Name, store: s, settings: cfg, known: known, clusterToken: o.ClusterToken, clusterHash: digest(o.ClusterToken), host: host, port: port, now: o.Now, client: o.Client, peerClient: o.PeerClient, probe: o.Probe, tickInterval: o.TickInterval, historyRetention: o.HistoryRetention, historyLimit: o.HistoryLimit, webhookTimeout: o.WebhookTimeout, peerSeen: map[string]int64{}, nextProbe: map[string]int64{}, probing: map[string]context.CancelFunc{}, sending: map[string]bool{}, stopDone: make(chan struct{})}
	if n.now == nil {
		n.now = func() int64 { return time.Now().UnixMilli() }
	}
	if n.client == nil {
		n.client = newHTTPClient()
	}
	if n.peerClient == nil {
		n.peerClient = n.client
	}
	if n.probe == nil {
		n.probe = func(ctx context.Context, m Monitor) ProbeResult {
			if m.Type == "system-service" {
				return SystemServiceProbe(ctx, m)
			}
			if m.Type == "pm2" {
				return PM2Probe(ctx, m)
			}
			if m.Type == "steam" {
				return SteamProbe(ctx, m)
			}
			if m.Type == "globalping" {
				return GlobalpingProbe(ctx, m)
			}
			if isServiceType(m.Type) {
				return ServiceProbe(ctx, m)
			}
			if m.Type == "ping" {
				return PingProbe(ctx, m)
			}
			if m.Type == "manual" {
				return manualProbe(ctx, m)
			}
			if m.Type != "http" || m.HTTP != nil || m.TLS != nil {
				return ActiveProbe(ctx, m)
			}
			return HTTPProbe(ctx, m, n.client)
		}
	}
	if n.tickInterval == 0 {
		n.tickInterval = 50 * time.Millisecond
	}
	if n.historyRetention == 0 {
		n.historyRetention = 7 * 24 * time.Hour
	}
	if n.historyLimit == 0 {
		n.historyLimit = 1000
	}
	if n.webhookTimeout == 0 {
		n.webhookTimeout = 5 * time.Second
	}
	for _, v := range []struct {
		n         string
		v, lo, hi int64
	}{{"tickIntervalMs", n.tickInterval.Milliseconds(), 10, 1000}, {"historyLimit", int64(n.historyLimit), 1, 100000}, {"historyRetentionMs", n.historyRetention.Milliseconds(), 1000, 31536000000}, {"webhookTimeoutMs", n.webhookTimeout.Milliseconds(), 25, 120000}} {
		if err = bounded(v.v, v.n, v.lo, v.hi); err != nil {
			return nil, err
		}
	}
	if n.heads, err = loadAll[[]Version](s, "heads"); err != nil {
		return nil, err
	}
	if n.groups, err = loadAll[[]GroupVersion](s, "groupHeads"); err != nil {
		return nil, err
	}
	if n.resources, err = loadAll[[]ResourceVersion](s, "resourceHeads"); err != nil {
		return nil, err
	}
	if err = n.initializeMaintenancePlans(); err != nil {
		return nil, err
	}
	if n.samples, err = loadAll[Sample](s, "samples"); err != nil {
		return nil, err
	}
	if n.episodes, err = loadAll[Incident](s, "incidents"); err != nil {
		return nil, err
	}
	if n.receipts, err = loadAll[Receipt](s, "receipts"); err != nil {
		return nil, err
	}
	if n.intents, err = loadAll[NotificationIntent](s, "notificationIntents"); err != nil {
		return nil, err
	}
	n.intentIndex = map[string]map[string]bool{}
	for _, intent := range n.intents {
		n.indexIntent(intent)
	}
	if err = n.initializeCausalFacts(); err != nil {
		return nil, err
	}
	if err = n.loadPushEvents(); err != nil {
		return nil, err
	}
	if n.accounts, err = loadAll[account](s, "accounts"); err != nil {
		return nil, err
	}
	if n.sessions, err = loadAll[loginSession](s, "sessions"); err != nil {
		return nil, err
	}
	n.loginLimits = map[string]loginLimit{}
	n.passwordWorkers = make(chan struct{}, 2)
	n.notificationWorkers = make(chan struct{}, 8)
	if n.attempts, err = loadAll[attempt](s, "attempts"); err != nil {
		return nil, err
	}
	for id, a := range n.attempts {
		if a.Kind == "test" && (a.State == "queued" || a.State == "sending") {
			a.State, a.ErrorCode, a.NextAt, a.Held = "unknown", "interrupted", 0, false
			if err = put(s.db, "attempts", id, a); err != nil {
				return nil, err
			}
			n.attempts[id] = a
		}
	}
	if n.sequence, _, err = loadOne[int64](s, "settings", "sequence"); err != nil {
		return nil, err
	}
	if err = n.loadProbePolicies(); err != nil {
		return nil, err
	}
	if err = n.openOrigin(o.IdentityDirectory, restoring); err != nil {
		return nil, err
	}
	if _, exists := n.accounts["bootstrap-admin"]; o.AdminToken != "" && !exists && len(n.accounts) == 0 {
		a := account{AccountView: AccountView{ID: "bootstrap-admin", Name: "admin", Role: "admin"}, Hash: digest(o.AdminToken)}
		if err = put(s.db, "accounts", a.ID, a); err != nil {
			return nil, err
		}
		n.accounts[a.ID] = a
	}
	hasAdmin := false
	for _, a := range n.accounts {
		if a.Role == "admin" {
			hasAdmin = true
		}
		if matches(a.Hash, o.ClusterToken) {
			return nil, errors.New("Management and cluster tokens must differ")
		}
	}
	if !hasAdmin {
		return nil, errors.New("CANARY_ADMIN_TOKEN is required to bootstrap an administrator")
	}
	if err = put(s.db, "settings", "identity", identity); err != nil {
		return nil, err
	}
	if err = put(s.db, "settings", "runtime", cfg); err != nil {
		return nil, err
	}
	n.ctx, n.cancel = context.WithCancel(context.Background())
	n.peerProgress = map[string]syncProgress{}
	n.server = &http.Server{Handler: n, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 * 1024, BaseContext: func(net.Listener) context.Context { return n.ctx }}
	return n, nil
}
func (n *Node) Start() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped {
		return errors.New("Create a new node instance after stop")
	}
	if n.started {
		return nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(n.host, strconv.Itoa(n.port)))
	if err != nil {
		return errors.New("Listener failed to start")
	}
	host := n.host
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	n.url = "http://" + net.JoinHostPort(host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	n.started = true
	n.launchLocked(func() { _ = n.server.Serve(listener) })
	n.launchLocked(func() {
		n.tick()
		timer := time.NewTicker(n.tickInterval)
		defer timer.Stop()
		for {
			select {
			case <-n.ctx.Done():
				return
			case <-timer.C:
				n.tick()
			}
		}
	})
	return nil
}
func (n *Node) URL() string { n.mu.Lock(); defer n.mu.Unlock(); return n.url }
func (n *Node) Stop() error {
	n.mu.Lock()
	if n.stopped {
		done := n.stopDone
		n.mu.Unlock()
		<-done
		return nil
	}
	n.stopped = true
	n.cancel()
	for _, cancel := range n.probing {
		cancel()
	}
	n.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = n.server.Shutdown(ctx)
	_ = n.server.Close()
	n.wg.Wait()
	err := n.store.close()
	if c, ok := n.client.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
	if c, ok := n.peerClient.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
	close(n.stopDone)
	return err
}
func (n *Node) launchLocked(f func()) {
	if n.stopped || n.fault != nil {
		return
	}
	n.wg.Add(1)
	go func() { defer n.wg.Done(); f() }()
}
func (n *Node) dbPut(category, id string, value any) bool {
	if n.fault != nil {
		return false
	}
	if err := put(n.store.db, category, id, value); err != nil {
		n.fault = errors.New("Persistence failed")
		n.cancel()
		return false
	}
	return true
}
func (n *Node) current(id string) *Monitor {
	h := n.heads[id]
	if len(h) != 1 || h[0].Monitor.Deleted {
		return nil
	}
	if !n.monitorVersionReady(h[0]) {
		return nil
	}
	m := h[0].Monitor
	return &m
}
func (n *Node) currentGroup(id string) *Group {
	h := n.groups[id]
	if len(h) != 1 || h[0].Group.Deleted {
		return nil
	}
	if !n.groupVersionReady(h[0]) {
		return nil
	}
	g := h[0].Group
	return &g
}
func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (n *Node) writeMonitor(input MonitorInput, previous []Version, id string, deleted bool) (Monitor, error) {
	if id == "" {
		id = newID()
	}
	m := Monitor{MonitorInput: input, ID: id, Revision: newID(), UpdatedAt: n.now(), Deleted: deleted}
	v, edges, err := n.joinMonitorRevision(m, previous)
	if err != nil {
		return m, err
	}
	m = v.Monitor
	if err := n.applySnapshot(Snapshot{Versions: []Version{v}, RevisionEdges: edges}); err != nil {
		return m, fail(503, "Persistence unavailable")
	}
	if m.Type == "manual" {
		n.nextProbe[id] = 0
	}
	return m, nil
}
func (n *Node) writeGroup(input GroupInput, previous []GroupVersion, id string, deleted bool) (Group, error) {
	if id == "" {
		id = newID()
	}
	g := Group{input, id, newID(), n.now(), deleted}
	v, edges, err := n.joinGroupRevision(g, previous)
	if err != nil {
		return g, err
	}
	g = v.Group
	if err := n.applySnapshot(Snapshot{GroupVersions: []GroupVersion{v}, RevisionEdges: edges}); err != nil {
		return g, fail(503, "Persistence unavailable")
	}
	return g, nil
}
func (n *Node) fresh(m Monitor) []Sample {
	out := []Sample{}
	for _, id := range m.NodeIDs {
		if s, ok := n.samples[m.ID+"/"+id]; ok && s.Revision == m.Revision && s.CheckedAt >= n.now()-max(probeSpacing(m, s)*3, m.TimeoutMs*2, n.settings.SyncIntervalMs*3) {
			out = append(out, s)
		}
	}
	return out
}
func (n *Node) tick() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.fault != nil {
		return
	}
	now := n.now()
	for id := range n.heads {
		m := n.current(id)
		if m == nil || !m.Enabled || !slices.Contains(m.NodeIDs, n.ID) || n.probing[id] != nil || now < n.nextProbe[id] {
			continue
		}
		n.nextProbe[id] = now + m.IntervalMs
		ctx, cancel := context.WithTimeout(n.ctx, time.Duration(m.TimeoutMs)*time.Millisecond)
		n.probing[id] = cancel
		monitor := *m
		var pushResult ProbeResult
		var pushWitness string
		if monitor.Type == "push" {
			pushResult, pushWitness, _ = n.pushEvaluationAt(monitor, n.now())
		}
		n.launchLocked(func() {
			defer cancel()
			r := pushResult
			if monitor.Type != "push" {
				r = n.probe(ctx, monitor)
			}
			n.mu.Lock()
			defer n.mu.Unlock()
			defer delete(n.probing, monitor.ID)
			if n.stopped || n.fault != nil || ctx.Err() == context.Canceled {
				return
			}
			current := n.current(monitor.ID)
			if current == nil || current.Revision != monitor.Revision || !current.Enabled {
				return
			}
			sampleAt := int64(0)
			pushDeadline := int64(0)
			if monitor.Type == "push" {
				sampleAt = n.now()
				latest, witness, deadline := n.pushEvaluationAt(monitor, sampleAt)
				if witness != pushWitness {
					n.nextProbe[monitor.ID] = 0
					return
				}
				r, pushDeadline = latest, deadline
			}
			if r.State != "up" && r.State != "down" && r.State != "pending" && r.State != "unknown" {
				r.State = "down"
				r.Message = "Probe failed"
			}
			key := monitor.ID + "/" + n.ID
			seq, sequenceErr := n.nextProbeSequence()
			if sequenceErr != nil {
				n.fault = errors.New("Local sequence reservation failed")
				n.cancel()
				return
			}
			policy, r, policyResult, policyErr := n.applyProbePolicy(monitor, r, seq)
			if policyErr != nil {
				n.fault = errors.New("Local check policy failed")
				n.cancel()
				return
			}
			var downSince *int64
			if r.State == "down" {
				downSince = policy.DownSince
			}
			latency := r.LatencyMs
			if r.State == "unknown" {
				latency, r.LatencyAvailable = 0, Ptr(false)
			}
			if !finite(latency) {
				latency = 0
			}
			if monitor.Type != "push" {
				sampleAt = n.now()
			}
			sample := Sample{Observation: Observation{MonitorID: monitor.ID, NodeID: n.ID, State: r.State, RawState: string(policyResult.RawState), ConsecutiveFailures: policyResult.ConsecutiveFailures, CheckedAt: sampleAt, LatencyMs: min(3600000, max(0, latency)), LatencyAvailable: r.LatencyAvailable, Message: safeMessage(r.Message), StatusCode: r.StatusCode, Certificate: r.Certificate}, Sequence: seq, Revision: monitor.Revision, DownSince: downSince, CheckGeneration: monitor.CheckGeneration}
			// Persist this observation's original validity policy. Future edits
			// cannot extend this deadline or backfill missing observed time.
			validUntil, maintenance := n.localAvailabilityPolicy(monitor, sample)
			tx, err := n.store.db.Begin()
			if err == nil {
				err = put(tx, "samples", key, sample)
				if err == nil {
					err = put(tx, "probePolicies", monitor.ID, policy)
				}
				if err == nil {
					err = put(tx, "settings", "sequence", seq)
				}
				if err == nil {
					err = recordLocalAvailability(tx, monitor, sample, validUntil, maintenance)
				}
				if err == nil {
					err = appendHistory(tx, sample.Observation, n.historyLimit, n.now()-n.historyRetention.Milliseconds())
				}
				if err == nil {
					err = tx.Commit()
				} else {
					_ = tx.Rollback()
				}
			}
			if err != nil {
				n.fault = errors.New("Persistence failed")
				n.cancel()
				return
			}
			n.sequence = seq
			n.probePolicies[monitor.ID] = policy
			n.samples[key] = sample
			n.nextProbe[monitor.ID] = now + policyResult.NextIntervalMs
			if monitor.Type == "push" && pushDeadline > 0 {
				// A deadline crossed during commit is already due, not irrelevant.
				n.nextProbe[monitor.ID] = min(n.nextProbe[monitor.ID], pushDeadline)
			}
			// One sample changes one monitor. The scheduler tick still evaluates
			// every monitor for time, holds and cross-monitor dependencies.
			n.evaluateMonitor(monitor.ID)
		})
	}
	n.evaluate()
	if !n.syncing && now >= n.nextSync {
		n.startSyncLocked()
	}
}
func (n *Node) saveEpisode(i Incident) {
	if equalJSON(n.episodes[i.ID], i) && equalJSON(combineUncertainties(n.episodes[i.ID].uncertainties, nil), combineUncertainties(i.uncertainties, nil)) && !n.needsLocalIncidentDelivery(i) {
		return
	}
	updates := n.localIncidentUpdates(i)
	intents := []NotificationIntent{}
	unchanged := len(updates) > 0
	for _, update := range updates {
		stored, exists := n.incidentSources[update.ID]
		original, complete, err := hydrateIncident(stored, n.incidentFacts, n.evidenceLinks)
		// Interrupted evaluation operates on original sources. Their IDs may be
		// hidden by a merged projection, so compare against the retained source
		// rather than treating that projection mismatch as a new observation.
		original.EvidenceRoot = ""
		candidate := update
		candidate.EvidenceRoot = ""
		same := exists && err == nil && complete && equalJSON(original, candidate) &&
			equalJSON(combineUncertainties(original.uncertainties, nil), combineUncertainties(candidate.uncertainties, nil))
		unchanged = unchanged && same && !n.needsLocalIncidentDelivery(update)
		for _, target := range unique(update.DeliveryTargets) {
			if !n.indexedDeliveryIntent(update, target) {
				intents = append(intents, deliveryIntent(update.MonitorID, update.ID, target))
			}
		}
	}
	if unchanged && len(intents) == 0 {
		return
	}
	// This is a complete local projection. Its previous root commits the old
	// state, so stage the changed arrays as a new source update.
	if err := n.applySnapshot(Snapshot{Incidents: updates, Intents: intents}); err != nil {
		n.fault = errors.New("Persistence failed")
		n.cancel()
		return
	}
	n.nextSync = 0
}
func (n *Node) normalizeEpisodes() {
	// applySnapshot projects complete original sources atomically. Inferred
	// alias projections must not be fed back as new source facts on every tick.
}
func (n *Node) forEpisode(i Incident) []Receipt {
	out := []Receipt{}
	ids := episodeIDs(i)
	for _, r := range n.receipts {
		if r.MonitorID == i.MonitorID && slices.Contains(ids, r.IncidentID) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Receipt) int {
		if a.SentAt < b.SentAt {
			return -1
		}
		if a.SentAt > b.SentAt {
			return 1
		}
		return stringsCompare(a.ID, b.ID)
	})
	return out
}
func (n *Node) evaluate() {
	if n.stopped || n.fault != nil {
		return
	}
	n.normalizeEpisodes()
	n.retireSuperseded()
	for id := range n.heads {
		n.evaluateMonitor(id)
	}
}

// Called under mu after a sample commit and from the full scheduler sweep.
// Samples cannot change another monitor's configuration or incident scope;
// dependency and clock-driven changes retain the full tick evaluation.
func (n *Node) evaluateMonitor(id string) {
	if n.stopped || n.fault != nil {
		return
	}
	m := n.current(id)
	if m == nil || !m.Enabled {
		return
	}
	observations := n.fresh(*m)
	if n.interruptedHistory(*m) {
		n.evaluateInterrupted(*m, observations)
		if slices.Contains(m.NodeIDs, n.ID) && len(n.notificationTargets(*m)) > 0 {
			n.scheduleNotification(*m, observations)
		}
		return
	}
	closed := []Incident{}
	records := []Incident{}
	for _, i := range n.episodes {
		if i.MonitorID == id && i.Revision == checkGeneration(*m) {
			records = append(records, i)
			if i.Closed != nil {
				closed = append(closed, i)
			}
		}
	}
	downs := []Sample{}
	for _, s := range observations {
		valid := s.State == "down"
		for _, i := range closed {
			valid = valid && sampleAfterClosure(s, i)
		}
		if valid {
			downs = append(downs, s)
		}
	}
	active := activeEpisode(records, downs)
	if len(downs) > 0 {
		opening := active == nil
		if active == nil {
			episodeID := newID()
			opened := downs[0].CheckedAt
			pred := []string{}
			for _, s := range downs {
				opened = min(opened, s.CheckedAt)
			}
			for _, i := range closed {
				pred = append(pred, episodeIDs(i)...)
			}
			active = &Incident{ID: episodeID, Aliases: []string{episodeID}, MonitorID: id, MonitorName: m.Name, Revision: checkGeneration(*m), OpenedAt: opened, Runs: map[string][]int64{}, Predecessors: unique(pred)}
		}
		tracked := n.recordMaintenance(*m, *active, opening)
		active = &tracked
		targets := append([]string{}, active.DeliveryTargets...)
		for _, target := range n.notificationTargets(*m) {
			targets = append(targets, target.ID)
		}
		active.DeliveryTargets = unique(targets)
		n.saveEpisode(addRuns(*active, downs))
	} else if active != nil && len(observations) == len(m.NodeIDs) {
		up := true
		for _, s := range observations {
			up = up && s.State == "up"
		}
		for node, seqs := range active.Runs {
			for _, seq := range seqs {
				found := false
				for _, s := range observations {
					if s.NodeID == node && s.Sequence > seq {
						found = true
					}
				}
				up = up && found
			}
		}
		if up {
			tracked := n.recordMaintenance(*m, *active, false)
			active = &tracked
			active.ResolvedAt = Ptr(n.now())
			active.Closed = map[string]int64{}
			for _, s := range observations {
				active.Closed[s.NodeID] = s.Sequence
			}
			if active.SuppressedByGroupID != "" && !hasReceipt(n.forEpisode(*active), "down") {
				active.SuppressedComplete = true
			}
			active.MaintenanceComplete = n.maintenanceCovers(*m, *active, *active.ResolvedAt)
			n.saveEpisode(*active)
			n.completeDependencySuppressions(*m, *active)
		}
	}
	if slices.Contains(m.NodeIDs, n.ID) && len(n.notificationTargets(*m)) != 0 {
		n.scheduleNotification(*m, observations)
	}
}
func hasReceipt(rs []Receipt, kind string) bool {
	for _, r := range rs {
		if r.Kind == kind {
			return true
		}
	}
	return false
}
func (n *Node) connected(id string) bool {
	seen, ok := n.peerSeen[id]
	return id == n.ID || (ok && n.now()-seen < n.settings.SyncIntervalMs*3+n.settings.PeerTimeoutMs)
}
func (n *Node) Error() error { n.mu.Lock(); defer n.mu.Unlock(); return n.fault }

func (n *Node) Done() <-chan struct{} { return n.ctx.Done() }
