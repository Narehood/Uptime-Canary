# Local observed-time availability history

Availability history records explicit local observations as half-open Unix
millisecond intervals. It is independent of bounded raw sample history and of
peer observations. It does not backfill existing samples, project remote
coverage, infer latency, or assert an SLA. Missing evidence is unknown.

## Storage and hooks

The implementation is `apps/node-go/internal/canary/availability_history.go`.
It uses the existing SQLite driver and standard library. The pure calculator
contract remains `apps/node-go/internal/availability/README.md`.

```go
func initializeAvailabilityStore(db *sql.DB) error
func recordLocalAvailability(e dbExec, m Monitor, s Sample,
    validUntil int64, maintenance bool) error
func closeLocalAvailability(e dbExec, monitorID string, at int64) error
func (n *Node) availabilityHistory(id string, query url.Values) (any, error)
```

The store opener calls the initializer after creating the existing schema.
Initialization is a separate schema transaction, is idempotent, imports no
data, and returns only `Cannot initialize availability history` on failure.
An existing database starts with entirely unknown observed-time history until
the first post-integration accepted local sample. Existing sample and
sample-statistics APIs keep their own meaning and retention policy.

The initializer creates two STRICT tables. `availability_heads` has one row per
ever-observed monitor, keyed by `monitor_id`, with these fields:

```text
monitor_id, high_water, checked_at, valid_until, revision, generation,
sequence, state, maintenance, closed, span_start
```

`high_water` is the largest accepted local event time, including closures.
`checked_at` and `valid_until` preserve the latest accepted sample's original
timestamps. Revision, generation and sequence preserve its original provenance.
`closed` prevents extension after invalidation; `span_start` names its current
run or is null when no extendable span remains. Pruning does not discard the
head's watermark or provenance. Closing an ID with no observed head creates no
record. Closing an existing head with no remaining span still advances its
watermark.

`availability_spans` holds these fields:

```text
monitor_id, start_ms, end_ms, state, maintenance
PRIMARY KEY (monitor_id, start_ms)
```

The composite primary key indexes chronological range reads and pruning.
Spans have positive length, are disjoint, and use exactly `up`, `down`,
`pending`, or `unknown`. Maintenance is a parallel boolean annotation.

Root calls the record hook only for an accepted local sample, before bounded
raw-history pruning. The hook verifies identity, revision/generation,
assignment, enabled/deleted status, sequence, state and supported timestamps;
root owns local-origin selection and conflict readiness. It never admits a
peer sample through synchronization. The store has no per-peer history table.

Root supplies the original freshness deadline, calculated when accepting the
sample, with the runtime's inclusive freshness boundary converted to an
exclusive end:

```text
validUntil = min(maxSafe, checkedAt
    + max(probeSpacing(m, s)*3, timeoutMs*2, syncIntervalMs*3) + 1)
```

Storage uses that value unchanged. It does not inspect current intervals,
recalculate old deadlines, or add another freshness allowance. Changes to
current monitor settings cannot establish past coverage.
Root may additionally shorten the supplied deadline at an already-known
maintenance schedule boundary so one annotation is not carried across a future
transition without another local observation.

The sample, policy state, sequence, availability writes, interval closure and
both retention operations must participate in the SAME caller SQL transaction.
The write helpers use only `dbExec.Exec`; they never open, commit or roll back
their own data transactions. Any failure must roll back the whole caller
transaction. Calling a write hook directly on `*sql.DB` cannot provide atomicity
and is outside this integration contract.

Root closes the local head in the configuration transaction for revision or
generation changes, disablement, deletion, assignment loss, unresolved
configuration conflicts, and any other event invalidating current evidence.
Root also conservatively closes when effective maintenance/group configuration
changes could alter its historical annotation. Only a fresh local sample can
resume coverage; a peer observation or config change alone cannot resume it.

## Timeline behavior and backward clocks

