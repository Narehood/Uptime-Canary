// SOURCE-REVIEWED ONLY: Linux/Alpine; never run this fixture on Windows.
// Root installs a separate lockfile-pinned pm2@6.0.8 fixture package at
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

const VERSION = '6.0.8';
const COMMIT = '3b3b54727dab5acf6b43778314cd26b84ffa94d3';
const NAME = 'canary-pm2-fixture';
const NAMESPACE = 'canary-fixture';
const CORE = {
  'package.json': 'adb2e8a5f18bc868c3540697b0763d14945f03786689db823189d2534cc2d9e3',
  'index.js': '4774d3dfdf283f67a049ab24e08c2362b236b5b50299a77eed1b5e3e29f6daf1',
  'paths.js': 'f85d7df72d80ebc34bf96c97b8cd538bacb13924af45c14ca7e201bc82e021eb',
  'constants.js': 'ab50b83365fb4e3d95cd110be1ee7c7ad4e148abd385a16319e06f0adb9ccbbe',
  'lib/API.js': 'e7d6f7bf6800ebbd3fa90681b7b85135c112934cb6ec5f1288aab8c1f63c9dca',
  'lib/Client.js': '2c05d0f5e51e3101de8f1e0b4c24d9b50d0a85e0e13b92c41fb68f280e4be093',
  'lib/Daemon.js': '820725216d7237c1c90b193e3b19c7338893b5672afb4a0ba81b244aac8f231a',
  'lib/God.js': 'a43594c030138c6db8d308ee1647903a72a49fc3ae781e600cc56338aa85ba90',
  'lib/God/ClusterMode.js': '81b63a129581cc493e6d73f6b0f94fa9ba3d74a76334f083b24fdd4887b558a9',
  'lib/God/ForkMode.js': '3d1f57dee19060862b4856b15cd61ca406047e658071609e545ac5e1e7755cb4',
  'lib/God/Methods.js': 'd063342503f501ee267eaca19c732d29bc640bf3188f08674b2cdddb9beeeb11',
  'lib/God/ActionMethods.js': 'fac3eb453287059b94461cff459209c2d0a7c10bcb94ebf0f86aad8d087f82f8',
  'lib/Common.js': '34c3db95665c2be6e3d8796086faad88acd3b0f7bd25bf068e3c6a2a325add3e',
  'lib/ProcessContainer.js': 'beaf002a3743489c3df5b884f45d11abceb2491402efc560a4b7c0a15d7e45b8',
  'lib/ProcessUtils.js': 'aefdfb0ec23957c21da61cc690d75117dbd9a69e7cdfc46fe18f922c386c2f23',
  'lib/Worker.js': '42a499e37f3c4c653d7e0da33fb4c09744de494d97d1647dca4691f1d80303c7',
  'lib/VersionCheck.js': '94ec1eef8127ab2bbc74adb06bacb3cf81eac07a40391223ddb45ded3527a0d2',
  'lib/Utility.js': '97dc35e42a3ca1fdb4d79829346e8dbc693f1b180a764c99f202abc3328f3129',
  'lib/Configuration.js': 'ff471fe43b64001f4d9945c546863083d5d691bbd7702feb7094db958f7c9d5b',
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
