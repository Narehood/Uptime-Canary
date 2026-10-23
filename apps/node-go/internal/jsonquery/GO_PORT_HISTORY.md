# JSONata monitor leaf

This package implements real JSONata compilation and evaluation in the existing
resource-limited private worker. Monitor schemas, HTTP, MQTT, MongoDB, SNMP,
root dispatch, and UI integration belong to the root implementation. This leaf
does not edit those components. This source is outside the frozen protocol-9
validation artifact and does not inherit its test or Alpine runtime claims.

The local finite-intermediate profile closes the eight original false-UP paths
outside `$number`. The followup also closes the independently reviewed matcher
continuation and native-sort findings; all unchanged review assertions pass.
R6 also repairs the four subsequent ROUND2 mechanisms and the allocating length
count, preserving all old tests and references. See [ROUND6_REPAIR.md](ROUND6_REPAIR.md).
It deliberately returns
DOWN/error for some expressions that JS can turn into finite success. Independent
guard/upstream-diff review and root release integration remain separate. See the
[profile contract](FINITE_PROFILE.md) and [historical blocker/repair](NONFINITE_REPAIR_PROPOSAL.md).

## Integration contract

```go
type Operator string // "==", "!=", ">", ">=", "<", "<=", "contains"

type Config struct {
    Expression string          `json:"expression"`
    Operator   Operator        `json:"operator"`
    Expected   json.RawMessage `json:"expected"`
}

type Request struct {
    Type     string          `json:"type"`
    Timeout  time.Duration   `json:"timeout"`
    Query    Config          `json:"query"`
    Document json.RawMessage `json:"document"`
}

func (Config) Validate() error
func (Request) Validate() error
func Check(context.Context, Request) process.Result
func WorkerHandler() process.Handler
func CheckInWorker(context.Context, Request) process.Result
```

`Type` must equal `jsonquery.Type` (`"json-query"`). `Timeout` must be positive
and at most five minutes; its JSON representation is nanoseconds, matching the
existing process header. `Document` is the actual JSON response/message/result,
not a URL, a preselected dotted-path value, or a JSON string containing another
document. Preserve the source document when extracting a MongoDB or SNMP result.
JSON strings, numbers, booleans, null, arrays, and objects are valid documents.

`Config.Validate` checks only shape, type, UTF-8, and size. It accepts malformed
JSONata syntax because compilation belongs to the child. Expression length is
1 through 4096 bytes; whitespace-only expressions are rejected. Use `$` for
identity. `Expected` must be one JSON string, finite number, or boolean, at most
4096 encoded bytes. Null and containers are rejected. `Request.Validate` also
checks valid UTF-8 JSON and a 1 MiB document limit without materializing it.
The evaluator's encoded output has a separate 1 MiB limit.

Call `Check(ctx, Request{...})` after transport I/O, outside `Node.mu` and other
state locks. It validates the bounded data, then delegates to `process.RunConfig`.
Keep the request and its byte slices immutable during the call. Never call
`CheckInWorker` in the parent, including when worker startup or resource control
fails. There is no in-process fallback and no compiled-expression cache.

The root private-mode dispatcher needs to inspect the bounded header and create
a fresh `WorkerHandler()` when its type is `json-query`. Delegate `Validate(b)`
and `Check(ctx)` to that same handler instance within the existing
`process.Dispatch(args, dynamicHandler)` call. Selection, typed decoding and
validation precede `started`; compilation/evaluation follow `started`, only after
OS controls are installed. Do not call `process.Dispatch` a second time or launch
a different executable. This leaf imports only the services-free process package;
it must not import `services` or the `probeworker` wrapper.

The root release integration should also include this package's dependency
notice in its third-party manifest/notices and distribution. Those files are
outside this leaf's edit ownership.

