# Uptime Kuma parity audit

## Completion implementation checkpoint, 2026-10-25

This section supersedes the implementation labels in the dated inventories
below. The pinned Kuma 2.5.6 comparison and the independent Go/SQLite HA goal
remain unchanged. New implementation is not accepted merely because its
component checks pass; the completion PR must retain complete native,
container, race, architecture, process, recovery and review gates.

The protocol-19 candidate has 32 Canary monitor families. Kuma's separate
keyword and JSON-query forms are HTTP options, and its Group monitor is
represented by Canary's Groups resource. The three added specialist profiles
are Tailscale client ping, actual remote Chromium navigation, and the separate
GameDig 5.3.3 query service with its pinned 358-ID catalogue. The catalogue is
not evidence that all 358 games were exercised. PM2, system-service and Steam
retain their existing contracts and exact platform prerequisites.

The candidate also adds DNS SRV/SOA/CAA, WebSocket OAuth2 and subprotocol
negotiation, optional TCP TLS, monitor descriptions and cloning, safe resource
round trips, default notification selection and atomic apply-to-existing,
unsaved channel tests, status-page branding/refresh/safe Markdown/default-off
RSS, and dashboard registry rename/address maintenance and delegated credential
rotation. The Go dashboard still owns its accounts and one shared node list;
private node connections originate at that server.

The agreed first-release provider priority remains Discord, SMTP, Mailgun,
Brevo and Mailchimp Transactional; all are native adapters among Canary's
11 providers. SMS remains deferred. This does not claim the 108-provider Kuma
catalogue or every option in each provider. Generic webhooks and the optional
GameDig service each count as one integration, not an adapter for every API or
game listed upstream.

Known option differences remain explicit: NTLM, saved proxy profiles/SOCKS,
domain-registration expiry, Kafka AWS authentication, arbitrary browser steps,
raw status CSS, managed custom-domain routing, email/webhook subscriptions and
additional provider-specific extras are not implemented by this candidate.
Status pages accept an operator-supplied public URL but do not configure DNS,
TLS or reverse proxies. These rows remain partial in the comparison. Unsafe
WebSocket accept-header bypass is not offered. Retained causal-history growth
and the documented fixed-membership/capacity limits are operating constraints,
not a claim of unlimited retention or global exactly-once delivery.

Windows evidence includes focused and full changed-package checks, actual
private Chromium and GameDig process exercises, and static editor checkpoints.
Every result applies only to its recorded source. Final local Windows source
`54e5dd23...` passes three workspace type checks, 260 pure groups and 205/205
dashboard browser checks. Native Linux actual browser and PM2 interoperability,
extracted-package upgrade, current ordinary proofs, Alpine/race, arm64 and image
publication still need the completion head's own evidence. No tagged release or completed release
validation is claimed by this inventory. See [browser](browser-monitors.md),
[Tailscale](tailscale-monitors.md), [GameDig](game-server-monitors.md),
[upgrades](upgrades.md) and [shipping progress](shipping-progress.md).

## Historical implementation note, 2026-10-23

The comparison below is the original researched baseline. Its `Gap` and
`in progress` cells are historical. This current inventory supersedes those
implementation labels; the pinned upstream research remains unchanged.

Protocol 14 adopts official JSONata 2.2.2 in the existing bounded worker through
Goja and private finite-value guards. The former Go port has no production
fallback. Its seven reported false-healthy mechanisms no longer block this
source: all 130 unchanged independent review cases pass on actual Alpine.
The finite profile deliberately differs from unrestricted JavaScript JSONata;
the [migration notes](../apps/node-go/internal/jsonquery/RUNTIME_MIGRATION.md)
record the narrow accepted expectation changes. The earlier failed candidate
and emulated-platform results remain historical evidence, not current results.

Current development source includes 28 monitor types, sixteen service engines,
passive push, manual checks, Globalping, HTTP OAuth2, HTTP/MQTT/SNMP/MongoDB
JSONata, retries/inversion, maintenance, group hierarchy, tags, local observed
availability, public status pages and current-state badges. Protocol 15 adds
default-off public 24 h observed history and uptime badges, validated at PR #1
head `e12ba9e` by the complete CI workflow and exact artifact manifests. Protocol
16 adds local systemd/Windows service monitoring, whose Windows suites and
143/143 dashboard groups pass; merged main `187f3ae` subsequently passed
all seven hosted jobs, including native Linux arm64 and Linux race checks.
Protocol 17 adds Steam directory membership with local Windows acceptance;
its hosted Linux/race checks and review remain pending. See [system-service evidence](system-services.md). Package READMEs
state the options and protocol differences. Notifications include eleven
native providers with typed configuration, destination-specific identities,
durable attempts and peer receipts. Accounts support passwords, MFA, sessions
and roles. Recovery uses quarantine/activation and separately retained sequence
state. The license is MIT.

The protocol-14 Windows checkpoint passes all 17 process proofs, its refreshed dashboard
contract passes 124/124, and a controlled 13-to-14 upgrade proof preserves
configuration, accounts, sequence monotonicity and delivery receipts. The
dashboard harness explicitly accepts the catalogue's 25-to-26 addition, checks
uniqueness and Globalping, and adds ten checks for the five chat/push providers.
The earlier 113/114 result is preserved as historical. Current dependency scans
find no reachable Go vulnerability or npm advisory. Exact source and platform
scopes are in [the protocol-14 checkpoint](protocol14-checkpoint.md) and
[container validation](container-validation.md). These checks do not declare
the complete near-parity goal achieved.

Remaining feature gaps include hosted Steam validation, PM2, browser checks,
Tailscale and GameDig; additional options within implemented protocols; more of the
upstream notification catalogue; and custom domains, subscriptions and
status-page styling. Published observed history is explicitly one answering
node's record, with separate availability and coverage; current-state badges
still do not imply an uptime metric. Generic webhooks do not count as native
provider coverage. SMS remains explicitly deferred. Native arm64 execution passed for protocol 16;
protocol-17 arm64 checks and tagged, reproducible release-artifact publication
remain separate operational work.

