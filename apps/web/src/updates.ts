import type { SoftwareUpdate, UpdateStatus } from '@uptime-canary/shared';
import { ApiFailure, checkRegistration, describeFailure, isAbort } from './api';
import { plural } from './format';
import { validProjectionTime } from './publicAvailability';

/*
 * Software update notices on the control plane. The dashboard's server compares the fixed official images on its own
 * schedule and stores the result. The browser only reads that stored result from its own dashboard
 * (`GET /api/dashboard/updates`, every role) or asks it to check sooner (`POST /api/dashboard/updates/check`,
 * administrators). Nothing here contacts a registry or GitHub, pulls, installs, restarts or changes an image tag.
 *
 * Every value in an answer is checked before it's shown. Links are fixed; a commit becomes part of a link only after it
 * reads as a full hexadecimal commit. A comparison the answer can't back up (no running or no latest commit, or an image
 * that isn't the official one) is shown as unknown or unavailable, never as current or as an update.
 *
 * When both commits name the same source, only the build times the official publisher stamps into its images
 * (`currentBuiltAt`, `latestBuiltAt`; docs/software-updates.md) can tell a newer build of that source
 * apart. Without both, the same commit is unknown. The exact image digest a component runs is never known here.
 */

export type UpdateState = UpdateStatus['state'];
export type ImageStatus = SoftwareUpdate['status'];
export type UpdateComponent = SoftwareUpdate['component'];

/** The public repository the official images are built and published from. Every link here is fixed. */
const REPOSITORY = 'https://github.com/Narehood/Uptime-Canary';
export const UPDATE_LINKS = {
  upgrades: `${REPOSITORY}/blob/main/docs/upgrades.md`,
  packages: `${REPOSITORY}/packages`,
  workflow: `${REPOSITORY}/actions/workflows/container-images.yml`,
} as const;

/** The official image each component is compared with. Shown from this list, never from the answer's text. */
export const OFFICIAL_IMAGES: Readonly<Record<UpdateComponent, string>> = {
  dashboard: 'ghcr.io/narehood/uptime-canary-web',
  node: 'ghcr.io/narehood/uptime-canary-node',
};

/** Rows read from one answer; more than any dashboard registry holds. */
export const MAX_UPDATE_ROWS = 256;

/** The stored result is read at most once a minute, and while nothing is running every five minutes or less often. */
export const UPDATE_MIN_GAP_MS = 60_000;
export const UPDATE_IDLE_MS = 300_000;

/**
 * How much later than the check that read it the latest image's build time may be. The publisher's clock and the
 * dashboard's can disagree a little, never by days; a later time can't belong to the image that check read.
 */
export const BUILD_CLOCK_SLACK_MS = 86_400_000;

export interface UpdateRow {
  /** Unique within one answer: `dashboard`, `node:<registration>`, or `node#<position>` without a registration. */
  key: string;
  component: UpdateComponent;
  registrationId?: string;
  label: string;
  /** The official repository it is compared with; absent when the answer named no official image. */
  image?: string;
  /** Lowercase hexadecimal source commits, 7 to 64 digits. */
  currentCommit?: string;
  latestCommit?: string;
  currentVersion?: string;
  latestVersion?: string;
  /**
   * When the official publisher built each image, epoch ms, kept only with its commit. The latest one is also dropped
   * when it's more than `BUILD_CLOCK_SLACK_MS` after the check that read it, or the answer has no check time.
   */
  currentBuiltAt?: number;
  latestBuiltAt?: number;
  status: ImageStatus;
}

export interface UpdateReport {
  /** False also when the state is `disabled`; then nothing is compared. */
  enabled: boolean;
  /** Hours between the server's own checks, or null when the answer gave no usable number. */
  intervalHours: number | null;
  state: UpdateState;
  /** The dashboard server's clock, epoch ms. */
  checkedAt?: number;
  nextCheckAt?: number;
  /** The dashboard first, then nodes in the server's order. */
  rows: UpdateRow[];
}

