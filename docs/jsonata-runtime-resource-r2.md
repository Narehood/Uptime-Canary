**Private JSONata candidate R2 resource repair — 2026-10-22; no adoption approval**

R2 is frozen and **fails the focused Alpine gate**. Both direct and Docker
still return DOWN for the supported exact-visit-budget document and maximum
numeric string. Lexical-shadow computed-string success now passes. Windows
tests and native helper improvements therefore do not establish resource fit.
The native guard comparison improved one exact-budget walk by about 11%, with
essentially unchanged total allocation. No C1 rerun, product adoption, assertion
change or limit increase occurred. R2 is stopped pending root's decision about
any separate future candidate.

Everything described below is under
`.canary/jsonata-runtime-candidate/r2-resource-20261022T221741Z/`, abbreviated
`R`. The source froze at **2026-10-22T22:30:12.211490Z**. This new report is the
only shared-tree file written by this R2 task. The earlier C1 source, capture,
archive, binaries, fixtures and `docs/jsonata-runtime-candidate.md` remain frozen.

**Observed C1 baseline.** The completed Alpine direct/Docker suites each covered
all 24 top-level tests, with **820 passing / 12 failing / zero skipped events**,
in 396.138 / 363.652 seconds. Besides the four disclosed legacy assertion
differences, these four unchanged assertions failed:

| Assertion / expression | Required outcome | Direct / Docker observation |
| --- | --- | --- |
| `exact_visit_budget_document`: `$count($)`, document of 65,535 zero elements | UP, scalar 65535; root plus children consumes exactly 65,536 visits | DOWN, 1239.081 / 1252.313 ms |
| `maximum_string`: `$number($pad('0',1048576,'0'))` | UP, scalar 0 | DOWN, 2567.943 / 2454.070 ms |
| `local_shadow_remains_unrestricted_by_builtin_shim`: `($number := function($x){42}; $number($pad('0',1048577,'0')))` | UP, scalar 42 | DOWN, 2328.966 / 2237.857 ms |
| `allocation`: `$length($pad('x',268435456,'x'))` | PENDING, `service worker stopped without a valid result`, then a successful recovery request | DOWN, `service check timed out`, 4007.010 / 4009.380 ms; recovery assertion was not reached |

The diagnostic exact-budget case also returned DOWN at 1259.608 ms. Its 74
samples reached VmData 89188 kB and RSS 27860 kB, with the 128 MiB hard limit
applied. Sampled lower bounds do not establish a failure cause. The separate
production exact-1-MiB check was UP at 1595.366 ms; the controller's original
exit-1 whitespace-sensitive regex result remains intact alongside the derived
assessment. No C1 memory- or CPU-cause claim is inferred from these latencies.
Raw evidence remains under
`.canary/alpine-validation/runs/20261022T220107Z-private-js-candidate/`:
`evidence/results/{alpine-direct,docker-restricted}-jsonquery.log`,
`evidence/diagnostic-results/boundary.log`, and
`evidence/production-results/derived-assessment.json`.

**Actual R2 Alpine failure checkpoint, received 22:36 UTC.** Root's focused
run at `.canary/alpine-validation/runs/20261022T223250Z-r2-resource-gate`
reports the following from the captured R2 artifacts, without assertion overlays:

- Exact visit boundary fails in **both modes**: zero passing / two failing
  events per mode (the unchanged assertion and its parent). Direct latency is
  **1285.643 ms**, outcome DOWN where UP is required.
- Numeric category reports **five passing / two failing events in both modes**.
  The sole failing assertion is `maximum_string`, with its enclosing test also
  failing. Direct latency is **1142.623 ms**, outcome DOWN where UP is required.
- The lexical-shadow computed string now passes at approximately **470 ms**
  directly, versus C1's approximately 2300 ms failing observation. Excessive
  computed string, NaN, Infinity and privacy controls pass.
- Allocation/safety checks are still in progress at this checkpoint. No result
  or subsequent recovery success is claimed for them here. No C1 or full-suite
  rerun was requested by this task.

