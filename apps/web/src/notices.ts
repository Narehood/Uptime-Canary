import { useSyncExternalStore } from 'react';

export interface Notice { id: number; text: string }

let notices: Notice[] = [];
let nextId = 1;
const listeners = new Set<() => void>();
const emit = (): void => { for (const listener of listeners) listener(); };

/** Confirms an acknowledged action. Shown as a toast and read by screen readers through its status region. */
export function notify(text: string): void {
  const id = nextId++;
  notices = [...notices.slice(-2), { id, text }];
  emit();
  setTimeout(() => dismiss(id), 8_000);
}

export function dismiss(id: number): void {
  if (!notices.some(notice => notice.id === id)) return;
  notices = notices.filter(notice => notice.id !== id);
  emit();
}

export function clearNotices(): void {
  notices = [];
  emit();
}

export function useNotices(): Notice[] {
  return useSyncExternalStore(listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, () => notices);
}
