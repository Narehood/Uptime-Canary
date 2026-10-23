# Globalping remote measurement leaf

This package implements a single remote ping, HTTP, or DNS measurement using a
stdlib HTTP client. It imports the existing guarded `jsonquery.Check` engine.
It has no scheduler, database, HA voting, API handlers, shared monitor types,
UI, notification delivery, or local target probes. Root integration is separate.

## Stable integration contract

```go
func Validate(Config) error
func Check(context.Context, Config) Result

type Config struct {
    Target  string
    Timeout time.Duration
    Options Options
}

type Result struct {
    State            string // "up", "down", "pending"; exported constants Up/Down/Pending
    LatencyMs        float64
    LatencyAvailable bool
    Message          string // fixed sanitized text
    StatusCode       int    // remote HTTP code only; zero omitted from JSON
}
```

`Options` JSON keys are `mode`, `location`, `apiUrl`, `token`,
`allowInsecureApi`, `apiCa`, `probeTimeoutSeconds`, `ipVersion`, `ping`, `http`,
`dns`, and `apiFailureIsDown`. Go field names are those in `config.go` (including
`APIURL`, `APICA`, `HTTP`, `DNS`, `JSONata`). Exactly one mode options pointer
must exist and match `mode`; use an empty struct for defaults. Mode is trimmed
and lowercased; protocol/method/query-type values are uppercased. Validation
does not modify the caller's data. Callers must not mutate configuration during
a check. `Timeout` is mandatory, positive, and at most five minutes; as a Go
`time.Duration`, its JSON representation is an integer number of nanoseconds.

`Validate` performs bounded shape checks, certificate parsing, and bounded RE2
compilation. It never resolves a hostname, contacts a provider/target, or compiles
JSONata. Invalid configuration makes `Check` return Pending with a fixed message,
including when `apiFailureIsDown` is set. Every other operation uses the earlier
of the caller's context deadline and check-start plus `Timeout`. Run checks
outside monitor/state locks. The executable must call the existing
`probeworker.Dispatch` before ordinary startup for guarded JSONata to work.

`location` is a required, nonblank free-form Globalping magic expression, at
most 256 UTF-8 **bytes**, with control characters rejected. It is passed through
without interpreting locations or claiming that the selected probe matches a
specific geography. `probeTimeoutSeconds` is omitted at zero; otherwise 5–30.
It is the provider's timeout, not the outer check deadline. The schema recommends
allowing another ten seconds for API finalization. `ipVersion` is zero (omitted),
4, or 6; any nonzero value with a numeric target is rejected, even when the
families agree. No IP family is silently changed. Hostnames use ASCII DNS labels
(punycode where necessary), at most 253 bytes; DNS owner names also allow `_`.

## Remote conditions and timing

- **Ping**: `packets` defaults to 3, allowed 1–16. `protocol` defaults to ICMP,
  also permits TCP. `port` is valid only for TCP and defaults to 80. Zero means
  unspecified for all configurable ports; explicit port zero is not supported.
  A finished result with at least one received packet is Up, including partial
  loss. Complete loss is Down. Statistics must have consistent packet counts,
  total equal to requested packets, loss within one percentage point of the
  counts (provider rounding), nonnegative finite min/avg/max in order, and
  one finite RTT entry per received packet (ICMP also requires TTL 0–255).
  All-loss min/avg/max must be present and null. Latency is `stats.avg` only when
  a reply was measured; no average is invented for complete loss or failures.
- **HTTP**: target must be an absolute HTTP/HTTPS URL without userinfo or
  fragment. Hostname, escaped path, raw query, port, and scheme are mapped to
  API options; an absent path becomes `/`. Methods are GET (default), HEAD,
  OPTIONS only. `protocol: "HTTP2"` requires an HTTPS URL; an empty protocol
  derives HTTP/HTTPS from the URL. Headers allow 32 entries and 32 KiB aggregate
  name/value bytes; names must be HTTP tokens, values UTF-8 without control
  characters except HTAB. Case-duplicate names and reserved Host/User-Agent
  are rejected. Explicit Authorization supports Basic or Bearer target auth.
  `acceptedStatuses` is up to 32 inclusive `{min,max}` ranges within 100–599;
  omitted/empty defaults to 200–299. Status, TLS authorization (for HTTPS unless
  `ignoreTls`), optional literal case-sensitive keyword, and optional JSONata
  condition must all pass. `invertKeyword` requires a nonempty keyword (up to
  4096 UTF-8 bytes). Latency is remote `timings.total`, including on condition
  mismatch. Relevant present timing components are also validated.
