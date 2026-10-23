import { useState, type ReactNode } from 'react';
import type { MonitorView, Observation } from '@uptime-canary/shared';
import { useCanary } from '../context';
import { ago, andList, latencyOf, plural, stamp } from '../format';
import { isPending, matchesFilter, matchesSearch, observationFor, outOfView, receiptKey, retired, type Assessed, type NodeColumn, type StateFilter, type Tone } from '../model';
import { href, navigate } from '../router';
import { Card, EmptyState, FilterChips, PageHead, StateChip, Tag, TypeTag, nodeStateText, nodeTone } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';
import { IncidentRow, ReceiptList } from './Incidents';
import { MaintenanceTag } from './Maintenance';

function sentence({ view, a }: Assessed, groupName: (id?: string) => string | undefined): ReactNode {
  const name = <b>{view.monitor.name}</b>;
  const held = view.suppressedByGroupId ? <> Its alert is held back by group <b>{groupName(view.suppressedByGroupId) ?? 'unknown'}</b>.</> : null;
  switch (a.kind) {
    case 'everywhere': return <>{name} is down {a.total === 1 ? <>from <span className="mono">{a.failing[0]}</span></> : 'everywhere'}.{held}</>;
    case 'partial': return <>{name} is down from {a.failing.length === 1 ? <><span className="mono">{a.failing[0]}</span> only</> : `${a.failing.length} of ${a.total} locations`}{a.unknown.length ? ` (${a.unknown.length} unknown)` : ''}.{held}</>;
    case 'conflict': return <>{name} has a configuration conflict to resolve.</>;
    case 'pending': return <>{name} is pending from {a.pending.length === 1 ? <span className="mono">{a.pending[0]}</span> : `${a.pending.length} of ${a.total} locations`}.</>;
    default: return <>{name} has no fresh result from {a.unknown.length === 1 ? <span className="mono">{a.unknown[0]}</span> : `${a.unknown.length} of ${a.total} locations`}.</>;
  }
}

function Kpi({ label, tone, value, of, sub }: { label: string; tone?: Tone; value: number; of?: number; sub: ReactNode }) {
  return (
    <div className="kpi">
      <div className="l">{tone ? <Glyph tone={tone} /> : null}{label}</div>
      <div className="v">{value}{of !== undefined ? <small> of {of}</small> : null}</div>
      <div className="s">{sub}</div>
    </div>
  );
}

function cellText(view: MonitorView, node: NodeColumn, observation: Observation | undefined, endpoint: string, now: number): { tone: Tone; label: string; tip: string } {
  const head = `${node.id}${node.name !== node.id ? ` · ${node.name}` : ''}`;
  if (!view.monitor.nodeIds.includes(node.id)) return { tone: 'none', label: `${node.id}: not assigned`, tip: `${head}\nNot assigned. This node doesn’t run ${view.monitor.name}.` };
  if (view.state === 'paused') return { tone: 'paused', label: `${node.id}: paused`, tip: `${head}\nPaused` };
  const checked = observation && observation.checkedAt > 0 ? `${stamp(observation.checkedAt, now)} (${ago(observation.checkedAt, now)})` : null;
  const away = outOfView(node) ? `\n${endpoint} can’t reach ${node.id}; it keeps monitoring on its own.` : '';
  if (observation?.state === 'down') return { tone: 'down', label: `${node.id}: down, ${observation.message}`, tip: `${head}\nDown · ${observation.message}\nChecked ${checked ?? 'unknown'}${away}` };
  if (observation?.state === 'up') {
    const measured = latencyOf(observation) === '—' ? 'no measured latency' : latencyOf(observation);
    return { tone: 'up', label: `${node.id}: up, ${measured}`, tip: `${head}\nUp · ${measured}\nChecked ${checked ?? 'unknown'}${away}` };
  }
  if (isPending(observation?.state)) return { tone: 'pending', label: `${node.id}: pending, ${observation!.message}`, tip: `${head}\nPending (neither up nor down) · ${observation!.message}\nChecked ${checked ?? 'unknown'}${away}` };
  const why = observation?.message || 'No fresh observation';
  return {
    tone: 'unknown',
    label: `${node.id}: unknown${outOfView(node) ? ', not in view' : ''}, ${why}`,
    tip: `${head}\nUnknown · ${why}${checked ? `\nLast check ${checked}` : ''}${away}`,
  };
}

