import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject } from 'react';
import { isAbort } from './api';
import { session } from './session';

/** Re-renders on an interval; used only for honest "n s ago" ages. */
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

export interface Resource<T> {
  data: T | undefined;
  error: unknown;
  loading: boolean;
  /** Browser time of the last successful load. */
  loadedAt: number | undefined;
  reload: () => void;
}

/**
 * Loads data for a view, aborting on unmount or when `key` changes (for example after an endpoint switch).
 * Optional refreshes are sequential, never overlapping, and keep the previous data on failure. While the overview poll
 * reports the credential rejected (401), they wait instead of sending it again; `reload` still asks at once.
 */
export function useResource<T>(key: string, load: (signal: AbortSignal) => Promise<T>, refreshMs?: number): Resource<T> {
  const [state, setState] = useState<{ key: string; data?: T; error?: unknown; loading: boolean; loadedAt?: number }>({ key, loading: true });
  const [nonce, setNonce] = useState(0);
  const loader = useRef(load);
  loader.current = load;
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const run = async (): Promise<void> => {
      setState(previous => previous.key === key ? { ...previous, loading: true } : { key, loading: true });
      try {
        const data = await loader.current(controller.signal);
        if (!controller.signal.aborted) setState({ key, data, error: undefined, loading: false, loadedAt: Date.now() });
      } catch (error) {
        if (controller.signal.aborted || isAbort(error)) return;
        setState(previous => ({ key, data: previous.key === key ? previous.data : undefined, error, loading: false, loadedAt: previous.key === key ? previous.loadedAt : undefined }));
      }
      if (refreshMs && !controller.signal.aborted) timer = setTimeout(refresh, refreshMs);
    };
    const refresh = (): void => {
      if (session.getSnapshot().connection?.link === 'rejected') timer = setTimeout(refresh, refreshMs);
      else void run();
    };
    void run();
    return () => { controller.abort(); if (timer) clearTimeout(timer); };
  }, [key, nonce, refreshMs]);
  const reload = useCallback(() => setNonce(n => n + 1), []);
  const current = state.key === key ? state : { key, loading: true };
  return { data: current.data, error: current.error, loading: current.loading, loadedAt: current.loadedAt, reload };
}

/** Tracks an element's content width so charts redraw to their card instead of scaling. */
export function useWidth<T extends HTMLElement>(ref: RefObject<T | null>, fallback = 640): number {
  const [width, setWidth] = useState(fallback);
  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) return;
    setWidth(element.clientWidth || fallback);
    const observer = new ResizeObserver(entries => {
      const next = Math.round(entries[0]?.contentRect.width ?? fallback);
      if (next > 0) setWidth(next);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [ref, fallback]);
  return width;
}

const darkQuery = typeof window !== 'undefined' ? window.matchMedia('(prefers-color-scheme: dark)') : null;
export function useSystemDark(): boolean {
  return useSyncExternalStore(
    listener => { darkQuery?.addEventListener('change', listener); return () => darkQuery?.removeEventListener('change', listener); },
    () => darkQuery?.matches ?? false,
  );
}

/** Focus the error summary after each failed attempt, so keyboard and screen reader users hear what to fix. */
export function useSummaryFocus(): [RefObject<HTMLDivElement | null>, () => void] {
  const ref = useRef<HTMLDivElement>(null);
  const [attempts, setAttempts] = useState(0);
  useEffect(() => { if (attempts) ref.current?.focus(); }, [attempts]);
  return [ref, () => setAttempts(n => n + 1)];
}
