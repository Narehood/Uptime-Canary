# Diskless Alpine checkpoint

R3 remains private: the actual Alpine runtime gate failed. This is not Docker,
production-node, native-Linux-host, arm64 or release validation.

Root ran `scripts/alpine-bounded-runtime.py` through the bounded runner against
the preserved R3 source, with no source edits during execution. Its Go/JS/JSON
input fingerprint before and after was
`137b70b728e63e0d1d738498fa4c2bc76e4c6d85f5ebd29a844be9a6c48a5ffa`.
The static Linux/amd64 query test SHA256 was
`05f31c397fb36d95a1d25e56731c743cd572cca1ad8509f02a201bb150b65a65`.

## Actual result

The first selected test executed unchanged:

```text
TestMatcherProjectionReviewReference/finite_controls/exact_visit_budget_document
observed=down want=up
state=down want=up message="service check failed" latency=1269.794764
--- FAIL: TestMatcherProjectionReviewReference (1.43s)
    --- FAIL: TestMatcherProjectionReviewReference/finite_controls/exact_visit_budget_document (1.30s)
```

The command returned exit 1. There were zero passing events, two failure events
(the assertion and its enclosing function), and no skips. Later selectors did
not execute because the guest script stops at the first failure. No full-suite,
numeric-string, allocation-recovery, one-MiB or new 33-case worker success is
claimed on this runtime. Windows results remain separately recorded in
[the independent review](jsonata-native-runtime-review.md).

The generic private error does not establish whether time, allocation or another
evaluation bound caused this failure. TCG performance is a limitation of this
environment; it is not a basis for relabeling the result or raising the
one-second evaluation deadline. The two-worker slots, 128 MiB data limit and
64 MiB Go soft target were unchanged in the supplied source. Worker limits were
not separately sampled in this invocation.

## Runtime and storage

QEMU 11.1.0 used TCG, two virtual CPUs and one GiB guest RAM. Alpine 3.24.3 ran
kernel 6.18.58-0-virt from a hash-verified, read-only ISO. There was **no writable
virtual disk**, Docker store, host installation, SSH key or persistent guest
state. The exact executable, JSON fixtures and three signed Alpine packages
were transferred into guest RAM and SHA256-checked. Alpine's own package
verification remained enabled; `--force-non-repository` only acknowledged that
these volatile local packages disappear on reboot.

The selected QEMU executable/DLL/BIOS subset was 127,408,309 bytes. Installer
and extractor hashes matched the retained published integrity record. Only
selected archive members were extracted. The installer was removed before
acquiring the ISO. Windows uses an inherited process error mode for the owned
QEMU child because `dump-guest-core=off` is unsupported on that host; no registry
or persistent error-reporting setting changed.

The test ran inside a private mount namespace and chroot with read-only base
and input binds and a writable private tmpfs. Parent identity was UID/GID
65534, umask 0022, empty supplemental groups, NoNewPrivs 1 and all five capability
sets zero. This is Linux containment evidence, not a substitute for testing the
original Dockerfiles.

The final guarded run took 92.062 seconds. Sampled peak repository size was
1,607,106,175 bytes and disposable output/scratch 357,870,572 bytes. After
cleanup: repository 1,249,731,607 bytes, owned cache 268,322,966 bytes, scratch
zero, module-cache growth zero, external compiler cache 14,702 bytes, and
`pauseRequired: false`. Peaks are periodic observations, not instantaneous
allocation guarantees. QEMU exited and its loopback server closed before
cleanup removed the tools, ISO, packages and executable.

Raw evidence is one bounded `.canary/alpine-bounded-runtime.log`; artifact
hashes/argv are `.canary/alpine-bounded-result.json`. These files are overwritten
by a future deliberately authorized checkpoint rather than accumulated into
binary or VM collections.

Earlier preparation attempts failed before any test executed: archive-line and
PE-import parsing, an optional firmware assumption, an unsupported Windows
QEMU option, conservative host-memory reservation, diskless package admission,
Windows text encoding and Alpine's `/bin/setpriv` location. Each attempt stopped
its owned processes and reclaimed its output. Their corrections changed only
the validation controller; no test assertion or worker resource limit changed.

The user has Linux Docker hosts managed through Portainer but is away from the
desk. No remote credentials, connection, upload or deployment has been used.
Native-host fit and actual Docker builds remain pending. Other implementation
work can continue within the storage budget.

## Guard allocation experiment: platform gate still fails

Root tested a private compiler overlay that reuses synchronous builtin-call
argument slices and passes internally generated array indexes as numeric keys.
The pinned Goja implementation dispatches those keys to own-index lookup. No
traversed object is exported or read with `Get`, and the descriptor, proxy,
accessor, cancellation, depth, visit and procedure-identity checks remain.
The overlay SHA256 is
`cd9667ed139d605a2ae40da03bd495fe25455438dc9c6d640c9947a22d2a508c`.
All 415 original files stayed byte-identical; production was not changed.

Ten-iteration Windows measurements at the exact array boundary showed about
60.1 MB / 1,026,405 allocations for the baseline versus 54.0 MB / 829,130 for
the overlay. End-to-end evaluation measured 76.0 versus 68.5 ms. These are
small local benchmark samples, not a platform or worst-case guarantee.
The overlaid native suite passed 140 test/subtest/package events, and focused
worker compatibility/resource tests passed another 250, with zero failures
or skips. Five new native subcases cover canonical versus noncanonical numeric
keys, high sparse positions, non-enumerable accessors, reentrant descriptor
construction and recovery after failed calls. The known legacy assertion
differences remain separate; this was not a clean full-leaf release run.

One new diskless Alpine invocation used that exact overlay and the unchanged
controller, selectors and resource limits. Its first case again failed:

