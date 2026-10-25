import { createHash, timingSafeEqual } from 'node:crypto';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { isIP } from 'node:net';
import { Worker } from 'node:worker_threads';
import catalogue from '../../apps/node-go/internal/gamedigmonitor/catalogue.json' with { type: 'json' };
import provenance from '../../apps/node-go/internal/gamedigmonitor/upstream.json' with { type: 'json' };

export const PROFILE = 'gamedig/5.3.3';
const games = new Map(catalogue.games.map(game => [game.id, game.defaultPort || 0]));
const allowed = new Set(['id', 'game', 'host', 'port', 'givenPortOnly', 'token', 'timeoutMs']);
function validHost(value) {
  if (typeof value !== 'string' || Buffer.byteLength(value) > 253) return false;
  if (!/^[\x00-\x7f]*$/u.test(value)) return false;
  if (isIP(value) === 4) { const first = Number(value.split('.')[0]); return value !== '0.0.0.0' && !(first >= 224 && first <= 239); }
  if (isIP(value) === 6) { const host = new URL(`http://[${value}]/`).hostname; return host !== '[::]' && !host.startsWith('[ff'); }
  return value.replace(/\.$/u, '').split('.').every(label => label.length >= 1 && label.length <= 63 && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/iu.test(label));
}

// Decode a small exact object and reject duplicate decoded keys, including
// escaped aliases. The subsequent shape check never consults a second parser's
// differing interpretation. Nested values are not part of this protocol.
export function decode(raw) {
  const text = new TextDecoder('utf-8', { fatal: true }).decode(raw);
  let i = 0;
  const ws = () => { while (i < text.length && /[\t\n\r ]/u.test(text[i])) i++; };
  const string = () => {
    const start = i++; let escaped = false;
    while (i < text.length) {
      const ch = text[i++];
      if (ch === '"' && !escaped) return JSON.parse(text.slice(start, i));
      if (ch === '\\' && !escaped) escaped = true; else escaped = false;
    }
    throw new Error('invalid query');
  };
  ws(); if (text[i++] !== '{') throw new Error('invalid query');
  const seen = new Set(); ws();
  if (text[i] !== '}') for (;;) {
    ws(); if (text[i] !== '"') throw new Error('invalid query');
    const key = string();
    if (!allowed.has(key) || seen.has(key)) throw new Error('invalid query');
    seen.add(key); ws(); if (text[i++] !== ':') throw new Error('invalid query'); ws();
    if (text[i] === '"') string();
    else { const start = i; while (i < text.length && !/[,}\s]/u.test(text[i])) i++; if (i === start) throw new Error('invalid query'); }
    ws(); if (text[i] === '}') break;
    if (text[i++] !== ',') throw new Error('invalid query');
  }
  if (text[i++] !== '}') throw new Error('invalid query'); ws(); if (i !== text.length) throw new Error('invalid query');
  const input = JSON.parse(text);
  const unicode = value => typeof value === 'string' && value.isWellFormed() && !/[\p{Cc}]/u.test(value);
  if (typeof input.id !== 'string' || !/^[a-f0-9]{32}$/u.test(input.id) || !games.has(input.game) || !validHost(input.host) ||
      !Number.isInteger(input.timeoutMs) || input.timeoutMs < 1 || input.timeoutMs > 90000 ||
      input.port !== undefined && (!Number.isInteger(input.port) || input.port < 0 || input.port > 65535) || !input.port && !games.get(input.game) ||
      input.givenPortOnly !== undefined && typeof input.givenPortOnly !== 'boolean' || input.givenPortOnly && !input.port ||
      input.token !== undefined && (!unicode(input.token) || Buffer.byteLength(input.token) > 1024)) throw new Error('invalid query');
  return input;
}

