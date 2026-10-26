# Notification channels

The runtime supports reusable Discord, SMTP, Mailgun, Brevo,
Mailchimp Transactional (Mandrill), JSON webhook, Slack, Telegram, ntfy, Gotify
and Pushover channels. Channel configuration is
replicated with explicit revision conflicts and deletion tombstones. Account
credentials, browser sessions and delivery attempts remain local to each node.

## Message presets and previews (protocol 20)

The channel editor offers **Built-in**, **Compact** and **Detailed** message
presets, with Down, Still down, Recovery and Test sample previews. Choosing a
preset is explicit; replacing custom wording asks first, and Undo remains
available until the next edit. Changing provider never applies a preset or
overwrites custom text. Previewing uses local sample data and sends nothing.
The existing **Test these settings** action is a separate, confirmed real send.

For Discord, protocol 20 adds custom plain messages and an explicit **Card**
layout (`webhook.embed`). The card uses event colors and a UTC event timestamp.
The frame in the dashboard is an illustration; its rendered text follows the
node's substitution rules. Email HTML is shown as escaped source, never executed
inside the preview. Webhook channels still send the JSON event and ignore message
templates. Their unused templates cannot fail a JSON webhook's delivery.

Protocol 20 also activates nonempty Discord templates stored by earlier builds.
There is no automatic clearing or separate migration opt-in; this is an
intentional pre-release compatibility change.

The editor checks the selected node's advertised peer protocol. Older or
unidentified nodes retain the original six substitutions and fixed Discord
message. Newly entered protocol-20 substitutions cannot be saved or tested on
them; stored settings are preserved. Upgrade the cluster together before using
the additions. The dashboard does not claim every other node already supports
the selected node's format.

| Placeholder | Value |
| --- | --- |
| `{{event_kind}}` | `down`, `repeat`, `recovery` or `test` |
| `{{monitor_name}}` | Monitor name; `Notification test` for a test |
| `{{monitor_id}}` | Monitor ID |
| `{{incident_id}}` | Incident ID |
| `{{observations_summary}}` | Existing per-node ID, state and message lines, unchanged |
| `{{idempotency_key}}` | Stable attempt ID across retries |
| `{{status}}` | `DOWN`, `STILL DOWN`, `UP` or `TEST` |
| `{{monitor_type}}` | Check type |
| `{{monitor_target}}` | Safe target: web scheme/host/port only, or a supported network host/port; empty when no safe form exists |
| `{{opened_at}}` | Outage opening time, UTC RFC 3339 |
| `{{resolved_at}}` | Recovery time, only for recovery events |
| `{{downtime}}` | Recovery duration, such as `7m 12s`; first two nonzero units, floored to seconds |
| `{{node_results}}` | Per-node result lines, with rounded milliseconds when latency is available |
| `{{sender_node}}` | Sending node's name |

Only the first six placeholders exist before protocol 20. Substitution happens
once: a monitor name containing `{{status}}` does not become template source.
Unknown or spaced forms such as `{{ monitor_name }}` are sent literally. The
editor warns about look-alike tokens. There are no conditionals, so a Detailed
preset's recovery-only lines are empty on an outage. Tests leave monitor type,
target, incident times, downtime and result lines empty; they do not invent an
outage or expose another monitor's settings.

The safe target omits URL credentials, paths, queries and fragments. Database
DSNs, push tokens and private browser/GameDig service addresses are never used
as that value. Check messages are still the messages produced by your checks;
use trusted notification destinations for them. Monitor descriptions are not
automatically included.

Custom Discord values have Markdown punctuation escaped; template-authored
formatting remains. All mentions are disabled. Plain title plus text must fit
2,000 UTF-16 units; a card title must fit 256 and its description 4,096. An
oversized custom result records `invalid_message` instead of being shortened.
Each empty card template falls back independently to built-in wording. Empty
templates with Card off preserve the previous fixed Discord payload exactly.
Generic webhook payloads and legacy per-monitor webhook URLs also retain their
existing wire format.

`MonitorInput.notificationIds` selects up to 16 channels. In protocol 19,
omitting it on a new monitor without a legacy webhook selects the enabled
channels marked `isDefault`. An explicit empty array selects none. Existing
monitors are unchanged. Conflicting or causally incomplete enabled defaults
fail closed with 409; more than sixteen defaults requires an explicit list. `webhookUrl` remains
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
- `POST /api/notification-channels/preview-test` accepts the complete channel
  input, including disabled or unsaved settings, and returns
  `{preview:true,attempts:DeliveryAttemptView[]}` (202). It does not save the
  channel or create an incident or receipt. The ordinary two-worker admission
  and five-second test throttle still apply.
- `POST /api/notification-channels/:id/apply` accepts
  `{revision,monitors:[{id,revision}]}` for one to 200 monitors. It adds the
  channel while retaining existing destinations, in one transaction. Any
  stale/conflicting member or destination-limit failure rejects the entire
  batch. It returns `{changedIds:[...]}` and keeps each check generation.
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

A node records completion for a named destination only when it observes the
child close with applicable dependency or grace evidence. That local record
survives its own database restart. Peers without the destination's historical
record fail open; incident-wide legacy suppression cannot supply that proof.
A successful down receipt takes precedence and still requires recovery.
Peer up-observation frontiers may advance without adding outage runs, so that
refinement preserves completion when the opening, runs and resolution time
stay unchanged. A changed resolution time remains uncertain and fails open:
parent acceptance may have followed a newly earlier resolution, and wall-clock
comparison across nodes is not introduced as historical authority.

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