A newly accepted sequence first ends its predecessor's future horizon at the
effective event time. Continuous runs with equal state and maintenance may
coalesce only when their endpoints touch exactly, their provenance agrees, and
the previous head is open. Different states, annotations, provenance, explicit
closure and actual gaps keep separate runs. Zero-duration spans are removed.
A sample whose original deadline has already expired adds no new coverage.

Every sample or closure uses:

```text
effectiveEvent = max(originalEventTime, durableHighWater)
```

The durable watermark never moves backward. A backward-clock event cannot
rewrite the timeline before the last accepted sample/closure event. Its
deadline stays the original `validUntil`; storage does not shift or lengthen it
to compensate for the clamp. For example, with watermark 200, a sample checked
at 150 with deadline 240 can cover only `[200,240)`, not `[150,240)` or
`[200,290)`. A deadline at or before 200 gives it no coverage. This deliberate
undercoverage can leave unknown intervals until the clock catches up and a
new sample supplies a deadline beyond the watermark.

Closures use the same clamp, preserve the original sample/deadline, and cannot
extend an old span. Repeated closure at the same time is idempotent. A later
closure of an already-closed head can advance its watermark without adding
coverage. Sample sequences must increase within the database's local origin;
an equal or lower sequence is ignored and cannot reopen a closed head, even
after restart. The watermark orders accepted local events; it is not a clock
repair mechanism or a reconstruction of real elapsed time during clock skew.

A current head may have a future horizon. Reads reject `to` beyond their
captured current clock and the calculator clips all coverage to the query.
Thus the API never reports future time as observed availability.

## Retention and bounded reads

Each monitor retains at most 100,000 span rows, independently of the raw sample
history limit (normally 1,000 rows). On each record/close transaction, storage:

1. Removes spans ending at or before `highWater - 90 days`.
2. Clips a crossing span to that cutoff without changing its original head
   provenance or deadline.
3. Removes the oldest excess runs beyond 100,000 rows and repairs the head's
   span pointer.

These operations affect only that monitor and roll back with the sample or
closure. Physical cleanup is lazy for idle monitors: expired rows disappear on
their next write/closure. Reads always apply their current clock's 90-day cutoff
too, so idle expired rows never contribute to a result. This is a row and
logical-time retention policy, not a promise to shrink the SQLite file or to
erase historical pages from backups. A whole-SQLite backup includes both new
tables automatically; there is no separate archive file.

Reads use one SQL snapshot for metadata and spans. They select the indexed
`[from,to)` start range plus at most one crossing predecessor, with a hard SQL
limit of 100,001 to detect excess data. The collector rejects a 100,001st span
and bounds its slice capacity to 100,000. Summary and buckets are calculated
after releasing the SQL read transaction. No application mutex is acquired by
this helper. Root owns its request synchronization; all processing is bounded
by 100,000 spans and 4,096 buckets, including when invoked under `n.mu`.

## Authenticated API contract

Root exposes `GET /api/monitors/:id/availability` behind management
authentication and monitor existence checks. Only `from`, `to`, and `stepMs`
are accepted, each once as a nonempty ASCII decimal integer. Signs, whitespace,
fractions, exponent notation, unknown keys, duplicate keys, values above
`2^53 - 1`, and more than 16 digits are rejected with 400.

`to` defaults to the captured current clock. `from` defaults to 24 hours before
the selected `to`, floored at zero. `stepMs` defaults to 300,000 (five minutes).
`0 <= from < to <= now`, a range no longer than 90 days, and a step between 1
and 31,536,000,000 milliseconds are required. At most 4,096 epoch-aligned
buckets are allowed, including clipped edge buckets. Range and bucket limits
are checked before reading spans or allocating bucket output; excessive ranges
and future endpoints return explicit 400 errors rather than silent clipping.

The concrete Go response is `availabilityHistoryResult`:

```text
monitorId: string
nodeId: string
from, to, stepMs: integer milliseconds
retainedFrom: integer milliseconds | null
recordLimit: 100000
retentionMs: 7776000000
summary: availability.Totals
buckets: availability.Bucket[]
```

