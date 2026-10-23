package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func daily(zone, first, last, clock string, duration int) Config {
	return Config{Strategy: Daily, Timezone: zone, StartDate: first, EndDate: last, StartTime: clock, DurationMinutes: duration}
}

func mustPlan(t testing.TB, c Config) *Plan {
	t.Helper()
	p, err := Compile(c)
	if err != nil {
		t.Fatalf("Compile(%+v): %v", c, err)
	}
	return p
}

func instant(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(err)
	}
	return t
}

func expectState(t *testing.T, p *Plan, now string, active bool, start, end, next string) {
	t.Helper()
	s, err := p.Evaluate(instant(now))
	if err != nil {
		t.Fatal(err)
	}
	if s.Active != active {
		t.Errorf("Evaluate(%s).Active = %v, want %v", now, s.Active, active)
	}
	for _, item := range []struct {
		name string
		got  *time.Time
		want string
	}{{"start", s.Start, start}, {"end", s.End, end}, {"nextStart", s.NextStart, next}} {
		if item.want == "" {
			if item.got != nil {
				t.Errorf("Evaluate(%s).%s = %s, want nil", now, item.name, item.got)
			}
		} else if item.got == nil || !item.got.Equal(instant(item.want)) || item.got.Location() != time.UTC {
			t.Errorf("Evaluate(%s).%s = %v, want UTC %s", now, item.name, item.got, item.want)
		}
	}
}

func TestOnceBoundariesAndCallerClock(t *testing.T) {
	c := daily("UTC", "2026-10-20", "2026-10-21", "23:30", 90)
	c.Strategy = Once
	p := mustPlan(t, c)
	start, end := "2026-10-20T23:30:00Z", "2026-10-21T01:00:00Z"
	expectState(t, p, "2026-10-20T23:29:59.999999999Z", false, "", "", start)
	expectState(t, p, start, true, start, end, "")
	expectState(t, p, "2026-10-21T00:59:59.999999999Z", true, start, end, "")
	expectState(t, p, end, false, "", "", "")
	// Evaluation has no memory of the previous caller's clock.
	expectState(t, p, "2026-10-21T00:30:00Z", true, start, end, "")
	expectState(t, p, "2026-10-20T23:30:00-04:00", false, "", "", "")
}

func TestEndDateClipsOvernightWindow(t *testing.T) {
	p := mustPlan(t, daily("Etc/GMT+5", "2026-10-20", "2026-10-20", "23:30", 90))
	expectState(t, p, "2026-10-21T04:45:00Z", true, "2026-10-21T04:30:00Z", "2026-10-21T05:00:00Z", "")
	expectState(t, p, "2026-10-21T05:00:00Z", false, "", "", "")
}

func TestIntervalAnchoredCalendarDaysAcrossDST(t *testing.T) {
	c := daily("America/New_York", "2026-03-06", "2026-03-12", "08:00", 30)
	c.Strategy, c.IntervalDays = Interval, 2
	p := mustPlan(t, c)
	expectState(t, p, "2026-03-07T15:00:00Z", false, "", "", "2026-03-08T12:00:00Z")
	expectState(t, p, "2026-03-08T12:00:00Z", true, "2026-03-08T12:00:00Z", "2026-03-08T12:30:00Z", "2026-03-10T12:00:00Z")
	if got := p.windows[1].start - p.windows[0].start; got != 47*3600 {
		t.Errorf("calendar interval spanning spring DST = %ds, want 47 hours", got)
	}
}

func TestWeeklyISOSelectionAndOvernight(t *testing.T) {
	c := daily("Europe/Berlin", "2026-10-19", "2026-10-26", "23:30", 120)
	c.Strategy, c.Weekdays = Weekly, []int{1, 7}
	p := mustPlan(t, c)
	expectState(t, p, "2026-10-19T22:00:00Z", true, "2026-10-19T21:30:00Z", "2026-10-19T23:30:00Z", "2026-10-25T22:30:00Z")
	expectState(t, p, "2026-10-25T22:30:00Z", true, "2026-10-25T22:30:00Z", "2026-10-26T00:30:00Z", "2026-10-26T22:30:00Z")
}

