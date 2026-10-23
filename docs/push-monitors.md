# Passive push / heartbeat monitors

Push monitors run in the Go/SQLite runtime without a dashboard. An external job
sends a heartbeat to any configured node, including a node that is not assigned
to observe that monitor. Assigned observers evaluate the replicated heartbeat
register through the ordinary scheduler, retry/inversion policy, incidents, and
notification delivery. HTTP success means the accepting node durably committed
the heartbeat; it does not acknowledge replication or notification delivery.

## Configuration and client contract

Create or edit a monitor through the authenticated management API, with these
type-specific fields alongside the ordinary scheduling, placement, and delivery
fields:

```json
{
  "name": "Nightly import",
  "type": "push",
  "target": "push",
  "intervalMs": 60000,
  "timeoutMs": 1000,
  "nodeIds": ["node:home", "node:office"],
  "enabled": true,
  "push": {"token": "<64 random lowercase hexadecimal characters>", "allowGet": false}
}
```

The example token is a placeholder. Generate a fresh 32-byte cryptographic random
secret in the client and encode it as 64 lowercase hex characters. The API
requires a token and rejects a low-diversity value; it does not silently generate
or repair an empty token from replication. Use a separate secret for every
monitor. Management, session, and cluster credentials are rejected as push
credentials. Local writes reject token reuse across other live monitor
candidates; if independently edited peers introduce an ambiguous token, ingress
fails closed until configuration is resolved. Viewer projections remove the
secret. Admin/editor configuration and private peer/backup data retain it.

`intervalMs` is the maximum silence before a heartbeat is missed, bounded by the
existing 100 ms–24 hour monitor range. `timeoutMs` remains the scheduler evaluation
timeout, with its ordinary validation. `retries`, `retryIntervalMs`, and
`upsideDown` use the shared check policy. Initial waiting is pending, not success;
inversion does not turn pending into success. Missing measured latency is
explicitly unavailable, while a supplied zero is a real measurement.

Preferred request:

```http
POST /api/push
Authorization: Push <monitor-secret>
Content-Type: application/json

{"status":"up","msg":"Import completed","ping":12.5}
```

The authorization scheme is **Push**, distinct from management/cluster Bearer
authentication. `status` defaults to `up` and accepts only `up` or `down`.
`msg` is optional, at most 256 UTF-8 bytes; control characters are stripped using
the runtime's existing safe-message rule. An occurrence of this monitor's secret
in its submitted message is redacted. `ping` is optional finite latency in
milliseconds, inclusive range 0–3,600,000. A client cannot supply monitor/node
IDs, sequence numbers, timestamps, generations, or causal context.

POST accepts a JSON object only. Unknown keys, case variants, duplicates, nulls,
wrong scalar types, trailing JSON, and query parameters are rejected. Payloads
are at most 4,096 bytes. Responses contain a generic error or `{"ok":true}`;
they do not echo the token, URL, or submitted message.

When `allowGet` is explicitly true, the compatibility form is available:

```http
GET /api/push/<monitor-secret>?status=up&msg=Import%20completed&ping=12.5
```

Only `status`, `msg`, and `ping` are accepted, once each. The same value limits
apply, and the encoded query is at most 4,096 bytes. GET rejects a request body
or an Authorization header. Its secret is part of a URL: configure reverse
proxies/access logs accordingly and prefer POST when URL disclosure matters.
Canary's handler does not log these requests. No full upstream API or payload
equivalence is claimed; see [the parity audit](parity-audit.md).

Invalid credentials, absent/disabled monitors, ambiguous tokens, conflicts, and
configuration that lacks causal proof all return the same 404 response. Valid
credentials receive 400 for malformed payloads, 413 for excessive size, 429 for
admission, and 503 when state/reservation/persistence is unavailable. A node
accepts at most one heartbeat per monitor every 100 ms; clients should leave
more room than that. Admission uses the persisted latest local event after
restart and a map keyed by current monitor/static origin, never by submitted
tokens or arbitrary client IDs. There is no application goroutine queue.

## Generations, restart, and expiry

