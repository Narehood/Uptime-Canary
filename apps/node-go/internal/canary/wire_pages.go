package canary

import (
	"encoding/json"
	"slices"
	"uptime-canary/apps/node-go/internal/heartbeat"
)

const syncPagesPerRound = 4
const pageRecords = 512
const pageTargetBytes = 256 * 1024

// New semantics must not be silently discarded by an older peer decoder.
const peerProtocol = 15

type syncProgress struct{ Send, Receive string }
type pageRecord struct {
	key   string
	value any
}

// Stable keyset pagination needs no server-side session or full-snapshot
// response. An interrupted sweep resumes at its last acknowledged key. New
// keys behind a cursor and mutations to visited records arrive next sweep.
// Records are never retired: receipts, aliases, frontiers and predecessors
// remain available to arbitrarily delayed peers.
func (n *Node) snapshotPage(after, pull string) (Snapshot, error) {
	if err := n.ensureCausalFacts(); err != nil {
		return Snapshot{}, err
	}
	page := Snapshot{Protocol: peerProtocol, Node: Identity{n.ID, n.Name}, Versions: []Version{}, GroupVersions: []GroupVersion{}, Resources: []ResourceVersion{}, Intents: []NotificationIntent{}, Observations: []Sample{}, Incidents: []Incident{}, Receipts: []Receipt{}, After: pull}
	records := []pageRecord{}
	add := func(key string, value any) {
		if key > after {
			records = append(records, pageRecord{key, value})
		}
	}
	for id, heads := range n.heads {
		for _, v := range heads {
			add("0/"+id+"/"+v.Monitor.Revision, v)
		}
	}
	for id, heads := range n.groups {
		for _, v := range heads {
			add("1/"+id+"/"+v.Group.Revision, v)
		}
	}
	for key, heads := range n.resources {
		for _, v := range heads {
			add("1a/"+key+"/"+v.Resource.Revision, v)
		}
	}
	for id, intent := range n.intents {
		add("1b/"+id, intent)
	}
	for id, e := range n.revisionEdges {
		add("1c/"+id, e)
	}
	for id, c := range n.revisionClaims {
		add("1d/"+id, c)
	}
	for id, event := range n.pushEvents {
		add("1e/"+id, event)
	}
	for id, f := range n.incidentFacts {
		add("2a/"+id, f)
	}
	for id, l := range n.evidenceLinks {
		add("2b/"+id, l)
	}
	// Delivery evidence precedes incidents and observations so a reconnecting
	// receiver normally learns receipts before evaluating new outage evidence.
	for id, r := range n.receipts {
		add("2/"+id, r)
	}
	for id, i := range n.incidentSources {
		add("3/"+id, i)
	}
	for id, i := range n.pendingIncidents {
		add("3a/"+id, i)
	}
	for id, o := range n.samples {
		add("4/"+id, o)
	}
	slices.SortFunc(records, func(a, b pageRecord) int { return stringsCompare(a.key, b.key) })
	// Reserve envelope/cursor overhead. A large individual record may exceed
	// the soft target but the encoded HTTP body always remains below 1 MiB.
	size := 4096
	for index, r := range records {
		b, err := json.Marshal(r.value)
		if err != nil {
			return page, err
		}
		if len(b)+4096 > maxBody {
			return page, fail(503, "Individual peer record exceeds page capacity")
		}
		if index > 0 && (index >= pageRecords || size+len(b)+1 > pageTargetBytes) {
			page.Next = records[index-1].key
			break
		}
		size += len(b) + 1
		switch value := r.value.(type) {
		case Version:
			page.Versions = append(page.Versions, value)
		case GroupVersion:
			page.GroupVersions = append(page.GroupVersions, value)
		case ResourceVersion:
			page.Resources = append(page.Resources, value)
		case NotificationIntent:
			page.Intents = append(page.Intents, value)
		case Receipt:
			page.Receipts = append(page.Receipts, value)
		case Incident:
			page.Incidents = append(page.Incidents, value)
		case Sample:
			page.Observations = append(page.Observations, value)
		case RevisionEdge:
			page.RevisionEdges = append(page.RevisionEdges, value)
		case RevisionClaim:
			page.RevisionClaims = append(page.RevisionClaims, value)
		case IncidentFact:
			page.IncidentFacts = append(page.IncidentFacts, value)
		case EvidenceLink:
			page.EvidenceLinks = append(page.EvidenceLinks, value)
		case heartbeat.Event:
			page.Heartbeats = append(page.Heartbeats, value)
		}
	}
	return page, nil
}
