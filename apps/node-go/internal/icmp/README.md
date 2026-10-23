# System ICMP engine

This standalone package supplies system-ping monitoring; the parent application
must integrate configuration, state handling and capability reporting separately.
It has no third-party imports and does not open raw sockets, install utilities,
change capabilities, or elevate privileges.

```go
payload := 32
c := icmp.Config{
    Target: "127.0.0.1", Timeout: 3 * time.Second,
    Count: 3, Size: &payload, Family: "ipv4",
}
err := icmp.Validate(c)
args, err := icmp.Arguments(c, "linux")
result := icmp.Check(ctx, c)
```

The public API is `Config`, `Validate(Config) error`,
`Arguments(c Config, platform string) ([]string, error)`, and
`Check(context.Context, Config) Result`.
`Result` contains `State string`, `LatencyMs float64`, `LatencyAvailable bool`, and
`Message string`. Exported state constants are `Up`, `Down`, and `Unknown`, with
values `up`, `down`, and `unknown`.

## Bounds and validation

- Target is 1–253 bytes: an IP literal without a zone, or an ASCII hostname with
  1–63 byte labels, alphanumeric label ends and internal hyphens. A final dot and
  single-label hosts are allowed. This matches Canary's existing `pingTarget`
  grammar, including numeric-looking hostnames. No trimming, URL, port, brackets,
  underscore, whitespace, control character, shell syntax or option prefix is
  accepted. Unicode names must be supplied as ASCII/Punycode.
- Timeout must be positive and at most 120 seconds. It has no implicit default.
  A single Go context covers launch, name resolution, spacing, every packet and
  response processing. An earlier caller deadline wins. There is no per-packet
  reset or retry outside this budget.
- Count zero defaults to 1; explicit counts are 1–10. Count is the requested
  maximum, not a promise all packets fit inside the deadline.
- Size is ICMP data bytes, excluding ICMP/IP headers: nil defaults to 32, and a
  non-nil integer permits 0–65500 inclusive. Explicit zero remains zero.
- Family is empty/`auto`, `ipv4` or `ipv6` (case-sensitive). Literal/family mismatch
  is rejected, treating IPv4-mapped literals as IPv4. Hostname resolution in auto
  mode uses the system utility's selection; it does not try both families.
- Diagnostics are fixed strings without target, arguments, raw output or error
  values. Validation errors identify the field without echoing its value.

## Execution and platform differences

Only fixed system paths are eligible. Linux tries `/bin/ping` then `/usr/bin/ping`;
Darwin uses `/sbin/ping`. Windows obtains the OS system directory using
`GetSystemDirectoryW` and executes its `PING.EXE`, hiding the subprocess window.
Working-directory and PATH executable shadowing cannot select a program. No
command shell or user-defined switches/executable path is exposed.

The combined stdout/stderr capture retains at most 16384 bytes. Overflow cancels
the child and reports unknown; excess output is discarded. Cancellation kills
the process. Pipe cleanup is limited by `exec.Cmd.WaitDelay` to 100 ms; OS process
reaping and scheduling can add cleanup time after the deadline. No more packets
are intentionally allowed after context cancellation.

The child's explicit environment is an allowlist: PATH, SYSTEMROOT and WINDIR
when present, plus `LC_ALL=C`, `LANG=C`, and `LANGUAGE=C`. Application secrets,
proxy settings and loader hooks are not copied. Windows UI language is not
changed by these locale variables.

- **Linux iputils and BusyBox fancy ping:** `-n -c N -s SIZE -i 0.2 -W SECONDS
  -w SECONDS`, optional `-4`/`-6`, then `-- TARGET`. Seconds round upward; Go's
  context still enforces the exact shorter budget. Their `-W` semantics differ,
  so it is a fallback wait bound, not an independently configurable per-packet
  timeout. The 200 ms spacing suits ordinary unicast without flood privileges.
  Multicast/broadcast and unusually restricted binaries can reject the options;
  these are unknown local execution outcomes. Minimal BusyBox without fancy
  ping or fractional-duration support, IPv6-disabled builds, and other Linux ping
  implementations are unsupported.
  BusyBox accepts numeric `-n` even though its help omits it, and supports the
  fractional interval without a UID check in the ping applet. Container socket
  permission still depends on the image, user and host kernel.
- **Windows:** `-n N -l SIZE -w MILLISECONDS`, optional `-4`/`-6`, then TARGET.
  Milliseconds round upward. Windows' native packet spacing (about one second)
  remains in effect: a short deadline can interrupt even a healthy multi-packet
  check. Reverse lookup is not enabled.
