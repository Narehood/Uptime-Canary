/** Literal public metadata from the pinned GameDig catalogue; no runtime engine. */
import catalogue from '../../../apps/node-go/internal/gamedigmonitor/catalogue.json' with { type: 'json' };

export interface GameDigGame { id: string; name: string; defaultPort?: number }
export const GAMEDIG_VERSION: string = catalogue.version;
export const GAMEDIG_GAMES: readonly GameDigGame[] = catalogue.games;
