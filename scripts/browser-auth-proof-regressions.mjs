// Fixed-behavior regressions against real UI input and isolated Go nodes; secrets stay in memory.
import { api, assert, card, dialog, pass, rememberSecret, report, secret, sleep, totp, until } from './browser-auth-proof.mjs';
export async function run({a,b,page:p}) {
  const passwordA=secret(),passwordB=secret();
  assert((await api(a,'/api/auth/password','POST',{password:passwordA})).status===200,'A password fixture');
  assert((await api(b,'/api/auth/password','POST',{password:passwordB})).status===200,'B password fixture');
  report.findings=[];
  async function tokenA() {
    await p.field('Node API address',a.url);await sleep(550);await p.click('choice','Account token');await p.field('Account token',a.token);await p.click('button','Sign in');await p.text('Viewing from');
  }
  async function switchTo(node,password) {
    const current=await p.eval('document.querySelector("header.topbar .tb-btn").innerText.replace(/\\s+/g," ").trim()');
    await p.click('button',current);
    await p.field('Node API address',node.url,dialog);await sleep(550);await p.click('choice','Password',dialog);
    await p.field('Password',password,dialog);await p.click('button','Sign in and switch',dialog);await p.text(`Viewing from ${node.id}`);
  }
  await tokenA();await p.route('/account');await p.text('Change password');
  await p.field('Current password',passwordA,card('Two-factor authentication'));await p.click('button','Set up authenticator');await p.text('Set up your authenticator');
  const enrollment=(await p.lastResponse('/api/auth/totp/setup')).value;
  const polls=p.requests.filter(r=>r.method==='GET'&&new URL(r.url).pathname==='/api/overview').length;
  await until(()=>p.requests.filter(r=>r.method==='GET'&&new URL(r.url).pathname==='/api/overview').length>polls,'live poll during enrollment');
  assert(await p.eval(`document.querySelector('.keybox .mono')?.textContent.replace(/\\s/g,'')===${JSON.stringify(enrollment.secret)}`),'Poll discarded pending enrollment');
  await p.click('button',`Viewing from ${a.id} No peers · switch`);
  await p.field('Node API address',b.url,dialog);await sleep(550);await p.click('choice','Password',dialog);
  await p.field('Password',secret(),dialog);await p.click('button','Sign in and switch',dialog);await p.text('Invalid username, password or authenticator code');
  await p.click('button','Close',dialog);
  assert(await p.eval(`document.querySelector('.keybox .mono')?.textContent.replace(/\\s/g,'')===${JSON.stringify(enrollment.secret)}`),'Failed switch discarded enrollment');
  pass('poll and failed switch preserve pending MFA enrollment on its issuing connection');
  await switchTo(b,passwordB);
  const retained=await p.eval(`(()=>{const e=document.querySelector('.keybox .mono');return !!e&&e.textContent.replace(/\\s/g,'')===${JSON.stringify(enrollment.secret)};})()`);
  if(retained) {
    await p.field('Authenticator code',totp(enrollment.secret));await p.click('button','Turn on two-factor');await p.text('Current password is incorrect');
    const response=await p.lastResponse('/api/auth/totp/confirm');
    const raw=p.responses.filter(r=>new URL(r.response.url).pathname==='/api/auth/totp/confirm').at(-1);
    const body=JSON.parse((await p.send('Network.getRequestPostData',{requestId:raw.requestId})).postData);
    assert(body.currentPassword===passwordA&&raw.response.url.startsWith(b.url),'Expected old password body sent to B');
    report.findings.push({id:'AUTH-UI-01',severity:'high',title:'Pending authenticator enrollment survives endpoint switch and sends the previous node password to the new node',oldEnrollmentRetained:true,previousNodePasswordSentToNewNode:true,newNodeResponse:response.status,oldNodeMfaEnabled:(await api(a,'/api/auth/me')).value.mfaEnabled,newNodeMfaEnabled:(await api(b,'/api/auth/me')).value.mfaEnabled});
    console.log('BUG AUTH-UI-01: pending enrollment/password crossed endpoint boundary');
    await p.click('button','Cancel',card('Set up your authenticator'));
  } else pass('pending enrollment cleared on endpoint switch');
  assert(!retained,'AUTH-UI-01: old MFA seed/password remained on new connection');
  assert(!p.requests.some(r=>r.url===b.url+'/api/auth/totp/confirm'),'AUTH-UI-01: old enrollment submitted to B');
  assert(!(await api(a,'/api/auth/me')).value.mfaEnabled&&!(await api(b,'/api/auth/me')).value.mfaEnabled,'MFA unexpectedly enabled');
  await p.click('button','Sign out','document.querySelector("header.topbar")');await p.text('Sign in to a node');await tokenA();await p.route('/accounts');await p.text('Add an account');
  const s=card('Add an account');await p.field('Name','proof-retained-token',s);
  await p.click('choice','Password For people using the dashboard. They can add two-factor authentication themselves.',s);
  await p.click('choice','API token For the CLI, scripts and headless access.',s);await p.click('button','Create account',s);
  await p.text('Account “proof-retained-token” created');
  const issued=rememberSecret(await p.eval(`document.querySelector('input[aria-label="API token for proof-retained-token"]').value`));
  await switchTo(b,passwordB);
  const stale=await p.eval(`(()=>{const e=document.querySelector('input[aria-label="API token for proof-retained-token"]');return {retained:e?.value===${JSON.stringify(issued)},labelClaimsNewNode:document.querySelector('.issued')?.innerText.includes(${JSON.stringify(b.id)})};})()`);
  if(stale.retained) {
    assert((await api(a,'/api/auth/me','GET',undefined,issued)).status===200,'Issued token not from A');
    assert((await api(b,'/api/auth/me','GET',undefined,issued)).status===401,'Issued token unexpectedly works on B');
    report.findings.push({id:'AUTH-UI-02',severity:'medium',title:'Issued account token survives endpoint switch and is relabeled as belonging to the new node',...stale,oldNodeAcceptsToken:true,newNodeAcceptsToken:false});
    console.log('BUG AUTH-UI-02: old node token relabeled as new node token');
    // Capture only factual layout, with the fixture token hidden in the image.
    await p.eval(`document.querySelector('input[aria-label="API token for proof-retained-token"]').style.color='transparent'`);await p.screenshot('issued-token-wrong-node');
  } else pass('issued token cleared on endpoint switch');
  assert(!stale.retained&&!stale.labelClaimsNewNode,'AUTH-UI-02: old issued token relabeled on new connection');
  assert((await api(a,'/api/auth/me','GET',undefined,issued)).status===200&&(await api(b,'/api/auth/me','GET',undefined,issued)).status===401,'Issued-token origin checks changed');
  // Draft account credentials must not silently migrate to another endpoint either.
  await p.field('Name','proof-draft-on-b',s);await p.field('Initial password',passwordB,s).catch(async()=>{await p.click('choice','Password For people using the dashboard. They can add two-factor authentication themselves.',s);await p.field('Initial password',passwordB,s)});
  await p.field('Confirm password',passwordB,s);
  const draftPolls=p.requests.filter(r=>r.method==='GET'&&new URL(r.url).pathname==='/api/overview').length;
  await until(()=>p.requests.filter(r=>r.method==='GET'&&new URL(r.url).pathname==='/api/overview').length>draftPolls,'live poll during account draft');
  assert(await p.eval(`(()=>{const s=${s};return [...s.querySelectorAll('input')].filter(e=>e.type==='password').every(e=>e.value===${JSON.stringify(passwordB)})})()`),'Poll discarded account draft');
  await switchTo(a,passwordA);
  const draft=await p.eval(`(()=>{const s=${s};const inputs=[...s.querySelectorAll('input')];return {nameRetained:inputs.some(e=>e.value==='proof-draft-on-b'),passwordRetained:inputs.some(e=>e.type==='password'&&e.value===${JSON.stringify(passwordB)})};})()`);
  if(draft.nameRetained||draft.passwordRetained) {
    await p.click('button','Create account',s);await until(async()=>(await api(a,'/api/accounts')).value.some(u=>u.name==='proof-draft-on-b'),'old draft submitted to A');
    const aHasDraft=(await api(a,'/api/accounts')).value.some(u=>u.name==='proof-draft-on-b'),bHasDraft=(await api(b,'/api/accounts')).value.some(u=>u.name==='proof-draft-on-b');
    assert(aHasDraft&&!bHasDraft,'Draft destination differs from switched endpoint');
    report.findings.push({id:'AUTH-UI-03',severity:'medium',title:'New-account draft including password survives switching to another node',...draft,createdOnSwitchedNode:aHasDraft,createdOnDraftOrigin:bHasDraft});console.log('BUG AUTH-UI-03: account draft/credentials crossed endpoint boundary');
  } else pass('new-account draft cleared on endpoint switch');
  assert(!draft.nameRetained&&!draft.passwordRetained,'AUTH-UI-03: account credentials crossed connection boundary');
  await p.click('button','Create account',s);await p.text('Enter a name.');
  assert(!(await api(a,'/api/accounts')).value.some(u=>u.name==='proof-draft-on-b')&&!(await api(b,'/api/accounts')).value.some(u=>u.name==='proof-draft-on-b'),'Cleared draft created account on either node');
  // Reconnect to the same URL/account with fresh credentials: URL/account keys would miss this.
  await p.field('Name','proof-same-node-draft',s);await p.field('Initial password',passwordA,s);await p.field('Confirm password',passwordA,s);
  await switchTo(a,passwordA);
  assert(await p.eval(`(()=>{const s=${s};return [...s.querySelectorAll('input')].every(e=>!['text','password'].includes(e.type)||e.value==='')})()`),'Same-node fresh session retained credential draft');
  pass('successful same-node/account reconnect clears drafts; polls keep them');
  pass('cross-node regression probes completed using distinct fixture passwords');
  // Additional security-page checks use the same isolated fixtures.
  const viewerPassword=secret(),viewerNext=secret();
  assert((await api(a,'/api/accounts','POST',{name:'proof-viewer',role:'viewer',password:viewerPassword})).status===201,'Viewer fixture creation');
  await p.click('button','Sign out','document.querySelector("header.topbar")');await p.text('Sign in to a node');
  async function viewerLogin(password) {await p.field('Node API address',a.url);await sleep(450);await p.click('choice','Password');await p.field('Account name','proof-viewer');await p.field('Password',password);await p.click('button','Sign in');await p.text('Viewing from');return (await p.lastResponse('/api/auth/login')).value.token;}
  const viewerSession=await viewerLogin(viewerPassword),parallelViewer=(await api(a,'/api/auth/login','POST',{name:'proof-viewer',password:viewerPassword},'')).value.token;
  await p.route('/account');await p.field('Current password',viewerPassword,card('Change password'));await p.field('New password',viewerNext,card('Change password'));await p.field('Confirm new password',viewerNext,card('Change password'));await p.click('button','Change password');await p.text('Password changed. Sign in');
  assert((await api(a,'/api/auth/me','GET',undefined,viewerSession)).status===401&&(await api(a,'/api/auth/me','GET',undefined,parallelViewer)).status===401,'Viewer password change failed to revoke both sessions');
  pass('viewer changes own password and both existing sessions are revoked');
  const viewerSession2=await viewerLogin(viewerNext),parallelViewer2=(await api(a,'/api/auth/login','POST',{name:'proof-viewer',password:viewerNext},'')).value.token;
  await p.route('/account');await p.click('button','Sign out everywhere');await p.click('button','Sign out everywhere',dialog);await p.text('Signed out of every password session');
  assert((await api(a,'/api/auth/me','GET',undefined,viewerSession2)).status===401&&(await api(a,'/api/auth/me','GET',undefined,parallelViewer2)).status===401,'Logout-all failed to revoke both viewer sessions');
  pass('viewer Sign out everywhere revokes current and parallel password sessions');
  await tokenA();await p.route('/account');
  const parallelAdmin=(await api(a,'/api/auth/login','POST',{name:'admin',password:passwordA},'')).value.token;assert(parallelAdmin,'Parallel admin login fixture failed');
  await p.click('button','Sign out everywhere');await p.click('button','Sign out everywhere',dialog);await p.text('This tab stays connected with the account token.');
  assert((await api(a,'/api/auth/me','GET',undefined,parallelAdmin)).status===401&&(await api(a,'/api/auth/me')).status===200,'Token logout-all behavior differs');
  pass('token-authenticated Sign out everywhere revokes password sessions and retains the token connection');
  const { run: delayed } = await import('./browser-auth-proof-delayed.mjs');
  await delayed({ a, b, page: p }, { passwordA, passwordB });
}
