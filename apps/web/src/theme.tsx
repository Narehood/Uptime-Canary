import { useLayoutEffect, useState } from 'react';
import { useSystemDark } from './hooks';
import { Icon } from './ui/icons';

export type ThemePreference = 'system' | 'light' | 'dark';
export interface ThemeControl { preference: ThemePreference; mode: 'light' | 'dark'; cycle: () => void }

const KEY = 'uptime-canary.theme';
const ORDER: ThemePreference[] = ['system', 'light', 'dark'];

function stored(): ThemePreference {
  try {
    const value = localStorage.getItem(KEY);
    return value === 'light' || value === 'dark' ? value : 'system';
  } catch { return 'system'; }
}

export function useTheme(): ThemeControl {
  const [preference, setPreference] = useState<ThemePreference>(stored);
  const systemDark = useSystemDark();
  const mode = preference === 'system' ? (systemDark ? 'dark' : 'light') : preference;
  useLayoutEffect(() => {
    document.documentElement.dataset.mode = mode;
  }, [mode]);
  const cycle = (): void => {
    const next = ORDER[(ORDER.indexOf(preference) + 1) % ORDER.length]!;
    setPreference(next);
    try { if (next === 'system') localStorage.removeItem(KEY); else localStorage.setItem(KEY, next); } catch { /* preference lasts this visit only */ }
  };
  return { preference, mode, cycle };
}

export function ThemeButton({ theme }: { theme: ThemeControl }) {
  const label = { system: `System (${theme.mode})`, light: 'Light', dark: 'Dark' }[theme.preference];
  const next = ORDER[(ORDER.indexOf(theme.preference) + 1) % ORDER.length];
  return (
    <button type="button" className="icon-btn" onClick={theme.cycle} aria-label={`Theme: ${label}. Switch to ${next}.`} data-tip={`Theme: ${label}`}>
      <Icon name={theme.preference === 'system' ? 'auto' : theme.preference === 'light' ? 'sun' : 'moon'} />
    </button>
  );
}
