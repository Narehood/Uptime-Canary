Independent local observed-time archive storage and root integration review, 2026-10-22.

Verdict: no remaining correctness, atomicity, retention, restart, or privacy defect found in the reviewed scope after the root's small API correction. One API validation defect was independently reproduced on the frozen baseline and is now verified fixed. This is a development-code review, not release-candidate certification or whole-node release validation.

The reviewer added only `apps/node-go/internal/canary/availability_review_test.go` and this report to the shared tree. No production, existing test, module, UI, default binary, environment file, or user database was edited by the reviewer. No nested agents, dependency installs, services, listening servers, or broad whole-node suite were used. The initial checkout has no Git metadata; preservation and drift were checked using file hashes.

The arithmetic review in `docs/availability-review.md` was read as background. It was not treated as evidence that SQL selection, transaction wiring, provenance, or archive lifecycle behavior was correct.

Frozen evidence and scope

Before experiments, 368 files under `apps/node-go`, `internal`, `packages/shared`, and `docs`, plus root `go.mod` and `go.sum`, were copied into this private directory:

```text
C:/Users/Michael/AppData/Local/Temp/uptime-availability-storage-review-2cbd1b60b3e94828a4821b9eb51f7a3a
```

Every source and copy was rehashed against the initial manifest before testing. The manifest was created at 2026-10-22 16:21:31 -04:00. Its SHA-256 is:

```text
snapshot-manifest.json
969d85a51e9b539cb01c91c982738ed4f66b4b7c625e0c6f3d7fa249b2f37474
```

All 368 frozen baseline files were rechecked unchanged at the end. No baseline was overwritten when the API correction arrived. A separate byte-checked copy of the root's corrected `api.go` and updated `availability_integration_test.go` was placed in `api-correction`; Go's overlay mechanism applied just those two files for corrected private checks. The API delta was also checked to be exactly the `net/url` import and replacement of `r.URL.Query()` with explicit raw-query parsing and its 400 error branch.

The following reviewed files had identical starting and ending shared-tree hashes:

```text
apps/node-go/internal/canary/availability_history.go
101c7cae193a4ed8b6ce32ad9cb99195030d43b775817cc2a73ee2e9f391b714

apps/node-go/internal/canary/availability_history_test.go
9659e0e3a16758d160277990ca15984c668cd3c07984b2578f80c06f675545fe

apps/node-go/internal/canary/availability_integration.go
243a0b191328d1088e08e544a92a818ed5e9f244a293a8699c50ceebfe399ffe

apps/node-go/internal/canary/runtime.go
9a6694432e469ab0dd43b48eee2e9b9e75dc521a189b8086e2dced339667b8ac

apps/node-go/internal/canary/store.go
e5a714aed9dcf526e199359317fb05a35e80ec76736605194374d2e0a31a6849

apps/node-go/internal/canary/causal_store.go
1916a96b1ebd9cb65dc19ab491825efb81d8cf2258cae0782b175469df062b8e

apps/node-go/internal/canary/backup.go
6486139e6e8557c20989590314af0e998a6a33c8f23054dc8c3b6c3df0826dc4

apps/node-go/internal/canary/restore_activate.go
4063ec5ba095a4c7f96d83799297d60475ded434283eff807deb01c26c59cce5

docs/availability-history.md
69fe91f7cd0f189233739c002405a81f17b2b5b5dfa641b3e795d39d83313942

packages/shared/src/index.ts
eb9d0cbe42da4bd3cdbe98ea6366676aa4187d59f876f66ba0b22f4ac269a4fa

go.mod
2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a

go.sum
6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
```

The in-scope correction drift was:

```text
apps/node-go/internal/canary/api.go
start 042916c335ac11de809f5635d7f21b7c515065ce94ad4bb4f2e0a32aa76b75e9
end   0c057f5f704b3ca77d56d2b85fedc188b890c855093485e3ca6d2c4fd6fa656f

apps/node-go/internal/canary/availability_integration_test.go
start aa90184109348c5acf1e893d27725704a5b2a67141ecc67f6d4ad38019bed765
end   aa4999b9f92a7e6978dbba61a69984f5c2719d51437f6433d4bf0f32484223dc
```

