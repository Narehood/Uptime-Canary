**Independent FIRST review: no new blocking defect found in the scoped private
candidate.** Reviewed 2026-10-22. This supports continuing the root-owned Alpine
and integration gates. It does not approve production adoption, certify a
release, or establish universal JSONata equivalence/no-false-UP behavior. The
current shared R6 evaluator remains known bad from ROUND3 and is unchanged.

The subject is `.canary/jsonata-runtime-candidate/run-20261022T213352Z/source`
(397 frozen files), with JSONata 2.2.2 and Goja
`v0.0.0-20261022125820-07c4b9d6265b`. The review used an independently copied
workspace at `.canary/jsonata-candidate-review/run-20261022-first/` (`R` below).
It did not switch to the later integration preflight as its baseline.

**Preservation and provenance verified.** The original input manifest matches
`f429a3cb2571c0ca2a3087c65e9511c161562277512c84e3a39c07c701b97d88`;
all 391 reference files match it. Among those files, only private `check.go`,
`go.mod`, and `go.sum` differ in the candidate. All 295 entries in the candidate
capture match, including the exact ROUND3 test
`d144f225a61bde7b177b5cddba192673a2f9dbe665f295b0390eeab6f5c73c19`.
All 397 final source-manifest entries match both the candidate and the original
files in the independent copy. The source archive contains those same 397
files with matching hashes. Deep paths were copied and read through
`scripts/validation_paths.py`'s extended Windows filesystem spelling.

The recorded 11-hunk bundle patch was independently applied in memory without
Git or newline conversion. It reconstructs the patched bundle byte for byte.
The cached npm archive has the reported SHA-512 integrity; its bundle,
`package.json` (version 2.2.2), and LICENSE match the retained upstream files.
The raw bundle hash is
`9c2a35f4d895797f123f65c7f9a4ce16b3a295d209e0f3f225a0c3bae1bd9049`;
the patched hash is
`6de44a5a504ec8b5a765687ac6e436ca4193faf5fde8675ed8812120d5bfa069`.
This establishes the retained artifacts' consistency, not independent online
registry authenticity or a current vulnerability scan.

**Guard and host boundary inspection.** The actual bundle checks successful AST
dispatch results before predicate/group consumption. Shared `applyInner`
returns and partial-native `applyNativeFunction` returns also check results.
Native callback wrappers route back through `apply`; tail calls retain that
route. Nested `$eval` shares this evaluator and its hard-coded 10,000 range
bound. The source patch adds no public guard registration or builtin wrapper
list. It preserves the approved stronger partial-sort rejection.

The walker starts each call at root depth zero with 65,536 visits, accepts
depth 128, counts repeated references again, and bounds cycles through the
same budgets. It traverses enumerable own object data properties and indexed
array slots, ignoring array sequence metadata. It obtains descriptors and
rejects accessors on traversed properties without reading their values. JS
functions remain opaque. Actual native headers, lambdas, tail-call lambdas,
and partial procedures are registered where created in a private `WeakSet`;
forged marker flags cannot grant an exemption. It does not inspect closure
environments. This is a returned-value profile, not a scan of every private
builtin temporary or every property of an opaque engine object.

The Go bridge creates the VM only inside `CheckInWorker`'s evaluator call.
It runs fixed embedded library/bridge source and passes expression/document
text as primitive arguments. JSON is parsed in the VM. No Go object, Go
function, Node binding, module loader, filesystem/network API, user registration,
source-map loader, fallback evaluator, runtime selector, or alternate executable
was found. It accepts only a fulfilled Promise containing a serialized,
non-null finite scalar; rejected or unsettled Promises return a fixed error.
The new self-exec pending-thenable test returns bounded DOWN and a subsequent
ordinary request succeeds. It does not compile JSONata in the controller.

The `check.go` diff changes evaluator wiring and removes old evaluator setup;
the public `Check`, `WorkerHandler`, request/config validation, scalar decoding,
and Go scalar comparator are unchanged. All frozen worker/process source files
are byte-identical. Inspection retains fixed executable dispatch, EOF input,
nonce frames, redaction, two shared admissions, deadline/cancellation, kill/reap,
128 MiB OS control and 64 MiB Go soft limit. The existing Windows maximum-document,
privacy, deadline, resource/recovery, and two-slot tests passed in this review's
unchanged-suite run. No independent Linux memory measurement is claimed here.

The declared `$number` adjustment is confined to the builtin's selected base
conversion branch and computed-string byte bound. Wide-number aliases,
partials, lexical shadowing and nested eval pass. Reverting to Goja's original
`Number(arg)` makes the four new wide-number worker assertions fail. The
`$length` adjustment keeps undefined and the original non-string iterable/error
path, counts paired surrogates once without a per-codepoint array, and preserves
lexical shadowing. New lone-surrogate and mixed-surrogate controls pass as well
as the unchanged ROUND3 type/coercion cases and maximum-document test.

