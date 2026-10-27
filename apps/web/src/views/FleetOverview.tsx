import { Fragment, useState, type ReactNode } from 'react';
import { nodeStateInfo, shownNodeId } from '../dashboard';
import { FLEET_SNAPSHOT_MAX_AGE_MS, fleetSourceCurrent, type FleetMonitorRow, type FleetResult, type FleetSource } from '../fleet';
import type { FleetOverviewProps } from '../fleetViews';
import { ago, elapsed, hasLatency, latencyOf, plural, utc } from '../format';
import { matchesFilter, stateTone, stateWord, type StateFilter, type Tone } from '../model';
import { Banner, Card, EmptyState, FilterChips, PageHead, Rev, Spinner, StateChip, Tag, TypeTag } from '../ui/bits';
import { Glyph, Icon, type IconName } from '../ui/icons';

/**
 * Every monitor on every node registered with this dashboard, drawn from root's fleet rows and sources only. Reading a
 * registration is transport: a snapshot says whether it's current, and only per-node results carry monitoring health.
 * A result from a snapshot that is no longer current never shows as healthy, and a result one HA peer reported for
 * another names the registration that reported it. Targets, connection settings and raw read errors are never shown.
 */

const validTime = (ms: number | null | undefined): ms is number => typeof ms === 'number' && Number.isFinite(ms) && ms > 0 && ms < 8.64e15;

/** The HTTP status of a failed read, if it carries one. Nothing else from the error is ever displayed. */
function statusOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null || !('status' in error)) return undefined;
  const status = (error as { status: unknown }).status;
  return typeof status === 'number' && Number.isInteger(status) ? status : undefined;
}

/** Why a registration couldn't be read, in the dashboard's words. The registry's own state comes first. */
function readProblem(source: FleetSource): string {
  if (source.node.state !== 'answering' && source.node.state !== 'starting') return `${nodeStateInfo(source.node).label}.`;
  const status = statusOf(source.error);
  if (status === 502) return 'The dashboard’s server got no valid answer from this node.';
  if (status === 503 || status === 504) return 'The node didn’t answer in time.';
  if (status === 409) return 'The dashboard’s server isn’t reading this node right now.';
  if (status === 404) return 'This registration is no longer on the dashboard.';
  if (status === 429) return 'The dashboard is limiting reads for now.';
  if (status !== undefined && status >= 500) return 'The dashboard’s server couldn’t read this node.';
  if (status === undefined && source.error !== undefined) return 'The browser couldn’t complete the read.';
  return 'The last read didn’t succeed.';
}

type SnapshotKind = 'current' | 'reading' | 'refreshing' | 'stale' | 'cached' | 'unavailable' | 'held';
interface Snapshot { kind: SnapshotKind; icon: IconName; label: string; text: string }

function snapshotOf(source: FleetSource, now: number): Snapshot {
  const read = validTime(source.readAt) ? ago(source.readAt, now) : null;
  const kept = source.overview && read ? ` Its last snapshot was read ${read}; those results count as unknown.` : '';
  if (fleetSourceCurrent(source, now)) return { kind: 'current', icon: 'eye', label: 'Current', text: `Read ${read ?? 'recently'}` };
  switch (source.status) {
    case 'loading':
      return source.overview
        ? { kind: 'refreshing', icon: 'refresh', label: 'Reading again', text: `Not current until this read finishes.${kept}` }
        : { kind: 'reading', icon: 'refresh', label: 'Reading…', text: 'No snapshot from this node yet.' };
    case 'pending':
      return { kind: 'held', icon: 'lock', label: nodeStateInfo(source.node).label, text: `The dashboard doesn’t read it until this change finishes.${kept}` };
    case 'unavailable':
      return source.overview
        ? { kind: 'cached', icon: 'clock', label: 'Unavailable · cached', text: `${readProblem(source)}${kept}` }
        : { kind: 'unavailable', icon: 'plug', label: 'Unavailable', text: readProblem(source) };
    case 'ready':
      return { kind: 'stale', icon: 'clock', label: 'Not current', text: `${read ? `Read ${read}, older` : 'Older'} than ${elapsed(FLEET_SNAPSHOT_MAX_AGE_MS)}, so its results count as unknown.` };
  }
}

