// Linux-only actual PM2 interoperability acceptance, invoked by its guarded
// launcher inside an isolated network namespace. Excluded from ordinary proofs.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFileSync, readlinkSync, writeFileSync } from 'node:fs';
import { rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import test from 'node:test';
import { Cluster } from '../apps/proof/fixtures/cluster.ts';

const root = process.cwd(), guard = resolve(process.env.PM2_GUARD || '');
const managerSource = join(root, 'scripts/fixtures/pm2-real/manager.mjs');
function group(pid) { return Number(readFileSync(`/proc/${pid}/stat`, 'utf8').split(') ')[1].split(' ')[2]); }
const ownedGroup = group(process.pid);

async function manager() {
  const run = join(guard, 'pm2-real');
  const child = spawn(process.execPath, ['--max-old-space-size=64', managerSource, guard], { cwd: guard, detached: false,
    env: { HOME: join(run, 'home'), PM2_HOME: join(run, 'pm2'), TMPDIR: join(run, 'tmp'),
      PATH: '/usr/bin:/bin', LANG: 'C', LC_ALL: 'C', TZ: 'UTC' }, stdio: ['pipe', 'pipe', 'pipe'] });
  const exited = new Promise(resolveExit => child.once('exit', (code, signal) => resolveExit({ code, signal })));
  let text = '', bytes = 0, serial = 0, fatal;
  const queue = [];
  child.on('error', () => { fatal = new Error('private manager startup failed'); });
  child.stdout.on('data', data => {
    bytes += data.length; text += data.toString('utf8');
    if (bytes > 65536 || text.length > 8192) { fatal = new Error('private manager output exceeded bound'); child.kill('SIGKILL'); return; }
    let index;
    while ((index = text.indexOf('\n')) >= 0) {
      try { queue.push(JSON.parse(text.slice(0, index))); }
      catch { fatal = new Error('private manager protocol failed'); }
      text = text.slice(index + 1);
    }
  });
  let stderr = 0;
  child.stderr.on('data', data => { stderr += data.length; if (stderr > 65536) { fatal = new Error('private manager stderr exceeded bound'); child.kill('SIGKILL'); } });
  async function packet() {
    const deadline = Date.now() + 8000;
    while (!queue.length) {
      if (fatal) throw fatal;
      assert.equal(child.exitCode, null, 'private manager exited before acknowledgement');
      assert.equal(child.signalCode, null, 'private manager was killed before acknowledgement');
      assert.ok(Date.now() < deadline, 'private manager acknowledgement timed out');
      await delay(10);
    }
    const value = queue.shift(); assert.notEqual(value.event, 'error', 'actual PM2 fixture failed'); return value;
  }
  try {
    const ready = await packet();
    assert.equal(ready.event, 'ready'); assert.equal(ready.version, '6.0.8'); assert.equal(ready.coreFilesVerified, 19);
    assert.equal(ready.status, 'online'); assert.equal(group(ready.managerPid), ownedGroup); assert.equal(group(ready.appPid), ownedGroup);
    const appPIDs = new Set([ready.appPid]);
    return { ready, async command(op) {
      const id = ++serial; child.stdin.write(JSON.stringify({ id, op }) + '\n');
      const reply = await packet(); assert.equal(reply.id, id);
      if (reply.appPid) { assert.equal(group(reply.appPid), ownedGroup); appPIDs.add(reply.appPid); }
      return reply;
    }, async close() {
      try {
        if (child.exitCode === null && child.signalCode === null) {
          child.stdin.write(JSON.stringify({ id: ++serial, op: 'quit' }) + '\n');
          const closed = await packet(); assert.equal(closed.event, 'closed');
        }
        const result = await Promise.race([exited, delay(8000, undefined, { ref: false }).then(() => { throw new Error('private manager cleanup timed out'); })]);
        assert.equal(result.code, 0); assert.equal(result.signal, null);
        for (const pid of appPIDs) assert.throws(() => readFileSync(`/proc/${pid}/stat`), 'fixture worker must have been reaped');
      } finally { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); }
    } };
  } catch (error) { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); await exited; throw error; }
}