The immutable monitor payload records `pushStartedAt` before hashing its revision
edge. It is the generation's initial grace anchor. With no exact-generation
signal, evaluation is pending while `now < pushStartedAt + intervalMs`, then raw
down with `Heartbeat missed` at equality. A retained signal expires at
`now >= receivedAt + intervalMs`. Peer replay and restart retain the original
timestamps; neither grants another grace period. The scheduler schedules the
next relevant deadline, subject to its ordinary tick and retry execution time.

Names, tags, and other metadata edits preserve the register and original anchor.
Push token rotation, GET-policy changes, heartbeat-interval changes, and other
result-changing edits start a new check generation. Rotation immediately rejects
the old token on the node holding the new configuration, starts a new grace
period, and leaves old-generation events passive. Partitioned nodes can continue
accepting the old configuration until the edit reaches them. Resolve conflicts
before treating a rotation as cluster-wide revocation.

A multihead resolution inherits an anchor only when every previous head is live,
has the same check semantics/generation, and carries the identical anchor.
Otherwise it starts a new generation and anchor. Restoring a quarantined backup
through the retained-identity activation path also creates new generations and
new grace anchors. Historical heartbeat rows are not authority for those new
generations. Restoring/reusing a node identity still requires the runtime's
retained external identity directory.

## HA state and partitions

The dedicated `heartbeatEvents` SQLite category and `heartbeats` snapshot domain
retain one immutable latest event per **monitor / generation / static origin**.
An accepting node reserves its sequence with the same retained external origin
allocator used by probes, then writes the heartbeat and `settings.sequence` in
one SQL transaction. It publishes memory, cancellation/rescheduling, and HTTP
success only after commit. A failed reservation or write fails closed. Gaps in
reserved sequence numbers are intentional. Heartbeats do not create configuration
revisions, use raw local history as authority, or reuse incident/alias domains.

Each event carries its scoped IDs, origin, reserved sequence, node reception
timestamp, up/down state, safe message, optional latency, and a version-vector
context over at most 32 configured nodes. Every new accepted signal observes all
known contexts for its exact generation, including expired signals. Merge checks
same-dot payload collisions, own-dot self-reference, causal closure/cycles, and
monotonic origin causality. It is order-independent for valid input, including
new-generation pages arriving before configuration proof. Unknown configurations
remain passive; events cannot create a monitor or select a conflicting head.

Evaluation first removes causally dominated events, then considers fresh maximal
events. A fresh maximal DOWN wins over unrelated concurrent UP signals. A later
accepted UP that observes the failure can dominate it and recover normally.
Once a concurrent DOWN expires, a still-fresh maximal UP may become effective.
If every maximal event has expired, the result is missed/down; dominated old
signals do not reappear. Diagnostic selection among equally effective concurrent
events uses deterministic origin order, not wall-clock recency.

The existing approximate-clock assumption applies. Event timestamps come from
nodes, never from clients. Events more than 30 seconds ahead of the receiving
clock are rejected; no maximum past age is imposed, so delayed valid pages can
arrive already expired. An accepted future timestamp can extend freshness by at
most that 30-second bound. Maintain synchronized node clocks and choose a
heartbeat interval appropriate to clock error and peer synchronization latency.
A clock outside the accepted range produces unknown rather than fabricated
success. Clocks are used for expiry, not last-write-wins conflict resolution.

Connected observers learn signals through ordinary peer synchronization and
schedule reevaluation on change. During a partition an observer that cannot
receive the signal can legitimately miss it; independent delivery remains
available. A connected topology still needs replication to finish before the
heartbeat deadline. An ingress acknowledgment cannot guarantee remote freshness.

The scheduler captures an evaluation witness under its mutex and rechecks it
before reserving a sample sequence or applying policy. The witness covers exact
generation, anchor, interval, register, and temporal result/deadline. A new signal
or expiry boundary invalidates precomputed work; arrivals also cancel in-flight
evaluation and schedule a new one. Thus an older computed expiry cannot commit
after a newer received signal, and a computed UP cannot commit past its expiry.

## Storage and interoperability limits

