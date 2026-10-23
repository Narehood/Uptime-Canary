No reproducible contract violation was found in the frozen rollup package in this independent review on 2026-10-22. There are no scoped findings requiring a production change or outstanding objections from this round. The result is limited to the inspected implementation and executed checks below.

The package remains an isolated feature leaf. Root runtime, API, HA, schema, UI, deployment, notification delivery, and alert integration remain outside this review and unverified. Existing named groups' alert suppression is a separate feature. The near-Kuma goal is not completed by this package.

The only repository additions are [review_test.go](C:/Users/Michael/Documents/DEV/Uptime-Canary/apps/node-go/internal/rollup/review_test.go) and this report. Production, the original tests and benchmark, README, root modules, and existing assertions were preserved. No dependencies or tools were installed. No services, user database, environment files, commits, nested agents, or external messages were used.

All four original package files were read in full. Original bytes and module bytes were copied before any test into:

```text
C:\Users\Michael\AppData\Local\Temp\canary-rollup-independent-review-88f6657383cb49e990ae60e50eff1438
```

`original/` contains the byte snapshots; `snapshot.json` records original lengths and SHA-256 hashes. Root `go.mod` and `go.sum` existed; root workspace files were absent. The supplied owner `validation.json` and `source-manifest.sha256` were read and copied into that private directory. The supplied manifest's SHA-256 matches exactly:

```text
148f613ecb92dd2086e7089f0fc3e8a03ee1d354ad137c8d942a8a2cdcbe7c67  source-manifest.sha256
```

Original hashes, also verified unchanged after the checks:

```text
2e5532bac755d7c2dd0f6c68d41c1171c5f812ab462dcd05fd044eb5fbba5ad0  apps/node-go/internal/rollup/benchmark_test.go
975ef1d583b906d96e4777199c8db9535a9e571560f9738d9eaeebbb384537ad  apps/node-go/internal/rollup/README.md
c4fab11206decfc12536b2d79d2250ec1458731a6cab4c50ef07228548deb318  apps/node-go/internal/rollup/rollup.go
0864a1b914480967077f56463f696edff56fd03b046a8b44508cedc81fa24951  apps/node-go/internal/rollup/rollup_test.go
2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a  go.mod
6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47  go.sum
```

The added fixture's SHA-256 is:

```text
5361fc83de7e56ef9ac3ffecc54b7a24905151c9ebcbb56f5193ca0907b77e1d  apps/node-go/internal/rollup/review_test.go
```

The private `final-validation.json` and `final-manifest.sha256` record final integrity checks and this report's hash without placing a self-referential hash in the report.

Executed independently on native Windows/amd64, using `.canary/tools/go/bin/go.exe`, which reports `go1.27.3 windows/amd64`. Its executable SHA-256 is `83b7ba66215e696414fa49462d0db2da4f4cf15c7bebd42f0b189fa04866999d`, matching the owner evidence. The private `setup.ps1` fixes `GOTOOLCHAIN=local`, `GOWORK=off`, `GOENV=off`, `GOPROXY=off`, `GOSUMDB=off`, `GOVCS=*:off`, native OS/architecture, empty `GOFLAGS`, and private build/module/temp caches. Every test and vet invocation used `-mod=readonly`.

- Original suite before fixture addition: exit 0, `baseline.log`.
- Independent fixtures: exit 0, `review-native.log`.
- Full package suite including original assertions and added fixtures: exit 0, `full-native.log`.
- Package vet: exit 0, `vet.log`.
- Selected native race tests for ownership, mixed concurrent evidence, and atomic errors: exit 0, `review-race.log`. This used `CGO_ENABLED=1` and the existing `.canary/tools/race-gcc/extracted/w64devkit/bin/gcc.exe`; other native checks used `CGO_ENABLED=0`.

Commands executed from the repository root, with the private environment setup above:

```powershell
$reviewGo = '.\.canary\tools\go\bin\go.exe'
& $reviewGo test -mod=readonly -count=1 -timeout=90s ./apps/node-go/internal/rollup
& $reviewGo test -mod=readonly -count=1 -timeout=90s -run '^TestReview' -v ./apps/node-go/internal/rollup
& $reviewGo vet -mod=readonly ./apps/node-go/internal/rollup
& $reviewGo test -race -mod=readonly -count=1 -timeout=90s -run '^Test(ReviewOpaqueIDsOwnershipAndMixedConcurrency|ReviewErrorsDoNotReuseHealth|OwnershipAndConcurrentEvaluation)$' -v ./apps/node-go/internal/rollup
```

The new reference expands each subtree independently into nodes and enabled paths, derives health from active leaves and terminal active groups, and counts structural membership separately. It does not call the existing test oracle, use compiler internals, or aggregate production child results. A separate bounded ancestor walk independently decides graph validity.

