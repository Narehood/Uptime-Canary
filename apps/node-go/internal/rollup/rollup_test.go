package rollup_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"testing"

	"uptime-canary/apps/node-go/internal/rollup"
)

func evaluate(t *testing.T, nodes []rollup.Node, observations []rollup.Observation) map[string]rollup.Result {
	t.Helper()
	p, err := rollup.Compile(nodes)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Evaluate(observations)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLeafEvidence(t *testing.T) {
	for _, state := range []rollup.State{rollup.Up, rollup.Down, rollup.Pending, rollup.Unknown} {
		for _, fresh := range []bool{false, true} {
			for _, enabled := range []bool{false, true} {
				for _, maintenance := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/fresh=%t/enabled=%t/maintenance=%t", state, fresh, enabled, maintenance), func(t *testing.T) {
						nodes := []rollup.Node{{ID: "m", Enabled: enabled}}
						obs := []rollup.Observation{{ID: "m", State: state, Fresh: fresh, Maintenance: maintenance}}
						got := evaluate(t, nodes, obs)["m"]
						want := rollup.Result{State: rollup.Paused, LeafCount: 1, PausedCount: 1}
						if enabled {
							want = rollup.Result{State: rollup.Unknown, LeafCount: 1, ActiveLeafCount: 1}
							if fresh {
								want.State = state
							}
							switch want.State {
							case rollup.Up:
								want.UpCount = 1
							case rollup.Down:
								want.DownCount = 1
							case rollup.Pending:
								want.PendingCount = 1
							case rollup.Unknown:
								want.UnknownCount = 1
							}
							want.Maintenance = maintenance
							if maintenance {
								want.MaintenanceCount = 1
							}
						}
						if got != want {
							t.Fatalf("got %+v, want %+v", got, want)
						}
					})
				}
			}
		}
	}
	got := evaluate(t, []rollup.Node{{ID: "m", Enabled: true}}, nil)["m"]
	if got != (rollup.Result{State: rollup.Unknown, LeafCount: 1, ActiveLeafCount: 1, UnknownCount: 1}) {
		t.Fatalf("missing evidence: %+v", got)
	}
}

