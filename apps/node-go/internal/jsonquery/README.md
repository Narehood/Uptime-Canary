# JSONata monitor conditions

Protocol 14 evaluates the official JSONata **2.2.2** source inside the existing
disposable worker. The former Go evaluator is retained for historical regression
coverage; production has no fallback to it. Public `Config`, `Request`, `Check`,
`WorkerHandler` and `CheckInWorker` interfaces are unchanged.

The replacement corrects the independently reproduced matcher, callback,
signature, grouping, replacement and Unicode discrepancies recorded in
[the review](../../../../docs/jsonata-matcher-projection-review.md).
Lookahead, `??` and `?:` now follow the pinned official reference. The selected
10,000-element range limit also applies inside lexical blocks.

## Execution and limits

`Config.Validate` checks shape and sizes only. Compilation, nested `$eval`,
evaluation, trusted runtime initialization and serialization run in the worker,
outside node-state locks. Expressions and documents enter the fixed bridge as
primitive arguments, never as JavaScript program text. No user bindings, host
objects, filesystem, network, environment or process functions are exposed.

The existing worker retains its two-slot admission, 128 MiB kernel memory
control, 64 MiB Go soft target, parent deadline, termination/reaping, bounded
I/O and private status-frame protocol. The VM has a one-second advisory timer
covering initialization as well as evaluation. Native Go builtins can outlast
that advisory timer; the parent deadline remains the hard wall-clock boundary.
Request timeout still includes admission, startup and execution.

The finite profile deliberately rejects some results that unmodified JSONata
can consume to produce a finite result. Its checks run at intermediate AST and
callback boundaries, before nonfinite values can disappear inside a boolean,
comparison or aggregate. Container traversal has depth **128** and **65,536**
visited values per boundary, counting repeated aliases again. Proxies and
accessors are rejected before their content is read. Only privately registered
JSONata procedure identities and actual callable values are opaque.

Input documents are strict UTF-8 JSON up to 1 MiB; expression and expected-value
limits remain 4 KiB. Results must be non-null finite scalar values. Computed
numeric strings have a separate 1 MiB UTF-8 limit. Syntax/evaluation/profile
errors return bounded DOWN; unavailable or stopped workers return Pending.
Diagnostics contain no expression, document or provider credentials.

Operators remain `==`, `!=`, `>`, `>=`, `<`, `<=`, and `contains`. Equality and
contains compare scalar text; ordering requires finite numeric conversion.
This is the project's selected Kuma comparison contract, not JavaScript's
general coercive equality. Time and random builtins are available but should
not be treated as cross-node agreement values.

## Provenance and maintenance

- [jsvm/upstream.json](jsvm/upstream.json) pins JSONata 2.2.2, its upstream
  commit, canonical source, patch and embedded asset hashes.
- [jsvm/jsonata-guard.patch](jsvm/jsonata-guard.patch) records the complete
  upstream-to-canonical change. The embedded file is esbuild 0.28.2 output
  with whitespace minification only; identifiers are preserved.
- [Goja's local source replacement](../../third_party/goja/CANARY_PATCH.json)
  pins the upstream module checksum and 81 original file hashes. Its single
  added metadata helper avoids allocating a descriptor object for every
  visited value. The module cache is never patched.
- Both upstream projects retain their MIT licenses; the generated root
  third-party notices include the linked dependencies and embedded runtime.

The JavaScript asset and descriptor/numeric callbacks derive from the preserved
reviewed candidate. Its raw capture stays private; all source needed for a clean
build is now in the repository. No private identifier-compaction experiment is
enabled or required. Neither Node.js nor a JavaScript package download is needed
in a deployed headless node.

## Validation and upgrade

All 19 Windows native node packages pass, including the unchanged 130-case
matcher/projection review. Vet, module/notice/provenance verification and static
Linux amd64/arm64 builds pass. Linux race/container execution and the full
workflow remain separate gates; these results do not establish a release.

Upgrade all cluster nodes together: protocol 14 peers reject older protocols.
Keep normal data/identity retention and backup rules. This upgrade changes no
monitor option shape or notification destination identity.

[RUNTIME_MIGRATION.md](RUNTIME_MIGRATION.md) records the narrowly changed legacy
expectations and resource-result classification. The prior Go-port README is
preserved as [GO_PORT_HISTORY.md](GO_PORT_HISTORY.md); `FINITE_PROFILE.md`,
`NUMERIC_COMPATIBILITY.md`, `SOURCE_FREEZE.json` and earlier validation/repair
reports describe that historical implementation, not the active JS runtime.