interface ResultShape { tone: Tone; word: string; detail: string; out: boolean }

/** Shown as reported, except that a result whose snapshot is no longer current is never shown as up, down or paused. */
function resultShape(result: FleetResult, reporter: FleetSource | undefined, now: number): ResultShape {
  const stale = result.registrationId !== undefined && (!reporter || !fleetSourceCurrent(reporter, now));
  const out = stale || result.registrationId === undefined;
  if (stale && result.state !== 'unknown') {
    return { tone: 'unknown', word: 'Unknown', detail: `last reported ${stateWord(result.state).toLowerCase()}, but that snapshot isn’t current`, out };
  }
  const tone = stateTone(result.state), word = stateWord(result.state);
  if (result.state === 'up') return { tone, word, detail: hasLatency(result.observation) ? latencyOf(result.observation) : 'no measured latency', out };
  if (result.state === 'paused') return { tone, word, detail: '', out };
  return { tone, word, detail: result.message, out };
}

/** The registration whose snapshot reported this result: the row's own member first, then the current sources. */
function reporterOf(row: FleetMonitorRow, result: FleetResult, byId: Map<string, FleetSource>): FleetSource | undefined {
  if (result.registrationId === undefined) return undefined;
  return row.sources.find(member => member.source.node.id === result.registrationId)?.source ?? byId.get(result.registrationId);
}

/**
 * Age of the measurement now. Both intervals are on one clock each: the node's own clock from check to read, then the
 * browser's from read to now, so clock skew between them never enters.
 */
function checkedAge(result: FleetResult, reporter: FleetSource | undefined, now: number): number | null {
  const checkedAt = result.observation?.checkedAt;
  const nodeNow = reporter?.overview?.now;
  if (!validTime(checkedAt) || !validTime(nodeNow) || !validTime(result.readAt)) return null;
  return Math.max(0, nodeNow - checkedAt) + Math.max(0, now - result.readAt);
}

interface ShapedResult { result: FleetResult; reporter: FleetSource | undefined; shape: ResultShape }
interface ShapedRow { row: FleetMonitorRow; results: ShapedResult[] }

function ResultItem({ item, now }: { item: ShapedResult; now: number }) {
  const { result, reporter, shape } = item;
  const age = checkedAge(result, reporter, now);
  const parts: ReactNode[] = [];
  if (age !== null) {
    const at = result.observation!.checkedAt;
    parts.push(<time key="c" dateTime={new Date(at).toISOString()} title={`${utc(at)} on the node’s clock`}>checked {age < 1500 ? 'just now' : `${elapsed(age)} ago`}</time>);
  }
  if (validTime(result.readAt)) parts.push(<span key="r">read {ago(result.readAt, now)}</span>);
  if (result.viaPeer) parts.push(<span key="v" className="fl-via"><Icon name="nodes" className="inline" />{reporter ? `via peer ${reporter.node.label}` : 'via an HA peer'}</span>);
  return (
    <li className={`fl-r t-${shape.tone}${shape.out ? ' out' : ''}`}>
      <Glyph tone={shape.tone} />
      <span className="fl-r-b">
        <span className="fl-r-n"><b>{result.name}</b>{result.name !== result.nodeId ? <span className="mono">{result.nodeId}</span> : null}</span>
        <span className="fl-r-s"><b>{shape.word}</b>{shape.detail ? <> · {shape.detail}</> : null}</span>
        {parts.length ? <span className="fl-r-m">{parts.map((part, index) => <Fragment key={index}>{index ? ' · ' : ''}{part}</Fragment>)}</span> : null}
      </span>
    </li>
  );
}

