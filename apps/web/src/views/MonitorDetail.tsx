import { useId, useMemo, useState, type ReactNode } from 'react';
import type { DeliveryAttemptView, LocalAvailabilityHistory, LocalHistoryStatistics, MonitorView, Observation } from '@uptime-canary/shared';
import { ApiFailure, describeFailure } from '../api';
import { DAY, allowedSteps, autoStep, duration, pct, planAvailability, stepText, type AvailabilityPlan } from '../availability';
import { checkFacts, inputOf, typeLabel } from '../checks';
import { useCanary } from '../context';
import { nodeStateInfo, registryOrder } from '../dashboard';
import { ago, elapsed, hasLatency, latency, latencyOf, maskedUrl, percent, plural, shortRev, span } from '../format';
import { useResource } from '../hooks';
import { candidateOrder, observationFor, outOfView, stateTone, stateWord, type Assessment } from '../model';
import { notify } from '../notices';
import { POLL_MS } from '../session';
import { href, navigate } from '../router';
import { Banner, Card, CopyButton, EmptyState, Modal, PageHead, ReadOnly, Rev, StateChip, Time, TypeTag } from '../ui/bits';
import { AvailabilityChart, HistoryStrip, LatencyChart } from '../ui/charts';
import { CandidateOrder, Seg } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';
import { MonitorTags } from '../ui/tags';
import { DeliveryTable, ProviderIcon, providerLabel } from './Channels';
import { IncidentRow, ReceiptList, SuppressionLine } from './Incidents';
import { MaintenanceTag } from './Maintenance';

function interpretation(view: MonitorView, a: Assessment): ReactNode {
  const ids = (list: string[]): ReactNode => list.map((id, index) => (
    <span key={id}>{index === 0 ? '' : index === list.length - 1 ? ' and ' : ', '}<span className="mono">{id}</span></span>
  ));
  // A system service check reads each node's own OS: locations differ by machine, never by network path.
  if (view.monitor.type === 'system-service') {
    switch (a.kind) {
      case 'everywhere': return a.total === 1
        ? <>Failing on {ids(a.failing)}, the only node assigned. It reads the service on that node’s own operating system.</>
        : 'Failing on every assigned node. Each node reads the service on its own operating system, so this is the state on each of those machines.';
      case 'partial': return <>
        Failing on {ids(a.failing)}{a.passing.length ? <> but passing on {ids(a.passing)}</> : null}. Each node reads its own copy of the service, so the machines differ; this isn’t a network problem.
        {a.unknown.length ? <> {ids(a.unknown)} {a.unknown.length === 1 ? 'has' : 'have'} no fresh result.</> : null} Any fresh failure opens an incident.
      </>;
      case 'pending': return <>Pending from {ids(a.pending)}: a retry in progress, or the node can’t read its own service manager (none present, as in an ordinary container; no permission; an unsupported OS; busy check workers; or no answer within the timeout). Pending is neither up nor down and never sends a recovery.</>;
      default: break;
    }
  }
  // A PM2 check reads each node's own daemon: locations differ by machine, never by network path.
  if (view.monitor.type === 'pm2') {
    switch (a.kind) {
      case 'everywhere': return a.total === 1
        ? <>Failing on {ids(a.failing)}, the only node assigned. It reads the process list of that machine’s own PM2 daemon.</>
        : 'Failing on every assigned node. Each node reads its own machine’s PM2 daemon, so on each of them the process is missing or not online.';
      case 'partial': return <>
        Failing on {ids(a.failing)}{a.passing.length ? <> but passing on {ids(a.passing)}</> : null}. Each node reads its own PM2 daemon, so the machines differ; this isn’t a network problem.
        {a.unknown.length ? <> {ids(a.unknown)} {a.unknown.length === 1 ? 'has' : 'have'} no fresh result.</> : null} Any fresh failure opens an incident.
      </>;
      case 'pending': return <>Pending from {ids(a.pending)}: a retry in progress, or the node got no definite answer from its PM2 daemon (none running at that connection; a daemon run by another account, or on Windows a pipe whose present owner or permissions don’t fit; no permission; a connection its system doesn’t use, or a system other than Linux and Windows; another PM2 version; an unreadable answer; a state it doesn’t know; busy or failed check workers; or no answer within the timeout). Pending is neither up nor down and never sends a recovery.</>;
      default: break;
    }
  }
  // Every node asks the same Steam directory, so locations differ by DNS answer or API path, not by the route to the server.
  if (view.monitor.type === 'steam') {
    switch (a.kind) {
      case 'everywhere': return a.total === 1
        ? <>Failing on {ids(a.failing)}, the only node assigned. It asked Steam’s directory whether the server is listed.</>
        : 'Failing on every assigned node. They all ask the same Steam directory, so the server most likely isn’t listed (stopped, private, or listed under another address or port), or, where API failures count as down, the Steam API is failing for all of them.';
      case 'partial': return <>
        Failing on {ids(a.failing)}{a.passing.length ? <> but passing on {ids(a.passing)}</> : null}. Every node asks the same Steam directory, so a split usually means the nodes resolved the host to different addresses or, where API failures count as down, that some of them couldn’t reach the API. It says little about the route to the game server.
        {a.unknown.length ? <> {ids(a.unknown)} {a.unknown.length === 1 ? 'has' : 'have'} no fresh result.</> : null} Any fresh failure opens an incident.
      </>;
      case 'pending': return <>Pending from {ids(a.pending)}: a retry in progress, or the Steam API couldn’t be reached, timed out, answered with an error or sent an answer that isn’t a valid directory listing. Pending is neither up nor down and never sends a recovery.</>;
      default: break;
    }
  }
  switch (a.kind) {
    case 'paused': return 'Paused. No node checks it and no alerts are sent until it’s resumed.';
    case 'conflict': return 'Nodes hold different versions of this monitor. Results show as unknown until someone resolves the conflict.';
    case 'everywhere': return a.total === 1
      ? <>Failing from {ids(a.failing)}, its only location. Add more locations to tell a service failure from a network path problem.</>
      : 'Failing from every assigned location. The service itself is the most likely cause, not the network path to it.';
    case 'partial': return <>
      Failing from {ids(a.failing)}{a.passing.length ? <> but passing from {ids(a.passing)}</> : null}. That points to a location-specific problem, such as a network path or regional edge, rather than the service.
      {a.unknown.length ? <> {ids(a.unknown)} {a.unknown.length === 1 ? 'has' : 'have'} no fresh result.</> : null} Any fresh failure opens an incident.
    </>;
    case 'pending': return <>Pending from {ids(a.pending)}: a retry in progress, a container starting, or a manual monitor without a status. Pending is neither up nor down and never sends a recovery.</>;
    case 'unknown': return <>No usable result from {ids(a.unknown)}. Unknown never counts as up or down: an open incident stays open until every assigned location reports up.</>;
    case 'up': return a.total === 1 ? <>Passing from {ids(a.passing)}.</> : 'Passing from every assigned location.';
  }
}

