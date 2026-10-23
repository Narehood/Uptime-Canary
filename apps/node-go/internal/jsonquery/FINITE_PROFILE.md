# Local finite-intermediate profile

The authorized local evaluator is `internal/jsonquery/evaluator`, copied from
`github.com/jsonata-go/jsonata/v206` at commit
`599f35f32e5f31297f8b2153b5da44ddbb330e28`. Exactly six upstream production Go
files and the upstream root `LICENSE` were copied. No upstream tests, tools,
version selector, global table changes, module-cache patch or new dependency
is involved. Root owns module cleanup and distribution notices.

[upstream.json](evaluator/upstream.json) records the module, path, exact commit,
raw per-file SHA-256, final source hashes and [patch](evaluator/finite-profile.patch).
Raw Git blobs were independently compared to the pinned module-cache source
before copying. Three production files and LICENSE remain byte-for-byte original.
The edited files are `jsonata.go`, `functions.go`, and `utils.go`; the latter now
records explicit matcher callability metadata. Added production files are
`finite.go`, `callbacks.go`, `matchdata.go`, `native.go`, and `length.go`.
No closure or environment inspection is involved.
Reapplying the unified patch to
raw upstream source reconstructs every final production file byte-for-byte;
on Windows use `git -c core.autocrlf=false -c core.eol=lf apply` to preserve LF.

## Guard contract

One fixed, pure `rejectNonfiniteResult` walker runs at two result boundaries:

1. After successful AST dispatch in `evaluate`, before its predicate/group
   processing can consume or discard the result.
2. Before successful return from `functions.go`'s shared `invokeFunction`
   helper. Native higher-order callbacks can bypass the normal AST call path;
   `$filter([[1e308,1e308]], $sum)` needs this guard before its callback result
   is consumed as a boolean. Aliases use the same helper.

The second call is a named-return/defer check covering the existing return
branches. It preserves upstream errors, checks only successful results, and
clears the returned value on a profile failure. No evaluator callbacks, arbitrary
native functions, instrumentation bindings or expression rewriting are added.
The fixed per-expression `$number` compatibility shim remains the sole registered
native exception; it rejects its own nonfinite outputs and numeric arguments.

## Matcher and sort repair after independent review

The first frozen patch still let matcher continuations discard invocation
errors and let sort ignore native comparators. The independent report is retained
at `docs/jsonata-profile-review.md`; its tests and 251 original JS reference
records are unchanged. Their original 20 failing assertions now pass. The old
freeze/provenance/patch are retained in `testdata/history/finite-profile-v1/`;
the old failed review artifacts and hashes remain documented in VALIDATION.md.

`MatchResult.Next` is now `func() (*MatchResult, error)` in this local vendor
type. Both initial and recursive map results use one adapter. Native regex
continuation errors, DSL invocation errors, nonfinite rejections and malformed
continuation results propagate through that adapter and all three consumers:
`$match`, `$split`, `$replace`. Only an actual undefined result ends the stream
normally. Missing/noncallable next functions and wrong result contracts fail;
errors never become successful partial output. The existing metadata check
follows the reference's start/end/groups/next alternatives. The capture groups
exposed by `$match` now use the evaluator's JSON array representation, rather
than a Go-only `[]string`. Replace no longer evaluates its matcher twice in a
separate zero-width preflight; normal continuation errors enforce that contract.

Reference `evaluateMatcher(next)` supplies one undefined string argument. The
DSL/native-wrapper path preserves that argument; fixed Go regex continuations
use their internal zero-argument signature. A second reference distinction also
matters: raw builtin/partial objects stored directly as `next` lack JS `.apply`,
while literal lambdas and normal expression argument wrappers have it. Explicit
private booleans record this distinction at lambda creation and argument
wrapping. Wrapping copies only a function header, preserving captured state and
signatures without mutating global values. The matcher reads the boolean, never
function code or captured environments. Both raw-error and wrapped-finite cases
are tested; accepting every Go-callable value would introduce another false UP.

All custom sort comparators now receive their two arguments through the same
guarded `invokeFunction`. Supported internal order-by Go closures still work;
unsupported comparators fail rather than behaving as always false. Native
function signatures are validated on ordinary invocation, preserving type/arity
errors and undefined substitution. Native partials retain their full header,
but omit the outer validator only for their reference implementation-call path;
the tested underlying error/coercion semantics are repaired in R6. Comparator results use the reference JS
conditional truthiness, not `$boolean`: null/undefined/zero/empty text are false;
empty arrays/objects and functions are true. Invocation errors always propagate.

Root also approved a stronger profile for returned sort callback values:
`$count($sort([[1e308,1e308],[1]],$sum(?)))` now returns bounded DOWN/error even
though pinned JS returns 2. Alias and nested-eval forms do the same. These three
cases are explicitly classified as deliberate safe-DOWN divergences, not exact
reference matches. This repairs a concrete bypass without claiming every HOF
or every native/private temporary is guarded.

