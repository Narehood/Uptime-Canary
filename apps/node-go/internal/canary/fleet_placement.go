package canary

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

const fleetPlacementCategory = "fleetPlacements"

// The dashboard keeps one stable placement ID per independent peer scope.
// Retries acknowledge the original write; they never silently overwrite a
// later local edit, revive a deletion, or manufacture another monitor ID.
type fleetPlacementCommand struct {
	Sequence  int64                `json:"sequence"`
	Revision  string               `json:"revision"`
	MonitorID string               `json:"monitorId,omitempty"`
	Adopt     bool                 `json:"adopt,omitempty"`
	Monitor   json.RawMessage      `json:"monitor,omitempty"`
	Deleted   bool                 `json:"deleted,omitempty"`
	Resources []fleetResourceInput `json:"resources,omitempty"`
}

type fleetPlacementResult struct {
	PlacementID string `json:"placementId"`
	MonitorID   string `json:"monitorId"`
	Sequence    int64  `json:"sequence"`
	Revision    string `json:"revision"`
	Deleted     bool   `json:"deleted"`
}

// Only one small receipt per retained monitor ID. No request credentials or
// private monitor settings are copied into this local authorization journal.
// These receipts are never part of a peer snapshot.
type fleetPlacementReceipt struct {
	Owner       string `json:"owner"`
	CommandHash string `json:"commandHash"`
	fleetPlacementResult
}

func fleetPlacementID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

func fleetPlacementOwner(a account) string {
	if (a.Role != "admin" && a.Role != "editor") || !dashboardManagedAccountID(a.ID) {
		return ""
	}
	return strings.TrimSuffix(a.ID, "-"+a.Role)
}

// Key ordering and whitespace do not change an operation's identity. Numbers
// retain their original precision; admission subsequently validates the types.
func fleetCommandHash(b []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fail(400, "Invalid placement command")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fail(400, "Invalid placement command")
	}
	return digest(string(canonical)), nil
}

