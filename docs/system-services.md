# Local system-service monitoring

Protocol 16 development adds monitor type `system-service`, using the existing
`target` as the **internal service name**. There is no extra options object or
credential. Every assigned node queries its own operating system; selecting two
nodes checks two local service instances. This is not a remote service-manager
connection or a process/PID existence check.

Names contain 1–256 ASCII bytes: the first is alphanumeric or underscore;
remaining characters may additionally contain `.`, `-` and `@`. Whitespace,
paths, wildcards, quotes and leading options are rejected. On Linux the name
receives `.service` when that suffix is absent. Examples are `sshd.service`,
`app@one.service` and Windows `Spooler`. Ordinary interval, timeout, retries,
inversion, assignment, groups, maintenance and tags remain applicable.

## Read-only managers and outcomes

Windows uses local `OpenSCManager`/`OpenService` with only
`SC_MANAGER_CONNECT` and `SERVICE_QUERY_STATUS`, followed by
`QueryServiceStatusEx`. It never starts, stops or reconfigures a service and
uses no PowerShell. A running service is UP; known stopped, paused or transition
states are DOWN; a nonexistent service is DOWN. Permission failures, unavailable
queries and unknown states remain Pending. The status is the manager's latest
reported state, as specified by [Microsoft](https://learn.microsoft.com/en-us/windows/win32/api/winsvc/nf-winsvc-queryservicestatusex).

Linux invokes only a fixed `/usr/bin/systemctl` or `/bin/systemctl`, with literal
arguments for `show --no-pager --all --property=LoadState,ActiveState -- <name>`.
It reads system services only, not user services. It supplies a minimal locale
environment; a caller cannot redirect D-Bus through inherited environment data.
Structured output is limited to 4 KiB, stderr is discarded, and raw manager
diagnostics never enter observations. Active/reloading loaded or masked services
are UP; known nonrunning states and structured not-found services are DOWN.
Malformed output, unknown states, missing helpers, permissions or an unavailable
manager remain Pending. See [systemctl's property interface](https://www.freedesktop.org/software/systemd/man/latest/systemctl.html).

Ordinary Alpine containers have neither the host's systemd nor Windows service
manager. They do not gain host visibility from this monitor; it reports Pending
when its local prerequisite is unavailable. There is no automatic privileged
mode, host mount, shell command or user-selectable executable.

## Isolation and HA

Queries execute through the existing two-slot process runner with unchanged
kernel memory limits, parent deadline, cancellation, kill/reap and framing.
Windows manager calls can block, so they are never made in a node goroutine as
an isolation fallback. Worker saturation or unsupported controls remain Pending.
Linux subprocess output uses an owned pipe and bounded reader. The helper also
uses a parent-death signal tied to a locked worker thread, so forcibly stopping
that worker stops its helper. Normal helper exit is waited; an orphaned killed
helper is reaped by the operating system's init. No measured network
latency is claimed; `latencyAvailable` is false.

The ordinary retry/inversion and incident/delivery policies apply. Pending does
not certify UP or recovery. Renames and tags preserve a check generation;
changing the service name, assignment, timeout, enablement or semantic retry
settings follows the existing generation rules. No new database category or
receipt identity is added. Protocol 16 rejects older peers because those peers
cannot validate or execute this new kind. Upgrade cluster members together.

The pinned comparison is [Uptime Kuma 2.5.6's system-service implementation](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/system-service.js).
Canary uses native Windows query rights and preserves local uncertainty rather
than interpreting every failed manager query as an outage. Linux parser output,
command choice and platform exclusions above are deliberate and explicit.

## Windows development validation

The final development source passes the complete canary (161.028 s),
probeworker (2.952 s), process (4.768 s) and systemservice (2.680 s) suites,
plus vet for all node packages. Linux amd64/arm64 leaf test executables compile
(4,214,944 / 3,997,856 bytes) and are deleted without execution. This sequential
380.734 s guarded command kept source unchanged at v3 fingerprint
`fb798b0674dd92b51a60692b0d1674bfaefb14cba49b20d333db39267b7c5f22`;
sampled scratch peaked at 450,555,588 bytes and returned to zero. No module-cache
growth occurred. Earlier results below retain their exact, narrower source.

Pinned Go 1.27.3 with read-only modules and cgo disabled passed the complete
canary (142.952 s), probeworker (2.878 s) and systemservice (1.717 s) packages
in one 177.750 s guarded command. This included SCM API fixtures, real isolated
synthetic-manager children, strict private payloads, peer replication, SQLite
restart/generation controls and ordinary pending/down/recovery policy. Sampled
scratch peaked at 263,950,907 bytes and returned to zero; module growth was zero.
Source remained unchanged at v3 fingerprint
`b033954be11e55c75b4c81f4cdb15b4457d9e5b6b217b97a44de9ce8d247ff28`.
This earlier run predates the final typed-frame and cancellation refinements;
the final run above includes them.

A source audit added a stalled-manager regression before this full run. It
first failed with `{down service check timed out 1002.4554}` (18.172 s guarded,
fingerprint `5beb09762fb0725d2ee206e73994bd1ced4417a20acb9d0724051281a93ab062`).
The shared runner has a definitive network-engine timeout outcome. This adapter
now maps that exact fixed hard-timeout outcome to Pending, because a manager
that never answered has certified no service state. The unchanged regression
and complete leaf then passed in 7.031 s guarded. A following running fixture
proves the stalled child was reaped and capacity released. Worker limits and
network-probe behavior are unchanged.

Workspace type-checking passed in 9.579 s guarded. All **12** authored pure
interface groups passed in 4.140 s using tracked
`scripts/system-service-pure.mjs`; the handoff's count of 13 was incorrect.
The initial attempt to promote that fixture failed before it existed, so the
first guarded invocation reported a missing module and ran no assertions.
After correcting the source-file promotion, all original assertions passed.

### Worker boundary follow-through

Strengthening the synthetic worker fixture to assert the agreed messages
reproduced another integration gap: the running result was sanitized to
`service check succeeded` (4.328 s guarded, fingerprint
`6e7ec115b051f551878ea2ae581aaf5c2b90bf0f9ada262f0f400221461b755e`).
The common boundary also previously rejected every child Pending frame.
Only `system-service` now admits its four fixed state/message pairs; arbitrary
diagnostics, mismatched pairs, invalid scalars and Pending from other engine
types remain rejected. Original process regressions are unchanged. The leaf
also maps post-start cancellation to Pending and proves capacity is released.
Linux has additional literal-argument/output/deadline and worker-death helper
fixtures. They compiled here; actual Linux and focused race execution are
explicitly included in the new CI selection, not claimed as local passes.

### Production binary and dashboard

One freshly built Windows binary, SHA-256
`25e2cfa1639bac1ff8cd32d0f5f58b38bd1550a5401d5d500efb5de19a5977d5`,
passes the new real-process proof **1/1** (2.016 s execution command) and the
full dashboard harness **143/143**, with zero page exceptions or blocked
requests. The second build has identical binary bytes. The process proof
queries only a randomly named nonexistent local service and checks private
peer configuration, metadata generation, restart and unavailable RTT; it does
not install or change any host service. An unavailable manager is allowed to
remain Pending, so this is not a claim that every platform provides a manager.

Workspace type-checking passes again in 12.422 s. The final 12 pure interface
groups, browser-script syntax and disposable Vite build pass. The harness adds
four groups to the prior 139 assertions: API/form round trip, browser rename,
literal-name validation/details and conflict projection. It now accepts 27
unique types while preserving all earlier 26, sixteen service engines and
eleven providers. This accepted catalogue addition is not a waived failure.

Native preview opened the private app, but its snapshot failed twice with
`Preview automation snapshot timed out after 15000ms.` The documented
Chrome/CDP fallback used one disposable profile and en-US locale. Harness
elapsed time was 159.780 s including the preview wait. App-source manifest
SHA-256 is `f231eed943d045db807bfdee31451c5e052bb382368984fe5972e6eb01557916`;
the harness found no changed inputs. The existing large-bundle notice remains:
the single JS asset is 762,381 bytes, with no raised size threshold.

The combined process/browser command took 250.172 s guarded, source unchanged
at the final v3 fingerprint above. Sampled scratch was 437,278,544 bytes,
returning to zero; idle repository usage was 1,233,705,843 bytes (about
1.15 GiB), with zero module growth. All temporary executables, private databases,
build outputs and the browser profile were reclaimed. No user database,
environment file, running user binary, external provider or VM was used.

Actual Linux/Alpine execution, native arm64, race and complete protocol-16 CI
remain separate gates. These development checks do not certify a tagged
release, all host service states or full Uptime Kuma parity. Existing
protocol-15 evidence does not certify this wave.