The current process `safeMessage` accepts this type using generic fixed messages:
`service check succeeded` or `service check failed`. It does not expose the leaf's
more specific fixed diagnostics. A root allowlist extension is optional, not a
prerequisite for routing. Never add expression, expected value, document, evaluator
error text, or evaluated value to the frame. Precheck errors are fixed text;
protocol/configuration failures, deadlines, cancellation, resource exhaustion,
and unavailable capacity retain the existing process result semantics. In
particular, worker death/OOM is `pending`, not a normal failed condition.

## Verified Kuma comparisons

The comparison source is [Kuma 2.5.6 `evaluateJsonQuery`][kuma-util] at commit
`7ca4a3debd76443d8452da95d4040200a1479af4`, called from the HTTP/json-query branch
of [`server/model/monitor.js`][kuma-monitor]. Kuma's separate
[`monitor-conditions/operators.js`][kuma-operators] has different coercions and
is not this helper's contract. The local [parity audit][audit] identifies actual
JSONata evaluation as a gap; a JSONPath/dotted-path replacement cannot close it.

Both result and expectation are decoded as typed JSON scalars. Then:

- `==` and `!=` compare scalar text, as Kuma calls `.toString()` on both sides.
  Number `2` equals string `"2"`, but differs from string `"2.0"`; boolean `true`
  equals string `"true"` and differs from number `1`.
- `contains` searches literal scalar text, case sensitively. It is not a regex
  operator. Number `1234` contains number `23`; empty text contains empty text.
  Regex operations remain available inside the JSONata expression.
- `>`, `>=`, `<`, `<=` convert scalar text according to the pinned JS JSONata
  `$number(string)` grammar. Decimal/exponent and supported base-prefixed
  strings work. Empty strings, ordinary surrounding decimal whitespace, leading
  plus, and text `true`/`false` fail conversion. Booleans do not become 1/0 here
  because Kuma stringifies them first. Base-prefix whitespace behavior follows
  the reference's asymmetric regular expression and is covered by fixtures.
- Numeric JSON uses IEEE-754 float64, like JS `Number`; integers above 2^53 can
  round. Negative zero stringifies to `0`. Fixed/exponent number text uses
  ECMAScript thresholds, including `1e-7` and `1e+21`.
- Null and undefined results always fail, including for `!=`. Arrays (including
  singleton/empty arrays), objects, and functions also fail; queries must use an
  index or aggregate to produce a non-null scalar. Undefined and null are
  collapsed to JSON `null` by this Go library's public `Evaluate`; both fail here.

Unlike Kuma's helper, this request requires valid JSON and a nonempty expression;
callers can explicitly JSON-encode a textual transport payload and use `$`.
Expectations must be supported typed scalars rather than arbitrary JS values.
Malformed/non-finite numeric conversions fail. Arbitrary unpaired UTF-16
surrogates cannot be preserved by Go's UTF-8 JSON decoder. These are explicit
boundaries, not claims of complete helper equivalence.

## Pinned evaluator and containment

The evaluator is a bounded local copy in `internal/jsonquery/evaluator` of the
six production Go files from [`github.com/jsonata-go/jsonata`][go-repo] `v206`,
exact commit [`599f35f32e5f31297f8b2153b5da44ddbb330e28`][go-commit], originally
pinned as `v0.0.0-20250709164031-599f35f32e5f`. The wrapper now imports that local
package; it no longer links the external evaluator package. Root owns module
cleanup and explicit third-party notice inventory for this main-module copy.
[upstream.json](evaluator/upstream.json) records module/path/commit, raw per-file
SHA-256, local hashes, the byte-accurate [MIT LICENSE](evaluator/LICENSE), and the
exact [unified patch](evaluator/finite-profile.patch). The patch reconstructs the
local source from raw commit bytes. Three original Go files and LICENSE are
unchanged; `jsonata.go`, `functions.go`, and `utils.go` contain the declared
guard, matcher/error, argument-callability and sort repairs. `finite.go` and
`callbacks.go`, `matchdata.go`, `native.go`, and `length.go` are added production
files. No upstream tests/tools were copied.

