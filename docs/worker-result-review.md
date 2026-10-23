# Independent worker output-collection review

Reviewed 2026-10-22. **No confirmed correctness blockers found** in the bounded
process output-collection repair at the source identity recorded below.
No production fixes are proposed by this review.

## Source identity and scope

The baseline is the immutable parent snapshot:

```text
.canary/alpine-validation/windows-race/snapshots/0cc233a7be89d24cb8c10bd7f22e5456aa19a9b10ad10674e7a3b3d21111f2d9/source
```

The SHA-256 of its `source-manifest.json` is the snapshot directory name.
All **305 manifest entries** were verified against the frozen source before
and after testing. All 15 existing process Go files matched the working tree
before and after testing. The private source copy also retained all 305 original
hashes; its only addition was `output_review_test.go`.

Working-tree drift relative to the manifest was confined to these six existing
files outside this review's process scope:

```text
apps/node-go/internal/jsonquery/check.go
apps/node-go/internal/jsonquery/number.go
apps/node-go/internal/services/mqtt.go
apps/web/src/serviceSpecs.tsx
apps/web/src/views/MonitorForm.tsx
apps/web/src/views/ServiceFields.tsx
```

Tests and builds used the private baseline copy, not those later source changes.
The parent's prior 1,935 passing Go test events, including its 5.327-second
process run and approximately 161-second core run, are supplied baseline
evidence, not additional runs performed by this reviewer.

Review covered production `run.go`, `output.go`, `protocol.go`, `observation.go`,
Windows/Linux pipe creation and readiness, worker output, executable factories,
and adjacent lifecycle/resource tests. The only new checkout files are
`apps/node-go/internal/probeworker/process/output_review_test.go` and this report.
No existing production files, modules, dispatcher, UI, scripts, fixtures,
environment files, or user databases were edited. No subagents were used, and
the shared Alpine VM was not used or started.

## Correctness assessment

* **Exit, input, and collector ordering:** `run.go:110` keeps the 10 ms
  `WaitDelay`; output is supplied as owned files. `run.go:146` closes the parent's
  output writers after start. Credentials are written only after guard
  attachment (`run.go:158`). `run.go:190` waits for the direct child's actual OS
  status, then joins stdin copying and both output collectors before accepting
  a result. Attachment failure also kills/reaps and joins both collectors.
  The deferred I/O callback join and guard cleanup precede admission release.
* **EOF and held writers:** `output.go:63` waits for actual EOF on both streams.
  The post-exit timer probes readiness without consuming output. An empty
  inherited writer causes closure/rejection; buffered bytes and EOF can drain
  under the original total deadline. Both normal completion and abort join the
  collectors. Existing stdout/stderr held-writer tests and 150 ms consumer-delay
  tests pass on native and race-instrumented Windows parents.
* **Platform handles:** `pipe_windows.go:30` creates an overlapped inbound reader,
  a random local name, first-instance protection, a single instance, and remote
  client rejection. Its writer is connected before launch. `pipeReadable` uses
  `SyscallConn.Control`, preserving the read handle's runtime integration.
  The installed Go 1.27.3 `os/file_windows.go` detects asynchronous handles in
  `os.NewFile`, and `internal/poll/fd_windows.go` cancels pending pipe I/O on
  close. Windows tests exercise pending reads, cancellation, buffered peeks,
  and EOF. `pipe_linux.go:14` uses zero-timeout poll with data/hangup/error
  readiness and handles EINTR without consuming bytes; Linux verification
  here is source review, vet, and compilation only.
* **Budgets and protocol:** `output.go:30` retains fixed 4 KiB collector buffers.
  The sink retains the independent 64 KiB stream budgets, 2 KiB candidate bound,
  ordered nonce-bound frames, state/message/latency checks, and discarded logs.
  EOF finalization rejects a nonce-matching truncated trailing frame. A final
  frame cannot override nonzero exit, incomplete stdin, malformed completion,
  cancellation, or absent stream EOF.