func TestGroupPrecedenceAndConservativeEmptyBranches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		children []rollup.Node
		obs      []rollup.Observation
		want     rollup.Result
	}{
		{name: "empty", want: rollup.Result{State: rollup.Pending}},
		{name: "all paused", children: []rollup.Node{{ID: "a"}, {ID: "b"}}, want: rollup.Result{State: rollup.Pending, DirectChildCount: 2, DescendantCount: 2, LeafCount: 2, PausedCount: 2}},
		{name: "enabled empty group", children: []rollup.Node{{ID: "a", Group: true, Enabled: true}}, want: rollup.Result{State: rollup.Pending, DirectChildCount: 1, ActiveChildCount: 1, DescendantCount: 1}},
		{name: "paused empty group", children: []rollup.Node{{ID: "a", Group: true}}, want: rollup.Result{State: rollup.Pending, DirectChildCount: 1, DescendantCount: 1}},
		{name: "up", children: []rollup.Node{{ID: "a", Enabled: true}, {ID: "b"}}, obs: []rollup.Observation{{ID: "a", State: rollup.Up, Fresh: true}}, want: rollup.Result{State: rollup.Up, DirectChildCount: 2, ActiveChildCount: 1, DescendantCount: 2, LeafCount: 2, ActiveLeafCount: 1, UpCount: 1, PausedCount: 1}},
		{name: "pending beats up", children: []rollup.Node{{ID: "a", Enabled: true}, {ID: "b", Enabled: true}}, obs: []rollup.Observation{{ID: "a", State: rollup.Up, Fresh: true}, {ID: "b", State: rollup.Pending, Fresh: true}}, want: rollup.Result{State: rollup.Pending, DirectChildCount: 2, ActiveChildCount: 2, DescendantCount: 2, LeafCount: 2, ActiveLeafCount: 2, UpCount: 1, PendingCount: 1}},
		{name: "unknown beats pending", children: []rollup.Node{{ID: "a", Enabled: true}, {ID: "b", Enabled: true}}, obs: []rollup.Observation{{ID: "a", State: rollup.Pending, Fresh: true}}, want: rollup.Result{State: rollup.Unknown, DirectChildCount: 2, ActiveChildCount: 2, DescendantCount: 2, LeafCount: 2, ActiveLeafCount: 2, PendingCount: 1, UnknownCount: 1}},
		{name: "down beats unknown", children: []rollup.Node{{ID: "a", Enabled: true}, {ID: "b", Enabled: true}}, obs: []rollup.Observation{{ID: "a", State: rollup.Down, Fresh: true}}, want: rollup.Result{State: rollup.Down, DirectChildCount: 2, ActiveChildCount: 2, DescendantCount: 2, LeafCount: 2, ActiveLeafCount: 2, DownCount: 1, UnknownCount: 1}},
		{name: "stale down is unknown", children: []rollup.Node{{ID: "a", Enabled: true}}, obs: []rollup.Observation{{ID: "a", State: rollup.Down}}, want: rollup.Result{State: rollup.Unknown, DirectChildCount: 1, ActiveChildCount: 1, DescendantCount: 1, LeafCount: 1, ActiveLeafCount: 1, UnknownCount: 1}},
		{name: "empty pending survives healthy sibling", children: []rollup.Node{{ID: "a", Group: true, Enabled: true}, {ID: "b", Enabled: true}}, obs: []rollup.Observation{{ID: "b", State: rollup.Up, Fresh: true}}, want: rollup.Result{State: rollup.Pending, DirectChildCount: 2, ActiveChildCount: 2, DescendantCount: 2, LeafCount: 1, ActiveLeafCount: 1, UpCount: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []rollup.Node{{ID: "g", Group: true, Enabled: true}}
			for _, child := range tc.children {
				child.ParentID = "g"
				nodes = append(nodes, child)
			}
			if got := evaluate(t, nodes, tc.obs)["g"]; got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNestedPausedSubtreesAndMaintenance(t *testing.T) {
	// Input deliberately lists children before parents and includes a second root.
	nodes := []rollup.Node{
		{ID: "hidden", ParentID: "off", Enabled: true},
		{ID: "up", ParentID: "deep", Enabled: true},
		{ID: "stale", ParentID: "live", Enabled: true},
		{ID: "deep", ParentID: "live", Group: true, Enabled: true},
		{ID: "off", ParentID: "root", Group: true},
		{ID: "live", ParentID: "root", Group: true, Enabled: true},
		{ID: "root", Group: true, Enabled: true},
		{ID: "independent", Enabled: true},
	}
	obs := []rollup.Observation{
		{ID: "hidden", State: rollup.Down, Fresh: true, Maintenance: true},
		{ID: "up", State: rollup.Up, Fresh: true, Maintenance: true},
		{ID: "stale", State: rollup.Up, Maintenance: true},
		{ID: "independent", State: rollup.Down, Fresh: true, Maintenance: true},
	}
	got := evaluate(t, nodes, obs)
	want := map[string]rollup.Result{
		"root":        {State: rollup.Unknown, Maintenance: true, DirectChildCount: 2, ActiveChildCount: 1, DescendantCount: 6, LeafCount: 3, ActiveLeafCount: 2, UpCount: 1, UnknownCount: 1, PausedCount: 1, MaintenanceCount: 2},
		"off":         {State: rollup.Paused, DirectChildCount: 1, DescendantCount: 1, LeafCount: 1, PausedCount: 1},
		"hidden":      {State: rollup.Down, Maintenance: true, LeafCount: 1, ActiveLeafCount: 1, DownCount: 1, MaintenanceCount: 1},
		"independent": {State: rollup.Down, Maintenance: true, LeafCount: 1, ActiveLeafCount: 1, DownCount: 1, MaintenanceCount: 1},
	}
	for id, result := range want {
		if got[id] != result {
			t.Errorf("%s: got %+v, want %+v", id, got[id], result)
		}
	}
	if expected := oracle(nodes, obs); !reflect.DeepEqual(got, expected) {
		t.Fatalf("got %+v, oracle %+v", got, expected)
	}
	for i := range obs {
		obs[i].Maintenance = false
	}
	withoutMaintenance := evaluate(t, nodes, obs)
	for id, result := range got {
		result.Maintenance, result.MaintenanceCount = false, 0
		if result != withoutMaintenance[id] {
			t.Errorf("maintenance changed health or denominator for %s", id)
		}
	}
}

func TestCompileValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nodes []rollup.Node
	}{
		{"empty id", []rollup.Node{{}}},
		{"duplicate id", []rollup.Node{{ID: "a"}, {ID: "a"}}},
		{"missing parent", []rollup.Node{{ID: "a", ParentID: "absent"}}},
		{"non-group parent", []rollup.Node{{ID: "a"}, {ID: "b", ParentID: "a"}}},
		{"self", []rollup.Node{{ID: "a", ParentID: "a", Group: true}}},
		{"cycle", []rollup.Node{{ID: "a", ParentID: "b", Group: true}, {ID: "b", ParentID: "a", Group: true}}},
		{"disconnected disabled cycle", []rollup.Node{{ID: "root", Enabled: true}, {ID: "a", ParentID: "b", Group: true}, {ID: "b", ParentID: "c", Group: true}, {ID: "c", ParentID: "a", Group: true}, {ID: "leaf", ParentID: "b"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := rollup.Compile(tc.nodes)
			validation := rollup.Validate(tc.nodes)
			if p != nil || err == nil || validation == nil || validation.Error() != err.Error() {
				t.Fatalf("compile=%v/%v validate=%v", p, err, validation)
			}
		})
	}
	// IDs are opaque, rather than silently trimmed or case-folded.
	nodes := []rollup.Node{{ID: "A", Group: true}, {ID: "a", ParentID: "A"}, {ID: " "}}
	if err := rollup.Validate(nodes); err != nil {
		t.Fatal(err)
	}
}

func TestObservationValidationIsAtomic(t *testing.T) {
	p, err := rollup.Compile([]rollup.Node{{ID: "g", Group: true, Enabled: true}, {ID: "m", ParentID: "g", Enabled: true}, {ID: "off"}})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := p.Evaluate(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, obs := range [][]rollup.Observation{
		{{ID: "absent", State: rollup.Up, Fresh: true}},
		{{ID: "", State: rollup.Up}},
		{{ID: "g", State: rollup.Up, Fresh: true}},
		{{ID: "m", State: rollup.Up, Fresh: true}, {ID: "m", State: rollup.Down, Fresh: true}},
		{{ID: "m", State: rollup.Paused, Fresh: true}},
		{{ID: "m", State: "UP", Fresh: true}},
		{{ID: "m", State: ""}},
		{{ID: "m", State: "broken"}},
		{{ID: "off", State: rollup.Paused}},
		{{ID: "off", State: rollup.Up}, {ID: "off", State: rollup.Down}},
	} {
		if got, err := p.Evaluate(obs); err == nil || got != nil {
			t.Errorf("accepted invalid observations %+v: %v, %v", obs, got, err)
		}
		if after, err := p.Evaluate(nil); err != nil || !reflect.DeepEqual(after, baseline) {
			t.Fatal("invalid evaluation changed the plan", err)
		}
	}
	var absent *rollup.Plan
	if got, err := absent.Evaluate(nil); err == nil || got != nil {
		t.Fatalf("nil plan: %v, %v", got, err)
	}
}

func TestEmptyAndZeroValuePlans(t *testing.T) {
	compiled, err := rollup.Compile(nil)
	if err != nil || rollup.Validate(nil) != nil {
		t.Fatal(err)
	}
	for _, p := range []*rollup.Plan{compiled, new(rollup.Plan)} {
		got, err := p.Evaluate(nil)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty forest: %+v, %v", got, err)
		}
		if got, err := p.Evaluate([]rollup.Observation{{ID: "x", State: rollup.Up}}); got != nil || err == nil {
			t.Fatal("empty forest accepted observation")
		}
	}
}

func TestMaximumAdmissionAndDeepUnbalancedForest(t *testing.T) {
	nodes := make([]rollup.Node, rollup.MaxNodes)
	for i := range nodes {
		nodes[i] = rollup.Node{ID: fmt.Sprint(i), Group: i < len(nodes)-1, Enabled: true}
		if i > 0 {
			nodes[i].ParentID = nodes[i-1].ID
		}
	}
	p, err := rollup.Compile(nodes)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Evaluate([]rollup.Observation{{ID: nodes[len(nodes)-1].ID, State: rollup.Down, Fresh: true}})
	if err != nil {
		t.Fatal(err)
	}
	want := rollup.Result{State: rollup.Down, DirectChildCount: 1, ActiveChildCount: 1, DescendantCount: len(nodes) - 1, LeafCount: 1, ActiveLeafCount: 1, DownCount: 1}
	if len(got) != len(nodes) || got["0"] != want {
		t.Fatalf("deep forest: root %+v, results %d", got["0"], len(got))
	}
	tooMany := append(slices.Clone(nodes), rollup.Node{ID: "extra"})
	if p, err := rollup.Compile(tooMany); p != nil || !errors.Is(err, rollup.ErrCapacity) {
		t.Fatalf("node admission: %v, %v", p, err)
	}
	if err := rollup.Validate(tooMany); !errors.Is(err, rollup.ErrCapacity) {
		t.Fatalf("validate admission: %v", err)
	}
	tooManyObs := make([]rollup.Observation, rollup.MaxObservations+1)
	if got, err := p.Evaluate(tooManyObs); got != nil || !errors.Is(err, rollup.ErrCapacity) {
		t.Fatalf("observation admission: %v, %v", got, err)
	}
	// The exact observation limit must also succeed with unique valid leaves.
	observations := make([]rollup.Observation, rollup.MaxObservations)
	for i := range nodes {
		nodes[i].Group, nodes[i].ParentID = false, ""
		observations[i] = rollup.Observation{ID: nodes[i].ID, State: rollup.Up, Fresh: true}
	}
	p, err = rollup.Compile(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Evaluate(observations); err != nil || len(got) != len(nodes) || got["0"].State != rollup.Up {
		t.Fatalf("exact observation limit: count %d, %v", len(got), err)
	}
}

func TestOwnershipAndConcurrentEvaluation(t *testing.T) {
	nodes := []rollup.Node{{ID: "g", Group: true, Enabled: true}, {ID: "m", ParentID: "g", Enabled: true}}
	obs := []rollup.Observation{{ID: "m", State: rollup.Down, Fresh: true, Maintenance: true}}
	originalNodes, originalObs := slices.Clone(nodes), slices.Clone(obs)
	p, err := rollup.Compile(nodes)
	if err != nil {
		t.Fatal(err)
	}
	want := oracle(nodes, obs)
	got, err := p.Evaluate(obs)
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(nodes, originalNodes) || !reflect.DeepEqual(obs, originalObs) {
		t.Fatal("input mutation or unexpected initial result", err)
	}
	nodes[0] = rollup.Node{ID: "changed"}
	nodes[1].ParentID = "missing"
	obs[0].State = rollup.Up
	got["g"] = rollup.Result{State: rollup.Up}
	delete(got, "m")
	got["injected"] = rollup.Result{}
	if next, err := p.Evaluate(originalObs); err != nil || !reflect.DeepEqual(next, want) {
		t.Fatal("caller mutation leaked into plan", err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 24; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			state := rollup.Up
			if worker%2 == 0 {
				state = rollup.Down
			}
			for i := 0; i < 40; i++ {
				result, err := p.Evaluate([]rollup.Observation{{ID: "m", State: state, Fresh: true}})
				if err != nil || result["g"].State != state || result["m"].State != state {
					t.Errorf("concurrent evaluation: %v, %v", result, err)
					return
				}
				delete(result, "g")
			}
		}(worker)
	}
	wg.Wait()
}

func TestJSONContract(t *testing.T) {
	encoded, err := json.Marshal(rollup.Result{State: rollup.Pending})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"state":"pending","maintenance":false,"directChildCount":0,"activeChildCount":0,"descendantCount":0,"leafCount":0,"activeLeafCount":0,"upCount":0,"downCount":0,"pendingCount":0,"unknownCount":0,"pausedCount":0,"maintenanceCount":0}`
	if string(encoded) != want {
		t.Fatalf("JSON contract: %s", encoded)
	}
}

// oracle deliberately scans ancestor paths for each possible descendant of each
// root. It does not fold child results or use the production traversal. Terminal
// enabled groups inject pending evidence independently from leaf health counts.
// It is intentionally slow and used only for small, already valid forests.
func oracle(nodes []rollup.Node, observations []rollup.Observation) map[string]rollup.Result {
	byID := make(map[string]rollup.Node)
	obs := make(map[string]rollup.Observation)
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, o := range observations {
		obs[o.ID] = o
	}
	out := make(map[string]rollup.Result)
	for _, root := range nodes {
		r := rollup.Result{State: rollup.Up}
		states := make(map[rollup.State]bool)
		for _, candidate := range nodes {
			if candidate.ParentID == root.ID {
				r.DirectChildCount++
				if root.Enabled && candidate.Enabled {
					r.ActiveChildCount++
				}
			}
			cursor, active := candidate, true
			for {
				active = active && cursor.Enabled
				if cursor.ID == root.ID || cursor.ParentID == "" {
					break
				}
				cursor = byID[cursor.ParentID]
			}
			if cursor.ID != root.ID {
				continue
			}
			if candidate.ID != root.ID {
				r.DescendantCount++
			}
			if candidate.Group {
				if active {
					hasActiveChild := false
					for _, child := range nodes {
						if child.ParentID == candidate.ID && child.Enabled {
							hasActiveChild = true
						}
					}
					if !hasActiveChild {
						states[rollup.Pending] = true
					}
				}
				continue
			}
			r.LeafCount++
			if !active {
				r.PausedCount++
				continue
			}
			r.ActiveLeafCount++
			o := obs[candidate.ID]
			state := rollup.Unknown
			if o.Fresh {
				state = o.State
			}
			states[state] = true
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
			if o.Maintenance {
				r.MaintenanceCount++
			}
		}
		switch {
		case !root.Enabled:
			r.State = rollup.Paused
		case states[rollup.Down]:
			r.State = rollup.Down
		case states[rollup.Unknown]:
			r.State = rollup.Unknown
		case states[rollup.Pending]:
			r.State = rollup.Pending
		}
		r.Maintenance = r.MaintenanceCount > 0
		out[root.ID] = r
	}
	return out
}

func TestRandomizedOracleAndOrderInvariance(t *testing.T) {
	rng := rand.New(rand.NewSource(0xCA9A))
	for trial := 0; trial < 300; trial++ {
		data := make([]byte, 4*(1+rng.Intn(48)))
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		nodes, obs := forestFromBytes(data)
		want := oracle(nodes, obs)
		for repeat := 0; repeat < 3; repeat++ {
			rng.Shuffle(len(nodes), func(i, j int) { nodes[i], nodes[j] = nodes[j], nodes[i] })
			rng.Shuffle(len(obs), func(i, j int) { obs[i], obs[j] = obs[j], obs[i] })
			if err := rollup.Validate(nodes); err != nil {
				t.Fatal(err)
			}
			got := evaluate(t, nodes, obs)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d: nodes %+v obs %+v\ngot %+v\nwant %+v", trial, nodes, obs, got, want)
			}
			for id, r := range got {
				if r.LeafCount != r.ActiveLeafCount+r.PausedCount || r.ActiveLeafCount != r.UpCount+r.DownCount+r.PendingCount+r.UnknownCount || r.MaintenanceCount > r.ActiveLeafCount {
					t.Fatalf("count invariants %s: %+v", id, r)
				}
			}
		}
	}
}

// Four bytes describe flags, parent selection, health and evidence annotation.
// Parents come from earlier groups, producing valid varied forests without
// assuming anything about the production compiler's ordering.
func forestFromBytes(data []byte) ([]rollup.Node, []rollup.Observation) {
	if len(data) > 256 {
		data = data[:256]
	}
	nodes := []rollup.Node{}
	obs := []rollup.Observation{}
	groups := []string{}
	for i := 0; i+3 < len(data); i += 4 {
		n := rollup.Node{ID: fmt.Sprint(i / 4), Group: data[i]&1 != 0, Enabled: data[i]&2 != 0}
		if len(groups) > 0 && data[i+1]&1 != 0 {
			n.ParentID = groups[int(data[i+1]/2)%len(groups)]
		}
		nodes = append(nodes, n)
		if n.Group {
			groups = append(groups, n.ID)
		} else if data[i+3]&1 != 0 {
			obs = append(obs, rollup.Observation{ID: n.ID, State: []rollup.State{rollup.Up, rollup.Down, rollup.Pending, rollup.Unknown}[data[i+2]%4], Fresh: data[i+3]&2 != 0, Maintenance: data[i+3]&4 != 0})
		}
	}
	return nodes, obs
}

func FuzzEvaluateAgainstOracle(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{3, 0, 0, 0, 2, 1, 1, 7})
	f.Add([]byte{3, 0, 0, 0, 1, 1, 0, 0, 2, 3, 1, 7, 2, 1, 0, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		nodes, obs := forestFromBytes(data)
		want := oracle(nodes, obs)
		if got := evaluate(t, nodes, obs); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, oracle %+v", got, want)
		}
		slices.Reverse(nodes)
		slices.Reverse(obs)
		if got := evaluate(t, nodes, obs); !reflect.DeepEqual(got, want) {
			t.Fatalf("order changed result: %+v, want %+v", got, want)
		}
	})
}

// validByWalking independently validates small arbitrary graphs by checking each
// ancestor chain for repeated IDs. It deliberately has no topological traversal.
func validByWalking(nodes []rollup.Node) bool {
	byID := make(map[string]rollup.Node)
	for _, n := range nodes {
		if n.ID == "" {
			return false
		}
		if _, exists := byID[n.ID]; exists {
			return false
		}
		byID[n.ID] = n
	}
	for _, start := range nodes {
		seen := make(map[string]bool)
		for current := start; ; {
			if seen[current.ID] {
				return false
			}
			seen[current.ID] = true
			if current.ParentID == "" {
				break
			}
			parent, exists := byID[current.ParentID]
			if !exists || !parent.Group {
				return false
			}
			current = parent
		}
	}
	return true
}

func FuzzCompileGraph(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 2, 1, 2, 1, 1})
	f.Add([]byte{1, 0, 1, 2, 1, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 96 {
			data = data[:96]
		}
		nodes := []rollup.Node{}
		for i := 0; i+2 < len(data); i += 3 {
			id, parent := "", ""
			if data[i]%34 != 0 {
				id = fmt.Sprint(data[i] % 34)
			}
			if data[i+1]%34 != 0 {
				parent = fmt.Sprint(data[i+1] % 34)
			}
			nodes = append(nodes, rollup.Node{ID: id, ParentID: parent, Group: data[i+2]&1 != 0, Enabled: data[i+2]&2 != 0})
		}
		wantValid := validByWalking(nodes)
		p, err := rollup.Compile(nodes)
		if (err == nil) != wantValid || (rollup.Validate(nodes) == nil) != wantValid {
			t.Fatalf("validation mismatch: nodes %+v, valid %t, err %v", nodes, wantValid, err)
		}
		if wantValid {
			got, err := p.Evaluate(nil)
			if err != nil || !reflect.DeepEqual(got, oracle(nodes, nil)) {
				t.Fatalf("valid graph: result %+v err %v", got, err)
			}
		} else if p != nil {
			t.Fatal("invalid compile returned a plan")
		}
	})
}
