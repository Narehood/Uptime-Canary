package rollup_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"uptime-canary/apps/node-go/internal/rollup"
)

// reviewReference expands each subtree separately into a flat list of nodes and
// enabled paths. Health comes from active leaves and terminal active groups;
// it never folds child results, caches counts, or uses production/private test
// helpers. This intentionally quadratic reference is only for small forests.
func reviewReference(nodes []rollup.Node, observations []rollup.Observation) map[string]rollup.Result {
	children := make(map[string][]rollup.Node)
	evidence := make(map[string]rollup.Observation)
	for _, n := range nodes {
		if n.ParentID != "" {
			children[n.ParentID] = append(children[n.ParentID], n)
		}
	}
	for _, o := range observations {
		evidence[o.ID] = o
	}
	rank := map[rollup.State]int{rollup.Up: 1, rollup.Pending: 2, rollup.Unknown: 3, rollup.Down: 4}
	out := make(map[string]rollup.Result, len(nodes))
	for _, root := range nodes {
		r := rollup.Result{State: rollup.Up, DirectChildCount: len(children[root.ID])}
		if root.Enabled {
			for _, child := range children[root.ID] {
				if child.Enabled {
					r.ActiveChildCount++
				}
			}
		}
		type entry struct {
			node   rollup.Node
			active bool
		}
		stack := []entry{{root, root.Enabled}}
		for len(stack) != 0 {
			item := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			n := item.node
			if n.ID != root.ID {
				r.DescendantCount++
			}
			if n.Group {
				hasEnabledChild := false
				for _, child := range children[n.ID] {
					hasEnabledChild = hasEnabledChild || child.Enabled
					stack = append(stack, entry{child, item.active && child.Enabled})
				}
				if item.active && !hasEnabledChild && rank[r.State] < rank[rollup.Pending] {
					r.State = rollup.Pending
				}
				continue
			}
			r.LeafCount++
			if !item.active {
				r.PausedCount++
				continue
			}
			r.ActiveLeafCount++
			state := rollup.Unknown
			if o, present := evidence[n.ID]; present {
				if o.Fresh {
					state = o.State
				}
				if o.Maintenance {
					r.MaintenanceCount++
				}
			}
			if rank[state] > rank[r.State] {
				r.State = state
			}
			switch state {
			case rollup.Up:
				r.UpCount++
			case rollup.Down:
				r.DownCount++
			case rollup.Pending:
				r.PendingCount++
			case rollup.Unknown:
				r.UnknownCount++
			}
		}
		if !root.Enabled {
			r.State = rollup.Paused
		}
		r.Maintenance = r.MaintenanceCount != 0
		out[root.ID] = r
	}
	return out
}

// A valid n-node parent chain reaches a root in at most n visited nodes. This
// bounded walk detects arbitrary cycles without the compiler's Kahn traversal.
func reviewValid(nodes []rollup.Node) bool {
	byID := make(map[string]rollup.Node)
	for _, n := range nodes {
		if _, duplicate := byID[n.ID]; n.ID == "" || duplicate {
			return false
		}
		byID[n.ID] = n
	}
	for _, n := range nodes {
		for steps := 0; ; steps++ {
			if steps == len(nodes) {
				return false
			}
			if n.ParentID == "" {
				break
			}
			parent, exists := byID[n.ParentID]
			if !exists || !parent.Group {
				return false
			}
			n = parent
		}
	}
	return true
}

func reviewAssertResults(t *testing.T, got, want map[string]rollup.Result) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("results differ:\ngot  %#v\nwant %#v", got, want)
	}
	for id, r := range got {
		if r.LeafCount != r.ActiveLeafCount+r.PausedCount ||
			r.ActiveLeafCount != r.UpCount+r.DownCount+r.PendingCount+r.UnknownCount ||
			r.MaintenanceCount < 0 || r.MaintenanceCount > r.ActiveLeafCount ||
			r.Maintenance != (r.MaintenanceCount > 0) {
			t.Fatalf("count invariant at %q: %+v", id, r)
		}
	}
}

