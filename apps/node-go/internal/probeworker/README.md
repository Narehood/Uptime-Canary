# Disposable service probe workers

`Run(ctx context.Context, config services.Config) services.Result` is the parent
compatible typed API. `services.Check(ctx, config)` also automatically isolates
Oracle; callers do not need a core-specific routing hook. The child executes
`services.CheckInWorker` in a new copy of the current executable. There is no
alternate engine, TCP-only Oracle probe, vendor patch, or in-process fallback.

## Parent integration contract

The executable must call `Dispatch(os.Args[1:])` before its ordinary startup.
`cmd/canary/main.go` already does this before CLI parsing, environment options,
database access, or listener startup. `Dispatch` returns `(handled, exitCode)`;
when handled, return that code and exit immediately.

The fixed worker argument is `--canary-probe-worker-v1`. Extra arguments are
rejected. `Run` uses the absolute path from `os.Executable`, no PATH lookup or
shell, and exactly this argument. Do not add a configurable executable or
arguments. Replacing/removing the running executable can prevent checks; keep
the executable available during its lifetime.

The parent should keep calling `services.Check(ctx, c)`: its Oracle branch
always invokes the process boundary, before engine validation or execution.
`probeworker.Run(ctx, c)` remains compatible and uses that same Oracle path;
explicitly routing other engines through it also isolates them. Pass the
original DSN, query, condition, TLS CA, hostname, and client certificate/key
unchanged. `services.CheckInWorker` is the explicit local engine entry used
only by worker dispatch after hard limits and validation. There is no
context/environment bypass marker and no recursive worker spawn.

The dependency chain is `services -> probeworker/process`; the root
`probeworker` adapter imports both. The leaf imports no services package,
including in its tests. Its plain API is `process.Run(ctx, boundedJSON []byte)
process.Result`, with JSON `type` and nanosecond `timeout` fields. The typed
adapters use `process.RunConfig(ctx, timeout, value)`, which admits before
bounded serialization, includes that work in the deadline, and shares the
same two slots. `process.Result` contains state, fixed message, and milliseconds.
`process.Dispatch(args, Handler)` applies controls before reading; the root
handler decodes/validates a `services.Config`, then calls `CheckInWorker`.

Translate the returned state/message/latency directly into the existing core
probe result. Preserve `pending`; capacity/setup failures do not establish a
target outage. Scheduling, retries, notifications, and core liveness remain
the parent's responsibility. No `service_options.go` change is needed.

Any executable that calls `Run` or Oracle `services.Check` needs the same entry dispatch. Go test binaries
are distinct executables: their package's `TestMain` must call `Dispatch` before
`m.Run`. The optional `internal/canary/worker_entry_test.go` provides this for
core integration tests. `services/worker_entry_test.go` is an external
`services_test` TestMain importing the root adapter without an import cycle.
Existing internal service fixtures remain unchanged. This package and the
generic leaf have their own test entries.
Never dispatch through test flags or an inherited secret-bearing environment.

`config.Timeout` must be positive and at most five minutes. The total budget is
the earlier of this duration and the caller's context deadline; it includes
bounded serialization, startup, resource setup, stdin, child validation, the
engine, and result I/O. The absolute deadline travels inside the input envelope.
The parent kills synchronous CPU work on expiry and waits/reaps before returning
or releasing admission. Kernel process creation/termination/reaping and output
pipe cleanup can add OS scheduling overhead; `exec.WaitDelay` caps output pipe
cleanup at 10 ms. `os/exec` cannot preempt a blocked Windows CreateProcess call
before it returns a process handle. Under concurrent Windows test/toolchain
load, a 75 ms startup deadline once returned after 1.194 s and failed the
unchanged one-second contract assertion. The isolated services package passes;
this observed startup overrun remains a Windows wall-clock limitation, not a
claim of a passing stress/startup bound. The actual authentication cancellation
fixture returned 6.2972 ms after cancellation, within its unchanged 50 ms grace.
There are at most two admitted workers per parent process, with no queue.