At the original pin verification, repository `git ls-remote` HEAD/master and
the [Go module proxy metadata][go-proxy] independently agreed on that commit
and `2025-07-09T16:40:31Z`. No release tags were advertised at verification.
The upstream module requires Go 1.23.0 and suggests toolchain 1.24.2; the copied
v206 production package imports only the standard library. This task changes
neither the project's Go 1.27.3 directive nor module files; x/crypto remains
v0.58.0. Original module archive hashes:

```text
h1:JnGon8QHtmjFPq0NcSu8OTEnQDDEgFME7gtj/NkjCUo=
go.mod h1:rYUEOEiieWXHNCE/eDXV/o5s7jZ2VyUzQKbqVns9pik=
```

The pinned [public API][go-api] exposes `Compile(expr, false)`,
`Expression.Evaluate(document, nil)`, and per-expression `SetMaxDepth`,
`SetMaxTime`, `SetMaxRange`, and `RegisterFunction`. This is a pinned public API, not a promise of future
API stability. The local copy retains that API and the explicit v206 implementation.
The older [Blues library][blues] describes 1.5.4
parity and acknowledges failing suite cases. Neither README's broad parity
language is treated as compliance evidence. Its MIT license and source copyright
notices are retained in [NOTICE.txt](NOTICE.txt) and [evaluator/LICENSE](evaluator/LICENSE).

The local patch checks AST dispatch results before predicate/group consumption
and results of the shared native higher-order invocation helper. It rejects
NaN/Infinity and scans JSON containers with depth 128 and 65,536 visited values
per result. Cycles exhaust the depth budget; aliases count on each visit.
Closures/environments and other opaque engine values are not inspected. Bounds
and nonfinite intermediates return a single fixed redacted internal error.
Matcher continuation errors propagate through both adapters and all three
consumers. Sort invokes native/lambda/partial comparators through the shared
guard, preserves the appropriate native/partial signature semantics and uses
JS conditional truthiness. Typed partial metadata preserves undefined positions
and context-aware eval; original match maps and optional capture slots are kept.
Numeric matcher limits are not truncated before sign/loop checks. Its
partial-native nonfinite results deliberately fail closed. The existing process
boundary still supplies the hard resource controls. See
[FINITE_PROFILE.md](FINITE_PROFILE.md) for the exact scope and deliberate divergences.

All user-provided compilation, nested `$eval` parsing, evaluation, and result serialization
run under the existing worker's 128 MiB kernel memory control, 64 MiB Go soft
limit, one GOMAXPROCS, shared two-slot admission, total parent deadline,
kill/reap, parent-death protection, bounded input/output, and nonce-authenticated
2048-byte status frame. This package does not change any process limits. The
1 MiB document plus bounded expression/expectation fit the runner's conservative
8 MiB serialization budget. Memory failures cannot fall back into the parent.

Per-expression advisory settings are depth 128, range 10,000, and time 1 second
or the remaining parent deadline, whichever is earlier. They do not replace
the process boundary. **Source bug:** `createFrame` in the pinned `jsonata.go`
does not inherit `maxRange`, so a lexical block can bypass `SetMaxRange`.
`TestRangeLimitSourceGap` reproduces this with a harmless 10,001-element range.
Parsing and some native builtins do not universally check depth/time; output
serialization and post-checking also allocate inside the guarded child.
The upstream package also parses one fixed internal function-composition
expression at initialization, including in parents; no user text enters that path.

