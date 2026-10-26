import { useState } from 'react';
import type { FleetDeployment } from '@uptime-canary/shared';
import { useCanary } from '../context';
import { fleetMonitorRows } from '../fleet';
import { useFleetReader } from '../fleetState';
import { useNow } from '../hooks';
import { canEdit } from '../model';
import { href, navigate } from '../router';
import { FleetOverviewView } from './FleetOverview';
import { FleetNodeGroupsView } from './FleetNodeGroups';
import { Modal } from '../ui/bits';

/** Integration only: all traffic is same-origin and the presentation receives no credentials. */
export function FleetController({ groups = false }: { groups?: boolean }) {
  const { api, registry, fleet, connection, role, select, selection, search, setSearch } = useCanary();
  const now = useNow(1000);
  const [failure, setFailure] = useState<string>();
  const [removing, setRemoving] = useState<FleetDeployment>();
  const [operation, setOperation] = useState(false);
  const live = connection.link !== 'rejected' && !fleet?.denied;
  const reader = useFleetReader(api, registry.list ?? [], role, `${connection.identity}|${role}`, live && !groups && registry.list !== null);
  const refresh = (): void => { setFailure(undefined); registry.reload(); fleet?.reload(); reader.reload(); };
  const editable = live && canEdit(role) && !operation;
  if (groups) return <FleetNodeGroupsView nodes={live ? registry.list ?? [] : []} groups={live ? fleet?.groups ?? [] : []}
    onOpenDependencies={selection ? () => navigate(href('outage-groups')) : undefined}
    loading={registry.loading || Boolean(fleet?.loading)} editable={editable} error={failure ?? fleet?.error}
    onRefresh={refresh} onSave={async (input, existing) => {
      try { await api.saveNodeGroup(input, existing); fleet?.reload(); }
      catch (error) { setFailure('The group was not confirmed. Reload to check its latest revision and membership before retrying.'); fleet?.reload(); throw error; }
    }} onRemove={async group => {
      try { await api.removeNodeGroup(group); fleet?.reload(); }
      catch (error) { setFailure('The removal was not confirmed. Reload the saved groups before retrying.'); fleet?.reload(); throw error; }
    }} />;
  return <><FleetOverviewView sources={reader.sources} rows={fleetMonitorRows(reader.sources, now, live ? fleet?.deployments : [])}
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
    onEditDeployment={editable ? deployment => {
      const node = registry.map.get(deployment.placements[0]?.registrationId ?? deployment.registrationIds[0] ?? '');
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
    {removing && live && canEdit(role) ? <Modal title={`${removing.canCancel ? 'Cancel placement' : 'Remove everywhere'}: ${removing.monitor.name}?`} busy={operation} onClose={() => { if (!operation) setRemoving(undefined); }}>
      <p>{removing.canCancel ? 'Cancel this saved plan before any node receives a configuration change. Existing monitors stay as they are.' : 'Delete this managed monitor from every assigned node. Offline placements stay pending until their nodes confirm the deletion.'}</p>
      <div className="row-wrap modal-acts"><button type="button" className="btn danger-solid" disabled={operation} onClick={() => {
        setOperation(true);setFailure(undefined);void api.removeFleetMonitor(removing).then(() => setRemoving(undefined)).catch(() => setFailure('Removal was not confirmed. Refresh and review the saved deployment.')).finally(() => { setOperation(false); fleet?.reload(); reader.reload(); });
      }}>Confirm {removing.canCancel ? 'cancellation' : 'removal'}</button><button type="button" className="btn" disabled={operation} onClick={() => setRemoving(undefined)}>Keep it</button></div>
    </Modal> : null}</>;
}
