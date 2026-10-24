import { useId } from 'react';
import { PM2_SOCKET, PM2_SOCKET_MAX, PM2_VERSION, pm2HomeProblem, pm2KnownTransport, pm2SocketPath, type Pm2Draft, type Pm2Field } from '../pm2';
import { ErrorText } from '../ui/bits';
import { Seg, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

/**
 * The PM2 section of the monitor form. Where the check runs and what it may touch come first, then the exact process
 * name, the daemon connection and an optional namespace. Each node reads its own daemon, so the note names the
 * deployment prerequisite (access to the daemon's socket) and the authority it carries, and the form offers no
 * privilege, user, shell or command settings. `stored` is the connection of the monitor as stored, if it is a PM2 one.
 */
export function Pm2Fields({ d, set, errors, disabled, stored }: {
  d: Pm2Draft; set: (change: Partial<Pm2Draft>) => void; errors: Partial<Record<Pm2Field, string>>; disabled: boolean; stored?: string;
}) {
  const id = useId();
  const socket = d.transport === 'unix' && !pm2HomeProblem(d.home) ? pm2SocketPath(d.home) : '';
  return (
    <>
      <div className="full pm2-note" role="note">
        <Icon name="nodes" className="inline" />
        <span>
          <b>Each selected node asks the PM2 daemon on its own machine, never another host’s.</b> It connects to a daemon that is already running, as the account Canary runs under, and only reads the process list: it never starts a daemon and never starts, stops or restarts a process. Only PM2 {PM2_VERSION} is read; a daemon reporting another version stays pending. Giving Canary’s account access to the daemon’s socket is a deployment step, and that access lets anything running as that account control PM2 fully, even through a read-only mount. A node in a container gets no access to the host’s PM2 automatically: it needs explicitly provisioned access to the socket of the existing daemon you mean, and the same account rule applies to the daemon’s user ID as mapped into the container.
        </span>
      </div>
      <TextField full mono label="Process name" value={d.target} onChange={target => set({ target })} error={errors.pm2Target} placeholder="api" disabled={disabled}
        hint={<>Exactly as <span className="mono">pm2 list</span> shows it, case, internal spaces and punctuation included. A number is a name too: <span className="mono">3</span> means a process named “3”, never PM2 id 3 or a PID. Every process with this name counts, cluster instances included. Changing it starts a new check.</>} />
      <Seg legend="Daemon connection" name={`${id}-transport`} value={d.transport} onChange={transport => set({ transport })} disabled={disabled} full
        options={[{ value: 'unix', label: 'Unix socket (Linux)' }, { value: 'windows-pipe', label: 'Windows named pipe' }]}
        hint={d.transport === 'windows-pipe'
          ? 'PM2 on Windows uses one fixed local pipe whatever its home, so there’s no path to enter. The daemon must already be running as Canary’s own account. From the pipe’s first creation, only Canary’s account, SYSTEM and Administrators may have been able to create instances of it or change its security; this is about who could serve or reconfigure the pipe, not which clients connect to it. The node stays pending when the daemon’s account or the pipe’s present owner and permissions don’t fit, but it can’t see their history, so this is a deployment step: tightening a pipe that was once more open isn’t enough. Linux nodes need a separate monitor with the Unix socket.'
          : 'Each node opens the socket in the PM2 home below and asks only a daemon whose effective user ID is Canary’s own: a daemon run by another account stays pending even when its socket is reachable. Windows nodes need a separate monitor with the named pipe.'} />
      {pm2KnownTransport(d.transport) ? null : d.transport === stored ? (
        <p className="full small fwarn">
          <Glyph tone="warn" />This monitor is stored with the connection “{d.transport}”, which this dashboard doesn’t know. It’s kept as stored, with the settings that came with it; choosing one above replaces it and drops those settings.
        </p>
      ) : (
        <p className="full small fwarn">
          <Glyph tone="warn" />The connection “{d.transport}” isn’t one this dashboard knows, and this monitor isn’t stored with it, so it can’t be saved. Choose one above.
        </p>
      )}
      {errors.pm2Transport ? <div className="full"><ErrorText id={`${id}-transport-e`}>{errors.pm2Transport}</ErrorText></div> : null}
      {d.transport === 'unix' ? (
        <TextField full mono label="PM2 home" value={d.home} onChange={home => set({ home })} error={errors.pm2Home} placeholder="/home/deploy/.pm2" disabled={disabled}
          hint={<>The PM2_HOME of the running daemon as one absolute path: by default <span className="mono">.pm2</span> in the home directory of the user who started PM2; for a node in a container, the path where that home is mounted inside the container. Written literally, without <span className="mono">~</span> or <span className="mono">$VARIABLES</span>. Every chosen node uses this same path{socket ? <> and opens <span className="mono wrap">{socket}</span></> : <> and opens {PM2_SOCKET} inside it (at most {PM2_SOCKET_MAX} bytes in all)</>}.</>} />
      ) : null}
      <TextField full mono label="Namespace (optional)" value={d.namespace} onChange={namespace => set({ namespace })} error={errors.pm2Namespace} disabled={disabled}
        hint="Empty matches the name in every namespace. Otherwise only processes in exactly this namespace count, case-sensitive." />
      <p className="full muted small"><Icon name="clock" className="inline" />Latency isn’t measured and shows as a dash. Choose a timeout that covers how long each node’s PM2 can take to answer; no answer in time stays pending. A restart passes through stopping and launching, which count as down; retries before down ride that out.</p>
    </>
  );
}
