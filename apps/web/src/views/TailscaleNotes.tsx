import { TAILSCALE_LINUX_PATHS, TAILSCALE_PENDING, TAILSCALE_WINDOWS_PATH } from '../tailscale';
import { Icon } from '../ui/icons';

/**
 * What a Tailscale check asks, where, and what each node needs, beside its one setting, the peer. Each node uses the
 * client installed on its own machine, so the note names that prerequisite rather than suggesting mounts, paths or
 * privileges: the form has no path, command, socket or credential setting.
 */
export function TailscaleNotes() {
  return (
    <>
      <div className="full svc-note ts-note" role="note">
        <Icon name="nodes" className="inline" />
        <span>
          <b>Each selected node asks its own Tailscale client, never another machine’s.</b> It runs <span className="mono">tailscale ping</span> once: Tailscale’s peer ping over the tailnet, not an ICMP ping, with no settings or credentials of its own. It changes nothing, starts no daemon and never falls back to ICMP or another check. Up is exactly one pong, direct or through a DERP or peer relay; down is the client confirming that its one ping timed out with no reply. Anything else is pending ({TAILSCALE_PENDING}). Pending is never inverted and never recovers an outage.
        </span>
      </div>
      <div className="full svc-note ts-note" role="note">
        <Icon name="wrench" className="inline" />
        <span>
          <b>Each node needs the Tailscale client and a running, signed-in daemon.</b> Linux nodes run {TAILSCALE_LINUX_PATHS.map((path, index) => <span key={path}>{index ? ' or ' : ''}<span className="mono">{path}</span></span>)}; Windows nodes run <span className="mono">{TAILSCALE_WINDOWS_PATH}</span>. No other path is used, and none can be set here. Other operating systems stay pending. The ordinary Alpine node image has neither the client nor the daemon, and a container gets no access to the host’s Tailscale by itself: whoever runs the node has to provide the client, a daemon and the permission to use it.
        </span>
      </div>
      <p className="full muted small"><Icon name="clock" className="inline" />The client gets three quarters of the timeout for its one ping; the rest covers starting it and reading its answer. If the whole timeout passes first, the result is pending. Latency is the round trip the client reports, only for a pong.</p>
    </>
  );
}
