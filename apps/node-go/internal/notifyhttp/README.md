# Native HTTP notification leaf

`notifyhttp` implements five native adapters: Slack incoming webhooks, Telegram
Bot API `sendMessage`, ntfy publication, Gotify message creation, and Pushover.
It uses only the Go standard library and imports no delivery or runtime package.
This is a bounded send leaf, with no notification API, persistence, HA receipts,
authorization, secret storage, destination admission, scheduler, UI, or SMS.
Root owns those integration concerns. Five adapters do not imply a broader
provider count or full Uptime Kuma option parity.

## Stable integration contract

```go
engine, err := notifyhttp.New(notifyhttp.Options{
    Timeout: 5 * time.Second,
    // AdditionalRootCAPEM: trustedProcessCAPEM,
})
if err != nil {
    // A fixed sanitized construction error; do not log configuration.
    return err
}
defer engine.CloseIdleConnections()

if err := notifyhttp.Validate(config); err != nil {
    return err // pure; no DNS, HTTP, quota check, or credential validation
}
destinationIdentity, err := notifyhttp.Identity(config)
if err != nil {
    return err
}
_ = destinationIdentity // root binds this together with configuration resource ID

result := engine.Send(ctx, config, notifyhttp.Message{
    Title: "API is down", Text: "Two observers reported failure",
    EventKind: "down", MonitorName: "API", MonitorID: "monitor-1",
    IncidentID: "incident-1", IdempotencyKey: "persisted-attempt-1",
})
_ = result
```

Exact exported types, JSON names, and constants are in `types.go`.

- `Kind` is `slack`, `telegram`, `ntfy`, `gotify`, or `pushover`.
- `Config` has `Kind Kind` and `Slack *SlackConfig`,
  `Telegram *TelegramConfig`, `Ntfy *NtfyConfig`, `Gotify *GotifyConfig`,
  `Pushover *PushoverConfig`. Exactly one matching pointer is required. An empty
  foreign pointer is rejected, rather than silently ignored.
- `Validate(Config) error` and `Identity(Config) (string, error)` are pure.
  Invalid inputs return fixed errors containing no field values.
- `New(Options) (*Engine, error)` accepts only `Timeout time.Duration` and
  `AdditionalRootCAPEM []byte`. There is no injectable client/Doer/transport,
  arbitrary header map, raw API payload, or scripting/evaluator option.
- `(*Engine).Send(context.Context, Config, Message) Result` performs one attempt.
  Package `Send` and nil/zero engines use shared safe defaults. A nil context is
  treated as a background context with the engine timeout.
- `Message` has string fields `Title`, `Text`, `EventKind`, `MonitorName`,
  `MonitorID`, `IncidentID`, and `IdempotencyKey`. Only title/text are sent.
  Metadata is never implicitly added to a notification or request header.
- `Result` has `Kind Kind`, `Status Status`, `Accepted bool`, `HTTPStatus int`,
  `RetryAfter time.Duration`, `ProviderID string`, `ErrorCode string`, and
  `ErrorMessage string`. JSON duration values are nanoseconds, following Go's
  `time.Duration`; root should convert them for any seconds-based API.

`Config.UnmarshalJSON` rejects unknown fields, alternate field casing, duplicate
keys, foreign provider objects, and explicit null provider objects. It applies
local validation after decoding. Omit inactive objects entirely. A root envelope
decoder still needs its own strict field/size handling and safe error mapping.
Programmatic construction must pass `Validate`; typed structs alone do not make
configuration trusted. Do not mutate configuration pointers/slices concurrently
with a send. Configuration JSON deliberately contains credentials for private
storage; it must never be returned directly in an API response or logs. Mask bot
tokens, app tokens, passwords, webhook URLs, topics, chat IDs, and user keys at
root's presentation boundary.

## Outcomes and retry ownership

`accepted` means the provider acknowledged the configured publication request.
It never means inbox/device delivery, human acknowledgement, or exactly once.
`ProviderID` is either empty or a lowercase 64-character SHA256 digest. Slack's
plain acknowledgement supplies no message ID, so that field stays empty.

