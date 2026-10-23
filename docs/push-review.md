# Independent first protocol-9 push review

Reviewed 2026-10-22. **Four remaining findings are established: one P1 deadline
race and three P2 integration/public-work defects.** Three new regression
functions intentionally fail against the captured candidate. A fourth finding
uses measured request-work evidence and passing ambiguity controls, without an
invented timing threshold. This is not release approval.

## PUSH-R1-1 — P1: crossing expiry after the witness check drops prompt reevaluation

Frozen locations: `apps/node-go/internal/canary/runtime.go:587`, `:619`, and
`:646–650`; `push.go:158` supplies the post-commit deadline.

Repro: `TestPushReviewDeadlineCrossingDuringCommit`, all three subcases.
These execute the real scheduler, retained sequence allocator, check policy,
SQLite sample/history transaction, and next scheduler tick. Only the supplied
clock is controlled; no production hook or implementation is changed.

The clock advances monotonically by one millisecond at a heartbeat boundary:

- Ordinary, before sample construction: the dispatch and final guard see
  1,400,999. The sample is timestamped 1,401,000, at exact expiry, but commits
  raw/effective UP. Current heartbeat evaluation is DOWN. `nextProbe` becomes
  1,401,999, and another tick at expiry retains the same stale sample sequence.
- Inverted, before sample construction: the same transition commits raw UP /
  effective DOWN. With the valid 86,400,000 ms retry interval, `nextProbe` becomes
  87,800,999: **86,399,999 ms after expiry**. The next tick does not reevaluate,
  although expired raw DOWN should now yield effective UP.
- Inverted, after deadline calculation: the sample is correctly timestamped
  1,400,999. `pushNextDeadline` returns 1,401,000, but the immediately following
  clock comparison reads 1,401,000 and discards that deadline. The same almost
  24-hour deferral follows. This subcase does not depend on an incorrectly
  timestamped sample.

The final witness guard correctly rejects expiry that occurs before that guard;
`TestPushReviewDispatchGuardRejectsAlreadyExpiredWitness` passes and confirms
that sample, policy, and sequence remain untouched and reevaluation is immediate.
The remaining gap is later. A deadline which has just become due is ignored,
and an evaluation returning deadline zero after expiry also leaves generic
policy spacing intact. The mutex cannot stop time from crossing this boundary.

Required behavior: keep the evaluation time consistent with the sample's
observation time, retain the pre-policy/pre-sequence witness check, and make an
expiry crossed during reservation/commit/deadline scheduling immediately
eligible for reevaluation. Do not discard an already-due captured heartbeat
deadline in favor of ordinary interval/retry spacing. Preserve external sequence
reservation gaps and ordinary policy semantics.

The controlled-clock repro establishes the interleavings and resulting stored
schedule, not their frequency on a deployed host. No 24-hour wall-clock wait was
performed or needed.

## PUSH-R1-2 — P2: generation change plus restart bypasses 100 ms admission

Frozen locations: `apps/node-go/internal/canary/push.go:53` and `:352–358`.

Repro: `TestPushReviewAdmissionAcrossGenerationRestart`, subcases `token`,
`interval`, and `allowGet`.

A real POST commits a heartbeat at 1,100,000. At 1,100,050, a management edit
starts a new generation. A second POST on the live node returns 429. Stop and
reopen that same private SQLite database with the retained origin directory and
unchanged clock: the identical POST returns 200. SQLite now retains accepted
events for two generations only 50 ms apart.

Restart empties `pushAdmission`; its persisted fallback looks up only the
current generation's event. The previous generation remains durable but is not
consulted, so the per-monitor admission contract differs across restart.

Required behavior: reconstruct or persist the latest local acceptance per
monitor/static origin across semantic generations. Keep the in-memory map
bounded by applicable monitor IDs and configured origins, without token/client
keys or unbounded request admission state. Generation isolation for health
evaluation must remain intact.

## PUSH-R1-3 — P2: account writes can promote an existing push secret to Bearer

Frozen locations: `apps/node-go/internal/canary/accounts.go:67–75`, `:164`,
`:176–181`; compare the opposite-direction checks in `groups.go:9–20` and the
ingress exclusion in `push.go:299–303`.

Repro: `TestPushReviewManagementTokenCannotReusePushSecret`, `create` and
`update` subcases, using real account and push HTTP endpoints.

Before the account write, the configured push secret receives 401 as a
management Bearer and works as a push credential. An administrator can then
create an editor account with that existing secret (201), or update a distinct
editor account to use it (200). The push secret subsequently receives 200 on
`GET /api/overview` as a management Bearer, while its push endpoint returns 404.

Push configuration rejects preexisting management credentials, but the account
write path checks the cluster token and other accounts only. It does not check
existing live push candidates. The ingress exclusion fails closed after the
collision, but cannot preserve credential separation or the monitor's ingress.

