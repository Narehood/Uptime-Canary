package canary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
	"uptime-canary/apps/node-go/internal/notifyhttp"
)

func TestAutomationChannelEventsReplicateAndSurviveRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	var up atomic.Bool
	var mu sync.Mutex
	calls := map[string]int{}
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid provider request")
		}
		if r.Header.Get("Authorization") != "Bearer local-fixture" {
			t.Error("missing bearer")
		}
		kind, _ := body["kind"].(string)
		if strings.Contains(r.URL.Path, "/_matrix/") {
			kind = strings.SplitN(body["body"].(string), "\n", 2)[0]
		}
		mu.Lock()
		calls[r.URL.Path+"/"+kind]++
		mu.Unlock()
		if strings.Contains(r.URL.Path, "/_matrix/") {
			io.WriteString(w, `{"event_id":"$fixture"}`)
		} else {
			io.WriteString(w, `{"message":"Event uptime_canary fired."}`)
		}
	}))
	defer sink.Close()
	database := filepath.Join(t.TempDir(), "automation.db")
	a := messagingNode(t, "a", "b", database, &clock)
	b := messagingNode(t, "b", "a", ":memory:", &clock)
	probe := func(context.Context, Monitor) ProbeResult {
		state := "down"
		if up.Load() {
			state = "up"
		}
		return ProbeResult{State: state, Message: "local fixture"}
	}
	a.probe, b.probe = probe, probe
	ha := ChannelInput{Name: "Home Assistant recovery", Enabled: true, Events: []string{"recovery"}, Config: providerDelivery.Config{Kind: providerDelivery.HomeAssistant, HomeAssistant: &notifyhttp.HomeAssistantConfig{BaseURL: sink.URL, AccessToken: "local-fixture", EventType: "uptime_canary", AllowInsecureHTTP: true}}}
	matrix := ChannelInput{Name: "Matrix outage", Enabled: true, Events: []string{"down"}, Config: providerDelivery.Config{Kind: providerDelivery.Matrix, Matrix: &notifyhttp.MatrixConfig{BaseURL: sink.URL, AccessToken: "local-fixture", RoomID: "!ops:example.test", AllowInsecureHTTP: true}, Templates: providerDelivery.Templates{Subject: "{{event_kind}}", Text: "local fixture"}}}
	hc, mc := addMessagingChannel(t, a, ha), addMessagingChannel(t, a, matrix)
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.RepeatAfterMs = Ptr(int64(1000))
	in.IntervalMs, in.TimeoutMs, in.NotificationIDs = 60000, 1000, []string{hc.ID, mc.ID}
	// Use management admission so immutable payloads contain the same canonical
	// target that peer ingress validates (http://host:port/ with the trailing /).
	code, body := messagingAPI(a, "/api/monitors", "POST", in, adminToken)
	var m Monitor
	if code != 201 || json.Unmarshal(body, &m) != nil {
		t.Fatal("monitor admission", code, string(body))
	}
	drive := func() {
		partitionDrive(t, a)
		messagingPeerPage(t, a, b)
		partitionDrive(t, b)
		messagingPeerPage(t, b, a)
		partitionDrive(t, a)
	}
	messagingPeerPage(t, a, b)
	drive()
	check := func(want int) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		total := 0
		for route, count := range calls {
			total += count
			if count != 1 || strings.Contains(route, "/api/events/") && !strings.HasSuffix(route, "/recovery") || strings.Contains(route, "/_matrix/") && !strings.HasSuffix(route, "/down") {
				t.Fatal("wrong event or duplicate", calls)
			}
		}
		if total != want {
			t.Fatal("missing notification", calls, want)
		}
	}
	check(1)
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	a = messagingNode(t, "a", "b", database, &clock)
	a.probe = probe
	clock.Add(10000)
	drive()
	check(1)
	up.Store(true)
	clock.Add(1000)
	a.nextProbe[m.ID], b.nextProbe[m.ID] = 0, 0
	drive()
	messagingPeerPage(t, a, b)
	clock.Add(10000)
	drive()
	check(2)
	for _, n := range []*Node{a, b} {
		if len(n.receipts) != 2 {
			t.Fatal("missing replicated receipts", n.ID, len(n.receipts))
		}
	}
	// Re-adding a recovery route after the outage cannot invent intent.
	mu.Lock()
	calls = map[string]int{}
	mu.Unlock()
	copy := ha
	copy.Name = "New recovery destination"
	added := addMessagingChannel(t, a, copy)
	edit := m.MonitorInput
	edit.NotificationIDs = append(edit.NotificationIDs, added.ID)
	if _, err := a.writeMonitor(edit, a.heads[m.ID], m.ID, false); err != nil {
		t.Fatal(err)
	}
	drive()
	check(0)
}