if (process.argv.includes('--containment-control')) {
  // The SYSTEM timeout kills only the outer unshare supervisor. This helper
  // and its actual PM2 worker must then die through namespace containment.
  const pm2 = await manager();
  writeFileSync(join(guard, 'control-ready.json'), JSON.stringify({
    namespace: readlinkSync('/proc/self/ns/pid'), ...pm2.ready,
  }));
  await new Promise(() => {});
} else test('actual PM2 6.0.8 RPC drives production Go workers and preserves outage state across manager loss', { timeout: 120000 }, async t => {
  assert.equal(process.platform, 'linux');
  assert.ok(guard.startsWith(join(root, '.canary/work/current/outputs') + '/'));
  const cluster = new Cluster();
  let pm2;
  t.after(async () => { try { await cluster.cleanup(); } finally { if (pm2) await pm2.close(); } });
  pm2 = await manager();
  await cluster.initialize(['real-pm2-a', 'real-pm2-b']); await cluster.startAll();
  const [a, b] = cluster.nodes;
  const input = { name: 'Actual private PM2', type: 'pm2', target: 'canary-pm2-fixture',
    pm2: { transport: 'unix', home: join(guard, 'pm2-real/pm2'), namespace: 'canary-fixture' },
    nodeIds: [a.id, b.id], intervalMs: 2000, timeoutMs: 1500, enabled: true, repeatAfterMs: 0 };
  const monitor = await cluster.createMonitor(input);
  async function settle(state, since) {
    await cluster.wait(`actual PM2 reports ${state} on both observing nodes`, async () =>
      (await cluster.overviews()).every(view => {
        assert.ok(!JSON.stringify(view).includes('private-pm2-fixture-redaction-marker'));
        const current = view.monitors.find(item => item.monitor.id === monitor.id);
        return current?.observations.length === 2 && current.observations.every(item => item.state === state &&
          item.checkedAt >= since && item.latencyAvailable === false && item.latencyMs === 0);
      }));
  }
  await settle('up', Date.now() - 5000);
  let since = Date.now();
  const restarted = await pm2.command('restart');
  assert.equal(restarted.status, 'online'); assert.notEqual(restarted.appPid, pm2.ready.appPid);
  assert.throws(() => readFileSync(`/proc/${pm2.ready.appPid}/stat`), 'restart must reap the old worker');
  await settle('up', since);
  since = Date.now(); assert.equal((await pm2.command('stop')).status, 'stopped'); await settle('down', since);
  await cluster.wait('both nodes retain a real PM2 outage', async () =>
    (await cluster.overviews()).every(view => view.incidents.some(item => item.monitorId === monitor.id && item.resolvedAt === null)));
  const opened = (await cluster.overview(a)).incidents.find(item => item.monitorId === monitor.id && item.resolvedAt === null).openedAt;
  since = Date.now(); assert.equal((await pm2.command('outage')).rpc, 'offline'); await settle('pending', since);
  await cluster.stop(b); await cluster.start(b); since = Date.now(); await settle('pending', since);
  for (const view of await cluster.overviews()) assert.ok(view.incidents.some(item =>
    item.monitorId === monitor.id && item.resolvedAt === null && item.openedAt === opened));
  await pm2.close(); pm2 = undefined;
  // Exact owned disposable path, after manager/worker exit. No user home exists
  // here; relaunch at the same endpoint proves recovery without a config edit.
  const run = join(guard, 'pm2-real'); assert.equal(resolve(run), guard + '/pm2-real');
  await rm(run, { recursive: true, force: false });
  pm2 = await manager(); since = Date.now(); await settle('up', since);
  await cluster.wait('real online evidence closes the retained outage', async () =>
    (await cluster.overviews()).every(view => {
      const incidents = view.incidents.filter(item => item.monitorId === monitor.id);
      return incidents.some(item => item.openedAt === opened && item.resolvedAt !== null) &&
        incidents.every(item => item.resolvedAt !== null);
    }));
  since = Date.now(); assert.equal((await pm2.command('remove')).status, 'removed'); await settle('down', since);
  assert.equal(cluster.events.length, 0, 'the real-manager fixture configures no external notifications');
});
