import { BROWSER_DELAY_MAX, BROWSER_PARALLEL, BROWSER_PENDING, browserEndpointKind, type BrowserDraft, type BrowserField } from '../browser';
import { span } from '../format';
import { SCREENSHOT_HEIGHT, SCREENSHOT_WIDTH } from '../screenshot';
import { CheckField, SecretField, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

/**
 * The real-browser section of the monitor form, below the page address. What runs where and what each node needs come
 * first: Canary bundles and starts no browser, so the operator provides a dedicated Chromium service that every chosen
 * node can reach. Then the private endpoint and token (masked inputs), the optional wait and the optional screenshot. The
 * form offers no script, command, executable or browser-profile setting. `storedBearer` says the stored monitor has a
 * token, for the note that saving an emptied field removes it.
 */
export function BrowserFields({ d, set, errors, disabled, storedBearer, timeoutMs }: {
  d: BrowserDraft; set: (change: Partial<BrowserDraft>) => void; errors: Partial<Record<BrowserField, string>>; disabled: boolean; storedBearer: boolean; timeoutMs: number | null;
}) {
  const { kind, encrypted } = browserEndpointKind(d.endpoint.trim());
  return (
    <>
      <div className="full svc-note browser-note" role="note">
        <Icon name="globe" className="inline" />
        <span>
          <b>Each check loads the page in a dedicated Chromium browser service that you run.</b> Every selected node connects to it at the endpoint below over the DevTools protocol, opens a fresh private browser context and page for the check and closes them afterwards. Downloads are denied, only http:// and https:// requests are allowed, and the check adds no scripts and runs no commands. Up is a main document with a 2xx or 3xx status once the page is network-idle; a failed navigation, a download or any other status is down; anything else is pending ({BROWSER_PENDING}). Pending is never inverted and never recovers an outage.
        </span>
      </div>
      <div className="full svc-note browser-note" role="note">
        <Icon name="wrench" className="inline" />
        <span>
          <b>Canary doesn’t include or start a browser.</b> No node image bundles Chrome or Chromium: run a separate Chromium with its DevTools endpoint turned on where every chosen node can reach it. Use a dedicated, trusted browser with its own sandbox and network limits, never someone’s everyday browser or profile: anyone who can use the endpoint controls that browser, and the pages it loads can reach whatever its network allows.
        </span>
      </div>
      <SecretField full label="Browser endpoint" value={d.endpoint} onChange={endpoint => set({ endpoint })} error={errors.browserEndpoint} disabled={disabled} placeholder="ws://chromium.internal:9222/devtools/browser/…"
        hint={<>Either the browser’s full DevTools WebSocket URL (<span className="mono">ws://</span> or <span className="mono">wss://</span>, the <span className="mono">webSocketDebuggerUrl</span> it reports), or the <span className="mono">http://</span> or <span className="mono">https://</span> origin of its DevTools endpoint without a path or query: the node then reads <span className="mono">/json/version</span> there and connects only to a WebSocket it names on the same host and port. No user:password@ or #fragment; at most 2,048 bytes. Private: hidden from viewers and masked in previews until you show credentials. Changing it starts a new check.</>} />
      <SecretField full optional label="Bearer token" value={d.bearer} onChange={bearer => set({ bearer })} error={errors.browserBearer} disabled={disabled} storedSet={storedBearer}
        hint={<>Sent as <span className="mono">Authorization: Bearer</span> to the browser service only, for discovery and the WebSocket upgrade, never to the page. Printable ASCII without spaces, at most 4,096 characters. Private, like the endpoint.</>} />
      {d.bearer.trim() && kind !== 'unknown' && !encrypted ? (
        <p className="full small fwarn"><Glyph tone="warn" />This endpoint isn’t encrypted ({kind === 'websocket' ? 'ws://' : 'http://'}), so the token travels in clear. Across a network you don’t fully control, use {kind === 'websocket' ? 'wss://' : 'https://'}.</p>
      ) : null}
      <TextField label="Wait after load (optional)" value={d.delay} onChange={delay => set({ delay })} error={errors.browserDelay} placeholder="0" inputMode="numeric" disabled={disabled}
        hint={<>Milliseconds to wait once the page is network-idle, for late rendering, before the result{d.screenshot ? ' and the screenshot' : ''}. 0 to {BROWSER_DELAY_MAX.toLocaleString('en-US')}, and less than the timeout{timeoutMs ? ` (${span(timeoutMs)})` : ''}. Empty: no wait. It counts toward the latency.</>} />
      <CheckField label="Save a screenshot of each loaded page" checked={d.screenshot} onChange={screenshot => set({ screenshot })} disabled={disabled}
        hint={`A ${SCREENSHOT_WIDTH}×${SCREENSHOT_HEIGHT} PNG, at most 192 KiB, taken after the wait. Each node keeps only its own newest capture for this monitor, on its own disk: never replicated, published on status pages or shown to viewers; editors and admins see it on the monitor’s page. A capture that fails or is larger makes the check pending. Pages can show private data, so turn this on only for pages every editor may see. Changing it starts a new check.`} />
      <p className="full muted small"><Icon name="clock" className="inline" />Latency is page-load time, from the start of navigation until the page is network-idle (and the wait), not a network round trip. The timeout covers reaching the browser service, loading the page, the wait{d.screenshot ? ' and the screenshot' : ''}; past it the result is pending. Each node runs at most {BROWSER_PARALLEL} browser checks at once, and another one at that moment is pending.</p>
    </>
  );
}
