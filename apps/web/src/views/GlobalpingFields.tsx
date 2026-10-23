import { useId } from 'react';
import { GP_DEFAULT_API, GP_DNS_TYPES, GP_MODES, type GpDraft, type GpField, type GpMode } from '../globalping';
import { plural, span } from '../format';
import { CheckField, Disclosure, HeaderEditor, PemField, SecretField, Seg, TextField } from '../ui/forms';
import { ErrorText } from '../ui/bits';
import { Glyph, Icon } from '../ui/icons';
import { JsonataInputs } from './ServiceFields';

const TARGET: Record<GpMode, { label: string; placeholder: string; hint: string }> = {
  http: { label: 'URL', placeholder: 'https://www.example.com/health', hint: 'An absolute http:// or https:// URL without credentials or #fragment. The probe requests it; path and query are sent to Globalping too.' },
  ping: { label: 'Host or IP address', placeholder: 'example.com or 192.0.2.10', hint: 'A public host name (ASCII; punycode for other scripts) or IP address the probe can reach.' },
  dns: { label: 'Name to query', placeholder: 'example.com', hint: 'A domain name (underscores allowed, as in _sip._tcp.example.com), or an IP address for PTR.' },
};

/**
 * The Globalping section of the monitor form. Essentials first (mode, target, location), then provider access, request,
 * response and DNS options in disclosures. Timing and quota guidance uses the form's own interval, timeout and nodes.
 */
