# JSONata leaf validation, 2026-10-22

## Current R6 repair after independent ROUND2 and the Linux length gate

R6 edits only `internal/jsonquery/**`. All three independent review files,
all 408 pre-R6 JS records/scripts, the original maximum-document assertion and
all earlier histories remain byte-identical. No root module, notices generator,
process/worker limits, core/service/check/UI, environment file or database was
edited. Request/API, cheap validation, nil bindings and the fixed per-expression
numeric shim remain unchanged. Root retains module/notices ownership.

[ROUND6_REPAIR.md](ROUND6_REPAIR.md) records the implementation contract:
fractional limits preserve their original number; empty input reaches required
matchers; typed native partials retain undefined bindings, full native metadata
and context; replacement callbacks see original match objects; optional captures
retain positions/custom values; and the length count no longer allocates a
million-element intermediate array. The central depth-128/65,536-value walker
is unchanged. No arbitrary binding, global registration, reflected closure
inspection, evaluator logging, parent execution or fallback was introduced.

Ordinary native signatures validate as before. Pinned JS native partials bypass
the outer signature, so only the transient partial call copy omits that
validator while retaining the original header. The affected sum/power/contains
implementation semantics and eval context are corrected rather than relabeling
valid finite results as safe-DOWN. Typed eval parameter metadata preserves its
observable expr/focus locals and prevents invented placeholder names from
appearing in eval. This is a tested scoped repair, not full builtin/coercion,
JSONata conformance or universal no-false-UP certification.

The final schema-v3 [SOURCE_FREEZE.json](SOURCE_FREEZE.json) enumerates **51
files**, excluding package prose. It froze before the final full checks and all
entries remained unchanged through completion. The earlier R6 freeze was
superseded by five final eval-scope probes; its manifest/patch/provenance and
exact changed test/reference bytes remain in `testdata/history/finite-profile-v3-interim/`.
The v1 and v2 histories are also preserved. Old failure reports/artifacts were
not edited or relabeled as passes.

Completed checks on **Go 1.27.3 windows/amd64**, dependency network disabled
with `GOPROXY=off`, `GOSUMDB=off`, builds/tests/vet using `-mod=readonly`:

- Final full leaf command, `go test -mod=readonly -json -count=1
  ./apps/node-go/internal/jsonquery/...`: **PASS, 716 test/subtest events**,
  zero failures/skips. Leaf: **700 events / 22 top-level tests / 12.107s**.
  Evaluator helpers: **16 events / 11 top-level tests / 0.588s**.
- Every original **505 event remains passing**. ROUND2's unchanged **105
  worker cases plus enclosing test** now all pass: all **56 prior false-UP**
  assertions, **ten finite-DOWN** mismatches and **39 prior controls**. Its
  four already approved stronger-profile cases retain JS UP and require Go
  bounded DOWN. No finite wrong result/error was reclassified as policy.
- R6 adds **97 worker records plus enclosing test** and **seven codepoint
  count-helper events**: 505 + 106 + 98 + 7 = **716**. New cases cover both
  sides of invalid partial sort outcomes, fractional/negative/zero limits,
  literal split conversion, empty/undefined inputs, original callback data,
  callable next/cursor behavior, optional/custom group positions, repeated
  partials, lexical/context/undefined eval behavior, codepoint lengths and the
  approved returned-overflow profile. Public private-error markers stay redacted.
- All **610 pinned offline JS records verify**: original **408 unchanged**,
  **105 unchanged independent embedded records**, and **97 R6 records**.
  New R6 classification: **73 matching UP**, **22 matching DOWN/error**,
  **two approved safe-DOWN** outcomes. The old 408 are not regenerated or
  reinterpreted. The R6 verifier checks the independent Go-embedded JSON
  directly without editing its file.
- The unchanged `TestMaximumDocumentThroughWorker` passes on Windows with its
  one-MiB ASCII JSON string and expected **1,048,574** codepoints. Tests also
  retain advisory/parent timeout, cancellation, depth/range/output/visit limits,
  cycles/aliases, kernel-OOM Pending handling, recovery and fixed diagnostics.
- Pure count-helper tests cover ASCII, multibyte, combining, empty and invalid
  byte/range behavior without evaluator compilation/execution. The one-MiB
  allocation regression measures **zero allocations**. The benchmark reports:

  | Pure helper benchmark | ns/op | B/op | allocs/op |
  | --- | ---: | ---: | ---: |
  | ASCII, 1 MiB | 235146 | 0 | 0 |
  | Multibyte | 452339 | 0 | 0 |
  | Combining | 136696 | 0 | 0 |

  These host measurements use `-run '^$' -bench
  '^BenchmarkStringCodepointCount$' -benchtime=100ms`; they measure the count
  helper, not zero allocation for the complete evaluator/worker.
- Final `go vet -mod=readonly ./apps/node-go/internal/jsonquery/...`: **PASS**.
  `go mod verify`: **all modules verified**. x/crypto remains **v0.58.0**;
  current root module hashes below are unchanged throughout R6.
- Final Linux **amd64/arm64**, **CGO_ENABLED=0**, `-mod=readonly -trimpath`
  leaf and evaluator test-executable builds: **PASS**. Metadata confirms Linux
  targets and only x/sys beyond the main module for the leaf. These are static
  builds, not actual Alpine runtime or release certification.