const STATES: readonly string[] = ['not-checked', 'checking', 'ok', 'unavailable', 'disabled'] satisfies UpdateState[];
const STATUSES: readonly string[] = ['update-available', 'current', 'ahead', 'unknown', 'unavailable'] satisfies ImageStatus[];
const COMMIT = /^[0-9a-f]{7,64}$/iu;
const FULL_COMMIT = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/u;

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
/** The same cleaning the registry's labels get: control characters become spaces, at most `limit` characters. */
const cleanText = (text: string, limit: number): string => text.replace(/[\x00-\x1f\x7f]+/gu, ' ').trim().slice(0, limit);
const commitOf = (value: unknown): string | undefined => typeof value === 'string' && COMMIT.test(value) ? value.toLowerCase() : undefined;
const sameCommit = (a: string, b: string): boolean => a.startsWith(b) || b.startsWith(a);

function registrationOf(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined;
  try { return checkRegistration(value); } catch { return undefined; }
}

/** The official repository when the answer names it (alone, or with a tag or digest the page doesn't show). */
function officialImage(component: UpdateComponent, value: unknown): string | undefined {
  const official = OFFICIAL_IMAGES[component];
  return typeof value === 'string' && (value === official || value.startsWith(`${official}:`) || value.startsWith(`${official}@`)) ? official : undefined;
}

/** Both commits are known and name the same source (a short form may prefix the full one). */
export function sameSource(row: UpdateRow): boolean {
  return !!row.currentCommit && !!row.latestCommit && sameCommit(row.currentCommit, row.latestCommit);
}

/**
 * What a row may claim. The server decides the outcome; the page only refuses claims the row itself can't support:
 * without an official image or a running commit nothing was compared (unknown); without the latest commit the latest
 * image wasn't read (unavailable). Different commits can be an update or newer, never current; the server's history
 * comparison decides, and build times don't overrule it. The same commit needs both build times, and the claim must
 * match them: a later latest build is an update, an equal one current, an earlier one newer.
 */
function supported(status: ImageStatus, row: UpdateRow): ImageStatus {
  if (status !== 'update-available' && status !== 'current' && status !== 'ahead') return status;
  if (!row.image || !row.currentCommit) return 'unknown';
  if (!row.latestCommit) return 'unavailable';
  if (!sameSource(row)) return status === 'current' ? 'unknown' : status;
  if (row.currentBuiltAt === undefined || row.latestBuiltAt === undefined) return 'unknown';
  const shown: ImageStatus = row.latestBuiltAt > row.currentBuiltAt ? 'update-available' : row.latestBuiltAt < row.currentBuiltAt ? 'ahead' : 'current';
  return status === shown ? status : 'unknown';
}

function readRow(value: unknown, index: number, enabled: boolean, checkedAt: number | undefined): UpdateRow | null {
  if (!isRecord(value) || (value.component !== 'dashboard' && value.component !== 'node')) return null;
  const component: UpdateComponent = value.component === 'dashboard' ? 'dashboard' : 'node';
  const registrationId = registrationOf(value.registrationId);
  const image = officialImage(component, value.image);
  const currentCommit = commitOf(value.currentCommit);
  const latestCommit = commitOf(value.latestCommit);
  const currentBuiltAt = currentCommit && validProjectionTime(value.currentBuiltAt) ? value.currentBuiltAt : undefined;
  const latestBuiltAt = latestCommit && checkedAt !== undefined && validProjectionTime(value.latestBuiltAt) && value.latestBuiltAt <= checkedAt + BUILD_CLOCK_SLACK_MS
    ? value.latestBuiltAt : undefined;
  const status = typeof value.status === 'string' && STATUSES.includes(value.status) ? value.status as ImageStatus : 'unknown';
  const row: UpdateRow = {
    key: component === 'dashboard' ? 'dashboard' : registrationId ? `node:${registrationId}` : `node#${index}`,
    component,
    label: (typeof value.label === 'string' ? cleanText(value.label, 128) : '') || (component === 'dashboard' ? 'Dashboard' : 'Unnamed node'),
    status: 'unknown',
  };
  if (registrationId) row.registrationId = registrationId;
  if (image) row.image = image;
  if (currentCommit) row.currentCommit = currentCommit;
  if (latestCommit) row.latestCommit = latestCommit;
  if (typeof value.currentVersion === 'string' && value.currentVersion.trim()) row.currentVersion = cleanText(value.currentVersion, 128);
  if (typeof value.latestVersion === 'string' && value.latestVersion.trim()) row.latestVersion = cleanText(value.latestVersion, 128);
  if (currentBuiltAt !== undefined) row.currentBuiltAt = currentBuiltAt;
  if (latestBuiltAt !== undefined) row.latestBuiltAt = latestBuiltAt;
  // With checks off nothing is compared, whatever an older stored row said.
  row.status = enabled ? supported(status, row) : 'unknown';
  return row;
}

