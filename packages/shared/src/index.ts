/** Public management API contract. Runtime coordination types remain private to the node. */
export * from './status.js';
export * from './dashboard.js';
export type MonitorType = 'http' | 'ping' | 'tcp' | 'tls' | 'dns' | 'websocket' | 'smtp' | 'docker' | 'redis' | 'postgres' | 'mysql' | 'mqtt' | 'sftp' | 'mongodb' | 'sqlserver' | 'ntp' | 'grpc' | 'snmp' | 'radius' | 'sip-options' | 'rabbitmq' | 'kafka-producer' | 'oracledb' | 'manual' | 'push' | 'globalping' | 'system-service' | 'steam' | 'pm2';
export type ObservationState = 'up' | 'down' | 'unknown' | 'pending';
export type Role = 'admin' | 'editor' | 'viewer';

export interface MonitorInput {
  name: string;
  type: MonitorType;
  target: string;
  intervalMs: number;
  timeoutMs: number;
  nodeIds: string[];
  enabled: boolean;
  /** Omitted means 200-299 for HTTP checks. */
  expectedStatusCodes?: number[];
  /** A shared destination is required for coordinated webhook delivery. */
  webhookUrl?: string;
  /** Reusable channels; each email recipient has independent delivery state. */
  notificationIds?: string[];
  /** Zero disables repeat notifications. */
  repeatAfterMs?: number;
  /** Optional user-defined group. Groups have no built-in names or topology. */
  groupId?: string;
  http?: HTTPMonitorOptions;
  tls?: TLSMonitorOptions;
  dns?: DNSMonitorOptions;
  websocket?: WebSocketMonitorOptions;
  smtp?: { tlsMode?: 'plain' | 'implicit' | 'starttls' };
  upsideDown?: boolean;
  /** Consecutive failures tolerated as pending before confirming down. */
  retries?: number;
  /** Failed/pending check spacing; omission follows intervalMs. */
  retryIntervalMs?: number;
  service?: ServiceMonitorOptions;
  /** Operator-controlled observations, replicated through normal configuration revisions. */
  manual?: { state?: 'up' | 'down' | 'pending'; message?: string };
  ping?: { count?: number; size?: number; family?: 'auto' | 'ipv4' | 'ipv6' };
  tags?: MonitorTag[];
  /** Passive heartbeat credential; omitted from public status and blanked for viewers. */
  push?: { token: string; allowGet?: boolean };
  /** Remote measurement; API trust/credentials and query text are private. */
  globalping?: GlobalpingMonitorOptions;
  steam?: SteamMonitorOptions;
  pm2?: PM2MonitorOptions;
}

/** Local connection options are private and omitted from viewer/public projections. */
export interface PM2MonitorOptions {
  transport: 'unix' | 'windows-pipe';
  home?: string;
  namespace?: string;
}

export interface SteamMonitorOptions {
  apiKey: string;
  apiUrl?: string;
  apiCa?: string;
  allowInsecureApi?: boolean;
  apiFailureIsDown?: boolean;
  skipLatency?: boolean;
  packetSize?: number;
}

export interface GlobalpingMonitorOptions {
  mode: 'ping' | 'http' | 'dns';
  location: string;
  apiUrl?: string;
  token?: string;
  allowInsecureApi?: boolean;
  apiCa?: string;
  probeTimeoutSeconds?: number;
  ipVersion?: 0 | 4 | 6;
  apiFailureIsDown?: boolean;
  ping?: { packets?: number; protocol?: 'ICMP' | 'TCP'; port?: number };
  http?: { method?: 'GET' | 'HEAD' | 'OPTIONS'; protocol?: 'HTTP2'; headers?: Record<string, string>; acceptedStatuses?: { min: number; max: number }[]; keyword?: string; invertKeyword?: boolean; jsonata?: JSONQueryOptions; ignoreTls?: boolean };
  dns?: { queryType?: 'A' | 'AAAA' | 'ANY' | 'CNAME' | 'DNSKEY' | 'DS' | 'HTTPS' | 'MX' | 'NS' | 'NSEC' | 'PTR' | 'RRSIG' | 'SOA' | 'TXT' | 'SRV' | 'SVCB'; protocol?: 'UDP' | 'TCP'; port?: number; resolver?: string; regex?: string; allowEmptyAnswers?: boolean };
}

