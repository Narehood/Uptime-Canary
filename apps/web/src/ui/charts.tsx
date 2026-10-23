import { useId, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import type { Observation, ObservedTimeTotals } from '@uptime-canary/shared';
import { duration, pct, shares } from '../availability';
import { hasLatency, latency, latencyOf, shortClock, stamp } from '../format';
import { useWidth } from '../hooks';
import { stateTone, stateWord } from '../model';
import { Glyph } from './icons';

function niceMax(value: number): number {
  const power = 10 ** Math.floor(Math.log10(Math.max(value, 1)));
  for (const step of [1, 2, 2.5, 5, 10]) if (step * power >= value) return step * power;
  return 10 * power;
}

/** `other`: checks that were neither up nor down, such as a container still starting (pending). */
interface Bucket { from: number; to: number; up: number; down: number; other: number }

/** One cell per check, or per equal-size group of checks when there are more than fit. Oldest on the left. */
export function HistoryStrip({ samples, now, maxCells = 90 }: { samples: Observation[]; now: number; maxCells?: number }) {
  const buckets = useMemo(() => {
    const size = Math.max(1, Math.ceil(samples.length / maxCells));
    const result: Bucket[] = [];
    for (let i = 0; i < samples.length; i += size) {
      const slice = samples.slice(i, i + size);
      result.push({
        from: slice[0]!.checkedAt, to: slice[slice.length - 1]!.checkedAt,
        up: slice.filter(s => s.state === 'up').length, down: slice.filter(s => s.state === 'down').length,
        other: slice.filter(s => s.state !== 'up' && s.state !== 'down').length,
      });
    }
    return result;
  }, [samples, maxCells]);
  const failed = samples.filter(s => s.state === 'down').length;
  const summary = `${samples.length} checks, ${failed} failed`;
  return (
    <div>
      <div className="strip" role="img" aria-label={`Check results, oldest to newest: ${summary}.`}>
        {buckets.map(bucket => {
          const cls = bucket.down ? (bucket.up || bucket.other ? 'p' : 'd') : bucket.other ? (bucket.up ? 'm' : 'n') : '';
          const total = bucket.up + bucket.down + bucket.other;
          const when = bucket.from === bucket.to ? stamp(bucket.from, now) : `${stamp(bucket.from, now)} – ${stamp(bucket.to, now)}`;
          const tip = total === 1
            ? `${when}\n${bucket.down ? 'Down' : bucket.up ? 'Up' : 'Pending or unknown (neither up nor down)'}`
            : `${when}\n${bucket.up} up · ${bucket.down} down${bucket.other ? ` · ${bucket.other} pending or unknown` : ''}`;
          return <i key={bucket.from} className={cls || undefined} data-tip={tip} aria-hidden="true" />;
        })}
      </div>
      <div className="axis-row" aria-hidden="true">
        <span>{shortClock(samples[0]!.checkedAt, now)}</span>
        <span>{shortClock(samples[samples.length - 1]!.checkedAt, now)}</span>
      </div>
    </div>
  );
}

const HEIGHT = 190;
const M = { l: 48, r: 66, t: 14, b: 26 };

/** Single-series response time with failing bands. Hover or arrow keys move the crosshair; the table twin holds every value. */
export function LatencyChart({ samples, now, label }: { samples: Observation[]; now: number; label: string }) {
  const box = useRef<HTMLDivElement>(null);
  const width = useWidth(box);
  const [focus, setFocus] = useState<number | null>(null);
  const [spoken, setSpoken] = useState('');
  const W = Math.max(width - M.l - M.r, 40);
  const H = HEIGHT - M.t - M.b;
  const t0 = samples[0]!.checkedAt;
  const t1 = samples[samples.length - 1]!.checkedAt;
  const spanMs = Math.max(t1 - t0, 1);
  const X = (t: number): number => M.l + ((t - t0) / spanMs) * W;
  const ups = samples.filter(s => s.state === 'up' && hasLatency(s));
  const top = niceMax(Math.max(1, ...ups.map(s => s.latencyMs)) * 1.08);
  const Y = (v: number): number => M.t + H - (Math.min(v, top) / top) * H;

  const { line, area, dots, bands } = useMemo(() => {
    const runs: Observation[][] = [];
    const downRuns: Observation[][] = [];
    let run: Observation[] = [];
    let downRun: Observation[] = [];
    for (const sample of samples) {
      if (sample.state === 'up' && hasLatency(sample)) {
        run.push(sample);
        if (downRun.length) { downRuns.push(downRun); downRun = []; }
      } else if (sample.state === 'down') {
        downRun.push(sample);
        if (run.length) { runs.push(run); run = []; }
      } else {
        // Pending, unknown and unmeasured checks break the line without drawing a failure band.
        if (run.length) { runs.push(run); run = []; }
        if (downRun.length) { downRuns.push(downRun); downRun = []; }
      }
    }
    if (run.length) runs.push(run);
    if (downRun.length) downRuns.push(downRun);
    let linePath = '', areaPath = '';
    const single: Observation[] = [];
    for (const r of runs) {
      if (r.length === 1) { single.push(r[0]!); continue; }
      const points = r.map(s => `${X(s.checkedAt).toFixed(1)},${Y(s.latencyMs).toFixed(1)}`);
      linePath += `M${points.join('L')}`;
      areaPath += `M${X(r[0]!.checkedAt).toFixed(1)},${Y(0)}L${points.join('L')}L${X(r[r.length - 1]!.checkedAt).toFixed(1)},${Y(0)}Z`;
    }
    return {
      line: linePath, area: areaPath, dots: single,
      bands: downRuns.map(r => {
        const x0 = X(r[0]!.checkedAt), x1 = X(r[r.length - 1]!.checkedAt);
        const w = Math.max(x1 - x0, 4);
        return { x: Math.max(M.l, x0 - (w === 4 && x1 === x0 ? 2 : 0)), w, from: r[0]!.checkedAt, count: r.length };
      }),
    };
    // X and Y are derived from width, samples and top.
  }, [samples, width, top]);

  // Direct-label the newest value only when the newest check succeeded; otherwise the failing band speaks.
  const newest = samples[samples.length - 1]!;
  const lastUp = newest.state === 'up' && hasLatency(newest) ? newest : undefined;
  const ticks = [0, top / 2, top];
  const xTicks = [t0, t0 + spanMs / 2, t1];

  const nearest = (clientX: number): number => {
    const rect = box.current!.getBoundingClientRect();
    const t = t0 + ((clientX - rect.left - M.l) / W) * spanMs;
    let lo = 0, hi = samples.length - 1;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (samples[mid]!.checkedAt < t) lo = mid; else hi = mid;
    }
    return Math.abs(samples[lo]!.checkedAt - t) <= Math.abs(samples[hi]!.checkedAt - t) ? lo : hi;
  };
  const describe = (sample: Observation): string => `${stamp(sample.checkedAt, now)}: ${sample.state === 'up' ? `up, ${hasLatency(sample) ? latency(sample.latencyMs) : 'no measured latency'}` : `${stateWord(sample.state).toLowerCase()}, ${sample.message}`}`;
  const onKey = (event: KeyboardEvent): void => {
    const last = samples.length - 1;
    let next = focus ?? last;
    if (event.key === 'ArrowLeft') next = Math.max(0, next - 1);
    else if (event.key === 'ArrowRight') next = Math.min(last, next + 1);
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = last;
    else if (event.key === 'Escape') { setFocus(null); return; }
    else return;
    event.preventDefault();
    setFocus(next);
    setSpoken(describe(samples[next]!));
  };

  const active = focus !== null ? samples[focus] : undefined;
  const ax = active ? X(active.checkedAt) : 0;
  return (
    <div
      ref={box} className="chart" tabIndex={0} role="group"
      aria-label={`${label}. Use the left and right arrow keys to read individual checks.`}
      onKeyDown={onKey} onBlur={() => setFocus(null)}
      onPointerMove={(event: PointerEvent) => setFocus(nearest(event.clientX))} onPointerLeave={() => setFocus(null)}
    >
      <svg width={width} height={HEIGHT} aria-hidden="true" focusable="false">
        {ticks.map(v => (
          <g key={v}>
            <line className={v === 0 ? 'base' : 'gridl'} x1={M.l} x2={M.l + W} y1={Y(v)} y2={Y(v)} />
            <text className="tick" x={M.l - 8} y={Y(v) + 4} textAnchor="end">{v === 0 ? '0 ms' : latency(v)}</text>
          </g>
        ))}
        {xTicks.map((t, i) => (
          <text key={i} className="tick" x={X(t)} y={HEIGHT - 6} textAnchor={i === 0 ? 'start' : i === 2 ? 'end' : 'middle'}>{shortClock(t, now)}</text>
        ))}
        {bands.map(band => (
          <g key={band.from}>
            <rect className="band" x={band.x} y={M.t} width={band.w} height={H} />
            <line className="bandedge" x1={band.x} x2={band.x} y1={M.t} y2={M.t + H} />
          </g>
        ))}
        <path className="area" d={area} />
        <path className="line" d={line} />
        {dots.map(sample => <circle key={sample.checkedAt} className="dot" cx={X(sample.checkedAt)} cy={Y(sample.latencyMs)} r={3.5} />)}
        {lastUp ? (
          <g>
            <circle className="dot" cx={X(lastUp.checkedAt)} cy={Y(lastUp.latencyMs)} r={4} />
            <text className="lbl" x={X(lastUp.checkedAt) + 9} y={Y(lastUp.latencyMs) + 4}>{latency(lastUp.latencyMs)}</text>
          </g>
        ) : null}
        {active ? (
          <g>
            <line className="xh" x1={ax} x2={ax} y1={M.t} y2={M.t + H} />
            {active.state === 'up' && hasLatency(active) ? <circle className="dot" cx={ax} cy={Y(active.latencyMs)} r={4.5} /> : null}
          </g>
        ) : null}
      </svg>
      {active ? (
        <div className="tip chart-tip" style={{ left: Math.min(Math.max(ax, 120), width - 120), top: 4 }}>
          <b>{stamp(active.checkedAt, now)}</b>
          <span className="r"><Glyph tone={stateTone(active.state)} /><strong>{active.state === 'up' ? latencyOf(active) : stateWord(active.state)}</strong></span>
          {active.state !== 'up' ? <span className="s">{active.message}</span> : null}
        </div>
      ) : null}
      <span className="vh" aria-live="polite">{spoken}</span>
    </div>
  );
}