Result semantics:

* `up` / `down`: a validated engine result, or `down` for invalid configuration.
* `down`, `service check timed out`: the deadline expired after the child's
  nonce-bound `started` acknowledgement, emitted immediately before the engine.
* `down`, `service check canceled`: cancellation after acknowledgement.
* `pending`, latency zero: capacity unavailable, cancellation before acknowledgement, expiry before
  acknowledgement, unsupported/failed resource setup, startup failure, child
  crash, excessive logging, malformed output, or no valid completed result.

The services API preserves its existing total-deadline contract: Oracle
`services.Check` maps expiry before acknowledgement to `down` / `service check
timed out`, including a deadline consumed by process startup. The generic
process API keeps that case `pending`. Admission/resource/startup failures
remain `pending`; no capacity result is converted to target failure. A
pre-canceled call returns `pending` with `service check canceled`, preserving
the existing cancellation diagnostic without spawning a worker.

Completed measurements report parent elapsed milliseconds, including startup.
Child latency must be finite, nonnegative, and within the configured timeout.
An engine `down` may represent driver failure as well as endpoint failure;
worker process failures without a valid engine result remain `pending`.

## Protocol and secrets

One strict JSON request, followed by EOF, is sent over a pipe after parent
resource attachment. The envelope carries version, a fresh 256-bit random
nonce, absolute deadline, and full config. Encoded input is limited to 8 MiB.
A conservative bounded traversal rejects excessive strings/containers before
JSON allocation in the parent; unusually escape-heavy configs may be rejected
earlier. Normal maximum-size inline Oracle TLS PEM fields fit this bound.
No configuration, credentials, nonce, or query is placed in command arguments.

Stdout frames use a fixed version prefix, the request nonce, and bounded JSON
lines. Only an ordered `started` plus `result`, or a pre-start `invalid-config`
frame, is accepted. Other stdout is discarded. Stdout and stderr each have a
64 KiB total budget, including framing; exceeding either cancels the child.
Only a candidate line of at most 2 KiB is retained. Stderr is never retained.
Protocol errors and OS/driver/panic text never become parent diagnostics.

Result states are restricted to `up` and `down`. Messages must be nonempty,
valid UTF-8, printable, and at most 256 bytes. Oracle messages pass a fixed
allowlist of engine diagnostics, preserving query-condition failures. Other
messages become fixed success/failure summaries; the parent validates this
again. Neither arbitrary result text nor incidental logs are returned.

The environment contains only `GOMAXPROCS=1`, `GOMEMLIMIT=64MiB`,
`GOTRACEBACK=none`, and present OS/CA paths `SystemRoot`, `WINDIR`,
`SSL_CERT_FILE`, `SSL_CERT_DIR` (each bounded to 4096 bytes). PATH, HOME, admin
and cluster secrets, proxy variables, Oracle/SQL driver variables, GODEBUG,
and unrelated environment values are absent. Only standard I/O handles are
passed. The worker's working directory is the executable directory.

## Resource capabilities and limits

Linux (kernel 4.7 or newer, including Alpine): before reading stdin, the child
checks kernel support, disables core dumps, and lowers both soft and hard
`RLIMIT_DATA` to at most 128 MiB, retaining stricter inherited limits. It reads
the limits back and fails closed if setup fails. Since Linux 4.7 this kernel
limit covers data allocations through `mmap`, including Go heap allocations.
It is a data-segment/allocation limit, not a total RSS/cgroup accounting limit:
code, shared/file mappings and stacks have different accounting. A small
`RLIMIT_AS` is intentionally avoided because Go reserves substantial virtual
address space. The Go heap target of 64 MiB is an additional soft control.

Linux startup sets `Pdeathsig=SIGKILL` via Go's exec path. The creating parent
OS thread stays locked until the child is reaped, because Linux's parent-death
signal is tied to that thread. The child verifies the signal before config.
Go's fork/exec implementation covers the parent-death setup race. A node running
as container PID 1 is supported. No worker daemon or extra runtime is needed.

