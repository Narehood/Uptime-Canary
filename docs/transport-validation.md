# Protocol-20 transport, alerts and update-notice validation

This is candidate evidence, not complete release acceptance. The new features
are [native TLS](transport-tls.md), [message presets](notifications.md#message-presets-and-previews-protocol-20)
and [cached software updates](software-updates.md). Matching published images,
hosted platform gates and operator deployment checks remain separate.

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
