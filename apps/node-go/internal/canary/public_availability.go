package canary

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"uptime-canary/apps/node-go/internal/availability"
)

const (
	publicAvailabilityWindow  int64 = 24 * 60 * 60 * 1000
	publicAvailabilityFrame   int64 = 30 * 1000
	publicAvailabilityRows          = 5000
	publicAvailabilityEntries       = 256
	publicAvailabilityTimeout       = 100 * time.Millisecond
)

// An opt-in metric contains only time/state totals. It cannot identify a node,
// private monitor, target, sample, incident, maintenance plan or error message.
type PublicObservedAvailability struct {
	Status  string                `json:"status"`
	Scope   string                `json:"scope"`
	From    int64                 `json:"from"`
	To      int64                 `json:"to"`
	Summary *availability.Totals  `json:"summary,omitempty"`
	Buckets []availability.Bucket `json:"buckets,omitempty"`
}

type publicAvailabilityKey struct {
	id, revision string
	to           int64
}

type publicAvailabilityCache struct {
	// TryLock provides one nonwaiting reader. SQL never holds the node mutex.
	mu      sync.Mutex
	entries map[publicAvailabilityKey]*PublicObservedAvailability
	keys    [publicAvailabilityEntries]publicAvailabilityKey
	next    int
}

func unavailablePublicAvailability() *PublicObservedAvailability {
	return &PublicObservedAvailability{Status: "unavailable", Scope: "serving-node"}
}

func (c *publicAvailabilityCache) remember(k publicAvailabilityKey, v *PublicObservedAvailability) {
	if c.entries == nil {
		c.entries = make(map[publicAvailabilityKey]*PublicObservedAvailability)
	}
	if _, present := c.entries[k]; !present {
		delete(c.entries, c.keys[c.next])
		c.keys[c.next] = k
		c.next = (c.next + 1) % len(c.keys)
	}
	c.entries[k] = v
}

// readPublicAvailability consumes a request-wide row budget, including rows
// outside the clipped range. One additional sentinel detects overflow; no
// incomplete prefix is ever reported as coverage. Both queries use the
// (monitor_id,start_ms) primary key, avoiding a scan/sort of retained history.
func readPublicAvailability(ctx context.Context, tx *sql.Tx, id string, from, to int64, left *int) *PublicObservedAvailability {
	bad := unavailablePublicAvailability
	if *left <= 0 || ctx.Err() != nil {
		return bad()
	}
	spans := make([]availability.Span, 0, min(*left, 128))
	var prior availability.Span
	err := tx.QueryRowContext(ctx, `SELECT start_ms,end_ms,state,maintenance FROM availability_spans
 WHERE monitor_id=? AND start_ms<? ORDER BY start_ms DESC LIMIT 1`, id, from).
		Scan(&prior.StartMs, &prior.EndMs, &prior.State, &prior.Maintenance)
	if err != nil && err != sql.ErrNoRows {
		return bad()
	}
	if err == nil {
		*left--
		if prior.EndMs > from {
			spans = append(spans, prior)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT start_ms,end_ms,state,maintenance FROM availability_spans
 WHERE monitor_id=? AND start_ms>=? AND start_ms<? ORDER BY start_ms LIMIT ?`, id, from, to, *left+1)
	if err != nil {
		return bad()
	}
	defer rows.Close()
	for rows.Next() {
		if *left == 0 {
			return bad()
		}
		*left--
		var span availability.Span
		if rows.Scan(&span.StartMs, &span.EndMs, &span.State, &span.Maintenance) != nil {
			return bad()
		}
		// Bound capacity as well as length; append's automatic growth can
		// otherwise allocate beyond the accepted request row count.
		if len(spans) == cap(spans) {
			grown := make([]availability.Span, len(spans), min(publicAvailabilityRows, max(1, 2*cap(spans))))
			copy(grown, spans)
			spans = grown
		}
		spans = append(spans, span)
	}
	if rows.Err() != nil || ctx.Err() != nil {
		return bad()
	}
	summary, err := availability.Summarize(from, to, spans)
	if err != nil {
		return bad()
	}
	buckets, err := availability.Buckets(from, to, 60*60*1000, spans)
	if err != nil || ctx.Err() != nil {
		return bad()
	}
	return &PublicObservedAvailability{Status: "available", Scope: "serving-node", From: from, To: to, Summary: &summary, Buckets: buckets}
}

// fillPublicAvailability runs only after an eligible public projection exists.
// Opt-out requests do no SQL or cache work. Cache hits expire by the rounded
// observation-window end, never by a client timestamp. Publication and monitor
// proofs are checked again after SQL so cache contents cannot grant authority.
func (n *Node) fillPublicAvailability(parent context.Context, page *PublicStatusPage) bool {
	keys := make([]publicAvailabilityKey, 0)
	now := page.GeneratedAt
	to := now - now%publicAvailabilityFrame
	for _, g := range page.Groups {
		for _, m := range g.Monitors {
			if m.availabilityID != "" {
				keys = append(keys, publicAvailabilityKey{m.availabilityID, m.availabilityRevision, to})
			}
		}
	}
	if len(keys) == 0 || now < publicAvailabilityWindow || now > availability.MaxTimeMs {
		return true
	}
	c := &n.publicAvailability
	values := make(map[publicAvailabilityKey]*PublicObservedAvailability)
	if c.mu.TryLock() {
		func() {
			defer c.mu.Unlock()
			ctx, cancel := context.WithTimeout(parent, publicAvailabilityTimeout)
			defer cancel()
			var tx *sql.Tx
			left := publicAvailabilityRows
			defer func() {
				if tx != nil {
					_ = tx.Rollback()
				}
			}()
			for _, k := range keys {
				if v := c.entries[k]; v != nil {
					values[k] = v
					continue
				}
				v := unavailablePublicAvailability()
				if left > 0 && ctx.Err() == nil {
					if tx == nil {
						tx, _ = n.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
					}
					if tx != nil {
						v = readPublicAvailability(ctx, tx, k.id, max(0, to-publicAvailabilityWindow), to, &left)
					}
				}
				values[k] = v
				c.remember(k, v)
			}
		}()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	current := n.publicStatusPage(page.Slug)
	if current == nil || current.publicationKey != page.publicationKey {
		return false
	}
	for i := range current.Groups {
		for j := range current.Groups[i].Monitors {
			m := &current.Groups[i].Monitors[j]
			k := publicAvailabilityKey{m.availabilityID, m.availabilityRevision, to}
			// A frame boundary or clock rollback during the read cannot make
			// the returned metric older/newer than its declared projection frame.
			if current.GeneratedAt-current.GeneratedAt%publicAvailabilityFrame == to && m.availabilityID != "" && values[k] != nil {
				m.Availability = values[k]
			}
		}
	}
	*page = *current
	return true
}