Required behavior: apply the credential-separation invariant when creating or
changing management tokens too, under the account commit lock. Conflicted and
unready live push candidates must participate. Do not make arbitrary peer
configuration capable of revoking an independently authorized management
credential as a substitute for local write validation.

This requires an authorized administrator to configure the collision; it is
not evidence of unauthenticated privilege escalation. The concrete defect is
asymmetric validation, accidental promotion of a distributed job credential,
and loss of its heartbeat ingress.

## PUSH-R1-4 — P2: unknown credentials force a full retained-head scan under the node lock

This candidate was suggested by the integrating owner and independently
reproduced here. Frozen locations: `apps/node-go/internal/canary/push.go:293–335`
and the repeated `matches(hash, m.Push.Token)` at `:318`; `runtime.go:83–85`
implements hashing. The public-status boundary separately budgets retained-head
and byte scanning in `status_pages.go:18–22,489`; push has no corresponding
request-work bound or lookup index.

Repro/evidence: `TestPushReviewUnauthenticatedHeadFanoutWork`.

The fixtures have only **two monitor IDs**. One retains 64, 4,096, or 16,385
concurrent push heads; the other has one ready head. Every generated header and
edge passes the actual peer-wire validator in bounded pages. The fixture stores
the retained head set/edges in private SQLite, stops, and successfully reopens
through `New`. Its first fork has complete proof; its final fork deliberately
awaits its separately paged edge. That unready final candidate also duplicates
the ready monitor's secret.

An unchanged two-byte POST body and a valid-shaped unknown 64-hex secret return
404 in every case. Measured allocations per request are:

- 65 total candidate heads: **189 allocations**.
- 4,097 total candidate heads: **8,253 allocations**.
- 16,386 total candidate heads: **32,831 allocations**.

The counts are exactly two additional allocations per additional candidate in
these fixtures. The final run observed approximately 1.015 ms/request at 4,097
and 2.960 ms/request at 16,386; an earlier isolated run observed 0.996 ms and
3.331 ms respectively. These are small, local in-process HTTP-handler samples,
not throughput guarantees. The smallest final timing rounded to zero on this
Windows host; allocation counts were stable.

Both the unknown secret and the ready secret duplicated in the late unready
head receive identical 404 bodies. No heartbeat/admission state is created.
The scan occurs before credential success and before the 100 ms admission gate,
while holding the mutex used by probes, replication, and delivery. Repeated
unauthenticated requests therefore repeatedly pay retained-head hashing/heap
cost without possessing a monitor secret. Static membership and monitor-ID
ceilings do not bound the number of restore-generated concurrent heads.

This is a distinct public-request multiplier over valid retained state, not a
new claim that internal retained history or reconciliation must be bounded.
No saturation attack, deployed availability failure, arbitrary-history latency
bound, or full 16,385-head peer-merge/restore-activation run is claimed. Direct
private snapshot seeding intentionally excludes unrelated sibling-merge cost;
wire validation and runtime reopen establish the fixture's admission/readiness
properties. The measurement test passes because it records actual work and
fail-closed behavior instead of inventing a numeric performance assertion.

A digest lookup index is compatible with recovery and fail-closed semantics:

- Include every nondeleted push candidate, including disabled, conflicted, and
  unready candidates. Preserve per-monitor matching and exact ambiguity rules;
  indexing only current ready monitors would authorize the late-duplicate case.
- Build/rebuild from retained heads on startup. Stage index changes with
  configuration changes and publish only after their SQL commit. Missing proof,
  edge-only completion, conflict resolution, deletion, and restart must not
  leave a stale ready binding.
- An unknown digest should require bounded lookup work. A unique matching
  monitor can still pass through the existing current/readiness and credential
  exclusions before acceptance. Keep secrets out of responses and diagnostics.
- Keep valid replicated/restore-generated head unions. Do not solve public
  request cost by dropping arbitrary retained heads, choosing an early match,
  raising ceilings, or turning expiry/causality into last-write-wins.

No index or other production fix was implemented by this reviewer.

## Executed evidence and scope

All tests ran from the private frozen source below with the bundled
`go version go1.27.3 windows/amd64` toolchain and `-mod=readonly`:

```powershell
go test -mod=readonly ./apps/node-go/internal/heartbeat -count=1 -v
go test -mod=readonly ./apps/node-go/internal/canary -run '^TestPush' -count=1 -v
go vet -mod=readonly ./apps/node-go/internal/heartbeat ./apps/node-go/internal/canary
```

Final engine run: **seven functions pass, 0.490 s, exit 0**. Final canary push
selection: **16 functions pass and three fail, 8.119 s, exit 1**. All eleven
preexisting push functions pass. The three failing functions are exactly the
reproductions for findings 1–3, with eight failing leaf subcases (3 admission,
2 account, 3 deadline), not eight separate findings. Vet exits 0 without output.

