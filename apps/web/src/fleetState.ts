import { useCallback, useEffect, useRef, useState } from 'react';
import type { FleetDeployment, FleetNodeGroup, RegisteredNode, Role } from '@uptime-canary/shared';
import { ApiFailure, type CanaryApi } from './api';
import { pendingOperation } from './dashboard';
import { FLEET_REFRESH_MS, readFleet, type FleetSource } from './fleet';

export interface FleetCatalog {
  groups: FleetNodeGroup[]; deployments: FleetDeployment[]; loading: boolean; ready: boolean; denied: boolean; error?: string; reload: () => void;
}

/** No catalogue survives a credential/role change or a 401/403. No browser persistence. */
export function useFleetCatalog(api: CanaryApi, key: string, enabled: boolean): FleetCatalog {
  const [state, setState] = useState<{ key: string; groups: FleetNodeGroup[]; deployments: FleetDeployment[]; loading: boolean; ready: boolean; denied: boolean; error?: string }>({ key, groups: [], deployments: [], loading: true, ready: false, denied: false });
  const [nonce, setNonce] = useState(0);
  const reload = useCallback(() => setNonce(n => n + 1), []);
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined; let running = false; let denied = false;
    const run = async (): Promise<void> => {
      if (running || denied || controller.signal.aborted) return;
      if (document.hidden) { timer = setTimeout(() => void run(), FLEET_REFRESH_MS); return; }
      running = true;
      try {
        const [groups, deployments] = await Promise.all([api.nodeGroups(controller.signal), api.fleetMonitors(controller.signal)]);
        if (!controller.signal.aborted) setState({ key, groups, deployments, loading: false, ready: true, denied: false });
      } catch (error) {
        if (controller.signal.aborted) return;
        denied = error instanceof ApiFailure && (error.status === 401 || error.status === 403);
        setState(old => ({ key, groups: denied || old.key !== key ? [] : old.groups, deployments: denied || old.key !== key ? [] : old.deployments,
          loading: false, ready: !denied && old.key === key && old.ready, denied, error: denied ? 'Your dashboard sign-in no longer permits this view.' : 'The dashboard fleet could not be read. Retry to refresh its saved placements and groups.' }));
      } finally { running = false; }
      if (!controller.signal.aborted && !denied) timer = setTimeout(() => void run(), FLEET_REFRESH_MS);
    };
    const visible = (): void => { if (!document.hidden && !running) { if (timer) clearTimeout(timer); void run(); } };
    document.addEventListener('visibilitychange', visible); void run();
    return () => { controller.abort(); if (timer) clearTimeout(timer); document.removeEventListener('visibilitychange', visible); };
  }, [api, key, enabled, nonce]);
  const current = enabled && state.key === key ? state : { groups: [], deployments: [], loading: enabled, ready: false, denied: false };
  return { ...current, reload };
}

/** Four same-origin readers, incremental results, no overlap and cancellation before publishing a replaced view. */
export function useFleetReader(api: CanaryApi, nodes: readonly RegisteredNode[], role: Role, key: string, enabled: boolean) {
  const [state, setState] = useState<{ key: string; sources: FleetSource[]; loading: boolean; refreshing: boolean; error?: string }>({ key, sources: [], loading: true, refreshing: false });
  const [nonce, setNonce] = useState(0); const latest = useRef(nodes); latest.current = nodes;
  const cache = useRef<{ key: string; sources: FleetSource[] }>({ key, sources: [] });
  const topology = JSON.stringify(nodes.map(n => [n.id, n.nodeId, pendingOperation(n.state)]).sort());
  const reload = useCallback(() => setNonce(n => n + 1), []);
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined; let held: FleetSource[] = cache.current.key === key ? cache.current.sources : [];
    let running = false; let denied = false;
    const run = async (): Promise<void> => {
      if (running || denied || controller.signal.aborted) return;
      if (document.hidden) { timer = setTimeout(() => void run(), FLEET_REFRESH_MS); return; }
      running = true;
      const list = latest.current;
      setState(old => ({ key, sources: held.length ? held : list.map(node => ({ node, status: 'loading', readAt: null })), loading: !held.length, refreshing: true }));
      try {
        held = await readFleet(api, list, role, controller.signal, held, source => {
          if (controller.signal.aborted) return;
          setState(old => ({ ...old, key, sources: list.map(node => node.id === source.node.id ? source : old.sources.find(s => s.node.id === node.id) ?? { node, status: 'loading', readAt: null }) }));
        });
        if (!controller.signal.aborted) { cache.current = { key, sources: held }; setState({ key, sources: held, loading: false, refreshing: false }); }
      } catch (error) {
        if (controller.signal.aborted) return;
        denied = error instanceof ApiFailure && (error.status === 401 || error.status === 403);
        if (denied) cache.current = { key, sources: [] };
        setState({ key, sources: [], loading: false, refreshing: false, error: denied ? 'Your dashboard sign-in no longer permits reading these nodes.' : 'The registered node list could not be read.' });
      } finally { running = false; }
      if (!controller.signal.aborted && !denied) timer = setTimeout(() => void run(), FLEET_REFRESH_MS);
    };
    const visible = (): void => { if (!document.hidden && !running) { if (timer) clearTimeout(timer); void run(); } };
    document.addEventListener('visibilitychange', visible); void run();
    return () => { controller.abort(); if (timer) clearTimeout(timer); document.removeEventListener('visibilitychange', visible); };
  }, [api, role, key, topology, enabled, nonce]);
  return { ...(enabled && state.key === key ? state : { sources: [], loading: enabled, refreshing: false, error: undefined }), reload };
}
