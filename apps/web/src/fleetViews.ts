import type { RegisteredNode } from '@uptime-canary/shared';
import type { FleetMonitorRow, FleetSource } from './fleet';
import type { FleetDeployment } from '@uptime-canary/shared';
export type { FleetNodeGroup, FleetNodeGroupInput } from '@uptime-canary/shared';
import type { FleetNodeGroup, FleetNodeGroupInput } from '@uptime-canary/shared';

/** Dashboard placement groups, distinct from a monitoring node's outage dependencies. */

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
}

export interface FleetPlacementFieldsProps {
  nodes: readonly RegisteredNode[];
  groups: readonly FleetNodeGroup[];
  selectedRegistrationIds: readonly string[];
  onChange: (registrationIds: string[]) => void;
  disabled?: boolean;
  error?: string;
  /** Allows reuse within the dashboard group editor without conflating registration and peer IDs. */
  legend?: string;
}

export interface FleetNodeGroupsProps {
  onOpenDependencies?: () => void;
  nodes: readonly RegisteredNode[];
  groups: readonly FleetNodeGroup[];
  loading: boolean;
  editable: boolean;
  error?: string;
  onSave: (input: FleetNodeGroupInput, existing?: FleetNodeGroup) => Promise<void>;
  onRemove: (group: FleetNodeGroup) => Promise<void>;
  onRefresh: () => void;
}
