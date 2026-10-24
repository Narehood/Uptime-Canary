package canary

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
)

// Root may overlay this fixture into internal/canary and run it through the
// bounded runner. Nodes never Start, no sends occur, and stores are private.
// These controls inspect routing behavior, not private cache representation.
func capacityTargetsWrite(t *testing.T, n *Node, id string, in ChannelInput, deleted bool) Resource {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	r, err := n.writeResource(resourceChannel, id, in, deleted)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func capacityTargetsGet(t *testing.T, n *Node, m Monitor, count int) []notificationTarget {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []notificationTarget
	for range 3 {
		out = n.notificationTargets(m)
		if len(out) != count {
			t.Fatalf("notification target count=%d want=%d", len(out), count)
		}
	}
	return out
}

func TestCapacityTargetsCacheRefreshAndFanout(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceNode(t, &clock, ":memory:")
	in := messagingChannelInput(providerDelivery.Ntfy, "http://127.0.0.1:1")
	in.Config.Ntfy.Tags = []string{"warning"}
	r := capacityTargetsWrite(t, n, "capacity-targets", in, false)
	m := Monitor{MonitorInput: MonitorInput{NotificationIDs: []string{r.ID}}}
	first := capacityTargetsGet(t, n, m, 1)[0]
	if first.Config.Ntfy == nil || first.Config.Ntfy.Token != "fixture_ntfy_key" || first.Revision != r.Revision {
		t.Fatal("initial native target is incomplete")
	}

	// Changing returned value fields must not corrupt subsequent lookups. Nested
	// configs are intentionally read-only; this does not demand a deep clone.
	returned := capacityTargetsGet(t, n, m, 1)
	returned[0].ID, returned[0].Revision, returned[0].Config.Kind = "corrupted", "corrupted", "corrupted"
	unchanged := capacityTargetsGet(t, n, m, 1)[0]
	if unchanged.ID != first.ID || unchanged.Revision != first.Revision || unchanged.Config.Kind != providerDelivery.Ntfy {
		t.Fatal("changing returned value fields corrupted a later lookup")
	}
	r = capacityTargetsWrite(t, n, r.ID, in, false) // Same bytes, new revision.
	current := capacityTargetsGet(t, n, m, 1)[0]
	if current.ID != first.ID || current.Revision != r.Revision || current.Config.Kind != providerDelivery.Ntfy {
		t.Fatal("returned slice or identical-data revision reused stale target state")
	}

	in.Config.Ntfy.Token = "rotated_ntfy_key"
	in.Config.Ntfy.Tags = []string{"rotated"}
	in.Config.Templates.Text = "rotated template"
	r = capacityTargetsWrite(t, n, r.ID, in, false)
	current = capacityTargetsGet(t, n, m, 1)[0]
	if current.ID != first.ID || current.Revision != r.Revision || current.Config.Ntfy == nil ||
		current.Config.Ntfy.Token != "rotated_ntfy_key" || len(current.Config.Ntfy.Tags) != 1 ||
		current.Config.Ntfy.Tags[0] != "rotated" || current.Config.Templates.Text != "rotated template" {
		t.Fatal("credential/template rotation changed identity or retained stale config")
	}
	if first.Config.Ntfy.Token != "fixture_ntfy_key" || first.Config.Ntfy.Tags[0] != "warning" {
		t.Fatal("refresh mutated a config already captured by an earlier caller")
	}
	n.mu.Lock()
	views := n.channelViews("viewer")
	n.mu.Unlock()
	if len(views) != 1 || views[0].Channel.Config.Ntfy != nil || views[0].Channel.Config.Kind != providerDelivery.Ntfy {
		t.Fatal("warm target lookup leaked native credentials through viewer projection")
	}

	in.Config.Ntfy.Topic = "different-topic"
	r = capacityTargetsWrite(t, n, r.ID, in, false)
	routed := capacityTargetsGet(t, n, m, 1)[0]
	if routed.ID == current.ID || routed.Revision != r.Revision || routed.Config.Ntfy == nil || routed.Config.Ntfy.Topic != "different-topic" {
		t.Fatal("route edit retained the old recipient identity or config")
	}
	in = hookChannel("Changed provider", "http://127.0.0.1:1/new-hook")
	r = capacityTargetsWrite(t, n, r.ID, in, false)
	hook := capacityTargetsGet(t, n, m, 1)[0]
	if hook.ID == routed.ID || hook.Provider != string(providerDelivery.Webhook) || hook.Revision != r.Revision ||
		hook.URL != in.Config.Webhook.URL || hook.Config.Ntfy != nil {
		t.Fatal("provider edit retained old routing or provider secrets")
	}

	in = ChannelInput{Name: "Recipient fanout", Enabled: true, Config: providerDelivery.Config{
		Kind: providerDelivery.Brevo, Sender: providerDelivery.Address{Email: "sender@example.test"},
		Recipients: []providerDelivery.Address{{Email: "alpha@example.test"}, {Email: "beta@example.test"}},
		Brevo:      providerDelivery.BrevoConfig{APIKey: "original_email_key"},
	}}
	r = capacityTargetsWrite(t, n, r.ID, in, false)
	before := capacityTargetsGet(t, n, m, 2)
	if before[0].ID == before[1].ID || len(before[0].Config.Recipients) != 1 || len(before[1].Config.Recipients) != 1 {
		t.Fatal("recipient fanout lost per-recipient routing")
	}
	in.Config.Recipients = []providerDelivery.Address{{Email: "beta@example.test"}, {Email: "gamma@example.test"}}
	in.Config.Brevo.APIKey = "rotated_email_key"
	r = capacityTargetsWrite(t, n, r.ID, in, false)
	after := capacityTargetsGet(t, n, m, 2)
	for index, target := range after {
		if target.Revision != r.Revision || target.Provider != string(providerDelivery.Brevo) ||
			target.Config.Brevo.APIKey != "rotated_email_key" || len(target.Config.Recipients) != 1 ||
			target.Config.Recipients[0].Email != in.Config.Recipients[index].Email {
			t.Fatal("recipient or secret edit retained stale fanout config")
		}
	}
	if after[0].ID != before[1].ID || after[1].ID == before[0].ID || after[1].ID == before[1].ID ||
		before[1].Config.Brevo.APIKey != "original_email_key" {
		t.Fatal("recipient edit reused another recipient's receipts or mutated old config")
	}
	in.Enabled = false
	capacityTargetsWrite(t, n, r.ID, in, false)
	capacityTargetsGet(t, n, m, 0)
	in.Enabled = true
	in.Config.Recipients = []providerDelivery.Address{{Email: "delta@example.test"}}
	in.Config.Brevo.APIKey = "reenabled_email_key"
	r = capacityTargetsWrite(t, n, r.ID, in, false)
	current = capacityTargetsGet(t, n, m, 1)[0]
	if current.Revision != r.Revision || len(current.Config.Recipients) != 1 ||
		current.Config.Recipients[0].Email != "delta@example.test" || current.Config.Brevo.APIKey != "reenabled_email_key" {
		t.Fatal("reenabling reused disabled channel config")
	}
	capacityTargetsWrite(t, n, r.ID, in, true)
	capacityTargetsGet(t, n, m, 0)
}

func TestCapacityTargetsCacheChurnPreservesRouting(t *testing.T) {
	for _, many := range []bool{true, false} {
		name, count := "many-small-channels", 140
		if !many {
			name, count = "few-large-channels", 10
		}
		t.Run(name, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			n := maintenanceNode(t, &clock, ":memory:")
			var first notificationTarget
			var initial Monitor
			for index := range count {
				in := hookChannel(fmt.Sprintf("Channel %d", index), fmt.Sprintf("http://127.0.0.1:1/route-%d", index))
				if !many {
					in.Config.Templates.Text = strings.Repeat("t", 64<<10)
					in.Config.Templates.HTML = strings.Repeat("h", 64<<10)
				}
				r := capacityTargetsWrite(t, n, fmt.Sprintf("capacity-churn-%d", index), in, false)
				m := Monitor{MonitorInput: MonitorInput{NotificationIDs: []string{r.ID}}}
				target := capacityTargetsGet(t, n, m, 1)[0]
				if target.ChannelID != r.ID || target.Revision != r.Revision || target.URL != in.Config.Webhook.URL ||
					target.Config.Templates != in.Config.Templates {
					t.Fatal("channel churn changed routing, revision or templates")
				}
				if index == 0 {
					first, initial = target, m
				}
			}
			again := capacityTargetsGet(t, n, initial, 1)[0]
			if again.ID != first.ID || again.Revision != first.Revision || again.URL != first.URL ||
				again.Config.Templates != first.Config.Templates {
				t.Fatal("revisiting an earlier channel after churn changed delivery behavior")
			}
		})
	}
}

