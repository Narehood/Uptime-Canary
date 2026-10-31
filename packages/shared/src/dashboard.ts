import type { Monitor, MonitorInput, MonitorTag, Role } from './index.js';

export interface FleetTargetReferences { notificationIds?: string[]; groupId?: string; tags?: MonitorTag[] }

export interface FleetNodeGroup { id: string; name: string; registrationIds: string[]; revision: string; updatedAt: number; syncState?: 'pending' | 'applied' | 'blocked'; syncReason?: string }
export interface FleetNodeGroupInput { name: string; registrationIds: string[] }
export interface FleetSourceReference { registrationId: string; monitorId: string; revision: string }
export interface FleetPlacement {
  id: string; registrationId: string; registrationIds: string[]; nodeIds: string[];
  retiringRegistrationIds?: string[];
  monitorId: string; sequence: number; revision: string; state: 'pending' | 'syncing' | 'applied' | 'removed';
}
export interface FleetDeployment {
  pinnedRegistrationIds?: string[];
  groupIds?: string[];
  syncNotifications?: boolean;
  notificationSourceRegistrationId?: string;
  references?: Record<string, FleetTargetReferences>;
  canCancel?: boolean;
  canRemove?: boolean;
  id: string; revision: string; updatedAt: number; registrationIds: string[]; source?: FleetSourceReference;
  placements: FleetPlacement[]; state: 'pending' | 'applied' | 'blocked' | 'unauthorized' | 'removed'; reason?: string;
  monitor: Monitor;
}
export interface FleetDeploymentInput {
  groupIds?: string[];
  syncNotifications?: boolean;
  notificationSourceRegistrationId?: string;
  references?: Record<string, FleetTargetReferences>;
  /** Kept in form memory across retries of a create whose answer was lost. */
  requestId?: string; revision?: string; registrationIds: string[]; monitor: MonitorInput; source?: FleetSourceReference;
}

/** Dashboard-owned registry, shared by every dashboard account. Never contains a node credential. */
export interface RegisteredNode {
  id: string;
  label: string;
  nodeId: string;
  nodeName: string;
  /** Only administrators receive the server-reachable address. */
  url?: string;
  local: boolean;
  state: 'starting' | 'answering' | 'not-answering' | 'credential-refused' | 'different-node' | 'enrolling' | 'removing' | 'rotating';
  lastReadAt?: number;
  monitorCount?: number;
  error?: string;
  /** Omitted for legacy/direct records. Outbound nodes initiate the connection. */
  connection?: 'direct' | 'outbound';
  /** Transport activity, not an observation or proof that monitoring is healthy. */
  lastConnectedAt?: number;
  /** Administrator-only certificate trust anchor for a direct HTTPS node. */
  tlsFingerprint?: string;
  /** Last observed running software, not the requested image tag. */
  version?: string;
  sourceCommit?: string;
}

export interface NodeVerification {
  ticket: string;
  expiresAt: number;
  node: { id: string; name: string };
  label: string;
  url: string;
  plainHttp: boolean;
  tlsFingerprint?: string;
}

export interface DashboardOwnerInput { name: string; password: string; code: string }
export interface NodeVerificationInput { label: string; url: string; token: string; tlsFingerprint?: string }
export const DASHBOARD_ROLES: readonly Role[] = ['admin', 'editor', 'viewer'];

/** Admin-only wizard configuration; the browser never contacts connectionUrl. */
export interface NodeOnboardingCapabilities {
  outboundEnabled: boolean;
  connectionUrl?: string;
  /** Present only when the dedicated listener itself terminates native TLS. */
  connectionTlsFingerprint?: string;
  connectionProtocol: 1;
  imageTag: string;
  containerPlatforms: string[];
  nativeDownloadAvailable: boolean;
}

export interface NodeInvitationInput { label: string; nodeId: string; connectionUrl?: string }

/** Admin-only invitation status. The one-time secret is not returned here. */
export interface NodeInvitation {
  id: string;
  label: string;
  nodeId: string;
  connectionUrl: string;
  connectionTlsFingerprint?: string;
  expiresAt: number;
  state: 'waiting' | 'connected' | 'confirmed' | 'expired';
  registrationId?: string;
  /** Reported build/identity bound to the invitation's expected node ID. */
  node?: { id: string; name: string; version: string; sourceCommit?: string; fingerprint: string };
}

/** Returned only at creation; keep the invitation token in memory or a private download. */
export interface NodeInvitationCreated extends NodeInvitation { token: string }
