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

## Protocol 14 to 15 upgrade acceptance

The protocol-14 checkpoint's controlled 13-to-14 proof does not cover this
publication-policy change. A separate `--publication-upgrade` mode now selects
`public-availability-upgrade.compat.ts`; it is outside the ordinary process
suite so a source-only container context cannot accidentally run it without
retained Git history.

The old fixture is rebuilt from protocol-14 main commit
`65fc953ddf430ae7e454027f2a163c1f871e1b51` using five declared historical
production-file fragments. The new `public_availability.go` is absent through
Go's overlay deletion rule. Other production inputs and `go.mod`/`go.sum` must
match the reference; undeclared drift aborts before fixture writes or builds.
This uses no copied checkout or cache and is not an original published artifact.
Both executables and their hashes are checked; all disposable fragments,
binaries and private databases remain under guarded outputs.

The authored proof checks old API refusal of the new flag, unchanged default-off
pages after upgrade, bidirectional mixed-peer refusal, preservation of an exact
pre-upgrade local-history range, retained account role/configuration/generation,
increasing origin sequence, explicit opt-in and peer synchronization after both
nodes upgrade, and one original down followed by one genuine recovery without
an upgrade resend. It contacts only private loopback nodes and a local webhook
fixture.

The proof-runner unit checks pass: five test functions in 0.210 seconds,
guarded command 4.079 seconds, with zero scratch after cleanup. These tests cover
refusal of undeclared Go/module/embedded-code changes and exact overlay deletion
without copying a checkout. The guard fingerprint was scoped to `scripts/`,
`f6354fb877c9bf4f75f409c3adaaca962fb6c2e0744066b3050eac00404b3445`,
while Opus independently authored frontend source. This is not a process-proof
or frontend pass. The proof project's `tsc --noEmit` subsequently passed without
diagnostics in 4.093 guarded seconds; its fingerprint was scoped to `apps/proof/`,
`1a4be43517ec9ba1b7d13b8efee790a640eb9d320e030aaa7110d7ef668cb444`.
No application or browser fixture ran. The real compatibility proof remains
pending stable integrated source. Its root command is:

```text
python scripts/bounded_validation.py --timeout 480 -- python -B scripts/process-proof-validation.py --publication-upgrade
```

Opus has resumed a source-only continuation for the public editor/presentation.
Earlier partial work and provider failures remain recorded above; no completed
interface or browser acceptance is claimed yet.

### Frontend acceptance extension (authored, not yet executed)

`scripts/dashboard-contract-review.mjs --public-availability` retains the
existing dashboard assertions and adds the root-owned
`scripts/public-availability-browser.mjs` checks. The extension exercises
default-off and explicitly saved publication, metadata-edit preservation,
actual local measured coverage and hourly rendering, credentialless SVG links,
current DOWN beside historical up time, malformed/unavailable/zero-known-time
fixtures, expanded-table layout at 390/1440 pixels, late old-node responses,
same-node slug changes, clearing a withdrawn 404 page, and UI opt-out.

Unavailable and malformed payloads are explicitly injected through the private
browser fixture, not reported as genuine backend outcomes. Other measurements,
publication writes and withdrawal responses come from its real loopback nodes.
No screenshot, profile collection, copied application source or external
provider is needed. The command builds into the single guarded output tree and
must run after Opus's source freeze, using native preview first or the documented
fallback after its required failed attempts:

```text
python scripts/bounded_validation.py --timeout 900 -- node scripts/dashboard-contract-review.mjs --await-browser --public-availability
```

Both browser-harness JavaScript files pass `node --check` in 4.094 guarded
seconds, with a `scripts/` source fingerprint of
`593f75ae91678227e5e193bf4895c32f14cb50218dbf6b76ebcb77ac34683fd1`.
This is a syntax check only: the extension has no browser result yet. The older
124/124 dashboard checkpoint does not validate the new controls or public
history view. Both guards left zero scratch and no module-cache growth.

## Integrated Opus source: first executed checks

After Opus froze the actual editor, public history view, badge sharing and
strict reader, one guarded Windows command passed the full workspace typecheck,
all **13** authored pure frontend checks, and a disposable production Vite build.
The guard took **12.640 seconds**, source-v2 fingerprint
`cc6e0890afa202c7d31214b3e8593aaa3f99a11d945e382efd593c8feb24db43`,
with source unchanged. Scratch peaked at 1,702,484 bytes and returned to zero;
module-cache growth was zero. No browser or application process ran in this
command, and the build output was deleted by the guard.

