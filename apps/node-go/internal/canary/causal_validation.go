package canary

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strings"
)

// Apply the commitment omission/null distinction to persisted records as well
// as peer pages. Case aliases and duplicate keys cannot erase that distinction.
func decodeCommittedRecord(b []byte, field string, out any) error {
	bad := func() error { return fail(400, "Invalid causal commitment") }
	d := json.NewDecoder(bytes.NewReader(b))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return bad()
	}
	seen := false
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return bad()
		}
		key, ok := token.(string)
		if !ok {
			return bad()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return bad()
		}
		if strings.EqualFold(key, field) {
			if key != field || seen {
				return bad()
			}
			seen = true
			var id string
			if json.Unmarshal(value, &id) != nil || !validCommitment(id) {
				return bad()
			}
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return bad()
	}
	if _, err = d.Token(); err != io.EOF {
		return bad()
	}
	return json.Unmarshal(b, out)
}

func (v *Version) UnmarshalJSON(b []byte) error {
	type plain Version
	var out plain
	if err := decodeCommittedRecord(b, "edgeId", &out); err != nil {
		return err
	}
	*v = Version(out)
	return nil
}
func (v *GroupVersion) UnmarshalJSON(b []byte) error {
	type plain GroupVersion
	var out plain
	if err := decodeCommittedRecord(b, "edgeId", &out); err != nil {
		return err
	}
	*v = GroupVersion(out)
	return nil
}
func (v *ResourceVersion) UnmarshalJSON(b []byte) error {
	type plain ResourceVersion
	var out plain
	if err := decodeCommittedRecord(b, "edgeId", &out); err != nil {
		return err
	}
	*v = ResourceVersion(out)
	return nil
}
func (v *Incident) UnmarshalJSON(b []byte) error {
	type plain Incident
	var out plain
	if err := decodeCommittedRecord(b, "evidenceRoot", &out); err != nil {
		return err
	}
	*v = Incident(out)
	return nil
}

func (v *uncertainHistory) UnmarshalJSON(b []byte) error {
	type plain uncertainHistory
	var out plain
	if err := decodeCommittedRecord(b, "evidenceRoot", &out); err != nil {
		return err
	}
	*v = uncertainHistory(out)
	return nil
}

func validCommitment(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 32 && id == hex.EncodeToString(b)
}
func validRevisionKind(kind string) bool {
	return slices.Contains([]string{"monitor", "group", resourceChannel, resourceMaintenance, resourceStatusPage, resourceTag}, kind)
}

func (n *Node) validateCausalFacts(s Snapshot) error {
	bad := func() error { return fail(400, "Invalid causal evidence") }
	// Record integrity also applies to atomic local operations such as restore
	// activation. Only wire admission imposes the per-page cardinality limit.
	for _, v := range s.RevisionEdges {
		if !validRevisionKind(v.Kind) || identifier(v.EntityID, "entityId") != nil || identifier(v.Revision, "revision") != nil || !validIDs(v.Parents, maxClusterMembers) || !slices.Equal(v.Parents, unique(v.Parents)) || slices.Contains(v.Parents, v.Revision) || !validCommitment(v.PayloadHash) || v.ID != edgeID(v) || v.LegacyRoot != "" && !validCommitment(v.LegacyRoot) {
			return bad()
		}
		if v.Generation != "" && identifier(v.Generation, "generation") != nil {
			return bad()
		}
		if v.Kind != "monitor" && v.Generation != "" {
			return bad()
		}
		if v.LegacyRoot != "" && len(v.Parents) > 0 {
			return bad()
		}
	}
	for _, v := range s.RevisionClaims {
		if !validRevisionKind(v.Kind) || identifier(v.EntityID, "entityId") != nil || identifier(v.Revision, "revision") != nil || identifier(v.Ancestor, "ancestor") != nil || v.Revision == v.Ancestor || v.ID != claimID(v) {
			return bad()
		}
	}
	for _, v := range s.EvidenceLinks {
		if v.ID != linkID(v) || v.Left != "" && !validCommitment(v.Left) || v.Right != "" && !validCommitment(v.Right) || v.FactID != "" && !validCommitment(v.FactID) {
			return bad()
		}
		// Append has one fact and an optional previous root. Union has two
		// roots. An empty commitment cannot certify missing evidence.
		if v.FactID == "" {
			if v.Left == "" || v.Right == "" {
				return bad()
			}
		} else if v.Right != "" {
			return bad()
		}
	}
	for _, v := range s.IncidentFacts {
		if identifier(v.MonitorID, "monitorId") != nil || identifier(v.Revision, "revision") != nil || identifier(v.IncidentID, "incidentId") != nil || v.ID != incidentFactID(v) {
			return bad()
		}
		switch v.Kind {
		case "run":
			if !n.known[v.NodeID] || v.Run < 1 || v.Run > maxSafe || v.RelatedID != "" || v.ObserverID != "" || v.Boundary != nil {
				return bad()
			}
		case "alias", "predecessor":
			if identifier(v.RelatedID, "relatedId") != nil || v.NodeID != "" || v.Run != 0 || v.Kind == "predecessor" && v.RelatedID == v.IncidentID || v.ObserverID != "" || v.Boundary != nil {
				return bad()
			}
		case "uncertainty":
			h := v.Boundary
			if h == nil || !n.known[v.ObserverID] || !n.known[v.NodeID] || v.Run < 1 || v.Run > maxSafe || v.RelatedID != "" {
				return bad()
			}
			// A projected canonical identity may equal a boundary's original
			// identity. Its older committed root remains a distinct proof;
			// identity equality alone neither completes nor authorizes it.
			if identifier(h.ID, "boundaryId") != nil || h.MonitorID != v.MonitorID || h.Revision != v.Revision || !validCommitment(h.EvidenceRoot) || !validTime(h.OpenedAt) || len(h.Closed) > maxClusterMembers || (h.Closed == nil) != (h.ResolvedAt == nil) {
				return bad()
			}
			if h.ResolvedAt != nil && !validTime(*h.ResolvedAt) {
				return bad()
			}
			for node, seq := range h.Closed {
				if !n.known[node] || seq < 1 || seq > maxSafe {
					return bad()
				}
			}
		default:
			return bad()
		}
	}
	return nil
}
