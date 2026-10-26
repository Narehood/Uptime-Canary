/** Cached server-side checks of the fixed official public container packages. */
export interface SoftwareUpdate {
  component: 'dashboard' | 'node';
  registrationId?: string;
  label: string;
  image: string;
  currentCommit?: string;
  latestCommit?: string;
  currentBuiltAt?: number;
  latestBuiltAt?: number;
  status: 'update-available' | 'current' | 'ahead' | 'unknown' | 'unavailable';
}

export interface UpdateStatus {
  enabled: boolean;
  intervalHours: 6;
  state: 'not-checked' | 'checking' | 'ok' | 'unavailable' | 'disabled';
  checkedAt?: number;
  nextCheckAt?: number;
  images: SoftwareUpdate[];
}