The complete starting/ending drift ledger is `shared-drift-end.json` in the private directory, SHA-256 `ded8870cd87e5e97d105b6a4d54e2becd076cdb52760e16cfb4d7cf79b6af581`. The final comparison at 16:35:22 -04:00 found ten changed original shared files: the two correction files above and eight concurrent changes outside this archive review: `canary/cli.go`, five JSONata documents (`FINITE_PROFILE.md`, `NONFINITE_REPAIR_PROPOSAL.md`, `NOTICE.txt`, `README.md`, `VALIDATION.md`), `docs/protocol10-checkpoint.md`, and `docs/shipping-progress.md`. Their full path and hash pairs are retained in the ledger. The earlier 16:32:54 comparison remains in `shared-drift-final.json`, SHA-256 `5262d75d33f3c15fe7e7224e5f61bfb4a74f6c4ebbcc3c46bc1e6730d7760566`; only `docs/protocol10-checkpoint.md` changed again between those comparisons. All in-scope implementation hashes stayed stable. No availability calculator or JSONata Go source drift was observed. The eight unrelated changes were excluded from the corrected private overlay; the final shared-tree check compiled the current checkout too.

The new review test's final SHA-256 is `8f4a8e77f6cb5c01f9f156be2fdf0241d644371c5a3ecfd50f215ddd9f85faf2`. It is identical in the private and shared trees.

Product finding and its resolution

P2, resolved: the original availability route called `r.URL.Query()`, which silently discards URL query parsing errors. Requests such as `from=%zz`, `from=10;to=20`, and `stepMs=%zz` returned 200 using a default range or step. Adding a malformed second `from` also bypassed duplicate validation, and a malformed unknown parameter disappeared before unknown-key validation. The result described a different query from the one supplied, violating the documented strict input contract.

`TestAvailabilityStorageReviewMalformedRawQueryRejected` retains five desired 400 assertions. All five independently failed with 200 on the original frozen API, including a final isolated baseline reproduction after the correction. The root then reported its own discovery and supplied the explicit `url.ParseQuery(r.URL.RawQuery)` correction. All five unchanged assertions pass with that correction and on the current shared tree. The reviewer made no production correction. Valid-query behavior, storage bytes, and original validity policy were unchanged.

One initial failure was a fixture issue, not a product defect: a conflict scenario attempted to call the local edit writer with no previous heads while an existing head was present. The writer correctly rejected the stale head set with `Configuration heads changed; reload all current heads`. The fixture was corrected to admit a separate causal root through the incoming snapshot path, then explicitly checked that two heads existed. Its original desired closure assertion was preserved and now passes. No final assertion was relaxed to accept existing incorrect behavior.

Independent evidence

- `TestAvailabilityStorageReviewLabelledTimelineSQLProjection` executes 270 deterministic events across three private monitors. Its oracle stores labelled milliseconds, not spans or SQL. It independently counts complete query durations and epoch bucket edges without calling the calculator or reading returned edges to construct expectations. It includes all four explicit states, maintenance, gaps, same/older clocks, closures, equal-sequence replays, and revision/generation changes. Every event also checks durable high water and the original checked time, deadline, revision, generation, and sequence. SQL results, known-time availability, coverage, and maintenance's independent denominator agree with that oracle.
- `TestAvailabilityStorageReviewRawPruneFailureRollsBackRealSample` drives the actual runtime and injects SQLite trigger failures at raw insertion, count pruning, and age pruning, after archive writes. A generic logical dump discovers and compares every user table, including causal indexes. No sample, policy, sequence, archive, raw history, or other table change survives; staged local memory is not published. File-backed fixtures also exercise the existing private origin reservation. Reservation gaps after failed acceptance are permitted; acceptance must not reuse sequences.
- `TestAvailabilityStorageReviewCommitFailureRollsBack` adds a private deferred foreign-key fixture and first proves its statement succeeds while COMMIT fails. It then forces actual runtime sample and configuration commits to fail after their archive writes. All table contents and published configuration/sample/policy/sequence remain unchanged, and a subsequent transaction succeeds on the pooled connection. The pinned cached SQLite driver's COMMIT cleanup was inspected; this result does not depend on assuming that a deferred `sql.Tx.Rollback` can act after a failed COMMIT.
- `TestAvailabilityStorageReviewConfigurationClosures` uses accepted local runtime samples and real config/snapshot paths for metadata edits, disablement, deletion, local assignment loss, conflict, incomplete monitor proof, group edits, and maintenance edits. Coverage ends at exactly the config event. Group/maintenance changes close both local monitors conservatively; other edits preserve the unrelated monitor's horizon. Fresh peer registers alone never resume local history.
- `TestAvailabilityStorageReviewAnnotationProofCompletionClosesAllHeads` admits group and maintenance heads with missing parent proof, takes local samples, and completes only the parent proof later. Both local heads close at that event without retroactive annotation. A trigger failure in one closure rolls back the proof and every archive change and leaves the graph unpublished. Retrying the unchanged proof after removing the failure succeeds.
- `TestAvailabilityStorageReviewFreshnessExclusiveDeadline` independently specifies cases where normal/retry spacing, timeout, and sync interval dominate. It checks all four states and exactly one millisecond for the original inclusive boundary. The existing root boundary fixture additionally verifies conservative clipping at selected maintenance start/end transitions, without inheriting annotation through an unobserved transition.
- `TestAvailabilityStorageReviewRetentionPointerAndReadCutoff` exercises a crossing run whose primary key moves twice during 90-day retention. The head pointer follows it, a later observation still coalesces correctly, an idle read clips logically expired coverage, and a backward clock cannot resurrect physically pruned cells. The deliberately long supplied horizon is a low-level storage fixture, not an accepted runtime policy.
- `TestAvailabilityStorageReviewRuntimeBackwardClockAcrossRestart` drives actual local samples at 10000, 11000, then 10800. The durable watermark stays 11000 and the original deadline stays 13801. A config closure at 10500 clamps to 11000. After a file-backed restart at 5000, a fresh sample keeps its expired original deadline 8001 and adds no coverage. Reads cannot expose the future head. A later sample at 12000 resumes coverage without filling the explicit closure gap.
- `TestAvailabilityStorageReviewBackupRestoreClosesHead` takes a live whole-SQLite backup, prepares a quarantined restore, and compares original head and span values in both copies. Public startup rejects the quarantine. Offline activation resets the generation and closes the archived head, retaining the original deadline, provenance, and sequence. Retained origin state enforces a later sequence floor. A fresh restored local sample resumes coverage only at its own time, leaving the missing interval unknown.
- The eleven existing `TestAvailabilityHistory` tests and four `TestObservedArchive` tests were run fresh, including the 100000-row cap, 4096 buckets, SQL failure phases, exact runs, non-backfill, raw retention independence, peer isolation, restart, authentication, read caps, query limits, and sanitized storage errors. Their assertions were preserved. These checks supplement the independent fixtures rather than replacing them.

