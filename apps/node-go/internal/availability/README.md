# Observed time availability

This package calculates time-weighted history from explicit observed validity
spans. It is a pure in-memory calculation using only the Go standard library.
It does not read configuration, samples, clocks, files, databases, or networks.
It does not provide persisted rollups or integrate storage, retention, APIs, or
the UI. The existing bounded local sample history and `sampleAvailability`
remain separate concepts; samples alone cannot establish continuous coverage.

## Stable API

```go
type State string

const (
    StateUp      State = "up"
    StateDown    State = "down"
    StatePending State = "pending"
    StateUnknown State = "unknown"
)

type Span struct {
    StartMs     int64 `json:"startMs"`
    EndMs       int64 `json:"endMs"`
    State       State `json:"state"`
    Maintenance bool  `json:"maintenance"`
}

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

type Bucket struct { Totals }

func Summarize(startMs, endMs int64, spans []Span) (Totals, error)
func Buckets(startMs, endMs, stepMs int64, spans []Span) ([]Bucket, error)
```

`Bucket` JSON is flat, with exactly the `Totals` field names. A nil availability
percentage encodes as `null`, with the field present. State values are strict
and case sensitive. Errors return zero `Totals` or nil buckets; error strings
are diagnostic, not a machine-readable error contract.

## Interval and percentage semantics

Queries and spans are half-open `[startMs, endMs)` integer Unix milliseconds.
An interval must have positive length. Spans must arrive sorted and disjoint;
touching endpoints are allowed. Every supplied span is validated, including
spans wholly outside the query. Duplicates, overlaps, unsorted spans, malformed
times, and unrecognized states cause an error. Inputs are never sorted or
resolved by last-wins rules.

Only the intersection of each span with the query contributes. Gaps are
unknown. An up observation ends exactly at its supplied `EndMs`; it is never
carried into a gap. There is no inferred sampling interval, current generation,
freshness horizon, or validity policy inside this package.

The accounting identities are:

```text
windowMs = endMs - startMs
upMs + downMs + pendingMs + unknownMs = windowMs
knownMs = upMs + downMs
availabilityPercent = null, if knownMs == 0
availabilityPercent = 100 * upMs / knownMs, otherwise
coveragePercent = 100 * knownMs / windowMs
```

All duration numerators and denominator components are exposed. Durations are
exact integers; percentages are floating-point approximations. Pending and
unknown do not contribute to known time. Explicit unknown spans and missing
history both contribute to `unknownMs`.

`maintenanceMs` measures the overlap with spans explicitly marked as maintenance,
including up, down, pending, and observed unknown spans. It is separate from the
state partition and never removes time from a percentage denominator. A gap
does not inherit maintenance from an earlier span. These are observed-history
metrics, not an SLA assertion.

## Buckets and limits

Exported constants enforce the following exact limits:

| Constant | Value | Meaning |
| --- | ---: | --- |
| `MaxTimeMs` | 9,007,199,254,740,991 | Inclusive maximum timestamp, 2^53 - 1 |
| `MinStepMs` | 1 | Inclusive minimum bucket step |
| `MaxStepMs` | 31,536,000,000 | Inclusive maximum step, 365 days of 24 hours |
| `MaxSpans` | 100,000 | All provided spans, even outside the query |
| `MaxBuckets` | 4,096 | All returned buckets, including partial edges |

Every timestamp must be in `[0, MaxTimeMs]`. Negative timestamps, empty or
reversed queries/spans, unsupported steps, and excessive inputs are rejected.
The span count is checked before walking spans. Step and bucket count are
checked before span validation or output allocation. No narrowing of an
unbounded bucket count to `int` occurs. For a valid query and step, the count is:

```text
(endMs - 1) / stepMs - startMs / stepMs + 1
```

This avoids overflow from adding a step to an endpoint and excludes a bucket
starting exactly at the query end. Validated timestamps and disjoint spans
bound all duration arithmetic by `MaxTimeMs`.

