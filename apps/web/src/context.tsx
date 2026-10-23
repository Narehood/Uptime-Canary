import { createContext, useContext } from 'react';
import type { AccountView, GroupView, MaintenanceView, MonitorView, NotificationChannelView, Overview, Role, TagView } from '@uptime-canary/shared';
import type { CanaryApi } from './api';
import type { Assessed, Assessment, NodeColumn } from './model';
import type { Connection } from './session';

export interface SwitchRequest {
  url?: string;
  reason?: string;
}

/** Catalogs load separately from the overview. `unsupported`: the node predates this API (404). */
export interface Catalog<T> {
  list: T[] | null;
  map: Map<string, T>;
  loading: boolean;
  error: unknown;
  unsupported: boolean;
  reload: () => void;
}
export type ChannelState = Catalog<NotificationChannelView>;
export type TagState = Catalog<TagView>;

export interface Canary {
  connection: Connection;
  api: CanaryApi;
  overview: Overview;
  /** The node this dashboard talks to. Every fact on screen is "as known to" this node. */
  endpoint: string;
  role: Role;
  /** This account as the endpoint reports it; `null` when the node predates `GET /api/auth/me`. */
  account: AccountView | null;
  editable: boolean;
  admin: boolean;
  nodes: NodeColumn[];
  nodeMap: Map<string, NodeColumn>;
  assessed: Assessed[];
  monitorMap: Map<string, MonitorView>;
  assessmentOf: (monitorId: string) => Assessment | undefined;
  /** `null` when the endpoint's API predates user-defined groups. */
  groups: GroupView[] | null;
  groupMap: Map<string, GroupView>;
  /** `null` when the endpoint's overview has no maintenance list. */
  maintenance: MaintenanceView[] | null;
  maintenanceMap: Map<string, MaintenanceView>;
  channels: ChannelState;
  tags: TagState;
  search: string;
  setSearch: (value: string) => void;
  refresh: () => void;
  openSwitch: (request?: SwitchRequest) => void;
  /**
   * False once this tab has signed in somewhere else. Async completions check it before publishing a result,
   * so a slow answer from one node can never be shown as if it came from another.
   */
  stillCurrent: () => boolean;
}

export const CanaryContext = createContext<Canary | null>(null);

export function useCanary(): Canary {
  const value = useContext(CanaryContext);
  if (!value) throw new Error('useCanary needs a connected session');
  return value;
}
