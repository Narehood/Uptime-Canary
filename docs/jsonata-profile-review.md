**Independent review: changes required.** Reviewed 2026-10-22 against the frozen
JSONata leaf. Two confirmed false-UP paths remain. Both derive from upstream
behavior retained by the small patch; neither is an objection to the selected
profile's deliberate safe-DOWN divergences. Production corrections belong to
the root owner. This review changes no production source, module metadata,
existing tests, fixtures, manifests, process code, or UI.

**P1: matcher continuations suppress errors, including finite-profile rejection.**
In `apps/node-go/internal/jsonquery/evaluator/functions.go`, the continuation
closures around lines 1023 and 1173 call `invokeFunction`, then return `nil` when
its error is non-nil. The equivalent native continuation branches around lines
1011 and 1162 also suppress errors. `MatchResult.Next` returns only a result
pointer, so callers cannot distinguish failure from normal exhaustion.
`$match`, `$split`, and `$replace` consequently return successful partial output.
The new finite guard rejects the bad result correctly; its caller discards that
rejection and the final finite scalar passes the monitor comparison.

Minimal reproduction, document `{}`, operator `==`, expected JSON number `1`:

```jsonata
$count($match("a", function($s){
  {"match":"a", "start":0, "end":1, "groups":[],
   "next":function(){ $boolean(1e308*10) }}
}))
```

Executed through the actual guarded Go worker: **UP**, `service check succeeded`.
Executed with the existing local official `jsonata@2.2.2`: **DOWN/error D1001**.
Replacing the continuation body with `$type($sum([1e308,1e308]))` produces the
same discrepancy. `$error('review-private-marker')` also becomes Go UP, whereas
JS throws D3137. These are errors being converted into success, not JS-success
expressions deliberately rejected by the profile.

The new `TestProfileReviewMatcherContinuation` has 24 worker cases. Eighteen
fail on the frozen source: three error bodies, three consumers, and two
continuation stages. The second stage exercises `convertMapToMatchResult`,
not just the initial `evaluateMatcher` adapter. Six finite exhaustion controls
pass. Desired behavior: propagate continuation failures through every adapter
and consumer to bounded DOWN/error, while retaining ordinary exhausted and
multi-match results. The private marker must never enter public diagnostics.
Merely adding another result scan cannot fix an already discarded error.

**P1: `$sort` silently ignores native function comparators and produces finite
false UP.** In `functions.go` around lines 3969-4001, custom comparator dispatch
handles a Go comparator closure and `*Lambda`; it has no `*Function` branch or
unsupported-callable error. A normal builtin such as `$contains` is a `*Function`.
The merge therefore leaves `shouldSwap` false and preserves the original order.

Reproduction, document `{}`, operator `==`:

```jsonata
$sort(["ab","a"], $contains)[0]
```

Pinned JS evaluates this to `"a"`; Go behaves as `"ab"`. With expected `"ab"`, Go
is **UP** and JS is **DOWN**. With expected `"a"`, Go is DOWN and JS is UP. This
uses a boolean-returning comparator with two valid string arguments. All values
are finite and small. This is outside the declared
nonfinite/depth/visit policy and the documented lookahead/`??`/`?:` gaps.
The two corresponding assertions in `TestProfileReviewNativeSort` fail; its
ordinary lambda comparator control passes. An earlier independent probe also
confirmed `$sort([2,1],$power)[0]` returns Go `2` versus JS `1`.
`$power(?,?)`, represented as a lambda, works in the additional controls.
Desired behavior: invoke valid
native comparators, honor callback errors, and consume their actual result.
Do not silently accept an unsupported callable as an always-false comparator.

**Additional executed scope observation, not a third release blocker under the
documented narrow contract.**

```jsonata
$count($sort([[1e308,1e308],[1]], $sum(?)))
```

