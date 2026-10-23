package maintenance

import (
	"fmt"
	"sort"
	"time"
)

// minuteRange is inclusive. In runs its coordinates are indexes in minutes;
// in wallPiece they are local minutes since the civil day's midnight.
type minuteRange struct{ lo, hi int }
type wallPiece struct {
	minuteRange
	offset int
}

// Compile validates all inputs and builds a finite immutable plan. Dense cron
// starts are compressed into runs before processing dates. The output is merged
// as it is produced; event/window limits are checked before growing storage.
func Compile(c Config) (*Plan, error) {
	s, err := checkShape(c)
	if err != nil {
		return nil, err
	}
	start, err := civilBoundary(s.start, s.loc)
	if err != nil {
		return nil, err
	}
	end, err := civilBoundary(s.end.AddDate(0, 0, 1), s.loc)
	if err != nil {
		return nil, err
	}
	duration := int64(c.DurationMinutes) * 60
	runs := startRuns(s.minutes, c.DurationMinutes)
	plan := &Plan{}
	events := 0
	var pieces [maxZoneSegments]wallPiece
	for day, dayIndex := s.start, 0; !day.After(s.end); day, dayIndex = day.AddDate(0, 0, 1), dayIndex+1 {
		if c.Strategy == Once && dayIndex > 0 {
			break
		}
		if !s.matchesDay(c, day, dayIndex) {
			continue
		}
		n, err := dayPieces(day, s.loc, &pieces)
		if err != nil {
			return nil, err
		}
		for _, piece := range pieces[:n] {
			// Zone pieces are chronological UTC segments. Only the first
			// occurrence of a repeated wall minute is present in the pieces.
			first := sort.SearchInts(s.minutes, piece.lo)
			past := sort.SearchInts(s.minutes, piece.hi+1)
			if first == past {
				continue
			}
			firstRun := sort.Search(len(runs), func(i int) bool { return runs[i].hi >= first })
			for _, run := range runs[firstRun:] {
				if run.lo >= past {
					break
				}
				events++
				if events > MaxCompileEvents {
					return nil, fmt.Errorf("%w: more than %d compressed spans", ErrLimit, MaxCompileEvents)
				}
				lo, hi := max(run.lo, first), min(run.hi, past-1)
				x := span{
					start: day.Unix() + int64(s.minutes[lo]*60-piece.offset),
					end:   day.Unix() + int64(s.minutes[hi]*60-piece.offset) + duration,
				}
				x.start, x.end = max(x.start, start), min(x.end, end)
				if x.end <= x.start {
					continue
				}
				if err := plan.appendSpan(x); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(plan.windows) == 0 {
		return nil, ErrNoOccurrences
	}
	return plan, nil
}

func (s shape) matchesDay(c Config, day time.Time, index int) bool {
	switch c.Strategy {
	case Once, Daily:
		return true
	case Interval:
		return index%c.IntervalDays == 0
	case Weekly:
		iso := (int(day.Weekday())+6)%7 + 1
		return s.weekdays[iso]
	case Monthly:
		return s.monthdays[day.Day()] || (c.LastDayOfMonth && day.AddDate(0, 0, 1).Day() == 1)
	case Cron:
		return s.cron.matchesDay(day)
	}
	return false
}

func startRuns(minutes []int, duration int) []minuteRange {
	runs := make([]minuteRange, 0, len(minutes))
	for i, minute := range minutes {
		if i > 0 && minute-minutes[i-1] <= duration {
			runs[len(runs)-1].hi = i
		} else {
			runs = append(runs, minuteRange{i, i})
		}
	}
	return runs
}

func (p *Plan) appendSpan(x span) error {
	if n := len(p.windows); n > 0 {
		last := &p.windows[n-1]
		if x.start < last.start {
			return invalid("timezone produces non-monotonic first wall-time occurrences")
		}
		if x.start <= last.end {
			last.end = max(last.end, x.end)
			return nil
		}
	}
	if len(p.windows) == MaxWindows {
		return fmt.Errorf("%w: more than %d disconnected windows; shorten date bounds", ErrLimit, MaxWindows)
	}
	// Explicit capacity growth prevents an amortized append from reserving
	// beyond the published maximum, including just before the limit is hit.
	if len(p.windows) == cap(p.windows) {
		capacity := min(MaxWindows, max(16, cap(p.windows)*2))
		grown := make([]span, len(p.windows), capacity)
		copy(grown, p.windows)
		p.windows = grown
	}
	p.windows = append(p.windows, x)
	return nil
}

// zoneSegments covers every possible instant for this civil day for offsets
// within +/-48h. IANA offsets are smaller; reject any impossible larger offset.
// ZoneBounds jumps directly to transitions, without hourly/minute probing.
func zoneSegments(day time.Time, loc *time.Location, visit func(start, end int64, offset int) error) error {
	cursor, limit := day.Add(-48*time.Hour), day.Add(72*time.Hour)
	for count := 0; cursor.Before(limit); count++ {
		if count == maxZoneSegments {
			return fmt.Errorf("%w: too many timezone transitions around a day", ErrLimit)
		}
		local := cursor.In(loc)
		_, offset := local.Zone()
		if offset < -48*3600 || offset > 48*3600 {
			return invalid("timezone offset exceeds supported bounds")
		}
		_, end := local.ZoneBounds()
		if !end.IsZero() && !end.After(cursor) {
			// Go's POSIX-rule extrapolator (time/zoneinfo.go:tzset)
			// reports yearStart+365d as the final interval end even in
			// a leap year. On Dec 31 it can therefore return a stale
			// bound. Its final offset remains valid until Jan 1 UTC.
			// Repair only that exact sentinel; never loop or scan minutes.
			year := cursor.UTC().Year()
			startOfYear := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			nextYear := startOfYear.AddDate(1, 0, 0)
			badEnd := startOfYear.Add(365 * 24 * time.Hour)
			if end.Equal(badEnd) && badEnd.Before(nextYear) && cursor.Before(nextYear) {
				end = nextYear
			}
		}
		if end.IsZero() || end.After(limit) {
			end = limit
		}
		if !end.After(cursor) {
			return invalid("timezone transition failed to advance")
		}
		if err := visit(cursor.Unix(), end.Unix(), offset); err != nil {
			return err
		}
		cursor = end
	}
	return nil
}

// civilBoundary is the first instant at or after the requested local midnight.
// Missing midnights advance to the first valid local time. A wholly skipped
// calendar date advances to the next date; repeated midnight chooses the first.
func civilBoundary(day time.Time, loc *time.Location) (int64, error) {
	var result int64
	found := false
	err := zoneSegments(day, loc, func(start, end int64, offset int) error {
		candidate := max(start, day.Unix()-int64(offset))
		if !found && candidate < end {
			result, found = candidate, true
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, invalid("cannot resolve local date boundary")
	}
	return result, nil
}

// dayPieces maps local start minutes onto fixed-offset UTC segments. Subtracting
// minutes covered by earlier segments removes the second occurrence in a fold.
// Entirely skipped dates yield no pieces. All scratch storage has fixed bounds.
func dayPieces(day time.Time, loc *time.Location, output *[maxZoneSegments]wallPiece) (int, error) {
	var covered [maxZoneSegments]minuteRange
	var remainder, next [maxZoneSegments + 1]minuteRange
	coveredCount, outputCount := 0, 0
	dayStart := day.Unix()
	err := zoneSegments(day, loc, func(start, end int64, offset int) error {
		lo := max(start+int64(offset), dayStart)
		hi := min(end+int64(offset), dayStart+86400)
		if lo >= hi {
			return nil
		}
		original := minuteRange{int((lo - dayStart + 59) / 60), int((hi - dayStart - 1) / 60)}
		if original.lo > original.hi {
			return nil
		}
		remainder[0] = original
		remaining := 1
		for _, previous := range covered[:coveredCount] {
			nextCount := 0
			for _, r := range remainder[:remaining] {
				if previous.hi < r.lo || previous.lo > r.hi {
					next[nextCount] = r
					nextCount++
					continue
				}
				if previous.lo > r.lo {
					next[nextCount] = minuteRange{r.lo, previous.lo - 1}
					nextCount++
				}
				if previous.hi < r.hi {
					next[nextCount] = minuteRange{previous.hi + 1, r.hi}
					nextCount++
				}
			}
			remainder, next, remaining = next, remainder, nextCount
		}
		for _, r := range remainder[:remaining] {
			if outputCount == len(output) {
				return fmt.Errorf("%w: too many local-time pieces", ErrLimit)
			}
			output[outputCount] = wallPiece{r, offset}
			outputCount++
		}
		covered[coveredCount] = original
		coveredCount++
		return nil
	})
	return outputCount, err
}
