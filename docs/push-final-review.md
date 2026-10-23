Independent protocol-9 push review, round two, 2026-10-22.

**Post-repair result: all 32 push functions pass, 15.831 s, exit 0.** The
integrating owner repaired the admission defect identified below. A separate
byte-checked post-repair snapshot passed the unchanged final review fixture,
all prior push controls, heartbeat tests, and vet. No concrete push finding
remains reproduced in that post-repair artifact. This is focused review evidence,
not whole-release approval; the two source identities and results are kept separate.

**The initial frozen candidate reproduced one P2 in four lifecycle cases.**
Its combined push run has 31 passing functions and one failing function
(four failing leaf cases), exit 1, 19.719 s. All 24 previously supplied push
functions pass unchanged. Seven of the eight new review functions pass. This
initial evidence describes the exact frozen pre-repair source below; the later
post-repair check is recorded separately near the end of this report.

**PUSH-R2-1: passive configuration loses durable per-monitor/origin admission.**

Frozen source locations: `apps/node-go/internal/canary/push.go:57` through the
startup filter at line 60, request fallback at line 375, and pruning at lines
418–422. Reproduction: `TestPushFinalReviewAdmissionAcrossPassiveLifecycle`
in `apps/node-go/internal/canary/push_final_review_test.go:206`.

A real public POST commits an UP at 3,300,000 ms. The clock advances by only
50 ms. The same monitor subsequently receives another HTTP 200 and a second
durable local-origin event at 3,300,050 ms in each of these cases:

- Disable the monitor, restart the same private SQLite database with its retained
  origin directory, and re-enable it.
- Receive a token-changing descendant without its separately paged edge,
  restart while configuration is unready/conflicted, then receive the missing
  edge. This uses authenticated `/internal/snapshot` HTTP for both pages.
- Receive two proven concurrent descendants, restart while conflicted, and
  resolve all heads into an enabled generation. The incoming page uses the
  authenticated peer HTTP boundary.
- Disable the monitor, accept an unrelated monitor's heartbeat (which prunes
  the first monitor's admission entry), and re-enable it without restarting.

The three restart cases advance the persisted local sequence from 1 to 65,537;
the live pruning case advances it from 2 to 3. The retained allocator gap is
expected. The defect is the second successful commit only 50 ms after the first.
Both timestamps are loaded from the actual `heartbeatEvents` SQL category.
The configured identity and monitor ID are unchanged.

Three matching live controls, without restart or unrelated-monitor pruning,
return 429 at +50 ms, retain one local event and the previous sequence, remain
429 at +99 ms, and accept at exact +100 ms. In every case the old-generation
health signal remains passive: evaluation of the newly active generation is
pending before the attempted second heartbeat. These controls distinguish
admission continuity from the intended health-generation reset.

Source-level cause: startup reconstruction computes a maximum across generations
only for an enabled monitor returned by `current()`. Disabled monitors fail its
`Enabled` check; unready/conflicted monitors make `current()` return nil. Their
durable timestamps are omitted. Separately, a successful heartbeat for any
monitor removes admission entries whose current configuration is passive.
Re-enable, edge completion, or conflict resolution then authorizes a generation
with no current-generation event. The request fallback checks only that exact
generation, so the older durable acceptance no longer enforces the 100 ms gate.

Required behavior: preserve the maximum accepted local-origin timestamp per
retained monitor ID across generations and temporary passive configuration.
Configuration readiness, enabled state, token rotation, and health authority must
remain separate checks. Keep admission keyed by retained monitor/local origin;
do not scan retained history on public requests or create keys from arbitrary
submitted credentials. The integrating owner confirmed both source-level causes
and proposed removing the startup configuration filter and config-based pruning.
The later implementation is outside this first frozen result and was checked in
the separate post-repair artifact below.

This requires configuration changes/restart or a passive replication state; it
does not establish unauthenticated privilege escalation or arbitrary-token state
growth. All four failures assert the same admission invariant and are one finding.

**Evidence that the other repairs survive the independent controls.**

