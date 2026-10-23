package canary

import (
	"context"
	"errors"
	"slices"
)

type RestoreActivation struct {
	NodeID        string `json:"nodeId"`
	MonitorsReset int    `json:"monitorsReset"`
	SequenceFloor int64  `json:"sequenceFloor"`
	ActivatedAt   int64  `json:"activatedAt"`
}

// ActivateRestoredDatabase requires the latest independently retained origin
// state. It resets every live monitoring generation because a backup cannot
// prove continuity across its missing interval. It does not start any I/O.
func ActivateRestoredDatabase(ctx context.Context, database, identityDirectory string) (RestoreActivation, error) {
	if ctx.Err() != nil {
		return RestoreActivation{}, errors.New("Restore activation cancelled")
	}
	if err := recoveryDatabase(database); err != nil {
		return RestoreActivation{}, err
	}
	// The offline planner needs a valid internal credential but never starts
	// peer/probe work. This ephemeral value is neither returned nor persisted.
	n, err := newNode(Options{DBPath: database, IdentityDirectory: identityDirectory, ClusterToken: newID() + newID(), Port: Ptr(0)}, true)
	if err != nil {
		return RestoreActivation{}, err
	}
	defer n.Stop()
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, i := range n.episodes {
		if i.OpenedAt > n.now() {
			return RestoreActivation{}, errors.New("Correct the node clock before activating restored history")
		}
	}
	ids := make([]string, 0, len(n.heads))
	for id := range n.heads {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	versions := []Version{}
	edges := []RevisionEdge{}
	monitorsReset := 0
	for _, id := range ids {
		reset := false
		for _, head := range n.heads[id] {
			m := head.Monitor
			if m.Deleted {
				continue
			}
			// Preserve conflicts as conflicts: each candidate gets its own child.
			// Never select an arbitrary candidate or infer missing parent proof.
			m.Revision, m.CheckGeneration, m.UpdatedAt = newID(), "", n.now()
			if m.Type == "push" {
				m.PushStartedAt = m.UpdatedAt
			}
			e := newRevisionEdge("monitor", m.ID, m.Revision, m, []string{head.Monitor.Revision}, m.Revision)
			versions = append(versions, Version{Monitor: m, Ancestors: e.Parents, EdgeID: e.ID})
			edges = append(edges, e)
			reset = true
		}
		if reset {
			monitorsReset++
		}
	}
	if ctx.Err() != nil {
		return RestoreActivation{}, errors.New("Restore activation cancelled")
	}
	if err = n.applySnapshot(Snapshot{Versions: versions, RevisionEdges: edges}); err != nil {
		return RestoreActivation{}, errors.New("Cannot reset restored monitoring generations")
	}
	n.retireSuperseded()
	if n.fault != nil {
		return RestoreActivation{}, errors.New("Cannot retire restored monitoring continuity")
	}
	// Persist a consumed reservation floor with activation. Even if the origin
	// directory is later lost, the new latest DB cannot reuse the pre-restore
	// reservation's numbers. Future DB rollback still requires retained state.
	sequence, err := n.nextProbeSequence()
	if err != nil {
		return RestoreActivation{}, errors.New("Cannot reserve a fresh restored sequence")
	}
	result := RestoreActivation{NodeID: n.ID, MonitorsReset: monitorsReset, SequenceFloor: sequence, ActivatedAt: n.now()}
	tx, err := n.store.db.BeginTx(ctx, nil)
	if err != nil {
		return RestoreActivation{}, errors.New("Restore activation did not commit")
	}
	defer tx.Rollback()
	err = put(tx, "settings", "sequence", sequence)
	for id, a := range n.accounts {
		if err != nil {
			break
		}
		a.AuthVersion++
		a.PendingTOTP, a.PendingTOTPExpires = "", 0
		if a.MFAEnabled {
			// A rolled-back replay counter cannot authorize a code from the
			// current ±1-step window again. Keep the seed; wait for a new step.
			a.TOTPLastStep = max(a.TOTPLastStep, n.now()/30000+1)
		}
		err = put(tx, "accounts", id, a)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM records WHERE category='sessions'")
	}
	if err == nil {
		err = put(tx, "settings", "restoreActivation", result)
	}
	if err == nil {
		err = put(tx, "settings", "restoreQuarantined", false)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return RestoreActivation{}, errors.New("Restore activation did not commit")
	}
	n.sequence = sequence
	return result, nil
}

func activateRestoreCLI(ctx context.Context, args []string) (any, error) {
	if len(args) != 2 && (len(args) != 4 || args[2] != "--identity-directory") {
		return nil, errors.New("Usage: activate-restore <quarantined-database-file> [--identity-directory <retained-directory>]")
	}
	directory := ""
	if len(args) == 4 {
		directory = args[3]
	}
	return ActivateRestoredDatabase(ctx, args[1], directory)
}
