package canary

import "testing"

func TestCapacityDeliveryReceiptWitness(t *testing.T) {
	const monitor, destination, channel = "monitor", "destination", "channel"
	newNode := func() *Node {
		return &Node{ID: "owner", episodes: map[string]Incident{
			"canonical": {ID: "canonical", MonitorID: monitor, Aliases: []string{"canonical", "source"}},
		}, receipts: map[string]Receipt{}}
	}
	a := attempt{ID: "own-key", MonitorID: monitor, IncidentID: "source", DestinationID: destination, ChannelID: channel, Kind: "down", State: "retrying"}
	receipt := func(id, source string) Receipt {
		return Receipt{NotificationView: NotificationView{ID: id, MonitorID: monitor, IncidentID: source,
			DestinationID: destination, ChannelID: channel, Kind: "down", NodeID: "sender"}}
	}
	t.Run("exact receipt preferred over sibling", func(t *testing.T) {
		n := newNode()
		n.receipts["sibling-key"] = receipt("sibling-key", "canonical")
		n.receipts[a.ID] = receipt(a.ID, "source")
		v := n.attemptView(a)
		if v.SatisfiedByReceiptID != a.ID || v.SourceIncidentID != "source" || v.IncidentID != "canonical" || v.State != "accepted" {
			t.Fatalf("missing exact source/receipt witness: %+v", v)
		}
	})
	t.Run("sibling satisfaction revokes after split", func(t *testing.T) {
		n := newNode()
		n.receipts["sibling-key"] = receipt("sibling-key", "canonical")
		v := n.attemptView(a)
		if v.SatisfiedByReceiptID != "sibling-key" || v.SourceIncidentID != "source" || v.State != "accepted" {
			t.Fatalf("missing sibling witness: %+v", v)
		}
		n.episodes = map[string]Incident{
			"canonical": {ID: "canonical", MonitorID: monitor},
			"source":    {ID: "source", MonitorID: monitor},
		}
		if v := n.attemptView(a); v.SatisfiedByReceiptID != "" || v.State == "accepted" || v.SourceIncidentID != "source" {
			t.Fatalf("split retained stale witness: %+v", v)
		}
	})
	t.Run("canonical source also needs a concrete sibling witness", func(t *testing.T) {
		n := newNode()
		n.receipts["sibling-key"] = receipt("sibling-key", "source")
		canonical := a
		canonical.IncidentID = "canonical"
		if v := n.attemptView(canonical); v.SatisfiedByReceiptID != "sibling-key" || v.SourceIncidentID != "canonical" {
			t.Fatalf("canonical source cannot identify coverage: %+v", v)
		}
	})
	for _, field := range []string{"monitor", "destination", "kind", "incident"} {
		t.Run("unrelated "+field+" receipt cannot satisfy", func(t *testing.T) {
			n := newNode()
			r := receipt("other-key", "source")
			switch field {
			case "monitor":
				r.MonitorID = "other"
			case "destination":
				r.DestinationID = "other"
			case "kind":
				r.Kind = "recovery"
			case "incident":
				r.IncidentID = "other"
			}
			n.receipts[r.ID] = r
			if v := n.attemptView(a); v.SatisfiedByReceiptID != "" || v.SourceIncidentID != "source" {
				t.Fatalf("unrelated receipt acquired witness: %+v", v)
			}
		})
	}
	t.Run("repeat coverage requires sufficient slot", func(t *testing.T) {
		n := newNode()
		repeat := a
		repeat.Kind, repeat.Slot = "repeat", 3
		r := receipt("repeat-key", "source")
		r.Kind, r.Slot = "repeat", 2
		n.receipts[r.ID] = r
		if v := n.attemptView(repeat); v.SatisfiedByReceiptID != "" {
			t.Fatalf("older repeat supplied witness: %+v", v)
		}
		r.Slot = 4
		n.receipts[r.ID] = r
		if v := n.attemptView(repeat); v.SatisfiedByReceiptID != r.ID {
			t.Fatalf("covering repeat lacks witness: %+v", v)
		}
	})
}