- `TestPushFinalReviewTemporalCommitSweep` passes all 30 combinations of five
  temporal transitions and six clock-read boundaries. It covers fresh UP,
  inverted UP, initial grace with and without inversion, and concurrent DOWN
  expiry exposing a still-fresh UP. Retry spacing is the valid 24-hour maximum.
  A boundary before the final guard consumes no sample, policy, or sequence;
  a stable earlier observation keeps its own timestamp and captured deadline.
  Crossing on the history-cutoff clock read after sample/policy/sequence SQL
  writes still makes the next tick observe the boundary immediately.
- The unchanged first-review deadline regressions pass, including
  `TestPushReviewDispatchGuardRejectsAlreadyExpiredWitness`. Their committed
  pre-expiry samples are timestamped 1,400,999, capped at deadline 1,401,000,
  and replaced by raw DOWN on the next tick. The ordinary retry/inversion
  integration test also passes unchanged.
- `TestPushFinalReviewEnabledAdmissionMaxAndOriginIsolation` passes: restart
  takes the maximum of two old local generations, ignores a newer remote-origin
  timestamp for admission, rejects through +99 ms, and accepts at +100 ms. The
  accepted current-generation UP causally dominates the known remote DOWN.
- `TestPushFinalReviewReverseCredentialCandidateMatrix` passes account creation,
  update, and the legacy locked API path against ready, disabled, unready,
  conflicted, duplicate, and live-with-tombstone reservations. Rejections leave
  account cardinality and management authority unchanged. A fully deleted
  monitor releases its reservation. `TestPushFinalReviewPeerCollisionPreservesManagementAuthority`
  confirms private peer push configuration cannot revoke an already authorized
  management token; public push still fails closed before reading the body.
- `TestPushFinalReviewIndexLateSQLFailureAndEdgePublication` passes. A SQL failure
  on the head write rolls back the earlier heartbeat and immutable edge writes,
  retains the original SQL/memory head, and leaves the digest index unchanged.
  A partial rotated descendant reserves an ambiguity sentinel alongside its
  retained parent, including after reopen. Edge-only completion removes that
  conflict and publishes the unique binding; the ready credential then works
  after the seeded event's normal admission interval.
- `TestPushFinalReviewFinalReauthorization` passes body-time rotation, new unready
  duplicate, conflict, cluster/account/session overlap, and GET-policy change.
  Each returns uniform 404 with no event, admission entry, or sequence reservation.
  Local credential overlap is private fixture state; peer-collision management
  continuity is separately exercised through the real peer HTTP boundary.
- `TestPushFinalReviewBoundedUnknownPathWithFullLocalAccounts` records **42
  allocations/request** at both 32 and 16,386 retained candidate heads with
  64 local accounts. Unknown requests never read an 8,192-byte body. Filling
  the account map changes no allocation count. The reused fanout constructor
  validates every header/edge through bounded real wire pages, persists a
  private snapshot, and reopens through `New`; the request/account assertions
  are new. The unchanged fanout controls also pass, including the late unready
  duplicate. This is request-work evidence, not a deployed throughput bound.
- All unchanged local allocator/settings-write rollback, peer-page atomicity,
  generation, partition, concurrent-DOWN precedence, expiry, restore, payload,
  viewer-redaction, and authentication controls selected by `^TestPush` pass.

An initial new index fixture incorrectly expected a unique index binding for an
unready descendant whose parent remained retained. A second intermediate
expectation overlooked the seeded local event's 100 ms admission interval.
Those test expectations were corrected before the final combined run. A helper
was also corrected to encode the peer's required `groupVersions` as an empty
array. None is a product finding; no prior fixture or production code was edited.
The final fixture's exact hash is recorded below.

**Commands and reproducibility.**

Before testing, 239 Go/module/testdata/contract/context inputs were copied with
before/copy/after byte checks to:

```text
C:/Users/Michael/AppData/Local/Temp/canary-push-final-review-4nebqitc
```

`source-manifest.json` contains every captured input path and SHA-256. The only
additional Go input is the new `push_final_review_test.go`, byte-identical in the
private and shared locations. All 239 frozen inputs were verified unchanged after
the final tests. At the 14:19 EDT integrity check, all shared inputs still matched
as well. Later concurrent root repairs must be identified separately. The shared
checkout has no accessible Git metadata; no commit identity is asserted.