/**
 * The update status as the dashboard's server stored it, checked field by field, or null when the answer isn't one
 * (not an object, no `enabled`, a state this page doesn't know, or `images` that aren't a list). Unknown row statuses
 * read as unknown; rows without a known component, and repeats of the same component, are left out.
 */
export function readUpdateStatus(value: unknown): UpdateReport | null {
  if (!isRecord(value) || typeof value.enabled !== 'boolean' || typeof value.state !== 'string' || !STATES.includes(value.state)) return null;
  if (value.images !== undefined && value.images !== null && !Array.isArray(value.images)) return null;
  const enabled = value.enabled && value.state !== 'disabled';
  const hours = value.intervalHours;
  const report: UpdateReport = {
    enabled,
    intervalHours: typeof hours === 'number' && Number.isInteger(hours) && hours >= 1 && hours <= 168 ? hours : null,
    state: enabled ? value.state as UpdateState : 'disabled',
    rows: [],
  };
  if (validProjectionTime(value.checkedAt)) report.checkedAt = value.checkedAt;
  if (validProjectionTime(value.nextCheckAt)) report.nextCheckAt = value.nextCheckAt;
  const seen = new Set<string>();
  const images: unknown[] = Array.isArray(value.images) ? value.images.slice(0, MAX_UPDATE_ROWS) : [];
  images.forEach((item, index) => {
    const row = readRow(item, index, enabled, report.checkedAt);
    if (!row || seen.has(row.key)) return;
    seen.add(row.key);
    report.rows.push(row);
  });
  // Stable: nodes keep the server's order.
  report.rows.sort((a, b) => Number(a.component !== 'dashboard') - Number(b.component !== 'dashboard'));
  return report;
}

export interface UpdateCounts { available: number; current: number; ahead: number; unknown: number; unavailable: number; total: number }

export function updateCounts(report: UpdateReport | null): UpdateCounts {
  const counts: UpdateCounts = { available: 0, current: 0, ahead: 0, unknown: 0, unavailable: 0, total: 0 };
  for (const row of report?.rows ?? []) {
    counts.total++;
    if (row.status === 'update-available') counts.available++;
    else counts[row.status]++;
  }
  return counts;
}

export interface UpdateNotice {
  count: number;
  title: string;
  text: string;
  /**
   * Changes when another component, a newer image or a newer build of the same source becomes available, so a
   * dismissed notice can return.
   */
  signature: string;
}

/** The shell's discreet notice: only when something can be updated. Everything current, unknown or off stays quiet. */
export function updateNotice(report: UpdateReport | null): UpdateNotice | null {
  if (!report?.enabled) return null;
  const rows = report.rows.filter(row => row.status === 'update-available');
  const only = rows.length === 1 ? rows[0] : undefined;
  if (!rows.length) return null;
  // Newer builds of the same source change no code, so there's nothing to compare in the details.
  const rebuilt = rows.every(sameSource);
  return {
    count: rows.length,
    title: only ? `An update is available for ${only.component === 'dashboard' ? 'the dashboard' : only.label}` : `Updates are available for ${rows.length} components`,
    text: `${only ? 'A newer official image was' : 'Newer official images were'} ${rebuilt ? 'built from the same source code' : 'published'}. Nothing is pulled or installed automatically; the details say ${rebuilt ? 'how' : 'what changed and how'} to update.`,
    signature: rows.map(row => `${row.key}@${row.latestCommit ?? ''}@${row.latestBuiltAt ?? ''}`).sort().join(' '),
  };
}

/** In memory only: the notice set aside with "Not now" for one sign-in, until its signature changes. */
let dismissedNotice: string | null = null;

export function dismissNotice(feedKey: string, notice: UpdateNotice): void {
  dismissedNotice = `${feedKey} ${notice.signature}`;
}

export function noticeDismissed(feedKey: string, notice: UpdateNotice): boolean {
  return dismissedNotice === `${feedKey} ${notice.signature}`;
}

