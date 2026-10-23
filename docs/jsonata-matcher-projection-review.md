**Independent ROUND3 review: changes required.** Reviewed 2026-10-22 against
R6 freeze `842457019b5516a84d2e525c7d0cb976157e8b4f21ad2751b43798dd08cca98f`.
Seven remaining P1 mechanisms produce false UP in the scoped matcher/native
paths. The original 716 events, including every unchanged ROUND2 assertion,
pass. The new 130 worker cases have **40 false-UP assertions, 20 finite-DOWN
mismatches and 70 passing controls**. These are assertion counts across variants
and paired expectations, not counts of independent defects.

This review adds only `matcher_projection_review_test.go` and this report to
the shared checkout. It changes no production, existing tests, fixtures,
goldens, provenance, freeze, modules, process code/limits or root integration.
It uses synthetic documents and the existing production self-exec worker.
No nested agent, install, live database, environment file, VM, Linux job,
external message, default application binary or commit was used.

**P1: native partials still manufacture finite results from unsupported receiver
types, including the changed length path.**

Document `{}`, operator `==`, expected `2`:

```jsonata
$sort([2,1],$length(?))[0]
```

Go: **UP**, `service check succeeded`. Official JS 2.2.2: **TypeError / DOWN**.
The smaller `($f:=$length(?);$f(123))`, expected `0`, has the same discrepancy.
Booleans, null and objects also become length zero instead of errors. A numeric
array produces zero in Go versus its iterable length in JS: `[1,2]` is JS `2`.
Alias/property, lambda comparator and nested-eval variants reproduce the error.
Ordinary `$length(123)` remains correctly rejected; finite string partials work.

`evaluator/native.go:16` removes the outer signature for partial invocation,
as the reference requires for valid coercions. But `functions.go:782` defaults
the receiver to empty text on a failed string assertion. The returned-value
guard sees an ordinary finite zero. Reinstating every outer signature would
break valid partial semantics already tested; repair or explicitly reject
unsupported implementation arguments before producing success.

The same retained receiver handling affects `$split`, `$replace` and `$match`:
`($f:=$split(?,',');$count($f(123)))` is Go UP for expected `1` versus JS
TypeError; `($f:=$replace(?,/a/,'x');$f(123))` is Go UP for expected `""`
versus JS result `123`; and partial `$match(?,/1/)` counts zero versus JS one.
These are related implementation-call defects, not approved profile differences.

**P1: new primitive conversion helpers replace reference conversion errors with
finite success.**

Document `{}`, operator `==`, expected `1`:

```jsonata
($f:=$power(?,0);$f({}))
```

Go: **UP**. JS: **TypeError / DOWN**. `native.go:156` substitutes NaN for the
object conversion. `functions.go:2858` then computes `math.Pow(NaN,0) == 1`;
the guard cannot recover the lost error. The DSL-created object in this exact
reference expression cannot be converted to a primitive. An object containing
`'toString':1`, repeated partial application and a partial comparator also
reproduce errors being changed into success. `$count($sort([{},{}],$power(?,0)))`
is Go UP for expected `2` versus JS TypeError.

The new sum path has the corresponding problem: `($f:=$sum(?);$f([{}]))` is
Go UP for expected `"0[object Object]"` versus JS TypeError. At `native.go:165`,
`nativeJSAdd` formats maps through `matchGroupText` rather than preserving a
failed conversion. Six false-UP assertions exercise this mechanism. Numeric
text power, empty-array power, primitive sum and mixed string sum controls pass.
This finding does not ask for host bindings or inspection/invocation of opaque
captures. Unsupported conversions must fail boundedly; the prose qualification
about unverified coercions cannot authorize successful replacement values for
these known errors.

**P1: `$match` still deletes non-array custom `groups`, suppressing errors in
consumers of the projected record.**

Document `{}`, operator `==`, expected `"ok"`:

```jsonata
$exists($match('a',function($s){
  {'match':'a','start':0,'end':1,'groups':'q',
   'next':function(){missing}}
}).groups) ? $error('round3-private-marker') : 'ok'
```

Go: **UP**. JS: **D3137 / DOWN**. The reference accepts the record through
its numeric start/end alternative and projects the original groups value.
`functions.go:993` asserts an array, obtaining nil for other shapes;
`functions.go:1216` publishes that reduced value. Numeric, boolean, null and
object groups all reproduce the skipped error branch: five false-UP assertions.
These cases have valid continuations and satisfy the reference's existing
structural alternatives. Preserve the observable value or reject an unsupported
record before success.

Replacement callbacks already receive the original non-array groups and pass
their controls. Unmatched optional capture slots, participating empty captures,
ordinary second-group access and original custom callback metadata also pass.
R6 repairs the old optional-slot finding without closing every projection.

**P1: replacement consumes `end` instead of the reference's matched-text length.**

Document `{}`, operator `==`, expected `"xc"`:

```jsonata
$replace('abc',function($s){
  {'match':'a','start':0,'end':2,'groups':[],
   'next':function(){missing}}
},'x')
```

