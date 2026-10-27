import type { Canary } from './context';
import type { FleetDeployment, RegisteredNode } from '@uptime-canary/shared';
import { ApiFailure } from './api';
import { managedMonitor, fleetSourceCurrent, type FleetSource } from './fleet';

export function fleetEditingRegistration(deployment: FleetDeployment, nodes: ReadonlyMap<string, RegisteredNode>): RegisteredNode | undefined {
  const ids = deployment.placements.filter(p => p.state !== 'removed').flatMap(p => [p.registrationId, ...p.registrationIds]);
  return [...ids, ...deployment.registrationIds].map(id => nodes.get(id)).find(node => node !== undefined);
}

/** Do not fall back to a node-local write while the dashboard's ownership map is unreadable. */
export function selectedDeployment(ctx: Canary, monitorId: string) {
  if (!ctx.controlPlane || !ctx.fleet) return undefined;
  if (!ctx.fleet.ready || ctx.fleet.error || ctx.fleet.denied) throw new ApiFailure(409, 'Reload the saved fleet placements before changing this monitor.');
  return managedMonitor(ctx.fleet.deployments, ctx.selection?.id, monitorId);
}
/** Validate at the time of the action, not a timer tick captured by render. */
export function reviewedFleetHeads(deployment: FleetDeployment, sources: readonly FleetSource[], now = Date.now()): { revisions: Record<string, string>; labels: Record<string, string> } | null {
  const revisions: Record<string, string> = {}, labels: Record<string, string> = {};
  for (const placement of deployment.placements.filter(p => p.state !== 'removed')) {
    const source = sources.find(s => s.node.id === placement.registrationId);
    const view = source?.overview?.monitors.find(v => v.monitor.id === placement.monitorId);
    if (!source || !fleetSourceCurrent(source, now)) return null;
    if (!view && placement.sequence === 0 && placement.monitorId === `fleet-${placement.id}`) continue;
    if (!view || view.configConflict) return null;
    revisions[placement.id] = view.monitor.revision; labels[placement.id] = source.node.label;
  }
  return Object.keys(revisions).length ? { revisions, labels } : null;
}
