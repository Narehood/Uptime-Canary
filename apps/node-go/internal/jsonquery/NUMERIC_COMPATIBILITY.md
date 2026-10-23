# Fixed per-expression finite-only numeric compatibility

The pinned Go evaluator's standard `$number` incorrectly accepts `+2` and `.5`
and rejects valid large base-prefixed numbers beyond int64. This leaf corrects
those three cases without rewriting expressions, registering anything globally,
or accepting host bindings. The separate [finite-intermediate profile](FINITE_PROFILE.md)
now uses a narrowly patched local evaluator copy; the numeric shim itself keeps
the same fixed per-expression registration and conversion contract.

`CheckInWorker` compiles the original expression and calls:

```go
e.RegisterFunction("number", compatibleNumber, "<x-:n>")
```

This happens only inside the existing guarded private child. It changes the
standard builtin in that expression's environment, not the global function
table. `Evaluate(document, nil)` continues to receive no supplied bindings.
Every request gets a fresh expression. The request fields, `Config.Validate`,
handler contract, deadlines, admission, and OS limits are unchanged. No module
files or process-package source were changed for this followup.

## Why this signature

The JS reference registers `$number` with `<(nsb)-:n>`: one number/string/boolean
argument, defaulting to the current context when omitted. The pinned Go
`defineFunctionWithError` counts all three type letters inside the union as
three parameters when calculating callback arity. Registering that exact
signature therefore causes `$map(..., $number)` to pass value, index and array
to a function that should receive only its value.

The fixed `<x-:n>` signature preserves arity one, context substitution, and
extra-argument rejection. The implementation itself accepts only number,
string, boolean, or undefined; it rejects null, arrays, objects, and functions.
No additional native functions or public configuration options are exposed.
Internal error codes for invalid context types can differ from JS's T0411,
because the wrapper does the type check after context substitution. The public
monitor boundary returns fixed failure diagnostics, never evaluator error codes,
argument values, or raw error text.

## Conversion contract

The official reference used here is the already isolated, pinned
`jsonata@2.2.2` installation and its `number` implementation. No external
requests or package installation are needed to reproduce this followup.

- Undefined propagates as undefined. Explicit null and containers fail.
- Boolean `true` becomes 1 and `false` becomes 0. This is the actual builtin
  contract, unlike the outer Kuma monitor helper, which stringifies booleans
  before numeric comparison and therefore rejects them for ordering.
- Finite numeric arguments preserve their numeric value. Native Go integer results
  are normalized to float64, matching the reference's JS Number model.
  Signed zero is retained internally; monitor scalar text normalizes it to 0.
  NaN and both signs of Infinity are rejected with a fixed error, including
  actual numeric arguments produced independently by other native functions.
- Decimal strings follow the reference grammar, including leading zeroes and
  exponents. Leading plus, leading/trailing decimal dots, decimal whitespace,
  `NaN`, `Infinity`, and decimal overflow fail. Underflow to zero is allowed.
- Hexadecimal, octal, and binary strings use arbitrary-width parsing followed
  by float64 rounding, rather than overflowing an int64 parser. The existing
  verified conversion code is shared with the outer comparator. The reference's
  asymmetric base-prefix regexp and ECMAScript whitespace set are preserved.
- The reference base-prefix regexp accepts some malformed text for which
  JS Number returns NaN; sufficiently large valid base-prefixed strings can
  become Infinity. The shared parser represents those values internally, but
  the shim now rejects them before returning. Every numeric return, whether
  parsed from a string or received as a numeric argument, passes the same
  finite check. The outer comparator continues to reject nonfinite values too.
- Computed numeric strings are limited to **1 MiB**, even if the expression
  builds them from a small document. Larger strings fail with a fixed error.
  This explicit resource bound can reject strings accepted by unrestricted JS.
  All parsing and allocation still run under the unchanged worker limits.

Errors created by the shim contain only a fixed code and argument index. They
contain no numeric text or document values. The shim has no I/O, environment
access, external callbacks, or mutable global state.

## Expression semantics and evidence

The correction acts on evaluated arguments, including values computed from the
document, `$join`, `$substring`, arithmetic, map callbacks, and partial
application. Function aliases and closures capture it through the normal
environment. Nested `$eval` uses the current environment and therefore sees
the corrected builtin. Local variables and lambda parameters named `$number`
still shadow it, including inside `$eval`. Expressions, string literals,
variable names, and regex literals are never text-replaced.

There are **101 numeric reference cases** in `testdata/number-fixtures.json`,
in addition to the original 76 cases. They verify the three corrections,
the cases above, and preserved lexical/nested-evaluation behavior. Raw probe
metadata records undefined, NaN and Infinity distinctly; it does not collapse
them to JSON null. The negative-zero baseline is also explicit. Go tests
evaluate the condition expressions only through `process.Dispatch` self-exec.
Additional worker tests exercise the maximum/excessive computed-string sizes,
redacted numeric errors, final nonfinite rejection, and request isolation.

The **177** cases are classified as **158 reference-matching cases**, **16
deliberate fail-closed cases**, and **three unsupported constructs** (lookahead,
`??`, `?:`). Of the 16 policy cases, six also end DOWN in JS and ten would
produce finite success in JS. They are kept separate in the report even when
the final state agrees. Policy records must require DOWN/error; no known
false-UP can be accepted as a golden override.

All five original downstream nonfinite cases now return a bounded DOWN/error:
two Infinity ordering cases, two `$type(Infinity)` cases, and `$type(NaN)`.
`TestNonfiniteFailClosedRegressions` enforces those five outcomes independently
of golden overrides and includes ordinary finite, boolean, lexical-shadow and
nested-eval UP controls. Other cases cover numeric arguments produced by
overflowed sums, aliases, native callbacks, and discarded intermediates.

This is the explicitly selected **finite-only `$number` profile**. It diverges
from JS intermediate nonfinite semantics and can reject an expression that JS
would later transform into a valid scalar. The guarantee is scoped to values
passing through this fixed builtin; lexical shadowing remains ordinary JSONata
behavior, and other producers do not pass through the shim.

## Historical independent producer blocker and current repair

The additional `TestIndependentNonfiniteReleaseGate` compares 34 pinned JS
probes that produce nonfinite values without calling `$number`. The pre-repair
run confirmed **eight false-UPs**, **three other DOWN differences**, and **23
matching outcomes**, with no incomplete probes. `$type` and `$boolean` can
consume arithmetic/aggregate nonfinite values and return UP where JS returns
DOWN/error. `$power(10,999)` consistently rejects overflow before consumption.
The four finite/lexical/nested controls remain UP.

Those observations failed the gate and were never accepted compatibility gaps.
The authorized local copy now guards AST results before predicate/group handling
and the shared higher-order invocation result. All eight paths return bounded
DOWN/error. The unchanged 34 reference records now yield 22 matching outcomes,
12 deliberate safe-DOWN differences and zero false-UP/incomplete probes. The
profile deliberately rejects independent nonfinite intermediates even when JS
can consume/discard them successfully; it also bounds container walks. See
[FINITE_PROFILE.md](FINITE_PROFILE.md) for scope and limits, and
[NONFINITE_REPAIR_PROPOSAL.md](NONFINITE_REPAIR_PROPOSAL.md) for the retained
failed-gate history. No universal no-false-UP claim is made. The upstream lexical
`MaxRange` bug and unrelated process-owner investigation remain separate. This
is not complete JSONata compliance.
