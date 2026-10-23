// Package availability measures time covered by explicit observed validity spans.
// It never extends an observation beyond its supplied end or fills gaps as up.
// All intervals are half-open Unix milliseconds; see README.md for integration
// requirements and the limits enforced before processing.
package availability

import "fmt"

// State is the observed state throughout a Span. Other strings are invalid.
type State string

const (
	StateUp      State = "up"
	StateDown    State = "down"
	StatePending State = "pending"
	StateUnknown State = "unknown"
)

const (
	// MaxTimeMs is the largest supported Unix millisecond timestamp and the
	// largest safe integer for a JavaScript number.
	MaxTimeMs int64 = 1<<53 - 1
	// MinStepMs and MaxStepMs bound epoch-aligned bucket widths. A day is
	// exactly 24 hours here; buckets do not follow local calendars or DST.
	MinStepMs int64 = 1
	MaxStepMs int64 = 365 * 24 * 60 * 60 * 1000
	// MaxSpans counts all supplied spans, including those outside the query.
	MaxSpans = 100_000
	// MaxBuckets includes partial buckets at both edges of the query.
	MaxBuckets = 4_096
)

// Span is an explicit caller-supplied observation valid on [StartMs, EndMs).
// Maintenance annotates only this span and does not change its observed state.
type Span struct {
	StartMs     int64 `json:"startMs"`
	EndMs       int64 `json:"endMs"`
	State       State `json:"state"`
	Maintenance bool  `json:"maintenance"`
}

// Totals describes [StartMs, EndMs). UpMs + DownMs + PendingMs + UnknownMs
// equals EndMs - StartMs. MaintenanceMs is an independent overlapping measure.
// KnownMs is UpMs + DownMs. AvailabilityPercent is nil when KnownMs is zero;
// otherwise it is 100 * UpMs / KnownMs. CoveragePercent is 100 * KnownMs /
// (EndMs - StartMs). Maintenance never changes either denominator.
type Totals struct {
	StartMs             int64    `json:"startMs"`
	EndMs               int64    `json:"endMs"`
	UpMs                int64    `json:"upMs"`
	DownMs              int64    `json:"downMs"`
	PendingMs           int64    `json:"pendingMs"`
	UnknownMs           int64    `json:"unknownMs"`
	MaintenanceMs       int64    `json:"maintenanceMs"`
	KnownMs             int64    `json:"knownMs"`
	AvailabilityPercent *float64 `json:"availabilityPercent"`
	CoveragePercent     float64  `json:"coveragePercent"`
}

// Bucket embeds Totals, so its JSON fields are flat and identical to Totals.
// Edge buckets describe only their intersection with the requested query.
type Bucket struct {
	Totals
}

// Summarize clips explicit spans to [startMs, endMs), counting gaps as unknown.
// The query and every supplied span must have 0 <= start < end <= MaxTimeMs.
// At most MaxSpans spans may be supplied, sorted and disjoint; touching spans
// are allowed, overlaps and duplicates are rejected even outside the query.
// The function neither sorts nor mutates inputs and has no retained state.
func Summarize(startMs, endMs int64, spans []Span) (Totals, error) {
	if err := validateQuery(startMs, endMs, len(spans)); err != nil {
		return Totals{}, err
	}
	if err := validateSpans(spans); err != nil {
		return Totals{}, err
	}
	totals, _ := summarizeRange(startMs, endMs, spans, 0)
	if totals.KnownMs > 0 {
		percent := 100 * (float64(totals.UpMs) / float64(totals.KnownMs))
		totals.AvailabilityPercent = &percent
	}
	return totals, nil
}