**Executed validation, with categories kept separate.** Existing portable Go
1.27.3 on Windows amd64 was used with CGO disabled, `-mod=readonly`, local
toolchain selection, network module lookup disabled, and copied private module,
build and temporary caches. There was no installation, shared-cache write,
Git action, environment-file access, database use, external message, or subagent.

- Unchanged `go test -p=1 -json -count=1
  ./apps/node-go/internal/jsonquery/...`: **853 passing events, five failing
  events, zero skips**. The actual leaf accounts for 827 passing/five failing
  events; 16 old Go-helper and 10 pre-existing JS-helper events account for the
  other 26 passes. The old Go-helper events do not certify the VM.
- Exactly **737 of 740 original case expectations match**. The categories are
  primary 76 (73 pass/three fail), numeric 101, independent nonfinite 34, finite
  profile 40, matcher repair 157, ROUND2 105, R6 repair 97, and ROUND3 130.
  Every ROUND3 case and every approved stronger safe-DOWN case passes unchanged.
  The standalone range assertion and enclosing primary-fixture test account
  for the other two failing events; there is no all-pass full-suite claim.
- The new shared test file executes **72 actual worker requests**: 63 table
  cases, seven container/alias controls, and pending-Promise plus recovery
  requests. These produce **73 passing test/subtest events**, zero failures or
  skips. They use only the public jsonquery API and the existing self-exec
  dispatcher; they import no Goja or evaluator implementation.
- A NEW private `jsvm/independent_review_test.go` extracts the guard from the
  actual embedded bundle. Its **29 graph controls plus enclosing test** pass,
  and a separate fixed-engine diagnostic passes: **31 events**. These exercise
  exact/excess depth and visit budgets, sparse arrays, object keys, aliases,
  mutation/fresh calls, cycles, getters/setters, inherited fields, opaque
  functions/registered headers, copied headers, marker forgery and fixed error
  text. They compile only fixed JS helper programs, never user JSONata.
- The final new-test selector therefore has **104 passing events**, zero
  failures/skips. Leaf `go vet` and private `go mod verify` pass. The shared test also compiles and runs
  an empty selector against a private copy of the unchanged current root,
  with its original module files and **no `jsvm` directory or Goja dependency**.
  This is a root compilation check, not a root semantic pass.

The 63 table cases were separately evaluated with the retained unmodified
JSONata 2.2.2 in Node 24.22.1, and with the exact patched bundle in Node. The
patched oracle matches all 63 candidate states. Unmodified JS differs on
11 explicitly stronger DOWN cases: AST discard/predicate/group (three),
partial-sort return variants (three), marked nonfinite input (two), and range
scope variants (three). Those are recorded as profile differences. Node's
ordinary-JSONata evaluation is a subprocess oracle, not application-parent
evaluation or certification of Goja. It excludes the pending-thenable test.

Two initial new-test expectations were corrected after the unmodified oracle:
the finite filter expression returns count 2, and `$exists` on a forged
function header triggers an upstream TypeError. The latter control now uses
`($;true)` to avoid that callability confound. The original failed run is
retained as `artifacts/independent.log`, not counted as a pass. Earlier oracle
harness/setup failures are retained separately too.

**Mutation sensitivity was checked on private copies only.** Removing the AST
guard makes six new assertions fail, including bounded container consumption.
Removing both shared/partial callback-return checks makes the three stronger
partial-sort assertions fail. Replacing private identity with public marker
flags fails four actual-guard controls. Restoring the upstream range bound
fails three worker assertions. Reverting the number conversion fails four
wide-number worker assertions. Enclosing test failures are additional events;
these counts describe 20 assertions across five separate mutations.

The worker-level marker mutation alone was initially undetected. The fixed
Goja diagnostic explains why: its `JSON.parse('1e400')` throws SyntaxError,
where Node creates Infinity, so those worker cases reject earlier. They are
integration rejection controls, not proof of WeakSet isolation. The actual
guard helper controls detect the weakened identity, independently of parsing.
That limitation and the initial ineffective mutation log are preserved.

**The four original assertion differences were independently confirmed and
should receive only explicit, narrow updates if root adopts this candidate.**
All use `{}`, operator `==`:

- `$contains('abc', /a(?=b)/)`, expected `true`: unmodified official JS and
  candidate are UP; the old fixture expects DOWN for the Go RE2 language gap.
- `missing ?? 'fallback'`, expected `"fallback"`: official JS and candidate
  are UP; the old fixture expects DOWN for the Go parser gap. New false/null
  controls also confirm coalescing behavior.
