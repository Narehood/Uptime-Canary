# Published observed availability

Working protocol 15 adds explicit publication of one service's local observed
availability and hourly history. This is development source; its tests and
platform gates must be recorded against the final integrated source before
claiming a validated checkpoint. Protocol 14's green evidence remains scoped
to that earlier source.

An editor or administrator can enable `showAvailability` on a status-page
monitor selection. It is optional and false by default. Omitting it preserves
the old resource payload. Nulls, aliases, repeated keys, wrong types and unknown
fields are rejected. Normal revision, slug, role and complete-proof gates apply.
Because publication policy changes, peers require a coordinated protocol-15
upgrade and refuse older protocols. There is no automatic public backfill flag.

The existing `/public/status/<slug>` response includes `availability` only for
opted-in selections. It has `status: available|unavailable` and the fixed
`scope: serving-node`. An available result includes `from`, `to`, `summary` and
at most 25 UTC hourly bucket intersections. The range is exactly 24 hours;
its end is rounded down to a 30-second frame. It publishes only state/time
totals, never private monitor IDs, targets, node names, diagnostics, latency,
incident metadata or archive internals. Unavailable results have no summary or
buckets; fixed zero `from`/`to` values are not a measurement range.

The archive is local to the answering node. Peer snapshots do not transfer it.
Results can differ between nodes; no global or HA uptime is inferred. Gaps and
pending observations are not counted as known. Availability is up time divided
by up plus down time. Coverage is known time divided by the entire 24 hours.
Maintenance annotates observed time and changes neither denominator. Empty
history is available with zero coverage and no availability percentage. This
is not an SLA. Existing archive validity and retention rules remain unchanged;
prior configurations of the same selected private monitor ID can contribute
their retained observed time. Opting in discloses outage timing for that range.

The selected monitor must have complete current configuration proof, be enabled
and assign the answering node. Otherwise the optional metric is unavailable;
its separate current-state projection remains governed by the original rules.
No current reading can be made healthy from historical up time. Disabling the
flag withdraws history and its metric badge as soon as the node accepts the
revision; partitioned peers can retain their previous publication policy until
they synchronize, just as for existing public status data.

## Bounded unauthenticated work

Unknown, unpublished, conflicting or duplicate-slug pages retain the identical
404 response. Opt-out pages do no historical SQL or cache work. Eligible reads
run outside `n.mu`, with one nonwaiting archive reader, a request-wide 100 ms
context and 5,000 accepted SQL rows across the whole page. One additional row
may detect overflow. Indexed predecessor/range queries avoid scanning the full
retained archive. Overflow, cancellation, invalid spans or database contention
produce unavailable, never a partial prefix counted as history.

One FIFO cache holds at most 256 metrics, keyed by private monitor revision and
rounded window end. Cache lookup/bookkeeping uses a short-held mutex separate
from the nonwaiting SQL-reader gate; an unrelated cache miss cannot hide an
already cached result. Cached errors also expire with that frame. Cache eviction
has no durable or causal effect. Publication and current monitor readiness are
checked again after reading, so cached data grants no publication authority.
SQL reads use a transaction and never prune, insert or update history. The
existing resource-scan limits remain in force. These budgets do not claim a
universal resource bound for retained causal proof evaluation elsewhere.

## Uptime badge

Use `/public/badge/<slug>.svg?group=<public-group-id>&monitor=<public-name>&metric=uptime`.
Exactly these three single-valued parameters are accepted. A unique public
service and its explicit availability opt-in are required. No whole-page
historical aggregate or private ID selection is exposed. The existing current
state badge routes keep their original meaning.

The metric badge shows both observed availability and coverage, truncated to
two decimals so a small observed failure cannot round to 100.00%. Unknown time
is not silently healthy. The SVG uses fixed text and numeric values only,
with no scripts or external resources. GET and HEAD are credentialless, allow
CORS `*`, and use `no-store` and the existing public SVG response policy.

## Validation status

The first guarded launch could not resolve a relative Go executable on Windows
and exited before compilation. Using the existing toolchain's absolute path
fixed the launch. The first actual focused run exposed three new fixture setup
errors: two monitor edits omitted the API's required current-head list, and the
rollback trigger watched `resources` rather than the actual `resourceHeads`
category. Those setups were corrected without weakening their assertions or
changing production behavior. The original failing results were not counted
as a pass.

