import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react';
import type { DeliveryAttemptView, EmailAddress, NotificationChannel, NotificationChannelInput, NotificationConfig, NotificationProvider } from '@uptime-canary/shared';
import { discordTemplateProblem, TEMPLATE_PROTOCOL, templateCapability, templateSummary, unsupportedPlaceholders, withoutAddedPlaceholders, type TemplateLevel } from '../alertTemplates';
import { ApiFailure, describeFailure, isAbort, jsonBytes } from '../api';
import { useCanary } from '../context';
import { ago, andList, bytesOf, maskedUrl, plural, shortId, shortRev, span } from '../format';
import { useResource } from '../hooks';
import { automationDestination, buildMessaging, control, isMessaging, messagingDraft, messagingRoute, MFIELD_LABEL, templateProblem, urlProblem, type MessagingDraft, type MField } from '../messaging';
import { notify } from '../notices';
import { deliveryHref, href, navigate, registerNavigationGuard } from '../router';
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
  { kind: 'matrix', label: 'Matrix', hint: 'Notice in an unencrypted room', icon: 'chat', email: false, group: 'push' },
  { kind: 'smtp', label: 'SMTP', hint: 'Your own mail server', icon: 'mail', email: true, group: 'email' },
  { kind: 'mailgun', label: 'Mailgun', hint: 'US or EU API', icon: 'mail', email: true, group: 'email' },
  { kind: 'brevo', label: 'Brevo', hint: 'Transactional email API', icon: 'mail', email: true, group: 'email' },
  { kind: 'mandrill', label: 'Mailchimp Transactional', hint: 'Mandrill API, not Marketing', icon: 'mail', email: true, group: 'email' },
  { kind: 'webhook', label: 'Webhook', hint: 'JSON POST to your endpoint', icon: 'hook', email: false, group: 'custom' },
  { kind: 'homeassistant', label: 'Home Assistant', hint: 'Event that triggers automations', icon: 'plug', email: false, group: 'custom' },
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
  event_filtered: 'The channel’s alert events leave out this kind of alert, so it was cancelled without sending.',
};
export const errorText = (code: string | undefined): string | undefined => code ? ERROR_TEXT[code] ?? code.replace(/_/gu, ' ') : undefined;
const KIND_LABEL: Record<DeliveryAttemptView['kind'], string> = { down: 'Down alert', repeat: 'Repeat', recovery: 'Recovery', test: 'Test' };

// ---------- alert events (peer protocol 22)

/** The peer protocol that adds per-channel alert events and the Home Assistant and Matrix providers (buildChannel's gate). */
const EVENTS_PROTOCOL = 22;
const PROTOCOL_22_KINDS: ReadonlySet<string> = new Set(['homeassistant', 'matrix']);
type AlertEvent = 'down' | 'repeat' | 'recovery';
const EVENT_ORDER: readonly AlertEvent[] = ['down', 'repeat', 'recovery'];
const EVENT_CHOICE: Record<AlertEvent, { label: string; hint: string; noun: string }> = {
  down: { label: 'Down alerts', hint: 'When a monitor goes down.', noun: 'down alerts' },
  repeat: { label: 'Repeats', hint: 'Reminders while the monitor stays down. Needs down alerts.', noun: 'repeats' },
  recovery: { label: 'Recoveries', hint: 'When it’s back up.', noun: 'recoveries' },
};
/** UI dependencies are applied immediately; the API builder still rejects invalid explicit selections. */
export function toggleAlertEvent(events: readonly AlertEvent[], event: AlertEvent, on: boolean): AlertEvent[] {
  const next = new Set(events);
  if (on) next.add(event); else next.delete(event);
  if (!next.has('down')) next.delete('repeat');
  return EVENT_ORDER.filter(item => next.has(item));
}

/** A stored selection that isn't all three, in words; nothing for the default. */
export const eventsSummary = (events: readonly string[] | undefined): string | undefined => {
  if (!events) return undefined;
  const known = EVENT_ORDER.filter(event => events.includes(event));
  if (known.length === EVENT_ORDER.length) return undefined;
  return known.length ? `${andList(known.map(event => EVENT_CHOICE[event].noun))} only` : 'no alert events';
};

/** A Home Assistant test is a real event: automations that trigger on it run unless they check the kind. */
function HaTestWarning({ eventType }: { eventType: string | undefined }) {
  return <p className="fwarn"><Glyph tone="warn" /><span>A real <span className="mono">{eventType || 'uptime_canary'}</span> test event can switch real devices. Check <span className="mono">kind=test</span> before outage actions. Duplicates are possible: use repeat-safe actions and deduplicate with <span className="mono">idempotency_key</span>.</span></p>;
}

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
function AttemptWhy({ attempt, explainAcceptance = true }: { attempt: DeliveryAttemptView; explainAcceptance?: boolean }) {
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
  if (explainAcceptance && attempt.state === 'accepted') parts.push('The provider acknowledged it. That isn’t proof of inbox, channel or device delivery.');
  if (!parts.length) return null;
  return <div className="why">{parts.map((part, index) => <span key={index}>{part}</span>)}</div>;
}

