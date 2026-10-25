import type { Role } from './index.js';

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
}

export interface NodeVerification {
  ticket: string;
  expiresAt: number;
  node: { id: string; name: string };
  label: string;
  url: string;
  plainHttp: boolean;
}

export interface DashboardOwnerInput { name: string; password: string; code: string }
export interface NodeVerificationInput { label: string; url: string; token: string }
export const DASHBOARD_ROLES: readonly Role[] = ['admin', 'editor', 'viewer'];

/** Admin-only wizard configuration; the browser never contacts connectionUrl. */
export interface NodeOnboardingCapabilities {
  outboundEnabled: boolean;
  connectionUrl?: string;
  connectionProtocol: 1;
  imageTag: string;
  containerPlatforms: string[];
  nativeDownloadAvailable: boolean;
}

export interface NodeInvitationInput { label: string; nodeId: string }

/** Admin-only invitation status. The one-time secret is not returned here. */
export interface NodeInvitation {
  id: string;
  label: string;
  nodeId: string;
  connectionUrl: string;
  expiresAt: number;
  state: 'waiting' | 'connected' | 'confirmed' | 'expired';
  registrationId?: string;
  /** Reported build/identity bound to the invitation's expected node ID. */
  node?: { id: string; name: string; version: string; sourceCommit?: string; fingerprint: string };
}

/** Returned only at creation; keep the invitation token in memory or a private download. */
export interface NodeInvitationCreated extends NodeInvitation { token: string }
