# Worker result collection validation

Validated on 2026-10-22 with the existing Go 1.27.3 Windows amd64 SDK and
portable GCC. Ownership for this change is `internal/probeworker/process/**`,
an append to the worker README, and this document. The dispatcher, engine
fixtures, root scripts, modules, frozen artifacts, UI, node databases, and
shared Alpine VM were not changed by this work.

## Diagnosis and its limits

The old runner attached `streamSink` writers directly to `exec.Cmd.Stdout`
and `Stderr`. Go therefore created output-copy goroutines. A successful OS
process exit did not imply those goroutines had finished. Its independent
10 ms `WaitDelay` could expire while an output consumer was delayed.

Before changing the behavior, private injection delayed the stdout consumer
for 150 ms after parsing the final frame. Three consecutive executions of the
old production runner returned `pending: service worker stopped without a
valid result`, with the following independently observed parent state:

```text
Reaped=true ExitCode=0 ExitSuccess=true Wait=wait-delay
InputComplete=true Context=active
Stdout: Bytes=300 Rejected=false Started=true Final=true
Stderr: Bytes=25 Rejected=false
```

These are actual `cmd.ProcessState` and `cmd.Wait` observations, rather than
the child's intended dispatch return. EOF was not observable through the old
exec-owned copying abstraction. The permanent
`TestExecCopyTimerExpiresAfterSuccessfulExit` retains a standard-library
control: actual exit 0 and valid frames still produce `exec.ErrWaitDelay` when
the copy consumer is held beyond 10 ms.

This proves a concrete false-pending mechanism. It does **not** establish the
exact cause of the earlier JSONata observation in `jsonquery/VALIDATION.md`:
that historical trace did not capture the OS exit status or parent wait error.
No engine retry, expected-outcome change, or in-process fallback was added.

## Repair and acceptance contract

The parent now supplies owned output file handles to `exec.Cmd`, so exec
creates no stdout/stderr-copy goroutines. `cmd.Wait` collects the actual
direct-child exit independently of output scheduling. Two owned collectors
retain the existing 64 KiB per-stream total budgets, 2 KiB candidate frame
limit, nonce binding, state/latency/message validation, and discarded logs.
They use fixed 4 KiB read buffers.

A result is accepted only after successful process exit, successful complete
input delivery, valid ordered frames, and actual EOF without collection errors
on **both** stdout and stderr. The original total context must still be active.
An exit-0 frame is insufficient by itself. Duplicate frames, a truncated
nonce-matching trailing frame at EOF, unexpected states/messages, excessive
output, and nonzero exit after a valid result remain failures.

The 10 ms post-exit held-pipe grace remains. After that interval, a kernel
readiness probe distinguishes buffered data or EOF from an empty pipe whose
writer remains open. Readable data/EOF can finish under the original total
deadline, even when the consumer was delayed; an empty held pipe is closed
and rejected. Probes do not consume output. All collectors and input copying
are joined before admission is released. Context expiry closes the owned
handles and retains kill/reap behavior. `cmd.WaitDelay` itself remains 10 ms;
the repair does not enlarge it or ignore an output failure.

Linux uses zero-timeout `poll` for data/hangup readiness. Windows uses
`PeekNamedPipe`, with **overlapped parent read handles**: synchronous anonymous
handles can block a peek behind a pending read. A real held-writer test caught
that constraint during implementation. Windows output pairs use random local
pipe names, first-instance protection, one client instance, and rejection of
remote clients. The parent connects its writer before launch; only that writer
is passed as the child's standard output/error handle. Go detects the reader's
overlapped mode. There is no pending connect goroutine, new network listener,
or persistent filesystem file. Stdin remains the existing bounded private pipe.

Public APIs `process.Run`, `process.RunConfig`, `process.Handler`, and the wire
protocol are unchanged. No root dispatcher hook is needed. A fixed new
`pending: service worker output incomplete` diagnostic distinguishes failure
to complete output collection; raw errors, command lines, nonces, queries,
documents, evaluated values, and incidental logs are never returned.

Private test observations contain only OS exit code/success/reaping, a fixed
wait category, complete-input and context state, stream counters, frame flags,
actual EOF/read categories, and readiness/held-pipe flags. Injection is private
to the runner and populated by package tests; there is no configuration,
environment, or public API switch to enable it.

The 128 MiB kernel controls, 64 MiB Go soft target, one child GOMAXPROCS, two
admission slots, bounded stdin, fixed executable/argument selection, parent-death
controls, and strict result diagnostics remain. Pre/post SHA-256 comparison
confirmed both executable-factory files and all three resource-limit files
were unchanged. The explicit `canary_worker_race && race` factory variant is
preserved; ordinary production builds cannot select its test artifact.

## Completed evidence

All commands used read-only modules and existing tools. Tests used private
children and loopback fixtures; no external monitoring targets or production
ports were exercised.

