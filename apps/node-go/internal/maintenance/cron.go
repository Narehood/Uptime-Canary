package maintenance

import (
	"strconv"
	"strings"
	"time"
)

// The parser accepts a deliberately bounded five-field cron dialect. There is
// no job runner, shell interpretation, or expression expansion by input counts.
type cronField struct {
	bits uint64
	star bool
	last bool
}

func (f cronField) has(n int) bool { return f.bits&(uint64(1)<<uint(n)) != 0 }

type cronSpec struct {
	minute, hour, dom, month, dow cronField
}

var monthNames = map[string]int{"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6, "JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12}
var dayNames = map[string]int{"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6}

func parseCron(expression string) (*cronSpec, error) {
	if len(expression) == 0 || len(expression) > MaxCronBytes {
		return nil, invalid("cron must contain 1..%d bytes", MaxCronBytes)
	}
	for _, c := range expression {
		if c < 32 || c > 126 {
			return nil, invalid("cron must contain printable ASCII")
		}
	}
	parts := strings.Fields(expression)
	if len(parts) != 5 {
		return nil, invalid("cron requires five fields: minute hour day-of-month month weekday")
	}
	spec := &cronSpec{}
	fields := []*cronField{&spec.minute, &spec.hour, &spec.dom, &spec.month, &spec.dow}
	mins, maxs := []int{0, 0, 1, 1, 0}, []int{59, 23, 31, 12, 7}
	for i, field := range fields {
		names := map[string]int(nil)
		if i == 3 {
			names = monthNames
		} else if i == 4 {
			names = dayNames
		}
		parsed, err := parseCronField(strings.ToUpper(parts[i]), mins[i], maxs[i], names, i == 2, i == 4)
		if err != nil {
			return nil, invalid("cron field %d: %s", i+1, err)
		}
		*field = parsed
	}
	return spec, nil
}

func parseCronField(input string, min, max int, names map[string]int, allowLast, sundayAlias bool) (cronField, error) {
	var result cronField
	terms := strings.Split(input, ",")
	if len(terms) > 64 {
		return result, invalid("at most 64 list terms")
	}
	for _, term := range terms {
		if allowLast && term == "L" {
			result.last = true
			continue
		}
		stepParts := strings.Split(term, "/")
		if len(stepParts) > 2 || stepParts[0] == "" {
			return result, invalid("invalid list term")
		}
		step := 1
		if len(stepParts) == 2 {
			var err error
			step, err = cronNumber(stepParts[1], nil)
			if err != nil || step < 1 || step > max-min+1 {
				return result, invalid("step must be 1..%d", max-min+1)
			}
		}
		base := stepParts[0]
		lo, hi := min, max
		if base == "*" {
			result.star = true
		} else {
			rangeParts := strings.Split(base, "-")
			if len(rangeParts) > 2 {
				return result, invalid("invalid range")
			}
			var err error
			lo, err = cronNumber(rangeParts[0], names)
			if err != nil {
				return result, err
			}
			hi = lo
			if len(rangeParts) == 2 {
				hi, err = cronNumber(rangeParts[1], names)
				if err != nil {
					return result, err
				}
			} else if len(stepParts) == 2 {
				hi = max // a/n means a through the field maximum, stepping n.
			}
		}
		if lo < min || hi > max || lo > hi {
			return result, invalid("range must be ascending and within %d..%d", min, max)
		}
		for n := lo; n <= hi; n += step {
			v := n
			if sundayAlias && v == 7 {
				v = 0
			}
			result.bits |= uint64(1) << uint(v)
		}
	}
	return result, nil
}

func cronNumber(value string, names map[string]int) (int, error) {
	if n, ok := names[value]; ok {
		return n, nil
	}
	if len(value) == 0 || len(value) > 3 {
		return 0, invalid("expected number or supported three-letter name")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, invalid("unsupported cron syntax")
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, invalid("invalid number")
	}
	return n, nil
}

func (s *cronSpec) minutesOfDay() []int {
	minutes := make([]int, 0, 1440)
	for h := 0; h < 24; h++ {
		if s.hour.has(h) {
			for m := 0; m < 60; m++ {
				if s.minute.has(m) {
					minutes = append(minutes, h*60+m)
				}
			}
		}
	}
	return minutes
}

func (s *cronSpec) matchesDay(day time.Time) bool {
	if !s.month.has(int(day.Month())) {
		return false
	}
	dom := s.dom.has(day.Day()) || (s.dom.last && day.AddDate(0, 0, 1).Day() == 1)
	dow := s.dow.has(int(day.Weekday()))
	// Vixie cron semantics: when either day field contains *, require both
	// matches; otherwise match either restricted day field. */n contains *.
	if s.dom.star || s.dow.star {
		return dom && dow
	}
	return dom || dow
}