export type RowKind = 'available' | 'current' | 'ahead' | 'unknown' | 'unavailable' | 'off';

/** A row's chip and explanation. Only `available` is ever offered as something to install. */
export function rowStatus(row: UpdateRow, report: UpdateReport): { kind: RowKind; label: string; text: string } {
  if (!report.enabled) return { kind: 'off', label: 'Not compared', text: 'Update checks are off for this deployment.' };
  const same = sameSource(row);
  switch (row.status) {
    case 'update-available':
      return {
        kind: 'available',
        label: 'Update available',
        text: same ? 'The latest official image is a newer build of the same source, so the code is unchanged. This check can’t see what else differs inside the image, such as its base image.'
          : 'A newer official image was published from later source.',
      };
    case 'current':
      return { kind: 'current', label: 'Current', text: 'Reports the same source and build time as the latest official image.' };
    case 'ahead':
      return {
        kind: 'ahead',
        label: 'Newer than latest',
        text: same ? 'Reports a later build of the same source than the latest official image the last check read, perhaps one published since. There’s nothing to install, and an older image isn’t suggested.'
          : 'Runs source the latest official image doesn’t include, such as a development or custom build. There’s nothing to install, and an older image isn’t suggested.',
      };
    case 'unavailable':
      return { kind: 'unavailable', label: 'Couldn’t check', text: 'The latest official image couldn’t be read at the last check.' };
    case 'unknown':
      return {
        kind: 'unknown',
        label: 'Unknown',
        text: !row.image ? 'No official image is named for it, so it isn’t compared.'
          : !row.currentCommit ? 'It doesn’t report the source it was built from (an older image or a build from source), so it can’t be compared.'
          : same && row.currentBuiltAt === undefined ? 'It reports the same source as the latest official image but no usable build time (older images and custom builds don’t have one), so a newer build of that source can’t be told apart.'
          : same && row.latestBuiltAt === undefined ? 'It reports the same source as the latest official image, but no usable build time was read for that image, so a newer build of that source can’t be told apart.'
          : 'This result can’t be compared with the latest official image.',
      };
  }
}

/** First 12 digits of a commit that already read as hexadecimal. */
export const shortCommit = (commit: string): string => commit.slice(0, 12);

/**
 * "What changed" for an available update: a fixed repository link, and only for two different full hexadecimal
 * commits. A newer build of the same source changes no code, so it has nothing to compare.
 */
export function compareHref(row: UpdateRow): string | null {
  if (row.status !== 'update-available' || !row.currentCommit || !row.latestCommit || sameSource(row)) return null;
  if (!FULL_COMMIT.test(row.currentCommit) || !FULL_COMMIT.test(row.latestCommit)) return null;
  return `${REPOSITORY}/compare/${row.currentCommit}...${row.latestCommit}`;
}

/** One feed per dashboard sign-in. Node selections remount the shell but keep it; another sign-in gets its own. */
export function updateFeedKey(connection: { api: { baseUrl: string; credential: string; expiresAt: number | null }; account: { id: string } | null }): string {
  return `${connection.api.baseUrl}|${connection.api.credential}|${connection.api.expiresAt ?? ''}|${connection.account?.id ?? ''}`;
}

export interface UpdateSnapshot {
  /** The last readable answer, kept when a later read fails. */
  report: UpdateReport | null;
  /** Browser time that answer was read. */
  readAt: number | null;
  /** The latest read failure, newer than `report`. */
  failure: unknown;
  /** The server has no update route (404): nothing more is asked and nothing is shown. */
  unsupported: boolean;
  /** An administrator's check is being sent. */
  sending: boolean;
  /** Why the last manual check was refused or failed; cleared by the next attempt. */
  checkFailure: unknown;
}

export const NO_UPDATES: UpdateSnapshot = Object.freeze({ report: null, readAt: null, failure: null, unsupported: false, sending: false, checkFailure: null });

const UNREADABLE = 'This dashboard’s server sent an update status this page couldn’t read.';

