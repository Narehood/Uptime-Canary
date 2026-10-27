package canary

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
)

// Membership can now route private monitor/channel settings automatically.
// Authenticate both membership and worker authority against database-only edits.
func (d *Dashboard) signGroupMetadata(kind string, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, d.box.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(d.box.Seal(nonce, nonce, nil, append([]byte("canary-group-"+kind+"\x00"), data...))), nil
}
func (d *Dashboard) verifyGroupMetadata(kind, proof string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b, err := base64.RawStdEncoding.DecodeString(proof)
	if err != nil || len(b) != d.box.NonceSize()+d.box.Overhead() {
		return fail(503, "Saved node-group authority cannot be verified")
	}
	_, err = d.box.Open(nil, b[:d.box.NonceSize()], b[d.box.NonceSize():], append([]byte("canary-group-"+kind+"\x00"), data...))
	if err != nil {
		return fail(503, "Saved node-group authority cannot be verified")
	}
	return nil
}
func (d *Dashboard) signNodeGroup(group *dashboardNodeGroup) error {
	group.Proof = ""
	proof, err := d.signGroupMetadata("membership", *group)
	group.Proof = proof
	return err
}
func (d *Dashboard) verifyNodeGroup(group dashboardNodeGroup) error {
	proof := group.Proof
	group.Proof = ""
	return d.verifyGroupMetadata("membership", proof, group)
}
func (d *Dashboard) signGroupJob(job *dashboardGroupSync) error {
	job.Proof = ""
	proof, err := d.signGroupMetadata("job", *job)
	job.Proof = proof
	return err
}
func (d *Dashboard) verifyGroupJob(job dashboardGroupSync) error {
	proof := job.Proof
	job.Proof = ""
	return d.verifyGroupMetadata("job", proof, job)
}
func publicNodeGroup(group dashboardNodeGroup) dashboardNodeGroup { group.Proof = ""; return group }

// Legacy groups were inert selections and have no queued worker authority or
// encrypted definition bound to them. Migrate only those records, once; a group
// already used to route settings must always retain its valid proof.
func (d *Dashboard) loadGroupAuthority() error {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	groups, err := loadAll[dashboardNodeGroup](n.store, "dashboardNodeGroups")
	if err != nil || len(groups) > 200 {
		return fail(503, "Cannot load node groups")
	}
	jobs, err := loadAll[dashboardGroupSync](n.store, "dashboardGroupSyncs")
	if err != nil || len(jobs) > 200 {
		return fail(503, "Cannot load group sync authority")
	}
	for id, job := range jobs {
		if job.GroupID != id || !fleetPlacementID(id) {
			return fail(503, "Invalid saved group sync identity")
		}
		if err := d.verifyGroupJob(job); err != nil {
			return err
		}
	}
	for id, group := range groups {
		if group.ID != id || !fleetPlacementID(id) {
			return fail(503, "Invalid saved node-group identity")
		}
		if group.Proof == "" {
			for _, row := range d.fleet {
				for _, linked := range row.GroupIDs {
					if linked == id {
						return fail(503, "A monitor's node-group authority is missing")
					}
				}
			}
			if _, queued := jobs[id]; queued {
				return fail(503, "A queued node group's authority is missing")
			}
			if err := d.signNodeGroup(&group); err != nil {
				return err
			}
			if err := n.identityTransactionLocked(func(e dbExec) error { return put(e, "dashboardNodeGroups", id, group) }); err != nil {
				return err
			}
		} else if err := d.verifyNodeGroup(group); err != nil {
			return err
		}
	}
	return nil
}