export interface MonitorTag { tagId: string; value?: string; public?: boolean }
export interface TagInput { name: string; color: string }
export interface Tag extends TagInput { id: string; revision: string; updatedAt: number; deleted?: boolean }
export interface TagView { tag: Tag; configConflict: boolean }

export interface ServiceTLSOptions { serverName?: string; caPem?: string; clientCertPem?: string; clientKeyPem?: string; ignoreVerification?: boolean }
export interface ScalarCondition { operator: 'eq' | 'ne' | 'gt' | 'ge' | 'lt' | 'le' | 'contains' | 'prefix' | 'suffix'; expected: string | number | boolean | null }
export interface SQLMonitorOptions { dsn: string; query?: string; condition?: ScalarCondition; tlsMode?: 'tls' | 'plain'; tls?: ServiceTLSOptions }
export interface ServiceMonitorOptions {
  docker?: { endpoint: string; container: string; apiVersion?: string; tls?: ServiceTLSOptions };
  redis?: { address: string; username?: string; password?: string; database?: number; tlsMode?: 'tls' | 'plain'; tls?: ServiceTLSOptions };
  postgres?: SQLMonitorOptions;
  mysql?: SQLMonitorOptions;
  mqtt?: { address: string; username?: string; password?: string; topic: string; condition?: ScalarCondition; jsonata?: JSONQueryOptions; tlsMode?: 'tls' | 'plain'; tls?: ServiceTLSOptions };
  sftp?: { address: string; username: string; password?: string; privateKeyPem?: string; privateKeyPassphrase?: string; hostKeySha256?: string; knownHosts?: string; unsafeIgnoreHostKey?: boolean; path?: string };
  mongodb?: { dsn: string; tlsMode?: 'tls' | 'plain'; tls?: ServiceTLSOptions; collection?: string; filter?: Record<string, string | number | boolean | null>; scalarPath?: string[]; condition?: ScalarCondition; jsonata?: JSONQueryOptions };
  sqlserver?: SQLMonitorOptions;
  ntp?: { address: string; stratumThreshold?: number; offsetThresholdMs?: number; rootDispersionThresholdMs?: number; latencyThresholdMs?: number };
  grpc?: { address: string; mode: 'health' | 'unary'; tlsMode?: 'tls' | 'plain'; tls?: ServiceTLSOptions; healthService?: string; descriptorSet?: string; method?: string; request?: unknown; keyword?: string; invertKeyword?: boolean; scalarPath?: string[]; condition?: ScalarCondition; metadata?: Record<string, string> };
  snmp?: { address: string; version: '1' | '2c' | '3'; oid: string; community?: string; username?: string; securityLevel?: 'noAuthNoPriv' | 'authNoPriv' | 'authPriv'; authProtocol?: 'sha' | 'sha256'; authPassphrase?: string; privacyProtocol?: 'aes'; privacyPassphrase?: string; contextName?: string; condition?: ScalarCondition; jsonata?: JSONQueryOptions };
  radius?: { address: string; username: string; password: string; secret: string; calledStationId?: string; callingStationId?: string; unsafeAllowMissingMessageAuthenticator?: boolean };
  sip?: { address: string; transport: 'udp' | 'tcp' | 'tls'; requestUri?: string; expectedStatus?: number[]; tls?: ServiceTLSOptions };
  rabbitmq?: { mode: 'amqp' | 'management'; address?: string; username: string; password: string; virtualHost?: string; tlsMode?: 'plain' | 'tls'; tls?: ServiceTLSOptions; managementUrls?: string[] };
  kafka?: { brokers: string[]; mode: 'metadata' | 'produce'; topic?: string; message?: string; allowAutoTopicCreation?: boolean; tlsMode?: 'plain' | 'tls'; tls?: ServiceTLSOptions; sasl?: { mechanism: 'plain' | 'scram-sha-256' | 'scram-sha-512'; username: string; password: string } };
  oracle?: SQLMonitorOptions;
}

