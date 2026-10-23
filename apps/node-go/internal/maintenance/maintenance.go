// Package maintenance compiles finite, timezone-aware maintenance schedules into
// immutable plans. It does not run timers or decide which resources to suppress.
package maintenance

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA zones available in minimal/Alpine deployments.
)

// Strategy selects the recurrence rule. Interval means local calendar days,
// anchored to StartDate, rather than elapsed 24-hour periods.
type Strategy string

const (
	Once     Strategy = "once"
	Interval Strategy = "interval"
	Daily    Strategy = "daily"
	Weekly   Strategy = "weekly"
	Monthly  Strategy = "monthly"
	Cron     Strategy = "cron"
)

// Compile limits apply before growing output storage or enumerating more spans.
const (
	MaxSpanDays        = 3660
	MaxDurationMinutes = 31 * 24 * 60
	MaxWindows         = 65536
	MaxCompileEvents   = 250000
	MaxCronBytes       = 256
	maxZoneSegments    = 32
)

var (
	ErrInvalidConfig = errors.New("invalid maintenance configuration")
	ErrLimit         = errors.New("maintenance compilation limit exceeded")
	ErrNoOccurrences = errors.New("maintenance has no occurrences within its date bounds")
)

// Config has no enable flag: the wrapping resource owns enablement and targets.
// StartDate and EndDate are required inclusive YYYY-MM-DD calendar dates in
// Timezone. DurationMinutes is elapsed time, even across timezone transitions.
// Weekdays uses ISO weekdays: Monday=1 through Sunday=7.
type Config struct {
	Strategy        Strategy `json:"strategy"`
	Timezone        string   `json:"timezone"`
	StartDate       string   `json:"startDate"`
	EndDate         string   `json:"endDate"`
	StartTime       string   `json:"startTime,omitempty"`
	DurationMinutes int      `json:"durationMinutes"`
	IntervalDays    int      `json:"intervalDays,omitempty"`
	Weekdays        []int    `json:"weekdays,omitempty"`
	DaysOfMonth     []int    `json:"daysOfMonth,omitempty"`
	LastDayOfMonth  bool     `json:"lastDayOfMonth,omitempty"`
	Cron            string   `json:"cron,omitempty"`
}

// State describes the current merged [Start, End) window. Start and End are nil
// when inactive. NextStart is the next disjoint window, including while active.
// All returned timestamps are UTC and owned by the caller.
type State struct {
	Active    bool       `json:"active"`
	Start     *time.Time `json:"start,omitempty"`
	End       *time.Time `json:"end,omitempty"`
	NextStart *time.Time `json:"nextStart,omitempty"`
}

type span struct{ start, end int64 }

// Plan is immutable after Compile and safe for concurrent Evaluate calls. Its
// zero value is invalid. Compile once per configuration revision, outside locks.
type Plan struct{ windows []span }

// Validate performs the same work and checks as Compile, then discards the plan.
// It is not merely shape validation; see README.md for its bounded cost.
func Validate(c Config) error {
	_, err := Compile(c)
	return err
}

// Evaluate uses only now supplied by the caller. Windows include their start and
// exclude their end; touching and overlapping occurrences have already merged.
func (p *Plan) Evaluate(now time.Time) (State, error) {
	if p == nil || len(p.windows) == 0 {
		return State{}, fmt.Errorf("%w: uncompiled plan", ErrInvalidConfig)
	}
	utcYear := now.UTC().Year()
	if now.IsZero() || utcYear < 1 || utcYear > 9999 {
		return State{}, fmt.Errorf("%w: now must be a nonzero year 1..9999 instant", ErrInvalidConfig)
	}
	// End timestamps have whole-second precision. Unix seconds preserves the
	// half-open rule for fractional instants on either side of a boundary.
	n := now.Unix()
	i := sort.Search(len(p.windows), func(i int) bool { return p.windows[i].end > n })
	state := State{}
	if i < len(p.windows) && p.windows[i].start <= n {
		state.Active = true
		state.Start = utcPointer(p.windows[i].start)
		state.End = utcPointer(p.windows[i].end)
		i++
	}
	if i < len(p.windows) {
		state.NextStart = utcPointer(p.windows[i].start)
	}
	return state, nil
}

func utcPointer(seconds int64) *time.Time {
	t := time.Unix(seconds, 0).UTC()
	return &t
}

type shape struct {
	start, end time.Time // Civil dates represented in UTC, never actual instants.
	loc        *time.Location
	minutes    []int
	weekdays   [8]bool
	monthdays  [32]bool
	cron       *cronSpec
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, args...))
}