Pinned production source imports and builtins were inspected for host access.
There are no network, file, environment, or process functions. This wrapper
supplies no external bindings, global registrations, or evaluation callbacks.
There is one fixed whitelisted native compatibility implementation of the
standard `$number` builtin, registered separately on each compiled expression
inside the guarded child. It is pure, accepts only supported scalar types or
undefined, rejects every NaN/Infinity conversion result or numeric argument,
bounds computed numeric strings to 1 MiB, and exposes no host
capability or user-selected native function. See [numeric compatibility](NUMERIC_COMPATIBILITY.md)
for the signature/arity workaround and verified semantics. Input objects are inert JSON data. JSONata lambdas,
local variables, standard builtins, and nested JSONata `$eval` remain available;
`$eval` evaluates JSONata, not host code. Standard `$now`, `$millis`, `$random`,
and `$shuffle` remain available and nondeterministic. They cannot be used as
cross-node agreement values. Global evaluator configuration is never changed;
integration must not enable `jsonata.Debug` or register global functions.
Pinned debug output is guarded by `Debug` (false by default). No evaluator
errors are formatted or logged. The process boundary discards incidental
stdout/stderr and rejects overflow rather than forwarding query/body secrets.

## Differential evidence and checks

The official JS reference is [jsonata 2.2.2][js-package], the version pinned
by Kuma 2.5.6. It is installed only in a private temporary directory with no
project npm dependency or lockfile change. Registry integrity:

```text
sha512-XDFH2PuaAXv0AXJEWwElXbqADmKFGKMLMkFb+qOz0EhjQW2EIJ6pgtordLU+4u3IVERyBfqy6bi6kZKEncvRqA==
```

`testdata/fixtures.json` and `testdata/number-fixtures.json` hold 177 pinned
reference cases. The report separates **158 reference-matching cases**, **16
deliberate fail-closed cases**, and **three unsupported-language cases**. The
original three `$number` coercion mismatches are closed by the fixed
per-expression shim, including computed/higher-order/nested calls.

- JS regex lookahead is rejected by the port's Go RE2 parser.
- `??` and `?:` syntax supported by the newer JS version is absent (two cases).
- The selected finite-only `$number` profile rejects NaN/Infinity at conversion,
  including numeric arguments from other native functions. It intentionally
  rejects ten cases in which unrestricted JS can later produce finite success.
  Six additional policy cases have DOWN outcomes in both implementations and
  are still reported as policy cases, not folded into exact matching. All five
  original downstream nonfinite regressions now return bounded DOWN/error.

Separately, the original 34 records in `testdata/independent-nonfinite-probes.json`
remain unchanged. The repaired gate requires all 30 nonfinite-producer probes
to return bounded DOWN/error, and all four finite/lexical controls to stay UP.
All eight formerly observed false-UPs now return DOWN/error. There are **22
matching state outcomes**, **12 deliberate safe-DOWN differences**, **zero
false-UPs**, and **zero incomplete probes** in the completed run. The pre-repair
failed gate remains historical evidence in `VALIDATION.md`; its unsafe outcomes
were never accepted compatibility overrides.

An additional 40 records in `testdata/finite-profile-fixtures.json` verify 28
finite/lexical/HOF/predicate/group/container/alias/partial/closure/eval UP controls
and 12 required profile rejections. Four rejections also end DOWN in JS; eight
are deliberate safe-DOWN divergences, including depth/visit bounds. These are
state comparisons, not proof that internal values, errors or every evaluation
step match. There is no universal no-false-UP or full-language-compliance claim.

The followup adds 157 cases in `testdata/matcher-sort-fixtures.json`, without
changing any of those 251 existing records or either independent review test.
There are 54 matching UP and 100 matching DOWN/error outcomes; three partial-sort
overflow cases deliberately reject JS UP under the approved stronger profile.
Raw versus argument-wrapped native/partial continuation callability is explicit;
malformed continuations and errors never become partial success. The original
20 failing review assertions and all finite controls now pass. The previous
freeze/patch/provenance and failed artifact hashes remain historical evidence.

The unchanged 105-case ROUND2 review now passes, including its 56 false-UP and
ten finite-DOWN assertions. R6 adds 97 JS records: 73 matching UP, 22 matching
DOWN/error and two already approved safe-DOWN outcomes. The constant-allocation
length helper preserves codepoint counting; its Windows maximum-document pass
and pure allocation checks do not substitute for the pending actual Alpine
rerun. No other finite mismatch was accepted as an intentional policy gap.

