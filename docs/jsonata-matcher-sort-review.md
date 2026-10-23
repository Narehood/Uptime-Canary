**Independent ROUND2 review: changes required.** Reviewed 2026-10-22 against
the private, byte-checked snapshot described below. The original two P1
reproductions are fixed, and the approved stronger returned-comparator finite
profile works in the executed controls. Four further P1 false-UP paths remain
in the retained upstream matcher/comparator behavior. These findings require
production-owner decisions and repairs; this reviewer has no release authority.

This review added only
[`matcher_sort_review_test.go`](../apps/node-go/internal/jsonquery/matcher_sort_review_test.go)
and this report to the shared checkout. It changed no existing tests,
production code, modules, provenance, reference files, process code, integration,
UI, environment files, or user database. No nested agents were used. All
expressions used synthetic JSON documents, ordinary DSL functions and builtins,
and the existing production self-exec worker with its OS controls. No evaluator
host bindings, controller-process evaluation, package installation, or network
access were needed.

**P1: fractional limits and empty strings can suppress required matcher calls.**

In `evaluator/functions.go`, lines 1232, 1389 and 1714 convert a numeric limit
to `int` before testing its sign or using it in the loop. The reference tests
the original number. A positive limit of 0.5 therefore causes Go to skip the
initial matcher entirely, although JS invokes it. A limit of -0.5 becomes zero
instead of the reference's error. A limit of 1.5 processes one result instead
of two and can skip an error in the second continuation. The new error-aware
`Next` cannot propagate a callback that was never called.

Minimal reproduction, document `{}`, operator `==`, expected JSON number `0`:

```jsonata
$count($match("a", function($s){$error("round2-private-marker")}, 0.5))
```

Guarded Go worker: **UP**, `service check succeeded`. Pinned JS 2.2.2:
**DOWN/error D3137**. Replacing the body with `$boolean(1e308*10)` gives
Go UP versus JS error D1001. `$split` and `$replace` have the same positive
fractional-limit bypass. The new tests retain initial and continuation variants,
all three consumers, negative fractions, and second-stage errors at limit 1.5.
Zero-limit and integer-limit controls remain separate.

Additionally, the `str == ""` returns at lines 1239 and 1720 confuse an empty
string with undefined input. For example:

```jsonata
$count($match("", function($s){$error("round2-private-marker")}))
```

Go is UP for expected `0`; JS throws D3137. The equivalent split case also
fails. Empty-string replace also skips its matcher internally, but its undefined
final result is rejected by the scalar monitor boundary in the executed case;
that case is not claimed as false UP. Preserve numeric limit semantics and the
distinction between undefined and empty input, or reject unsupported requests
before successful comparison. Do not convert skipped failures into empty success.

**P1: partially applied native comparators can convert reference errors into
finite successful sorting.**

Minimal reproduction, document `{}`, operator `==`, expected JSON number `2`:

```jsonata
$sort([2,1], $contains(?,?))[0]
```

Guarded Go worker: **UP**, `service check succeeded`. Pinned JS:
**DOWN/TypeError**, `matcher.apply is not a function`. Two other concise cases:

```jsonata
$sort(["b","a"], $sum(?))[0]
$sort(["b","a"], $power(?,?))[0]
```

Both Go cases are UP for expected `"b"`. JS throws TypeError for the first and
D3061 for the second. These outcomes reproduce through aliases, object property
lookup, a literal lambda that calls the partial, and nested `$eval`: fifteen
false-UP assertions, with valid partial/native comparator controls passing.

`invokeFunction` now guards a lambda's returned value correctly, but the partial
native path has already changed the result. `jsonata.go:3781` passes only
`p.Implementation` into partial application; the wrapper at line 3972 invokes
that implementation directly. Native signature handling is absent on that
path, and the retained implementations coerce/ignore invalid inputs differently
from JS. `$contains` even reaches the matcher path with a numeric token. The
result is a finite boolean/number, so the returned-value guard has nothing to
reject. This is not the approved case where both evaluators succeed and the
profile deliberately rejects an overflow. Preserve the native error semantics
through partial invocation, or fail boundedly; invoking every comparator alone
does not establish that its result is sound.

**P1: replacement callbacks receive a reduced, different match object.**

The callback object constructed at `functions.go:1460` has `match`, `index` and
`groups`. Reference replace passes the original matcher object, which exposes
`start`, `end`, `groups`, `match`, and `next`, and preserves custom matcher fields.
Missing properties can change control flow before the finite guard runs.

Minimal reproduction, document `{}`, operator `==`, expected JSON string `"xb"`:

```jsonata
$replace("ab", /a/, function($m){
  $exists($m.start) ? $error("round2-private-marker") : "x"
})
```