```text
TestMatcherProjectionReviewReference/finite_controls/exact_visit_budget_document
observed=down want=up
state=down want=up message="service check failed" latency=1268.276311
```

There were zero passing events, two failure events and zero skips. The six
later selectors stopped without running. The static test executable SHA256 was
`61358f4a3dcffb820cce197b90f14ef4bdcb7c8bdbd879494b7e796355869619`.
This does not establish the precise reason for the generic worker error, clear
the native-Linux/Docker gate, or justify another unchanged-artifact retry.
The overlay remains private and unadopted.

The guarded command took 110.078 seconds. Sampled peaks were 1,716,873,064
repository bytes and 357,876,261 scratch/output bytes. Final repository size
was 1,249,666,447 bytes, scratch was zero, module-cache growth was zero and
`pauseRequired` was false. QEMU stopped and all acquired tools, ISO, packages,
overlay files and executables were reclaimed. The original failure remains in
`.canary/alpine-bounded-r3-original-result.json` and
`.canary/alpine-bounded-r3-original.log`; the latest overlay result and log use
the existing `alpine-bounded-result.json` and `alpine-bounded-runtime.log`
paths. Allocation measurements and native checks are small JSON records named
`jsonata-numeric-index-study.json` and `jsonata-guard-compatibility-result.json`.

## Descriptor API and primitive numeric candidate

A subsequent private candidate makes an explicit local Goja module replacement.
It reconstructs 81 production/license/module files, 1,466,627 bytes, from the
pinned archive and adds one own-property metadata helper. It does not copy a
module/build cache or alter the cached module. The archive's Go `h1` content
hash matches the frozen `go.sum`. The source replacement, compiler overlays and
all generated assets live only in guarded scratch and are removed afterward.
Production modules, the current Go evaluator and all 415 R3 baseline files
remain unchanged.

The helper reads own data-property storage and flags accessors without creating
a JavaScript descriptor object. The caller still rejects proxies before any
property operation. Existing prototype-poisoning behavior uses the original
captured-builtin path. At the exact array boundary, ten-iteration Windows
samples measured 58.2 ms / 917,405 allocations for the old guard versus
0.68 ms / two allocations for this helper. Full evaluation measured 74.1 versus
18.1 ms. Thirty new API events and all 134 retained native events pass.

This candidate cleared the original Alpine visit-budget assertion but exposed
the next numeric-string failure. A primitive-only private callback now performs
linear UTF-8 counting and exact numeric grammar scans. For a complete validated
decimal string, it uses Goja's same `strconv` float conversion without repeating
the builtin's prefix regexp. It preserves signed zero, overflow handling,
radix-regexp anchoring quirks, original BigInt conversion and lexical `$number`
shadowing. It is a private bootstrap argument, never a global or expression
binding. The canonical standalone JS fallback remains intact.

The resulting native run passes 146 events, including differential grammar and
value checks, UTF-8 boundaries, cancellation, object-coercion refusal and private
binding checks. Maximum numeric-string evaluation measured 34.1 ms locally,
versus 113.5 ms with scans optimized but the redundant conversion regex intact.
The full worker package records 933 passing events and six failing events:
four previously disclosed legacy assertions plus their enclosing function and
package. Those assertions concern lookahead, coalesce, Elvis and the old
variable-range limit expectation. They have not been edited or waived. Vet and
static Linux amd64/arm64 library builds pass; arm64 was not executed.

### Actual Alpine outcome

The combined descriptor/numeric artifact SHA256 is
`5d1b73f28788bc1347a762f182b6aad4af05fc94acec1c841e5d7adb316f8225`.
Its first invocation records 12 passing and two failing test events, zero skips:

- The exact visit-budget case passes in 0.87 s.
- All numeric-string/privacy subcases pass; the maximum-string case takes
  0.84 s.
- Range, depth and output resource controls pass.
- Allocation returns DOWN / `service check timed out` at 4009.977877 ms;
  the retained assertion expects Pending / stopped worker. It fails, and that
  subcase's later capacity-recovery check is not reached.

The controller then checked only the four previously unexecuted selectors,
without retrying allocation. The reconstructed executable hash is identical.
Those selectors record 43 passing events and zero failures/skips, covering the
one-MiB document, container-profile boundaries, exact range and independent
procedure/numeric/isolation checks. This is **not a clean combined gate**:
allocation remains unresolved and the candidate is unadopted.

The first run's full raw log remains in
`.canary/alpine-bounded-descriptor-numeric.log` with its structured result.
Root accidentally overwrote the follow-up raw log when a subsequent guarded
command opened `latest.log`. The follow-up structured controller result,
43-pass count, exact selectors, executable hash, source verification and cleanup
result survived in `.canary/alpine-bounded-result.json`. No full raw transcript
or unchanged-artifact rerun is claimed for that follow-up. The controller now
saves its bounded serial bytes directly before cleanup; six preparation/logging
unit tests pass, including an independent-next-log overwrite regression.

The large-allocation timeout does not establish whether allocation retries, GC
or another native operation stalled. The native string-repeat implementation
allocates its buffer and loops without interpreter checkpoints; identifying the
actual stalled phase remains necessary. Neither the four-second request deadline
nor the one-second VM timer, 128 MiB kernel data cap, 64 MiB Go soft target or
two-worker capacity changed. No result/expectation was rewritten to clear it.

Both VMs stopped. The sampled scratch peaks stayed below 361 MB; all downloads,
staged dependency sources, overlays, executables and guest state were removed.
Shared module-cache growth remains zero. Only bounded text/source evidence and
the single trimmed compiler cache remain. Reproduction uses
`scripts/jsonata_descriptor_candidate.py` inside the storage guard; numeric
source is `scripts/jsonata_numeric_candidate.go.txt`. These development helpers
do not enable or import the candidate in production.