export interface RequestCredentials {
  headers?: Record<string, string>;
  basicAuth?: { username: string; password: string };
  bearerToken?: string;
  proxyUrl?: string;
  useEnvironmentProxy?: boolean;
}
export interface HTTPMonitorOptions extends RequestCredentials {
  oauth2?: { tokenUrl: string; clientId: string; clientSecret: string; scope?: string; audience?: string; authMethod?: 'client_secret_basic' | 'client_secret_post'; tokenCaPem?: string; allowInsecureHttp?: boolean };
  method?: string;
  body?: string;
  statusRanges?: { min: number; max: number }[];
  maxRedirects?: number;
  keyword?: { value: string; regex?: boolean; negate?: boolean };
  json?: { path: string; expected?: string; existsOnly?: boolean; negate?: boolean };
  jsonata?: JSONQueryOptions;
}
export interface JSONQueryOptions { expression: string; operator: '==' | '!=' | '>' | '>=' | '<' | '<=' | 'contains'; expected: string | number | boolean }
export interface TLSMonitorOptions {
  serverName?: string;
  caPem?: string;
  clientCertPem?: string;
  clientKeyPem?: string;
  minValidityDays?: number;
  ignoreVerification?: boolean;
}
export interface DNSMonitorOptions { recordType?: 'A' | 'AAAA' | 'CNAME' | 'MX' | 'TXT' | 'NS' | 'PTR'; server?: string; expected?: string[] }
export interface WebSocketMonitorOptions extends RequestCredentials {}

export interface Monitor extends MonitorInput {
  id: string;
  revision: string;
  updatedAt: number;
  deleted?: boolean;
  /** Runtime-owned identity for check semantics; metadata edits retain it. */
  checkGeneration?: string;
  /** Passive heartbeat generation anchor, retained across metadata edits. */
  pushStartedAt?: number;
}

export interface Observation {
  monitorId: string;
  nodeId: string;
  state: ObservationState;
  checkedAt: number;
  latencyMs: number;
  /** False means there is no measured RTT; latencyMs is then a compatibility zero. */
  latencyAvailable?: boolean;
  message: string;
  rawState?: 'up' | 'down' | 'pending' | 'unknown';
  consecutiveFailures?: number;
  statusCode?: number;
  certificate?: CertificateSummary;
}
export interface CertificateSummary { notBefore: string; notAfter: string; daysRemaining: number; verified: boolean }

export interface LocalHistoryStatistics {
  monitorId: string;
  nodeId: string;
  from: number;
  to: number;
  count: number;
  up: number;
  down: number;
  pending: number;
  unknown: number;
  /** Samples with a measured latency; excludes unavailable ICMP RTTs. */
  latencyCount: number;
  /** Up samples divided by up + down samples, not a wall-clock SLA. */
  sampleAvailability: number | null;
  averageLatencyMs: number | null;
  p50LatencyMs: number | null;
  p95LatencyMs: number | null;
  firstCheckedAt: number | null;
  lastCheckedAt: number | null;
  retentionMs: number;
  recordLimit: number;
}

/** Observed milliseconds; gaps and pending results are excluded from knownMs. */
export interface ObservedTimeTotals {
  startMs: number;
  endMs: number;
  upMs: number;
  downMs: number;
  pendingMs: number;
  unknownMs: number;
  maintenanceMs: number;
  knownMs: number;
  /** 0..100, or null when there is no known time. */
  availabilityPercent: number | null;
  coveragePercent: number;
}

/** Local archive only; percentages are observed coverage, not an SLA. */
export interface LocalAvailabilityHistory {
  monitorId: string;
  nodeId: string;
  from: number;
  to: number;
  stepMs: number;
  retainedFrom: number | null;
  recordLimit: number;
  retentionMs: number;
  summary: ObservedTimeTotals;
  buckets: ObservedTimeTotals[];
}