Guarded Go worker: **UP**, `service check succeeded`. JS: **DOWN/error D3137**.
The same result occurs for `end` and `next`, and for a custom property returned
by an ordinary DSL matcher. A branch consuming `$type($sum([1e308,1e308]))`
instead of `$error` gives Go UP versus JS D1001 for all three regular properties.
The invented `index` property is observable too: `$exists($m.index)` is true in
Go and false in the reference replacement callback. These are actual finite
wrong-result/error discrepancies, not requests to inspect opaque captures or
add host bindings. Preserve the callback's original observable match data.

**P1: unmatched optional captures are deleted, shifting capture positions.**

Minimal reproduction, document `{}`, operator `==`, expected JSON string `"b"`:

```jsonata
$match("b", /(a)?(b)/).groups[0]
```

Go: **UP**, result `"b"`. JS: **DOWN**, result undefined. JS's capture array
has two positions, `[undefined, "b"]`; the regex closure at `jsonata.go:2931`
appends only participating groups, producing `["b"]`. The adapter at
`functions.go:1036` also deletes every nonstring group instead of retaining its
position. Exposing groups as a JSON array fixes their representation but does
not repair these contents.

`$count($match("b", /(a)?(b)/).groups)` is Go `1` versus JS `2`.
`$replace("b", /(a)?(b)/, "$1")` is Go `"b"` versus JS `""`;
`$replace("b", /(a)?(b)/, "$2")` is Go `""` versus JS `"b"`.
The new fixtures retain both wrong-result rejection and correct-result controls.
Ordinary captures and a participating empty-string capture pass. Additional
executed custom-matcher observations show numeric groups being dropped and a
fractional `start` being truncated to zero; these are further projection defects,
not separate release claims about unsupported host values. Preserve capture
slots and observable metadata instead of manufacturing finite replacements.

**Other confirmed compatibility differences, distinguished from false UP.**

`$sort(["false","true"], $eval)[0]` and its `$eval(?,?)` comparator form
return JS `"false"` but bounded Go DOWN. A lambda comparator calling
`$eval($a,$b)` passes. The native callback dispatch invokes the placeholder
implementation directly and does not perform `applyInner`'s context-aware eval
fallback (`jsonata.go:3634`). These two finite DOWN differences are preserved
as desired-UP fixtures, not classified as approved finite-profile divergences.

A wrapped partial-native continuation also remains different:

```jsonata
($mk:=function($f){function($s){
  {"match":"a","start":0,"end":1,"groups":[],"next":$f}
}};
 $count($match("ab", $mk($sum(?)))))
```

JS exhausts normally and returns `1`; Go returns bounded DOWN. The explicit
callability flag accepts the wrapped partial, but the retained partial-native
argument collector stops on nil and leaves the placeholder in the bound
arguments. Go sum ignores it and returns zero, which is then a malformed matcher
result. This is a finite compatibility defect in the invocation path, not proof
that the metadata flag itself is wrong. No false UP is claimed for this case.

Raw native and partial objects stored directly as `next` are rejected in both
engines. Literal continuations, normally wrapped native arguments, wrapped
partial identity, closure capture, lookup/nested-eval wrappers and undefined
continuation arguments pass their controls. Tail-call and chain forms that lack
normal argument wrapping retain the reference error. A same-request test also
confirms that wrapping a builtin does not make the raw builtin callable later.
Source inspection confirms private header copies and explicit booleans; no
closure/environment inspection or global function-table mutation was added.
The wrapped partial sum difference prevents a blanket claim that every wrapped
callable now has equivalent execution semantics.

**Finite-profile and exact-diff conclusions.**

The bounded walker is byte-identical to the first profile. Its root-at-zero
depth limit remains 128, rejecting depth 129; its local visit budget remains
65,536 including containers and leaves. Aliases count again; cycles exhaust
the same bounds. It visits only the documented map/slice/JSONataArray shapes,
leaves opaque functions/captures alone, and returns fixed text without formatting
values or invoking callbacks. Both the original pure tests and unchanged
independent walker tests pass, including mixed graphs, alias mutation, aggregate
visits, fresh budgets, cycles and redaction. No new walker defect was identified.

The AST guard still runs before predicate/group consumption. The shared
invocation guard preserves errors and clears rejected successful results.
Initial and recursive matcher adapters and all three consumers now propagate
errors for callbacks they actually execute. The original eighteen matcher
overflow/explicit-error assertions and two native-sort assertions all pass
unchanged. Sort invokes supported comparators through the guarded helper and
uses JS conditional truthiness; the ordinary lambda/native/partial, internal
order-by, empty-array/object truthiness and uncalled singleton controls pass.

Four new overflow controls cover direct partial sum, alias, property access and
a lambda calling the partial. All produce bounded Go DOWN although JS returns
`2`. These are explicit approved stronger-profile outcomes, kept separate from
reference matches. No private marker appeared in public diagnostics. The
remaining false-UP findings concern skipped calls, incorrect finite callback
results or altered match data; they do not establish a failure of the pure
nonfinite walker. Nor do these checks establish coverage of every private
temporary or every HOF/native path.

