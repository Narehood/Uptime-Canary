import { useState, type ReactNode } from 'react';
import type {
  Group, GroupInput, Maintenance, MaintenanceInput, Monitor, MonitorInput, NotificationChannel, NotificationChannelInput, StatusPage, StatusPageInput, StatusPageView, Tag as TagResource, TagInput,
} from '@uptime-canary/shared';
import { ApiFailure, describeFailure } from '../api';
import { browserMasked } from '../browser';
import { acceptedStatusText, typeLabel } from '../checks';
import { useCanary, type Canary } from '../context';
import { MASK, andList, maskedUrl, plural, secretNamed, shortRev, span } from '../format';
import { gamedigMasked } from '../gamedig';
import { useResource } from '../hooks';
import { messagingRoute } from '../messaging';
import { candidateOrder, groupPath } from '../model';
import { notify } from '../notices';
import { pm2Masked } from '../pm2';
import { href } from '../router';
import { steamMasked } from '../steam';
import { Banner, Card, EmptyState, PageHead, ReadOnly, Rev, Tag, Time } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';
import { MonitorTags, TagPill } from '../ui/tags';
import { providerLabel } from './Channels';
import { scheduleText } from './Maintenance';

interface Row<T> {
  key: string;
  label: string;
  /** Every field this row owns, present or not. Fields no row owns are carried by the "Other fields" row. */
  keys: readonly string[];
  /** The writable fields this row contributes. Fields that must agree (type, target and options) share a row. */
  project: (head: T) => Record<string, unknown>;
  render: (head: T) => ReactNode;
}

const pick = <T extends object, K extends keyof T>(value: T, keys: readonly K[]): Record<string, unknown> => {
  const out: Record<string, unknown> = {};
  for (const key of keys) if (value[key] !== undefined && value[key] !== null && value[key] !== '') out[key as string] = structuredClone(value[key]);
  return out;
};
/** A row that copies its fields as stored. */
const picked = <T extends object>(key: string, label: string, keys: readonly (keyof T & string)[], render: (head: T) => ReactNode): Row<T> =>
  ({ key, label, keys, project: head => pick(head, keys), render });

/** Server-owned fields: never part of a resolution body. */
const RESOLVE_META = new Set(['id', 'revision', 'updatedAt', 'deleted', 'checkGeneration', 'pushStartedAt']);

/**
 * Fields no row owns, such as ones added by a newer node, become one row so a resolution keeps them (from the version
 * picked for that row) instead of silently dropping them.
 */
export function withOtherFields<T extends object>(rows: Row<T>[], heads: T[], reveal: boolean, viewer: boolean): Row<T>[] {
  const owned = new Set(rows.flatMap(row => row.keys));
  const extra = [...new Set(heads.flatMap(head => Object.keys(head)))].filter(key => !owned.has(key) && !RESOLVE_META.has(key)).sort();
  if (!extra.length) return rows;
  return [...rows, {
    key: '__other', label: 'Other fields', keys: extra,
    project: head => pick(head as Record<string, unknown>, extra),
    render: head => {
      const value = pick(head as Record<string, unknown>, extra);
      return Object.keys(value).length
        ? <><span className="muted small">Not edited here; kept as stored.</span><pre className="json-pre small">{JSON.stringify(viewer || !reveal ? maskOptions(value) : value, null, 2)}</pre></>
        : <span className="muted">None</span>;
    },
  }];
}

const CHECK_KEYS = ['type', 'target', 'expectedStatusCodes', 'http', 'tls', 'dns', 'websocket', 'smtp', 'service', 'ping', 'manual', 'push', 'globalping', 'steam', 'pm2', 'browser', 'gamedig', 'upsideDown'] as const;
const OPTION_KEYS = ['http', 'tls', 'dns', 'websocket', 'smtp', 'service', 'ping', 'manual', 'push', 'globalping', 'steam', 'pm2', 'browser', 'gamedig'] as const;
/**
 * The options a resolver shows, masked unless revealed. The Steam API key stays masked even then; a browser's whole endpoint
 * and token, and a game query service's whole address, its token and the game token, are masked until revealed.
 */
const shownOption = (key: (typeof OPTION_KEYS)[number], value: unknown, reveal: boolean): unknown =>
  key === 'steam' ? steamMasked(reveal ? value : maskOptions(value)) : reveal ? value : key === 'pm2' ? pm2Masked(value) : key === 'browser' ? browserMasked(value)
    : key === 'gamedig' ? gamedigMasked(value) : maskOptions(value);