// Buckets partitions [startMs, endMs) at UTC Unix epoch multiples of stepMs.
// The first and last bucket may be partial. It accepts the same spans and query
// as Summarize, requires MinStepMs <= stepMs <= MaxStepMs, and rejects queries
// producing more than MaxBuckets before allocating the result. It validates
// every supplied span and performs a linear sweep over spans and buckets.
// On error it returns nil, never a partial result.
func Buckets(startMs, endMs, stepMs int64, spans []Span) ([]Bucket, error) {
	if err := validateQuery(startMs, endMs, len(spans)); err != nil {
		return nil, err
	}
	if stepMs < MinStepMs || stepMs > MaxStepMs {
		return nil, fmt.Errorf("availability: stepMs must be in [%d, %d]", MinStepMs, MaxStepMs)
	}
	// endMs is positive. Subtracting one keeps an exact end boundary out of
	// the following bucket, without computing endMs+stepMs or multiplying.
	count := (endMs-1)/stepMs - startMs/stepMs + 1
	if count > MaxBuckets {
		return nil, fmt.Errorf("availability: query requires %d buckets; at most %d allowed", count, MaxBuckets)
	}
	if err := validateSpans(spans); err != nil {
		return nil, err
	}
	result := make([]Bucket, int(count))
	var percentages []float64
	spanIndex := 0
	for i, cursor := 0, startMs; i < len(result); i++ {
		end := endMs
		// Add only when the next boundary lies strictly within the query,
		// keeping arithmetic within the already validated timestamp range.
		if distance := stepMs - cursor%stepMs; distance < endMs-cursor {
			end = cursor + distance
		}
		result[i].Totals, spanIndex = summarizeRange(cursor, end, spans, spanIndex)
		if result[i].KnownMs > 0 {
			if percentages == nil {
				percentages = make([]float64, len(result))
			}
			percentages[i] = 100 * (float64(result[i].UpMs) / float64(result[i].KnownMs))
			result[i].AvailabilityPercent = &percentages[i]
		}
		cursor = end
	}
	return result, nil
}

func validateQuery(startMs, endMs int64, spanCount int) error {
	if startMs < 0 || endMs > MaxTimeMs || endMs <= startMs {
		return fmt.Errorf("availability: query must satisfy 0 <= startMs < endMs <= %d", MaxTimeMs)
	}
	if spanCount > MaxSpans {
		return fmt.Errorf("availability: at most %d spans allowed", MaxSpans)
	}
	return nil
}

func validateSpans(spans []Span) error {
	for i, span := range spans {
		if span.StartMs < 0 || span.EndMs > MaxTimeMs || span.EndMs <= span.StartMs {
			return fmt.Errorf("availability: span %d must satisfy 0 <= startMs < endMs <= %d", i, MaxTimeMs)
		}
		switch span.State {
		case StateUp, StateDown, StatePending, StateUnknown:
		default:
			return fmt.Errorf("availability: span %d has invalid state", i)
		}
		if i > 0 && span.StartMs < spans[i-1].EndMs {
			return fmt.Errorf("availability: span %d is unsorted or overlaps the previous span", i)
		}
	}
	return nil
}

// summarizeRange consumes spans ending within the range and retains an index
// to a span crossing its end. Disjoint spans limit total intersections across
// all buckets to O(len(spans) + len(buckets)). Validated disjoint durations
// and all intermediate sums are bounded by the query length <= MaxTimeMs.
func summarizeRange(startMs, endMs int64, spans []Span, index int) (Totals, int) {
	totals := Totals{StartMs: startMs, EndMs: endMs, UnknownMs: endMs - startMs}
	for index < len(spans) && spans[index].EndMs <= startMs {
		index++
	}
	for index < len(spans) && spans[index].StartMs < endMs {
		span := spans[index]
		start, end := max(startMs, span.StartMs), min(endMs, span.EndMs)
		duration := end - start
		switch span.State {
		case StateUp:
			totals.UpMs += duration
			totals.UnknownMs -= duration
		case StateDown:
			totals.DownMs += duration
			totals.UnknownMs -= duration
		case StatePending:
			totals.PendingMs += duration
			totals.UnknownMs -= duration
		}
		if span.Maintenance {
			totals.MaintenanceMs += duration
		}
		if span.EndMs > endMs {
			break
		}
		index++
	}
	totals.KnownMs = totals.UpMs + totals.DownMs
	totals.CoveragePercent = 100 * (float64(totals.KnownMs) / float64(endMs-startMs))
	return totals, index
}