const AV_HEIGHT = 168;
const AM = { l: 48, r: 12, t: 20, b: 26 };
const LANE = { y: 4, h: 7 };

/** What one bucket's tooltip, table row and screen-reader line say. Percentages come from the node, never re-derived. */
export function bucketWords(t: ObservedTimeTotals): string {
  const parts = [`up ${duration(t.upMs)}`, `down ${duration(t.downMs)}`];
  if (t.pendingMs) parts.push(`pending ${duration(t.pendingMs)}`);
  if (t.unknownMs) parts.push(`unknown ${duration(t.unknownMs)}`);
  const known = t.availabilityPercent === null ? 'no known time' : `${pct(t.availabilityPercent)} available of known time`;
  return `${parts.join(', ')}; ${known}; coverage ${pct(t.coveragePercent)}${t.maintenanceMs ? `; maintenance overlapped ${duration(t.maintenanceMs)}` : ''}`;
}

/**
 * Observed time per bucket as a 100 % column: up at the baseline, then down, pending and unknown (hatched) on top, so
 * gaps stay visible instead of being drawn as healthy. Maintenance is a separate lane above; it removes nothing.
 */
export function AvailabilityChart({ buckets, now, label }: { buckets: ObservedTimeTotals[]; now: number; label: string }) {
  const box = useRef<HTMLDivElement>(null);
  const width = useWidth(box);
  const hatchId = `hatch-${useId().replace(/[^a-zA-Z0-9_-]/gu, '')}`;
  const [focus, setFocus] = useState<number | null>(null);
  const [spoken, setSpoken] = useState('');
  const W = Math.max(width - AM.l - AM.r, 40);
  const H = AV_HEIGHT - AM.t - AM.b;
  const t0 = buckets[0]!.startMs;
  const t1 = buckets[buckets.length - 1]!.endMs;
  const X = (t: number): number => AM.l + ((t - t0) / Math.max(t1 - t0, 1)) * W;
  const base = AM.t + H;

  const columns = useMemo(() => buckets.map(bucket => {
    const x0 = X(bucket.startMs), x1 = X(bucket.endMs);
    const pixel = x1 - x0;
    const gap = pixel >= 6 ? 2 : pixel >= 3 ? 1 : 0;
    const s = shares(bucket);
    let y = base;
    const segments: { cls: string; y: number; h: number }[] = [];
    for (const [cls, share] of [['av-up', s.up], ['av-down', s.down], ['av-pend', s.pending], ['av-unk', s.unknown]] as const) {
      const h = share * H;
      if (h <= 0) continue;
      // A one-pixel surface gap separates stacked states when there's room; a sliver is never dropped.
      const inner = h >= 3 && y < base ? 1 : 0;
      segments.push({ cls, y: y - h, h: Math.max(h - inner, 0.75) });
      y -= h;
    }
    return { x: x0 + gap / 2, w: Math.max(pixel - gap, 1), segments, maintenance: s.maintenance };
    // X depends on width and the bucket bounds.
  }), [buckets, width, H, base]);

  const describe = (bucket: ObservedTimeTotals): string => `${stamp(bucket.startMs, now)} to ${stamp(bucket.endMs, now)}: ${bucketWords(bucket)}`;
  const nearest = (clientX: number): number => {
    const rect = box.current!.getBoundingClientRect();
    const t = t0 + ((clientX - rect.left - AM.l) / W) * (t1 - t0);
    const index = buckets.findIndex(bucket => t < bucket.endMs);
    return index < 0 ? buckets.length - 1 : index;
  };
  const onKey = (event: KeyboardEvent): void => {
    const last = buckets.length - 1;
    let next = focus ?? last;
    if (event.key === 'ArrowLeft') next = Math.max(0, next - 1);
    else if (event.key === 'ArrowRight') next = Math.min(last, next + 1);
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = last;
    else if (event.key === 'Escape') { setFocus(null); return; }
    else return;
    event.preventDefault();
    setFocus(next);
    setSpoken(describe(buckets[next]!));
  };
  const active = focus !== null ? buckets[focus] : undefined;
  const column = focus !== null ? columns[focus] : undefined;
  const xTicks = [t0, t0 + (t1 - t0) / 2, t1];
  return (
    <div
      ref={box} className="chart av-chart" tabIndex={0} role="group"
      aria-label={`${label}. Use the left and right arrow keys to read individual buckets.`}
      onKeyDown={onKey} onBlur={() => setFocus(null)}
      onPointerMove={(event: PointerEvent) => setFocus(nearest(event.clientX))} onPointerLeave={() => setFocus(null)}
    >
      <svg width={width} height={AV_HEIGHT} aria-hidden="true" focusable="false">
        <defs>
          <pattern id={hatchId} width="5" height="5" patternUnits="userSpaceOnUse" patternTransform="rotate(135)">
            <rect width="5" height="5" className="av-unk-bg" /><line x1="0" y1="0" x2="0" y2="5" className="av-unk-line" />
          </pattern>
        </defs>
        {[0, 0.5, 1].map(v => (
          <g key={v}>
            <line className={v === 0 ? 'base' : 'gridl'} x1={AM.l} x2={AM.l + W} y1={base - v * H} y2={base - v * H} />
            <text className="tick" x={AM.l - 8} y={base - v * H + 4} textAnchor="end">{v * 100}%</text>
          </g>
        ))}
        <text className="tick" x={AM.l - 8} y={LANE.y + LANE.h - 0.5} textAnchor="end">maint.</text>
        <rect className="av-lane" x={AM.l} y={LANE.y} width={W} height={LANE.h} rx={2} />
        {xTicks.map((t, i) => (
          <text key={i} className="tick" x={X(t)} y={AV_HEIGHT - 6} textAnchor={i === 0 ? 'start' : i === 2 ? 'end' : 'middle'}>{shortClock(t, now)}</text>
        ))}
        {columns.map((c, i) => (
          <g key={buckets[i]!.startMs} className={focus === i ? 'on' : undefined}>
            {c.maintenance > 0 ? <rect className="av-maint" x={c.x} y={LANE.y} width={c.w} height={LANE.h} /> : null}
            {c.segments.map(seg => <rect key={seg.cls} className={seg.cls} x={c.x} y={seg.y} width={c.w} height={seg.h} fill={seg.cls === 'av-unk' ? `url(#${hatchId})` : undefined} />)}
          </g>
        ))}
        {column ? <rect className="av-focus" x={column.x - 1} y={AM.t - 1} width={column.w + 2} height={H + 2} rx={2} /> : null}
      </svg>
      {active && column ? (
        <div className="tip chart-tip av-tip" style={{ left: Math.min(Math.max(column.x + column.w / 2, 130), width - 130), top: 4 }}>
          <b>{stamp(active.startMs, now)} – {stamp(active.endMs, now)}</b>
          <span className="r"><strong>{pct(active.availabilityPercent)}</strong> of known time up · coverage {pct(active.coveragePercent)}</span>
          <span className="s">Up {duration(active.upMs)} · Down {duration(active.downMs)} · Pending {duration(active.pendingMs)} · Unknown {duration(active.unknownMs)}</span>
          {active.maintenanceMs ? <span className="s">Maintenance overlapped {duration(active.maintenanceMs)} (still counted)</span> : null}
        </div>
      ) : null}
      <span className="vh" aria-live="polite">{spoken}</span>
    </div>
  );
}
