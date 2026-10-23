// Package rollup compiles an immutable monitor forest and evaluates current
// caller-supplied health evidence. It has no clock, storage, or network effects.
package rollup

import (
	"errors"
	"fmt"
)

// Admission limits bound this computation, not replicated configuration.
const (
	MaxNodes        = 100_000
	MaxObservations = 100_000
)

// ErrCapacity means a computation limit was exceeded. A caller integrating this
// leaf must present unknown health on this error, never truncate the hierarchy
// or reject an otherwise valid replication union to fit these limits.
var ErrCapacity = errors.New("rollup capacity exceeded")

// State is a health state. Paused is produced only by compiled disabled nodes;
// observations must use Up, Down, Pending, or Unknown.
type State string

const (
	Up      State = "up"
	Down    State = "down"
	Pending State = "pending"
	Unknown State = "unknown"
	Paused  State = "paused"
)

// Node belongs to a forest. IDs are opaque, nonempty, case-sensitive strings.
// An empty ParentID denotes a root; only a Group may have children.
// Enabled is explicit: its zero value is disabled.
type Node struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	Group    bool   `json:"group"`
	Enabled  bool   `json:"enabled"`
}

// Observation is the caller's current evidence for a non-group node. Fresh
// applies only to State. Maintenance is a current parallel annotation, even
// when health evidence is stale. Missing evidence means unknown and no supplied
// maintenance annotation. Group observations and observed Paused are invalid.
type Observation struct {
	ID          string `json:"id"`
	State       State  `json:"state"`
	Fresh       bool   `json:"fresh"`
	Maintenance bool   `json:"maintenance"`
}

// Result describes a node as the root of its own subtree, independently of any
// disabled ancestors. Structural counts include disabled branches. Health and
// maintenance counts count non-group leaves, including self for a leaf. Empty
// groups contribute no leaves but still contribute their pending health to an
// enabled parent's state. All JSON fields, including zero counts, are emitted.
type Result struct {
	State            State `json:"state"`
	Maintenance      bool  `json:"maintenance"`
	DirectChildCount int   `json:"directChildCount"`
	ActiveChildCount int   `json:"activeChildCount"`
	DescendantCount  int   `json:"descendantCount"`
	LeafCount        int   `json:"leafCount"`
	ActiveLeafCount  int   `json:"activeLeafCount"`
	UpCount          int   `json:"upCount"`
	DownCount        int   `json:"downCount"`
	PendingCount     int   `json:"pendingCount"`
	UnknownCount     int   `json:"unknownCount"`
	PausedCount      int   `json:"pausedCount"`
	MaintenanceCount int   `json:"maintenanceCount"`
}

type compiledNode struct {
	id               string
	parent           int
	group            bool
	enabled          bool
	directChildCount int
	activeChildCount int
	descendantCount  int
	leafCount        int
}

// Plan is immutable after Compile and safe for concurrent Evaluate calls.
// Its zero value is an empty forest. Inputs must not be mutated concurrently
// with the call consuming them; no caller-owned slice or result is retained.
type Plan struct {
	nodes []compiledNode
	index map[string]int
	order []int // Children precede parents, constructed without recursion.
}

// Compile validates and copies the entire forest in O(n) time and space.
// It rejects empty/duplicate IDs, absent or non-group parents, self links,
// cycles, and more than MaxNodes nodes. It never repairs or truncates input.
func Compile(nodes []Node) (*Plan, error) {
	if len(nodes) > MaxNodes {
		return nil, fmt.Errorf("%w: %d nodes exceeds %d", ErrCapacity, len(nodes), MaxNodes)
	}
	p := &Plan{
		nodes: make([]compiledNode, len(nodes)),
		index: make(map[string]int, len(nodes)),
		order: make([]int, 0, len(nodes)),
	}
	for i, n := range nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("rollup node %d: empty id", i)
		}
		if _, exists := p.index[n.ID]; exists {
			return nil, fmt.Errorf("rollup node %q: duplicate id", n.ID)
		}
		p.index[n.ID] = i
		p.nodes[i] = compiledNode{id: n.ID, parent: -1, group: n.Group, enabled: n.Enabled}
		if !n.Group {
			p.nodes[i].leafCount = 1
		}
	}
	for i, n := range nodes {
		if n.ParentID == "" {
			continue
		}
		if n.ParentID == n.ID {
			return nil, fmt.Errorf("rollup node %q: self parent", n.ID)
		}
		parent, exists := p.index[n.ParentID]
		if !exists {
			return nil, fmt.Errorf("rollup node %q: missing parent %q", n.ID, n.ParentID)
		}
		if !p.nodes[parent].group {
			return nil, fmt.Errorf("rollup node %q: parent %q is not a group", n.ID, n.ParentID)
		}
		p.nodes[i].parent = parent
		p.nodes[parent].directChildCount++
		if n.Enabled && p.nodes[parent].enabled {
			p.nodes[parent].activeChildCount++
		}
	}
	remaining := make([]int, len(nodes))
	for i, n := range p.nodes {
		remaining[i] = n.directChildCount
		if remaining[i] == 0 {
			p.order = append(p.order, i)
		}
	}
	// Kahn's traversal from leaves both detects cycles and computes structural
	// totals once. A chain at the full admission limit does not grow the stack.
	for next := 0; next < len(p.order); next++ {
		i := p.order[next]
		n := p.nodes[i]
		if n.parent < 0 {
			continue
		}
		parent := &p.nodes[n.parent]
		parent.descendantCount += 1 + n.descendantCount
		parent.leafCount += n.leafCount
		remaining[n.parent]--
		if remaining[n.parent] == 0 {
			p.order = append(p.order, n.parent)
		}
	}
	if len(p.order) != len(nodes) {
		return nil, errors.New("rollup hierarchy contains a cycle")
	}
	return p, nil
}

