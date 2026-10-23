# Monitor check policy

`checkpolicy` is a pure, standard-library-only policy engine for one monitor and
one observer. It performs no probes, scheduling, clock reads, persistence,
notification delivery, or message formatting. The runtime now applies it to
accepted local results and persists policy, observation, sequence and history
in one transaction. Retry/inversion integration has focused independent review
coverage in [the integration report](../../../../docs/origin-restore-review.md).

## Go API

```go
func Validate(config Config) error
func (state State) Validate() error
func Evaluate(config Config, state State, raw Status) (State, Result, error)
```

`Status` has constants `Up`, `Down`, `Pending` and `Unknown`. Unknown is an
explicit inability to obtain a measurement, such as unavailable local ICMP
capability. Pending and unknown break the failure streak without reporting
recovery, request retry spacing, and remain unchanged under inversion. Empty,
stale, maintenance or other values return an error. Computed views must not be
passed as observations. Unknown support was added during ICMP runtime integration;
the original engine contract accepted only the other three states.

`Evaluate` consumes exactly one completed observation and returns the next state
and a plain result. Arguments are values and are never mutated. Every error
returns the original input state and a zero result: check the error before using
either output. Validation errors do not echo submitted values. There is no
silent normalization, fallback, or recovery from corrupt state.

## JSON contract and limits

All exported struct fields have the following exact camelCase JSON names; no
fields use `omitempty`. All durations are integer milliseconds.

`Config`:

```json
{"generation":"monitor/check-generation-1","retries":2,"intervalMs":60000,"retryIntervalMs":1000,"timeoutMs":500,"upsideDown":false}
```

- `Generation string`: `generation`, an opaque identity with 1..256 valid UTF-8
  bytes. The caller owns its meaning and issuance.
- `Retries int`: `retries`, 0..100 inclusive. This is the number of consecutive
  effective failures tolerated as pending, before the next failure is down.
- `IntervalMs int64`: `intervalMs`, 100..86,400,000 inclusive.
- `RetryIntervalMs int64`: `retryIntervalMs`, the same limits. It may be shorter
  than, equal to, or longer than the normal interval.
- `TimeoutMs int64`: `timeoutMs`, 25..120,000 inclusive, and no greater than
  **either** interval, including with zero retries.
- `UpsideDown bool`: `upsideDown`, whether to invert raw up/down before policy
  evaluation. Pending is never inverted.

There are no interval or timeout defaults: zero is invalid. `retries:0` and
`upsideDown:false` are valid zero values. Validate before accepting configuration;
`Evaluate` also validates every call. Exported constants document the same limits.

`State`:

```json
{"generation":"monitor/check-generation-1","consecutiveFailures":2}
```

- `Generation string`: `generation`, the same size/encoding limits as Config.
- `ConsecutiveFailures int`: `consecutiveFailures`, 0..101 inclusive, counting
  post-inversion failures. Counts saturate at 101; they are not an unbounded
  lifetime failure total. This cap is independent of the configured threshold.
- The zero state `{"generation":"","consecutiveFailures":0}` is the one
  exception to the nonempty generation rule and explicitly starts a new streak.
  An empty generation with a nonzero count is invalid.

`Result`, for the first raw down in this example:

```json
{"rawState":"down","effectiveState":"pending","consecutiveFailures":1,"nextIntervalMs":1000}
```

- `RawState Status`: `rawState`, the exact accepted observation before inversion.
- `EffectiveState Status`: `effectiveState`, the result after inversion/retries.
- `ConsecutiveFailures int`: `consecutiveFailures`, equal to the next State count.
- `NextIntervalMs int64`: `nextIntervalMs`, the requested spacing between check
  **starts**, not a wall-clock deadline or a delay from completion.

Encoding uses the standard `encoding/json` package. The engine does not decode
JSON or enforce JSON member presence/unknown-member rules; its validators check
the resulting typed values. The persistence adapter must preserve the entire
State and distinguish an absent or damaged record from an intentional reset.

## Transition rules

First invert raw `up` and `down` when `upsideDown` is true; leave raw `pending`
unchanged. Then evaluate that interpreted observation:

- Up returns up, clears the count to zero, and requests `intervalMs`.
- Down increments the count, saturating at 101. Counts 1 through `retries` return
  pending; counts greater than `retries` return down. Every interpreted down
  requests `retryIntervalMs`, including confirmed down and `retries:0`.
- A raw service pending returns pending, clears the count to zero, and requests
  `retryIntervalMs`. It provides no evidence of recovery or failure, so it breaks
  the **consecutive** failure streak. It never returns up or down, including
  when the preceding result was confirmed down or inversion is enabled.

For `retries:2`, raw `down,down,pending,down,down,down,up` produces effective
`pending,pending,pending,pending,pending,down,up` and counts `1,2,0,1,2,3,0`.
Retry-generated pending is distinguishable from service pending by `rawState`.
Never feed an earlier effective state back as a new raw observation.

## Persistence, edits, and runtime integration

