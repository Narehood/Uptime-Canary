package canary

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/checks"
	"uptime-canary/apps/node-go/internal/jsonquery"
	"uptime-canary/apps/node-go/internal/services"
)

func TestServiceOptionsReplicatePersistAndRemainPrivate(t *testing.T) {
	dir := t.TempDir()
	c := cluster(t, []string{"a", "b"}, func(o *Options) { o.DBPath = filepath.Join(dir, o.NodeID+".sqlite") })
	n := c.nodes[0]
	configs := map[string]*ServiceMonitorOptions{
		"docker":         {Docker: &services.DockerConfig{Endpoint: "http://127.0.0.1:1234", Container: "app"}},
		"redis":          {Redis: &services.RedisConfig{Address: "127.0.0.1:1234", Username: "private-user", Password: "private-password", TLSMode: "plain"}},
		"postgres":       {Postgres: &services.SQLConfig{DSN: "postgres://private-user:private-password@127.0.0.1:5432/app", Query: "SELECT 'private-query'", TLSMode: "plain"}},
		"mysql":          {MySQL: &services.SQLConfig{DSN: "private-user:private-password@tcp(127.0.0.1:3306)/app", Query: "SELECT 'private-query'", TLSMode: "plain"}},
		"mqtt":           {MQTT: &services.MQTTConfig{Address: "127.0.0.1:1234", Username: "private-user", Password: "private-password", Topic: "private-topic", TLSMode: "plain", Condition: &services.Condition{Operator: "eq", Expected: json.RawMessage(`42`)}}},
		"sftp":           {SFTP: &services.SFTPConfig{Address: "127.0.0.1:1234", Username: "private-user", Password: "private-password", UnsafeIgnoreHostKey: true, Path: "/private-path"}},
		"mongodb":        {MongoDB: &services.MongoDBConfig{DSN: "mongodb://private-user:private-password@127.0.0.1:27017/app", TLSMode: "plain"}},
		"sqlserver":      {SQLServer: &services.SQLConfig{DSN: "sqlserver://private-user:private-password@127.0.0.1:1433/app", Query: "SELECT 'private-query'", TLSMode: "plain"}},
		"ntp":            {NTP: &NTPMonitorOptions{Address: "127.0.0.1:123", OffsetThresholdMs: 750, RootDispersionThresholdMs: 500, LatencyThresholdMs: 1000}},
		"grpc":           {GRPC: &services.GRPCConfig{Address: "127.0.0.1:1234", Mode: "health", TLSMode: "plain", Metadata: map[string]string{"authorization": "Bearer private-password"}}},
		"snmp":           {SNMP: &services.SNMPConfig{Address: "127.0.0.1:161", Version: "2c", OID: "1.3.6.1.2.1.1.1.0", Community: "private-password"}},
		"radius":         {RADIUS: &services.RADIUSConfig{Address: "127.0.0.1:1812", Username: "private-user", Password: "private-password", Secret: "private-secret"}},
		"sip-options":    {SIP: &services.SIPConfig{Address: "127.0.0.1:5060", Transport: "udp", RequestURI: "sip:private-user@127.0.0.1:5060"}},
		"rabbitmq":       {RabbitMQ: &services.RabbitMQConfig{Mode: "amqp", Address: "127.0.0.1:5672", Username: "private-user", Password: "private-password", TLSMode: "plain"}},
		"kafka-producer": {Kafka: &services.KafkaConfig{Brokers: []string{"127.0.0.1:9092"}, Mode: "produce", Topic: "private-topic", Message: Ptr("private-message"), TLSMode: "plain", SASL: &services.KafkaSASLConfig{Mechanism: "plain", Username: "private-user", Password: "private-password"}}},
		"oracledb":       {Oracle: &services.SQLConfig{DSN: "oracle://private-user:private-password@127.0.0.1:1521/private-service", Query: "SELECT 'private-query' FROM DUAL", TLSMode: "plain"}},
	}
	for _, query := range []**jsonquery.Config{&configs["mqtt"].MQTT.JSONata, &configs["mongodb"].MongoDB.JSONata, &configs["snmp"].SNMP.JSONata} {
		*query = &jsonquery.Config{Expression: "private_query", Operator: jsonquery.Equal, Expected: json.RawMessage(`"private-query"`)}
	}
	configs["mongodb"].MongoDB.Collection = "private-collection"
	ids := map[string]string{}
	for kind, options := range configs {
		in := input([]string{"a", "b"}, "", "")
		in.Type, in.Name, in.Enabled, in.Service = kind, kind, false, options
		m := add(t, n, in)
		ids[kind] = m.ID
		if strings.Contains(m.Target, "private-") {
			t.Fatal("service target exposed private configuration", kind)
		}
	}
	eventually(t, func() bool {
		peer := c.nodes[1]
		peer.mu.Lock()
		defer peer.mu.Unlock()
		for kind, id := range ids {
			m := peer.current(id)
			if m == nil || !equalJSON(m.Service, configs[kind]) {
				return false
			}
		}
		return true
	})
	code, result := api(t, n, "/api/accounts", "POST", map[string]string{"name": "service-viewer", "role": "viewer", "token": viewerToken}, adminToken)
	if code != 201 {
		t.Fatal("create viewer", code, string(result))
	}
	code, result = api(t, n, "/api/overview", "GET", nil, viewerToken)
	if code != 200 {
		t.Fatal(code)
	}
	for _, secret := range []string{"private-user", "private-password", "private-query", "private-topic", "private-path", "private-secret", "private-message", "private-service"} {
		if strings.Contains(string(result), secret) {
			t.Fatal("viewer received private service configuration")
		}
	}
	if err := c.nodes[1].Stop(); err != nil {
		t.Fatal(err)
	}
	restored, err := New(Options{NodeID: "b", DBPath: filepath.Join(dir, "b.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Stop()
	for kind, id := range ids {
		m := restored.current(id)
		if m == nil || !equalJSON(m.Service, configs[kind]) {
			t.Fatal("service config changed across restart", kind)
		}
	}
}

func TestDockerPendingDoesNotOpenOrRecoverAnIncident(t *testing.T) {
	var state atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/containers/app/json" {
			w.WriteHeader(400)
			return
		}
		bodies := []string{`{"State":{"Running":true,"Health":{"Status":"starting"}}}`, `{"State":{"Running":true,"Health":{"Status":"unhealthy"}}}`, `{"State":{"Running":true,"Restarting":true}}`, `{"State":{"Running":true,"Health":{"Status":"healthy"}}}`}
		_, _ = w.Write([]byte(bodies[state.Load()]))
	}))
	defer server.Close()
	c := cluster(t, []string{"a", "b"}, nil)
	r := webhook(t)
	in := input([]string{"a", "b"}, "", r.URL)
	in.Type, in.Service = "docker", &ServiceMonitorOptions{Docker: &services.DockerConfig{Endpoint: server.URL, Container: "app"}}
	add(t, c.nodes[0], in)
	eventually(t, func() bool { return allState(t, c, "pending") })
	for _, n := range c.nodes {
		if v := getOverview(t, n); len(v.Incidents) != 0 || len(v.Notifications) != 0 || v.Monitors[0].Observations[0].State != "pending" {
			t.Fatal("pending was treated as up/down")
		}
	}
	state.Store(1)
	eventually(t, func() bool { return r.count("down") == 1 })
	initial := r.all()[0].IncidentID
	state.Store(2)
	eventually(t, func() bool { return allState(t, c, "pending") })
	time.Sleep(300 * time.Millisecond)
	if r.count("recovery") != 0 {
		t.Fatal("pending generated a recovery")
	}
	for _, n := range c.nodes {
		v := getOverview(t, n)
		if len(v.Incidents) != 1 || v.Incidents[0].ResolvedAt != nil || v.Incidents[0].ID != initial {
			t.Fatal("pending closed the active incident")
		}
	}
	state.Store(3)
	eventually(t, func() bool { return r.count("recovery") == 1 && allState(t, c, "up") })
	if r.all()[1].IncidentID != initial {
		t.Fatal("real recovery changed incident identity")
	}
}