Both pinned JS and the frozen Go worker return `2` (UP for expected `2`). Source
inspection explains why an overflowed callback result escapes this guard:
`sortFunc` directly calls `lambda.apply` around line 3976; the partially applied
native body returns through `applyNativeFunction` and `applyFunction` without
an AST-dispatch or shared `invokeFunction` check. Sort consumes the Infinity
as truthy, then returns an entirely finite array. This is a concrete limit on
callback coverage, not evidence of Go UP versus JS DOWN. `FINITE_PROFILE.md`
explicitly excludes some comparator/private temporaries and does not claim
universal finite-intermediate coverage. Do not describe the two-site repair as
covering every HOF callback. If root chooses that stronger contract, this case
must become a DOWN regression; that is a separate scope decision. It is not
included as a failing release assertion here, and no existing golden was changed.

**Freeze, provenance, and patch verification executed before probing.**

- `SOURCE_FREEZE.json` SHA-256 matched the supplied
  `284a0f42035705a4c128f100ba84f2d400cb475ef751a6203acca7520580d94d`.
  All 24 explicitly listed file hashes matched before copying and at the final
  audit, in both the checkout and private snapshot. The manifest's scope string
  is broader than its enumerated hashes: prose and the two new review test files
  are not among those 24 frozen entries.
- The coherent private source is
  `.canary/jsonata-profile-review/run-45a39cf4/source`. All executions and builds
  used that copy. Original production files were never edited. Dependency
  downloads were disabled with `GOPROXY=off` and `GOSUMDB=off`.
- Read-only verification compared all six original production Go files and
  LICENSE in the cached pseudo-version against exact Git blobs from local
  commit `599f35f32e5f31297f8b2153b5da44ddbb330e28`. Every raw SHA-256 agrees with
  `evaluator/upstream.json`; Git blob bytes and cached bytes agree.
- Applied `finite-profile.patch` to a separate raw-source directory with
  `core.autocrlf=false` and `core.eol=lf`. It reconstructed all seven local
  production files byte for byte. Four original Go files and LICENSE are
  unchanged. The only upstream changes are the AST result check and the
  deferred invocation-result check; `finite.go` is the sole added production
  file. No global function-table change, expression rewrite, or cache edit
  is present in this patch.
- The retained MIT license is byte-identical, SHA-256
  `188e548d24101b7580e507e48e1a71d76e8c8add79d2e0855f3d78555637ff3f`.
  Upstream source copyright headers remain intact. Distribution inventory is
  root-owned; this review does not certify a packaged release's notices.

**Executed checks.** Portable Go `go1.27.3 windows/amd64`; synthetic documents
only; no live node, environment file, user database, or external network calls.
No nested agents were used.

- Unmodified frozen suite: **292 passing test/subtest events**, no failures or
  skips. This independently reproduces the leaf owner's result, including the
  original five numeric and eight independent nonfinite regressions and the
  worker deadline/cancellation/OOM/recovery cases. It is not a repeat of the
  process runner owner's separate full audit.
- Final suite including the two authorized review files: **324 passing events**
  and **22 failing events**. The failures are 20 assertion cases described above
  plus their two enclosing tests; neither is an infrastructure/pending outcome.
  Leaf: 316 pass/22 fail events. Evaluator: eight pass/zero fail events. No skips.
  The original 292 passing events remain passing.
- The new review files execute **47 worker cases** and four pure walker tests.
  All 20 additional boundary controls pass, covering unexecuted branches and
  closures, short circuiting, composition, partial native callbacks, lexical
  shadowing, subsequent fresh builtin state, aliases, nested eval, predicates,
  grouping, reduce/sift/single/filter, ordering, and lambda sort.
- New pure tests pass for mixed pointer/value container depth, repeated-reference
  aggregate visit accounting at exactly 65,536 visits and one over, mixed cycles,
  alias mutation followed by a fresh walk, fresh budgets after rejection, and
  fixed redaction without invoking an opaque value's String method. No expression
  compilation, evaluation, or evaluator binding occurs in those pure tests.
- Existing offline official JS verifiers pass for all **251 unchanged records**:
  177 primary/numeric, 34 independent, 40 finite-profile. No `--record` flag was
  used. Additional local JS evaluation of all 47 review cases confirms every
  UP control and the findings. Five DOWN expectations deliberately reject JS UP
  under the accepted profile; they are recorded separately, not reported as bugs.