export function StatusBoard({ title = 'Status by location' }: { title?: string }) {
  const { assessed, nodes, endpoint, overview, search, groupMap } = useCanary();
  const [filter, setFilter] = useState<StateFilter>('all');
  const searched = assessed.filter(({ view }) => matchesSearch(view, search, groupMap.get(view.monitor.groupId ?? '')?.group.name));
  const rows = searched.filter(({ a }) => matchesFilter(a, filter));
  const count = (f: StateFilter): number => searched.filter(({ a }) => matchesFilter(a, f)).length;
  return (
    <Card title={title} hint={<>Latest fresh result from each node, as known to <span className="mono">{endpoint}</span>. History stays on the node that measured it.</>}>
      <FilterChips<StateFilter> label="Filter monitors by state" value={filter} onChange={setFilter} options={[
        { value: 'all', label: 'All', count: count('all') },
        { value: 'failing', label: 'Failing', count: count('failing') },
        { value: 'unknown', label: 'Unknown', count: count('unknown') },
        { value: 'up', label: 'Up', count: count('up') },
        { value: 'paused', label: 'Paused', count: count('paused') },
      ]} />
      <div className="mx-wrap">
        <table className="mx">
          <caption className="vh">Monitors by node. Rows are monitors, columns are nodes in candidate order.</caption>
          <thead>
            <tr>
              <th scope="col" className="l">Monitor</th>
              <th scope="col" className="l st-col">State</th>
              {nodes.map(node => (
                <th scope="col" key={node.id} className={node.id === endpoint ? 'ep' : undefined}>
                  <a className={`nh${node.id === endpoint ? ' ep' : ''}`} href={href('nodes')} data-tip={`${node.id}${node.name !== node.id ? ` · ${node.name}` : ''}\n${nodeStateText(node, endpoint, overview.now)}`}>
                    <Glyph tone={nodeTone(node)} />
                    <span className="mono">{node.id}</span>
                    <span className="vh">, {nodeStateText(node, endpoint, overview.now)}</span>
                  </a>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map(({ view, a }) => (
              <tr key={view.monitor.id} className={a.kind === 'up' || a.kind === 'paused' ? 'ok' : undefined} onClick={event => {
                if ((event.target as HTMLElement).closest('a, button')) return;
                navigate(href('monitors', view.monitor.id));
              }}>
                <th scope="row" className="mon">
                  <a href={href('monitors', view.monitor.id)}>{view.monitor.name}</a><TypeTag type={view.monitor.type} />
                  <span className="tgt">{view.monitor.target}</span>
                  {view.suppressedByGroupId || view.maintenanceIds?.length ? <span className="hold-line">{view.suppressedByGroupId ? <Tag icon="groups" tone="hold">alerts held</Tag> : null} <MaintenanceTag ids={view.maintenanceIds} /></span> : null}
                  <span className="st-inline"><StateChip a={a} /></span>
                </th>
                <td className="st"><StateChip a={a} /></td>
                {nodes.map(node => {
                  const observation = observationFor(view, node.id);
                  const cell = cellText(view, node, observation, endpoint, overview.now);
                  const away = view.monitor.nodeIds.includes(node.id) && outOfView(node);
                  return (
                    <td key={node.id} className={`c${away ? ' out' : ''}`} data-node={node.id} data-tip={cell.tip}>
                      <Glyph tone={cell.tone} label={cell.label} />
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
        {!rows.length ? <EmptyState title={assessed.length ? 'Nothing matches' : 'No monitors yet'}>{assessed.length ? 'Try another filter or search.' : 'Monitors you add appear here with one column per node.'}</EmptyState> : null}
      </div>
      <div className="legend" aria-label="Legend">
        <span><Glyph tone="up" />Up</span><span><Glyph tone="down" />Down</span><span><Glyph tone="serious" />Config conflict</span>
        <span><Glyph tone="unknown" />Unknown</span>{assessed.some(({ a }) => a.pending.length) ? <span><Glyph tone="pending" />Pending</span> : null}<span><i className="sw out" />Out of view</span><span><Glyph tone="paused" />Paused</span><span><Glyph tone="none" />Not assigned</span>
      </div>
    </Card>
  );
}

/** With nothing open, say how the known incidents ended. A config change is not a recovery. */
function closedQuietly(total: number, retiredCount: number, endpoint: string): string {
  if (!total) return `${endpoint} knows of no incidents.`;
  if (!retiredCount) return `Every incident known to ${endpoint} has recovered.`;
  if (retiredCount === total) return `Every incident known to ${endpoint} was closed after a config change, without a recovery.`;
  return `Every incident known to ${endpoint} is closed: ${total - retiredCount} recovered, ${retiredCount} closed after a config change.`;
}

export function OverviewView() {
  const { overview, assessed, nodes, endpoint, editable, groups, groupMap, connection, maintenance } = useCanary();
  const down = assessed.filter(({ a }) => a.kind === 'everywhere' || a.kind === 'partial');
  const everywhere = down.filter(({ a }) => a.kind === 'everywhere');
  const unknown = assessed.filter(({ a }) => a.kind === 'unknown' || a.kind === 'conflict');
  const conflicts = assessed.filter(({ a }) => a.kind === 'conflict').length;
  const up = assessed.filter(({ a }) => a.kind === 'up').length;
  const paused = assessed.filter(({ a }) => a.kind === 'paused').length;
  const pending = assessed.filter(({ a }) => a.kind === 'pending').length;
  const peers = nodes.filter(node => node.id !== endpoint && node.state !== 'unlisted');
  const peersIn = peers.filter(node => node.state === 'connected').length;
  const out = nodes.filter(node => node.state === 'unreachable');
  const dayAgo = overview.now - 86_400_000;
  const recent = overview.notifications.filter(receipt => receipt.sentAt >= dayAgo);
  const duplicateKeys = new Map<string, number>();
  for (const receipt of recent) if (receipt.kind !== 'repeat') duplicateKeys.set(receiptKey(receipt), (duplicateKeys.get(receiptKey(receipt)) ?? 0) + 1);
  const duplicateSends = [...duplicateKeys.values()].filter(n => n > 1).length;
  const holding = (groups ?? []).filter(view => view.suppressionActive);
  const windows = (maintenance ?? []).filter(view => view.active && view.maintenance.enabled && !view.configConflict);
  const inMaintenance = assessed.filter(({ view }) => view.maintenanceIds?.length).length;

  const problems = assessed.filter(({ a }) => a.kind === 'everywhere' || a.kind === 'partial' || a.kind === 'conflict' || a.kind === 'unknown' || a.kind === 'pending');
  const groupName = (id?: string): string | undefined => id ? groupMap.get(id)?.group.name : undefined;
  const open = overview.incidents.filter(incident => incident.resolvedAt === null).sort((a, b) => a.openedAt - b.openedAt);
  const receipts = [...overview.notifications].sort((a, b) => b.sentAt - a.sentAt);

  return (
    <div className="view">
      <PageHead
        title="Overview"
        sub={<>{connection.link === 'live' ? 'Everything' : 'The last data'} <span className="mono">{endpoint}</span> {connection.link === 'live' ? 'knows right now' : 'reported'} · {plural(assessed.length, 'monitor')} on {plural(nodes.length, 'node')}</>}
      />
      <Card className="situation">
        <h2 className="vh">Situation</h2>
        {assessed.length === 0 ? (
          <div className="quiet">
            <p className="headline">No monitors yet.</p>
            <p className="sec">{editable ? 'Add a check (HTTP, ping, databases, queues and 20 more), choose which nodes run it, and pick where alerts go.' : 'An editor or admin can add monitors. You can watch them here once they exist.'}</p>
            {editable ? <a className="btn primary" href={href('monitors', 'new')}><Icon name="plus" />New monitor</a> : null}
          </div>
        ) : problems.length ? (
          <p className="headline">
            {problems.slice(0, 3).map((item, index) => <span key={item.view.monitor.id}>{index ? ' ' : ''}{sentence(item, groupName)}</span>)}
            {problems.length > 3 ? <span className="q"> +{problems.length - 3} more</span> : null}
          </p>
        ) : (
          <p className="headline">All quiet. Every canary is singing.</p>
        )}
        {assessed.length ? (
          <div className="kpis">
            <Kpi label="Down" tone={down.length ? 'down' : 'up'} value={down.length} sub={down.length ? `${everywhere.length} everywhere · ${down.length - everywhere.length} partial` : 'No outages'} />
            <Kpi label="Unknown" tone={unknown.length ? 'unknown' : undefined} value={unknown.length} sub={`${plural(conflicts, 'config conflict')}${out.length ? ` · ${plural(out.length, 'node')} out of view` : ''}`} />
            <Kpi label="Up" tone={up ? 'up' : undefined} value={up} of={assessed.length} sub={`${paused} paused${pending ? ` · ${pending} pending` : ''}`} />
            {peers.length
              ? <Kpi label="Peers in view" tone={peersIn === peers.length ? 'up' : 'warn'} value={peersIn} of={peers.length} sub={<>as seen from <span className="mono">{endpoint}</span></>} />
              : <Kpi label="Peers" value={0} sub="Standalone node" />}
            <Kpi label="Notifications · 24 h" value={recent.length} sub={duplicateSends ? `${plural(duplicateSends, 'duplicate send')}` : 'No duplicates seen'} />
            {maintenance && maintenance.length ? <Kpi label="Maintenance" tone={windows.length ? 'warn' : undefined} value={windows.length} of={maintenance.length} sub={windows.length ? `holding alerts for ${plural(inMaintenance, 'monitor')}` : 'No window active'} /> : null}
            {groups && groups.length ? <Kpi label="Groups holding alerts" tone={holding.length ? 'warn' : undefined} value={holding.length} of={groups.length} sub={holding.length ? andList(holding.map(view => view.group.name)) : 'None suppressing'} /> : null}
          </div>
        ) : null}
      </Card>
      {assessed.length ? <StatusBoard /> : null}
      <div className="g2">
        <Card title="Open incidents" hint="Merged by monitor across nodes" actions={<a className="btn sm" href={href('incidents')}>All incidents<Icon name="chevRight" /></a>}>
          {open.length
            ? <ul className="inc-list">{open.map(incident => <IncidentRow key={incident.id} incident={incident} />)}</ul>
            : <EmptyState title="No open incidents">{closedQuietly(overview.incidents.length, overview.incidents.filter(retired).length, endpoint)}</EmptyState>}
        </Card>
        <div className="vstack">
          <Card title="Nodes" hint={<>As seen from <span className="mono">{endpoint}</span></>} actions={<a className="btn sm" href={href('nodes')}>Details<Icon name="chevRight" /></a>}>
            <div className="nodes-mini">
              {nodes.map(node => (
                <a key={node.id} className={`nm${outOfView(node) ? ' out' : ''}`} href={href('nodes')} data-tip={`${node.id} · ${node.name}\n${nodeStateText(node, endpoint, overview.now)}`}>
                  <Glyph tone={nodeTone(node)} />
                  <span>
                    <b>{node.id}</b>
                    <span>{node.state === 'local' ? 'endpoint' : node.state === 'connected' ? 'connected' : node.state === 'unlisted' ? 'not a peer' : node.lastSeenAt ? `seen ${ago(node.lastSeenAt, overview.now)}` : 'never reached'}</span>
                  </span>
                </a>
              ))}
            </div>
          </Card>
          <Card title="Recent notifications" hint={<>Receipts known to <span className="mono">{endpoint}</span></>} actions={<a className="btn sm" href={href('notifications')}>All<Icon name="chevRight" /></a>}>
            <ReceiptList receipts={receipts} limit={5} />
          </Card>
        </div>
      </div>
    </div>
  );
}