func TestConfiguredHTTPSOptionsAndCertificateMetadataAcrossPeers(t *testing.T) {
	var authorized atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "private-user" || password != "private-password" || r.Method != "POST" || r.Header.Get("X-Monitor-Key") != "private-header" {
			w.WriteHeader(401)
			return
		}
		authorized.Add(1)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"status":{"ok":true}}`))
	}))
	defer server.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	c := cluster(t, []string{"a", "b"}, nil)
	in := input([]string{"a", "b"}, server.URL, "")
	in.HTTP = &checks.HTTPConfig{Method: "POST", Body: "private-body", Headers: map[string]string{"X-Monitor-Key": "private-header"}, BasicAuth: &checks.BasicAuth{Username: "private-user", Password: "private-password"}, StatusRanges: []checks.StatusRange{{Min: 201, Max: 201}}, JSON: &checks.JSONMatcher{Path: "$.status.ok", Expected: "true"}}
	in.TLS = &TLSMonitorOptions{CAPEM: ca}
	m := add(t, c.nodes[0], in)
	eventually(t, func() bool {
		if !allState(t, c, "up") {
			return false
		}
		for _, n := range c.nodes {
			v := getOverview(t, n)
			for _, o := range v.Monitors[0].Observations {
				if o.StatusCode != 201 || o.Certificate == nil || !o.Certificate.Verified || o.Certificate.NotAfter.IsZero() {
					return false
				}
			}
		}
		return true
	})
	if authorized.Load() < 2 {
		t.Fatal("configured requests were not made from each node")
	}
	code, result := api(t, c.nodes[0], "/api/accounts", "POST", map[string]string{"name": "https-viewer", "role": "viewer", "token": viewerToken}, adminToken)
	if code != 201 {
		t.Fatal(code, string(result))
	}
	code, result = api(t, c.nodes[0], "/api/overview", "GET", nil, viewerToken)
	if code != 200 {
		t.Fatal(code)
	}
	for _, secret := range []string{"private-user", "private-password", "private-header", "private-body", "BEGIN CERTIFICATE"} {
		if strings.Contains(string(result), secret) {
			t.Fatal("viewer received configured HTTPS credentials")
		}
	}
	for _, n := range c.nodes {
		n.mu.Lock()
		current := n.current(m.ID)
		same := current != nil && equalJSON(current.HTTP, in.HTTP) && current.TLS.CAPEM == ca
		n.mu.Unlock()
		if !same {
			t.Fatal("HTTP options did not survive peer round trip")
		}
	}
}

func TestServiceCredentialEditRetiresOldEpisodeWithoutRecovery(t *testing.T) {
	n := local(t, func(o *Options) {
		o.Probe = func(context.Context, Monitor) ProbeResult {
			return ProbeResult{State: "down", Message: "fixture failure"}
		}
	})
	r := webhook(t)
	in := input([]string{n.ID}, "", r.URL)
	in.Type, in.Service = "redis", &ServiceMonitorOptions{Redis: &services.RedisConfig{Address: "127.0.0.1:6379", Password: "first-private-password", TLSMode: "plain"}}
	m := add(t, n, in)
	eventually(t, func() bool { return r.count("down") == 1 })
	first := getOverview(t, n).Incidents[0].ID
	m.Service.Redis.Password = "second-private-password"
	editMonitor(t, n, m)
	eventually(t, func() bool {
		for _, i := range getOverview(t, n).Incidents {
			if i.ID == first {
				return i.RetiredBy != "" && i.ResolvedAt != nil
			}
		}
		return false
	})
	if r.count("recovery") != 0 {
		t.Fatal("credential edit invented a recovery")
	}
}
