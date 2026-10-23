**Independent R3 native-validator review complete: no scoped correctness objection.**

Reviewed candidate `.canary/jsonata-runtime-candidate/r3-intrinsic-20261022T223939Z/source` on 2026-10-22/23. This review is limited to numerical and structural correctness of the private Go validator, its bootstrap wiring, and relevant numeric conversion behavior. Source inspection and root's focused checks identify no new concrete correctness defect in these paths. All 30 native assertions pass after correcting one new synthetic fixture's expectation; all 33 unchanged worker checks pass. The original failure and its classification are retained below. The reviewer executes no fixtures. This is not a Linux, adoption, deployment, or release conclusion.

Only these new files are authored by this review:

- Candidate `source/apps/node-go/internal/jsonquery/jsvm/native_guard_review_test.go`.
- Candidate `source/apps/node-go/internal/jsonquery/native_runtime_review_test.go`.
- This report at repository `docs/jsonata-native-runtime-review.md`.

The reviewer runs no build, test, compiler, Go/JS program, download, install, cache creation, snapshot, archive, VM, browser, or subagent. Root owns focused execution through `scripts/bounded_validation.py` under the existing 3 GiB project, 1.25 GiB idle, 256 MiB cache, and 512 MiB scratch ceilings. Existing local source was inspected with read-only PowerShell/rg operations, including the already present pinned Goja source. No candidate or baseline implementation, test, module, or provenance file is edited.

**Source conclusions.**

`jsvm/evaluate.go` passes the Go callback only as the argument of a trusted initialization closure. The patched bundle captures that argument and its private `WeakSet` inside the JSONata implementation. It does not bind a JSONata function or install the validator as a global property. The production `runtimeLibrary` is used by this closure; inspecting only the standalone canonical JS helper would not establish native coverage. Expression and document strings continue to enter as primitive arguments to the fixed bridge.

`jsvm/native_guard.go` checks the cancellation flag/context and depth/visit budget before processing each occurrence. Every boundary receives a fresh 65,536-value budget; there is no visited-object memoization that would discount repeated aliases. Depth is rooted at zero. Array positions, including holes, consume visits; ordinary objects use their own enumerable string-key domain. Consequently an inherited index is not read to fill a hole, nonenumerable array indices are still visited, and nonenumerable ordinary object properties are outside this guard's domain. These distinctions match the retained canonical guard rather than describing new public semantics.

The descriptor callback is captured before evaluation. A traversed property is obtained with `Object.getOwnPropertyDescriptor`, and an accessor descriptor is rejected without calling it. The sole `Get("value")` during traversal is on a fresh descriptor object after its own data-value field has been established with the captured `Object.hasOwn`. No traversed object content is exported, coerced to a primitive, or converted through general reflection.

The explicitly approved metadata check is justified by the pinned dependency source: `Object.ExportType` directly dispatches to `exportType`, and `proxyObject.exportType` returns the fixed proxy type without consulting the target or handler. This includes callable and revoked proxies. The guard performs this check before `AssertFunction`, the private identity lookup, `ClassName`, key enumeration, or descriptor lookup. The review does not expand the approved use of reflection beyond `reflect.TypeOf(goja.Proxy{})` and this metadata comparison.

Known callable values are opaque. Registered JSONata procedure objects are opaque only by membership in the private identity set. Inheriting from a registered object does not confer membership; copying a marker field does not confer membership. Procedure identity is populated at lambda, partial-application, and native-definition construction sites. The new worker controls cover returned closures, partial lambdas, composition, tail-call thunks, arrays of functions, regex callables, and closures returned by `$eval`, paired with an invoked nonfinite body that must fail before its result is discarded.

The R3 decimal optimization retains the primitive-string grammar and byte guard. It stores the one `parseFloat` result and tests that number for finiteness. In this accepted decimal grammar, it does not introduce object conversion or change the returned conversion routine. The new numeric controls use explicit expected results at binary64 integer ties, the maximum finite rounding boundary, the half-minimum-subnormal boundary, negative underflow, and alias/partial/`$eval` routes; they are not solely old-versus-new implementation comparisons.

**Additional fixtures and exact selectors.**

For `./apps/node-go/internal/jsonquery/jsvm`, the exact selector is:

```text
^TestNativeGuardReview(Descriptors|CombinedLimits|CapturedIntrinsics)$
```

