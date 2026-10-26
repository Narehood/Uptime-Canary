import { useEffect, useId, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react';
import type { DeliveryAttemptView, EmailAddress, NotificationChannel, NotificationChannelInput, NotificationConfig, NotificationProvider } from '@uptime-canary/shared';
import { discordTemplateProblem, TEMPLATE_PROTOCOL, templateCapability, templateSummary, unsupportedPlaceholders, withoutAddedPlaceholders, type TemplateLevel } from '../alertTemplates';
import { ApiFailure, describeFailure, isAbort, jsonBytes } from '../api';
import { useCanary } from '../context';
import { ago, bytesOf, maskedUrl, plural, shortId, shortRev, span } from '../format';
import { useResource } from '../hooks';
import { buildMessaging, control, isMessaging, messagingDraft, messagingRoute, MFIELD_LABEL, templateProblem, urlProblem, type MessagingDraft, type MField } from '../messaging';
import { notify } from '../notices';
import { href, navigate } from '../router';
import { Banner, Card, Chip, EmptyState, ErrorText, FilterChips, Modal, PageHead, ReadOnly, Rev, Tag, Time } from '../ui/bits';
import { CheckField, Disclosure, ErrorSummary, SecretField, Seg, TextField } from '../ui/forms';
import { Glyph, Icon, type IconName } from '../ui/icons';
import type { Tone } from '../model';
import { storedExtras } from '../writable';
import { MessageTemplates } from './MessageTemplates';
import { MessagingFields } from './MessagingFields';

// ---------- providers

type Kind = NotificationConfig['kind'];
type ProviderGroup = 'push' | 'email' | 'custom';
export const PROVIDERS: { kind: Kind; label: string; hint: string; icon: IconName; email: boolean; group: ProviderGroup }[] = [
  { kind: 'discord', label: 'Discord', hint: 'Channel webhook, acknowledged message', icon: 'chat', email: false, group: 'push' },
  { kind: 'slack', label: 'Slack', hint: 'Incoming webhook, plain text', icon: 'hash', email: false, group: 'push' },
  { kind: 'telegram', label: 'Telegram', hint: 'Bot message to a chat or topic', icon: 'send', email: false, group: 'push' },
  { kind: 'ntfy', label: 'ntfy', hint: 'Topic on ntfy.sh or your server', icon: 'bell', email: false, group: 'push' },
  { kind: 'gotify', label: 'Gotify', hint: 'Your Gotify server', icon: 'megaphone', email: false, group: 'push' },
  { kind: 'pushover', label: 'Pushover', hint: 'One user or group key', icon: 'phone', email: false, group: 'push' },
  { kind: 'smtp', label: 'SMTP', hint: 'Your own mail server', icon: 'mail', email: true, group: 'email' },
  { kind: 'mailgun', label: 'Mailgun', hint: 'US or EU API', icon: 'mail', email: true, group: 'email' },
  { kind: 'brevo', label: 'Brevo', hint: 'Transactional email API', icon: 'mail', email: true, group: 'email' },
  { kind: 'mandrill', label: 'Mailchimp Transactional', hint: 'Mandrill API, not Marketing', icon: 'mail', email: true, group: 'email' },
  { kind: 'webhook', label: 'Webhook', hint: 'JSON POST to your endpoint', icon: 'hook', email: false, group: 'custom' },
];
const PROVIDER_GROUPS: { key: ProviderGroup; label: string }[] = [{ key: 'push', label: 'Chat and push' }, { key: 'email', label: 'Email' }, { key: 'custom', label: 'Your endpoint' }];
const PROVIDER = new Map(PROVIDERS.map(entry => [entry.kind as string, entry]));
export const providerLabel = (kind: NotificationProvider | string): string => kind === 'generic' ? 'Legacy webhook' : PROVIDER.get(kind)?.label ?? kind;
export const ProviderIcon = ({ kind }: { kind: NotificationProvider | string }) => <span className="prov-ic" aria-hidden="true"><Icon name={PROVIDER.get(kind)?.icon ?? 'hook'} /></span>;
/** Email recipients only; chat, push and webhook channels have none. */
export const recipientCount = (channel: NotificationChannel): number => channel.config.recipients?.length ?? 0;
/** Destinations a send reaches: one per email recipient, otherwise one per channel. */
export const destinationCount = (channel: NotificationChannel): number => PROVIDER.get(channel.config.kind)?.email ? recipientCount(channel) : 1;

// ---------- delivery outcomes

const STATE: Record<DeliveryAttemptView['state'], { tone: Tone; label: string }> = {
  accepted: { tone: 'up', label: 'Accepted' },
  queued: { tone: 'pending', label: 'Queued' },
  sending: { tone: 'pending', label: 'Sending' },
  retrying: { tone: 'warn', label: 'Retrying' },
  held: { tone: 'warn', label: 'Held' },
  failed: { tone: 'down', label: 'Failed' },
  cancelled: { tone: 'none', label: 'Cancelled' },
  unknown: { tone: 'unknown', label: 'Outcome unknown' },
  unavailable: { tone: 'unknown', label: 'Unavailable' },
};
export const attemptState = (state: string): { tone: Tone; label: string } => STATE[state as DeliveryAttemptView['state']] ?? { tone: 'unknown', label: state };

const ERROR_TEXT: Record<string, string> = {
  invalid_config: 'The node rejected the channel configuration before sending.',
  invalid_message: 'The message couldn’t be built within the provider’s limits. Custom templates are sent exactly, never shortened.',
  canceled: 'The attempt timed out or was cancelled.',
  transport: 'The connection failed. The provider may or may not have accepted it.',
  response: 'The provider’s acknowledgement couldn’t be read, so acceptance is unknown.',
  response_too_large: 'The provider’s response was too large, so acceptance is unknown.',
  response_headers: 'The provider’s response headers were too large, so acceptance is unknown.',
  redirect_refused: 'The provider answered with a redirect, which isn’t followed. Check the address.',
  rate_limited: 'The provider is rate limiting. The retry honors its Retry-After hint.',
  unavailable: 'The provider was temporarily unavailable.',
  provider_rejected: 'The provider rejected the request.',
  recipient_rejected: 'The provider rejected this recipient.',
  partial_acceptance: 'The provider accepted only some recipients.',
  smtp_protocol: 'SMTP negotiation failed.',
  smtp_tls: 'The mail server’s verified TLS failed.',
  smtp_auth: 'SMTP authentication failed.',
  delivery_unconfirmed: 'Sent, but acceptance wasn’t confirmed. A retry can duplicate it.',
  destination_unavailable: 'The channel is disabled or deleted, or the monitor no longer uses it.',
  channel_changed: 'The channel’s route changed before this attempt was sent.',
  completed_during_maintenance: 'The whole outage happened inside planned maintenance; this destination was never notified, so it isn’t replayed.',
};
export const errorText = (code: string | undefined): string | undefined => code ? ERROR_TEXT[code] ?? code.replace(/_/gu, ' ') : undefined;
const KIND_LABEL: Record<DeliveryAttemptView['kind'], string> = { down: 'Down alert', repeat: 'Repeat', recovery: 'Recovery', test: 'Test' };

export function NotificationTabs({ current }: { current: 'channels' | 'deliveries' | 'receipts' }) {
  const tabs = [
    { key: 'channels', label: 'Channels', to: href('notifications') },
    { key: 'deliveries', label: 'Delivery attempts', to: href('notifications', 'deliveries') },
    { key: 'receipts', label: 'Receipts', to: href('notifications', 'receipts') },
  ];
  return (
    <nav className="tabs" aria-label="Notifications">
      {tabs.map(tab => <a key={tab.key} href={tab.to} aria-current={current === tab.key ? 'page' : undefined}>{tab.label}</a>)}
    </nav>
  );
}

/** Why an attempt is held or still owed, using only what the endpoint reports. */
function AttemptWhy({ attempt }: { attempt: DeliveryAttemptView }) {
  const { monitorMap, groupMap, maintenanceMap } = useCanary();
  const view = attempt.monitorId ? monitorMap.get(attempt.monitorId) : undefined;
  const parts: ReactNode[] = [];
  const text = errorText(attempt.errorCode);
  if (text) parts.push(text);
  if (attempt.state === 'held' && view?.maintenanceIds?.length) {
    const names = view.maintenanceIds.map(id => maintenanceMap.get(id)?.maintenance.name).filter(Boolean);
    parts.push(<>Held by planned maintenance{names.length ? <> (<b>{names.join(', ')}</b>)</> : null}. It’s sent if the outage continues after the window.</>);
  }
  if (view?.suppressedByGroupId && attempt.kind !== 'test') {
    const group = groupMap.get(view.suppressedByGroupId)?.group.name ?? shortId(view.suppressedByGroupId);
    parts.push(attempt.state === 'held'
      ? <>Held by group <b>{group}</b>{view.suppressionReason ? `: ${view.suppressionReason}` : ''}.</>
      : <>Group <b>{group}</b> holds alerts only for destinations that received its outage alert; this destination fails open after the grace period.</>);
  }
  if (attempt.state === 'accepted') parts.push('The provider acknowledged it. That isn’t proof of inbox, channel or device delivery.');
  if (!parts.length) return null;
  return <div className="why">{parts.map((part, index) => <span key={index}>{part}</span>)}</div>;
}

export function DeliveryTable({ attempts, compact }: { attempts: DeliveryAttemptView[]; compact?: boolean }) {
  const { monitorMap, channels, overview } = useCanary();
  return (
    <div className="tbl-wrap">
      <table className="tbl stack deliveries">
        <caption className="vh">Delivery attempts, newest first</caption>
        <thead>
          <tr>
            <th scope="col">Last attempt</th><th scope="col">Event</th><th scope="col">Destination</th><th scope="col">Outcome</th>
            {compact ? null : <th scope="col">Node</th>}<th scope="col" className="num">Failures</th>
          </tr>
        </thead>
        <tbody>
          {attempts.map(attempt => {
            const monitor = attempt.monitorId ? monitorMap.get(attempt.monitorId)?.monitor : undefined;
            const channel = attempt.channelId ? channels.map.get(attempt.channelId)?.channel : undefined;
            const state = attemptState(attempt.state);
            return (
              <tr key={attempt.id}>
                <td data-label="Last attempt" className="tn">{attempt.lastAttemptAt ? <Time at={attempt.lastAttemptAt} now={overview.now} /> : <span className="muted">not yet</span>}</td>
                <td data-label="Event" className="lead">
                  <b>{KIND_LABEL[attempt.kind] ?? attempt.kind}{attempt.kind === 'repeat' && attempt.slot ? ` #${attempt.slot}` : ''}</b>
                  {attempt.kind === 'test' ? <span className="muted"> · channel test</span>
                    : monitor ? <> · <a href={href('monitors', monitor.id)}>{monitor.name}</a></>
                    : attempt.monitorId ? <span className="muted"> · deleted monitor</span> : null}
                  {attempt.incidentId ? <div className="muted small">incident <span className="mono" title={attempt.incidentId}>{shortId(attempt.incidentId)}</span></div> : null}
                </td>
                <td data-label="Destination">
                  <span className="nowrap"><ProviderIcon kind={attempt.provider} />{channel ? <a href={href('channels', channel.id, 'edit')}>{channel.name}</a> : attempt.channelId ? <span className="muted">channel {shortId(attempt.channelId)}</span> : providerLabel(attempt.provider)}</span>
                  {attempt.destinationId ? <div className="muted small">destination <span className="mono" title={attempt.destinationId}>{shortId(attempt.destinationId)}</span></div> : null}
                </td>
                <td data-label="Outcome" className="full">
                  <span className="nowrap"><Chip tone={state.tone} label={state.label} />{attempt.httpStatus ? <span className="muted small"> HTTP {attempt.httpStatus}</span> : null}</span>
                  {attempt.nextAttemptAt ? <div className="muted small">next try <Time at={attempt.nextAttemptAt} now={overview.now} mode="short" /></div> : null}
                  {attempt.providerId ? <div className="muted small">acknowledgement <span className="mono" title="SHA-256 of the provider’s ID">{attempt.providerId.slice(0, 10)}…</span></div> : null}
                  <AttemptWhy attempt={attempt} />
                </td>
                {compact ? null : <td data-label="Node"><span className="mono">{attempt.nodeId}</span></td>}
                <td data-label="Failures" className="num">{attempt.failures}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

type AttemptFilter = 'all' | 'problems' | 'accepted' | 'held' | 'tests';
const PROBLEM = new Set(['failed', 'unknown', 'unavailable', 'retrying']);

export function DeliveriesView() {
  const { api, endpoint, search, monitorMap, channels, connection } = useCanary();
  const [limit, setLimit] = useState(200);
  const [filter, setFilter] = useState<AttemptFilter>('all');
  const [channelId, setChannelId] = useState('');
  const attempts = useResource<DeliveryAttemptView[]>(`${connection.identity}|deliveries|${limit}`, signal => api.deliveries({ limit }, signal), 15_000);
  const q = search.trim().toLowerCase();
  const all = (attempts.data ?? []).filter(attempt => (!channelId || attempt.channelId === channelId)
    && (!q || `${monitorMap.get(attempt.monitorId ?? '')?.monitor.name ?? ''} ${channels.map.get(attempt.channelId ?? '')?.channel.name ?? ''} ${attempt.provider} ${attempt.nodeId} ${attempt.errorCode ?? ''} ${attempt.incidentId ?? ''}`.toLowerCase().includes(q)));
  const keep = (attempt: DeliveryAttemptView, f: AttemptFilter): boolean => f === 'all' || (f === 'problems' ? PROBLEM.has(attempt.state) : f === 'accepted' ? attempt.state === 'accepted' : f === 'held' ? attempt.state === 'held' : attempt.kind === 'test');
  const rows = all.filter(attempt => keep(attempt, filter));
  const unsupported = attempts.error instanceof ApiFailure && attempts.error.status === 404;
  return (
    <div className="view">
      <PageHead title="Notifications" sub={<>Delivery attempts known to <span className="mono">{endpoint}</span>, newest first. Each email recipient, and each chat, push or webhook channel, is its own destination with its own retries.</>} />
      <NotificationTabs current="deliveries" />
      <Card
        title="Delivery attempts"
        hint={attempts.loadedAt ? <>Loaded {ago(attempts.loadedAt, Date.now())} · refreshes every 15 s · this node’s attempts and the receipts it has learned from peers</> : undefined}
        actions={<>
          {(channels.list?.length ?? 0) > 0 ? (
            <label className="inline-sel"><span className="vh">Channel</span>
              <select value={channelId} onChange={event => setChannelId(event.target.value)}>
                <option value="">All channels</option>
                {channels.list!.map(view => <option key={view.channel.id} value={view.channel.id}>{view.channel.name}</option>)}
              </select>
            </label>
          ) : null}
          <button type="button" className="icon-btn sm" onClick={attempts.reload} aria-label="Reload delivery attempts" data-tip="Reload"><Icon name="refresh" /></button>
        </>}
        className={attempts.loading && attempts.data ? 'refetching' : undefined}
      >
        {unsupported ? <EmptyState title={`${endpoint} doesn’t report delivery attempts`}>It probably runs an older Uptime-Canary version. Receipts are still listed under Receipts.</EmptyState>
          : attempts.error && !attempts.data ? <Banner tone="down" title="Delivery attempts could not be loaded">{describeFailure(attempts.error)}</Banner>
          : !attempts.data ? <p className="muted">Loading delivery attempts from {endpoint}…</p>
          : (
            <>
              <FilterChips<AttemptFilter> label="Filter attempts" value={filter} onChange={setFilter} options={[
                { value: 'all', label: 'All', count: all.length },
                { value: 'problems', label: 'Needs attention', count: all.filter(a => keep(a, 'problems')).length },
                { value: 'accepted', label: 'Accepted', count: all.filter(a => keep(a, 'accepted')).length },
                { value: 'held', label: 'Held', count: all.filter(a => keep(a, 'held')).length },
                { value: 'tests', label: 'Tests', count: all.filter(a => keep(a, 'tests')).length },
              ]} />
              {rows.length ? <DeliveryTable attempts={rows} /> : <EmptyState title="No attempts here">{all.length ? 'Nothing matches this filter.' : `${endpoint} has no delivery attempts yet. They appear when an incident notifies a channel, or when you test one.`}</EmptyState>}
              {attempts.data.length >= limit && limit < 1000 ? <div className="row-wrap center"><button type="button" className="btn sm" onClick={() => setLimit(1000)}>Load up to 1,000 attempts</button></div> : null}
            </>
          )}
        <p className="foot-note">
          <b>Accepted</b> means the provider acknowledged the request (Discord with a saved message, Slack with “ok”, Telegram, ntfy, Gotify and Pushover with a message or request ID, email services and SMTP after DATA). It isn’t proof of inbox or device delivery.
          Outcomes, error codes and HTTP statuses are fixed values: provider bodies, addresses and secret URLs are never shown. Partitions can cause duplicate sends.
        </p>
      </Card>
    </div>
  );
}

// ---------- channel list

function TestDialog({ channel, onClose }: { channel: NotificationChannel; onClose: () => void }) {
  const { api, endpoint, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<DeliveryAttemptView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const destinations = destinationCount(channel);
  const emergency = channel.config.kind === 'pushover' && channel.config.pushover?.priority === 2 ? channel.config.pushover : undefined;
  const send = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      const response = await api.testChannel(channel.id, channel.revision);
      if (!stillCurrent()) return;
      setResult(Array.isArray(response?.attempts) ? response.attempts : []);
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure, { preferDetail: true }));
    } finally { setBusy(false); }
  };
  return (
    <Modal title={`Send a test from ${channel.name}?`} onClose={onClose} busy={busy}>
      {result ? (
        <>
          <p><Glyph tone="up" /> {endpoint} queued {plural(result.length, 'test attempt')}. Each makes one attempt per destination, without automatic retries.</p>
          <p className="muted small">Outcomes appear under Delivery attempts within a few seconds. Accepted means the provider acknowledged the message, not that it reached an inbox, chat or device.</p>
          <div className="row-wrap modal-acts">
            <a className="btn primary" href={href('notifications', 'deliveries')} onClick={onClose}><Icon name="send" />Open delivery attempts</a>
            <button type="button" className="btn" onClick={onClose}>Close</button>
          </div>
        </>
      ) : (
        <>
          <p>
            This sends a <b>real message</b> from <span className="mono">{endpoint}</span> to {destinations === 1 ? 'this channel’s destination' : `all ${destinations} recipients`} through {providerLabel(channel.config.kind)}.
            It creates no monitor, incident or receipt.
          </p>
          {emergency ? <p className="fwarn"><Glyph tone="warn" />Emergency priority: Pushover repeats this test every {span((emergency.retrySeconds ?? 0) * 1000)} until someone acknowledges it, for up to {span((emergency.expireSeconds ?? 0) * 1000)}.</p> : null}
          <p className="muted small">Tests use revision <Rev revision={channel.revision} />. A node sends one batch at a time, at least 5 seconds apart.</p>
          {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Test not sent</b><p>{error}</p></div></div> : null}
          <div className="row-wrap modal-acts">
            <button type="button" className="btn primary" onClick={() => { void send(); }} disabled={busy || !channel.enabled}><Icon name="send" />{busy ? 'Queuing…' : 'Send test'}</button>
            <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          </div>
          {!channel.enabled ? <p className="muted small">Enable the channel before testing it.</p> : null}
        </>
      )}
    </Modal>
  );
}

/** Outcomes that won't change any more for a one-shot test attempt (tests have no automatic retries). */
const SETTLED = new Set(['accepted', 'failed', 'cancelled', 'unknown', 'unavailable']);
const PREVIEW_POLL_MS = 1500;
const PREVIEW_WAIT_MS = 30_000;

/**
 * Sends one real test with the form's current settings, saved or not, enabled or not (protocol 19's preview test). The
 * settings go only in that request: nothing is saved, stored in the browser or logged. Outcomes come from this node's
 * delivery attempts, read by their IDs until they settle or 30 seconds pass. The node allows one test batch at a time, at
 * least 5 seconds apart, shared with saved channels' tests.
 */
function PreviewTestDialog({ input, onClose }: { input: NotificationChannelInput; onClose: () => void }) {
  const { api, endpoint, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [attempts, setAttempts] = useState<DeliveryAttemptView[] | null>(null);
  const [waiting, setWaiting] = useState(false);
  const stop = useRef<AbortController | null>(null);
  useEffect(() => () => stop.current?.abort(), []);
  const kind = input.config.kind;
  const recipients = PROVIDER.get(kind)?.email ? input.config.recipients?.length ?? 0 : 1;
  const emergency = kind === 'pushover' && input.config.pushover?.priority === 2 ? input.config.pushover : undefined;
  const follow = (ids: string[]): void => {
    const controller = new AbortController();
    stop.current = controller;
    const until = Date.now() + PREVIEW_WAIT_MS;
    setWaiting(true);
    const poll = async (): Promise<void> => {
      if (controller.signal.aborted) return;
      try {
        const all = await api.deliveries({ limit: 200 }, controller.signal);
        if (controller.signal.aborted || !stillCurrent()) return;
        const mine = ids.map(id => all.find(attempt => attempt.id === id)).filter((attempt): attempt is DeliveryAttemptView => Boolean(attempt));
        if (mine.length) setAttempts(previous => ids.map(id => mine.find(attempt => attempt.id === id) ?? previous?.find(attempt => attempt.id === id)).filter((attempt): attempt is DeliveryAttemptView => Boolean(attempt)));
        if (mine.length === ids.length && mine.every(attempt => SETTLED.has(attempt.state))) { setWaiting(false); return; }
      } catch (failure) {
        if (controller.signal.aborted || isAbort(failure)) return;
      }
      if (Date.now() >= until) { setWaiting(false); return; }
      setTimeout(() => { void poll(); }, PREVIEW_POLL_MS);
    };
    setTimeout(() => { void poll(); }, PREVIEW_POLL_MS);
  };
  const send = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      const response = await api.previewChannelTest(input);
      if (!stillCurrent()) return;
      setAttempts(response.attempts);
      follow(response.attempts.map(attempt => attempt.id).filter(id => typeof id === 'string'));
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure, { preferDetail: true }));
    } finally { setBusy(false); }
  };
  return (
    <Modal title="Test these settings?" onClose={onClose} busy={busy}>
      {attempts ? (
        <>
          <p role="status"><Glyph tone="up" /> {endpoint} queued {plural(attempts.length, 'test attempt')} with these settings. Nothing was saved.</p>
          {attempts.length ? (
            <ul className="preview-out">
              {attempts.map(attempt => {
                const state = attemptState(attempt.state);
                return (
                  <li key={attempt.id}>
                    <ProviderIcon kind={attempt.provider} />
                    <span className="grow">{providerLabel(attempt.provider)}{attempt.httpStatus ? <span className="muted small"> · HTTP {attempt.httpStatus}</span> : null}
                      {errorText(attempt.errorCode) ? <span className="muted small"> · {errorText(attempt.errorCode)}</span> : null}</span>
                    <Chip tone={state.tone} label={state.label} />
                  </li>
                );
              })}
            </ul>
          ) : null}
          <p className="muted small">{waiting ? 'Waiting for the outcome…' : 'Accepted means the provider acknowledged the message, not that it reached an inbox, chat or device.'} These attempts are listed under Delivery attempts too.</p>
          <div className="row-wrap modal-acts"><button type="button" className="btn" onClick={onClose}>Close</button></div>
        </>
      ) : (
        <>
          <p>
            This sends a <b>real message</b> from <span className="mono">{endpoint}</span> to {recipients === 1 ? 'the destination' : `all ${recipients} recipients`} in the form, through {providerLabel(kind)}, with the settings as they are now, saved or not. {input.enabled ? '' : 'The channel stays disabled. '}
            Nothing is saved, and no monitor, incident or receipt is created.
          </p>
          {emergency ? <p className="fwarn"><Glyph tone="warn" />Emergency priority: Pushover repeats this test every {span((emergency.retrySeconds ?? 0) * 1000)} until someone acknowledges it, for up to {span((emergency.expireSeconds ?? 0) * 1000)}.</p> : null}
          <p className="muted small">A node sends one test batch at a time, at least 5 seconds apart, shared with saved channels’ tests.</p>
          {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Test not sent</b><p>{error}</p></div></div> : null}
          <div className="row-wrap modal-acts">
            <button type="button" className="btn primary" onClick={() => { void send(); }} disabled={busy}><Icon name="send" />{busy ? 'Queuing…' : 'Send test'}</button>
            <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          </div>
        </>
      )}
    </Modal>
  );
}

/** At most this many monitors in one application batch (the node's limit, also its monitor cap). */
const APPLY_MAX = 200;

/**
 * Adds one enabled channel to monitors the user picks, never all of them silently (protocol 19). The node applies the
 * whole batch at the revisions shown or nothing: a stale or conflicting monitor, a changed channel or a monitor that
 * already has 16 channels refuses it, and the list is reloaded. Monitors keep their other destinations, and nothing
 * about what is checked changes.
 */
function ApplyDialog({ channelId, onClose }: { channelId: string; onClose: () => void }) {
  const { api, endpoint, channels, overview, refresh, stillCurrent } = useCanary();
  const formId = useId();
  const [selected, setSelected] = useState<string[]>([]);
  const [query, setQuery] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [opened] = useState(() => channels.map.get(channelId)?.channel.revision);
  const view = channels.map.get(channelId);
  const channel = view?.channel;
  const rows = [...overview.monitors].filter(entry => !entry.monitor.deleted).sort((a, b) => a.monitor.name.localeCompare(b.monitor.name)).map(entry => {
    const ids = entry.monitor.notificationIds ?? [];
    const has = ids.includes(channelId);
    const full = !has && ids.length >= 16;
    return { entry, has, full, blocked: has || full || entry.configConflict };
  });
  const q = query.trim().toLowerCase();
  const shown = rows.filter(row => !q || `${row.entry.monitor.name} ${row.entry.monitor.type} ${row.entry.monitor.target}`.toLowerCase().includes(q));
  const picked = selected.filter(id => rows.some(row => row.entry.monitor.id === id && !row.blocked));
  const toggle = (id: string, on: boolean): void => setSelected(list => on ? (list.includes(id) ? list : [...list, id]) : list.filter(other => other !== id));
  const selectShown = (): void => setSelected(list => [...new Set([...list, ...shown.filter(row => !row.blocked).map(row => row.entry.monitor.id)])].slice(0, APPLY_MAX));
  const apply = async (): Promise<void> => {
    if (!channel || !picked.length) return;
    setBusy(true); setError(null);
    try {
      const monitors = picked.map(id => rows.find(row => row.entry.monitor.id === id)!.entry.monitor);
      const { changedIds } = await api.applyChannel(channel.id, channel.revision, monitors);
      if (!stillCurrent()) return;
      notify(changedIds.length
        ? `Added “${channel.name}” to ${plural(changedIds.length, 'monitor')} on ${endpoint}. Peers pick it up when they next sync.`
        : `No monitor changed: the selected monitors already had “${channel.name}”.`);
      refresh(); channels.reload();
      onClose();
    } catch (failure) {
      if (!stillCurrent()) return;
      if (failure instanceof ApiFailure && failure.status === 409) {
        setError(`Nothing was changed. ${describeFailure(failure, { preferDetail: true })} The list has been reloaded: review the selection and try again.`);
        refresh(); channels.reload();
      } else setError(describeFailure(failure, { preferDetail: true }));
    } finally { setBusy(false); }
  };
  if (!channel) {
    return <Modal title="Add a channel to monitors" onClose={onClose}><p>{endpoint} no longer lists this channel.</p><div className="row-wrap modal-acts"><button type="button" className="btn" onClick={onClose}>Close</button></div></Modal>;
  }
  return (
    <Modal title={`Add ${channel.name} to monitors`} onClose={onClose} busy={busy} wide>
      <p>Choose the monitors that should also alert through <b>{channel.name}</b> ({providerLabel(channel.config.kind)}). Each keeps its other channels. Nothing about what’s checked changes, so open incidents continue.</p>
      {opened && channel.revision !== opened ? <p className="small fwarn"><Glyph tone="warn" />This channel was edited since you opened this. Check its destination before adding it.</p> : null}
      {!channel.enabled ? <p className="small fwarn"><Glyph tone="warn" />This channel is disabled. Enable it before adding it to monitors.</p> : null}
      {view?.configConflict ? <p className="small fwarn"><Glyph tone="warn" />This channel has a configuration conflict. Resolve it first.</p> : null}
      <div className="fld">
        <label htmlFor={`${formId}-q`}>Find monitors <span className="muted">(optional)</span></label>
        <input id={`${formId}-q`} type="search" value={query} onChange={event => setQuery(event.target.value)} autoComplete="off" spellCheck={false} />
      </div>
      <div className="row-wrap pick-acts">
        <button type="button" className="linkbtn" onClick={selectShown} disabled={busy}>Select every monitor shown</button>
        <span aria-hidden="true" className="muted">·</span>
        <button type="button" className="linkbtn" onClick={() => setSelected([])} disabled={busy || !selected.length}>Clear</button>
        <span className="muted small">{picked.length} selected, at most {APPLY_MAX}</span>
      </div>
      <fieldset className="picklist apply-list" disabled={busy}>
        <legend className="vh">Monitors</legend>
        {shown.length ? shown.map(row => (
          <label key={row.entry.monitor.id} className={row.blocked ? 'off' : undefined}>
            <input type="checkbox" checked={row.has || picked.includes(row.entry.monitor.id)} disabled={row.blocked} onChange={event => toggle(row.entry.monitor.id, event.target.checked)} />
            <span><b>{row.entry.monitor.name}</b> <span className="muted small">{row.has ? 'already has it' : row.full ? 'already has 16 channels' : row.entry.configConflict ? 'configuration conflict' : `${plural(row.entry.monitor.notificationIds?.length ?? 0, 'channel')}`}</span></span>
          </label>
        )) : <p className="muted small">{rows.length ? 'No monitor matches.' : `${endpoint} has no monitors yet.`}</p>}
      </fieldset>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not added</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn primary" onClick={() => { void apply(); }} disabled={busy || !picked.length || picked.length > APPLY_MAX || !channel.enabled || Boolean(view?.configConflict)}>
          <Icon name="plus" />{busy ? 'Adding…' : picked.length ? `Add to ${plural(picked.length, 'monitor')}` : 'Add to monitors'}
        </button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
      <p className="muted small">The node adds it to all of them at once at the revisions listed here, or to none if any changed meanwhile. New monitors pick up default channels on their own; this changes existing ones only when you ask.</p>
    </Modal>
  );
}

function DeleteChannelDialog({ channel, users, onClose }: { channel: NotificationChannel; users: number; onClose: () => void }) {
  const { api, endpoint, channels, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const remove = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      await api.deleteChannel(channel.id, channel.revision);
      if (!stillCurrent()) return;
      notify(`Deleted channel “${channel.name}” on ${endpoint}. Peers keep the deletion when they sync.`);
      channels.reload();
      onClose();
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure));
      setBusy(false);
    }
  };
  return (
    <Modal title={`Delete channel ${channel.name}?`} onClose={onClose} busy={busy}>
      <p>{users ? `${plural(users, 'monitor')} still list${users === 1 ? 's' : ''} it. Their alerts to this channel stop; other destinations are unaffected.` : 'No monitor uses this channel.'} Accepted receipts stay accepted.</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not deleted</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void remove(); }} disabled={busy}><Icon name="trash" />{busy ? 'Deleting…' : 'Delete channel'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
    </Modal>
  );
}