function LocationTable({ view }: { view: MonitorView }) {
  const { nodeMap, overview, endpoint } = useCanary();
  return (
    <div className="tbl-wrap">
      <table className="tbl stack">
        <caption className="vh">Latest result by location</caption>
        <thead><tr><th scope="col">Location</th><th scope="col">Result</th><th scope="col">Checked</th><th scope="col" className="num">Latency</th><th scope="col">Message</th></tr></thead>
        <tbody>
          {candidateOrder(view.monitor.nodeIds).map(id => {
            const node = nodeMap.get(id);
            const o = observationFor(view, id);
            const away = outOfView(node);
            const state = view.state === 'paused' ? 'paused' : o?.state ?? 'unknown';
            return (
              <tr key={id} className={`${away ? 'out' : ''}${id === endpoint ? ' ep' : ''}`.trim() || undefined}>
                <td className="lead" data-label="Location">
                  <span className="mono">{id}</span>{node && node.name !== id ? <span className="muted"> {node.name}</span> : null}
                  {away ? <div className="muted small">Out of view from {endpoint}. It keeps checking on its own.</div> : null}
                </td>
                <td data-label="Result">
                  <span className="nowrap"><Glyph tone={stateTone(state)} /> {stateWord(state)}</span>
                  {o?.consecutiveFailures && state !== 'paused' ? <div className="muted small">{plural(o.consecutiveFailures, 'failure')} in a row</div> : null}
                  {o?.statusCode ? <div className="muted small">HTTP {o.statusCode}</div> : null}
                </td>
                <td data-label="Checked" className={o && o.checkedAt > 0 ? 'tn' : 'tn nil'}>{o && o.checkedAt > 0 ? <><Time at={o.checkedAt} now={overview.now} /> <span className="muted">· {ago(o.checkedAt, overview.now)}</span></> : <span className="muted">never</span>}</td>
                <td data-label="Latency" className={o?.state === 'up' && hasLatency(o) ? 'num' : 'num nil'}>{o && o.state === 'up' ? latencyOf(o) : <span className="muted">—</span>}</td>
                <td data-label="Message" className="msg full">
                  {o?.message || '—'}
                  {o?.certificate ? <div className="muted small"><Icon name="lock" className="inline" />Certificate {o.certificate.verified ? 'verified' : <b>not verified</b>}, valid until {new Date(o.certificate.notAfter).toLocaleDateString()} ({plural(o.certificate.daysRemaining, 'day')} left)</div> : null}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

type Range = '1h' | '6h' | '24h' | '7d' | '30d' | '90d' | 'custom';
const RANGE_MS: Record<Exclude<Range, 'custom'>, number> = { '1h': 3_600_000, '6h': 21_600_000, '24h': DAY, '7d': 7 * DAY, '30d': 30 * DAY, '90d': 90 * DAY };
const localInput = (ms: number): string => { const d = new Date(ms - new Date(ms).getTimezoneOffset() * 60_000); return d.toISOString().slice(0, 16); };

interface HistoryData { samples: Observation[]; stats: LocalHistoryStatistics | null; statsError: unknown; from: number; to: number }

function StatTile({ label, value, sub }: { label: string; value: ReactNode; sub: ReactNode }) {
  return <div className="kpi"><div className="l">{label}</div><div className="v">{value}</div><div className="s">{sub}</div></div>;
}

function Statistics({ stats, shown }: { stats: LocalHistoryStatistics; shown: number }) {
  const decided = stats.up + stats.down;
  return (
    <>
      <div className="kpis stats">
        <StatTile label="Samples in range" value={stats.count.toLocaleString()} sub={<>{stats.up} up · {stats.down} down{stats.pending ? ` · ${stats.pending} pending` : ''}{stats.unknown ? ` · ${stats.unknown} unknown` : ''}</>} />
        <StatTile label="Sample availability" value={stats.sampleAvailability === null ? '—' : percent(stats.sampleAvailability)}
          sub={decided ? <>{stats.up.toLocaleString()} of {decided.toLocaleString()} up-or-down samples</> : 'No up or down samples'} />
        <StatTile label="Latency p50 · p95" value={stats.p50LatencyMs === null ? '—' : <>{latency(stats.p50LatencyMs)}<small> · {stats.p95LatencyMs === null ? '—' : latency(stats.p95LatencyMs)}</small></>}
          sub={stats.latencyCount ? <>from {plural(stats.latencyCount, 'measured sample')}{stats.averageLatencyMs !== null ? ` · mean ${latency(stats.averageLatencyMs)}` : ''}</> : 'No measured latency in range'} />
      </div>
      <p className="foot-note">
        Availability is up ÷ (up + down) over retained samples; pending and unknown are excluded and shown above. It is a sample ratio from {stats.nodeId} alone, not a time-weighted uptime or SLA; the observed time above is the time-weighted figure, and isn’t an SLA either.
        {typeof stats.latencyCount === 'number' && stats.latencyCount < stats.up ? ` ${stats.up - stats.latencyCount} up samples had no measured round trip and aren’t in the latency figures.` : ''}
        {' '}This node keeps up to {stats.recordLimit.toLocaleString()} samples per monitor for {elapsed(stats.retentionMs)}; older measurements are gone.
        {stats.count > shown ? ` The chart shows the newest ${shown.toLocaleString()} of these ${stats.count.toLocaleString()} samples.` : ''}
      </p>
    </>
  );
}

interface ObservedData { history?: LocalAvailabilityHistory; plan?: AvailabilityPlan; problem?: string; missing?: boolean }

/**
 * Time-weighted observed history from this node's own fresh results (protocol 11). Presets end at the node's clock as of
 * its latest overview, because the node rejects a future end rather than clipping it.
 */
function ObservedTime({ view, range, customFrom, customTo, asTable }: { view: MonitorView; range: Range; customFrom: number; customTo: number; asTable: boolean }) {
  const { api, endpoint, overview } = useCanary();
  const formId = useId();
  const [step, setStep] = useState<number | 'auto'>('auto');
  const nodeNow = overview.now;
  const wantFrom = range === 'custom' ? customFrom : nodeNow - RANGE_MS[range];
  const wantTo = range === 'custom' ? Math.min(customTo, nodeNow) : nodeNow;
  const steps = Number.isFinite(wantFrom) && Number.isFinite(wantTo) && wantTo > wantFrom ? allowedSteps(Math.max(0, wantFrom), wantTo) : [];
  const chosen = step !== 'auto' && steps.includes(step) ? step : 'auto';
  const observed = useResource<ObservedData>(
    `${api.baseUrl}|availability|${view.monitor.id}|${range}|${range === 'custom' ? `${customFrom}-${customTo}` : ''}|${chosen}`,
    async signal => {
      const now = overview.now;
      const plan = range === 'custom' ? planAvailability(customFrom, customTo, now, chosen) : planAvailability(now - RANGE_MS[range], now, now, chosen);
      if ('error' in plan) return { problem: plan.error };
      try {
        return { history: await api.availability(view.monitor.id, plan, signal), plan };
      } catch (error) {
        // Nodes before protocol 11 have no such route; the monitor itself exists (the overview lists it).
        if (error instanceof ApiFailure && error.status === 404 && !signal.aborted) return { missing: true };
        throw error;
      }
    },
    range !== 'custom' ? POLL_MS * 3 : undefined,
  );
  const data = observed.data;
  const h = data?.history;
  const s = h?.summary;
  return (
    <section className="obs" aria-labelledby={`${formId}-h`}>
      <div className="obs-h">
        <h3 className="mini-h" id={`${formId}-h`}>Observed time</h3>
        {steps.length ? (
          <label className="inline-sel">Buckets
            <select value={String(chosen)} onChange={event => setStep(event.target.value === 'auto' ? 'auto' : Number(event.target.value))}>
              <option value="auto">Automatic ({stepText(autoStep(Math.max(0, wantFrom), wantTo))})</option>
              {steps.map(value => <option key={value} value={value}>{stepText(value)}</option>)}
            </select>
          </label>
        ) : null}
      </div>
      {observed.error && !data ? <Banner tone="down" title="Observed time could not be loaded">{describeFailure(observed.error, { preferDetail: true })}</Banner>
        : !data ? <p className="muted">Loading observed time from {endpoint}…</p>
        : data.missing ? <p className="muted small">{endpoint} doesn’t report observed time; that needs protocol 11 or later. The check figures below still apply.</p>
        : data.problem ? <p className="small fwarn"><Glyph tone="warn" />{data.problem}</p>
        : h && s ? (
          <>
            {observed.error ? <p className="muted small"><Glyph tone="warn" /> Couldn’t refresh: {describeFailure(observed.error)} Showing the last loaded figures.</p> : null}
            <div className="kpis stats">
              <StatTile label="Observed availability" value={pct(s.availabilityPercent)}
                sub={s.knownMs ? <>up {duration(s.upMs)} of {duration(s.knownMs)} known</> : 'No up or down time observed'} />
              <StatTile label="Coverage" value={pct(s.coveragePercent)}
                sub={<>{duration(s.knownMs)} known of {duration(s.endMs - s.startMs)}{s.unknownMs ? ` · ${duration(s.unknownMs)} unknown` : ''}{s.pendingMs ? ` · ${duration(s.pendingMs)} pending` : ''}</>} />
              <StatTile label="Down" value={duration(s.downMs)}
                sub={s.maintenanceMs ? <>{duration(s.maintenanceMs)} overlapped maintenance, still counted</> : 'No maintenance overlap'} />
            </div>
            {asTable ? (
              <div className="tbl-wrap scroll-y">
                <table className="tbl">
                  <caption className="vh">Observed time by {h.nodeId} in {stepText(h.stepMs)} buckets, newest first</caption>
                  <thead><tr><th scope="col">From</th><th scope="col">To</th><th scope="col" className="num">Up</th><th scope="col" className="num">Down</th><th scope="col" className="num">Pending</th><th scope="col" className="num">Unknown</th><th scope="col" className="num">Maintenance</th><th scope="col" className="num">Availability</th><th scope="col" className="num">Coverage</th></tr></thead>
                  <tbody>
                    {[...h.buckets].reverse().map(b => (
                      <tr key={b.startMs}>
                        <td className="tn"><Time at={b.startMs} now={overview.now} /></td>
                        <td className="tn"><Time at={b.endMs} now={overview.now} /></td>
                        <td className="num">{duration(b.upMs)}</td><td className="num">{duration(b.downMs)}</td><td className="num">{duration(b.pendingMs)}</td>
                        <td className="num">{duration(b.unknownMs)}</td><td className="num">{duration(b.maintenanceMs)}</td>
                        <td className="num">{pct(b.availabilityPercent)}</td><td className="num">{pct(b.coveragePercent)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <>
                <AvailabilityChart buckets={h.buckets} now={overview.now} label={`Observed time by ${h.nodeId} in ${h.buckets.length} buckets of ${stepText(h.stepMs)}: ${pct(s.availabilityPercent)} available of known time, ${pct(s.coveragePercent)} coverage`} />
                <div className="legend">
                  <span><i className="sw av-up" />Up</span><span><i className="sw av-down" />Down</span><span><i className="sw av-pend" />Pending</span>
                  <span><i className="sw out" />Unknown (no fresh result)</span><span><i className="sw av-maint" />Maintenance (annotation)</span>
                </div>
              </>
            )}
            <p className="foot-note">
              Time-weighted from {h.nodeId}’s own fresh results, <Time at={h.from} now={overview.now} /> to <Time at={h.to} now={overview.now} />{data.plan?.clipped ? ' (the node’s current time)' : ''}, in {stepText(h.stepMs)} buckets.
              {' '}Availability is up ÷ (up + down) time; pending and unknown time don’t count for or against it but lower the coverage. Time without a fresh result is unknown, never assumed up. Maintenance is shown alongside and removes nothing from either figure.
              {' '}{h.retainedFrom === null ? 'No observed time is retained yet. Older checks aren’t converted into observed time.' : <>Retained from <Time at={h.retainedFrom} now={overview.now} />, for up to {elapsed(h.retentionMs)} and {h.recordLimit.toLocaleString()} spans; earlier time is unknown.</>}
              {' '}Other nodes keep their own. Not an SLA.
            </p>
          </>
        ) : null}
    </section>
  );
}

function HistoryCard({ view }: { view: MonitorView }) {
  const { api, endpoint, overview, nodeMap, openSwitch, controlPlane, registry, selection, select } = useCanary();
  const formId = useId();
  const [asTable, setAsTable] = useState(false);
  const [range, setRange] = useState<Range>('24h');
  const [limit, setLimit] = useState(300);
  const [custom, setCustom] = useState(() => ({ from: localInput(Date.now() - 86_400_000), to: localInput(Date.now() + 60_000) }));
  const runsHere = view.monitor.nodeIds.includes(endpoint);
  const customFrom = Date.parse(custom.from);
  const customTo = Date.parse(custom.to);
  const customBad = range === 'custom' && (!Number.isFinite(customFrom) || !Number.isFinite(customTo) || customTo <= customFrom);
  const history = useResource<HistoryData>(
    `${api.baseUrl}|${view.monitor.id}|${runsHere}|${range}|${limit}|${range === 'custom' ? `${customFrom}-${customTo}` : ''}`,
    async signal => {
      if (!runsHere || customBad) return { samples: [], stats: null, statsError: null, from: 0, to: 0 };
      // The node compares against its own sample times; reach slightly past both clocks so the newest check is included.
      const to = range === 'custom' ? customTo : Math.max(Date.now(), overview.now) + 1000;
      const from = range === 'custom' ? customFrom : to - RANGE_MS[range];
      const [samples, stats] = await Promise.all([
        api.historyWindow(view.monitor.id, from, to, limit, signal),
        api.statistics(view.monitor.id, from, to, signal).then(value => ({ value, error: null }), error => { if (signal.aborted) throw error; return { value: null, error }; }),
      ]);
      return { samples, stats: stats.value, statsError: stats.error, from, to };
    },
    runsHere && range !== 'custom' ? POLL_MS * 3 : undefined,
  );
  const samples = useMemo(() => [...(history.data?.samples ?? [])].sort((a, b) => a.checkedAt - b.checkedAt), [history.data]);
  const measured = samples.filter(s => s.state === 'up' && hasLatency(s));

  if (!runsHere) {
    const elsewhere = candidateOrder(view.monitor.nodeIds).filter(id => nodeMap.get(id)?.url);
    // Control plane: an assigned node that is also on this dashboard opens with the same sign-in, on this same page.
    const registered = controlPlane
      ? registryOrder(registry.list ?? []).filter(node => node.id !== selection?.id && view.monitor.nodeIds.includes(node.nodeId) && nodeStateInfo(node).readable)
      : [];
    return (
      <Card title={<>History on <span className="mono">{endpoint}</span></>} hint="History stays on the node that measured it">
        <EmptyState title={`${endpoint} doesn’t run this check`}>
          {controlPlane
            ? `So it has no local history for it. ${registered.length ? 'Open an assigned node on this dashboard to read that node’s history.' : 'None of its assigned nodes is on this dashboard; an administrator can add one.'}`
            : 'So it has no local history for it. Switch the endpoint to an assigned node to read that node’s history.'}
        </EmptyState>
        {registered.length ? (
          <div className="row-wrap center">
            {registered.map(node => <button key={node.id} type="button" className="btn sm" onClick={() => select(node)}><Icon name="eye" />Open {node.label}</button>)}
          </div>
        ) : null}
        {!controlPlane && elsewhere.length ? (
          <div className="row-wrap center">
            {elsewhere.map(id => <button key={id} type="button" className="btn sm" onClick={() => openSwitch({ url: nodeMap.get(id)!.url, reason: `Reading ${view.monitor.name}’s history needs a connection to a node that runs it, such as ${id}.` })}><Icon name="plug" />View from {id}</button>)}
          </div>
        ) : null}
      </Card>
    );
  }
  const stats = history.data?.stats;
  const statsMissing = history.data?.statsError instanceof ApiFailure && (history.data.statsError as ApiFailure).status === 404;

  return (
    <Card
      title={<>History measured by <span className="mono">{endpoint}</span></>}
      hint={samples.length ? <>{plural(samples.length, 'check')} shown, <Time at={samples[0]!.checkedAt} now={overview.now} mode="short" /> to <Time at={samples[samples.length - 1]!.checkedAt} now={overview.now} mode="short" />. Other nodes keep their own.</> : 'History never leaves the node that measured it'}
      actions={<>
        <button type="button" className="btn sm" aria-pressed={asTable} onClick={() => setAsTable(value => !value)}><Icon name={asTable ? 'chart' : 'table'} />{asTable ? 'Show charts' : 'Show as tables'}</button>
        <button type="button" className="icon-btn sm" onClick={history.reload} aria-label="Reload history" data-tip="Reload history"><Icon name="refresh" /></button>
      </>}
      className={history.loading && history.data ? 'refetching' : undefined}
    >
      <div className="hist-ctl">
        <Seg legend="Range" name={`${formId}-range`} value={range} onChange={setRange}
          options={[{ value: '1h', label: '1 h' }, { value: '6h', label: '6 h' }, { value: '24h', label: '24 h' }, { value: '7d', label: '7 d' }, { value: '30d', label: '30 d' }, { value: '90d', label: '90 d' }, { value: 'custom', label: 'Custom' }]} />
        <label className="inline-sel">Show up to
          <select value={limit} onChange={event => setLimit(Number(event.target.value))}>
            {[100, 300, 1000].map(n => <option key={n} value={n}>{n.toLocaleString()} checks</option>)}
          </select>
        </label>
        {range === 'custom' ? (
          <div className="row-wrap custom-range">
            <label className="inline-sel">From <input type="datetime-local" value={custom.from} onChange={event => setCustom(c => ({ ...c, from: event.target.value }))} aria-invalid={customBad ? true : undefined} /></label>
            <label className="inline-sel">to <input type="datetime-local" value={custom.to} onChange={event => setCustom(c => ({ ...c, to: event.target.value }))} aria-invalid={customBad ? true : undefined} /></label>
            {customBad ? <small className="ferr"><Glyph tone="down" />The end must be after the start.</small> : <small className="muted">Your local time. The start is included, the end isn’t.</small>}
          </div>
        ) : null}
      </div>
      {customBad ? null : <ObservedTime view={view} range={range} customFrom={customFrom} customTo={customTo} asTable={asTable} />}
      <h3 className="mini-h checks-h">Individual checks</h3>
      {history.error && !history.data ? (
        <Banner tone="down" title="History could not be loaded">{describeFailure(history.error)}</Banner>
      ) : history.loading && !history.data ? (
        <p className="muted">Loading history from {endpoint}…</p>
      ) : (
        <>
          {history.error ? <p className="muted small"><Glyph tone="warn" /> Couldn’t refresh: {describeFailure(history.error)} Showing the last loaded checks.</p> : null}
          {stats ? <Statistics stats={stats} shown={samples.length} /> : statsMissing ? <p className="muted small">{endpoint} doesn’t report statistics; figures below come from the checks shown.</p> : null}
          {!samples.length ? (
            <EmptyState title="No checks in this range">{view.state === 'paused' ? 'The monitor is paused.' : `${endpoint} records each check as it runs. Retention may also have removed older samples.`}</EmptyState>
          ) : (
            <>
              <HistoryStrip samples={samples} now={overview.now} />
              {asTable ? (
                <div className="tbl-wrap scroll-y">
                  <table className="tbl">
                    <caption className="vh">Checks by {endpoint}, newest first</caption>
                    <thead><tr><th scope="col">Checked</th><th scope="col">Result</th><th scope="col" className="num">Latency</th><th scope="col">Message</th></tr></thead>
                    <tbody>
                      {[...samples].reverse().map(sample => (
                        <tr key={`${sample.checkedAt}-${sample.nodeId}`}>
                          <td className="tn"><Time at={sample.checkedAt} now={overview.now} /></td>
                          <td><span className="nowrap"><Glyph tone={stateTone(sample.state)} /> {stateWord(sample.state)}</span></td>
                          <td className="num">{latencyOf(sample)}</td>
                          <td className="msg">{sample.message}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="chart-block">
                  <h3 className="mini-h">Response time</h3>
                  {measured.length
                    ? <LatencyChart samples={samples} now={overview.now} label={`Response time measured by ${endpoint} over ${samples.length} checks; ${measured.length} with a measured round trip`} />
                    : <p className="muted">No measured round trips in this range, so there’s nothing to plot. Manual, system service and PM2 checks, Steam checks without a measured echo, and unavailable ICMP replies have no latency.</p>}
                  <div className="legend"><span><i className="sw line" />Response time (measured, successful checks)</span><span><i className="sw band" />Failed checks</span></div>
                </div>
              )}
            </>
          )}
        </>
      )}
    </Card>
  );
}

function Facts({ view }: { view: MonitorView }) {
  const { role, groupMap, overview, monitorMap, channels, tags } = useCanary();
  const [reveal, setReveal] = useState(false);
  const m = view.monitor;
  const viewer = role === 'viewer';
  const group = m.groupId ? groupMap.get(m.groupId) : undefined;
  const dependency = group?.group.outageMonitorId ? monitorMap.get(group.group.outageMonitorId)?.monitor : undefined;
  const facts = checkFacts(m, { viewer, reveal });
  const hasSecrets = !viewer && Boolean(m.webhookUrl || m.http?.headers || m.http?.body || m.http?.proxyUrl || m.websocket?.headers || m.websocket?.proxyUrl || m.service || m.globalping?.token || m.globalping?.http?.headers || m.globalping?.apiUrl || m.steam?.apiUrl || m.pm2?.home);
  return (
    <>
      <dl className="kv">
        <dt>Monitor ID</dt><dd><span className="mono">{m.id}</span><span className="muted small"> · same on every node</span> <CopyButton value={m.id} className="linkbtn" /></dd>
        <dt>Check</dt><dd>{typeLabel(m.type)}{m.type !== 'manual' ? <> · <span className="mono wrap">{m.target}</span></> : null}</dd>
        {facts.map(fact => <Fact key={fact.label} label={fact.label}>{fact.value}</Fact>)}
        <dt>Schedule</dt><dd>{m.type === 'push' ? <>Heartbeat expected every {span(m.intervalMs)}</> : <>Every {span(m.intervalMs)} · timeout {span(m.timeoutMs)}</>}</dd>
        {m.type === 'push' && m.pushStartedAt ? <><dt>Deadline counted from</dt><dd><Time at={m.pushStartedAt} now={overview.now} /> <span className="muted small">· this check generation began</span></dd></> : null}
        <dt>Channels</dt>
        <dd>
          {m.notificationIds?.length ? (
            <span className="hdrs">{m.notificationIds.map(id => {
              const channel = channels.map.get(id)?.channel;
              return <span key={id} className="nowrap">{channel ? <><ProviderIcon kind={channel.config.kind} /><a href={href('channels', id, 'edit')}>{channel.name}</a> <span className="muted small">{providerLabel(channel.config.kind)}{channel.enabled ? '' : ' · disabled'}</span></> : <span className="muted">Unknown channel <span className="mono">{id.slice(0, 8)}</span></span>}</span>;
            })}</span>
          ) : <span className="muted">None</span>}
        </dd>
        <dt>Legacy webhook</dt>
        <dd>
          {viewer ? <span className="muted">Hidden from viewers</span>
            : m.webhookUrl ? <span className="mono wrap">{reveal ? m.webhookUrl : maskedUrl(m.webhookUrl)}</span>
            : <span className="muted">None</span>}
        </dd>
        <dt>Repeat while down</dt><dd>{m.repeatAfterMs ? `Every ${span(m.repeatAfterMs)}` : 'Off'}</dd>
        <dt>Group</dt>
        <dd>
          {!m.groupId ? <span className="muted">None</span>
            : group ? <><a href={href('groups')}>{group.group.name}</a>{group.group.outageMonitorId === m.id ? <span className="muted"> · its outage monitor</span> : dependency ? <span className="muted"> · depends on {dependency.name}</span> : null}</>
            : <span className="muted">Missing group; alerts are sent normally</span>}
        </dd>
        {m.tags?.length ? <><dt>Tags</dt><dd><MonitorTags tags={m.tags} catalog={tags.map} /></dd></> : null}
        <dt>Revision</dt><dd>{view.configConflict ? <a href={href('config')}><Glyph tone="serious" /> conflicting versions</a> : <><Rev revision={m.revision} /> <span className="muted">saved <Time at={m.updatedAt} now={overview.now} /></span></>}</dd>
      </dl>
      {hasSecrets ? <button type="button" className="linkbtn small reveal-btn" aria-pressed={reveal} onClick={() => setReveal(value => !value)}><Icon name={reveal ? 'eyeOff' : 'eye'} />{reveal ? 'Hide credentials' : 'Reveal credentials and full URLs'}</button> : null}
    </>
  );
}
const Fact = ({ label, children }: { label: string; children: ReactNode }) => <><dt>{label}</dt><dd>{children}</dd></>;

/** Editors set a manual monitor's result here. It keeps the check generation, so an open outage can recover. */
function ManualControl({ view }: { view: MonitorView }) {
  const { api, endpoint, refresh, stillCurrent } = useCanary();
  const m = view.monitor;
  const formId = useId();
  const [state, setState] = useState<'' | 'up' | 'down' | 'pending'>(m.manual?.state ?? '');
  const [message, setMessage] = useState(m.manual?.message ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const dirty = state !== (m.manual?.state ?? '') || message.trim() !== (m.manual?.message ?? '');
  const tooLong = new TextEncoder().encode(message.trim()).length > 256 || /[\x00-\x1f\x7f]/u.test(message);
  const save = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      const input = inputOf(m);
      const manual: NonNullable<typeof input.manual> = { ...(m.manual ?? {}) };
      if (state) manual.state = state; else delete manual.state;
      if (message.trim()) manual.message = message.trim(); else delete manual.message;
      if (Object.keys(manual).length) input.manual = manual; else delete input.manual;
      const saved = await api.updateMonitor(m.id, { ...input, revision: m.revision });
      if (!stillCurrent()) return;
      notify(`Set “${saved.name}” to ${state || 'not set (pending)'} on ${endpoint} (revision ${shortRev(saved.revision)}). Assigned nodes report it on their next check.`);
      refresh();
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(failure instanceof ApiFailure && failure.status === 409 ? 'The monitor changed first. Wait for the page to refresh and try again.' : describeFailure(failure, { preferDetail: true }));
    } finally { setBusy(false); }
  };
  return (
    <Card title="Manual status" hint="You decide the result. No network request is made.">
      <div className="form one">
        <Seg legend="Report" name={`${formId}-m`} value={state} onChange={setState} disabled={busy || view.configConflict}
          options={[{ value: '', label: 'Not set' }, { value: 'up', label: 'Up' }, { value: 'down', label: 'Down' }, { value: 'pending', label: 'Pending' }]} />
        <div className="fld">
          <label htmlFor={`${formId}-msg`}>Message <span className="muted">(optional)</span></label>
          <input id={`${formId}-msg`} type="text" value={message} maxLength={256} onChange={event => setMessage(event.target.value)} disabled={busy || view.configConflict} aria-invalid={tooLong ? true : undefined} />
          <small>Up recovers an open outage and sends its recovery; pending doesn’t. Each node reports the new status on its next check (every {span(m.intervalMs)}).</small>
        </div>
        {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not saved</b><p>{error}</p></div></div> : null}
        <div className="row-wrap">
          <button type="button" className="btn primary" disabled={!dirty || busy || tooLong || view.configConflict} onClick={() => { void save(); }}><Icon name="check" />{busy ? `Saving to ${endpoint}…` : 'Set status'}</button>
          {dirty ? <button type="button" className="btn" disabled={busy} onClick={() => { setState(m.manual?.state ?? ''); setMessage(m.manual?.message ?? ''); }}>Reset</button> : null}
        </div>
      </div>
    </Card>
  );
}

function DeliveriesCard({ view }: { view: MonitorView }) {
  const { api, connection, endpoint } = useCanary();
  const attempts = useResource<DeliveryAttemptView[]>(`${connection.identity}|deliveries|${view.monitor.id}`, signal => api.deliveries({ monitorId: view.monitor.id, limit: 50 }, signal), 20_000);
  if (attempts.error instanceof ApiFailure && attempts.error.status === 404) return null;
  return (
    <Card title="Delivery attempts" hint={<>Per destination, as known to <span className="mono">{endpoint}</span></>} actions={<a className="btn sm" href={href('notifications', 'deliveries')}>All<Icon name="chevRight" /></a>}>
      {attempts.error && !attempts.data ? <p className="muted small">{describeFailure(attempts.error)}</p>
        : !attempts.data ? <p className="muted small">Loading…</p>
        : attempts.data.length ? <DeliveryTable attempts={attempts.data.slice(0, 12)} compact />
        : <EmptyState title="No delivery attempts">None for this monitor yet.</EmptyState>}
    </Card>
  );
}

export function MonitorDetailView({ id }: { id: string }) {
  const { monitorMap, assessmentOf, overview, endpoint, editable, api, refresh, nodeMap, tags, stillCurrent } = useCanary();
  const view = monitorMap.get(id);
  const a = assessmentOf(id);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (!view || !a) {
    return (
      <div className="view">
        <PageHead crumbs={<a href={href('monitors')}>Monitors</a>} title="Monitor not found" />
        <Card><EmptyState title={`${endpoint} doesn’t know this monitor`}>It may have been deleted, or created on a node that hasn’t synced with {endpoint} yet.</EmptyState></Card>
      </div>
    );
  }
  const m = view.monitor;
  const incidents = overview.incidents.filter(incident => incident.monitorId === id).sort((x, y) => y.openedAt - x.openedAt);
  const receipts = overview.notifications.filter(receipt => receipt.monitorId === id).sort((x, y) => y.sentAt - x.sentAt);
  const openIncident = incidents.find(incident => incident.resolvedAt === null);
  const sender = openIncident ? receipts.filter(r => r.incidentId === openIncident.id && r.kind === 'down').sort((x, y) => x.sentAt - y.sentAt)[0]?.nodeId : undefined;

  const togglePause = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      const saved = await api.updateMonitor(m.id, { ...inputOf(m), enabled: !m.enabled, revision: m.revision });
      if (!stillCurrent()) return;
      notify(`${saved.enabled ? 'Resumed' : 'Paused'} “${saved.name}” on ${endpoint} (revision ${shortRev(saved.revision)}).${saved.enabled ? '' : ' An open incident closes without a recovery alert.'}`);
      refresh();
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure));
    } finally { setBusy(false); }
  };

  return (
    <div className="view">
      <PageHead
        crumbs={<a href={href('monitors')}>Monitors</a>}
        title={<>{m.name}<TypeTag type={m.type} /></>}
        sub={<span className="sub-row"><StateChip a={a} /><MaintenanceTag ids={view.maintenanceIds} />{m.type !== 'manual' ? <span className="mono tgt-lg">{m.target}</span> : null}<MonitorTags tags={m.tags} catalog={tags.map} /></span>}
        actions={editable ? (
          <>
            <a className={`btn${view.configConflict ? ' is-disabled' : ''}`} href={view.configConflict ? undefined : href('monitors', m.id, 'edit')} aria-disabled={view.configConflict || undefined}><Icon name="edit" />Edit</a>
            <button type="button" className="btn" onClick={() => { void togglePause(); }} disabled={busy || view.configConflict}><Icon name={m.enabled ? 'pause' : 'play'} />{busy ? 'Saving…' : m.enabled ? 'Pause' : 'Resume'}</button>
            <button type="button" className="btn danger" onClick={() => setConfirmDelete(true)} disabled={busy || view.configConflict}><Icon name="trash" />Delete</button>
          </>
        ) : <ReadOnly />}
      />
      {error ? <Banner tone="down" role="alert" title="Not saved">{error}</Banner> : null}
      {view.configConflict ? (
        <Banner tone="warn" title="Configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Review and resolve</a>}>
          Nodes accepted different edits while they couldn’t reach each other. Editing, pausing and deleting wait until someone picks the version to keep.
        </Banner>
      ) : null}
      {view.maintenanceIds?.length ? (
        <Banner tone="hold" title="Planned maintenance is holding this monitor’s alerts">
          Checks, history and incidents continue, and the result above is still what was measured. Owed notifications go out after the window if the outage continues. <a href={href('maintenance')}>See maintenance</a>
        </Banner>
      ) : null}
      {view.suppressedByGroupId ? (
        <Banner tone="hold" title="Notifications for this monitor are held back">
          <SuppressionLine groupId={view.suppressedByGroupId} reason={view.suppressionReason} />
        </Banner>
      ) : null}
      <div className="g2">
        <div className="vstack">
          <Card title="Now, by location" hint={<>Latest result from each assigned node, as known to <span className="mono">{endpoint}</span></>}>
            <p className="interp">{interpretation(view, a)}</p>
            <LocationTable view={view} />
          </Card>
          {m.type === 'manual' && editable ? <ManualControl key={m.revision} view={view} /> : null}
          <HistoryCard view={view} />
        </div>
        <div className="vstack">
          <Card title="Details"><Facts view={view} /></Card>
          <Card title="Notification order" hint="Assigned nodes in ID order">
            <CandidateOrder ids={m.nodeIds} nodes={nodeMap} sender={sender} />
            <p className="foot-note">
              {sender ? <><span className="mono">{sender}</span> sent the down alert for the open incident. </> : null}
              Nodes take turns per destination; one that sees another’s receipt stands down. Out-of-view nodes may send on their own.
            </p>
          </Card>
          <Card title="Incidents" hint="Known to this endpoint">
            {incidents.length ? <ul className="inc-list compact">{incidents.slice(0, 6).map(incident => <IncidentRow key={incident.id} incident={incident} />)}</ul> : <EmptyState title="No incidents">{endpoint} knows of no incidents for this monitor.</EmptyState>}
          </Card>
          <DeliveriesCard view={view} />
          <Card title="Receipts"><ReceiptList receipts={receipts} limit={8} /></Card>
        </div>
      </div>
      {confirmDelete ? <DeleteDialog view={view} onClose={() => setConfirmDelete(false)} /> : null}
    </div>
  );
}

function DeleteDialog({ view, onClose }: { view: MonitorView; onClose: () => void }) {
  const { api, endpoint, refresh, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const m = view.monitor;
  const remove = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      await api.deleteMonitor(m.id, m.revision);
      if (!stillCurrent()) return;
      notify(`Deleted “${m.name}” on ${endpoint}. Peers keep the deletion when they sync.`);
      refresh();
      navigate(href('monitors'));
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure));
      setBusy(false);
    }
  };
  return (
    <Modal title={`Delete ${m.name}?`} onClose={onClose} busy={busy}>
      <p>This stops the check on {plural(m.nodeIds.length, 'node')} and writes a deletion that peers respect, so it stays deleted when nodes reconnect. An open incident closes without a recovery alert.</p>
      <p className="muted small">Deleting revision <Rev revision={m.revision} />. If someone changed it in the meantime, {endpoint} rejects the deletion.</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not deleted</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void remove(); }} disabled={busy}><Icon name="trash" />{busy ? 'Deleting…' : 'Delete monitor'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
    </Modal>
  );
}
