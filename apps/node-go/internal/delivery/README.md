# Delivery provider contract

This standard-library-only package owns one bounded outbound attempt. It does
not persist configuration, schedule retries, send SMS, or claim inbox delivery.
The runtime owns authorization, secret storage/redaction, destination access
policy, replicated channels, and per-destination receipts. Discord remains in
the existing runtime adapter.

## Integration API

```go
engine := delivery.New(delivery.Options{}) // default entire attempt: 5 seconds
err := delivery.Validate(config)          // local checks; never sends
result := engine.Send(ctx, config, delivery.Message{
    EventKind: "down", MonitorID: "monitor-1", MonitorName: "API",
    IncidentID: "incident-1", ObservationsSummary: "Two nodes observed failure",
    IdempotencyKey: "receipt-1", Subject: "API is down", Text: "Check failed",
})
```

`Config`, `Message`, `Result` and nested types are JSON tagged. `Config.Kind`
is `smtp`, `mailgun`, `brevo`, `mandrill` (Mailchimp Transactional), or `webhook`.
Mail providers use `Sender Address{Email, Name}` and `Recipients []Address`.
`SMTP SMTPConfig{Server, Port, TLSMode, Username, Password}` accepts TLS modes
`starttls` (default), `implicit`, and explicit unauthenticated `plain`.
`Mailgun MailgunConfig{APIKey, Domain, Region, BaseURL}` uses US by default or
explicit `EU`. Brevo and Mandrill each have `APIKey` and optional `BaseURL`.
Custom API base URLs are roots to which versioned endpoint paths are appended.
`Webhook WebhookConfig{URL, BearerToken}` POSTs JSON. API configs and webhook
config have `AllowInsecureHTTP`; setting it explicitly permits HTTP for a
private relay/test server. HTTPS is the default requirement.
Reuse an `Engine` for repeated attempts. The package-level `Send` convenience
function also shares a default engine/connection pool. Verified senders/domains,
provider credits, and sandbox recipient allowlists must be configured with the
provider; this package does not provision accounts or DNS records.

`Message.Subject`, `Text`, `HTML` are literal strings; optional `APIBody` is a
bounded JSON object for webhooks only. Otherwise webhooks receive the message
as JSON. `Config.Templates` offers literal `Subject`, `Text`, `HTML` strings with
only `{{event_kind}}`, `{{monitor_name}}`, `{{monitor_id}}`, `{{incident_id}}`,
`{{observations_summary}}`, `{{idempotency_key}}` substitutions. No expressions,
code, files, or template functions execute; substitutions into HTML are escaped.
Nonempty message content overrides templates. Missing subject/text fall back to
bounded event/monitor/observation text. Callers should avoid sensitive monitor
details in content. Configuration includes secrets and must not be exposed as
a public receipt.

`Result.Accepted` means the provider acknowledged all requested recipients (or
the webhook acknowledged its HTTP request). `Status` is `accepted`, `partial`,
`rejected`, `retryable`, or `unknown`. `ProviderID` is a SHA-256 digest of the
provider's opaque ID, never the raw ID. `HTTPStatus` is numeric.
`RetryAfter` is a Go duration (JSON nanoseconds), capped at 24 hours and only a
hint: the parent persists the next retry; the engine never sleeps/retries.
`ErrorCode` and `ErrorMessage` are fixed package strings, never provider bodies,
URLs, secrets, addresses, or reflected error messages. Validation errors also
use fixed messages. `Recipients []RecipientResult` identifies recipients by
zero-based `Index`, fixed `Status`, `Accepted`, and sanitized `ProviderID`.
Counts are available as `AcceptedRecipients` and `RejectedRecipients`.

Mandrill accepts `sent`, `queued`, and `scheduled` as outbound acceptance;
`rejected` and `invalid` are failures. Every requested recipient must have one
matching result; missing/duplicate/unknown recipient entries produce an unknown
outcome. Mixed results are `partial`, never whole-message acceptance. Do not
blindly retry a partial result: retry only known unaccepted destinations or
accept duplicate risk. SMTP aborts with RSET before DATA on any RCPT refusal,
so no recipients receive message data from that attempt. SMTP disconnects during
DATA/final acknowledgement and ambiguous HTTP outcomes are `unknown`: a retry
can duplicate delivery. This package provides at-least-once building blocks,
not exactly-once delivery. `IdempotencyKey` supplies correlation metadata; email
providers do not promise deduplication. Webhooks receive an `Idempotency-Key`
header; receivers must implement deduplication themselves.