export function GlobalpingFields({ d, set, errors, disabled, open, onToggle, storedToken, timeoutMs, intervalMs, nodeCount, onUseTimeout }: {
  d: GpDraft; set: (change: Partial<GpDraft>) => void; errors: Partial<Record<GpField, string>>; disabled: boolean;
  open: Record<string, boolean>; onToggle: (section: string) => void; storedToken: boolean;
  timeoutMs: number | null; intervalMs: number | null; nodeCount: number; onUseTimeout: (ms: number) => void;
}) {
  const id = useId();
  const target = d.targets[d.mode];
  const https = /^https:\/\//iu.test(target.trim());
  const probe = Number(d.probeTimeout);
  const probeSet = Number.isInteger(probe) && probe >= 5 && probe <= 30;
  const recommended = probeSet ? (probe + 10) * 1000 : null;
  const perHour = intervalMs ? Math.round((3_600_000 / intervalMs) * Math.max(nodeCount, 1)) : null;
  const headerCount = d.headers.filter(row => row.name.trim()).length;
  const apiSummary = `${d.apiUrl.trim() || 'api.globalping.io'} · ${d.token ? 'token set' : 'no token'}${d.ca.trim() ? ' · custom CA' : ''}${d.insecure ? ' · plain HTTP allowed' : ''}${d.failureDown ? ' · API failures are down' : ''}`;
  const responseSummary = [d.statuses ? `status ${d.statuses}` : 'any 2xx', d.keyword ? `${d.invert ? 'not ' : ''}“${d.keyword.slice(0, 24)}”` : '', d.jsonata.expression ? 'JSONata' : ''].filter(Boolean).join(' · ');
  const headSkipsBody = d.mode === 'http' && d.httpMethod === 'HEAD' && Boolean(d.keyword || d.jsonata.expression);
  return (
    <>
      <div className="full gp-note" role="note">
        <Icon name="globe" className="inline" />
        <span>
          <b>A third-party probe measures this target.</b> Each check asks Globalping to run one {d.mode === 'http' ? 'HTTP request' : d.mode === 'dns' ? 'DNS query' : 'ping'} from a probe matching your location, then reads its result. The target, location{d.mode === 'http' ? ', path, query and request headers (including any Authorization header)' : ''} are sent to Globalping and that probe, which may keep or publish measurement data under their own policies. The API token and CA certificates stay on your nodes.
        </span>
      </div>
      <Seg legend="Measurement" name={`${id}-mode`} value={d.mode} onChange={mode => set({ mode })} disabled={disabled} full
        options={GP_MODES.map(mode => ({ value: mode.value, label: mode.label }))} hint={GP_MODES.find(mode => mode.value === d.mode)!.hint} />
      <TextField full mono label={TARGET[d.mode].label} value={target} onChange={value => set({ targets: { ...d.targets, [d.mode]: value } })} error={errors.gpTarget}
        type={d.mode === 'http' ? 'url' : 'text'} inputMode={d.mode === 'http' ? 'url' : 'text'} placeholder={TARGET[d.mode].placeholder} hint={TARGET[d.mode].hint} disabled={disabled} />
      <TextField full label="Probe location" value={d.location} onChange={location => set({ location })} error={errors.gpLocation} placeholder="Germany" maxLength={256} disabled={disabled}
        hint={<>Globalping’s free-form “magic” location: a continent, country, city, network (<span className="mono">AS13335</span>), cloud region (<span className="mono">aws-eu-west-1</span>) or combinations like <span className="mono">Europe+Cloudflare</span>. One probe is picked per check; the dashboard doesn’t verify where it is.</>} />

      {d.mode === 'ping' ? (
        <>
          <TextField label="Packets (optional)" value={d.pingPackets} onChange={pingPackets => set({ pingPackets })} error={errors.gpPing} placeholder="3" inputMode="numeric" disabled={disabled}
            hint="1 to 16. One reply is enough for up; complete loss is down." />
          <Seg legend="Ping protocol" name={`${id}-pp`} value={d.pingProtocol === 'TCP' ? 'TCP' : 'ICMP'} onChange={pingProtocol => set({ pingProtocol })} disabled={disabled}
            options={[{ value: 'ICMP', label: 'ICMP' }, { value: 'TCP', label: 'TCP' }]} />
          {d.pingProtocol === 'TCP' ? <TextField label="TCP port (optional)" value={d.pingPort} onChange={pingPort => set({ pingPort })} placeholder="80" inputMode="numeric" disabled={disabled} hint="Empty: 80." /> : null}
        </>
      ) : null}

      {d.mode === 'http' ? (
        <>
          <Seg legend="Method" name={`${id}-method`} value={['GET', 'HEAD', 'OPTIONS'].includes(d.httpMethod) ? d.httpMethod : 'GET'} onChange={httpMethod => set({ httpMethod })} disabled={disabled}
            options={[{ value: 'GET', label: 'GET' }, { value: 'HEAD', label: 'HEAD' }, { value: 'OPTIONS', label: 'OPTIONS' }]} hint="Globalping sends no request body." />
          <div className="fld">
            <CheckField full={false} label="HTTP/2" checked={d.http2} onChange={http2 => set({ http2 })} disabled={disabled} hint="Needs an https:// URL. Off: HTTP/1.1 or HTTPS from the URL." />
            {https || d.ignoreTls ? <CheckField full={false} label="Skip TLS verification" checked={d.ignoreTls} onChange={ignoreTls => set({ ignoreTls })} disabled={disabled} danger
              hint="The probe doesn’t verify the certificate, so an impostor could pass. Status and body conditions still apply." /> : null}
          </div>
          {headSkipsBody ? <p className="full small fwarn"><Glyph tone="warn" />HEAD responses have no body, so the keyword or JSONata condition below can never pass: the check will be down.</p> : null}
          <Disclosure title="Request headers" summary={headerCount ? plural(headerCount, 'header') : 'None'} open={Boolean(open.gpRequest)} onToggle={() => onToggle('gpRequest')}>
            <HeaderEditor legend="Headers" rows={d.headers} onChange={headers => set({ headers })} error={errors.gpHeaders} disabled={disabled}
              hint={<>Up to 32, at most 32 KiB in total. Host and User-Agent are the probe’s. An <span className="mono">Authorization</span> header (Basic or Bearer) is the only way to authenticate; it is sent to Globalping and the probe in the measurement.</>} />
          </Disclosure>
          <Disclosure title="Response conditions" summary={responseSummary} open={Boolean(open.gpResponse)} onToggle={() => onToggle('gpResponse')}>
            <TextField mono label="Accepted status" value={d.statuses} onChange={statuses => set({ statuses })} error={errors.gpStatuses} placeholder="200-299" disabled={disabled}
              hint="Codes or ranges, like 200-299, 301. Up to 32. Empty: any 2xx. HTTPS also needs a valid certificate unless verification is skipped." />
            <TextField full mono label="Keyword (optional)" value={d.keyword} onChange={keyword => set({ keyword })} error={errors.gpKeyword} disabled={disabled}
              hint="Literal, case-sensitive text in the body. Globalping returns only the first 10 kB; a truncated or missing body can’t satisfy a keyword or JSONata condition, so the check is down." />
            {d.keyword || d.invert ? <CheckField label="Fail when found" checked={d.invert} onChange={invert => set({ invert })} disabled={disabled} hint="Down when the body contains the keyword." /> : null}
            <JsonataInputs label="JSONata condition" value={d.jsonata} onChange={jsonata => set({ jsonata })} input="globalping" error={errors.gpJsonata} disabled={disabled}
              hint="Must pass together with the status and keyword." />
          </Disclosure>
        </>
      ) : null}

      {d.mode === 'dns' ? (
        <>
          <div className="fld">
            <label htmlFor={`${id}-qt`}>Record type</label>
            <select id={`${id}-qt`} value={(GP_DNS_TYPES as readonly string[]).includes(d.dnsType) ? d.dnsType : 'A'} onChange={event => set({ dnsType: event.target.value })} disabled={disabled}>
              {GP_DNS_TYPES.map(type => <option key={type} value={type}>{type}</option>)}
            </select>
          </div>
          <Seg legend="DNS transport" name={`${id}-dp`} value={d.dnsProtocol === 'TCP' ? 'TCP' : 'UDP'} onChange={dnsProtocol => set({ dnsProtocol })} disabled={disabled}
            options={[{ value: 'UDP', label: 'UDP' }, { value: 'TCP', label: 'TCP' }]} />
          <TextField mono label="Resolver (optional)" value={d.resolver} onChange={resolver => set({ resolver })} error={errors.gpDns} placeholder="1.1.1.1" disabled={disabled}
            hint="A host name or IP address, without a port. Empty: the probe’s own resolver." />
          <TextField label="Port (optional)" value={d.dnsPort} onChange={dnsPort => set({ dnsPort })} placeholder="53" inputMode="numeric" disabled={disabled} hint="Empty: 53." />
          <TextField full mono label="Answer pattern (optional)" value={d.regex} onChange={regex => set({ regex })} error={errors.gpRegex} placeholder="^192\.0\.2\." disabled={disabled}
            hint="RE2 syntax, matched case-insensitively against each answer’s value on its own (not owner, type or TTL). At least one answer must match." />
          <CheckField label="Accept an empty answer" checked={d.allowEmpty} onChange={allowEmpty => set({ allowEmpty })} disabled={disabled}
            hint="Off: NOERROR with no answers is down. Never overrides a DNS error or the answer pattern." />
        </>
      ) : null}

      <Disclosure title="Globalping API and failures" summary={apiSummary} open={Boolean(open.gpApi)} onToggle={() => onToggle('gpApi')}>
        <SecretField full label="API token" value={d.token} onChange={token => set({ token })} error={errors.gpToken} disabled={disabled} storedSet={storedToken} optional
          hint="A Globalping token raises your hourly limit. Sent only to the Globalping API, stored on nodes and hidden from viewers." />
        <TextField full mono label="API address (optional)" value={d.apiUrl} onChange={apiUrl => set({ apiUrl })} error={errors.gpApi} placeholder={GP_DEFAULT_API} disabled={disabled}
          hint="Empty uses the public API. For a self-hosted API or a reverse proxy, give its base address with any path prefix, without /v1/measurements." />
        <CheckField label="Allow plain HTTP for the API" checked={d.insecure} onChange={insecure => set({ insecure })} disabled={disabled} danger
          hint="Only for a self-hosted API or a local test server. The token then travels unencrypted. HTTPS certificates are always verified." />
        <PemField label="Extra CA certificates for the API" kind="certificate" value={d.ca} onChange={ca => set({ ca })} error={errors.gpCa} disabled={disabled}
          hint="Added to the system roots for the API connection only, up to 32 certificates. It doesn’t change how the probe checks your target." />
        <CheckField label="Treat Globalping failures as down" checked={d.failureDown} onChange={failureDown => set({ failureDown })} disabled={disabled}
          hint="Off (default): authentication, quota, API errors, offline probes and Globalping-internal failures are pending, because they say nothing about the target. On: they count as down, as in Uptime Kuma. Target and resolver failures are always down; a busy JSONata worker is always pending." />
      </Disclosure>

      <TextField label="Probe timeout (optional)" value={d.probeTimeout} onChange={probeTimeout => set({ probeTimeout })} error={errors.gpProbe} placeholder="Globalping’s default" inputMode="numeric" disabled={disabled}
        hint="5 to 30 seconds, how long the probe may take. The check’s timeout below must also cover creating and polling the measurement." />
      <Seg legend="IP version" name={`${id}-ip`} value={d.ipVersion} onChange={ipVersion => set({ ipVersion })} disabled={disabled}
        options={[{ value: '0', label: 'Either' }, { value: '4', label: 'IPv4' }, { value: '6', label: 'IPv6' }]} hint={errors.gpIp ? undefined : 'Only for host names; an IP address fixes the family.'} />
      {errors.gpIp ? <div className="fld"><ErrorText id={`${id}-ip-e`}>{errors.gpIp}</ErrorText></div> : null}

      <div className="full gp-timing" role="note">
        <Icon name="clock" className="inline" />
        <span>
          {recommended !== null && timeoutMs !== null && timeoutMs < recommended
            ? <><b>The timeout ({span(timeoutMs)}) is shorter than the {d.probeTimeout} s probe timeout plus 10 s for creating and polling.</b> Short timeouts may end checks as pending before the probe answers. <button type="button" className="linkbtn" onClick={() => onUseTimeout(recommended)} disabled={disabled}>Use {span(recommended)}</button></>
            : recommended !== null ? <>The timeout{timeoutMs !== null ? ` (${span(timeoutMs)})` : ''} covers creating the measurement, the {d.probeTimeout} s probe and polling (at most two polls a second). {span(recommended)} or more is recommended.</>
            : <>The check’s timeout{timeoutMs !== null ? ` (${span(timeoutMs)})` : ''} covers creating the measurement, the probe and polling (at most two polls a second). Set a probe timeout to get a recommendation: the probe timeout plus 10 s.</>}
          {' '}{perHour !== null ? <>Every assigned node creates its own measurement on each check: about <b>{perHour.toLocaleString()} measurements an hour</b> from {plural(Math.max(nodeCount, 1), 'node')}, each counting against your Globalping limit or credits, even when a check times out.</> : null}
          {intervalMs !== null && intervalMs < 60_000 ? <> <span className="warn-text">An interval under a minute uses a lot of quota.</span></> : null}
        </span>
      </div>
    </>
  );
}
