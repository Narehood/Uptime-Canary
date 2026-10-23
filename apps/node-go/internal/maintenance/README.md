# Maintenance schedule engine

This package evaluates schedules. It has no clock, timers, background goroutines,
database, network client, notification sender, monitor selection, or enable flag.
The caller supplies the observation time to `plan.Evaluate(now)` and owns the
versioned resource, enablement, monitor/node selection, and incident lifecycle.
Maintenance should hold notifications while observations and incidents continue.
An expired window does not imply recovery. The caller decides whether an ongoing
outage should notify after maintenance; the engine never manufactures an event.

## API and integration

```go
config := maintenance.Config{
    Strategy: maintenance.Weekly,
    Timezone: "America/New_York",
    StartDate: "2026-10-01",
    EndDate: "2026-12-31",
    StartTime: "23:30",
    DurationMinutes: 90,
    Weekdays: []int{1, 7}, // Monday and Sunday, ISO numbering.
}
plan, err := maintenance.Compile(config)
if err != nil {
    return err
}
// Cache plan by resource revision. Compilation belongs outside shared locks.
state, err := plan.Evaluate(callerProvidedTime)
```

`Compile(Config) (*Plan, error)` fully validates and compiles a finite immutable
plan. It retains no references to the Config or its slices. Plans can be shared
between goroutines and processes can compile the same resource independently.
There is no package cache or mutable evaluation state.

`Validate(Config) error` is **Compile-equivalent**, including recurrence feasibility
and work/storage limits: it builds and discards a plan. It is not a cheap shape
check. Calling Compile once supplies both validation and the reusable plan.

`(*Plan).Evaluate(time.Time) (State, error)` uses a binary search over compiled
windows. It returns an error for an uncompiled/nil plan or a zero/out-of-range
caller time. Out-of-order caller times and restarts inside a window work without
replaying starts. Returned timestamp pointers are fresh caller-owned values and
cannot mutate the plan. Evaluation allocates at most three small time values.

State JSON uses `active`, `start`, `end`, and `nextStart`. Start/end are UTC RFC3339
timestamps for the current merged window, present only when active. NextStart is
the next **disjoint merged window**, even while active; it is absent after the
last window. It does not enumerate later raw starts inside an active window.
For example:

```json
{"active":true,"start":"2026-10-19T23:30:00Z","end":"2026-10-20T01:00:00Z","nextStart":"2026-10-25T23:30:00Z"}
```

Errors wrap `ErrInvalidConfig`, `ErrLimit`, or `ErrNoOccurrences` for `errors.Is`.
A valid recurrence with no starts inside its finite bounds returns
ErrNoOccurrences, including day 31 selected for February alone.

## Inputs

Config JSON keys are camelCase. Fields for other strategies are rejected rather
than silently ignored. An API decoder should separately reject unknown JSON keys.

| Field | Contract |
| --- | --- |
| `strategy` | `once`, `interval`, `daily`, `weekly`, `monthly`, or `cron` |
| `timezone` | Required explicit IANA name/alias or `UTC`; `Local`, server defaults, file paths, and unknown zones are rejected |
| `startDate`, `endDate` | Required inclusive local `YYYY-MM-DD` dates; years 1970..9998; at most 3660 dates |
| `startTime` | Required `HH:MM` for every strategy except cron, which must omit it |
| `durationMinutes` | Required integer 1..44640 (31 elapsed days) |
| `intervalDays` | Required integer 1..3660 for interval; omit otherwise |
| `weekdays` | Required nonempty unique ISO weekdays 1=Monday..7=Sunday for weekly |
| `daysOfMonth` | Unique integers 1..31 for monthly; absent days are skipped |
| `lastDayOfMonth` | Optional monthly last-day selection, unioned with daysOfMonth and deduplicated; monthly requires at least one selection |
| `cron` | Required expression for cron; omit otherwise |

`once` starts only on startDate at startTime. Duration can continue overnight or
over multiple dates, but all windows are clipped to the date bounds. `interval`
starts every N **local calendar dates**, anchored to startDate; DST does not move
its wall start time. `daily` starts each selected date, weekly uses weekdays, and
monthly uses daysOfMonth/lastDayOfMonth without normalizing missing days into the
following month. Leap years follow the Gregorian calendar.

Date bounds begin at the first instant at/after the start date's local midnight
and end at the first instant at/after midnight following endDate. EndDate is
inclusive as a date, but that final instant is exclusive. A missing midnight moves
to the first valid local time; a wholly skipped date has no starts. Overnight
windows need a sufficient endDate to retain their entire duration.