Buckets split at integer multiples of `stepMs` from the UTC Unix epoch.
They do not follow local time zones, calendar months, or daylight saving time.
For query `[5, 25)` and step 10, the returned intervals are `[5, 10)`,
`[10, 20)`, and `[20, 25)`. Each edge bucket uses its clipped length as its
coverage denominator; it makes no statement about the remainder of the nominal
bucket outside the query. Even `[9, 11)` with step 10 requires two buckets.

Adding all bucket duration fields, including `maintenanceMs` and `knownMs`,
reproduces `Summarize` for the same query. Recompute percentages from the summed
durations; do not average bucket percentages.

Processing uses validation and a simple linear sweep, O(spans + buckets), with
no scan over individual milliseconds. `Summarize` uses constant working space;
`Buckets` allocates the bounded result slice plus at most one bounded backing
array for percentage values, with a distinct value for each known bucket.
Input and output counts are explicitly bounded;
these limits are not a CPU, memory, or wall-time guarantee for the whole service.

Functions do not mutate caller spans or retain references to their storage.
Results do not share mutable percentage pointers across buckets or calls.
Concurrent calls may read the same spans safely while callers keep the input
unchanged. Callers own their returned slices and percentage pointers and must
synchronize any writes to shared inputs or results themselves.

## Integration responsibilities

The root integration must persist each observation's original `validUntil`,
generation, and sequence, together with the observation and policy provenance
needed to interpret its validity. `Span.EndMs` must come from that original
observed validity, clipped at actual superseding observations or policy changes.
Do not reconstruct it using today's monitor interval or current configuration.
Close intervals on configuration/generation changes, disablement, and other
events that invalidate the observation. Resolve event ordering and produce
disjoint explicit spans before calling this API; the package deliberately
rejects overlapping candidates.

Keep raw history local. SQLite/archive persistence and retention require a
separate integration: writes of observations, interval closures, any derived
archive or rollup coverage, and deletion of the corresponding raw history must
be coordinated in the same SQL transaction. Querying archive boundaries must
also preserve partial-edge semantics and avoid duplicate coverage. This package
does not implement or establish the existence of that transaction or archive.

Old records without original validity policy/provenance cannot backfill time
coverage honestly. Preserve them as sample history; leave their unsupported
time coverage unknown. An old healthy sample cannot justify an indefinitely
healthy interval. Storage, retention, API/UI wiring, and release validation
remain root integration work.

## Verification

Tests use fixed arbitrary periods and deterministic random small timelines,
with an independent bounded per-millisecond oracle. They cover clipping,
partition identities, all observed states, gaps, maintenance, JSON, timestamp
extremes, bucket alignment, resource limits, input immutability, and concurrent
read-only calls. Fuzzers bound their decoded timelines and allocations; benchmarks
exercise maximum accepted span/bucket counts and a large timestamp gap.

Run the repository's pinned SDK with network access disabled and module files
read-only. From the repository root in a disposable PowerShell process:

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
& .\.canary\tools\go\bin\go.exe test -mod=readonly -count=1 ./apps/node-go/internal/availability
& .\.canary\tools\go\bin\go.exe vet -mod=readonly ./apps/node-go/internal/availability
& .\.canary\tools\go\bin\go.exe test -mod=readonly -run='^$' -fuzz='^FuzzObservedTimeline$' -fuzztime=3s -parallel=1 ./apps/node-go/internal/availability
& .\.canary\tools\go\bin\go.exe test -mod=readonly -run='^$' -fuzz='^FuzzInvalidInputs$' -fuzztime=3s -parallel=1 ./apps/node-go/internal/availability
& .\.canary\tools\go\bin\go.exe test -mod=readonly -run='^$' '-bench=.' -benchtime=100ms -count=1 ./apps/node-go/internal/availability
```

For Linux compile checks, set process-local `GOOS=linux`, `CGO_ENABLED=0`, and
`GOARCH=amd64`, then `arm64`. Use the pinned SDK's `go test -mod=readonly -c -o`
with an output path under this package; cross-compiled test binaries are compile
checks and must not be reported as Linux test execution. No dependency installs,
user databases, live processes, or environment-based fixtures are needed.