This supplies 30 fixed guard assertions. Coverage adds inherited proxy/getter safety, reserved keys on null-prototype objects, nonenumerable indexed array data/accessors, own-versus-inherited data descriptors, `Symbol.toStringTag`, poisoned descriptor prototypes and captured builtins, and a registered prototype whose child is not registered. Combined-limit cases put 65,408 packed or sparse leaves at depth 128 with exactly 65,536 total visits, then add one visit; they also check empty containers at depth 128, holes at depth 129, repeated aliases at different depths, exact alias aggregates, independent full budgets, and repaired cyclic graphs.

For `./apps/node-go/internal/jsonquery`, the exact selector is:

```text
^TestNativeRuntimeReview(Procedures|NumericStrings|BoundaryIsolation)$
```

This supplies 33 worker checks using the existing self-exec protocol and fixed public messages. The numeric tests include two valid UTF-8 byte-cap boundaries that are not masked by invalid number grammar:

```jsonata
$number($pad('0o10',-524290,'\u00a0'))
$number($pad('0o10',-349528,'\u2000'))
```

The retained upstream octal regexp alternative permits leading whitespace, and the pinned BigInt conversion trims it. The first expression contains 524,286 two-byte NBSP characters plus four ASCII bytes; the second contains 349,524 three-byte EN QUAD characters plus four ASCII bytes. Both are exactly 1,048,576 UTF-8 bytes and should return `8` (UP for expected `8`). Increasing either pad width magnitude by one must be DOWN before a following `true` can hide the error. A small NBSP-octal control separates conversion correctness from resource-success behavior. These cases preserve the one-second production evaluator budget, so an exact-boundary failure needs classification from root's execution evidence.

**Preservation and provenance.**

Read-only SHA-256 verification found all 415 entries in the preserved `source-manifest-final.json` present and byte-identical before these additions and again after the new fixture correction. Its hash is `55854d8a9bece4ed6bc308c9cd25362878a004a2f1ee78e75d7a1ff49147b7ce`, matching `HANDOFF.json`. Verified local hashes also match the handoff for canonical `jsonata-source.js` (`242264318177fc2209e0959638d30273deaa646468b7380bb69481ec93a34e0b`), actual `jsonata.js` (`51ce0298ee0c6956335ece6bd3def5d520477b4444306cb1bf569735c7cbbf30`), and the retained upstream patch (`139adbbd2d46bfba832c4caf29005a003ce949faab5b12009fcaaefdb9a6c71d`). The embedded version is JSONata 2.2.2, upstream commit `6c7e95fdbf4405a1e741852a7cd8cd985b4305bb`, and Goja is pinned to `v0.0.0-20261022125820-07c4b9d6265b`. This review reads the existing esbuild 0.28.2 provenance; it does not rerun bundle generation or reconstruction.

The final new native fixture SHA-256 is `e279537d1290f43e1d76f24fc89fd945e92c508e5a660419006675202a4e822a`; the unchanged worker fixture SHA-256 is `04243d1b77f2544cceea91bbae97547c8dafffb47384a3824846c3fb4ace7681`. Total additions are below 1 MiB. The working root retains its old evaluator; this review does not adopt the private candidate.

**Execution evidence and retained findings.**

Root reported the original R3 native helper selection PASS in 4.010 s, and the saved 130-case matcher/projection review plus numeric-boundary, resource-recovery, maximum-document, and JSCandidateReview controls PASS in 8.376 s. Those are root-reported results, not reviewer-executed tests.

Root then ran the two new package selections together with `-mod=readonly`, `-count=1`, and `-timeout=60s` through the bounded validator. All 33 worker checks PASS in 4.148 s, including both exact UTF-8 numeric-string boundaries and their above-cap failures. The native package FAIL in 1.234 s contains exactly one failing subcase, reproduced verbatim here:

```text
--- FAIL: TestNativeGuardReviewDescriptors (0.00s)
    --- FAIL: TestNativeGuardReviewDescriptors/descriptor_prototype_value_getter_is_not_read (0.00s)
        native_guard_review_test.go:130: fixed native guard program failed: TypeError: JSON query result rejected by finite profile at uptime-canary/apps/node-go/internal/jsonquery/jsvm.privateValidator.func4 (native)
FAIL
FAIL	uptime-canary/apps/node-go/internal/jsonquery/jsvm	1.234s
ok  	uptime-canary/apps/node-go/internal/jsonquery	4.148s
FAIL
```

