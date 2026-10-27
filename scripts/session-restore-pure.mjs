// Root-only reload regressions. Stand-in storage/fetch; no browser, credentials or external requests.
import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
process.env.TSX_DISABLE_CACHE = '1';
globalThis.location = { origin: 'https://dashboard.example.test', protocol: 'https:', hash: '' };
globalThis.document = { addEventListener() {}, hidden: false };
globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true;
const memory = new Map(), key = 'uptime-canary.sign-in', calls = [];
globalThis.sessionStorage = { getItem: k => memory.get(k) ?? null, setItem: (k,v) => memory.set(k,v), removeItem: k => memory.delete(k) };
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'session-restore', tsconfig: resolve('apps/web/tsconfig.json') });
const load = p => scope.import(pathToFileURL(resolve('apps/web/src',p)).href, import.meta.url);
const { CanaryApi } = await load('api.ts');
const { Session } = await load('session.ts');
const { savedSignIn } = await load('browserSession.ts');
const token = 'private-fixture-reload-token-12345';
const user = { id: 'account-one', name: 'owner', role: 'admin', passwordEnabled: true };
let status = 200, failures = 0, passed = 0;
globalThis.fetch = async (url, init) => {
  const path = new URL(url).pathname; calls.push({path,init});
  if (path === '/api/auth/logout') return new Response(null,{status:204});
  if (status !== 200) return Response.json({error:'fixture refusal'},{status});
  return Response.json(path === '/api/auth/me' ? user : { node:{id:'dashboard',name:'Dashboard'}, now:Date.now(), user, monitors:[],nodes:[],incidents:[],notifications:[] });
};
const dispose = s => { const c=s.getSnapshot().connection; if(c)s.end({url:c.api.baseUrl,message:'Fixture ended',tone:'info'},c); };
const seed = credential => new CanaryApi(location.origin,token,{credential,...(credential==='session'?{expiresAt:Date.now()+3600000}:{})});
async function check(name, fn) { memory.clear(); calls.length=0; status=200; try { await fn(); passed++; console.log(`PASS ${name}`); } catch(e) { failures++; console.error(`FAIL ${name}: ${e.message}`); } }
for (const credential of ['session','token']) await check(`${credential} sign-in survives a reload and is revalidated with its issuer`, async () => {
  const first=new Session(); await first.connect(seed(credential));
  const raw=memory.get(key); assert.ok(raw); dispose(first); memory.set(key,raw); calls.length=0;
  const next=new Session(); await next.restore();
  try {
    assert.equal(next.getSnapshot().connection.account.id,user.id);
    assert.equal(next.getSnapshot().connection.api.credential,credential);
    assert.deepEqual(calls.map(c=>c.path).sort(),['/api/auth/me','/api/overview']);
    assert.ok(calls.every(c=>c.init.headers.Authorization===`Bearer ${token}`));
    assert.equal(next.getSnapshot().connection.selection,null);
  } finally { dispose(next); }
});
await check('sign out removes the saved credential and reload cannot resurrect it', async () => {
  const s=new Session(); await s.connect(seed('session')); await s.signOut();
  assert.equal(memory.has(key),false); const next=new Session(); await next.restore(); assert.equal(next.getSnapshot().connection,null);
});
await check('a confirmed expired or revoked session clears storage without another logout request', async () => {
  seed('session').remember(); status=401; const s=new Session(); await s.restore();
  assert.equal(s.getSnapshot().connection,null); assert.equal(memory.has(key),false);
  assert.ok(!calls.some(c=>c.path==='/api/auth/logout')); assert.match(s.getSnapshot().farewell.message,/expired|revoked/);
});
await check('a temporary issuer failure retains the sign-in and never revokes it', async () => {
  seed('session').remember(); status=502; const s=new Session(); await s.restore();
  assert.equal(s.getSnapshot().connection,null); assert.ok(memory.has(key)); assert.ok(!calls.some(c=>c.path==='/api/auth/logout'));
  status=200; const next=new Session(); await next.restore(); assert.ok(next.getSnapshot().connection); dispose(next);
});
await check('a rotated account token replaces the remembered credential', async () => {
  const s=new Session(); await s.connect(seed('token')); s.adoptToken('replacement-fixture-token',s.getSnapshot().connection);
  assert.equal(savedSignIn().token,'replacement-fixture-token'); dispose(s);
});
await check('selection is never persisted and gateway or origin changes cannot forward a saved token', async () => {
  seed('session').withRegisteredNode('a'.repeat(32)).remember(); assert.ok(!memory.get(key).includes('a'.repeat(32)));
  const raw=memory.get(key); globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__=false;
  assert.equal(CanaryApi.remembered(),null); globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__=true; memory.set(key,raw);
  location.origin='https://another.example.test'; assert.equal(CanaryApi.remembered(),null); location.origin='https://dashboard.example.test';
});
await check('malformed saved data is cleared without making requests', () => {
  for(const raw of ['{',JSON.stringify({token,baseUrl:'https://attacker.invalid'}),'x'.repeat(5000)]) {
    memory.set(key,raw); assert.equal(CanaryApi.remembered(),null); assert.equal(memory.has(key),false);
  }
  assert.equal(calls.length,0);
});
await check('disabled browser storage does not break normal sign-in or sign-out', async () => {
  const old=globalThis.sessionStorage; globalThis.sessionStorage={getItem(){throw Error('disabled')},setItem(){throw Error('disabled')},removeItem(){throw Error('disabled')}};
  try { const s=new Session(); await s.connect(seed('token')); assert.ok(s.getSnapshot().connection); await s.signOut(); }
  finally { globalThis.sessionStorage=old; }
});
await check('a late restore cannot replace a newer successful sign-in or clear its saved credential', async () => {
  seed('session').remember(); const normal=globalThis.fetch; let release;
  globalThis.fetch=async (url,init)=>new URL(url).pathname==='/api/overview'&&init.headers.Authorization===`Bearer ${token}`
    ? new Promise(resolve=>{release=()=>normal(url,init).then(resolve)}) : normal(url,init);
  const s=new Session();
  try {
    const pending=s.restore(); await Promise.resolve();
    const newer=new CanaryApi(location.origin,'newer-fixture-account-token'); await s.connect(newer);
    release(); await pending;
    assert.ok(s.getSnapshot().connection.api.sameCredential(newer)); assert.equal(savedSignIn().token,'newer-fixture-account-token');
  } finally { globalThis.fetch=normal; dispose(s); }
});
scope.unregister(); console.log(`${passed}/${passed+failures} session restore groups passed`); if(failures)process.exitCode=1;
