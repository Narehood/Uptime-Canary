// SOURCE-REVIEWED ONLY: Linux/Alpine; never run this fixture on Windows.
// Root installs a separate lockfile-pinned pm2@7.1.1 fixture package at
//   GUARD/pm2-package/node_modules/pm2
// and launches: /absolute/node --max-old-space-size=64 /absolute/manager.mjs GUARD
// GUARD must already be a real, current-UID-owned 0700 guarded output directory.
// Spawn with detached:false, cwd:GUARD, execArgv:[], and an EXPLICIT environment:
//   HOME=GUARD/pm2-real/home PM2_HOME=GUARD/pm2-real/pm2
//   TMPDIR=GUARD/pm2-real/tmp PATH=/usr/bin:/bin LANG=C LC_ALL=C TZ=UTC
// No other inherited variables, NODE_OPTIONS, preloads, or real credentials.
// Root owns an outbound-denied/loopback-only Linux process group, resource and
// scratch limits, bounded stderr capture, and final group reaping even on SIGKILL.
// Core hashes below are raw Git bytes, NOT a check of the full dependency tree.
// Root must verify npm integrity, the complete resolved lockfile, package/source
// equivalence, and selected Node's non-detached cluster.fork implementation first.
// No CLI, API singleton, Client.connect/start, Daemon.start/close, interactor,
// system-monitor module, resurrect, process dump, arbitrary options, or scripts.
//
// stdout: one ready line, then one reply per NDJSON {"id":1,"op":"status"}.
// Send ONE command at a time, await its reply, id 1..64 strictly increasing.
// Ops: status, version, online, stop, restart, remove, outage, quit.
// Initial ready means the fixed app sent ready AND PM2 records it online.
// outage closes the REAL RPC listener/connections; it is intentionally one-way.
// Root can quit, reap, remove ONLY GUARD/pm2-real, then relaunch at the SAME path
// to recover the endpoint without changing the monitor's configuration.
// RPC endpoint: GUARD/pm2-real/pm2/rpc.sock (UTF-8 length must be <=100 bytes).
// Fixed monitor selector: name=canary-pm2-fixture namespace=canary-fixture.
// Root can assert that the fixed env redaction marker below never escapes Go.
// This helper emits only fixed fields/statuses and known fixture process IDs;
// never emit getMonitorData, pm2_env, errors/stacks, arbitrary input, or logs.
// TTL: 180s. PM2/app commands: 5s. Cleanup: bounded and fail-closed.
// Root, not this helper, cleans the known disposable directory after reaping.

import fs from 'node:fs';
import path from 'node:path';
import cluster from 'node:cluster';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';

