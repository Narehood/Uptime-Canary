import { useId } from 'react';
import { plural, span } from '../format';
import { STEAM_DAILY_CALLS, STEAM_DEFAULT_API, STEAM_ENDPOINT, STEAM_PACKET_DEFAULT, type SteamDraft, type SteamField } from '../steam';
import { CheckField, Disclosure, PemField, SecretField, Seg, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

/**
 * The Steam game server section of the monitor form. What is shared comes first, then the address, the key, the failure
 * policy and the optional ICMP measurement; the API endpoint and its trust sit in a disclosure. Quota guidance uses the
 * form's own interval and nodes.
 */
export function SteamFields({ d, set, errors, disabled, open, onToggle, storedKey, timeoutMs, intervalMs, nodeCount }: {
  d: SteamDraft; set: (change: Partial<SteamDraft>) => void; errors: Partial<Record<SteamField, string>>; disabled: boolean;
  open: Record<string, boolean>; onToggle: (section: string) => void;
  /** The key the edited monitor has stored; empty for a new monitor or another type. */
  storedKey: string;
  timeoutMs: number | null; intervalMs: number | null; nodeCount: number;
}) {
  const id = useId();
  const api = d.apiUrl.trim();
  const plainHttp = /^http:/iu.test(api);
  const keyChanged = Boolean(storedKey) && d.apiKey.trim() !== storedKey;
  const nodes = Math.max(nodeCount, 1);
  const perDay = intervalMs ? Math.round((86_400_000 / intervalMs) * nodes) : null;
  const apiSummary = [api || 'api.steampowered.com (default)', d.ca.trim() ? 'custom CA' : 'system roots', d.insecure ? 'plain HTTP allowed' : ''].filter(Boolean).join(' · ');
  return (
    <>
      <div className="full steam-note" role="note">
        <Icon name="lock" className="inline" />
        <span>
          <b>Each check tells the Steam API which server you watch, and sends your API key with it.</b> Every selected node resolves the host (IPv4 first) and makes its own request to {api ? 'the API endpoint you configure below' : 'Steam’s Web API'}, carrying the resolved address and port and the key, which travels as a query parameter in the request URL. Use Steam’s API, or an HTTPS endpoint or proxy you trust: whoever runs it, and its logs, can read the key. This checks the server’s listing in Steam’s directory; nothing speaks the game server’s own protocol.
        </span>
      </div>
      <TextField full mono label="Server address" value={d.target} onChange={target => set({ target })} error={errors.steamTarget} placeholder="game.example.net:27015" disabled={disabled}
        hint={<>host:port, or [IPv6]:port like <span className="mono">[2001:db8::10]:27015</span>. Use the port Steam’s server list shows for this server; for some games that is a query port rather than the game port. Host names are ASCII (punycode for other scripts). Changing it starts a new check.</>} />
      <SecretField full label="Steam Web API key" value={d.apiKey} onChange={apiKey => set({ apiKey })} error={errors.steamKey} disabled={disabled}
        hint="Required, from steamcommunity.com/dev/apikey. Stored on the nodes and sent only to the API. Never shown to viewers, in lists, in conflict views or in the JSON preview." />
      {keyChanged ? (
        <p className="full small fwarn steam-key-new">
          <Glyph tone="warn" />Saving a different key starts a new check generation: an open incident closes without a recovery alert.{' '}
          <button type="button" className="linkbtn" disabled={disabled} onClick={() => set({ apiKey: storedKey })}>Keep the current key</button>
        </p>
      ) : null}

      <Seg legend="If the Steam API fails" name={`${id}-fail`} value={d.failureDown ? 'down' : 'pending'} onChange={value => set({ failureDown: value === 'down' })} disabled={disabled} full
        options={[{ value: 'pending', label: 'Pending (recommended)' }, { value: 'down', label: 'Down' }]}
        hint={d.failureDown
          ? <span className="warn-text">Down, as in Uptime Kuma: an unreachable or timed-out API, an error status (a rate limit or a refused key included), a redirect or an unreadable answer opens incidents and alerts as if the server were gone, though the fault may be Steam’s, the key’s or a proxy’s. A check the node cancels still stays pending.</span>
          : 'An unreachable or timed-out API, an error status (a rate limit or a refused key included), a redirect or an unreadable answer is pending, because it says nothing about your server. Pending never recovers an outage. A missing listing and a host name that doesn’t resolve are always down.'} />

      <CheckField label="Measure ICMP latency after a positive listing" checked={d.measure} onChange={measure => set({ measure })} disabled={disabled}
        hint="One echo request to the resolved address, sent only once Steam lists the server, allowed at most 1 s and a quarter of the time left. Its round trip is the only latency shown: a lost reply, a firewall or a node without ICMP permission shows a dash and never changes the listed result. The Steam API’s response time is never shown as latency." />
      <TextField label="ICMP payload size (optional)" value={d.size} onChange={size => set({ size })} error={errors.steamSize} placeholder={String(STEAM_PACKET_DEFAULT)} inputMode="numeric" disabled={disabled}
        hint={<>0 to 65,500 bytes. Empty: {STEAM_PACKET_DEFAULT}. 0 sends an echo without payload.{d.measure ? '' : ' Not used while latency is skipped; kept as set.'}</>} />

      <Disclosure title="Steam API endpoint" summary={apiSummary} open={Boolean(open.steamApi)} onToggle={() => onToggle('steamApi')}>
        <TextField full mono label="API address (optional)" value={d.apiUrl} onChange={apiUrl => set({ apiUrl })} error={errors.steamApi} placeholder={STEAM_DEFAULT_API} disabled={disabled}
          hint={<>Empty uses Steam’s API. For a proxy or compatible endpoint you trust, give its https:// base with an optional path prefix, without credentials, query or fragment; the node adds <span className="mono">{STEAM_ENDPOINT}</span>. Redirects aren’t followed.</>} />
        <CheckField label="Allow plain HTTP for the API" checked={d.insecure} onChange={insecure => set({ insecure })} disabled={disabled} danger
          hint="Only for a local test server or a proxy on a network you control. The key and the server address then travel unencrypted. HTTPS certificates are always verified; there is no option to skip verification." />
        <PemField label="Extra CA certificates for the API" kind="certificate" value={d.ca} onChange={ca => set({ ca })} error={errors.steamCa} disabled={disabled}
          hint="Added to the system roots for the HTTPS connection to the API only, up to 128 KiB. Nothing connects to the game server over TLS." />
        {plainHttp && d.ca.trim() ? <p className="full small muted"><Icon name="info" className="inline" />These certificates aren’t used while the API address is plain http://.</p> : null}
      </Disclosure>

      <div className="full steam-timing" role="note">
        <Icon name="clock" className="inline" />
        <span>
          The timeout{timeoutMs !== null ? ` (${span(timeoutMs)})` : ''} covers resolving the host, the API request and the optional echo.
          {perDay !== null ? <> Every selected node calls the API on each check: about <b>{perDay.toLocaleString()} calls a day</b> from {plural(nodes, 'node')}, more while failing checks retry faster.</> : null}
          {perDay !== null && perDay > STEAM_DAILY_CALLS ? <> <span className="warn-text">Steam’s Web API terms allow {STEAM_DAILY_CALLS.toLocaleString()} calls a day per key. Calls Steam refuses make checks {d.failureDown ? 'down' : 'pending'}.</span></> : null}
        </span>
      </div>
    </>
  );
}
