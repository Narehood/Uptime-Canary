# Official runtime migration: protocol 14

The Go-port implementation passed its earlier repair fixtures but the next
independent 130-case review still reproduced 40 false-UP results and 20 finite
DOWN discrepancies. That review file remains unchanged. The active runtime now
uses the previously reviewed official JSONata 2.2.2 candidate with its bounded
finite-intermediate profile and optimized descriptor/numeric callbacks.

This is a coordinated semantic upgrade. The wire protocol is 14 so old and new
evaluators cannot silently participate in the same cluster. Public request and
monitor option shapes remain unchanged. Process limits are unchanged.

## Exact legacy expectation changes

The [independent candidate review](../../../../docs/jsonata-candidate-review.md)
verified four old expectations against unmodified official JSONata 2.2.2 and
recommended these narrow changes at adoption:

| Expression | Former expectation | Active expectation |
| --- | --- | --- |
| `$contains('abc', /a(?=b)/)` | DOWN: Go RE2 lacks lookahead | UP for `true`, matching official JS |
| `missing ?? 'fallback'` | DOWN: old parser lacks coalescing | UP for `"fallback"`, matching official JS |
| `'' ?: 'fallback'` | DOWN: old parser lacks Elvis | UP for `"fallback"`, matching official JS |
| `($n := 10001; $count([1..$n]))` | UP: old lexical frame bypassed the selected limit | DOWN: range cap applies inside the block |

Only the three obsolete Go-gap override fields were removed from
`testdata/fixtures.json`; the pinned official observations remain intact.
`TestRangeLimitSourceGap` now enforces the selected limit. All other stored
reference expectations and independent review fixtures remain unchanged.
The old expressions, overrides and behavior are retained in Git history and
the historical review reports. There is no blanket failure waiver.

## Huge allocation outcome

The historical allocation control evaluates
`$length($pad('x', 268435456, 'x'))`. Its assertion assumed that exceeding the
kernel memory limit must terminate the worker and yield Pending. Actual Alpine
runs also produced bounded DOWN at the parent deadline with the same 128 MiB
data limit. This result is preserved in
[the bounded Alpine report](../../../../docs/bounded-alpine-result.md).

That assumption is not portable: Linux `RLIMIT_DATA` makes allocation syscalls
fail with `ENOMEM`; it does not require immediate process termination.
[Linux man-pages: getrlimit](https://man7.org/linux/man-pages/man2/getrlimit.2.html).
Goja also documents that its interrupt cannot stop a native Go builtin in
progress. The existing parent's deadline/kill/reap path is the hard bound.
[Goja interrupt contract](../../third_party/goja/runtime.go).

The control now accepts either a stopped-worker Pending or a fixed bounded
DOWN/error, still requires completion within five seconds, and always checks
that a fresh successful request can acquire the worker slot afterward. Its
expected scalar is now **268435456**, the value a successful unconstrained
execution would return. Thus merely completing the oversized allocation and
missing an unrelated condition cannot pass the resource test. Kernel-policy
and stopped-worker controls in `probeworker/process` remain unchanged.

## Evidence boundaries

All 19 Windows native node packages pass on the new source, including
`jsonquery/jsvm` and `jsonquery`. Vet, module/notice/provenance verification and
static Linux amd64/arm64 builds pass. Canonical source and callbacks preserve the original one-second
advisory VM timer, depth/visit/range limits, parent deadline, worker count and
memory cap. The root module keeps `x/crypto` pinned at v0.58.0.

Native integration and actual Linux/Alpine race/container runs are separate
gates. The previous C1/R2 failures, R3 reports and old source freeze are retained;
they are not relabeled as passing or as current production evidence.