export function createQueryServer(token) {
  if (typeof token !== 'string' || token.length < 24 || token.length > 256 || /[^\x21-\x7e]/u.test(token)) throw new Error('query service credential required');
  const credential = Buffer.from('Bearer ' + token);
  const active = new Set();
  const server = createServer({ maxHeaderSize: 8192, requestTimeout: 5000, headersTimeout: 5000, keepAliveTimeout: 1000 }, async (request, response) => {
    const reply = (status, value) => { response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff', Connection: 'close' }); response.end(JSON.stringify(value)); };
    if (request.method === 'GET' && request.url === '/readyz') { reply(200, { profile: PROFILE }); return; }
    const provided = Buffer.from(String(request.headers.authorization || ''));
    if (provided.length !== credential.length || !timingSafeEqual(provided, credential) || request.headers.origin !== undefined) { reply(401, { error: 'Query service credential required' }); return; }
    if (request.method !== 'POST' || request.url !== '/v1/query' || request.headers['content-type']?.split(';')[0].trim().toLowerCase() !== 'application/json') { reply(404, { error: 'Not found' }); return; }
    if (active.size >= 2) { reply(503, { error: 'Query capacity unavailable' }); return; }
    let size = 0, raw = [], input;
    const bodyTimer = setTimeout(() => request.destroy(), 5000); bodyTimer.unref();
    try {
      for await (const chunk of request) { size += chunk.length; if (size > 8192) throw new Error('request too large'); raw.push(chunk); }
      input = decode(Buffer.concat(raw));
    } catch { if (!response.destroyed) reply(400, { error: 'Invalid query' }); return; }
    finally { clearTimeout(bodyTimer); raw = []; }
    if (active.size >= 2) { reply(503, { error: 'Query capacity unavailable' }); return; }
    let worker;
    try { worker = new Worker(new URL('./worker.mjs', import.meta.url), { workerData: { ...input, deadline: Date.now() + input.timeoutMs },
      resourceLimits: { maxOldGenerationSizeMb: 64, maxYoungGenerationSizeMb: 8, stackSizeMb: 2 }, stdout: true, stderr: true, env: { LANG: 'C', LC_ALL: 'C', TZ: 'UTC', NO_COLOR: '1' } }); }
    catch { reply(503, { error: 'Query worker unavailable' }); return; }
    active.add(worker);
    let done = false, output = 0;
    const finish = async value => {
      if (done) return; done = true; clearTimeout(timer);
      try { await worker.terminate(); }
      catch { server.close(); process.exitCode = 1; response.destroy(); return; }
      active.delete(worker);
      if (!response.destroyed) reply(200, { id: input.id, profile: PROFILE, ...value });
    };
    const unavailable = () => void finish({ state: 'pending', latencyMs: 0 });
    const timer = setTimeout(unavailable, input.timeoutMs); timer.unref();
    for (const stream of [worker.stdout, worker.stderr]) stream.on('data', data => { output += data.length; if (output > 4096) unavailable(); });
    worker.once('error', unavailable); worker.once('exit', unavailable);
    worker.once('message', value => {
      if (!value || !['up', 'down', 'pending'].includes(value.state) || !Number.isFinite(value.latencyMs) || value.latencyMs < 0 || value.latencyMs > input.timeoutMs || value.state !== 'up' && value.latencyMs !== 0) { unavailable(); return; }
      void finish({ state: value.state, latencyMs: value.latencyMs });
    });
    response.once('close', () => { if (!response.writableEnded) unavailable(); });
  });
  server.maxConnections = 8; server.maxHeadersCount = 32;
  server.shutdown = async () => {
    server.closeAllConnections(); server.close(); await Promise.all([...active].map(worker => worker.terminate())); active.clear();
  };
  return server;
}

async function main() {
  // Refuse an unexpected installed dependency before readiness, without loading
  // GameDig (and its protocol modules) into this long-lived service process.
  const packageURL = new URL('../package.json', import.meta.resolve('gamedig'));
  const installed = JSON.parse(await readFile(packageURL, 'utf8'));
  const installedGames = await readFile(new URL('./games.js', import.meta.resolve('gamedig')));
  if (installed.version !== '5.3.3' || catalogue.version !== installed.version || createHash('sha256').update(installedGames).digest('hex') !== provenance.sourceSha256) throw new Error('unsupported game query profile');
  const server = createQueryServer(process.env.CANARY_GAME_QUERY_TOKEN);
  const port = Number(process.env.CANARY_GAME_QUERY_PORT || 3121);
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('invalid service port');
  for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => {
    const deadline = setTimeout(() => process.exit(1), 2000); deadline.unref();
    void server.shutdown().then(() => process.exit(0), () => process.exit(1));
  });
  server.on('error', () => { console.error('Game query service listener failed'); process.exitCode = 1; });
  server.listen(port, process.env.CANARY_GAME_QUERY_HOST || '127.0.0.1', () => console.log('Game query service ready'));
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(() => { console.error('Game query service startup failed'); process.exitCode = 1; });
}
