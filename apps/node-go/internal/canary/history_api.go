package canary

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"strconv"
)

type historyRange struct {
	From, To int64
	Limit    int
}

func (n *Node) historyRange(query url.Values, allowLimit bool) (historyRange, error) {
	r := historyRange{From: 0, To: maxSafe, Limit: 100}
	for key, values := range query {
		if len(values) != 1 || key != "from" && key != "to" && !(allowLimit && key == "limit") {
			return r, fail(400, "Invalid or repeated history parameter")
		}
		if !digits.MatchString(values[0]) {
			return r, fail(400, "History parameters must be decimal integers")
		}
		v, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || v < 0 || v > maxSafe {
			return r, fail(400, "History parameter is outside the supported range")
		}
		switch key {
		case "from":
			r.From = v
		case "to":
			r.To = v
		case "limit":
			if v < 1 || v > 1000 {
				return r, fail(400, "limit must be an integer between 1 and 1000")
			}
			r.Limit = int(v)
		}
	}
	if r.To <= r.From {
		return r, fail(400, "to must be greater than from")
	}
	return r, nil
}

func (n *Node) localHistory(id string, query url.Values) ([]Observation, error) {
	r, err := n.historyRange(query, true)
	if err != nil {
		return nil, err
	}
	rows, err := n.store.db.Query(`SELECT payload FROM history WHERE monitor_id=? AND checked_at>=? AND checked_at<? ORDER BY id DESC LIMIT ?`, id, r.From, r.To, r.Limit)
	if err != nil {
		return nil, fail(503, "History is unavailable")
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		var payload string
		var o Observation
		if rows.Scan(&payload) != nil || json.Unmarshal([]byte(payload), &o) != nil {
			return nil, fail(503, "History is unavailable")
		}
		out = append(out, o)
	}
	if rows.Err() != nil {
		return nil, fail(503, "History is unavailable")
	}
	return out, nil
}

type historyStatistics struct {
	MonitorID          string   `json:"monitorId"`
	NodeID             string   `json:"nodeId"`
	From               int64    `json:"from"`
	To                 int64    `json:"to"`
	Count              int64    `json:"count"`
	Up                 int64    `json:"up"`
	Down               int64    `json:"down"`
	Pending            int64    `json:"pending"`
	Unknown            int64    `json:"unknown"`
	LatencyCount       int64    `json:"latencyCount"`
	SampleAvailability *float64 `json:"sampleAvailability"`
	AverageLatencyMs   *float64 `json:"averageLatencyMs"`
	P50LatencyMs       *float64 `json:"p50LatencyMs"`
	P95LatencyMs       *float64 `json:"p95LatencyMs"`
	FirstCheckedAt     *int64   `json:"firstCheckedAt"`
	LastCheckedAt      *int64   `json:"lastCheckedAt"`
	RetentionMs        int64    `json:"retentionMs"`
	RecordLimit        int      `json:"recordLimit"`
}

// This is explicitly sample availability, not an inferred wall-clock SLA.
// Historical rows do not retain each prior interval/staleness policy; inventing
// time coverage from today's configuration would turn missing data healthy.
func (n *Node) historyStatistics(id string, query url.Values) (historyStatistics, error) {
	r, err := n.historyRange(query, false)
	out := historyStatistics{MonitorID: id, NodeID: n.ID, From: r.From, To: r.To, RetentionMs: n.historyRetention.Milliseconds(), RecordLimit: n.historyLimit}
	if err != nil {
		return out, err
	}
	rows, err := n.store.db.Query(`SELECT json_extract(payload,'$.state'),COUNT(*),AVG(CASE WHEN json_extract(payload,'$.state')<>'unknown' AND COALESCE(json_extract(payload,'$.latencyAvailable'),1)<>0 THEN json_extract(payload,'$.latencyMs') END),MIN(checked_at),MAX(checked_at),SUM(CASE WHEN json_extract(payload,'$.state')<>'unknown' AND COALESCE(json_extract(payload,'$.latencyAvailable'),1)<>0 THEN 1 ELSE 0 END) FROM history WHERE monitor_id=? AND checked_at>=? AND checked_at<? GROUP BY json_extract(payload,'$.state')`, id, r.From, r.To)
	if err != nil {
		return out, fail(503, "History statistics are unavailable")
	}
	totalLatency := float64(0)
	for rows.Next() {
		var state string
		var count, first, last, measured int64
		var average sql.NullFloat64
		if rows.Scan(&state, &count, &average, &first, &last, &measured) != nil {
			rows.Close()
			return out, fail(503, "History statistics are unavailable")
		}
		out.Count += count
		out.LatencyCount += measured
		if average.Valid {
			totalLatency += average.Float64 * float64(measured)
		}
		switch state {
		case "up":
			out.Up += count
		case "down":
			out.Down += count
		case "pending":
			out.Pending += count
		default:
			out.Unknown += count
		}
		if out.FirstCheckedAt == nil || first < *out.FirstCheckedAt {
			out.FirstCheckedAt = Ptr(first)
		}
		if out.LastCheckedAt == nil || last > *out.LastCheckedAt {
			out.LastCheckedAt = Ptr(last)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, fail(503, "History statistics are unavailable")
	}
	if out.Up+out.Down > 0 {
		out.SampleAvailability = Ptr(float64(out.Up) / float64(out.Up+out.Down))
	}
	if out.LatencyCount > 0 {
		out.AverageLatencyMs = Ptr(totalLatency / float64(out.LatencyCount))
		for _, item := range []struct {
			percent int64
			target  **float64
		}{{50, &out.P50LatencyMs}, {95, &out.P95LatencyMs}} {
			var latency float64
			offset := (item.percent*out.LatencyCount+99)/100 - 1
			if err = n.store.db.QueryRow(`SELECT CAST(json_extract(payload,'$.latencyMs') AS REAL) FROM history WHERE monitor_id=? AND checked_at>=? AND checked_at<? AND json_extract(payload,'$.state')<>'unknown' AND COALESCE(json_extract(payload,'$.latencyAvailable'),1)<>0 ORDER BY CAST(json_extract(payload,'$.latencyMs') AS REAL),id LIMIT 1 OFFSET ?`, id, r.From, r.To, offset).Scan(&latency); err != nil {
				return out, fail(503, "History statistics are unavailable")
			}
			*item.target = Ptr(latency)
		}
	}
	return out, nil
}