1. Maintain one State per monitor **and observer**, serializing updates to that
   pair. Do not share a streak across nodes or monitors, or count the same
   completed probe twice. The engine has no sequence numbers or deduplication.
2. Give Config the check's semantic generation. Change it for target, assertion,
   timeout, retry-count, inversion, or other result-changing edits. Retain it for
   interval, name, or notification-only edits. The engine cannot inspect the
   monitor configuration or detect semantic edits made with an unchanged key.
   Using every document revision as generation would incorrectly reset streaks
   on scheduling edits.
3. A valid State with a different generation is reset before processing the
   current observation. Interval-only edits with the same generation preserve
   all accumulated evidence and use the new interval immediately in the next
   Result. Merely validating edited configuration consumes no observation and
   changes no counter. `State{}` is the explicit caller reset hook.
4. Restore the full State before the next observation after restart. Restoring
   `consecutiveFailures:2` with `retries:2` makes the next interpreted down
   immediately down with count 3. Reconstructing from effective status alone is
   insufficient. Invalid state is rejected even when its generation is old;
   any decision to discard corrupt persistence belongs to the caller.
5. Reject canceled, out-of-order, superseded, disabled, or stale probe work
   **before** calling `Evaluate`. The engine has no causality or freshness data;
   a mismatched generation resets evidence and is not a stale-work detector.
   Pass raw probe status without preapplying runtime inversion. Applying
   inversion twice reverses its meaning.
6. On success, atomically persist the returned State with the accepted result or
   observation. Only then publish it and update scheduling. Keep the raw probe
   message/latency/diagnostics in the caller; the package has no message field.
   Associate the returned spacing with this probe's start time, enforce at most
   one in-flight check, and handle late completion without overlapping probes.
   Computing deadlines, jitter, missed ticks, and restart scheduling is caller
   work. No next-start timestamp is persisted by this engine.
7. Preserve open incidents through **all** pending observations. Pending after
   down does not establish recovery; only effective up can do that. Persist
   incident continuity separately: this State deliberately stores no last-known
   health, outage identity, delivery history, or recovery evidence.
8. Keep failed **notification delivery** attempts and successful-provider repeat
   policies in their existing subsystem. These monitor retries must never cause
   an already accepted provider notification to be retried or change delivery
   backoff, receipts, repeat alerts, or recovery eligibility.

## Pinned Kuma comparison and limitations

The comparison is Uptime Kuma **2.5.6**, commit
`7ca4a3debd76443d8452da95d4040200a1479af4`, matching
[the shared parity audit](../../../../docs/parity-audit.md). The following
findings come from pinned executable source, rather than copying comments or
assuming current master behavior:

- The [monitor model's retry branch](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js#L919-L964)
  makes the first configured number of failures pending, then down, and clears
  retries on a nonthrowing probe path, including service pending. This engine
  preserves that threshold and pending streak-break behavior.
- Kuma [restores retries from the previous heartbeat](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js#L450-L454).
  This engine likewise preserves retry progress when its full State is restored.
- Kuma [inverts before retry handling](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js#L911-L959),
  and its [flipStatus helper](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/util.ts#L169-L179)
  leaves pending unchanged. This engine preserves those semantics explicitly.
- **Deliberate cadence difference:** Kuma starts each beat with
  [the normal interval](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js#L432-L436)
  and [uses retryInterval only for effective pending](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js#L1036-L1055).
  Confirmed down returns to its normal interval. This engine intentionally keeps
  the retry interval for all interpreted failures, including confirmed down.
- **Deliberate boundedness/validation differences:** Kuma's retry branch keeps
  incrementing during down. This engine saturates at 101. Its 100 ms minimum
  interval and explicit timeout bounds match current Canary validation; Kuma's
  [server interval checks](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js#L1631-L1638)
  use a [one-second minimum](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/util.ts#L46).
  This package requires a positive, explicit retry interval and timeout instead
  of copying fallback behavior.

All interpreted downs use the same retry rule. Kuma's special JSON-query
`retryOnlyOnStatusCodeFailure` bypass is not represented by this three-status
API. Maintenance, groups/dependencies, provider delivery/repeat behavior,
incident notifications, and live scheduler integration are outside this package.

## Verification

From the repository root, use the pinned local toolchain. These commands operate
only on this package; they do not test or build the concurrently edited runtime:

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOWORK = 'off'
$env:CGO_ENABLED = '0'
& .canary/tools/go/bin/go.exe test -mod=readonly -count=1 -cover ./apps/node-go/internal/checkpolicy
& .canary/tools/go/bin/go.exe vet -mod=readonly ./apps/node-go/internal/checkpolicy
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
& .canary/tools/go/bin/go.exe test -mod=readonly -c -o NUL ./apps/node-go/internal/checkpolicy
```

The explicit Windows `NUL` output discards the cross-compiled Linux test binary
and avoids writing a default binary. Linux compilation is not Linux execution.
Fixtures cover transitions, restart after each observation, a serialized retry
boundary, inversion, service pending, semantic generation changes, interval
edits, all 101 retry thresholds, saturation, invalid inputs, numeric limits,
timeout constraints, and exact JSON encoding.