Retained causal evidence and local delivery provenance are not forever bounded
by the wire-page limits. Their long-run storage, heap and lock costs remain
documented operating constraints; complete retained-history compaction is not
claimed. Further feature waves must preserve the agreed storage budget for
development and the existing HA/recovery/privacy contracts.

## Original comparison and research

Research date: 2026-10-22. Scope: read-only source/tooling audit; this document is the only authorized write. No implementation, dependency installation, build, container execution, commits, or delegated agents are part of this audit.

## Immediate feature-contract recommendations

1. Keep the accepted destination: near/identical monitoring and alert options to the current stable Uptime Kuma. Delivery waves below order the work; they do not redefine completion as an HTTP/ping MVP.
2. Pin the comparison to **Uptime Kuma 2.5.6**, released **2026-10-09**, commit **7ca4a3debd76443d8452da95d4040200a1479af4**. The stable editor exposes **34 distinct monitor types**; the server registers **108 notification providers**, including its Apprise adapter. Provider/category duplicates in the UI do not count twice. [Latest-release API](https://api.github.com/repos/louislam/uptime-kuma/releases/latest), [release](https://github.com/louislam/uptime-kuma/releases/tag/2.5.6), [tag identity](https://api.github.com/repos/louislam/uptime-kuma/git/ref/tags/2.5.6).
3. Prioritize Discord: explicit provider selection, valid Discord content/embeds, down/recovery/repeat/test events, editable formatting, and actionable delivery errors. A generic Canary incident JSON payload posted to a Discord URL is not a Discord integration.
4. Add reusable destinations and per-destination attempt/receipt identities before fan-out. Preserve connected receipt deduplication, sender failover, partition availability with duplicates allowed, and recovery tied to actual outage delivery. A receipt for one channel must never suppress another channel.
5. Implement and expose the monitoring options, not just type names. JSONata evaluation is part of the upstream contract despite the field being named `jsonPath`. SQL query execution, MQTT message evaluation, and Docker container state require actual protocol checks; TCP reachability alone does not satisfy them.
6. Do not count a prospective Apprise/notification bridge as 90+ implemented providers. A bridge counts as one working integration until its configuration, deployment, error handling, and individual capabilities are exercised and disclosed.
7. Keep Alpine in every Canary container stage. Upstream v2 removed its Alpine images; that is an upstream packaging difference, not permission to discard Canary's accepted packaging requirement. Container execution still needs a real Linux container runtime: neither Docker nor Podman is on PATH, and `wsl --status` / `wsl --list --verbose` report WSL is not installed here.

8. **Latest user clarification:** first-release notifications are **Discord, Mailgun, Brevo, Mailchimp Transactional, and SMTP**. SMS is explicitly later. This is a release-priority decision, not a claim that five adapters equal Kuma's whole catalogue. Mailgun and Mailchimp Transactional are Canary requirements beyond Kuma's native registry. Licensing remains undecided; do not select a license or label a custom source-available license open source.

## Evidence and status boundaries

The matrices compare the stable source, not `master` or an old v1 feature list. [Stable editor][editor], [monitor model][model], [monitor implementations][types], [provider registry][registry], and [provider implementations][providers] are pinned to the commit above. Wiki pages are current explanatory material and can describe older behavior; pinned executable source takes precedence.

**The workspace changed during this audit.** The initial baseline was HTTP/ping plus generic webhooks, token accounts, and 42 Go tests/seven process proof cases. By **07:26-07:30 EDT**, `notification_provider.go`, its tests, `auth_primitives.go`, account/session structures, and auth routing were being added by other work. The Discord code now formats bounded content, disables unsolicited mentions, requests confirmed webhook responses, and parses rate-limit delays. These are **in progress, not verified complete by this audit**. The HTTP/ping-only validator and receipt-only notification UI remained visible. Parent work on accounts/TOTP and the checks engine must reconcile the baseline gaps below against its final code and test results.

No test suite was run: build/test scripts write binaries, caches, or fixtures outside this document. Test declarations/source are evidence of existing coverage, not a fresh passing result. No `.git` directory was present (`git status` reported that this is not a repository), so there is no audited commit/diff identity for Canary. No secrets or live webhook endpoints were read or used.

| Area | Baseline capability actually present | Gap / next acceptance evidence |
| --- | --- | --- |
| Runtime | Independent Go nodes, SQLite, optional React/TS dashboard, CLI/API, local history/accounts | Keep working without dashboard or peers after every feature addition |
| HTTP | GET; default 2xx; explicit numeric accepted codes; five redirects; timeout and TLS verification | All options below; user-visible diagnostics instead of only generic request-failed messages |
| ICMP | Validated fixed `ping` arguments, bounded process output, deadline/cancellation | Packet size/count/address-family options; actual RTT rather than subprocess wall time |
| Alert engine | Persisted retry timers, outage/repeat/recovery identities, receipt replication/failover | Reusable destinations, destination-specific identities, failed-attempt API/UI; Discord adaptation is now in progress |
| HA/config | Version DAG/conflicts, tombstones, independent checks, partition availability, alias reconciliation | New schemas/secrets/maintenance must replicate safely; old nodes must not silently drop new fields |
| Groups | Named user groups, dependency monitor, grace, cycles/conflicts visible | Group health rollup/nesting and scheduled maintenance are separate behaviors |
| History | Bounded local observations; persistent incidents/receipts | Long-window uptime/response aggregates, retention controls, metrics/status publication |
| Accounts | Local token accounts and admin/editor/viewer enforcement | Password/TOTP/session CRUD is now in progress; review final auth flow and revocation tests |
| Operational UI | Monitor/group CRUD, conflict resolution, node views, incidents, successful receipts | Test notification, failed delivery, tags, maintenance, public status pages, backup/restore |

## Monitor catalogue: all 34 stable types

`Partial` means implemented with fewer options. `Gap` means absent from the baseline, not impossible in Go. Waves are implementation order; the near-parity comparison continues to include later waves. Each protocol needs success, refusal, timeout, malformed-response, cancellation, restart, redaction, and UI/API round-trip cases. An open socket alone does not prove an application protocol healthy.

| Type / upstream token | Stable behavior and type-specific options to preserve | Baseline / wave |
| --- | --- | --- |
| HTTP(S) `http` | HTTP request/response contract in next table | Partial / W1 |
| HTTP keyword `keyword` | Body contains keyword, inverted matching; HTTP options | Gap / W1 |
| HTTP JSON query `json-query` | JSONata expression, expected scalar and comparison operator; retry-only-on-status-failure option | Gap / W1 |
| TCP `port` | Host/port; plain/TLS/STARTTLS, CA/client cert/key, expected TLS alert; certificate expiry. [Source][tcp] | Gap / W2 |
| ICMP `ping` | Host, packet size, numeric output, packet count, per-request and total timeout; platform validation | Partial / W1 |
| DNS `dns` | Custom resolver(s)/port, A/AAAA/CAA/CNAME/MX/NS/PTR/SOA/SRV/TXT, last result, record conditions. [Source][dns] | Gap / W2 |
| Docker `docker` | Container ID/name and saved socket/TCP/TLS Docker host; running/paused/restarting and Docker health status, including pending. [Source][model] | Gap / W3 |
| System service `system-service` | Named systemd/Windows service; Linux/Windows host installs, hidden in upstream containers. [Source][service] | Gap / W5 |
| PM2 `pm2` | Named PM2 process and actual online status, not PID existence. [Source][pm2] | Gap / W5 |
| Browser `real-browser` | Beta Chromium navigation, screenshots/delay, configured remote browser. [Source][browser] | Gap / W5 |
| WebSocket `websocket-upgrade` | Upgrade handshake, subprotocol, accepted codes, ignore-accept-header option, headers, Basic/Bearer/OAuth/mTLS. [Source][websocket] | Gap / W2 |
| SFTP `sftp` | Host/port, username, password or private key/passphrase, optional path existence. [Source][sftp] | Gap / W3 |
| Group `group` | Parent/child rollup and group hierarchy; upstream computes children health. [Source][group] | Partial: Canary groups differ / W2 |
| Push `push` | Secret push URL; supplied up/down/message/latency and missed-heartbeat detection, retries/inversion. [Router][push] | Gap / W2 |
| Manual `manual` | Explicit manually controlled status. [Source][manual] | Gap / W2 |
| Globalping `globalping` | Location-selected remote ping/HTTP/DNS probes, subtype options, API token/quota failures. [Source][globalping] | Gap / W5 |
| gRPC keyword `grpc-keyword` | Target, user protobuf schema, service/method, JSON request, TLS flag, response keyword/inversion. [Source][grpc] | Gap / W3 |
| Kafka producer `kafka-producer` | Broker list, topic/message, TLS, auto-topic creation, SASL none/plain/SCRAM-256/SCRAM-512/AWS credentials. [Model][model] | Gap / W3 |
| MQTT `mqtt` | Broker host/port, credentials, topic/WebSocket path; message/JSONata/conditions matching. [Source][mqtt] | Gap / W3 |
| NTP `ntp` | UDP query; stratum, time-offset, root-dispersion thresholds and unsynchronized-server detection. [Source][ntp] | Gap / W3 |
| RabbitMQ `rabbitmq` | Multiple management API node URLs, username/password, alarm-health checks; not just AMQP connect. [Source][rabbitmq] | Gap / W3 |
| SIP `sip-options` | SIP OPTIONS transaction; upstream hides it in containers. [Source][sip] | Gap / W5 |
| SMTP `smtp` | Server verification; implicit TLS, required STARTTLS, or no STARTTLS. This is a monitor, separate from sending email. [Source][smtp-monitor] | Gap / W2 |
| SNMP `snmp` | Host/port, community, version, OID, query/operator/expected value; v3 username currently noAuthNoPriv. Do not promise upstream authPriv support. [Source][snmp] | Gap / W3 |
| Tailscale `tailscale-ping` | Tailscale-specific peer ping, not ordinary ICMP; upstream hides it in containers. [Source][tailscale] | Gap / W5 |
| SQL Server `sqlserver` | DSN, query, scalar result conditions. [Source][mssql] | Gap / W3 |
| MongoDB `mongodb` | Connection string, JSON command, JSONata/expected result. [Source][mongodb] | Gap / W3 |
| MySQL/MariaDB `mysql` | DSN/query, scalar conditions. [Source][mysql] | Gap / W3 |
| Oracle `oracledb` | Connection string, credentials, query, scalar conditions. [Source][oracle] | Gap / W3 |
| PostgreSQL `postgres` | Connection string and optional query. [Source][postgres] | Gap / W3 |
| RADIUS `radius` | Server/port, user/password/shared secret, calling/called station IDs, authentication result. [Model][model] | Gap / W3 |
| Redis `redis` | Authenticated connection string, TLS validation option, actual PING. [Source][redis] | Gap / W3 |
| GameDig `gamedig` | Game protocol catalogue, host/port, given-port-only option, protocol token; not interchangeable with Steam. [Source][gamedig] | Gap / W5 |
| Steam game server `steam` | Steam server lookup/API key plus server reachability; hostname resolution. [Source][steam] | Gap / W5 |

### Shared and HTTP option contract

Upstream schemas/defaults are in the [editor][editor], [model][model], [query helper][util], [condition operators][operators], [proxy implementation][proxy], and [domain-expiry implementation][domain]. Preserve option semantics even if Canary uses different names/units. Do not blindly copy undocumented upstream quirks.

| Capability | Contract and implementable acceptance gate | Canary baseline |
| --- | --- | --- |
| Scheduling | Normal interval, timeout, retry count and separate retry interval; pending state until failures exceed retries. Default upstream interval/retry interval 60s and retries 0. Table-driven transition tests plus a real scheduler test | Interval/timeout; first failure immediately down |
| Resending | Upstream `resendInterval` counts continued DOWN checks; Canary `repeatAfterMs` is elapsed time since delivery. Expose/document both semantics or an explicit conversion; test edits and restart | Elapsed-time repeats only |
| Inversion | `upsideDown` flips success/failure, interacting with retries and recovery. Test both directions and canceled/stale work | Missing |
| Request | GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS; headers and JSON/form/XML bodies; validation/content type and byte bounds. Fixture server must assert exact method/headers/body | GET only; response body discarded |
| HTTP auth | None, Basic, Bearer, NTLM domain/workstation, OAuth2 client credentials (token URL, client ID/secret, scopes, audience, client authentication method), mTLS CA/cert/key. Exercise expired tokens and failed refresh; do not confuse probe OAuth with dashboard SSO | Missing; URLs reject embedded credentials |
| HTTP response | Accepted single codes/ranges, redirect limit, cache busting; keyword/inverted keyword; JSONata scalar evaluation with `==`, `!=`, `>`, `>=`, `<`, `<=`, `contains`. Test malformed JSON, null, arrays/objects, invalid queries and numeric coercion against pinned upstream helper | Numeric codes; fixed five redirects (upstream default ten) |
| Retry selector | JSON-query `retryOnlyOnStatusCodeFailure`: distinguish query mismatch from retryable transport/status failure; prove transitions | Missing |
| TLS/certificates | Validate trust/hostname by default; explicit ignore-TLS option, custom trust and client credentials; certificate metadata and expiry alerts with threshold/repeat dedup, renewal/reset, chain coverage. Test local CA, expired/wrong-host certs, mTLS and renewal | Default platform trust only |
| Domain expiry | RDAP-supported domain detection, warning thresholds, unsupported registry feedback, cached/rate-limited lookups, persisted notification history; independent from certificate expiry | Missing |
| Proxies | Saved authenticated HTTP/HTTPS/SOCKS/SOCKS4/SOCKS5/SOCKS5h choices, per-monitor selection/default/application; remote-DNS semantics matter. Verify proxy traffic and credential isolation | Process environment HTTP proxy only |
| Addressing | Relevant host/port/protocol and IP family; validate IPv6 literals, DNS names and resolver failures | Basic HTTP/ping target validation |
| Conditions | Typed grouped AND/OR expressions where supported: DNS records, MQTT topic/message/JSON value and SQL scalar results; string equality/contains/prefix/suffix and numeric comparisons | Missing |
| Diagnostic history | Response and error-response saving, bounded max length (upstream defaults: successful response off, error response on, 1024-byte setting), useful safe error detail; never put credentials into public output | Status/message/latency only |
| Organization | Description, parent/nesting, order/weight, tags with color/value, clone, pause/resume/delete, per-monitor notification associations/defaults | Named groups, pause/edit/delete; no tags/clone/nested rollup |
| Browser/other settings | Saved remote browsers, Docker hosts and notification targets are real managed resources with test actions; show unsupported platform prerequisites before enabling | Missing |

**Canary-specific lifecycle gate:** extend `sameCheck` in `lifecycle.go` for method, body, headers, auth, response assertion, TLS/proxy and every result-changing option. A semantic edit retires old work without a false recovery. Name/description and delivery-policy changes retain correct incident continuity. Keep all new serialized schemas within bounded peer pages; currently API bodies are 32 KiB and there are 200-monitor/200-group limits. Protobuf/certificates or a large header/body configuration may require a deliberate, tested limit change.

## Notifications: first-release contract and full comparison

First release needs these five adapters plus the existing generic webhook. Native email APIs are distinct integrations; silently routing them all through generic SMTP does not fulfill provider-specific setup expectations. Every adapter needs saved configuration, masked secret editing, multiple monitor assignments, test delivery, and down/recovery/repeat messages.

| First-release adapter | Required configuration / behavior | Acceptance gate |
| --- | --- | --- |
| Discord | Webhook, display name, normal/minimal/custom format, prefix, include/hide URL, silent flag, channel/forum/thread routing; see detailed gate below | Provider-valid payloads and a visible test/error result |
| SMTP | Host/port, authentication, implicit TLS/required STARTTLS/plain policy, CA validation, From/To/CC/BCC/Reply-To, subject/text/HTML templates and headers; Kuma additionally has DKIM controls. [Stable sender][smtp-provider] | Real local SMTP sink with auth/TLS cases; rejected recipient is failure; envelope and MIME asserted |
| Mailgun | API key, verified sending domain, US/EU endpoint, sender/recipients, subject/text/HTML, optional headers/tags. Multipart Messages API, safe response parsing. [Official API](https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/messages/post-v3--domain-name--messages) | Request/auth/domain/region fixtures; accepted message ID; rate limits and provider rejection surfaced |
| Brevo | API key, sender, recipients/CC/BCC/reply-to, subject/text/HTML or declared template controls, headers/tags; `POST /v3/smtp/email`. [Official API](https://developers.brevo.com/reference/send-transac-email) | Validate 201/messageId and failure bodies; stable idempotency strategy where supported |
| Mailchimp Transactional | Transactional/Mandrill key, sender, recipient types, subject/text/HTML, reply-to/headers/tags; API message submission. [Official outbound-email docs](https://mailchimp.com/developer/transactional/docs/outbound-email/) | Inspect each recipient result; HTTP 200 alone is insufficient when recipients are rejected/invalid. Distinguish sent/queued/failed and partial recipient acceptance |

Email acceptance means **accepted by the SMTP server/provider**, not guaranteed inbox delivery. Record provider message IDs where available. Do not resend to already accepted recipients when retrying only a partially rejected batch; persist recipient-level outcomes or use separately keyed recipient jobs. Delivery/bounce webhooks can be a later explicitly scoped feature. Test buttons send real external messages only when the operator chooses them; this audit sent none.

### Complete stable registry

The 108 registration class names below are an inventory, **not a supported-provider claim for Canary**. They were counted from actual `new Provider()` registrations, not the README's historical marketing count or Apprise's transitive services. [Pinned registry][registry].

```text
Alerta, AlertNow, AliyunSms, Apprise, Bale, Bark, BearSMS, Bitrix24,
ClickUp, ClickSendSMS, CallMeBot, SMSC, DingDing, Discord, Fluxer, Elks,
EgoSMS, Feishu, FreeMobile, GoogleChat, GoogleSheets, Gorush, Gotify,
GrafanaOncall, HomeAssistant, HeiiOnCall, Keep, Kook, Line, LunaSea,
Matrix, Mattermost, NextcloudTalk, Nostr, NotifyApp, Ntfy, Octopush,
OneChat, OneBot, Onesender, Ooredoo, Opsgenie, JiraServiceManagement,
PagerDuty, FlashDuty, Flowtriq, PagerTree, Pinglet, Plivo, PromoSMS,
Pumble, Pushbullet, PushDeer, Pushover, PushPlus, Pushy, RocketChat,
ServerChan, SerwerSMS, Signal, Signalgrid, SIGNL4, SMSManager,
SMSPartner, Slack, SMSEagle, SMSGateway, SMTP, Squadcast, Stackfield,
Teams, TechulusPush, Telegram, Teltonika, Telnyx, Threema, Twilio,
Splunk, Webhook, WeCom, GoAlert, ZohoCliq, SevenIO, Whapi, WAHA,
Evolution, OpenWa, GtxMessaging, Cellsynt, Wpush, WxPusher, Brevo,
Resend, YZJ, AmootSMS, SMSPlanet, SpugPush, Notifery, SMSIR, SendGrid,
TurboSMTP, Whatsapp360messenger, Webpush, HaloPSA, Max, VK, VKTeams, Milky
```

| Catalogue capability | What actual parity requires / implementation approach |
| --- | --- |
| Reusable notifications | Named saved destinations, defaults for new monitors, apply to existing monitors, multiple associations; per-destination success/failure. [Dialog][dialog] |
| Generic webhook | Configurable method, JSON/form/custom templated body and headers; test action. Existing fixed POST event JSON is partial. [Sender][webhook-provider] |
| Chat | Provider-specific payload/cards, channel/chat IDs, mentions, topics/threads, formatting and templates. Examples: [Slack][slack], [Telegram][telegram], [Teams][teams]. These are not one interchangeable webhook schema |
| Email | SMTP plus Brevo/Resend/SendGrid/TurboSMTP in Kuma; selected Canary Mailgun/Mailchimp APIs expand this set. Sender/recipient/auth/template controls must actually work |
| Push | Tokens/apps/users/topics, priority/sound/TTL/attachments where supported; browser Web Push needs subscription lifecycle/VAPID and secure browser context |
| Incident management | Provider event action, severity/routing and matching recovery/resolve/ack behavior. [PagerDuty][pagerduty] has a dedup key and configurable recovery action; a plain text alert is incomplete |
| SMS/voice/regional | Accounts, source/destination numbers, gateway differences and error semantics. **Explicitly later**, including Twilio, Telnyx, Plivo and regional services |
| Gateways/bridges | Signal/WhatsApp and similar often require separately deployed gateways. Apprise in Kuma runs an installed executable. [Source][apprise] Implement a typed HTTP integration or an explicitly packaged fixed helper; no arbitrary shell commands |
| Templates/transport | Bounded template evaluation with documented context and escaping; network timeout, configured proxy, rate-limit handling, secret-safe diagnostics. [Base provider][base-provider] |

### Discord must-pass gate

Kuma's [Discord sender][discord] and [Discord form][discord-form] implement normal embeds, minimalist text, custom templated text, username/prefix, URL visibility, new forum posts, existing threads and silent notifications. The [notification dialog][dialog] tests the currently configured values and returns success/failure. Canary's in-progress formatter covers only a portion of this contract.

1. **Valid messages:** down/repeat/recovery/test each sends nonempty `content` or valid embeds. Include monitor, state, event time and safe failure detail; recovery includes downtime where known. Apply Discord content/embed bounds and escape/truncate Unicode correctly. Disable unsolicited mentions; opt-in configured mentions need an explicit policy. [Discord execute webhook](https://docs.discord.com/developers/resources/webhook).
2. **Test action:** an authorized API/CLI action and UI button use the same formatter/transport as real alerts, accept current edited settings, show progress and a useful result, and do not create incidents or mark a real outage delivered. Rate-limit tests and never expose the credential-bearing URL in errors.
3. **Confirmed acceptance:** recommended `wait=true`, requiring the documented created-message response rather than a redirect or arbitrary 2xx. Store a safe provider/message identifier; the current in-progress code already checks for a nonzero message ID. This stronger confirmation is a Canary implementation choice.
4. **Visible failures:** publish destination, monitor/incident/action, attempt count, last attempt, safe error category/HTTP status, next retry, and permanent/configuration failure versus waiting. Existing `attempt.Failures/NextAt` and successful receipts cannot explain whether a webhook was invalid, throttled or unreachable. Show this in the notification list and monitor detail.
5. **Rate limits and retry:** honor bounded fractional JSON/header `retry_after` and `Retry-After`; persist the due time across restart. Treat 400/401/403/404 distinctly from 429/5xx/timeouts. Surface required operator action and avoid unbounded tight retries. [Discord rate limits](https://docs.discord.com/developers/topics/rate-limits).
6. **HA and edits:** retries retain destination/event keys; a timeout after provider acceptance can still duplicate because Discord does not offer Canary's event-key dedup contract. Do not claim exactly once. Keep connected dedup/failover/partition/recovery tests and add two different destinations, URL changes, failed tests, 429/restart, thread routing, malformed/oversized responses and secret-redaction cases.

**Destination identity recommendation:** key work by `(monitor, destination ID, incident identity, kind, repeat slot)` and persist delivery outcomes independently. Add provider/recipient identity as needed for email. Define destination-edit/revision behavior explicitly: historical successes must not be erased or replayed automatically. A successful email delivery must not suppress Discord, and failed Discord must not block email. Decide how each node obtains secrets without replicating local accounts/passwords into the config DAG.

## Product/operations matrix

| Area | Stable Kuma contract | Canary gap and acceptance gate |
| --- | --- | --- |
| Maintenance | Manual, single window, cron, every-N-days, weekday, day-of-month; time/date ranges, duration, timezone/server timezone; affected monitors and status pages. [Wiki](https://github.com/louislam/uptime-kuma/wiki/Maintenance), [model][maintenance] | Missing. Test DST, midnight overlap, month-end, restart inside window, conflicting peer edits and expiry while partitioned. Show maintenance/suppression distinctly and avoid replaying old alerts after a completed window |
| Public status pages | Multiple slugs/domains; selected ordered public groups/monitors; title/logo/description/theme/CSS/footer; tags/certificate display, refresh interval, analytics choices, RSS; public incident create/edit/pin/resolve/history. [Editor][status-ui], [handler][status-handler], [router][status-router] | Missing. Explicit publication allowlist, public projection with no secrets, domain routing, incident lifecycle and configured retention. The private dashboard is not a public status page |
| Status-page authentication | Public read; authenticated administration. Password column is commented out in stable save handler | Do not invent upstream private/password-protected status pages as an established parity requirement. Additional private-page access is a separately scoped feature |
| Login/2FA | Username/password, password changes, rate limiting, TOTP enroll/verify/disable, token login and invalidation; emergency reset/remove-2FA scripts. [Auth][auth], [security UI][security], [server][server] | Parent implementation underway. Gate account CRUD/roles, password hashing, generic login errors, bounded auth work, TOTP replay protection, session expiry/logout/revoke/password-change revocation, last-admin protection, recovery/reset, and local-only account storage |
| SSO/API auth | Probe OAuth2 is not dashboard SSO. Stable security UI offers disabling auth for an external auth proxy; `/metrics` supports API-key auth | Keep SSO claims explicit; do not count upstream probe OAuth as OIDC login support. Retain least-privilege local API tokens and separate metrics credentials |
| Backups/restore | v2 removed deprecated JSON backup/restore; supported guidance is backup of stopped data directory. [Migration guide](https://github.com/louislam/uptime-kuma/wiki/Migration-From-v1-To-v2) | SQLite persistence is not a restore procedure. Document stopped-node data backup or implement a consistent SQLite backup API; prove restore with identities, credentials, config DAG, local history and receipts, including WAL safety |
| Import/export | Legacy v1 JSON feature is not a current v2 gate; third-party migration tools are not built-in support | Versioned Canary config export/import and Kuma migration are useful chosen additions. Validate/dry-run/map unsupported types and secret handling; never silently omit fields or import upstream DB as Canary DB |
| Tags | Shared tag catalogue, monitor associations, color/value, filtering and optional public visibility. [Tag model][tag] | Missing. Round-trip, conflicts/deletion and public redaction; groups do not substitute for tags |
| Metrics/badges | Status/response time, certificate validity/days, sliding uptime and mean response; `/metrics`, API keys and public status/uptime/ping badges. [Exporter][metrics], [wiki](https://github.com/louislam/uptime-kuma/wiki/Prometheus-Integration), [badge router][push] | Missing. Validate exposition/auth, stale/pending/maintenance values, bounded labels, time units and known fixtures. Distinguish node-local history from cluster aggregates; do not fabricate unavailable historical uptime |
| Retention/history | Important transitions, heartbeat charts and long-window aggregates/retention controls | Current bounded local history is partial. Preserve unknown gaps and explain uptime denominator/maintenance exclusions; test retention and performance |
| Release/packaging | Stable release notes include security dependency fixes; Kuma v2 uses non-Alpine images | Canary retains all-Alpine stages, optional dashboard and independent nodes. Require real Linux-container execution; upstream runtime packaging is not the template to copy |
| Licensing | User selection is pending | Release gate: record user choice, then add accurate license/attributions/dependency notices. Do not choose GPL/custom terms here or call custom source-available licensing open source |

## Go implementation feasibility, without arbitrary shell execution

These are feasible candidates, not dependencies installed or validated by this audit. Pin versions after checking their current Go support, license, vulnerabilities and `CGO_ENABLED=0` Alpine builds. Use contexts and bounded result sizes; close connections and keep slow I/O outside the monitor-state lock.

| Family | Practical implementation | Remaining compatibility evidence |
| --- | --- | --- |
| SQL | `database/sql` with [pgx](https://github.com/jackc/pgx), [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql), [Microsoft go-mssqldb](https://github.com/microsoft/go-mssqldb), pure-Go [go-ora](https://github.com/sijms/go-ora) | DSN/auth/TLS/query cancellation, timeout and scalar conditions against real servers; Oracle does not inherently require an OCI shell/client binary |
| Mongo/Redis | Official [mongo-go-driver](https://github.com/mongodb/mongo-go-driver), [go-redis](https://github.com/redis/go-redis) | Auth/TLS, JSON command/query behavior and connection cleanup |
| MQTT/Kafka/RabbitMQ | [Paho MQTT](https://github.com/eclipse-paho/paho.mqtt.golang), pure-Go [franz-go](https://github.com/twmb/franz-go); RabbitMQ's matching check can use its HTTP management API | Real message/retained-message behavior, SASL variants, errors and broker deadlines. Kafka production changes the external system: document one message per assigned node/check and do not promise exactly once |
| Docker | Direct [Engine HTTP API or Go SDK](https://docs.docker.com/reference/api/engine/), Unix socket or authenticated TLS endpoint | Inspect running/paused/restarting/health, authorization failures and missing container; no `docker exec` or CLI required |
| Network protocols | Standard `net`, `crypto/tls`, `net/http`; [gosnmp](https://github.com/gosnmp/gosnmp), [beevik/ntp](https://github.com/beevik/ntp), [pkg/sftp](https://github.com/pkg/sftp) with Go SSH; gRPC/protobuf dynamic descriptors | Preserve protocol-specific checks, SSH host trust and cancellation; gRPC requires user schema handling, not only hardcoded health service |
| Games | [go-a2s](https://github.com/rumblefrog/go-a2s) covers Source/A2S; other game protocols need additional implementations | A2S is not GameDig's full game catalogue. Build a protocol-by-protocol supported list and fixtures; Steam's upstream web lookup also needs its API behavior |
| Notifications | Direct `net/http` adapters for selected APIs; an SMTP/MIME library. [Shoutrrr](https://github.com/containrrr/shoutrrr) or [notify](https://github.com/nikoksr/notify) can supply transport building blocks | Neither is automatic Kuma provider/options parity. `notify` itself cautions about external-service reliability; wrap and test provider semantics. Prefer focused direct adapters for the first five |
| JSONata/templates | [jsonata-go](https://github.com/blues/jsonata-go) is a candidate; a bounded Liquid-compatible engine for templated providers | jsonata-go documents parity with JS **1.5.4**, while pinned Kuma depends on **2.2.2**. Differentially test expressions/operators; JSONPath substitution cannot close this gap. A modern bounded evaluator/compatibility work may be needed |
| Browser/host integration | Actual remote browser service or packaged Chromium; typed OS APIs/fixed helpers for service manager, PM2, Tailscale | Browser is a declared optional runtime prerequisite, not arbitrary shell monitoring. Match upstream protocol for remote browser. Systemd services do not become available inside an ordinary Alpine container |

## Package/tool/container prerequisite audit

All checks here were read-only. Existing pin validity is different from a passing clean install or container build.

| Check | Observed result / action |
| --- | --- |
| Node/npm | Local Node **24.22.1**, npm **11.19.1**; official [Node index](https://nodejs.org/dist/index.json) confirms this Node release and npm pair. `engines` currently accepts `>=24.13.0 <25`; Docker web builder uses 24.22.1 |
| Go | No global `go` command; portable `.canary/tools/go/bin/go.exe` reports **go1.27.3 windows/amd64**. `scripts/go.mjs` chooses `CANARY_GO`, portable tool, then PATH, with `CGO_ENABLED=0`/`GOTOOLCHAIN=local` |
| Go archive integrity | Actual local ZIP SHA-256 matches [official Go metadata](https://go.dev/dl/?mode=json): `f26081f438319757f8a1b46d7d0b2c5ec7583d4038e485e0aa725cfde7885fff`. This verifies the archive, not every extracted tool file |
| Go packages | Initial direct dependency `modernc.org/sqlite v1.61.0`; [module metadata](https://proxy.golang.org/modernc.org/sqlite/@v/v1.61.0.info) exists. Auth work added `golang.org/x/crypto v0.58.0` during audit. New drivers not yet established by this baseline |
| npm lock | Lockfile v3 resolves React/ReactDOM **19.3.1**, Vite **8.3.5**, plugin-react **6.1.2**, TS **5.9.3**, tsx **4.23.15**. Version endpoints checked for React/ReactDOM/Vite/TS/tsx; manifests use caret ranges, so release installs should use `npm ci` |
| Registry image availability | Docker Hub APIs returned active tags for [golang:1.27.3-alpine](https://hub.docker.com/v2/repositories/library/golang/tags/1.27.3-alpine), [alpine:3.24](https://hub.docker.com/v2/repositories/library/alpine/tags/3.24), [node:24.22.1-alpine](https://hub.docker.com/v2/repositories/library/node/tags/24.22.1-alpine), [nginx-unprivileged:stable-alpine](https://hub.docker.com/v2/repositories/nginxinc/nginx-unprivileged/tags/stable-alpine), including linux/amd64 and arm64 |
| Reproducibility gap | All four image tags are mutable; `stable-alpine` also floats nginx version. Pin reviewed manifest digests for release and record automatic update policy; availability does not prove installed packages/base Alpine minor or reproducible output |
| Runtime prerequisites | Node image installs CA roots/iputils, creates unprivileged user/data ownership; Compose grants NET_RAW and no-new-privileges. Verify ping actually works as that user/capability combination on the target kernel, TLS roots work, volume persistence/permissions and healthcheck work |
| Execution availability | Docker/Podman absent from PATH; WSL executable exists but reports WSL not installed. No container/image was run. Existing Linux amd64/arm64 binaries and cross-builds are not Alpine execution proof |
| Verification workflow | Root `npm run check` defines typecheck, vet, Go tests, process proofs and builds. This audit did not run it. Existing script `build-linux` builds amd64; release needs explicit supported-architecture jobs and execution evidence |

Observed manifest-list digests, useful for the parent to review/pin (not modified here):

```text
golang:1.27.3-alpine  sha256:d530cf60723abf1ffae0cd16bb546b379ec0550d2f3815e5b16ed69dbc00fba4
alpine:3.24          sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35
node:24.22.1-alpine  sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53
nginx stable-alpine sha256:f5116ed4606f3eb015dd4a8e28cb9b351476dc0ad4bc6e9f93b19cec130cdf03
```

## Execution waves and release gates

**Hard product destination:** preserve independent nodes, optional UI, all-Alpine builds, local history/accounts, config conflicts and existing HA guarantees while closing the monitor/options/product comparison. **Chosen first-release notification scope:** Discord plus the four named email adapters; SMS later. The 108-provider inventory remains an honest comparison, not a requirement to implement all 108 before this clarified first release. Do not label the restricted provider set broad notification parity.

| Wave | Dependencies and executable exit gate |
| --- | --- |
| W0: delivery/config foundation | Versioned typed monitor/provider schemas and capability discovery; backward-compatible migration or explicit protocol rejection; per-destination/recipient attempts and receipts. Finish Discord test button and failure API/UI. Add durable outcome fields, secret redaction and retry policy before broad adapter fan-out |
| W1: existing check completeness | W0 schema/lifecycle support. HTTP methods/auth/bodies/assertions/TLS/certificates/proxy, ICMP options, pending/retry/inversion/resend semantics. Show configured options in API/CLI/UI. Real HTTP/TLS/proxy fixtures and semantic-edit/partition tests must pass |
| W2: network/passive + first-release email | W0/W1 scheduling and outcomes. TCP/DNS/SMTP/WebSocket/push/manual/group rollup. SMTP/Mailgun/Brevo/Mailchimp adapters with recipient-aware acceptance, templates and test actions. Push deadline/replay and cross-node observation ownership are specified and tested |
| W3: application protocols | W1/W2 evaluator and transport abstractions. Real SQL/Redis/Mongo/MQTT/Kafka/RabbitMQ/Docker/SFTP/gRPC/SNMP/NTP/RADIUS checks; authenticated fixture services and failing/malformed cases, not socket-only substitutes |
| W4: operational completeness | W0 config/credentials + history semantics. Maintenance/timezones, tags, metrics/badges, public status pages, backup/restore; merge parent's password/TOTP/session/account work and test revocation/local isolation. This track can progress alongside W2/W3 |
| W5: specialist breadth | Actual browser and host prerequisites, Globalping, Steam/GameDig protocol coverage; broaden remaining nonsms providers as tracked scope. Later SMS/regional adapters stay visibly unimplemented until real integrations pass. Document exact platform-conditioned exclusions |
| W6: release evidence | Clean locked install/build/checks; supported Linux architecture container execution; fresh install, node restart, upgrades and restore; real multireplica partitions/failover plus multiple destinations; no missing selected-provider settings; performance/retention/capacity limits; docs and user-selected license complete |

For the first release, record every row as `implemented + verified`, `partial with listed missing options`, `later by explicit user choice`, or `platform prerequisite`. For monitoring rows, a later wave is scheduling priority rather than a user waiver. Any uncompleted monitoring parity rows must remain explicit release gaps; do not silently turn them into an MVP definition. Five notification adapters can be described accurately as five adapters, without claiming the 108-provider catalogue.

Final go/no-go checks for the parent:

- Every claimed type/option has an API/CLI/UI configuration path, persistence, restart behavior and actual protocol evidence; unsupported options are rejected rather than ignored.
- Selected Discord/email integrations have visible test results and failed delivery, safe credentials, provider-specific acceptance parsing, durable retry timing, and independent channel outcomes. No credentials are needed to finish deterministic fixtures; a live provider smoke test is a separately identified operational validation.
- Existing HA guarantees remain intact after new fields/destinations: connected dedup, sender death, provider isolation, partition duplicates allowed, alias reconciliation, all-assigned-nodes recovery, metadata edits, semantic retirement and dependency/grace behavior.
- Maintenance/status/metrics expose unknown, pending, suppressed and local-versus-cluster history truthfully. Data backup/restore and schema upgrade procedures are exercised.
- Container checks are run on a real Linux runtime before claiming Alpine production execution. Record unresolved capability/platform gaps and licensing decision; do not mark those gates passed from a cross-build or source audit.

[editor]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/pages/EditMonitor.vue
[model]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js
[types]: https://github.com/louislam/uptime-kuma/tree/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types
[registry]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification.js
[providers]: https://github.com/louislam/uptime-kuma/tree/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers
[tcp]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/tcp.js
[dns]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/dns.js
[service]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/system-service.js
[pm2]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/pm2.js
[browser]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/real-browser-monitor-type.js
[websocket]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/websocket-upgrade.js
[sftp]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/sftp.js
[group]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/group.js
[push]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/routers/api-router.js
[manual]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/manual.js
[globalping]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/globalping.js
[grpc]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/grpc.js
[mqtt]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/mqtt.js
[ntp]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/ntp.js
[rabbitmq]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/rabbitmq.js
[sip]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/sip-options.js
[smtp-monitor]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/smtp.js
[snmp]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/snmp.js
[tailscale]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/tailscale-ping.js
[mssql]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/mssql.js
[mongodb]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/mongodb.js
[mysql]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/mysql.js
[oracle]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/oracledb.js
[postgres]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/postgres.js
[redis]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/redis.js
[gamedig]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/gamedig.js
[steam]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/steam.js
[util]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/util.ts
[operators]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-conditions/operators.js
[proxy]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/proxy.js
[domain]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/domain_expiry.js
[dialog]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/components/NotificationDialog.vue
[discord]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/discord.js
[discord-form]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/components/notifications/Discord.vue
[smtp-provider]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/smtp.js
[webhook-provider]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/webhook.js
[slack]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/slack.js
[telegram]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/telegram.js
[teams]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/teams.js
[pagerduty]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/pagerduty.js
[apprise]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/apprise.js
[base-provider]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/notification-providers/notification-provider.js
[maintenance]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/maintenance.js
[status-ui]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/pages/StatusPage.vue
[status-handler]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/socket-handlers/status-page-socket-handler.js
[status-router]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/routers/status-page-router.js
[auth]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/auth.js
[security]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/components/settings/Security.vue
[server]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/server.js
[tag]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/tag.js
[metrics]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/prometheus.js
