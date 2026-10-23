package maintenance

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// slowReference deliberately walks real UTC minutes and deduplicates local
// labels. It neither resolves time.Date in a target zone nor uses production
// cron parsing, zone-piece mapping, start-run compression, or merge helpers.
// Test ranges are short and all selected zones have minute-aligned offsets.
func slowReference(t *testing.T, c Config, selected func(time.Time) bool) []span {
	t.Helper()
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := time.Parse("2006-01-02", c.StartDate)
	last, _ := time.Parse("2006-01-02", c.EndDate)
	seen := make(map[string]bool)
	var starts []int64
	cutoff := int64(0)
	for cursor := first.Add(-48 * time.Hour); cursor.Before(last.Add(96 * time.Hour)); cursor = cursor.Add(time.Minute) {
		local := cursor.In(loc)
		date := local.Format("2006-01-02")
		if cutoff == 0 && date > c.EndDate {
			cutoff = cursor.Unix()
		}
		if date < c.StartDate || date > c.EndDate {
			continue
		}
		label := local.Format("2006-01-02 15:04")
		if seen[label] {
			continue
		}
		seen[label] = true
		if selected(local) {
			starts = append(starts, cursor.Unix())
		}
	}
	var windows []span
	for _, start := range starts {
		end := start + int64(c.DurationMinutes)*60
		if end > cutoff {
			end = cutoff
		}
		if end <= start {
			continue
		}
		if len(windows) > 0 && start <= windows[len(windows)-1].end {
			if end > windows[len(windows)-1].end {
				windows[len(windows)-1].end = end
			}
		} else {
			windows = append(windows, span{start, end})
		}
	}
	return windows
}

func TestCompiledPlanMatchesIndependentUTCOracle(t *testing.T) {
	for _, dates := range []struct{ zone, first, last string }{
		{"America/New_York", "2026-03-07", "2026-03-09"},
		{"America/New_York", "2026-10-31", "2026-11-02"},
		{"Europe/Berlin", "2026-03-28", "2026-03-30"},
		{"Europe/Berlin", "2026-10-24", "2026-10-26"},
		{"Australia/Lord_Howe", "2026-04-04", "2026-04-06"},
		{"Australia/Lord_Howe", "2026-10-03", "2026-10-05"},
		{"America/Sao_Paulo", "2018-11-03", "2018-11-05"},
		{"Pacific/Apia", "2011-12-29", "2011-12-31"},
		{"Asia/Kathmandu", "2026-10-20", "2026-10-22"},
		{"Etc/GMT+5", "2026-10-20", "2026-10-22"},
		{"America/New_York", "2035-12-30", "2036-01-02"},
		{"America/New_York", "2032-12-30", "2033-01-02"},
		{"Europe/Berlin", "2032-12-30", "2033-01-02"},
		{"Australia/Lord_Howe", "2032-12-30", "2033-01-02"},
		{"America/New_York", "9998-12-30", "9998-12-31"},
	} {
		for _, pattern := range []struct {
			expression string
			duration   int
			selected   func(time.Time) bool
		}{
			{"* * * * *", 1, func(time.Time) bool { return true }},
			{"* * * * *", 61, func(time.Time) bool { return true }},
			{"5,25,45 0-3 * * *", 22, func(d time.Time) bool {
				return d.Hour() < 4 && (d.Minute() == 5 || d.Minute() == 25 || d.Minute() == 45)
			}},
			{"*/17 0-4 * * *", 10, func(d time.Time) bool { return d.Hour() <= 4 && d.Minute()%17 == 0 }},
			{"30 2 * * *", 90, func(d time.Time) bool { return d.Hour() == 2 && d.Minute() == 30 }},
		} {
			t.Run(dates.zone+dates.first+pattern.expression, func(t *testing.T) {
				c := cronConfig(pattern.expression, dates.zone, dates.first, dates.last, pattern.duration)
				got := mustPlan(t, c).windows
				want := slowReference(t, c, pattern.selected)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("compiled windows %v differ from UTC oracle %v", got, want)
				}
			})
		}
	}
}

func TestHistoricSecondOffsetsAndMidnightTransition(t *testing.T) {
	p := mustPlan(t, daily("Africa/Monrovia", "1971-01-01", "1971-01-01", "12:00", 1))
	expectState(t, p, "1971-01-01T12:44:30Z", true, "1971-01-01T12:44:30Z", "1971-01-01T12:45:30Z", "")
	p = mustPlan(t, daily("Africa/Monrovia", "1972-01-06", "1972-01-07", "00:30", 1))
	expectState(t, p, "1972-01-07T00:45:00Z", false, "", "", "")
	c := daily("Africa/Monrovia", "1972-01-06", "1972-01-06", "23:59", 120)
	p = mustPlan(t, c)
	expectState(t, p, "1972-01-07T00:44:00Z", true, "1972-01-07T00:43:30Z", "1972-01-07T00:44:30Z", "")
}

func FuzzCronCompilation(f *testing.F) {
	for _, expression := range []string{"* * * * *", "*/7 1-4 * * MON", "0 0 L * *", "30 2 * * *", "5,25,45 0-3 * * *", "0 0 31 FEB *", "*/0 * * * *", "", "0 0 * * 7"} {
		f.Add(expression, uint8(0), uint16(60))
	}
	f.Fuzz(func(t *testing.T, expression string, zoneIndex uint8, duration uint16) {
		zones := []string{"UTC", "America/New_York", "Europe/Berlin", "Australia/Lord_Howe", "Asia/Kathmandu"}
		dates := [][2]string{{"2026-03-07", "2026-03-09"}, {"2026-10-24", "2026-10-26"}, {"2032-12-30", "2033-01-02"}, {"9998-12-30", "9998-12-31"}, {"1970-01-01", "1970-01-03"}}
		selected := dates[(int(zoneIndex)/len(zones))%len(dates)]
		c := cronConfig(expression, zones[int(zoneIndex)%len(zones)], selected[0], selected[1], int(duration))
		p, err := Compile(c)
		if err != nil {
			if p != nil || (!errors.Is(err, ErrInvalidConfig) && !errors.Is(err, ErrLimit) && !errors.Is(err, ErrNoOccurrences)) {
				t.Fatalf("unexpected failed compile result: plan=%v, error=%v", p, err)
			}
			return
		}
		again, err := Compile(c)
		if err != nil || !reflect.DeepEqual(p.windows, again.windows) {
			t.Fatal("compilation must be deterministic", err)
		}
		for i, w := range p.windows {
			if w.start >= w.end || (i > 0 && p.windows[i-1].end >= w.start) {
				t.Fatal("invalid or unmerged windows", p.windows)
			}
			start, end := time.Unix(w.start, 0).UTC(), time.Unix(w.end, 0).UTC()
			s, err := p.Evaluate(start)
			if err != nil || !s.Active || !s.Start.Equal(start) || !s.End.Equal(end) {
				t.Fatal("start boundary not included", s, err)
			}
			s, err = p.Evaluate(end)
			if err != nil || s.Active {
				t.Fatal("end boundary not excluded", s, err)
			}
		}
	})
}

func BenchmarkCompileDenseTenYear(b *testing.B) {
	c := cronConfig("* * * * *", "America/New_York", "2026-01-01", "2035-12-31", 1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(c); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvaluate(b *testing.B) {
	p := mustPlan(b, daily("America/New_York", "2026-01-01", "2035-12-31", "08:00", 30))
	now := instant("2030-06-01T12:15:00Z")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Evaluate(now); err != nil {
			b.Fatal(err)
		}
	}
}