The build still reports its ordinary large-chunk advisory: main JS 756.81 kB,
221.15 kB gzipped. No threshold was raised or warning suppressed. The older
component/platform evidence remains scoped above; real-process upgrade and
browser acceptance of this integrated interface are still pending.

The controlled 14-to-15 proof subsequently passed **1/1**, with zero failures,
skips, cancellations or todos, on native Windows. Its test phase took
**26.875 seconds**; both builds plus the guard took **121.391 seconds**.
The fingerprint above remained unchanged, as did both binaries and the proof
fixtures. Current binary SHA-256:
`0617c5581232b7da084c4aa88b5585c5c3e28e29158403aac92368971b2c0179`;
rebuilt historical fixture:
`86daea66466ff3e327f723c7f8854f6d4c25833f80878c268915eb4ed3734b65`.
The checks described in the upgrade section all executed, including exact
pre-upgrade history totals, default-off publication, mixed-peer refusal and no
resend of the accepted down before its genuine recovery. This is rebuilt-source
Windows evidence, not an original release-artifact or Alpine upgrade claim.

Sampled scratch peaked at 507,768,052 bytes, below 512 MiB, and returned to
zero. The cache trimmed to 250,653,321 bytes and the repository to 1,241,170,787
bytes; module-cache growth was zero. No binaries or overlay fragments remain.
Browser acceptance still needs execution. Its fixture explicitly sets en-US
for consistent percentage assertions; the product formats visitors' locales.

### First integrated browser run: navigation regression

The first full contract run with the public-history extension executed **137**
assertion groups: **135 passed, two failed**, with zero browser exceptions or
blocked external requests. All original assertions passed. Both new failures
occurred at the same preparation step, after selecting node B from a two-node
link and navigating to a single-node link for A:

```text
FAIL Public observed: switching slug on the same node clears prior service history: Timeout: heading Observed acceptance page
FAIL Public observed: unpublishing returns 404 and clears old page, history and embed links: Timeout: heading Observed acceptance page
```

`PublicStatusView` retained `active = 1` when the new candidate list contained
only A, making its base URL undefined. The same-node slug assertion and later
404-clearing assertion were not reached; they must not be counted as passed.
The tests remain unchanged. Opus received the reproduced navigation defect for
a source-only repair. A separate source finding requests that an available
metric also fail closed on invalid/zero `generatedAt`, instead of bypassing its
frame check; the earlier new pure fixture's zero-time acceptance is not adopted
as the reader contract.

The new positive history/table/coverage and metric SVG, current DOWN versus old
up time, credentialless requests, malformed/unavailable/zero-known responses,
390/1440-pixel expanded table, late A response after selecting B, and editor
opt-out checks all passed. The injected response cases remain distinct from
actual backend outcomes.

Native preview opened the private fixture, but both snapshots returned
`Preview automation snapshot timed out after 15000ms.` The owned tab was closed,
and the documented Chrome/CDP fallback ran under guarded outputs. No screenshots
were captured. Guard time was **250.891 seconds**, source-v2 fingerprint
`b5118ede21213fc1d728e359c9d158837d3651b77e52df6e68d3ce5e01b46e44`,
source unchanged; production binary still matched the upgrade proof's
`0617c558...` hash. The compact result SHA-256 was
`fc0739e08849a6f468b375f2b08f25ec3561b4a3ae78a08f72cbcfbd0d1b21fa`.
Scratch peaked at 224,718,741 bytes and returned to zero; repository returned
to 1,257,830,313 bytes, below the idle target. The fixture processes, private
databases, binary, dist and single Chrome profile were removed by cleanup.
This failed run is preserved as a failure, not a whole-feature pass.

Opus repaired node selection by retaining a selected URL with the list it
belongs to; a new list selects its first endpoint, while normal polls retain
the visitor's explicit choice. The reader also now refuses quantified available
history without a positive safe projection timestamp. Its earlier zero-time
acceptance assertion is quoted in the pure fixture and replaced by an explicit
invalid-time regression for both measured and empty history; unavailable
responses continue to draw no numbers. This is the stricter accepted reader
contract, not a change to a historical backend regression.

