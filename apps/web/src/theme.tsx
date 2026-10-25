import { useLayoutEffect, useState, useSyncExternalStore } from 'react';
import { useSystemDark } from './hooks';
import { Icon } from './ui/icons';

export type ThemePreference = 'system' | 'light' | 'dark';
export interface ThemeControl {
  preference: ThemePreference; mode: 'light' | 'dark'; cycle: () => void;
  /** A public status page's own light or dark default while it's shown; null elsewhere. */
  page: 'light' | 'dark' | null;
}

const KEY = 'uptime-canary.theme';
const ORDER: ThemePreference[] = ['system', 'light', 'dark'];

function stored(): ThemePreference {
  try {
    const value = localStorage.getItem(KEY);
    return value === 'light' || value === 'dark' ? value : 'system';
  } catch { return 'system'; }
}

// A shown public status page may set its operator's light or dark theme (protocol 19). It replaces only the system
// setting: a visitor who picked light or dark keeps their choice. It's never stored; leaving the page clears it.
let pageTheme: 'light' | 'dark' | null = null;
const listeners = new Set<() => void>();
const subscribe = (listener: () => void): (() => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
export function setPageTheme(mode: 'light' | 'dark' | null): void {
  if (pageTheme === mode) return;
  pageTheme = mode;
  for (const listener of listeners) listener();
}

export function useTheme(): ThemeControl {
  const [preference, setPreference] = useState<ThemePreference>(stored);
  const systemDark = useSystemDark();
  const page = useSyncExternalStore(subscribe, () => pageTheme, () => null);
  const mode = preference === 'system' ? (page ?? (systemDark ? 'dark' : 'light')) : preference;
  useLayoutEffect(() => {
    document.documentElement.dataset.mode = mode;
  }, [mode]);
  const cycle = (): void => {
    const next = ORDER[(ORDER.indexOf(preference) + 1) % ORDER.length]!;
    setPreference(next);
    try { if (next === 'system') localStorage.removeItem(KEY); else localStorage.setItem(KEY, next); } catch { /* preference lasts this visit only */ }
  };
  return { preference, mode, cycle, page };
}

export function ThemeButton({ theme }: { theme: ThemeControl }) {
  const automatic = theme.page ? `Page default (${theme.mode})` : `System (${theme.mode})`;
  const label = { system: automatic, light: 'Light', dark: 'Dark' }[theme.preference];
  const next = ORDER[(ORDER.indexOf(theme.preference) + 1) % ORDER.length];
  const nextLabel = next === 'system' ? (theme.page ? 'the page default' : 'system') : next;
  return (
    <button type="button" className="icon-btn" onClick={theme.cycle} aria-label={`Theme: ${label}. Switch to ${nextLabel}.`} data-tip={`Theme: ${label}`}>
      <Icon name={theme.preference === 'system' ? 'auto' : theme.preference === 'light' ? 'sun' : 'moon'} />
    </button>
  );
}