Go: **UP**, result `"xc"`. JS: result `"xbc"`, **DOWN**. JS sets the next
position to `matches.start + matches.match.length`. Go sets it to `matches.End`
at `functions.go:1406`. Both string and callback replacement forms fail paired
wrong-result/correct-result checks; fractional start metadata also differs.
The split control correctly consumes end, demonstrating why the two consumers
cannot share this replacement rule.

A missing match property adds a direct error-to-success case: the same record
without `'match':'a'` and expected `"xbc"` is Go UP versus JS TypeError when
JS reads `match.length`. Retaining the original callback object alone does not
fix how the enclosing consumer uses it. No malformed-index panic is needed.

**P1: replacement `$0` is discarded when no capture groups exist.**

Document `{}`, operator `==`, expected `"b"`:

```jsonata
$replace('ab',/a/,'$0')
```

Go: **UP**, result `"b"`. JS: result `"ab"`, **DOWN**. The zero-capture
branch at `functions.go:1255` discards every digit token, including zero.
JS handles `$0` as the whole match before considering capture groups.
The paired correct-result assertion fails as well. `$00` and `$01` retain
further finite wrong results; capture-present zero tokens, ordinary `$1`/`$2`,
literal `$$` and a trailing dollar provide passing controls. This is finite
substitution semantics, not a nonfinite rejection policy.

**P1: regex continuation changes anchors and word boundaries by slicing input.**

Document `{}`, operator `==`, expected `3`:

```jsonata
$count($match('aaa',/^a/))
```

Go: **UP**. JS: result `1`, **DOWN**. At `jsonata.go:2918`, the continuation
searches `str[fromIndex:]`; `^` now sees a fresh beginning at each offset.
The corresponding replace gives Go `"xxx"` versus JS `"xaa"`; split gives
four parts versus two. `\b` similarly loses preceding context: matching
`/\ba/` against `'aa'` counts two versus one. Paired assertions preserve
both false-UP and finite-DOWN evidence. Unanchored repeats and the multiline
anchor control pass. This uses supported ordinary regex syntax, without the
documented unsupported lookahead construct.

**P1: observable regex offsets use UTF-8 bytes instead of JS UTF-16 positions.**

Expression `$match($,/a/).index`, document JSON string `"éa"`, operator `==`,
expected `2`: Go **UP** versus JS result `1`, **DOWN**. With document `"😀a"`,
Go is UP for expected `4` versus JS result `2`. Combining text also differs.
`jsonata.go:2929` exposes regexp byte offsets directly. Replacement callbacks
that stringify `.start` reproduce the discrepancy through the newly preserved
original map. Native byte offsets remain appropriate for Go slicing, but they
cannot also serve as the exposed reference positions. These tests distinguish
the codepoint semantics of `$length` from UTF-16 regex positions.

**Profile, length and repair controls that passed.**

All original matcher continuation propagation, native-sort, fractional/empty
input, optional-capture and eval-scope regressions pass unchanged. The new tiny
positive limit invokes its failing matcher, the tiny negative limit rejects,
and zero-limit controls preserve reference callback suppression. Explicit
matcher/comparator errors remain bounded and redacted. No private marker occurs
in public results. New context controls pass, including `$eval`'s actual
parameter names and absence of generated partial locals. Some raw native HOF
callback constructions error in JS too; they remain error controls.

The pure walker is byte-identical to its prior frozen version. Its documented
root-at-zero depth 128 and 65,536 visited-value budgets remain intact; aliases
count again, cycles exhaust the bounded budget and closures remain opaque.
The AST check still precedes predicate/group consumption, and the shared
invocation check still preserves errors and clears rejected returns. New worker
documents pass at exactly 65,536 visits and depth 128 and reject at 65,537 visits
and depth 129. Four explicit profile controls retain **JS UP / Go DOWN**:
returned partial-sort overflow, AST overflow and those two excessive documents.
They remain separate from reference matches and the seven findings.

The `utf8.RuneCountInString` optimization itself has no identified count or
allocation regression. Native and partial string calls pass empty, ASCII,
multibyte, astral, combining, joiner and NUL controls; undefined propagates.
The unchanged maximum-document worker test and the existing one-MiB zero-
allocation helper assertion pass on Windows. The partial type errors precede
the count helper and do not establish a RuneCount defect.

**Executed validation and preservation.**

All Go execution used the coherent private snapshot at
`%TEMP%\uptime-r3-projection-c3b6b3cea03448ac982f67127d485e70\source`.
The leaf and modules were copied with hashes before any test. The complete
snapshot contains 77 files: the 59 original leaf files, both root Go module
files and the process support files. No root API/hierarchy source was copied
or used as review evidence. The final byte audit found zero drift in those
77 paths or the 59 original leaf files. Both earlier review reports are retained
privately and were read in full with the current README, FINITE_PROFILE,
VALIDATION, freeze, provenance, exact patch and R6 repair documentation.

- Go **1.27.3 windows/amd64**, existing portable toolchain, `GOPROXY=off`,
  `GOSUMDB=off`, `GOTOOLCHAIN=local`, `-mod=readonly`.
