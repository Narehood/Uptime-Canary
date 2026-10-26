import type { FleetDeployment, Monitor, MonitorView, Observation, Overview, RegisteredNode, Role } from '@uptime-canary/shared';
import { ApiFailure, type CanaryApi } from './api';
import { pendingOperation } from './dashboard';
import { assess, nodeColumns, type Assessment } from './model';

/** Fleet reads are independent of the selected node. They keep the same dashboard credential and origin. */
export const FLEET_READ_CONCURRENCY = 4;
export const FLEET_REFRESH_MS = 15_000;
export const FLEET_SNAPSHOT_MAX_AGE_MS = 30_000;

export function managedMonitor(deployments: readonly FleetDeployment[], registrationId: string | undefined, monitorId: string): FleetDeployment | undefined {
  return deployments.find(d => d.state !== 'removed' && (d.placements.some(p => p.monitorId === monitorId && (p.registrationId === registrationId || p.registrationIds.includes(registrationId ?? '')))
    || d.source?.monitorId === monitorId && d.source.registrationId === registrationId));
}

export interface FleetSource {
  node: RegisteredNode;
  status: 'loading' | 'ready' | 'unavailable' | 'pending';
  /** Last successful read, browser clock. Never used as the monitoring node's measurement clock. */
  readAt: number | null;
  overview?: Overview;
  error?: unknown;
}

export interface FleetResult {
  nodeId: string;
  name: string;
  state: Observation['state'] | 'paused';
  message: string;
  observation?: Observation;
  /** Registration whose snapshot reports this result; absent when no valid snapshot reports it. */
  registrationId?: string;
  readAt: number | null;
  /** True when the result came through another HA peer, not directly from this node. */
  viaPeer: boolean;
}

export interface FleetMonitorRow {
	deployment?: FleetDeployment;
  key: string;
  monitor: Monitor;
  assessment: Assessment;
  results: FleetResult[];
  /** The actual registered views, retained for contextual navigation and revision checks. */
  sources: Array<{ source: FleetSource; view: MonitorView }>;
  /** Different revisions are displayed as disagreement; this is not a claim of a causal configuration conflict. */
  differingViews: boolean;
}

/** Never treat a cached healthy response as current after a failed read, pending operation or stale snapshot. */
export function fleetSourceCurrent(source: FleetSource, now: number): boolean {
  return source.status === 'ready' && source.overview !== undefined && source.readAt !== null
    && Number.isFinite(source.readAt) && now >= source.readAt && now - source.readAt <= FLEET_SNAPSHOT_MAX_AGE_MS;
}

/**
 * Read at most the server's 64 registrations with four workers. A slow or refused node leaves other nodes usable.
 * A dashboard 401/403 stops the whole batch and returns no old privileged data. A node refusal is a dashboard 502.
 * Queued reads stop on cancellation; progress is published only while this batch is live.
 */
export async function readFleet(api: CanaryApi, nodes: readonly RegisteredNode[], role: Role, signal: AbortSignal,
  previous: readonly FleetSource[] = [], progress?: (source: FleetSource) => void,
  now: () => number = Date.now): Promise<FleetSource[]> {
  if (nodes.length > 64 || new Set(nodes.map(node => node.id)).size !== nodes.length) {
    throw new ApiFailure(200, 'The dashboard returned an invalid node list.');
  }
  const controller = new AbortController();
  const combined = AbortSignal.any([signal, controller.signal]);
  const held = new Map(previous.map(source => [source.node.id, source]));
  const results = new Array<FleetSource>(nodes.length);
  let next = 0;
  let denied: unknown;
  const publish = (index: number, source: FleetSource): void => {
    if (combined.aborted) return;
    results[index] = source;
    progress?.(source);
  };
  const worker = async (): Promise<void> => {
    while (!combined.aborted && next < nodes.length) {
      const index = next++, node = nodes[index];
      const old = held.get(node.id);
      // Renames preserve data; a registration now representing another identity must never inherit its old snapshot.
      const cached = old?.overview?.node.id === node.nodeId ? old.overview : undefined;
      const readAt = cached ? old!.readAt : null;
      if (pendingOperation(node.state)) { publish(index, { node, status: 'pending', overview: cached, readAt }); continue; }
      try {
        const overview = await api.withRegisteredNode(node.id).overview(combined);
        if (overview.node.id !== node.nodeId || overview.user.role !== role) throw new ApiFailure(502, 'The registered node returned an invalid overview.');
        publish(index, { node, status: 'ready', overview, readAt: now() });
      } catch (error) {
        if (signal.aborted || combined.aborted) return;
        if (error instanceof ApiFailure && (error.status === 401 || error.status === 403)) {
          denied = error;
          controller.abort();
          return;
        }
        publish(index, { node, status: 'unavailable', overview: cached, readAt, error });
      }
    }
  };
  await Promise.all(Array.from({ length: Math.min(FLEET_READ_CONCURRENCY, nodes.length) }, worker));
  if (denied) throw denied;
  if (signal.aborted) throw new DOMException('The fleet read was cancelled.', 'AbortError');
  return results;
}

