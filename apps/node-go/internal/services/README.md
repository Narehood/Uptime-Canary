# Service protocol engines

Integration contract (package `services`; the parent owns runtime/API integration):

```go
func Validate(Config) error
func (Config) Validate() error
func Check(context.Context, Config) Result
func (Config) Check(context.Context) Result
```

`Config` selects `Type` (constants `Docker`, `Redis`, `Postgres`, `MySQL`, `MQTT`,
`SFTP`, `MongoDB`, `SQLServer`, `NTP`, `GRPC`, `SNMP`, `RADIUS`, `SIP`, `RabbitMQ`,
`Kafka`, `Oracle`;
values `docker`, `redis`, `postgres`, `mysql`, `mqtt`, `sftp`, `mongodb`, `sqlserver`,
`ntp`, `grpc`, `snmp`, `radius`, `sip-options`, `rabbitmq`, `kafka-producer`, `oracledb`),
mandatory `Timeout` (>0, <=5 minutes), and exactly one corresponding option pointer.
All public structs have camelCase JSON tags. Durations are Go durations; the parent
converts public millisecond fields. `Result` contains `State` (`up`, `down`, or
Docker-only `pending`), `Message` (fixed sanitized text), and `LatencyMs`.
Validation errors identify fields without their values. Never put option structs
in public history: they contain credentials, PEMs, DSNs, and private queries.

Every check validates before I/O, applies a total deadline, honors earlier caller
cancellation, and closes its connections. No shell/process execution, environment
credentials, dashboard/database state, peer state, or daemon discovery is used.
Parent integration must include every semantic option in lifecycle comparison,
preserve pending semantics, and preserve HA/config/history behavior.

TLS defaults to verified trust/hostname and TLS >=1.2. `TLSConfig` supports additive
CA PEM, server name, paired client certificate/key PEM, and explicit unsafe
`ignoreVerification` (false by default; Oracle rejects it). Plaintext requires
explicit `plain` mode for Redis, databases, MQTT, and Kafka;
Docker uses its explicit http/https endpoint.
SFTP requires a pinned SHA256 host fingerprint or inline known_hosts data unless
`unsafeIgnoreHostKey` is explicitly true. No host-key trust-on-first-use.

## Protocol configuration and behavior

- `DockerConfig`: `endpoint` is an HTTP(S) origin or
  `unix:///absolute/canonical/socket`; `container` is an ID or a simple Docker
  name (no leading slash); optional `apiVersion` is such as `v1.51`; optional
  `tls`. Exactly one `GET /[version/]containers/{container}/json` is made. No
  redirect, proxy, environment Docker host, API negotiation, or write operation.
  HTTP 200 plus a valid `State.Running` is required. Stopped, paused, dead, unknown
  health, and unhealthy states are down. Restarting and health `starting` are
  pending; a running container without a healthcheck is up. Container health log
  output is neither interpreted nor returned. Docker socket access remains a
  privileged deployment choice: request a read-only socket mount and restrict
  access to the node; a `:ro` mount alone does not restrict Docker API methods.
  This engine sends only the inspection GET.
- `RedisConfig`: `address` is explicit host:port, optional `username`/`password`,
  `database` (default 0), `tlsMode` (`tls` default, explicit `plain`), optional
  `tls`. RESP2 AUTH (password or ACL username/password), optional SELECT, and PING
  must each receive their exact expected simple-string response. Authentication,
  server errors, truncated/oversized replies, or a different PING reply fail.
  No URL DSN, Sentinel, Cluster discovery, or environment credentials.
- `SQLConfig` for PostgreSQL and MySQL: private `dsn`, `query` (default `SELECT 1`),
  optional `condition`, `tlsMode` (`tls` default, explicit `plain`), optional `tls`.
  PostgreSQL accepts `postgres://user:password@host:port/database` and
  `postgresql://...`; user must be explicit, port defaults to 5432. MySQL accepts
  `user:password@tcp(host:port)/database` with explicit TCP address. DSN query
  options are rejected; use the engine's TLS fields. PostgreSQL keyword DSNs,
  multi-host/fallback/service/file parameters, Unix database sockets, and MySQL
  session parameters, local files, compression, multi-statements, and global
  TLS registries are unsupported. PostgreSQL refuses a nonempty `PGSERVICE` in
  the node environment because pgx otherwise reads a service file; all other
  driver environment credential/TLS/session settings are overridden. No
  PostgreSQL password/service/certificate files are loaded.
  PostgreSQL startup requests `default_transaction_read_only=on`. Queries must
  begin with SELECT, WITH, SHOW, VALUES, or EXPLAIN and have no semicolon except
  one optional final semicolon (even semicolons inside literals are rejected).
  This lexical restriction is not a SQL security parser: WITH/EXPLAIN/functions
  can have side effects, especially in MySQL. Operators must use a database
  account with read-only monitoring permissions. Query success requires rows;
  assertions inspect the first row/first column and still drain/bound subsequent
  rows. Empty results, extra result sets, query errors, and result limits fail.
  PostgreSQL uses one direct pgconn connection; MySQL has one connection in a
  per-check database/sql pool, zero idle connections, and closes the pool.
- `MQTTConfig`: explicit `address`, `topic` filter, optional
  `username`/`password`, optional `condition`, `tlsMode` (`tls` default, explicit
  `plain`), optional `tls`. Implements the MQTT 3.1.1 CONNECT/CONNACK and
  SUBSCRIBE/SUBACK exchanges, requesting QoS 0 with a fresh random alphanumeric
  client ID and clean session. Up requires an accepted subscription AND a
  publication whose topic and condition match. A matching publication arriving
  before SUBACK is held until SUBACK succeeds. Retained messages are accepted;
  QoS 1 publications receive PUBACK; QoS 2 is rejected. A mismatch continues
  waiting within the same deadline; no message is down, not up. Topic filters
  support `+`, terminal `#`, and MQTT's `$` wildcard rule. No publishing, broker
  persistence, background client, auto-reconnect, MQTT 5, WebSocket transport,
  JSONata, or JSON selectors. A best-effort DISCONNECT precedes successful close.
- `SFTPConfig`: `address`, `username`, exactly one `password` or `privateKeyPem`;
  optional `privateKeyPassphrase`; exactly one `hostKeySha256` (OpenSSH
  `SHA256:...`), inline `knownHosts`, or `unsafeIgnoreHostKey:true`; optional
  `path`. Establishes authenticated SSH, opens only the SFTP subsystem, and
  completes SFTP version negotiation. If a path is set it must pass remote Stat.
  There is no shell, exec request, upload, download, or local key/known_hosts file
  access. Inline known_hosts entries must match the exact normalized hostname
  and port; wildcard/hashed hosts and markers/certificate authorities are
  rejected. Key PEM envelopes validate before I/O; key decoding/passphrase
  validation happens in Check. OpenSSH bcrypt KDF rounds are capped at 32 before
  key derivation. Both encrypted and unencrypted private keys are exercised.