- Untouched baseline `go test -mod=readonly -json -count=1
  ./apps/node-go/internal/jsonquery/...`: **716 pass, zero fail/skip**.
- Final same full subset: **786 pass, 61 fail, zero skip**. Leaf: 770 pass,
  61 fail; evaluator: 16 pass, zero fail. All 716 baseline passing event names
  remain passing, including the unchanged 105 ROUND2 cases. The 61 failures
  are 60 new case assertions plus their enclosing test. There are **zero
  Pending/incomplete cases**. The 40 false-UP assertions comprise 21 reference
  errors and 19 different finite scalars; none is an approved safe-DOWN case.
- `go vet -mod=readonly ./apps/node-go/internal/jsonquery/...`: **PASS**.
  `go mod verify`: **all modules verified**.
- Five existing offline JS scripts reverify all **610 original records**,
  including the 105 embedded ROUND2 cases. All **130 new records** reverify;
  the exact JSON embedded in the new Go test matches those observations.
  New JS outcomes: 70 UP, 60 DOWN, including 36 errors; four UP records
  explicitly require the approved profile's Go DOWN. No old `--record` refresh,
  expected-result weakening or fixture alteration was used.
- All **51 freeze entries** and the supplied freeze digest match before and
  after validation. Exact Git blobs at commit
  `599f35f32e5f31297f8b2153b5da44ddbb330e28` match the cached pseudo-version
  and recorded raw hashes for all six original production files and LICENSE.
  Applying the patch with Git CRLF conversion disabled reconstructs all
  **11 local production files** byte for byte. Three original Go files and
  LICENSE remain original; three Go files are edited and five helpers added.
- Existing official JS installation version **2.2.2**: `jsonata.js` and
  `package.json` bytes match the already cached registry tarball, whose SHA-512
  equals the pinned npm integrity string. This is an offline byte/integrity
  check, not a fresh registry or supply-chain audit.

**Inspected and unverified scope.** The finite walker, its two guard boundaries,
matcher adapters/consumers, sort invocation/truthiness, native partial metadata/
context/coercion and length implementation were inspected. No new guard-walker
defect or host capability was identified. Existing tests exercise the Windows
worker controls; this review did not redo the independent process or transport
audit. No Linux cross-build, actual Alpine execution or VM run was performed.
Root's active Alpine agent owns the unchanged maximum-document rerun. The old
8b5 Alpine Pending observations and core/race passes are not recast as evidence
for current semantic approval. Full language parity, universal no-false-UP,
complete builtin coercion, current root integration, distribution notices and
release certification remain unverified by this scoped review.

**Stable hashes and evidence.** SHA-256:

```text
matcher_projection_review_test.go
d144f225a61bde7b177b5cddba192673a2f9dbe665f295b0390eeab6f5c73c19
SOURCE_FREEZE.json
842457019b5516a84d2e525c7d0cb976157e8b4f21ad2751b43798dd08cca98f
evaluator/upstream.json
0463bb46a968a6ad4fe41ace181975bc86c1c3368b388491ea11e1f793bcb533
evaluator/finite-profile.patch
85ea0bcede0301f1fcf6e4153a1f90e87a5909b0949c14cc8b41325373504d41
evaluator/finite.go
03d2f4ad804e2bdb18b977b5db8de317ef82e9065d441b67135fa2b6d3bc4f47
evaluator/LICENSE
188e548d24101b7580e507e48e1a71d76e8c8add79d2e0855f3d78555637ff3f
go.mod
2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a
go.sum
6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
baseline-tests.json
dcb6b8bdbd7bdfdff0fd7f33b5d4277508fd56aae4db0ec73c3f23cb0858baa3
final-tests.json
96c0366db507cdc1ac935d95234f80575fa82fac7bf457d7a1634aaaf620909a
reference-results.json
0203cdfbed5f3c8d888de6600b6626f14655de2b42fbd12446825e2268ade95e
case-outcomes.json
0cffeb9e9b689ab97044c27d5537cbf5b9bb8006435d9403ac8948d58516ac7f
snapshot-hashes.json
281c21cbf00e8562fbd185b1fffdae6f02387b1ee3bfa9dd51f4763842d6ed49
```

Private evidence includes the original byte snapshot, raw/reconstructed upstream
files, provenance verification, unchanged module copies, immutable JS
observations, baseline/final logs, per-case classifications, validation command
results and final drift audit. Early exploratory logs remain separate. An
initial exploratory Unicode harness read was corrected to explicit UTF-8 before
the expanded and stable runs; no old fixture or production source was involved.

Reproduce from the private `source` directory with the existing absolute Go
executable, or the matching shared source and the same toolchain:

```text
go test -mod=readonly -count=1 -run '^TestMatcherProjectionReviewReference$' ./apps/node-go/internal/jsonquery
go test -mod=readonly -count=1 ./apps/node-go/internal/jsonquery/...
go vet -mod=readonly ./apps/node-go/internal/jsonquery/...
```

The private offline verifier is `node ../reference.mjs --verify --embedded`.
The review is complete; production corrections belong to the owner. The new
desired-behavior assertions remain failing on this frozen R6 source.
