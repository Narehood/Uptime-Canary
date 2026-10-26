# Protocol-20 transport, alerts and update-notice validation

This is candidate evidence, not complete release acceptance. The new features
are [native TLS](transport-tls.md), [message presets](notifications.md#message-presets-and-previews-protocol-20)
and [cached software updates](software-updates.md). Matching published images,
hosted platform gates and operator deployment checks remain separate.

## Post-merge follow-up, 2026-10-26

PRs #15 and #16 merged to `f62ff4550f3969d57066111f469ccdbe3e67f760`.
Its [image publisher](https://github.com/Narehood/Uptime-Canary/actions/runs/40352639018)
built and promoted all three images and verified anonymous pulls. That publisher
required successful image builds but did not require the separate native and
Alpine validation workflows to pass.

The matching [Alpine proof job](https://github.com/Narehood/Uptime-Canary/actions/runs/40352638842/job/121677386439)
passed 24/25 process tests. The unchanged 64-monitor capacity test failed its
45-second all-observer recovery condition: local observations remained UP,
while remote observations became stale and peer/API requests timed out. Both
processes remained alive. Native Linux amd64 and arm64 package jobs passed on
that same source; those results do not clear the Alpine failure. The failure
remains unresolved, and the published images do not establish full release
acceptance.

This follow-up changes listener logging, safe connection-error diagnostics,
and future `latest` promotion admission. The startup log identifies the actual
bound socket; the internal connectable loopback URL stays unchanged. DNS,
timeout, refused-connection and HTTP/HTTPS mismatches get specific diagnostic
categories without returning raw private URLs or credentials. Pin and CA
verification remain enforced before authenticated requests.

Promotion now requires the latest matching successful attempts of all three
image builds, four Alpine phases and three native package targets for the exact
current `main` commit. Completion events check once, and admission is checked
again immediately before registry writes. Missing, skipped, failed, cancelled,
pending, stale or superseded required results cannot approve promotion.
Existing commit tags and rollback controls remain. This does not roll back
already published tags, fix the capacity failure, create VPN routes, or prove
that a user's dashboard can reach a private node.

The logging and generic-error regressions failed against the original behavior
on Windows amd64. After the changes, the complete canary package passed on v3
source `df4d3a6d4da58f7c789debec37a2bf73fdcf81cdd7f7fa44b84c73bfbfaa00ca`
(core execution 186.111 s, 254.422 guarded seconds). Scratch returned to zero;
storage stayed within the existing budgets. Hosted checks and actual
`workflow_run` promotion behavior remain PR gates, separate from these local
checks. No capacity workload, freshness policy, assertion or deadline changed.

On source `0a5b1a6c92d37501ead01c0e945e60d9125587d939e3bb8b8264abc4a40ce5d5`,
all three workspace typechecks, configured web pure/control fixtures, thirteen
new publication controls, eleven retained registry rollback controls, and vet
of the core and CLI packages passed in 96.469 guarded seconds. The earlier
static run retained one failing source-location assertion after the transport
classifier moved files. Its repair keeps the exact original error wording and
status and additionally requires the registry handler to call that classifier.

A later Python-only control checks reruns of older workflow IDs: execution
update time, rather than a larger creation-time run ID, selects the latest
attempt. Fourteen publication controls passed on source
`f0fca9ad6aa403f7202bba78eb905c0a4edcaeab72442b13401b5d3c6ff02fa8`.
A read-only assessment of real `f62ff45` GitHub metadata refused promotion,
including its failed Alpine workflow. That check performs no registry writes,
artifact downloads or CI reruns. All Go sources and tests are unchanged from
the complete core pass above; final source identities retain their own scope.

## Local checkpoint, 2026-10-26

Windows 11 amd64, Node 24.22.1 and the pinned Go 1.27.3 toolchain. The v3 source
fingerprint is
`61ea890b19b801f929b1ad2dfec150513e3c8af01de4c6e55de57283a3b8d849`;
its scope is defined in [development storage](development-storage.md). Every
command used the serial storage guard, private databases and owned loopback
fixtures. No user's deployment database, environment file or running binary
was used.

| Check | Result | Guarded wall time |
| --- | --- | --- |
| Shared, web and proof workspace typechecks | All three passed | Included below |
| Pure/control groups, including TLS client fixture | 338 passed | 20.500 s including typechecks |
| Production web build and complete browser contract suite | 213/213, no page exceptions or blocked requests | 202.500 s |
| Complete regular process suite | 25/25, none skipped or cancelled; executable SHA-256 `4480f85b68118ebe9768a3e796a4b445f08eda9a32c4c26907887546d4f2a8a5` | 611.110 s including build |
| Complete canary core tests and vet of every Go application package | Passed; core package 163.557 s | 291.172 s including vet |

All four command scopes kept their source fingerprint unchanged. Disposable
scratch returned to zero. The largest sampled scratch peak was 517,424,653
bytes, below the 512 MiB ceiling; the repository stayed below 3 GiB and returned
below the 1.25 GiB idle target. The shared module cache did not grow.

Earlier all-package Go tests and vet passed across the unchanged
`6c65b8cf…` checkpoint;
the final dashboard-account TLS isolation fix and its affected TLS/account/
notification tests passed at `243439f9…`. These earlier scopes are not relabelled
as the final whole-source result.

## Final bundled-alias follow-up

A subsequent source review found that automatically adopting a manually added
node as the bundled node through a different HTTPS address copied the URL but
left out its configured certificate fingerprint. The new regression reproduced
that failure on v3 source `e034722a…`: discovery authenticated the pinned
endpoint, but proving the existing role bundle at the alias failed certificate
verification. No registration was replaced.

The fix carries the explicitly configured pin through every role proof and the
atomic reseal/placeholder deletion. Four cases cover successful adoption and
restart, a wrong pin before any HTTP request, a refused viewer role, and a
failed database commit. Existing identity, registration and role credentials
must remain intact. This changes only the Go adoption path and its regression;
the earlier browser/process results above retain their exact checkpoint scope.

On unchanged v3 source
`de8f171f2f8d244cf9d3a06b70ffba300c4cd5392ee385ffa294a7a147d0dcd4`,
the complete core package passed in 162.021 s, including all four new cases.
Vet of every Go application package also passed; the combined guarded command
took 205.828 s. The three typechecks and all 338 pure/control groups were then
rerun on the same unchanged source and passed in 20.187 guarded seconds.
Scratch returned to zero and storage stayed within budget.

## Deployment configuration follow-up

PR head `2af02ee` failed the Alpine node job before runtime validation because
the exact expected dashboard environment omitted the three new protocol-20
settings. The expectation now includes the accepted defaults, keeping the
full environment and split-service equality assertions. Additional checks
cover explicit native TLS/pins, disabled updates and retained outbound trust
after removing the invitation.

On unchanged v3 source `620e452ea5f7273a71527ff05abf80761f2f05132c267c6cf823f18036534e88`,
a checksum-verified standalone Docker Compose v2.39.4 parser on Windows amd64
reproduced the original assertion and passed all seven corrected configuration
groups in 6.875 guarded seconds, including the disposable tool download.
Scratch returned to zero. This checks configuration parsing only: it uses no
Docker daemon, image pull or deployment, and reads no local environment file.
Application sources are unchanged from the earlier checkpoints.

## Review follow-up

Two review findings reproduced on unchanged v3 source
`e5a0371141587b1fd87a86ca50af0d4f0968054be7f9627bb1c527fae81ab888`:
a valid Discord URL with an invalid template returned the URL error, and an
unpinned client could not connect to a CA-trusted TLS 1.2 proxy. The fixes
separate template validation and retain TLS 1.2 only for ordinary CA trust.
Native pinned connections still require TLS 1.3. Regressions retain the URL
error for an invalid URL, require HTTP 400 for invalid templates, and check
trusted CA success, untrusted CA/hostname refusal and pinned downgrade refusal
before any HTTP credential is sent.

The owner explicitly accepted activation of previously unused Discord text as
a pre-release breaking change. No opt-in flag, template clearing or other
migration behavior was added. Existing template and delivery assertions stay
unchanged; a newly proposed migration-compatibility test was removed because
its expected behavior was explicitly declined.

On unchanged v3 source
`5f87e992c7d00c2934df04dd5fd980e0a2a915f1b1e587edb7ecdc1195a6aac2`,
the affected TLS, native TLS, channel and notification-template tests passed
(nodetls 0.785 s; canary 3.748 s), followed by vet of both packages, in 74.609
guarded seconds. All 60 onboarding/template pure groups then passed in 4.312
guarded seconds on the same source. Existing assertions were retained. Scratch
returned to zero and storage stayed within budget. The earlier complete browser
and process results keep their original source scope.

## Hosted upgrade-fixture follow-up

The native Linux amd64 job on PR head
`f162d7a6127bca9a0e2322afacacfb4c63574464`
[failed at the published-baseline upgrade gate](https://github.com/Narehood/Uptime-Canary/actions/runs/40324419680/job/121577563951).
Its 25/25 regular process proofs, actual PM2 and sandboxed Chromium checks had
passed. The separate upgrade of dashboard accounts and the encrypted node
registry also passed. The retained-state upgrade test failed because its first
current-node snapshot probe still sent protocol 19: the protocol-20 node
correctly returned HTTP 400, "Invalid peer snapshot".

Both current-node probes now send protocol 20. The original published baseline
still uses protocol 18; mixed-version refusal, persisted sequences, retained
configuration/accounts/receipts and the no-duplicate-alert assertions are
unchanged. This is a fixture correction, with no application change.

On unchanged v3 source
`15ebd398af39b794eab81ba36fb05b5850f065737f6c05b5650695897ed1d255`,
the proof workspace typecheck, both upgrade-runner control tests and three
existing Go snapshot/protocol tests passed on Windows amd64 in 66.375 guarded
seconds (Go package 1.462 s). The Go controls reject every earlier protocol,
including 19, before merging configuration and accept the current protocol.
Scratch returned to zero and storage stayed within budget. The actual published
Linux executable upgrade requires the hosted Linux amd64 rerun; these local
checks do not replace that gate or establish release acceptance.

## Race-suite lifecycle investigation

The [Alpine race job on `df7be05`](https://github.com/Narehood/Uptime-Canary/actions/runs/40330706045/job/121599294646)
reported zero data-race warnings and 1,184 passing core test events, but failed
`TestMonitorMetadataEditsPreserveDeliveredOutageAcrossPeersAndRestart` while
waiting for exactly one recovery delivery and both peers' fresh up state after
restart. Its only assertion detail was "Condition did not become true";
the report did not distinguish missing delivery, duplicate delivery or stale
observations. The package and race gate therefore failed.

Failure-only diagnostics now record both webhook delivery lists, monitor
revisions/observations, incidents, receipts, queued attempts and runtime faults.
Every original assertion and the eight-second condition deadline remain
unchanged. There is no application change or claimed root-cause fix.

On unchanged v3 source
`0096c146c54b20c10359a08dfde2f987ebd7cad7c120f676fdf44645f810fb8b`,
twenty repetitions of that test passed on Windows amd64 without race
instrumentation (Go package 50.996 s; 158.171 guarded seconds including
compilation). Scratch returned to zero and storage stayed within budget.
No native C compiler or Docker is installed on that host, so this does not
reproduce the Alpine race environment. The failure remains unresolved pending
the hosted rerun and its additional diagnostics if it fails again.

## ARM64 DNS fixture reservation follow-up

The [native ARM64 job on `105250d`](https://github.com/Narehood/Uptime-Canary/actions/runs/40335375056/job/121615476064)
failed before packaging or process proofs. Its full canary core tests passed
(144.932 s), but `TestDNSExtendedCanonicalAdmissionAndTCPFallback` could not
start its private fixture: UDP selected port 41828, which was already occupied
in TCP. A free UDP port does not reserve that port in TCP.

Factoring out the unchanged allocator and forcing that collision reproduced
the failure on Windows amd64, v3 source
`912b16331eb787aae1084c006489514b3e40e61a4043cdf39f762934132da299`.
The fixture now reserves TCP first, then UDP on the same address. Only a UDP
address-in-use bind error retries allocation, with a 32-attempt cap and cleanup
of every rejected TCP reservation. Both successful sockets stay owned until
fixture cleanup. Regressions cover an occupied TCP port, a real UDP collision,
exhausted allocation attempts and immediate propagation of other bind errors.
No DNS query, assertion or deadline is retried or relaxed; production code is
unchanged.

On unchanged v3 source
`0fdbf92ed6c0d45f9f54607158ea029fd105454fbcc8976b7866e3ad41287c5d`,
twenty repetitions of all DNS tests passed (2.909 s), followed by the complete
checks package (3.238 s) and its vet checks, in 58.250 guarded seconds on
Windows amd64. Scratch returned to zero and storage stayed within budget.
The matching native ARM64 run is still required. This fixture fix does not
clear the earlier 64-monitor process-proof recovery failure or establish
Alpine race acceptance; those gates were not reached in this failed job.

## Lifecycle partition-fixture follow-up

The [Alpine race job on `105250d`](https://github.com/Narehood/Uptime-Canary/actions/runs/40335375003/job/121615407815)
passed the earlier metadata-edit/restart recovery test. It reported zero
data-race warnings, but the semantic conflict-resolution case failed with HTTP
409 while editing the original revision. The test transport's partition flag
refused new requests without draining already admitted exchanges: an older
request could return newly edited configuration after the supposed partition,
correctly making the other editor's revision stale.

A controlled pending-page regression reproduced the premature partition return
on Windows amd64, v3 source
`cfbff30a2c4a3f505c99b67f44ba832f2edd09cd87b53c09ab32bcd83a6caf7f`.
The two affected lifecycle fixtures now finish pre-partition sync rounds before
starting edits. The drain has its own eight-second bound; every original
assertion and condition deadline remains unchanged. No application behavior,
revision check or edit retry changes. The regression holds a validated peer
page, requires the partition setup to wait, and confirms that the original
merge completes before edits are allowed.

On unchanged v3 source
`7cfc1fc83e62c3ca2b4e5dc6068d754cc3e586389e2202fbece02fcd76739bf8`,
twenty repetitions each of the new pending-page regression and both affected
lifecycle tests passed on Windows amd64, followed by core-package vet
(Go tests 78.299 s; 148.093 guarded seconds). Scratch returned to zero and
storage stayed within budget. These are non-race checks; matching Alpine race
acceptance remains required.

Separately, the native Linux amd64 package job on `105250d` passed, including
the original published protocol-18 executable upgrade gate. That result keeps
its exact source/platform scope and does not clear ARM64 or Alpine race gates.

## What is exercised

- Real native TLS handshakes, retained keys, wrong pins before any HTTP
  credential, expiry, inspection, explicit renewal/retrust, HTTPS peer exchange,
  ticket trust, registered-node edits and viewer redaction. Bundled address
  migration retains the registration and three existing role credentials.
- Real CA/proxy and native-pinned outbound node processes, first-pair
  confirmation, dashboard/node restarts, retained registration, removal, and
  monitoring plus alerts while the dashboard is stopped. These use private
  loopback networks, not a deployed VPN.
- Custom Discord plain/card outage, repeat and recovery through the actual
  scheduling/receipt/adapter pipeline with an injected fake provider. Oversized
  custom text records `invalid_message` without provider I/O. Presets, Undo,
  explicit replacement, sample events and both themes at 390/728/1280 px are
  exercised in the browser; previews send nothing.
- Cached updater API access and roles, request coalescing, persisted cooldowns,
  conditional metadata, digest checks, fixed origins, stripped CDN credentials,
  source/build-time comparisons and throttling use fake network clients. The
  browser's available-update case uses labelled sample metadata after the real
  server authenticates its GET; its POST reaches the real disabled service.
  Banner dismissal, cached navigation, viewer controls and six layout cases
  are checked. This does not prove a live GHCR/GitHub lookup or image deployment.

The optional connection certificate must not affect the hidden dashboard account
runtime when that listener is off. A regression loads an expired optional
certificate, confirms account status still works, and requires explicitly
enabling that certificate to fail closed. Internal accounts now select TLS off;
the dedicated connection listener still enables and verifies TLS explicitly.

## Earlier failures retained

The original assertions were preserved. Browser fixture repairs changed action
selection and authentication setup, rather than weakening certificate or role
checks:

| v3 source prefix | Browser result | Finding and repair |
| --- | --- | --- |
| `f8ec34ec` | 207/213 | Intentional wrong-pin refusal changed the action to Verify again; the test looked for Verify node, leaving a dialog open and causing later failures. Select the existing retry label. |
| `39cb6ed1` | 209/213 | An unchecked extra password login preceded a viewer 401; fixture cases shared the production ten-per-minute socket-IP quota. Permission-only cases now use verified account tokens; all original 403 assertions and the limiter remain. |
| `c4ef5b97` | 212/213 | The updater test matched both the accessible header Close icon and footer Close. Target the footer. |
| `40d5f1a2` | 212/213 | The first Not now belonged to the token-only account's password reminder. Target the update banner and wait for its removal, keeping the original dismissal assertion. |

An earlier TLS peer fixture bypassed public monitor admission and stored a
noncanonical revision, triggering the existing immutable-revision collision
check. It now creates through the real pinned HTTPS API; the original exchange
assertion and production validation remain. A webhook fixture's monitor name
exceeded the existing 256-byte message bound; its corrected 256-byte name still
makes the unused template overflow if wrongly rendered. A new updater-error
fixture put the server detail in the exception's message argument; passing the
detail in its actual argument retains the disabled-service assertion.

No live Discord, NetBird/Tailscale, Compose, Portainer or VM deployment is
claimed. Native Linux amd64/arm64, Alpine/race, actual platform prerequisites,
packaging, published-baseline upgrades and image-build/publication gates must
be assessed on the PR's matching source. There is no tagged release or dashboard
HA claim.
