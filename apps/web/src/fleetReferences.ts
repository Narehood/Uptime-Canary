import { useCallback, useEffect, useState } from 'react';
import type { FleetTargetReferences, MonitorInput, RegisteredNode } from '@uptime-canary/shared';
import { ApiFailure, type CanaryApi } from './api';

export interface FleetReferenceOption { id: string; name: string; enabled?: boolean; isDefault?: boolean; conflicted?: boolean }
export interface FleetReferenceProfile {
  registrationId: string; label: string; nodeId: string; status: 'loading' | 'ready' | 'unavailable';
  channels: FleetReferenceOption[]; groups: FleetReferenceOption[]; tags: FleetReferenceOption[];
}
export interface FleetReferenceFieldsProps {
  profiles: readonly FleetReferenceProfile[];
  base: Pick<MonitorInput, 'notificationIds' | 'groupId' | 'tags'>;
  overrides: Readonly<Record<string, FleetTargetReferences>>;
  onChange: (overrides: Record<string, FleetTargetReferences>) => void;
  disabled?: boolean;
  onRetry: () => void;
}

/** Keep only resource names/IDs. Do not retain channel settings, destinations or credentials. */
export async function readReferenceProfiles(api: CanaryApi, nodes: readonly RegisteredNode[], signal: AbortSignal,
  progress: (profile: FleetReferenceProfile) => void): Promise<void> {
  if (nodes.length > 64 || new Set(nodes.map(n => n.id)).size !== nodes.length) throw new ApiFailure(200, 'Invalid destination list.');
  const controller = new AbortController(); const combined = AbortSignal.any([signal, controller.signal]);
  let next = 0; let denied: unknown;
  await Promise.all(Array.from({ length: Math.min(4, nodes.length) }, async () => {
    while (!combined.aborted && next < nodes.length) {
      const node = nodes[next++];
      const profile: FleetReferenceProfile = { registrationId: node.id, label: node.label, nodeId: node.nodeId, status: 'loading', channels: [], groups: [], tags: [] };
      try {
        const client = api.withRegisteredNode(node.id);
        const [channels, groups, tags] = await Promise.all([client.channels(combined), client.groups(combined), client.tags(combined)]);
        profile.channels = channels.filter(v => !v.channel.deleted).map(v => ({ id: v.channel.id, name: v.channel.name, enabled: v.channel.enabled, isDefault: v.channel.isDefault, conflicted: v.configConflict }));
        profile.groups = groups.filter(v => !v.group.deleted).map(v => ({ id: v.group.id, name: v.group.name, conflicted: v.configConflict }));
        profile.tags = tags.filter(v => !v.tag.deleted).map(v => ({ id: v.tag.id, name: v.tag.name, conflicted: v.configConflict }));
        profile.status = 'ready';
      } catch (error) {
        if (combined.aborted) return;
        if (error instanceof ApiFailure && (error.status === 401 || error.status === 403)) { denied = error; controller.abort(); return; }
        profile.status = 'unavailable';
      }
      if (!combined.aborted) progress(profile);
    }
  }));
  if (denied) throw denied;
}

export function useReferenceProfiles(api: CanaryApi, nodes: readonly RegisteredNode[], key: string, enabled: boolean) {
  const [profiles, setProfiles] = useState<{ key: string; rows: FleetReferenceProfile[] }>({ key, rows: [] });
  const [nonce, setNonce] = useState(0);
  const topology = JSON.stringify(nodes.map(n => [n.id, n.nodeId]).sort());
  const reload = useCallback(() => setNonce(n => n + 1), []);
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    setProfiles({ key, rows: nodes.map(n => ({ registrationId: n.id, label: n.label, nodeId: n.nodeId, status: 'loading', channels: [], groups: [], tags: [] })) });
    void readReferenceProfiles(api, nodes, controller.signal, profile => {
      if (!controller.signal.aborted) setProfiles(old => ({ key, rows: old.rows.map(p => p.registrationId === profile.registrationId ? profile : p) }));
    }).catch(() => { if (!controller.signal.aborted) setProfiles({ key, rows: [] }); });
    return () => controller.abort();
    // The key and topology describe the identity and selected registrations; regenerated arrays do not restart reads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, key, topology, enabled, nonce]);
  return { profiles: enabled && profiles.key === key ? profiles.rows : [], reload };
}
