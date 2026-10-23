import type { ReactNode } from 'react';
import type { JSONQueryOptions, Monitor, MonitorType, ServiceMonitorOptions } from '@uptime-canary/shared';

/**
 * Declarative forms for the sixteen service protocols. Each field names a path inside its protocol's option
 * object. Edits start from a copy of the stored object, so keys this dashboard doesn't model survive, and a
 * stored secret stays exactly as it was unless the operator changes or removes it.
 */

export type ProtocolKey = keyof ServiceMonitorOptions;
export const SERVICE_PROTOCOL: Partial<Record<MonitorType, ProtocolKey>> = {
  docker: 'docker', redis: 'redis', postgres: 'postgres', mysql: 'mysql', mqtt: 'mqtt', sftp: 'sftp', mongodb: 'mongodb',
  sqlserver: 'sqlserver', ntp: 'ntp', grpc: 'grpc', snmp: 'snmp', radius: 'radius', 'sip-options': 'sip', rabbitmq: 'rabbitmq',
  'kafka-producer': 'kafka', oracledb: 'oracle',
};

export interface PairRow { id: number; name: string; value: string }
export type FieldValue = string | boolean | PairRow[];
export type Values = Record<string, FieldValue>;

export type FieldKind = 'text' | 'secret' | 'int' | 'select' | 'bool' | 'lines' | 'intlist' | 'pem' | 'json' | 'pairs' | 'path' | 'textarea' | 'base64' | 'condition' | 'jsonata';
export type FieldSection = 'main' | 'auth' | 'query' | 'tls' | 'advanced';

export interface ServiceField {
  key: string;
  label: string;
  kind: FieldKind;
  section?: FieldSection;
  required?: boolean | ((v: Values) => boolean);
  hint?: string;
  placeholder?: string;
  options?: { value: string; label: string }[];
  /** Hidden fields are removed from the saved object: the node rejects options that don't apply. */
  when?: (v: Values) => boolean;
  /** Credentials and keys: masked in previews and facts. */
  private?: boolean;
  /** Certificates or keys for `pem` fields. */
  pem?: 'certificate' | 'key';
  danger?: boolean;
  min?: number;
  max?: number;
  /** Default for a new monitor's select. */
  initial?: string;
  full?: boolean;
  /** Integer fields measured in milliseconds show a unit. */
  unit?: string;
  /**
   * An empty value is a real value the node accepts (Kafka's zero-length record, RabbitMQ's empty password): while
   * the field applies, its key is sent as "" rather than removed, so it neither fails as missing nor changes meaning.
   */
  keepEmpty?: boolean;
  /** For `jsonata` fields: what `$` is for this check. */
  input?: 'http' | 'mqtt' | 'mongodb' | 'snmp';
}

export const SECTION_TITLE: Record<FieldSection, string> = {
  main: '', auth: 'Credentials', query: 'Query and assertion', tls: 'TLS and certificates', advanced: 'Advanced',
};

const str = (v: Values, key: string): string => typeof v[key] === 'string' ? v[key] as string : '';
const isOn = (v: Values, key: string): boolean => v[key] === true;
const notPlain = (v: Values): boolean => str(v, 'tlsMode') !== 'plain';

const TLS_MODE: ServiceField = {
  key: 'tlsMode', label: 'Transport security', kind: 'select', section: 'main',
  options: [{ value: '', label: 'Verified TLS (default)' }, { value: 'plain', label: 'Plain, unencrypted' }],
  hint: 'TLS 1.2+ with certificate and host name verification unless you choose plain.',
};

function tlsFields(when: (v: Values) => boolean, ignore = true): ServiceField[] {
  const fields: ServiceField[] = [
    { key: 'tls.serverName', label: 'Server name (SNI)', kind: 'text', section: 'tls', when, placeholder: 'Defaults to the address host', hint: 'Override when the certificate names a different host.' },
    { key: 'tls.caPem', label: 'Extra trusted CA certificates', kind: 'pem', pem: 'certificate', section: 'tls', when, hint: 'Added to the system roots, for private CAs.' },
    { key: 'tls.clientCertPem', label: 'Client certificate (mutual TLS)', kind: 'pem', pem: 'certificate', section: 'tls', when },
    { key: 'tls.clientKeyPem', label: 'Client private key', kind: 'pem', pem: 'key', private: true, section: 'tls', when, hint: 'Paired with the client certificate. Stored on nodes, hidden from viewers.' },
  ];
  if (ignore) fields.push({ key: 'tls.ignoreVerification', label: 'Skip certificate verification', kind: 'bool', danger: true, section: 'tls', when, hint: 'Doesn’t check the chain or host name, so an impostor could pass. Prefer adding the CA above.' });
  return fields;
}

const CONDITION_HINT = 'Optional. Compare a single value from the response; without it, a successful exchange is enough.';