func TestReviewExhaustiveThreeNodeGraphs(t *testing.T) {
	ids := []string{"A", "a", "\x00"}
	permutations := [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	valid, invalid := 0, 0
	for parents := 0; parents < 64; parents++ {
		for groups := 0; groups < 8; groups++ {
			for enabled := 0; enabled < 8; enabled++ {
				nodes := make([]rollup.Node, 3)
				obs := []rollup.Observation{}
				for i := range nodes {
					nodes[i] = rollup.Node{ID: ids[i], Group: groups&(1<<i) != 0, Enabled: enabled&(1<<i) != 0}
					if parent := (parents >> (2 * i)) & 3; parent != 0 {
						nodes[i].ParentID = ids[parent-1]
					}
					if !nodes[i].Group {
						obs = append(obs, rollup.Observation{ID: ids[i], State: []rollup.State{rollup.Up, rollup.Down, rollup.Pending, rollup.Unknown}[(parents+groups+i)%4], Fresh: (enabled+i)%2 == 0, Maintenance: (parents+i)%2 == 0})
					}
				}
				isValid := reviewValid(nodes)
				var want map[string]rollup.Result
				if isValid {
					valid++
					want = reviewReference(nodes, obs)
				} else {
					invalid++
				}
				for _, order := range permutations {
					ordered := []rollup.Node{nodes[order[0]], nodes[order[1]], nodes[order[2]]}
					before := slices.Clone(ordered)
					p, err := rollup.Compile(ordered)
					validation := rollup.Validate(ordered)
					if (err == nil) != isValid || (validation == nil) != isValid || (!isValid && p != nil) || !slices.Equal(before, ordered) {
						t.Fatalf("parents=%d groups=%d enabled=%d order=%v: valid=%t compile=%v validate=%v", parents, groups, enabled, order, isValid, err, validation)
					}
					if !isValid {
						continue
					}
					got, err := p.Evaluate(obs)
					if err != nil {
						t.Fatal(err)
					}
					reviewAssertResults(t, got, want)
					slices.Reverse(obs)
				}
			}
		}
	}
	t.Logf("4096 graph/flag combinations, 6 orders each: %d valid, %d invalid", valid, invalid)
}

func TestReviewEvidenceAndSubtreeOracle(t *testing.T) {
	// Missing evidence plus every health/freshness/maintenance combination.
	variants := []*rollup.Observation{nil}
	for _, state := range []rollup.State{rollup.Up, rollup.Down, rollup.Pending, rollup.Unknown} {
		for _, fresh := range []bool{false, true} {
			for _, maintenance := range []bool{false, true} {
				variants = append(variants, &rollup.Observation{State: state, Fresh: fresh, Maintenance: maintenance})
			}
		}
	}
	for flags := 0; flags < 32; flags++ {
		nodes := []rollup.Node{
			{ID: "r", Group: true, Enabled: flags&1 != 0},
			{ID: "branch", ParentID: "r", Group: true, Enabled: flags&2 != 0},
			{ID: "empty", ParentID: "branch", Group: true, Enabled: flags&4 != 0},
			{ID: "left", ParentID: "r", Enabled: flags&8 != 0},
			{ID: "right", ParentID: "branch", Enabled: flags&16 != 0},
			{ID: "off", ParentID: "r", Group: true},
			{ID: "hidden", ParentID: "off", Enabled: true},
		}
		for _, left := range variants {
			for _, right := range variants {
				obs := []rollup.Observation{{ID: "hidden", State: rollup.Down, Fresh: true, Maintenance: true}}
				for i, variant := range []*rollup.Observation{left, right} {
					if variant != nil {
						o := *variant
						o.ID = []string{"left", "right"}[i]
						obs = append(obs, o)
					}
				}
				want := reviewReference(nodes, obs)
				for repeat := 0; repeat < 2; repeat++ {
					beforeNodes, beforeObs := slices.Clone(nodes), slices.Clone(obs)
					p, err := rollup.Compile(nodes)
					if err != nil {
						t.Fatal(err)
					}
					got, err := p.Evaluate(obs)
					if err != nil || !slices.Equal(nodes, beforeNodes) || !slices.Equal(obs, beforeObs) {
						t.Fatal("evaluation failed or mutated input", err)
					}
					reviewAssertResults(t, got, want)
					slices.Reverse(nodes)
					slices.Reverse(obs)
				}
			}
		}
	}
	t.Log("9248 evidence/enable combinations, forward and reversed node/observation orders")
}

func TestReviewErrorsDoNotReuseHealth(t *testing.T) {
	nodes := []rollup.Node{
		{ID: "r", Group: true, Enabled: true},
		{ID: "off", ParentID: "r", Group: true},
		{ID: "live", ParentID: "r", Enabled: true},
		{ID: "hidden", ParentID: "off", Enabled: true},
		{ID: "paused", ParentID: "r"},
	}
	p, err := rollup.Compile(nodes)
	if err != nil {
		t.Fatal(err)
	}
	healthyObs := []rollup.Observation{{ID: "live", State: rollup.Up, Fresh: true}}
	healthy, err := p.Evaluate(healthyObs)
	if err != nil || healthy["r"].State != rollup.Up {
		t.Fatal("healthy baseline", err)
	}
	cases := [][]rollup.Observation{}
	for _, fresh := range []bool{false, true} {
		for _, id := range []string{"live", "hidden", "paused"} {
			for _, state := range []rollup.State{rollup.Paused, "", "UP", "unknown ", "\x00"} {
				cases = append(cases, []rollup.Observation{{ID: id, State: state, Fresh: fresh, Maintenance: true}})
			}
			cases = append(cases, []rollup.Observation{{ID: id, State: rollup.Up, Fresh: fresh}, {ID: id, State: rollup.Down, Fresh: fresh}})
		}
		for _, id := range []string{"r", "off", "absent", ""} {
			cases = append(cases, []rollup.Observation{{ID: id, State: rollup.Up, Fresh: fresh}})
		}
	}
	for index, invalid := range cases {
		// Independent valid evidence before and after every invalid block tests
		// rejection even after partial per-call work, and across repeated errors.
		for _, validFirst := range []bool{false, true} {
			obs := []rollup.Observation{{ID: "hidden", State: rollup.Down, Fresh: true, Maintenance: true}}
			if validFirst {
				obs = append(obs, invalid...)
			} else {
				obs = append(slices.Clone(invalid), obs...)
			}
			before := slices.Clone(obs)
			if got, err := p.Evaluate(obs); err == nil || got != nil || !slices.Equal(obs, before) {
				t.Fatalf("case %d validFirst=%t accepted/mutated invalid observations: %+v err=%v", index, validFirst, got, err)
			}
			got, err := p.Evaluate(nil)
			if err != nil {
				t.Fatal(err)
			}
			reviewAssertResults(t, got, reviewReference(nodes, nil))
			reviewAssertResults(t, healthy, reviewReference(nodes, healthyObs))
		}
	}
	// Failed Compile calls also leave an already compiled healthy plan intact.
	for _, invalid := range [][]rollup.Node{
		{{ID: ""}}, {{ID: "x"}, {ID: "x"}}, {{ID: "x", ParentID: "absent"}},
	} {
		before := slices.Clone(invalid)
		if got, err := rollup.Compile(invalid); got != nil || err == nil || !slices.Equal(before, invalid) {
			t.Fatal("invalid compilation accepted or mutated input", err)
		}
		got, err := p.Evaluate(healthyObs)
		if err != nil {
			t.Fatal(err)
		}
		reviewAssertResults(t, got, healthy)
	}
}

func TestReviewOpaqueIDsOwnershipAndMixedConcurrency(t *testing.T) {
	ids := []string{"A", "a", " ", "\t\r\n", "\x00", "\xff\xfe", "é", "e\u0301", strings.Repeat("long/", 800)}
	nodes := []rollup.Node{{ID: "root", Group: true, Enabled: true}}
	for _, id := range ids {
		nodes = append(nodes, rollup.Node{ID: id, ParentID: "root", Enabled: true})
	}
	original := slices.Clone(nodes)
	p, err := rollup.Compile(nodes)
	if err != nil {
		t.Fatal(err)
	}
	for i := range nodes {
		nodes[i] = rollup.Node{ID: "overwritten", ParentID: "missing"}
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			obs := make([]rollup.Observation, 0, len(ids))
			for i, id := range ids {
				if (worker+i)%5 != 0 {
					obs = append(obs, rollup.Observation{ID: id, State: []rollup.State{rollup.Up, rollup.Down, rollup.Pending, rollup.Unknown}[(worker+i)%4], Fresh: (worker+i)%3 != 0, Maintenance: (worker+i)%2 == 0})
				}
			}
			want := reviewReference(original, obs)
			for repeat := 0; repeat < 40; repeat++ {
				before := slices.Clone(obs)
				got, err := p.Evaluate(obs)
				if err != nil || !reflect.DeepEqual(got, want) || !slices.Equal(before, obs) {
					t.Errorf("worker %d: independent evidence/result mismatch: %v", worker, err)
					return
				}
				for id := range got {
					delete(got, id)
				}
				got["injected"] = rollup.Result{State: rollup.Up}
				if partial, err := p.Evaluate([]rollup.Observation{{ID: "root", State: rollup.Up}}); partial != nil || err == nil {
					t.Errorf("worker %d: invalid concurrent call accepted", worker)
					return
				}
			}
			// Earlier observation backing storage is caller owned after Evaluate.
			for i := range obs {
				obs[i] = rollup.Observation{ID: "overwritten", State: rollup.Up, Fresh: true}
			}
			got, err := p.Evaluate(nil)
			if err != nil || !reflect.DeepEqual(got, reviewReference(original, nil)) {
				t.Errorf("worker %d: retained caller observation state: %v", worker, err)
			}
		}(worker)
	}
	wg.Wait()
}

