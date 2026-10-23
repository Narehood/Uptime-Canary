package canary

// Rebuilt only after successful configuration publication, never by a public
// lookup. Even an unready or disabled candidate reserves its token; a conflict
// or duplicate entity uses the empty sentinel and cannot become an arbitrary
// winner based on head order or a scan limit.
func (n *Node) rebuildPushTokenIndex() {
	index := map[string]string{}
	for id, heads := range n.heads {
		for _, version := range heads {
			m := version.Monitor
			if m.Deleted || m.Type != "push" || m.Push == nil {
				continue
			}
			key := digest(m.Push.Token)
			if len(heads) != 1 {
				index[key] = ""
				continue
			}
			if previous, exists := index[key]; exists && previous != id {
				index[key] = ""
			} else if !exists {
				index[key] = id
			}
		}
	}
	n.pushTokenIndex = index
}

// Called while n.mu is held. The derived index includes every live candidate,
// including unresolved proof/conflicts, so account creation cannot promote a
// passive-monitor credential into a management bearer.
func (n *Node) pushCredentialInUse(token string) bool {
	_, exists := n.pushTokenIndex[digest(token)]
	return exists
}