export function DeliveryTable({ attempts, compact, recipientLabels, explainAcceptance = true }: { attempts: DeliveryAttemptView[]; compact?: boolean; recipientLabels?: ReadonlyMap<string, string>; explainAcceptance?: boolean }) {
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
            const state = attemptState(attempt.state), recipient = recipientLabels?.get(attempt.id);
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
                  <span className="attempt-destination"><ProviderIcon kind={attempt.provider} /><span>{recipient ?? (channel ? <a href={href('channels', channel.id, 'edit')}>{channel.name}</a> : attempt.channelId ? <span className="muted">channel {shortId(attempt.channelId)}</span> : providerLabel(attempt.provider))}</span></span>
                  {attempt.destinationId ? <div className="muted small">destination <span className="mono" title={attempt.destinationId}>{shortId(attempt.destinationId)}</span></div> : null}
                </td>
                <td data-label="Outcome" className="full">
                  <span className="nowrap"><Chip tone={state.tone} label={state.label} />{attempt.httpStatus ? <span className="muted small"> HTTP {attempt.httpStatus}</span> : null}</span>
                  {attempt.nextAttemptAt ? <div className="muted small">next try <Time at={attempt.nextAttemptAt} now={overview.now} mode="short" /></div> : null}
                  {attempt.providerId ? <div className="muted small">acknowledgement <span className="mono" title="SHA-256 of the provider’s ID">{attempt.providerId.slice(0, 10)}…</span></div> : null}
                  <AttemptWhy attempt={attempt} explainAcceptance={explainAcceptance} />
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

export function DeliveriesView({ channelFilter }: { channelFilter?: string }) {
  const { api, endpoint, search, monitorMap, channels, connection, role, overview } = useCanary();
  const [limit, setLimit] = useState(200);
  const [filter, setFilter] = useState<AttemptFilter>('all');
  const channelId = channelFilter ?? '';
  const privateAttempts = role !== 'viewer' && overview.user.role !== 'viewer';
  const attempts = useResource<DeliveryAttemptView[]>(`${connection.identity}|deliveries|${privateAttempts}|${limit}`, signal => privateAttempts ? api.deliveries({ limit }, signal) : Promise.resolve([]), privateAttempts ? 15_000 : undefined);
  const q = search.trim().toLowerCase();
  const all = (attempts.data ?? []).filter(attempt => (!channelId || attempt.channelId === channelId)
    && (!q || `${monitorMap.get(attempt.monitorId ?? '')?.monitor.name ?? ''} ${channels.map.get(attempt.channelId ?? '')?.channel.name ?? ''} ${attempt.provider} ${attempt.nodeId} ${attempt.errorCode ?? ''} ${attempt.incidentId ?? ''}`.toLowerCase().includes(q)));
  const keep = (attempt: DeliveryAttemptView, f: AttemptFilter): boolean => f === 'all' || (f === 'problems' ? PROBLEM.has(attempt.state) : f === 'accepted' ? attempt.state === 'accepted' : f === 'held' ? attempt.state === 'held' : attempt.kind === 'test');
  const rows = all.filter(attempt => keep(attempt, filter));
  const unsupported = attempts.error instanceof ApiFailure && attempts.error.status === 404;
  if (!privateAttempts) return <div className="view"><PageHead title="Notifications" /><NotificationTabs current="deliveries" /><ReadOnly>Viewers see channel names and providers only. Delivery attempts are private.</ReadOnly></div>;
  return (
    <div className="view delivery-view">
      <PageHead title="Notifications" sub={<>Delivery attempts known to <span className="mono">{endpoint}</span>, newest first. Each email recipient, and each chat, push or webhook channel, is its own destination with its own retries.</>} />
      <NotificationTabs current="deliveries" />
      <Card
        title="Delivery attempts"
        hint={attempts.loadedAt ? <>Loaded {ago(attempts.loadedAt, Date.now())} · refreshes every 15 s · this node’s attempts and the receipts it has learned from peers</> : undefined}
        actions={<>
          {(channels.list?.length ?? 0) > 0 ? (
            <label className="inline-sel"><span className="vh">Channel</span>
              <select value={channelId} onChange={event => navigate(deliveryHref(event.target.value))}>
                <option value="">All channels</option>
                {channelId && !channels.map.has(channelId) ? <option value={channelId}>Channel {shortId(channelId)}</option> : null}
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
              {rows.length ? <DeliveryTable attempts={rows} /> : <EmptyState title="No attempts here">{all.length ? 'Nothing matches this filter.' : 'No matching attempts in this recent, capped view. Older attempts may be outside it; this is not delivery-health evidence.'}</EmptyState>}
              {attempts.data.length >= limit && limit < 1000 ? <div className="row-wrap center"><button type="button" className="btn sm" onClick={() => setLimit(1000)}>Load up to 1,000 attempts</button></div> : null}
            </>
          )}
        {attempts.error && attempts.data ? <p className="fwarn" role="alert">Refresh failed. Showing the last loaded attempts; outcomes may have changed.</p> : null}
        <p className="foot-note">
          {channelId ? <>Filtered to <b>{channels.map.get(channelId)?.channel.name ?? `channel ${shortId(channelId)}`}</b>. <a href={deliveryHref()}>Show all channels</a>. </> : null}
          Showing up to {limit} recent attempts known to this node, including learned peer receipts; older attempts may be outside this view.{' '}
          <b>Accepted</b> means the provider acknowledged the request (Discord with a saved message, Slack with “ok”, Telegram, ntfy, Gotify and Pushover with a message or request ID, Matrix with an event ID, Home Assistant when it reports the event fired, email services and SMTP after DATA). It isn’t proof of inbox or device delivery, or that an automation ran.
          Outcomes, error codes and HTTP statuses are fixed values: provider bodies, addresses and secret URLs are never shown. Partitions can cause duplicate sends.
        </p>
      </Card>
    </div>
  );
}

// ---------- channel list

/** Exact public routing fields, shared by saved and unsaved test confirmations. */
function AutomationTestDestination({ config, beforeSend = true }: { config: NotificationConfig; beforeSend?: boolean }) {
  const destination = automationDestination(config);
  if (!destination) return null;
  return (
    <section className="channel-test-destination" aria-label="Test destination">
      <h3>Test destination</h3>
      <dl>
        <div><dt>Server</dt><dd className="mono">{destination.server}</dd></div>
        <div><dt>{destination.targetLabel}</dt><dd className="mono">{destination.target}</dd></div>
      </dl>
      {beforeSend && config.kind === 'matrix' ? <p className="fwarn"><Glyph tone="warn" /><span>The account must have joined this unencrypted room. End-to-end encrypted rooms are unsupported.</span></p> : null}
    </section>
  );
}

/** Only explicit native validation/auth/conflict/throttle rejections prove this submission was not queued.
 * Timeouts (including HTTP 408), unreadable successes and every 5xx are uncertain, even after a proxy replies.
 */
export function requestDisposition(failure: unknown): 'rejected' | 'unconfirmed' {
  return failure instanceof ApiFailure && [400, 401, 403, 404, 409, 413, 422, 429].includes(failure.status) ? 'rejected' : 'unconfirmed';
}

// Kept for the original focused test contract; mutations use the same conservative distinction.
export const testSubmissionDisposition = requestDisposition;

type TestFailure = { message: string; disposition: ReturnType<typeof testSubmissionDisposition> };
function TestSubmissionError({ failure }: { failure: TestFailure }) {
  return <div className="errsum" role="alert"><Glyph tone="down" /><div>
    {failure.disposition === 'unconfirmed' ? null : <b>Test not sent</b>}
    <p>{failure.message}</p>
    {failure.disposition === 'unconfirmed' ? <p>A test may already be queued: real messages or device actions may occur. Check delivery attempts before resending; another test can duplicate them.</p> : <p>Correct the problem before trying again.</p>}
  </div></div>;
}

/** Move focus once when a submitted confirmation becomes a result; polling must not steal it again. */
function useTestResultFocus(phase: string) {
  const anchor = useRef<HTMLParagraphElement>(null);
  useLayoutEffect(() => {
    if (phase === 'confirm') return;
    const heading = anchor.current?.closest('dialog')?.querySelector<HTMLElement>('.modal-h h2');
    if (heading) { heading.tabIndex = -1; heading.focus(); }
  }, [phase]);
  return anchor;
}

function TestDialog({ channel, onClose }: { channel: NotificationChannel; onClose: () => void }) {
  const { api, endpoint, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<DeliveryAttemptView[] | null>(null);
  const [error, setError] = useState<TestFailure | null>(null);
  const focusAnchor = useTestResultFocus(result ? 'submitted' : error?.disposition ?? 'confirm');
  const destinations = destinationCount(channel);
  const emergency = channel.config.kind === 'pushover' && channel.config.pushover?.priority === 2 ? channel.config.pushover : undefined;
  const emergencyWarning = emergency ? <p className="fwarn"><Glyph tone="warn" /><span>Emergency priority: If accepted by Pushover, this test repeats every {span((emergency.retrySeconds ?? 0) * 1000)} until someone acknowledges it, for up to {span((emergency.expireSeconds ?? 0) * 1000)}.</span></p> : null;
  const send = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      const response = await api.testChannel(channel.id, channel.revision);
      if (!stillCurrent()) return;
      setResult(response.attempts);
    } catch (failure) {
      if (!stillCurrent()) return;
      setError({ message: describeFailure(failure, { preferDetail: true }), disposition: testSubmissionDisposition(failure) });
    } finally { setBusy(false); }
  };
  return (
    <Modal title={result ? 'Test queued' : error ? error.disposition === 'unconfirmed' ? 'Test submission unconfirmed' : 'Test request rejected' : `Send a test from ${channel.name}?`} onClose={onClose} busy={busy}>
      <p ref={focusAnchor} className="vh" role="status" aria-live="polite" aria-atomic="true">{result ? `${endpoint} queued ${plural(result.length, 'test attempt')}. Outcomes are available in delivery attempts; queued does not mean delivered.` : ''}</p>
      {result ? (
        <>
          <p><b>{endpoint}</b> queued {plural(result.length, 'test attempt')}. One attempt per destination; no automatic retries.</p>
          {emergencyWarning}
          <p className="muted small">Outcomes appear in Delivery attempts within seconds.{result.some(attempt => attempt.state === 'accepted') ? ' Accepted means the provider acknowledged it; it doesn’t confirm delivery or that an automation ran.' : null}</p>
          <div className="row-wrap modal-acts">
            <a className="btn primary" href={deliveryHref(channel.id)} onClick={onClose}><Icon name="send" />Open delivery attempts</a>
            <button type="button" className="btn" onClick={onClose}>Close</button>
          </div>
        </>
      ) : (
        <>
          {error ? <TestSubmissionError failure={error} /> : null}
          {error?.disposition !== 'unconfirmed' ? <p>
            Sends a <b>real message</b> from <span className="mono">{endpoint}</span> through {providerLabel(channel.config.kind)} to {destinations === 1 ? 'this channel’s destination' : `all ${destinations} recipients`} using saved settings.
            Saves no settings; creates no monitor, incident or receipt.
          </p> : null}
          {emergencyWarning}
          <AutomationTestDestination config={channel.config} beforeSend={error?.disposition !== 'unconfirmed'} />
          {error?.disposition !== 'unconfirmed' && channel.config.kind === 'homeassistant' ? <HaTestWarning eventType={channel.config.homeassistant?.eventType} /> : null}
          {error?.disposition !== 'unconfirmed' ? <p className="muted small">Uses saved revision <Rev revision={channel.revision} />. All tests share this node’s limit: one batch at a time, at least 5 seconds apart.</p> : null}
          <div className="row-wrap modal-acts">
            {error?.disposition === 'unconfirmed' ? <a className="btn primary" href={deliveryHref(channel.id)} onClick={onClose}>View delivery attempts</a>
              : <button type="button" className="btn primary" onClick={() => { void send(); }} disabled={busy || !channel.enabled}><Icon name="send" />{busy ? 'Queuing…' : 'Send test'}</button>}
            <button type="button" className="btn" onClick={onClose} disabled={busy}>{error?.disposition === 'unconfirmed' ? 'Close' : 'Cancel'}</button>
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
/** Read-only inspection stays inside the test dialog, so the editor and its credentials remain mounted. */
function TestAttemptsInspector({ provider, attemptIds, recipientLabels, onBack, onClose }: { provider: Kind; attemptIds?: string[]; recipientLabels?: ReadonlyMap<string, string>; onBack: () => void; onClose: () => void }) {
  const { api, endpoint, connection } = useCanary();
  const region = useRef<HTMLElement>(null);
  const recent = useResource<DeliveryAttemptView[]>(`${connection.identity}|inspect-test|${provider}|${attemptIds?.join(',') ?? 'unconfirmed'}`, signal => api.deliveries({ limit: 200 }, signal), 15_000);
  useLayoutEffect(() => {
    const dialog = region.current?.closest('dialog');
    if (!dialog) return;
    dialog.scrollTop = 0;
    const title = dialog.querySelector<HTMLElement>('.modal-h h2');
    if (title) { title.tabIndex = -1; title.focus({ preventScroll: true }); }
  }, []);
  const rows = (recent.data ?? []).filter(attempt => attempt.kind === 'test' && attempt.provider === provider && (!attemptIds || attemptIds.includes(attempt.id)));
  return <section ref={region}>
    <p className="muted small">From <span className="mono">{endpoint}</span>'s latest 200 attempts. {attemptIds ? 'Only IDs confirmed in this test reply are shown.' : 'Other test batches may appear; none is confirmed as this submission.'} Your channel settings stay in the form.</p>
    {recent.error ? <Banner title={recent.data ? 'Attempt refresh failed' : 'Test attempts could not be read'} tone={recent.data ? 'warn' : 'down'}>{recent.data ? 'Showing the last loaded attempts. ' : ''}{describeFailure(recent.error)}</Banner> : null}
    {recent.data ? rows.length ? <DeliveryTable attempts={rows} compact recipientLabels={recipientLabels} explainAcceptance={false} />
      : <p>{recent.error ? 'No matching attempts in the last loaded view.' : 'No matching attempts in this recent view.'}</p>
      : !recent.error ? <p role="status">Loading recent test attempts...</p> : null}
    <p className="muted small">{rows.some(attempt => attempt.state === 'accepted') ? 'Accepted means the provider acknowledged it; it doesn’t confirm delivery or that an automation ran. ' : null}Older or delayed attempts may be missing; absence doesn’t prove the test was unsent. Resending may duplicate it.</p>
    <div className="row-wrap modal-acts">
      <button type="button" className="btn primary" onClick={onBack}>Back to test status</button>
      <button type="button" className="btn" onClick={recent.reload}>Refresh attempts</button>
      <button type="button" className="btn" onClick={onClose}>Close</button>
    </div>
  </section>;
}

/** Queue acknowledgements follow configured recipient order; subsequent reads may reorder attempts. */
export function previewRecipientLabels(config: NotificationConfig, attempts: DeliveryAttemptView[]): ReadonlyMap<string, string> {
  const recipients = PROVIDER.get(config.kind)?.email ? config.recipients : undefined;
  if (!recipients || recipients.length !== attempts.length || attempts.some(attempt => attempt.provider !== config.kind)
    || new Set(attempts.map(attempt => attempt.id)).size !== attempts.length) return new Map();
  return new Map(attempts.map((attempt, index) => [attempt.id, recipients[index]!.email]));
}

export function PreviewOutcomeList({ attempts, recipientLabels }: { attempts: DeliveryAttemptView[]; recipientLabels?: ReadonlyMap<string, string> }) {
  return <ul className="preview-out">
      {attempts.map(attempt => {
        const state = attemptState(attempt.state), recipient = recipientLabels?.get(attempt.id);
        return (
          <li key={attempt.id}>
            <ProviderIcon kind={attempt.provider} />
            <span className="grow">{recipient ?? providerLabel(attempt.provider)}{attempt.httpStatus ? <span className="muted small"> · HTTP {attempt.httpStatus}</span> : null}
              {recipient ? <small className="muted small">{providerLabel(attempt.provider)}</small> : attempts.length > 1 ? <small className="preview-destination mono">{attempt.destinationId ? `Destination ${attempt.destinationId}` : `Attempt ${attempt.id}`}</small> : null}
              {errorText(attempt.errorCode) ? <small className="muted small">{errorText(attempt.errorCode)}</small> : null}</span>
            <Chip tone={state.tone} label={state.label} />
          </li>
        );
      })}
  </ul>;
}

function PreviewTestDialog({ input, onClose }: { input: NotificationChannelInput; onClose: () => void }) {
  const { api, endpoint, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<TestFailure | null>(null);
  const [attempts, setAttempts] = useState<DeliveryAttemptView[] | null>(null);
  const [waiting, setWaiting] = useState(false);
  const [readFailure, setReadFailure] = useState<string | null>(null);
  const recipientLabels = useRef<ReadonlyMap<string, string>>(new Map());
  const focusAnchor = useTestResultFocus(attempts ? 'submitted' : error?.disposition ?? 'confirm');
  const [inspecting, setInspecting] = useState(false);
  const inspectButton = useRef<HTMLButtonElement>(null);
  const wasInspecting = useRef(false);
  useEffect(() => {
    if (!inspecting && wasInspecting.current) inspectButton.current?.focus();
    wasInspecting.current = inspecting;
  }, [inspecting]);
  const stop = useRef<AbortController | null>(null);
  useEffect(() => () => stop.current?.abort(), []);
  const kind = input.config.kind;
  const recipients = PROVIDER.get(kind)?.email ? input.config.recipients?.length ?? 0 : 1;
  const emergency = kind === 'pushover' && input.config.pushover?.priority === 2 ? input.config.pushover : undefined;
  const emergencyWarning = emergency ? <p className="fwarn"><Glyph tone="warn" /><span>Emergency priority: If accepted by Pushover, this test repeats every {span((emergency.retrySeconds ?? 0) * 1000)} until someone acknowledges it, for up to {span((emergency.expireSeconds ?? 0) * 1000)}.</span></p> : null;
  const follow = (ids: string[]): void => {
    const controller = new AbortController();
    stop.current = controller;
    const until = Date.now() + PREVIEW_WAIT_MS;
    setWaiting(true); setReadFailure(null);
    const poll = async (): Promise<void> => {
      if (controller.signal.aborted) return;
      try {
        const all = await api.deliveries({ limit: 200 }, controller.signal);
        if (controller.signal.aborted || !stillCurrent()) return;
        setReadFailure(null);
        const mine = ids.map(id => all.find(attempt => attempt.id === id)).filter((attempt): attempt is DeliveryAttemptView => Boolean(attempt));
        if (mine.length) setAttempts(previous => ids.map(id => mine.find(attempt => attempt.id === id) ?? previous?.find(attempt => attempt.id === id)).filter((attempt): attempt is DeliveryAttemptView => Boolean(attempt)));
        if (mine.length === ids.length && mine.every(attempt => SETTLED.has(attempt.state))) { setWaiting(false); return; }
      } catch (failure) {
        if (controller.signal.aborted || isAbort(failure) || !stillCurrent()) return;
        setReadFailure(describeFailure(failure, { preferDetail: true }));
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
      recipientLabels.current = previewRecipientLabels(input.config, response.attempts);
      setAttempts(response.attempts);
      follow(response.attempts.map(attempt => attempt.id).filter(id => typeof id === 'string'));
    } catch (failure) {
      if (!stillCurrent()) return;
      setError({ message: describeFailure(failure, { preferDetail: true }), disposition: testSubmissionDisposition(failure) });
    } finally { setBusy(false); }
  };
  const outcomeSummary = attempts ? Object.entries(STATE).map(([state, label]) => {
    const count = attempts.filter(attempt => attempt.state === state).length;
    return count ? `${count} ${label.label.toLowerCase()}` : '';
  }).filter(Boolean).join(', ') : '';
  const settled = Boolean(attempts?.every(attempt => SETTLED.has(attempt.state)));
  const completion = readFailure ? waiting ? 'Retrying updates; these are the last reported results.' : 'Stopped waiting after outcome updates failed. Check delivery attempts for later outcomes.' : waiting ? 'Waiting for outcomes.' : settled ? 'All test attempts have reported an outcome.' : 'Stopped waiting; check delivery attempts for later outcomes.';
  return (
    <Modal title={inspecting ? `Recent ${providerLabel(kind)} test attempts` : error ? error.disposition === 'unconfirmed' ? 'Test submission unconfirmed' : 'Test request rejected' : attempts ? waiting ? 'Test queued' : settled ? 'Test results' : 'Waiting stopped' : 'Test these settings?'} onClose={onClose} busy={busy} wide={inspecting}>
      <p ref={focusAnchor} className="vh" role="status" aria-live="polite" aria-atomic="true">{attempts ? `${endpoint} queued ${plural(attempts.length, 'test attempt')}. ${outcomeSummary}. ${readFailure ? `Outcome updates unavailable: ${readFailure}. ` : ''}${completion} Accepted is not proof of delivery. Nothing was saved.` : ''}</p>
      {inspecting ? <>{emergencyWarning}<TestAttemptsInspector provider={kind} attemptIds={attempts?.map(attempt => attempt.id)} recipientLabels={recipientLabels.current} onBack={() => setInspecting(false)} onClose={onClose} /></> : attempts ? (
        <>
          <p><b>{endpoint}</b> queued {plural(attempts.length, 'test attempt')} with these settings. Nothing was saved.</p>
          {emergencyWarning}
          {readFailure ? <p className="fwarn"><Glyph tone="warn" /><span><b>Outcome updates unavailable.</b> {readFailure} Showing the last reported results. Check delivery attempts for updates.</span></p> : null}
          <PreviewOutcomeList attempts={attempts} recipientLabels={recipientLabels.current} />
          <p>{completion}</p>
          {attempts.some(attempt => attempt.state === 'accepted') ? <p className="muted small">Accepted means the provider acknowledged it; it doesn’t confirm delivery or that an automation ran.</p> : null}
          <div className="row-wrap modal-acts"><button ref={inspectButton} type="button" className="btn primary" onClick={() => setInspecting(true)}>View delivery attempts</button><button type="button" className="btn" onClick={onClose}>Close</button></div>
        </>
      ) : (
        <>
          {error ? <TestSubmissionError failure={error} /> : null}
          {error?.disposition !== 'unconfirmed' ? <p>
            Sends a <b>real message</b> from <span className="mono">{endpoint}</span> through {providerLabel(kind)} to {recipients === 1 ? 'the destination' : `all ${recipients} recipients`} using current form settings, saved or not. {input.enabled ? '' : 'The channel stays disabled. '}
            Saves no settings; creates no monitor, incident or receipt.
          </p> : null}
          {emergencyWarning}
          <AutomationTestDestination config={input.config} beforeSend={error?.disposition !== 'unconfirmed'} />
          {error?.disposition !== 'unconfirmed' && kind === 'homeassistant' ? <HaTestWarning eventType={input.config.homeassistant?.eventType} /> : null}
          {error?.disposition !== 'unconfirmed' ? <p className="muted small">All tests share this node’s limit: one batch at a time, at least 5 seconds apart.</p> : null}
          <div className="row-wrap modal-acts">
            {error?.disposition === 'unconfirmed' ? <button ref={inspectButton} type="button" className="btn primary" onClick={() => setInspecting(true)}>View delivery attempts</button>
              : <button type="button" className="btn primary" onClick={() => { void send(); }} disabled={busy}><Icon name="send" />{busy ? 'Queuing…' : 'Send test'}</button>}
            <button type="button" className="btn" onClick={onClose} disabled={busy}>{error?.disposition === 'unconfirmed' ? 'Close' : 'Cancel'}</button>
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
  const [error, setError] = useState<TestFailure | null>(null);
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
        setError({ disposition: 'rejected', message: `Nothing was changed. ${describeFailure(failure, { preferDetail: true })} The list has been reloaded: review the selection and try again.` });
        refresh(); channels.reload();
      } else setError({ disposition: requestDisposition(failure), message: describeFailure(failure, { preferDetail: true }) });
    } finally { setBusy(false); }
  };
  if (!channel) {
    return <Modal title="Add a channel to monitors" onClose={onClose}><p>{endpoint} no longer lists this channel.</p><div className="row-wrap modal-acts"><button type="button" className="btn" onClick={onClose}>Close</button></div></Modal>;
  }
  return (
    <Modal title={error?.disposition === 'unconfirmed' ? 'Assignment unconfirmed' : `Add ${channel.name} to monitors`} onClose={onClose} busy={busy} wide>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div>{error.disposition === 'unconfirmed' ? null : <b>Assignment rejected</b>}<p>{error.message}</p>{error.disposition === 'unconfirmed' ? <p>The node may have added this channel. Refresh and review monitors before resending.</p> : null}</div></div> : null}
      <p>Choose monitors to alert through <b>{channel.name}</b> ({providerLabel(channel.config.kind)}). Other channels and checks stay unchanged; open incidents continue.</p>
      {opened && channel.revision !== opened ? <p className="small fwarn"><Glyph tone="warn" /><span>This channel was edited since you opened this. Check its destination before adding it.</span></p> : null}
      {!channel.enabled ? <p className="small fwarn"><Glyph tone="warn" /><span>This channel is disabled. Enable it before adding it to monitors.</span></p> : null}
      {view?.configConflict ? <p className="small fwarn"><Glyph tone="warn" /><span>This channel has a configuration conflict. Resolve it first.</span></p> : null}
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
      <div className="row-wrap modal-acts">
        <button type="button" className="btn primary" onClick={() => { if (error?.disposition === 'unconfirmed') { refresh(); channels.reload(); onClose(); } else void apply(); }} disabled={busy || error?.disposition !== 'unconfirmed' && (!picked.length || picked.length > APPLY_MAX || !channel.enabled || Boolean(view?.configConflict))}>
          <Icon name="plus" />{error?.disposition === 'unconfirmed' ? 'Refresh monitors and close' : busy ? 'Adding…' : picked.length ? `Add to ${plural(picked.length, 'monitor')}` : 'Add to monitors'}
        </button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>{error?.disposition === 'unconfirmed' ? 'Close' : 'Cancel'}</button>
      </div>
      <p className="muted small">The node adds this channel to all selected monitors or none if any listed revision changed. Defaults apply automatically only to new monitors.</p>
    </Modal>
  );
}

function DeleteChannelDialog({ channel, users, onClose }: { channel: NotificationChannel; users: number; onClose: () => void }) {
  const { api, endpoint, channels, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<TestFailure | null>(null);
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
      setError({ disposition: requestDisposition(failure), message: describeFailure(failure) });
      setBusy(false);
    }
  };
  return (
    <Modal title={error?.disposition === 'unconfirmed' ? 'Deletion unconfirmed' : `Delete channel ${channel.name}?`} onClose={onClose} busy={busy}>
      <p>{users ? `${plural(users, 'monitor')} still list${users === 1 ? 's' : ''} it. Their alerts to this channel stop; other destinations are unaffected.` : 'No monitor uses this channel.'} Accepted receipts stay accepted.</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div>{error.disposition === 'unconfirmed' ? null : <b>Delete request rejected</b>}<p>{error.message}</p>{error.disposition === 'unconfirmed' ? <p>The channel may already be deleted. Refresh the list before another request.</p> : null}</div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className={error?.disposition === 'unconfirmed' ? 'btn primary' : 'btn danger-solid'} onClick={() => { if (error?.disposition === 'unconfirmed') { channels.reload(); onClose(); } else void remove(); }} disabled={busy}><Icon name={error?.disposition === 'unconfirmed' ? 'refresh' : 'trash'} />{error?.disposition === 'unconfirmed' ? 'Refresh channels and close' : busy ? 'Deleting…' : 'Delete channel'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>{error?.disposition === 'unconfirmed' ? 'Close' : 'Cancel'}</button>
      </div>
    </Modal>
  );
}

function routeText(channel: NotificationChannel, viewer: boolean): ReactNode {
  if (viewer) return <span className="muted">Configuration hidden from viewers</span>;
  const c = channel.config;
  const messaging = messagingRoute(c);
  if (messaging) return <><span className="mono channel-route-server">{messaging.where}</span>{messaging.facts.map((fact, index) => <span className="channel-route-fact" key={index}>{fact}</span>)}</>;
  switch (c.kind) {
    case 'discord': return <span className="mono">{c.webhook?.url ? maskedUrl(c.webhook.url) : 'no URL'}</span>;
    case 'webhook': return <span className="mono">{c.webhook?.url ? maskedUrl(c.webhook.url) : 'no URL'}{c.webhook?.bearerToken ? ' · bearer token' : ''}</span>;
    case 'smtp': return <span className="mono">{c.smtp?.server}:{c.smtp?.port} · {c.smtp?.tlsMode || 'starttls'}</span>;
    case 'mailgun': return <><span className="mono">{c.mailgun?.domain}</span> · {c.mailgun?.region === 'EU' ? 'EU' : 'US'} region</>;
    default: return c.sender?.email ? <>from <span className="mono">{c.sender.email}</span></> : null;
  }
}

/** Overview freshness is independent of this catalogue; retained settings must never look freshly verified. */
function ChannelRefreshNotice() {
  const { channels } = useCanary();
  if (!channels.error || !channels.list) return null;
  return <Banner tone="warn" title="Channels could not be refreshed" actions={<button type="button" className="btn sm" disabled={channels.loading} onClick={channels.reload}>Retry refresh</button>}>
    Showing the last loaded channel data; destinations and enabled/default settings may be out of date. {describeFailure(channels.error)}
  </Banner>;
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
    <div className="view channels-view">
      <PageHead
        title="Notifications"
        sub={<>Reusable channels known to <span className="mono">{endpoint}</span>. Monitors pick channels; every node can deliver them.</>}
        actions={editable ? <a className="btn primary" href={href('channels', 'new')}><Icon name="plus" />New channel</a> : <ReadOnly>Viewers see names and providers only</ReadOnly>}
      />
      <NotificationTabs current="channels" />
      <ChannelRefreshNotice />
      <Card title="Channels" className="channel-list-card">
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
                      <div className="channel-route">{routeText(c, role === 'viewer')}</div>
                      <div className="channel-meta">
                        {recipientCount(c) ? <span>{plural(recipientCount(c), 'recipient')}</span> : null}
                        {eventsSummary(c.events) ? <span>{eventsSummary(c.events)}</span> : null}
                        <span>Used by {plural(count, 'monitor')}</span>
                        <span className="channel-revision">Revision <Rev revision={c.revision} /></span>
                      </div>
                    </div>
                    {editable ? (
                      <div className="acts">
                        <a className={`btn sm${view.configConflict ? ' is-disabled' : ''}`} href={view.configConflict ? undefined : href('channels', c.id, 'edit')} aria-disabled={view.configConflict || undefined} aria-label={`Edit ${c.name}`}><Icon name="edit" />Edit</a>
                        <button type="button" className="btn sm" onClick={() => setTesting(c)} aria-label={`Test ${c.name}`} disabled={view.configConflict || !c.enabled} data-tip={c.enabled ? 'Send one real test message' : 'Enable the channel to test it'}><Icon name="send" />Test</button>
                        <details className="channel-more-actions">
                          <summary className="btn sm" aria-label={`More actions for ${c.name}`}>More<Icon name="chevDown" /></summary>
                          <div className="channel-secondary-actions">
                            <a className="btn sm" href={deliveryHref(c.id)} aria-label={`View attempts for ${c.name}`}>View attempts</a>
                            <button type="button" className="btn sm" onClick={() => setApplying(c.id)} disabled={view.configConflict || !c.enabled} data-tip={c.enabled ? 'Choose existing monitors to add it to' : 'Enable the channel to add it to monitors'} aria-label={`Add ${c.name} to monitors`}><Icon name="plus" />Add to monitors</button>
                            <button type="button" className="btn sm danger" onClick={() => setDeleting(c)} disabled={view.configConflict} aria-label={`Delete ${c.name}`}><Icon name="trash" /></button>
                          </div>
                        </details>
                      </div>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          ) : (
            <EmptyState title={channels.list.length ? 'Nothing matches' : channels.error ? 'No channels in the last loaded list' : 'No channels yet'} action={!channels.list.length && editable ? <a className="btn primary" href={href('channels', 'new')}><Icon name="plus" />New channel</a> : undefined}>
              {channels.list.length ? 'Try another search.' : 'Add Discord, Slack, Telegram, Matrix, ntfy, Gotify, Pushover, an email service, Home Assistant or a JSON webhook, then pick it in a monitor’s alerts.'}
            </EmptyState>
          )}
        <p className="channel-delivery-caveat">Accepted means the provider acknowledged it; it doesn’t confirm delivery or that an automation ran. Retries and partitions can cause duplicates.</p>
      </Card>
      <details className="channel-delivery-details">
        <summary>How delivery works</summary>
        <div className="channel-delivery-body">
          <div><b><Icon name="send" />Delivery comes first</b><p>Assigned nodes take turns per destination. During a partition each side may send, so duplicates are possible. Nothing is suppressed to avoid one.</p></div>
          <div><b><Icon name="mail" />One destination per recipient</b><p>Each email recipient has its own retries and outcome; a chat, push or webhook channel is one destination. Acceptance by one never completes another.</p></div>
          <div><b><Icon name="check" />Accepted isn’t delivered</b><p>Accepted means the provider acknowledged the request. Inbox placement, bounces, chat visibility and phone notifications aren’t tracked.</p></div>
        </div>
      </details>
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
 events: ('down' | 'repeat' | 'recovery')[];
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
    events: channel?.events ? [...channel.events] : ['down', 'repeat', 'recovery'],
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

/** Compare form values in memory only; recipient row identity is an editing detail, not a channel change. */
export function channelDraftChanged(draft: ChannelDraft, original: ChannelDraft): boolean {
  const values = (value: ChannelDraft): string => JSON.stringify({ ...value, recipients: value.recipients.map(({ email, name }) => ({ email, name })) });
  return values(draft) !== values(original);
}

type CField = MField | 'events' | 'name' | 'discordUrl' | 'hookUrl' | 'hookBearer' | 'sender' | 'recipients' | 'smtpServer' | 'smtpPort' | 'smtpAuth' | 'apiKey' | 'mailgunDomain' | 'templates' | 'size';
const CFIELD_LABEL: Record<CField, string> = {
  ...MFIELD_LABEL, events: 'Alert events',
  name: 'Name', discordUrl: 'Discord webhook URL', hookUrl: 'Webhook URL', hookBearer: 'Bearer token', sender: 'Sender', recipients: 'Recipients', smtpServer: 'Server',
  smtpPort: 'Port', smtpAuth: 'Authentication', apiKey: 'API key', mailgunDomain: 'Sending domain', baseUrl: 'API base URL', templates: 'Templates', size: 'Size',
};

const EMAIL = /^[\x21-\x7e]+@[\x21-\x7e]+\.[\x21-\x7e]+$/u;
const HOST = /^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.?$|^[0-9a-fA-F:.]+$/u;
const DISCORD = /^https:\/\/(?:(?:canary|ptb)\.)?discord(?:app)?\.com\/api\/(?:v\d+\/)?webhooks\/\d+\/[\w-]+\/?$/u;

/** Config keys this form models; anything else from a newer node is carried through unchanged. */
const MODELED_CONFIG = new Set(['kind', 'sender', 'recipients', 'smtp', 'mailgun', 'brevo', 'mandrill', 'webhook', 'slack', 'telegram', 'ntfy', 'gotify', 'pushover', 'homeassistant', 'matrix', 'templates']);
/** Channel keys this form writes. Other stored keys belong to the channel, not its provider, so they survive a provider change. */
const CHANNEL_KEYS = ['name', 'enabled', 'config', 'isDefault', 'events'];

/** Preserve validation priority while keeping the exact control that can correct each address error. */
export function channelAddresses(draft: Pick<ChannelDraft, 'senderEmail' | 'senderName' | 'recipients'>): {
  sender: EmailAddress; recipients: EmailAddress[];
  senderProblem?: { field: 'email' | 'name'; message: string };
  recipientProblem?: { key?: number; field: 'email' | 'name' | 'collection'; message: string };
} {
  const sender = draft.senderEmail.trim();
  let senderProblem: { field: 'email' | 'name'; message: string } | undefined;
  let recipientProblem: { key?: number; field: 'email' | 'name' | 'collection'; message: string } | undefined;
  if (!EMAIL.test(sender)) senderProblem = { field: 'email', message: 'Enter the sender’s email address, like alerts@example.com.' };
  else if (draft.senderName.length > 128 || control(draft.senderName)) senderProblem = { field: 'name', message: 'Use a sender name of at most 128 characters.' };
  const recipients: EmailAddress[] = [], seen = new Set<string>();
  for (const row of draft.recipients) {
    const address = row.email.trim();
    if (!address && !row.name.trim()) continue;
    if (!EMAIL.test(address)) { recipientProblem = { key: row.key, field: 'email', message: `“${address || '(empty)'}” isn’t an email address.` }; continue; }
    if (seen.has(address.toLowerCase())) { recipientProblem = { key: row.key, field: 'email', message: `${address} is listed twice.` }; continue; }
    if (row.name.length > 128 || control(row.name)) { recipientProblem = { key: row.key, field: 'name', message: 'Recipient names are at most 128 characters.' }; continue; }
    seen.add(address.toLowerCase());
    recipients.push({ email: address, ...(row.name.trim() ? { name: row.name.trim() } : {}) });
  }
  if (!recipients.length && !recipientProblem) recipientProblem = { key: draft.recipients[0]?.key, field: 'email', message: 'Add at least one recipient.' };
  if (recipients.length > 50) recipientProblem = { field: 'collection', message: 'Use at most 50 recipients.' };
  return { sender: { email: sender, ...(draft.senderName.trim() ? { name: draft.senderName.trim() } : {}) }, recipients, senderProblem, recipientProblem };
}

/**
 * `level` is the selected node's template grammar (alertTemplates.ts templateCapability). Peer protocol 20 applies templates
 * to Discord and adds the card layout; for an older or unknown node the form writes neither the layout nor newly typed
 * protocol 20 placeholders, and keeps what the node already stores.
 */
function buildChannel(draft: ChannelDraft, original: NotificationChannel | undefined, level: TemplateLevel = 'legacy', protocol = 22): { input?: NotificationChannelInput; errors: Partial<Record<CField, string>> } {
  const errors: Partial<Record<CField, string>> = {};
  if (!draft.events.length || draft.events.includes('repeat') && !draft.events.includes('down')) errors.events = 'Select at least one event. Repeats require down alerts.';
  if (protocol < 22 && (draft.kind === 'homeassistant' || draft.kind === 'matrix' || JSON.stringify(draft.events) !== JSON.stringify(original?.events ?? ['down', 'repeat', 'recovery']))) errors.events = 'These providers and event selection require node protocol 22. Upgrade the cluster together.';
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
    const addresses = channelAddresses(draft);
    if (addresses.senderProblem) errors.sender = addresses.senderProblem.message;
    if (addresses.recipientProblem) errors.recipients = addresses.recipientProblem.message;
    config.sender = addresses.sender;
    config.recipients = addresses.recipients;
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
  const input: NotificationChannelInput = { name, enabled: draft.enabled, config, ...(draft.events.length !== 3 ? { events: [...draft.events] } : {}), ...(draft.isDefault ? { isDefault: true } : {}), ...storedExtras(original, CHANNEL_KEYS) };
  if (jsonBytes(input) > 250_000) return { errors: { size: 'This channel is larger than nodes accept. Shorten the templates.' } };
  return { input, errors };
}

/**
 * The form model, for scripts/frontend-review-pure.mjs. The functions keep their own names unexported because the
 * contract harness appends `export { channelDraft, buildChannel }` to its review copy of this file.
 */
export const channelFormModel = { channelDraft, buildChannel };

/** Conservative equality of complete writable values; never print or persist the credentials compared here. */
export function sameChannelSettings(left: NotificationChannelInput, right: NotificationChannelInput): boolean {
  const stable = (value: unknown): unknown => Array.isArray(value) ? value.map(stable)
    : value && typeof value === 'object' ? Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([key, item]) => [key, stable(item)])) : value;
  // Go's value-typed provider structs encode these exact zero values even with omitempty.
  // Keep every nonempty option and unknown key, including credentials in inactive providers.
  const configValues = (config: NotificationConfig): Record<string, unknown> => {
    const copy: Record<string, unknown> = { ...config };
    const empty = { sender: { email: '' }, smtp: { server: '', port: 0 }, mailgun: { apiKey: '', domain: '' },
      brevo: { apiKey: '' }, mandrill: { apiKey: '' }, webhook: { url: '' }, templates: {} };
    for (const [key, value] of Object.entries(empty)) {
      if (JSON.stringify(stable(copy[key])) === JSON.stringify(stable(value))) delete copy[key];
    }
    return copy;
  };
  const values = (item: NotificationChannelInput): string => JSON.stringify(stable({ ...storedExtras(item, CHANNEL_KEYS), name: item.name, enabled: item.enabled,
    config: configValues(item.config), isDefault: Boolean(item.isDefault), events: [...(item.events ?? EVENT_ORDER)].sort() }));
  return values(left) === values(right);
}

/** Readback is inspection, never an automatic retry or a claim that the interrupted request completed. */
function SaveReconciliation({ requested, draftInput, channelId, onUse, onRetry }: {
  requested: NotificationChannelInput; draftInput?: NotificationChannelInput; channelId?: string;
  onUse: (channel: NotificationChannel) => void; onRetry: () => void;
}) {
  const { api, endpoint } = useCanary();
  const stored = useResource(`${api.baseUrl}|save-readback`, signal => api.channels(signal));
  const candidates = (stored.data ?? []).filter(view => !view.channel.deleted && (channelId ? view.channel.id === channelId : view.channel.name === requested.name || view.channel.name === draftInput?.name));
  const matching = candidates.some(view => draftInput && !view.configConflict && sameChannelSettings(view.channel, draftInput));
  return <section className="channel-context-details channel-save-readback" aria-label="Check stored channels">
    <div>
      <h3>Check stored channels</h3>
      <p>Fresh read from <span className="mono">{endpoint}</span>; a request may still be finishing.</p>
      {stored.loading ? <p role="status">Reading stored channels...</p> : stored.error ? <p role="alert">Stored channels could not be checked. {describeFailure(stored.error)}</p> : <>
        <p role="status">{candidates.length ? `${plural(candidates.length, 'stored channel')} to review.` : channelId ? 'This channel is not in the returned list.' : 'No channel with this name is in the returned list.'} {!candidates.length ? 'Absence is not proof that the save failed.' : ''}</p>
        {candidates.map(view => {
          const channel = view.channel, matches = Boolean(draftInput && !view.configConflict && sameChannelSettings(channel, draftInput));
          return <div key={channel.id} className="channel-save-candidate">
            <b>{channel.name}</b> <span className="muted small">{providerLabel(channel.config.kind)}</span>
            <p className="mono">ID {channel.id} <Rev revision={channel.revision} /></p>
            <p>{routeText(channel, false)}</p>
            <p>{channel.enabled ? 'Enabled' : 'Disabled'}; {eventsSummary(channel.events) ?? 'all alert events'}{view.configConflict ? '; configuration conflict' : ''}.</p>
            <p>{matches ? 'Stored settings, including credentials, match your current draft.' : 'Stored settings differ from your current draft, or a conflict blocks use.'}</p>
            {matches ? <button type="button" className="btn primary" onClick={() => onUse(channel)}>Use this stored channel</button> : null}
          </div>;
        })}
      </>}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn" disabled={stored.loading} onClick={stored.reload}>Check again</button>
        {!stored.loading && !stored.error && stored.data && !matching ? <button type="button" className="btn" onClick={onRetry}>Allow another save</button> : null}
      </div>
      {!matching && !stored.loading && !stored.error && stored.data ? <p>Allowing another save sends nothing. Saving again may duplicate a channel or reapply settings.</p> : null}
    </div>
  </section>;
}

/**
 * The edit form mounts only once the stored channel is known, so its draft, disclosures and base revision come from
 * that channel. A link opened before the channel list loaded shows a loading note and then mounts fresh.
 */
export function ChannelFormView({ id }: { id?: string }) {
  return <ChannelFormSession key={id ?? 'new'} id={id} />;
}

/** Bootstrap once per route, then retain the editor even if a later catalogue omits this channel. */
function ChannelFormSession({ id }: { id?: string }) {
  const { channels } = useCanary();
  const existing = id ? channels.map.get(id) : undefined;
  const seed = useRef<NotificationChannel | undefined>(existing?.channel);
  if (!seed.current && existing) seed.current = existing.channel;
  const reload = channels.reload;
  // A deep link can name a channel created outside this tab since its catalogue last loaded.
  useEffect(() => { if (id && !seed.current) reload(); }, [id, reload]);
  return <ChannelFormBody key={seed.current ? 'known' : 'unloaded'} id={id} seed={seed.current} />;
}

function ChannelFormBody({ id, seed }: { id?: string; seed?: NotificationChannel }) {
  const { channels, endpoint, editable, api, overview, stillCurrent } = useCanary();
  const existing = id ? channels.map.get(id) : undefined;
  const [draft, setDraft] = useState<ChannelDraft>(() => channelDraft(seed));
  const [initial] = useState<ChannelDraft>(() => channelDraft(seed));
  const [base, setBase] = useState<NotificationChannel | undefined>(seed);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [readingLatest, setReadingLatest] = useState(false);
  const [latestError, setLatestError] = useState<string | null>(null);
  const [failure, setFailure] = useState<{ message: string; stale: boolean; disposition: ReturnType<typeof requestDisposition>; submitted: NotificationChannelInput } | null>(null);
  const [attempts, setAttempts] = useState(0);
  const [templatesOpen, setTemplatesOpen] = useState(Boolean(initial.subject || initial.text || initial.html || initial.discordEmbed));
  const [apiOpen, setApiOpen] = useState(Boolean(initial.mailgunBase || initial.brevoBase || initial.mandrillBase || initial.tgBase || initial.poBase));
  const [providerOpen, setProviderOpen] = useState(!existing?.channel);
  const [previewing, setPreviewing] = useState<NotificationChannelInput | null>(null);
  const summaryRef = useRef<HTMLDivElement>(null);
  const formId = useId();
  const baseline = useRef(initial);
  const dirty = channelDraftChanged(draft, baseline.current);
  const [pendingNavigation, setPendingNavigation] = useState<{ proceed: () => void } | null>(null);
  const guardState = useRef({ dirty });
  const releaseGuard = useRef<() => void>(() => {});
  const mounted = useRef(true);
  useLayoutEffect(() => { guardState.current = { dirty }; }, [dirty]);
  useLayoutEffect(() => {
    mounted.current = true;
    const release = registerNavigationGuard(proceed => {
      if (!guardState.current.dirty) return false;
      setPendingNavigation(pending => pending ?? { proceed });
      return true;
    });
    releaseGuard.current = release;
    const beforeUnload = (event: BeforeUnloadEvent): void => {
      if (!guardState.current.dirty) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', beforeUnload);
    return () => { mounted.current = false; release(); window.removeEventListener('beforeunload', beforeUnload); };
  }, []);
  // The stored version as a draft, to tell a new destination from a credential rotation.
  const previous = useMemo(() => base ? channelDraft(base) : undefined, [base]);
  useEffect(() => { if (attempts || latestError) summaryRef.current?.focus(); }, [attempts, failure, latestError]);

  if (!editable) return <div className="view channel-editor"><PageHead title={id ? 'Edit channel' : 'New channel'} /><Card><EmptyState title="Viewers can’t change channels">Sign in with an editor or admin account.</EmptyState></Card></div>;
  if (channels.unsupported) return <div className="view channel-editor"><PageHead title="New channel" /><Card><EmptyState title={`${endpoint} doesn’t offer reusable channels`}>It probably runs an older Uptime-Canary version.</EmptyState></Card></div>;
  if (id && !existing && !base) {
    return <div className="view channel-editor"><PageHead crumbs={<a href={href('notifications')}>Notifications</a>} title="Edit channel" /><ChannelRefreshNotice /><Card>{channels.loading ? <p className="muted" role="status">Checking the channel on {endpoint}...</p> : channels.error && !channels.list ? <Banner tone="down" title="Channels could not be loaded" actions={<button type="button" className="btn sm" onClick={channels.reload}>Retry loading</button>}>{describeFailure(channels.error)}</Banner> : channels.error && channels.list ? <EmptyState title="Channel could not be confirmed">Refresh the channel list before concluding this channel was deleted.</EmptyState> : channels.list ? <EmptyState title="Channel not found">{endpoint} doesn’t know this channel. It may have been deleted.</EmptyState> : <p className="muted">Loading the channel from {endpoint}…</p>}</Card></div>;
  }

  // What the selected node renders: its overview's reported peer protocol, or the six-placeholder grammar when it reports none.
  const capability = templateCapability(overview.build);
  const { input, errors } = buildChannel(draft, base, capability.level, capability.protocol ?? 0);
  // Alert events and the protocol 22 providers: editable only when the selected node reports protocol 22 or later.
  const eventsReady = (capability.protocol ?? 0) >= EVENTS_PROTOCOL;
  const providerBlocked = !eventsReady && PROTOCOL_22_KINDS.has(draft.kind);
  const reported = capability.protocol ? `reports peer protocol ${capability.protocol}` : 'doesn’t report a peer protocol';
  const toggleEvent = (event: AlertEvent, on: boolean): void =>
    setDraft(d => ({ ...d, events: toggleAlertEvent(d.events, event, on) }));
  const visible = showErrors ? errors : {};
  const set = <K extends keyof ChannelDraft>(key: K, value: ChannelDraft[K]): void => setDraft(d => ({ ...d, [key]: value }));
  const patch = (change: Partial<ChannelDraft>): void => setDraft(d => ({ ...d, ...change }));
  const smtpAuthField = !visible.smtpAuth ? undefined : control(draft.smtpUser) ? 'user' : control(draft.smtpPass) ? 'password' : draft.smtpTls === 'plain' ? 'both' : !draft.smtpUser ? 'user' : 'password';
  const provider = PROVIDER.get(draft.kind)!;
  const addressIssues = provider.email ? channelAddresses(draft) : undefined;
  const recipientIssue = visible.recipients ? addressIssues?.recipientProblem : undefined;
  const current = existing?.channel ?? base;
  const missing = Boolean(id && base && !existing);
  const saveUnconfirmed = failure?.disposition === 'unconfirmed';
  const changedElsewhere = Boolean(current && base && current.revision !== base.revision);
  const conflicted = Boolean(existing?.configConflict);
  const users = current ? overview.monitors.filter(view => view.monitor.notificationIds?.includes(current.id)).length : 0;
  // Secrets stored in the revision being edited (after "Load latest" too), for the "Saving removes" warnings.
  const stored = (key: keyof ChannelDraft): boolean => Boolean(current && base?.config.kind === draft.kind && previous?.[key]);

  const finishSaved = (saved: NotificationChannel, verified = false): void => {
    if (!mounted.current || !stillCurrent()) return;
    notify(verified ? `Using stored channel "${saved.name}" on ${endpoint}; no new save was sent.` : `${current ? 'Saved' : 'Created'} channel “${saved.name}” on ${endpoint} (revision ${shortRev(saved.revision)}).${current ? '' : ' Pick it in a monitor’s alerts to use it.'}`);
    channels.reload();
    // An acknowledgement or an explicitly chosen matching readback permits leaving the draft.
    baseline.current = draft;
    guardState.current.dirty = false;
    releaseGuard.current();
    setPendingNavigation(null);
    navigate(href('notifications'));
  };
  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (saving || providerBlocked || missing || saveUnconfirmed) return;
    setShowErrors(true); setFailure(null); setLatestError(null);
    if (!input) {
      if (errors.templates) setTemplatesOpen(true);
      if (errors.baseUrl) setApiOpen(true);
      setAttempts(n => n + 1);
      return;
    }
    setSaving(true);
    try {
      const saved = current && base ? await api.updateChannel(current.id, { ...input, revision: base.revision }) : await api.createChannel(input);
      if (!sameChannelSettings(saved, input)) throw new ApiFailure(200, 'The node returned different channel settings. Check stored settings before another save.');
      finishSaved(saved);
    } catch (error) {
      if (!stillCurrent()) return;
      const stale = error instanceof ApiFailure && error.status === 409;
      setFailure({ message: stale ? 'Someone saved a newer revision first, or nodes now hold conflicting versions.' : describeFailure(error, { preferDetail: true }), stale, disposition: requestDisposition(error), submitted: input });
      setAttempts(n => n + 1);
    } finally { setSaving(false); }
  };
  const loadLatest = async (): Promise<void> => {
    if (saving || !current) return;
    setReadingLatest(true); setLatestError(null); setSaving(true);
    try {
      const views = await api.channels();
      if (!mounted.current || !stillCurrent()) return;
      const latest = views.find(view => view.channel.id === current.id);
      if (!latest) throw new Error('The channel is not in the latest list.');
      if (latest.configConflict) throw new Error('The channel has conflicting versions. Resolve them before loading.');
      const next = channelDraft(latest.channel);
      baseline.current = next; setDraft(next); setBase(latest.channel); setFailure(null); setShowErrors(false); setProviderOpen(false);
      channels.reload();
    } catch (error) {
      if (mounted.current && stillCurrent()) setLatestError(describeFailure(error));
    } finally { if (mounted.current) { setReadingLatest(false); setSaving(false); } }
  };
  // A test of the form as it is: only complete, valid settings are sent, and the dialog holds them in memory alone.
  const testSettings = (): void => {
    if (saving || providerBlocked) return;
    setShowErrors(true); setFailure(previous => previous?.disposition === 'unconfirmed' ? previous : null);
    if (!input) {
      if (errors.templates) setTemplatesOpen(true);
      if (errors.baseUrl) setApiOpen(true);
      setAttempts(n => n + 1);
      return;
    }
    setPreviewing(input);
  };
  const errorEntries = Object.entries(visible);
  const fieldLabel = (field: CField): string => {
    if (field === 'sender') return addressIssues?.senderProblem?.field === 'name' ? 'From name (optional)' : 'From address';
    if (field === 'recipients' && recipientIssue?.key !== undefined) return `Recipient ${draft.recipients.findIndex(row => row.key === recipientIssue.key) + 1} ${recipientIssue.field}`;
    if (field === 'serverUrl' && draft.kind === 'ntfy') return 'Server URL (optional)';
    if (field === 'serverUrl') return draft.kind === 'homeassistant' ? 'Home Assistant URL' : draft.kind === 'matrix' ? 'Homeserver URL' : 'Server URL';
    if (field === 'accessToken' && draft.kind === 'homeassistant') return 'Long-lived access token';
    if (field === 'device') return 'Device (optional)';
    if (field === 'sound') return 'Sound (optional)';
    if (field === 'tags') return 'Tags (optional)';
    if (field === 'appToken' && draft.kind === 'pushover') return 'Application API token';
    if (field === 'threadId') return 'Forum topic ID (optional)';
    if (field === 'baseUrl') return 'API base URL (optional)';
    if (field === 'smtpPort') return 'Port (optional)';
    return CFIELD_LABEL[field];
  };
  const errorList = errorEntries.map(([field, message]) => `${fieldLabel(field as CField)}: ${message}`);
  const focusError = (index: number): void => {
    const entry = errorEntries[index], form = summaryRef.current?.closest('form');
    if (!entry || !form) return;
    const message = entry[1];
    const error = [...form.querySelectorAll<HTMLElement>('[id]')].find(element => element.textContent?.trim() === message);
    const clearSmtp = entry[0] === 'smtpAuth' && draft.smtpTls === 'plain' ? form.querySelector<HTMLElement>('[data-clear-smtp]') : null;
    const described = error?.id ? [...form.querySelectorAll<HTMLElement>('input,select,textarea')].find(element => !element.matches(':disabled') && element.getAttribute('aria-describedby')?.split(/\s+/u).includes(error.id)) : null;
    const recipientGroup = entry[0] === 'recipients' && recipientIssue?.field === 'collection' ? form.querySelector<HTMLElement>('[data-recipient-group]') : null;
    const control = clearSmtp ?? recipientGroup ?? described ?? error?.closest('.fld')?.querySelector<HTMLElement>('input:not(:disabled),select:not(:disabled),textarea:not(:disabled)');
    const target = control ?? error;
    if (target) { if (!control) target.tabIndex = -1; target.scrollIntoView({ block: 'center' }); target.focus({ preventScroll: true }); }
  };
  const updateRecipient = (key: number, change: Partial<Recipient>): void => set('recipients', draft.recipients.map(row => row.key === key ? { ...row, ...change } : row));

  return (
    <div className="view channel-editor">
      <PageHead
        crumbs={<a href={href('notifications')}>Notifications</a>}
        title={current ? `Edit ${current.name}` : 'New channel'}
        sub={current ? <>Editing revision <Rev revision={base?.revision ?? current.revision} />{users ? ` · used by ${plural(users, 'monitor')}` : ''}. Saving writes a new revision on <span className="mono">{endpoint}</span>.</> : <>Creates a reusable channel on <span className="mono">{endpoint}</span>, shared with every peer.</>}
      />
      {conflicted ? <Banner title="This channel has a configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Resolve in Configuration</a>}>Edits are rejected until the conflict is resolved.</Banner>
        : changedElsewhere && !latestError && !failure?.stale ? <Banner title={<>This channel changed on {endpoint} while you were editing</>} actions={<button type="button" className="btn sm" disabled={saving} onClick={() => { void loadLatest(); }}><Icon name="refresh" />Load latest (discards your edits)</button>}>Saving your version will be rejected because it’s based on an older revision.</Banner>
        : null}
      {missing ? <Banner tone="warn" title="Channel no longer listed" actions={<button type="button" className="btn sm" onClick={channels.reload}>Refresh channels</button>}>
        <span id={`${formId}-missing`}>Your draft is kept. This channel is absent from the last loaded list, possibly because it was deleted elsewhere. Saving is blocked until it appears again.</span>
      </Banner> : null}
      {dirty ? <span className="channel-unsaved" role="status">Unsaved changes</span> : null}
      <ChannelRefreshNotice />
      <Card>
        <form className="form" onSubmit={event => { void submit(event); }} noValidate aria-busy={saving}>
          <ErrorSummary ref={summaryRef} title={latestError ? 'Latest settings could not be loaded' : failure ? (saveUnconfirmed ? 'Save unconfirmed' : failure.stale ? 'Save rejected: the channel changed or has a conflict' : 'Save request rejected') : 'Check these fields'} errors={failure || latestError ? [] : errorList} onErrorSelect={focusError}>
            {failure || latestError ? <>
              <p>{latestError ? <>Your draft is kept. {latestError}</> : failure?.message}</p>
              {(latestError || failure?.stale) && current ? <p><button type="button" className="linkbtn" disabled={saving} onClick={() => { void loadLatest(); }}>Load latest (discards your edits)</button>{latestError ? null : ' and apply your change again.'}</p> : null}
            </> : null}
          </ErrorSummary>
          {saveUnconfirmed && failure ? <div className="full" id={`${formId}-savecheck`}>
            <p>The node may have saved this change and resumed owed alerts. Your draft is kept. Check stored settings before saving again{current ? '.' : ', which could create a duplicate channel.'}</p>
            <SaveReconciliation requested={failure.submitted} draftInput={input} channelId={id} onUse={stored => finishSaved(stored, true)} onRetry={() => setFailure(null)} />
          </div> : null}
          <h3 className="fsec full">Channel</h3>
          <fieldset className="fld channel-provider" disabled={saving}>
            <legend>Provider</legend>
            {current && !providerOpen ? (
              <div className="channel-provider-current">
                <span><ProviderIcon kind={draft.kind} /><b>{provider.label}</b></span>
                <button type="button" className="btn sm" onClick={() => setProviderOpen(true)} aria-expanded={false}>Change provider</button>
              </div>
            ) : (
              <select aria-label="Provider" aria-describedby={`${formId}-provider-h`} value={draft.kind} autoFocus={Boolean(current)} onChange={event => set('kind', event.target.value as Kind)}>
                {PROVIDER_GROUPS.map(group => (
                  <optgroup key={group.key} label={group.label}>
                    {PROVIDERS.filter(entry => entry.group === group.key).map(entry => {
                      const gated = !eventsReady && PROTOCOL_22_KINDS.has(entry.kind);
                      return <option key={entry.kind} value={entry.kind} disabled={gated && draft.kind !== entry.kind}>{entry.label}</option>;
                    })}
                  </optgroup>
                ))}
              </select>
            )}
            <small id={`${formId}-provider-h`}>{provider.hint}</small>
            {!current || providerOpen ? <small>13 providers, including Home Assistant, Matrix and email{!eventsReady ? '; Home Assistant / Matrix need peer protocol 22' : ''}.</small> : null}
            {current && current.config.kind !== draft.kind ? <small className="fwarn"><Glyph tone="warn" /><span>A different provider is a new destination. Alerts already owed to the old one aren’t moved.</span></small> : null}
            {!eventsReady && PROTOCOL_22_KINDS.has(draft.kind) ? <small className="fwarn"><Glyph tone="warn" /><span>{provider.label} needs peer protocol {EVENTS_PROTOCOL}; <span className="mono">{endpoint}</span> {reported}. Saving and testing are refused until every node is upgraded.</span></small> : null}
          </fieldset>
          <TextField label="Name" value={draft.name} onChange={value => set('name', value)} error={visible.name} hint="Shown when picking channels for monitors" maxLength={128} autoFocus={!current} disabled={saving} />

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
              {draft.smtpTls === 'plain' && (draft.smtpUser || draft.smtpPass) ? <div className="full">
                <p className="fwarn"><Glyph tone="warn" /><span>Plain relay cannot log in. Remove the credentials from this draft, or choose STARTTLS / implicit TLS.</span></p>
                <button type="button" className="btn" data-clear-smtp disabled={saving} onClick={event => {
                  const radio = event.currentTarget.closest('form')?.querySelector<HTMLInputElement>('input[type="radio"][value="plain"]');
                  patch({ smtpUser: '', smtpPass: '' }); radio?.focus();
                }}>Remove SMTP credentials</button>
              </div> : null}
              {draft.smtpTls === 'plain' && !draft.smtpUser && !draft.smtpPass && (stored('smtpUser') || stored('smtpPass')) ? <p className="full fwarn"><Glyph tone="warn" /><span>Saving Plain relay removes the stored SMTP login.</span></p> : null}
              {draft.smtpTls !== 'plain' || draft.smtpUser || draft.smtpPass ? (
                <>
                  <TextField label="User name (optional)" value={draft.smtpUser} onChange={value => set('smtpUser', value)} error={smtpAuthField === 'user' || smtpAuthField === 'both' ? visible.smtpAuth : undefined} errorId={`${formId}-smtpauth`} disabled={saving} autoComplete="off" />
                  <SecretField optional label="Password" value={draft.smtpPass} onChange={value => set('smtpPass', value)} error={smtpAuthField === 'password' || smtpAuthField === 'both' ? visible.smtpAuth : undefined} errorId={`${formId}-smtpauth`} disabled={saving} storedSet={stored('smtpPass')} hint={draft.smtpTls === 'plain' ? 'Remove the login to use a plain relay.' : 'AUTH PLAIN over TLS.'} />
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
              <TextField mono label="From address" value={draft.senderEmail} onChange={value => set('senderEmail', value)} error={addressIssues?.senderProblem?.field === 'email' ? visible.sender : undefined} placeholder="alerts@example.com" disabled={saving}
                hint="Must be a sender or domain your provider has verified." />
              <TextField label="From name (optional)" value={draft.senderName} onChange={value => set('senderName', value)} error={addressIssues?.senderProblem?.field === 'name' ? visible.sender : undefined} placeholder="Uptime Canary" maxLength={128} disabled={saving} />
              <fieldset className="fld full hdr-f" data-recipient-group tabIndex={-1} disabled={saving} aria-invalid={visible.recipients ? true : undefined} aria-describedby={`${formId}-rcpt-h${visible.recipients ? ` ${formId}-rcpt` : ''}`}>
                <legend>Recipients <span className="muted">({draft.recipients.filter(row => row.email.trim()).length} of at most 50)</span></legend>
                <div className="hdr-rows">
                  {draft.recipients.map((row, index) => (
                    <div className="hdr-row channel-recipient" key={row.key}>
                      <label><span>Recipient {index + 1} email</span>
                        <input type="email" className="mono" value={row.email} placeholder="oncall@example.com" aria-label={`Recipient ${index + 1} email`} aria-invalid={recipientIssue?.key === row.key && recipientIssue.field === 'email' ? true : undefined} aria-describedby={recipientIssue?.key === row.key && recipientIssue.field === 'email' ? `${formId}-rcpt` : undefined} autoComplete="off" spellCheck={false} onChange={event => updateRecipient(row.key, { email: event.target.value })} />
                      </label>
                      <label><span>Recipient {index + 1} name <span className="muted">(optional)</span></span>
                        <input type="text" value={row.name} placeholder="name (optional)" aria-label={`Recipient ${index + 1} name`} aria-invalid={recipientIssue?.key === row.key && recipientIssue.field === 'name' ? true : undefined} aria-describedby={recipientIssue?.key === row.key && recipientIssue.field === 'name' ? `${formId}-rcpt` : undefined} autoComplete="off" onChange={event => updateRecipient(row.key, { name: event.target.value })} />
                      </label>
                      <button type="button" className="icon-btn sm" onClick={() => set('recipients', draft.recipients.filter(other => other.key !== row.key))} aria-label={`Remove recipient ${row.email || index + 1}`} disabled={draft.recipients.length === 1}><Icon name="close" /></button>
                    </div>
                  ))}
                </div>
                <div className="row-wrap pick-acts">
                  <button type="button" className="linkbtn" onClick={() => set('recipients', [...draft.recipients, { key: ++recipientKey, email: '', name: '' }])} disabled={draft.recipients.length >= 50}><Icon name="plus" />Add recipient</button>
                </div>
                <small id={`${formId}-rcpt-h`}>Each recipient is a separate destination with its own retries, outcome and receipt. Adding one later doesn’t replay finished outages to it.</small>
                <ErrorText id={`${formId}-rcpt`}>{visible.recipients}</ErrorText>
              </fieldset>
            </>
          ) : null}

          <h3 className="fsec full">Alert behavior</h3>
          <fieldset className="fld full channel-events" id={`${formId}-events`} tabIndex={-1} disabled={saving || !eventsReady} aria-invalid={visible.events ? true : undefined} aria-describedby={`${formId}-events-h${!draft.events.includes('down') ? ` ${formId}-repeat-h` : ''}${visible.events ? ` ${formId}-events-e` : ''}`}>
            <legend>Alert events</legend>
            <div className="channel-event-options">
              {EVENT_ORDER.map(event => (
                <CheckField key={event} full={false} label={EVENT_CHOICE[event].label} hint={EVENT_CHOICE[event].hint} disabled={event === 'repeat' && !draft.events.includes('down')} checked={draft.events.includes(event)} onChange={on => toggleEvent(event, on)} />
              ))}
            </div>
            {!draft.events.includes('down') ? <small id={`${formId}-repeat-h`}>Enable down alerts to choose repeats.</small> : null}
            {eventsReady ? <small id={`${formId}-events-h`}>Excluded events are cancelled for this channel only.</small>
              : <small id={`${formId}-events-h`} className="fwarn"><Glyph tone="warn" /><span>Choosing events needs peer protocol {EVENTS_PROTOCOL}; <span className="mono">{endpoint}</span> {reported}. Upgrade every node in the cluster together. Until then this selection is read-only.</span></small>}
            <ErrorText id={`${formId}-events-e`}>{visible.events}</ErrorText>
          </fieldset>
          <CheckField full={false} label="Enabled" checked={draft.enabled} onChange={value => set('enabled', value)} disabled={saving} hint="Disabled: no sends. Enable during an outage to resume owed alerts." />
          <CheckField full={false} label="Default for new monitors" checked={draft.isDefault} onChange={value => set('isDefault', value)} disabled={saving}
            hint={draft.isDefault && !draft.enabled ? 'Only while enabled: a disabled default isn’t preselected.' : 'Preselected for new monitors; existing monitors stay unchanged.'} />

          <Disclosure title="Message templates" summary={templateSummary(draft.kind, draft, capability.level, draft.discordEmbed)} open={templatesOpen} onToggle={() => setTemplatesOpen(v => !v)}>
            <MessageTemplates kind={draft.kind} providerLabel={provider.label} icon={<ProviderIcon kind={draft.kind} />} channelName={draft.name}
              fields={{ subject: draft.subject, text: draft.text, html: draft.html }} onChange={patch} saving={saving} error={visible.templates} formId={formId}
              capability={capability} nodeName={endpoint} embed={draft.discordEmbed} onEmbed={value => set('discordEmbed', value)} />
          </Disclosure>

          {visible.size ? <div className="full"><ErrorText id={`${formId}-size`}>{visible.size}</ErrorText></div> : null}
          {providerBlocked ? <p className="full fwarn small" id={`${formId}-blocked`}>Save and Test are unavailable: {provider.label} needs peer protocol {EVENTS_PROTOCOL}; {endpoint} {reported}. Upgrade every node together.</p> : null}
          <div className="full form-acts">
            <button type="submit" className="btn primary" disabled={saving || conflicted || providerBlocked || missing || saveUnconfirmed} aria-describedby={[providerBlocked ? `${formId}-blocked` : '', missing ? `${formId}-missing` : '', saveUnconfirmed ? `${formId}-savecheck` : ''].filter(Boolean).join(' ') || undefined}><Icon name={current ? 'check' : 'plus'} />{saving ? readingLatest ? 'Reading latest settings…' : `Saving to ${endpoint}…` : current ? 'Save changes' : 'Create channel'}</button>
            <button type="button" className="btn" disabled={saving || providerBlocked} aria-describedby={providerBlocked ? `${formId}-blocked` : undefined} onClick={testSettings}><Icon name="send" />Test these settings</button>
            <a className="btn" href={href('notifications')}>Cancel</a>
            {saving ? <span className="muted small" role="status">{readingLatest ? 'Reading stored settings…' : <>Waiting for {endpoint} to acknowledge…</>}</span> : <span className="muted small">Saving can resume owed alerts but sends no test. Tests need confirmation. Cancel won’t undo saved changes.</span>}
          </div>
        </form>
      </Card>
      {previewing ? <PreviewTestDialog input={previewing} onClose={() => setPreviewing(null)} /> : null}
      {pendingNavigation ? <Modal title="Discard unsaved channel changes?" onClose={() => setPendingNavigation(null)} busy={saving}>
        <p>Leaving discards the draft in this form. It does not undo any change already applied on a node.</p>
        <div className="row-wrap modal-acts">
          <button type="button" className="btn primary" disabled={saving} onClick={() => setPendingNavigation(null)}>Keep editing</button>
          <button type="button" className="btn" disabled={saving} onClick={() => {
            const proceed = pendingNavigation.proceed;
            // Discard is real even if the next action only opens a node picker and is later cancelled.
            setDraft(baseline.current);
            guardState.current.dirty = false;
            setPendingNavigation(null);
            proceed();
          }}>Discard changes</button>
        </div>
      </Modal> : null}
    </div>
  );
}
