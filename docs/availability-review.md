Independent availability package review, 2026-10-22.

Verdict: no remaining correctness or resource-limit blocker found in the reviewed pure package. The initial relocation broke the original test imports; root corrected those imports, and the corrected snapshot passes the original tests, independent review tests, vet, three bounded fuzz campaigns, and focused benchmarks. This verdict applies to the content hashes below.

The reviewed production file is `apps/node-go/internal/availability/availability.go`, SHA-256 `a6a2ee660e936039fd9ab0e0c7abc0b7b711cc0214d01f72d81bd36ebfe00f27`. It was byte-identical before and after the import correction and remained unchanged through final shared-tree verification.

The reviewer added only `apps/node-go/internal/availability/review_test.go` and this report to the shared tree. No existing implementation, test, assertion, README, module, core, or UI file was edited by the reviewer. No nested agents, process services, database access, environment-file reads, dependency installs, or whole-node test suite were used. Private snapshots copied only the package and root `go.mod`/`go.sum`.

The first private snapshot was frozen and source/copy hashes checked before running experiments:

```text
C:/Users/Michael/AppData/Local/Temp/uptime-availability-review-ee987c1d252d4479912b1e65359a48a4
snapshot-manifest.json SHA-256:
74d123e473662c187fa03e90d3e49159cb665efc85e3c2435bf09c262630191c
```

In that snapshot, focused `go test` and `go vet` both failed during setup because the three original external test files still imported `uptime-canary/internal/availability`. The error was `package uptime-canary/internal/availability is not in std`. This was a relocation defect, separate from availability arithmetic.

After root's correction, a second private snapshot was frozen and checked before semantic experiments:

```text
C:/Users/Michael/AppData/Local/Temp/uptime-availability-corrected-d6969da544f644b0926c907fc7c4f165
snapshot-manifest.json SHA-256:
cf18ba75aafbff64ece07aec681c7155cc7e0c7baca82ab23b01121fbfcd16c2
```

For each of the three original test files, replacing the old import string in the initial bytes with `uptime-canary/apps/node-go/internal/availability` reproduced the corrected file byte for byte. No assertions or other bytes changed. Final verification rehashed every initial snapshot file and every corrected snapshot file, and matched all corrected files against the shared tree.

SHA-256 manifest for the corrected snapshot; paths are relative to the repository root:

```text
7424  apps/node-go/internal/availability/availability.go
a6a2ee660e936039fd9ab0e0c7abc0b7b711cc0214d01f72d81bd36ebfe00f27

24535 apps/node-go/internal/availability/availability_test.go
8493f2fe7362986846b40c1674dd4c75162504fa958d6389a084c7de0126a122

2140  apps/node-go/internal/availability/benchmark_test.go
c2ff8aa6be0d89b65be0eff39d5539a6ff1bccd830888f8b8370899e61aae117

4194  apps/node-go/internal/availability/fuzz_test.go
eaccf81631d034039d1feff20154fec14279bcab51b2bff22511e5c1c1bddca1

9344  apps/node-go/internal/availability/README.md
4d334026945133715de17e94a35fc0018c26be91331db9dbecebccac946c2fee

14086 apps/node-go/internal/availability/review_test.go
c0ee4c5bf2f477aff01d26eb3f4c575168bdda6d66afe28902436ba4934e0f04

1817  go.mod
2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a

15511 go.sum
6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
```

The initial snapshot had the same implementation, README, and module hashes. Its original test hashes and byte sizes were:

```text
24522 apps/node-go/internal/availability/availability_test.go
f27a78effc74338e9f83180f77da59e4aea3bd93e5d721c61dc2db6f0aa0ad3c

2127  apps/node-go/internal/availability/benchmark_test.go
829d4bc42a9837d9775fb1c42e528556acc5284cb1ae844ae24158bebfe54791

4181  apps/node-go/internal/availability/fuzz_test.go
b59106e4bcb7804ec61d461661fdd97ed53fe41e78e3e37f5f17c2915589350d
```

Independent semantic checks and reasoning:

- The new oracle starts from a complete labelled partition containing gaps and all four states, each with and without maintenance. It generates the input spans separately. Expected query measures are differences of prefix measures over the original cells; the oracle never reads the generated spans or calls production functions. Percentages use exact `math/big.Rat` arithmetic before conversion. Expected bucket edges enumerate an arbitrary-precision epoch lattice without the production count formula or returned endpoints.
- `TestReviewExhaustiveLabelledCells` exhausts all 6,561 assignments of the nine possible labels to four unequal cells, clips both query edges, and splits observations at bucket boundaries. `TestReviewWeightedCellsAtSafeTimestampEdges` adds 240 deterministic weighted timelines across origins zero, an arbitrary epoch offset, and the inclusive JavaScript-safe maximum. Scales reach the maximum supported step. A separate full-domain summary includes one-millisecond observations, huge gaps, and boundaries around 2^31, 2^32, and 2^48.
- Gap cells remain unknown, explicit unknown maintenance is counted, pending stays outside known time, and maintenance does not change either percentage denominator. Bucket duration fields reproduce the independently measured summary; edge coverage uses clipped window length. Availability is null exactly when known time is zero, including pending/unknown-only buckets. Known down time has a numeric zero percentage.
- Strict invalid states, reversed/overflowing timestamps, unsorted disjoint intervals, and overlaps wholly after an otherwise valid query reject the whole input and return zero totals or nil buckets. Original tests also cover invalid prefixes, duplicates, touching spans, query/step extremes, and exact acceptance/rejection limits. Input equality is checked after successful and rejected calls.
- `TestReviewCombinedLimitsAndPointerOwnership` combines 100,000 supplied spans with 4,096 clipped epoch buckets near the timestamp ceiling. It includes 99,999 irrelevant earlier spans and one observation crossing every bucket. Every returned percentage pointer must be distinct, and writes to all 4,096 pointers must remain local. Independent summary calls must also own their percentage values. A further clipped edge producing 4,097 buckets is rejected, as are 100,001 supplied spans.
- `TestReviewFlatJSONExactIntegersAndNull` checks exactly ten flat fields for summaries and buckets, exact integer tokens near 2^53-1, present null availability for no known history, and JSON round trips. No nested `Totals` object or omitted zero/null field is accepted. Percentage comparisons explicitly reject NaN, infinity, and values outside [0,100].
- Arithmetic review: successful validation bounds every timestamp to [0,2^53-1] and every interval to positive length. The predicates also reject starts above the ceiling because no supported end can exceed them. Sorted disjointness bounds every accumulated duration and state sum by the query length. Bucket-count arithmetic only subtracts one from an already positive end and operates on bounded nonnegative quotients. Conversion to `int` occurs after enforcing the 4,096 cap. Next-boundary addition happens only when its distance is strictly less than the remaining validated query length. No timestamp or duration overflow path was found.
- Complexity review: validation walks all supplied spans once. The sweep index only advances or retains the single span crossing a bucket edge. Each repeated crossing consumes another bucket, so total intersections and skips are O(spans+buckets), including gaps and spans wholly outside the query. There is no per-millisecond production scan, sorting, input write, or retained global state. Output is bounded by the result slice plus at most one percentage backing array with distinct elements.

Review-test sensitivity was checked using four deliberate production mutations, each in a separate private overlay. Every mutant failed a new review assertion: treating explicit unknown as pending; including a bucket starting at the exclusive end; skipping span validation; and aliasing all percentage pointers. These are test-validation experiments, not findings against the unchanged implementation. The frozen sources and shared production were not edited. Private mutant sources, overlay files, hashes, and assertion output remain under:

```text
C:/Users/Michael/AppData/Local/Temp/availability-review-mutations-d6969da5/results.json
```

Pinned toolchain: `.canary/tools/go/bin/go.exe`, `go version go1.27.3 windows/amd64`, SHA-256 `83b7ba66215e696414fa49462d0db2da4f4cf15c7bebd42f0b189fa04866999d`. All Go runs used process-local `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `GOMAXPROCS=2`, and `-mod=readonly`; compilation was limited with `-p=1`. The observed default was `CGO_ENABLED=0`.

The corrected private snapshot passed all 18 named tests and the 12 seed cases across three fuzz targets. Focused package time was 0.507s. Vet passed. A final focused run against the shared tree passed in 0.511s, followed by successful shared-tree vet and hash comparison. Each fuzz target ran sequentially with a three-second fuzz budget and one worker:

- `FuzzObservedTimeline`: 42,979 executions, pass.
- `FuzzInvalidInputs`: 1,637 executions, pass.
- `FuzzReviewWeightedCells`: 26,157 executions, pass.

The new fuzz decoder bounds each case to 24 weighted cells/spans and at most 768 nominal buckets plus a possible clipped edge. It frequently ends at exactly the timestamp ceiling. Original fuzz decoders separately bound their input counts and small per-millisecond oracle windows; raw invalid timestamp fuzzing does not feed unbounded durations to that oracle. No fuzz failure corpus was produced.

Focused 100ms benchmarks passed on this Windows amd64 host. Representative measurements were 333,051 ns/op, 8 B/op, one allocation for a 100,000-span summary; 469,924 ns/op, 281,216 B/op, two allocations for maximum-span buckets; and 155,501 ns/op, 360,448 B/op, two allocations for 4,096 buckets crossed by one span. The full safe-domain empty-history summary measured 29.32 ns/op with no allocation. These measurements support the source-level resource analysis; they are not service budgets or portable timing guarantees.

Reproduction commands, from the repository root using a disposable PowerShell process, or from the corrected private snapshot with the same absolute pinned tool path:

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
$env:GOMAXPROCS = '2'
$reviewGo = 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe'
& $reviewGo test -mod=readonly -p=1 -count=1 -v ./apps/node-go/internal/availability
& $reviewGo vet -mod=readonly -p=1 ./apps/node-go/internal/availability
& $reviewGo test -mod=readonly -p=1 '-run=^$' '-fuzz=^FuzzObservedTimeline$' -fuzztime=3s -parallel=1 ./apps/node-go/internal/availability
& $reviewGo test -mod=readonly -p=1 '-run=^$' '-fuzz=^FuzzInvalidInputs$' -fuzztime=3s -parallel=1 ./apps/node-go/internal/availability
& $reviewGo test -mod=readonly -p=1 '-run=^$' '-fuzz=^FuzzReviewWeightedCells$' -fuzztime=3s -parallel=1 ./apps/node-go/internal/availability
& $reviewGo test -mod=readonly -p=1 '-run=^$' '-bench=.' -benchtime=100ms -count=1 ./apps/node-go/internal/availability
```

Limits of this review: fuzz campaigns are short and bounded; this is not an exhaustive proof of every int64 input or a service-level performance guarantee. Concurrent read-only calls passed the original functional concurrency test, but the race detector was not run. No Linux execution or cross-compilation was performed. SQLite/archive transactions, observation provenance, retention, API/UI wiring, and integration/release behavior are outside this pure package and were not reviewed. They remain root integration responsibilities. The corrected package has no outstanding review blocker within the stated contract.
