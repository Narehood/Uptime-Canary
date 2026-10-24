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
  state: 'starting' | 'answering' | 'not-answering' | 'credential-refused' | 'different-node' | 'enrolling' | 'removing';
  lastReadAt?: number;
  monitorCount?: number;
  error?: string;
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
