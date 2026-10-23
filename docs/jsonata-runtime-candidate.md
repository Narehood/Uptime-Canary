**Private JSONata JS candidate — 2026-10-22; no adoption or release approval**

The candidate is promising enough for independent source review and actual
Alpine evaluation. It preserves every exercised stronger finite-profile outcome,
passes all 130 R3 cases and the Windows 1 MiB test, and uses the unchanged
existing worker. It does **not** pass every unchanged public assertion: three
historical language-gap expectations and the documented lexical range-bypass
expectation fail. The owner explicitly selected “Enforce range limit; report
failures.” No fixture, expected value, old assertion or production-tree source
was amended to accommodate these results.

Everything is private under
`.canary/jsonata-runtime-candidate/run-20261022T213352Z/` (called `C` below).
`C/source` is the candidate; `C/reference` preserves its original frozen inputs;
`C/control` is a separate unmodified-JS experiment, never a fallback. This new
report is the only shared-tree deliverable. Current protocol-11 hierarchy work,
the frontend, root modules, process implementation, databases, environment
files, default binaries and deployment are outside the candidate.

**Exact capture and preservation.** Input manifest
`f429a3cb2571c0ca2a3087c65e9511c161562277512c84e3a39c07c701b97d88`
was verified against all **391** snapshot files before copying. The exact
current R3 test was appended to the private copies, verified as
`d144f225a61bde7b177b5cddba192673a2f9dbe665f295b0390eeab6f5c73c19`.
No original leaf test or fixture changed. `C/candidate-capture.json`, SHA-256
`46d0ee2a293c08161a1c4a5ea8252598406c904732a337271a907cb10f7f25b1`,
records original-file changes plus individual production/test hashes. Among
the 391 original files, only private `check.go`, `go.mod` and `go.sum` differ.
All frozen process/worker files, config/request definitions, existing tests,
fixtures and the rest of the frozen application remain byte-identical.
The original `SOURCE_FREEZE.json` is historical R6 provenance, not a claim that
the candidate matches the old evaluator.

The public `Check`, `WorkerHandler`, `CheckInWorker`, `Config`, `Request` and
scalar-comparison interfaces remain. `Check` and `WorkerHandler` behavior and
the scalar-comparison implementation remain unchanged; `CheckInWorker` changes
only evaluator wiring. The process still installs its 128 MiB hard control and
64 MiB Go soft limit before evaluation, uses the fixed dispatcher and executable,
admits two slots, enforces EOF/deadlines/kill/reap and emits the same redacted
nonce-authenticated status frame. No process file changed. Root module hashes
remain `2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a`
and `6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47`;
before/after records are retained.

The final full source freeze contains **397 files**. Its canonical manifest is
`C/source-manifest-final.json`, SHA-256
`b5011bd1d65f4e561728c355beb8b1031234ab85fd64914a100380d4ca97a7e7`;
`C/candidate-source.tar.gz` is SHA-256
`1c2858d71f00afaed763e6a2527015a362256736d172718ab2aaffd341de2694`.
No candidate source changed after the earlier capture. `HANDOFF.json` confirms
every frozen worker/process file and root module hash again.

**Declared implementation.** `source/.../jsonquery/jsvm/evaluate.go` loads the
fixed embedded library and trusted bridge only inside the existing worker.
Expression/document text enters as JS primitive arguments; it is never
interpolated into JS source. JSON is parsed in the VM; no Go object or function
is exposed. Source-map loading is disabled. No Node support, external loader,
filesystem/network capability, registered host function, expression rewriting,
alternate executable, runtime selection switch or fallback was introduced.
Promise rejection or an unsettled Promise fails with fixed error text. The
existing `CheckInWorker` discards evaluator errors. Goja interruption and JS
stack/time checks supplement the unchanged parent deadline; native regex work
still requires the process boundary.

The bundle patch declares four changes:

- A pure guard runs immediately after AST dispatch, **before** predicate/group
  consumption, and on shared normal/callback and partial-native return paths.
  It rejects nonfinite numbers, resets each invocation to root depth zero and
  65,536 visits, accepts depth 128, counts aliases repeatedly, and lets cycles
  exhaust the bounded budget. It reads own data descriptors, rejects accessors
  without invoking them, ignores array sequence metadata and never inspects
  captured environments. Native JS functions are opaque. Actual native-header,
  lambda and partial-lambda objects are registered at creation in a private
  WeakSet; user-forgeable marker flags grant no guard exemption.
- Ranges exceeding 10,000 reject. Unlike the old lexical-frame bug, the bound
  remains effective inside lexical blocks and nested eval. Advisory evaluation
  depth/time remain 128/1 second or the earlier remaining deadline. The distinct
  finite-container budget remains 65,536, not a 10,000-element sequence cap.
- `$number` retains the existing finite-only and 1 MiB UTF-8 computed-string
  profile without a Go binding. The pinned Goja's `Number(base-prefixed-string)`
  yields NaN for wide integers. The declared source adjustment uses
  `Number(BigInt(arg))` for the builtin's already-selected base-prefix branch;
  invalid conversions and nonfinite returns still fail closed. Lexical shadowing,
  aliases, partial invocation and nested eval remain covered by unchanged tests.
- The builtin `$length` avoids allocating one array entry per string codepoint.
  A surrogate check provides the common fast path; paired UTF-16 surrogates
  count once. Undefined behavior and the original non-string iterable path are
  retained, preserving array partials and invalid-receiver TypeErrors. It does
  not replace a lexically shadowed function.

No guard is installed through public bindings. The 4096-byte expression bound,
1 MiB document/output bounds, typed expectations and non-null scalar-only result
rule remain. The approved partial-sort overflow still rejects, including aliases
and nested eval. These executed controls are evidence for the declared profile,
not a universal semantic-equivalence or no-false-UP proof.

**Exactly four unchanged assertion differences.** All use document `{}` and
operator `==`. These are not approved stronger finite-profile cases.

- `$contains('abc', /a(?=b)/)`, expected scalar `true`: frozen test expects
  **DOWN** because Go RE2 lacks lookahead; official JS returns **true / UP**;
  candidate returns **UP**. Goja implements this JS regex capability.
- `missing ?? 'fallback'`, expected scalar `"fallback"`: frozen test expects
  **DOWN** because the old parser lacks coalescing; official JS returns
  **"fallback" / UP**; candidate returns **UP**.
- `'' ?: 'fallback'`, expected scalar `"fallback"`: frozen test expects
  **DOWN** because the old parser lacks Elvis syntax; official JS returns
  **"fallback" / UP**; candidate returns **UP**.
- `($n := 10001; $count([1..$n]))`, expected scalar `10001`: frozen
  `TestRangeLimitSourceGap` expects **UP**, exposing the old frame-inheritance
  bug; unrestricted official JS returns **10001 / UP**; candidate returns
  **DOWN**, applying the explicitly selected 10,000 range guard.

The three language results require independent compatibility review before root
changes any historical expectation. The range difference is disclosed tightening
under the owner's stated choice. All four assertions remain in the artifacts.

**Executed tests and category disclosure.** Final Windows Go 1.27.3 run:
`go test -p=1 -json -count=1 ./apps/node-go/internal/jsonquery/...`, with
`CGO_ENABLED=0`, offline private caches and `-mod=readonly`:

- Actual leaf/worker tests: **827 passing test/subtest events, five failing
  events, zero skips**. The five are the four assertions above plus the
  enclosing `TestOfficialReferenceFixtures` event. All actual user DSL tests
  continue to self-exec through the original worker.
- The corpus comprises **740 unchanged cases**: primary 76, numeric 101,
  independent nonfinite 34, finite profile 40, matcher/sort 157, R2 105,
  R6 97 and R3 130. **737 pass and three fail** against their unchanged
  fixture expectations; the standalone range assertion is outside those 740.
  Every R3 case passes, including its explicit profile rejections. No approved
  stronger safe-DOWN case became UP.
