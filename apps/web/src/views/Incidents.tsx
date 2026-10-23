import { useMemo, useState, type ReactNode } from 'react';
import type { IncidentView, NotificationView } from '@uptime-canary/shared';
import { useCanary } from '../context';
import { andList, elapsed, plural, shortId, shortRev } from '../format';
import { candidateOrder, duplicateReceipts, incidentReceipts, receiptKindLabel, retired } from '../model';
import { href } from '../router';
import { Card, Chip, Dots, EmptyState, FilterChips, PageHead, StateChip, Tag, Time } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';
import { NotificationTabs } from './Channels';

function senders(receipts: NotificationView[], kind: NotificationView['kind']): NotificationView[] {
  return receipts.filter(receipt => receipt.kind === kind);
}

/** One plain line about what was delivered for an incident, as known to the endpoint. */
function DeliveryLine({ incident, receipts }: { incident: IncidentView; receipts: NotificationView[] }) {
  const { monitorMap, role, endpoint } = useCanary();
  const monitor = monitorMap.get(incident.monitorId)?.monitor;
  const parts: ReactNode[] = [];
  const describe = (kind: NotificationView['kind']): void => {
    const list = senders(receipts, kind);
    if (!list.length) return;
    const nodes = [...new Set(list.map(receipt => receipt.nodeId))];
    const first = list[0]!;
    if (kind === 'repeat') {
      parts.push(<span key={kind}>{plural(list.length, 'repeat')} sent</span>);
      return;
    }
    const destinations = new Set(list.map(receipt => receipt.destinationId ?? receipt.channelId ?? ''));
    const extra = list.length - destinations.size;
    parts.push(
      <span key={kind}>
        {receiptKindLabel[kind]} by <span className="mono">{andList(nodes)}</span> at <Time at={first.sentAt} mode="short" />
        {destinations.size > 1 ? ` to ${destinations.size} destinations` : ''}
        {extra > 0 ? <> <Tag tone="accent">{extra === 1 ? '1 duplicate' : `${extra} duplicates`}</Tag></> : null}
      </span>,
    );
  };
  describe('down');
  describe('repeat');
  describe('recovery');
  if (!parts.length) {
    // A held-back alert is explained by the suppression line below.
    if (incident.suppressedByGroupId) return null;
    if (role !== 'viewer' && monitor && !monitor.webhookUrl && !monitor.notificationIds?.length) {
      parts.push(<span key="none">No destination configured. Recorded without notifications.</span>);
    } else if (incident.maintenanceComplete) {
      parts.push(<span key="maint">The whole outage happened during planned maintenance, so destinations that weren’t notified aren’t sent a stale outage and recovery.</span>);
    } else if (incident.resolvedAt === null) {
      parts.push(<span key="pending">No alert receipt known to <span className="mono">{endpoint}</span> yet</span>);
    } else {
      parts.push(<span key="none">No alert receipt known to <span className="mono">{endpoint}</span></span>);
    }
  }
  return (
    <div className="d">
      <Icon name="bell" />
      <span>{parts.map((part, index) => <span key={index}>{index ? <span aria-hidden="true" className="sep"> · </span> : null}{part}</span>)}</span>
    </div>
  );
}

export function SuppressionLine({ groupId, reason }: { groupId?: string; reason?: string }) {
  const { groupMap } = useCanary();
  if (!groupId) return null;
  const name = groupMap.get(groupId)?.group.name;
  return (
    <div className="d hold">
      <Icon name="groups" />
      <span>
        Notifications held back by group {name ? <a href={href('groups')}><b>{name}</b></a> : <span className="mono">{shortId(groupId)}</span>}
        {reason ? <>: {reason}</> : null}. Checks and incidents are still recorded.
      </span>
    </div>
  );
}