- Final raw-upstream patch reconstruction: **PASS** against exact Git blobs
  and cached commit `599f35f32e5f31297f8b2153b5da44ddbb330e28`. The patch
  reconstructs all **11 production files**, retaining three original Go files
  and LICENSE byte-for-byte; three originals are edited and five helpers added.
  Final diff: **624 insertions / 418 deletions**, **1,423 patch lines**.
  Prior comment-only Unicode encoding drift is removed; copyright headers and
  LICENSE remain accurate. No modcache or module source is edited.

Actual Linux maximum-document status remains separate. Root's immutable **8b5**
development binary reproduced Pending after approximately **1.36–1.43s**, before
its unchanged four-second deadline, in Alpine/direct, restricted Docker and
isolated repeats. Observed VmData near **131,616 kB** and VmRSS around **60 MiB**
support a memory-pressure inference, not a confirmed exact exit cause. The old
failure and evidence remain in `docs/container-validation.md`. No 128 MiB/64
MiB limit or old document assertion was weakened. Root must rerun the unchanged
test on actual Alpine with this exact new artifact. Root's 8b5 process/core/race
passes and separate deployment snapshots remain developmental platform evidence,
not certification of these semantic repairs, final R6, or release readiness.

Final freeze/provenance SHA-256:

```text
SOURCE_FREEZE.json               842457019b5516a84d2e525c7d0cb976157e8b4f21ad2751b43798dd08cca98f
evaluator/upstream.json          0463bb46a968a6ad4fe41ace181975bc86c1c3368b388491ea11e1f793bcb533
evaluator/finite-profile.patch   85ea0bcede0301f1fcf6e4153a1f90e87a5909b0949c14cc8b41325373504d41
evaluator/functions.go           b90aba9bc4b1e0c344832e827be0d56c3cfff06d798615ff55385631dd3e4a52
evaluator/jsonata.go             8ba85fcf80935e07c2d6b5724cf9d1dc422ec8f8fbff59d09d8dbe89799a2b91
evaluator/utils.go               cde9dd88ae71a0dd619131c275a275960d1202279d75c6347df4630bd4296ccd
evaluator/native.go              cd866e9967bc3a5ac2c0b40530ff6561d68c34b01899370d330252b7a50c8de6
evaluator/matchdata.go           d8106d309a53d0e76b837cfae1597eeaa2ecbf0616d48a32e31ef26ae56b25dc
evaluator/length.go              39336aad7ca2c314e806ead791c7234890663f945b40d29a9387e9d0d90a800a
```

The unchanged finite walker remains
`03d2f4ad804e2bdb18b977b5db8de317ef82e9065d441b67135fa2b6d3bc4f47`;
LICENSE remains `188e548d24101b7580e507e48e1a71d76e8c8add79d2e0855f3d78555637ff3f`.
The independent ROUND2 test remains
`22f98416fb31b6394eb8ea12d0947a6501eee9436c49a5a12d30807544d555c0`;
the two older review-file hashes remain as listed in the historical section.

Final artifacts under `%TEMP%\canary-jsonquery-research`, SHA-256:

```text
go-test-round6-final.json                 27b3efab5a9fe7746516976161a96e8c636741bc29b1a1e4fd95d7aa178fb16e
jsonquery-round6-final-linux-amd64.test   6e5bad7875e06bc638c0a69abab0ce2aceb153f0aa6503217666d9474ef4acb6
jsonquery-round6-final-linux-arm64.test   66924bcf8a69eeb4a877fa41038bdd2b4f6377f8d793c79e089123955da36b34
evaluator-round6-final-linux-amd64.test   cbde08025ca5d15c810b345087032648a1159864a8dbda4289937a90f22291cc
evaluator-round6-final-linux-arm64.test   c314f427a59658729222d5a117961fdd2b670265c3cba331b9bafc930391b515
```

The leaf binary needs six runtime JSON fixtures: the original five plus
`testdata/round6-fixtures.json`. The 105 independent cases are embedded in its
unchanged Go test file. The separate evaluator binary supplies its 16 helper
events; a leaf-only run must not be reported as 716 events.

Superseded R6 intermediate evidence remains, not overwritten:

```text
interim SOURCE_FREEZE.json               d2b1f331f58bb9942c23aaa71f3c0692aa2d56db57a50ef9183c8f6ed3e78aab
interim evaluator/upstream.json          4c7e4e7be6234be619dae996fa274d0e9bcd8bb936a1d0e43b1172ca652fe0c7
interim evaluator/finite-profile.patch   a13d202601a781e665895f194d77a267a1d65222b0ab916bec910c26c978934c
go-test-round6-repair.json               aad62db54d1d78c0a13b1e0d171d351db086158ab39583d018dc8bb5233ca356
jsonquery-round6-linux-amd64.test         c0b481d4759e37c873715a3f40f69263fd430249bab38c6c04023a21ddfa76e0
jsonquery-round6-linux-arm64.test         d00cc744063ccdf883e6180aab74b917d854e91fa253eec3ef2def20a2776f0f
```