- Existing maximum document, computed-number string bounds, lexical shadowing,
  advisory timeout, parent cancellation/deadline, bounded errors, host-function
  absence, allocation failure and subsequent capacity-reuse tests pass. The
  added `TestCandidateTwoSlotsAndRecovery` verifies third-request admission
  remains pending, cancels/reaps two active workers and observes fresh success.
- **16 old evaluator helper events** also pass, unchanged. They exercise the
  retained Go helper package, not the new VM, and provide no new-VM certification.
  `number.go` still imports that package for its now-unused compatibility helper;
  it is linked but never used as an evaluator fallback.
- **10 new JS helper/diagnostic events** pass. These compile fixed trusted JS
  helper programs, not user JSONata, and check exact guard budgets, alias
  accounting, mutation, cycles, marker forgery, accessor non-invocation and
  opaque registered identity. They are separate from worker execution tests.
- Private leaf `go vet` passes; `go mod verify` reports all modules verified.
  Initial cache/setup failures and the corrected private helper-test harness
  runs remain in separate logs; they were not overwritten or counted as passes.

The separate unmodified-bundle control executed all **740** records through its
private worker: 721 matching states, 19 mismatches. Its monitor adapter retains
the scalar boundary, so it is not a raw JS value/conformance oracle for records
that deliberately observe NaN/Infinity. The wide-base `Number()` defect is also
directly reproduced by a fixed engine diagnostic: `Number('0x10000000000000000')`
becomes NaN, while `Number(BigInt(...))` yields the expected finite number.
Complete named mismatches remain in `control-all740.jsonl` / `test-summary.json`.
The unmodified control is not represented as passing reference verification.

**Native resources and platforms.** The Windows maximum-document test passed
through the real unchanged worker. A separate observer sampled only this owned
test executable's worker: sampled peak working set 27,807,744 bytes and private
bytes 61,509,632; the test reported 0.08 seconds. The observation stores its
exact observed binary hash. Samples are lower bounds, not true peaks or Go-heap
measurements. They do not establish Linux data-limit fit or lower usage than
the Go port. Actual Alpine execution remains root-owned. No limit was raised.

CGO-disabled, stripped leaf test executables were built for Windows amd64 and
Linux amd64/arm64. Linux ELF inspection found no interpreter or dynamic segment.
`C/build-results.json` gives exact sizes/hashes; `C/node-build-results.json`
records the separately requested frozen-P10 production-node build results.
Production binaries are build artifacts only and were not started. Artifact
paths and hashes are also listed in `C/HANDOFF.json`.

Linux artifacts under `C` (all SHA-256):

- `jsonquery-linux-amd64.test`:
  `fb5c1281ea28949797dd456928214e0657eee4d01ebd5d657416b3f82ea2f9b9`.
- `jsonquery-linux-arm64.test`:
  `b511d7c4ae7149a6d6877a9ebe75154054291f8e5d18cbdf462105cd94de5880`.
- `canary-candidate-linux-amd64`:
  `fdbcd6a668929c6ea1adba00b7f2c480117f0bd7a00d95a0fea14e3d72183e65`.
- `canary-candidate-linux-arm64`:
  `8bc4077603af427767cb7934988680dbb944247d9d0d0a6d7fc8e465cbf6c0a2`.

Both complete-node Linux artifacts also have no ELF interpreter/dynamic segment.
Windows query/node artifacts and their hashes are recorded in the handoff.

The complete-node build uses `C/node.go.mod` / `node.go.sum`, **not edits to the
captured candidate module files**. Full-application module selection wanted a
newer uncached pprof; the build-only file explicitly replaces it with the same
verified `798e818bf904` source used by the leaf. `C/node-module.diff` records all
module-graph differences. Those build-only dependency choices need independent
review before adoption. All builds used private copied caches, offline, without
modifying the shared cache. No complete production-node runtime or startup-memory
pass is claimed from the leaf-test observations.

