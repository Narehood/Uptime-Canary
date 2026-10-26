import type { Canary } from './context';
import { ApiFailure } from './api';
import { managedMonitor } from './fleet';

/** Do not fall back to a node-local write while the dashboard's ownership map is unreadable. */
export function selectedDeployment(ctx: Canary, monitorId: string) {
  if (!ctx.controlPlane || !ctx.fleet) return undefined;
  if (!ctx.fleet.ready || ctx.fleet.error || ctx.fleet.denied) throw new ApiFailure(409, 'Reload the saved fleet placements before changing this monitor.');
  return managedMonitor(ctx.fleet.deployments, ctx.selection?.id, monitorId);
}
