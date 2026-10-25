import { useId, useState } from 'react';
import {
  GAMEDIG_GAMES, GAMEDIG_PARALLEL, GAMEDIG_PENDING, GAMEDIG_VERSION, gamedigEndpointScheme, gamedigFilter, gamedigGame, gamedigNeedsPort, type GameDigDraft, type GameDigField,
} from '../gamedig';
import { span } from '../format';
import { ErrorText } from '../ui/bits';
import { CheckField, SecretField, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

/**
 * The GameDig section of the monitor form. What runs where comes first: Canary includes no GameDig and starts no service,
 * so the operator runs the separate query service that every chosen node reaches. Then the game (picked from the pinned
 * catalogue, never typed freely), the server address, port and private game token, and the service's private address and
 * token (masked inputs). The form offers no other GameDig option, script or module. `storedGame` is the stored game ID,
 * listed even when this dashboard's catalogue doesn't know it; `storedToken` says the stored monitor has a game token, for
 * the note that saving an emptied field removes it.
 */
export function GameDigFields({ d, set, errors, disabled, storedGame, storedToken, timeoutMs }: {
  d: GameDigDraft; set: (change: Partial<GameDigDraft>) => void; errors: Partial<Record<GameDigField, string>>; disabled: boolean;
  storedGame: string | undefined; storedToken: boolean; timeoutMs: number | null;
}) {
  const id = useId();
  const [query, setQuery] = useState('');
  const game = gamedigGame(d.game);
  const needsPort = gamedigNeedsPort(d.game);
  const shown = gamedigFilter(query);
  // The chosen game stays in the list while a search hides it, so the select never shows a value it can't name.
  const options = game && !shown.includes(game) ? [game, ...shown] : shown;
  const unknown = d.game && !game ? d.game : storedGame && !gamedigGame(storedGame) && storedGame !== d.game ? storedGame : '';
  const scheme = gamedigEndpointScheme(d.endpoint.trim());
  const describedBy = [`${id}-game-h`, errors.gamedigGame ? `${id}-game-e` : ''].filter(Boolean).join(' ');
  return (
    <>
      <div className="full svc-note gamedig-note" role="note">
        <Icon name="nodes" className="inline" />
        <span>
          <b>Each check asks your GameDig query service to query the game server.</b> Every selected node sends the service one request with the game, address, port and game token below, and reads back only up, down or pending. GameDig {GAMEDIG_VERSION}’s own code for the chosen game runs in the service, which contacts the game server from its own network, not the node’s. Up is the game server’s reply, with the round trip GameDig measured; down is GameDig reporting that its query failed; anything else is pending ({GAMEDIG_PENDING}). Pending is never inverted and never recovers an outage. No other GameDig options, scripts or modules can be set.
        </span>
      </div>
      <div className="full svc-note gamedig-note" role="note">
        <Icon name="wrench" className="inline" />
        <span>
          <b>Canary doesn’t include GameDig.</b> The node and dashboard images contain no JavaScript runtime or game query code, and nothing starts the separate game query service for you: build and run it yourself (<span className="mono">Dockerfile.game-query</span> in the Canary source) where every selected node can reach it. It answers {GAMEDIG_PARALLEL} queries at a time and refuses more rather than queueing them. Run it as a dedicated, trusted service: it receives the server addresses and game tokens, and anyone holding its token can make it send game queries to any address its network reaches.
        </span>
      </div>
      <div className="fld">
        <label htmlFor={`${id}-find`}>Find a game <span className="muted">(optional)</span></label>
        <input id={`${id}-find`} type="search" value={query} onChange={event => setQuery(event.target.value)} disabled={disabled} placeholder="minecraft, Terraria…"
          autoComplete="off" spellCheck={false} aria-describedby={`${id}-find-h`} />
        <small id={`${id}-find-h`}>Narrows the list by name or ID. {query.trim() ? `${shown.length} of ${GAMEDIG_GAMES.length} games match.` : `${GAMEDIG_GAMES.length} games.`}</small>
      </div>
      <div className="fld">
        <label htmlFor={`${id}-game`}>Game</label>
        <select id={`${id}-game`} value={d.game} onChange={event => set({ game: event.target.value })} disabled={disabled} aria-invalid={errors.gamedigGame ? true : undefined} aria-describedby={describedBy}>
          {!d.game ? <option value="">Choose a game…</option> : null}
          {unknown ? <option value={unknown}>{`${unknown} (not in this dashboard’s catalogue)`}</option> : null}
          {options.map(entry => <option key={entry.id} value={entry.id}>{`${entry.name} (${entry.id})`}</option>)}
        </select>
        <small id={`${id}-game-h`}>
          GameDig {GAMEDIG_VERSION}’s catalogue of {GAMEDIG_GAMES.length} game IDs. Each uses GameDig’s own protocol code for that game; Canary hasn’t tested every game in this list.
          {game ? (game.defaultPort ? ` Default port: ${game.defaultPort}.` : null) : d.game && d.game === storedGame ? ' Kept as stored: the node decides whether it knows this game. Changing the game starts a new check.' : null}
        </small>
        {needsPort ? <small className="fwarn"><Glyph tone="warn" />GameDig’s catalogue lists no default port for this game, so the node needs its port: enter it below.</small> : null}
        <ErrorText id={`${id}-game-e`}>{errors.gamedigGame}</ErrorText>
      </div>
      <TextField full mono label="Game server address" value={d.target} onChange={target => set({ target })} error={errors.gamedigTarget} disabled={disabled} placeholder="play.example.com or 203.0.113.10"
        hint="The server’s host name or IP address, as the query service resolves it. One name or address only: no URL, port, brackets, zone or spaces; the port goes below." />
      <TextField label={needsPort ? 'Game port' : 'Game port (optional)'} value={d.port} onChange={port => set({ port })} error={errors.gamedigPort} disabled={disabled} inputMode="numeric" placeholder={game?.defaultPort ? String(game.defaultPort) : undefined}
        hint={needsPort ? '1 to 65,535. Required for this game: it has no default port in GameDig’s catalogue.' : <>1 to 65,535. Empty: the game’s default port{game?.defaultPort ? ` (${game.defaultPort})` : ''}.</>} />
      <CheckField full={false} label="Only query this port" checked={d.givenPortOnly} onChange={givenPortOnly => set({ givenPortOnly })} disabled={disabled}
        hint="Without it, GameDig may also try other ports it knows for this game. Needs a port." />
      <SecretField full optional label="Game token" value={d.token} onChange={token => set({ token })} error={errors.gamedigToken} disabled={disabled} storedSet={storedToken}
        hint={<>Only for games whose query needs a credential, such as a Terraria TShock REST token. Sent exactly as typed to the query service with each query, which passes it to the game server. Up to 1,024 bytes, no control characters. Private: hidden from viewers and masked in previews until you show credentials. Changing it starts a new check.</>} />
      <SecretField full label="Query service address" value={d.endpoint} onChange={endpoint => set({ endpoint })} error={errors.gamedigEndpoint} disabled={disabled} placeholder="https://game-query.internal:3121"
        hint={<>The service’s origin: <span className="mono">https://</span> with a certificate the node’s system trusts, or <span className="mono">http://</span> on a private network with plain HTTP allowed below. No path, query, user:password@ or #fragment; at most 2,048 bytes. The node posts to <span className="mono">/v1/query</span> there itself and follows no redirects. Private: hidden from viewers and masked in previews, conflict views and details until you show credentials. Changing it starts a new check.</>} />
      <SecretField full label="Query service token" value={d.bearer} onChange={bearer => set({ bearer })} error={errors.gamedigBearer} disabled={disabled}
        hint={<>The service’s own token, its <span className="mono">CANARY_GAME_QUERY_TOKEN</span>, sent as <span className="mono">Authorization: Bearer</span> to the service only, never to the game server. 24 to 256 printable ASCII characters without spaces. Not a node or cluster token. Private, like the address.</>} />
      <CheckField label="Allow plain HTTP to the query service" checked={d.insecure} onChange={insecure => set({ insecure })} disabled={disabled} danger
        hint={`Needed for an http:// address, and only there. The service token, the game token and the server address then travel unencrypted, so use it only on a private network you control.${scheme === 'https' && d.insecure ? ' This address is https://, so it doesn’t need it.' : ''}`} />
      {scheme === 'http' && d.insecure ? (
        <p className="full small fwarn"><Glyph tone="warn" />This address isn’t encrypted (http://), so both tokens and the server address travel in clear. Across a network you don’t fully control, use https://.</p>
      ) : null}
      <p className="full muted small"><Icon name="clock" className="inline" />The node gives the service three quarters of the timeout{timeoutMs ? ` (${span(Math.floor(timeoutMs * 3 / 4))})` : ''} for the query; the rest covers the request and its answer. If the whole timeout passes first, the result is pending. Each node runs at most {GAMEDIG_PARALLEL} game queries at once, and another one at that moment is pending, never queued. Latency is the round trip GameDig reports for the reply, measured by the service from its network.</p>
    </>
  );
}
