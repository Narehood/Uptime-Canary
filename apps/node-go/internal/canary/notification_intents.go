package canary

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
)

// Intent is a monotonic fact: this destination was configured during a fresh
// observed outage. Separate records keep recipient churn out of incident size
// limits without discarding facts while channel configuration is unavailable.
type NotificationIntent struct {
	ID            string `json:"id"`
	MonitorID     string `json:"monitorId"`
	IncidentID    string `json:"incidentId"`
	DestinationID string `json:"destinationId"`
}

// Empty is a real legacy destination, so omission and null must not acquire its
// authority. Decode exact field names and reject duplicate keys even outside
// the peer decoder (for example when loading a durable fact).
func (i *NotificationIntent) UnmarshalJSON(b []byte) error {
	bad := func() error { return fail(400, "Invalid notification intent") }
	d := json.NewDecoder(bytes.NewReader(b))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return bad()
	}
	fields := map[string]string{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return bad()
		}
		key, ok := token.(string)
		if !ok || !slices.Contains([]string{"id", "monitorId", "incidentId", "destinationId"}, key) {
			return bad()
		}
		if _, exists := fields[key]; exists {
			return bad()
		}
		value, err := d.Token()
		if err != nil {
			return bad()
		}
		text, ok := value.(string)
		if !ok {
			return bad()
		}
		fields[key] = text
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(fields) != 4 {
		return bad()
	}
	if _, err := d.Token(); err != io.EOF {
		return bad()
	}
	*i = NotificationIntent{ID: fields["id"], MonitorID: fields["monitorId"], IncidentID: fields["incidentId"], DestinationID: fields["destinationId"]}
	return nil
}

const inlineDeliveryTargets = 801

func deliveryIntent(monitor, incident, destination string) NotificationIntent {
	return NotificationIntent{ID: digest(monitor + "/" + incident + "/" + destination), MonitorID: monitor, IncidentID: incident, DestinationID: destination}
}

func (n *Node) indexIntent(i NotificationIntent) {
	key := i.MonitorID + "/" + i.IncidentID
	if n.intentIndex[key] == nil {
		n.intentIndex[key] = map[string]bool{}
	}
	n.intentIndex[key][i.DestinationID] = true
}

func (n *Node) hasDeliveryIntent(i Incident, destination string) bool {
	if slices.Contains(i.DeliveryTargets, destination) {
		return true
	}
	return n.indexedDeliveryIntent(i, destination)
}

func (n *Node) indexedDeliveryIntent(i Incident, destination string) bool {
	for _, id := range episodeIDs(i) {
		if n.intentIndex[i.MonitorID+"/"+id][destination] {
			return true
		}
	}
	return false
}

// The inline list is a bounded compatibility summary. The complete audience is
// retained as paged facts, including after disablement, conflicts and restart.
func boundedDeliveryTargets(i Incident) Incident {
	i.DeliveryTargets = unique(i.DeliveryTargets)
	if len(i.DeliveryTargets) > inlineDeliveryTargets {
		i.DeliveryTargets = i.DeliveryTargets[:inlineDeliveryTargets]
	}
	return i
}