**Provenance and licenses.** Sources/caches were reused from the prior verified
private study; no runtime or package was installed globally. Official npm
JSONata 2.2.2 maps to commit
`6c7e95fdbf4405a1e741852a7cd8cd985b4305bb`. Raw bundle SHA-256:
`9c2a35f4d895797f123f65c7f9a4ce16b3a295d209e0f3f225a0c3bae1bd9049`.
Its archive integrity is
`sha512-XDFH2PuaAXv0AXJEWwElXbqADmKFGKMLMkFb+qOz0EhjQW2EIJ6pgtordLU+4u3IVERyBfqy6bi6kZKEncvRqA==`.
The patched bundle SHA-256 is
`6de44a5a504ec8b5a765687ac6e436ca4193faf5fde8675ed8812120d5bfa069`.
`C/jsonata-guard.patch` reconstructs it byte-for-byte using Git with CRLF
conversion disabled; `C/upstream`, `patch.py` and the capture retain provenance.
The patch SHA-256 is
`3289f4a328a0ac9cfdfb7b51cf3b15694f569c871e70ad86221dfbda9c2fd61c`.
This is a declared derived bundle, not a claim of unmodified official semantics.

Goja pin: `v0.0.0-20261022125820-07c4b9d6265b`, full commit
`07c4b9d6265b9d472b736d9a57295b03809ce02b`, module sum
`h1:TFm3j5vQYAb+aRIs5fHjoBnUiKm9CPg1qDL83a+WhAs=`. No engine source
was patched. The leaf's actual external module set is Goja, regexp2/v2 v2.8.1,
sourcemap v2.1.3, pprof `798e818bf904`, x/text v0.43.0 and x/sys v0.49.0.
The private main-module graph retains the frozen application dependencies and
adds four requirements; full files and linked-package records are retained.
JSONata/Goja/regexp2 are MIT, sourcemap BSD-2-Clause, x/text and x/sys
BSD-3-Clause, and pprof Apache-2.0. Exact license copies are in `C/licenses`.
Prior [research](jsonata-runtime-options.md) supplies the official release,
capability and advisory links. No new supply-chain authenticity claim is made
beyond the verified source/cache and retained checksum evidence.

**Exact Alpine selectors; no assertion overlays.** Use the architecture's binary
from `C`, with working directory equal to the captured
`source/apps/node-go/internal/jsonquery` so its unchanged `testdata` is present.
Set `Q` to the copied query-test executable's absolute guest path. Execute:

```sh
# Full suite: expect the four disclosed assertion failures, never call it all-pass.
"$Q" -test.v -test.count=1 -test.timeout=180s

# Maximum supported document.
"$Q" -test.run '^TestMaximumDocumentThroughWorker$' -test.v -test.count=1 -test.timeout=180s

# Finite boundaries and original guard/matcher/sort review.
"$Q" -test.run '^(TestFiniteIntermediateProfile|TestIndependentNonfiniteReleaseGate|TestProfileReviewMatcherContinuation|TestProfileReviewBoundaryControls|TestProfileReviewNativeSort)$' -test.v -test.count=1 -test.timeout=180s

# All repair and R3 cases, including approved stronger returned-sort DOWNs.
"$Q" -test.run '^(TestMatcherProjectionReviewReference|TestMatcherSortReviewReference|TestMatcherSortRepairReference|TestRound6RepairReference)$' -test.v -test.count=1 -test.timeout=180s

# Wall time, two slots, bounded errors, failure recovery, privacy and host exclusion.
"$Q" -test.run '^(TestAdvisoryEvaluationTimeout|TestParentDeadlineAndCancellation|TestResourceBoundsAndRecovery|TestCandidateTwoSlotsAndRecovery|TestSensitiveErrorsStayPrivate|TestNumericConversionBoundsAndPrivacy|TestNoHostFunctionsOrBindings|TestStrictWorkerDecode)$' -test.v -test.count=1 -test.timeout=180s
```

Focused selectors are subsets, not a rewritten full-suite claim. Preserve raw
failure logs and artifact hashes. `C/final-tests.jsonl`, `test-summary.json`,
`candidate-capture.json`, `HANDOFF.json`, build/verification logs, observation,
source copies and patch are the handoff. Candidate source is frozen after this
handoff; independent review, actual Alpine results and any product adoption are
separate root decisions.
