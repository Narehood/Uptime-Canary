import { useCanary } from '../context';
import { ago, plural } from '../format';
import { gatewayMode } from '../gateway';
import { outOfView } from '../model';
import { Card, PageHead, Time, nodeStateText, nodeTone } from '../ui/bits';
import { EndpointMark, Glyph, Icon } from '../ui/icons';

export function NodesView() {
  const { nodes, endpoint, overview, api, openSwitch, assessed } = useCanary();
  const peers = nodes.filter(node => node.id !== endpoint && node.state !== 'unlisted');
  const inView = peers.filter(node => node.state === 'connected').length;
  // Gateway mode: the browser reaches only the dashboard's server, which forwards to its one node. Peers' addresses
  // (the ones nodes use to reach each other) are shown as text from the overview and are never contacted from here.
  const gateway = gatewayMode();
  const peerAddress = new Map(overview.nodes.map(node => [node.id, node.url]));
  return (
    <div className="view">
      <PageHead title="Nodes" sub={<>Every node as seen from <span className="mono">{endpoint}</span>. Each node runs its own checks, history, incidents and alerts.</>} />
      <Card title="How this dashboard is connected">
        <div className="path" role="img" aria-label={gateway
          ? `Your browser talks only to this dashboard’s server, which forwards to ${endpoint}’s management API. ${endpoint} reaches ${inView} of ${plural(peers.length, 'peer')}.`
          : `Your browser talks to ${endpoint}'s management API, which reaches ${inView} of ${plural(peers.length, 'peer')}.`}>
          <span className="box"><Icon name="overview" />This browser tab</span>
          <span className="arr" aria-hidden="true">→</span>
          {gateway ? <><span className="box"><Icon name="globe" /><span><b>Dashboard server</b><span className="mono sub">{api.baseUrl}</span></span></span><span className="arr" aria-hidden="true">→</span></> : null}
          <span className="box ep"><EndpointMark /><span><b className="mono">{endpoint}</b>{gateway ? <span className="sub">address set on the server</span> : <span className="mono sub">{api.baseUrl}</span>}</span></span>
          <span className="arr" aria-hidden="true">⇄</span>
          <span className="box"><Icon name="nodes" />{peers.length ? `${inView} of ${plural(peers.length, 'peer')} in view` : 'No peers configured'}</span>
        </div>
        <p className="foot-note">
          {gateway
            ? <>Your browser reaches only this dashboard’s address. Its server forwards each request to {endpoint}, the one node it is set up with, so no node needs a route from your browser. </>
            : null}
          The dashboard is optional and reads one node at a time. Closing it never affects monitoring or alerts.
          Reachability below is only {endpoint}’s view; other nodes may see their peers differently.
        </p>
      </Card>
      <div className="nodes-grid">
        {nodes.map(node => {
          const assigned = assessed.filter(({ view }) => view.monitor.nodeIds.includes(node.id));
          const fresh = assigned.filter(({ view }) => view.observations.some(o => o.nodeId === node.id && o.state !== 'unknown')).length;
          const isEndpoint = node.id === endpoint;
          return (
            <article key={node.id} className={`ncard${isEndpoint ? ' ep' : ''}${outOfView(node) ? ' out' : ''}`} aria-labelledby={`n-${node.id}`}>
              <div className="nc-h">
                <div>
                  <b id={`n-${node.id}`}>{node.id}</b>
                  <span>{node.name !== node.id ? node.name : ' '}</span>
                </div>
                <Glyph tone={nodeTone(node)} label={node.state === 'unreachable' ? 'out of view' : node.state} />
              </div>
              <p className="nc-state">{nodeStateText(node, endpoint, overview.now)}</p>
              <dl className="kv">
                <dt>Monitors assigned</dt><dd>{assigned.length}</dd>
                <dt>Fresh results</dt><dd>{assigned.length ? `${fresh} of ${assigned.length}` : '—'}</dd>
                {!isEndpoint ? <><dt>Last sync</dt><dd>{node.lastSeenAt ? <><Time at={node.lastSeenAt} now={overview.now} /> <span className="muted">· {ago(node.lastSeenAt, overview.now)}</span></> : 'Never'}</dd></> : null}
              </dl>
              {gateway
                ? <p className="nc-addr"><span className="muted">{isEndpoint ? 'Reached through' : 'Peer address'}</span> <span className="mono wrap">{isEndpoint ? api.baseUrl : peerAddress.get(node.id) ?? 'not configured'}</span></p>
                : <p className="nc-addr"><span className="muted">Address</span> <span className="mono wrap">{isEndpoint ? api.baseUrl : node.url ?? 'not configured'}</span></p>}
              {isEndpoint
                ? <span className="tag"><Icon name="plug" />{gateway ? 'Dashboard’s node' : 'Current endpoint'}</span>
                : !gateway && node.url ? <button type="button" className="btn sm" onClick={() => openSwitch({ url: node.url })}><Icon name="plug" />Use as endpoint</button> : null}
            </article>
          );
        })}
      </div>
      <Card title="Accounts across nodes">
        {gateway ? (
          <p className="prose">
            Each node keeps its own accounts, and accounts aren’t replicated. This dashboard signs you in with an account on {endpoint}, the node behind it, and {endpoint} shows its peers without an account on each of them.
            Passwords and sessions stay local to each node.
          </p>
        ) : (
          <p className="prose">
            Each node keeps its own accounts, and accounts aren’t replicated. Switching endpoints needs an account on that node: sign in there with its password or account token.
            To reuse one account token everywhere, create the same account, with the same token, on each node. Passwords and sessions stay local to each node.
          </p>
        )}
      </Card>
    </div>
  );
}