const VERSION = '7.1.1';
const COMMIT = '34dec9197301165850741b319890b2633ae797f4';
const NAME = 'canary-pm2-fixture';
const NAMESPACE = 'canary-fixture';
const CORE = {
  'package.json': 'de979d9fa51d17baf686ced802d18dcdb21eff12f5ba496119c641af4fa497f8',
  'index.js': '7b99a3037651e0802349187be8f012feb09da740ccfcf65e9c5a373757f30590',
  'paths.js': 'e5773c2711f1d41b79de6575cda89e42e96fa4ba42b6c8de373c0785888f9749',
  'constants.js': 'f6ea58459ed2f60c0ac6385c786cc2be4eec4c92c37feb70db8835a2d47110bd',
  'lib/API.js': '1bd03fd1800d6e66751ffdf8e0f11d18e35d6c621ad323a8c3dd499d16d4b275',
  'lib/Client.js': '7fa92932e33f189c00604e76caf409f5d25924c2baefda2e0e2c249e430f57ce',
  'lib/Daemon.js': 'c4e27c6a2b0f380c9846e298b7a1ef4c745c8f33f00bbd61d9a10eee19ee2fb9',
  'lib/God.js': '244e0c2e4725960b323394ac941a73a416e6db8cbbb5ddfb5cfdf2405ec64f91',
  'lib/God/ClusterMode.js': 'a8f8c22bfd73d81aa35dc72f59740e2a0eb2d46c13a39d182f2669137a933b75',
  'lib/God/ForkMode.js': 'a7f82fbbe3a40804b68a76f89a7b8092f21634cec359b6d7cf0ac554671e90c8',
  'lib/God/Methods.js': 'db19828776122ce43a44b4f971b8e9fc1e76804d2cf8df05d4b67e2b72a5684e',
  'lib/God/ActionMethods.js': '2707f1f2d75929e4747b4ea170f64392ba773909b9a3c278042636ce22090f58',
  'lib/Common.js': '223476aede876e172f8653e8a00227f536538ea1fc174fdb0c4eee198b5f9dbf',
  'lib/ProcessContainer.js': '87474be33cd36f8a52e1a46695634e9a98a33ce46830e1e0e3168756f3a0c7c9',
  'lib/ProcessUtils.js': '4166ef73d883fa6871e43cdc94dcddd2f2e221219754e5faea14556bce2e5dfe',
  'lib/Worker.js': '479701c010251490fe8fcec3af4922e7e68b76a0f6bef8d61ec3a538c7edaa4f',
  'lib/VersionCheck.js': '94ec1eef8127ab2bbc74adb06bacb3cf81eac07a40391223ddb45ded3527a0d2',
  'lib/Utility.js': '7ef90946cb3c7ac9256078153d2d2d9f230706b1eefdeb21d1cc0ed17efdec0f',
  'lib/Configuration.js': '3060b4ec29e8591424e91732ef9c827dccb6b513234bb75cff513c41fd858077',
};
const rawWrite = process.stdout.write.bind(process.stdout);
const workers = new Set();
let God, daemon, Common, run, pmHome, app, pmId = null;
let closing = false, shutdownTask, rpcUp = false, proof = false, ready = false;
let lastId = 0, busy = false, input = '', replies = 0;

function requireCondition(ok) { if (!ok) throw new Error('fixture'); }
function emit(value) {
  requireCondition(++replies <= 67 && process.stdout.writableLength < 8192);
  rawWrite(JSON.stringify(value) + '\n');
}
function bounded(start, ms = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('fixture')), ms);
    const done = (err, value) => {
      clearTimeout(timer);
      if (err) reject(new Error('fixture')); else resolve(value);
    };
    try { start(done); } catch { done(new Error('fixture')); }
  });
}
function call(method, arg) { return bounded(cb => God[method](arg, cb)); }
function closeSocket(socket) {
  if (!socket?.server?.listening) return Promise.resolve();
  return bounded(cb => {
    // Axon's close callback runs BEFORE net.Server's close event; await the latter.
    socket.server.once('close', () => cb(null));
    socket.close();
  }, 1500);
}
function killWorkers() {
  for (const worker of workers) {
    if (!worker.isDead()) { try { worker.kill('SIGKILL'); } catch {} }
  }
}
async function reapWorkers() {
  const until = Date.now() + 1000;
  while ([...workers].some(w => !w.isDead()) && Date.now() < until)
    await new Promise(resolve => setTimeout(resolve, 10));
  const forced = [...workers].some(w => !w.isDead());
  killWorkers();
  await bounded(cb => {
    const pending = [...workers].filter(w => !w.isDead());
    if (!pending.length) return cb(null);
    let remaining = pending.length;
    for (const worker of pending) worker.once('exit', () => { if (!--remaining) cb(null); });
  }, 2000);
  return forced;
}
function shutdown(code = 0, id = null) {
  if (shutdownTask) return shutdownTask;
  closing = true;
  process.stdin.pause();
  shutdownTask = (async () => {
    let clean = true;
    // Root MUST still reap the whole owned process group after this helper exits.
    const hardStop = setTimeout(() => { killWorkers(); process.exit(2); }, 6500);
    try {
      if (God) {
        God.notifyKillPM2();
        God.Worker?.stop();
        for (const proc of Object.values(God.clusters_db)) {
          proc.pm2_env.autorestart = false;
          clearTimeout(proc.pm2_env.restart_task);
        }
        for (const key of Object.keys(God.clusters_db)) {
          try { await bounded(cb => God.deleteProcessId(Number(key), cb), 2000); }
          catch { clean = false; }
        }
      }
      if (await reapWorkers()) clean = false;
      await closeSocket(daemon?.rpc_socket);
      await closeSocket(daemon?.pub_socket);
    } catch { clean = false; killWorkers(); }
    clearTimeout(hardStop);
    // Never label a failed operation/forced cleanup as success.
    const result = code || (clean ? 0 : 2);
    try { emit({ event: result ? 'error' : 'closed', id, code: result ? 'fixture-failed' : 'ok' }); }
    catch {}
    // Do not wait for an unread/full stdout pipe. Total protocol output is capped.
    setTimeout(() => process.exit(result), 20);
  })();
  return shutdownTask;
}