func TestMonthlyShortMonthsLeapYearAndLastDay(t *testing.T) {
	c := daily("UTC", "2024-01-01", "2024-04-30", "10:00", 60)
	c.Strategy, c.DaysOfMonth = Monthly, []int{29, 31}
	p := mustPlan(t, c)
	expectState(t, p, "2024-01-31T11:00:00Z", false, "", "", "2024-02-29T10:00:00Z")
	expectState(t, p, "2024-02-29T10:30:00Z", true, "2024-02-29T10:00:00Z", "2024-02-29T11:00:00Z", "2024-03-29T10:00:00Z")
	c.StartDate, c.EndDate, c.DaysOfMonth, c.LastDayOfMonth = "2023-01-01", "2023-04-30", []int{31}, true
	p = mustPlan(t, c)
	if len(p.windows) != 4 {
		t.Fatalf("31 plus last-day should deduplicate: %d windows", len(p.windows))
	}
	expectState(t, p, "2023-02-28T10:00:00Z", true, "2023-02-28T10:00:00Z", "2023-02-28T11:00:00Z", "2023-03-31T10:00:00Z")
	expectState(t, p, "2023-04-30T10:30:00Z", true, "2023-04-30T10:00:00Z", "2023-04-30T11:00:00Z", "")
}

func TestDSTSkippedAndRepeatedStarts(t *testing.T) {
	for _, tc := range []struct {
		name, zone, first, last, clock, now, start, end, next string
		active                                                bool
	}{
		{"ny skipped", "America/New_York", "2026-03-07", "2026-03-09", "02:30", "2026-03-08T07:30:00Z", "", "", "2026-03-09T06:30:00Z", false},
		{"ny first fold", "America/New_York", "2026-11-01", "2026-11-02", "01:30", "2026-11-01T05:30:00Z", "2026-11-01T05:30:00Z", "2026-11-01T06:00:00Z", "2026-11-02T06:30:00Z", true},
		{"ny second fold", "America/New_York", "2026-11-01", "2026-11-02", "01:30", "2026-11-01T06:30:00Z", "", "", "2026-11-02T06:30:00Z", false},
		{"europe skipped", "Europe/Berlin", "2026-03-28", "2026-03-30", "02:30", "2026-03-29T01:30:00Z", "", "", "2026-03-30T00:30:00Z", false},
		{"europe first fold", "Europe/Berlin", "2026-10-25", "2026-10-26", "02:30", "2026-10-25T00:30:00Z", "2026-10-25T00:30:00Z", "2026-10-25T01:00:00Z", "2026-10-26T01:30:00Z", true},
		{"europe second fold", "Europe/Berlin", "2026-10-25", "2026-10-26", "02:30", "2026-10-25T01:30:00Z", "", "", "2026-10-26T01:30:00Z", false},
		{"half hour fold", "Australia/Lord_Howe", "2026-04-05", "2026-04-06", "01:45", "2026-04-04T14:45:00Z", "2026-04-04T14:45:00Z", "2026-04-04T15:15:00Z", "2026-04-05T15:15:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := mustPlan(t, daily(tc.zone, tc.first, tc.last, tc.clock, 30))
			expectState(t, p, tc.now, tc.active, tc.start, tc.end, tc.next)
		})
	}
}

func TestElapsedDurationAcrossDSTAndMissingMidnight(t *testing.T) {
	for _, tc := range []struct{ zone, date, clock, start, end string }{
		{"America/New_York", "2026-03-08", "01:30", "2026-03-08T06:30:00Z", "2026-03-08T08:30:00Z"},
		{"America/New_York", "2026-11-01", "01:30", "2026-11-01T05:30:00Z", "2026-11-01T07:30:00Z"},
		{"America/Sao_Paulo", "2018-11-04", "01:00", "2018-11-04T03:00:00Z", "2018-11-04T05:00:00Z"},
	} {
		t.Run(tc.zone+tc.date, func(t *testing.T) {
			p := mustPlan(t, daily(tc.zone, tc.date, tc.date, tc.clock, 120))
			expectState(t, p, tc.start, true, tc.start, tc.end, "")
		})
	}
	// Samoa skipped an entire civil date in its 2011 date-line change.
	p := mustPlan(t, daily("Pacific/Apia", "2011-12-29", "2011-12-31", "12:00", 60))
	if len(p.windows) != 2 {
		t.Fatalf("skipped date should not normalize into another date: %d windows", len(p.windows))
	}
	expectState(t, p, "2011-12-30T00:00:00Z", false, "", "", "2011-12-30T22:00:00Z")
}