export interface MonitorView {
  monitor: Monitor;
  state: ObservationState | 'paused';
  observations: Observation[];
  /** Counts assigned nodes without fresh observations. */
  unknownNodes: number;
  configConflict: boolean;
  suppressedByGroupId?: string;
  suppressionReason?: string;
  /** Active planned work holds alerts, without replacing measured state. */
  maintenanceIds?: string[];
}

export interface NodeView {
  id: string;
  name: string;
  url?: string;
  state: 'local' | 'connected' | 'unreachable';
  lastSeenAt: number | null;
}

export interface IncidentView {
  id: string;
  monitorId: string;
  monitorName: string;
  openedAt: number;
  resolvedAt: number | null;
  notifiedAt: number | null;
  recoveryNotifiedAt: number | null;
  suppressedByGroupId?: string;
  suppressionReason?: string;
  /** Administrative closure after a check change, pause or deletion, without recovery. */
  retiredBy?: string;
  retirementReason?: string;
  maintenanceIds?: string[];
  /** Entire observed episode was planned; never-notified destinations do not replay it. */
  maintenanceComplete?: boolean;
}

export interface MaintenanceSchedule {
  strategy: 'once' | 'interval' | 'daily' | 'weekly' | 'monthly' | 'cron';
  timezone: string;
  startDate: string;
  endDate: string;
  startTime?: string;
  durationMinutes: number;
  intervalDays?: number;
  weekdays?: number[];
  daysOfMonth?: number[];
  lastDayOfMonth?: boolean;
  cron?: string;
}
export interface MaintenanceInput {
  name: string;
  enabled: boolean;
  allMonitors?: boolean;
  monitorIds?: string[];
  groupIds?: string[];
  schedule: MaintenanceSchedule;
}
export interface Maintenance extends MaintenanceInput { id: string; revision: string; updatedAt: number; deleted?: boolean }
export interface MaintenanceView {
  maintenance: Maintenance;
  active: boolean;
  /** UTC RFC3339 instants; current merged window, or null when inactive. */
  start?: string;
  end?: string;
  nextStart?: string;
  configConflict: boolean;
  reason: string;
}

export interface GroupInput {
  name: string;
  /** Node membership describes the user's location or failure domain. */
  nodeIds: string[];
  /** An existing HTTP(S) or ping monitor used as this group's outage dependency. */
  outageMonitorId?: string;
  /** Delay dependent alerts briefly so the dependency can establish its state. */
  graceMs: number;
  /** Optional user-defined hierarchy parent. Alert suppression remains direct. */
  parentId?: string;
}

export interface Group extends GroupInput {
  id: string;
  revision: string;
  updatedAt: number;
  deleted?: boolean;
}

export interface GroupView {
  group: Group;
  state: ObservationState;
  configConflict: boolean;
  suppressionActive: boolean;
  reason: string;
  reachableNodes: number;
  totalNodes: number;
  /** Current APIs provide health separately from the legacy dependency state. */
  healthState?: ObservationState | 'paused';
  health?: HealthRollup;
  /** Counts are omitted when the hierarchy cannot be determined safely. */
  healthReason?: string;
}

export interface HealthRollup {
  state: ObservationState | 'paused';
  maintenance: boolean;
  directChildCount: number;
  activeChildCount: number;
  descendantCount: number;
  leafCount: number;
  activeLeafCount: number;
  upCount: number;
  downCount: number;
  pendingCount: number;
  unknownCount: number;
  pausedCount: number;
  maintenanceCount: number;
}

export interface NotificationView {
  id: string;
  monitorId: string;
  incidentId: string;
  kind: 'down' | 'recovery' | 'repeat';
  nodeId: string;
  sentAt: number;
  destinationId?: string;
  channelId?: string;
  provider?: NotificationProvider;
}