/** The headline of the details. */
export function updateSummary(snapshot: UpdateSnapshot): { title: string; text: string } {
  const { report, failure } = snapshot;
  if (snapshot.unsupported) return { title: 'Updates aren’t reported here', text: 'This dashboard’s server doesn’t report software updates.' };
  if (!report) return failure ?{ title: 'Couldn’t read the update status', text: describeFailure(failure) } : { title: 'Reading the update status…', text: '' };
  const counts = updateCounts(report);
  switch (report.state) {
    case 'disabled':
      return { title: 'Update checks are off', text: 'This deployment turned off update checks, so the dashboard’s server doesn’t read the official images. Turning them on is a deployment setting.' };
    case 'not-checked':
      return { title: 'Not checked yet', text: 'The dashboard’s server runs its first check on its own schedule.' };
    case 'checking':
      return { title: 'Checking now', text: 'The dashboard’s server is reading the latest official images. This page asks for the result again in about a minute.' };
    case 'unavailable':
      return { title: 'The last check couldn’t finish', text: 'The dashboard’s server couldn’t read the official images. It tries again at its next scheduled check.' };
    case 'ok':
      if (counts.available) return { title: `${plural(counts.available, 'update')} available`, text: 'Update the components marked below together with the rest of your setup.' };
      if (counts.unknown + counts.unavailable) return { title: 'No update found', text: `${plural(counts.unknown + counts.unavailable, 'component')} couldn’t be compared; see below.` };
      if (!counts.total) return { title: 'Nothing to compare', text: 'The dashboard’s server listed no components.' };
      return { title: 'Up to date', text: 'Every component reports the source and build of the latest official image, or newer.' };
  }
}

/** Why an administrator's check didn't start. Shown once; the page never sends it again on its own. */
export function checkProblem(error: unknown): string {
  if (error instanceof ApiFailure) {
    if (error.status === 409 && /disabled/iu.test(error.detail ?? '')) return 'Update checks are off in this dashboard’s deployment settings.';
    if (error.status === 409) return 'A check is already running. Its result appears here when it finishes.';
    if (error.status === 429) return 'A check ran recently. Manual checks are limited to one every 15 minutes; scheduled checks still run.';
    if (error.status === 403) return 'Only administrators of this dashboard can start a check.';
    if (error.status === 404) return 'This dashboard’s server doesn’t offer update checks.';
  }
  return describeFailure(error);
}

/**
 * When the next read is due after an answer: in a minute while a check runs, a minute after the next scheduled check
 * when that's sooner, and otherwise in five minutes. A next check already past doesn't speed reads up.
 */
export function nextReadDelay(report: UpdateReport | null, now: number): number {
  if (report?.state === 'checking') return UPDATE_MIN_GAP_MS;
  if (report?.enabled && report.nextCheckAt !== undefined && report.nextCheckAt > now) {
    return Math.min(UPDATE_IDLE_MS, report.nextCheckAt - now + UPDATE_MIN_GAP_MS);
  }
  return UPDATE_IDLE_MS;
}

/** The dashboard's two update routes, as the page's API client sends them. */
export interface UpdateSource {
  read: (signal: AbortSignal) => Promise<unknown>;
  check: (signal: AbortSignal) => Promise<unknown>;
  /** True while nothing should be asked: a rejected sign-in, or a hidden page. */
  paused: () => boolean;
}

export interface UpdateTimers {
  now: () => number;
  set: (run: () => void, ms: number) => unknown;
  clear: (handle: unknown) => void;
}