function monitorRows(ctx: Canary, reveal: boolean): Row<Monitor>[] {
  const groupName = (id?: string): string => id ? groupPath(id, ctx.groupMap) : 'No group';
  const viewer = ctx.role === 'viewer';
  return [
    picked<Monitor>('name', 'Name', ['name'], m => m.name),
    // Private metadata (protocol 19), never part of the check: plain text with its line breaks.
    picked<Monitor>('description', 'Description', ['description'], m => typeof m.description === 'string' && m.description ? <span className="pre-line wrap">{m.description}</span> : <span className="muted">None</span>),
    {
      key: 'check', label: 'Check', keys: CHECK_KEYS, project: m => pick(m, CHECK_KEYS),
      render: m => {
        const options = OPTION_KEYS.filter(key => m[key] !== undefined && m[key] !== null);
        return (
          <>
            <span className="ttype">{typeLabel(m.type)}</span> {m.type !== 'manual' && m.type !== 'push' ? <span className="mono wrap">{m.target}</span> : null}
            {m.type === 'globalping' && m.globalping ? <span className="muted small"> · {String(m.globalping.mode).toLowerCase()} from <span className="mono">{m.globalping.location}</span></span> : null}
            {m.type === 'system-service' ? <span className="muted small"> · read by each node from its own operating system</span> : null}
            {m.type === 'tailscale-ping' ? <span className="muted small"> · pinged by each node through its own Tailscale client</span> : null}
            {m.type === 'steam' ? <span className="muted small"> · Steam directory listing{viewer || !m.steam ? '' : `${m.steam.skipLatency ? ', no ICMP latency' : ', then an ICMP echo'}${m.steam.apiFailureIsDown ? ', API failures down' : ''} · API key hidden`}</span> : null}
            {m.type === 'pm2' ? <span className="muted small"> · read by each node from its own PM2 daemon{viewer || !m.pm2 ? null : <>{m.pm2.transport === 'unix' ? ' over a Unix socket' : m.pm2.transport === 'windows-pipe' ? ' over the Windows named pipe' : ` over “${m.pm2.transport}”`}{m.pm2.namespace ? <>, namespace <span className="mono wrap">{m.pm2.namespace}</span></> : null}</>}</span> : null}
            {m.type === 'real-browser' ? <span className="muted small"> · loaded in a dedicated Chromium service{viewer || !m.browser ? '' : `${m.browser.delayMs ? `, then waits ${span(m.browser.delayMs)}` : ''}${m.browser.screenshot ? ', with a private screenshot' : ''}${reveal ? '' : ` · endpoint${m.browser.bearerToken ? ' and token' : ''} hidden`}`}</span> : null}
            {m.type === 'gamedig' ? <span className="muted small"> · queried through a GameDig service{viewer || !m.gamedig ? null : <>: <span className="mono wrap">{m.gamedig.game}</span>{m.gamedig.port ? `, port ${m.gamedig.port}${m.gamedig.givenPortOnly ? ' only' : ''}` : ', default port'}{reveal ? '' : ` · service address and ${m.gamedig.token ? 'tokens' : 'token'} hidden`}</>}</span> : null}
            {m.type === 'http' ? <span className="muted small"> · accepts {acceptedStatusText(m)}</span> : null}
            {m.type === 'tcp' ? <span className="muted small"> · {m.tls ? 'with a verified TLS handshake' : 'connection only'}</span> : null}
            {m.type === 'dns' && m.dns?.recordType ? <span className="muted small"> · {m.dns.recordType} record{m.dns.server ? <> from <span className="mono">{m.dns.server}</span></> : ''}</span> : null}
            {m.type === 'websocket' && m.websocket?.subprotocols?.length ? <span className="muted small"> · offers <span className="mono wrap">{m.websocket.subprotocols.join(', ')}</span>{m.websocket.requireSubprotocol ? ', one required' : ''}</span> : null}
            {m.type === 'manual' ? <span className="muted small"> · status {m.manual?.state ?? 'not set'}{m.manual?.message ? `: ${m.manual.message}` : ''}</span> : null}
            {m.upsideDown ? <span className="muted small"> · inverted</span> : null}
            {options.length && !viewer ? (
              <details className="opts"><summary className="small">{options.join(', ')} options</summary>
                <pre className="json-pre small">{JSON.stringify(Object.fromEntries(options.map(key => [key, shownOption(key, m[key], reveal)])), null, 2)}</pre>
              </details>
            ) : null}
          </>
        );
      },
    },
    picked<Monitor>('schedule', 'Schedule', ['intervalMs', 'timeoutMs'], m => `Every ${span(m.intervalMs)} · timeout ${span(m.timeoutMs)}`),
    { key: 'retries', label: 'Retries', keys: ['retries', 'retryIntervalMs'], project: m => pick(m, ['retries', 'retryIntervalMs']), render: m => m.retries ? `${plural(m.retries, 'retry', 'retries')}${m.retryIntervalMs ? ` every ${span(m.retryIntervalMs)}` : ''}` : m.retryIntervalMs ? `None · failing checks every ${span(m.retryIntervalMs)}` : 'None' },
    { key: 'nodes', label: 'Run on', keys: ['nodeIds'], project: m => ({ nodeIds: candidateOrder(m.nodeIds) }), render: m => <span className="mono">{candidateOrder(m.nodeIds).join(', ')}</span> },
    { key: 'enabled', label: 'Active', keys: ['enabled'], project: m => ({ enabled: m.enabled }), render: m => m.enabled ? 'Active' : 'Paused' },
    {
      key: 'alerts', label: 'Destinations', keys: ['notificationIds', 'webhookUrl'], project: m => pick(m, ['notificationIds', 'webhookUrl']),
      render: m => {
        const channels = (m.notificationIds ?? []).map(id => ctx.channels.map.get(id)?.channel.name ?? `channel ${id.slice(0, 8)}`);
        const hook = viewer ? (m.webhookUrl ? 'webhook hidden' : '') : m.webhookUrl ? `webhook ${reveal ? m.webhookUrl : maskedUrl(m.webhookUrl)}` : '';
        const parts = [...channels, ...(hook ? [hook] : [])];
        return parts.length ? <span className="wrap">{parts.join(' · ')}</span> : <span className="muted">None</span>;
      },
    },
    { key: 'repeat', label: 'Repeat while down', keys: ['repeatAfterMs'], project: m => ({ repeatAfterMs: m.repeatAfterMs ?? 0 }), render: m => m.repeatAfterMs ? `Every ${span(m.repeatAfterMs)}` : 'Off' },
    picked<Monitor>('group', 'Group', ['groupId'], m => groupName(m.groupId)),
    { key: 'tags', label: 'Tags', keys: ['tags'], project: m => pick(m, ['tags']), render: m => m.tags?.length ? <MonitorTags tags={m.tags} catalog={ctx.tags.map} /> : <span className="muted">None</span> },
  ];
}

