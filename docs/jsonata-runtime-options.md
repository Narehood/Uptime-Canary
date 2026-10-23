**JSONata runtime feasibility study — 2026-10-22**

The best short-term choice is to finish the bounded Go-port repairs and the
assigned `$length` allocation optimization, retaining the existing regression
assertions and worker limits. Confidence in that sequencing is high; this study
does not certify the repairs or authorize shipping them. Longer term, official
JSONata JS 2.2.2 embedded in Goja is technically credible: a private spike ran the
unmodified official bundle and built without CGO for all three required targets.
Confidence in language/platform feasibility is moderate, while confidence in
production resource fit and preservation of the current finite profile remains
low. QuickJS-in-Wasm through wazero is a plausible second research option with
more build, ABI, security-update and memory-accounting work. Neither candidate
is a drop-in replacement under the current contract.

Scope: research only. The current [JSON query README](../apps/node-go/internal/jsonquery/README.md),
[finite profile](../apps/node-go/internal/jsonquery/FINITE_PROFILE.md),
[matcher/sort review](jsonata-matcher-sort-review.md) and relevant
[Alpine observations](container-validation.md#reproduced-1-mib-document-failure)
were read first. The review's four remaining false-UP mechanisms motivate this
study; root's concurrent repairs were neither changed nor retested. Only this
new report is a product-tree deliverable. All downloaded sources, experimental
code, binaries and private module/build caches are under
`.canary/jsonata-runtime-study/run-bf7ace51f517/`. No nested agents, production
changes, test edits, shared module-cache changes, npm installation, database or
environment-file access were used. Core Go/SQLite and the frontend stay outside
this study.

**What actually ran.** The official npm 2.2.2 archive was downloaded directly
from its registry endpoint, checked against both registry SHA-512 integrity and
the already documented pin, then extracted privately without running package
scripts. Its metadata identifies Git commit
`6c7e95fdbf4405a1e741852a7cd8cd985b4305bb`; the
[official release](https://github.com/jsonata-js/jsonata/releases/tag/v2.2.2)
identifies the same commit prefix. The native bundle is 298,848 bytes, SHA-256
`9c2a35f4d895797f123f65c7f9a4ce16b3a295d209e0f3f225a0c3bae1bd9049`.
Its [package metadata](https://github.com/jsonata-js/jsonata/blob/v2.2.2/package.json)
declares no runtime npm dependencies. The browser-compatible bundle ran without
Node, Chromium, a host module loader or registered host functions.

Goja was pinned to
`v0.0.0-20261022125820-07c4b9d6265b`, full commit
`07c4b9d6265b9d472b736d9a57295b03809ce02b`, obtained through
`proxy.golang.org` with `sum.golang.org` verification in a private module.
`go mod verify` passed. Go 1.27.3 builds used `CGO_ENABLED=0`, `-mod=readonly`,
`-trimpath`, `-ldflags '-s -w'`, private caches and serial package compilation.
The final build script also directs compiler temporary files into the private
study directory; the initial build used Go's ordinary disposable temporary
directory. Final Windows amd64 execution succeeded. Linux amd64 and arm64
cross-builds succeeded; direct ELF-header inspection found neither `PT_INTERP`
nor `PT_DYNAMIC`. These are standalone feasibility executables, not application
builds or Alpine runtime passes.

The 15 small synthetic expressions reproduced the reference behavior of selected
examples from all four review mechanisms: fractional matcher invocation threw
D3137; partial `$contains` comparator threw TypeError; replacement callback
`start` was present and its deliberate error propagated; optional capture slot
zero stayed undefined and capture count stayed two. Ordinary aggregation,
lookahead, `??`, `$fromMillis(0)` and nested `$eval` also worked. The returned
Promise was explicitly required to be fulfilled. This is a smoke sample, not
the complete local fixture suite or upstream conformance suite.

**Goja capability and containment findings.** The inspected pin supports
async/await, Promise and ordinary generators; all three passed direct smoke
checks. Async generators are explicitly rejected in the
[compiler source](https://github.com/dop251/goja/blob/07c4b9d6265b9d472b736d9a57295b03809ce02b/compiler_expr.go#L1747).
That does not block the native JSONata 2.2.2 bundle: JSONata
[changed its evaluator from generators to async functions in 2.0](https://github.com/jsonata-js/jsonata/blob/v2.2.2/CHANGELOG.md).
If support for arbitrary async-generator plugins becomes mandatory, this Goja
pin does not meet it. No transpiler or fallback was used.

RegExp lookahead and Date passed the small checks. Goja's
[README](https://github.com/dop251/goja/blob/07c4b9d6265b9d472b736d9a57295b03809ce02b/README.md)
documents RE2 plus regexp2 fallback, UTF-16 JSON limitations and extreme Date
overflow differences. Running official JSONata on Goja therefore does not prove
V8-equivalent behavior for every expression. The inspected runtime's
[Interrupt documentation](https://github.com/dop251/goja/blob/07c4b9d6265b9d472b736d9a57295b03809ce02b/runtime.go#L1570)
explicitly excludes native Go builtins, including regex work. The process
deadline and kill/reap remain indispensable.

The fresh VM exposed none of `require`, `process`, `fetch`, `XMLHttpRequest`,
timers, console or file helpers in the smoke check. Importing `goja_nodejs` is
unnecessary. An important default must be disabled:
[source-map loading can read the filesystem](https://github.com/dop251/goja/blob/07c4b9d6265b9d472b736d9a57295b03809ce02b/parser/parser.go#L63).
The final spike uses `parser.WithDisableSourceMaps`. A future adapter would pass
inert JSON as data, expose no Go objects/functions, and invoke only the fixed
library entry point. JSONata `$eval` remains the DSL parser, not JS eval.
These observations support a capability-free embedding design; they do not
certify absence of every incidental host access in a finished adapter.

**The finite profile cannot be preserved by an unmodified runtime swap.** The
existing contract checks results before AST predicate/group consumption and at
the shared callback-return boundary, with independent root-at-zero depth 128
and 65,536-visit budgets. Aliases count repeatedly; cycles exhaust bounds;
functions and captured environments remain opaque. It includes approved
safe-DOWN cases, including returned partial-sort overflow.

The unmodified JS-on-Goja spike returned `true` for `$exists(1e308*10)`, `"ok"`
for `(1e308*10; "ok")`, `false` for
`$exists($sum([1e308,1e308])[false])`, and `2` for
`$count($sort([[1e308,1e308],[1]],$sum(?)))`. The current contract requires
bounded rejection for those expressions. A terminal finite-scalar check would
accept them and therefore **change the internal contract**. They cannot be
reclassified as equivalent results.

Source inspection supplies a concrete obstacle: the official
[AST exit hook runs after predicates and grouping](https://github.com/jsonata-js/jsonata/blob/v2.2.2/src/jsonata.js#L118),
and native higher-order consumers call functions directly, for example
[`filter`](https://github.com/jsonata-js/jsonata/blob/v2.2.2/src/functions.js#L1512).
Neither that hook nor the official `timeout`, `stack` and `sequence` options
implements the existing returned-container walker. A VM call-stack limit also
measures a different thing from container depth.

Preserving the profile's intent appears possible only with an explicitly
maintained, private JS-source patch: a pure bounded guard before predicate/group
consumption and guarded native/callback return paths, including partial-native,
matcher, sort and nested-eval execution. It must preserve errors and opaque
closures, distinguish engine procedure objects from data, ignore sequence
metadata, and reproduce alias/depth/visit accounting without invoking getters,
iterators or callbacks. No host function registration is needed for such a
source-level guard. Exact acceptance equivalence is **unproved**, because the
two engines represent sequences and procedures differently and have different
evaluation paths. No such patch was implemented here. With an unmodified
official bundle as a hard requirement, this study found no way to preserve the
current profile. An explicit separately authorized contract change would be the
alternative; this study authorizes neither course.

**QuickJS/Wasm is feasible in architecture, unmeasured here.** Bellard's
[official documentation](https://bellard.org/quickjs/quickjs.html)
describes ES2025 support, including the needed language facilities, runtime
memory/stack limits and an interrupt handler. Its native C embedding does not
satisfy the CGO-disabled requirement by itself. QuickJS-NG is a distinct fork:
its own [feature documentation](https://quickjs-ng.github.io/quickjs/es_features/),
[WASI reactor build target](https://github.com/quickjs-ng/quickjs/blob/master/CMakeLists.txt#L366)
and [pending-job API](https://github.com/quickjs-ng/quickjs/blob/master/quickjs.h#L1158)
make a Wasm build plus explicit Promise-job draining credible. Neither engine's
language-support claim is evidence that this JSONata workload fits the worker.

[wazero](https://github.com/wazero/wazero) provides a CGO-free Go host, compiler
support for Windows amd64/Linux amd64/arm64 and an interpreter alternative. Its
documented scratch-container tests support independence from Alpine libc;
actual Alpine behavior under this application's limits is still unknown.
A prebuilt embedded Wasm asset would require no C compiler, Node or browser
in the shipped container. Building that asset reproducibly remains a build-time
responsibility.

The stock NG [reactor](https://github.com/quickjs-ng/quickjs/blob/master/qjs-wasi-reactor.c)
includes the CLI, initializes standard helpers and installs a module loader;
[`JS_NewCustomContext`](https://github.com/quickjs-ng/quickjs/blob/master/qjs.c#L230)
registers std/os modules. It is not the desired minimal evaluator bridge. A
future source-built bridge would omit those facilities, audit every Wasm
import, expose no filesystem/network/process capability or host function to JS,
and keep Promise state, exceptions, string copies and JSValue lifetimes bounded.
Wazero defaults to no filesystem access, but its clock defaults are synthetic:
retaining real `$now`/`$millis` semantics requires deliberate clock plumbing,
not blindly accepting defaults. Its
[configuration API](https://github.com/wazero/wazero/blob/v1.12.0/config.go)
supports linear-memory page limits and cancellation checks. Neither bounds
combined Go heap, executable code, stacks and Wasm memory; disk compilation
caches would also conflict with evaluator hermeticity. No WASI compiler/toolchain
was verified in this bounded Windows study, so no third-party Wasm binary was downloaded or run and
no QuickJS startup, heap, size or platform result is claimed.

**Measured resources and their limits.** Final Windows observations, with one
GOMAXPROCS and the existing 64 MiB Go soft limit, were:

- Stripped standalone executables: Windows Goja+bundle 11,567,104 bytes;
  Linux amd64 11,329,696; Linux arm64 10,748,064. A small standard-library-only
  Windows baseline was 2,727,936 bytes. This is not the production binary delta.
- Three final process wall times were 382.23, 65.68 and 74.47 ms. Time measured
  inside main through bundle loading was 17.97–21.99 ms; through all 15 cases,
  34.78–38.23 ms. Process launch and cold filesystem/security scanning are not
  isolated, so these are observations rather than startup guarantees.
- Sampled HeapAlloc after bundle load was 3,889,352–4,001,008 bytes; after the
  cases, 6,322,128–6,499,712. Sampled Go Sys reached 16,674,816 bytes. Sampled
  Windows peak-working-set counters reached 20,344,832 bytes. Neither the heap
  phase samples nor the last live process sample proves a true peak.

The spike did not install the production 128 MiB kernel control or exercise the
production dispatcher, status frame, two-slot contention, maximum 1 MiB document,
profile walker, adversarial regex, OOM recovery or Alpine. It supplies **no
evidence of lower resource use than the current Go port**. The existing 8b5
Alpine `$length($)` maximum-document failure remains a failed success requirement;
the observed data-limit pressure is an inference, not a captured exit cause.
The assigned minimal RuneCount optimization and unchanged-limit Alpine checks
remain the appropriate current work.

**Maintenance burden.** The existing local port has an MIT provenance/patch
manifest; minimal repairs preserve a small dependency footprint but leave the
project owning translation semantics and known language gaps. Official JS would
remove that translation layer while adding an engine and, to preserve policy,
a private JS guard patch and reproducible bundle generation. JSONata's
[security policy](https://github.com/jsonata-js/jsonata/blob/v2.2.2/SECURITY.md)
targets fixes at the latest release. Maintainer advisories include
[prototype/function-chain execution issues](https://github.com/jsonata-js/jsonata/security/advisories/GHSA-66mm-25pp-rfff)
fixed in 2.2.1 and
[`$toMillis` regex exhaustion](https://github.com/jsonata-js/jsonata/security/advisories/GHSA-86vw-mfpg-wwv9)
fixed in 2.2.0 on the 2.x line. Version 2.2.2 is outside those stated affected
ranges; that is not an assurance against future findings. Their applicability
to the Go translation cannot be inferred from the JS advisory alone.

[JSONata](https://github.com/jsonata-js/jsonata/blob/v2.2.2/LICENSE) and
[Goja](https://github.com/dop251/goja/blob/07c4b9d6265b9d472b736d9a57295b03809ce02b/LICENSE)
are MIT. The observed linked Goja module set also includes
[regexp2 (MIT)](https://github.com/dlclark/regexp2/blob/v2.8.1/LICENSE),
[sourcemap (BSD-2-Clause)](https://github.com/go-sourcemap/sourcemap/blob/v2.1.3/LICENSE),
[x/text (BSD-3-Clause)](https://github.com/golang/text/blob/v0.43.0/LICENSE) and
[pprof (Apache-2.0)](https://github.com/google/pprof/blob/798e818bf904/LICENSE);
their license files are retained in the private verified cache. Goja's module
graph mentioning goja_nodejs does not mean Node support was linked or installed;
the executable's build record lists the actual five dependencies. QuickJS/NG
is MIT; [wazero is Apache-2.0](https://github.com/wazero/wazero/blob/v1.12.0/LICENSE).
The Wasm route additionally needs pinned engine/bridge/compiler/sysroot sources,
asset hashes, build instructions and notices for included libc components.

Security-update work remains real for either route. NG's maintainer advisories
include [RegExp allocation-failure memory corruption](https://github.com/quickjs-ng/quickjs/security/advisories/GHSA-rxpx-wj25-jhqq)
and [32-bit TypedArray overflow](https://github.com/quickjs-ng/quickjs/security/advisories/GHSA-9wv5-5cgr-grj6),
both listing 0.16.0 as patched. The latter explicitly includes Wasm in scope;
a 64-bit Go host does not make a wasm32 guest 64-bit. These are update-workload
examples, not findings against an adopted runtime. Every update would require
source/asset verification and regression checks for the local profile and
worker behavior.

The remaining decision gates are concrete: demonstrate the full existing
safe-DOWN/finite-control contract with a declared JS patch, then measure fresh
workers on Windows and restricted Alpine amd64/arm64 at the unchanged limits,
including maximum documents, regex interruption, failure recovery and two-slot
load. Goja is the first candidate for that separately authorized work because
this study verified its bundle execution and static builds with fewer artifact
layers. There is no present reason to replace or delay root's r6 Go repair and
Alpine checks.

Private evidence: `prepare.log`, `sources/jsonata-registry.json`,
`sources/goja-proxy.json`, private `go.mod`/`go.sum`, `build.log`,
`elf-inspection.json`, `measurements.json`, and the exact `main.go`, `smoke.js`,
`build.ps1`, `measure.ps1` under the study directory. Earlier measurement/build
logs are retained separately. Root module hashes still matched the initial
snapshot at the final audit; no clean-Git claim is made because this checkout
has no Git metadata.

## Root follow-up: rejected trusted identifier compaction

R3's first actual diskless Alpine finite-boundary case still fails. Root tested
whether compacting identifiers in the trusted asset could reduce its startup
cost without changing the guard, timer or worker limits. This experiment was
opt-in under `jsonata_identifiers_candidate`; production and ordinary R3 builds
remain unchanged. All 415 retained baseline files still match their manifest.

Pinned esbuild 0.28.2 reduced the trusted text from 129,423 to 93,891 bytes with
identifier/whitespace compaction, syntax compaction disabled and function names
retained. Three-iteration native benchmarks showed no demonstrated end-to-end
improvement: compilation approximately 9.45 versus 9.35 ms, exact-boundary
evaluation 74.73 versus 77.55 ms. These small samples are not a statistical
performance claim.

More decisively, the unchanged reference
`context/eval_original_local` returned DOWN instead of UP for
`($f:=$eval(?);$f('$expr'))`. The retained implementation's
`getNativeFunctionArguments` derives argument bindings from a native function's
`toString()` text. Keeping function names does not preserve parameter names.
This source mechanism explains why general identifier renaming is unsuitable
without additional semantic changes. Root rejected the experiment and ran no
Alpine retry with it. No regression assertion or timer was relaxed.

The failing log, options, hashes and benchmark results are in
`.canary/jsonata-identifier-study-result.json`. Automatic approval review blocked
deletion of the four experiment files (`blocked by policy`); 97,644 bytes remain
private and opt-in, with the rejection recorded in their metadata. No binaries,
new caches, snapshots or VM artifacts remain.

A read-only `WHvGetCapability(WHvCapabilityCodeHypervisorPresent)` call returned
success with `FALSE` on this host. This is the Windows Hypervisor Platform API
capability, not a claim that Windows has no hypervisor. The capability code and
return shape follow [Microsoft's API documentation](https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/funcs/whvgetcapability).
No Windows feature, registry value or service was changed. Native Linux/Docker
verification remains open while the user's Portainer-managed hosts are unavailable.

## Root follow-up: native guard allocation reduction

Root measured private compiler overlays rather than editing the retained R3
source. Reusing trusted builtin argument slices and using numeric own-index
keys reduced an exact-boundary Windows evaluation sample from about 1.03 million
to 829 thousand allocations. The public-callable variant passed 140 native and
250 focused worker test events. Raw builtin invocation offered only a small
additional speed change and was not selected for the platform trial. Enumerating
descriptor names increased allocation and was rejected. A possible native
descriptor API would require an actual maintained dependency change; no module
cache modification or dependency fork was made.

The selected overlay still failed its first exact-visit Alpine case at
1268.276311 ms, so it remains unadopted. All 415 baseline files and all fixed
resource limits remain unchanged. No native-host or Docker success is implied.
See [the bounded result](bounded-alpine-result.md#guard-allocation-experiment-platform-gate-still-fails)
for the tested source hash, original failure preservation and storage cleanup.

Root subsequently staged an explicit source extension against pinned Goja:
one own-property metadata helper, with all 81 original production files verified
and unchanged. It removes descriptor-object construction. A primitive numeric
callback also removes redundant UTF-8/grammar/conversion scans while retaining
the declared profile and original conversion behavior, including signed zero and
radix quirks. Both formerly failing finite-boundary cases now pass actual Alpine.
The allocation/stopped-worker classification still fails there, so this is not
an adoption recommendation or a cleared resource gate. All staged source and
runtime artifacts are reclaimed; root modules and the production Go runtime
remain unchanged. [Exact results and logging limits](bounded-alpine-result.md#descriptor-api-and-primitive-numeric-candidate)
include the 43-pass continuation, the preserved allocation failure and the
unwaived legacy assertion differences.
