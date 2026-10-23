# Standalone active checks

Integration contract (implementation in this directory only; standard library,
Go 1.27.3):

```go
func Validate(Config) error
func (Config) Validate() error
func Check(context.Context, Config) Result

type Config struct {
    Type Type // HTTP, TCP, TLS, DNS, WebSocket, SMTP constants
    Target string // HTTP/WS URL; TCP/TLS/SMTP host:port; DNS query name/IP
    Timeout time.Duration // mandatory, > 0 and <= 5 minutes
    HTTP *HTTPConfig
    TLS *TLSConfig
    DNS *DNSConfig
    WebSocket *WebSocketConfig
    SMTP *SMTPConfig
}
type Result struct {
    State string // "up" or "down"
    LatencyMs float64
    Message string // fixed descriptions; no target, credentials, body, DNS values
    StatusCode int // HTTP/WS only, when a response exists
    Certificate *CertificateSummary // validity dates, days remaining, verified flag
}
```

The parent maps its monitor type, timeout milliseconds, and expected status codes
to this contract. `Check` validates before network I/O, creates a timeout context
even if the caller has none, honors earlier cancellation, and returns down for
invalid configuration. `Validate` errors describe fields without echoing values.
No shell, files, process environment mutation, application state, or dependencies
are needed. This package does not replace the existing ICMP check.

Public option structs have camelCase JSON field tags and can be aliased by the
parent's monitor input types. `Config.Timeout` and `TLSConfig.MinValidity` use Go
durations; the parent converts timeout milliseconds and public TLS minimum
validity days before calling the engine. Callers must treat the entire option
configuration as private administrator data, including header/auth/PEM fields.

HTTP options: method (default GET), string headers, body, basic or bearer auth,
inclusive `[]StatusRange{Min, Max}` (default 200–299), `*MaxRedirects` (nil = 5;
zero = do not follow), optional `*KeywordMatcher{Value, Regex, Negate}`, optional
`*JSONMatcher{Path, Expected, ExistsOnly, Negate}`, proxy URL or explicit opt-in to
environment proxy settings. `Expected` is a JSON encoded value, compared
structurally, including exact numeric equality. Keyword regex uses Go RE2 syntax.
JSON paths support `$`, `.name`, `[0]`, and `["quoted key"]`; no wildcards,
recursive descent, filters, scripts, or coercion. Missing selectors fail equality;
`Negate` complements the existence/equality result. Invalid JSON always fails.
HEAD can check status/headers; body matchers on HEAD are rejected.

HTTP and WebSocket URLs forbid userinfo and fragments. HTTPS downgrade redirects
are refused. Only same-origin redirects retain configured headers or request
bodies; cross-origin redirects use no configured headers and must not replay a
body. Proxy URLs allow HTTP or HTTPS, without userinfo, query, or fragment;
environment proxy use is off by default. Private targets are intentionally
allowed for uptime monitoring; the parent must restrict who can create checks.

TLS options (HTTPS, TLS, WSS, or encrypted SMTP): `ServerName`, additive `CAPEM`,
paired `ClientCertPEM`/`ClientKeyPEM`, `MinValidity time.Duration`, and explicit
`IgnoreVerification`. TLS 1.2 is the minimum. IgnoreVerification disables chain
and hostname verification; certificate validity dates and MinValidity still
apply, and Certificate.Verified is false. No PEM, key, subject name, or remote
error text appears in results. Certificates for each HTTPS redirect are checked.

DNS options: `RecordType` (A, AAAA, CNAME, MX, TXT, NS, PTR), optional `Server`
(literal IP:port, including bracketed IPv6), optional `Expected []string`.
No expectations means at least one answer; otherwise all expected records must
be present, allowing extra answers. IPs are canonicalized; DNS names ignore case
and final dot; MX expectations are `priority hostname`; TXT compares exact text.
PTR Target is an IP. The pure Go resolver uses the configured server for UDP and
TCP, and caller deadlines. Go's resolver follows system hosts/search precedence;
IP literals or hosts-file answers which bypass DNS return down rather than
claiming DNS availability. CNAME uses Go's canonical-name lookup, which can
return the input's canonical name when address records exist without an alias.
DNS TTL, DNSSEC, and arbitrary RR types are not exposed.

WebSocket options: headers, basic/bearer auth, and the same explicit proxy options.
The check requires an RFC 6455 HTTP/1.1 upgrade and a matching response to a
random, masked protocol ping. It answers server pings while awaiting the pong.
It negotiates no compression or subprotocol, follows no redirects, and sends no
application messages. Incoming application frames are discarded within bounds.

SMTP options: `TLSMode` = `plain` (default), `implicit`, or `starttls`. The check
requires 220 greeting and 250 EHLO; STARTTLS additionally requires advertised
STARTTLS, 220 upgrade, TLS handshake, and successful second EHLO. It sends no
authentication or mail and uses a fixed `localhost` EHLO identity.

Hard limits: HTTP request/decoded response body 1 MiB; response headers 64 KiB;
configured headers 32 KiB / 100 fields; matcher/JSON selector inputs bounded;
maximum 10 redirects (default 5); PEM inputs 256 KiB each; DNS answers 1024 / 64
KiB; WebSocket 64 frames / 64 KiB aggregate payload; SMTP 100 lines / 64 KiB per
reply, 4096 bytes per line. Reaching a bound fails rather than reporting success
from truncated input. JSON nesting is limited to 64 levels and numeric literals
to 1024 bytes; selectors have at most 64 steps and 2048 bytes. HTTP redirects
discard intermediate bodies without reading them; the final response body is
fully read within its limit. All raw connections close on cancellation.

Run the independent fixture suite from the repository root:

```powershell
$env:CGO_ENABLED='0'
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/checks
.canary/tools/go/bin/go.exe vet ./apps/node-go/internal/checks
```

RDAP/domain expiry, ICMP, push, databases, Docker, game protocols, notification
delivery, scheduling, and persistence are outside this package. These checks do
not claim full Uptime Kuma parity.

Protocol/API references: [Go net/http](https://pkg.go.dev/net/http),
[Go net.Resolver](https://pkg.go.dev/net#Resolver),
[RFC 6455](https://www.rfc-editor.org/rfc/rfc6455.html),
[SMTP / RFC 5321](https://www.rfc-editor.org/rfc/rfc5321.html),
[STARTTLS / RFC 3207](https://www.rfc-editor.org/rfc/rfc3207.html).

Validation completed with Go 1.27.3 and CGO disabled: package tests, `go vet`,
and Linux amd64 package compilation. Fixtures use loopback listeners and
in-memory generated certificate authorities; no public network or external
service is used by the tests. Coverage includes HTTP/auth/body matchers,
decompression and header/body bounds, redirect isolation (including mTLS),
certificate expiry and trust, all DNS record types and TCP fallback, masked WS
ping/pong and malformed frames, SMTP plaintext/implicit TLS/STARTTLS, and blocked
read/handshake cancellation.