Toolchain: `go version go1.27.3 windows/amd64`, using the existing bundled
`C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe`.
Commands ran from the private directory with `GOPROXY=off`, `GOTOOLCHAIN=local`:

```powershell
$reviewGo = 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe'
$env:GOPROXY = 'off'
$env:GOTOOLCHAIN = 'local'
& $reviewGo test -mod=readonly ./apps/node-go/internal/canary -run '^TestPush' -count=1 -json
& $reviewGo test -mod=readonly ./apps/node-go/internal/heartbeat -count=1 -v
& $reviewGo test -mod=readonly ./apps/node-go/internal/checkpolicy -count=1 -v
& $reviewGo vet -mod=readonly ./apps/node-go/internal/heartbeat ./apps/node-go/internal/canary
```

Pre-repair results: push selection 31 functions pass / 1 fails, 19.719 s, exit 1;
heartbeat seven functions pass, 0.578 s, exit 0; checkpolicy passes, 0.503 s,
exit 0; vet exits 0 without diagnostics. `push-validation.json` records every
function outcome and the four exact failing leaf names. `push-final-tests.jsonl`
retains the original test events; `push-final-tests.log` contains their readable
output. `controls-validation.json` records control commands/results and
`integrity-check.json` records hashes and post-test equality checks.

To reproduce just the remaining finding against this artifact:

```powershell
& $reviewGo test -mod=readonly ./apps/node-go/internal/canary -run '^TestPushFinalReviewAdmissionAcrossPassiveLifecycle$' -count=1 -v
```

SHA-256 values for the reviewed artifact and final evidence:

```text
heartbeat/heartbeat.go       b94caf7d274c3c58b5dadb3fa7d06dede2bf51c2fbba5e6063f1a0cbc615cd37
heartbeat/heartbeat_test.go  882cda8aa980545ca15170d5940d046cdb76ef4d5ff09c846a240008490d34e9
canary/push.go               d18817fb67fd6027129972ccfe085348da77453b200f80d43c32526070a7aaf2
canary/runtime.go            424bfc9429a35af4ff99e2f970259272bb6f3d7b04e2c183d9722e2fe38879a2
canary/accounts.go           160dba5263a64a34b1e86508027f586349d636a57ed1f730ec835a4077834a19
canary/api.go                401b9e50b6cb59ea2c4f4208bedec14ef4ed6bdadc6f5556ed47af7b84e623e2
canary/credential_boundary.go 959e31fa7fd6408c4d7247d2a2f1efcaf0be3005c99df062e36e6f5d61b3a2f7
canary/causal_store.go       bddf0b5880c55718a49c2be669ddb8a37fb55b7a74dea191ed44851e782eeb78
canary/causal_uncertainty.go ae5881a0ad1873b02cfd4fb0e64973c116fb97616c1f848c1ed7c4c7f74832e8
canary/push_test.go          10391ef4ee03768d4f773d9cb8943c314c380d5bb5a8e0dcd466ce12f2c5e463
canary/push_review_test.go   c0b8ae28287a728c9feea42cfde46d971bacee398ed4b8fa31bc7d256d336be2
canary/push_ingress_repair_test.go f36eb82113c0913c128bc99a1d49ad621940b9fcec9d1cc2fcd6ee670c6707c8
canary/push_final_review_test.go 3567bfe0229d615d026c748f05206b833b8c2dcf646206abfa888836d1585019
go.mod                      2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a
go.sum                      6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
source-manifest.json        e369c44bfe170786376ae940ca61dbd85a3e80f296f38f6dfe3b3e556a851924
push-final-tests.jsonl      bdb3017520813144fb7324ac7ad1b3c4c7b619fafeb88261fe57b1a6d70c695a
push-final-tests.log        dcaab7b3a869ae1a551e57ea11f029dfb6719ae09c967513cbf30749eacaf1af
heartbeat-final.log         e2e1dd44752b761aad95906473f5fef4ff6d2e733cd6c25ea4ea7d5688bf0e6f
checkpolicy-final.log       ee8d3a86887574f8dc146c508c5541ab917a391c897b3025aa25db0f470209a6
vet-final.log               e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

**Separate post-repair verification.**

After the integrating owner changed admission, a new 242-input snapshot was
captured with the same before/copy/after byte checks:

```text
C:/Users/Michael/AppData/Local/Temp/canary-push-final-repaired-2scqgjdh
```

Among the original 239 captured inputs, only `canary/push.go` and
`docs/push-monitors.md` changed. Three inputs were added: the unchanged final
review fixture, this report's then-current draft, and another owner's
`canary/closure_standalone_repair_test.go`. That last fixture compiled with the
package but was not selected by `^TestPush`. No other production source changed
between these two captures. The scheduler, credential/index publication code,
heartbeat engine, and all previous push fixtures retain the earlier hashes.

Startup now takes the maximum timestamp over every retained local-origin
heartbeat, without configuration gating. Acceptance no longer prunes another
monitor's entry based on passive configuration. Each admission key remains a
retained monitor/local-origin pair; no request-time history scan was added.
The documentation now describes retained monitor IDs rather than only current
enabled monitors. These are integrating-owner production edits, not reviewer edits.

Using the same toolchain and offline module settings, the following commands
ran against this second private snapshot:

```powershell
& $reviewGo test -mod=readonly ./apps/node-go/internal/canary -run '^TestPush' -count=1 -json
& $reviewGo test -mod=readonly ./apps/node-go/internal/heartbeat -count=1 -v
& $reviewGo vet -mod=readonly ./apps/node-go/internal/heartbeat ./apps/node-go/internal/canary
```

Push: **32 functions pass, zero fail, 15.831 s, exit 0**. All seven passive
lifecycle subcases pass unchanged, including four formerly failing cases, their
three live controls, +99 ms rejection and exact +100 ms admission. Heartbeat:
seven functions pass, 0.437 s, exit 0. Vet: exit 0, no diagnostics. The earlier
checkpolicy pass is retained as earlier evidence; it was not rerun for an
unchanged policy package in this second capture.

All 242 frozen inputs were verified unchanged after testing. The final fixture
still matches the shared copy at SHA-256
`3567bfe0229d615d026c748f05206b833b8c2dcf646206abfa888836d1585019`.
No first-review or repair fixture was changed. The second snapshot's manifest,
test event stream, readable output, summaries and integrity check are retained
separately from the pre-repair evidence:

```text
canary/push.go              289f1b8b813cca593f22d747cb136af1bf3abc64d5aa517841cab70447e10216
source-manifest.json       b601629c85a2202df384450f073a08bb4e82c1835e7511c18b98e001827e4891
push-repaired-tests.jsonl  815976651b516f27142628826005bf7f55d9f2230702ba210bb05c677f82e900
push-repaired-tests.log    64ccb0db6f972b327accf4eb0eb5423ba194caa5a28665440f6429e54695d205
heartbeat-repaired.log     ec55d09e18b2f76408c19a0f9aa685b94e210ab43b712d8c2a55c5fc29cc31cf
vet-repaired.log           e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

**Limits and shared writes.** No production/core/shared/module/UI edit, earlier
fixture edit, `.env` read, user database, default executable, development node,
live external traffic, commit, or nested agent was used. Nodes are never started;
fixtures use isolated temporary SQLite/origin directories and in-process or
loopback HTTP. The final test file and this report are the only reviewer-owned
shared writes.

This is focused Windows source/SQLite/scheduler evidence. The clock fixtures
establish interleavings and timestamps, not their frequency on a deployed host.
The fanout fixture intentionally excludes full sibling-union merge throughput.
No complete node suite, fresh race run, compiled-process proof, Alpine/container
execution, UI, or live-provider validation was performed for this artifact.
Earlier frozen8/frozen9/14cf process or race runs predate these new fixtures and
are not either artifact's whole-release signoff. Concurrent causal-specialist
work and any further changes require their own coherent source identity and
validation; they are not silently substituted into these frozen review results.