function sqlFields(type: 'postgres' | 'mysql' | 'sqlserver' | 'oracle'): ServiceField[] {
  const dsn: Record<typeof type, { placeholder: string; hint: string }> = {
    postgres: { placeholder: 'postgres://monitor:password@db.internal:5432/app', hint: 'postgres:// or postgresql:// with an explicit user. No query options: use the TLS fields below.' },
    mysql: { placeholder: 'monitor:password@tcp(db.internal:3306)/app', hint: 'user:password@tcp(host:port)/database. No session parameters.' },
    sqlserver: { placeholder: 'sqlserver://monitor:password@db.internal:1433/app', hint: 'sqlserver://user:password@host[:port]/database. No named instances or query parameters.' },
    oracle: { placeholder: 'oracle://monitor:password@db.internal:1521/ORCLPDB1', hint: 'oracle://user:password@host:port/service. Runs in a separate worker process on each node.' },
  };
  const selectOnly = type === 'sqlserver' || type === 'oracle';
  return [
    { key: 'dsn', label: 'Connection string', kind: 'secret', required: true, private: true, full: true, ...dsn[type] },
    TLS_MODE,
    { key: 'query', label: 'Query', kind: 'textarea', section: 'query', full: true, placeholder: type === 'oracle' ? 'SELECT 1 FROM DUAL' : 'SELECT 1',
      hint: `Empty runs ${type === 'oracle' ? 'SELECT 1 FROM DUAL' : 'SELECT 1'}. One statement starting with ${selectOnly ? 'SELECT' : 'SELECT, WITH, SHOW, VALUES or EXPLAIN'}. Use an account that can only read: this is not a SQL security filter.` },
    { key: 'condition', label: 'First column of the first row', kind: 'condition', section: 'query', hint: CONDITION_HINT },
    ...tlsFields(notPlain, type !== 'oracle'),
  ];
}