HTTP 429 and 500-599 return `retryable`. Other HTTP 400-499 return `rejected`;
this includes 408. HTTP 300-399 return `rejected`/`redirect_refused` without
following the redirect. Unexpected success statuses, invalid/contradictory
acknowledgements, truncated/oversized responses, TLS/network failures, timeout,
and cancellation return `unknown`. A known HTTP rejection/rate/server error
remains authoritative if its body is malformed or unreadable. An explicit
Telegram `ok:false` or Pushover `status:0` error cannot become acceptance even
with HTTP 200. Unknown provider status values never count as success.

There is no retry, fallback or sleep. Fresh HTTP/1 connections, disabled
keep-alives/HTTP2, no idempotency header, and `Request.GetBody=nil` prevent an
implicit replay on a stale pooled connection. Root must decide what to do with
an unknown outcome; retrying it can duplicate a message already accepted.
Pushover's explicit emergency settings authorize provider-side repeated alerts;
they do not enable an internal retry loop in this package.

`RetryAfter` is the largest usable hint from decimal/date `Retry-After`,
Telegram `parameters.retry_after`, or a depleted Pushover quota's UNIX
`X-Limit-App-Reset` header. It is bounded to 0-24 hours, with very large decimal
seconds saturated rather than overflowing. Bad/negative/fractional hints are
ignored. Accepted results clear retry hints. Root's persisted scheduler owns
backoff, per-destination holds, provider rate limits, quotas, and concurrency
across engines. These send endpoints have no shared generic idempotency-key
contract; `Message.IdempotencyKey` stays local. ntfy sequence updates are not
substituted for deduplication.

## Transport, URL policy and bounds

The timeout defaults to 5 seconds; negative or greater than 60 seconds is a
construction error. The earlier parent deadline wins. One request context and
client timeout cover DNS, connection queue/dial, verified TLS, request writing,
headers, and the complete bounded response read. Every obtained response body
is closed, including on failure. Each engine permits two connections per host;
root owns application-level limits across channels and engine instances.

The transport does not inherit `http.DefaultClient` or `http.DefaultTransport`.
It has no proxy function, cookie jar, ambient authentication, redirect following,
automatic compression, or unverified TLS. HTTPS requires TLS 1.2 or newer and
normal hostname/chain verification. `AdditionalRootCAPEM` augments system roots
for that engine only, up to 256 KiB and 32 CA certificates. Input must be PEM CA
certificates without unrelated blocks/garbage. There is no OS trust installation
or per-channel CA option. Root may later wrap process trust per channel.

`AllowInsecureHTTP` is an explicit opt-in on every provider config. It permits
a valid configured HTTP endpoint, including private LAN/DNS/proxy hosts; it is
never inferred from a host being private. HTTP exposes the message and provider
credentials to the network. All tests use literal loopback addresses only.

Every URL is at most 2,048 bytes, absolute HTTP(S), without embedded userinfo,
query, fragment, backslashes, ambiguous/nested escapes, path traversal, empty
interior segments, or encoded separators. Only explicit HTTP opt-in permits
HTTP. Host/scheme/default port/path encoding are normalized. Slack supplies the
exact webhook path. Other providers take optional `BaseURL` API roots with a
clean optional path prefix; Gotify's root is required because it is self-hosted.
Defaults are `https://api.telegram.org`, `https://ntfy.sh`, and
`https://api.pushover.net`. Prefix concatenation cannot replace the origin.
No config URL can inject credential queries. Gotify uses `X-Gotify-Key`, not a
token query. Telegram's validated bot credential is an encoded path segment.
Only trusted configured API origins receive their corresponding credentials;
text, returned bodies, or redirects never choose another credential destination.
Root must still implement its own destination/SSRF policy and access controls.

