/** Operator-authored publication data. No private monitor name is inherited. */
export interface StatusPageMonitor {
  monitorId: string;
  name: string;
  showCertificate: boolean;
  /** Both this flag and each monitor assignment's public flag must be enabled. */
  showTags?: boolean;
}

export interface StatusPageGroup {
  id: string;
  name: string;
  monitors: StatusPageMonitor[];
}

export type StatusPageAnnouncementState = "investigating" | "identified" | "monitoring" | "resolved";

export interface StatusPageAnnouncement {
  id: string;
  title: string;
  message: string;
  state: StatusPageAnnouncementState;
  createdAt: number;
  updatedAt: number;
  pinned: boolean;
}

export interface StatusPageInput {
  slug: string;
  title: string;
  description: string;
  enabled: boolean;
  groups: StatusPageGroup[];
  announcements: StatusPageAnnouncement[];
}

export interface StatusPage extends StatusPageInput {
  id: string;
  revision: string;
  updatedAt: number;
  deleted?: boolean;
}

export interface StatusPageView {
  statusPage: StatusPage;
  configConflict: boolean;
}

export type PublicStatusState = "up" | "down" | "pending" | "unknown";

export interface PublicStatusCertificate {
  /** ISO 8601 validity dates, only when opted in and all fresh peers agree. */
  notBefore: string;
  notAfter: string;
}

export interface PublicStatusMonitor {
  name: string;
  state: PublicStatusState;
  maintenance: boolean;
  certificate?: PublicStatusCertificate;
  tags?: { name: string; color: string; value?: string }[];
}

export interface PublicStatusGroup {
  /** Operator-authored public group ID; not a private monitor group ID. */
  id: string;
  name: string;
  state: PublicStatusState;
  maintenance: boolean;
  monitors: PublicStatusMonitor[];
}

export interface PublicStatusPage {
  slug: string;
  title: string;
  description: string;
  state: PublicStatusState;
  maintenance: boolean;
  /** Time of this projection in Unix milliseconds; not a measurement time. */
  generatedAt: number;
  groups: PublicStatusGroup[];
  announcements: StatusPageAnnouncement[];
}