cluster.on('fork', worker => {
  workers.add(worker);
  worker.once('exit', () => workers.delete(worker));
  worker.on('error', () => { void shutdown(2); });
  if (closing) { try { worker.kill('SIGKILL'); } catch {} }
});
process.on('SIGINT', () => { void shutdown(); });
process.on('SIGTERM', () => { void shutdown(); });
process.on('SIGHUP', () => { void shutdown(); });
process.on('uncaughtException', () => { void shutdown(2); });
process.on('unhandledRejection', () => { void shutdown(2); });
process.stdout.on('error', () => { void shutdown(2); });
process.stdin.on('error', () => { void shutdown(2); });
process.stdin.on('end', () => { void shutdown(ready && !busy && !input ? 0 : 2); });
// Synchronous last resort; SIGKILL still requires the root-owned process group.
process.on('exit', killWorkers);
setTimeout(() => { void shutdown(2); }, 180000).unref();

function snapshot() {
  const proc = pmId === null ? null : God.clusters_db[pmId];
  const state = proc?.pm2_env.status ?? 'removed';
  requireCondition(['online', 'stopped', 'removed'].includes(state));
  return { status: state, pmId: proc ? pmId : null, appPid: proc?.process.pid || 0, rpc: rpcUp ? 'online' : 'offline' };
}
async function online() {
  requireCondition(!closing);
  if (pmId !== null && God.clusters_db[pmId]) {
    if (snapshot().status !== 'online') await startReady('restartProcessId', { id: pmId });
    return;
  }
  // These are the SAME PM2 normalizer and God.prepare used by PM2's real API.
  const conf = Common.prepareAppConf({ cwd: run }, {
    script: app, cwd: run, name: NAME, namespace: NAMESPACE,
    exec_mode: 'cluster_mode', exec_interpreter: process.execPath,
    instances: 1, instance_var: 'NODE_APP_INSTANCE',
    node_args: ['--max-old-space-size=64'], args: [],
    autostart: true, autorestart: false, watch: false, vizion: false,
    automation: false, pmx: false, trace: false, source_map_support: false,
    disable_source_map_support: true, disable_logs: true,
    out_file: path.join(run, 'app-out.log'), error_file: path.join(run, 'app-err.log'),
    pid_file: path.join(pmHome, 'pids', 'app.pid'), merge_logs: true,
    wait_ready: true, listen_timeout: 3000, kill_timeout: 500,
    kill_retry_time: 50, treekill: false, min_uptime: 100,
    env: { CANARY_PM2_FIXTURE_SECRET: 'private-pm2-fixture-redaction-marker', pmx: 'false' },
  });
  requireCondition(!(conf instanceof Error) && conf.exec_mode === 'cluster_mode');
  const launched = await startReady('prepare', conf);
  requireCondition(launched.length === 1);
  pmId = launched[0].pm2_env.pm_id;
  requireCondition(Number.isSafeInteger(pmId) && snapshot().status === 'online');
}
async function startReady(method, arg) {
  requireCondition(!closing);
  // PM2 times out wait_ready by marking online anyway; require the app's packet
  // on both creation and restart, rather than trusting that fallback.
  let appReady = false;
  const listener = packet => {
    if (packet.raw === 'ready' && packet.process.name === NAME &&
        packet.process.namespace === NAMESPACE) appReady = true;
  };
  God.bus.on('process:msg', listener);
  try {
    const result = await call(method, arg);
    requireCondition(appReady && !closing);
    return result;
  } finally { God.bus.removeListener('process:msg', listener); }
}