func TestOverlappingAndTouchingWindowsMerge(t *testing.T) {
	for _, duration := range []int{1440, 1500, MaxDurationMinutes} {
		p := mustPlan(t, daily("UTC", "2026-10-20", "2026-10-23", "10:00", duration))
		if len(p.windows) != 1 {
			t.Fatalf("duration %d: got %d windows, want one", duration, len(p.windows))
		}
		expectState(t, p, "2026-10-22T10:00:00Z", true, "2026-10-20T10:00:00Z", "2026-10-24T00:00:00Z", "")
	}
	// A one-hour spring shift closes an otherwise separated daily gap.
	p := mustPlan(t, daily("America/New_York", "2026-03-07", "2026-03-09", "08:00", 1380))
	expectState(t, p, "2026-03-08T12:00:00Z", true, "2026-03-07T13:00:00Z", "2026-03-09T11:00:00Z", "2026-03-09T12:00:00Z")
}

func cronConfig(expression, zone, first, last string, duration int) Config {
	return Config{Strategy: Cron, Cron: expression, Timezone: zone, StartDate: first, EndDate: last, DurationMinutes: duration}
}

func TestCronFieldsNamesRangesListsAndSteps(t *testing.T) {
	p := mustPlan(t, cronConfig("5,20-40/10 8-9/1 * OCT MON-FRI", "UTC", "2026-10-19", "2026-10-25", 5))
	expectState(t, p, "2026-10-19T08:05:00Z", true, "2026-10-19T08:05:00Z", "2026-10-19T08:10:00Z", "2026-10-19T08:20:00Z")
	expectState(t, p, "2026-10-23T09:45:00Z", false, "", "", "")
	p = mustPlan(t, cronConfig("0 12 * * 7", "UTC", "2026-10-19", "2026-10-25", 10))
	expectState(t, p, "2026-10-25T12:00:00Z", true, "2026-10-25T12:00:00Z", "2026-10-25T12:10:00Z", "")
	p = mustPlan(t, cronConfig("10/20 8 * * *", "UTC", "2026-10-20", "2026-10-20", 1))
	expectState(t, p, "2026-10-20T08:31:00Z", false, "", "", "2026-10-20T08:50:00Z")
}

func TestCronDayRulesAndLastDay(t *testing.T) {
	// Restricted day-of-month and weekday use OR, not AND.
	p := mustPlan(t, cronConfig("0 12 15 * MON", "UTC", "2026-10-14", "2026-10-20", 60))
	expectState(t, p, "2026-10-15T12:00:00Z", true, "2026-10-15T12:00:00Z", "2026-10-15T13:00:00Z", "2026-10-19T12:00:00Z")
	// A star step is a star-bearing day field, so both constraints apply.
	p = mustPlan(t, cronConfig("0 12 */2 * MON", "UTC", "2026-10-19", "2026-10-26", 60))
	if len(p.windows) != 1 {
		t.Errorf("star-bearing day constraint: %d windows, want one", len(p.windows))
	}
	p = mustPlan(t, cronConfig("0 12 L FEB,APR *", "UTC", "2024-01-01", "2024-04-30", 1))
	expectState(t, p, "2024-02-29T12:00:00Z", true, "2024-02-29T12:00:00Z", "2024-02-29T12:01:00Z", "2024-04-30T12:00:00Z")
}