* **Admission and cancellation:** two admitted runs with collectors deliberately
  held in a test writer retain both slots after cancellation. A third run
  receives the fixed capacity diagnostic. Slots are released after the gates
  open and the collectors join. Thirty-two separate cancellations with both
  pipe writers still live also terminate and join both pending reads.
* **Diagnostics and factory boundary:** runner failure paths return fixed
  diagnostics; accepted results pass `validResult` and `safeMessage`. Private
  trailing logs and raw-error text do not escape the result. The default build
  selects `executable.go`, whose factory returns `os.Executable`, and excludes
  `executable_raceharness.go`. Production symbol inspection found no review
  helpers, test executable factory, or race artifact selection variables.
  The test factory requires the explicit `canary_worker_race && race` variant
  and linker-selected artifacts; there is no runtime environment executable
  override. Resource-limit files and both executable-factory files retain
  their frozen hashes.

## Independent tests and results

The added test file contributes seven top-level tests and six subtests:

* `output_review_test.go:116`: exactly 64 KiB on **each** stream succeeds;
  one additional byte on either stream fails with `service worker output
  rejected`. A missing final newline and oversized trailing matching frame
  fail. Private trailing logs remain absent from the successful result.
* `output_review_test.go:144`: a controlled child emits complete valid frames
  and exits 0 after reading only 128 bytes of a 512 KiB padded request.
  Actual wait succeeds and both streams reach EOF, but incomplete input
  delivery still produces the fixed invalid-result diagnostic.
* `output_review_test.go:165`: 32 readiness probes preserve all buffered bytes;
  EOF remains readable after the writer closes and those bytes are consumed.
* `output_review_test.go:192`: 32 cancellations close and join both collectors
  while their writers remain open.
* `output_review_test.go:233`: two blocked collectors retain admission through
  cancellation until both runs join; a third admission is rejected.
* `output_review_test.go:288`: every two-part split of valid frames parses
  correctly, and byte-at-a-time matching truncated suffixes fail at EOF.
* `output_review_test.go:326`: a collector short write cannot count as EOF or
  successful collection.

Results from the private baseline copy:

* Full native Windows process suite: **PASS, 54 test/subtest events**, no failures
  or skips; Go-reported package time **11.918 seconds**.
* Full Windows process suite with a race-instrumented parent: **PASS, 54 events**,
  no failures, skips, or race warnings; package time **11.917 seconds**.
* Both full suites include the existing 256-child load test, CPU deadline and
  cancellation/reaping, blocked stdin/startup, admission, guard failures, parent
  crash, malformed output, delayed consumers, held writers, and stdlib
  `exec.ErrWaitDelay` mechanism control.
* Native process vet and Linux amd64 process vet: **PASS**.
* Windows CGO-disabled production worker and process fixture builds: **PASS**.
* Linux amd64 CGO-disabled production node and process test executable builds:
  **PASS**. Both ELF64 artifacts have no interpreter or dynamic segment.

The race run used the existing documented harness environment and build
contract: portable GCC, explicit build tag, and linker-fixed paths to separately
built **CGO-disabled, uninstrumented children**. The 128 MiB kernel cap, 64 MiB
Go soft target, one child GOMAXPROCS, bounded input, and two admission slots were
preserved. Child driver/evaluator internals were not race-instrumented.
All commands used existing tools, read-only module mode, and disabled Go proxy
and checksum-server access. Fixtures used private children and loopback only.

Raw logs, commands, summaries, private source, and artifacts are retained in:

```text
%TEMP%\canary-output-review-20261022-7rumgfj7\
  baseline-verification.json
  final-verification.json
  native-full.log
  race-full.log
  native-summary.json
  race-summary.json
  linux-summary.json
  factory-selection.json
  production-symbols.log
  run_review.py
```