- **HTTP body conditions** use only an explicit string `rawBody`; `rawOutput`
  is never parsed. A missing field or invalid field type is provider-invalid
  Pending. A null or explicitly truncated body cannot satisfy a positive or
  inverted body condition and produces Down. A status/TLS-only check can accept
  null or truncated body data because it makes no body-content assertion.
  Globalping currently returns only the first 10 kB; the local admitted string
  cap is 64 KiB. JSONata requires strict complete JSON (any JSON value), at most
  32 container levels and 100,000 values, with duplicate keys rejected. Unlike
  Kuma's text fallback, malformed JSON is **not** wrapped into a JSON string.
  Only `jsonquery.Check` receives the document, with the remaining outer budget;
  there is no in-process compile/evaluate or fallback. Query failure/mismatch
  is Down; unavailable guarded worker is always Pending, even with provider-down
  policy. The existing Go JSONata implementation and its known R6 limitations
  are inherited; the private official-JS candidate is not used or adopted here.
- **DNS**: query type defaults to A; supported types are A, AAAA, ANY, CNAME,
  DNSKEY, DS, HTTPS, MX, NS, NSEC, PTR, RRSIG, SOA, TXT, SRV, SVCB. CAA is not
  in the supplied current API schema. Protocol defaults to UDP, also allows TCP;
  port defaults to 53. An optional resolver is a hostname or unbracketed IP,
  without a port or URL. A finished nonzero DNS response code is Down. No-error
  responses require answers by default; `allowEmptyAnswers` permits an empty
  NOERROR answer set. That option never overrides a DNS error or a regex.
  Optional `regex` is bounded RE2 (4096 bytes), prefixed with `(?i)`; RE2 inline
  flags retain their usual scope. It must match at least one individual named
  `answers[].value` string, not owner/type/TTL/class or joined raw output.
  Each answer requires typed name/type/class/TTL/value fields; at most 256 records,
  8192 bytes per value and 64 KiB aggregate record text. Latency is remote
  `timings.total`. This deliberately differs from matching Kuma's formatted
  DNS output; regexes spanning records and raw-output-only matches do not pass.

`LatencyAvailable` is mandatory evidence for interpreting `LatencyMs`. The
numeric field's zero value with a false flag means **unmeasured**, not a zero-ms
measurement. A reported finite zero is valid with a true flag. API round trips,
polling time, process time, and local elapsed time never become target latency.
`StatusCode` is populated only after valid remote HTTP measurement fields are
admitted, never from the provider API's own HTTP status.

## Provider failures and response admission

Auth, quota, API transport/TLS/HTTP errors, unknown semantic statuses, malformed
data, exhausted budgets, offline probes, incomplete results, cancellation,
and `failureSource: "internal"` default to Pending: they supply no target
availability evidence. `apiFailureIsDown: true` maps those provider failures to
Down for Kuma-like behavior. Actual `failureSource: "target"` or `"resolver"`
is Down. A failed result with absent failureSource is also Down for backwards
compatibility, without diagnosing the cause. An unknown, empty, null, or
contradictory failureSource is provider-invalid. Failure classification is the
provider's best-effort report, not proof of network root cause.

Creation is exactly one `POST <api-prefix>/v1/measurements`, with `limit:1`,
`locations:[{magic:location}]`, `inProgressUpdates:false`, mode/target/options,
and optional probe timeout. There is no POST retry or fallback, including after
ambiguous transport failure. Only 202 plus bounded JSON with one admitted probe
and a nonempty 1–128-byte ASCII `[A-Za-z0-9_-]` ID is accepted. GET URLs are built
from this validated ID; server Location headers are ignored.

Polls are sequential, with a 500 ms wait **after the response is read** for each
in-progress response or cached 304. There are at most 120 GET attempts, with no
fixed ticker. Only HTTP 200 plus a valid fresh matching body can complete a
measurement. Safe ETags are reused only with a validated cached in-progress
body; an unsolicited 304 is invalid, and a 304 can never complete a measurement.
Poll bodies must match ID, type, target, probesCount=1, and results length=1.
`createdAt` is mandatory RFC3339, within **±30 seconds of local check start**;
it must remain the same instant across polls. Keep node clocks synchronized.
This rejects old results rather than treating IDs or cache headers as proof of
freshness. Overall and per-result statuses must be recognized, and both must
be finished before evaluating success.

Admitted JSON bodies are at most 1 MiB each; total body bytes read are capped at
8 MiB per check (including any oversize detection byte). Response headers are
capped at 32 KiB per response. Parsing rejects trailing values, malformed JSON,
non-UTF-8, unpaired Unicode surrogates, excessive depth/value count, and duplicate
object keys at any depth, including case variants and escaped spellings. Exact
critical field names are required; unused provider metadata is ignored after
strict bounded parsing. Required consumed measurement fields must be present,
correctly typed, and all used timings finite and nonnegative. Invalid data never
becomes Up. Provider error strings, target bodies/headers, probe metadata,
location, target, and credentials are never included in results or error text.