Windows: the parent creates a fresh unnamed, non-inheritable Job Object with
a 128 MiB committed-process-memory limit, active-process limit of one, and
kill-on-job-close. It assigns the child before feeding stdin; assignment or
query failure kills/reaps the child and returns `pending`. The child verifies
the immediate job's limits before reading config. It can wait up to five
seconds for the parent assignment; the parent's earlier deadline still kills
it. Nested-job restrictions fail closed. Closing the parent's last job handle,
including on parent crash, terminates the assigned worker.

Windows has a pre-assignment startup window because `os/exec` starts the process
before job assignment. No configuration or target work occurs in this window;
the child waits for the job and fails after five seconds if it is absent.
Go runtime/package initialization occurs before worker dispatch on both OSes;
these startup allocations are not covered by Linux's self-applied limit, or
by the Windows job until assignment. This is not a claim of an atomic
pre-runtime sandbox. No configuration is read before verified hard controls.

Other OSes fail clearly with `pending` resource-control-unavailable; direct
worker dispatch exits 78. There is no soft-limit-only fallback. This provides
resource isolation for monitoring dependencies, not a filesystem, network,
identity, or syscall sandbox. Workers retain the node's OS identity and can
access the network/files permitted to it. Container/cgroup limits may provide
additional deployment-level controls.

## Validation and provenance

Windows Go 1.27.3 tests exercise a real Redis PING and an independent local
TNS/TTC peer through go-ora v3.0.1 (authenticated custom SELECT, condition
mismatch, verified TLS, mTLS, and untrusted CA). The bounded worker fixture is
copied from `internal/services/oracle_test.go`; those engine tests remain
unchanged and are also run separately. This is protocol-fixture evidence, not
an Oracle database/version certification.

Small test-only helpers cover synchronous work, cancellation, reaping,
parent crash, excessive stdout/stderr, arbitrary driver diagnostics, environment
absence, concurrent admission, bad envelopes/results, and resource setup failure.
No helper requests a large allocation. Production has no helper-mode hooks.
Linux CGO-disabled cross-compilation is checked; actual Alpine runtime and
Linux kernel-limit behavior must also be verified by the parent on Linux.

The ordinary Windows worker suites pass with hard controls active, including
parent-crash cleanup. The unchanged Oracle authentication-cancellation review
now runs through the automatic `services.Check` boundary; it passed without
relaxing its 50 ms grace or moving CPU work to a detached goroutine. The entire
services package passes, including the unchanged Kafka metadata controls,
Oracle engine positives, conditions, verified TLS/mTLS, cancellation, malformed
reply, and validation fixtures. Both worker packages pass. Node-wide vet and
the Linux CGO-disabled build pass. A concurrent node-wide run also found new
core partition-causal review failures outside this ownership; report those
separately from service/worker acceptance. The final serialized node-wide run passed services (6.338 s), the root worker
(2.610 s), the generic worker (2.002 s), and every other package except core.
Its four failing core tests were:

* `TestPartitionCausalReviewOwedBriefDownSurvivesWitnessReplacement`
* `TestPartitionCausalReviewLateOpaqueHeaderPreservesAlreadyOwedDown`
* `TestPartitionCausalReviewMixedObserverContinuityConverges`
* `TestPartitionCausalReviewMixedObserverConnectedRepeatDedup`

These core assertions were not edited. No failed test was relaxed or removed.

An additional Windows `-race` subprocess run could not start the instrumented
child within the production memory cap: ThreadSanitizer exited 66 while trying
to allocate 127205376 bytes (Windows error 1455). A separate no-config startup
diagnostic confirmed this before any engine ran. The hard cap was retained.
This is not a passing subprocess race run; use non-instrumented child acceptance
tests and report race instrumentation separately. No test is silently skipped
and no production fallback or larger limit is introduced for instrumentation.

From the repository root with the existing SDK (modules remain read-only):

