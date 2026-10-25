package canary

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestDefaultChannelsOnlyApplyToOmittedNewSelection(t *testing.T) {
	node := local(t, nil)
	defaultInput := hookChannel("Default destination", "http://127.0.0.1:1/default")
	defaultInput.IsDefault = true
	channel := addChannel(t, node, defaultInput)
	otherInput := defaultInput
	otherInput.Name, otherInput.Enabled = "Disabled default", false
	_ = addChannel(t, node, otherInput)
	base := input([]string{node.ID}, "http://127.0.0.1:1", "")
	base.Enabled = false
	create := func(value any) Monitor {
		t.Helper()
		code, raw := api(t, node, "/api/monitors", "POST", value, adminToken)
		var monitor Monitor
		if code != 201 || json.Unmarshal(raw, &monitor) != nil {
			t.Fatalf("monitor create: %d %s", code, raw)
		}
		return monitor
	}
	created := create(base)
	if !slices.Equal(created.NotificationIDs, []string{channel.ID}) {
		t.Fatal("enabled default was not selected")
	}
	raw, _ := json.Marshal(base)
	var explicit map[string]any
	_ = json.Unmarshal(raw, &explicit)
	explicit["notificationIds"] = []string{}
	if got := create(explicit); len(got.NotificationIDs) != 0 {
		t.Fatal("explicit empty selection was overwritten")
	}
	legacy := base
	legacy.WebhookURL = "http://127.0.0.1:1/legacy"
	if got := create(legacy); len(got.NotificationIDs) != 0 || got.WebhookURL != legacy.WebhookURL {
		t.Fatal("legacy explicit destination was changed")
	}
	// A conflicted default cannot silently disappear from an implicit selection.
	node.mu.Lock()
	key := resourceKey(resourceChannel, channel.ID)
	old := slices.Clone(node.resources[key])
	second := old[0]
	second.Resource.Revision = "concurrent-default"
	node.resources[key] = append(slices.Clone(old), second)
	_, err := node.defaultChannelIDs()
	node.resources[key] = old
	node.mu.Unlock()
	if err == nil {
		t.Fatal("conflicted default was silently omitted")
	}
}

func TestApplyChannelBatchPreservesExistingDestinationsAndRejectsStaleAtomically(t *testing.T) {
	node := local(t, nil)
	selected := addChannel(t, node, hookChannel("Apply destination", "http://127.0.0.1:1/new"))
	existing := addChannel(t, node, hookChannel("Existing destination", "http://127.0.0.1:1/existing"))
	create := func(name string) Monitor {
		base := input([]string{node.ID}, "http://127.0.0.1:1", "")
		base.Enabled, base.Name = false, name
		base.NotificationIDs = []string{existing.ID}
		code, raw := api(t, node, "/api/monitors", "POST", base, adminToken)
		var monitor Monitor
		if code != 201 || json.Unmarshal(raw, &monitor) != nil {
			t.Fatalf("create: %d %s", code, raw)
		}
		return monitor
	}
	first, second := create("First"), create("Second")
	payload := func(secondRevision string) map[string]any {
		return map[string]any{"revision": selected.Revision, "monitors": []map[string]string{{"id": first.ID, "revision": first.Revision}, {"id": second.ID, "revision": secondRevision}}}
	}
	path := "/api/notification-channels/" + selected.ID + "/apply"
	if code, _ := api(t, node, path, "POST", payload("stale"), adminToken); code != 409 {
		t.Fatal("stale batch admitted", code)
	}
	node.mu.Lock()
	unchanged := node.current(first.ID).Revision == first.Revision && node.current(second.ID).Revision == second.Revision
	node.mu.Unlock()
	if !unchanged {
		t.Fatal("stale member left a partially applied batch")
	}
	if code, body := api(t, node, path, "POST", payload(second.Revision), adminToken); code != 200 {
		t.Fatalf("apply: %d %s", code, body)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	for _, before := range []Monitor{first, second} {
		after := node.current(before.ID)
		if after == nil || !slices.Contains(after.NotificationIDs, existing.ID) || !slices.Contains(after.NotificationIDs, selected.ID) || len(after.NotificationIDs) != 2 || checkGeneration(*after) != checkGeneration(before) {
			t.Fatal("application replaced an existing destination or restarted checks")
		}
	}
	for _, raw := range []string{`{"revision":"one","Revision":"two","monitors":[]}`, `{"revision":"one","monitors":[{"id":"a","id":"b","revision":"r"}]}`} {
		if _, _, err := node.applyChannel(selected.ID, []byte(raw)); err == nil {
			t.Fatal("ambiguous batch admitted")
		}
	}
}