async function exited(worker) {
  if (!worker || worker.isDead()) return;
  await bounded(cb => worker.once('exit', () => cb(null)), 2000);
  requireCondition(worker.isDead());
}

async function command(req) {
  requireCondition(!closing && req && Object.getPrototypeOf(req) === Object.prototype &&
    Object.keys(req).length === 2 && Number.isInteger(req.id) &&
    req.id > lastId && req.id <= 64 && typeof req.op === 'string');
  lastId = req.id;
  const previousPID = snapshot().appPid;
  const previous = [...workers].find(worker => worker.process.pid === previousPID);
  if (previousPID) requireCondition(previous);
  switch (req.op) {
    case 'status': break;
    case 'version':
      requireCondition(proof && await call('getVersion', {}) === VERSION);
      emit({ id: req.id, version: VERSION, commit: COMMIT, coreFilesVerified: Object.keys(CORE).length });
      return;
    case 'online': await online(); break;
    case 'stop':
      if (pmId !== null && God.clusters_db[pmId]) await call('stopProcessId', pmId);
      await exited(previous);
      requireCondition(snapshot().status === 'stopped');
      break;
    case 'restart':
      requireCondition(pmId !== null && God.clusters_db[pmId]);
      await startReady('restartProcessId', { id: pmId });
      await exited(previous);
      requireCondition(snapshot().appPid !== previousPID);
      break;
    case 'remove':
      if (pmId !== null && God.clusters_db[pmId]) await call('deleteProcessId', pmId);
      await exited(previous);
      pmId = null;
      break;
    case 'outage':
      await closeSocket(daemon.rpc_socket);
      rpcUp = false;
      break;
    case 'quit': await shutdown(0, req.id); return;
    default: throw new Error('fixture');
  }
  if (!closing) emit({ id: req.id, ...snapshot() });
}