/**
 * The monitor resolver rows, for scripts/browser-pure.mjs. `monitorRows` keeps its own name unexported because the
 * contract harness appends `export { monitorRows }` to its review copy of this file.
 */
export const resolverModel = { monitorRows };

function maskOptions(value: unknown): unknown {
  if (typeof value !== 'object' || value === null) return value;
  if (Array.isArray(value)) return value.map(maskOptions);
  return Object.fromEntries(Object.entries(value).map(([key, inner]) => [key, secretNamed(key) ? MASK : maskOptions(inner)]));
}

function groupRows(ctx: Canary): Row<Group>[] {
  return [
    picked<Group>('name', 'Name', ['name'], g => g.name),
    { key: 'nodes', label: 'Nodes', keys: ['nodeIds'], project: g => ({ nodeIds: candidateOrder(g.nodeIds) }), render: g => <span className="mono">{candidateOrder(g.nodeIds).join(', ')}</span> },
    picked<Group>('dependency', 'Outage monitor', ['outageMonitorId'],
      g => g.outageMonitorId ? ctx.monitorMap.get(g.outageMonitorId)?.monitor.name ?? <span className="muted">missing monitor</span> : <span className="muted">None</span>),
    { key: 'grace', label: 'Grace period', keys: ['graceMs'], project: g => ({ graceMs: g.graceMs }), render: g => g.graceMs ? span(g.graceMs) : 'None' },
    // Parent links organize health only. A chosen parent must still exist without conflicts when this is saved.
    picked<Group>('parent', 'Parent group', ['parentId'], g => !g.parentId ? <span className="muted">None · top level</span>
      : g.parentId === g.id ? <span>Itself <span className="muted">(invalid; choose another)</span></span>
      : ctx.groupMap.has(g.parentId) ? groupPath(g.parentId, ctx.groupMap)
      : <span className="muted">missing group {g.parentId.slice(0, 8)}</span>),
  ];
}