/** One way in per registered view, through that registration and the monitor ID it actually holds. */
function OpenChoices({ row, now, onOpen }: { row: FleetMonitorRow; now: number; onOpen: FleetOverviewProps['onOpen'] }) {
  const choices = row.sources.map(({ source, view }) => ({
    key: `${source.node.id}\n${view.monitor.id}`, registrationId: source.node.id, monitorId: view.monitor.id, label: source.node.label,
    revision: view.monitor.revision, readable: nodeStateInfo(source.node).readable, current: fleetSourceCurrent(source, now),
  }));
  if (!choices.length) return null;
  if (choices.length === 1) {
    const [choice] = choices;
    return choice.readable
      ? <button type="button" className="btn sm" onClick={() => onOpen(choice.registrationId, choice.monitorId)}>Open<span className="vh"> {row.monitor.name} on {choice.label}</span><Icon name="chevRight" /></button>
      : <span className="fl-open-off">{choice.label} can’t be read</span>;
  }
  const buttons = choices.map(choice => choice.readable ? (
    <button key={choice.key} type="button" className="btn sm" onClick={() => onOpen(choice.registrationId, choice.monitorId)}>
      <span className="vh">Open {row.monitor.name} on </span>{choice.label}
      {row.differingViews ? <Rev revision={choice.revision} /> : null}
      {choice.current ? null : <span className="fl-open-st">not current</span>}
    </button>
  ) : <span key={choice.key} className="fl-open-off">{choice.label} can’t be read</span>);
  if (choices.length > 4) {
    return (
      <details className="fl-open-more">
        <summary className="btn sm">Open on…<span className="fl-open-st">{choices.length}</span></summary>
        <div className="fl-open">{buttons}</div>
      </details>
    );
  }
  return <div className="fl-open" role="group" aria-label={`Open ${row.monitor.name} on`}><span className="fl-open-k" aria-hidden="true">Open on</span>{buttons}</div>;
}