/**
 * Legacy monitors can share an ID only within a known peer scope. Require both registered nodes to report having
 * contacted the other as a configured peer; names, equal IDs or similar configurations alone never merge scopes.
 * Disconnected nodes can retain that last-known scope, while their current health remains unknown.
 */
function peerScopes(sources: readonly FleetSource[]): Map<string, string> {
  const parent = new Map(sources.map(source => [source.node.id, source.node.id]));
  const root = (id: string): string => { let p = parent.get(id)!; while (p !== parent.get(p)) p = parent.get(p)!; return p; };
  const hasSeen = (source: FleetSource, other: FleetSource): boolean => Boolean(source.overview?.nodes.some(node =>
    node.id === other.node.nodeId && node.state !== 'local' && typeof node.lastSeenAt === 'number' && node.lastSeenAt > 0));
  for (let i = 0; i < sources.length; i++) for (let j = i + 1; j < sources.length; j++) {
    const a = sources[i], b = sources[j];
    if (hasSeen(a, b) && hasSeen(b, a)) {
      const x = root(a.node.id), y = root(b.node.id);
      if (x !== y) parent.set(x < y ? y : x, x < y ? x : y);
    }
  }
  return new Map(sources.map(source => [source.node.id, root(source.node.id)]));
}

/**
 * A central inventory of existing node monitors. Per-node observations keep their reporting source and read time.
 * Placement/deployment authority is deliberately separate: this projection never writes configuration or peers nodes.
 */