* Whole process suite: **PASS**, 41 test/subtest events, zero failures. This
  includes existing CPU cancellation, startup/stdin bounds, admission,
  resource failures, environment absence, child crash/panic, excessive logs,
  parent crash, and reaping controls.
* Gated repair controls: delays before stdout parsing, after the final stdout
  frame, and before stderr parsing all finish with actual exit 0, successful
  wait, complete input, validated frames, and EOF on both streams. Each delay
  is 150 ms, well beyond the unchanged 10 ms grace. Representative repaired
  observations recorded 14 readiness probes and no held-pipe/probe failure.
* Held-writer controls: actual empty open stdout and stderr handles are rejected
  after one grace interval, with `HeldOpen=true`, collector read closed, and
  EOF absent on the held stream. These model inherited output writers without
  launching an uncontrolled descendant. Windows job child-admission limits
  and Linux parent-death setup remain unchanged.
* Malformed-completion controls: valid result followed by nonzero exit 23,
  missing/duplicate/truncated frames, invalid state, private message text, and
  stdout/stderr overflow are rejected. Cancellation during post-exit draining
  still overrides an otherwise valid completed frame.
* Loaded process repetitions: **2,048 children**, eight runs of 256 with two
  admitted workers and two yielding scheduler-load goroutines. Every child
  independently satisfied actual exit 0, wait success, full input, final
  frame, both EOFs, no stream rejection, and active context. **Zero failures
  or pending results.**
* Overlapping JSONata reference repetitions: **14 rounds / 1,064 children**,
  1,078 passing fixture/top-level events, zero failures. Existing expected Go
  outcomes, including documented language differences, were preserved.
* Entire current JSONata suite: **PASS**, 193 test/subtest events. Its existing
  allocation/OOM, output/depth/range, deadline/cancellation, privacy, and fresh
  worker recovery assertions were not changed.
* Focused existing Oracle authentication-cancellation and real TNS/TTC
  query/condition/TLS/mTLS fixtures: **PASS**, nine test/subtest events.
* Whole process suite with a race-instrumented parent and separately compiled
  **uninstrumented, capped** child fixture: **PASS**, 41 events, zero failures
  and zero race warnings. This preserves the root's explicit factory design;
  it is not a claim that child evaluator/driver internals are race-instrumented.
* Native process vet and Linux process vet: **PASS**.
* Linux amd64, `CGO_ENABLED=0`: production node build and process test-executable
  build **PASS**. The node is ELF64 with no interpreter or dynamic segment.
  No runtime execution of this repair on Alpine is claimed; the shared VM was
  left to its current owner. Prior Alpine results describe the prior source.

Evidence is retained outside the checkout in
`%TEMP%\canary-worker-result-validation-20261022-followup\`: full JSON event
logs, stress observations, race-parent results, preserved-file hashes, and an
artifact/source checksum summary. The Linux node SHA-256 is
`282a384738c8b6d7e8f4482add9d5ea9fe9ab05f1c59552d23c2edb1bc78d4f0`;
the Linux process test executable is
`8767b093c71db8619c2b60fe370fdc14ecc4fa3ab0241c78ea881812c0ad1d9d`.
These artifacts include the working node source at build time, rather than
constituting a new frozen protocol checkpoint.

## Reproduction and primary references

From the repository root, run the process suite normally, or select the two
controls to observe the old mechanism and repaired result collection together:

```powershell
& .canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/internal/probeworker/process -count=1 -timeout=90s
& .canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/internal/probeworker/process -run 'Test(ExecCopyTimer|CompletedChildSurvives|KernelReadiness|CompletedFrame)' -count=3 -v
& .canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/internal/probeworker/process -run '^TestCompletedChildrenUnderLoad$' -count=8 -timeout=120s
& .canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/internal/jsonquery -run '^TestOfficialReferenceFixtures$' -count=14 -timeout=120s
```

The root-owned `scripts/worker-race-validation.py` remains the release workflow
for race-instrumented parents and capped uninstrumented children. It was not
edited here. The focused race validation above uses that same existing build
tag/linker factory contract for the process package.

* [Go os/exec](https://pkg.go.dev/os/exec): `Cmd.Wait`, `WaitDelay`, copy
  goroutines, `ErrWaitDelay`, and context cancellation. The installed Go
  1.27.3 `src/os/exec/exec.go` was also inspected directly.
* [Microsoft PeekNamedPipe](https://learn.microsoft.com/en-us/windows/win32/api/namedpipeapi/nf-namedpipeapi-peeknamedpipe):
  readiness inspection and the synchronous-handle blocking caveat.
* [Microsoft ConnectNamedPipe](https://learn.microsoft.com/en-us/windows/win32/api/namedpipeapi/nf-namedpipeapi-connectnamedpipe):
  a client can establish a valid connection with CreateFile before a server
  ConnectNamedPipe call.
* [Linux poll](https://man7.org/linux/man-pages/man2/poll.2.html): zero-timeout
  readiness and hangup with remaining buffered data.