- **Darwin:** IPv4 only; IPv6 literals and `Family: "ipv6"` are explicitly
  rejected by Arguments/Check. Auto hostname checks use Darwin's IPv4 ping.
  Uses numeric output, count, size, a conservative one-second interval,
  millisecond `-W`, second `-t` and `--`. IPv6 through `ping6` is not implemented.
- **Other platforms:** explicitly unsupported.

## Evidence and latency policy

At least one verified echo reply means up, including partial loss, nonzero exit
status, or exhaustion of the configured budget after a reply. An explicit
caller cancellation/deadline always returns unknown, even after a reply. Missing
executable, launch failure, output overflow, known permission/socket/send/resource
errors, unsupported output and unverified timeouts return unknown. Parent
integration must preserve unknown as an inability to establish health, rather
than silently turning it into a verified target outage.

Down requires parsed evidence: Unix positive transmitted count with zero received,
or supported Windows timeout/unreachable output, with an ordinary result/exit-1
or configured deadline. Bare nonzero exit, elapsed deadline, successful exit, and
Windows' received count alone are insufficient. Windows can count unreachable
error messages as received packets while exiting successfully.

Linux/Darwin use numeric echo lines or the C-locale packet summary for reply
evidence. RTT comes from a valid min/average/max summary when present, otherwise
the arithmetic mean of exact numeric echo times captured so far. Duplicate echo
lines follow the system utility's summary policy; without a summary, every exact
sample in captured reply lines contributes. Small payloads may not contain the
Unix utility's timestamp, so up can have unavailable latency.

Windows reply parsing requires a numeric source address and one ASCII `ms` time
token using `=` or `<`; IPv6 does not require TTL. Translated words, OEM-encoded
labels, dot/comma decimals and source addresses at the start of a line are
supported. A summary with three numeric `=...ms` tokens is read in Windows'
minimum/maximum/average order, but only after reply evidence. This preserves
Windows' own rounded millisecond average, including zero. An isolated `<1ms`
establishes a reply but is not converted to a fabricated numeric RTT. Without a
summary, exact `=` samples alone contribute to the average; censored `<` samples
are excluded. Other units, non-ASCII digits or changed numeric layouts remain
unsupported. No-reply Windows diagnostics currently recognize English timeout
and host/net-unreachable phrases; other languages return unknown without reply
evidence. This is an explicit locale limitation.

`LatencyAvailable == false` means `LatencyMs == 0` is a placeholder. When true,
zero is an actual system-reported value. Startup/process wall time is never RTT.
If a Unix utility buffers output and is killed before flushing, a real reply
may be unobservable: the result stays unknown. No grace period extends the
configured packet budget to wait for a summary.

## Validation evidence

`icmp_test.go` exercises validation, argument fixtures, bounded concurrent output,
environment isolation, result classification, deadline/cancellation and parsing
fixtures under `testdata/`. Actual ping calls use only IPv4/IPv6 loopback, including
count/size controls and interruption of a multi-packet command. They require a
working system ping and loopback stack; they do not silently skip missing local
prerequisites on a supported OS. Darwin's IPv6 case is omitted intentionally.

Run from the repository root using the existing toolchain:

```powershell
& .canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/internal/icmp -count=1
& .canary/tools/go/bin/go.exe vet -mod=readonly ./apps/node-go/internal/icmp
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
& .canary/tools/go/bin/go.exe build -mod=readonly ./apps/node-go/internal/icmp
```

Native Windows execution and cross-compilation do not prove Alpine execution.
The parent must run this package's loopback tests in the final Alpine container
with its actual ping provider, runtime user and existing container permissions.
Darwin behavior currently has argument/parser fixture evidence only.

Primary references consulted for platform decisions:

- [BusyBox ping implementation and embedded usage](https://raw.githubusercontent.com/mirror/busybox/master/networking/ping.c)
- [BusyBox fractional-duration build option](https://raw.githubusercontent.com/mirror/busybox/master/libbb/duration.c)
- [iputils ping manual source](https://raw.githubusercontent.com/iputils/iputils/master/doc/ping.xml)
- [Windows ping options and units](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/ping)
- [Apple IPv4 ping implementation](https://raw.githubusercontent.com/apple-oss-distributions/network_cmds/main/ping.tproj/ping.c)

The repository comparison is [docs/parity-audit.md](../../../../docs/parity-audit.md),
particularly its ICMP option/RTT gaps. This package does not supply separate user
controls for inter-packet interval or per-request timeout, or claim complete
Uptime Kuma/UI/configuration parity.