The followup adds 157 separately recorded JS cases: 54 matching UP outcomes,
100 matching DOWN/error outcomes and three deliberate safe-DOWN outcomes. They
cover both continuation stages, malformed callbacks, private errors, aliases,
nested eval, HOF/closures, capture groups, ordinary regexes, zero-width behavior,
native/lambda/partial comparators, their errors, and JS truthiness. All actual
expression evaluation stays in the private production worker.

ROUND2 identified four further mechanisms in those retained paths: fractional/
empty inputs skipping callbacks, incorrect partial-native semantics/context,
reduced replacement objects, and shifted optional captures. R6 repairs all 105
unchanged review cases and adds 97 own JS records. The approved returned-sort
overflow policy remains separate. It also removes the allocating length count
implicated in the separately reported Linux maximum-document failure. Actual
Alpine confirmation remains root-owned. See [ROUND6_REPAIR.md](ROUND6_REPAIR.md)
for the precise contract, scope and remaining limits.

## Bounded walker

The walker rejects float64/float32 NaN and either sign of Infinity. It traverses
only `map[string]interface{}`, `[]interface{}`, and `JSONataArray.Data` (pointer
or value). Scalar leaves and opaque engine values are not traversed. It does
not inspect functions, captured lexical environments, arbitrary reflected
structures or host values, and never calls a function. There is no reflection
or pointer identity table in the guard. The wrapper accepts no host bindings.

Each invocation starts with independent limits:

| Limit | Contract |
| --- | --- |
| Depth | Root depth 0; depth 128 is accepted, depth 129 rejected. |
| Visited values | At most 65,536, including root, containers and leaves. |
| Container accounting | A JSONataArray and its Data count as one container; map keys and sequence flags are not visited. |
| Aliases | Every traversal counts again; shared graphs receive no identity-cache exemption. |
| Cycles | Exhaust the same depth/visit budget, at most 129 edges on any path. |

Container lengths are checked against remaining visits before iteration;
aggregate nested visits also consume the same budget. This bounds work without
unbounded cycle bookkeeping or skipping aliased data after mutation. Map
iteration order cannot change acceptance: a fully traversed finite graph within
the budgets passes, while any encountered nonfinite value or exhausted budget
returns the same error. No values, keys, source text or function names appear in
the fixed internal error: `JSON query result rejected by finite profile`.
The leaf discards all evaluator errors and the process emits fixed diagnostics.

## Deliberate safe-DOWN divergences and remaining scope

The profile rejects nonfinite intermediates before a later expression can turn
them into success. For example JS can return UP for `$exists(1e308*10)`,
`(1e308*10; 'ok')`, overflow ordering, or a predicate/group that discards or
replaces the intermediate. The profile returns bounded DOWN/error. Large or
deep finite containers can also be rejected before a finite `$count`/`$exists`
consumes them. These are explicit resource/semantic limits, never exact matches.

This guards the two specified returned-value boundaries, not every private
temporary inside every builtin/parser/comparator or opaque function capture.
Some internal calculations can occur before either boundary and may produce
finite results. The fixture set is not an exhaustive language/conformance or
universal no-false-UP proof. No silent fork expansion or additional builtin
wrapper list is claimed. Known lookahead, `??`, `?:` language gaps and the
upstream lexical-frame MaxRange inheritance bug remain documented.

The public request/API, cheap shape validation, typed scalar comparisons and
strict child-only user-expression compile/evaluation rule are unchanged. Upstream
package initialization still parses its fixed internal composition expression;
that constant parsing can occur in the parent and never receives user text.
The 128 MiB kernel
cap, 64 MiB soft memory limit, two slots, deadlines, kill/reap, input/output
bounds and fixed nonce frame are unchanged. Advisory evaluator limits remain
supplementary. No fallback, alternate executable, environment override, logging,
external traffic, file/network binding or global registration is added.

## Evidence and review handoff

The original 177 reference records and original 34 independent JS records remain
unchanged. The latter still record JS success for exists/discard/comparison
consumption; the gate separately requires profile DOWN/error for every one of
its 30 nonfinite-producer cases and UP for all four finite controls. The eight
pre-repair false-UP names are explicitly required to remain covered and close.
The completed repaired gate has 22 matching state outcomes, 12 deliberate safe-DOWN
differences, zero false-UPs, zero incomplete probes and eight closed regressions.
The earlier failed gate remains in the historical validation record.

Forty new official-JS records cover 28 finite, lexical, HOF, predicate, group,
container, alias, partial, closure and nested-eval UP controls, plus 12 required
profile rejections: four reference-DOWN and eight deliberate safe-DOWN cases.
Native `$filter` callback/alias probes exercise the second boundary; predicates
and grouping exercise the pre-consumption boundary. Pure helper tests separately
test nested nonfinite containers, exact and excessive budgets, shared graphs,
cycles and opaque closures without any evaluator host binding. All actual
compilation/evaluation tests still run through the production self-exec worker.

[SOURCE_FREEZE.json](SOURCE_FREEZE.json) freezes code, tests, provenance, patch,
license and reference records for root's independent review. Package prose can
be updated with validation results without changing this source snapshot. Root
arranges the new independent guard/upstream-diff review and release integration.
Full validation and artifact hashes are in [VALIDATION.md](VALIDATION.md).