func TestCronDSTMergesAndFoldGap(t *testing.T) {
	p := mustPlan(t, cronConfig("* * * * *", "America/New_York", "2026-03-08", "2026-03-08", 1))
	expectState(t, p, "2026-03-08T07:00:00Z", true, "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z", "")
	p = mustPlan(t, cronConfig("* * * * *", "America/New_York", "2026-11-01", "2026-11-01", 1))
	expectState(t, p, "2026-11-01T05:59:59Z", true, "2026-11-01T04:00:00Z", "2026-11-01T06:00:00Z", "2026-11-01T07:00:00Z")
	expectState(t, p, "2026-11-01T06:00:00Z", false, "", "", "2026-11-01T07:00:00Z")
	expectState(t, p, "2026-11-01T07:00:00Z", true, "2026-11-01T07:00:00Z", "2026-11-02T05:00:00Z", "")
	// A long duration bridges the repeated-hour gap; no invented release.
	p = mustPlan(t, cronConfig("* * * * *", "America/New_York", "2026-11-01", "2026-11-01", 61))
	if len(p.windows) != 1 {
		t.Fatalf("duration should bridge fold gap: %d windows", len(p.windows))
	}
	// A nonexistent matching cron time is skipped, as in daily recurrence.
	p = mustPlan(t, cronConfig("30 2 * * *", "Europe/Berlin", "2026-03-28", "2026-03-30", 15))
	expectState(t, p, "2026-03-29T01:30:00Z", false, "", "", "2026-03-30T00:30:00Z")
}