The five passing review functions independently cover:

- Peer config/event SQL rollback after event insertion, unchanged retryable
  page, passive unready config, uniform 404, edge completion, and restart.
- Actual dispatch rejection when expiry precedes the final witness check,
  without consuming sample/policy/sequence authority.
- Local settings-sequence write failure after heartbeat insertion, SQL rollback,
  unchanged memory/admission/schedule, and retained allocator gaps on restart.
- Expired causal maxima not resurrecting still-fresh dominated UP signals,
  including accepted clock skew; exact concurrent-DOWN expiry exposing a fresh
  concurrent UP, then that UP expiring.
- Public request fanout measurement and late unready duplicate fail-closed
  behavior after runtime reopen.

Inspection covered the requested engine/tests and actual root types, runtime,
API, wire/paging, causal store, revision joins, lifecycle, validation, groups,
viewer projection, restore activation, shared TypeScript contract, and related
origin/account/policy/storage/publication code. The engine's causal maximal
selection, same-dot/context checks, deep copying, unknown-generation passivity,
generation-anchor construction, viewer token blanking, own-secret message
redaction, and protocol-9 gate were inspected and their existing focused tests
passed where present. This is scoped evidence, not proof over every history.

The known SR5 causal defects and ongoing repair are separate context, not new
push findings here. No full node suite, race run, container/process proof, UI,
or live-provider validation was executed by this reviewer. All fixtures use
temporary SQLite databases and in-process/loopback HTTP; nodes never `Start`.
No user database, `.env`, default executable, development port 3100/5173,
commit, nested agent, production/core/shared/module/frontend edit, or earlier
fixture edit was used.

Separately reported by the integrating owner, not executed or recertified
here: its coherent protocol-9 Windows normal run also reproduced exactly the
three canary regression functions. That full run additionally failed ICMP
fixtures because its source snapshot selector omitted `.txt` testdata; the
owner corrected that selector and reported separate native/Alpine ICMP passes.
Its 13/13 actual compiled-process proofs passed in 88.439 s with unchanged
source; a terminal display-wrapper encoding error occurred after result files
were written. These reports do not clear the edge cases or establish a passing
full protocol-9 suite.

## Exact source and artifacts

Before any test, 211 source/module/contract/context files were copied with
before/copy/after byte checks to:

```text
C:/Users/Michael/AppData/Local/Temp/canary-push-review-j3w66fzs
```

All 211 private input hashes were checked unchanged after testing. The shared
checkout has no accessible Git metadata; no commit identity is asserted.
Observed concurrent shared drift affected `probeworker/process/process_test.go`,
`probeworker/process/run.go`, and `docs/partition-causal-repair.md` at the recorded
recheck; none was copied over the frozen source or attributed to this review.

SHA-256 values (the first two match the owned handoff hashes exactly):

```text
heartbeat/heartbeat.go       b94caf7d274c3c58b5dadb3fa7d06dede2bf51c2fbba5e6063f1a0cbc615cd37
canary/push.go               8f07aa9b890660a338346bd281b5f9ee0578c687a7c732646b50aa4dd548c2a7
heartbeat/heartbeat_test.go  882cda8aa980545ca15170d5940d046cdb76ef4d5ff09c846a240008490d34e9
canary/push_test.go          10391ef4ee03768d4f773d9cb8943c314c380d5bb5a8e0dcd466ce12f2c5e463
canary/push_review_test.go   c0b8ae28287a728c9feea42cfde46d971bacee398ed4b8fa31bc7d256d336be2
canary/runtime.go            82c033335ba45abf4843d010f217bfc2716ee56cc53ee2464474dbd98d49632b
canary/accounts.go           4e6b70235c17963fc3bd2e6a3a9d5e91b1b3838be01b70cd998853472431bc5a
canary/status_pages.go       2cef00037b30bc2ca900f423982cfbefd23ce885819f2961d91bbf5d9ffbb046
go.mod                      2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a
go.sum                      6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
source-manifest.json        8165ee056b9e94621ed4a768339826c6f6694c27366616f44f0d89246b5d38a7
push-final-tests.log        a43e743f00de0d7c0223e7aac16fa85d397ae18d91abdeb9cc7af2338c2ef18e
heartbeat-final-tests.log   c58b54785e20ce9b2ad4253d6aae7fa61e917ec2731207f6e21795b65c578e09
```

The manifest contains every captured input hash. `review-validation.json`
records final function outcomes, log hashes, private-source verification and
vet's empty successful result. The new review test is byte-identical in shared
and private locations. Reviewer shared writes are exactly
`apps/node-go/internal/canary/push_review_test.go` and this report.