The guard took 37.812 s, exit 1, with 501 bytes of untruncated output. Its source fingerprint before and after is `1e00bcc61dcd19dab0b34323b861b5a9f49241ddc3fb14b3d0501736bcd65d68`. Root reported 1,249,647,759 project bytes, 268,407,589 cache bytes, zero scratch, zero module growth, and `pauseRequired: false`. The failed run remains a failed run; it is not relabeled as passing after a fixture correction.

**Classification of the synthetic fixture failure.**

The original `native_guard_review_test.go` SHA-256 at that run, independently read before correction, is `190d06a90fbd3ba56e7024fb75bc5d4a7d2d522975fa5a87a651ba14992eb8f1`. Its exact original script is retained here:

```javascript
let reads = 0;
const safe = {x: 1}, accessor = {};
Object.defineProperty(accessor, 'x', {get() { reads++; throw 1; }, enumerable: true});
Object.defineProperty(Object.prototype, 'value', {get() { reads++; throw 2; }, configurable: true});
try {
    canaryFinite(safe);
    return reviewRejects(accessor) && reads === 0;
} finally { delete Object.prototype.value; }
```

Pinned Goja `builtin_object.go:31` (`valuePropToDescriptorObject`) allocates a normal `r.NewObject()` and populates descriptor fields with `obj.setOwnStr("value", value, false)`. `object.go:550` (`setOwnStr`) consults inherited properties. At `object.go:624` (`_setForeignStr`), the synthetic getter-only `Object.prototype.value` is nonwritable according to `value.go:511` (`isWritable`). Assignment returns false without calling the getter. Descriptor construction ignores that false, so the fresh descriptor has no own `value` field. `native_guard.go` checks that field with the captured `Object.hasOwn`, rejects immediately, and never reaches `trusted.Get("value")`.

The observed rejection occurs during validation of `safe`, before the original fixture can check the accessor. Source therefore explains a wrong fixture expectation, not a getter call or a false-healthy result. JSON documents parsed by the bridge cannot install JS accessor descriptors on `Object.prototype`; ordinary JSONata property lookup uses own-property checks and excludes function internals, and object/group/merge results use null prototypes. No expression-language route to the required global mutation has been identified in the reviewed source. This is a synthetic global-environment limitation in the pinned dependency, not an additional requirement for ordinary document descriptors.

Root explicitly authorized correction of this new fixture while retaining all 415 baseline files. The renamed `descriptor_prototype_poisoning_fails_closed_then_recovers` now checks private rejection of both finite and accessor-bearing objects under this synthetic poisoning, zero getter reads, and successful finite validation after restoring the prototype. It also confirms that the actual document accessor still rejects without being read. It makes no permanent assertion about an internal descriptor object's layout. The original script and failed-run evidence remain above.

Root reran only `./apps/node-go/internal/jsonquery/jsvm` with the exact native selector above: **PASS, 0.671 s**, guarded wall time **12.328 s**, exit **0**. All 30 native assertions therefore pass, including zero accessor calls and restoration recovery in the corrected synthetic case. The source fingerprint before and after this rerun is `137b70b728e63e0d1d738498fa4c2bc76e4c6d85f5ebd29a844be9a6c48a5ffa`, unchanged during execution. Root reported 1,247,576,167 project bytes, 266,321,783 cache bytes, zero scratch, zero module growth, and `pauseRequired: false`. The worker source is unchanged; its earlier 33 checks PASS in 4.148 s remain the applicable evidence. No additional execution or candidate source changes follow this final result.

The old Go R6 review retains seven compatibility mechanisms and 40 false-healthy reference assertions in `jsonata-matcher-projection-review.md`; official JS behavior resolves them in the C1/R2/R3 controls. C1/R2 failed real Alpine resource-success boundaries. R3 has not run Alpine within this review; root's separate bounded local Alpine work remains the fit/adoption gate. No remote host or Portainer deployment is reviewed here. The four unchanged legacy failures remain unresolved and unwaived: lookahead, coalescing, and Elvis are asserted unsupported by historical tests even though official JS supports them, and `TestRangeLimitSourceGap` expects UP for 10,001 range values despite the declared 10,000 bound. This review neither edits those assertions nor counts them as new R3 defects.