Raw bytes of all six original production Go files and LICENSE were independently
read from Git commit `599f35f32e5f31297f8b2153b5da44ddbb330e28`, checked against
`upstream.json`, and compared with the cached pinned pseudo-version. All agree.
Applying the supplied patch to a separate raw-source directory with
`core.autocrlf=false` and `core.eol=lf` reconstructs all eight final production
Go files byte for byte. The edits are in `functions.go`, `jsonata.go`, and
`utils.go`; `finite.go` and `callbacks.go` are added. The other three original
Go files and LICENSE are unchanged. The exact diff also contains unrelated
comment-only Unicode encoding changes in functions.go; those have no observed
runtime effect and are not one of the P1 findings. The original MIT license
and source copyright headers remain present. Distribution inventory remains
root-owned.

All four P1 mechanisms are retained upstream behavior visible in the raw source,
not newly introduced guard algorithms. The patch specifically edits or consumes
these paths without closing the concrete cases above. This review therefore
does not attribute all language incompatibilities to the patch, or infer that
the first frozen profile had stronger parity than it did.

The first-profile freeze, upstream manifest and patch retained under
`testdata/history/finite-profile-v1/` are byte-identical to the prior independent
snapshot at `.canary/jsonata-profile-review/run-45a39cf4/source`. Both original
review test files also match that snapshot exactly. The original 251 JS records
still verify unchanged; none was rewritten to accept a failing outcome.

**Executed validation and scope.**

Portable Go **1.27.3 windows/amd64**, executable
`.canary/tools/go/bin/go.exe`; dependency network access disabled with
`GOPROXY=off` and `GOSUMDB=off`. Builds/tests/vet used `-mod=readonly`.
The original working tree has no Git metadata, so preservation was established
with enumerated raw-byte hashes, not a claimed clean Git status.

- Unmodified frozen suite: **505 passing test/subtest events**, zero failures
  or skips: leaf 496, evaluator 9. This independently reproduces the repair
  owner's frozen-suite count.
- Final suite: **544 passing events and 67 failing events**, zero skips.
  Every original 505 event still passes. Of the 105 new worker cases, 39 pass
  and 66 fail; the additional failure event is their enclosing test. There are
  **56 false-UP assertions** and **10 Go-DOWN/JS-UP assertions**. These are
  assertion counts across variants and paired expectations, not 66 independent
  bugs. Of the 56 false-UP assertions, 43 have JS errors, twelve have a different
  finite scalar, and one has JS undefined. There are no pending/incomplete
  cases in this final run.
- The exact new Go-embedded records were compared to the independently executed
  offline JS records. All **105** new records verify: 67 reference DOWN, 38
  reference UP; four of the UP records deliberately require profile DOWN.
  All **408** original/implementation JS records also reverify unchanged:
  177 primary/numeric, 34 independent, 40 finite-profile, 157 matcher/sort.
  No existing verifier used `--record`.
- `go vet -mod=readonly ./apps/node-go/internal/jsonquery/...`: PASS.
  `go mod verify`: all modules verified.
- CGO-disabled Linux amd64 and arm64 `-mod=readonly -trimpath` test-executable
  builds pass for both packages. These are compilation/linkage observations;
  the leaf binaries contain the new failing regressions. No Linux/Alpine
  runtime pass is claimed here.

The final test run was **2026-10-22 16:31:46–16:31:55 America/New_York**
(UTC 20:31:46–20:31:55). The private source was frozen before any experiments
at `%TEMP%\canary-jsonata-matcher-sort-review-08980ad9\source`.
All 309 copied baseline files still match their captured hashes. All 35 frozen
entries and both module files still match the shared checkout. At the
2026-10-22 20:33:56 UTC drift audit, root/other work had changed only four of the
captured shared paths: `internal/canary/api.go`, its availability integration
test, and leaf `NONFINITE_REPAIR_PROPOSAL.md` / `VALIDATION.md`. Their old/new
full hashes are in `final-drift-audit.json`. These changes were not used in the
tests or attributed to this reviewer. Current leaf production/frozen test bytes
have no drift. Snapshot prose, not later prose edits, supplied this review's
initial documentation context.

Root's process-proof and availability/integration reviews are separate. This
review does not certify their current changes, worker availability under load,
Linux kernel enforcement, full application behavior, universal language parity,
universal no-false-UP behavior, packaging, or release readiness. It identified
no additional containment defect in its executed scope. Full frozen-protocol
and Alpine history are not inherited as new evidence.

**Hashes and retained evidence.** All digests below are SHA-256.