/** Planned work held this incident's alerts; the measured outage is still recorded. */
function MaintenanceLine({ incident }: { incident: IncidentView }) {
  const { maintenanceMap } = useCanary();
  if (!incident.maintenanceIds?.length) return null;
  const names = incident.maintenanceIds.map(id => maintenanceMap.get(id)?.maintenance.name ?? `schedule ${shortId(id)}`);
  return (
    <div className="d hold">
      <Icon name="wrench" />
      <span>
        {incident.maintenanceComplete ? 'Entirely inside planned maintenance' : 'Overlapped planned maintenance'} (<a href={href('maintenance')}>{names.join(', ')}</a>).
        {incident.maintenanceComplete ? ' Alerts that were never sent stay unsent; any already delivered keep their recovery.' : ' Alerts were held during the window; owed ones go out after it.'}
      </span>
    </div>
  );
}

/** A retired incident ended because its check changed, not because anything came back up. */
function RetirementLine({ incident, receipts }: { incident: IncidentView; receipts: NotificationView[] }) {
  if (!retired(incident)) return null;
  const reason = incident.retirementReason?.trim().replace(/\.$/u, '') || 'What this monitor checks changed, or it was paused or deleted';
  return (
    <div className="d">
      <Icon name="config" />
      <span>
        {reason}. Closed by config revision <span className="mono" title={incident.retiredBy}>{shortRev(incident.retiredBy!)}</span>
        {receipts.some(receipt => receipt.kind === 'recovery') ? null : <>; no recovery alert is sent</>}.
      </span>
    </div>
  );
}

export function IncidentRow({ incident }: { incident: IncidentView }) {
  const { overview, monitorMap, assessmentOf } = useCanary();
  const view = monitorMap.get(incident.monitorId);
  const a = assessmentOf(incident.monitorId);
  const open = incident.resolvedAt === null;
  const closedByChange = retired(incident);
  const receipts = incidentReceipts(incident, overview.notifications);
  const name = view?.monitor.name ?? incident.monitorName;
  const title = open ? (a?.kind === 'unknown' ? `${name}: failing location out of view` : `${name} down`) : `${name} was down`;
  return (
    <li className="inc">
      <Glyph tone={open ? (a?.tone === 'up' ? 'unknown' : a?.tone ?? 'down') : closedByChange ? 'none' : 'up'} />
      <div>
        <div className="t">
          {view ? <a href={href('monitors', incident.monitorId)}>{title}</a> : <span>{title} <span className="muted">(monitor deleted)</span></span>}
          {open ? (a ? <StateChip a={a} /> : <Chip tone="down" label="Open" />) : closedByChange ? <Chip tone="none" label="Closed after config change" /> : <Chip tone="up" label="Recovered" />}
          {open && view && a ? <Dots view={view} a={a} /> : null}
        </div>
        <div className="m">
          {open
            ? <>Opened <Time at={incident.openedAt} now={overview.now} /> · open {elapsed(overview.now - incident.openedAt)}</>
            : <><Time at={incident.openedAt} now={overview.now} />–<Time at={incident.resolvedAt!} now={overview.now} /> · {closedByChange ? 'open ' : ''}{elapsed(incident.resolvedAt! - incident.openedAt)}</>}
          <span className="muted"> · incident <span className="mono" title={incident.id}>{shortId(incident.id)}</span></span>
        </div>
        <RetirementLine incident={incident} receipts={receipts} />
        <MaintenanceLine incident={incident} />
        <DeliveryLine incident={incident} receipts={receipts} />
        <SuppressionLine groupId={incident.suppressedByGroupId} reason={incident.suppressionReason} />
      </div>
    </li>
  );
}

type IncidentFilter = 'open' | 'closed' | 'all';

