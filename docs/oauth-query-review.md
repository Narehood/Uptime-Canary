# OAuth2 and transport JSONata correctness review

Independent review, 2026-10-22. One reproduced P2 integration defect was found:
MQTT treated the first false JSONata condition as a failed check instead of
waiting for a later matching publication. The root owner repaired it during
review. The unchanged desired-behavior regression now passes. No additional
OAuth2, transport-integration, credential-projection, or configuration blockers
were found in this review's scope. This does **not** clear protocol 10 for
release: the separately owned nonfinite evaluator gate remains blocking.

The reviewer added only
`apps/node-go/internal/canary/oauth_query_review_test.go` and this report to
the shared workspace. No production, module, shared DTO, UI, or existing test
file was edited by the reviewer. Tests used private SQLite databases, generated
TLS identities and loopback listeners. No `.env`, user database, external
service, dependency update, or new subagent was used.

The original source snapshot is at
`C:\Users\Michael\AppData\Local\Temp\canary-oauth-query-review-wt5ruerh`.
It contains 314 files, 2,710,812 bytes. Its sorted per-file SHA-256 manifest,
`source-manifest.json`, has SHA-256:

```text
6b4d32d6d58e024e72ee58b82f04dd94cddbba5dc7a6af37c11bff243d3cc192
```

An allowlisted collector read Go source/tests, shared DTOs, docs, examples,
deployment source and selected root configuration. It did not traverse private
or generated trees such as `.canary`, `node_modules`, `dist`, `.git`, or
symlinks, and excluded `.env` and database files. The selected file list and
every source byte were compared with a second read before the snapshot was
written. Copied bytes were then checked again. At the final audit, every
original snapshot file still matched its recorded manifest hash. The added
review test and execution logs are separate, unmanifested review artifacts.

**P2, repaired: a nonmatching MQTT publication prematurely fails JSONata.**
The frozen location is `apps/node-go/internal/services/mqtt.go:227`, especially
the unconditional `return result` at line 230. After an accepted subscription,
the fixture queues retained `{"state":"starting"}` followed by
`{"state":"ready"}`. Existing scalar `contains "ready"` correctly waits and
returns UP. JSONata `state == "ready"` returned DOWN on the first publication,
although the second was already queued and most of the five-second check
budget remained. This can falsely report an outage for retained status streams
or wildcard subscriptions. The existing MQTT contract explicitly says that a
mismatch continues waiting within the same deadline.

Reusable fixture and assertion:
`apps/node-go/internal/canary/oauth_query_review_test.go:131`,
`TestOAuthQueryReviewMQTTWaitsForMatchingPublication`. Its `jsonata=false`
subtest is the scalar control; `jsonata=true` requires the same UP outcome.
Both use the same two publications and original check budget. Reproduce from
the private snapshot with the existing toolchain:

```powershell
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' test -mod=readonly ./apps/node-go/internal/canary -run '^TestOAuthQueryReviewMQTTWaitsForMatchingPublication$' -count=1 -v -timeout=30s
```

The first frozen execution failed only `jsonata=true` with DOWN in 777.2772 ms;
the scalar control passed. A later frozen run with the final review file again
failed only that subtest, in 1,037.5367 ms. This failure is retained in
`frozen-independent-review.txt` in the private snapshot. Neither the expected
UP assertion nor the publications were changed to obtain a pass.

The root-owned repair at current `mqtt.go:229` returns Pending immediately,
returns Down if the context has expired, and otherwise continues through a
Down query result within the original packet, byte and deadline bounds.
The existing helper does not distinguish a false comparison from syntax or
evaluation errors, so those failures can also keep waiting until the check
ends. The root explicitly accepted and documented that bounded behavior.
No evaluator compilation moved into the parent and no ABI changed.

Frozen `mqtt.go` SHA-256:
`0931743cad0d73b50f8625b9279fb691f8a6303aeabe3f6948c5ce81cceaf3e8`.
Independently tested repaired `mqtt.go` SHA-256:
`ce7547170f9e08eee925ff2f22cf7e4613068e6621dc584ca6f7450e1fe46ae5`.

**Executed evidence.** All commands used Go 1.27.3, Windows/amd64,
`CGO_ENABLED=0`, the existing `.canary/tools/go/bin/go.exe`, and
`-mod=readonly`.

- Frozen existing focused suites: PASS. Command:
  `test -mod=readonly -p 1 ./apps/node-go/internal/checks ./apps/node-go/internal/services ./apps/node-go/internal/canary -run 'Test(OAuth2|HTTPJSONata|MQTTJSONata|SNMPJSONata|MongoDBJSONata|ServiceOptions)' -count=1 -timeout=150s`.
  Package times: checks 2.663 s, services 5.766 s, canary 3.619 s.
  Log: `frozen-focused-baseline.txt`.
- An earlier concurrent-package invocation passed checks/services but timed out
  in the existing canary OAuth2-options API PUT fixture, whose client timeout
  is three seconds. The same unmodified canary test then passed alone in
  2.661 s, followed by the passing serial focused run above. This is recorded
  as an intermittent fixture/run failure, not a reproduced OAuth2 product bug
  or an unconditional first-run pass.