The corrected focused Windows run passed in 4.819 seconds (guarded compilation
and tests 123.140 seconds). It covers nine new availability functions plus
existing status-page, badge and protocol-upgrade controls. Guard source-v2
fingerprint: `801143cb7723cf483c09d45b7ab181d77012ca3c387113a5c1208757e6dfa172`.
Source remained unchanged; scratch returned to zero and the repository to
about 1.15 GiB.

The complete sequential Windows command `go test -mod=readonly -p 1 -count=1
./apps/node-go/...` then passed in 388.375 guarded seconds, including the full
core package in 196.529 seconds and the JSONata package in 47.955 seconds. All
19 packages completed (the command package has no tests). This is native
Windows/cgo-disabled evidence, not an execution claim for Unix-only cases.
The same source-v2 fingerprint remained unchanged. Sampled repository/scratch
peaks were 1,875,412,160 / 400,371,338 bytes; cleanup returned scratch to zero
and the repository to 1,254,247,795 bytes. Module-cache size did not grow.

The new `public-availability.test.ts` proof and the existing
`public-metadata.test.ts` proof both passed against two real Windows node
processes: two tests, zero failures, skips, cancellations or todos. The test
phase took 47.515 seconds; build plus guarded validation took 142.609 seconds.
It checks explicit opt-in, measured coverage, independent current status,
credentialless privacy, metric badges, process restart and withdrawal on both
peers. The disposable node SHA-256 was
`efb6074934df1ba3a2646146a70971fbca26836c355090f715257408c4cfade4`.
Source, proof fixtures and the binary remained unchanged; the guard deleted
the executable and private databases afterward. Sampled scratch peaked at
507,895,060 bytes, below 512 MiB; scratch returned to zero.

Node-wide `go vet -mod=readonly ./apps/node-go/...` passed without diagnostics
in 137.281 guarded seconds. The proof project's `tsc --noEmit` passed in 8.797
seconds. They use the same working-source fingerprint above. That working
tree includes held, uncommitted Opus UI helpers; these runs do not certify a
completed interface or a specific future dashboard commit.

Presentation is only partially authored because both configured Opus
connections became unavailable. The user explicitly kept frontend ownership
reserved for Opus. Root does not take over those files. Pending helpers are
`apps/web/src/publicAvailability.ts`, `apps/web/src/badges.ts` and
`apps/web/src/ui/badges.tsx`; editor controls and public-page integration still
need completion. The already-authored Opus SVG function is included in the
backend draft and its numeric/safety regressions pass; no new visual proof is
claimed. The partial dashboard changes are kept outside the backend commit.

This backend remains draft feature work; frontend acceptance is still separate.
The green protocol-14 main branch remains the validated development checkpoint.
No release pass is claimed.

## First draft Alpine checkpoint and cache review