export const SERVICE_FIELDS: Record<ProtocolKey, ServiceField[]> = {
  docker: [
    { key: 'endpoint', label: 'Docker endpoint', kind: 'text', required: true, placeholder: 'unix:///var/run/docker.sock', hint: 'An http:// or https:// origin, or unix:///absolute/socket. Socket access is privileged: mount it read-only and restrict the node.' },
    { key: 'container', label: 'Container', kind: 'text', required: true, placeholder: 'web-1', hint: 'Container ID or name, without a leading slash. Starting or restarting reports pending.' },
    { key: 'apiVersion', label: 'API version', kind: 'text', section: 'advanced', placeholder: 'v1.51', hint: 'Optional. Empty uses the daemon’s unversioned API.' },
    ...tlsFields(v => str(v, 'endpoint').trim().toLowerCase().startsWith('https://')),
  ],
  redis: [
    { key: 'address', label: 'Address', kind: 'text', required: true, placeholder: 'cache.internal:6379', hint: 'host:port, or [IPv6]:port.' },
    TLS_MODE,
    { key: 'username', label: 'User name', kind: 'text', section: 'auth', hint: 'Optional ACL user.' },
    { key: 'password', label: 'Password', kind: 'secret', section: 'auth', private: true },
    { key: 'database', label: 'Database', kind: 'int', section: 'advanced', min: 0, max: 1_000_000, placeholder: '0', hint: 'Number selected before PING. Empty: 0.' },
    ...tlsFields(notPlain),
  ],
  postgres: sqlFields('postgres'),
  mysql: sqlFields('mysql'),
  sqlserver: sqlFields('sqlserver'),
  oracle: sqlFields('oracle'),
  mongodb: [
    { key: 'dsn', label: 'Connection string', kind: 'secret', required: true, private: true, full: true, placeholder: 'mongodb://monitor:password@mongo.internal:27017/admin', hint: 'One mongodb:// host. Only authSource and authMechanism query options. No SRV or replica-set discovery.' },
    TLS_MODE,
    { key: 'collection', label: 'Collection', kind: 'text', section: 'query', hint: 'Optional. With a collection, one document must match the filter.' },
    { key: 'filter', label: 'Filter (JSON)', kind: 'json', section: 'query', when: v => Boolean(str(v, 'collection').trim()), placeholder: '{"status": "active"}', hint: 'A flat equality object, up to 16 keys. Values: text, true/false, null or whole numbers.' },
    { key: 'scalarPath', label: 'Field to compare', kind: 'path', section: 'query', when: v => Boolean(str(v, 'collection').trim()), placeholder: 'health.state', hint: 'Field names separated by dots. Needs a condition.' },
    { key: 'condition', label: 'Field condition', kind: 'condition', section: 'query', when: v => Boolean(str(v, 'collection').trim()), hint: CONDITION_HINT },
    { key: 'jsonata', label: 'JSONata condition', kind: 'jsonata', section: 'query', input: 'mongodb', when: v => Boolean(str(v, 'collection').trim()), hint: 'Optional. Must also pass, together with every other condition.' },
    ...tlsFields(notPlain),
  ],
  mqtt: [
    { key: 'address', label: 'Broker address', kind: 'text', required: true, placeholder: 'broker.internal:8883', hint: 'host:port. MQTT 3.1.1 over TCP.' },
    { key: 'topic', label: 'Topic filter', kind: 'text', required: true, placeholder: 'sensors/+/status', hint: 'Subscribes with QoS 0. + and a final # are allowed. Up needs a matching message within the timeout.' },
    TLS_MODE,
    { key: 'username', label: 'User name', kind: 'text', section: 'auth' },
    { key: 'password', label: 'Password', kind: 'secret', section: 'auth', private: true },
    { key: 'condition', label: 'Message payload', kind: 'condition', section: 'query', hint: 'Optional. Without it, any message on the topic counts.' },
    { key: 'jsonata', label: 'JSONata condition', kind: 'jsonata', section: 'query', input: 'mqtt', hint: 'Optional. Must also pass, together with every other condition.' },
    ...tlsFields(notPlain),
  ],
  sftp: [
    { key: 'address', label: 'Address', kind: 'text', required: true, placeholder: 'files.example.com:22' },
    { key: 'username', label: 'User name', kind: 'text', required: true },
    { key: 'password', label: 'Password', kind: 'secret', section: 'auth', private: true, hint: 'Use a password or a private key, not both.' },
    { key: 'privateKeyPem', label: 'Private key', kind: 'pem', pem: 'key', section: 'auth', private: true, hint: 'OpenSSH or PEM private key.' },
    { key: 'privateKeyPassphrase', label: 'Key passphrase', kind: 'secret', section: 'auth', private: true, when: v => Boolean(str(v, 'privateKeyPem').trim()) },
    { key: 'hostKeySha256', label: 'Host key fingerprint', kind: 'text', section: 'auth', placeholder: 'SHA256:…', hint: 'Pin the server: a SHA256 fingerprint, known_hosts lines, or (unsafe) no check. Exactly one.' },
    { key: 'knownHosts', label: 'known_hosts lines', kind: 'textarea', section: 'auth', placeholder: 'files.example.com ssh-ed25519 AAAA…', hint: 'Exact, unhashed host entries for this address.' },
    { key: 'unsafeIgnoreHostKey', label: 'Don’t verify the host key', kind: 'bool', danger: true, section: 'auth', hint: 'Anyone who can intercept the connection could pass the check and learn the credentials.' },
    { key: 'path', label: 'Path that must exist', kind: 'text', section: 'advanced', placeholder: '/upload', hint: 'Optional. Checked with a remote stat.' },
  ],
  ntp: [
    { key: 'address', label: 'Server', kind: 'text', required: true, placeholder: 'time.example.com:123', hint: 'host:port over UDP. Not authenticated time.' },
    { key: 'stratumThreshold', label: 'Stratum must be below', kind: 'int', min: 2, max: 16, placeholder: '5', hint: '2–16. Empty: below 5.' },
    { key: 'offsetThresholdMs', label: 'Offset must be below', kind: 'int', unit: 'ms', min: 0, max: 86_400_000, placeholder: '1000', hint: 'Empty or 0: 1000 ms.' },
    { key: 'rootDispersionThresholdMs', label: 'Root dispersion below', kind: 'int', unit: 'ms', min: 0, max: 86_400_000, placeholder: '500', section: 'advanced', hint: 'Empty or 0: 500 ms.' },
    { key: 'latencyThresholdMs', label: 'Round trip below', kind: 'int', unit: 'ms', min: 0, max: 300_000, placeholder: 'Not checked', section: 'advanced', hint: 'Empty or 0: not checked.' },
  ],
  grpc: [
    { key: 'address', label: 'Address', kind: 'text', required: true, placeholder: 'api.internal:443' },
    { key: 'mode', label: 'Call', kind: 'select', required: true, initial: 'health', options: [{ value: 'health', label: 'Health check (grpc.health.v1)' }, { value: 'unary', label: 'Unary method' }] },
    { key: 'healthService', label: 'Health service name', kind: 'text', when: v => str(v, 'mode') !== 'unary', hint: 'Empty checks the server as a whole. Up requires SERVING.' },
    { key: 'method', label: 'Method', kind: 'text', required: true, when: v => str(v, 'mode') === 'unary', placeholder: '/package.Service/Method' },
    { key: 'descriptorSet', label: 'Descriptor set (base64)', kind: 'base64', required: true, full: true, when: v => str(v, 'mode') === 'unary', hint: 'A binary FileDescriptorSet with its imports (protoc --include_imports --descriptor_set_out). Load the file or paste base64.' },
    { key: 'request', label: 'Request (JSON)', kind: 'json', section: 'query', when: v => str(v, 'mode') === 'unary', placeholder: '{}', hint: 'Protobuf JSON for the request message. Empty sends {}.' },
    { key: 'keyword', label: 'Response contains', kind: 'text', section: 'query', when: v => str(v, 'mode') === 'unary', hint: 'Optional text in the JSON form of the response.' },
    { key: 'invertKeyword', label: 'Fail when the text is found', kind: 'bool', section: 'query', when: v => str(v, 'mode') === 'unary' && Boolean(str(v, 'keyword')) },
    { key: 'scalarPath', label: 'Field to compare', kind: 'path', section: 'query', when: v => str(v, 'mode') === 'unary', placeholder: 'status.code', hint: 'Protobuf field names separated by dots. Needs a condition.' },
    { key: 'condition', label: 'Field condition', kind: 'condition', section: 'query', when: v => str(v, 'mode') === 'unary', hint: CONDITION_HINT },
    { key: 'metadata', label: 'Metadata', kind: 'pairs', section: 'auth', private: true, hint: 'Up to 16 lowercase keys, such as authorization. Values are hidden from viewers.' },
    TLS_MODE,
    ...tlsFields(notPlain),
  ],
  snmp: [
    { key: 'address', label: 'Agent address', kind: 'text', required: true, placeholder: 'switch.internal:161', hint: 'host:port over UDP.' },
    { key: 'version', label: 'Version', kind: 'select', required: true, initial: '2c', options: [{ value: '1', label: 'v1' }, { value: '2c', label: 'v2c' }, { value: '3', label: 'v3 (USM)' }] },
    { key: 'oid', label: 'OID', kind: 'text', required: true, placeholder: '1.3.6.1.2.1.1.3.0', hint: 'Numeric OID of one scalar. No MIB names or walks.' },
    { key: 'community', label: 'Community', kind: 'secret', private: true, section: 'auth', required: true, when: v => str(v, 'version') !== '3', hint: 'v1 and v2c don’t authenticate the agent.' },
    { key: 'username', label: 'User name', kind: 'text', section: 'auth', required: true, when: v => str(v, 'version') === '3' },
    { key: 'securityLevel', label: 'Security level', kind: 'select', section: 'auth', required: true, when: v => str(v, 'version') === '3', initial: 'authPriv',
      options: [{ value: 'authPriv', label: 'Authentication and privacy' }, { value: 'authNoPriv', label: 'Authentication only' }, { value: 'noAuthNoPriv', label: 'None' }] },
    { key: 'authProtocol', label: 'Authentication', kind: 'select', section: 'auth', required: true, initial: 'sha256', when: v => str(v, 'version') === '3' && str(v, 'securityLevel') !== 'noAuthNoPriv',
      options: [{ value: 'sha256', label: 'HMAC-SHA-256' }, { value: 'sha', label: 'HMAC-SHA-1' }] },
    { key: 'authPassphrase', label: 'Authentication passphrase', kind: 'secret', private: true, section: 'auth', required: true, when: v => str(v, 'version') === '3' && str(v, 'securityLevel') !== 'noAuthNoPriv', hint: '8 to 1024 bytes.' },
    { key: 'privacyProtocol', label: 'Privacy', kind: 'select', section: 'auth', required: true, initial: 'aes', when: v => str(v, 'version') === '3' && str(v, 'securityLevel') === 'authPriv', options: [{ value: 'aes', label: 'AES-128' }] },
    { key: 'privacyPassphrase', label: 'Privacy passphrase', kind: 'secret', private: true, section: 'auth', required: true, when: v => str(v, 'version') === '3' && str(v, 'securityLevel') === 'authPriv', hint: '8 to 1024 bytes.' },
    { key: 'contextName', label: 'Context name', kind: 'text', section: 'advanced', when: v => str(v, 'version') === '3' },
    { key: 'condition', label: 'Returned value', kind: 'condition', section: 'query', hint: CONDITION_HINT },
    { key: 'jsonata', label: 'JSONata condition', kind: 'jsonata', section: 'query', input: 'snmp', hint: 'Optional. Must also pass, together with every other condition.' },
  ],
  radius: [
    { key: 'address', label: 'Server address', kind: 'text', required: true, placeholder: 'radius.internal:1812', hint: 'host:port over UDP. Only an authenticated Access-Accept is up.' },
    { key: 'username', label: 'User name', kind: 'text', required: true, section: 'auth' },
    { key: 'password', label: 'Password', kind: 'secret', required: true, private: true, section: 'auth', hint: 'Sent with PAP hiding, the legacy RADIUS scheme. Use a dedicated test account.' },
    { key: 'secret', label: 'Shared secret', kind: 'secret', required: true, private: true, section: 'auth' },
    { key: 'calledStationId', label: 'Called-Station-Id', kind: 'text', section: 'advanced' },
    { key: 'callingStationId', label: 'Calling-Station-Id', kind: 'text', section: 'advanced' },
    { key: 'unsafeAllowMissingMessageAuthenticator', label: 'Accept replies without Message-Authenticator', kind: 'bool', danger: true, section: 'advanced', hint: 'Only for old servers. An invalid authenticator still fails.' },
  ],
  sip: [
    { key: 'address', label: 'Address', kind: 'text', required: true, placeholder: 'pbx.example.com:5060' },
    { key: 'transport', label: 'Transport', kind: 'select', required: true, initial: 'udp', options: [{ value: 'udp', label: 'UDP' }, { value: 'tcp', label: 'TCP' }, { value: 'tls', label: 'TLS' }] },
    { key: 'requestUri', label: 'Request URI', kind: 'text', placeholder: 'sip:pbx.example.com', hint: 'Optional. Empty uses sip: (or sips: with TLS) and the address.' },
    { key: 'expectedStatus', label: 'Accepted final statuses', kind: 'intlist', placeholder: '200', hint: 'Up to 16 codes from 200 to 699. Empty: any 2xx.' },
    ...tlsFields(v => str(v, 'transport') === 'tls'),
  ],
  rabbitmq: [
    { key: 'mode', label: 'Check', kind: 'select', required: true, initial: 'management', options: [{ value: 'management', label: 'Management API alarms' }, { value: 'amqp', label: 'AMQP connection and vhost' }] },
    { key: 'managementUrls', label: 'Management URLs', kind: 'lines', required: true, when: v => str(v, 'mode') !== 'amqp', placeholder: 'https://rabbit-1.internal:15671/', hint: 'One to eight base URLs, one per line, tried in order. Up when the alarms endpoint reports ok.' },
    { key: 'address', label: 'AMQP address', kind: 'text', required: true, when: v => str(v, 'mode') === 'amqp', placeholder: 'rabbit.internal:5671' },
    { key: 'virtualHost', label: 'Virtual host', kind: 'text', when: v => str(v, 'mode') === 'amqp', placeholder: '/' },
    { key: 'username', label: 'User name', kind: 'text', required: true, section: 'auth' },
    { key: 'password', label: 'Password', kind: 'secret', keepEmpty: true, private: true, section: 'auth', hint: 'Can be empty if the broker accepts an empty password.' },
    TLS_MODE,
    ...tlsFields(notPlain),
  ],
  kafka: [
    { key: 'brokers', label: 'Brokers', kind: 'lines', required: true, placeholder: 'kafka-1.internal:9093', hint: 'One to sixteen host:port addresses, one per line.' },
    { key: 'mode', label: 'Check', kind: 'select', required: true, initial: 'metadata', options: [{ value: 'metadata', label: 'Read cluster metadata' }, { value: 'produce', label: 'Produce a message (acknowledged)' }] },
    { key: 'topic', label: 'Topic', kind: 'text', required: true, when: v => str(v, 'mode') === 'produce' },
    { key: 'message', label: 'Message', kind: 'textarea', keepEmpty: true, when: v => str(v, 'mode') === 'produce', hint: 'Every assigned node appends this message on every check. Empty produces a zero-length record.' },
    { key: 'allowAutoTopicCreation', label: 'Allow automatic topic creation', kind: 'bool', when: v => str(v, 'mode') === 'produce' },
    { key: 'sasl.mechanism', label: 'SASL', kind: 'select', section: 'auth', options: [{ value: '', label: 'None' }, { value: 'plain', label: 'PLAIN' }, { value: 'scram-sha-256', label: 'SCRAM-SHA-256' }, { value: 'scram-sha-512', label: 'SCRAM-SHA-512' }] },
    { key: 'sasl.username', label: 'SASL user name', kind: 'text', section: 'auth', required: true, when: v => Boolean(str(v, 'sasl.mechanism')) },
    { key: 'sasl.password', label: 'SASL password', kind: 'secret', private: true, section: 'auth', required: true, when: v => Boolean(str(v, 'sasl.mechanism')) },
    TLS_MODE,
    ...tlsFields(notPlain),
  ],
};

