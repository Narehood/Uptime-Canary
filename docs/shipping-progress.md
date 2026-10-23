# Shipping progress

## Validated main checkpoint: protocol 14, 2026-10-23

The ready-to-ship independent Go/SQLite HA system with near-Uptime-Kuma
monitoring and notification abilities remains the goal. The green workflow is
an acceptance milestone, not a declaration that all parity or release work is
done. Original failures and the older checkpoint below remain historical.

- Runtime commit `77416a1` adopts official JSONata 2.2.2 on Goja with the finite
  profile and optimized private guards. The old evaluator has no production
  fallback. Its seven reported false-healthy mechanisms are resolved by the
  replacement; all 130 unchanged independent review cases pass on actual
  Alpine. Narrow legacy expectation changes are documented in
  [the migration notes](../apps/node-go/internal/jsonquery/RUNTIME_MIGRATION.md).
- All 19 Windows native node packages pass. Node-wide vet, provenance, notices,
  module verification and static Linux amd64/arm64 builds pass under the
  storage guard. Arm64 execution is still a separate gate.
- [GitHub workflow 39972484403](https://github.com/Narehood/Uptime-Canary/actions/runs/39972484403)
  passes node, web and race jobs: 11 node runtime checks, the web build/runtime,
  and the Portainer stack configuration. Alpine race-parent runs contain 869
  core and 1,527 worker-parent passing events, zero failures/skips/races. Helper
  packages pass race tests separately. Production worker children intentionally
  remain uninstrumented under their original caps.
- Protocol 14 is a coordinated cluster upgrade. It rejects older peers;
  monitor options and notification destination identities are unchanged.
- Storage remains a requirement: repository below 3 GiB, idle below 1.25 GiB,
  scratch below 512 MiB, one owned cache and one validation at a time. No new
  source snapshots, binary archives or persistent validation VMs are permitted.

### Refreshed acceptance milestone and next work

- Windows: all 17 current real-process tests and the refreshed 124/124 dashboard
  contract pass against the same development binary. A separately scoped
  controlled 13-to-14 upgrade proof passes, including mixed-peer refusal,
  accounts/configuration, monotonic origin sequences and retained receipts.
- Current source-symbol/advisory gate: no reachable Go finding, zero npm
  advisories, and explicit verified Goja/JSONata version and commit lookups
  without active advisories. The module-only OpenPGP finding remains disclosed.
- [Workflow 39991889659](https://github.com/Narehood/Uptime-Canary/actions/runs/39991889659)
  on `4d51a6a` passes all four node/web/race/proof jobs. It adds actual Alpine
  execution of all 17 process proofs and actual Compose runtime checks for the
  Portainer template. Core 869 and worker-parent 1,527 race events pass, with
  helper race packages separate. All 560 manifest entries match the checkout.
- One new standalone validation-fixture import defect was reproduced and
  repaired; the failed predecessor run remains preserved. Product runtime and
  dashboard source are unchanged from `77416a1`. See the exact
  [checkpoint evidence](protocol14-checkpoint.md).
- Continue missing publication, monitor and provider capabilities in
  [the parity inventory](parity-audit.md). The user selected public observed
  availability/history and uptime badges for the active protocol-15 wave
  described below. SMS stays deferred.
- Retained-history operating costs, capacity/backlog recovery and reproducible
  tagged release artifacts still need deliberate release work. Bounded wire
  pages do not bound total retained evidence. Arm64 was compiled, not executed.

There is no tagged release or release certification at this checkpoint.
See [container evidence](container-validation.md) for the exact current run.

## Active feature: protocol 15, public observed availability

[Draft PR #1](https://github.com/Narehood/Uptime-Canary/pull/1) contains explicit
per-service publication of the serving node's last 24 hours of observed
availability, hourly history and uptime badges. This remains one node's view,
with separate known-time availability and coverage; it is not merged HA uptime.
Default-off publication, proof revalidation, SQL budgets and credentialless
redaction are specified in [the feature contract](public-availability.md).
Protocol 15 requires coordinated peer upgrades; main remains protocol 14.

- The backend at `1e3dd97` passed all four Alpine CI execution jobs in
  [workflow 40003648085](https://github.com/Narehood/Uptime-Canary/actions/runs/40003648085):
  11 node checks, web runtime checks, all 18 process proofs, combined-template
  Compose runtime, 887 core and 1,527 worker-parent race events. The CodeRabbit
  cache-contention finding has a before-fix reproduction and passing repaired
  regression. Capped worker children remain deliberately uninstrumented.
- Documentation head `871717c` also passed all four jobs in
  [workflow 40007603974](https://github.com/Narehood/Uptime-Canary/actions/runs/40007603974).
  Neither run includes the held, unfinished frontend helpers. CodeRabbit's
  later green status means draft review was skipped, not that it re-reviewed
  the repair.
- `0c3d131` adds [separate dashboard and node Compose/Portainer files](portainer.md).
  Both preserve the combined template's hardened service settings. Bounded
  local syntax/parity/source-selection checks and three proof-runner tests
  pass. [Workflow 40013131856](https://github.com/Narehood/Uptime-Canary/actions/runs/40013131856)
  passes all four jobs, including actual configuration parsing and separate
  Compose projects with CORS, independent networks and restart persistence.
  All 565 source-manifest entries match that commit. No Portainer-host or
  cross-host deployment has been exercised.
- Opus completed the editor opt-in, public history presentation and uptime
  badge sharing. The reviewed interface passes workspace typechecking, 15 pure
  checks, a disposable production build and **139/139** browser assertions.
  The first browser run's navigation defect and its two blocked assertions
  remain recorded as a failure. The subsequent Date-range finding has a failing
  coherent-boundary regression and passing repair, with projection and adjacent
  announcement safety checked in the browser. Host-local reverse-proxy guidance
  now retains loopback binding. A controlled **14-to-15** process upgrade also
  passes, preserving exact historical totals, identities, account roles and
  receipts without opting old pages in. This is native Windows rebuilt-source
  evidence, not original release-artifact or Alpine upgrade evidence. Exact
  source hashes and results are in [the feature contract](public-availability.md).
  The complete integrated source still needs its own CI result before merge.

### Release gates still open

1. Complete and validate the integrated protocol-15 feature, including frontend,
   its new deployment files and the exact source proposed for merge. Local
   interface and 14-to-15 acceptance have passed; the earlier protocol-14
   controlled 13-to-14 proof remains a separate historical result.
2. Reconcile remaining monitor families and protocol/provider options against
   the pinned [parity inventory](parity-audit.md). System-service, PM2, browser,
   Tailscale, GameDig and Steam checks are still absent; native provider coverage
   is eleven, not the full upstream catalogue. Status-page domains, styling and
   subscriptions remain gaps. These are not waived by backend CI passes.
3. Keep recovery, mixed-peer refusal, identities, retention and backlog behavior
   tied to each final source. Retained causal history still has growing storage,
   heap and catch-up costs. Arm64 compilation is not arm64 execution.
4. Produce a reviewed versioned release with checksummed artifacts and Alpine
   images, repeat applicable dependency/provenance and coherent acceptance
   gates, and document installation, supported limits and coordinated upgrades.
   No tag, published release binary or release-image validation exists yet.

Storage remains below the enforced idle budget, with one owned cache and zero
scratch after the split-template local checks. No VM, source snapshot or binary
archive was created. The complete independent Go/SQLite HA and near-Kuma goal
remains active; the draft feature and green checkpoints are not completion.

## Historical development checkpoint: 2026-10-22

The record below is preserved from earlier source. Statements that the
JavaScript replacement is private or JSONata is still blocked refer to that
older checkpoint, not protocol 14. Its source-specific failures are not erased
or relabelled as passing by the new workflow.


Development checkpoint: 2026-10-22. **The release goal is active; this is not a
ready-to-ship claim.** Evidence belongs to the source snapshot recorded by each
test, rather than automatically covering subsequent edits.

## Agreed requirements

Independent Go/SQLite monitoring nodes, optional separate React/TypeScript
dashboard, Alpine containers, user-defined groups, HTTP(S) and ICMP, HA delivery
and reconciliation, explicit configuration conflicts, accounts and server-side
roles remain requirements. Delivery takes priority during partitions, so
duplicates are allowed. The expanded goal is monitoring/alert capabilities near
Uptime Kuma. Priority destinations are Discord, SMTP, Mailgun, Brevo and
Mailchimp Transactional; SMS is later. The license is MIT. Opus 5.5 xhigh owns
dashboard/visual design.

Static membership, any-fresh-down/all-fresh-up incident policy, local accounts,
one selected dashboard endpoint, exact group-dependency/grace mechanics and
maintenance completion policy are implementation choices. They are not new
requirements or waivers of remaining parity work.

## Implemented and checked

| Area | Current evidence |
| --- | --- |
| Check engines | 26 runtime/API types including manual, passive push and Globalping, 16 service engines, and active network protocols. ICMP packet options, HTTP OAuth2 and HTTP/MQTT/SNMP/MongoDB JSONata are integrated. Independent integration reviews found repaired MQTT matching and Globalping viewer-redaction defects. The current Go evaluator still has known false-healthy behavior; a separately reviewed replacement remains private pending its Alpine gate |
| Notifications | Provider acceptance, per-recipient retries, persisted Retry-After, bounded test API, safe outcomes and destination-specific group fail-open. Protocol-10 development source 8b5 passed 14/14 process proofs. Its later-discovered evaluator findings remain open, so this is not release certification |
| Human authentication | Passwords, TOTP, revocable sessions and account lifecycle. Functional UI checks passed 39 normal and 23 regression checks after node-switch fixes, including delayed responses; visual redesign was not part of those fixes |
| Maintenance backend | Bounded timezone/DST engine; replicated CRUD/CLI; checks/history continue while alerts are held. Alias, uncovered-interval and queued-worker review findings have fixes and focused passing regressions |
| Capacity/convergence | Paged revision edges/evidence, bounded fan-in joins, incomplete-history local liveness, durable exact owed-down provenance, and recursive closure/exact queued identity repairs are implemented. Development snapshot 8b5 passed 2,444 ordinary Go events and 14 process proofs. Its evaluator findings remain open. Retained-history CPU/heap/storage costs remain unbounded; fresh combined validation is required |
| Retry policy | Retry count, retry spacing and inversion are integrated with local persistence and dashboard forms. Independent origin/restore/policy controls passed; pending does not claim recovery |
| Backup/restore | Consistent backup, quarantine and activation with separately retained sequence state. Actual Alpine and restricted Docker each passed 49 focused recovery events with zero failures/skips. The separate identity volume passed 11 container checks and seven migration/restore checks. Compose was unavailable; the migration used equivalent Docker commands |
| Observability | Authenticated Prometheus gauges, local history and separate sample/latency statistics. The local observed-time archive preserves original freshness, transactional closures and bounded 90-day coverage; arithmetic and storage integration passed independent review. Actual Alpine/Docker archive controls passed. Opus implemented observed-time charts, hierarchy health and badge sharing, with a 23-check development browser proof. These figures are not an SLA |
| Dashboard/publication | All 26 types have structured forms. The Globalping browser proof passed 16/16 against a private protocol-12 development binary using a loopback provider fixture. Its unchanged older contract harness passed 113/114: the remaining assertion expects exactly 25 types. Earlier OAuth/query and contract proofs remain historical. Public status pages, tags and badges are implemented; final combined-artifact verification remains required |
| Worker containment | Owned output collection repairs a proven false-pending mechanism. Independent native and race-parent tests each passed 54 events. Actual Alpine and restricted Docker each passed 41 process events with zero failures/skips; child limits remain unchanged |
| Alpine | Frozen protocol 9 passed 10/10 node checks and all 32 push functions; web passed the separate corrected multi-header consumer check. Protocol-10 process/recovery artifacts passed as above. The final combined node/web source still needs a new build/runtime run |
| Dependency scans | Current Linux symbol scan found no reachable findings; npm audit found zero advisories. Separate OSV version/commit queries cover the locally patched evaluator. The unused OpenPGP module-only advisory is retained; final artifact scans remain required |

See [architecture](architecture.md), [parity inventory](parity-audit.md),
[notifications](notifications.md), [maintenance](maintenance.md),
[observability](observability.md), [authentication](authentication.md),
[browser evidence](auth-ui-validation.md), [container/race evidence](container-validation.md)
and [dependency checks](security-validation.md).

## Remaining release work

- Repair the seven additional compatibility mechanisms found by the third
  evaluator review after the previous 105 cases were repaired. An isolated
  official-JavaScript candidate is being evaluated; no runtime switch or profile
  waiver has been approved. The first candidate's actual Alpine and restricted
  Docker suites each returned 820 passing and 12 failing events. Four extra
  resource assertions failed beyond the disclosed historical differences:
  exact visit-budget input, maximum computed numeric string, lexical-shadow
  computed string, and allocation-result classification. Its separate 1 MiB
  production request observed UP; the original controller's parser failure is
  retained alongside a derived read-only assessment. Neither observation clears
  the suite gate. A second frozen private repair also fails the two finite
  boundaries in both modes and three additional focused safety assertions.
  Its allocation case passes in Docker but fails directly; the later capacity
  recovery assertion runs only in the passing case. Its one production 1 MiB
  request is an actual DOWN, unlike the first controller's parser defect. A
  distinct private third repair is underway with unchanged limits; no candidate
  has been adopted. Local observed-time
  storage review found no remaining issue after query parsing was corrected.

  After cleanup, the preserved R3 replacement passed the selected native helper,
  worker, numerical-boundary and maximum-document checks through the storage
  guard. Independent review added 30 native and 33 worker checks; all pass after
  correcting one new synthetic fixture assumption, with the original failure
  preserved in [the review report](jsonata-native-runtime-review.md). All 415
  baseline files remain unchanged. R3 is still private; its Alpine fit check and
  production adoption are separate gates. Its bounded diskless Alpine run
  subsequently failed the first exact visit-budget case (DOWN instead of UP,
  1269.794764 ms); later selectors did not run. The VM and downloads were
  removed, leaving zero scratch. Native Linux/Docker verification remains open;
  see [the exact checkpoint](bounded-alpine-result.md). These results do not
  clear C1/R2 or justify a timer change.
  A private argument-reuse/numeric-index guard overlay subsequently passed
  140 native and 250 worker test events and reduced allocation in local samples,
  but its new diskless Alpine trial still failed that first case at
  1268.276311 ms. It remains unadopted; all original source and limits are
  unchanged, all scratch was reclaimed, and the earlier failure is preserved.
  A distinct declared Goja source-extension candidate plus a primitive numeric
  scanner now clears both Alpine finite-boundary failures. Its retained worker
  suite has 933 passing events and only the four already disclosed legacy
  assertions; all 146 native events, vet and Linux amd64/arm64 library builds
  pass. Actual Alpine has 12 passing / two failing events in its initial slice
  and 43 passing events in the previously unexecuted slice on the identical
  executable. Allocation still times out instead of producing the expected
  stopped-worker Pending result, so this candidate remains private. The later
  capacity-recovery assertion was not reached. The follow-up raw-log loss and
  repaired bounded logging are disclosed in the same checkpoint document.
  Badge backend review found no correctness/privacy blocker. Opus has supplied
  the revised renderer and implemented badge links, observed-time charts and
  hierarchy forms. Its private 23-check browser proof passes on an earlier
  renderer binary; its final source/screenshots were subsequently handed off,
  and the protocol-12 development binary includes the renderer. Fresh
  combined-artifact verification remains required.
- The unchanged supported 1 MiB query-document test now passes on actual Alpine
  and restricted Docker with the original worker limits. This resource result
  does not clear the independent semantic defects above.
- Complete hierarchical group/health rollups and remaining monitor/provider
  options from the parity inventory. Observed-time duration arithmetic and the
  SQLite/runtime/API archive are implemented and passed independent arithmetic
  and storage/integration review; dashboard presentation is implemented and
  combined artifact validation is pending. The pure hierarchy evaluator passed independent review
  and actual Alpine checks. Working protocol 11 adds parent links and a read-only
  current-health projection; seven native integration tests pass. Independent
  integration review found no defects across 16 tests and 20,480 oracle
  projections, with focused race checks and all eight mutations detected.
  The dashboard presentation and its final handoff are complete.
  Globalping HTTP/ping/DNS is connected to the runtime/API in working protocol
  12. Four integration tests, vet, shared/proof TypeScript checks and a new
  two-process proof pass. Independent review verified the viewer privacy repair;
  the structured dashboard has passed its focused loopback proof.
  See [Globalping](globalping.md). Frozen protocol-11 source
  17161f1 passed all 15 process proofs in 88.686 seconds and 815 ordinary core
  test/subtest events in 150.366 seconds, with two expected Windows skips.
  Existing sample ratios keep their separate meaning.
- Slack, Telegram, ntfy, Gotify and Pushover are connected in working protocol
  13 to channel CRUD, durable destination identity, retries/holds and peer
  receipts. Four native integration functions cover provider-test safety,
  credential rotation, delayed revision proof, receipt exchange and down/recovery
  after SQLite restart. The full current canary package passed in 143.604 seconds,
  delivery in 1.939 seconds and all 26 leaf groups in 1.384 seconds. Targeted vet
  and Linux amd64/arm64 library builds pass. Both acknowledgement findings,
  malformed-PEM skipping and the follow-up strict-date issue have reproduced
  regressions and repairs. The five dashboard forms are written; root typecheck,
  eight pure builder checks and a disposable production build now pass after
  correcting an unescaped Unicode line separator. The current 21-check browser
  proof passes against a disposable Windows development node and local native
  provider fixtures; every provider creates, renames and sends successfully.
  The first harness selector failure is preserved separately. No source copies,
  binaries or profiles remain. These are development source checks, not Alpine, live-provider or
  coherent release certification. SMS and the broader provider/option catalogue
  remain gaps.
- Preserve the separately validated `/identity` container volume and stopped-node
  migration from earlier `/data/.canary-identities` deployments. Keep the latest
  sequence state independent of database restores; see [backups](backups.md).
- Exercise backup/restore, schema/coordination upgrades, authentication recovery,
  storage retention, memory limits and backlog throughput. Bounded peer messages
  do not bound the total retained coordination history or reconciliation cost.
- Capture the combined source after review fixes and rerun full native
  tests/vet, compiled process proofs, native race checks, supported Linux builds,
  actual Alpine runtime and dependency/license checks. The latest working
  protocol is 13; protocol-9 through protocol-12 checkpoints remain historical evidence.
- Document and test coordinated upgrades. Earlier protocol-4 inferred alias
  histories cannot be reliably split without their original source provenance;
  rejecting old peer protocols alone does not repair historical state.

Provider acceptance is not inbox delivery. Local fixtures do not constitute live
Discord or email-service validation. No real external notification or user
database was used by the validation fixtures.

Development resumed under [enforced storage limits](development-storage.md)
after reclaiming 33.3 GiB. Builds use one cache and disposable outputs; the
old persistent VM and per-task cache/snapshot collections must not be recreated.