export function IncidentsView() {
  const { overview, endpoint, search, monitorMap } = useCanary();
  const [filter, setFilter] = useState<IncidentFilter>('open');
  const q = search.trim().toLowerCase();
  const matching = overview.incidents.filter(incident => !q || `${incident.monitorName} ${monitorMap.get(incident.monitorId)?.monitor.target ?? ''} ${incident.id}`.toLowerCase().includes(q));
  const open = matching.filter(incident => incident.resolvedAt === null).sort((a, b) => a.openedAt - b.openedAt);
  const closed = matching.filter(incident => incident.resolvedAt !== null).sort((a, b) => b.resolvedAt! - a.resolvedAt!);
  const shown = filter === 'open' ? open : filter === 'closed' ? closed : [...open, ...closed];
  return (
    <div className="view">
      <PageHead
        title="Incidents"
        sub={<>Incidents known to <span className="mono">{endpoint}</span>, merged by monitor across nodes. Any fresh failure opens one; recovery needs a fresh up from every assigned node. Changing what a monitor checks, pausing or deleting it closes its incident without a recovery.</>}
      />
      <Card>
        <FilterChips<IncidentFilter> label="Show incidents" value={filter} onChange={setFilter} options={[
          { value: 'open', label: 'Open', count: open.length },
          { value: 'closed', label: 'Closed', count: closed.length },
          { value: 'all', label: 'All', count: matching.length },
        ]} />
        {shown.length
          ? <ul className="inc-list">{shown.map(incident => <IncidentRow key={incident.id} incident={incident} />)}</ul>
          : <EmptyState title={filter === 'open' ? 'No open incidents' : 'Nothing here'}>{q ? 'Nothing matches the search.' : filter === 'open' && closed.length ? `Every incident known to ${endpoint} is closed.` : `${endpoint} has no incidents in this list.`}</EmptyState>}
      </Card>
    </div>
  );
}

export function ReceiptList({ receipts, limit }: { receipts: NotificationView[]; limit?: number }) {
  const { overview, monitorMap } = useCanary();
  const duplicates = useMemo(() => duplicateReceipts(overview.notifications), [overview.notifications]);
  const shown = limit ? receipts.slice(0, limit) : receipts;
  if (!shown.length) return <EmptyState title="No notifications yet">Sends appear here once a node gets an alert through.</EmptyState>;
  return (
    <ul className="evlog">
      {shown.map(receipt => {
        const monitor = monitorMap.get(receipt.monitorId)?.monitor;
        const duplicate = duplicates.has(receipt.id);
        return (
          <li key={receipt.id}>
            <Time at={receipt.sentAt} now={overview.now} mode="short" />
            <span className={`ev-ic ${receipt.kind === 'recovery' ? 'ev-ok' : duplicate ? 'ev-dup' : 'ev-send'}`} aria-hidden="true" />
            <span className="tx">
              {receiptKindLabel[receipt.kind]} · {monitor ? <a href={href('monitors', receipt.monitorId)}>{monitor.name}</a> : <span className="muted">deleted monitor</span>}
              <span className="det">sent by <span className="mono">{receipt.nodeId}</span>{duplicate ? <> · <Tag tone="accent">duplicate</Tag></> : null}</span>
            </span>
          </li>
        );
      })}
    </ul>
  );
}

type ReceiptFilter = 'all' | 'down' | 'repeat' | 'recovery' | 'duplicate';