The repaired source passes workspace typechecking, all **14** pure checks and
the disposable Vite build in **12.484 guarded seconds**, source-v2 fingerprint
`076899d570600991c7d55c8fbe04a0ea1d0f0731d553e731f4f1e4eb66cc1ea0`,
source unchanged. Scratch peaked at 1,700,928 bytes and returned to zero, with
no module-cache growth. The normal large-chunk notice remains (756.91 kB main
JS, 221.19 kB gzipped). The unchanged browser assertions must now be rerun,
including the previously unreached same-node slug and 404 withdrawal checks.

### Repaired integrated browser acceptance

The unchanged **137/137** contract groups now pass, with zero failures, browser
exceptions or blocked external requests. This includes all original 124 groups
and all 13 new public-availability groups. The same-node slug and unpublication
404 assertions both reached their actual assertions and passed. No test was
removed or weakened to clear the node-selection defect.

The browser phase ran at 2026-10-23 20:09:22.755–20:10:33.801 UTC; the complete
guard took **113.765 seconds**. Source-v2 fingerprint remained
`076899d570600991c7d55c8fbe04a0ea1d0f0731d553e731f4f1e4eb66cc1ea0`.
Application source manifest:
`4bb826b33d1e6d3faec575973f3b9a3983bd0930620f69cb46701024dcb2266a`;
the binary still matched `0617c558...`, also used by the upgrade proof.
Final JS SHA-256:
`09da793f0fc1efaddf19bf320c78c714ad63fdeb05fce856a8572666df096353`;
CSS: `d6456689765c26781177df2b2874709d68dc9cdd0c0b161ce405ffe2eff000c8`.
The compact result SHA-256 is
`dbe4bd60d47d8eccda0335e4c1554b8053925744ce6d008f56e7d39f35fefb37`.

This rerun reused the documented CDP fallback after the first run's two native
snapshot timeouts; it made no additional native-preview attempt. The fixture
sets en-US; the application itself retains locale-aware formatting. Scratch
peaked at 250,922,037 bytes, returned to zero, and the repository returned to
1,257,616,777 bytes. Module-cache growth remained zero. No screenshots, private
databases, binary, dist or Chrome profile were retained.

The 14 pure checks are now tracked at `scripts/public-availability-pure.mjs`;
only their module-root lookup changed from the authored ignored fixture. They
also pass from that permanent path (4.125 guarded seconds, source unchanged).
The web Dockerfile now runs them after `npm ci` and before building, and the
CI source selector includes the script. Docker execution of that new build
step and the complete integrated commit's CI remain pending. Local browser
acceptance does not imply arm64, Portainer-host or release approval.

## Split deployment checkpoint

