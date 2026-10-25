import type { ReactNode } from 'react';
import type { Tone } from '../model';

const stroke = { fill: 'none', stroke: 'currentColor', strokeWidth: 1.8, strokeLinecap: 'round', strokeLinejoin: 'round' } as const;

const ICONS = {
  overview: <g {...stroke}><rect x="3.5" y="3.5" width="7" height="7" rx="1.8" /><rect x="13.5" y="3.5" width="7" height="7" rx="1.8" /><rect x="3.5" y="13.5" width="7" height="7" rx="1.8" /><rect x="13.5" y="13.5" width="7" height="7" rx="1.8" /></g>,
  pulse: <path {...stroke} d="M2.5 12.5h4l2.2-5.5 4.2 11 2.4-5.5h6.2" />,
  incident: <g {...stroke}><path d="M12 3.8 21 19.5H3z" /><path d="M12 10v4.2" /><path d="M12 17.2h.01" /></g>,
  nodes: <g {...stroke}><circle cx="6" cy="6.5" r="2.6" /><circle cx="18" cy="6.5" r="2.6" /><circle cx="12" cy="17.5" r="2.6" /><path d="M8.6 6.5h6.8M7.3 8.8l3.4 6.4M16.7 8.8l-3.4 6.4" /></g>,
  bell: <g {...stroke}><path d="M6 16.5V11a6 6 0 0 1 12 0v5.5l1.5 1.8h-15z" /><path d="M10 20.8a2.2 2.2 0 0 0 4 0" /></g>,
  config: <g {...stroke}><path d="M4 7h8.5M17.5 7H20M4 17h2.5M11.5 17H20" /><circle cx="15" cy="7" r="2.4" /><circle cx="9" cy="17" r="2.4" /></g>,
  users: <g {...stroke}><circle cx="9" cy="8.5" r="3.3" /><path d="M3 19.5c.7-3.3 3.2-5.2 6-5.2s5.3 1.9 6 5.2" /><path d="M15.5 5.4a3.2 3.2 0 0 1 0 6.2M17.6 14.6c1.8.7 3 2.4 3.4 4.9" /></g>,
  groups: <g {...stroke}><path d="M12 3.6 20.5 8 12 12.4 3.5 8z" /><path d="m3.5 12.2 8.5 4.4 8.5-4.4" /><path d="m3.5 16.2 8.5 4.4 8.5-4.4" /></g>,
  search: <g {...stroke}><circle cx="11" cy="11" r="6.5" /><path d="m16 16 4.5 4.5" /></g>,
  chevDown: <path {...stroke} d="m7 10 5 5 5-5" />,
  chevRight: <path {...stroke} d="m10 7 5 5-5 5" />,
  chevLeft: <path {...stroke} d="m14 7-5 5 5 5" />,
  sun: <g {...stroke}><circle cx="12" cy="12" r="4" /><path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M5.3 5.3l1.6 1.6M17.1 17.1l1.6 1.6M5.3 18.7l1.6-1.6M17.1 6.9l1.6-1.6" /></g>,
  moon: <path {...stroke} d="M19.5 14.6A7.6 7.6 0 0 1 9.4 4.5a7.6 7.6 0 1 0 10.1 10.1z" />,
  auto: <g><circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" strokeWidth="1.8" /><path d="M12 4a8 8 0 0 1 0 16z" fill="currentColor" /></g>,
  send: <g {...stroke}><path d="M3.5 11.2 20.5 4l-7 16.5-2.6-6.6z" /><path d="M10.9 13.9 20.5 4" /></g>,
  repeat: <g {...stroke}><path d="M4 11V9.5A3.5 3.5 0 0 1 7.5 6H19" /><path d="m16 3 3 3-3 3" /><path d="M20 13v1.5a3.5 3.5 0 0 1-3.5 3.5H5" /><path d="m8 21-3-3 3-3" /></g>,
  branch: <g {...stroke}><circle cx="6" cy="5.5" r="2.2" /><circle cx="6" cy="18.5" r="2.2" /><circle cx="18" cy="7.5" r="2.2" /><path d="M6 7.7v8.6M18 9.7c0 4.3-6.5 3.4-11 6.8" /></g>,
  copy: <g {...stroke}><rect x="8.5" y="8.5" width="11" height="11" rx="2" /><path d="M15.5 8.5V6A1.5 1.5 0 0 0 14 4.5H6A1.5 1.5 0 0 0 4.5 6v8A1.5 1.5 0 0 0 6 15.5h2.5" /></g>,
  plus: <path fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" d="M12 5v14M5 12h14" />,
  plug: <g {...stroke}><path d="M9 3v4M15 3v4" /><path d="M6.5 7h11v3.5a5.5 5.5 0 0 1-11 0z" /><path d="M12 16v5" /></g>,
  clock: <g {...stroke}><circle cx="12" cy="12" r="8.5" /><path d="M12 7.5V12l3 2" /></g>,
  lock: <g {...stroke}><rect x="5" y="10.5" width="14" height="10" rx="2" /><path d="M8 10.5V8a4 4 0 0 1 8 0v2.5" /></g>,
  merge: <g {...stroke}><path d="M5 3.5c0 5.5 7 5 7 10.5v6.5M19 3.5c0 5.5-7 5-7 10.5" /><path d="m8.8 17.6 3.2 3.2 3.2-3.2" /></g>,
  table: <g {...stroke}><rect x="3.5" y="4.5" width="17" height="15" rx="2" /><path d="M3.5 9.5h17M3.5 14.5h17M9.5 9.5v10" /></g>,
  chart: <g {...stroke}><path d="M3.5 19.5h17" /><path d="m4.5 15 4.5-5 4 3.5 6.5-7.5" /></g>,
  eye: <g {...stroke}><path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" /><circle cx="12" cy="12" r="2.8" /></g>,
  eyeOff: <g {...stroke}><path d="M9.9 5.8A9.6 9.6 0 0 1 12 5.5c6 0 9.5 6.5 9.5 6.5a16.3 16.3 0 0 1-2.4 3.2M6.6 7.3C3.9 9 2.5 12 2.5 12s3.5 6.5 9.5 6.5c1.9 0 3.5-.6 4.9-1.5" /><path d="M10 10a2.8 2.8 0 0 0 4 4" /><path d="m4 4 16 16" /></g>,
  edit: <g {...stroke}><path d="M14.5 5.5 18.5 9.5 8.5 19.5H4.5v-4z" /><path d="m12.5 7.5 4 4" /></g>,
  pause: <g {...stroke}><circle cx="12" cy="12" r="8.5" /><path d="M10 9v6M14 9v6" /></g>,
  play: <g {...stroke}><circle cx="12" cy="12" r="8.5" /><path d="m10.2 8.8 5 3.2-5 3.2z" /></g>,
  trash: <g {...stroke}><path d="M4.5 7h15M9.5 7V4.5h5V7M6.5 7l1 12.5h9l1-12.5" /><path d="M10.5 11v5M13.5 11v5" /></g>,
  refresh: <g {...stroke}><path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3" /><path d="M19.5 4.5v4h-4" /></g>,
  logout: <g {...stroke}><path d="M14.5 4.5h3a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2h-3" /><path d="M10 16.5 5.5 12 10 7.5M5.5 12h10" /></g>,
  check: <path {...stroke} strokeWidth={2} d="m5 12.5 4.5 4.5L19 7.5" />,
  close: <path {...stroke} d="M6.5 6.5l11 11M17.5 6.5l-11 11" />,
  info: <g {...stroke}><circle cx="12" cy="12" r="8.5" /><path d="M12 11v5.5" /><path d="M12 7.8h.01" /></g>,
  key: <g {...stroke}><circle cx="8" cy="15" r="3.8" /><path d="m10.8 12.3 8-8M16.5 6.5l2.5 2.5M14.3 8.7l2 2" /></g>,
  calendar: <g {...stroke}><rect x="3.5" y="5" width="17" height="15.5" rx="2.2" /><path d="M3.5 10h17M8 3v4M16 3v4" /><path d="M8 14h3M8 17h6" /></g>,
  globe: <g {...stroke}><circle cx="12" cy="12" r="8.5" /><path d="M3.5 12h17M12 3.5c2.4 2.4 3.6 5.2 3.6 8.5s-1.2 6.1-3.6 8.5c-2.4-2.4-3.6-5.2-3.6-8.5S9.6 5.9 12 3.5z" /></g>,
  mail: <g {...stroke}><rect x="3.5" y="5.5" width="17" height="13" rx="2" /><path d="m4 7 8 6 8-6" /></g>,
  chat: <g {...stroke}><path d="M5 5.5h14a1.5 1.5 0 0 1 1.5 1.5v8.5A1.5 1.5 0 0 1 19 17h-7.5L7 20.5V17H5a1.5 1.5 0 0 1-1.5-1.5V7A1.5 1.5 0 0 1 5 5.5z" /><path d="M8.5 10.5h.01M12 10.5h.01M15.5 10.5h.01" /></g>,
  hook: <g {...stroke}><path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1" /><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1" /></g>,
  wrench: <g {...stroke}><path d="M14.7 4.2a4.5 4.5 0 0 0-5.4 5.9L4 15.4a2 2 0 0 0 2.8 2.8l5.3-5.3a4.5 4.5 0 0 0 5.9-5.4l-2.6 2.6-2.4-.6-.6-2.4z" /></g>,
  megaphone: <g {...stroke}><path d="M4 10v4a1 1 0 0 0 1 1h2.5l7 4V5l-7 4H5a1 1 0 0 0-1 1z" /><path d="M7.5 15 9 20h2.5M18 9.5a3.5 3.5 0 0 1 0 5" /></g>,
  arrowUp: <path {...stroke} d="M12 19V5M6.5 10.5 12 5l5.5 5.5" />,
  arrowDown: <path {...stroke} d="M12 5v14M6.5 13.5 12 19l5.5-5.5" />,
  pin: <g {...stroke}><path d="M9 4h6l-1 5 3.5 3.5h-11L10 9z" /><path d="M12 12.5V20" /></g>,
  external: <g {...stroke}><path d="M13.5 4.5h6v6M19.5 4.5 11 13" /><path d="M17.5 13.5v5a1.5 1.5 0 0 1-1.5 1.5H6A1.5 1.5 0 0 1 4.5 18.5V8.5A1.5 1.5 0 0 1 6 7h5" /></g>,
  upload: <g {...stroke}><path d="M12 15V4.5M7.5 9 12 4.5 16.5 9" /><path d="M4.5 15v3a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-3" /></g>,
  download: <g {...stroke}><path d="M12 4.5V15M7.5 10.5 12 15l4.5-4.5" /><path d="M4.5 15v3a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-3" /></g>,
  code: <g {...stroke}><path d="m8.5 7.5-5 4.5 5 4.5M15.5 7.5l5 4.5-5 4.5" /></g>,
  hand: <g {...stroke}><path d="M8 13V6.5a1.5 1.5 0 0 1 3 0V11M11 10.5V5a1.5 1.5 0 0 1 3 0v5.5M14 10.5V6.5a1.5 1.5 0 0 1 3 0v6.5c0 4.2-2.5 7.5-6.3 7.5-2.5 0-4-1.2-5.3-3.3L3.6 14a1.4 1.4 0 0 1 2.4-1.5L8 15" /></g>,
  hash: <path {...stroke} d="M10 4 8 20M16 4l-2 16M4.5 9h15M4.5 15h15" />,
  phone: <g {...stroke}><rect x="6.5" y="2.8" width="11" height="18.4" rx="2.4" /><path d="M10.5 18h3" /></g>,
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof ICONS;

export function Icon({ name, className }: { name: IconName; className?: string }) {
  return (
    <svg className={className ? `ic ${className}` : 'ic'} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      {ICONS[name]}
    </svg>
  );
}

const SHAPES: Record<Tone, ReactNode> = {
  up: <><circle cx="8" cy="8" r="7" fill="currentColor" /><path d="M4.9 8.3l2.1 2.1 4.1-4.4" fill="none" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></>,
  down: <><rect x="1.3" y="1.3" width="13.4" height="13.4" rx="3" fill="currentColor" /><path d="M5.4 5.4l5.2 5.2M10.6 5.4l-5.2 5.2" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" /></>,
  warn: <><path className="edge-warn" d="M8 1.4l7 12.4H1z" fill="currentColor" strokeWidth="1.3" strokeLinejoin="round" /><path d="M8 5.9v3.8" stroke="#151514" strokeWidth="1.7" strokeLinecap="round" /><circle cx="8" cy="11.9" r="1" fill="#151514" /></>,
  serious: <><path className="edge-serious" d="M8 .9l7.1 7.1L8 15.1.9 8z" fill="currentColor" strokeWidth="1.2" strokeLinejoin="round" /><path d="M8 4.6v4.2" stroke="#151514" strokeWidth="1.7" strokeLinecap="round" /><circle cx="8" cy="11.1" r="1" fill="#151514" /></>,
  unknown: <><circle cx="8" cy="8" r="6.4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeDasharray="2.4 1.8" /><path d="M6.3 6.4a1.8 1.8 0 1 1 2.6 1.6c-.6.3-.9.6-.9 1.2v.2" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /><circle cx="8" cy="11.5" r=".9" fill="currentColor" /></>,
  paused: <><circle cx="8" cy="8" r="6.4" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="M6.4 5.6v4.8M9.6 5.6v4.8" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /></>,
  pending: <><circle cx="8" cy="8" r="6.4" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="M8 4.7V8l2.3 1.5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" /></>,
  none: <path d="M5 8h6" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />,
};

/** Shape carries the meaning; color only reinforces it. Unlabelled glyphs are decorative. */
export function Glyph({ tone, label, className }: { tone: Tone; label?: string; className?: string }) {
  return (
    <span className={`g g-${tone}${className ? ` ${className}` : ''}`} {...(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': true })}>
      <svg viewBox="0 0 16 16" focusable="false" aria-hidden="true">{SHAPES[tone]}</svg>
    </span>
  );
}

const BIRD = 'M31.2 13.4C31.2 9.3 28.2 6 24.4 6C20.6 6 17.6 9.1 17.6 12.9C17.6 14.6 18.1 16 18.9 17.1C13.6 18.4 9.6 22.6 8.4 27.6L4 30.6L10.6 30.6C12.8 31.8 15.6 32.4 18.6 32.4C26.2 32.4 32.2 27.2 32.2 20.4C32.2 17.8 31.9 15.4 31.2 13.4Z';

/** The canary perched on a wire that turns into a heartbeat. `quiet` drops the blip for empty states. */
export function CanaryMark({ className, wire = '#a3a198', quiet = false }: { className?: string; wire?: string; quiet?: boolean }) {
  return (
    <svg className={className} viewBox="0 0 40 40" aria-hidden="true" focusable="false">
      <path d={quiet ? 'M1.6 35.5H38.4' : 'M1.6 35.5H23.2L25.8 31.4L28.8 38.2L31.4 35.5H38.4'} fill="none" stroke={wire} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M16.6 32.2V35.4M20.4 32V35.4" stroke="#f28c28" strokeWidth="1.6" strokeLinecap="round" />
      <path d={BIRD} fill="#ffd23f" />
      <path d="M13.6 24C17.4 21.2 23.4 21.4 27 25.2C22.6 27.4 17.2 27.2 13.6 24Z" fill="#e9a800" />
      <path d="M31 11.2L36.6 13.5L31.1 15.7Z" fill="#f28c28" />
      <circle cx="26.3" cy="11.6" r="1.6" fill="#151514" />
    </svg>
  );
}

/** Bird head on an ink tile: the endpoint's "plug" identity in the top bar. */
export function EndpointMark() {
  return (
    <span className="ep-mark" aria-hidden="true">
      <svg viewBox="4 2 36 36" focusable="false">
        <path d={BIRD} fill="#ffd23f" />
        <path d="M13.6 24C17.4 21.2 23.4 21.4 27 25.2C22.6 27.4 17.2 27.2 13.6 24Z" fill="#e9a800" />
        <path d="M31 11.2L36.6 13.5L31.1 15.7Z" fill="#f28c28" />
        <circle cx="26.3" cy="11.6" r="1.6" fill="#151514" />
      </svg>
    </span>
  );
}