func TestChannelEventValidation(t *testing.T) {
	in := hookChannel("Events", "http://127.0.0.1:1")
	data, _ := json.Marshal(in)
	for _, events := range []string{`[]`, `null`, `["test"]`, `["repeat"]`, `["down","down"]`, `"down"`} {
		value := strings.TrimSuffix(string(data), "}") + `,"events":` + events + `}`
		if _, err := parseChannel([]byte(value)); err == nil {
			t.Fatal("accepted events", events)
		}
	}
}

func TestChannelEventRecoveryIsTerminalAfterSelectionEdit(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-down-attempt", true: "filtered-hold-attempt"}[held], func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(time.Now().UnixMilli())
			n := maintenanceNode(t, &clock, ":memory:")
			sink := webhook(t)
			in := hookChannel("Recovery only", sink.URL)
			in.Events = []string{"recovery"}
			channel := addMessagingChannel(t, n, in)
			var up atomic.Bool
			n.probe = func(context.Context, Monitor) ProbeResult {
				state := "down"
				if up.Load() {
					state = "up"
				}
				return ProbeResult{State: state, Message: "private fixture"}
			}
			monitorInput := input([]string{"a"}, "http://127.0.0.1:1", "")
			monitorInput.IntervalMs, monitorInput.TimeoutMs = 60000, 1000
			monitorInput.NotificationIDs = []string{channel.ID}
			code, body := messagingAPI(n, "/api/monitors", "POST", monitorInput, adminToken)
			var monitor Monitor
			if code != 201 || json.Unmarshal(body, &monitor) != nil {
				t.Fatal(code, string(body))
			}
			partitionDrive(t, n)
			if sink.count("") != 0 {
				t.Fatal("recovery-only channel sent down")
			}
			var episode Incident
			for _, value := range n.episodes {
				if value.MonitorID == monitor.ID {
					episode = value
				}
			}
			if episode.ID == "" {
				t.Fatal("missing outage")
			}
			if held {
				target := n.notificationTargets(monitor)[0]
				n.deliveryHold(monitor, episode, target, true)
				n.deliveryHold(monitor, episode, target, false)
			}
			up.Store(true)
			clock.Add(1000)
			n.nextProbe[monitor.ID] = 0
			partitionDrive(t, n)
			if sink.count("recovery") != 1 || sink.count("down") != 0 {
				t.Fatal("missing recovery-only delivery", sink.all())
			}
			edit := func(events []string) {
				t.Helper()
				channel.Events = events
				code, body := messagingAPI(n, "/api/notification-channels/"+channel.ID, "PUT", channel, adminToken)
				if code != 200 || json.Unmarshal(body, &channel) != nil {
					t.Fatal(code, string(body))
				}
				partitionDrive(t, n)
			}
			edit(nil) // Omission restores all events, including down.
			if sink.count("down") != 0 || sink.count("recovery") != 1 {
				t.Fatal("selection edit replayed a closed outage", sink.all())
			}
			// A job already queued before learning recovery must also recheck it.
			for _, value := range n.episodes {
				if value.MonitorID == monitor.ID && value.Closed != nil {
					episode = value
				}
			}
			target := n.notificationTargets(monitor)[0]
			queued := attempt{ID: attemptIdentity(monitor.ID, episode.ID, "down", 0, target.ID), MonitorID: monitor.ID, IncidentID: episode.ID, Kind: "down", DestinationID: target.ID, ChannelID: channel.ID, TargetRevision: channel.Revision, Provider: target.Provider, State: "queued"}
			n.sendNotification(monitor, episode, queued, "fixture-down", nil)
			if sink.count("down") != 0 {
				t.Fatal("queued down escaped the recovery recheck", sink.all())
			}
			edit([]string{"recovery"})
			up.Store(false)
			clock.Add(1000)
			n.nextProbe[monitor.ID] = 0
			partitionDrive(t, n)
			if sink.count("down") != 0 {
				t.Fatal("new outage ignored selection")
			}
			edit(nil)
			if sink.count("down") != 1 {
				t.Fatal("enabling down during an open outage lost the alert", sink.all())
			}
		})
	}
}