That intermediate suite passed 711 events before the final five scope probes
were added; it is not the final source or a replacement for its validation.
The exact changed old test/reference bytes are preserved and checked against
their old frozen hashes in `changed-owned-files.json` (base64 data, not code
executed by any test). Original ROUND2 failed evidence remains under
`%TEMP%\canary-jsonata-matcher-sort-review-08980ad9`, with the report's immutable
hashes unchanged, including its final-tests.json
`8b040399cd0d876c87e7092de5fa1d61116950ac879edac3510eb4a33cdb3dfc`.

All owned leaf source is released after this completed validation/documentation.
Root modules/notices stay root-owned. Independent ROUND3 and the actual Alpine
maximum-document rerun are still required; no RC signoff is claimed.

## Historical first matcher/sort repair before independent ROUND2

The following 505-event evidence and source/artifact hashes describe the prior
freeze. ROUND2 subsequently demonstrated 56 false-UP and ten finite-DOWN cases
in its retained paths. Its passing baseline was never a whole-language or RC
certificate. The current R6 evidence above supersedes its implementation status.

The authorized followup changes only `internal/jsonquery/**`. No existing review
test or original reference record was edited. Root modules/notices/process/
integration and other production packages were not edited. The public API,
shape-only validation, one fixed pure numeric registration, nil evaluator
bindings and OS worker limits remain unchanged. Root's earlier module cleanup
is respected; module ownership remains root's.

`MatchResult.Next` now carries errors; one adapter handles initial and recursive
map results, and `$match`/`$split`/`$replace` propagate every continuation error.
Sort invokes native/lambda/partial and internal order-by comparators through
the existing guarded helper, validates native signatures, propagates errors,
and uses JS conditional truthiness. Explicit private callability flags preserve
the reference distinction between raw native/partial objects and argument
wrappers, without closure/reflection inspection or global mutation. Groups
returned by `$match` use the engine's JSON array representation. See
[FINITE_PROFILE.md](FINITE_PROFILE.md) for precise scope and deliberate limits.

Source/tests/provenance froze before the full checks at
[SOURCE_FREEZE.json](SOURCE_FREEZE.json), **35 enumerated files**, with package
prose explicitly excluded. Its SHA-256 and all listed files remained unchanged
through completion. No production/test edit followed the freeze. Root arranges
fresh independent review and its separate private-artifact process proofs.

Completed checks on Go **1.27.3 windows/amd64**:

- Full `go test -mod=readonly -json -count=1 ./apps/node-go/internal/jsonquery/...`:
  **PASS**, **505 passing test/subtest events**, zero failed or skipped events.
  Leaf: **7.120 seconds**, **496 passing events / 20 top-level tests**.
  Evaluator: **0.449 seconds**, **nine passing top-level tests**.
- All **292 original baseline events remain passing**. The unchanged review
  files had previously produced **324 passing / 22 failing events** (20
  assertions plus their two enclosing tests). They now give **346 passing /
  zero failing events** together with the baseline. The 18 matcher error
  assertions and both native-sort assertions now pass; their finite exhaustion,
  boundary, lambda and pure-walker controls remain passing.
- New `matcher_sort_repair_test.go`: **157 cases + its enclosing test**;
  new pure truthiness test: **one event**. These add **159 passing events** to
  the unchanged baseline/review suite, giving **505** total. New cases cover
  initial/recursive/native matcher errors, malformed continuations, redaction,
  aliases, nested eval, later stages, partial/wrapped callability, regex
  exhaustion/multiple/zero-width behavior and capture groups, and comparator
  arguments/errors/truthiness/unsupported types/native HOFs/internal order-by.
- The approved stricter profile is enforced for
  `$count($sort([[1e308,1e308],[1]],$sum(?)))`, its alias, and nested-eval form:
  all return bounded DOWN/error. Pinned JS returns 2/UP; these **three deliberate
  safe-DOWN divergences** are explicitly classified, not claimed as matches.
- Original five numeric and eight independent nonfinite regressions, bounded
  depth/visit/cycle tests, advisory timeout, parent timeout/cancellation, OOM,
  redaction and recovery all pass. The independent 34-probe gate still reports
  22 matching states, 12 deliberate safe-DOWN differences, zero false-UPs,
  zero incomplete probes and eight closed original regressions.
- Offline pinned JS **jsonata@2.2.2** verification: **408 records PASS**.
  All **251 existing records and their scripts are byte-for-byte unchanged**.
  The 157 new records are **54 matching UP**, **100 matching DOWN/error** and
  **three deliberate safe-DOWN** outcomes. Malformed callback records preserve
  their JS errors, including raw partial/native continuation contract failures;
  wrapped finite forms remain UP. No project npm dependency was installed.
- `go vet -mod=readonly ./apps/node-go/internal/jsonquery/...`: **PASS**.
  `go mod verify`: **all modules verified**. x/crypto remains **v0.58.0**.
- Linux **amd64 and arm64**, **CGO_ENABLED=0**, `-mod=readonly -trimpath`
  builds for both leaf and evaluator test executables: **PASS**. Build metadata
  confirms static Go targets and only x/sys beyond the main module for the
  leaf executable. These are builds, not new Alpine runtime/process-proof or
  security-scan evidence; no release or independent review signoff is implied.