**Later focused R2 update, received before R3 authorization.** The direct
safety selection finished in **99.379 s**, with **203 passing / five failing
events**. Its three failing assertions are
`TestJSCandidateReviewWorker/range_exact`,
`TestJSCandidateReviewContainerBounds/exact_visits`, and
`TestJSCandidateReviewContainerBounds/aliases_counted_six_times`, plus the two
enclosing tests. Direct allocation returned DOWN at its deadline, approximately
4014 ms; Docker allocation returned the required PENDING and its recovery
check passed. Lexical shadowing passed in both modes. The Docker safety
selection was still running when this update arrived. These additional
supported-success failures reinforce the blocked gate; no frozen R2 source,
assertion, cap or artifact changed. A distinct R3 study was subsequently
authorized; it does not retrospectively certify R2.

**Final R2 safety/production update.** Root subsequently confirmed that both
safety modes fail the same three assertions (`range_exact`, `exact_visits`,
`aliases_counted_six_times`). Its robust production controller also measured
the exact quoted-JSON 1-MiB body as **DOWN**, HTTP 200, **1620.968 ms**, message
`HTTP JSONata evaluation failed or condition not met`. Artifact and body hashes
remained exact. This is an actual failed production control, not a parser/regex
assessment error; the cause remains unproven. Raw output:
`.canary/alpine-validation/runs/20261022T223250Z-r2-resource-gate/production-execution.log`.
The same C1 body had passed at 1595.366 ms. No C1/R2 rerun or source change
followed these findings.

The two failed supported-success boundaries are sufficient to keep the gate
blocked regardless of the remaining focused results. These elapsed times do
not isolate JS initialization, native conversion or individual guard walks.
Root's suggestions about repeated AST/native-boundary traversal and numeric
scanning remain hypotheses requiring trusted-phase measurement in a separately
authorized scope. R2 makes no attribution or follow-up implementation change.

**Exact changes and their limits.** R2 copied and verified every one of C1's
397 files. Only one pre-existing file differs in R2:
`apps/node-go/internal/jsonquery/jsvm/jsonata.js`. Nine additional files contain
two unchanged independent review test files, test-only oracle/benchmark assets,
and the requested license/notice/provenance files. The complete capture contains
406 source files. All existing Go files, public APIs, comparison code, worker
files, fixtures, test files, expected values and module files are byte-identical
to C1. `supplemental-verification.json` separately lists all 21 unchanged files
under `internal/probeworker/`, rather than relying on filename heuristics.

The pad change replaces `(new Array(n+1)).join(char)` with `char.repeat(n)`
**only when `char` is a primitive string, `n` is an integer, and
`0 < n < 4294967295`**. The original surrounding code still supplies the default
separator, truncates signed/fractional widths, computes codepoint padding
length, truncates multicodeunit separators through the original substring
function, and chooses left/right concatenation. For valid Array lengths the
old construction concatenates the separator exactly `n` times, as does repeat.
Nonstring separators and invalid Array lengths retain the original construction
and its coercions/errors. No native-function registration or lexical interception
was added; partial-native and shadowed-function paths retain their original
dispatch. Undefined handling is unchanged.