// ---------- object paths

function getPath(object: unknown, path: string): unknown {
  let current: unknown = object;
  for (const part of path.split('.')) {
    if (typeof current !== 'object' || current === null) return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
}
function setPath(object: Record<string, unknown>, path: string, value: unknown): void {
  const parts = path.split('.');
  let current = object;
  for (const part of parts.slice(0, -1)) {
    const next = current[part];
    if (typeof next !== 'object' || next === null || Array.isArray(next)) current[part] = {};
    current = current[part] as Record<string, unknown>;
  }
  current[parts[parts.length - 1]!] = value;
}
function deletePath(object: Record<string, unknown>, path: string): void {
  const parts = path.split('.');
  let current: unknown = object;
  for (const part of parts.slice(0, -1)) {
    if (typeof current !== 'object' || current === null) return;
    current = (current as Record<string, unknown>)[part];
  }
  if (typeof current === 'object' && current !== null) delete (current as Record<string, unknown>)[parts[parts.length - 1]!];
}

let pairId = 0;
export const pairRows = (value: unknown): PairRow[] =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
    ? Object.entries(value as Record<string, unknown>).map(([name, v]) => ({ id: ++pairId, name, value: typeof v === 'string' ? v : JSON.stringify(v) }))
    : [];
export const newPairRow = (): PairRow => ({ id: ++pairId, name: '', value: '' });

// ---------- condition helpers

export type CompareAs = 'text' | 'number' | 'true' | 'false' | 'null';
export const OPERATORS = [
  { value: '', label: 'No condition' },
  { value: 'eq', label: 'equals' }, { value: 'ne', label: 'does not equal' },
  { value: 'gt', label: 'is greater than' }, { value: 'ge', label: 'is at least' }, { value: 'lt', label: 'is less than' }, { value: 'le', label: 'is at most' },
  { value: 'contains', label: 'contains' }, { value: 'prefix', label: 'starts with' }, { value: 'suffix', label: 'ends with' },
];
const NUMERIC = new Set(['gt', 'ge', 'lt', 'le']);
const TEXTUAL = new Set(['contains', 'prefix', 'suffix']);
export const compareKinds = (operator: string): CompareAs[] => NUMERIC.has(operator) ? ['number'] : TEXTUAL.has(operator) ? ['text'] : ['text', 'number', 'true', 'false', 'null'];

/** Draft values for one service type, from the stored options (or defaults for a new monitor). */
export function serviceValues(protocol: ProtocolKey, stored: unknown): Values {
  const values: Values = {};
  for (const field of SERVICE_FIELDS[protocol]) {
    const value = getPath(stored, field.key);
    switch (field.kind) {
      case 'bool': values[field.key] = value === true; break;
      case 'lines': values[field.key] = Array.isArray(value) ? value.join('\n') : ''; break;
      case 'intlist': values[field.key] = Array.isArray(value) ? value.join(', ') : ''; break;
      case 'path': values[field.key] = Array.isArray(value) ? value.join('.') : ''; break;
      case 'json': values[field.key] = value === undefined ? '' : JSON.stringify(value, null, 2); break;
      case 'pairs': values[field.key] = pairRows(value); break;
      case 'int': values[field.key] = typeof value === 'number' ? String(value) : ''; break;
      case 'jsonata': {
        const d = jsonataDraft(value);
        values[`${field.key}.expression`] = d.expression;
        values[`${field.key}.operator`] = d.operator;
        values[`${field.key}.as`] = d.as;
        values[`${field.key}.expected`] = d.expected;
        break;
      }
      case 'condition': {
        const condition = value as { operator?: string; expected?: unknown } | undefined;
        const expected = condition?.expected;
        values[`${field.key}.operator`] = condition?.operator ?? '';
        values[`${field.key}.as`] = expected === null ? 'null' : expected === true ? 'true' : expected === false ? 'false' : typeof expected === 'number' ? 'number' : 'text';
        values[`${field.key}.expected`] = typeof expected === 'string' || typeof expected === 'number' ? String(expected) : '';
        break;
      }
      case 'select': values[field.key] = typeof value === 'string' ? value : stored === undefined ? field.initial ?? '' : ''; break;
      default: values[field.key] = typeof value === 'string' ? value : '';
    }
  }
  return values;
}

export const fieldVisible = (field: ServiceField, values: Values): boolean => !field.when || field.when(values);
const fieldRequired = (field: ServiceField, values: Values): boolean => typeof field.required === 'function' ? field.required(values) : Boolean(field.required);

const PEM_CERT = /-----BEGIN [A-Z0-9 ]*CERTIFICATE-----/u;
const PEM_KEY = /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/u;
const HOST_PORT = /^(\[[0-9a-fA-F:.]+\]|[^:[\]\s/?#@]+):(\d{1,5})$/u;
const bytes = (text: string): number => new TextEncoder().encode(text).length;

/** Builds the protocol object from the draft, starting from the stored object so unmodeled keys survive. */
export function buildService(protocol: ProtocolKey, values: Values, stored: unknown): { value: Record<string, unknown>; errors: Record<string, string> } {
  const value: Record<string, unknown> = typeof stored === 'object' && stored !== null ? structuredClone(stored) as Record<string, unknown> : {};
  const errors: Record<string, string> = {};
  for (const field of SERVICE_FIELDS[protocol]) {
    if (!fieldVisible(field, values)) {
      if (field.kind === 'condition') delete value[field.key]; else deletePath(value, field.key);
      continue;
    }
    const required = fieldRequired(field, values);
    const raw = values[field.key];
    const text = typeof raw === 'string' ? raw : '';
    const missing = (): void => { if (required) errors[field.key] = `Enter ${field.label.toLowerCase()}.`; };
    switch (field.kind) {
      case 'bool':
        if (raw === true) setPath(value, field.key, true); else deletePath(value, field.key);
        break;
      case 'select':
        if (text) setPath(value, field.key, text); else { deletePath(value, field.key); if (required) errors[field.key] = `Choose ${field.label.toLowerCase()}.`; }
        break;
      case 'int': {
        if (!text.trim()) { deletePath(value, field.key); missing(); break; }
        const n = Number(text.trim());
        if (!Number.isInteger(n) || (field.min !== undefined && n < field.min) || (field.max !== undefined && n > field.max)) {
          errors[field.key] = `Use a whole number${field.min !== undefined ? ` from ${field.min.toLocaleString()}` : ''}${field.max !== undefined ? ` to ${field.max.toLocaleString()}` : ''}${field.unit ? ` ${field.unit}` : ''}.`;
        }
        setPath(value, field.key, n);
        break;
      }
      case 'lines': {
        const list = text.split('\n').map(line => line.trim()).filter(Boolean);
        if (list.length) setPath(value, field.key, list); else { deletePath(value, field.key); missing(); }
        break;
      }
      case 'intlist': {
        const parts = text.split(/[\s,]+/u).filter(Boolean);
        const list = parts.map(Number);
        if (parts.some(part => !/^\d+$/u.test(part))) errors[field.key] = 'Use whole numbers separated by commas.';
        if (list.length) setPath(value, field.key, list); else { deletePath(value, field.key); missing(); }
        break;
      }
      case 'path': {
        const list = text.split('.').map(part => part.trim()).filter(Boolean);
        if (list.length) setPath(value, field.key, list); else deletePath(value, field.key);
        break;
      }
      case 'json': {
        if (!text.trim()) { deletePath(value, field.key); missing(); break; }
        try { setPath(value, field.key, JSON.parse(text)); }
        catch { errors[field.key] = 'That isn’t valid JSON. Text needs double quotes.'; }
        break;
      }
      case 'pairs': {
        const rows = Array.isArray(raw) ? raw : [];
        const out: Record<string, string> = {};
        for (const row of rows) {
          const name = row.name.trim();
          if (!name && !row.value) continue;
          if (!name) { errors[field.key] = 'Every value needs a name.'; continue; }
          if (name in out) errors[field.key] = `${name} appears twice.`;
          out[name] = row.value;
        }
        if (Object.keys(out).length) setPath(value, field.key, out); else deletePath(value, field.key);
        break;
      }
      case 'jsonata': {
        const text = (suffix: string): string => typeof values[`${field.key}.${suffix}`] === 'string' ? values[`${field.key}.${suffix}`] as string : '';
        const built = buildJsonata(value[field.key], { expression: text('expression'), operator: (text('operator') || '==') as JsonataOperator, as: (text('as') || 'text') as JsonataAs, expected: text('expected') });
        if (built.error) errors[field.key] = built.error;
        if (built.value) value[field.key] = built.value; else if (!built.error) delete value[field.key];
        break;
      }
      case 'condition': {
        const operator = typeof values[`${field.key}.operator`] === 'string' ? values[`${field.key}.operator`] as string : '';
        if (!operator) { delete value[field.key]; break; }
        const kinds = compareKinds(operator);
        const chosen = values[`${field.key}.as`] as CompareAs;
        const as = kinds.includes(chosen) ? chosen : kinds[0]!;
        const expectedText = typeof values[`${field.key}.expected`] === 'string' ? values[`${field.key}.expected`] as string : '';
        let expected: string | number | boolean | null = expectedText;
        if (as === 'number') {
          const n = Number(expectedText.trim());
          if (!expectedText.trim() || !Number.isFinite(n)) errors[field.key] = 'Enter a number to compare with.';
          else if (Math.abs(n) > Number.MAX_SAFE_INTEGER || (Number.isInteger(n) && !Number.isSafeInteger(n))) errors[field.key] = 'Use a number within ±9,007,199,254,740,991.';
          expected = n;
        } else if (as === 'true') expected = true;
        else if (as === 'false') expected = false;
        else if (as === 'null') expected = null;
        else if (bytes(expectedText) > 4000) errors[field.key] = 'Use at most 4,000 bytes.';
        const previous = value[field.key];
        value[field.key] = { ...(typeof previous === 'object' && previous !== null && !Array.isArray(previous) ? previous : {}), operator, expected };
        break;
      }
      case 'pem': {
        if (!text.trim()) { deletePath(value, field.key); missing(); break; }
        if (field.pem === 'certificate' ? !PEM_CERT.test(text) : !PEM_KEY.test(text) && !/-----BEGIN OPENSSH PRIVATE KEY-----/u.test(text)) {
          errors[field.key] = field.pem === 'certificate' ? 'Paste PEM certificates (-----BEGIN CERTIFICATE-----).' : 'Paste a PEM private key (-----BEGIN … PRIVATE KEY-----).';
        }
        setPath(value, field.key, text);
        break;
      }
      case 'secret':
      case 'textarea':
        // Secrets and multi-line values are sent exactly as typed: spaces can be significant.
        if (!text) { if (field.keepEmpty) setPath(value, field.key, ''); else { deletePath(value, field.key); missing(); } break; }
        if (field.kind === 'secret' && /[\r\n]/u.test(text)) errors[field.key] = 'Remove line breaks.';
        setPath(value, field.key, text);
        break;
      case 'base64': {
        const compact = text.replace(/\s+/gu, '');
        if (!compact) { deletePath(value, field.key); missing(); break; }
        if (!/^[A-Za-z0-9+/]*={0,2}$/u.test(compact) || compact.length % 4 === 1) errors[field.key] = 'That isn’t base64. Load the descriptor file instead.';
        setPath(value, field.key, compact);
        break;
      }
      default: {
        const trimmed = text.trim();
        if (!trimmed) { deletePath(value, field.key); missing(); break; }
        setPath(value, field.key, trimmed);
      }
    }
  }
  // Address-shaped fields get an early hint; the node remains the authority.
  for (const key of ['address']) {
    const address = values[key];
    if (typeof address === 'string' && address.trim() && SERVICE_FIELDS[protocol].some(field => field.key === key && fieldVisible(field, values)) && !HOST_PORT.test(address.trim())) {
      errors[key] = 'Use host:port, or [IPv6]:port.';
    }
  }
  if (protocol === 'kafka') {
    const bad = (getPath(value, 'brokers') as string[] | undefined)?.find(broker => !HOST_PORT.test(broker));
    if (bad) errors.brokers = `“${bad}” isn’t host:port.`;
  }
  if (protocol === 'sftp') {
    const credentials = [value.password, value.privateKeyPem].filter(Boolean).length;
    if (credentials !== 1) errors.password = 'Use exactly one: a password or a private key.';
    const pins = [value.hostKeySha256, value.knownHosts, value.unsafeIgnoreHostKey].filter(Boolean).length;
    if (pins !== 1) errors.hostKeySha256 = 'Choose exactly one host key check: a fingerprint, known_hosts lines, or no check.';
  }
  for (const key of ['tls', 'sasl']) {
    const nested = value[key];
    if (typeof nested === 'object' && nested !== null && !Object.keys(nested).length) delete value[key];
  }
  const tls = value.tls as Record<string, unknown> | undefined;
  if (tls && Boolean(tls.clientCertPem) !== Boolean(tls.clientKeyPem)) errors[tls.clientCertPem ? 'tls.clientKeyPem' : 'tls.clientCertPem'] = 'Mutual TLS needs both the client certificate and its private key.';
  return { value, errors };
}

/** A copy of service options with private values replaced, for previews. */
export function maskService(protocol: ProtocolKey, value: unknown): unknown {
  const copy = typeof value === 'object' && value !== null ? structuredClone(value) as Record<string, unknown> : value;
  if (typeof copy !== 'object' || copy === null) return copy;
  for (const field of SERVICE_FIELDS[protocol]) {
    if (!field.private) continue;
    const current = getPath(copy, field.key);
    if (current === undefined) continue;
    if (field.kind === 'pairs' && typeof current === 'object' && current !== null) {
      setPath(copy as Record<string, unknown>, field.key, Object.fromEntries(Object.keys(current).map(key => [key, '•••'])));
    } else setPath(copy as Record<string, unknown>, field.key, '•••');
  }
  return copy;
}

const opWord = (operator: string): string => OPERATORS.find(entry => entry.value === operator)?.label ?? operator;

/** Detail rows for a service monitor. Viewers receive no service options at all. */
export function serviceFacts(m: Monitor, viewer: boolean, reveal: boolean): { label: string; value: ReactNode }[] {
  const protocol = SERVICE_PROTOCOL[m.type];
  if (!protocol) return [];
  const stored = m.service?.[protocol];
  if (viewer || !stored) return [{ label: 'Connection settings', value: <span className="muted">{viewer ? 'Hidden from viewers' : 'Not configured'}</span> }];
  const values = serviceValues(protocol, stored);
  const facts: { label: string; value: ReactNode }[] = [];
  for (const field of SERVICE_FIELDS[protocol]) {
    if (!fieldVisible(field, values)) continue;
    const raw = getPath(stored, field.key);
    if (field.kind === 'jsonata') {
      const q = raw as Partial<JSONQueryOptions> | undefined;
      if (q?.expression) facts.push({ label: field.label, value: <><span className="mono wrap">{q.expression}</span> {q.operator} <span className="mono wrap">{JSON.stringify(q.expected)}</span></> });
      continue;
    }
    if (field.kind === 'condition') {
      const condition = raw as { operator: string; expected: unknown } | undefined;
      if (condition) facts.push({ label: field.label, value: <>{opWord(condition.operator)} <span className="mono wrap">{JSON.stringify(condition.expected)}</span></> });
      continue;
    }
    if (raw === undefined || raw === '' || raw === false) {
      if (field.key === 'tlsMode') facts.push({ label: field.label, value: 'Verified TLS' });
      continue;
    }
    let value: ReactNode;
    if (field.private && !reveal) value = <span className="muted">set · hidden</span>;
    else if (field.kind === 'bool') value = field.danger ? <span className="warn-text">On</span> : 'On';
    else if (field.kind === 'pem') value = field.pem === 'key' ? <span className="muted">Private key stored</span> : `${((raw as string).match(/-----BEGIN [^-]*CERTIFICATE-----/gu) ?? []).length} certificate(s)`;
    else if (field.kind === 'base64') value = `${Math.round(((raw as string).length * 3) / 4).toLocaleString()} bytes`;
    else if (field.kind === 'select') value = field.options?.find(option => option.value === raw)?.label ?? String(raw);
    else if (Array.isArray(raw)) value = <span className="mono wrap">{field.kind === 'path' ? raw.join('.') : raw.join(', ')}</span>;
    else if (typeof raw === 'object') value = <span className="mono wrap">{field.kind === 'pairs' ? Object.keys(raw as object).join(', ') : JSON.stringify(raw)}</span>;
    else value = <span className="mono wrap">{String(raw)}{field.unit ? ` ${field.unit}` : ''}</span>;
    facts.push({ label: field.label, value });
  }
  return facts;
}

// ---------- JSONata conditions (evaluated only by the node's guarded worker; never compiled here)

export type JsonataOperator = JSONQueryOptions['operator'];
export type JsonataAs = 'text' | 'number' | 'true' | 'false';
export interface JsonataDraft { expression: string; operator: JsonataOperator; as: JsonataAs; expected: string }
export const JSONATA_OPERATORS: { value: JsonataOperator; label: string }[] = [
  { value: '==', label: '== equals' }, { value: '!=', label: '!= does not equal' },
  { value: '>', label: '> greater than' }, { value: '>=', label: '>= at least' }, { value: '<', label: '< less than' }, { value: '<=', label: '<= at most' },
  { value: 'contains', label: 'contains (literal text)' },
];
/**
 * Expected value types. The node accepts text, a finite number or a boolean for every operator (never null or a
 * container), so every type stays available and a stored type is never changed by the form. Some pairs can never
 * pass (ordering with true/false); that is shown as a warning, not a save error.
 */
export const jsonataKinds = (_operator: JsonataOperator): JsonataAs[] => ['text', 'number', 'true', 'false'];
const ORDERING = new Set<JsonataOperator>(['>', '>=', '<', '<=']);
/** A combination the node accepts but that can never pass, worth a non-blocking note. */
export function jsonataCaveat(d: JsonataDraft): string | undefined {
  if (ORDERING.has(d.operator) && (d.as === 'true' || d.as === 'false')) return 'Ordering converts both sides with JSONata $number; true and false don’t convert, so this condition always fails.';
  if (ORDERING.has(d.operator) && d.as === 'text' && d.expected.trim() && !Number.isFinite(Number(d.expected.trim()))) return 'Ordering converts both sides with JSONata $number; this text doesn’t look numeric, so the condition may always fail.';
  return undefined;
}

export function jsonataDraft(stored: unknown): JsonataDraft {
  const o = typeof stored === 'object' && stored !== null ? stored as Partial<JSONQueryOptions> : {};
  const expected = o.expected;
  return {
    expression: typeof o.expression === 'string' ? o.expression : '',
    operator: (JSONATA_OPERATORS.some(entry => entry.value === o.operator) ? o.operator : '==') as JsonataOperator,
    as: expected === true ? 'true' : expected === false ? 'false' : typeof expected === 'number' ? 'number' : 'text',
    expected: typeof expected === 'string' || typeof expected === 'number' ? String(expected) : '',
  };
}

const utf8Bytes = (text: string): number => new TextEncoder().encode(text).length;

/**
 * Builds `{expression, operator, expected}` on top of the stored object, so keys from a newer node survive. An empty
 * expression means "no JSONata condition" and removes the object. Bounds mirror the node's shape check only.
 */
export function buildJsonata(stored: unknown, d: JsonataDraft): { value?: Record<string, unknown>; error?: string } {
  if (d.expression === '') return {};
  const previous = typeof stored === 'object' && stored !== null && !Array.isArray(stored) ? structuredClone(stored) as Record<string, unknown> : {};
  if (!d.expression.trim()) return { error: 'Enter a JSONata expression, or clear it to remove the condition.' };
  if (utf8Bytes(d.expression) > 4096) return { error: 'Use an expression of at most 4,096 bytes.' };
  const kinds = jsonataKinds(d.operator);
  const as = kinds.includes(d.as) ? d.as : kinds[0]!;
  let expected: string | number | boolean;
  if (as === 'true') expected = true;
  else if (as === 'false') expected = false;
  else if (as === 'number') {
    const n = Number(d.expected.trim());
    if (!d.expected.trim() || !Number.isFinite(n)) return { error: 'Enter a finite number to compare with.' };
    expected = n;
  } else {
    if (utf8Bytes(JSON.stringify(d.expected)) > 4096) return { error: 'Use expected text of at most about 4 KiB.' };
    expected = d.expected;
  }
  return { value: { ...previous, expression: d.expression, operator: d.operator, expected } };
}

/** What `$` refers to for each kind of check. */
export const JSONATA_INPUT: Record<'http' | 'mqtt' | 'mongodb' | 'snmp' | 'globalping', string> = {
  http: 'the response body as JSON; a body that isn’t JSON is passed as a text string',
  globalping: 'the remote response body (Globalping returns at most the first 10 kB) parsed as strict JSON; a body that isn’t complete JSON, or was truncated, fails the check, with no text fallback',
  mqtt: 'each message payload that meets the payload condition, as JSON or as a text string; a message that doesn’t pass keeps the check waiting for the next one until the deadline',
  mongodb: 'the found document as relaxed MongoDB Extended JSON',
  snmp: 'the returned value as JSON, or as a text string',
};