- Exact upstream patch reconstruction: **PASS**. Six raw upstream files and
  LICENSE still match the recorded Git blobs/cache. The revised 687-line patch
  modifies three originals and adds two local helpers: 285 insertions and 207
  deletions. It reconstructs all eight production files byte-for-byte from raw
  upstream with Git CRLF conversion disabled. No source-header/license change,
  modcache write, global registration, expression rewrite or host binding occurs.

Frozen SHA-256 for fresh review:

```text
SOURCE_FREEZE.json               5d59f53fab22c8836c1dd05e3780d9a1a58a5bc49d2e3b3074d0eccee88c28bf
evaluator/upstream.json          a84bfcbcd64d435a16fa83d505f438b2840f804559fd99314d9cfbbf76333c97
evaluator/finite-profile.patch   dc8faa3849dfe73ad6c872aa61d828705d75c11472b3367561d43c4214eee783
evaluator/functions.go           872e9df482e7c9fe447cf8a3e79e43e82b5e2c9b6f82aa76fd121d70c0c4b688
evaluator/jsonata.go             2f0159616896c7d0817b84515644f5dc67245f709ef8be140b2f08ed04361229
evaluator/utils.go               7d81f3c70ab17df74b8ca54fd92a0d602078a8fbf3a1050b5ca13f8d18e4b614
evaluator/callbacks.go           9ef538354054b6852241a7dbe6a2b17017d8567111529d3336065ddccb07f525
```

The walker, public config, wrapper and numeric shim remain unchanged from the
first local-copy freeze. LICENSE remains
`188e548d24101b7580e507e48e1a71d76e8c8add79d2e0855f3d78555637ff3f`.
Unchanged independent review test SHA-256:

```text
profile_review_test.go           cabd65607e780b73197e84959b681cb70d24033cde4d0a5b59e670e0b066e34c
evaluator/finite_review_test.go   c801a0c52d659c76a8850b62ff434a55c5508b8308d1c96dfcde880342c33e27
```

New artifacts under `%TEMP%\canary-jsonquery-research`, SHA-256:

```text
go-test-matcher-sort-repair.json          48fb35f831777f4b54a29fd98ef294ab0e8d5d4edd590609a8790b98cf0cece2
jsonquery-matcher-sort-linux-amd64.test   ed3ce6e0cde8c82bb0af13a3f5a14010e1597fd99a1e735cb8502caa9efd653f
jsonquery-matcher-sort-linux-arm64.test   64acabd88e303a4c572beb7e14ebacedfbb0ec3fe17ee79d5dd15dec71f052ad
evaluator-matcher-sort-linux-amd64.test   576cb2048ed63b156afc6f8dbbdbccbcb9a6472340e5c466a5a366e4d56952a5
evaluator-matcher-sort-linux-arm64.test   b44d9b7e0db793bc0787cf76df1b6a32388900397d89a32da31a5880949d5ecf
```

The old freeze/provenance/patch remain in
`testdata/history/finite-profile-v1/`; their original SHA-256 and first-build
hashes remain below. The independent review's private source/artifacts were
read only, not replaced or relabeled. Original evidence under
`.canary/jsonata-profile-review/run-45a39cf4/`, SHA-256:

```text
baseline-tests.json                  789011d9c1197cb883d40205567ba1bc2f080ceb3f2392a529f7214711de01ca
final-tests.json                     e8ce9e5fbfdd3a111ee9ea8e282f73803ae6333cab9854380f51dbe51d815ef9
review-js-results.json               09202f2e6513f1db3d8cc17ef1df0a2fd0fe5ef4440557c9267a2d20e443ba67
review-jsonquery-linux-amd64.test     70c4fa94ac59480f91b5a504b42188f6e1f623e62e807084f0743596bbef5438
review-jsonquery-linux-arm64.test     265418d145cd1cdf2537b2e8bc50c3ac99c06d4f8ac86c13312a4b6b2fa6fe06
review-evaluator-linux-amd64.test     d176623f9b3902672145d93c1ba87dfdcc31d412d9bf1947305d09aa5d848833
review-evaluator-linux-arm64.test     43bfc2a881564309af49b3f594c0033bed2a4da097cf02d7b3fcfe4b941acd82
```

The unchanged root-owned module hashes observed throughout this followup were:

```text
go.mod 2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a
go.sum 6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
```

All owned leaf source is released. Root modules/notices remain root-owned. The
fixed errors and explicit safe-DOWN profile are not a universal no-false-UP or
complete JSONata conformance claim; root's fresh independent review remains
separate from this completed implementation validation.

## Historical first local evaluator repair before matcher/sort review

The following 292-event evidence and hashes describe the first local copy,
before the independent review identified the two matcher/sort P1 findings.
The current followup above supersedes its implementation status; its source
baseline, artifacts and observed outcomes are retained unchanged in meaning.

Root authorized the local v206 source copy and the two central result guards.
All new implementation edits remain under `apps/node-go/internal/jsonquery/**`.
The wrapper imports `internal/jsonquery/evaluator`; the public request/config,
cheap validation, worker handler and OS/process limits remain unchanged.
No root module, notice-generator, process, check, service, UI, database, environment
file or outside-package documentation was edited. Module ownership was already
released and remains with root. Root separately owns final integration/notices,
module cleanup and the new independent review.