Heartbeat volume does not grow the latest register within a generation. Semantic
generation changes and monitor creation still grow retained state, alongside the
retained configuration graph. No safe cross-generation garbage-collection policy
is introduced here, and no forever-bounded storage/CPU claim is made. Actual
incoming merges copy retained state and validate causal compatibility; unrelated
snapshot writes use a no-change fast path. Peer page record/body limits remain
unchanged. Static membership is at most 32 nodes.

The register is not a complete immutable heartbeat log. Same-dot collisions are
detected while that dot is retained or co-present in a received page; superseded
old payloads cannot replace the latest slot. There is no retained payload digest
for every historical dot. Cluster peers are authenticated and trusted with the
private replicated configuration; the public client cannot construct wire events.

The coordinated peer protocol is **9**. Protocol 8 peers are rejected explicitly
because they cannot preserve the new heartbeat/anchor semantics. Upgrade peers
together; frozen protocol-8 evidence is historical and does not validate this
feature. The work does not provide JSONata, upstream notification-catalogue parity,
global exactly-once delivery, client timestamp ordering, or dashboard dependency.

## Verification

The focused Go tests use private temporary SQLite files and loopback HTTP only:

```text
.canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/internal/heartbeat -count=1
.canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/internal/canary -run '^TestPush' -count=1
```

Coverage includes every merge order of a partition/recovery chain; replay;
transitive-context/cycle/equal-dot rejection; copies, clocks, exact expiry and
generation isolation; HTTP auth, strict payload/query/wire checks, admission and
viewer redaction; unassigned ingress, real peer HTTP, delayed configuration and
old-generation pages; metadata grace, token/deadline changes, 71-head anchor
resolution; retry/inversion; SQLite restart, retained-origin restore activation,
and fail-closed SQL/origin failures; and arrival/time-boundary witness rejection.
Whole-release validation remains the integrating owner's responsibility.

Observed during implementation on 2026-10-22, using the bundled Windows Go
toolchain with `-mod=readonly`:

- The seven pure-engine tests and eleven push integration tests passed (engine
  0.489 s; selected canary package run 2.880 s).
- Push plus focused manual/retry/revision-join/origin-restore/readiness/tag/peer
  compatibility tests passed in 24.611 s.
- `go vet ./apps/node-go/internal/heartbeat ./apps/node-go/internal/canary`
  completed successfully.

Supplementary evidence reported by the integrating owner: its actual two-process
`apps/proof/test/push.test.ts` run passed in 18.296 s against a development
protocol-9 binary with SHA-256
`2730535a19b7a7caa5efa6948fcef1665b541f47b1904809906fa3a1c6ef3fee`.
It exercised one ingress feeding both observers, missed/down and resumed/recovery,
12.5 ms latency, message-secret redaction, rejected Bearer/GET/client timestamps,
and process kill/restart without accepted-signal replay. Sources were still being
integrated during that run; this is supplementary development evidence, not a
manifest or whole-release signoff. Earlier protocol-8 race evidence does not cover
this feature.

## First-review repairs and ingress index

The [first independent push review](push-review.md) identified four integration
defects in the earlier candidate: expiry crossing the final guard/commit window,
admission reset after a semantic edit plus restart, reverse management-credential
reuse, and public request work proportional to retained candidate heads. Its
unchanged `push_review_test.go` regressions remain part of the validation selection.
The earlier passing evidence above describes that earlier source, not clearance
of these findings.

The runtime now derives `pushTokenIndex` from every nondeleted push candidate,
including disabled and unready candidates and every head of a conflicted entity.
Keys are SHA-256 token digests. A value names a monitor only when exactly one
entity with a single candidate reserves that digest; the empty sentinel records
any duplicate or conflicted binding. A candidate beyond any earlier set of
16,384 heads still participates. Tombstoned candidates release their reservation;
a deleted sibling does not make an unresolved live sibling authoritative.