`retainedFrom` is the earliest currently retained span start, clipped to the
current 90-day cutoff, across this monitor's retained history. It is independent
of the requested window and is null when no span remains eligible before now.
It is not evidence of continuous coverage from that point: gaps and all earlier
unsupported time remain unknown. A record-cap deletion may move it forward by
more than the age limit. Old windows can be queried if their width is within
the limit; unsupported or expired coverage returns unknown.

Summary and bucket fields are `startMs`, `endMs`, `upMs`, `downMs`, `pendingMs`,
`unknownMs`, `maintenanceMs`, `knownMs`, `availabilityPercent`, and
`coveragePercent`. The pure calculator enforces:

```text
upMs + downMs + pendingMs + unknownMs = endMs - startMs
knownMs = upMs + downMs
availabilityPercent = null when knownMs == 0, otherwise 100 * upMs / knownMs
coveragePercent = 100 * knownMs / (endMs - startMs)
```

Maintenance includes overlap with any explicitly annotated state and never
removes time from either denominator. Missing intervals do not inherit an old
maintenance annotation. No latency field is generated. Buckets split at Unix
epoch multiples of `stepMs`; partial edges use their clipped duration. Sum the
duration fields to combine buckets, then recompute percentages instead of
averaging them. All percentages are finite and expressed on a 0–100 scale.

Storage/read/corrupt-span errors return only 503 `Availability history is
unavailable`, without SQL diagnostics, database paths or private values.

## Verification scope

The new storage tests use `openStore` or `New` with private temporary SQLite
databases, fixed clocks and direct transaction hooks. They cover exact states,
gaps, clipping, maintenance, percentages, original deadlines, closure, replay,
restart, backward clocks, old database non-backfill, raw-history independence,
age clipping, idle expiration, the row cap, strict query limits and sanitized
errors. An actual `New` fixture invokes the authenticated handler in memory
without starting a listening server. Injected SQL failures verify that record,
close and every pruning phase roll back with sample, policy and raw history.
Root integration tests own selecting the correct runtime/configuration hooks.

From the repository root, use the pinned Go SDK and existing cached modules:

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
& .\.canary\tools\go\bin\go.exe test -mod=readonly -count=1 -run '^TestAvailabilityHistory' ./apps/node-go/internal/canary
& .\.canary\tools\go\bin\go.exe vet -mod=readonly ./apps/node-go/internal/canary
```

These checks require no user database, `.env`, service startup or live sends.
Passing them is storage/API validation, not full release validation.

## Retained-limit workload measurements

The file-backed SQLite benchmark in `availability_benchmark_test.go` seeds the
documented 100,000-span limit for one monitor, with alternating states and gaps.
It measures the real archive query and transaction hooks, outside setup time:

```powershell
& .\.canary\tools\go\bin\go.exe test -mod=readonly ./apps/node-go/internal/canary -run '^$' -bench '^BenchmarkAvailability' -benchtime=3x -count=1
```

On native Windows with Go 1.27.3 and a Ryzen 9 5900XT, this development run
observed:

| Workload | Mean time per operation | Allocated bytes per operation |
| --- | ---: | ---: |
| Read all 100,000 spans into summary and buckets | 87.67 ms | 18,288,082 |
| Read the newest 100 spans from the same archive | 0.437 ms | 18,128 |
| Append, prune and commit at the 100,000-span limit | 28.86 ms | 74,586 |

The benchmark verifies exact known duration after reads and the retained row
count after appends. Its source SHA-256 is
`8989c688cb503bce519b140ebf436ad8adc334cdd82d7b9e8831c663f752e327`.
These are three-iteration workload observations while other development tests
were running, not latency guarantees or a many-monitor capacity certification.
Full-window reads allocate and process more data than narrow indexed reads;
retention does not make that work constant. No worker limit, archive record cap
or production storage code was changed for this measurement.