function channelRows(viewer: boolean): Row<NotificationChannel>[] {
  return [
    picked<NotificationChannel>('name', 'Name', ['name'], c => c.name),
    { key: 'enabled', label: 'Enabled', keys: ['enabled'], project: c => ({ enabled: c.enabled }), render: c => c.enabled ? 'Enabled' : 'Disabled' },
    {
      key: 'config', label: 'Provider and destination', keys: ['config'], project: c => ({ config: structuredClone(c.config) }),
      render: c => {
        if (viewer) return <>{providerLabel(c.config.kind)} <span className="muted">· configuration hidden</span></>;
        const messaging = messagingRoute(c.config);
        const route = messaging ? [messaging.where, ...messaging.facts].join(' · ')
          : c.config.webhook?.url ? maskedUrl(c.config.webhook.url) : c.config.smtp?.server ? `${c.config.smtp.server}:${c.config.smtp.port}` : c.config.mailgun?.domain ?? c.config.sender?.email ?? '';
        return <>{providerLabel(c.config.kind)} · <span className="mono wrap">{route}</span>{c.config.recipients?.length ? ` · ${plural(c.config.recipients.length, 'recipient')}` : ''}{c.config.templates && Object.keys(c.config.templates).length ? ' · custom templates' : ''}</>;
      },
    },
  ];
}

function maintenanceRows(ctx: Canary): Row<Maintenance>[] {
  return [
    picked<Maintenance>('name', 'Name', ['name'], m => m.name),
    { key: 'enabled', label: 'Enabled', keys: ['enabled'], project: m => ({ enabled: m.enabled }), render: m => m.enabled ? 'Enabled' : 'Disabled' },
    {
      key: 'selection', label: 'Applies to', keys: ['allMonitors', 'monitorIds', 'groupIds'], project: m => pick(m, ['allMonitors', 'monitorIds', 'groupIds']),
      render: m => m.allMonitors ? 'All monitors' : andList([
        ...(m.monitorIds ?? []).map(id => ctx.monitorMap.get(id)?.monitor.name ?? `monitor ${id.slice(0, 8)}`),
        ...(m.groupIds ?? []).map(id => `group ${groupPath(id, ctx.groupMap)}`),
      ]),
    },
    { key: 'schedule', label: 'Schedule', keys: ['schedule'], project: m => ({ schedule: structuredClone(m.schedule) }), render: m => scheduleText(m.schedule) },
  ];
}

function statusPageRows(ctx: Canary): Row<StatusPage>[] {
  return [
    { key: 'page', label: 'Title and address', keys: ['slug', 'title', 'description'], project: p => ({ slug: p.slug, title: p.title, description: p.description }), render: p => <>{p.title} · <span className="mono">/status/{p.slug}</span>{p.description ? <div className="muted small">{p.description.slice(0, 160)}</div> : null}</> },
    { key: 'enabled', label: 'Published', keys: ['enabled'], project: p => ({ enabled: p.enabled }), render: p => p.enabled ? 'Published' : 'Draft' },
    {
      key: 'groups', label: 'Sections', keys: ['groups'], project: p => ({ groups: structuredClone(p.groups) }),
      // Selections are kept as stored, including the 24 h history opt-in and any field this dashboard doesn't know.
      render: p => <>{p.groups.map(group => `${group.name} (${group.monitors.map(sel => sel.showAvailability === true ? `${sel.name} + 24 h history` : sel.name).join(', ') || 'empty'})`).join('; ') || <span className="muted">None</span>}{p.groups.some(group => group.monitors.some(sel => !ctx.monitorMap.has(sel.monitorId))) ? <div className="muted small">Includes monitors unknown to {ctx.endpoint}</div> : null}</>,
    },
    { key: 'announcements', label: 'Announcements', keys: ['announcements'], project: p => ({ announcements: structuredClone(p.announcements) }), render: p => p.announcements.length ? p.announcements.map(note => `${note.title} (${note.state})`).join('; ') : <span className="muted">None</span> },
    {
      // Protocol 19 presentation: public values, kept together so a theme, its links and the feed come from one version.
      key: 'presentation', label: 'Appearance and feed', keys: ['appearance', 'publicUrl', 'rssEnabled'], project: p => pick(p, ['appearance', 'publicUrl', 'rssEnabled']),
      render: p => {
        const a = p.appearance;
        const parts = [
          a?.theme ? `${a.theme} theme` : '', a?.accentColor ? `accent ${a.accentColor}` : '', a?.logoUrl ? 'logo' : '', a?.footerText ? 'footer' : '',
          a?.refreshIntervalMs ? `refresh ${span(a.refreshIntervalMs)}` : '', a?.markdown ? 'Markdown' : '', p.publicUrl ? `public address ${p.publicUrl}` : '', p.rssEnabled ? 'RSS feed on' : '',
        ].filter(Boolean);
        return parts.length ? <span className="wrap">{parts.join(' · ')}</span> : <span className="muted">Defaults</span>;
      },
    },
  ];
}