All four jobs in [workflow 40013131856](https://github.com/Narehood/Uptime-Canary/actions/runs/40013131856)
passed on `0c3d131802cd5059d37e90f7bda0cb7a7476b9e8`, completing at
2026-10-23 20:05:34 UTC. Its inspected artifacts confirm all 11 node checks,
web runtime checks, 18/18 original-image process proofs, combined Compose
runtime, and the new separate dashboard/node Compose runtime scenario.
Independent projects/networks, a credential-free dashboard, CORS, private node
identity storage and independent stop/start persistence all passed. Core 887
and worker-parent 1,527 race events passed with zero failed/skipped tests and
zero race warnings; helper packages raced separately.

Every one of the common **565** manifest entries matched the exact committed
Git blobs, not the locally modified frontend. Manifest SHA-256:
`47a44ed6fe6bd0a31d9bd3f361e162e1eb90b29fef515fd8c1737a66ae21af8b`.
All four archives were read in memory, without local downloads:

| Artifact | ID | Archive SHA-256 |
| --- | --- | --- |
| Node | 12526898243 | `83a5a51beed1329a384d08539c4e35df2d6efa8c1b0b4af66e6d487f8a837cca` |
| Web | 12526009890 | `36f66fb9c2af769a06bb68df3e3104d717dae8089c7af9317e29a4767b9296e3` |
| Proof | 12527163402 | `8aac2681e2f72c9b8fa92d4542ce857a16814cea385c5e76542ea712db2c7409` |
| Race | 12529225260 | `b2bac19a5498ac5db428136915672befeebec18b81e37edacacba7986e757899` |

That CI source predates the completed interface and new web-build pure-check
step; its pass must not be extended to them. No deployment through Portainer's
own UI or separate Docker hosts was exercised.

## Completed review: Date-range and host-proxy guidance

CodeRabbit review `5634028561` on `cb7de6e` posted two findings at
2026-10-23 20:24:30 UTC. Both were checked against current source; the earlier
cache issue was already marked addressed.

The timestamp finding is valid: safe integers extend beyond JavaScript's
[Date TimeClip range](https://tc39.es/ecma262/multipage/numbers-and-dates.html#sec-timeclip).
The new independent tracked pure check builds a coherent 24-hour metric ending
at `8_640_000_000_000_000`; the exact boundary is a positive control, while
`generatedAt = boundary + 1` must fail closed. This avoids a meaningless test
that an existing frame mismatch would already reject. Native `toISOString()`
throws above the boundary, but the unfixed reader accepted the metric:

```text
FAIL Date range: coherent boundary metrics never authorize an unrenderable projection timestamp
actual kind: available
expected kind: unreadable
```

The guarded pre-repair run failed one new group while all original 14 passed,
in 4.141 seconds, source unchanged at fingerprint
`0f5a4406117752a001b38de6d705bdeaf9cc1e8fa4fc9fc0f52e3855641402fe`.
Scratch returned to zero. The regression and positive boundary control remain
unchanged for the repair. Opus owns the reader and public-page clock sanitizer;
the latter must also protect the footer when a page has no historical metric.
An added browser group injects an out-of-Date projection both with and without
history and requires the page to render without exceptions. It has not yet run.

The deployment wording also needed correction. The instructions now explicitly
retain loopback publication for a reverse proxy process on the Docker host;
only a required direct connection from another host justifies selecting a
different bind interface. Container-local loopback is distinguished from the
Docker host. This follows [Docker's published-port documentation](https://docs.docker.com/engine/network/port-publishing/).
No template networking or bind default changed, and no new deployment pass is
claimed for this prose correction. Repaired frontend validation is pending.

Opus added one shared positive/safe/Date-range check for the availability reader
and page projection clock. Source inspection also confirmed that announcement
creation/update numbers reached the same formatters without Date-range checks;
both now use the shared sanitizer, preserving note content while suppressing
unknown date labels. A second browser group covers that adjacent path with a
valid page clock and invalid note dates. That adjacent group is post-repair
coverage; no separate pre-repair browser failure is claimed.

The workspace typecheck, all **15** tracked pure checks, browser-script syntax
and disposable production build pass in **12.546 guarded seconds**, source
unchanged at fingerprint
`a357c014becc135f2804ee73fc81b8aa2d8bc31d00f7c57fbd24313d4185769d`.
The unchanged coherent-boundary regression now passes, including the exact
valid Date limit. Scratch peaked at 1,700,928 bytes and returned to zero, with
no module-cache growth. The full 139-group browser run is the next gate.

The repaired full browser run passes **139/139**, with zero exceptions or
blocked external requests. The original 137 assertions remain unchanged, and
both added Date-safety groups pass: an invalid projection clock with and
without history, and invalid announcement dates alongside a valid current page,
note and historical view. The coherent-boundary pure regression remains
unchanged from its failing pre-repair run.

The browser phase ran at 2026-10-23 20:34:42.092–20:36:04.494 UTC. Complete
guard time was **129.422 seconds**, at the unchanged `a357c014...` fingerprint
above. Application-manifest SHA-256:
`efd7b9a5f669839737ebe943b58e8f6386242d33096c8af0670d0e86c6148fd3`;
browser-node binary:
`e6f152558b0295f126c30a29fd01f17a79afa68726f8c61adb6f1782fdd677d6`;
JS: `95544ed27f9fb8a14d0c094e4243f04354bb425d5494c73b5d70aa4aaed1e6a2`;
CSS: `d6456689765c26781177df2b2874709d68dc9cdd0c0b161ce405ffe2eff000c8`.
Compact-result SHA-256:
`7efb3626820c27c918b177bd4eb6548b4cba2ae918200ae4ff5823e24d91380a`.
The Go production source is unchanged; the controlled upgrade remains scoped
to its separately recorded binary and source checkpoint.

The same earlier native-preview timeouts justify the documented bounded CDP
fallback; no new preview or screenshot attempt was made. Scratch peaked at
240,437,177 bytes and returned to zero; the repository returned to 1,257,865,418
bytes and the cache to 267,047,922 bytes. Module-cache growth was zero. New
exact-head CI is still required for the repaired commit. No review finding is
waived because earlier CI passed on older source.