Source review found the documented transaction boundaries in place: only the caller owns write transactions; accepted local sample, policy, durable sequence, archive mutation/pruning, and raw pruning share one transaction. Configuration closure is inside `applySnapshot`'s transaction and precedes in-memory publication. Peer register merging has no archive record hook. Initialization creates schema without reading old samples. A backup uses whole-SQLite `VACUUM INTO`; restore activation's config reset passes through archive closure. The read transaction captures metadata and spans together; bounded calculation happens after it commits. The range query includes the indexed start range and one predecessor, and its SQL/output capacities are bounded. Current wall-clock clipping prevents future time from being counted. `LocalAvailabilityHistory` matches the local-only response, finite/null percentages, retention metadata, and absence of latency/SLA fields. No database diagnostics or private sample messages enter archive responses.

Checks and reproduction

Pinned toolchain: `.canary/tools/go/bin/go.exe`, `go version go1.27.3 windows/amd64`, SHA-256 `83b7ba66215e696414fa49462d0db2da4f4cf15c7bebd42f0b189fa04866999d`. All runs used process-local `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `GOMAXPROCS=2`, `-mod=readonly`, and compilation limit `-p=1`. The native environment reports `CGO_ENABLED=0`.

The frozen original storage/root baseline passed its 15 named test functions in 2.287s. The final corrected private run passed all 25 named functions, including the ten new review tests and their subcases, in 3.826s. Corrected private vet passed; final current shared-tree focused tests passed in 3.825s and shared-tree vet passed. Full logs remain in the private directory: `baseline-focused.log`, `review-first.log`, `baseline-malformed-confirmed.log`, `corrected-final-focused.log`, `corrected-final-vet.log`, `shared-final-focused.log`, and `shared-final-vet.log`.

From the current checkout, using a disposable PowerShell process:

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
$env:GOMAXPROCS = '2'
& .\.canary\tools\go\bin\go.exe test -mod=readonly -p=1 -count=1 -timeout=120s -run '^(TestAvailabilityHistory|TestObservedArchive|TestAvailabilityStorageReview)' ./apps/node-go/internal/canary
& .\.canary\tools\go\bin\go.exe vet -mod=readonly -p=1 ./apps/node-go/internal/canary
```

For the private snapshot, use the absolute pinned executable and add `-overlay <snapshot>/api-correction/overlay.json`. Omitting the overlay intentionally reproduces the old malformed-query finding. The original 368 baseline files remain intact in both cases.

Limits: no race detector, Linux run, process/browser proof, adversarial disk I/O failure, or full-node suite was run here. The root's earlier process proofs predate its raw-query correction; this review directly validates that correction through the authenticated in-process handler and focused tests. Passing this scope does not certify unrelated concurrent JSONata/CLI work or the overall nearKuma shipping goal. No unresolved review finding remains within the stated archive contract.