The integrating owner's builder publishes this derived map after successful SQL
configuration publication and rebuilds it for startup. It is not a database or
wire authority and needs no protocol change. Failed SQL leaves the prior index
intact. Edge-only proof completion still passes through current readiness checks.
The ingress helper additionally rebuilds the index when loading heartbeat state.
Unknown or ambiguous digests return the uniform 404 directly through a map lookup,
before reading the body or scanning accounts/retained heads. A unique binding
still verifies the current credential, readiness, enabled state, GET policy, and
local cluster/account/session exclusions. It repeats those checks under the node
mutex after the bounded body read, so rotation, disablement, or deletion during
that read cannot authorize the old binding.

The index grows with distinct retained candidate credentials; building it still
scans retained heads on configuration publication/startup. This moves that scan
out of unknown public-token requests and preserves valid unions. It introduces
no new retained-head ceiling, arbitrary winner, data truncation, or claim of
forever-bounded configuration processing.

Admission reconstruction now takes the maximum local-origin acceptance timestamp
across all retained generations for each monitor with a local-origin event.
Temporary disablement, missing revision proof, and conflicts do not erase this
reservation, either on restart or when another monitor accepts a heartbeat.
There is one map entry per retained monitor/local origin, not per request or
generation; total retained monitor identity growth remains a storage/memory cost.
Rotating
a token, changing the deadline/GET policy, and restarting within 100 ms therefore
retain the same monitor/origin admission interval. Health evaluation remains
scoped to the exact current generation. Management-token creation/updates use the
index's key presence, including ambiguous and unready bindings, to preserve the
credential-separation invariant in the reverse direction too.

For deadline handling, evaluation result, witness, deadline, and sample observation
time share an explicit clock observation. The captured deadline remains a schedule
cap even if SQL work crosses it. To clarify the earlier timing description: a
correctly timestamped pre-expiry observation may finish committing after expiry;
the due captured deadline must make the next scheduler tick eligible immediately.
There is no promise that a mutex or a witness freezes wall time during SQL work.

The new `push_ingress_repair_test.go` covers private SQLite restart, failed and
successful peer/config/rotation SQL publication, unready proof completion,
disabled/conflicted/tombstoned candidates, final reauthorization after body-time
edits, and exact-current/local-credential defenses. Its request-work fixture
validates every candidate/edge through bounded real peer-wire pages, privately
persists the retained snapshot, and reopens it through `New`. With two monitor
IDs, it measured **42 allocations per unknown-token request at both 32 and 16,386
candidate heads**, and no body reads for either an unknown digest or the late
unready duplicate. The five new test functions passed in a 4.772 s package run;
this measures the fixed request path, not full sibling-merge throughput or a
deployed saturation attack.

After the integrating owner completed the explicit-clock scheduler guard, the
final scoped current-protocol-9 checks completed successfully:

```text
go test -mod=readonly ./apps/node-go/internal/heartbeat -count=1
  PASS, 0.515 s
go test -mod=readonly ./apps/node-go/internal/canary -run '^TestPush' -count=1
  PASS, 10.094 s
go vet -mod=readonly ./apps/node-go/internal/heartbeat ./apps/node-go/internal/canary
  exit 0, no diagnostics
```

The push selection includes the unchanged first-review regressions and the five
new ingress-repair functions. The unchanged review fixture has SHA-256
`c0b8ae28287a728c9feea42cfde46d971bacee398ed4b8fa31bc7d256d336be2`.
An initial intermediate run still failed its already-expired-dispatch guard;
that failure was reported and the integrating owner corrected the scheduler
before this passing combined run. No earlier fixture was relaxed or edited for
these repairs. These are focused implementation checks; fresh independent review
and coherent whole-release validation remain separate work.


### Passive configuration admission follow-up

The next independent review reproduced a missed lifecycle case: startup filtered
admission by executable/enabled configuration, and another monitor could prune
a temporarily passive monitor?s entry. The integrating owner removed both
configuration-based gates. The unchanged seven-subcase
`TestPushFinalReviewAdmissionAcrossPassiveLifecycle` passed in 3.103 seconds.
The earlier `14cf56c7?` snapshot predates this correction and its full normal
suite is recorded as failing, rather than relabeled as a passing release.
Fresh combined validation remains separate.
