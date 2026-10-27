import { useCallback, useEffect, useState } from 'react';
import type { NotificationChannelView, TagView } from '@uptime-canary/shared';
import type { CanaryApi } from './api';
import type { ChannelState, TagState } from './context';

/** Read the monitor's reference source without changing the page or its draft. */
export function useMonitorReferenceCatalogs(api: CanaryApi, source: string | undefined, selected: string | undefined,
  identity: string, channels: ChannelState, tags: TagState) {
  const key = `${identity}|${source ?? ''}`;
  const [nonce, setNonce] = useState(0);
  const reload = useCallback(() => setNonce(n => n + 1), []);
  const [state, setState] = useState<{ key: string; channels?: NotificationChannelView[]; tags?: TagView[]; error?: unknown }>({ key });
  useEffect(() => {
    if (!source || source === selected) return;
    const controller = new AbortController();
    const client = api.withRegisteredNode(source);
    void Promise.all([client.channels(controller.signal), client.tags(controller.signal)]).then(([channels, tags]) => {
      if (!controller.signal.aborted) setState({ key, channels, tags });
    }).catch(error => { if (!controller.signal.aborted) setState({ key, error }); });
    return () => controller.abort();
  }, [api, source, selected, key, nonce]);
  if (!source || source === selected) return { channels, tags };
  const current = state.key === key ? state : { key };
  return {
    channels: { list: current.channels ?? null, map: new Map((current.channels ?? []).map(v => [v.channel.id, v])), loading: !current.channels && !current.error,
      error: current.error, unsupported: false, reload } satisfies ChannelState,
    tags: { list: current.tags ?? null, map: new Map((current.tags ?? []).map(v => [v.tag.id, v])), loading: !current.tags && !current.error,
      error: current.error, unsupported: false, reload } satisfies TagState,
  };
}
