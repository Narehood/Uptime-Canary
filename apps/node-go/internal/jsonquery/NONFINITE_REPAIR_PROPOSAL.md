# Independent nonfinite release blocker and bounded repair proposal

**Status:** root approved and this leaf implemented the bounded local v206 copy.
Code/tests are frozen in [SOURCE_FREEZE.json](SOURCE_FREEZE.json). Both result
guards are implemented; the repaired independent gate passes with all eight
observed false-UPs returning bounded DOWN/error. The failed observations below
are retained historical evidence. See [FINITE_PROFILE.md](FINITE_PROFILE.md)
for the implemented contract, deliberate divergences and remaining scope.

The subsequent independent review found error suppression in matcher
continuations and ignored native sort comparators in that first local copy.
The authorized followup now propagates those errors and routes custom sort
comparators through the guarded helper, with native signature validation and
explicit JS matcher-callability metadata. The unchanged original 20 failing
assertions now pass. The stronger partial-sort nonfinite profile and three
deliberate safe-DOWN cases are documented separately in FINITE_PROFILE.md.
The old freeze/patch/provenance and failed review evidence remain historical
records; no original review test or 251-record reference baseline was changed.

ROUND2 later found four additional matcher/native-partial projection/call
mechanisms. R6 repairs all 105 unchanged desired-behavior cases, retains all
408 pre-R6 JS records, and preserves typed native context/undefined slots and
original matcher data. A separate Linux maximum-document gate motivated the
same-codepoint, nonallocating length count; its old failure remains historical
and actual Alpine confirmation is still root-owned. See
[ROUND6_REPAIR.md](ROUND6_REPAIR.md) and current [VALIDATION.md](VALIDATION.md).
These repairs do not turn earlier platform/test checkpoints into RC signoff.

## Historical blocker before the local copy

The finite-only `$number` shim fixes its five original downstream regressions.
It is not a universal guard: arithmetic and other native functions can produce
NaN/Infinity without calling `$number`. Before the local repair,
`TestIndependentNonfiniteReleaseGate` failed on observed false-UPs;
they were not accepted as golden compatibility gaps.

## Probe scope

`testdata/independent-reference.mjs` uses the existing isolated official
`jsonata@2.2.2` to record/verify 34 synthetic cases. Producers are positive and
negative arithmetic overflow, `$power(10,999)`, overflowed `$sum`, and a sum of
opposite overflowed sums producing NaN. Each is consumed by `$type`, `$boolean`,
`$string`, `$exists`, a comparison, and a block that discards the intermediate.
Four finite arithmetic/aggregate, lexical-shadow, and nested-eval controls are
included. No producer calls `$number`. The Go probe uses only the existing
guarded private worker and reports each state next to its JS reference state.

The first probe run confirmed seven false-UPs and one incomplete `pending`
observation. Confirmed paths include `$type` and `$boolean` consuming positive
arithmetic overflow and overflowed sums, `$type` consuming negative arithmetic
overflow, and `$type`/`$boolean` consuming aggregate NaN. The negative-overflow
boolean probe was incomplete in that run and was not classified or silently
retried. `$power(10,999)` rejected overflow before every consumer in both
implementations. String consumption rejected it in both implementations;
`$exists` and discarded intermediates can legitimately produce UP in JS.
Infinity ordering produced additional safe DOWN differences. Exact final-run
counts and any incomplete observations belong in `VALIDATION.md`.

The subsequent full-suite run, after adding explicit incomplete accounting to
the gate, completed all 34 probes: **eight false-UPs**, **three safe DOWN
differences**, **23 matches**, and **zero incomplete probes**. The eighth
confirmed path is negative arithmetic overflow consumed by `$boolean`. The
initial pending observation remains recorded; there is no retry/fallback in
the test or production runner.