`python <that-directory>\run_review.py native`, `race`, or `linux` reproduces
each group from the retained private source. An initial metadata summarizer
error occurred after native tests and vet passed: it treated multi-line
`go list -json` output as test events. The private summarizer was corrected;
the saved complete JSON was parsed independently to verify factory selection.
This was not a process-test failure.

## SHA-256 verification

For each existing process file below, **frozen baseline = private tested copy =
working tree**, verified again after execution:

```text
executable.go              ea267640a37930e43aeeedebd1d08b35bd94fc47cbce24b8a0b997181a6b3853
executable_raceharness.go  e636a8da4d989d4841e9fb72b79215d76f638e69d9443e38319b90542a3f5351
limits_linux.go            a493cc16150e4ddef24cffc79c3eff990903b372ee969f0a67f2b96e7e70ad2d
limits_unsupported.go      a32ae996a6bccf425fbb5950b06ef89fb554e8204925031586828d41a1b6e770
limits_windows.go          8e42b647b3c77c49f197748043956af5ad75f5f8b74646381cb78d86c27e0f77
observation.go             e0e06fe9987e8f4d87193c213f63dff402bf1ae4c7fedfdfb18330398304427f
output.go                  7b3539bf16c0300d9cd3d8e514983236deea732fb69b6c967e73fc8db1141166
pipe_linux.go              52dfc3e04cb9ba660739306aa21f8b2fb875f7396b31e344117bec4c3cecfbb1
pipe_unsupported.go        affa95238fe391254e9fd706d456a20458f2854cfd20cea660fc9cca15fa3a73
pipe_windows.go            8c99544f4d4e0e9782e7bfc47430144a9d3d3c15454e9e083b33bd0ea7f23a65
process_test.go            df7eb22e1c0f71bc091f92f64bf1b8f48ceef853258f0dcd49bdef0df77059ca
protocol.go                a2985259bf8f38247b0048d73485dddd440572e95ad0e5f866ac5fb96b173dba
result_validation_test.go  8f734f51bf9b6c29952c90ae6d3d214b9d25fd7fd6772441feea3a9db44706d8
run.go                     857f64670ac3e9023753b288b1ccdf1d47cfd229ad1b4725d496f4585c947f01
worker.go                  9750dce6aebbf9b6888a6d57c4109b478b9c5d42bc12a0e5804b3f14b88aefe5
```

The new review test SHA-256, identical in the private copy and working tree:

```text
output_review_test.go e58977857972b44851641e854aeda2407afedf043464dff67e98251048d9ad10
```

Built artifacts:

```text
canary-production-worker.exe  2c6ca87646f27559cb572f3665abb4a85b06177f5c063c3f857288fd5b7488e7
process-review-fixture.exe    02974a0012159716add0910ed8fa7d194ea91e6db26df3aa931f68486b470467
canary-review-linux           a61a7176957cacbc87f7af3367ceedb55df75f2b39a05cbfa00b23d71615f0c2
process-review-linux.test     f9e942589f6b958e3e62b93e9dd88a476768c9130bdc9afa8a818dce25626b0b
```

## Limits of this conclusion

No repaired Linux/Alpine runtime execution was performed here. The shared VM
remains with its owner; static builds do not establish runtime pipe or
parent-death behavior on Alpine. Independent runtime evidence here is Windows
amd64 on Go 1.27.3, including the race-parent boundary described above.
Held-writer tests retain real output handles without launching uncontrolled
descendants; existing parent-crash tests exercise worker lifetime separately.

The implementation agent's larger stress, JSONata reference, Oracle, and
whole-repository results were not repeated outside process scope. The historical
1/1,064 JSONata observation remains unattributed without an OS exit/wait trace;
this review does not claim the repaired mechanism caused that historical event.
Later shared MQTT, numeric, and UI source changes are not covered by these
baseline test results. Any revised production repair requires a new review
round against its own recorded source identity.