export type NotificationProvider = 'generic' | 'webhook' | 'discord' | 'smtp' | 'mailgun' | 'brevo' | 'mandrill' | 'slack' | 'telegram' | 'ntfy' | 'gotify' | 'pushover';
export interface EmailAddress { email: string; name?: string }
export interface NotificationConfig {
  kind: Exclude<NotificationProvider, 'generic'>;
  sender?: EmailAddress;
  recipients?: EmailAddress[];
  smtp?: { server: string; port: number; tlsMode?: 'plain' | 'implicit' | 'starttls'; username?: string; password?: string };
  mailgun?: { apiKey: string; domain: string; region?: 'US' | 'EU'; baseURL?: string; allowInsecureHTTP?: boolean };
  brevo?: { apiKey: string; baseURL?: string; allowInsecureHTTP?: boolean };
  mandrill?: { apiKey: string; baseURL?: string; allowInsecureHTTP?: boolean };
  webhook?: { url: string; bearerToken?: string; allowInsecureHTTP?: boolean };
  slack?: { webhookURL: string; allowInsecureHTTP?: boolean };
  telegram?: { botToken: string; chatId: string; messageThreadId?: number; silent?: boolean; protectContent?: boolean; disableLinkPreview?: boolean; baseURL?: string; allowInsecureHTTP?: boolean };
  ntfy?: { topic: string; token?: string; username?: string; password?: string; priority?: number; tags?: string[]; baseURL?: string; allowInsecureHTTP?: boolean };
  gotify?: { appToken: string; priority?: number; baseURL: string; allowInsecureHTTP?: boolean };
  pushover?: { appToken: string; userKey: string; device?: string; priority?: number; sound?: string; retrySeconds?: number; expireSeconds?: number; baseURL?: string; allowInsecureHTTP?: boolean };
  templates?: { subject?: string; text?: string; html?: string };
}
export interface NotificationChannelInput { name: string; enabled: boolean; config: NotificationConfig }
export interface NotificationChannel extends NotificationChannelInput { id: string; revision: string; updatedAt: number; deleted?: boolean }
export interface NotificationChannelView { channel: NotificationChannel; configConflict: boolean }
export interface DeliveryAttemptView {
  id: string;
  nodeId: string;
  monitorId?: string;
  incidentId?: string;
  /** Original stored source, before the public incident ID is canonicalized. */
  sourceIncidentId?: string;
  /** Actual receipt satisfying this source/destination/event in its current episode. */
  satisfiedByReceiptId?: string;
  kind: 'down' | 'repeat' | 'recovery' | 'test';
  slot?: number;
  destinationId?: string;
  channelId?: string;
  provider: NotificationProvider;
  state: 'queued' | 'sending' | 'retrying' | 'accepted' | 'held' | 'failed' | 'cancelled' | 'unknown' | 'unavailable';
  failures: number;
  nextAttemptAt?: number;
  lastAttemptAt?: number;
  httpStatus?: number;
  errorCode?: string;
  providerId?: string;
  held?: boolean;
}

export interface Overview {
  node: { id: string; name: string };
  now: number;
  monitors: MonitorView[];
  nodes: NodeView[];
  incidents: IncidentView[];
  notifications: NotificationView[];
  user: { name: string; role: Role };
  groups?: GroupView[];
  maintenance?: MaintenanceView[];
}

export interface AccountView { id: string; name: string; role: Role; passwordEnabled?: boolean; mfaEnabled?: boolean }
export interface AuthenticationStatus {
  /** Present on the dashboard-owned account service, independent of any node. */
  dashboard?: boolean;
  passwordLogin: boolean;
  passwordConfigured: boolean;
  setupRequired: boolean;
  tokenLogin: boolean;
  mfaSupported: boolean;
}
export interface PasswordLoginInput { name: string; password: string; code?: string }
export type PasswordLoginResult = { mfaRequired: true } | { token: string; expiresAt: number; user: AccountView };
export interface AccountInput { name: string; role: Role; token?: string; password?: string }
export interface AuthenticatorEnrollment { secret: string; uri: string; expiresAt: number }
export interface ApiError { error: string }