function routeText(channel: NotificationChannel, viewer: boolean): ReactNode {
  if (viewer) return <span className="muted">Configuration hidden from viewers</span>;
  const c = channel.config;
  const messaging = messagingRoute(c);
  if (messaging) return <><span className="mono">{messaging.where}</span>{messaging.facts.map(fact => ` · ${fact}`).join('')}</>;
  switch (c.kind) {
    case 'discord': return <span className="mono">{c.webhook?.url ? maskedUrl(c.webhook.url) : 'no URL'}</span>;
    case 'webhook': return <span className="mono">{c.webhook?.url ? maskedUrl(c.webhook.url) : 'no URL'}{c.webhook?.bearerToken ? ' · bearer token' : ''}</span>;
    case 'smtp': return <span className="mono">{c.smtp?.server}:{c.smtp?.port} · {c.smtp?.tlsMode || 'starttls'}</span>;
    case 'mailgun': return <><span className="mono">{c.mailgun?.domain}</span> · {c.mailgun?.region === 'EU' ? 'EU' : 'US'} region</>;
    default: return c.sender?.email ? <>from <span className="mono">{c.sender.email}</span></> : null;
  }
}

export function ChannelsView() {
  const { channels, editable, endpoint, overview, search, role } = useCanary();
  const [testing, setTesting] = useState<NotificationChannel | null>(null);
  const [deleting, setDeleting] = useState<NotificationChannel | null>(null);
  const [applying, setApplying] = useState<string | null>(null);
  const q = search.trim().toLowerCase();
  const users = useMemo(() => {
    const counts = new Map<string, number>();
    for (const view of overview.monitors) for (const id of view.monitor.notificationIds ?? []) counts.set(id, (counts.get(id) ?? 0) + 1);
    return counts;
  }, [overview.monitors]);
  const list = (channels.list ?? []).filter(view => !q || `${view.channel.name} ${providerLabel(view.channel.config.kind)}`.toLowerCase().includes(q));
  return (
    <div className="view">
      <PageHead
        title="Notifications"
        sub={<>Reusable channels known to <span className="mono">{endpoint}</span>. Monitors pick channels; every node can deliver them.</>}
        actions={editable ? <a className="btn primary" href={href('channels', 'new')}><Icon name="plus" />New channel</a> : <ReadOnly>Viewers see names and providers only</ReadOnly>}
      />
      <NotificationTabs current="channels" />
      <div className="guar">
        <div><b><Icon name="send" />Delivery comes first</b><p>Assigned nodes take turns per destination. During a partition each side may send, so duplicates are possible. Nothing is suppressed to avoid one.</p></div>
        <div><b><Icon name="mail" />One destination per recipient</b><p>Each email recipient has its own retries and outcome; a chat, push or webhook channel is one destination. Acceptance by one never completes another.</p></div>
        <div><b><Icon name="check" />Accepted isn’t delivered</b><p>Accepted means the provider acknowledged the request. Inbox placement, bounces, chat visibility and phone notifications aren’t tracked.</p></div>
      </div>
      <Card>
        {channels.unsupported ? <EmptyState title={`${endpoint} doesn’t offer reusable channels`}>It probably runs an older Uptime-Canary version. Monitors can still use a legacy webhook URL.</EmptyState>
          : channels.error && !channels.list ? <Banner tone="down" title="Channels could not be loaded" actions={<button type="button" className="btn sm" onClick={channels.reload}><Icon name="refresh" />Try again</button>}>{describeFailure(channels.error)}</Banner>
          : !channels.list ? <p className="muted">Loading channels from {endpoint}…</p>
          : list.length ? (
            <ul className="chan-list">
              {list.map(view => {
                const c = view.channel;
                const count = users.get(c.id) ?? 0;
                return (
                  <li key={c.id} className={c.enabled ? undefined : 'off'}>
                    <ProviderIcon kind={c.config.kind} />
                    <div className="grow">
                      <div className="t">
                        <b>{c.name}</b>
                        <span className="muted small">{providerLabel(c.config.kind)}</span>
                        {c.enabled ? null : <Tag>disabled</Tag>}
                        {c.isDefault ? <Tag icon="plus">{c.enabled ? 'default for new monitors' : 'default, but disabled'}</Tag> : null}
                        {view.configConflict ? <a href={href('config')} className="nowrap small"><Glyph tone="serious" /> conflict</a> : null}
                      </div>
                      <div className="m">
                        {routeText(c, role === 'viewer')}
                        {recipientCount(c) ? <span> · {plural(recipientCount(c), 'recipient')}</span> : null}
                        <span className="muted"> · used by {plural(count, 'monitor')} · <Rev revision={c.revision} /></span>
                      </div>
                    </div>
                    {editable ? (
                      <div className="acts">
                        <button type="button" className="btn sm" onClick={() => setTesting(c)} disabled={view.configConflict || !c.enabled} data-tip={c.enabled ? 'Send one real test message' : 'Enable the channel to test it'}><Icon name="send" />Test</button>
                        <button type="button" className="btn sm" onClick={() => setApplying(c.id)} disabled={view.configConflict || !c.enabled} data-tip={c.enabled ? 'Choose existing monitors to add it to' : 'Enable the channel to add it to monitors'} aria-label={`Add ${c.name} to monitors`}><Icon name="plus" />Add to monitors</button>
                        <a className={`btn sm${view.configConflict ? ' is-disabled' : ''}`} href={view.configConflict ? undefined : href('channels', c.id, 'edit')} aria-disabled={view.configConflict || undefined}><Icon name="edit" />Edit</a>
                        <button type="button" className="btn sm danger" onClick={() => setDeleting(c)} disabled={view.configConflict} aria-label={`Delete ${c.name}`}><Icon name="trash" /></button>
                      </div>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          ) : (
            <EmptyState title={channels.list.length ? 'Nothing matches' : 'No channels yet'} action={!channels.list.length && editable ? <a className="btn primary" href={href('channels', 'new')}><Icon name="plus" />New channel</a> : undefined}>
              {channels.list.length ? 'Try another search.' : 'Add Discord, Slack, Telegram, ntfy, Gotify, Pushover, an email service or a JSON webhook, then pick it in a monitor’s alerts.'}
            </EmptyState>
          )}
      </Card>
      {testing ? <TestDialog channel={testing} onClose={() => setTesting(null)} /> : null}
      {applying ? <ApplyDialog channelId={applying} onClose={() => setApplying(null)} /> : null}
      {deleting ? <DeleteChannelDialog channel={deleting} users={users.get(deleting.id) ?? 0} onClose={() => setDeleting(null)} /> : null}
    </div>
  );
}

// ---------- channel form

interface Recipient { key: number; email: string; name: string }
let recipientKey = 0;

interface ChannelDraft extends MessagingDraft {
  name: string;
  enabled: boolean;
  /** Protocol 19: preselected for new monitors while enabled. Never applied to existing monitors by itself. */
  isDefault: boolean;
  kind: Kind;
  discordUrl: string;
  /** Peer protocol 20: Discord's card layout (webhook.embed). Only its own control changes it; a provider change or preset never does. */
  discordEmbed: boolean;
  hookUrl: string; hookBearer: string; hookInsecure: boolean;
  senderEmail: string; senderName: string;
  recipients: Recipient[];
  smtpServer: string; smtpPort: string; smtpTls: 'starttls' | 'implicit' | 'plain'; smtpUser: string; smtpPass: string;
  mailgunKey: string; mailgunDomain: string; mailgunRegion: 'US' | 'EU'; mailgunBase: string; mailgunInsecure: boolean;
  brevoKey: string; brevoBase: string; brevoInsecure: boolean;
  mandrillKey: string; mandrillBase: string; mandrillInsecure: boolean;
  subject: string; text: string; html: string;
}

function channelDraft(channel: NotificationChannel | undefined): ChannelDraft {
  const c = channel?.config;
  return {
    name: channel?.name ?? '',
    enabled: channel?.enabled ?? true,
    isDefault: channel?.isDefault === true,
    kind: c?.kind ?? 'discord',
    discordUrl: c?.kind === 'discord' ? c.webhook?.url ?? '' : '',
    discordEmbed: c?.kind === 'discord' && c.webhook?.embed === true,
    hookUrl: c?.kind === 'webhook' ? c.webhook?.url ?? '' : '', hookBearer: c?.kind === 'webhook' ? c.webhook?.bearerToken ?? '' : '', hookInsecure: Boolean(c?.kind === 'webhook' && c.webhook?.allowInsecureHTTP),
    senderEmail: c?.sender?.email ?? '', senderName: c?.sender?.name ?? '',
    recipients: (c?.recipients?.length ? c.recipients : [{ email: '' }]).map(r => ({ key: ++recipientKey, email: r.email, name: r.name ?? '' })),
    smtpServer: c?.smtp?.server ?? '', smtpPort: c?.smtp?.port ? String(c.smtp.port) : '', smtpTls: (c?.smtp?.tlsMode || 'starttls') as ChannelDraft['smtpTls'], smtpUser: c?.smtp?.username ?? '', smtpPass: c?.smtp?.password ?? '',
    mailgunKey: c?.mailgun?.apiKey ?? '', mailgunDomain: c?.mailgun?.domain ?? '', mailgunRegion: c?.mailgun?.region === 'EU' ? 'EU' : 'US', mailgunBase: c?.mailgun?.baseURL ?? '', mailgunInsecure: Boolean(c?.mailgun?.allowInsecureHTTP),
    brevoKey: c?.brevo?.apiKey ?? '', brevoBase: c?.brevo?.baseURL ?? '', brevoInsecure: Boolean(c?.brevo?.allowInsecureHTTP),
    mandrillKey: c?.mandrill?.apiKey ?? '', mandrillBase: c?.mandrill?.baseURL ?? '', mandrillInsecure: Boolean(c?.mandrill?.allowInsecureHTTP),
    subject: c?.templates?.subject ?? '', text: c?.templates?.text ?? '', html: c?.templates?.html ?? '',
    ...messagingDraft(c),
  };
}

type CField = MField | 'name' | 'discordUrl' | 'hookUrl' | 'hookBearer' | 'sender' | 'recipients' | 'smtpServer' | 'smtpPort' | 'smtpAuth' | 'apiKey' | 'mailgunDomain' | 'templates' | 'size';
const CFIELD_LABEL: Record<CField, string> = {
  ...MFIELD_LABEL,
  name: 'Name', discordUrl: 'Discord webhook URL', hookUrl: 'Webhook URL', hookBearer: 'Bearer token', sender: 'Sender', recipients: 'Recipients', smtpServer: 'Server',
  smtpPort: 'Port', smtpAuth: 'Authentication', apiKey: 'API key', mailgunDomain: 'Sending domain', baseUrl: 'API base URL', templates: 'Templates', size: 'Size',
};

const EMAIL = /^[\x21-\x7e]+@[\x21-\x7e]+\.[\x21-\x7e]+$/u;
const HOST = /^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.?$|^[0-9a-fA-F:.]+$/u;
const DISCORD = /^https:\/\/(?:(?:canary|ptb)\.)?discord(?:app)?\.com\/api\/(?:v\d+\/)?webhooks\/\d+\/[\w-]+\/?$/u;

/** Config keys this form models; anything else from a newer node is carried through unchanged. */
const MODELED_CONFIG = new Set(['kind', 'sender', 'recipients', 'smtp', 'mailgun', 'brevo', 'mandrill', 'webhook', 'slack', 'telegram', 'ntfy', 'gotify', 'pushover', 'templates']);
/** Channel keys this form writes. Other stored keys belong to the channel, not its provider, so they survive a provider change. */
const CHANNEL_KEYS = ['name', 'enabled', 'config', 'isDefault'];

/**
 * `level` is the selected node's template grammar (alertTemplates.ts templateCapability). Peer protocol 20 applies templates
 * to Discord and adds the card layout; for an older or unknown node the form writes neither the layout nor newly typed
 * protocol 20 placeholders, and keeps what the node already stores.
 */
function buildChannel(draft: ChannelDraft, original: NotificationChannel | undefined, level: TemplateLevel = 'legacy'): { input?: NotificationChannelInput; errors: Partial<Record<CField, string>> } {
  const errors: Partial<Record<CField, string>> = {};
  const name = draft.name.trim();
  if (!name || name.length > 128 || control(name)) errors.name = 'Enter a name of at most 128 characters.';
  const kind = draft.kind;
  const same = original?.config.kind === kind ? original.config : undefined;
  const config: NotificationConfig = { kind };
  if (same) {
    for (const [key, entry] of Object.entries(same)) if (!MODELED_CONFIG.has(key)) (config as unknown as Record<string, unknown>)[key] = structuredClone(entry);
  }
  const email = PROVIDER.get(kind)!.email;
  if (email) {
    const sender = draft.senderEmail.trim();
    if (!EMAIL.test(sender)) errors.sender = 'Enter the sender’s email address, like alerts@example.com.';
    else if (draft.senderName.length > 128 || control(draft.senderName)) errors.sender = 'Use a sender name of at most 128 characters.';
    config.sender = { email: sender, ...(draft.senderName.trim() ? { name: draft.senderName.trim() } : {}) };
    const recipients: EmailAddress[] = [];
    const seen = new Set<string>();
    for (const row of draft.recipients) {
      const address = row.email.trim();
      if (!address && !row.name.trim()) continue;
      if (!EMAIL.test(address)) { errors.recipients = `“${address || '(empty)'}” isn’t an email address.`; continue; }
      if (seen.has(address.toLowerCase())) { errors.recipients = `${address} is listed twice.`; continue; }
      if (row.name.length > 128 || control(row.name)) { errors.recipients = 'Recipient names are at most 128 characters.'; continue; }
      seen.add(address.toLowerCase());
      recipients.push({ email: address, ...(row.name.trim() ? { name: row.name.trim() } : {}) });
    }
    if (!recipients.length && !errors.recipients) errors.recipients = 'Add at least one recipient.';
    if (recipients.length > 50) errors.recipients = 'Use at most 50 recipients.';
    config.recipients = recipients;
  }
  const apiKey = (value: string): string => {
    if (!value) errors.apiKey = 'Enter the API key.';
    else if (value.trim() !== value || control(value)) errors.apiKey = 'Remove spaces or line breaks around the key.';
    return value;
  };
  switch (kind) {
    case 'discord': {
      const url = draft.discordUrl.trim();
      if (!DISCORD.test(url)) errors.discordUrl = 'Paste the webhook URL from Discord’s channel settings: https://discord.com/api/webhooks/…';
      config.webhook = { ...(same?.webhook ?? {}), url };
      delete config.webhook.bearerToken; delete config.webhook.allowInsecureHTTP;
      // Written only when chosen, as Go's omitempty stores it, so an unchanged channel saves as before.
      if (level === 'enhanced') {
        if (draft.discordEmbed) config.webhook.embed = true; else delete config.webhook.embed;
      }
      break;
    }
    case 'webhook': {
      const problem = urlProblem(draft.hookUrl, draft.hookInsecure, 'webhook URL', true);
      if (problem) errors.hookUrl = problem;
      if (control(draft.hookBearer) || draft.hookBearer.length > 4096) errors.hookBearer = 'Use at most 4096 characters on one line.';
      config.webhook = { ...(same?.webhook ?? {}), url: draft.hookUrl.trim() };
      if (draft.hookBearer) config.webhook.bearerToken = draft.hookBearer; else delete config.webhook.bearerToken;
      if (draft.hookInsecure) config.webhook.allowInsecureHTTP = true; else delete config.webhook.allowInsecureHTTP;
      break;
    }
    case 'smtp': {
      const server = draft.smtpServer.trim();
      if (!server || !HOST.test(server)) errors.smtpServer = 'Enter the mail server’s host name or IP address.';
      const port = draft.smtpPort.trim() ? Number(draft.smtpPort) : draft.smtpTls === 'implicit' ? 465 : draft.smtpTls === 'plain' ? 25 : 587;
      if (!Number.isInteger(port) || port < 1 || port > 65535) errors.smtpPort = 'Use a port from 1 to 65535.';
      if (Boolean(draft.smtpUser) !== Boolean(draft.smtpPass)) errors.smtpAuth = 'Enter both a user name and a password, or neither.';
      if (draft.smtpTls === 'plain' && (draft.smtpUser || draft.smtpPass)) errors.smtpAuth = 'Plain SMTP can’t send credentials. Use STARTTLS or implicit TLS to log in.';
      if (control(draft.smtpUser) || control(draft.smtpPass)) errors.smtpAuth = 'Remove line breaks from the credentials.';
      config.smtp = { ...(same?.smtp ?? {}), server, port, tlsMode: draft.smtpTls };
      if (draft.smtpUser) { config.smtp.username = draft.smtpUser; config.smtp.password = draft.smtpPass; } else { delete config.smtp.username; delete config.smtp.password; }
      break;
    }
    case 'mailgun': {
      const domain = draft.mailgunDomain.trim();
      if (!domain || !HOST.test(domain)) errors.mailgunDomain = 'Enter your Mailgun sending domain, like mg.example.com.';
      const base = urlProblem(draft.mailgunBase, draft.mailgunInsecure, 'API base URL', false);
      if (base) errors.baseUrl = base;
      config.mailgun = { ...(same?.mailgun ?? {}), apiKey: apiKey(draft.mailgunKey), domain, region: draft.mailgunRegion };
      // Nodes accept an absent region as US, but the raw value is part of each recipient's destination identity:
      // writing "US" over an absent region would make every recipient a new destination.
      if (draft.mailgunRegion === 'US' && !same?.mailgun?.region) delete config.mailgun.region;
      if (draft.mailgunBase.trim()) config.mailgun.baseURL = draft.mailgunBase.trim(); else delete config.mailgun.baseURL;
      if (draft.mailgunInsecure) config.mailgun.allowInsecureHTTP = true; else delete config.mailgun.allowInsecureHTTP;
      break;
    }
    case 'brevo': case 'mandrill': {
      const key = kind === 'brevo' ? draft.brevoKey : draft.mandrillKey;
      const baseText = kind === 'brevo' ? draft.brevoBase : draft.mandrillBase;
      const insecure = kind === 'brevo' ? draft.brevoInsecure : draft.mandrillInsecure;
      const base = urlProblem(baseText, insecure, 'API base URL', false);
      if (base) errors.baseUrl = base;
      const options = { ...((kind === 'brevo' ? same?.brevo : same?.mandrill) ?? {}), apiKey: apiKey(key) } as NonNullable<NotificationConfig['brevo']>;
      if (baseText.trim()) options.baseURL = baseText.trim(); else delete options.baseURL;
      if (insecure) options.allowInsecureHTTP = true; else delete options.allowInsecureHTTP;
      if (kind === 'brevo') config.brevo = options; else config.mandrill = options;
      break;
    }
    default:
      if (isMessaging(kind)) buildMessaging(kind, draft, same, config, errors);
  }
  // Chat and push providers, and Discord from peer protocol 20, take a title (stored as the subject template) and plain
  // text, never HTML. Older nodes don't use templates for Discord, so whatever it stores is kept there.
  const discordText = kind === 'discord' && level === 'enhanced';
  const messaging = isMessaging(kind) || discordText;
  const templates: NonNullable<NotificationConfig['templates']> = { ...(same?.templates ?? {}) };
  delete templates.subject; delete templates.text; delete templates.html;
  if (draft.subject) templates.subject = draft.subject;
  if (draft.text) templates.text = draft.text;
  if (draft.html && !messaging) templates.html = draft.html;
  if (bytesOf(draft.subject) > 512 || control(draft.subject)) errors.templates = `The ${messaging ? 'title' : 'subject'} is at most 512 bytes on one line.`;
  else if (bytesOf(draft.text) > 65_536 || !messaging && bytesOf(draft.html) > 65_536) errors.templates = 'Text and HTML templates are at most 64 KiB each.';
  else if (isMessaging(kind)) {
    // The added placeholders are values, not fixed text, where the node fills them in.
    const problem = level === 'enhanced' ? templateProblem(kind, withoutAddedPlaceholders(draft.subject), withoutAddedPlaceholders(draft.text)) : templateProblem(kind, draft.subject, draft.text);
    if (problem) errors.templates = problem;
  } else if (discordText) {
    const problem = discordTemplateProblem(draft.subject, draft.text, draft.discordEmbed);
    if (problem) errors.templates = problem;
  }
  const later = errors.templates ? [] : unsupportedPlaceholders({ subject: draft.subject, text: draft.text, html: messaging ? '' : draft.html }, original?.config.templates, level);
  if (later.length) {
    const names = later.map(name => `{{${name}}}`).join(', ');
    errors.templates = `${names} ${later.length === 1 ? 'is' : 'are'} filled in only by nodes on peer protocol ${TEMPLATE_PROTOCOL} or later; this node would send ${later.length === 1 ? 'it' : 'them'} as typed. Remove ${later.length === 1 ? 'it' : 'them'}, or upgrade the node first.`;
  }
  if (Object.keys(templates).length) config.templates = templates;
  if (Object.keys(errors).length) return { errors };
  // `isDefault` is written only when on, as Go's omitempty encoding stores it, so an unchanged channel saves as before.
  const input: NotificationChannelInput = { name, enabled: draft.enabled, config, ...(draft.isDefault ? { isDefault: true } : {}), ...storedExtras(original, CHANNEL_KEYS) };
  if (jsonBytes(input) > 250_000) return { errors: { size: 'This channel is larger than nodes accept. Shorten the templates.' } };
  return { input, errors };
}

/**
 * The form model, for scripts/frontend-review-pure.mjs. The functions keep their own names unexported because the
 * contract harness appends `export { channelDraft, buildChannel }` to its review copy of this file.
 */
export const channelFormModel = { channelDraft, buildChannel };

/**
 * The edit form mounts only once the stored channel is known, so its draft, disclosures and base revision come from
 * that channel. A link opened before the channel list loaded shows a loading note and then mounts fresh.
 */
export function ChannelFormView({ id }: { id?: string }) {
  const { channels } = useCanary();
  const existing = id ? channels.map.get(id) : undefined;
  return <ChannelFormBody key={existing ? `edit-${existing.channel.id}` : id ? 'missing' : 'new'} id={id} />;
}

function ChannelFormBody({ id }: { id?: string }) {
  const { channels, endpoint, editable, api, overview, stillCurrent } = useCanary();
  const existing = id ? channels.map.get(id) : undefined;
  const [draft, setDraft] = useState<ChannelDraft>(() => channelDraft(existing?.channel));
  const [initial] = useState<ChannelDraft>(() => channelDraft(existing?.channel));
  const [base, setBase] = useState<NotificationChannel | undefined>(existing?.channel);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<{ message: string; stale: boolean } | null>(null);
  const [attempts, setAttempts] = useState(0);
  const [templatesOpen, setTemplatesOpen] = useState(Boolean(initial.subject || initial.text || initial.html || initial.discordEmbed));
  const [apiOpen, setApiOpen] = useState(Boolean(initial.mailgunBase || initial.brevoBase || initial.mandrillBase || initial.tgBase || initial.poBase));
  const [previewing, setPreviewing] = useState<NotificationChannelInput | null>(null);
  const summaryRef = useRef<HTMLDivElement>(null);
  const formId = useId();
  // The stored version as a draft, to tell a new destination from a credential rotation.
  const previous = useMemo(() => base ? channelDraft(base) : undefined, [base]);
  useEffect(() => { if (attempts) summaryRef.current?.focus(); }, [attempts, failure]);

  if (!editable) return <div className="view"><PageHead title={id ? 'Edit channel' : 'New channel'} /><Card><EmptyState title="Viewers can’t change channels">Sign in with an editor or admin account.</EmptyState></Card></div>;
  if (channels.unsupported) return <div className="view"><PageHead title="New channel" /><Card><EmptyState title={`${endpoint} doesn’t offer reusable channels`}>It probably runs an older Uptime-Canary version.</EmptyState></Card></div>;
  if (id && !existing) {
    return <div className="view"><PageHead crumbs={<a href={href('notifications')}>Notifications</a>} title="Edit channel" /><Card>{channels.list ? <EmptyState title="Channel not found">{endpoint} doesn’t know this channel. It may have been deleted.</EmptyState> : <p className="muted">Loading the channel from {endpoint}…</p>}</Card></div>;
  }

  // What the selected node renders: its overview's reported peer protocol, or the six-placeholder grammar when it reports none.
  const capability = templateCapability(overview.build);
  const { input, errors } = buildChannel(draft, base, capability.level);
  const visible = showErrors ? errors : {};
  const set = <K extends keyof ChannelDraft>(key: K, value: ChannelDraft[K]): void => setDraft(d => ({ ...d, [key]: value }));
  const patch = (change: Partial<ChannelDraft>): void => setDraft(d => ({ ...d, ...change }));
  const provider = PROVIDER.get(draft.kind)!;
  const current = existing?.channel;
  const changedElsewhere = Boolean(current && base && current.revision !== base.revision);
  const conflicted = Boolean(existing?.configConflict);
  const users = current ? overview.monitors.filter(view => view.monitor.notificationIds?.includes(current.id)).length : 0;
  // Secrets stored in the revision being edited (after "Load latest" too), for the "Saving removes" warnings.
  const stored = (key: keyof ChannelDraft): boolean => Boolean(current && base?.config.kind === draft.kind && previous?.[key]);

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (saving) return;
    setShowErrors(true); setFailure(null);
    if (!input) {
      if (errors.templates) setTemplatesOpen(true);
      if (errors.baseUrl) setApiOpen(true);
      setAttempts(n => n + 1);
      return;
    }
    setSaving(true);
    try {
      const saved = current && base ? await api.updateChannel(current.id, { ...input, revision: base.revision }) : await api.createChannel(input);
      if (!stillCurrent()) return;
      notify(`${current ? 'Saved' : 'Created'} channel “${saved.name}” on ${endpoint} (revision ${shortRev(saved.revision)}).${current ? '' : ' Pick it in a monitor’s alerts to use it.'}`);
      channels.reload();
      navigate(href('notifications'));
    } catch (error) {
      if (!stillCurrent()) return;
      const stale = error instanceof ApiFailure && error.status === 409;
      setFailure({ message: stale ? 'Someone saved a newer revision first, or nodes now hold conflicting versions.' : describeFailure(error, { preferDetail: true }), stale });
      setAttempts(n => n + 1);
    } finally { setSaving(false); }
  };
  const loadLatest = (): void => { if (current) { setDraft(channelDraft(current)); setBase(current); setFailure(null); setShowErrors(false); } };
  // A test of the form as it is: only complete, valid settings are sent, and the dialog holds them in memory alone.
  const testSettings = (): void => {
    setShowErrors(true); setFailure(null);
    if (!input) {
      if (errors.templates) setTemplatesOpen(true);
      if (errors.baseUrl) setApiOpen(true);
      setAttempts(n => n + 1);
      return;
    }
    setPreviewing(input);
  };
  const errorList = Object.entries(visible).map(([field, message]) => `${CFIELD_LABEL[field as CField]}: ${message}`);
  const updateRecipient = (key: number, change: Partial<Recipient>): void => set('recipients', draft.recipients.map(row => row.key === key ? { ...row, ...change } : row));

  return (
    <div className="view">
      <PageHead
        crumbs={<a href={href('notifications')}>Notifications</a>}
        title={current ? `Edit ${current.name}` : 'New channel'}
        sub={current ? <>Editing revision <Rev revision={base?.revision ?? current.revision} />{users ? ` · used by ${plural(users, 'monitor')}` : ''}. Saving writes a new revision on <span className="mono">{endpoint}</span>.</> : <>Creates a reusable channel on <span className="mono">{endpoint}</span>, shared with every peer.</>}
      />
      {conflicted ? <Banner title="This channel has a configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Resolve in Configuration</a>}>Edits are rejected until the conflict is resolved.</Banner>
        : changedElsewhere ? <Banner title={<>This channel changed on {endpoint} while you were editing</>} actions={<button type="button" className="btn sm" onClick={loadLatest}><Icon name="refresh" />Load latest (discards your edits)</button>}>Saving your version will be rejected because it’s based on an older revision.</Banner>
        : null}
      <Card>
        <form className="form" onSubmit={event => { void submit(event); }} noValidate aria-busy={saving}>
          <ErrorSummary ref={summaryRef} title={failure ? (failure.stale ? 'Not saved: the channel changed or has a conflict' : 'Not saved') : 'Check these fields'} errors={failure ? [] : errorList}>
            {failure ? <p>{failure.message}{failure.stale && current ? <> <button type="button" className="linkbtn" onClick={loadLatest}>Load the latest version</button> and apply your change again.</> : null}</p> : null}
          </ErrorSummary>
          <h3 className="fsec full">Channel</h3>
          <TextField label="Name" value={draft.name} onChange={value => set('name', value)} error={visible.name} hint="Shown when picking channels for monitors" maxLength={128} autoFocus={!current} disabled={saving} />
          <CheckField full={false} label="Enabled" checked={draft.enabled} onChange={value => set('enabled', value)} disabled={saving} hint="Disabled channels receive nothing; owed alerts resume if you enable it during an outage." />
          <CheckField full={false} label="Default for new monitors" checked={draft.isDefault} onChange={value => set('isDefault', value)} disabled={saving}
            hint={draft.isDefault && !draft.enabled ? 'Only while enabled: a disabled default isn’t preselected.' : 'Preselected on every new monitor while enabled; each one can still clear it. Existing monitors change only when you add it to them from the channel list.'} />
          <fieldset className="fld full types-f" disabled={saving}>
            <legend>Provider</legend>
            {PROVIDER_GROUPS.map(group => (
              <div key={group.key} className="type-cat">
                <span className="type-cat-h" aria-hidden="true">{group.label}</span>
                <div className="types">
                  {PROVIDERS.filter(entry => entry.group === group.key).map(entry => (
                    <label key={entry.kind}>
                      <input type="radio" name={`${formId}-kind`} value={entry.kind} checked={draft.kind === entry.kind} onChange={() => set('kind', entry.kind)} />
                      <span><b><ProviderIcon kind={entry.kind} />{entry.label}</b><small>{entry.hint}</small></span>
                    </label>
                  ))}
                </div>
              </div>
            ))}
            {current && current.config.kind !== draft.kind ? <small className="fwarn"><Glyph tone="warn" />A different provider is a new destination. Alerts already owed to the old one aren’t moved.</small> : null}
          </fieldset>

          <h3 className="fsec full">{provider.email ? 'Mail service' : 'Destination'}</h3>
          {draft.kind === 'discord' ? (
            <SecretField full label="Discord webhook URL" value={draft.discordUrl} onChange={value => set('discordUrl', value)} error={visible.discordUrl} disabled={saving} storedSet={stored('discordUrl')}
              placeholder="https://discord.com/api/webhooks/…" hint="Channel settings → Integrations → Webhooks. The URL is the credential: stored on nodes, hidden from viewers. Delivery waits for Discord to save the message." />
          ) : null}
          {draft.kind === 'webhook' ? (
            <>
              <SecretField full label="Webhook URL" value={draft.hookUrl} onChange={value => set('hookUrl', value)} error={visible.hookUrl} disabled={saving} storedSet={stored('hookUrl')}
                placeholder="https://hooks.example.com/uptime" hint="Receives a JSON POST with an Idempotency-Key header. Any 2xx is acceptance. No query string." />
              <SecretField full optional label="Bearer token" value={draft.hookBearer} onChange={value => set('hookBearer', value)} error={visible.hookBearer} disabled={saving} storedSet={stored('hookBearer')}
                hint="Sent as Authorization: Bearer, separate from the URL." />
              <CheckField label="Allow plain HTTP" checked={draft.hookInsecure} onChange={value => set('hookInsecure', value)} disabled={saving} danger hint="Only for a private relay or test receiver. The message and token travel unencrypted." />
            </>
          ) : null}
          {draft.kind === 'smtp' ? (
            <>
              <TextField mono label="Server" value={draft.smtpServer} onChange={value => set('smtpServer', value)} error={visible.smtpServer} placeholder="smtp.example.com" disabled={saving} />
              <TextField mono label="Port (optional)" value={draft.smtpPort} onChange={value => set('smtpPort', value)} error={visible.smtpPort} inputMode="numeric" disabled={saving}
                placeholder={draft.smtpTls === 'implicit' ? '465' : draft.smtpTls === 'plain' ? '25' : '587'} hint="Empty uses the usual port for the mode." />
              <Seg legend="Security" name={`${formId}-smtp`} value={draft.smtpTls} onChange={value => set('smtpTls', value)} disabled={saving} full
                options={[{ value: 'starttls', label: 'STARTTLS' }, { value: 'implicit', label: 'Implicit TLS' }, { value: 'plain', label: 'Plain relay' }]}
                hint={draft.smtpTls === 'plain' ? 'Unencrypted and without login, for a trusted local relay only.' : 'Certificates and host name are verified (TLS 1.2+), with no plaintext fallback.'} />
              {draft.smtpTls !== 'plain' ? (
                <>
                  <TextField label="User name (optional)" value={draft.smtpUser} onChange={value => set('smtpUser', value)} disabled={saving} autoComplete="off" />
                  <SecretField optional label="Password" value={draft.smtpPass} onChange={value => set('smtpPass', value)} disabled={saving} storedSet={stored('smtpPass')} hint="AUTH PLAIN over TLS." />
                </>
              ) : null}
              {visible.smtpAuth ? <div className="full"><ErrorText id={`${formId}-smtpauth`}>{visible.smtpAuth}</ErrorText></div> : null}
            </>
          ) : null}
          {draft.kind === 'mailgun' ? (
            <>
              <SecretField label="API key" value={draft.mailgunKey} onChange={value => set('mailgunKey', value)} error={visible.apiKey} disabled={saving} storedSet={stored('mailgunKey')} />
              <TextField mono label="Sending domain" value={draft.mailgunDomain} onChange={value => set('mailgunDomain', value)} error={visible.mailgunDomain} placeholder="mg.example.com" disabled={saving} />
              <Seg legend="Region" name={`${formId}-region`} value={draft.mailgunRegion} onChange={value => set('mailgunRegion', value)} disabled={saving} full
                options={[{ value: 'US', label: 'US (api.mailgun.net)' }, { value: 'EU', label: 'EU (api.eu.mailgun.net)' }]} hint="Use the region your Mailgun domain was created in." />
            </>
          ) : null}
          {draft.kind === 'brevo' ? <SecretField full label="API key" value={draft.brevoKey} onChange={value => set('brevoKey', value)} error={visible.apiKey} disabled={saving} storedSet={stored('brevoKey')} hint="A Brevo API key (v3) for transactional email." /> : null}
          {draft.kind === 'mandrill' ? <SecretField full label="API key" value={draft.mandrillKey} onChange={value => set('mandrillKey', value)} error={visible.apiKey} disabled={saving} storedSet={stored('mandrillKey')} hint="A Mailchimp Transactional (Mandrill) key, not a Marketing API key. Each recipient is submitted separately." /> : null}
          {draft.kind === 'mailgun' || draft.kind === 'brevo' || draft.kind === 'mandrill' ? (
            <Disclosure title="API endpoint" summary={(draft.kind === 'mailgun' ? draft.mailgunBase : draft.kind === 'brevo' ? draft.brevoBase : draft.mandrillBase) || 'Provider default'} open={apiOpen} onToggle={() => setApiOpen(v => !v)}>
              <TextField full mono label="API base URL (optional)" disabled={saving} error={visible.baseUrl}
                value={draft.kind === 'mailgun' ? draft.mailgunBase : draft.kind === 'brevo' ? draft.brevoBase : draft.mandrillBase}
                onChange={value => set(draft.kind === 'mailgun' ? 'mailgunBase' : draft.kind === 'brevo' ? 'brevoBase' : 'mandrillBase', value)}
                placeholder="Provider default" hint="Only for a relay or test server. Versioned API paths are appended." />
              <CheckField label="Allow plain HTTP for this base URL" disabled={saving} danger
                checked={draft.kind === 'mailgun' ? draft.mailgunInsecure : draft.kind === 'brevo' ? draft.brevoInsecure : draft.mandrillInsecure}
                onChange={value => set(draft.kind === 'mailgun' ? 'mailgunInsecure' : draft.kind === 'brevo' ? 'brevoInsecure' : 'mandrillInsecure', value)}
                hint="The API key travels unencrypted. Private test servers only." />
            </Disclosure>
          ) : null}
          {isMessaging(draft.kind) ? (
            <MessagingFields kind={draft.kind} draft={draft} patch={patch} errors={visible} stored={stored} saving={saving} formId={formId}
              apiOpen={apiOpen} onApiToggle={() => setApiOpen(v => !v)} previous={current && base?.config.kind === draft.kind ? previous : undefined} />
          ) : null}

          {provider.email ? (
            <>
              <h3 className="fsec full">Addresses</h3>
              <TextField mono label="From address" value={draft.senderEmail} onChange={value => set('senderEmail', value)} error={visible.sender} placeholder="alerts@example.com" disabled={saving}
                hint="Must be a sender or domain your provider has verified." />
              <TextField label="From name (optional)" value={draft.senderName} onChange={value => set('senderName', value)} placeholder="Uptime Canary" maxLength={128} disabled={saving} />
              <fieldset className="fld full hdr-f" disabled={saving} aria-invalid={visible.recipients ? true : undefined}>
                <legend>Recipients <span className="muted">({draft.recipients.filter(row => row.email.trim()).length} of at most 50)</span></legend>
                <div className="hdr-rows">
                  {draft.recipients.map((row, index) => (
                    <div className="hdr-row" key={row.key}>
                      <input type="email" className="mono" value={row.email} placeholder="oncall@example.com" aria-label={`Recipient ${index + 1} email`} autoComplete="off" spellCheck={false} onChange={event => updateRecipient(row.key, { email: event.target.value })} />
                      <input type="text" value={row.name} placeholder="name (optional)" aria-label={`Recipient ${index + 1} name`} autoComplete="off" onChange={event => updateRecipient(row.key, { name: event.target.value })} />
                      <button type="button" className="icon-btn sm" onClick={() => set('recipients', draft.recipients.filter(other => other.key !== row.key))} aria-label={`Remove recipient ${row.email || index + 1}`} disabled={draft.recipients.length === 1}><Icon name="close" /></button>
                    </div>
                  ))}
                </div>
                <div className="row-wrap pick-acts">
                  <button type="button" className="linkbtn" onClick={() => set('recipients', [...draft.recipients, { key: ++recipientKey, email: '', name: '' }])} disabled={draft.recipients.length >= 50}><Icon name="plus" />Add recipient</button>
                </div>
                <small>Each recipient is a separate destination with its own retries, outcome and receipt. Adding one later doesn’t replay finished outages to it.</small>
                <ErrorText id={`${formId}-rcpt`}>{visible.recipients}</ErrorText>
              </fieldset>
            </>
          ) : null}

          <Disclosure title="Message templates" summary={templateSummary(draft.kind, draft, capability.level, draft.discordEmbed)} open={templatesOpen} onToggle={() => setTemplatesOpen(v => !v)}>
            <MessageTemplates kind={draft.kind} providerLabel={provider.label} icon={<ProviderIcon kind={draft.kind} />} channelName={draft.name}
              fields={{ subject: draft.subject, text: draft.text, html: draft.html }} onChange={patch} saving={saving} error={visible.templates} formId={formId}
              capability={capability} nodeName={endpoint} embed={draft.discordEmbed} onEmbed={value => set('discordEmbed', value)} />
          </Disclosure>

          {visible.size ? <div className="full"><ErrorText id={`${formId}-size`}>{visible.size}</ErrorText></div> : null}
          <div className="full form-acts">
            <button type="submit" className="btn primary" disabled={saving || conflicted}><Icon name={current ? 'check' : 'plus'} />{saving ? `Saving to ${endpoint}…` : current ? 'Save changes' : 'Create channel'}</button>
            <button type="button" className="btn" disabled={saving} onClick={testSettings}><Icon name="send" />Test these settings</button>
            <a className="btn" href={href('notifications')}>Cancel</a>
            {saving ? <span className="muted small" role="status">Waiting for {endpoint} to acknowledge…</span> : <span className="muted small">Saving sends nothing. Test these settings sends one message with what’s in the form, without saving it.</span>}
          </div>
        </form>
      </Card>
      {previewing ? <PreviewTestDialog input={previewing} onClose={() => setPreviewing(null)} /> : null}
    </div>
  );
}