// Validate accepts exactly the same hierarchies as Compile.
func Validate(nodes []Node) error {
	_, err := Compile(nodes)
	return err
}

// Evaluate returns a new result map, or nil and an error for invalid evidence.
// It runs in O(nodes + observations) time and space, using no clock or history.
// Disabled branches are excluded from ancestors' active totals. Enabled groups
// fold active direct children with Down > Unknown > Pending > Up precedence;
// no active children means Pending. Maintenance never changes health.
func (p *Plan) Evaluate(observations []Observation) (map[string]Result, error) {
	if len(observations) > MaxObservations {
		return nil, fmt.Errorf("%w: %d observations exceeds %d", ErrCapacity, len(observations), MaxObservations)
	}
	if p == nil {
		return nil, errors.New("rollup: nil plan")
	}
	results := make([]Result, len(p.nodes))
	for i, n := range p.nodes {
		r := &results[i]
		r.DirectChildCount = n.directChildCount
		r.ActiveChildCount = n.activeChildCount
		r.DescendantCount = n.descendantCount
		r.LeafCount = n.leafCount
		switch {
		case !n.enabled:
			r.State = Paused
			r.PausedCount = n.leafCount
		case !n.group:
			r.State = Unknown
		case n.activeChildCount == 0:
			r.State = Pending
		default:
			r.State = Up
		}
	}
	seen := make([]bool, len(p.nodes))
	for _, o := range observations {
		i, exists := p.index[o.ID]
		if !exists {
			return nil, fmt.Errorf("rollup observation %q: unknown id", o.ID)
		}
		if seen[i] {
			return nil, fmt.Errorf("rollup observation %q: duplicate id", o.ID)
		}
		seen[i] = true
		if p.nodes[i].group {
			return nil, fmt.Errorf("rollup observation %q: groups cannot have observations", o.ID)
		}
		switch o.State {
		case Up, Down, Pending, Unknown:
		default:
			return nil, fmt.Errorf("rollup observation %q: invalid state %q", o.ID, o.State)
		}
		if p.nodes[i].enabled {
			if o.Fresh {
				results[i].State = o.State
			}
			if o.Maintenance {
				results[i].MaintenanceCount = 1
			}
		}
	}
	for _, i := range p.order {
		n := p.nodes[i]
		r := &results[i]
		if n.enabled && !n.group {
			r.ActiveLeafCount = 1
			switch r.State {
			case Up:
				r.UpCount = 1
			case Down:
				r.DownCount = 1
			case Pending:
				r.PendingCount = 1
			case Unknown:
				r.UnknownCount = 1
			}
		}
		r.Maintenance = r.MaintenanceCount > 0
		if n.parent < 0 || !p.nodes[n.parent].enabled {
			continue
		}
		parent := &results[n.parent]
		parent.PausedCount += r.PausedCount
		if !n.enabled {
			continue
		}
		parent.ActiveLeafCount += r.ActiveLeafCount
		parent.UpCount += r.UpCount
		parent.DownCount += r.DownCount
		parent.PendingCount += r.PendingCount
		parent.UnknownCount += r.UnknownCount
		parent.MaintenanceCount += r.MaintenanceCount
		parent.State = worst(parent.State, r.State)
	}
	out := make(map[string]Result, len(p.nodes))
	for i, n := range p.nodes {
		out[n.id] = results[i]
	}
	return out, nil
}

func worst(a, b State) State {
	for _, state := range [...]State{Down, Unknown, Pending} {
		if a == state || b == state {
			return state
		}
	}
	return Up
}