The guarantee before the local repair was only: **the fixed `compatibleNumber`
never returns NaN or Infinity**. This blocks the original five paths, including
the `$type` false-UPs. It did not alter the other producers or consumers above.
That broader evaluator was blocked for release by the independent gate.

## Approved repair, now implemented

Root authorized the following narrowly patched local copy of the pinned
**v206 implementation only**, now implemented in `internal/jsonquery/evaluator`:

1. Copy the six production Go files (`jsonata.go`, `functions.go`, `utils.go`,
   `parser.go`, `signature.go`, `datetime.go`) and its MIT license into a
   dedicated `evaluator` package under `internal/jsonquery`. Record the original commit
   and per-file checksums. Do not copy upstream tests, tools, or version
   selectors. This v206 package is self-contained; the current built artifact
   has no evaluator dependency beyond standard Go packages.
2. Add one fixed `rejectNonfiniteResult(value)` helper and call it at two
   boundaries: immediately after `evaluate`'s AST dispatch succeeds, **before**
   predicate/group processing; and before `functions.go`'s shared
   `invokeFunction` callback helper returns its result. The latter matters
   because native higher-order callbacks bypass normal AST function dispatch.
   These are central result boundaries, not an expanding builtin wrapper list.
3. The helper checks float64/float32 for NaN and both signs of Infinity and
   walks only JSON-shaped maps/slices and `JSONataArray.Data`. The selected
   walk limits are depth **128** and **65,536 visited values** per result;
   reject budget overflow/cycles with a fixed error and record these bounds
   as intentional profile limits. These checks can reject large/deep results
   that unrestricted JS would accept. Never inspect function closures,
   captured environments, arbitrary
   reflected structures, or host values. Use a fixed error without formatting
   its value. The proposed profile deliberately rejects a nonfinite intermediate
   even when unrestricted JS could later turn it into finite success.
4. Import that local package from this leaf. Keep `Config`, `Request`, the
   worker handler, cheap validation, process limits and deadlines unchanged.
   Root continues to own any subsequent dependency cleanup. There is no need
   to edit module metadata or the module cache to build a local package.

Review the helper and the two call sites as an explicit, small patch against
the recorded upstream source. Root granted the source-copy scope before any
copy or patch. [upstream.json](evaluator/upstream.json) records the exact commit,
raw per-file SHA-256, local hashes and patch file. The unified patch reconstructs
the local source byte-for-byte from raw upstream. Module/cache changes are not
part of this repair; module cleanup and notices remain root-owned.

Acceptance is finite-only behavior for all 34 probes, with every previously
false-UP path returning a bounded DOWN/error and the four finite/lexical
controls still UP. All 30 producer probes now return DOWN/error and all four
controls UP: 22 matching state outcomes, 12 deliberate safe-DOWN differences,
zero false-UP/incomplete probes and eight closed regressions. Forty additional
official-JS records cover container, HOF callback, predicate/group, alias, partial
application, closure and nested `$eval` cases. The 177 existing reference
records and all timeout/cancellation/OOM limits remain unchanged. Intentional
fail-closed divergences are reported separately from reference matches. Full leaf
tests, vet, and CGO-disabled Linux builds pass. This is a bounded finite-intermediate
profile, not a claim of complete JSONata conformance or scrutiny of every native
function's private temporary calculations.

## Why not a result-only check or a collection of overrides

Checking only serialized output is too late: `$type`/`$boolean` can consume the
nonfinite value and return a valid JSON scalar. The current final scalar check
therefore cannot catch these false-UPs. Overriding only the next failing builtin
would leave other consumers and higher-order paths exposed.

The pinned API also exposes string-keyed entry/exit instrumentation callbacks
through `Assign`, but its exit callback runs **after** predicate/group handling,
and native higher-order callbacks can bypass AST dispatch. A callback-only guard
has not been proven complete or non-shadowable for this use. It is not used as
an unverified substitute for the two explicit result boundaries above. No
global registration, expression rewriting, arbitrary native bindings, or
in-process fallback is proposed.