## Bounds and transport guarantees

- At most 50 distinct recipients; ASCII mailbox addresses, no CR/LF/control
  injection; bounded names/subject/header fields. SMTP uses MIME encoding and
  quoted-printable body lines. No arbitrary email headers or attachments.
- Subject: 512 bytes; each text/HTML body: 64 KiB; webhook JSON: 64 KiB;
  observations: 16 KiB; metadata fields: 256 bytes; templates: 64 KiB each
  (subject templates: 512 bytes). Expansion stops at the rendered field limit;
  rendered output is revalidated. HTTP response bodies: at most 64 KiB.
  The default HTTP transport also caps response headers at 64 KiB.
- Every attempt has a deadline, including a background caller context. Options
  may change the 5-second default up to 60 seconds; earlier caller deadlines
  win. Cancellation closes SMTP sockets even during greetings, TLS, and DATA.
- SMTP certificates and hostnames are verified with TLS 1.2 minimum. STARTTLS
  is required by default, without plaintext fallback. Explicit plain SMTP cannot
  have credentials. AUTH PLAIN requires TLS. `Options.SMTPRootCAs` supports a
  private CA without disabling certificate verification. SMTP authentication
  supports AUTH PLAIN (including a 334 challenge); LOGIN-only servers are not
  supported. Alpine deployments need the `ca-certificates` package for public
  HTTPS/SMTP trust, or an explicitly supplied private CA/client trust store.
- HTTP redirects are refused even when an injected client follows redirects.
  `Options.HTTPClient` is cloned (its Transport is reused) for local fixtures and
  custom trust/proxies. A custom transport is trusted to honor request context
  and standard net/http security behavior. Request body replay is disabled so
  net/http cannot automatically retry these POST requests on stale connections.
- URLs cannot contain userinfo, query credentials, fragments, or invalid ports.
  API roots may include a path prefix, but no query; webhook URLs also disallow
  queries. Private networks are permitted deliberately; runtime authorization
  and network policy must control who may configure destinations. No network
  allowlist or DNS-rebinding prevention is implemented here.
- Network failures, timeouts and malformed successful API responses are unknown
  outcomes. HTTP 429 and 5xx are retryable; other HTTP failures are rejected.
  Retry-After seconds/date and Brevo x-sib-ratelimit-reset seconds are bounded.
  No server error text is returned. Acceptance is not bounce/delivery tracking.

## Official protocol references (checked 2026-10-22)

- [Mailgun send API](https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/messages/post-v3--domain-name--messages):
  multipart POST `/v3/{domain}/messages`, HTTP Basic user `api`;
  [US/EU routing](https://documentation.mailgun.com/docs/mailgun/user-manual/sending-messages/send-http).
- [Brevo send API](https://developers.brevo.com/reference/send-transac-email):
  POST `https://api.brevo.com/v3/smtp/email`, `api-key` header, sender/to objects.
  Following the [static-content guide](https://developers.brevo.com/docs/send-a-transactional-email),
  HTML takes precedence when both HTML and text are supplied to this adapter.
- [Mailchimp first-email guide](https://mailchimp.com/developer/transactional/guides/send-first-email/)
  explicitly documents POST `https://mandrillapp.com/api/1.0/messages/send`.
  This implementation deliberately uses that established endpoint rather than
  the [send API reference's 1.4 endpoint](https://mailchimp.com/developer/transactional/api/messages/send).
  Both are currently documented; there is no automatic version fallback that
  could send a message twice.
  [Outbound email and recipient statuses](https://mailchimp.com/developer/transactional/docs/outbound-email/).
- [Go SMTP implementation](https://pkg.go.dev/net/smtp) and
  [SMTP specification](https://www.rfc-editor.org/rfc/rfc5321).

Verification uses actual local HTTP/SMTP protocol fixtures and a private test CA;
no real account, secret, recipient, container, or network email send is required.
From the repository root: `.canary/tools/go/bin/go.exe test ./apps/node-go/internal/delivery`.