The code, tests, provenance, exact upstream patch, LICENSE and reference records
are frozen in [SOURCE_FREEZE.json](SOURCE_FREEZE.json). No source/test changes
followed that freeze; only package prose was updated with completed evidence.
[FINITE_PROFILE.md](FINITE_PROFILE.md) documents the exact result boundaries,
depth 128 / 65,536-value budget, bounded cycle handling, fixed error and limits
on the claim. This deliberately rejects some JS-success expressions and is not
a full-language or universal no-false-UP proof.

Completed checks on Go **1.27.3 windows/amd64**:

- `go test -mod=readonly -json -count=1 ./apps/node-go/internal/jsonquery/...`:
  **PASS**. Leaf **18.775 seconds**, **16 top-level tests / 288 passing
  test-subtest events**. Evaluator pure walker tests: **0.831 seconds**,
  **four passing top-level events**. Total **292 passing events**, zero failed
  or skipped events. Actual compilation/evaluation uses only the production
  self-exec worker. Pure walker tests do not compile/evaluate or add bindings.
- All eight independent pre-repair false-UP paths now require bounded DOWN/error.
  The gate completes all 34 probes: **22 matching state outcomes**, **12
  deliberate safe-DOWN differences**, **zero false-UPs**, **zero incomplete**,
  **eight closed regressions**. Every one of its 30 nonfinite-producer probes
  returns DOWN/error; all four finite/lexical/nested controls remain UP.
- New profile tests: **40 official-JS records**, **28 finite/lexical/HOF/
  predicate/group/container/alias/partial/closure/eval controls UP** and **12
  bounded DOWN/error rejections**. Four rejections are also JS DOWN; eight
  deliberately reject JS UP, including finite container depth/visit limits.
  Native callback/alias filter cases cover the shared invocation boundary.
- Pure walker tests cover float64/float32 nonfinite values inside maps,
  slices and JSONataArray, exact/excessive depth and visits, aggregate/shared
  visits, cyclic maps/slices/arrays/mixed graphs, fixed redacted errors, and
  opaque functions/closures without calling or inspecting them.
- The original five numeric downstream regressions still require DOWN/error.
  All original primary/numeric state expectations, finite/boolean/lexical/
  nested controls, advisory timeout, parent timeout/cancellation, output/range/
  depth bounds, OOM termination, privacy and recovery tests pass unchanged.
  The separately documented lexical MaxRange inheritance bug remains visible.
- `go vet -mod=readonly ./apps/node-go/internal/jsonquery/...`: **PASS**.
- `go mod verify`: **all modules verified**. `go list -m` still reports
  x/crypto **v0.58.0**. Module hashes and `config.go` match the pre-repair baseline.
- `go list -deps` confirms this leaf links the **local** evaluator and the
  services-free `internal/probeworker/process` only; no external JSONata package,
  `services`, or `probeworker` wrapper. The copied evaluator uses only standard
  library imports. Its license must be inventoried explicitly as copied source.
- Offline official JS `jsonata@2.2.2` verification: **251 records PASS**.
  The original **177** records remain 158 reference-matching, 16 explicit
  numeric policy cases (six also JS DOWN, ten deliberate JS-UP rejection),
  and three unsupported constructs. All original **34** independent records
  are unchanged, including their successful exists/discard/comparison outcomes.
  The **40** new records are classified separately above. These are fixture
  observations and state comparisons, not complete semantic equivalence.
- Linux **amd64 and arm64**, **CGO_ENABLED=0**, `-mod=readonly -trimpath`
  test-executable builds for both leaf and evaluator tests: **PASS**.
  Build metadata confirms Linux targets and CGO disabled; the linked leaf
  executable has only x/sys beyond the main module. No new Alpine runtime,
  frozen-artifact security check or distribution release is claimed.
- Each copied raw file and LICENSE matches the exact pinned Git blob and
  module-cache bytes. The 114-line unified patch has just the two guard sites
  and new 71-line helper; it applies cleanly and reconstructs all seven final
  production files byte-for-byte with Git automatic CRLF conversion disabled.
  No upstream file was reformatted, global function table modified, modcache
  edited, expression rewritten, arbitrary host binding added or fallback used.

Current artifacts under `%TEMP%\canary-jsonquery-research`:

```text
go-test-local-finite-profile.json
jsonquery-local-finite-linux-amd64.test
jsonquery-local-finite-linux-arm64.test
evaluator-local-finite-linux-amd64.test
evaluator-local-finite-linux-arm64.test
```

Current artifact SHA-256:

```text
go-test-local-finite-profile.json          d82e4a07f8d03c1af19124c5ff582f1067dff2c3c75b0a20815f3225656af9bf
jsonquery-local-finite-linux-amd64.test    6c25713df2c9a4a5ee7a0cc59848d86e2d6312f5a1fe6ea3a57d69abe58cd3dc
jsonquery-local-finite-linux-arm64.test    2294bcd2e64dc4c6256c7e315fe99bb4afea653fca2cffd6fbae438b215f0650
evaluator-local-finite-linux-amd64.test    99069beb28518dee3576043a0418391ef7aaf9c795caddd782ecab38e697427f
evaluator-local-finite-linux-arm64.test    88f1796e9745a941cb9337dbe595a9b0d211cb710e4886216e590745710fd152
```

Frozen source/provenance SHA-256:

```text
SOURCE_FREEZE.json               284a0f42035705a4c128f100ba84f2d400cb475ef751a6203acca7520580d94d
evaluator/upstream.json          fb0d1556997c65666fa79793c0bad439d868b947a613c3065ffb8b37f8447072
evaluator/finite-profile.patch   773af82acabe39bebd151c3feef43a71261fd1a4383ea9be3416b91827085f75
evaluator/finite.go              03d2f4ad804e2bdb18b977b5db8de317ef82e9065d441b67135fa2b6d3bc4f47
evaluator/jsonata.go             4da6def01cea7227fcae3d3b4e11eaf21e08eb886c7e1aa606985b2a5182e1fa
evaluator/functions.go           bb691034fa38698b16a4cf6d537d472324b614336bb5e278dc04213a17e1b15b
evaluator/LICENSE                188e548d24101b7580e507e48e1a71d76e8c8add79d2e0855f3d78555637ff3f
```

Every other code/test/reference hash is in SOURCE_FREEZE.json; raw upstream
hashes are in evaluator/upstream.json. Original references keep their prior
SHA-256 shown in the historical section below. Pre-repair failed logs remain
separate artifacts and have not been replaced by successful observations.
The source freeze is ready for root's independent exact guard/upstream-diff
review. All leaf source ownership is released; module ownership remains root's.

## Historical finite-only conversion and pre-repair independent probes

The following failed-gate evidence predates the authorized local copy. It is
retained unchanged in meaning; the completed repair above supersedes its
release-blocked implementation status.

Only `internal/jsonquery/**` changed. Module ownership remains with root;
`Config`, `Request`, validation, handler shape, process code/limits and module
files were not changed. `compatibleNumber` now checks every numeric return for
NaN or either sign of Infinity, including string conversions and actual numeric
arguments. All five original downstream regressions return bounded DOWN/error.
The selected profile deliberately rejects JS nonfinite intermediates even when
JS can later turn them into finite success.

**Release was blocked at this stage.** Independent arithmetic/aggregate producers bypass
the shim. The new `TestIndependentNonfiniteReleaseGate` fails on eight
confirmed false-UPs, rather than accepting them as compatibility overrides.
The exact scope and a bounded, unapplied vendoring proposal are in
[NONFINITE_REPAIR_PROPOSAL.md](NONFINITE_REPAIR_PROPOSAL.md).

Final checks on Go 1.27.3 windows/amd64:

- Full `go test -mod=readonly -json -count=1 ./apps/node-go/internal/jsonquery`:
  **FAIL**, **5.559 seconds**, solely at the independent release gate. All other
  **14 top-level tests / 212 test-subtest events PASS**, including the five
  explicit original regressions, numeric argument guards, finite controls,
  lexical shadowing, partial application, nested eval, timeout/cancellation,
  OOM and fixed diagnostics. The release gate adds 26 passing and eight failing
  subtests; the package has 238 passing events overall, not a passing suite.
- Independent gate: **34 completed probes**, **23 matching outcomes**,
  **eight false-UPs**, **three safe DOWN differences**, **zero incomplete**.
  False-UPs are `$type` and `$boolean` applied to positive/negative arithmetic
  overflow, overflowed `$sum`, and aggregate NaN. All `$power(10,999)` probes
  reject overflow consistently. Finite/lexical/nested controls pass.
- The initial independent probe run had seven confirmed false-UPs and one
  `pending` observation. It remains in `go-test-independent-nonfinite.json`.
  The full run above followed the addition of explicit incomplete accounting;
  no retry or fallback exists in the code, and the initial event is not hidden.
- `go vet -mod=readonly ./apps/node-go/internal/jsonquery`: **PASS**.
- Pinned offline JS verification: **177** primary/numeric records, classified
  separately as **158 reference-matching**, **16 intentional fail-closed**,
  and **three unsupported-language** cases. Six policy cases also end DOWN in
  JS, while ten reject finite JS success. All five original nonfinite
  regressions are tagged and independently tested. The additional **34**
  independent JS probe records also verify; they are not accepted gap records.
- Linux amd64/arm64 `go test -mod=readonly -c`, **CGO_ENABLED=0**: **PASS**.
  The executables include the failing release gate; successful compilation
  does not mean passing runtime tests or Alpine execution.
- Module SHA-256 and `config.go` SHA-256 match the previous baseline. No new
  dependency, module-cache patch, global registration, fork, text rewriting,
  alternate executable, or outside-package edit was made.

Current artifacts under `%TEMP%\canary-jsonquery-research`:

```text
go-test-independent-nonfinite.json
go-test-failclosed-with-release-gate.json
jsonquery-failclosed-linux-amd64.test
jsonquery-failclosed-linux-arm64.test
```

Current Linux artifact SHA-256:

```text
amd64 5056c5cbc2d063f61803a78a41d8f6c5345a5f099a4823a6b77810ade3ea602a
arm64 bf2276834c2774dd38243107dc229af66b9c5d642305f1a0d4cbb847a200f579
```

Current source/reference SHA-256:

```text
config.go                                  11cd50b6af3bd95a96e92e33c18a5f23fe24d49b1fbe82fc654ac92c3b5615b1
check.go                                   871baca32c7a8138f31c2c5d3f8e3b6e1b8896320ea3de8b95d2caf50ab9c334
number.go                                  2f86d40a2345025c45eb0c9d775873c2a1416db4ea939253c0363306e7bb8338
query_test.go                              24669db4da25cc6cb3c49e65b320fafa635297eaf36a8cbaf86c165bb92ff1dd
independent_probe_test.go                  6c12372d8016e8d6c5b3f14f0957aeb742b9b653d067fbffa74b7c01c60cb769
testdata/fixtures.json                     847ce0b2219715273a5a1d120c8ac3041a8d9ad824c63c8a6a172f6652563c2a
testdata/number-fixtures.json              d68bcd2a4598d62fbead3ed3608495af9593b3485b0182cae55ca93d648ea769
testdata/reference.mjs                     57aab2e6e124d70b0ac421f55fc88f7b0a7d5aeab0f025382cff6f670806b7bb
testdata/independent-reference.mjs         b20188b722879ce793e952a4063faaa7965ba9e04bc48fcb0e29b797ef6b287a
testdata/independent-nonfinite-probes.json ca7e7d319fc3c093f0fb875f9b85e588dfa31cef6db488adb5c25980fb0d4366
```

The unchanged module hashes are recorded in the historical followup below.

## Historical numeric compatibility followup before finite-only rejection

The following 168-case evidence predates the selected finite-only policy and
the independent release gate. It is superseded by the current results above.

This followup edited only `internal/jsonquery/**`. Module ownership remains
with root. `Config`, `Request`, `Validate`, the parent/worker handler API,
and the process limits are unchanged. One fixed pure per-expression `$number`
compatibility function now closes the three original coercion differences.
Its implementation and explicit native-function exception are documented in
[NUMERIC_COMPATIBILITY.md](NUMERIC_COMPATIBILITY.md).

Completed checks on Go 1.27.3 windows/amd64:

- `go test -mod=readonly -json -count=1 ./apps/node-go/internal/jsonquery`:
  **PASS**, **5.431 seconds**, **193 passing test/subtest events**.
- `go vet -mod=readonly ./apps/node-go/internal/jsonquery`: **PASS**.
- Isolated official JS `jsonata@2.2.2` verifier: **PASS**, **168 fixture
  records**, including distinct raw undefined/NaN/Infinity and negative-zero
  probe descriptions. **160 matching condition outcomes; eight recorded
  differences**: three unsupported constructs and five downstream nonfinite
  ordering/`$type` cases. The Infinity `$type` cases can produce a false UP
  relative to JS; this is an explicit remaining compatibility gap.
- Linux amd64 and arm64 test-executable builds with **CGO_ENABLED=0** and
  `-mod=readonly`: **PASS**. Both artifacts include the corrected numeric
  builtin and self-exec worker tests. Build metadata verifies the existing
  JSONata pin and target settings. These are cross-builds, not new Alpine
  execution evidence.
- Root `go.mod`/`go.sum` SHA-256 matched at the beginning and end of the
  followup checks. No dependency changes or module-cache edits were made.
- `config.go` SHA-256 remains unchanged from the initial implementation,
  including all cheap validation and public request fields.

Commands used the existing private JS installation and local Go dependency
cache. The process owner's temporary build break was observed during their
concurrent work and was resolved by that owner before the completed tests.
This followup did not edit the process package, add tracing, retry pending
outcomes, or substitute an executable/environment. The unrelated process-noise
investigation belongs to its original owner. The historical observation below
is retained as historical evidence, not reclassified as a numeric failure.

Current artifacts under `%TEMP%\canary-jsonquery-research`:

```text
go-test-number-followup.json
jsonquery-number-linux-amd64.test
jsonquery-number-linux-arm64.test
```

Current Linux artifact SHA-256:

```text
amd64 7d2c96877cbc63dc012ce9c81344e5f8a744eed515a9b0576ecd7b545b950936
arm64 c2049d80c91b2980b72b1b233d0c3829484b5155a7fe9ac5e55ad71480530d59
```

Current source/reference SHA-256:

```text
config.go                    11cd50b6af3bd95a96e92e33c18a5f23fe24d49b1fbe82fc654ac92c3b5615b1
check.go                     3878baed267dd330a05f465ab7a4ecd6f8743c84b18d0ec3687a28f90bc84dea
number.go                    b60b698e2ac636541207a1e112192a2e4a2f043ac6eb2043983d900101026e0c
query_test.go                0440c1649abe767695835be4ea9f2906de65e1c7dba87a9cddc9b954f1c153c3
testdata/fixtures.json       847ce0b2219715273a5a1d120c8ac3041a8d9ad824c63c8a6a172f6652563c2a
testdata/number-fixtures.json 74a75b2ea9dc88261e701884c20f7bdd93610f99cfc2161f5e082ea9a4e2166a
testdata/reference.mjs       d287b6749ba7cca578453aedb4b26ffb6c5e6d26676b50413e4ced868d354441
```

Unchanged module SHA-256 observed during this followup:

```text
go.mod b3271f37dc901ef630440a94b28f85b6e8d7a04f33d346bee80c34685b63fb2c
go.sum 5ebbff4047a6160d925222849bee9abfda78fa143dd4e02bffff67a8c66cf8a8
```

## Initial leaf evidence before the numeric followup

