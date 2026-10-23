**First independent scoped Globalping review ? one confirmed P1 blocker.**

Reviewed the immutable integration snapshot identified by manifest SHA256 `d3e2889eae787abe6d0891ec22f9a01265e920cd75f27b6276a00cd3e7fc0401`. All 411 files were enumerated and hashed through `scripts/validation_paths.py::filesystem_path`, copied to a private directory, and verified again. No deep fixture was skipped. The private source is `C:\Users\Michael\AppData\Local\Temp\uc-gp-review-qhryc7vm\source`. Original snapshot files and every original private-copy file still match the manifest after testing. Live Go source and root module files matched; `apps/node-go/RUNTIME.md` had documentation drift.

**P1 ? Admitted whitespace around HTTP mode bypasses management viewer target redaction.**

Location: `apps/node-go/internal/canary/globalping_options.go:93` (the predicate in `globalpingPublicTarget`, through line 99). The leaf normalizes mode with `strings.ToLower(strings.TrimSpace(o.Mode))` at `apps/node-go/internal/globalping/config.go:124`. Root admission calls that validator without storing the normalized mode, while viewer redaction compares the original value with `strings.EqualFold(m.Globalping.Mode, "http")`. Consequently `" http "` and `" HTTP "` are accepted HTTP configurations but fall through to returning the entire raw target.

Observed reproduction: create a disabled Globalping monitor through the management POST handler with target `https://example.com/private-path?key=private-query`, mode `" http "`, nonempty location, and HTTP options. Create a viewer account, then GET `/api/overview`, `/api/monitors`, or `/api/monitors/{id}/conflicts` as that viewer. All three return the private path and query in `monitor.target`/`target`. Canonical `"http"` and `"HTTP"` control cases redact to `https://example.com/`. The nested Globalping options themselves are removed in both cases.

Impact: a configuration explicitly admitted by the frozen contract exposes target path/query secrets to management viewers. A monitor need not be enabled and no provider request is necessary. This is an observed API privacy failure, not an inference about a live provider or a public status page.

Required behavior: every admitted HTTP mode spelling must produce the origin-only management target. Apply the same normalization at redaction, or use a consistent canonical representation across admission and display while preserving the frozen contract's accepted spellings. Keep the raw target intact for checks. No product fix is included in this review.

Unchanged desired-behavior regression: `apps/node-go/internal/canary/globalping_review_test.go:16`, `TestGlobalpingReviewViewerNormalizedHTTPMode`. The original four spellings and all three endpoint assertions are retained. The two canonical spellings pass; both padded spellings fail on every endpoint. A subsequent implementation should satisfy this test without weakening it.

**Observed validation on the private source.**

Go tool: the existing repository-local Go 1.27.3 Windows/amd64 toolchain; `-mod=readonly` throughout. Logs and coverage are outside product packages, under the private review directory.

- Full Globalping leaf: `go test -mod=readonly ./apps/node-go/internal/globalping -count=1 -timeout=60s -coverprofile=../leaf-review.cover -v` passed 25 top-level tests, zero failures/skips, 6.986 seconds, 92.8% statement coverage. This includes three new review tests plus the original assertions.
- New root regressions only: `go test -mod=readonly ./apps/node-go/internal/canary -run '^TestGlobalpingReview' -count=1 -timeout=60s -v` completed in 2.262 seconds: four top-level tests passed, the privacy regression failed as described, zero skips. No ordinary whole-core suite was duplicated.
- `go vet -mod=readonly ./apps/node-go/internal/globalping ./apps/node-go/internal/canary` passed with the final review fixtures.

New passing leaf regressions establish that a POST whose response is lost after its request body is consumed is issued once, with zero GETs and no measured latency; default Pending and explicit provider-down policy are both checked. A successful-looking response for another target after a validated conditional-GET cache state remains Pending without latency. A remote HEAD measurement with null body and explicitly reported zero timing remains measured zero despite an 80 ms API delay; target Authorization stays encoded in the measurement payload.