Headers plus trailers have a combined limit of 32 KiB and 64 values, with an
additional transport header-byte cap before interpretation. Response bodies are
limited to 64 KiB and must reach EOF before acceptance. JSON is one complete
object, valid UTF-8, with no duplicate/case-colliding keys or critical field case
variants, depth at most 16, 4,096 value nodes, 256 entries per object, 512 entries
per array, and 256-byte keys. IDs and UNIX hints use integer parsing, never
float64. Unrelated additive response fields may be present within those bounds.
Requests are at most 64 KiB; strict config JSON is at most 16 KiB.

All message strings must be valid UTF-8. Title is single-line, at most 250
Unicode code points/1,000 bytes. Text is nonempty, at most 16 KiB, with no
control characters except CR, LF, and tab. Each local metadata field is
single-line and at most 256 bytes. Provider-specific text limits below apply
before network I/O; no text is silently truncated or split into several sends.

## Native provider profiles

**Slack.** `SlackConfig{WebhookURL, AllowInsecureHTTP}` POSTs JSON with
`Title + "\n" + Text` (or text alone), escaping `&`, `<`, `>`.
`parse:"none"`, `mrkdwn:false`, `link_names:0`, `unfurl_links:false`, and
`unfurl_media:false` are fixed. The escaped combined text is at most 4,000
code points, a conservative local profile. Only HTTP 200 with exact body `ok`
accepts; `ok\n` or arbitrary 2xx do not. Modern webhooks bind their channel,
username and icon, so overrides, threading, blocks and attachments are absent.
Primary sources: [incoming webhooks](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/),
[text parsing](https://docs.slack.dev/messaging/formatting-message-text/), and
[rate limits](https://docs.slack.dev/apis/web-api/rate-limits/).

**Telegram.** `TelegramConfig{BotToken, ChatID, MessageThreadID, Silent,
ProtectContent, DisableLinkPreview, BaseURL, AllowInsecureHTTP}` POSTs JSON to
`/bot<encoded-token>/sendMessage`. Numeric chat IDs are canonical nonzero signed
integers with magnitude at most 2^52-1; `@name` supports 1-32 ASCII
letter/digit/underscore characters starting with a letter. Bot tokens use
1-20 decimal digits, colon, 1-235 URL-safe ASCII characters, at most 256 bytes.
Thread is zero/omitted or positive through 2^52-1. Combined title/text is bounded
to 4,096 UTF-16 units. There is no parse mode, entity injection, rich/paid
broadcast, business, ephemeral, media, direct-message topic, or reply support.
Only HTTP 200, `ok:true`, a positive integer `message_id`, and valid matching
chat acknowledge. Numeric destinations must match exactly. Aliases require a
matching returned username (case-insensitive); missing resolution is unknown.
Configured thread IDs must match the returned thread. The receipt digest binds
chat and message ID. A well-typed optional success `description` is permitted;
contradictory error fields and non-string descriptions remain unknown.
[Primary Bot API contract](https://core.telegram.org/bots/api#sendmessage).

**ntfy.** `NtfyConfig{Topic, Token, Username, Password, Priority, Tags, BaseURL,
AllowInsecureHTTP}` POSTs a JSON publication to the root/prefix `/`, with topic
in the object. Topic is 1-64 ASCII letters/digits/underscore/dash; text is at
most 4,096 bytes. Priority is zero/default or 1-5. The local tag profile allows
16 single-line UTF-8 tags of at most 64 bytes each, without commas. A token
(visible ASCII, at most 512 bytes) and Basic authentication are mutually
exclusive. Basic requires both username (128 bytes, no colon) and password
(512 bytes), single-line UTF-8. Unauthenticated publication is supported.
HTTP 200 needs a 12-character alphanumeric ID, `event:"message"`, and matching
topic. No stream subscription, keepalive, file, action, click, attachment,
scheduling, email, phone, or sequence-update feature is synthesized.
[Primary publication/authentication contract](https://docs.ntfy.sh/publish/).

**Gotify.** `GotifyConfig{AppToken, Priority, BaseURL, AllowInsecureHTTP}` POSTs
title/message/integer priority to `/message`, authenticating exclusively with
`X-Gotify-Key`. Token is 1-512 visible ASCII bytes. Priority 0-10 is a deliberate
local profile; zero is explicitly sent rather than using an app default.
HTTP 200 needs a positive int64 message ID, a positive integer application ID,
a string message and an RFC3339 date, with no contradictory error fields.
Text uses the global 16 KiB bound. Client tokens, app selection, plugins,
Markdown extras, files and receipt subscriptions are outside this profile.
Primary sources: [push messages](https://gotify.net/docs/pushmsg),
[official API schema](https://raw.githubusercontent.com/gotify/server/master/docs/spec.json),
and [server creation source](https://raw.githubusercontent.com/gotify/server/master/api/message.go).

**Pushover.** `PushoverConfig{AppToken, UserKey, Device, Priority, Sound,
RetrySeconds, ExpireSeconds, BaseURL, AllowInsecureHTTP}` POSTs URL-encoded form
fields to `/1/messages.json`. Tokens/user-or-group keys are exactly 30 ASCII
alphanumeric characters. One optional device is at most 25 ASCII
letters/digits/underscore/dash; a sound name is at most 64 such characters and
its availability is provider-validated. Text is at most 1,024 code points.
Priority is -2 through 2. Emergency 2 requires retry 30-10,800 seconds and
expiry 1-10,800 seconds, with retry no greater than expiry. Other priorities
reject inactive retry/expiry options. Only HTTP 200, integer `status:1` and a
UUID-shaped request ID accept. No multi-user/device list, callback, receipt
polling, attachment, URL, HTML or encrypted-payload feature is exposed.
[Primary API, acceptance, emergency and quota contract](https://pushover.net/api).

These primary sources were reviewed on 2026-10-22. The bounded local profiles
are intentionally narrower than provider APIs; unsupported fields fail strict
configuration decoding instead of being silently ignored.

## Destination identity

`Identity` returns a hex SHA256 hash of a JSON tuple containing the provider,
normalized API scheme/host/route, and recipient routing fields. It returns no
raw route, credential or recipient. It excludes credentials and presentation/
rate/error-policy fields. Telegram includes chat/thread; ntfy includes topic;
Pushover includes a digest of the user/group key and the optional device.
An alias identity is the configured alias, not a permanent Telegram chat ID;
changing who owns an alias is a root/operator concern.

Slack strips the credential segment only for the exact canonical structure
`/services/T[UPPERCASE-ALNUM]/B[UPPERCASE-ALNUM]/credential`, retaining team and
installation segments. Arbitrary custom/proxy routes retain their full path
inside the hash; their last segment is not blindly dropped. A proxy that places
a changing credential in an arbitrary route needs explicit root migration logic.

Gotify has no nonsecret application ID in its send configuration. Its identity
is the API route; token rotation is assumed to preserve the same configured
logical destination. Root MUST bind the configuration resource ID as well as
this hash so separate Gotify app channels at one API never share receipts/holds.
The same root resource binding is recommended for every provider. Rotating a
Pushover user key changes identity because that secret also identifies the
recipient; rotating its app token does not.

## Root-owned validation and handoff

Post-cleanup policy permits this implementation agent to author/review source
only. Root alone runs validation through `scripts/bounded_validation.py`, with
child-only `CGO_ENABLED=0`, `GOTOOLCHAIN=local`, and `-mod=readonly` on the
existing Go 1.27.3 toolchain. No compiler/test/download/VM/browser automation or
generated build/coverage artifacts were run/created by this agent after resume.

The leaf test selector is `./apps/node-go/internal/notifyhttp`, full `-run .`.
Test names cover native wire requests/acceptance, strict config/identity/bounds,
rate hints, contradictory/truncated/large acknowledgements, redirect refusal,
generated private-CA trust/hostname verification, timeout/cancellation, connection
loss without replay, and bounded header/body/JSON resources. All network fixtures
bind literal loopback and close their servers/connections; CA keys/certificates
stay in memory. Failures use fixed messages, never config/body/credential dumps.

Root's original guarded verification of the formatted leaf, before the review
repairs below, passed:

- Windows/amd64, Go 1.27.3, CGO disabled: all 21 top-level test groups passed
  with `go test -mod=readonly -count=1 -run . -timeout=60s
  ./apps/node-go/internal/notifyhttp`. Package execution took 1.105 seconds;
  the complete storage-guarded command took 15.110 seconds.
- `go vet -mod=readonly ./apps/node-go/internal/notifyhttp` passed.
- CGO-disabled `go build -mod=readonly ./apps/node-go/internal/notifyhttp`
  passed with `GOOS=linux` and each of `GOARCH=amd64`, `GOARCH=arm64`.
  Vet and these sequential library compilations took 48.032 seconds inside
  one guarded command. No standalone binaries were retained.

Both guarded commands recorded unchanged Go/JS/JSON input fingerprints, zero
module-cache growth, zero remaining scratch, and no required storage pause.
Root's small records are `.canary/bounded-checks.json` and
`.canary/work/current/latest.json`; their lifetime is root-managed.

Those are local Windows fixtures and Linux cross-compilation results only.
They do not establish Linux runtime/Alpine behavior, live-provider delivery,
or current notification API/HA/UI integration. There was no coverage output
at that checkpoint. Earlier pre-pause compilation/fixture observations are
historical and superseded by the guarded result above.

`SOURCE_FREEZE.json` records root-reported verification and working-file SHA256
hashes after root's formatting. It hashes every leaf source/test/README file,
excluding the manifest itself to avoid a self-reference. The implementation
agent releases ownership of this leaf after that manifest is finalized; root
owns notification integration and any subsequent leaf changes.

### Independent review repairs

[The first source review](../../../../docs/notifyhttp-review.md) identified
incomplete Gotify receipts being accepted, valid Telegram success descriptions
being rejected, and malformed PEM prefixes being skipped. Root reproduced all
three with new local fixtures before changing production code; the initial
focused run failed in 0.676 seconds. All original fixture assertions remain
unchanged.

Root tightened native receipt fields, permitted string success descriptions,
and isolated each certificate block before decoding. Four new test groups also
exercise cancellation behind the two occupied connection slots. The complete
25-group Windows suite passed in 1.246 seconds (9.969 seconds including the
guard); targeted vet passed (15.625 guarded seconds). Both commands preserved
the Go/JS/JSON input fingerprint
`215d0bb84b1bba7580aa54aabdae48b3eb55a4ea4c1ac15e7cda33f0c6c9cfc5`,
with zero remaining scratch and no storage pause. Current-source Linux/amd64
and Linux/arm64 CGO-disabled library builds also passed in one guarded command
(30.500 seconds), with the same unchanged input fingerprint and no retained
executable. Independent follow-up remains pending. The refreshed freeze keeps
the original validation as historical evidence; no integration is claimed.

### Strict date follow-up and protocol-13 integration

The second source review found that Go's RFC3339 parser accepts a one-digit
hour, comma fractions and an out-of-range offset. Root reproduced those three
counterexamples, then added a lexical date check before calendar validation.
The full 26-group Windows leaf suite passes (1.384 seconds), as do delivery
(1.939 seconds), the full canary package (143.604 seconds), targeted vet and
CGO-disabled Linux amd64/arm64 leaf/delivery library builds. The enclosing
guarded command later failed a frontend typecheck; those backend results do
not imply the complete command or dashboard passed.

Working protocol 13 now connects the five providers to channel CRUD, destination
identity, per-destination attempts, provider hints, successful receipts and peer
pages. Four new native integration functions cover local provider tests, RBAC
and redaction, credential/resource binding, delayed revision proof, genuine
down/recovery sends, receipt exchange and a SQLite reopen. No real provider was
contacted. Gotify identities remain explicitly bound to the channel resource.
The forms passed root workspace typechecking, eight pure builder checks, a
disposable production build and a 21-check Windows browser proof using only
local providers. No binary or browser profile is retained; there is no
current release, Linux runtime or live-provider approval.

See the preserved [round-two review and root response](../../../../docs/notifyhttp-review-round2.md)
and [runtime contract](../../../../docs/notifications.md). Neither Linux runtime
nor a coherent release artifact is certified by these source checks.