- Frozen independent suite: FAIL as intended for the MQTT defect; the scalar
  control and all other five top-level tests passed. Package time 4.644 s.
  Log: `frozen-independent-review.txt`.
- Current repaired independent suite: PASS, all six top-level tests and 13
  subtests, package time 3.756 s. Command:
  `test -mod=readonly ./apps/node-go/internal/canary -run '^TestOAuthQueryReview' -count=1 -v -timeout=90s`.
  Log: `current-independent-review.txt`.
- Current focused vet: PASS for checks, services and canary. Command:
  `vet -mod=readonly ./apps/node-go/internal/checks ./apps/node-go/internal/services ./apps/node-go/internal/canary`.
  Log: `current-focused-vet.txt`.

The new independent tests verify these behaviors:

- Root `ActiveProbe` combines a real grant with target POST/body/private
  headers/mTLS, legacy accepted-status conversion, keyword/JSON conditions and
  whole-body JSONata aggregation. Both Basic and POST client authentication
  preserve reserved-character encoding. Two checks obtain two distinct tokens;
  options retain no issued Bearer token. The issuer sees no target certificate,
  custom headers or target body. Removing issuer trust still fails when the
  target's unsafe TLS flag is enabled.
- Grant acquisition and target I/O share a 500 ms total deadline: two 300 ms
  stages do not receive independent full timeout budgets.
- Non-200 issuer success-shaped bodies, wrong media types, issuer errors,
  escaped duplicate credential keys and invalid UTF-8 are rejected before
  target access, with fixed diagnostics.
- HTTP/MQTT/SNMP/Mongo public configuration accepts malformed expression syntax
  as shape-valid without compiling it. Name edits retain generations; changing
  query expectations changes them. Viewer overview/list/conflicts responses
  omit private queries and issuer/service options. Config and generation
  survive a private SQLite restart. HTTP empty client secrets remain accepted.
- Two bounded loopback Oracle handshakes occupy the shared worker slots.
  HTTP and MQTT query integration promptly return Pending when admission is
  unavailable; HTTP retains the observed 200 status. The test does not invoke
  an evaluator or arbitrary program in the parent and cancels/reaps its workers.

Existing focused tests additionally exercised OAuth2 configuration rejection,
token bounds/redirect refusal, target cross-origin header stripping, HTTPS
defaults, text wrapping, invalid UTF-8, accepted-status/JSON matcher ordering,
authenticated SNMP query input, complete Mongo document input, peer replication
and restart. These were executed, not inferred from test names alone.

**Source review and limits.** Token requests use a fresh transport made from
`TokenCAPEM` only (`checks/oauth2.go:118`) and the intentionally shared proxy
configuration. Target headers/body/client certificate and target TLS bypass
flags are not forwarded. Token redirects are refused (`oauth2.go:144`), and
existing target redirect code blocks downgrade, strips cross-origin headers
and client certificates, and refuses cross-origin body replay. The HTTP and
service outer context handlers preserve worker Pending instead of overwriting
it with Down. These are source findings supported by the focused tests above;
this review did not repeat exhaustive process-runner stress.

The dynamic dispatcher selects a fresh JSON-query handler before generic
service/Oracle dispatch (`probeworker/worker.go:27`); parent validation does
bounded shape checks without calling JSONata compile/evaluate. HTTP evaluates
the complete accepted response after prior matchers. MQTT/SNMP pass their actual
payload/scalar to the UTF-8/text-wrapping helper. MongoDB encodes the unique
first document as relaxed Extended JSON after the existing scalar condition.
The Go and shared TypeScript OAuth2/JSONata field names, operators and scalar
expectation shapes were inspected and agree for these four monitor types.
No browser/dashboard behavior or TypeScript UI test is claimed.

Numeric behavior was explicitly assigned elsewhere. The current numeric report
describes a finite-only `$number` shim and an independent gate with eight
false-UPs, three other DOWN differences and 23 matches for nonfinite values
produced outside that shim. `$type`/`$boolean` consuming arithmetic or aggregate
nonfinite values can still yield UP where JS fails. This review neither reran
nor repaired that gate; its transport tests use ordinary finite/scalar queries.
The lexical `MaxRange` inheritance gap, unsupported language constructs and
advisory evaluator limits remain documented leaf limitations. Transport passes
do not certify numerical correctness or complete JSONata parity.

Shared source changed concurrently. Relative to the frozen manifest, modified
original files at the audit were `README.md`, `docs/design.md`,
`docs/jsonata-checks.md`, the two JSON-query README/numeric reports, and
`services/mqtt.go`. Added files included the owned review test, the numeric
independent probe/testdata/proposal, and `docs/protocol10-checkpoint.md`.
The current-source pass is therefore reported separately from the frozen
failure. Final review-test SHA-256:
`3e71fe63a1b33830c606d9faef8347ec59038120d4403b3bacbde4a7d5752ef8`.

No Alpine execution, new compiled two-node proof, full repository test pass,
race run, release-artifact certification or external issuer interoperability is
claimed. The previously documented 19.227 s two-node development proof was read
as prior evidence, not repeated or extended by this review.
