// One fixed query in a disposable V8 worker. No arbitrary module, script,
// method, command or environment setting comes from the caller.
import { parentPort, workerData } from 'node:worker_threads';

let GameDig;
try { ({ GameDig } = await import('gamedig')); }
catch { parentPort.postMessage({ state: 'pending', latencyMs: 0 }); process.exit(0); }
const input = workerData;
try {
  const remaining = Math.min(input.timeoutMs, input.deadline - Date.now());
  if (remaining < 25) { parentPort.postMessage({ state: 'pending', latencyMs: 0 }); process.exit(0); }
  const attempt = Math.max(1, Math.floor(remaining * 0.8));
  const result = await GameDig.query({
    type: input.game, host: input.host,
    ...(input.port ? { port: input.port } : {}), givenPortOnly: Boolean(input.givenPortOnly),
    ...(input.token ? { token: input.token } : {}),
    socketTimeout: Math.max(1, Math.min(attempt, 2000)), attemptTimeout: attempt,
    maxRetries: 1, requestPlayers: false, portCache: false, stripColors: true,
  });
  const latencyMs = result?.ping;
  parentPort.postMessage(Number.isFinite(latencyMs) && latencyMs >= 0 && latencyMs <= input.timeoutMs
    ? { state: 'up', latencyMs } : { state: 'pending', latencyMs: 0 });
} catch (error) {
  const localFailure = ['ERR_ACCESS_DENIED', 'ERR_MODULE_NOT_FOUND', 'ERR_WORKER_OUT_OF_MEMORY'].includes(error?.code);
  parentPort.postMessage({ state: localFailure ? 'pending' : 'down', latencyMs: 0 });
}
