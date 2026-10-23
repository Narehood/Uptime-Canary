# Notification channels

The working protocol-13 runtime supports reusable Discord, SMTP, Mailgun, Brevo,
Mailchimp Transactional (Mandrill), JSON webhook, Slack, Telegram, ntfy, Gotify
and Pushover channels. Channel configuration is
replicated with explicit revision conflicts and deletion tombstones. Account
credentials, browser sessions and delivery attempts remain local to each node.

`MonitorInput.notificationIds` selects up to 16 channels. `webhookUrl` remains
available for existing configurations. Each email recipient is a separate
delivery destination, with its own receipt, retry deadline and stable attempt
identity. Acceptance for one destination never completes another destination.

## Management API

Management requests use an account token or password-login session as a bearer.
Admin and editor accounts can manage and test channels. Viewers can read channel
names, provider types and outcomes; provider configuration is redacted.

- `GET /api/notification-channels` returns `NotificationChannelView[]`.
- `POST /api/notification-channels` accepts `{name, enabled, config}` and returns
  a channel with `id`, `revision`, and `updatedAt` (201).
- `GET /api/notification-channels/:id` returns the sole active revision.
- `PUT /api/notification-channels/:id` accepts the complete input plus `revision`.
- `DELETE /api/notification-channels/:id` accepts `{revision}` (204).
- `GET /api/notification-channels/:id/conflicts` returns candidate revisions.
- `POST /api/notification-channels/:id/resolve` accepts complete input plus
  `revisions`, naming every current head exactly once. Stale edits and incomplete
  resolutions return 409.
- `POST /api/notification-channels/:id/test` accepts `{revision}` and returns
  `{attempts: DeliveryAttemptView[]}` (202). The channel must be enabled.
- `GET /api/notification-deliveries?limit=200&monitorId=...&incidentId=...`
  returns outcomes known to this node, newest attempts first. Filters are optional;
  `limit` is 1–1000.

The TypeScript request/response contracts are in
[`packages/shared/src/index.ts`](../packages/shared/src/index.ts). Provider-specific
fields and protocol limits are documented in the
[delivery package](../apps/node-go/internal/delivery/README.md).

The CLI supports `channels`, `add-channel <json-file>`,
`update-channel <id> <json-file>`, `delete-channel <id> <revision>`,
`channel-conflicts <id>`, `resolve-channel <id> <json-file>`,
`test-channel <id> <revision>`, and `deliveries [monitor-id]`. CLI output masks
provider configuration; keep the original private input file when editing a
channel rather than treating masked output as a complete configuration export.

`config.kind` is `discord`, `smtp`, `mailgun`, `brevo`, `mandrill`, `webhook`,
`slack`, `telegram`, `ntfy`, `gotify`, or `pushover`.
Email channels use `sender: {email, name?}`, `recipients: [{email, name?}]` (up to
50 unique addresses), and the matching provider options. Mailgun supports US/EU
regions; Brevo uses its transactional API; Mandrill uses Mailchimp Transactional,
not its Marketing API. SMTP supports verified STARTTLS and implicit TLS, and an
explicit plaintext mode for unauthenticated relays. API base URL overrides and
plaintext HTTP require deliberate configuration. Discord uses `webhook.url` with
an exact recognized Discord webhook route. Generic webhooks can use a bearer
credential separate from their URL.

Optional subject, text and HTML templates use the bounded substitutions documented
by the delivery package. No template executes code or reads files.

### Chat and push providers

Slack, Telegram, ntfy, Gotify and Pushover use the matching typed object inside
`config`; they do not use email `sender` or `recipients`. Each channel is one
destination. Exact fields, limits and supported options are documented in
[`notifyhttp`](../apps/node-go/internal/notifyhttp/README.md). For example, an
ntfy channel input is:

```json
{
  "name": "Operations push",
  "enabled": true,
  "config": {"kind": "ntfy", "ntfy": {"topic": "your-private-topic", "token": "your-token"}}
}
```

These providers support `templates.subject` (Title) and `templates.text` only.
HTML templates are rejected. Generated default wording is shortened to the
provider limit with `[truncated; see monitor details]`. Explicit templates and
direct message text are never shortened: oversized rendered messages fail with
`invalid_message` before sending. Slack text is escaped; Telegram does not
enable markup parsing.

