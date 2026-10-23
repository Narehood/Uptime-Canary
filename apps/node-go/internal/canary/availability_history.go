package canary

import (
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"strconv"

	"uptime-canary/apps/node-go/internal/availability"
)

const (
	availabilityRetentionMs int64 = 90 * 24 * 60 * 60 * 1000
	availabilityRecordLimit       = availability.MaxSpans
	availabilityDefaultStep int64 = 5 * 60 * 1000
)

// initializeAvailabilityStore creates only schema. In particular, old samples
// cannot establish original validity and are deliberately never imported.
// The store opener must call this helper before returning a usable store.
func initializeAvailabilityStore(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return errors.New("Cannot initialize availability history")
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS availability_heads (
 monitor_id TEXT PRIMARY KEY,
 high_water INTEGER NOT NULL CHECK(high_water BETWEEN 0 AND 9007199254740991),
 checked_at INTEGER NOT NULL,
 valid_until INTEGER NOT NULL,
 revision TEXT NOT NULL,
 generation TEXT NOT NULL,
 sequence INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('up','down','pending','unknown')),
 maintenance INTEGER NOT NULL CHECK(maintenance IN (0,1)),
 closed INTEGER NOT NULL CHECK(closed IN (0,1)),
 span_start INTEGER
) STRICT;
CREATE TABLE IF NOT EXISTS availability_spans (
 monitor_id TEXT NOT NULL,
 start_ms INTEGER NOT NULL CHECK(start_ms BETWEEN 0 AND 9007199254740991),
 end_ms INTEGER NOT NULL CHECK(end_ms > start_ms AND end_ms <= 9007199254740991),
 state TEXT NOT NULL CHECK(state IN ('up','down','pending','unknown')),
 maintenance INTEGER NOT NULL CHECK(maintenance IN (0,1)),
 PRIMARY KEY(monitor_id,start_ms)
) STRICT;`)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return errors.New("Cannot initialize availability history")
	}
	return nil
}

// recordLocalAvailability must run in the SAME caller transaction as the local
// sample, check policy, sequence and bounded sample-history writes. It neither
// opens nor commits a transaction. Root alone selects local accepted samples
// and supplies the original validity deadline, never a recomputed old policy.
// Sequence is monotonic across the database's local origin. Replays, including
// replays after closure, cannot reopen an invalidated observation.
func recordLocalAvailability(e dbExec, m Monitor, s Sample, validUntil int64, maintenance bool) error {
	if m.ID == "" || s.MonitorID != m.ID || s.NodeID == "" || !m.Enabled || m.Deleted ||
		!slices.Contains(m.NodeIDs, s.NodeID) || s.Revision != m.Revision || s.CheckGeneration != m.CheckGeneration ||
		s.Sequence < 0 || s.Sequence > availability.MaxTimeMs || s.CheckedAt < 0 || s.CheckedAt > availability.MaxTimeMs ||
		validUntil < s.CheckedAt || validUntil > availability.MaxTimeMs {
		return errors.New("Invalid local availability observation")
	}
	switch availability.State(s.State) {
	case availability.StateUp, availability.StateDown, availability.StatePending, availability.StateUnknown:
	default:
		return errors.New("Invalid local availability state")
	}
	if err := ensureAvailabilityHead(e, m.ID); err != nil {
		return err
	}
	// The effective event time is max(original checkedAt, durable high_water).
	// Only the prior FUTURE horizon is superseded. A clock rollback must never
	// erase already observed time, or shift the new original deadline forwards.
	if _, err := e.Exec(`UPDATE availability_spans SET end_ms=MIN(end_ms,
 (SELECT MAX(high_water,?) FROM availability_heads WHERE monitor_id=?))
 WHERE monitor_id=? AND start_ms=(SELECT span_start FROM availability_heads WHERE monitor_id=? AND sequence<?)
 AND start_ms<(SELECT MAX(high_water,?) FROM availability_heads WHERE monitor_id=?)`,
		s.CheckedAt, m.ID, m.ID, m.ID, s.Sequence, s.CheckedAt, m.ID); err != nil {
		return err
	}
	// An observation replaced at exactly its start has no observed duration.
	if _, err := e.Exec(`DELETE FROM availability_spans WHERE monitor_id=?
 AND start_ms=(SELECT span_start FROM availability_heads WHERE monitor_id=? AND sequence<?)
 AND start_ms=(SELECT MAX(high_water,?) FROM availability_heads WHERE monitor_id=?)`,
		m.ID, m.ID, s.Sequence, s.CheckedAt, m.ID); err != nil {
		return err
	}
	// Only an open, same-provenance head ending EXACTLY at this event can
	// coalesce. A stale deadline, explicit closure or gap always breaks a run.
	if _, err := e.Exec(`INSERT INTO availability_spans(monitor_id,start_ms,end_ms,state,maintenance)
 SELECT monitor_id,COALESCE((SELECT start_ms FROM availability_spans
  WHERE monitor_id=h.monitor_id AND start_ms=h.span_start
  AND end_ms=MAX(h.high_water,?) AND state=? AND maintenance=?
  AND h.closed=0 AND h.revision=? AND h.generation=?),MAX(high_water,?)),?,?,?
 FROM availability_heads h WHERE monitor_id=? AND sequence<? AND ?>MAX(high_water,?)
 ON CONFLICT(monitor_id,start_ms) DO UPDATE SET end_ms=excluded.end_ms`,
		s.CheckedAt, s.State, maintenance, s.Revision, s.CheckGeneration, s.CheckedAt,
		validUntil, s.State, maintenance, m.ID, s.Sequence, validUntil, s.CheckedAt); err != nil {
		return err
	}
	if _, err := e.Exec(`UPDATE availability_heads SET
 span_start=CASE WHEN ?>MAX(high_water,?) THEN
  (SELECT start_ms FROM availability_spans WHERE monitor_id=? AND end_ms=? ORDER BY start_ms DESC LIMIT 1) END,
 high_water=MAX(high_water,?),checked_at=?,valid_until=?,revision=?,generation=?,sequence=?,state=?,maintenance=?,closed=0
 WHERE monitor_id=? AND sequence<?`,
		validUntil, s.CheckedAt, m.ID, validUntil, s.CheckedAt, s.CheckedAt, validUntil,
		s.Revision, s.CheckGeneration, s.Sequence, s.State, maintenance, m.ID, s.Sequence); err != nil {
		return err
	}
	return pruneLocalAvailability(e, m.ID)
}

func ensureAvailabilityHead(e dbExec, id string) error {
	_, err := e.Exec(`INSERT INTO availability_heads
 (monitor_id,high_water,checked_at,valid_until,revision,generation,sequence,state,maintenance,closed,span_start)
 VALUES(?,0,0,0,'','',-1,'unknown',0,1,NULL) ON CONFLICT(monitor_id) DO NOTHING`, id)
	return err
}

// closeLocalAvailability is also a caller-transaction hook. Root calls it for
// disablement, deletion, assignment loss, conflicts and revision/generation
// changes, including transitions applied through peer configuration merging.
// It leaves the original deadline/provenance intact and advances the durable
// event high-water mark even when its span has expired or the head is closed.
// Never-observed IDs stay absent; closure is not evidence of an observation.
func closeLocalAvailability(e dbExec, monitorID string, at int64) error {
	if monitorID == "" || at < 0 || at > availability.MaxTimeMs {
		return errors.New("Invalid local availability closure")
	}
	if _, err := e.Exec(`UPDATE availability_spans SET end_ms=MIN(end_ms,
 (SELECT MAX(high_water,?) FROM availability_heads WHERE monitor_id=?))
 WHERE monitor_id=? AND start_ms=(SELECT span_start FROM availability_heads WHERE monitor_id=? AND closed=0)
 AND start_ms<(SELECT MAX(high_water,?) FROM availability_heads WHERE monitor_id=?)`,
		at, monitorID, monitorID, monitorID, at, monitorID); err != nil {
		return err
	}
	if _, err := e.Exec(`DELETE FROM availability_spans WHERE monitor_id=?
 AND start_ms=(SELECT span_start FROM availability_heads WHERE monitor_id=? AND closed=0)
 AND start_ms=(SELECT MAX(high_water,?) FROM availability_heads WHERE monitor_id=?)`,
		monitorID, monitorID, at, monitorID); err != nil {
		return err
	}
	if _, err := e.Exec(`UPDATE availability_heads SET high_water=MAX(high_water,?),closed=1,span_start=NULL WHERE monitor_id=?`, at, monitorID); err != nil {
		return err
	}
	return pruneLocalAvailability(e, monitorID)
}

// Retention runs only through the caller transaction. The clock high-water
// mark makes its cutoff monotonic. Reads additionally apply their own current
// 90-day cutoff, so an idle monitor never exposes expired rows.
func pruneLocalAvailability(e dbExec, id string) error {
	cutoff := `(SELECT MAX(0,high_water-?) FROM availability_heads WHERE monitor_id=?)`
	if _, err := e.Exec(`DELETE FROM availability_spans WHERE monitor_id=? AND end_ms<=`+cutoff,
		id, availabilityRetentionMs, id); err != nil {
		return err
	}
	if _, err := e.Exec(`UPDATE availability_spans SET start_ms=`+cutoff+` WHERE monitor_id=? AND start_ms<`+cutoff,
		availabilityRetentionMs, id, id, availabilityRetentionMs, id); err != nil {
		return err
	}
	if _, err := e.Exec(`DELETE FROM availability_spans WHERE monitor_id=? AND start_ms<COALESCE(
 (SELECT start_ms FROM availability_spans WHERE monitor_id=? ORDER BY start_ms DESC LIMIT 1 OFFSET ?),-1)`,
		id, id, availabilityRecordLimit-1); err != nil {
		return err
	}
	_, err := e.Exec(`UPDATE availability_heads SET span_start=
 (SELECT start_ms FROM availability_spans WHERE monitor_id=? AND start_ms=MAX(availability_heads.span_start,MAX(0,high_water-?)))
 WHERE monitor_id=?`, id, availabilityRetentionMs, id)
	return err
}

// availabilityHistoryResult is local observed-time coverage, independent of
// sample statistics. No latency or peer-derived coverage is represented.
type availabilityHistoryResult struct {
	MonitorID    string                `json:"monitorId"`
	NodeID       string                `json:"nodeId"`
	From         int64                 `json:"from"`
	To           int64                 `json:"to"`
	StepMs       int64                 `json:"stepMs"`
	RetainedFrom *int64                `json:"retainedFrom"`
	RecordLimit  int                   `json:"recordLimit"`
	RetentionMs  int64                 `json:"retentionMs"`
	Summary      availability.Totals   `json:"summary"`
	Buckets      []availability.Bucket `json:"buckets"`
}

func availabilityQuery(query url.Values, now int64) (from, to, step int64, err error) {
	to, step = now, availabilityDefaultStep
	for key, values := range query {
		if len(values) != 1 || key != "from" && key != "to" && key != "stepMs" {
			return 0, 0, 0, fail(400, "Invalid or repeated availability parameter")
		}
		value := values[0]
		if len(value) == 0 || len(value) > 16 {
			return 0, 0, 0, fail(400, "Availability parameters must be supported decimal integers")
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return 0, 0, 0, fail(400, "Availability parameters must be decimal integers")
			}
		}
		v, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || v > availability.MaxTimeMs {
			return 0, 0, 0, fail(400, "Availability parameter is outside the supported range")
		}
		switch key {
		case "from":
			from = v
		case "to":
			to = v
		case "stepMs":
			step = v
		}
	}
	if _, present := query["from"]; !present {
		from = max(0, to-24*60*60*1000)
	}
	if now < 0 || now > availability.MaxTimeMs {
		return 0, 0, 0, fail(503, "Availability history is unavailable")
	}
	if to > now || from >= to || to-from > availabilityRetentionMs ||
		step < availability.MinStepMs || step > availability.MaxStepMs {
		return 0, 0, 0, fail(400, "Invalid availability range or step; maximum range is 90 days")
	}
	if (to-1)/step-from/step+1 > availability.MaxBuckets {
		return 0, 0, 0, fail(400, "Availability query exceeds 4096 buckets")
	}
	return from, to, step, nil
}

// availabilityHistory is called by root's authenticated monitor GET route.
// Root owns authorization and monitor existence checks. This function does not
// acquire n.mu; its read transaction gives metadata and spans one SQL snapshot.
func (n *Node) availabilityHistory(id string, query url.Values) (any, error) {
	now := n.now()
	from, to, step, err := availabilityQuery(query, now)
	if err != nil {
		return nil, err
	}
	unavailable := func() (any, error) { return nil, fail(503, "Availability history is unavailable") }
	tx, err := n.store.db.Begin()
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback()
	out := availabilityHistoryResult{MonitorID: id, NodeID: n.ID, From: from, To: to, StepMs: step,
		RecordLimit: availabilityRecordLimit, RetentionMs: availabilityRetentionMs}
	cutoff := max(0, now-availabilityRetentionMs)
	var retained sql.NullInt64
	err = tx.QueryRow(`SELECT MAX(start_ms,?) FROM availability_spans
 WHERE monitor_id=? AND end_ms>? AND start_ms<? ORDER BY start_ms LIMIT 1`, cutoff, id, cutoff, now).Scan(&retained)
	if err != nil && err != sql.ErrNoRows {
		return unavailable()
	}
	if retained.Valid {
		out.RetainedFrom = Ptr(retained.Int64)
	}
	// Indexed start range plus a single predecessor, rather than scanning all
	// historical rows before from. The SQL limit and append guard both apply
	// before allocating more than MaxSpans, including for a damaged database.
	lower := max(from, cutoff)
	rows, err := tx.Query(`SELECT start_ms,end_ms,state,maintenance FROM availability_spans
 WHERE monitor_id=? AND start_ms>=? AND start_ms<?
 UNION ALL SELECT start_ms,end_ms,state,maintenance FROM
 (SELECT start_ms,end_ms,state,maintenance FROM availability_spans
  WHERE monitor_id=? AND start_ms<? ORDER BY start_ms DESC LIMIT 1) WHERE end_ms>?
 ORDER BY start_ms LIMIT ?`, id, lower, to, id, lower, lower, availabilityRecordLimit+1)
	if err != nil {
		return unavailable()
	}
	spans := make([]availability.Span, 0)
	for rows.Next() {
		if len(spans) == availabilityRecordLimit {
			rows.Close()
			return unavailable()
		}
		var span availability.Span
		if rows.Scan(&span.StartMs, &span.EndMs, &span.State, &span.Maintenance) != nil {
			rows.Close()
			return unavailable()
		}
		span.StartMs = max(span.StartMs, cutoff)
		if span.StartMs < span.EndMs && span.StartMs < to {
			if len(spans) == cap(spans) {
				// append's automatic growth can allocate capacity above the
				// accepted row cap. Bound capacity as well as result length.
				grown := make([]availability.Span, len(spans), min(availabilityRecordLimit, max(128, 2*cap(spans))))
				copy(grown, spans)
				spans = grown
			}
			spans = append(spans, span)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return unavailable()
	}
	if err = tx.Commit(); err != nil {
		return unavailable()
	}
	out.Summary, err = availability.Summarize(from, to, spans)
	if err != nil {
		return unavailable()
	}
	out.Buckets, err = availability.Buckets(from, to, step, spans)
	if err != nil {
		return unavailable()
	}
	return out, nil
}