export function fleetMonitorRows(sources: readonly FleetSource[], now: number, deployments: readonly FleetDeployment[] = []): FleetMonitorRow[] {
  const scopes = peerScopes(sources);
  const grouped = new Map<string, FleetMonitorRow['sources']>();
  for (const source of sources) {
    if (source.overview?.node.id !== source.node.nodeId) continue;
    for (const view of source.overview.monitors) {
      if (view.monitor.deleted) continue;
      const key = JSON.stringify([scopes.get(source.node.id), view.monitor.id]);
      const members = grouped.get(key) ?? [];
      members.push({ source, view }); grouped.set(key, members);
    }
  }
  const rows: FleetMonitorRow[] = [];
  for (const [key, unsorted] of grouped) {
    const members = [...unsorted].sort((a, b) => a.source.node.id.localeCompare(b.source.node.id));
    const current = members.filter(member => fleetSourceCurrent(member.source, now));
    const primary = current[0] ?? members[0];
    const monitor = primary.view.monitor;
    const differingViews = new Set(members.map(member => member.view.monitor.revision)).size > 1;
    const assigned = [...new Set(members.flatMap(member => member.view.monitor.nodeIds))].sort();
    const results: FleetResult[] = assigned.map(nodeId => {
      const direct = members.find(member => member.source.node.nodeId === nodeId && fleetSourceCurrent(member.source, now));
      const candidates = direct ? [direct] : current.filter(member => member.view.monitor.revision === monitor.revision);
      const readings = candidates.filter(member => member.view.monitor.nodeIds.includes(nodeId)).map(member => ({
        member, observation: member.view.observations.find(value => value.nodeId === nodeId),
      }));
      const picked = readings.find(({ member, observation }) => member.view.state === 'paused' || observation !== undefined);
      const name = sources.find(source => source.node.nodeId === nodeId)?.node.label
        ?? primary.source.overview?.nodes.find(node => node.id === nodeId)?.name ?? nodeId;
      if (!picked) return { nodeId, name, state: 'unknown', message: 'No current node snapshot reports a result.', readAt: null, viaPeer: false };
      const { member, observation } = picked;
      const states = new Set(readings.map(reading => reading.member.view.state === 'paused' ? 'paused' : reading.observation?.state ?? 'unknown'));
      const disputed = !direct && states.size > 1;
      const state = disputed ? 'unknown' : member.view.state === 'paused' ? 'paused' : observation?.state ?? 'unknown';
      return { nodeId, name, state, message: disputed ? 'Reporting peers disagree; open their views to compare.' : observation?.message ?? 'Paused',
        ...(observation ? { observation } : {}), registrationId: member.source.node.id, readAt: member.source.readAt,
        viaPeer: member.source.node.nodeId !== nodeId };
    });
    const observations: Observation[] = results.map(result => ({
      monitorId: monitor.id, nodeId: result.nodeId, state: result.state === 'paused' ? 'unknown' : result.state,
      checkedAt: result.observation?.checkedAt ?? 0, latencyMs: result.observation?.latencyMs ?? 0,
      latencyAvailable: result.observation ? result.observation.latencyAvailable !== false : false, message: result.message,
    }));
    const state = results.length && results.every(result => result.state === 'paused') ? 'paused'
      : results.some(result => result.state === 'down') ? 'down'
        : !results.length || results.some(result => result.state === 'unknown' || result.state === 'paused') ? 'unknown'
          : results.some(result => result.state === 'pending') ? 'pending' : 'up';
    const merged: MonitorView = { monitor: { ...monitor, nodeIds: assigned }, observations, state,
      configConflict: differingViews || members.some(member => member.view.configConflict),
      unknownNodes: results.filter(result => result.state === 'unknown').length };
    const nodes = nodeColumns({ ...primary.source.overview!, monitors: [merged] });
    const assessment = assess(merged, new Map(nodes.map(node => [node.id, node])));
    if (differingViews) assessment.detail = 'node views have different revisions';
    rows.push({ key, monitor, assessment, results, sources: members, differingViews });
  }
  const consumed = new Set<string>();
  const managed: FleetMonitorRow[] = [];
  for (const deployment of deployments) {
    const matches = (row: FleetMonitorRow): boolean => row.sources.some(({ source, view }) =>
      deployment.placements.some(p => p.monitorId === view.monitor.id && (p.registrationId === source.node.id || p.registrationIds.includes(source.node.id)))
      || deployment.source?.registrationId === source.node.id && deployment.source.monitorId === view.monitor.id);
    const copies = rows.filter(matches);
    copies.forEach(row => consumed.add(row.key));
    if (deployment.state === 'removed') continue;
    const results: FleetResult[] = deployment.registrationIds.map(registrationId => {
      const source = sources.find(s => s.node.id === registrationId);
      const p = deployment.placements.find(p => p.registrationIds.includes(registrationId));
      const row = copies.find(r => r.sources.some(m => m.view.monitor.id === p?.monitorId));
      const found = row?.results.find(r => r.nodeId === source?.node.nodeId);
      return found ?? { nodeId: source?.node.nodeId ?? registrationId, name: source?.node.label ?? registrationId,
        state: 'unknown', message: 'No current result for this placement.', readAt: null, viaPeer: false };
    });
    const members = copies.flatMap(row => row.sources);
    const drift = deployment.placements.some(p => p.state === 'applied' && members.some(m =>
      m.source.node.id === p.registrationId && m.view.monitor.id === p.monitorId && m.view.monitor.revision !== p.revision));
    const conflict = members.some(m => m.view.configConflict) || drift;
    const observations: Observation[] = results.map(r => ({ monitorId: deployment.id, nodeId: r.nodeId,
      state: r.state === 'paused' ? 'unknown' : r.state, checkedAt: r.observation?.checkedAt ?? 0,
      latencyMs: r.observation?.latencyMs ?? 0, latencyAvailable: r.observation?.latencyAvailable ?? false, message: r.message }));
    const state = deployment.state !== 'applied' ? 'unknown' : results.length && results.every(r => r.state === 'paused') ? 'paused'
      : results.some(r => r.state === 'down') ? 'down' : !results.length || results.some(r => r.state === 'unknown' || r.state === 'paused') ? 'unknown'
        : results.some(r => r.state === 'pending') ? 'pending' : 'up';
    const monitor = { ...deployment.monitor, nodeIds: results.map(r => r.nodeId) };
    const assessment = assess({ monitor, state, observations, unknownNodes: results.filter(r => r.state === 'unknown').length, configConflict: conflict }, new Map());
    if (deployment.state !== 'applied') assessment.detail = deployment.reason || 'Placement is waiting for confirmation';
    else if (drift) assessment.detail = 'Changed directly on a node; review before saving';
    managed.push({ key: `fleet:${deployment.id}`, monitor, assessment, results, sources: members, differingViews: conflict, deployment });
  }
  return [...rows.filter(row => !consumed.has(row.key)), ...managed].sort((a, b) => a.assessment.severity - b.assessment.severity || a.monitor.name.localeCompare(b.monitor.name) || a.key.localeCompare(b.key));
}