- `go vet -mod=readonly ./apps/node-go/internal/jsonquery/...`: PASS.
  `go mod verify`: all modules verified for the private snapshot.
- Linux amd64 and arm64 CGO-disabled `-mod=readonly -trimpath` test-executable
  builds pass for both packages, including the new review tests. These are
  compilation/linkage facts, not runtime passes: the leaf executables contain
  the failing regressions. No new Alpine execution is claimed.

**Source conclusions and limits.** The bounded walker itself has no identified
defect in the reviewed contract. Its root-at-zero depth limit is 128, with
depth 129 rejected. It counts at most 65,536 values including containers and
leaves; aliases count again and cycles exhaust those bounds. It traverses only
the specified JSON containers and JSONataArray data, leaves closures and other
opaque values alone, never reflects or calls them, and returns one fixed error
without formatting raw input. Its budgets are local to each call. AST results
are checked before `evaluate` applies its predicates/groups, and successful
returns of the shared invocation helper are checked and cleared on rejection.
The matcher finding concerns subsequent propagation of those errors.

Source inspection finds user-controlled `Compile`, `Evaluate`, nested `$eval`
parsing, and serialization only on the child-side path. `Config.Validate` and
`Request.Validate` are bounded shape/scalar/UTF-8/JSON checks. The evaluator also
parses a small fixed function-composition expression at package initialization;
that happens in parent processes too, but does not parse user input. Thus
"all compilation in the child" should be read as user-provided compilation,
not an assertion that package initialization performs no parser work.
The shim is registered on each fresh expression with `<x-:n>` and Evaluate gets
nil bindings. Lexical overrides and nested evaluation are preserved in executed
controls. No mutable registration or default-limit changes are made by the
wrapper. Evaluator imports are standard library only; `net/url` performs URI
conversion, not network I/O. No exposed file, network, process, or environment
capability was found in the retained builtins.

The captured process code installs child limits before payload validation and
Check, uses the 128 MiB OS memory control and 64 MiB Go soft limit, GOMAXPROCS 1,
two shared admissions, bounded serialization/streams/nonce frames, deadlines,
kill/reap, and parent-death protection. No fallback to parent evaluation was
found in leaf, dynamic dispatch, HTTP, or service integration. This is source
inspection plus the existing leaf worker tests executed on Windows, not a new
independent measurement of Linux kernel limits. Advisory MaxRange inheritance
is still broken in upstream lexical frames as documented; that does not remove
the separate worker boundary. The previously reviewed process EOF repair and
Alpine evidence are neither repeated nor claimed as this review's new evidence.

**Snapshot drift and handoff.** At final audit, all frozen leaf files and the
captured production process/dispatch/integration files still matched the current
checkout. Root had removed the external JSONata requirement and its two go.sum
entries after the copy; those are the only differences found in the compared
module files. Snapshot go.mod SHA-256 is
`b3271f37dc901ef630440a94b28f85b6e8d7a04f33d346bee80c34685b63fb2c`;
snapshot go.sum is
`5ebbff4047a6160d925222849bee9abfda78fa143dd4e02bffff67a8c66cf8a8`.
Current hashes observed were respectively
`2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a` and
`6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47`.
Verification here applies to the retained snapshot module set. Later production
corrections and any revised freeze require a new review or explicit diff review.

Evidence is under `.canary/jsonata-profile-review/run-45a39cf4/`:
`baseline-tests.json`, `final-tests.json`, `review-js-results.json`,
`surrounding-source-hashes.json`, the raw/patched `upstream/` copy and four Linux
test executables. Earlier exploratory matcher/sort logs are retained separately.
Reproduce the blocking assertions from the private source directory with the
absolute cached Go executable and:

```text
go test -mod=readonly -count=1 -run TestProfileReview ./apps/node-go/internal/jsonquery
go test -mod=readonly -count=1 -run TestFiniteReview ./apps/node-go/internal/jsonquery/evaluator
```

The review is complete; production remains blocked on the two P1 findings.
No original reference expectations or historical regression meanings were
changed to accept their false UP outcomes.