- All 4,096 three-node combinations of parent selection, group flags, and enable flags were checked in all six node orders: 448 valid and 3,648 invalid combinations, totaling 24,576 order cases. Compile and Validate agreed with the independent validator; every valid result agreed with the new reference. This includes disabled cycles, self-links, non-group parents, multiple roots, empty groups, case-sensitive IDs, and children appearing before parents.
- All 9,248 combinations of five enable flags and two independently varied evidence records were checked in forward and reverse node/observation orders. Each record has 17 variants: missing, or each of four states with both freshness and maintenance flags. A separate disabled group contains a maintained DOWN leaf. This exercises precedence, missing/stale unknown, empty/all-paused pending, maintenance independent of freshness, structural counts, disabled-path exclusion, standalone descendant health, and both count identities.
- Invalid observations were placed before and after valid observations, across stale/fresh evidence and enabled/disabled leaves. Cases include duplicate, unknown, empty, group, paused, empty-state, and unrecognized-state observations. Errors returned nil results; subsequent missing evidence produced unknown; earlier caller-owned healthy results stayed unchanged. Invalid Compile calls also preserved an existing plan and their input slices.
- Twelve workers repeatedly evaluated independent four-state/missing/stale/maintenance evidence, mutated their returned maps, and interleaved failing evaluations against the same plan. Input node storage was overwritten after compilation; observation storage was overwritten after evaluation. Opaque IDs include case pairs, whitespace, NUL, invalid UTF-8 bytes, composed/decomposed Unicode, and a bounded 4,000-byte ID. The selected race run included these workers and the original concurrent ownership assertion.
- A fully disabled 100,000-node cycle was rejected as a graph error. A 100,000-node chain with a disabled middle group was evaluated in both input orders; every result's exact health and counts were checked. The cut excludes its entire branch from ancestors while descendants retain standalone DOWN/maintenance evidence. Node and observation overflows wrapped ErrCapacity and returned no partial results. A subsequent evaluation used new missing evidence. The original exact-100,000-observation acceptance assertion also ran.

Nine semantic mutations were applied only through private Go overlay files. All nine built and failed their selected assertions (exit 1); the repository source was never mutated. Eight were detected by the new fixtures, and the inclusive-observation-limit mutation was detected by the preserved original boundary test:

1. Treat stale UP as fresh health.
2. Require freshness for maintenance.
3. Make an empty/all-paused group UP.
4. Skip disabled-leaf observation validation.
5. Inflate paused counts during parent propagation.
6. Discard standalone leaf evidence beneath a disabled parent.
7. Accept cyclic graphs.
8. Rank pending above unknown.
9. Reject exactly MaxObservations records.

`mutations.json` records the selected test, exit status, and mutant/log hashes; `mutations/` retains each overlay, source, and log. Each run used `go test -mod=readonly -count=1 -timeout=30s -overlay <private-overlay> -run <selected-test> ./apps/node-go/internal/rollup`. An initial PowerShell argument form passed the overlay variable literally, so those attempts ran no tests. The argument was corrected and all nine checks were executed successfully. The initial diagnostic is retained separately as `mutation-harness-initial-argument-error.log` and `mutations-initial-argument-error.json`.

Inspected independently: the entire README, implementation, original tests/benchmarks, added fixtures, owner evidence, and the [pinned upstream group implementation](https://raw.githubusercontent.com/louislam/uptime-kuma/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/group.js). The source confirms that inactive children are skipped and the initial UP survives for a nonempty group whose children are all inactive. Canary's documented pending result is the explicitly required conservative difference. Inspection also confirms iterative graph processing, direct Compile delegation in Validate, value-only exported results, read-only plan use during evaluation, and no clock, history, latency, uptime, I/O, or notification behavior in the leaf.

Not independently executed: coverage measurement, generated fuzz campaigns, benchmarks, Linux cross-compilation, Linux/Alpine runtime, or deployment. Owner metadata reports 100% coverage, race success, 20,000 fuzz executions, and Linux cross-builds; those remain owner evidence. Ordinary native package tests executed the existing fuzz seeds. The focused race run covers the named tests, rather than claiming an independent full-suite race run.

Capacity here limits node/observation cardinality at the computation boundary. Traversal and bookkeeping scale with those cardinalities; string hashing, comparisons, formatting, and total input/output bytes also depend on identifier/state lengths. This review makes no universal total-resource bound for arbitrary unbounded strings. Identifier/field bounds remain a future root integration responsibility. Future integration must also preserve the valid configuration union on capacity errors, present unknown health on evaluation failure/overflow, and avoid reusing prior healthy results as current evidence. Those integration obligations remain unverified because this package is not integrated into root/API/HA/UI.
