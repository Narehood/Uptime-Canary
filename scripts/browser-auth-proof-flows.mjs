import { api, assert, card, dialog, freshCode, origin, pass, rememberSecret, report, secret, sleep, totp, until } from './browser-auth-proof.mjs';

export async function run({ a, b, page: p }) {
  const password = secret(), nextPassword = secret(), resetPassword = secret();
  let adminSession, seed, lastStep;
  const me = async (node = a, token) => api(node, '/api/auth/me', 'GET', undefined, token ?? node.token);
  const account = async name => (await api(a, '/api/accounts')).value.find(e => e.name === name);
  async function boot(node) {
    await p.field('Node API address', node.url); await sleep(700);
    await until(() => p.eval(`!![...document.querySelectorAll('input[name="token"]')].length`), 'bootstrap token choice');
    await p.field('Account token', node.token); await p.click('button', 'Sign in'); await p.text('Choose a password');
  }
  async function login(node, name, value, options = {}) {
    await p.field('Node API address', node.url); await sleep(550);
    // The current form exposes a radio with its name in the enclosing label.
    await p.click('choice', 'Password');
    await p.field('Account name', name); await p.field('Password', value); await p.click('button', options.switch ? 'Sign in and switch' : 'Sign in');
    if(options.seed) {
      await p.text('Password accepted for');
      if(options.replay) {
        await p.field('Authenticator code', options.replay); await p.click('button', 'Verify code'); await p.text('That code wasn’t accepted.');
        pass('MFA confirmation code replay rejected with fresh-code guidance');
      }
      const fresh = await freshCode(options.seed, options.lastStep); lastStep = fresh.step;
      await p.field('Authenticator code', fresh.code); await p.click('button', 'Verify code');
    }
    await p.text('Viewing from');
    return (await p.lastResponse('/api/auth/login')).value.token;
  }
  async function signOut() { await p.click('button', 'Sign out', 'document.querySelector("header.topbar")'); await p.text('Sign in to a node'); }
  async function connectToken(node = a) {
    await p.field('Node API address', node.url); await sleep(500); await p.click('choice', 'Account token'); await p.field('Account token', node.token); await p.click('button', 'Sign in'); await p.text('Viewing from');
  }
  async function noSecrets(label) {
    const state = await p.eval(`(async()=>({local:Object.fromEntries(Object.entries(localStorage)), session:Object.fromEntries(Object.entries(sessionStorage)), cookies:document.cookie, databases:typeof indexedDB.databases==='function'?(await indexedDB.databases()).map(d=>d.name):null, caches:await caches.keys(), href:location.href}))()`);
    const allowedKeys = new Set(['uptime-canary.endpoint','uptime-canary.theme']);
    assert(Object.keys(state.local).every(k => allowedKeys.has(k)), 'Unexpected localStorage keys');
    assert(Object.keys(state.session).length === 0 && state.cookies === '' && !state.databases?.length && !state.caches.length, 'Unexpected persistent browser data');
    assert(!JSON.stringify(state).includes(password) && !JSON.stringify(state).includes(adminSession) && (!seed || !JSON.stringify(state).includes(seed)), 'Fixture secret persisted');
    assert(!p.requests.some(r => r.hasUrlCredential), 'Credentials/query data in request URL');
    report.storage = { allowedLocalKeys: Object.keys(state.local), sessionStorageKeys: [], cookies: false, indexedDB: state.databases, caches: state.caches, urlCredentials: false };
    pass(label);
  }
  async function layouts(label, capture = false) {
    for(const scheme of ['light','dark']) {
      await p.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: scheme }] });
      for(const width of [390,1000,1440]) {
        await p.send('Emulation.setDeviceMetricsOverride', { width, height: 1000, deviceScaleFactor: 1, mobile: false }); await sleep(100);
        const r = await p.eval(`(()=>({viewport:innerWidth,scroll:document.documentElement.scrollWidth,body:document.body.scrollWidth,mode:document.documentElement.dataset.mode,dialogs:[...document.querySelectorAll('dialog[open]')].map(e=>({client:e.clientWidth,scroll:e.scrollWidth})),outside:[...document.querySelectorAll('main input,main button,main select,dialog[open] input,dialog[open] button')].filter(e=>e.getClientRects().length && !e.closest('.vh')).map(e=>({e,r:e.getBoundingClientRect()})).filter(({r})=>r.left< -1||r.right>innerWidth+1).map(({e})=>({tag:e.tagName,label:e.getAttribute('aria-label')||e.textContent.trim().slice(0,80)}))}))()`);
        report.layouts.push({ page:label,scheme,width,...r });
        if(capture && width===390) await p.screenshot(`${label}-${width}-${scheme}`);
      }
    }
    await p.send('Emulation.setDeviceMetricsOverride', { width:1440,height:1000,deviceScaleFactor:1,mobile:false });
    pass(`${label}: overflow measured at 390/1000/1440 in light/dark`);
  }
  await layouts('sign-in', true);
  await boot(a);
  pass('first connection detects setupRequired and token bootstrap routes to Your account');
  await p.click('button','Set password'); await p.text('Enter a password.');
  await p.field('New password','short',card('Choose a password')); await p.field('Confirm new password','different',card('Choose a password')); await p.click('button','Set password'); await p.text('Use at least 12 characters');
  await p.field('New password',password,card('Choose a password')); await p.field('Confirm new password','different',card('Choose a password')); await p.click('button','Set password'); await p.text('The passwords don’t match.');
  await p.field('Confirm new password',password,card('Choose a password')); await p.click('button','Set password'); await p.text('Your password is set');
  assert((await me()).value.passwordEnabled, 'Password not enabled'); pass('Set password validates empty/short/mismatch and succeeds with token retained');
  await p.click('button','Sign in with the password now'); await p.text('Sign in to a node');
  await p.field('Password',secret()); await p.click('button','Sign in'); await p.text('Invalid username, password or authenticator code');
  assert(await p.eval(`document.querySelector('input[name="password"]').value===''`),'Wrong password not cleared'); pass('wrong password denied and input cleared');
  adminSession = await login(a,'admin',password);
  assert((await me(a,adminSession)).status===200,'UI login session not usable'); pass('correct password signs in through actual API');
  await p.route('/account'); await p.text('Change password'); await layouts('your-account',true); await noSecrets('password session stays outside persistent browser storage and URLs');
  // Reload forgets the memory session but does not claim to revoke it on the server.
  await p.nav(origin); await p.text('Sign in to a node'); assert((await me(a,adminSession)).status===200,'Reload unexpectedly revoked session');
  await api(a,'/api/auth/logout','POST',undefined,adminSession); pass('reload forgets password session in tab');
  adminSession=await login(a,'admin',password); await p.route('/account');
  await p.field('Current password',secret(),card('Two-factor authentication')); await p.click('button','Set up authenticator'); await p.text('Current password is incorrect');
  await p.field('Current password',password,card('Two-factor authentication')); await p.click('button','Set up authenticator'); await p.text('Set up your authenticator');
  const enrollment=(await p.lastResponse('/api/auth/totp/setup')).value; seed=enrollment.secret;
  assert((await me(a,adminSession)).value.mfaEnabled===false,'MFA enabled before confirm');
  const shown=await p.eval(`document.querySelector('.keybox .mono').textContent.replace(/\\s/g,'')`);
  assert(shown===seed,'Manual setup key differs from API seed');
  const uri=new URL(enrollment.uri); assert(uri.protocol==='otpauth:' && uri.hostname==='totp' && uri.searchParams.get('secret')===seed,'Setup URI mismatch');
  // Exercise actual clipboard controls; reads happen only in this isolated profile.
  let clipboard={supported:false};
  try {
    await p.send('Browser.grantPermissions',{origin,permissions:['clipboardReadWrite','clipboardSanitizedWrite']});
    await p.click('button','Copy key'); const key=await p.eval('navigator.clipboard.readText()'); assert(key===seed,'Copied setup key mismatch');
    await p.click('button','Copy setup link'); const link=await p.eval('navigator.clipboard.readText()'); assert(link===enrollment.uri,'Copied URI mismatch'); clipboard={supported:true,keyMatches:true,uriMatches:true};
    await p.eval(`navigator.clipboard.writeText('')`);
  } catch(e) { clipboard={supported:false,reason:String(e.message).slice(0,180)}; }
  report.clipboard=clipboard;
  // Native independent decoder where Chrome supports QR. Do not reuse the app QR encoder as a decoder.
  report.qr=await p.eval(`(async()=>{ if(typeof BarcodeDetector==='undefined')return {supported:false}; const formats=await BarcodeDetector.getSupportedFormats();if(!formats.includes('qr_code'))return {supported:false};const svg=document.querySelector('svg.qr');const blob=new Blob([new XMLSerializer().serializeToString(svg)],{type:'image/svg+xml'});const img=await createImageBitmap(blob);const values=await new BarcodeDetector({formats:['qr_code']}).detect(img);return {supported:true,values:values.map(v=>v.rawValue)};})()`).catch(e=>({supported:false,error:e.message}));
  if(report.qr.values) { report.qr.matches=report.qr.values.length===1&&report.qr.values[0]===enrollment.uri; delete report.qr.values; }
  await noSecrets('pending authenticator seed/password absent from browser storage and URLs');
  await layouts('mfa-enrollment');
  await p.field('Authenticator code','123'); await p.click('button','Turn on two-factor'); await p.text('Enter the 6-digit code');
  const confirmCode=totp(seed); lastStep=Math.floor(Date.now()/30000);
  await p.field('Authenticator code',confirmCode); await p.click('button','Turn on two-factor'); await p.text('Two-factor authentication is on. Sign in');
  assert((await me(a,adminSession)).status===401,'Confirmation did not revoke session'); pass('MFA setup rejects wrong password; confirm requires six digits, enables MFA and revokes session');
  adminSession=await login(a,'admin',password,{seed,lastStep,replay:confirmCode});
  pass('MFA password challenge then fresh RFC 6238 code signs in');
  await p.route('/account');
  await p.field('Current password',password,card('Change password')); await p.field('New password',nextPassword,card('Change password')); await p.field('Confirm new password',nextPassword,card('Change password'));
  const changeCode=await freshCode(seed,lastStep); lastStep=changeCode.step;
  await p.field('Authenticator code',changeCode.code,card('Change password')); await p.click('button','Change password'); await p.text('Password changed. Sign in');
  assert((await me(a,adminSession)).status===401,'Password change left old session valid'); assert((await me()).status===200,'Password change invalidated API token');
  pass('password change with MFA revokes session and preserves independent API token');
  const old=await api(a,'/api/auth/login','POST',{name:'admin',password},''); assert(old.status===401,'Old password still works');
  adminSession=await login(a,'admin',nextPassword,{seed,lastStep});
  await p.route('/account'); await p.click('button','Turn off…');
  await p.field('Current password',nextPassword,card('Two-factor authentication'));
  const disableCode=await freshCode(seed,lastStep);lastStep=disableCode.step;
  await p.field('Authenticator code',disableCode.code,card('Two-factor authentication')); await p.click('button','Turn off two-factor'); await p.text('Two-factor authentication is off. Sign in');
  assert((await me(a,adminSession)).status===401,'MFA disable left session valid');pass('MFA disable with fresh code revokes sessions');
  adminSession=await login(a,'admin',nextPassword); await signOut(); assert((await me(a,adminSession)).status===401,'Logout did not revoke session'); pass('normal sign-out revokes server session');
  await connectToken(); await p.click('button','Disconnect','document.querySelector("header.topbar")'); await p.text('Sign in to a node'); assert((await me()).status===200,'Token disconnect revoked token'); pass('token disconnect forgets locally and leaves token valid');
  // Administration exercises password-only, token-only, and combined credentials.
  await connectToken(); await p.route('/accounts'); await p.text('Add an account');
  const names=['proof-password','proof-token','proof-both']; const userPasswords=[secret(),null,secret()]; const tokens={};
  for(let i=0;i<names.length;i++) {
    const s=card('Add an account'); await p.field('Name',names[i],s);
    const withPw=await p.eval(`(()=>{const s=${s};return [...s.querySelectorAll('input[type=checkbox]')][0].checked})()`);
    if(withPw!==(i!==1)) await p.click('choice','Password For people using the dashboard. They can add two-factor authentication themselves.',s);
    const withTok=await p.eval(`(()=>{const s=${s};return [...s.querySelectorAll('input[type=checkbox]')][1].checked})()`);
    if(withTok!==(i!==0)) await p.click('choice','API token For the CLI, scripts and headless access.',s);
    if(i!==1) { await p.field('Initial password',userPasswords[i],s); await p.field('Confirm password',userPasswords[i],s); }
    await p.click('button','Create account',s); await until(()=>p.eval(`!!document.querySelector('button[aria-label="Manage ${names[i]}"]')`),'created account listed');
    const u=await account(names[i]); assert(u.passwordEnabled===(i!==1),'Wrong password-enabled state');
    if(i!==0) { tokens[names[i]]=rememberSecret(await p.eval(`document.querySelector('input[aria-label="API token for ${names[i]}"]').value`)); assert((await me(a,tokens[names[i]])).status===200,'Generated token invalid'); await p.click('button','I’ve stored it'); }
    if(i!==1) { const r=await api(a,'/api/auth/login','POST',{name:names[i],password:userPasswords[i]},''); assert(r.status===200,'Created password invalid'); await api(a,'/api/auth/logout','POST',undefined,r.value.token); }
    pass(`admin creates ${['password-only','token-only','password-and-token'][i]} account; issued credentials authenticate`);
  }
  await layouts('accounts',true);
  await p.click('button','Manage proof-both'); await p.text('Name and role'); await layouts('manage-account',true);
  await p.field('Name','proof-both-renamed',dialog); await p.select('Role','editor',dialog); await p.click('button','Save name and role',dialog); await p.text('Manage proof-both-renamed');
  assert((await account('proof-both-renamed')).role==='editor','Role change not saved'); pass('admin edits account name and role');
  const previousToken=tokens['proof-both']; await p.click('button','Replace API token',dialog); await p.text('New API token for');
  const newToken=rememberSecret(await p.eval(`document.querySelector('input[aria-label="API token for proof-both-renamed"]').value`));
  assert((await me(a,previousToken)).status===401 && (await me(a,newToken)).status===200,'Token rotation invalidation failure'); await p.click('button','I’ve stored it');pass('admin rotates token; old token denied and replacement authenticates');
  // Enable MFA on the target via real API as a fixture precondition; reset remains a browser interaction.
  const target=await account('proof-both-renamed');
  const enrollment2=await api(a,'/api/auth/totp/setup','POST',{currentPassword:userPasswords[2]},newToken);assert(enrollment2.status===200,'Target setup failed');
  const conf=await api(a,'/api/auth/totp/confirm','POST',{currentPassword:userPasswords[2],code:totp(enrollment2.value.secret)},newToken);assert(conf.status===200,'Target confirm failed');
  await p.click('button','Reload accounts'); await sleep(350); await p.click('button','Manage proof-both-renamed'); await p.text('Also turn off two-factor authentication');
  await p.field('New password',resetPassword,dialog); await p.field('Confirm password',resetPassword,dialog);
  await p.click('choice','Also turn off two-factor authentication For a lost authenticator. They can set it up again after signing in. Otherwise two-factor stays on.',dialog);
  await p.click('button','Reset password',dialog); await until(async()=>!(await account('proof-both-renamed')).mfaEnabled,'admin reset removes MFA');
  const resetLogin=await api(a,'/api/auth/login','POST',{name:'proof-both-renamed',password:resetPassword},'');assert(resetLogin.status===200,'Reset password could not sign in');pass('admin password reset with lost-MFA option allows password login without code');
  await p.click('button','Delete proof-both-renamed…',dialog); await p.click('button','Delete account',dialog); await until(async()=>!(await account('proof-both-renamed')),'account deleted');
  assert((await me(a,newToken)).status===401&&(await me(a,resetLogin.value.token)).status===401,'Deleted account credentials remain valid');pass('admin delete revokes password session and API token');
  await p.click('button','Manage admin'); await p.select('Role','viewer',dialog); await p.click('button','Save name and role',dialog); await p.text('Cannot demote the last administrator');
  assert((await p.lastResponse('/api/accounts/bootstrap-admin')).status===409,'Last-admin demote did not return 409');
  await p.click('button','Delete admin…',dialog); await p.click('button','Delete account',dialog); await p.text('Cannot remove the last administrator');
  assert((await p.lastResponse('/api/accounts/bootstrap-admin')).status===409,'Last-admin delete did not return 409'); await p.click('button','Close',dialog);pass('last administrator demotion and deletion rejected with HTTP 409 and visible reasons');
  await noSecrets('admin forms and issued tokens absent from browser persistent storage');
  // Viewer can change own security settings, but account management remains denied.
  await p.click('button','Disconnect','document.querySelector("header.topbar")'); await p.text('Sign in to a node');
  const viewerSession=await login(a,'proof-password',userPasswords[0]); await p.route('/account'); await p.text('Change password'); await layouts('viewer-security');
  await p.route('/accounts'); await p.text('Only admins manage accounts');
  const forbidden=await api(a,'/api/accounts','GET',undefined,viewerSession);assert(forbidden.status===403,'Viewer account API not denied');pass('viewer opens own security page; Accounts page and API deny management');
  await signOut();
  // Endpoint switch must preserve old connection on a failed attempt and revoke the replaced session on success.
  adminSession=await login(a,'admin',nextPassword);
  await api(b,'/api/auth/password','POST',{password:nextPassword});
  await p.click('button',`Viewing from ${a.id} No peers · switch`);
  await p.field('Node API address',b.url,dialog); await sleep(550);
  await p.field('Password',secret(),dialog); await p.click('button','Sign in and switch',dialog); await p.text('Invalid username, password or authenticator code');
  assert((await me(a,adminSession)).status===200,'Failed switch invalidated old session');
  assert(await p.eval(`document.querySelector('header.topbar').innerText.includes(${JSON.stringify(a.id)})`),'Failed switch changed endpoint');pass('failed endpoint switch retains old node and usable session');
  await p.field('Password',nextPassword,dialog); await p.click('button','Sign in and switch',dialog); await p.text(`Viewing from ${b.id}`);
  await until(async()=>(await me(a,adminSession)).status===401,'old node session revoked on switch');pass('successful endpoint switch signs in separately and revokes previous node session');
  await noSecrets('endpoint switching uses no persisted credentials or URL credentials');
  // Stop only the owned node to prove sign-out wording is honest on a network failure.
  await p.route('/account'); b.child.kill(); await sleep(300); await p.click('button','Sign out','document.querySelector("header.topbar")'); await p.text('Signed out of this tab, but');
  assert(await p.eval(`document.body.innerText.includes('didn’t confirm the session ended')`),'Unconfirmed logout claimed success'); pass('unreachable server sign-out forgets tab and explicitly says revocation was unconfirmed');
  await layouts('signed-out',true);
  assert(!report.errors.length,'Browser runtime exceptions found');
  report.runtimeExceptions=0;
  report.externalRequests=report.network.filter(n=>!n.blocked&&!['http://localhost:5173','http://127.0.0.1:5173',a.url,b.url].includes(n.origin));
  assert(!report.externalRequests.length,'External app request escaped isolation');
  pass('no browser runtime exceptions or external app requests');
}