func TestReviewCapacityCycleAndDisabledCut(t *testing.T) {
	nodes := make([]rollup.Node, rollup.MaxNodes)
	for i := range nodes {
		nodes[i] = rollup.Node{ID: fmt.Sprint(i), ParentID: fmt.Sprint((i + 1) % len(nodes)), Group: true}
	}
	if p, err := rollup.Compile(nodes); p != nil || err == nil || errors.Is(err, rollup.ErrCapacity) {
		t.Fatal("full-size disabled cycle must fail graph validation, not admission", err)
	}
	cut := len(nodes) / 2
	for i := range nodes {
		nodes[i].ParentID = ""
		if i > 0 {
			nodes[i].ParentID = nodes[i-1].ID
		}
		nodes[i].Enabled = i != cut
		nodes[i].Group = i != len(nodes)-1
	}
	obs := []rollup.Observation{{ID: nodes[len(nodes)-1].ID, State: rollup.Down, Fresh: true, Maintenance: true}}
	for order := 0; order < 2; order++ {
		p, err := rollup.Compile(nodes)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Evaluate(obs)
		if err != nil || len(got) != len(nodes) {
			t.Fatal("full-size cut chain evaluation", err)
		}
		for i := 0; i < len(nodes); i++ {
			want := rollup.Result{State: rollup.Down, Maintenance: true, DescendantCount: len(nodes) - i - 1, LeafCount: 1, ActiveLeafCount: 1, DownCount: 1, MaintenanceCount: 1}
			if i != len(nodes)-1 {
				want.DirectChildCount, want.ActiveChildCount = 1, 1
			}
			if i <= cut {
				want.State = rollup.Pending
				want.ActiveLeafCount, want.DownCount, want.MaintenanceCount, want.Maintenance = 0, 0, 0, false
				want.PausedCount = 1
				if i == cut {
					want.State = rollup.Paused
				}
				if i == cut || i == cut-1 {
					want.ActiveChildCount = 0
				}
			}
			if got[fmt.Sprint(i)] != want {
				t.Fatalf("order %d index %d: got %+v want %+v", order, i, got[fmt.Sprint(i)], want)
			}
		}
		if partial, err := p.Evaluate(make([]rollup.Observation, rollup.MaxObservations+1)); partial != nil || !errors.Is(err, rollup.ErrCapacity) {
			t.Fatal("observation overflow did not fail atomically with ErrCapacity", err)
		}
		after, err := p.Evaluate(nil)
		if err != nil || after[obs[0].ID].State != rollup.Unknown || after[obs[0].ID].Maintenance {
			t.Fatal("capacity error reused old down/maintenance evidence", err)
		}
		slices.Reverse(nodes)
	}
	tooMany := append(slices.Clone(nodes), rollup.Node{ID: "extra"})
	if p, err := rollup.Compile(tooMany); p != nil || !errors.Is(err, rollup.ErrCapacity) {
		t.Fatal("node overflow did not fail atomically with ErrCapacity", err)
	}
	if err := rollup.Validate(tooMany); !errors.Is(err, rollup.ErrCapacity) {
		t.Fatal("Validate capacity is not compile-equivalent", err)
	}
}