/** Root-wired actions on a managed row, unchanged, grouped so they wrap under Open and name the monitor for assistive tech. */
function PlacementActions({ row, props }: { row: FleetMonitorRow; props: FleetOverviewProps }) {
  if (!row.deployment) return null;
  const edit = row.deployment && props.onEditDeployment ? <button type="button" className="btn sm" onClick={() => props.onEditDeployment?.(row.deployment!)}>Edit placement<span className="vh"> of {row.monitor.name}</span></button> : null;
  const retry = row.deployment && row.deployment.state !== 'applied' && props.onRetryDeployment ? <button type="button" className="btn sm" onClick={() => props.onRetryDeployment?.(row.deployment!)}>Retry placement<span className="vh"> of {row.monitor.name}</span></button> : null;
  const remove = (row.deployment.state === 'applied' || row.deployment.canCancel || row.deployment.canRemove) && props.onRemoveDeployment ? <button type="button" className="btn sm danger" onClick={() => props.onRemoveDeployment?.(row.deployment!)}>{row.deployment.canCancel ? 'Cancel placement' : 'Remove everywhere'}<span className="vh">: {row.monitor.name}</span></button> : null;
  const reconcile = (row.differingViews || row.deployment.state === 'blocked') && props.onReconcileDeployment ? <button type="button" className="btn sm" onClick={() => props.onReconcileDeployment?.(row.deployment!)}>Review node edits<span className="vh"> of {row.monitor.name}</span></button> : null;
  if (!edit && !retry && !remove && !reconcile) return null;
  return <div className="fl-dep-acts" role="group" aria-label={`Placement of ${row.monitor.name}`}>{edit}{retry}{remove}{reconcile}</div>;
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

const sourceOrder = (a: FleetSource, b: FleetSource): number =>
  Number(b.node.local) - Number(a.node.local) || a.node.label.localeCompare(b.node.label) || a.node.id.localeCompare(b.node.id);

const COUNTED: { tone: Tone; word: string }[] = [
  { tone: 'down', word: 'down' }, { tone: 'up', word: 'up' }, { tone: 'pending', word: 'pending' }, { tone: 'unknown', word: 'unknown' }, { tone: 'paused', word: 'paused' },
];

function NodeTile({ source, now, counts }: { source: FleetSource; now: number; counts: Map<Tone, number> | undefined }) {
  const snapshot = snapshotOf(source, now);
  const total = counts ? [...counts.values()].reduce((sum, n) => sum + n, 0) : 0;
  return (
    <li className={`fl-node s-${snapshot.kind}`}>
      <div className="fl-node-h">
        <b>{source.node.label}</b>
        {source.node.local ? <Tag tone="accent">local</Tag> : null}
      </div>
      <span className="mono fl-node-id">{shownNodeId(source.node) ?? 'node ID not read yet'}</span>
      <span className="fl-snap"><Icon name={snapshot.icon} />{snapshot.label}</span>
      <span className="fl-node-t">{snapshot.text}</span>
      {total ? (
        <ul className="hcounts" aria-label={`${plural(total, 'result')} measured by ${source.node.label}`}>
          {COUNTED.filter(({ tone }) => counts!.get(tone)).map(({ tone, word }) => <li key={tone}><Glyph tone={tone} /><b>{counts!.get(tone)}</b> {word}</li>)}
        </ul>
      ) : source.overview ? <span className="fl-node-t">No monitor is assigned to it{snapshot.kind === 'current' ? '' : ' in the data received'}.</span> : null}
    </li>
  );
}

export function FleetOverviewView(props: FleetOverviewProps) {
  const { sources, rows, now, loading, refreshing, error, search, onSearchChange, onRefresh, onOpen, onAdd, onManageGroups } = props;
  const [filter, setFilter] = useState<StateFilter>('all');
  const byId = new Map(sources.map(source => [source.node.id, source]));
  const ordered = [...sources].sort(sourceOrder);
  const current = sources.filter(source => fleetSourceCurrent(source, now)).length;
  const reading = sources.filter(source => source.status === 'loading' && !source.overview).length;
  const notCurrent = sources.length - current;
  const unavailable = sources.filter(source => source.status === 'unavailable').length;

  const shaped: ShapedRow[] = rows.map(row => ({
    row,
    results: row.results.map(result => {
      const reporter = reporterOf(row, result, byId);
      return { result, reporter, shape: resultShape(result, reporter, now) };
    }),
  }));
  const counts = new Map<string, Map<Tone, number>>();
  for (const { results } of shaped) for (const { result, shape } of results) {
    const node = counts.get(result.nodeId) ?? new Map<Tone, number>();
    node.set(shape.tone, (node.get(shape.tone) ?? 0) + 1);
    counts.set(result.nodeId, node);
  }

  const q = search.trim().toLowerCase();
  const searchable = ({ row }: ShapedRow): string => [row.monitor.name, row.monitor.type, row.monitor.id,
    ...row.results.flatMap(result => [result.name, result.nodeId]), ...row.sources.map(member => member.source.node.label)].join(' ').toLowerCase();
  const searched = q ? shaped.filter(item => searchable(item).includes(q)) : shaped;
  const shown = searched.filter(({ row }) => matchesFilter(row.assessment, filter));
  const count = (f: StateFilter): number => searched.filter(({ row }) => matchesFilter(row.assessment, f)).length;

  const failing = rows.filter(row => matchesFilter(row.assessment, 'failing'));
  const everywhere = failing.filter(row => row.assessment.kind === 'everywhere').length;
  const unknown = rows.filter(row => matchesFilter(row.assessment, 'unknown'));
  const differing = rows.filter(row => row.differingViews).length;
  const up = rows.filter(row => row.assessment.kind === 'up').length;
  const paused = rows.filter(row => row.assessment.kind === 'paused').length;
  const firstRead = loading && !sources.some(source => source.overview);

  const pendingRows = rows.filter(row => row.assessment.kind === 'pending').length;
  const unknownSub = [differing ? `${plural(differing, 'monitor')} with differing revisions` : '', pendingRows ? `${pendingRows} pending` : ''].filter(Boolean).join(' · ');

  const headline = failing.length ? <><b>{plural(failing.length, 'monitor')}</b> {failing.length === 1 ? 'is' : 'are'} down on at least one node.{unknown.length ? ` ${unknown.length} more ${unknown.length === 1 ? 'is' : 'are'} unknown or pending somewhere.` : ''}</>
    : unknown.length ? <>No monitor is down in the current snapshots, but <b>{plural(unknown.length, 'monitor')}</b> {unknown.length === 1 ? 'is' : 'are'} unknown or pending on at least one node.</>
    : notCurrent ? <>No monitor is down in the snapshots received, but {notCurrent} of {plural(sources.length, 'node')} {notCurrent === 1 ? 'isn’t' : 'aren’t'} current.</>
    : <>Every monitor is up or paused on every assigned node.</>;

  return (
    <div className="view fleet">
      <PageHead
        title="All nodes"
        sub={firstRead ? 'Reading the nodes registered with this dashboard…' : <>{plural(rows.length, 'monitor')} across {plural(sources.length, 'node')} registered with this dashboard · {current} of {sources.length} read now</>}
        actions={<>
          <span className="vh" role="status">{loading ? 'Reading the registered nodes.' : refreshing ? 'Reading every registered node again.' : ''}</span>
          <button type="button" className="btn" onClick={onRefresh} disabled={loading || refreshing}>
            <Icon name="refresh" />{loading ? 'Reading…' : refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
          {onManageGroups ? <button type="button" className="btn" onClick={onManageGroups}><Icon name="nodes" />Node groups</button> : null}
          {onAdd ? <button type="button" className="btn primary" onClick={onAdd}><Icon name="plus" />New monitor</button> : null}
        </>}
      />
      {error ? <Banner tone="down" role="alert" title="Monitors couldn’t be read">{error}</Banner> : null}

      {rows.length ? (
        <Card className="situation fl-sum">
          <h2 className="vh">Summary of every node</h2>
          <p className="headline">{headline}</p>
          <div className="kpis">
            <Kpi label="Down" tone={failing.length ? 'down' : undefined} value={failing.length} sub={failing.length ? `${everywhere} on every node · ${failing.length - everywhere} on some` : 'No failures reported'} />
            <Kpi label="Unknown" tone={unknown.length ? 'unknown' : undefined} value={unknown.length} sub={unknownSub || 'No current result from every node'} />
            <Kpi label="Up" tone={up ? 'up' : undefined} value={up} of={rows.length} sub={`${paused} paused`} />
            <Kpi label="Nodes read now" value={current} of={sources.length} sub={notCurrent ? [reading ? `${reading} reading` : '', unavailable ? `${unavailable} unavailable` : '', notCurrent - reading - unavailable > 0 ? `${notCurrent - reading - unavailable} not current` : ''].filter(Boolean).join(' · ') : 'Reading a node isn’t a monitoring result'} />
          </div>
        </Card>
      ) : null}

      <Card title="Nodes on this dashboard" className={refreshing ? 'refetching' : undefined} hint="What the dashboard’s server could read from each registration. Reading a node says nothing about the monitors it checks.">
        {ordered.length ? (
          <ul className="fl-nodes">
            {ordered.map(source => <NodeTile key={source.node.id} source={source} now={now} counts={counts.get(source.node.nodeId)} />)}
          </ul>
        ) : loading ? <div className="fl-wait"><Spinner label="Loading nodes…" /></div>
          : <EmptyState title="No nodes on this dashboard yet">Monitors appear here once monitoring nodes are registered with this dashboard.</EmptyState>}
      </Card>

      <Card
        title="Monitors on every node" className={`fl-mons${refreshing ? ' refetching' : ''}`}
        hint="One row per monitor, with each assigned node’s own latest result."
        actions={(
          <label className="search fl-search">
            <Icon name="search" />
            <span className="vh">Search monitors and nodes</span>
            <input type="search" value={search} placeholder="Search monitors and nodes" autoComplete="off" spellCheck={false}
              onChange={event => onSearchChange(event.target.value)} onKeyDown={event => { if (event.key === 'Escape' && search) onSearchChange(''); }} />
          </label>
        )}
      >
        {rows.length && notCurrent ? (
          <Banner tone="warn" title={`${notCurrent} of ${plural(sources.length, 'node')} ${notCurrent === 1 ? 'isn’t' : 'aren’t'} current`}>
            Monitors only {notCurrent === 1 ? 'that node' : 'those nodes'} know about may be missing here, and results {notCurrent === 1 ? 'it' : 'they'} would report show as unknown. A node keeps checking and alerting on its own while the dashboard can’t read it.
          </Banner>
        ) : null}
        {rows.length ? <FilterChips<StateFilter> label="Filter monitors by state" value={filter} onChange={setFilter} options={[
          { value: 'all', label: 'All', count: count('all') },
          { value: 'failing', label: 'Failing', count: count('failing') },
          { value: 'unknown', label: 'Unknown', count: count('unknown') },
          { value: 'up', label: 'Up', count: count('up') },
          { value: 'paused', label: 'Paused', count: count('paused') },
        ]} /> : null}
        {shown.length ? (
          <div className="fl-tbl-wrap" aria-busy={refreshing || undefined}>
            <table className="fl-tbl">
              <caption className="vh">Monitors on every registered node. Each row lists the latest result from each node the monitor is assigned to.</caption>
              <thead>
                <tr>
                  <th scope="col">Monitor</th>
                  <th scope="col">State</th>
                  <th scope="col">Results by node</th>
                  <th scope="col"><span className="vh">Open</span></th>
                </tr>
              </thead>
              <tbody>
                {shown.map(({ row, results }) => (
                  <tr key={row.key}>
                    <th scope="row" className="fl-mon">
                      <span className="fl-mon-n">{row.monitor.name}</span><TypeTag type={row.monitor.type} />
                      <span className="fl-mon-s">{results.length ? `Assigned to ${plural(results.length, 'node')}` : 'Not assigned to any node'}{row.sources.length > 1 ? ` · ${row.sources.length} views` : ''}</span>
                      {row.differingViews ? <span className="fl-diff"><Glyph tone="warn" />Nodes report different revisions. Nothing here picks one; open each view to compare.</span> : null}
                    </th>
                    <td className="fl-st"><StateChip a={row.assessment} /></td>
                    <td className="fl-res-c">
                      {results.length
                        ? <ul className="fl-res" aria-label={`Results for ${row.monitor.name} by node`}>{results.map(item => <ResultItem key={item.result.nodeId} item={item} now={now} />)}</ul>
                        : <span className="muted small">No node runs it.</span>}
                    </td>
                    <td className="fl-open-c"><OpenChoices row={row} now={now} onOpen={onOpen} />
                      <PlacementActions row={row} props={props} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : rows.length ? <EmptyState title="Nothing matches">Try another filter or search.</EmptyState>
          : loading || reading ? <div className="fl-wait"><Spinner label="Reading monitors from every node…" /></div>
          : !sources.length ? <EmptyState title="No monitors yet">Monitors appear here once monitoring nodes are registered with this dashboard.</EmptyState>
          : notCurrent === sources.length ? (
            <EmptyState title="No monitor data received">
              No registered node has a current snapshot yet, so this doesn’t mean {sources.length === 1 ? 'it has' : 'they have'} no monitors.
            </EmptyState>
          )
          : notCurrent ? (
            <EmptyState title="No monitors received">
              The nodes read now report none, but {notCurrent} of {plural(sources.length, 'registered node')} {notCurrent === 1 ? 'has' : 'have'} no current snapshot, so {notCurrent === 1 ? 'its' : 'their'} monitors may be missing here.
            </EmptyState>
          )
          : <EmptyState title="No monitors on any node yet" action={onAdd ? <button type="button" className="btn primary" onClick={onAdd}><Icon name="plus" />New monitor</button> : undefined}>
              {onAdd ? 'Add a monitor and choose which nodes run it.' : 'An editor or administrator can add monitors.'}
            </EmptyState>}
        {shown.length ? (
          <div className="legend" aria-label="Legend">
            <span><Glyph tone="up" />Up</span><span><Glyph tone="down" />Down</span><span><Glyph tone="pending" />Pending</span>
            <span><Glyph tone="unknown" />Unknown</span><span><Glyph tone="paused" />Paused</span>
            <span><i className="sw out" />No current snapshot</span><span><Icon name="nodes" className="inline" />Reported by an HA peer</span>
          </div>
        ) : null}
      </Card>
    </div>
  );
}