Commit `6789fe92be4f79938264f6c7f2e3ee54d0cb57c6` passed all five PR checks.
[Workflow 39999926780](https://github.com/Narehood/Uptime-Canary/actions/runs/39999926780)
passed the four node/web/proof/race jobs: 11 node smoke checks; web build/runtime
and security/asset checks; all 18 real-process proofs plus actual Compose
runtime; and 886 core plus 1,527 worker-parent race events. Parent reports have
zero failures, skips, unfinished tests or race warnings. Helper packages passed
race checks separately; capped worker children remain uninstrumented. The
production worker SHA-256 was
`06010eb280f23da80634c3d1272ab2b29bf0b942bf03c5226a7db951135a7d1b`.

All four artifacts were read in memory, without saving archives locally.
Their identical 563-entry source manifest matched committed `6789fe9`, SHA-256
`132a68c501d926353192063fb58d5aec6a6b55b4f1dde0622be239cb5b9d5495`.
The held UI changes and subsequent regression authoring were compared against
Git's original file bytes, not mistaken for that committed CI source.
Artifact IDs: node `12521557440`, web `12520489796`, proof `12521428699`,
race `12521873248`. No completed frontend or arm64 execution is claimed.

CodeRabbit's completed review identified one minor availability defect:
[discussion 4362859567](https://github.com/Narehood/Uptime-Canary/pull/1#discussion_r4362859567).
The original cache used one mutex for both its map and the SQL reader. A cold
read could hold it while waiting for SQL, making another request return
unavailable for a hot cached key. The new
`TestPublicAvailabilityHotCacheSurvivesUnrelatedSQLRead` uses a real occupied
SQL connection and a separate cold HTTP request; its hot-cache assertion failed
on the original implementation:

```text
an unrelated SQL read hid an already cached metric
&{unavailable serving-node 0 0 <nil> []}
```

The unchanged regression assertion now exercises a separate short-held cache
mutex and nonwaiting SQL-reader gate. The existing cold-reader contention test
locks that new gate, preserving its assertions. Timeout, row budget, cache size,
publication revalidation and wire format are unchanged.

The repaired focused availability/status-page/badge/upgrade selection passed
in 3.633 seconds, with Canary-package vet also passing without diagnostics.
The single guarded command took 51.469 seconds, source-v2 fingerprint
`b301c9455ba85b2ced8d57351d31dbb96c7cb00567415fda022bea9b4bfd4b22`;
source remained unchanged. Scratch peaked at 286,041,259 bytes and returned to
zero; idle repository size was 1,238,771,840 bytes, with no module-cache growth.
The earlier regression failure is preserved above. The subsequent CI evidence
for this repair is recorded below; the earlier green checkpoint remains scoped
to `6789fe9`.
The review's completion notices and informational docstring coverage warning
do not change the project's behavioral requirements.

## Repaired backend Alpine checkpoint

Commit `1e3dd977f67c8c1664b148831658af6ca297173c` passed the four execution
jobs in [workflow 40003648085](https://github.com/Narehood/Uptime-Canary/actions/runs/40003648085).
The final job completed at **2026-10-23 18:55:10 UTC**. The independently read
artifacts confirm:

- All 11 original node smoke checks and the original web build/runtime checks
  passed, including health, assets, headers, SPA fallback and nonroot execution.
- All 18 process proofs passed against the original node image executable,
  under nonroot/read-only/no-external-network policy. TAP has zero failures,
  cancellations, skips or todos; container exit is zero and OOM is false.
- Actual Portainer-template Compose runtime passed readiness, CORS, separate
  private identity storage and configuration/history persistence after restart.
- The full core race phase passed **887** test/subtest events, including the
  new hot-cache regression; worker-parent packages passed **1,527**. Both have
  zero failures, skips, unfinished tests, panics or race warnings. Source,
  worker and fixture hashes remained unchanged. Helper packages passed race
  checks separately; production worker children stay uninstrumented and capped.

The common production worker SHA-256 was
`4b9e254614593802e63b49042a5dafcec97c09b20d8461db2ea236f4ca78617f`.
All artifacts contain the same **563-entry** source manifest, SHA-256
`c05c7bf7faa6bbb40e2b8665c81a98986ece955bc822ead2f4803fdb7d017fe1`.
Every entry matched committed `1e3dd97`. The two held modified UI files were
checked against Git's committed versions; the untracked Opus helper is outside
this CI source. Archives were inspected in memory and not retained locally.

| Artifact | ID | Archive SHA-256 |
| --- | --- | --- |
| Node | 12522464022 | `bf42acbcd2f796b4d29a17b0eaae7dacd83682cabbaa2fbdc7e0b4470d8a4038` |
| Web | 12522977646 | `3cca771e8fa5d5f3e24152b5fb533274ddc30bb3d0eac80d9142f13e1d342f63` |
| Proof/Compose | 12522764345 | `cbafa5661133ee6bcd92d1b7befde85a3dc1805fa8297cf3b311b70569432393` |
| Race | 12524697377 | `7ea11aa6012d7d7cc5ba0ac433e3d300c3f2f946e3cdf20d943af748e67e434b` |

GitHub reports five successful checks, but CodeRabbit's latest success status
corresponds to **skipping automatic re-review of a draft PR**. Its actual review
covers `6789fe9`; no independent bot re-review of the repair is claimed.
The original finding and before-fix failure remain recorded above.

This validates the backend on Linux amd64, not the unfinished frontend, arm64
execution or a release artifact. PR #1 remains draft; main remains protocol 14.
Frontend ownership is still reserved for Opus. The final storage check found
1,238,799,308 bytes in the repository and zero scratch, below the idle target;
no VM, source snapshot, extra build cache or binary archive was created.