New passing root regressions exercise escaped duplicate keys, nulls, case aliases and Authorization-header aliases inside options; semantic generation changes for location, header maps, status slices and query configuration; metadata generation preservation; retained previous revision commitments; SQL rollback preserving heads, persisted configuration, edge count, schedule and probe cancellation state; cancellation only after successful retry/commit; protocol 11 rejection; separately paged heads/proof remaining unusable until proof arrives; valid-shape token/location/header payload substitutions rejected by the DAG commitment; persistence/restart; and independent option edits converging as a two-head conflict, followed by explicit resolution and a new semantic generation. Those nodes use in-process handlers and no started runtime, provider or delivery.

The full original leaf fixtures also passed their TLS/custom API CA, credential namespace, polling delay/ETag, cancellation, response bounds, stale/mismatched results, remote failure, HTTP/DNS/ping and guarded-query checks. This is executed fixture evidence, not live interoperability evidence. The new root proof checks transport pages through normal serialization/validation/application; they are not a new compiled-process HA or notification failover proof.

**Source conclusions and limits.**

Source inspection supports per-check API transport ownership, disabled redirects/environment proxies, separate provider-token/target-header namespaces, bounded strict response decoding, shared cancellation/deadline, and explicit remote latency availability. No additional transport blocker was confirmed in this scope. The cache and immutable payload tests are API/peer-boundary tests; they do not establish that arbitrary trusted Go callers can mutate retained in-process option pointers safely. The leaf explicitly forbids caller mutation during a check.

One documented compatibility exception remains: `globalping/result.go:34` maps `status:"failed"` with absent `failureSource` to Down even when `apiFailureIsDown` is false. The frozen README expressly specifies that fallback, and original tests assert it; this review preserves those assertions and does not count it as a newly discovered implementation defect. Such a result does not diagnose the cause. The broad statement that every failure without target evidence defaults to Pending therefore needs this qualification.

The cached primary API schema and official SDK model files were copied into private review evidence without changing the cached copies and match their supplied SHA256 hashes. Unsupported target mTLS/custom CA/OAuth, MTR/traceroute/multiple probes, and methods outside GET/HEAD/OPTIONS are documented scope limits, not findings. Existing Go JSONata R3 defects and the separate official-JS C1/R2 resource work were neither changed nor attributed to Globalping transport. The existing query engine/worker protocol and resource controls are unchanged.

No Git commit, product edit, original-test edit, root module edit, SOURCE_FREEZE edit, UI work, environment/default-binary/existing-database modification, subagent, global tool installation, VM, Docker, user port, external measurement, paid API request or notification was performed. Root fixtures create only private temporary SQLite databases; leaf APIs bind loopback ephemeral fixture ports. No whole-release, Alpine, whole-core race, visual, live-provider, or complete Kuma-equivalence approval is given. This scoped review retains a blocking objection until the P1 regression is fixed.

**Frozen review artifacts.**

Only these new files are published to the shared repository: the two review test files and this report. Existing assertions and all original source files remain unchanged by this review.

- `apps/node-go/internal/canary/globalping_review_test.go`: SHA256 `01cd50c2f4bcdf77a977635b6ca84e9f47e7ce5492b5e861b01b99d1856b6893`.
- `apps/node-go/internal/globalping/review_test.go`: SHA256 `b7b4183c1703c82e8c171377ee4783e8d7f323c1a93c6c618301a318b7d0ae78`.
- Private `reviewed-source-manifest.json`: the complete 411-file reviewed source hashes, aggregate SHA256 `d3e2889eae787abe6d0891ec22f9a01265e920cd75f27b6276a00cd3e7fc0401`.
- Private `canary-review-final.log`, `leaf-review.log`, `leaf-review.cover`, `vet-review-final.log`, and `references/` retain validation evidence. `review-artifacts.json` freezes the report, review fixtures and evidence file hashes. Earlier diagnostic logs are retained separately; the earlier rollback fixture used an invalid space-containing token and was corrected before the final run. That fixture-authoring issue is not a product finding.