Cases cover path projection, predicates, root context, bindings, sorting,
arithmetic, aggregates, grouping, lambdas, map/reduce, ranges, chains, functions,
Unicode, basic regex/match/replace, nested `$eval`, all scalar operators,
coercions, invalid syntax/runtime errors, and non-scalar rejection. The numeric
set also covers boolean conversion, undefined/null/containers, computed values,
context defaults, map callbacks, partial application, lexical shadowing, aliases,
nested `$eval`, precision rounding, whitespace, and nonfinite raw probes. Known gaps
are checked, not skipped. These are observed fixture outcomes, not the full
JSONata conformance suite and not a claim of complete 2.0.6 or 2.2.2 compliance.

Reproduce the JS side from the repository root (PowerShell):

```powershell
$referenceDir = Join-Path $env:TEMP 'canary-jsonquery-reference'
npm.cmd install --prefix $referenceDir --ignore-scripts --no-audit --no-fund --save-exact jsonata@2.2.2
node apps/node-go/internal/jsonquery/testdata/reference.mjs (Join-Path $referenceDir 'node_modules/jsonata')
node apps/node-go/internal/jsonquery/testdata/independent-reference.mjs (Join-Path $referenceDir 'node_modules/jsonata')
node apps/node-go/internal/jsonquery/testdata/finite-profile-reference.mjs (Join-Path $referenceDir 'node_modules/jsonata')
node apps/node-go/internal/jsonquery/testdata/matcher-sort-reference.mjs (Join-Path $referenceDir 'node_modules/jsonata')
node apps/node-go/internal/jsonquery/testdata/round6-reference.mjs (Join-Path $referenceDir 'node_modules/jsonata')
```

The optional `--record` flag explicitly refreshes the golden reference values;
review changes before accepting a different baseline. Ordinary Go tests need
no Node installation, network, credentials, database, or external service. Their
`TestMain` invokes `process.Dispatch` before testing, so actual compilation and
evaluation self-exec exclusively in the guarded private worker. Tests also cover
maximum document/expression/expectation sizes, the native conversion's separate
computed-string bound, strict typed decode, stale-handler
rejection, redacted error paths, unavailable host functions, advisory timeout,
parent timeout/cancellation, range/depth/output limits, OOM child termination,
and fresh successful checks after interrupted/failed workers.
Pure walker tests in `evaluator` additionally cover exact depth/visit limits,
nested shared graphs, cyclic maps/slices/JSONataArray values and opaque functions.
They do not compile or evaluate expressions, register bindings or call functions.

Run with the project's Go tool:

```text
go test -mod=readonly ./apps/node-go/internal/jsonquery/... -count=1
go vet -mod=readonly ./apps/node-go/internal/jsonquery/...
go mod verify
```

CGO-disabled Linux amd64/arm64 test-executable builds exercise linkage of this
leaf and the private worker. Cross-builds alone do not certify Alpine execution.
See `VALIDATION.md` for the completed checks and exact artifact scope.

[kuma-util]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/util.ts
[kuma-monitor]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js
[kuma-operators]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-conditions/operators.js
[audit]: ../../../../docs/parity-audit.md
[go-repo]: https://github.com/jsonata-go/jsonata
[go-commit]: https://github.com/jsonata-go/jsonata/commit/599f35f32e5f31297f8b2153b5da44ddbb330e28
[go-proxy]: https://proxy.golang.org/github.com/jsonata-go/jsonata/@v/v0.0.0-20250709164031-599f35f32e5f.info
[go-api]: https://github.com/jsonata-go/jsonata/blob/599f35f32e5f31297f8b2153b5da44ddbb330e28/v206/API.md
[blues]: https://github.com/blues/jsonata-go/blob/master/README.md
[js-package]: https://www.npmjs.com/package/jsonata/v/2.2.2