- `'' ?: 'fallback'`, expected `"fallback"`: official JS and candidate are UP;
  the old fixture expects DOWN for the Go parser gap. A false-input control
  confirms the Elvis fallback separately.
- `($n := 10001; $count([1..$n]))`, expected `10001`: unmodified JS is UP and
  candidate is DOWN. The old `TestRangeLimitSourceGap` expects UP as evidence
  of the Go lexical-frame bypass. The candidate correctly applies the owner's
  explicit 10,000 limit selection; exact 10,000, lambda and nested-eval controls
  confirm the scope of that fix.

Recommendation: at adoption, remove only the three obsolete Go-gap overrides
while retaining their official reference observations, and turn the standalone
range regression into an explicit source-limit enforcement assertion. Update
the corresponding runtime/profile prose and retain all historical artifacts.
Do not add a blanket waiver, edit the other 737 expectations, or remove ROUND3
or stronger safe-DOWN coverage. This review changes none of those assertions.

**Dependencies, licenses and remaining integration scope.** JSONata and Goja
retain their MIT terms; the exact JSONata license matches the npm archive and
the captured license. Captured Goja, regexp2, sourcemap, pprof, x/text and x/sys
licenses match the copied module sources byte for byte (MIT, BSD-2-Clause,
Apache-2.0 and BSD-3-Clause as recorded by the candidate). These checks do not
constitute distribution-notice integration or dependency advisory clearance.

The frozen candidate's leaf run uses pprof `798e818bf904`. Its original
complete-node artifacts instead use a build-only modfile whose graph selects
pprof `1facf8f91b50` and explicitly replaces it with that older source. Those
artifacts must not be described as demonstrating one identical leaf/main
dependency graph. The separate root-owned integration preflight
`20261022T215945453878Z` was inspected only as additional context: its module
diff/build record selects the normal 2026 pprof version without replacements,
keeps the Goja pin and x/crypto 0.58.0, and reports verification/build success.
Its summary also records 815 passing core events and two platform skips.
Those are root-preflight observations, not new review test coverage or changes
to the frozen candidate. A final coherent integration artifact and its checks
remain a separate adoption requirement.

`number.go` still imports the legacy Go helper for the unused compatibility
function. No runtime fallback call from the candidate path was found. Removing
that import/helper and reconciling old profile comments is reasonable subsequent
cleanup, outside this review's authority and not a blocking candidate defect.

This review does not validate actual Alpine execution, a full production-node
startup/memory envelope, every JSONata feature/private builtin temporary,
complete root protocol/hierarchy integration, adversarial regex hard-stop
behavior beyond the unchanged worker tests, or release notices/scans. Root's
Alpine and integration gates remain independent. No frozen source or shared
production/module/old-test/UI file was changed, and no adoption occurred.

**Reproduction and retained evidence.** From the repository root:

```text
python .canary/jsonata-candidate-review/run-20261022-first/run_go.py test -p=1 -json -count=1 ./apps/node-go/internal/jsonquery/...
python .canary/jsonata-candidate-review/run-20261022-first/run_go.py test -p=1 -json -count=1 -run "^(TestJSCandidateReview|TestIndependentCandidate)" ./apps/node-go/internal/jsonquery/...
```

The first command intentionally retains the four disclosed assertion failures;
the second passes. `R/artifacts` holds baseline/new raw logs and event lists,
oracle records, check.go diff, all mutation results, provenance reconstruction,
root-compilation proof, vet/module logs and the final byte audit. Mutated copies
are separate under `R/mutations`; original review-source files still match the
397-file frozen manifest. The only shared writes are the NEW
`apps/node-go/internal/jsonquery/js_candidate_review_test.go` and this report.

Stable SHA-256 identifiers:

```text
candidate-capture.json
46d0ee2a293c08161a1c4a5ea8252598406c904732a337271a907cb10f7f25b1
HANDOFF.json
9503ffd4215a479405a5dbd5c753eb1f7074a1fe0e3032616ec19a9003f0dc7c
candidate-source.tar.gz
1c2858d71f00afaed763e6a2527015a362256736d172718ab2aaffd341de2694
source-manifest-final.json
b5011bd1d65f4e561728c355beb8b1031234ab85fd64914a100380d4ca97a7e7
NEW shared js_candidate_review_test.go
11091bd30e2fc203ce8c93dbf02dc9e9e9c443ab52d57edcd0ead3cdafb5e3b0
NEW private jsvm/independent_review_test.go
bf74634ac30ce6017fd6ae3d21ccc3a385add97e045f9f38b0dc8b18ddef7c54
unchanged root go.mod
2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a
unchanged root go.sum
6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
```
