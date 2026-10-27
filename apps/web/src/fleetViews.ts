import type { RegisteredNode } from '@uptime-canary/shared';
import type { FleetMonitorRow, FleetSource } from './fleet';
import type { FleetDeployment } from '@uptime-canary/shared';
export type { FleetNodeGroup, FleetNodeGroupInput } from '@uptime-canary/shared';
import type { FleetNodeGroup, FleetNodeGroupInput } from '@uptime-canary/shared';

/** Dashboard node groups, distinct from a monitoring node's outage dependencies. */

/** Presentation contracts only. Root owns reads, permissions, writes and durable deployment state. */
export interface FleetOverviewProps {
  sources: readonly FleetSource[];
  rows: readonly FleetMonitorRow[];
  now: number;
  loading: boolean;
  refreshing: boolean;
  error?: string;
  search: string;
  onSearchChange: (value: string) => void;
  onRefresh: () => void;
  /** Navigate through a registration to its actual monitor, never to an inferred shared ID. */
  onOpen: (registrationId: string, monitorId: string) => void;
  onAdd?: () => void;
  onManageGroups?: () => void;
  onRetryDeployment?: (deployment: FleetDeployment) => void;
  onRemoveDeployment?: (deployment: FleetDeployment) => void;
  onEditDeployment?: (deployment: FleetDeployment) => void;
  onReconcileDeployment?: (deployment: FleetDeployment) => void;
}

/** A plain checklist of registered nodes: where one monitor runs, or the members of a node group. */
export interface FleetPlacementFieldsProps {
  nodes: readonly RegisteredNode[];
  selectedRegistrationIds: readonly string[];
  onChange: (registrationIds: string[]) => void;
  disabled?: boolean;
  error?: string;
  /** Allows reuse within the dashboard group editor without conflating registration and peer IDs. */
  legend?: string;
  /** Replaces the note on HA peers, which is about where one monitor runs. */
  note?: string;
}

export interface FleetNodeGroupsProps {
  onOpenDependencies?: () => void;
  nodes: readonly RegisteredNode[];
  groups: readonly FleetNodeGroup[];
  /** Monitors that follow each group, by group ID. Shown as a count only. */
  monitorCounts?: Readonly<Record<string, number>>;
  loading: boolean;
  editable: boolean;
  error?: string;
  onSave: (input: FleetNodeGroupInput, existing?: FleetNodeGroup) => Promise<void>;
  onRemove: (group: FleetNodeGroup) => Promise<void>;
  /** Starts a fresh collection for the group as it was shown; the dashboard compares its revision. */
  onSync?: (group: FleetNodeGroup) => Promise<void>;
  onRefresh: () => void;
}

/**
 * Notification channels and tags copied from one node to every other node with the monitor. The names come from the
 * source node's own lists, by ID; nothing is matched by name. Outage dependency groups are never copied.
 */
export interface FleetReferenceSync {
  sourceRegistrationId: string;
  sourceLabel: string;
  channelNames: Readonly<Record<string, string>>;
  tagNames: Readonly<Record<string, string>>;
  /** The form's outage dependency group, as the source node names it. */
  groupName?: string;
}