func TestValidationRejectsInvalidAndHostileInput(t *testing.T) {
	base := daily("UTC", "2026-10-20", "2026-10-21", "08:00", 60)
	cases := []struct {
		name string
		edit func(*Config)
	}{
		{"unknown strategy", func(c *Config) { c.Strategy = "manual" }},
		{"missing first", func(c *Config) { c.StartDate = "" }},
		{"missing last", func(c *Config) { c.EndDate = "" }},
		{"reversed dates", func(c *Config) { c.EndDate = "2026-10-19" }},
		{"wide dates", func(c *Config) { c.EndDate = "9998-12-31" }},
		{"overflow date", func(c *Config) { c.StartDate = "9999-01-01" }},
		{"short date", func(c *Config) { c.StartDate = "2026-1-01" }},
		{"bad leap date", func(c *Config) { c.StartDate = "2026-02-29" }},
		{"zero duration", func(c *Config) { c.DurationMinutes = 0 }},
		{"negative duration", func(c *Config) { c.DurationMinutes = -1 }},
		{"overflow duration", func(c *Config) { c.DurationMinutes = int(^uint(0) >> 1) }},
		{"large duration", func(c *Config) { c.DurationMinutes = MaxDurationMinutes + 1 }},
		{"missing clock", func(c *Config) { c.StartTime = "" }},
		{"bad clock", func(c *Config) { c.StartTime = "24:00" }},
		{"bad minute", func(c *Config) { c.StartTime = "10:60" }},
		{"noncanonical clock", func(c *Config) { c.StartTime = "8:00" }},
		{"clock seconds", func(c *Config) { c.StartTime = "08:00:00" }},
		{"zone empty", func(c *Config) { c.Timezone = "" }},
		{"zone local", func(c *Config) { c.Timezone = "Local" }},
		{"zone nonexistent", func(c *Config) { c.Timezone = "Mars/Olympus" }},
		{"zone traversal", func(c *Config) { c.Timezone = "../UTC" }},
		{"zone absolute", func(c *Config) { c.Timezone = "/usr/share/zoneinfo/UTC" }},
		{"zone windows path", func(c *Config) { c.Timezone = `C:\UTC` }},
		{"zone huge", func(c *Config) { c.Timezone = strings.Repeat("A", 129) }},
		{"ignored weekdays", func(c *Config) { c.Weekdays = []int{1} }},
		{"ignored monthdays", func(c *Config) { c.DaysOfMonth = []int{1} }},
		{"ignored last day", func(c *Config) { c.LastDayOfMonth = true }},
		{"ignored interval", func(c *Config) { c.IntervalDays = 1 }},
		{"ignored cron", func(c *Config) { c.Cron = "* * * * *" }},
		{"zero interval", func(c *Config) { c.Strategy = Interval }},
		{"negative interval", func(c *Config) { c.Strategy, c.IntervalDays = Interval, -1 }},
		{"huge interval", func(c *Config) { c.Strategy, c.IntervalDays = Interval, MaxSpanDays+1 }},
		{"empty weekdays", func(c *Config) { c.Strategy = Weekly }},
		{"zero weekday", func(c *Config) { c.Strategy, c.Weekdays = Weekly, []int{0} }},
		{"invalid weekday", func(c *Config) { c.Strategy, c.Weekdays = Weekly, []int{8} }},
		{"duplicate weekday", func(c *Config) { c.Strategy, c.Weekdays = Weekly, []int{1, 1} }},
		{"empty monthdays", func(c *Config) { c.Strategy = Monthly }},
		{"zero monthday", func(c *Config) { c.Strategy, c.DaysOfMonth = Monthly, []int{0} }},
		{"invalid monthday", func(c *Config) { c.Strategy, c.DaysOfMonth = Monthly, []int{32} }},
		{"duplicate monthday", func(c *Config) { c.Strategy, c.DaysOfMonth = Monthly, []int{1, 1} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.edit(&c)
			if err := Validate(c); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Validate() = %v, want ErrInvalidConfig", err)
			}
		})
	}
	for _, expression := range []string{
		"", "* * * *", "* * * * * *", "@hourly", "TZ=UTC * * * * *", "CRON_TZ=UTC * * * * *",
		"*/0 * * * *", "*/-1 * * * *", "*/999 * * * *", "60 * * * *", "-1 * * * *", "0 24 * * *", "0 0 0 * *", "0 0 * 13 *", "0 0 * * 8",
		"0 0 ? * *", "0 0 1W * *", "0 0 * * MON#2", "0 0 L/2 * *", "0 0 * * FRI-MON", "0,,1 * * * *", "0-1-2 * * * *", "0/2/3 * * * *",
		"0 0 31 FEB *", "0 0 * FOO *", "0 0 * * XXXX", "1;echo * * * *", "０ * * * *", "*\n* * * *", strings.Repeat("*", MaxCronBytes+1),
	} {
		t.Run("cron "+expression, func(t *testing.T) {
			c := cronConfig(expression, "UTC", "2026-01-01", "2026-12-31", 10)
			if err := Validate(c); err == nil {
				t.Fatal("invalid/unsupported/unsatisfiable cron accepted")
			}
		})
	}
	c := cronConfig("* * * * *", "UTC", "2026-10-20", "2026-10-20", 1)
	c.StartTime = "00:00"
	if err := Validate(c); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("cron must not silently ignore startTime")
	}
}