```text
SOURCE_FREEZE.json
5d59f53fab22c8836c1dd05e3780d9a1a58a5bc49d2e3b3074d0eccee88c28bf
evaluator/upstream.json
a84bfcbcd64d435a16fa83d505f438b2840f804559fd99314d9cfbbf76333c97
evaluator/finite-profile.patch
dc8faa3849dfe73ad6c872aa61d828705d75c11472b3367561d43c4214eee783
evaluator/finite.go
03d2f4ad804e2bdb18b977b5db8de317ef82e9065d441b67135fa2b6d3bc4f47
evaluator/LICENSE
188e548d24101b7580e507e48e1a71d76e8c8add79d2e0855f3d78555637ff3f
profile_review_test.go (unchanged)
cabd65607e780b73197e84959b681cb70d24033cde4d0a5b59e670e0b066e34c
evaluator/finite_review_test.go (unchanged)
c801a0c52d659c76a8850b62ff434a55c5508b8308d1c96dfcde880342c33e27
matcher_sort_review_test.go (new, 105 stable desired-behavior cases)
22f98416fb31b6394eb8ea12d0947a6501eee9436c49a5a12d30807544d555c0
first-profile SOURCE_FREEZE.json (preserved)
284a0f42035705a4c128f100ba84f2d400cb475ef751a6203acca7520580d94d
first-profile upstream.json (preserved)
fb0d1556997c65666fa79793c0bad439d868b947a613c3065ffb8b37f8447072
first-profile finite-profile.patch (preserved)
773af82acabe39bebd151c3feef43a71261fd1a4383ea9be3416b91827085f75
go.mod (snapshot and current)
2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a
go.sum (snapshot and current)
6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
pinned go.exe
83b7ba66215e696414fa49462d0db2da4f4cf15c7bebd42f0b189fa04866999d
```

The existing offline reference installation is
`%TEMP%\canary-jsonquery-research\node_modules\jsonata`, version 2.2.2.
Its recorded lockfile integrity is
`sha512-XDFH2PuaAXv0AXJEWwElXbqADmKFGKMLMkFb+qOz0EhjQW2EIJ6pgtordLU+4u3IVERyBfqy6bi6kZKEncvRqA==`.
This review used that installation without downloading/reinstalling it; this
is not a new registry authenticity audit. Observed local hashes:

```text
jsonata.js  9c2a35f4d895797f123f65c7f9a4ce16b3a295d209e0f3f225a0c3bae1bd9049
package.json 6366964252a19215b669295fe508479d2dff8def5e97b764017c6809a454fa65
```

Evidence under `%TEMP%\canary-jsonata-matcher-sort-review-08980ad9\` includes
the immutable source, raw/patched upstream directory, complete baseline/final
logs, standalone offline new-case verifier and records, outcome classification,
byte audits, exact raw/first-profile diffs, vet/module logs and Linux binaries.

```text
snapshot-hashes.json
797b12db8a9483e8889c4a5b34776e1d1a9f9b9f304415c3a6f56ad74cf06756
baseline-tests.json
949d1b0e22540ce3eb3b312183e06077925a3cd89b9910420f057a74bde4c18d
final-tests.json
8b040399cd0d876c87e7092de5fa1d61116950ac879edac3510eb4a33cdb3dfc
final-reference-results.json
6e86cbe577a688be4f5357c76a9033764b11c568fdf0865afb86739f776560c8
offline-js-verification.json
70c00f26f595f9e2089144832c94d7057c353941bab1b4e1ed98ebc1985e6d82
case-outcomes.json
7e22e8911e419720264493ea161729721fe792d899893d624f1020915d6fbf76
validation-results.json
bd9439fd253cdb0aa4821c55b9633cca40d79e93ca16c3b2ae1b16114b371fec
jsonquery-linux-amd64.test
aed6f3f69b01d01587080e1a96d91a25dd77bf204923252cc03898d438f62d9d
evaluator-linux-amd64.test
576cb2048ed63b156afc6f8dbbdbccbcb9a6472340e5c466a5a366e4d56952a5
jsonquery-linux-arm64.test
0400f2bbd7bbeac92b9be34fb1115aaa11ff2811e745400bf81bfdba0c8ecd4e
evaluator-linux-arm64.test
b44d9b7e0db793bc0787cf76df1b6a32388900397d89a32da31a5880949d5ecf
```

From the private `source` directory, reproduce the new assertions using the
absolute project portable Go executable:

```text
go test -mod=readonly -count=1 -run TestMatcherSortReviewReference ./apps/node-go/internal/jsonquery
go test -mod=readonly -count=1 ./apps/node-go/internal/jsonquery/...
```

To reverify the new immutable JS observations offline:

```text
node ../final-reference.mjs <existing-jsonata-2.2.2-module-path> --verify
```

No desired-behavior assertion was weakened to match the current failures.
Production remains blocked on the four concrete P1 findings above; the two
earlier findings are closed only within their reproduced scope.