This targets a concrete construction difference in the pinned engine:
[Goja's repeat implementation](https://github.com/dop251/goja/blob/07c4b9d6265b9d472b736d9a57295b03809ce02b/builtin_string.go)
reserves the final string through its native builder before its copy loop.
This provides an allocation attempt under the existing cap for the 256-MiB
fixture. It is a source-based explanation for testing this path, not proof of
the earlier Alpine failure's cause or of R2's eventual Alpine outcome.

The finite guard now captures trusted native descriptor/key/array/own-property
helpers in its private closure, uses `Object.hasOwn` for descriptor ownership,
inlines primitive/function leaf visits, and uses function-local loop variables.
The pinned engine provides native
[Object.hasOwn](https://github.com/dop251/goja/blob/07c4b9d6265b9d472b736d9a57295b03809ce02b/builtin_object.go).
Each invocation still starts a fresh 65,536-value budget at root depth zero.
Every occurrence, including repeated aliases and sparse-array holes, counts;
objects recurse, with the same maximum depth of 128. Every traversed property
still obtains its own descriptor and rejects accessors without invoking them.
Native functions are opaque; actual JSONata procedure objects remain opaque
only by membership in the private WeakSet at their creation sites. User marker
flags provide no exemption. No mutable-object answer is cached and no captured
environment is inspected. Guard locations before predicate/group consumption
and shared native/callback returns are unchanged, including nested eval and
partials. Exact/excess budgets, mutation, cycles and identity controls pass
against the actual final bundled guard.

The evaluator Go wiring and timer are unchanged: its one-second advisory timer
still starts before library initialization. JSON parsing/evaluation stays in
the isolated worker; the parent never compiles user expressions. The trusted
bundle and wrapper receive expression/document strings as arguments, without
interpolation, Go objects, host functions, source maps, a loader, filesystem or
network capability, or a fallback. The 128 MiB kernel/Job limit, 64 MiB Go soft
heap limit, two slots, fixed dispatcher, EOF, cancellation, parent deadlines,
kill/reap behavior, 1 MiB document/output bound, 4096-byte expression bound,
10,000 range cap, scalar boundary and error sanitization are unchanged. The
earlier stronger partial-sort-overflow DOWN controls remain DOWN. The finite
profile was not changed to terminal-only finiteness.

**Native measurements and executed proof.** Final Go 1.27.3 Windows/amd64 run,
CGO disabled, private offline caches, read-only modules:

| Fixed helper benchmark | C1 | Frozen R2 |
| --- | --- | --- |
| Guard on 65,535 primitive elements | 93.275 ms/op; 41,415,228 B/op; 720,148 allocations | 83.018 ms/op; 41,414,928 B/op; 720,147 allocations |
| Construct 1,048,575 ASCII padding characters | 79.269 ms/op; 13,351,420 B/op; 1,048,539 allocations | 2.728 ms/op; 1,048,903 B/op; 7 allocations |

These short benchmarks describe native execution and total allocation, not
startup, peak heap/RSS, Linux containment or QEMU performance. Earlier guard
samples varied; an exploratory descriptor-batching alternative was slower
and allocated more memory, so it was excluded. The guard's remaining descriptor
allocations remain a material risk at the exact success boundary.

The final two-package Windows run recorded **942 passing / five failing / zero
skipped test events**, with 39.304 seconds for the worker leaf and 2.617 seconds
for JS helper tests. This is not an all-pass result. Category disclosure:

- Actual worker leaf: **900 passing / five failing events**. The failures are
  exactly the four legacy assertions below plus the enclosing primary-fixture
  test. This includes all 130 unchanged R3 cases and the 73 passing independent
  candidate-review events appended unchanged. Every observed stronger profile
  rejection remains a rejection.
- The unchanged reference corpus still has **740 cases**: primary 76, numeric
  101, independent nonfinite 34, finite profile 40, matcher/sort 157, R2 105,
  R6 97 and R3 130. **737 pass / three fail** against existing expectations;
  the standalone legacy range assertion is outside that corpus.
- All four resource assertions pass natively: exact visits 0.18 s, maximum
  computed string 0.25 s, lexical-shadow computed string 0.07 s, allocation
  including its recovery check 0.11 s. Maximum document passes in 0.07 s.
  These are rounded test/subtest durations, not instrumented evaluator timings.
- Unchanged advisory-walltime, parent cancellation/deadline, two-slot admission,
  error privacy, strict decoding, host-binding absence and capacity recovery
  tests pass. The excessive computed-number string still returns DOWN.
- JS helper package: **42 passing events**. Thirty events exercise 29 controls
  extracted from the actual final bundle; one is the independent fixed-engine
  diagnostic; one pad test compares **535 fixed cases** with the unmodified
  official bundle. The remaining ten C1 helper events use the unchanged older
  manually duplicated guard fixture/engine diagnostic and do not certify R2's
  actual guard. Actual-bundle controls cover aliases, mutation, sparse arrays,
  exact/excess depth/visits, accessors, cycles and forged/copied identities.
- The pad parity test covers positive/negative/fractional widths; empty, ASCII,
  multicodepoint and astral strings; undefined/invalid separator types; native
  partials; lexical shadowing; lone surrogates; and invalid Array length. It
  compares values, undefined status and structured errors in pinned Goja with
  unmodified official JS. It runs fixed diagnostic cases, not a containment
  workload. Existing worker tests separately exercise large allocations.
- Old Go evaluator/helper files remain unchanged. Their direct internal tests
  were not part of this two-package run and supply no new-VM certification.
  The retained unused compatibility import is not an evaluator fallback.
- Private leaf/helper `go vet` passes; `go mod verify` reports all modules
  verified. No candidate runtime or package was installed globally.

An extra captured-binary Windows 1-MiB observation passes. One live child sample
reported peak working set 22,679,552 bytes and current private bytes 61,538,304;
the script observation took 482.9 ms including process/polling overhead. This
single sample is a lower bound, not a peak-heap proof. Its raw output is
`windows-max-document-observation.json`.

Native evidence is retained in `full-windows-final.log` and its summary,
`parity-guard-final.log`, `bench-final-supported.log`, `vet-final.log`, and
`mod-verify-final.log`. Earlier exploratory logs are separate. One captured
exploratory benchmark variant, `Descriptors`, predates the final loop-variable
rewrite and is not a valid final harness variant. It was not used for final
proof. Use the explicit supported selector
`-run '^$' -bench '^BenchmarkResource(Guard65535|Pad1MiB)/(C1|R2)$' -benchmem`;
do not use a broad benchmark selector. Frozen source was not edited to clean
up this test-only experiment after handoff.

**Four preserved legacy differences.** Document `{}`, equality comparison:

| Exact expression | Comparison scalar | Frozen required state | Official JS value/state | R2 state | Reason |
| --- | --- | --- | --- | --- | --- |
| `$contains('abc', /a(?=b)/)` | `true` | DOWN | true / UP | UP | JS lookahead capability absent in the previous Go regex engine |
| `missing ?? 'fallback'` | `"fallback"` | DOWN | "fallback" / UP | UP | Official coalescing syntax absent in the old parser |
| `'' ?: 'fallback'` | `"fallback"` | DOWN | "fallback" / UP | UP | Official Elvis syntax absent in the old parser |
| `($n := 10001; $count([1..$n]))` | `10001` | UP | 10001 / UP | DOWN | Enforced 10,000 range limit instead of the documented lexical bypass |

All four assertions and fixtures remain byte-identical. The owner selected
“Enforce range limit; report failures.” Language-gap UPs are disclosed capability
differences, not evidence that finite-profile checks were weakened. Any future
fixture amendment or public adoption belongs to root's independent decision.

**Capture, patch and license provenance.** The official JSONata 2.2.2 npm
artifact is retained unchanged, with commit
`6c7e95fdbf4405a1e741852a7cd8cd985b4305bb`; raw bundle SHA-256
`9c2a35f4d895797f123f65c7f9a4ce16b3a295d209e0f3f225a0c3bae1bd9049`.
`upstream/jsonata-registry.json`, package metadata, archive and upstream source
files are copied from the verified C1 inputs. Goja remains exactly
`v0.0.0-20261022125820-07c4b9d6265b`, full commit
`07c4b9d6265b9d472b736d9a57295b03809ce02b`, sum
`h1:TFm3j5vQYAb+aRIs5fHjoBnUiKm9CPg1qDL83a+WhAs=`. No engine source changed.

| Frozen object | SHA-256 |
| --- | --- |
| Final patched `source/.../jsvm/jsonata.js` | `7ad9abb332b2117f3a84a9c9087cf5455f48acdb4ea89410eadcbc591eda4b11` |
| Raw-official → R2 `jsonata-guard.patch` | `caf3aaf2368f330c7ee23c9dfebee7837c243f98627e728e1ab428dfaf1800a0` |
| C1 → R2 `c1-to-r2.patch` | `f3dd430965f79eb87a7d0bf7c650e8edda52df9a1b8043af8132508201a6e87f` |
| `source-manifest-final.json` (406 files) | `879fda58397a805df81b6c52d94c1bb97db4843055a2a7786b0318d311887bca` |
| `candidate-source.tar.gz` | `d803e3e773839e6129defbafd1cd57c579ee78d926a70847bf8bbd30da720890` |
| `candidate-capture.json` | `dc8848cd895f3c928be7892153d5f247f1fc0691f36e65bb6ecc3a57cdbf9a92` |
| `HANDOFF.json` | `3360a7d06f86abd7d4491c334162397df508346c705e602003d1244b040c05ed` |
| `supplemental-verification.json` | `ba9b837cc42237b55dd98c0a8fb0d6227331d7bd1349dd3c0fa591d6a10e466a` |

Both patches were applied to independent immutable starting bundles with
`git -c core.autocrlf=false apply`; reconstructed bytes match the final bundle.
No source text normalization is required. `capture_r2.py` records this proof.
The first attempt with the machine's default Git CRLF conversion failed the
hash check; conversion was disabled and the check rerun. It did not alter
candidate source or count as semantic verification.

`jsvm/upstream.json` records exactly module `jsonata`, version `2.2.2`, the
40-character upstream commit, and the actual patched bundle hash above.
`jsvm/LICENSE` retains the exact upstream MIT text; `jsvm/NOTICE.txt` identifies
the upstream and declared C1/R2 patches. JSONata, Goja and regexp2 use MIT;
sourcemap uses BSD-2-Clause; x/text and x/sys use BSD-3-Clause; pprof uses
Apache-2.0. Original license copies and individual hashes are in `licenses/`
and the supplemental verification record. Source/release/advisory maintenance
remains necessary; this resource repair does not establish a fresh advisory
clearance or replace root's release checks.

**Build graphs and artifacts.** All binaries use Go 1.27.3,
`CGO_ENABLED=0`, `-trimpath`, and `-ldflags='-s -w'`. Each Linux query/main ELF
has neither PT_INTERP nor PT_DYNAMIC. Windows/amd64 and Linux/amd64/arm64 query
and complete application binaries build successfully. This agent executed
tests on Windows; root's actual Alpine/amd64 checkpoint is reported above.
No arm64 execution is claimed.

The query/helper leaf retains C1's unchanged `source/go.mod` and `go.sum`, using
the 2023 pprof leaf. Complete application builds retain C1's **separate**
`node.go.mod`/`node.go.sum`: MVS selects 2026 pprof but that build-only sidecar
replaces it with verified 2023 pprof. This is disclosed reproduction of C1,
not root's newer proper normal-MVS graph. Root's read-only
`.canary/jsonata-integration-preflight/20261022T215945453878Z/source` uses cached
2026 pprof without a replacement; its reported 815 passes do not certify R2.
Actual linked versions for these artifacts are retained in `*.buildinfo.txt`.
The leaf module hashes are `bdb65c8d0309196b48affc2d7824b3c510fa558560b478f0494336efa7b7761f`
and `3ea58838beddb58c78fad6168cc5b86d98cf220bbaedc572be87df6988d9acae`.
The node sidecar hashes are `285782020274ac397a60a36c87019f3794fe81b1410b70ebf5b16e2fcf1d9286`
and the same sum-file hash. Private caches were copied before use, never shared
for writes with C1, root modules or global caches.

All paths below are relative to `R`; each row is an exact artifact:

| File | Bytes | SHA-256 |
| --- | ---: | --- |
| `jsonquery-linux-amd64.test` | 13,938,848 | `4d15df157c8878be94157b78fcec4eb34ecec4d1629e4aa7ffb665b03a1a7704` |
| `jsonquery-linux-arm64.test` | 13,172,896 | `608cb0430eaa024123b253e1179d230059550bb433d6932bc2270f8d00c0628e` |
| `canary-candidate-linux-amd64` | 53,776,544 | `8c84ecda0be1a13611caaaf2f76b38aa1d7bc3308c7d74bd8f7d105db6b920ac` |
| `canary-candidate-linux-arm64` | 51,118,240 | `1379d55c81e2671c95a6568697b5abde8349da4e80a76bc732eab9edb6c671ad` |
| `jsonquery-windows-amd64.test.exe` | 14,380,032 | `8990eab90104b117f992b64b695566affd26d37ee922891fa0cbb11b2c05947f` |
| `canary-candidate-windows-amd64.exe` | 54,392,320 | `c4bf6f881234276730f4709115c542102286bc930480465061d3e64f57b508c7` |
| `jsvm-linux-amd64.test` (supplemental helper binary) | see artifact | `d9b1ed3ed1df3447b8860ac062dae9775bb7c460691d189d6f3afe222e29efb8` |
| `jsvm-linux-arm64.test` (supplemental helper binary) | see artifact | `6cca3ae72cae66d4a0be3af7d9578920131531859cea55c80faf768f2c266df8` |

**Exact focused Alpine selectors; no overlays.** Run the architecture's query
binary with working directory `R/source/apps/node-go/internal/jsonquery`, so
its original relative fixtures resolve. First run the following independently,
with `-test.v -test.count=1` and the unchanged runtime controls:

```text
-test.run '^TestMatcherProjectionReviewReference$/^finite_controls$/^exact_visit_budget_document$'
-test.run '^TestNumericConversionBoundsAndPrivacy$'
-test.run '^TestResourceBoundsAndRecovery$'
```

The latter two run their entire original categories, including excessive-number
DOWN, lexical isolation, allocation's original stopped-worker message and
subsequent capacity recovery. If these fit, the coherent focused selector also
covers R3, stronger finite/sort controls, 1 MiB, walltime, two slots, cancellation,
privacy, host absence and strict decode:

```text
-test.run '^(TestMatcherProjectionReviewReference|TestNumericConversionBoundsAndPrivacy|TestResourceBoundsAndRecovery|TestMaximumDocumentThroughWorker|TestFiniteIntermediateProfile|TestIndependentNonfiniteReleaseGate|TestProfileReview.*|TestMatcherSortRepairReference|TestRound6RepairReference|TestJSCandidateReview.*|TestAdvisoryEvaluationTimeout|TestParentDeadlineAndCancellation|TestCandidateTwoSlotsAndRecovery|TestSensitiveErrorsStayPrivate|TestNoHostFunctionsOrBindings|TestStrictWorkerDecode)$'
```

The supplemental helper binary, working directory `R/source/.../jsonquery/jsvm`,
accepts this separate selector; it is not a worker-containment substitute:

```text
-test.run '^(TestIndependentCandidateActualGuard|TestIndependentCandidateFixedEngineJSON|TestResourcePadOfficialParity)$'
```

The full unchanged leaf selector remains `-test.run '^Test'`; it must still
report the four disclosed legacy assertion differences. A focused success would
not establish full-suite or production success; the actual focused checkpoint
above already fails. Root owns actual Alpine/main-binary verification; this
task started no VM and makes no new production-run claim. The selectors remain
captured for reproducibility, not an instruction to rerun C1 or a full suite.

All 397 C1 source files, its six captured binaries, capture, handoff and source
archive were rechecked unchanged. All 406 R2 source hashes were rechecked after
the builds. Root `go.mod` and `go.sum` remain respectively
`2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a`
and `6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47`.
No root module, production/shared/UI/process source, old test/assertion,
environment file, user database, default binary, release tool, commit or live
external message was changed. Protocol-11 integration and the frontend remain
root-owned. R2 source/artifacts will remain frozen pending root steering.
