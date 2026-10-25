import { useEffect, useRef } from 'react';
import type { BrowserScreenshot, MonitorView } from '@uptime-canary/shared';
import { describeFailure } from '../api';
import { useCanary } from '../context';
import { useResource } from '../hooks';
import { observationFor } from '../model';
import { SCREENSHOT_HEIGHT, SCREENSHOT_WIDTH, screenshotSrc } from '../screenshot';
import { Card, EmptyState, Tag, Time } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';

/** The check generation a capture must belong to: the monitor's own, or its revision on nodes that don't send one. */
export const screenshotGeneration = (view: MonitorView): string => view.monitor.checkGeneration || view.monitor.revision;

/**
 * One capture as the node described it. It is shown only when it belongs to the monitor, the node and the check generation
 * on screen; otherwise the figure says why instead of showing an image. "Older capture" means the node took it before its
 * latest fresh check, so it never stands in for the page's current state, and neither label is proof of health.
 */
export function ScreenshotFigure({ shot, view, endpoint, now }: { shot: BrowserScreenshot; view: MonitorView; endpoint: string; now: number }) {
  const m = view.monitor;
  if (shot.monitorId !== m.id || shot.nodeId !== endpoint) {
    return <p className="muted small"><Glyph tone="unknown" /> This capture came from another monitor or node than the one shown, so it isn’t displayed.</p>;
  }
  if (shot.checkGeneration !== screenshotGeneration(view)) {
    return <p className="muted small"><Glyph tone="unknown" /> This capture belongs to other settings of this monitor than the ones shown, so it isn’t displayed. It updates when this page catches up.</p>;
  }
  return (
    <figure className="shot">
      <div className="shot-h">
        {shot.current ? <Tag icon="check" tone="accent">Latest capture</Tag> : <Tag icon="clock" tone="hold">Older capture</Tag>}
        <span className="muted small">Taken by <span className="mono">{shot.nodeId}</span> <Time at={shot.checkedAt} now={now} /></span>
      </div>
      <img className="shot-img" src={screenshotSrc(shot)} width={SCREENSHOT_WIDTH} height={SCREENSHOT_HEIGHT} alt={`Screenshot of ${m.target}, taken by ${shot.nodeId}`} decoding="async" />
      <figcaption className="foot-note">
        {shot.current
          ? <>From <span className="mono">{shot.nodeId}</span>’s latest fresh check of this monitor.</>
          : <>Not from <span className="mono">{shot.nodeId}</span>’s latest fresh check: a later check took no capture, or there’s no fresh check now (for example while paused). Don’t read it as the page’s current state.</>}
        {' '}A screenshot shows how the page rendered at that moment; it isn’t proof that the monitor is healthy.
      </figcaption>
    </figure>
  );
}

/**
 * Editors and admins: the newest private screenshot the connected node took for a real-browser monitor with screenshots
 * on. Viewers get nothing and no request is made for them. The answer is keyed by the connection, so a new sign-in or
 * node selection drops one still on its way; a new check from this node asks again while the shown capture stays.
 */
export function BrowserScreenshotCard({ view }: { view: MonitorView }) {
  const { api, connection, endpoint, overview, editable } = useCanary();
  const m = view.monitor;
  const generation = screenshotGeneration(view);
  const wanted = editable && m.type === 'real-browser' && m.browser?.screenshot === true;
  const runsHere = m.nodeIds.includes(endpoint);
  const asks = wanted && runsHere;
  const latest = observationFor(view, endpoint)?.checkedAt ?? 0;
  const key = `${connection.identity}|screenshot|${m.id}|${generation}|${asks}`;
  const shot = useResource<BrowserScreenshot | null>(key, signal => asks ? api.screenshot(m.id, signal) : Promise.resolve(null));
  const { reload } = shot;
  // A new key loads on its own; only a newer check under the same key asks again.
  const seen = useRef({ key, latest });
  useEffect(() => {
    const before = seen.current;
    seen.current = { key, latest };
    if (asks && before.key === key && before.latest !== latest) reload();
  }, [key, latest, asks, reload]);
  if (!wanted) return null;
  return (
    <Card title="Screenshot" hint={<>Private to editors and admins · kept only on the node that took it</>}
      actions={asks ? <button type="button" className="icon-btn sm" onClick={reload} aria-label="Reload screenshot" data-tip="Reload screenshot"><Icon name="refresh" /></button> : undefined}
      className={shot.loading && shot.data ? 'refetching' : undefined}>
      {!runsHere ? (
        <EmptyState title={`${endpoint} doesn’t run this check`}>Each node keeps only the screenshots it took itself, so {endpoint} has none for this monitor. Open an assigned node to see its newest capture.</EmptyState>
      ) : shot.error && shot.data === undefined ? (
        <p className="muted small"><Glyph tone="warn" /> The screenshot couldn’t be loaded: {describeFailure(shot.error)}</p>
      ) : shot.data === undefined ? (
        <p className="muted">Loading the newest screenshot from {endpoint}…</p>
      ) : shot.data === null ? (
        <EmptyState title="No screenshot for the current check">
          {endpoint} keeps a capture only from a check of these settings that got a page to capture{view.state === 'paused' ? ', and this monitor is paused' : ''}. It has none yet, or its last one belonged to settings that have changed since.
        </EmptyState>
      ) : (
        <>
          {shot.error ? <p className="muted small"><Glyph tone="warn" /> Couldn’t refresh: {describeFailure(shot.error)} Showing the last loaded capture.</p> : null}
          <ScreenshotFigure shot={shot.data} view={view} endpoint={endpoint} now={overview.now} />
        </>
      )}
      <p className="foot-note">Screenshots are never replicated to other nodes, published on status pages or shown to viewers.</p>
    </Card>
  );
}