## API transport and product decisions

`apiUrl` defaults to `https://api.globalping.io`, without userinfo, query, or
fragment. It may have a literal reverse-proxy path prefix (safe ASCII segments,
no traversal/double slashes/percent encoding); do not include `/v1/measurements`
yourself. `allowInsecureApi` explicitly permits HTTP for self-hosted endpoints
and loopback fixtures; it never disables HTTPS certificate verification.
`apiCa` accepts up to 128 KiB of PEM containing at most 32 certificates only,
adds those certificates to system roots, verifies hostnames, and requires TLS
1.2+. A usable system root pool is required when adding custom API trust. API
trust is separate from the remote target's TLS policy; apiCa is not sent in the
measurement and does not establish a custom target CA.

Each check owns its HTTP transport. Redirects, environment proxies, cookie
storage, environment authentication, automatic decompression and connection
reuse are disabled. Bodies close on every path, request contexts cancel stalled
I/O, and transport idle connections close at return. Provider bearer tokens
appear only in API Authorization; target headers appear only in request JSON.

**Sending a measurement transmits the target, location expression, HTTP path,
query, and any configured target headers (including Authorization) to Globalping
and its selected third-party probe.** Sanitized local results do not make those
values private from the provider/probe. The provider may retain/publish
measurement data according to its own policies. Creation can consume quota or
paid credits even when the caller times out or loses the response. Only one
probe is requested per check; there is no credit purchase or billing API here.

Every HA node calling Check independently creates its own remote measurement
and consumes its own quota. This leaf has no leader selection, deduplication,
shared result cache, or HA aggregation. The owner must make that product and
budget decision at integration. There are no live create calls in this package's
validation suite: all measurement API traffic goes to loopback fixtures.

Unsupported target features: OAuth2 token acquisition/refresh, custom target
CA, mTLS/client certificates, target POST/request bodies, HTTP resolver override,
Host/User-Agent override, redirect controls, and DNS trace. Optional Basic/Bearer
target auth is possible only by supplying an explicit Authorization header.
No location availability, live provider interoperability, full Kuma parity,
Alpine runtime validation, integrated HA behavior, or full release readiness is
claimed by these fixture tests and cross-builds.

## References and validation evidence

Reference material supplied and verified by the owner on 2026-10-22:

- [Globalping API schema](https://github.com/jsdelivr/globalping/blob/master/public/v1/components/schemas.yaml),
  read-only cached `schemas.yaml` SHA256
  `3579c865148bc63a14ef6a23a545dc2051ba0c14913eb5134f692cd33d409ab6`.
- Read-only official Go SDK `measurement_models.go` SHA256
  `5b54b26af59275b5bd8187f7f7a183cfe09137c399db26211bbd9d0e4d5c1a63`.
- [Uptime Kuma 2.5.6 reference](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/globalping.js).
  This implementation is independent stdlib code, with no external Go SDK
  dependency and no wholesale upstream source copying.

Focused validation uses Go 1.27.3 on Windows/amd64. From the workspace root in
PowerShell, run the following (quote Go flags containing dotted file paths):

```powershell
.canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/internal/globalping -count=1 -timeout=45s '-coverprofile=apps/node-go/internal/globalping/coverage.out'
.canary/tools/go/bin/go.exe vet -mod=readonly ./apps/node-go/internal/globalping
.canary/tools/go/bin/go.exe tool cover '-func=apps/node-go/internal/globalping/coverage.out'
```

For each architecture, in a separate command process:

```powershell
$env:CGO_ENABLED='0'
$env:GOOS='linux'
$env:GOARCH='amd64' # repeat with arm64
.canary/tools/go/bin/go.exe build -mod=readonly ./apps/node-go/internal/globalping
```

Native fixtures pass with **92.6% statement coverage**; vet and CGO-disabled Linux
amd64 and arm64 library builds pass. Generated coverage profiles and executables
are not retained in the package. Coverage percentage, source hashes, and exact
final validation outcome are recorded in `SOURCE_FREEZE.json`. Tests exercise real HTTP
and TLS servers, credential namespace separation, mode payloads, remote timing
and failure semantics, guarded JSONata AND/worker-capacity handling, DNS answers,
ETag caching and rate delay, creation/poll HTTP admission, deadlines/cancellation,
malformed/duplicate/stale/mismatched JSON and wire/resource bounds. No whole-Go
suite, unrelated R3 repair, live provider request, or owner Alpine VM run occurs.