function tagRows(): Row<TagResource>[] {
  return [
    picked<TagResource>('name', 'Name', ['name'], t => t.name),
    { key: 'color', label: 'Color', keys: ['color'], project: t => pick(t, ['color']), render: t => <><TagPill name={t.color} color={t.color} /></> },
  ];
}

const same = (a: unknown, b: unknown): boolean => JSON.stringify(a) === JSON.stringify(b);

type Kind = 'monitor' | 'group' | 'channel' | 'maintenance' | 'status page' | 'tag';

interface ResolverProps<T extends { revision: string; updatedAt: number; deleted?: boolean }> {
  kind: Kind;
  id: string;
  title: string;
  link: string;
  load: (signal: AbortSignal) => Promise<T[]>;
  rows: (reveal: boolean) => Row<T>[];
  save: (input: Record<string, unknown>, revisions: string[]) => Promise<{ revision: string }>;
  secrets?: boolean;
  onSaved?: () => void;
}

function Resolver<T extends { revision: string; updatedAt: number; deleted?: boolean }>({ kind, id, title, link, load, rows: makeRows, save, secrets, onSaved }: ResolverProps<T>) {
  const { api, editable, endpoint, overview, refresh, role, stillCurrent } = useCanary();
  const heads = useResource<T[]>(`${api.baseUrl}|${kind}|${id}`, load);
  const [choice, setChoice] = useState<Record<string, number>>({});
  const [reveal, setReveal] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const list = heads.data ? [...heads.data].sort((a, b) => a.revision.localeCompare(b.revision)) : [];
  const live = list.map((head, index) => ({ head, index })).filter(({ head }) => !head.deleted);
  const rows = withOtherFields(makeRows(reveal), live.map(({ head }) => head), reveal, role === 'viewer');

  const chosenFor = (row: Row<T>): number | undefined => {
    if (choice[row.key] !== undefined) return choice[row.key];
    const values = live.map(({ head }) => row.project(head));
    return live.length && values.every(value => same(value, values[0])) ? live[0]!.index : undefined;
  };
  const differing = rows.filter(row => !live.every(({ head }) => same(row.project(head), row.project(live[0]!.head))));
  const complete = live.length > 0 && rows.every(row => chosenFor(row) !== undefined);
  const disabled = !editable || saving;

  const submit = async (): Promise<void> => {
    if (!complete) return;
    const input = Object.assign({}, ...rows.map(row => row.project(list[chosenFor(row)!]!))) as Record<string, unknown>;
    setSaving(true); setFailure(null);
    try {
      const saved = await save(input, list.map(head => head.revision));
      if (!stillCurrent()) return;
      notify(`Resolved “${title}” on ${endpoint} as revision ${shortRev(saved.revision)}, replacing ${plural(list.length, 'version')}.`);
      refresh();
      onSaved?.();
      heads.reload();
    } catch (error) {
      if (!stillCurrent()) return;
      if (error instanceof ApiFailure && error.status === 409) {
        setFailure('Someone resolved this first, or another version arrived. The versions below were reloaded; review them again.');
        setChoice({});
        heads.reload();
      } else setFailure(describeFailure(error, { preferDetail: true }));
    } finally { setSaving(false); }
  };

  return (
    <Card
      title={<><Glyph tone="serious" /> <a href={link}>{title}</a> <span className="ttype">{kind}</span></>}
      hint={list.length ? `${plural(list.length, 'version')} known to ${endpoint}. Nothing is merged automatically.` : undefined}
      actions={<>
        {role !== 'viewer' && secrets ? <button type="button" className="btn sm" aria-pressed={reveal} onClick={() => setReveal(v => !v)}><Icon name={reveal ? 'eyeOff' : 'eye'} />{reveal ? 'Hide credentials' : 'Reveal credentials'}</button> : null}
        <button type="button" className="icon-btn sm" onClick={heads.reload} aria-label="Reload versions" data-tip="Reload versions"><Icon name="refresh" /></button>
      </>}
    >
      {heads.error && !heads.data ? <Banner tone="down" title="Versions could not be loaded">{describeFailure(heads.error)}</Banner>
        : !heads.data ? <p className="muted">Loading versions from {endpoint}…</p>
        : list.length < 2 ? <EmptyState title="Already resolved">This {kind} has a single version again. The list updates on the next refresh.</EmptyState>
        : (
          <>
            {failure ? <Banner tone="down" role="alert" title="Resolution not saved">{failure}</Banner> : null}
            {list.some(head => head.deleted) ? <p className="muted small"><Icon name="info" className="inline" /> One version deletes this {kind}. Resolving keeps a version you pick; delete it afterwards if deletion is what you want.</p> : null}
            <div className="tbl-wrap">
              <table className="tbl diff">
                <caption className="vh">Conflicting versions of {title}. Choose one value per row.</caption>
                <thead>
                  <tr>
                    <th scope="col">Field</th>
                    {list.map((head, index) => (
                      <th scope="col" key={head.revision}>
                        Version {index + 1} <Rev revision={head.revision} />
                        <span className="th-sub">saved <Time at={head.updatedAt} now={overview.now} />{head.deleted ? <> · <Tag>deleted</Tag></> : null}</span>
                        {editable && !head.deleted ? (
                          <button type="button" className="linkbtn small" disabled={disabled} onClick={() => setChoice(Object.fromEntries(rows.map(row => [row.key, index])))}>Use this version</button>
                        ) : null}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map(row => {
                    const chosen = chosenFor(row);
                    const differs = differing.includes(row);
                    return (
                      <tr key={row.key} className={differs ? 'differs' : 'agrees'}>
                        <th scope="row">{row.label}{differs ? <span className="tag t-accent">differs</span> : null}</th>
                        {list.map((head, index) => (
                          <td key={head.revision} className={chosen === index && differs ? 'ch' : undefined}>
                            {head.deleted ? <span className="muted">deleted</span> : differs ? (
                              <label>
                                <input type="radio" name={`${kind}-${id}-${row.key}`} checked={chosen === index} disabled={disabled} onChange={() => setChoice(c => ({ ...c, [row.key]: index }))} />
                                <span>{row.render(head)}</span>
                              </label>
                            ) : <span className="muted-val">{row.render(head)}</span>}
                          </td>
                        ))}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <div className="result">
              <span>
                {complete
                  ? <>Saving keeps {andList(differing.map(row => `${row.label.toLowerCase()} from version ${chosenFor(row)! + 1}`)) || 'the shared values'} and replaces all {list.length} versions ({list.map(head => shortRev(head.revision)).join(', ')}).</>
                  : <>Pick a value for {plural(differing.filter(row => chosenFor(row) === undefined).length, 'differing field')} to continue.</>}
              </span>
              {editable
                ? <button type="button" className="btn primary" disabled={!complete || saving} onClick={() => { void submit(); }}><Icon name="merge" />{saving ? `Saving to ${endpoint}…` : 'Save resolution'}</button>
                : <ReadOnly>Editors and admins resolve conflicts</ReadOnly>}
            </div>
          </>
        )}
    </Card>
  );
}

export function ConfigView() {
  const ctx = useCanary();
  const { assessed, groups, endpoint, api, channels, maintenance, tags, connection, role } = ctx;
  const pages = useResource<StatusPageView[]>(`${connection.identity}|status-pages`, signal => api.statusPages(signal), 30_000);
  const monitors = assessed.filter(({ view }) => view.configConflict).map(({ view }) => view.monitor);
  const conflictedGroups = (groups ?? []).filter(view => view.configConflict).map(view => view.group);
  const conflictedChannels = (channels.list ?? []).filter(view => view.configConflict).map(view => view.channel);
  const conflictedMaintenance = (maintenance ?? []).filter(view => view.configConflict).map(view => view.maintenance);
  const conflictedPages = (pages.data ?? []).filter(view => view.configConflict).map(view => view.statusPage);
  const conflictedTags = (tags.list ?? []).filter(view => view.configConflict).map(view => view.tag);
  const total = monitors.length + conflictedGroups.length + conflictedChannels.length + conflictedMaintenance.length + conflictedPages.length + conflictedTags.length;
  return (
    <div className="view">
      <PageHead
        title="Configuration"
        sub={<>Conflicting versions known to <span className="mono">{endpoint}</span>. They appear when nodes accept different edits while they can’t reach each other.</>}
      />
      {total === 0 ? (
        <Card>
          <EmptyState title="No configuration conflicts">Every monitor, group, channel, maintenance window, status page and tag known to {endpoint} has a single version.</EmptyState>
          <p className="foot-note center">Edits always name the revision they change, so concurrent edits are kept side by side here instead of overwriting each other.</p>
        </Card>
      ) : (
        <>
          <p className="prose">Pick one value per field, preview the result, and save. The resolution names every version it replaces; if another version arrives first, the node rejects it and you review again. Conflicting resources don’t act until resolved: conflicting maintenance holds nothing, and conflicting status pages aren’t published.</p>
          {monitors.map(monitor => (
            <Resolver<Monitor>
              key={`m-${monitor.id}`} kind="monitor" id={monitor.id} title={monitor.name} link={href('monitors', monitor.id)} secrets={role !== 'viewer'}
              load={signal => api.conflicts(monitor.id, signal)}
              rows={reveal => monitorRows(ctx, reveal)}
              save={(input, revisions) => api.resolveMonitor(monitor.id, { ...(input as unknown as MonitorInput), revisions })}
            />
          ))}
          {conflictedGroups.map(group => (
            <Resolver<Group>
              key={`g-${group.id}`} kind="group" id={group.id} title={group.name} link={href('groups')}
              load={signal => api.groupConflicts(group.id, signal)}
              rows={() => groupRows(ctx)}
              save={(input, revisions) => api.resolveGroup(group.id, { ...(input as unknown as GroupInput), revisions })}
            />
          ))}
          {conflictedChannels.map(channel => (
            <Resolver<NotificationChannel>
              key={`c-${channel.id}`} kind="channel" id={channel.id} title={channel.name} link={href('notifications')}
              load={signal => api.channelConflicts(channel.id, signal)}
              rows={() => channelRows(role === 'viewer')}
              save={(input, revisions) => api.resolveChannel(channel.id, { ...(input as unknown as NotificationChannelInput), revisions })}
              onSaved={channels.reload}
            />
          ))}
          {conflictedMaintenance.map(m => (
            <Resolver<Maintenance>
              key={`w-${m.id}`} kind="maintenance" id={m.id} title={m.name} link={href('maintenance')}
              load={signal => api.maintenanceConflicts(m.id, signal)}
              rows={() => maintenanceRows(ctx)}
              save={(input, revisions) => api.resolveMaintenance(m.id, { ...(input as unknown as MaintenanceInput), revisions })}
            />
          ))}
          {conflictedPages.map(page => (
            <Resolver<StatusPage>
              key={`p-${page.id}`} kind="status page" id={page.id} title={page.title} link={href('status-pages')}
              load={signal => api.statusPageConflicts(page.id, signal)}
              rows={() => statusPageRows(ctx)}
              save={(input, revisions) => api.resolveStatusPage(page.id, { ...(input as unknown as StatusPageInput), revisions })}
              onSaved={pages.reload}
            />
          ))}
          {conflictedTags.map(tag => (
            <Resolver<TagResource>
              key={`t-${tag.id}`} kind="tag" id={tag.id} title={tag.name} link={href('tags')}
              load={signal => api.tagConflicts(tag.id, signal)}
              rows={() => tagRows()}
              save={(input, revisions) => api.resolveTag(tag.id, { ...(input as unknown as TagInput), revisions })}
              onSaved={tags.reload}
            />
          ))}
        </>
      )}
    </div>
  );
}