// Called under n.mu after the normal API reauthorization and bounded body read.
// A retained dashboard role delegation is required even for receipt reads.
func (n *Node) fleetPlacementAPI(r *http.Request, actor account, b []byte) (int, any, error) {
	id := strings.TrimPrefix(r.URL.Path, "/api/fleet-placements/")
	validate := strings.HasSuffix(id, "/validate")
	if validate {
		id = strings.TrimSuffix(id, "/validate")
	}
	if !fleetPlacementID(id) || (!validate && r.Method != "GET" && r.Method != "PUT") || validate && r.Method != "POST" {
		return 0, nil, fail(404, "Not found")
	}
	owner := fleetPlacementOwner(actor)
	if owner == "" {
		return 0, nil, fail(403, "A dashboard management delegation is required")
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return 0, nil, fail(400, "Placement commands do not accept a query")
	}
	var command fleetPlacementCommand
	if r.Method != "GET" {
		if err := dashboardJSON(b, &command, "sequence", "revision", "monitorId", "adopt", "monitor", "deleted", "resources"); err != nil {
			return 0, nil, err
		}
	}
	monitorID, indexed, err := loadOne[string](n.store, "fleetPlacementIndex", id)
	if err != nil {
		return 0, nil, fail(503, "Cannot read placement identity")
	}
	if !indexed {
		monitorID = "fleet-" + id
		if command.Adopt {
			monitorID = command.MonitorID
		}
	}
	if identifier(monitorID, "monitor ID") != nil || command.MonitorID != "" && command.MonitorID != monitorID {
		return 0, nil, fail(400, "Invalid placement monitor identity")
	}
	previous, exists, err := loadOne[fleetPlacementReceipt](n.store, fleetPlacementCategory, monitorID)
	if err != nil {
		return 0, nil, fail(503, "Cannot read placement receipt")
	}
	transfer := exists && command.Adopt && previous.PlacementID != id && !n.fleetOwnerExists(previous.Owner)
	if exists && !transfer && (previous.Owner != owner || previous.PlacementID != id || previous.MonitorID != monitorID) {
		return 0, nil, fail(409, "This placement belongs to another dashboard registration")
	}
	if r.Method == "GET" {
		if !exists {
			return 0, nil, fail(404, "Placement receipt not found")
		}
		return 200, previous.fleetPlacementResult, nil
	}
	oldIndex := ""
	if transfer {
		oldIndex = previous.PlacementID
		exists = false
	}
	if command.Sequence < 1 || command.Sequence > maxSafe ||
		(command.Revision != "" && identifier(command.Revision, "revision") != nil) ||
		command.Deleted && len(command.Monitor) != 0 || !command.Deleted && len(command.Monitor) == 0 || command.Adopt && (command.MonitorID == "" || command.Deleted) {
		return 0, nil, fail(400, "Invalid placement command")
	}
	hash, err := fleetCommandHash(b)
	if err != nil {
		return 0, nil, err
	}
	if exists && command.Sequence == previous.Sequence {
		if hash != previous.CommandHash {
			return 0, nil, fail(409, "Placement sequence already names a different command")
		}
		return 200, previous.fleetPlacementResult, nil
	}
	if !exists && command.Sequence != 1 || exists && command.Sequence != previous.Sequence+1 {
		return 0, nil, fail(409, "Placement sequence is stale or out of order")
	}
	if exists && previous.Deleted {
		return 0, nil, fail(409, "This placement was retired; create a new placement")
	}
	heads := n.heads[monitorID]
	// A native delete can finish before the dashboard retires its owned copy.
	// Acknowledge that ready tombstone without creating another revision or
	// reviving the monitor. Live or conflicted heads still require the normal CAS.
	if exists && command.Deleted && len(heads) == 1 && heads[0].Monitor.Deleted && n.monitorVersionReady(heads[0]) &&
		(command.Revision == previous.Revision || command.Revision == heads[0].Monitor.Revision) {
		if r.Context().Err() != nil {
			return 0, nil, fail(408, "Placement request cancelled")
		}
		if validate {
			return 200, map[string]bool{"valid": true}, nil
		}
		result := fleetPlacementResult{PlacementID: id, MonitorID: monitorID, Sequence: command.Sequence, Revision: heads[0].Monitor.Revision, Deleted: true}
		receipt := fleetPlacementReceipt{Owner: owner, CommandHash: hash, fleetPlacementResult: result}
		if err = n.applySnapshotLocal(Snapshot{}, func(e dbExec) error { return put(e, fleetPlacementCategory, monitorID, receipt) }); err != nil {
			return 0, nil, err
		}
		return 200, result, nil
	}
	if !exists {
		if command.Adopt {
			if len(heads) != 1 || heads[0].Monitor.Deleted || command.Revision != heads[0].Monitor.Revision || !n.monitorVersionReady(heads[0]) {
				return 0, nil, fail(409, "The monitor changed before adoption")
			}
		} else if len(heads) != 0 || command.Revision != "" || command.Deleted {
			return 0, nil, fail(409, "A new placement cannot replace an existing monitor")
		}
		if !command.Adopt && len(n.heads) >= maxMonitors {
			return 0, nil, fail(409, "Monitor capacity exceeded")
		}
	} else if len(heads) != 1 || heads[0].Monitor.Deleted ||
		command.Revision != heads[0].Monitor.Revision || !n.monitorVersionReady(heads[0]) {
		return 0, nil, fail(409, "Placement revision is stale or configuration has conflicting heads")
	}
	resourceSnapshot, resourceReceipts, bundled, err := n.prepareFleetResources(owner, command.Resources)
	if err != nil {
		return 0, nil, err
	}
	var input MonitorInput
	if command.Deleted {
		input = heads[0].Monitor.MonitorInput
	} else {
		input, err = parseMonitor(command.Monitor, n.known)
		if err != nil {
			return 0, nil, err
		}
		// Fleet configuration always names its destinations. Unlike an ordinary
		// POST, this route never silently adds a node's default channels.
		if err = n.validateFleetReferences(&Monitor{MonitorInput: input, ID: monitorID}, nil, bundled); err != nil {
			return 0, nil, err
		}
	}
	if r.Context().Err() != nil {
		return 0, nil, fail(408, "Placement request cancelled")
	}
	if validate {
		return 200, map[string]bool{"valid": true}, nil
	}
	monitor := Monitor{MonitorInput: input, ID: monitorID, Revision: newID(), UpdatedAt: n.now(), Deleted: command.Deleted}
	version, edges, err := n.joinMonitorRevision(monitor, heads)
	if err != nil {
		return 0, nil, err
	}
	result := fleetPlacementResult{PlacementID: id, MonitorID: monitorID, Sequence: command.Sequence,
		Revision: version.Monitor.Revision, Deleted: command.Deleted}
	receipt := fleetPlacementReceipt{Owner: owner, CommandHash: hash, fleetPlacementResult: result}
	resourceSnapshot.Versions = []Version{version}
	resourceSnapshot.RevisionEdges = append(resourceSnapshot.RevisionEdges, edges...)
	if err = n.applySnapshotLocal(resourceSnapshot, func(e dbExec) error {
		for key, receipt := range resourceReceipts {
			if err := put(e, "fleetResourceReceipts", key, receipt); err != nil {
				return err
			}
		}
		if oldIndex != "" {
			if err := remove(e, "fleetPlacementIndex", oldIndex); err != nil {
				return err
			}
		}
		if err := put(e, "fleetPlacementIndex", id, monitorID); err != nil {
			return err
		}
		return put(e, fleetPlacementCategory, monitorID, receipt)
	}); err != nil {
		return 0, nil, err
	}
	if version.Monitor.Type == "manual" {
		n.nextProbe[monitorID] = 0
	}
	status := 200
	if !exists {
		status = 201
	}
	return status, result, nil
}

func (n *Node) fleetOwnerExists(owner string) bool {
	for _, role := range dashboardRoles {
		if _, ok := n.accounts[owner+"-"+role]; ok {
			return true
		}
	}
	return false
}