## New engine contracts for parent integration

These additions preserve all six previous engine contracts. The new engines
return only `up`/`down`, with fixed sanitized messages and elapsed `LatencyMs`;
Docker's existing `pending` behavior is unchanged. The option pointers in
`Config` are `MongoDB *MongoDBConfig`, `SQLServer *SQLConfig`, `NTP *NTPConfig`,
and `GRPC *GRPCConfig`. Exactly one pointer must match `Type` and mandatory
`Timeout` remains positive and at most five minutes. `grpc` is Canary's type
token; it is not a claim that all Kuma `grpc-keyword` options are supported.

- `MongoDBConfig`: private `dsn`, `tlsMode` (verified `tls` by default; explicit
  `plain`), optional `tls`, `collection`, `filter` (raw JSON), `scalarPath`
  (array of exact document field names), `condition`. DSN format is
  `mongodb://[user:password@]host[:port]/database`; default port 27017 and
  database `admin`. Optional DSN query keys are only `authSource` and
  `authMechanism` (`SCRAM-SHA-256` default or explicit `SCRAM-SHA-1`), with exactly
  one nonempty value each and explicit credentials. Anonymous endpoints are
  allowed without auth options. No SRV, multiple hosts, replica-set discovery,
  environment credentials, cloud/LDAP/Kerberos/X.509 authentication, URI TLS
  settings, files, or compression. Driver configuration is built from typed
  fields without `ApplyURI`. Direct mode and a dialer allow only the single
  configured endpoint, including when a hello advertises other hosts. Reads and
  writes are not retried. Explicit silent logger settings prevent environment
  logging options from opening a log path or publishing private commands. The
  official driver can inspect process/container environment metadata for its
  standard hello telemetry; this is not used for monitor credentials, endpoint
  selection or discovery. There is no blanket claim that imported libraries
  never inspect their own runtime environment. One pooled connection plus monitoring connections,
  at most four total dial attempts per check, are closed on completion.
  Every check requires an actual successful MongoDB ping. With `collection`, it
  additionally sends `find` with limit 1, batchSize 1, and singleBatch true; no
  getMore or writes. The filter is an optional flat JSON equality object with
  at most 16 unique keys. Values are null, boolean, string, or signed 64-bit
  integer; nested objects/arrays, dotted keys, `$` operators and duplicate keys
  are rejected. A document must exist. The optional scalar path and condition
  must be paired. Missing fields fail, BSON null matches JSON null, and accepted
  scalar types are strings, booleans, int32/int64, finite doubles, decimal128
  text, and null. Object/array/date/binary/ObjectID assertions are unsupported.
  Single-batch results must contain exactly one document and cursor id zero.
  Use a read-only monitor account and an indexed filter; a collection/view can
  still cause expensive server work. Arbitrary commands and JSONata remain gaps
  relative to [Kuma's pinned MongoDB engine][kuma-mongo].
- SQL Server reuses `SQLConfig`: private `dsn`, optional `query` (default
  `SELECT 1`), `condition`, `tlsMode`, `tls`. Its intentionally restricted URL
  format is `sqlserver://user:password@host[:port]/database`, default port 1433,
  explicit username and database. Unlike the upstream driver's general URL
  syntax, the database here is a path component. Query parameters, ADO/ODBC
  strings, named instances, SQL Browser, failover/routing, alternate endpoints,
  local files, session initialization, password changes, integrated/federated
  authentication and Always Encrypted key providers are unavailable. A typed
  `msdsn.Config` reaches `NewConnectorConfig` directly; its general DSN parser
  never runs. Only a single direct TCP connection is allowed. The login sends
  read-only application intent; **this does not enforce database permissions**.
  Queries must begin with SELECT and pass the existing single-statement and
  16 KiB query limits. SELECT INTO/functions can have effects: an explicitly
  provisioned account with read-only monitoring permissions is required.
  Success requires at least one row; conditions inspect the first row/column.
  The rest is drained within 100 rows, 64 columns, and 1 MiB decoded scalar
  bytes; extra result sets fail. Native SQL authentication, required verified
  TLS >=1.2 by default, and explicit plaintext mode are supported. The pool is
  per check with one open and zero idle connections. The package does not
  register drivers or mutate driver/TLS/auth registries per monitor; importing
  Microsoft's driver itself performs its documented package-init SQL driver
  registration (as the existing MySQL dependency also does). Grouped conditions,
  wider DSNs and routing remain gaps against [Kuma SQL Server][kuma-sql].
- `NTPConfig`: `address` (explicit UDP host:port), `stratumThreshold` (uint8),
  `offsetThreshold`, `rootDispersionThreshold`, `latencyThreshold` (Go durations).
  Zero selects Kuma's defaults: stratum strictly below 5, absolute offset below
  1 second, root dispersion below 500 milliseconds. A nonzero latency threshold
  additionally requires calculated NTP round-trip delay strictly below it;
  zero disables that optional test. Stratum threshold is 2..16, offset and
  dispersion thresholds are at most 24 hours, latency at most five minutes.
  One 48-byte NTPv4 client request includes its transmit timestamp. Connected
  UDP restricts responses to that endpoint. Replies must be 48..512 bytes and
  four-byte aligned, version 3 or 4, server mode 4, have the matching originate
  timestamp, leap indicator other than 3, and stratum 1..15 (no kiss-of-death).
  Nonzero receive/transmit times must have valid ordering, be within 24 hours
  of the local clock, and imply no more processing time than the elapsed check
  plus 1 ms quantization tolerance. Era decoding handles the 2036 rollover.
  Offset and delay use RFC 5905's four timestamps; `LatencyMs` remains total
  check wall time. Extensions/MAC bytes are bounded but not authenticated or
  interpreted. There is no NTS, clock adjustment, or multi-server consensus.
  Thus this is an availability/quality probe, not authenticated time trust.
  Threshold comparison semantics follow [Kuma NTP][kuma-ntp], with additional
  protocol validation.
- `GRPCConfig`: `address` (one host:port), required `mode` (`health` or `unary`),
  `tlsMode` (verified `tls` default or explicit `plain`), optional `tls`, optional
  private `metadata` (string map, e.g. explicit Authorization token). Metadata
  has at most 16 lowercase ASCII keys, 64 bytes/key, 4096 bytes/value and 8192
  bytes total; values must be printable ASCII. Reserved grpc-/binary/transport
  headers are rejected. No environment proxy, resolver discovery, server
  service config, reflection RPC, default credentials, or automatic reconnect
  dialing. Each check permits one TCP dial and one RPC.
  Health mode has optional `healthService` (256 bytes) and calls
  `grpc.health.v1.Health/Check`, requiring SERVING. Custom method/response options
  are rejected in health mode. This mode reports health semantics explicitly.
  Unary mode requires inline `descriptorSet` (binary FileDescriptorSet;
  base64-encoded by JSON), `method` (`/package.Service/Method`), optional `request`
  (raw protobuf JSON object, default `{}`), optional `keyword`, `invertKeyword`,
  `scalarPath`, `condition`. The descriptor set must include its imports; proto3
  only, no streaming methods, extensions, external import files, source compiler,
  global type resolver or reflection discovery. Unknown JSON request fields
  fail. Keyword matching checks protobuf JSON response serialization and may be
  inverted; a keyword is required to set invertKeyword. A paired scalarPath and
  condition selects exact protobuf field names, traversing singular messages to
  strings, booleans or numeric fields. Lists/maps/bytes/enums/message terminals
  are unsupported for scalar assertions; missing presence-aware fields match
  null, while proto3 implicit-presence fields use their protobuf defaults.
  A configured keyword and condition must both pass. A unary check with neither
  assertion requires a successful actual RPC/decoded response. Descriptor-based
  custom methods are implemented; inline `.proto` source parsing, JSONata,
  streaming and general Any/extension type resolution remain gaps relative to
  [Kuma gRPC keyword][kuma-grpc].

New engine-specific bounds supplement the existing 4 MiB raw wire budget per
connection (including TLS overhead). MongoDB inspects each complete frame before
exposing its length to the driver: at most 1 MiB, OP_REPLY/OP_MSG only, no
compression or exhaust/document sequences, at most 4096 BSON elements and 32
levels. SCRAM payloads are at most 8192 bytes, with iteration counts 4096..100000
checked before PBKDF2. Cryptographic derivation has bounded work but is not
interruptible mid-derivation. MongoDB scalar strings are at most 64 KiB;
filters/DSNs are each 16 KiB. Scalar paths have at most 16 components of 128
bytes. Both wire frame and driver buffers can coexist; this is not a 1 MiB heap
cap. SQL Server's driver caps individual TDS packet buffers but can allocate
from advertised LOB, variant, federated-auth and metadata lengths **before**
post-decode limits or a subsequent socket read. The raw wire budget does not
prevent those allocations. **SQL Server and the existing MySQL engine are not
strict malicious-server memory sandboxes**; further upstream decoder hardening
is a release consideration for untrusted database endpoints. Parser errors are
sanitized; Go cannot recover fatal out-of-memory conditions.

gRPC descriptor sets and JSON requests are at most 64 KiB each; at most 16 files,
512 counted messages/fields/services/methods/oneofs/nested enums, nesting 16;
protobuf/JSON decoding recursion 32. Maximum sent protobuf message is 64 KiB,
received message 128 KiB, response headers 16 KiB. Rendering preflights a 1 MiB
JSON budget (including field names/escaping) and 8192 visited values before
allocating rendered JSON. Library decoding still allocates its message objects
within bounded input; limits are not promises of equal wire and heap sizes.
All parent API/replication limits still apply: base64 descriptors and TLS PEMs
can exceed the current request/page budgets, so parent integration must impose
compatible aggregate limits, preserve secret masking, and include every new
semantic option in lifecycle equality. No new public UI behavior is supplied by
this package.

[kuma-mongo]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/mongodb.js
[kuma-sql]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/mssql.js
[kuma-ntp]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/ntp.js
[kuma-grpc]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/grpc.js

`Condition` is `{ "operator": "ge", "expected": 42 }`. Operators are `eq`, `ne`,
`gt`, `ge`, `lt`, `le`, `contains`, `prefix`, `suffix`. Expected must be a JSON
scalar: null, boolean, number, or string. Ordering requires a number; substring
operators require a string. Numeric SQL text or MQTT payload compares as an
exact rational, including exponent notation; integers beyond float64 precision
stay exact. Numeric input is at most 1024 bytes with an exponent within +/-4096.
Boolean values accept true/false, t/f, and 1/0. Invalid numeric/boolean input
fails a typed comparison, including `ne`; SQL NULL remains distinct from empty
text and matches JSON null. MQTT conditions apply to raw payload text, not a
decoded JSON string, object, or expression. No response value enters Result.

## Specialist engine integration contract

The four specialist additions preserve the ten existing engines. Exact extension:

```go
const (
    SNMP Type = "snmp"
    RADIUS Type = "radius"
    SIP Type = "sip-options"
    RabbitMQ Type = "rabbitmq"
)
// Additional fields on services.Config:
SNMP     *SNMPConfig     // json:"snmp,omitempty"
RADIUS   *RADIUSConfig   // json:"radius,omitempty"
SIP      *SIPConfig      // json:"sip,omitempty"
RabbitMQ *RabbitMQConfig // json:"rabbitmq,omitempty"
```

Exactly one option pointer must match `Type`. All four return only `up` or `down`
with fixed messages and measured `LatencyMs`. The existing package/method
`Validate` and `Check` entrypoints and mandatory positive `Timeout` <=5 minutes
are unchanged. All addresses are explicit host:port, including brackets for IPv6.
No protocol default port, implicit credentials, environment proxy, executable,
shell, file-based credential loading, application payload publishing, or service
discovery is introduced. Only SNMPv3's protocol-required engine discovery runs.
Parent integration must add all four pointers/types to persistence, replication,
DTO validation, semantic/lifecycle equality, and secret masking. Never stringify
these config structs, credentials, response text, PEMs, URLs, or OIDs into public
targets/history/errors. These package checks do not supply UI/runtime integration.

### SNMPConfig (`snmp`)

Required camelCase JSON fields: `address`, `version` (`"1"`, `"2c"`, or `"3"`),
`oid`. Optional `condition` uses the existing scalar `Condition` contract.

- v1/v2c require `community` (1..127 bytes). All v3 fields must be absent. The
  127-byte limit follows the pinned dependency's one-octet community encoder;
  validation rejects longer communities before network I/O.
- v3 requires `username` (1..32 bytes) and explicit `securityLevel`:
  `noAuthNoPriv`, `authNoPriv`, or `authPriv`; `community` must be absent.
  `contextName` is optional, at most 32 bytes. `authNoPriv` and `authPriv` require
  `authProtocol` (`sha` = HMAC-SHA-1-96, or `sha256` = HMAC-SHA-256-192) and private
  `authPassphrase` (8..1024 bytes). `authPriv` additionally requires
  `privacyProtocol:"aes"` (AES-128-CFB) and private `privacyPassphrase`
  (8..1024 bytes). Privacy-only or unused/mismatched security fields are rejected.
  MD5, DES, other SHA variants and AES key sizes are not exposed.

Each check sends one UDP GET for one OID; v3 first sends one unauthenticated
USM engine-discovery GET and requires the matching unknown-engine REPORT. The
final response must be a successful GetResponse for the exact request ID and OID,
version/community, and (v3) message ID, username, engine/context and security
level. HMAC is verified on the pristine response **before** dependency decryption
and decoding. Authenticated responses cannot downgrade to noAuthNoPriv.
Engine boots must match discovery, and engine time must be within 150 seconds
after the discovered time; saturated boots are rejected. No cached engine state,
REPORT recovery/retry, or engine identity pinning. Parent check retries remain
independent. v1/v2c and explicit noAuthNoPriv do not authenticate the agent.

USM password localization is per check, uses fixed 1 MiB work per passphrase,
and checks context cancellation every 4 KiB. The dependency receives localized
keys; its process-wide passphrase cache is unused and no global settings change.
`gosnmp.SnmpDecodePacket` does not verify HMAC itself: our envelope/MAC validation
is mandatory. Decoder panics are contained and converted to fixed failures.
Datagrams are <=16 KiB before decoding. OIDs allow one optional leading dot,
2..128 canonical decimal components, each <=uint32, at most 1024 bytes. A GET
must return exactly one supported non-null scalar: integer/counter/gauge/ticks,
OctetString (<=8192 bytes), OID or IPAddress text (<=1024 bytes). Missing-instance,
missing-object, end-of-MIB, protocol errors and unsupported value types are down.
No walk, SET, traps, symbolic MIB loading, JSON query/expression language, or TCP.

Compared with [pinned Kuma SNMP][kuma-snmp], the scalar condition replaces its
JSON query controls. Kuma's pinned v3 code selects noAuthNoPriv; Canary also has
locally tested SHA/SHA-256 authentication and AES privacy. This is not full
net-snmp option parity.

### RADIUSConfig (`radius`)

Required `address`, `username` (1..253 bytes), private `password` (1..128 bytes),
private shared `secret` (1..1024 bytes); optional `calledStationId` and
`callingStationId` (each <=253 bytes). Optional
`unsafeAllowMissingMessageAuthenticator` is false by default. It only permits a
response without Message-Authenticator; an invalid or duplicate one always fails.

One connected UDP Access-Request carries User-Name, RFC 2865 PAP User-Password,
optional station IDs, a cryptographically random identifier/request authenticator,
and an always-present HMAC-MD5 Message-Authenticator. Replies are <=4096 bytes,
must match the identifier and exact declared length, and pass both the Response
Authenticator and (by default mandatory) Message-Authenticator checks. Attribute
lengths are checked; unknown bounded attributes are ignored. **Only authenticated
Access-Accept is up. Access-Reject is always down. Access-Challenge is down** with
an explicit unsupported-flow message. Reply-Message and all credentials remain
private. No retry, challenge continuation, EAP, CHAP, MS-CHAP, RadSec, RADIUS/1.1,
vendor attribute configuration, or NAS attribute configuration. PAP hiding and
MD5 authenticators are the legacy RADIUS protocol, not TLS or modern encryption.

The fields correspond to Kuma's RADIUS call in [pinned monitor.js][kuma-monitor]
and [util-server.js][kuma-util]. Canary adds explicit response authentication
requirements and a single total context deadline; Kuma's interval-derived timeout
and internal retry do not replace this package's timeout/retry contract.

### SIPConfig (`sip-options`)

Required `address`, explicit `transport` (`udp`, `tcp`, `tls`). Optional
`requestUri` is `sip:[user@]host[:port]` or `sips:[user@]host[:port]` (<=1024 bytes),
without a password, URI parameters, headers or whitespace. Default is `sip:` plus
address, or `sips:` for TLS. `sips:` requires TLS transport. `tls` uses the shared
verified TLSConfig by default, supports additive CA/serverName/client PEMs, and is
rejected with UDP/TCP. Explicit `ignoreVerification` remains an unsafe TLS opt-in.
Optional `expectedStatus` contains <=16 distinct final codes in 200..699; empty
means 200..299. A deliberately configured error status is an assertion about that
response, not successful SIP authentication.

The engine sends a real, fresh OPTIONS non-INVITE transaction with unique Via
branch, Call-ID, From tag and CSeq, Max-Forwards 0 and no body. It validates
SIP/2.0 status/framing, top Via transport/sent-by/branch, Call-ID, CSeq number and
OPTIONS method, From URI/tag, To URI and final-response To tag. Provisional 1xx
responses do not succeed. Compact Via/From/To/Call-ID/Content-Length names work.
UDP retransmits the identical transaction starting at 500 ms, doubling to 4 s;
after a provisional response it uses 4 s. At most 80 sends and 16 responses fit
within the total deadline. A malformed/unrelated datagram fails the check. TCP
and TLS handle fragmented and coalesced responses without retransmission.
Headers are <=16 KiB, <=64 fields, each line <=4096 bytes; body <=8192 bytes and
UDP datagram <=24 KiB. Content-Length is mandatory for streams and checked exactly
for UDP when present. Header folding and duplicate headers except Via are rejected.

No Digest/Basic SIP credentials, registration, calls, ACK/dialog setup, SRV/NAPTR,
proxy routing, arbitrary headers, or SDP assertions. 401/407 fail with default
expected statuses. Unlike [pinned Kuma SIP][kuma-sip], no `sipsak` executable or
container-specific exclusion is required; Kuma's fixed helper timeout is replaced
by the normal bounded package deadline. Digest authentication is not claimed.

### RabbitMQConfig (`rabbitmq`)

Required explicit `mode` is `amqp` or `management`, plus `username` (1..255 UTF-8
bytes) and private `password` (<=4096 UTF-8 bytes); NUL is rejected in both.
Shared `tlsMode` defaults to verified `tls`; explicit `plain` is required for
plaintext. Optional `tls` supports additive CA, serverName and client PEMs and
is rejected in plain mode. The two modes are never substituted for one another.

- `amqp`: required `address`; optional `virtualHost` (default `/`, <=255 UTF-8
  bytes, no NUL). `managementUrls` must be absent. Sends the actual AMQP 0-9-1
  protocol header, validates connection.start, authenticates with SASL PLAIN,
  negotiates connection.tune, opens the requested vhost and requires open-ok,
  then sends connection.close and requires close-ok. A TCP accept, HTTP response,
  protocol greeting alone or rejected credentials/vhost cannot succeed. Supports
  PLAIN and en_US only, including over verified TLS/mTLS. No SASL challenge or
  EXTERNAL mechanism, AMQP 1.0, channels, queues, bindings, publishing or consuming.
  The connection exists only for this bounded handshake; heartbeat frames are
  accepted (at most 16 before each expected method), with no persistent heartbeat
  worker. This does **not** assert that cluster resource alarms are clear.
  Server field-table entries are framed/validated and discarded within depth 8
  and 256 elements. Each complete frame is <=64 KiB including its eight-byte
  overhead; server frame limits below 4096 fail and larger limits negotiate down.
- `management`: required `managementUrls` contains 1..8 HTTP(S) base URLs, with
  optional canonical path prefix, no embedded credentials/query/fragment/escaped
  path. `address` and `virtualHost` must be absent. HTTPS is mandatory unless
  `tlsMode:"plain"` explicitly selects HTTP for every URL. A GET to each base's
  `api/health/checks/alarms/` uses Basic authentication. Up requires HTTP 200 with
  a decoded `status:"ok"` response; 503 alarm, 401/403, malformed/oversized body,
  redirect, TLS failure or exhausted nodes fail. At most one GET per configured
  node, in order, stops at the first successful node. All nodes share one total
  deadline; a stalled early node can exhaust it. No redirects, environment proxy,
  default credentials, compression, HTTP writes or application messages. Response
  headers <=16 KiB and body <=64 KiB. A colon in the Basic-auth username is invalid.

[Pinned Kuma RabbitMQ][kuma-rabbit] checks multiple management URLs and cluster
alarms. `management` implements that purpose with stricter JSON validation and
the shared timeout. `amqp` is an additional connection/vhost check, not a parity
claim for management alarm health. Neither is a queue/message-delivery assertion.

### Specialist verification and dependency pin

`snmp_test.go`, `radius_test.go`, `sip_test.go`, and `rabbitmq_test.go` use real
loopback protocol sockets/HTTP endpoints. They cover supported success paths,
authentication rejection, explicit RADIUS Reject/Challenge, TLS trust/name/mTLS,
transaction correlation, malformed/truncated/oversized responses, timeouts,
cancellation, and fixed-message secret redaction. SNMP localization additionally
matches RFC 3414 A.3.2's independent SHA vector. `specialist_parser_test.go`
provides a bounded-parser fuzz target. These fixtures do not claim production
interoperability with every server/version or test unsupported features.

Specialist validation completed 2026-10-22 with the existing Go 1.27.3 SDK:

- Full fourteen-engine fixture regression: `CGO_ENABLED=0 go test
  ./apps/node-go/internal/services -count=1 -cover -timeout=90s` passed in
  12.373 seconds, 85.2% package statement coverage.
- Native Windows `go test -race ./apps/node-go/internal/services -count=1
  -timeout=120s` passed in 12.032 seconds with the already installed portable
  w64devkit GCC. No compiler/system installation or setup changes were made.
- `CGO_ENABLED=0 go vet ./apps/node-go/internal/services` and `go mod verify`
  passed. `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build
  ./apps/node-go/internal/services` passed. This is package compilation, not a
  fresh Alpine runtime execution of these new engines.
- Five-second `FuzzSpecialistBoundedParsers` smoke run passed, 29,190 executions
  with two workers. It is bounded fuzz evidence, not an exhaustive parser audit.

Only new runtime dependency: pure-Go `github.com/gosnmp/gosnmp v1.46.0`, verified
against its [official release][gosnmp-release] and Go module proxy on 2026-10-22.
The current x/crypto v0.58.0 pin is retained. SIP, RADIUS and RabbitMQ's limited
handshake/management implementation use the Go standard library. No CGO or Alpine
runtime/build-stage changes are needed.

Primary protocol references:
[gosnmp API/source](https://github.com/gosnmp/gosnmp/tree/v1.46.0),
[SNMP USM RFC 3414](https://www.rfc-editor.org/rfc/rfc3414.html),
[SHA-2 USM RFC 7860](https://www.rfc-editor.org/rfc/rfc7860.html),
[AES privacy RFC 3826](https://www.rfc-editor.org/rfc/rfc3826.html),
[RADIUS RFC 2865](https://www.rfc-editor.org/rfc/rfc2865.html),
[Message-Authenticator RFC 3579](https://www.rfc-editor.org/rfc/rfc3579.html),
[SIP RFC 3261](https://www.rfc-editor.org/rfc/rfc3261.html),
[RabbitMQ AMQP specification](https://github.com/rabbitmq/amqp-0.9.1-spec),
[RabbitMQ's Go frame reader](https://github.com/rabbitmq/amqp091-go/blob/main/read.go),
[RabbitMQ HTTP API](https://www.rabbitmq.com/docs/http-api-reference).

[kuma-snmp]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/snmp.js
[kuma-monitor]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/model/monitor.js
[kuma-util]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/util-server.js
[kuma-sip]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/sip-options.js
[kuma-rabbit]: https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/rabbitmq.js
[gosnmp-release]: https://github.com/gosnmp/gosnmp/releases/tag/v1.46.0

## Bounds and evidence

The total timeout is mandatory and at most five minutes. Earlier caller deadlines
and cancellation close raw connections, including blocked TLS/SSH handshakes.
Socket reads have a 4 MiB aggregate wire budget per connection, including TLS/SSH
overhead. Docker decoded JSON is at most 1 MiB, HTTP response headers 64 KiB;
Redis replies use a 4096-byte line buffer. SQL DSNs and queries are each at most
16 KiB; one result set is at most 100 rows, 64 columns, and 1 MiB of scalar value
bytes. PostgreSQL also caps every protocol message body at 1 MiB. MySQL's pinned
driver can allocate its packet/result metadata buffers before the post-decode
row/column/value limits; these limits are not a strict driver heap cap. A
truncated MySQL ERR packet was observed to panic in the upstream decoder; the
engine contains parser panics at its driver boundary and returns a fixed down
message. Arbitrarily hostile MySQL server allocation behavior needs further
driver hardening; do not describe this as a complete malicious-server sandbox.

MQTT bounds each packet at 64 KiB, aggregate message bytes at 1 MiB, and the
subscription phase at 128 packets. SFTP uses the library's bounded SFTP/SSH packet
decoders plus the connection wire budget; it only negotiates and optionally stats
one path. PEMs/known_hosts are at most 256 KiB each; passwords at most 4096 bytes;
scalar expected input at most 4096 bytes. Reaching limits fails the check.

Pinned direct dependencies: `github.com/jackc/pgx/v5 v5.11.0`,
`github.com/go-sql-driver/mysql v1.10.1`, `github.com/pkg/sftp v1.13.11`, and the
existing `golang.org/x/crypto v0.58.0`. New direct pins are
`go.mongodb.org/mongo-driver/v2 v2.9.2`,
`github.com/microsoft/go-mssqldb v1.11.2`, `google.golang.org/grpc v1.84.0`, and
`google.golang.org/protobuf v1.36.12`. Driver module metadata requires Go 1.25
(pgx/SFTP) or 1.24 (MySQL), compatible with the repository's Go 1.27.3. MQTT,
Redis, and Docker use the standard library and their primary protocol specs.
All dependencies are pinned in the root go.mod/go.sum; no CGO dependency is added.

Earlier verification completed on 2026-10-22 with Go 1.27.3 / CGO_ENABLED=0: package
fixture suite passed (85.8% statement coverage across the first ten engines; previous
six-engine baseline was 88.4%). All `./apps/node-go/...` tests passed, node-wide
go vet passed, root modules verified, and Linux amd64 package-test and full
canary binary compilation passed. Linux binaries/tests and containers were not
executed. Final tidy preserves existing dependency versions and classifies the
parent's `golang.org/x/sys v0.49.0` as direct.

Run from the repository root using the parent's portable SDK:

```powershell
$env:CGO_ENABLED='0'
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/services -count=1 -cover -timeout 30s
.canary/tools/go/bin/go.exe vet ./apps/node-go/internal/services
.canary/tools/go/bin/go.exe mod verify
```

The deterministic suite uses loopback HTTP/RESP/MQTT/PostgreSQL/MySQL fixtures,
generated TLS authorities, and the libraries' actual SSH server and in-memory
SFTP request server. SQL fixtures exchange real startup/authentication/query/rows
through unmodified drivers: PostgreSQL cleartext-password authentication inside
plain/verified TLS; MySQL native-password challenge/response inside plain/verified
TLS. Both databases test refusal, query errors, malformed replies, no rows,
assertion mismatch, NULL, row/value bounds, query deadlines, and TLS trust failure.
Other fixtures cover strict Docker route/method/status/state and body bounds,
Redis exact AUTH/SELECT/PING and malformed replies, MQTT auth/subscription
refusals, retained/early messages, QoS 1 ACK, equality/keyword/numeric matches,
malformed/oversized packets and no-message deadline, SFTP password/private keys,
known_hosts/fingerprint mismatch, missing path, rejected subsystem and malformed
version response and excessive key KDF cost. All six engines test connection refusal and mandatory deadlines;
all non-HTTP engines also test caller cancellation during blocked handshakes.
Docker timeout cancels the HTTP request. No external server, daemon, credential,
user database, .env, or default executable is used.

The new fixtures exchange actual MongoDB OP_QUERY/OP_REPLY and OP_MSG commands,
including independently calculated SCRAM-SHA-256 proof/server signatures, verified
TLS, ping, flat find filters, exact large int64/decimal values, null and typed
scalars. They test authentication refusal/excessive KDF cost, query errors,
oversized/compressed/deep BSON replies, scalar/document limits, TLS trust failure,
deadlines, cancellation and suppression of ambient logging file options. SQL
Server fixtures exchange PRELOGIN, TDS-wrapped TLS negotiation, LOGIN7 SQL
credentials and read-only intent, SQLBatch, metadata/rows/DONE/errors through
the actual driver. They exercise plain/verified TLS, refusal, query errors,
NULL, scalar mismatch, no rows, row/column/value limits, multiple result sets,
malformed tokens, deadlines, cancellation and untrusted TLS. They do not attempt
the upstream huge-LOB allocation cases in-process.

NTP fixtures use actual loopback UDP packets and test origin/mode/version/leap/
stratum/time ordering/time range, zero timestamps, size, strict quality thresholds,
timeouts/cancellation and the 2036 era rollover. gRPC fixtures use the actual Go
HTTP/2 server with a synthetic descriptor-defined `fixture.Monitor/Probe` and
the standard health service. They assert JSON request serialization, explicit
Authorization metadata, rejection of inherited caller credentials, verified TLS,
health SERVING/NOT_SERVING/unknown service, numeric/keyword/inverted/nested/map
responses, server errors, message/rendering/recursion bounds and cancellation.
These are independent local protocol fixtures, not tests against production
MongoDB/SQL Server/NTP/gRPC deployments or external service impersonation.

These are protocol fixture results, not real PostgreSQL/MySQL/MariaDB deployment
certification. PostgreSQL SCRAM/MD5, MySQL caching_sha2_password/auth plugins,
real server/version combinations, and actual Docker daemon operation are not
verified here. The Unix-socket HTTP fixture also passes on this Windows host's
AF_UNIX support; it skips with a reported reason if AF_UNIX is unavailable.
Linux compilation does not prove Linux execution or Alpine container behavior.

## Remaining catalogue work

This package supplies sixteen actual service protocol engines, with the option
limitations above and Kafka/Oracle additions below. It does not implement
system services, PM2, browser, Globalping, Tailscale ping, GameDig, or Steam.
HTTP/keyword/JSON query, TCP/TLS, ICMP, DNS, WebSocket, SMTP, group, push, and manual
remain owned by other packages/the parent. MQTT JSONata/WebSocket options,
database grouped conditions and wider DSN options remain parity gaps.

The full 34-type destination in docs/parity-audit.md is unchanged. Parent runtime,
API/CLI/UI DTOs, persistence/replication, causal config comparison, retries,
history, and HA tests still must integrate these options and pending results;
package-level protocol success alone does not pass those release gates.

Primary references consulted:
[Docker Engine API](https://docs.docker.com/reference/api/engine/version/v1.51/),
[Redis PING](https://redis.io/docs/latest/commands/ping/),
[MQTT 3.1.1](https://docs.oasis-open.org/mqtt/mqtt/v3.1.1/os/mqtt-v3.1.1-os.html),
[pgconn](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.11.0/pgconn),
[Go MySQL driver](https://pkg.go.dev/github.com/go-sql-driver/mysql@v1.10.1),
[SFTP](https://pkg.go.dev/github.com/pkg/sftp),
[SSH](https://pkg.go.dev/golang.org/x/crypto/ssh).

New primary references: [MongoDB direct connections](https://www.mongodb.com/docs/drivers/go/current/connect/connection-targets/),
[MongoDB find](https://www.mongodb.com/docs/manual/reference/command/find/),
[MongoDB Go driver v2.9.2](https://pkg.go.dev/go.mongodb.org/mongo-driver/v2@v2.9.2),
[Microsoft Go SQL Server driver](https://github.com/microsoft/go-mssqldb/tree/v1.11.2),
[Microsoft TDS specification](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/),
[NTP RFC 5905](https://www.rfc-editor.org/rfc/rfc5905.html),
[gRPC health protocol](https://grpc.io/docs/guides/health-checking/),
[gRPC Go v1.84.0](https://pkg.go.dev/google.golang.org/grpc@v1.84.0),
[protobuf descriptors](https://pkg.go.dev/google.golang.org/protobuf/reflect/protodesc).

## Kafka and Oracle contracts and evidence (2026-10-22)

`Config.Kafka *KafkaConfig` uses `Kafka` (`kafka-producer`), and
`Config.Oracle *SQLConfig` uses `Oracle` (`oracledb`). Existing types, mandatory
timeout, exactly-one-options validation, private-safe Result and Docker pending
semantics remain unchanged. Parent-owned DTOs, validation, persistence, lifecycle
comparison, UI, HA and privacy integration still need these additions; package
dispatch alone does not change the parent's 21-type catalogue.

```go
type KafkaConfig struct {
    Brokers []string `json:"brokers"`
    Mode string `json:"mode"` // required: metadata or produce
    Topic string `json:"topic,omitempty"`
    Message *string `json:"message,omitempty"` // nil absent; &"" sends empty value
    AllowAutoTopicCreation bool `json:"allowAutoTopicCreation,omitempty"`
    TLSMode string `json:"tlsMode,omitempty"` // verified tls default; explicit plain
    TLS *TLSConfig `json:"tls,omitempty"`
    SASL *KafkaSASLConfig `json:"sasl,omitempty"`
}
type KafkaSASLConfig struct {
    Mechanism string `json:"mechanism"` // plain, scram-sha-256, scram-sha-512
    Username string `json:"username"`
    Password string `json:"password"`
}
// Oracle reuses SQLConfig: dsn, query, condition, tlsMode, tls.
```

Treat the entire Kafka configuration, including brokers/topic/message, and
Oracle DSN/query/condition/TLS as private. Lifecycle comparison must include
every field, including nil versus empty message, password changes and PEMs.
Validation and Result messages never interpolate credentials, queries, results,
addresses, or server error text. No driver object is retained in Config.

### Kafka behavior and pinned Kuma comparison

Metadata mode authenticates, requests **zero topics**, validates a nonempty broker
response and explicitly reports that no message was produced. It rejects all
produce options. It is a Canary availability mode, not Kuma producer parity.
Produce mode explicitly validates cluster and topic metadata, selects the
lowest numbered returned partition, and sends one uncompressed record through
franz-go's direct broker request API. It requires a successful matching Produce
acknowledgement with `acks=-1` (all in-sync replicas) and a nonnegative offset.
Metadata/auth success never substitutes for this acknowledgement. Each check
closes its fresh client. There is no consumer, transaction or application resend.

Configured brokers are 1..16 unique host:port endpoints. Advertised brokers must
be in that allowlist; include advertised leader addresses, not just a bootstrap
alias. Each address permits at most four TCP connections per check for separate
metadata/producer exchanges. Topic names follow Kafka ASCII rules, at most 249
bytes; messages are at most 64 KiB, including an explicit empty message. Broker
frames/requests are at most 1 MiB; decoded broker count is at most 16. Socket reads
also have the existing 4 MiB raw budget. Request and record retries are zero;
idempotent writes are disabled to avoid PID/recovery requests. Driver timer
minimums (100 ms request/overhead, 1 second delivery) cannot extend the captured
outer check/socket deadline. TLS and earlier caller cancellation apply to every
dial and blocked exchange.

Every consumed metadata response is checked, including the later topic response:
duplicate broker IDs, more than 16 brokers, unconfigured endpoints, mismatched
topics and invalid partition/leader/replica/ISR data fail. The high-level
producer's internal metadata refresh path is not used. Independent local
fixtures reproduce rejection of malformed later responses before any Produce.

SASL nil means none. PLAIN and SCRAM-256/512 use explicit credentials, never an
ambient provider chain. Verified TLS >=1.2, additive CA and paired mTLS PEMs use
the existing TLSConfig. Explicit plaintext can expose SASL credentials. SCRAM
challenges are at most 8192 bytes, with 4096..100000 iterations checked before
the upstream KDF. That bounded derivation itself is not interruptible.

Comparison is to Kuma 2.5.6, commit
`7ca4a3debd76443d8452da95d4040200a1479af4`, pinned by the parity audit:

| Kuma option/behavior | Canary implementation or exact gap |
| --- | --- |
| Broker list | Bounded explicit allowlist; expansion to unconfigured advertised brokers fails. |
| Topic and message | Real one-record acknowledged produce in required produce mode. |
| SSL checkbox, default false | Verified TLS defaults on; plaintext is explicit; custom CA/mTLS are additional typed options. |
| Allow auto topic creation, default false | Forwarded to producer topic metadata requests. |
| SASL None/plain/SCRAM-256/SCRAM-512 | Implemented with the typed mechanism tokens above. |
| SASL AWS identity/access-key/secret/session-token | Unsupported; aws and unknown mechanisms fail validation. |
| Zero request/producer retries | Zero; total configured deadline replaces interval-derived timing. |
| Metadata-only mode | Canary addition; no producer-health claim. |

Producing changes the target: each assigned node/check can append a message.
Parent retries or ambiguous acknowledgements can cause duplicates. This is not
an exactly-once delivery guarantee.

### Oracle behavior and pinned Kuma comparison

Private DSN format is `oracle://user:password@host[:port]/service`, port 1521 by
default. Explicit user, nonempty password and simple service name are required.
All user query options, fragments, JDBC/keyword/TNS descriptors, file wallets,
token files, external/OS/Kerberos authentication, SYS/SYSDBA, proxy/failover and
multi-host options are rejected. Only fixed engine options reach the driver.
One configured TCP endpoint and one dial are allowed; redirects/reconnects cannot
open a second connection or expand the endpoint. No per-check global driver,
configuration, or TLS registry mutation occurs. go-ora itself registers its SQL
driver at import. Its parser reads process identity/hostname for client metadata;
these do not select monitor credentials.

TLS >=1.2 verifies hostname and trust by default; custom CA and paired mTLS PEMs
are tested through real TNS-over-TLS exchanges. go-ora v3.0.1 overwrites
tls.Config.ServerName with the DSN hostname. A differing typed override is
rejected: use the certificate hostname in the DSN. `ignoreVerification:true`
is rejected, never silently ignored. Explicit plain mode is supported. Native
Oracle network encryption, wallet trust, certificate-DN matching and external
certificate-only login are not exposed.

Default query is `SELECT 1 FROM DUAL`. Explicit queries must start with SELECT,
fit 16 KiB, and pass the existing single-statement lexical restriction. This is
not SQL authorization: functions can have effects. The driver sends standard
ALTER SESSION language/territory/timezone login attributes; use a read-only
monitoring account. One synchronous query uses the actual go-ora connection and
statement API. Socket guards own cancellation and clamp driver deadline resets,
avoiding upstream context watchers racing over connection state. Raw sockets
close before connection cleanup; there is no pool/reuse or SQL reconnect retry.

At least one row is required. Existing typed Condition inspects first row/column;
null, text/bytes, int64, finite float64 and booleans are accepted. Other scalar
representations fail. Remaining rows are drained within 100 rows, 64 columns and
1 MiB decoded scalar bytes; extra result sets fail. Number conversion happens
inside the driver before Condition: this does not add arbitrary-precision Oracle
decoding. Fixtures prove NUMBER 1, text and NULL, not every precision/type.

| Kuma option/behavior | Canary implementation or exact gap |
| --- | --- |
| Separate connection string/user/password | Restricted private URL; broader TNS aliases/descriptors and external auth remain gaps. |
| Empty/blank query defaults to SELECT 1 FROM DUAL | Empty defaults identically; explicit whitespace-only query fails validation. |
| General execute, including writes and zero-row success | SELECT monitoring queries only; zero rows fail. |
| Grouped conditions against String(result) | Existing typed scalar Condition; grouped expressions/string coercion remain gaps. |
| Conditions require exactly one row and column | Existing first-row/column contract plus bounded drain; intentionally different cardinality. |
| Row count/result/raw driver errors in messages | Fixed sanitized messages; no private values in Result. |

**Oracle checks run in a disposable resource-limited worker.**
The 4 MiB raw connection budget and post-decode result limits are not upstream
heap sandboxes. go-ora allocates from TNS lengths, TTC metadata counts, LOB/value
sizes and other advertised lengths before these checks; hostile peers can cause
excessive allocation or fatal OOM. Modern password derivation can use expensive,
non-interruptible upstream iteration settings (up to 100000000), outlasting an
I/O deadline. Its decoder can print numeric query IDs to stdout/stderr: monitor
errors never include private strings, but strict upstream log silence is not
established. Synchronous monitoring parser panics are contained; upstream startup
queries can use their own goroutines. `services.Check` therefore invokes the
[worker boundary](../probeworker/README.md) automatically, with no in-process
fallback. The worker has a 128 MiB OS allocation/commit limit, a soft Go heap
target, a fixed executable and bounded private input/output. Deadline/cancellation
terminates and reaps the process, including non-interruptible driver work.
Incidental driver output is discarded rather than becoming node diagnostics.
The original driver limits still apply inside that process; this does not
certify every malicious response or native deployment. Windows process startup
has an observed wall-clock overrun under load, and actual Alpine execution of
the current boundary remains pending. The unchanged authentication cancellation
regression passed in 6.3 ms; full services and worker fixtures pass.

### Dependency and local fixture evidence

Official current module metadata was checked on 2026-10-22. Pins are franz-go
v1.22.2 (2026-10-13), separately versioned pkg/kmsg v1.15.0, and go-ora/v3 v3.0.1
(2026-07-24). franz-go requires compress v1.20.3 and lz4/v4 v4.1.33. The former
upgrades the existing shared compression dependency, covered by original-engine
regressions. Both drivers are pure Go; no OCI libraries or CGO are required.

Kafka loopback fixtures decode actual headers/API versions, SASL handshakes and
challenge/proof messages, metadata, auto-creation flags, produce batches, CRC32C
and exact message bytes before selective acknowledgement. SCRAM server proof is
computed with a separate implementation. Cases include bad auth, excessive KDF
cost, refused/absent acks, malformed/oversized frames, broker limits, TLS trust,
mTLS, deadlines and cancellation. This does not certify real multi-broker or AWS
deployments.

The independent Oracle fixture exchanges real TNS CONNECT/ACCEPT, TTC protocol
and type negotiation, independently computes the 11g AES password exchange and
checks the client's encrypted password before accepting authentication. It
returns session properties/version, checks SELECT bytes, and sends real TTC
columns/rows/NUMBER/NULL/errors through go-ora. Cases include bad auth, query
failure, malformed messages, empty rows, row/column/value limits, plain/TLS/mTLS,
untrusted TLS, timeout and cancellation. Its minimal TTC3/11g negotiation does
not certify modern Oracle 19c/21c/23ai deployments, 12c password verifiers,
wallets, RAC/redirects, or modern TTC capabilities.

No external service calls, host Docker/WSL, VM boot, installation, machine trust,
user DB, default executable, or live notification changes were used. Linux CGO=0
compilation is build evidence, not Alpine/Oracle runtime certification.

Final live-package validation, using portable Go 1.27.3 at
`.canary/tools/go/bin/go.exe`, all passed on 2026-10-22:

```text
CGO_ENABLED=0 go test -count=1 -timeout=4m ./apps/node-go/internal/services
  PASS (6.972s): complete original fourteen-engine and new-engine regressions
go test -race -count=1 -timeout=2m -run 'Kafka|Oracle' ./apps/node-go/internal/services
  PASS (3.115s): native Windows/amd64, CGO_ENABLED=1, portable GCC
CGO_ENABLED=0 go vet ./apps/node-go/internal/services
  PASS
go mod verify
  PASS: all modules verified
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c \
  -o .canary/staging/services-kafka-oracle-linux-amd64.test ./apps/node-go/internal/services
  PASS
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -o .canary/staging/node-go-kafka-oracle-linux-amd64 ./apps/node-go/cmd/canary
  PASS
```

Race setup used the existing `scripts/windows-race-validation.py` `environment()`
logic unchanged, including verified portable GCC and short owned test temporary
paths. It did not run that script's snapshot/proof writer or change the script.
The final live sources matched the staged sources that had also passed focused
race, full services regressions, vet and Linux compilation. These are package
fixtures and build checks; parent process/API/HA/privacy release proofs remain
parent-owned. Root go.mod/go.sum ownership is released after this implementation;
no commit was made.

Primary sources:
[Kuma Kafka helper](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/util-server.js#L189-L289),
[Kuma Oracle engine](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/oracledb.js),
[Kuma editor](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/src/pages/EditMonitor.vue),
[franz-go v1.22.2](https://pkg.go.dev/github.com/twmb/franz-go@v1.22.2/pkg/kgo),
[franz-go SCRAM](https://github.com/twmb/franz-go/blob/v1.22.2/pkg/sasl/scram/scram.go),
[go-ora v3.0.1](https://pkg.go.dev/github.com/sijms/go-ora/v3@v3.0.1),
[go-ora session](https://github.com/sijms/go-ora/blob/v3.0.1/network/session.go),
[go-ora auth](https://github.com/sijms/go-ora/blob/v3.0.1/auth_object.go),
[Kafka protocol](https://kafka.apache.org/protocol.html).