## Boundaries, DST, and determinism

Each occurrence is `[start, start + duration)`: exact start is active, exact end
is inactive. Duration is elapsed minutes, even across a DST change. Nonexistent
wall start minutes are skipped. Repeated wall start minutes run **once at the
earlier instant**, for cron and every other strategy. For example, New York's
2026-11-01 01:30 starts at 05:30Z, never again at 06:30Z. A 120-minute duration
from that first start ends at 07:30Z. New York's 2026-03-08 02:30 is skipped.

Overlapping **and touching** occurrences merge into one continuous window, with
the earliest start and latest end. This applies across midnight, DST transitions,
and date selections. A minute cron with a one-minute duration covers a spring
skip continuously. On a fall fold it leaves a gap during the second occurrence
of the repeated hour; increasing duration can bridge that gap. No false release
is returned between overlapping raw occurrences.

All arithmetic uses the caller's explicit timezone and clock, never time.Now or
time.Local. `time/tzdata` is imported so IANA zones work in Alpine/minimal images
without host zone files. Go may prefer available host/ZONEINFO zone data; nodes
should ship the same Go/tzdata version and zone configuration for identical
future political timezone rules. Standard Go timezone aliases such as
`Etc/GMT+5` are supported; their sign convention follows IANA.

## Cron dialect and parity limits

The bounded in-package parser implements **five fields**:
`minute hour day-of-month month weekday`, with no job execution or dependency.
Supported syntax: `*`, comma lists, ascending inclusive ranges, `/step`, numeric
values, case-insensitive JAN..DEC/MON..SUN names, Sunday 0 or 7, and `L` in the
day-of-month field for the last day. `a/n` steps from a through that field's maximum.
Steps must be 1..the field's number of values. Descending/wrapping ranges are
rejected; use lists instead. Expressions contain at most 256 printable ASCII
bytes and 64 terms per field. Use spaces between fields.

When both day fields are restricted, day-of-month and weekday use OR. If either
contains `*` (including `*/n`), both fields must match, following Vixie cron rules.
For example, `0 12 15 * MON` selects the 15th and Mondays, while
`0 12 */2 * MON` selects Mondays whose day number is 1,3,5,... .

Examples: `*/15 2-4 * * MON-FRI`, `30 23 L * *`, `0 8 1,15 JAN,JUL *`.
Timezone is always Config.timezone, never an inline cron setting.

This covers the maintenance strategies inventoried in the parity audit, but is
not full Croner syntax compatibility. Seconds/year fields, `@hourly`/`@every`
descriptors, `TZ=`/`CRON_TZ=`, `?`, `W`, `#`, and last-weekday/relative-last-day
extensions are explicitly rejected. An unbounded/manual schedule and server
timezone inheritance are intentionally outside this finite engine contract. Kuma
2.5.6's callback/last-run persistence quirks are not replicated. The comparison
source is the pinned [Kuma maintenance model](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/maintenance.js).

## Work and storage bounds

Compilation first builds at most 1440 selected local start minutes, then compresses
them into runs whose successive starts overlap/touch. It visits at most 3660
local dates and maps those runs through fixed-offset timezone segments using
`time.Time.ZoneBounds`. It does not walk the individual cron events across the
date range. A ten-year `* * * * *` with duration 1 emits roughly one compressed
span per date (plus timezone splits), then streams those into merged windows.
It never expands the roughly 5.2 million raw events.

Hard limits are checked **during** construction: 250000 emitted compressed spans,
65536 disconnected merged windows, and 32 offset segments/local pieces around
any date. A disconnected cron can hit the window limit; shorten its date bounds.
An exceeded bound returns ErrLimit and no partial Plan. Output capacity never
exceeds MaxWindows. Each stored window is two int64 seconds: at most 1 MiB of
retained window storage per Plan, plus small fixed/compiler scratch space.
Geometric growth temporarily retains old backing arrays until garbage collection.
Compilation does not enumerate occurrences on every evaluation tick. Evaluation
cost is O(log windows) with at most three small allocations.

Tests include independent UTC-minute reference evaluation over short transition
ranges, exact boundaries/subsecond clocks, overnight clipping, elapsed and
calendar durations, leap years/month ends, first/second fold occurrences, skipped
midnights/dates, historical second offsets, malformed/hostile cron inputs,
deterministic/concurrent reuse, limit checks, fuzz seeds, and benchmarks.