export function NotificationsView() {
  const { overview, endpoint, monitorMap, search, nodeMap } = useCanary();
  const [filter, setFilter] = useState<ReceiptFilter>('all');
  const duplicates = useMemo(() => duplicateReceipts(overview.notifications), [overview.notifications]);
  const q = search.trim().toLowerCase();
  const all = [...overview.notifications].sort((a, b) => b.sentAt - a.sentAt)
    .filter(receipt => !q || `${monitorMap.get(receipt.monitorId)?.monitor.name ?? ''} ${receipt.nodeId} ${receipt.incidentId}`.toLowerCase().includes(q));
  const keep = (receipt: NotificationView): boolean => filter === 'all' || (filter === 'duplicate' ? duplicates.has(receipt.id) : receipt.kind === filter);
  const rows = all.filter(keep);
  const outOfView = [...nodeMap.values()].filter(node => node.state === 'unreachable').map(node => node.id);
  return (
    <div className="view">
      <PageHead title="Notifications" sub={<>Successful sends (receipts) known to <span className="mono">{endpoint}</span>, for every destination.</>} />
      <NotificationTabs current="receipts" />
      <div className="guar">
        <div><b><Icon name="send" />Usually one</b><p>Assigned nodes take turns in ID order. The first that gets an alert through records a receipt, and the others stand down when they see it.</p></div>
        <div><b><Icon name="branch" />Duplicates are possible</b><p>During a partition each side may send, and a crash right after sending can repeat a send. Each alert carries an <span className="mono">Idempotency-Key</span>, which helps compatible receivers but can’t catch every case.</p></div>
        <div><b><Icon name="merge" />Reconciled afterwards</b><p>Receipts and incidents merge when nodes reconnect. Alerts that were already delivered are not sent again.</p></div>
      </div>
      <Card
        title="Receipts"
        hint={outOfView.length ? <>Sends by {andList(outOfView)} (out of view) appear here after they reconnect.</> : 'Newest first.'}
      >
        <FilterChips<ReceiptFilter> label="Filter receipts" value={filter} onChange={setFilter} options={[
          { value: 'all', label: 'All', count: all.length },
          { value: 'down', label: 'Down alerts', count: all.filter(r => r.kind === 'down').length },
          { value: 'repeat', label: 'Repeats', count: all.filter(r => r.kind === 'repeat').length },
          { value: 'recovery', label: 'Recoveries', count: all.filter(r => r.kind === 'recovery').length },
          { value: 'duplicate', label: 'Duplicates', count: all.filter(r => duplicates.has(r.id)).length },
        ]} />
        {rows.length ? (
          <div className="tbl-wrap">
            <table className="tbl stack">
              <caption className="vh">Notification receipts</caption>
              <thead><tr><th scope="col">Sent</th><th scope="col">Kind</th><th scope="col">Monitor</th><th scope="col">Sent by</th><th scope="col">Incident</th></tr></thead>
              <tbody>
                {rows.map(receipt => {
                  const monitor = monitorMap.get(receipt.monitorId)?.monitor;
                  const order = monitor ? candidateOrder(monitor.nodeIds) : [];
                  const rank = order.indexOf(receipt.nodeId);
                  return (
                    <tr key={receipt.id}>
                      <td data-label="Sent" className="tn"><Time at={receipt.sentAt} now={overview.now} /></td>
                      <td data-label="Kind">
                        <span className="kind"><span className={`ev-ic ${receipt.kind === 'recovery' ? 'ev-ok' : 'ev-send'}`} aria-hidden="true" />{receiptKindLabel[receipt.kind]}</span>
                        {duplicates.has(receipt.id) ? <> <Tag tone="accent">duplicate</Tag></> : null}
                      </td>
                      <td data-label="Monitor" className="lead">{monitor ? <a href={href('monitors', receipt.monitorId)}>{monitor.name}</a> : <span className="muted">deleted monitor</span>}</td>
                      <td data-label="Sent by"><span className="mono">{receipt.nodeId}</span>{rank >= 0 ? <span className="muted"> · candidate {rank + 1} of {order.length}</span> : null}</td>
                      <td data-label="Incident"><span className="mono" title={receipt.incidentId}>{shortId(receipt.incidentId)}</span></td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : <EmptyState title="No receipts here">{filter === 'duplicate' ? 'No duplicate down or recovery sends are known.' : 'Nothing has been sent that matches this filter.'}</EmptyState>}
        <p className="foot-note">Repeat receipts don’t say which repeat they were, so repeats are never marked as duplicates. Failed, retrying and held sends are under <a href={href('notifications', 'deliveries')}>Delivery attempts</a>.</p>
      </Card>
    </div>
  );
}
