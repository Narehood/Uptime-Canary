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
rounded window end. Cached errors also expire with that frame. Cache eviction
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

This backend is draft feature work, with protocol-15 Alpine/race checks and
frontend acceptance still separate. The green protocol-14 main branch remains
the validated development checkpoint. No release pass is claimed.
