package canary

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The frontend attaches recipient labels to acknowledgement IDs once, then follows
// those IDs while polling. Keep acknowledgement order aligned with input order.
func TestAutomationChannelPreviewRecipientOrder(t *testing.T) {
	n, err := New(Options{NodeID: "preview-order", DBPath: ":memory:", AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	// No listener is started, and every delivery worker exits before network I/O.
	n.cancel()
	var channel Channel
	err = json.Unmarshal([]byte(`{"id":"preview-order","revision":"preview-revision","name":"Order fixture","enabled":true,"config":{"kind":"smtp","sender":{"email":"canary@example.test"},"recipients":[{"email":"z-last@example.test"},{"email":"a-first@example.test"}],"smtp":{"server":"127.0.0.1","port":25,"tlsMode":"plain"}}}`), &channel)
	if err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	status, reply, err := n.queueChannelTest(channel, true)
	n.mu.Unlock()
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("queue acknowledgement: status %d, error %v", status, err)
	}
	views := reply.(map[string]any)["attempts"].([]DeliveryAttemptView)
	if len(views) != len(channel.Config.Recipients) {
		t.Fatalf("got %d acknowledgements for %d recipients", len(views), len(channel.Config.Recipients))
	}
	for index := range channel.Config.Recipients {
		single := channel
		single.Config.Recipients = channel.Config.Recipients[index : index+1]
		want := targetsForChannel(single)[0].ID
		if views[index].DestinationID != want || views[index].ID == "" {
			t.Fatalf("acknowledgement %d no longer follows its configured recipient", index)
		}
	}
}