try {
  // Built-in imports only above this point; refuse before loading any PM2 module.
  requireCondition(process.platform === 'linux' && cluster.isPrimary &&
    process.argv.length === 3 && process.execArgv.length === 1 && process.execArgv[0] === '--max-old-space-size=64');
  const guard = process.argv[2];
  requireCondition(path.isAbsolute(guard) && fs.realpathSync(guard) === guard && guard !== '/');
  const st = fs.statSync(guard);
  requireCondition(st.isDirectory() && st.uid === process.getuid() && (st.mode & 0o777) === 0o700);
  requireCondition(fs.realpathSync(process.cwd()) === guard);
  run = path.join(guard, 'pm2-real');
  pmHome = path.join(run, 'pm2');
  const home = path.join(run, 'home'), tmp = path.join(run, 'tmp');
  requireCondition(Buffer.byteLength(path.join(pmHome, 'rpc.sock')) <= 100);
  const allowed = new Set(['HOME', 'PM2_HOME', 'TMPDIR', 'PATH', 'LANG', 'LC_ALL', 'TZ']);
  requireCondition(Object.keys(process.env).every(k => allowed.has(k)) &&
    process.env.HOME === home && process.env.PM2_HOME === pmHome && process.env.TMPDIR === tmp);
  for (const key of Object.keys(process.env)) delete process.env[key];
  Object.assign(process.env, {
    HOME: home, PM2_HOME: pmHome, TMPDIR: tmp,
    PATH: `${path.dirname(process.execPath)}:/usr/bin:/bin`, LANG: 'C', LC_ALL: 'C', TZ: 'UTC',
    PM2_PROGRAMMATIC: 'true', PM2_SILENT: 'true', PM2_NO_INTERACTION: 'true',
    PM2_DISABLE_VERSION_CHECK: 'true', pmx: 'false',
  });
  process.umask(0o077);
  // Exclusive creation: NEVER attach to or inspect an existing PM2 home.
  fs.mkdirSync(run, { mode: 0o700 });
  for (const dir of [home, tmp, pmHome, path.join(pmHome, 'pids'), path.join(pmHome, 'logs')])
    fs.mkdirSync(dir, { mode: 0o700 });
  fs.writeFileSync(path.join(pmHome, 'module_conf.json'), '{}', { flag: 'wx', mode: 0o600 });
  process.chdir(run);
  app = path.join(run, 'fixed-app.cjs');
  fs.writeFileSync(app, `'use strict';\nsetInterval(() => {}, 1000);\nfor (const event of ['disconnect', 'SIGINT', 'SIGTERM']) process.on(event, () => process.exit(0));\nprocess.send('ready');\n`, { flag: 'wx', mode: 0o600 });
  const pkgDir = path.join(guard, 'pm2-package', 'node_modules', 'pm2');
  requireCondition(fs.realpathSync(pkgDir) === pkgDir);
  for (const [file, digest] of Object.entries(CORE)) {
    const full = path.join(pkgDir, file);
    requireCondition(fs.realpathSync(full) === full && fs.statSync(full).size <= 262144);
    requireCondition(createHash('sha256').update(fs.readFileSync(full)).digest('hex') === digest);
  }
  requireCondition(JSON.parse(fs.readFileSync(path.join(pkgDir, 'package.json'), 'utf8')).version === VERSION);
  proof = true;
  // Suppress vendor console output, preserving our bounded protocol write only.
  for (const method of ['log', 'info', 'warn', 'error', 'debug', 'trace']) console[method] = () => {};
  const requirePM2 = createRequire(path.join(pkgDir, 'package.json'));
  const Daemon = requirePM2('./lib/Daemon.js');
  God = requirePM2('./lib/God.js');
  Common = requirePM2('./lib/Common.js');
  // Keep worker stdio away from the command pipe, including startup failures.
  cluster.setupPrimary({ cwd: run, stdio: ['ignore', 'ignore', 'ignore', 'ipc'] });
  daemon = new Daemon({ ignore_signals: true, rpc_socket_file: path.join(pmHome, 'rpc.sock'),
    pub_socket_file: path.join(pmHome, 'pub.sock'), pid_file: path.join(pmHome, 'pm2.pid') });
  await bounded(cb => daemon.innerStart(cb));
  rpcUp = true;
  await online();
  requireCondition(await call('getVersion', {}) === VERSION && !closing);
  ready = true;
  emit({ event: 'ready', version: VERSION, commit: COMMIT, coreFilesVerified: Object.keys(CORE).length,
    name: NAME, namespace: NAMESPACE, managerPid: process.pid, ...snapshot() });
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', data => {
    if (closing) return;
    input += data;
    if (busy || Buffer.byteLength(input) > 256) { void shutdown(2); return; }
    const end = input.indexOf('\n');
    if (end === -1) return;
    // Refuse pipelining and trailing bytes; root waits for every acknowledgement.
    if (end !== input.length - 1) { void shutdown(2); return; }
    const line = input.slice(0, end);
    input = '';
    busy = true;
    Promise.resolve().then(() => command(JSON.parse(line))).catch(() => shutdown(2))
      .finally(() => { busy = false; });
  });
} catch { await shutdown(2); }