Scope: `apps/node-go/internal/jsonquery/**` and the new direct dependency plus
its two checksum entries in root `go.mod`/`go.sum`. No root dispatch, service,
check, worker-process, schema, UI, or outside-package documentation edits.
This implementation is outside the frozen protocol-9 artifact. The root's
parallel OAuth2/protocol-10 work is outside this evidence.

Toolchain: portable Go **1.27.3 windows/amd64**. Execution tests use the existing
Windows 128 MiB job controls and private self-exec protocol; limits were not
weakened. No project binaries or npm dependencies were added.

### Initial checks

- Leaf tests: **PASS**, 4.765 seconds, **93 passing test/subtest events**.
  This includes 76 reference cases, strict validation, maximum sizes,
  compilation errors, resource bounds, advisory timeout, parent timeout,
  cancellation, sensitive errors, unavailable host functions, and recovery.
- Existing generic process tests: **PASS**, 2.274 seconds, **21 passing
  test/subtest events**. The final leaf and process runs were serial.
- `go vet` for both packages: **PASS**.
- `go mod verify`: **all modules verified**.
- `go list -m` confirms JSONata pseudo-version
  `v0.0.0-20250709164031-599f35f32e5f` and x/crypto **v0.58.0**.
- Dependency-graph check: no `services` or `probeworker` wrapper import; the
  only probe dependency is `internal/probeworker/process`.
- Official JS **jsonata@2.2.2** reference verification: **PASS**, 76 pinned
  fixture records. **70 matching condition outcomes, six documented Go
  differences**. The Go tests explicitly assert all six differences. Details
  and reproduction commands are in [README.md](README.md).
- Linux **amd64 and arm64**, **CGO_ENABLED=0**, test-executable cross-builds:
  **PASS**. `go version -m` confirms the pin, GOOS/GOARCH, and CGO disabled.
  Test executables include this leaf and its actual private worker path;
  building only the current node executable would not link the unintegrated
  leaf. No Alpine execution is claimed for these new artifacts.

The final test commands were equivalent to:

```text
go test -json -count=1 ./apps/node-go/internal/jsonquery
go test -json -count=1 ./apps/node-go/internal/probeworker/process
go vet ./apps/node-go/internal/jsonquery ./apps/node-go/internal/probeworker/process
go mod verify
```

Test logs, isolated npm reference installation, upstream source checkout, and
cross-built test artifacts reside in
`%TEMP%\canary-jsonquery-research`, outside the project.

Linux test-executable SHA-256:

```text
amd64 da4428b8c3ff0a395b5a2864d3063bd687aa2d0a6c4b345210a877c5b2af7fa2
arm64 2870a64db6622ebf2c00cac9de914a4cc146122ce5e33aa14deaca927d42a4ff
```

Final source/fixture SHA-256:

```text
config.go             11cd50b6af3bd95a96e92e33c18a5f23fe24d49b1fbe82fc654ac92c3b5615b1
check.go              45f895d3fb70281c3f55f27d34b2774690ff654c0a23cd02477642ba1f2ee2bf
query_test.go         816af9363308504deca85a20e71c1a40cbac1a21df770f48ef13ab911b576acc
testdata/fixtures.json 3297b385ff4bc34b6ccb1abd4e65101d32198d8cb82d548238b169a120fdb7c3
testdata/reference.mjs 806ef9abb2f6af6b34da48fa707ebb61f90c5b5ecd98a2b84d14a8bd38484fca
```

### Historical loaded-run observation

Concurrent build/module-verification load intermittently produced
`pending: service worker stopped without a valid result` for simple fixtures.
Observed names included object rejection, boolean aggregation, a small exponent
number, and string formatting. These cases all pass in the final serial suite;
the initial object case also passed 20 isolated repetitions.

Temporary test-only phase tracing investigated this without changing the
process package or production API. Across 14 complete fixture rounds (**1,064
children**), every observed child reached successful typed validation, completed
its check, returned the expected up/down state, and reached the dispatch-return
trace with handled=true and exit code 0. One parent result in those rounds was
nevertheless `pending`. This records the child's intended exit; it does not
independently observe its OS exit status or the parent's `cmd.Wait`/pipe errors.
The public process API deliberately withholds those diagnostics, so the exact
cause remains unresolved. It is not established that JSONata evaluation caused
these failures. The fixed pending outcome preserved containment.

All temporary tracing was removed before the final source hashes, passing tests,
and Linux builds above. The diagnostic executable/phase trace remain only in
the private temporary directory. No retries, alternate executable, environment
override, fallback, or special production mode were added to hide the issue.
The owner of `probeworker/process` should investigate loaded Windows
wait/output handling and test the new leaf on Linux before claiming release
runtime reliability. This observation is separate from the six deterministic
language gaps and the upstream lexical-frame `MaxRange` bug documented in the
README. None of these checks establish full JSONata compliance.

## Root integration still required

Use the exact typed API and operator tokens in [README.md](README.md).
Route `"json-query"` to a fresh `WorkerHandler()` in the existing dynamic private
dispatcher; supply actual JSON documents from the selected transports; preserve
typed expectations; call parent `Check` outside state locks. Generic process
messages already work. More specific diagnostics require a root-owned fixed
allowlist, never arbitrary error/result text. Root release work must include
the dependency notice in the distribution's third-party inventory. No root
integration or protocol change is included in this leaf.