const browserTimers: UpdateTimers = {
  now: () => Date.now(),
  set: (run, ms) => setTimeout(run, ms),
  clear: handle => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

/**
 * The stored update status for one sign-in, shared by everything that shows it. Reads never overlap and are at least
 * `UPDATE_MIN_GAP_MS` apart, however often the shell renders or remounts. Watchers that leave and return in the same
 * turn (a node selection remounts the shell) keep the feed; when the last one leaves for good, the pending timer and
 * any request in flight are cancelled.
 */
export class UpdateFeed {
  private state: UpdateSnapshot = NO_UPDATES;
  private readonly listeners = new Set<() => void>();
  private source: UpdateSource | null = null;
  private watchers = 0;
  private timer: unknown = null;
  private stopTimer: unknown = null;
  private reading: AbortController | null = null;
  private sending: AbortController | null = null;
  private askedAt: number | null = null;
  private missed = false;
  /** Counts accepted manual checks, so a read sent before one can't replace its newer answer. */
  private checks = 0;

  constructor(private readonly timers: UpdateTimers = browserTimers, private readonly onStop?: () => void) {}

  readonly listen = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  readonly current = (): UpdateSnapshot => this.state;

  /** Starts reading (now, or once the minimum gap since the last read has passed) and returns the release. */
  watch(source: UpdateSource): () => void {
    this.source = source;
    this.watchers++;
    if (this.stopTimer !== null) { this.timers.clear(this.stopTimer); this.stopTimer = null; }
    if (!this.reading && this.timer === null && !this.state.unsupported) this.schedule(0);
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.watchers--;
      if (this.watchers === 0 && this.stopTimer === null) this.stopTimer = this.timers.set(() => this.stop(), 0);
    };
  }

  /** The page is visible again: a read skipped while it was hidden happens now, still after the minimum gap. */
  wake(): void {
    if (!this.missed || !this.watchers || this.reading || this.state.unsupported) return;
    this.schedule(0);
  }

  /** An administrator's explicit check. Never repeated on its own; a refusal stays shown until the next attempt. */
  async checkNow(): Promise<void> {
    const source = this.source;
    if (!source || !this.watchers || this.sending || this.state.unsupported) return;
    const controller = new AbortController();
    this.sending = controller;
    this.patch({ sending: true, checkFailure: null });
    try {
      const report = readUpdateStatus(await source.check(controller.signal));
      if (controller.signal.aborted) return;
      this.checks++;
      this.patch(report ? { sending: false, report, readAt: this.timers.now(), failure: null } : { sending: false });
    } catch (error) {
      if (controller.signal.aborted || isAbort(error)) return;
      this.patch({ sending: false, checkFailure: error });
    } finally {
      if (this.sending === controller) this.sending = null;
    }
    // Follow the check with one read after the minimum gap, never sooner.
    if (this.watchers && !this.reading) this.schedule(UPDATE_MIN_GAP_MS);
  }

  private patch(change: Partial<UpdateSnapshot>): void {
    this.state = { ...this.state, ...change };
    for (const listener of [...this.listeners]) listener();
  }

  private schedule(delay: number): void {
    if (this.timer !== null) this.timers.clear(this.timer);
    const gap = this.askedAt === null ? 0 : this.askedAt + UPDATE_MIN_GAP_MS - this.timers.now();
    this.timer = this.timers.set(() => { this.timer = null; void this.tick(); }, Math.max(delay, gap, 0));
  }

  private async tick(): Promise<void> {
    const source = this.source;
    if (!this.watchers || !source || this.reading || this.state.unsupported) return;
    if (source.paused()) {
      this.missed = true;
      this.schedule(UPDATE_IDLE_MS);
      return;
    }
    this.missed = false;
    const controller = new AbortController();
    const checks = this.checks;
    this.reading = controller;
    this.askedAt = this.timers.now();
    try {
      const report = readUpdateStatus(await source.read(controller.signal));
      if (controller.signal.aborted) return;
      // A manual check answered while this read was on its way is newer, so it stays.
      if (checks === this.checks) this.patch(report ? { report, readAt: this.timers.now(), failure: null } : { failure: new ApiFailure(200, UNREADABLE) });
    } catch (error) {
      if (controller.signal.aborted || isAbort(error)) return;
      if (error instanceof ApiFailure && error.status === 404) {
        this.patch({ unsupported: true, failure: null });
        return;
      }
      this.patch({ failure: error });
    } finally {
      if (this.reading === controller) this.reading = null;
    }
    if (this.watchers && !this.state.unsupported) this.schedule(nextReadDelay(this.state.report, this.timers.now()));
  }

  private stop(): void {
    this.stopTimer = null;
    if (this.watchers) return;
    if (this.timer !== null) { this.timers.clear(this.timer); this.timer = null; }
    this.reading?.abort();
    this.reading = null;
    this.sending?.abort();
    this.sending = null;
    this.source = null;
    if (this.state.sending) this.patch({ sending: false });
    this.onStop?.();
  }
}

const feeds = new Map<string, UpdateFeed>();

/** The feed for one sign-in; it leaves this list when its last watcher leaves. */
export function updateFeed(key: string): UpdateFeed {
  const existing = feeds.get(key);
  if (existing) return existing;
  const feed: UpdateFeed = new UpdateFeed(browserTimers, () => { if (feeds.get(key) === feed) feeds.delete(key); });
  feeds.set(key, feed);
  return feed;
}