```powershell
& .canary/tools/go/bin/go.exe test -mod=readonly -p=1 ./apps/node-go/... -count=1 -timeout=180s
& .canary/tools/go/bin/go.exe vet -mod=readonly ./apps/node-go/...
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
& .canary/tools/go/bin/go.exe build -mod=readonly -trimpath -o "$env:TEMP/canary-probeworker-linux" ./apps/node-go/cmd/canary
```

Primary references:

* [Linux getrlimit/setrlimit](https://man7.org/linux/man-pages/man2/getrlimit.2.html):
  hard/soft limits and Linux 4.7 `RLIMIT_DATA` mmap accounting.
* [Linux parent-death signal](https://man7.org/linux/man-pages/man2/PR_SET_PDEATHSIG.2const.html):
  creating-thread semantics and exec/credential caveats.
* [Microsoft Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects):
  assignment, nested jobs, and kill-on-job-close lifecycle.
* [Microsoft extended job limits](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_extended_limit_information):
  process committed-memory accounting.
* [Microsoft job queries](https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-queryinformationjobobject):
  a null handle queries the calling process's immediate job.
* [Go os/exec](https://pkg.go.dev/os/exec): argument execution without a shell,
  context cancellation, mandatory Wait, and WaitDelay.
* [Go runtime/debug memory limit](https://pkg.go.dev/runtime/debug#SetMemoryLimit):
  the Go runtime limit is soft and excludes some non-runtime memory.


## Race tests with bounded worker children

A race-instrumented Go executable needs additional address-space/runtime memory.
Executing that test binary under the unchanged 128 MiB worker cap can stop it
before the Oracle protocol fixture runs. An ordinary `go test -race` services
run on the protocol-8 checkpoint reproduced this failure; it is not a passing
race result and was not worked around by raising the production cap.

Run `python scripts/worker-race-validation.py` to instrument the parent services,
worker adapter and generic process tests while using separately built non-race
children. The runner builds the real production Canary executable and the
process package's private test fixture, hashes both, passes their absolute paths
through explicit test linker settings, runs every test in all three packages,
and verifies source/artifact hashes after execution. No test is skipped.
Child protocol/memory/CPU/deadline/kill/reaping controls remain enabled.

The production executable selector always uses `os.Executable`; no config or
environment can change it. A separate `canary_worker_race && race` build variant
admits the linked production artifact for this test only. The tag cannot compile
in the CGO-disabled release build. The fake-child selector lives solely in
`process_test.go`. Ordinary builds contain neither selector override.

The boundary is explicit: these are **race-instrumented parent tests with
uninstrumented children**. They do not prove the Oracle driver's internal
operations race-free. The children are separately exercised under their actual
production constraints, including the Alpine fixtures. The first complete
Windows runner execution on 2026-10-22 passed 397 test/subtest events with zero
skips, failures or race warnings; source and both artifacts stayed unchanged.
Evidence is `.canary/worker-race/20261022T174620473905Z/`. Container CI uses the
same runner; its new Linux execution remains to be validated.

## Completed-child output validation followup

The current runner owns stdout/stderr pipes and requires real EOF on both
streams. It no longer lets exec's independent 10 ms copy-goroutine timer discard
a valid completed result when its consumer is delayed. The 10 ms empty-held-pipe
check, original total deadline, strict frames/byte budgets, resource controls,
kill/reap, and explicit race factories remain. The Windows parent read handles
are overlapped so a readiness probe cannot block behind their pending read.

The old false-pending mechanism was reproduced with actual exit 0 plus
`exec.ErrWaitDelay` and valid frames. The repair passed 2,048 privately observed
completed children under scheduler load and 1,064 JSONata reference children
with zero pending results, the full process/JSONata suites, capped-child parent
race tests, vet, and Linux static cross-builds. This does not retrospectively
prove the cause of the historical JSONata trace or certify a new Alpine runtime
checkpoint. See [worker-result-validation.md](../../../../docs/worker-result-validation.md)
for the acceptance contract, private observations, exact evidence, and limits.