Destination identity includes the channel resource ID and the provider's route
digest. Credential-only rotation retains receipts. Pushover's user/group key and
device identify the recipient; changing either creates a different destination.
Gotify's application token cannot reveal its logical application locally. Rotate
it in place only for the same application; create a new channel for another app.

Native HTTP sends use verified HTTPS with the process trust roots. An explicit
`allowInsecureHTTP` permits a configured HTTP host. They refuse redirects and
environment proxies and perform one bounded attempt without implicit replay.
Channel test requests still require confirmation in the dashboard and create no
incident receipts. Pushover emergency priority can cause provider-side repeating
notifications; it is unrelated to the node's own retry schedule.

Protocol 13 adds these provider names to private peer pages. Upgrade peers as a
coordinated same-build group; older peers reject the new protocol. This working
version has no release approval. Local native fixtures and peer-page/restart
tests do not establish live-provider or Alpine behavior.

The five structured dashboard forms passed workspace typechecking, eight pure
builder checks and a disposable production build. A separate Windows browser
proof passed 21 checks against a freshly built protocol-13 development node and
local provider fixtures: all five creates, exact-config renames and accepted
test requests, explicit test confirmation, 390-pixel layout, endpoint-only
browser storage and no page/console errors. The initial browser run's new test
selector incorrectly required `role="dialog"`; the existing modal uses native
`<dialog>`. That failure is retained, and only the new fixture selector changed.
The broader historical dashboard contract harness was not rerun or waived.

Current native source checks passed: full Canary package (143.604 s), delivery
(1.939 s), all 26 notification-leaf groups (1.384 s), targeted vet and Linux
amd64/arm64 leaf/delivery library compilation. The original frontend regex
typecheck failure is retained separately from the later successful frontend
checks. The small records are `.canary/notification-integration-result.json`
and `.canary/notification-browser-result.json`. Guarded validation retained no
binaries, databases, browser profiles or source copies and left zero scratch.
This does not validate the private JSONata replacement or a release artifact.

## Acceptance, retry and failover

An accepted result means the provider acknowledged the request, not that an email
arrived in an inbox or a phone displayed a notification. Discord requires its `wait=true` message acknowledgement;
an empty 204 does not confirm Discord delivery. Generic webhooks accept 2xx.
SMTP acceptance occurs after DATA. Each Mandrill recipient is submitted separately,
so rejection of one address cannot resend a previously accepted address.

Retries preserve their persisted attempt identity and honor bounded provider
`Retry-After` hints across restart. Connected nodes exchange successful receipts
and use staggered sender windows. Partitions and ambiguous sends can produce
duplicates; provider-side idempotency is not guaranteed. A credential rotation
preserves destination identity when its route remains the same. A changed route
or recipient is a distinct destination. A newly added destination receives an
ongoing outage, but does not receive historical down/recovery pairs for an outage
that had already closed.

Group suppression is evaluated per named destination. It requires fresh parent
failure evidence and successful parent delivery to that destination. Other
destinations fail open after the child's grace period. Legacy `webhookUrl`
configurations retain their original single-destination grouping behavior.
The node never diagnoses internet, power or VPN failure solely from a failed
dependency check.

Delivery status contains fixed error codes, HTTP status, attempts/failures,
next retry time and hashed provider acknowledgement IDs. It never returns raw
provider bodies, addresses, private error strings or secret URLs. Unavailable
destinations have no advertised next retry; re-enabling an eligible route can
resume its owed active-outage work. Successful receipts remain accepted after a
channel is disabled or deleted.

Notification tests use the same adapters but create no monitor, observation,
incident or successful incident receipt. They perform one attempt per recipient,
without automatic test retries. A node admits one batch at a time, with at least
five seconds between batches. Two dedicated test workers cannot consume the
eight incident-delivery workers. Test outcomes are available through the delivery
endpoint.

## Operational limits

Local admission allows 512 retained resource IDs, including channel tombstones.
Peer replication retains valid partition unions beyond that local admission
limit, up to the fixed membership bound. Configuration
and secret material are protected by node/database access and the peer transport;
they are not automatically encrypted at rest. Keep peer traffic on trusted
private networks or HTTPS. Delivery attempt and coordination records are retained;
long-term storage, memory and sweep work still require release capacity work.

Local provider fixtures and separate-process evidence are recorded in
[notification proof results](notification-proof-results.md). No live Discord
messages or real emails are sent by those tests.