func checkShape(c Config) (shape, error) {
	var s shape
	var err error
	switch c.Strategy {
	case Once, Interval, Daily, Weekly, Monthly, Cron:
	default:
		return s, invalid("strategy must be once, interval, daily, weekly, monthly, or cron")
	}
	if c.DurationMinutes < 1 || c.DurationMinutes > MaxDurationMinutes {
		return s, invalid("durationMinutes must be 1..%d", MaxDurationMinutes)
	}
	if s.start, err = civilDate(c.StartDate); err != nil {
		return s, invalid("startDate must be YYYY-MM-DD, year 1970..9998")
	}
	if s.end, err = civilDate(c.EndDate); err != nil {
		return s, invalid("endDate must be YYYY-MM-DD, year 1970..9998")
	}
	if s.end.Before(s.start) || int(s.end.Sub(s.start)/(24*time.Hour)) >= MaxSpanDays {
		return s, invalid("inclusive date range must be 1..%d days", MaxSpanDays)
	}
	// time.Duration saturates for dates >~290 years apart. The saturated
	// positive result still exceeds the 3660-day limit above.
	if !validZoneName(c.Timezone) {
		return s, invalid("timezone must be an explicit IANA zone name or UTC")
	}
	s.loc, err = time.LoadLocation(c.Timezone)
	if err != nil {
		return s, invalid("unknown timezone %q", c.Timezone)
	}
	if c.Strategy != Interval && c.IntervalDays != 0 {
		return s, invalid("intervalDays is only valid for interval")
	}
	if c.Strategy != Weekly && len(c.Weekdays) != 0 {
		return s, invalid("weekdays is only valid for weekly")
	}
	if c.Strategy != Monthly && (len(c.DaysOfMonth) != 0 || c.LastDayOfMonth) {
		return s, invalid("daysOfMonth and lastDayOfMonth are only valid for monthly")
	}
	if c.Strategy != Cron && c.Cron != "" {
		return s, invalid("cron is only valid for cron strategy")
	}
	if c.Strategy == Cron {
		if c.StartTime != "" {
			return s, invalid("startTime must be omitted for cron")
		}
		s.cron, err = parseCron(c.Cron)
		if err != nil {
			return s, err
		}
		s.minutes = s.cron.minutesOfDay()
		return s, nil
	}
	minute, err := parseClock(c.StartTime)
	if err != nil {
		return s, err
	}
	s.minutes = []int{minute}
	switch c.Strategy {
	case Once, Daily:
	case Interval:
		if c.IntervalDays < 1 || c.IntervalDays > MaxSpanDays {
			return s, invalid("intervalDays must be 1..%d", MaxSpanDays)
		}
	case Weekly:
		if len(c.Weekdays) < 1 || len(c.Weekdays) > 7 {
			return s, invalid("weekly requires 1..7 unique weekdays")
		}
		for _, d := range c.Weekdays {
			if d < 1 || d > 7 || s.weekdays[d] {
				return s, invalid("weekdays must be unique ISO weekdays 1..7")
			}
			s.weekdays[d] = true
		}
	case Monthly:
		if len(c.DaysOfMonth) > 31 || (len(c.DaysOfMonth) == 0 && !c.LastDayOfMonth) {
			return s, invalid("monthly requires daysOfMonth or lastDayOfMonth")
		}
		for _, d := range c.DaysOfMonth {
			if d < 1 || d > 31 || s.monthdays[d] {
				return s, invalid("daysOfMonth must be unique days 1..31")
			}
			s.monthdays[d] = true
		}
	}
	return s, nil
}

func civilDate(value string) (time.Time, error) {
	if len(value) != 10 {
		return time.Time{}, errors.New("date length")
	}
	d, err := time.Parse("2006-01-02", value)
	if err != nil || d.Year() < 1970 || d.Year() > 9998 {
		return time.Time{}, errors.New("date format or year")
	}
	return d, nil
}

func validZoneName(name string) bool {
	if name == "" || len(name) > 128 || name == "Local" || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("/_+-", c)) {
			return false
		}
	}
	return true
}

func parseClock(value string) (int, error) {
	if len(value) != 5 || value[2] != ':' || value[0] < '0' || value[0] > '9' || value[1] < '0' || value[1] > '9' || value[3] < '0' || value[3] > '9' || value[4] < '0' || value[4] > '9' {
		return 0, invalid("startTime must be HH:MM")
	}
	h, m := int(value[0]-'0')*10+int(value[1]-'0'), int(value[3]-'0')*10+int(value[4]-'0')
	if h > 23 || m > 59 {
		return 0, invalid("startTime must be 00:00..23:59")
	}
	return h*60 + m, nil
}