func TestFiniteBoundsFeasibilityAndCompileLimits(t *testing.T) {
	c := daily("UTC", "2026-01-01", "2026-01-01", "00:00", 1)
	c.EndDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, MaxSpanDays-1).Format("2006-01-02")
	if err := Validate(c); err != nil {
		t.Fatal("inclusive maximum date span must be accepted:", err)
	}
	c.EndDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, MaxSpanDays).Format("2006-01-02")
	if err := Validate(c); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("date span over maximum accepted")
	}
	c = daily("UTC", "2026-02-01", "2026-02-28", "12:00", 1)
	c.Strategy, c.DaysOfMonth = Monthly, []int{31}
	if _, err := Compile(c); !errors.Is(err, ErrNoOccurrences) {
		t.Fatalf("empty finite recurrence = %v, want ErrNoOccurrences", err)
	}
	// Dense cron is compressed before date enumeration, not expanded into
	// 5.2 million individual events and then merged.
	c = cronConfig("* * * * *", "UTC", "2026-01-01", "2035-12-31", 1)
	p := mustPlan(t, c)
	if len(p.windows) != 1 || cap(p.windows) > 16 {
		t.Fatalf("dense cron expanded output: len=%d cap=%d", len(p.windows), cap(p.windows))
	}
	// Disconnected schedules hit the limit during streaming construction.
	c.Cron = "*/2 * * * *"
	if p, err := Compile(c); p != nil || !errors.Is(err, ErrLimit) {
		t.Fatalf("disconnected cron limit = %v, plan=%v", err, p)
	}
	// Capacity must stop growing at the bound, including direct boundary use.
	synthetic := &Plan{}
	for i := 0; i < MaxWindows; i++ {
		if err := synthetic.appendSpan(span{int64(i * 2), int64(i*2 + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := synthetic.appendSpan(span{2 * MaxWindows, 2*MaxWindows + 1}); !errors.Is(err, ErrLimit) || cap(synthetic.windows) != MaxWindows {
		t.Fatal("output limit failed before capacity growth", err, cap(synthetic.windows))
	}
}

func TestPlanIsImmutableIndependentAndConcurrent(t *testing.T) {
	c := daily("UTC", "2026-10-19", "2026-10-26", "08:00", 60)
	c.Strategy, c.Weekdays = Weekly, []int{1}
	p := mustPlan(t, c)
	c.Weekdays[0] = 2
	c.StartTime = "09:00"
	// Returned timestamp pointers cannot mutate plan internals.
	s, err := p.Evaluate(instant("2026-10-19T08:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	*s.Start, *s.End, *s.NextStart = time.Time{}, time.Time{}, time.Time{}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				expectState(t, p, "2026-10-19T08:30:00Z", true, "2026-10-19T08:00:00Z", "2026-10-19T09:00:00Z", "2026-10-26T08:00:00Z")
			}
		}()
	}
	wg.Wait()
}

func TestEvaluateInvalidPlanOrClock(t *testing.T) {
	for _, p := range []*Plan{nil, {}} {
		if _, err := p.Evaluate(instant("2026-10-20T08:00:00Z")); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("zero/nil plan must return an error", err)
		}
	}
	p := mustPlan(t, daily("UTC", "2026-10-20", "2026-10-20", "08:00", 1))
	if _, err := p.Evaluate(time.Time{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("zero caller clock must be rejected", err)
	}
	if _, err := p.Evaluate(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("unserializable caller clock must be rejected", err)
	}
}

func TestCamelCaseJSONRoundTrip(t *testing.T) {
	c := daily("UTC", "2026-10-19", "2026-10-26", "08:00", 60)
	c.Strategy, c.Weekdays = Weekly, []int{1, 7}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "StartDate") || !strings.Contains(string(data), `"durationMinutes":60`) {
		t.Fatal("unexpected config JSON", string(data))
	}
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, c) {
		t.Fatal("config JSON failed round trip", err, decoded)
	}
	p := mustPlan(t, c)
	s, _ := p.Evaluate(instant("2026-10-19T08:30:00Z"))
	data, err = json.Marshal(s)
	if err != nil || string(data) != `{"active":true,"start":"2026-10-19T08:00:00Z","end":"2026-10-19T09:00:00Z","nextStart":"2026-10-25T08:00:00Z"}` {
		t.Fatalf("unexpected state JSON %s: %v", data, err)
	}
	s, _ = p.Evaluate(instant("2027-01-01T00:00:00Z"))
	data, _ = json.Marshal(s)
	if string(data) != `{"active":false}` {
		t.Fatal("expired state must omit absent timestamps", string(data))
	}
}

func ExamplePlan_Evaluate() {
	plan, err := Compile(Config{
		Strategy: Daily, Timezone: "America/New_York",
		StartDate: "2026-10-20", EndDate: "2026-10-22",
		StartTime: "23:30", DurationMinutes: 90,
	})
	if err != nil {
		panic(err)
	}
	state, _ := plan.Evaluate(instant("2026-10-22T04:00:00Z"))
	fmt.Println(state.Active, state.Start.Format(time.RFC3339), state.End.Format(time.RFC3339))
	// Output: true 2026-10-22T03:30:00Z 2026-10-22T05:00:00Z
}
