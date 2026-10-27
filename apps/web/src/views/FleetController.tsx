import { useEffect, useState } from 'react';
import type { FleetDeployment } from '@uptime-canary/shared';
import { useCanary } from '../context';
import { fleetMonitorRows } from '../fleet';
import { fleetEditingRegistration, reviewedFleetHeads } from '../fleetActions';
import { useFleetReader } from '../fleetState';
import { useNow } from '../hooks';
import { canEdit } from '../model';
import { href, navigate } from '../router';
import { FleetOverviewView } from './FleetOverview';
import { FleetNodeGroupsView } from './FleetNodeGroups';
import { Modal, Rev } from '../ui/bits';

/** Integration only: all traffic is same-origin and the presentation receives no credentials. */
export function FleetController({ groups = false, view = 'overview' }: { groups?: boolean; view?: 'overview' | 'monitors' }) {
  const { api, registry, fleet, connection, role, select, selection, search, setSearch } = useCanary();
  useNow(1000);
  const now = Date.now();
  const [failure, setFailure] = useState<string>();
  const [removing, setRemoving] = useState<FleetDeployment>();
  const [reconciling, setReconciling] = useState<{ deployment: FleetDeployment; revisions: Record<string, string>; labels: Record<string, string> }>();
  const [operation, setOperation] = useState(false);
  const live = connection.link !== 'rejected' && !fleet?.denied;
  const reader = useFleetReader(api, registry.list ?? [], role, `${connection.identity}|${role}`, live && !groups && registry.list !== null);
  const refresh = (): void => { setFailure(undefined); registry.reload(); fleet?.reload(); reader.reload(); };
  const editable = live && canEdit(role) && !operation;
  // While a group is collecting monitors, read its progress 5 s after each answer rather than the catalogue's usual 15 s.
  // Re-armed by each new answer only, so a slow read is never cut short.
  const syncing = groups && live && Boolean(fleet?.groups.some(group => group.syncState === 'pending'));
  const reloadFleet = fleet?.reload;
  const answered = fleet?.groups;
  useEffect(() => {
    if (!syncing || !reloadFleet) return;
    const timer = setTimeout(reloadFleet, 5000);
    return () => clearTimeout(timer);
  }, [syncing, reloadFleet, answered]);
  if (groups) {
    const counts: Record<string, number> = {};
    for (const deployment of live ? fleet?.deployments ?? [] : []) {
      if (deployment.state === 'removed') continue;
      for (const id of new Set(deployment.groupIds ?? [])) counts[id] = (counts[id] ?? 0) + 1;
    }
    // Outage dependencies belong to one node; with none selected yet, that page asks which.
    return <FleetNodeGroupsView nodes={live ? registry.list ?? [] : []} groups={live ? fleet?.groups ?? [] : []} monitorCounts={live && fleet?.ready ? counts : undefined}
      onOpenDependencies={() => navigate(href('outage-groups'))}
      loading={registry.loading || Boolean(fleet?.loading)} editable={editable} error={failure ?? fleet?.error}
      onRefresh={refresh} onSave={async (input, existing) => {
        try { await api.saveNodeGroup(input, existing); fleet?.reload(); }
        catch (error) { setFailure('The group was not confirmed. Reload to check its latest revision and membership before retrying.'); fleet?.reload(); throw error; }
      }} onSync={async group => {
        // A sync gives the group a new revision, so the catalogue is read again either way.
        try { await api.syncNodeGroup(group); } finally { fleet?.reload(); }
      }} onRemove={async group => {
        try { await api.removeNodeGroup(group); fleet?.reload(); }
        catch (error) { setFailure('The removal was not confirmed. Reload the saved groups before retrying.'); fleet?.reload(); throw error; }
      }} />;
  }
  return <><FleetOverviewView view={view} sources={reader.sources} rows={fleetMonitorRows(reader.sources, now, live ? fleet?.deployments : [])}
    now={now} loading={registry.loading && !registry.list || reader.loading} refreshing={reader.refreshing}
    error={failure ?? reader.error ?? fleet?.error ?? (registry.error ? 'The node registry could not be refreshed; some nodes may be missing.' : undefined)}
    search={search} onSearchChange={setSearch} onRefresh={refresh}
    onOpen={(registrationId, monitorId) => {
      const node = registry.map.get(registrationId); if (!node || !live) return;
      if (selection?.id !== node.id) select(node);
      navigate(href('monitors', monitorId));
    }}
    onManageGroups={editable ? () => navigate(href('node-groups')) : undefined}
    onRetryDeployment={editable ? deployment => {
      setOperation(true); setFailure(undefined);
      void api.retryFleetMonitor(deployment).catch(() => setFailure('The retry was not confirmed. Refresh the saved deployment before trying again.')).finally(() => { setOperation(false); fleet?.reload(); });
    } : undefined}
    onRemoveDeployment={editable ? deployment => setRemoving(deployment) : undefined}
    onReconcileDeployment={editable ? deployment => {
      const reviewed = reviewedFleetHeads(deployment, reader.sources);
      if (!reviewed) {
        setFailure('Refresh every placement node and resolve configuration conflicts before reviewing node edits. A monitor deleted directly can be retired with Remove everywhere.'); return;
      }
      setReconciling({ deployment, ...reviewed });
    } : undefined}
    onEditDeployment={editable ? deployment => {
      // The form lists the selected node's channels and tags, so open it through the node they're copied from.
      const source = deployment.syncNotifications && deployment.notificationSourceRegistrationId ? registry.map.get(deployment.notificationSourceRegistrationId) : undefined;
      const node = source ?? fleetEditingRegistration(deployment, registry.map);
      if (!node) { setFailure('Register a destination node before editing this placement.'); return; }
      if (selection?.id !== node.id) select(node);
      navigate(href('fleet-monitors', deployment.id, 'edit'));
    } : undefined}
    onAdd={editable ? () => {
      const node = (selection ? registry.map.get(selection.id) : undefined) ?? registry.list?.find(n => n.state === 'answering' || n.state === 'starting');
      if (!node) { navigate(href('nodes')); return; }
      if (selection?.id !== node.id) select(node);
      navigate(href('monitors', 'new'));
    } : undefined} />
    {reconciling && live && canEdit(role) ? <Modal title={`Replace node edits: ${reconciling.deployment.monitor.name}?`} busy={operation} onClose={() => { if (!operation) setReconciling(undefined); }}>
      <p>Replace the node settings at these reviewed revisions with the dashboard’s saved configuration. Any further node change refuses this operation. Resolve an unknown saved command with Retry placement first.</p>
      <ul>{Object.entries(reconciling.revisions).map(([id, revision]) => <li key={id}>{reconciling.labels[id]}: <Rev revision={revision} /></li>)}</ul>
      <div className="row-wrap modal-acts"><button type="button" className="btn danger-solid" disabled={operation} onClick={() => {
        setOperation(true); setFailure(undefined);
        void api.reconcileFleetMonitor(reconciling.deployment, reconciling.revisions).then(() => setReconciling(undefined))
          .catch(() => setFailure('Reconciliation was not confirmed. Refresh the deployment and node revisions before reviewing again.'))
          .finally(() => { setOperation(false); fleet?.reload(); reader.reload(); });
      }}>Replace reviewed node settings</button><button type="button" className="btn" disabled={operation} onClick={() => setReconciling(undefined)}>Keep node edits</button></div>
    </Modal> : null}
    {removing && live && canEdit(role) ? <Modal title={`${removing.canCancel ? 'Cancel placement' : 'Remove everywhere'}: ${removing.monitor.name}?`} busy={operation} onClose={() => { if (!operation) setRemoving(undefined); }}>
      <p>{removing.canCancel ? 'Cancel this saved plan before any node receives a configuration change. Existing monitors stay as they are.' : 'Delete this managed monitor from every assigned node. Offline placements stay pending until their nodes confirm the deletion.'}</p>
      <div className="row-wrap modal-acts"><button type="button" className="btn danger-solid" disabled={operation} onClick={() => {
        setOperation(true);setFailure(undefined);void api.removeFleetMonitor(removing).then(() => setRemoving(undefined)).catch(() => setFailure('Removal was not confirmed. Refresh and review the saved deployment.')).finally(() => { setOperation(false); fleet?.reload(); reader.reload(); });
      }}>Confirm {removing.canCancel ? 'cancellation' : 'removal'}</button><button type="button" className="btn" disabled={operation} onClick={() => setRemoving(undefined)}>Keep it</button></div>
    </Modal> : null}</>;
}