func TestCapacityTargetsCacheWarmConflictAndDelayedProof(t *testing.T) {
	for _, mode := range []string{"conflict", "incomplete-parent-proof"} {
		t.Run(mode, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			n := maintenanceReviewNode(t, &clock, ":memory:")
			in := hookChannel("Initial route", "http://127.0.0.1:1/original")
			root := capacityTargetsWrite(t, n, "capacity-proof-target", in, false)
			m := Monitor{MonitorInput: MonitorInput{NotificationIDs: []string{root.ID}}}
			initial := capacityTargetsGet(t, n, m, 1)[0]
			if mode == "conflict" {
				in.Name = "Local edit"
				capacityTargetsWrite(t, n, root.ID, in, false)
				capacityTargetsGet(t, n, m, 1)
			}
			in.Config.Webhook.URL = "http://127.0.0.1:1/peer-route"
			data, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			peer := Resource{Kind: resourceChannel, ID: root.ID, Revision: "capacity-peer-edit", UpdatedAt: clock.Load(), Data: data}
			parents := []string{root.Revision}
			if mode == "incomplete-parent-proof" {
				parents = append(parents, "capacity-missing-parent")
			}
			edge := newRevisionEdge(resourceChannel, peer.ID, peer.Revision, peer, parents, "")
			version := ResourceVersion{Resource: peer, Ancestors: edge.Parents, EdgeID: edge.ID}
			maintenanceReviewMerge(t, n, Snapshot{Resources: []ResourceVersion{version}, RevisionEdges: []RevisionEdge{edge}})
			capacityTargetsGet(t, n, m, 0)
			n.mu.Lock()
			heads, ready := len(n.resources[resourceKey(resourceChannel, root.ID)]), n.resourceVersionReady(version)
			n.mu.Unlock()
			if mode == "conflict" {
				if heads != 2 || !ready {
					t.Fatal("fixture did not create a proven concurrent-head conflict")
				}
				resolved := capacityTargetsWrite(t, n, root.ID, in, false)
				target := capacityTargetsGet(t, n, m, 1)[0]
				if target.Revision != resolved.Revision || target.ID == initial.ID || target.URL != in.Config.Webhook.URL {
					t.Fatal("conflict resolution did not refresh the selected route")
				}
			} else {
				if heads != 1 || ready {
					t.Fatal("fixture did not create exactly one head awaiting parent proof")
				}
				missing := peer
				missing.Revision = "capacity-missing-parent"
				proof := newRevisionEdge(resourceChannel, missing.ID, missing.Revision, missing, []string{}, "")
				maintenanceReviewMerge(t, n, Snapshot{RevisionEdges: []RevisionEdge{proof}})
				target := capacityTargetsGet(t, n, m, 1)[0]
				if target.Revision != peer.Revision || target.ID == initial.ID || target.URL != in.Config.Webhook.URL {
					t.Fatal("late proof did not release the exact existing head with its new route")
				}
			}
		})
	}
}
