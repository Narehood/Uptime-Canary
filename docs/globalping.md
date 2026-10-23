# Globalping monitors

Working protocol **12** adds one remote Globalping measurement per assigned
Canary node and check. The optional dashboard is being extended with structured
fields. This is development source, not a release checkpoint.

Use monitor type `globalping`, the normal `target`, interval, timeout and node
assignment, and a `globalping` option object. Its required `mode` is `ping`,
`http` or `dns`; `location` selects a Globalping location. Supply exactly one
matching `ping`, `http` or `dns` object. The API token is optional. The default
API is `https://api.globalping.io`; a private proxy can have its own API URL and
CA, with plain HTTP requiring explicit opt-in.

| Mode | Target | Options |
| --- | --- | --- |
| Ping | Hostname or IP | ICMP or TCP, 1–16 packets, optional TCP port |
| HTTP | HTTP(S) URL | GET/HEAD/OPTIONS, headers, status ranges, keyword, JSONata, explicit TLS policy |
| DNS | Domain | Record type, UDP/TCP, port, resolver, case-insensitive RE2 pattern, empty-answer policy |

The configured timeout covers creation, polling and any query worker. The remote
probe timeout can be 5–30 seconds; leave extra time for API communication and
polling. An earlier configured Canary deadline is permitted and can leave the
measurement pending. The engine creates one probe and polls no faster than twice
per second. It does not retry creation.

Provider authentication, quota, transport, malformed-response and internal
failures are **pending** by default. `apiFailureIsDown` explicitly changes that
provider-failure policy. A measured target failure is **down**. An unavailable
JSONata worker remains pending under either policy. Status, keyword and query
conditions must all pass for an HTTP success.

For compatibility with older provider results, a remote result explicitly
reporting `status: "failed"` without `failureSource` counts as down. This does
not identify the cause. An explicitly unknown failure source remains pending.

Latency is the provider's remote timing and is meaningful only when
`latencyAvailable` is true. API elapsed time does not become target latency.
Unknown provider fields and raw output never become diagnostic messages.

Configuration, credentials and current observations replicate through the normal
authenticated peer protocol. Accounts and raw history stay local. Viewer accounts
receive no Globalping options, and HTTP targets are reduced to their origin in
that projection. Public status pages continue to require explicit publication.
Changing a result-affecting option starts a new check generation; a name-only edit
preserves it. All nodes must upgrade together: protocol-11 peers are rejected.

The [engine contract](../apps/node-go/internal/globalping/README.md) records exact
fields, bounds, trust behavior and differences from Kuma. In particular, this
does not add arbitrary HTTP methods, target mTLS/custom CA, automatic OAuth,
multiple-probe selection or traceroute/MTR modes.

## Development validation

The frozen leaf passes local HTTP/TLS fixtures with 92.6% statement coverage,
vet and static Linux amd64/arm64 builds. No public Globalping measurement was
submitted and no token or quota was consumed.

Four runtime integration tests pass in 4.982 seconds: exact nested JSON and
option selection; two-node replication, restart, generation and viewer privacy;
actual pending/down/recovery scheduling and remote latency; and transactional
rollback/retry. Both packages pass vet, and shared/proof TypeScript checks pass.

Immutable development snapshot `d3e2889eae787abe6d0891ec22f9a01265e920cd75f27b6276a00cd3e7fc0401`
built the Windows binary `78a390ec64b9e3e988da9df79660b3a209147f1ed37b6e328bfe4cf582ea69e8`.
Its new two-process proof passes in 18.361 seconds with zero failures or skips:
quota stays pending, remote results retain their measured RTT, connected alerts
deduplicate, sender restart preserves acceptance and real recovery uses the
original incident. Source, binary and fixture fingerprints remain unchanged.
Evidence is in `.canary/process-proofs/20261022T223341504686Z/`.

The complete ordinary canary-package run on that snapshot passed 830
test/subtest events in 157.985 seconds, with zero failures and two expected
Windows skips for Unix-only filename cases. Evidence is in
`.canary/ordinary-checks/20261022T224734499154Z/`. An earlier run is retained
separately: its harness used an extended Windows path as the process working
directory, so one fixture's relative-path calculation failed before its
ownership assertions. The unchanged fixture passes from the ordinary path;
neither product nor fixture code was altered for that correction.

The first independent review found an origin-redaction defect for padded HTTP
mode spellings. Current source now redacts by monitor type and parsed URL rather
than mode spelling; all nine original integration/review functions pass in
4.742 seconds without changing the reviewer fixtures. The second scoped review
closed that finding: twelve root tests, 25 leaf tests and vet pass, with no new
scoped blocker. See [the review](globalping-review-round2.md).

Fixed-source snapshot `0b6bace1c2778c015fbf249f07daefb562b7014840a1fe77b4da21da7bce1ac7`
also passed the extended two-process proof in 18.362 seconds. It covers viewer
redaction, provider failures, measured remote latency, deduplication, restart
and recovery. Source and fixture hashes stayed unchanged. Small results remain
in `.canary/process-proofs/20261022T230320049009Z/`; the disposable executable
was removed during the user-requested repository cleanup.

Opus's protocol-12 dashboard has passed its sixteen focused browser scenarios
against the earlier development binary with a loopback provider fixture. This
does not establish the later privacy fix or live provider interoperability.
Alpine and combined release gates remain separate. The application's existing
evaluator findings are still open; the private JavaScript candidate has not
been adopted. The older `d3e2889e` binary predates the privacy repair.
