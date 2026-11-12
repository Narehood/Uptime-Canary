// Actual React export controls against the private dashboard. Clipboard and
// downloads stay in page memory; no user deployment or external node is used.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';

/** Resolve copied YAML with the actual Compose parser, without reading a .env file or starting a container. */
function portainerStorage(yaml, variables = {}) {
  const result = spawnSync('docker', ['compose', '--env-file', '/dev/null', '--project-name', 'review-reused-stack', '--file', '-', 'config', '--format', 'json'], {
    input: yaml, encoding: 'utf8', maxBuffer: 128 * 1024,
    env: { ...process.env, CANARY_ADMIN_TOKEN: 'compose-fixture-admin-0123456789', CANARY_CLUSTER_TOKEN: 'compose-fixture-cluster-9876543210', ...variables },
  });
  assert.equal(result.status, 0, result.stderr);
  const config = JSON.parse(result.stdout);
  return { nodeId: config.services.node.environment.CANARY_NODE_ID,
    data: config.volumes['canary-data'].name, identity: config.volumes['canary-identity'].name, environment: config.services.node.environment };
}

export async function reviewNodeBootstrap({ fixture, page, step, until, fill, click, ready, owner, secrets }) {
  const q = JSON.stringify, id = 'review-first-start-dns';
  const caps = await fixture.api('/api/dashboard/onboarding', 'GET', undefined, owner);
  const writes = () => fixture.stats().paths.filter(row => row.method !== 'GET').length;
  assert.equal(caps.value.outboundEnabled, true);
  assert.equal(caps.value.connectionUrl, undefined, 'native listener starts without a configured default address');
  assert.match(caps.value.connectionTlsFingerprint, /^[0-9a-f]{64}$/u);
  let settings, bootstrapWidths, invitation;
  await step('Node exports: entered dashboard address produces complete setup on all platforms', async () => {
    try {
      await page.eval('location.hash="#/nodes"'); await ready();
      await click('Add node');
      await until(() => page.eval('!!document.querySelector("dialog[open]")'), 'Add node opened');
      const pick = async label => {
        await ready();
        await page.eval(`(() => {const e=[...document.querySelectorAll('dialog[open] label')].find(e=>e.querySelector('input[type=radio]')&&__norm(e.innerText).startsWith(${q(label)}));if(!e)throw Error('choice missing');e.querySelector('input').click();})()`);
      };
      await pick('Deploy a new node'); await pick('The node reaches this dashboard');
      await fill('Name on this dashboard', 'First-start DNS', 'dialog[open]');
      await fill('Node ID', id, 'dialog[open]');
      assert.ok(await page.eval('[...document.querySelectorAll("dialog[open] .guide button:not([role=tab])")].every(b=>b.disabled)'), 'the visible dashboard address is required');
      await fill('Dashboard IP or HTTPS address', fixture.connection.url, 'dialog[open]');
      await page.eval(`(() => {
        window.__bootstrapCopies=[];window.__bootstrapFiles=[];
        window.__bootstrapOriginals={clipboard:navigator.clipboard,create:URL.createObjectURL,revoke:URL.revokeObjectURL,click:HTMLAnchorElement.prototype.click};
        Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>{window.__bootstrapCopies.push(text)}}});
        URL.createObjectURL=blob=>{window.__bootstrapFiles.push(blob);return 'blob:private-bootstrap-fixture'};
        URL.revokeObjectURL=()=>{};
        HTMLAnchorElement.prototype.click=function(){if(!this.download)throw Error('unexpected navigation')};
      })()`);
      const action = async label => {
        await page.eval(`(() => {const b=[...document.querySelectorAll('dialog[open] .guide button')].find(b=>b.getAttribute('aria-label')===${q(label)});if(!b||b.disabled)throw Error('export disabled: '+${q(label)});b.click()})()`);
      };
      let pair, firstStorage;
      const before = writes();
      for (const platform of ['Docker Compose', 'Portainer stack', 'Linux VM', 'Docker run']) {
        await click(platform, 'dialog[open]');
        const files = await page.eval('[...document.querySelectorAll("dialog[open] .guide button")].map(b=>b.getAttribute("aria-label")||"").filter(l=>l.startsWith("Download ")).map(l=>l.slice(9))');
        assert.ok(files.some(name => name.endsWith('.env')));
        let yaml;
        for (const file of files) {
          const count = await page.eval('window.__bootstrapCopies.length');
          await action(`Copy ${file}`);
          const copied = await until(() => page.eval(`window.__bootstrapCopies.length>${count}&&window.__bootstrapCopies.at(-1)`), 'file copied');
          const saved = await page.eval('window.__bootstrapFiles.length');
          await action(`Download ${file}`);
          await until(() => page.eval(`window.__bootstrapFiles.length>${saved}`), 'file downloaded');
          assert.equal(await page.eval('window.__bootstrapFiles.at(-1).text()'), copied, `${platform}: exact downloaded bytes`);
          assert.doesNotMatch(copied, /INVITATION_TOKEN|\u2022{4}/u);
          if (/\.ya?ml$/u.test(file)) yaml = copied;
          if (platform === 'Portainer stack' && file.endsWith('.yml')) {
            firstStorage = portainerStorage(copied);
            assert.equal(firstStorage.nodeId, id);
            assert.match(firstStorage.data, /^canary-review-first-start-dns-[0-9a-f]{8}-data$/u);
            assert.equal(firstStorage.identity, firstStorage.data.slice(0, -5) + '-identity');
            const legacy = copied.replace(/^    name: .+\n/gmu, '');
            const oldStorage = portainerStorage(legacy);
            const oldOther = portainerStorage(legacy.replace(`CANARY_NODE_ID: '${id}'`, "CANARY_NODE_ID: 'review-unpaired-other'"));
            assert.notEqual(oldStorage.nodeId, oldOther.nodeId);
            assert.equal(oldStorage.data, oldOther.data, 'negative control: old exports reused the stack database across node IDs');
            assert.equal(oldStorage.identity, oldOther.identity, 'negative control: old exports also reused identity storage');
            assert.notEqual(firstStorage.data, oldStorage.data, 'the fixed export never mounts the old stack-scoped database');
          }
          if (file.endsWith('.env')) {
            const env = Object.fromEntries(copied.split('\n').filter(line=>line&&!line.startsWith('#')).map(line=>{const n=line.indexOf('=');return [line.slice(0,n),line.slice(n+1).replace(/^"|"$/gu,'')]}));
            const complete = yaml ? portainerStorage(yaml, env).environment : env;
            assert.equal(complete.CANARY_NODE_ID, id);
            assert.equal(complete.CANARY_DASHBOARD_URL, fixture.connection.url);
            assert.equal(complete.CANARY_DASHBOARD_TLS_FINGERPRINT, caps.value.connectionTlsFingerprint);
            assert.ok(env.CANARY_DASHBOARD_ENROLL_TOKEN?.length>=24); secrets.add(env.CANARY_DASHBOARD_ENROLL_TOKEN);
            const tokens = [env.CANARY_ADMIN_TOKEN, env.CANARY_CLUSTER_TOKEN];
            assert.ok(tokens.every(token=>token?.length>=24));
            tokens.forEach(token=>secrets.add(token));
            if (!pair) pair=tokens; else assert.deepEqual(tokens,pair,'same credentials on every platform');
            assert.ok(!(await page.eval('document.querySelector("dialog[open] .guide").innerText')).includes(tokens[0]), 'preview masks the real token');
            await action(`Show private values in ${file}`);
            assert.ok((await page.eval('document.querySelector("dialog[open] .guide").innerText')).includes(tokens[0]), 'Show reveals the real token');
            await action(`Hide private values in ${file}`);
            assert.ok(!(await page.eval('document.querySelector("dialog[open] .guide").innerText')).includes(tokens[0]), 'Hide masks the real token again');
            settings = complete;
          }
        }
      }
      // A generated invitation locks its ID and credentials. Existing direct-flow checks retain the editable-ID binding regression.
      assert.equal(writes(), before+1, 'first export creates exactly one invitation; later exports reuse it');
      const listed=await fixture.api('/api/dashboard/invitations','GET',undefined,owner);
      invitation=listed.value.find(row=>row.nodeId===id);
      assert.equal(invitation.state,'waiting');
      assert.equal(invitation.connectionUrl,fixture.connection.url);
      await page.send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:false});
      await click('Portainer stack','dialog[open]');
      const widths = bootstrapWidths = await page.eval('({page:document.documentElement.clientWidth,scroll:document.documentElement.scrollWidth,dialog:document.querySelector("dialog[open]").clientWidth,dialogScroll:document.querySelector("dialog[open]").scrollWidth})');
      assert.ok(widths.scroll<=widths.page+1&&widths.dialogScroll<=widths.dialog+1,'mobile export layout has no horizontal overflow');
      await page.eval('document.querySelector("dialog[open] .guide").scrollIntoView({block:"start"})');
      const image=await page.send('Page.captureScreenshot',{format:'png'});
      writeFileSync(new URL('../.canary/node-export-current.png',import.meta.url),Buffer.from(image.data,'base64'));
    } finally {
      fixture.previewOnboarding(null);
      try {
        await page.eval(`(() => {
          const o=window.__bootstrapOriginals;
          if(o){Object.defineProperty(navigator,'clipboard',{configurable:true,value:o.clipboard});URL.createObjectURL=o.create;URL.revokeObjectURL=o.revoke;HTMLAnchorElement.prototype.click=o.click;}
          delete window.__bootstrapOriginals;delete window.__bootstrapCopies;delete window.__bootstrapFiles;

        })()`);

      } finally { await page.send('Emulation.clearDeviceMetricsOverride'); }
    }
    return { platforms:4, copyMatchesDownload:true, hiddenInputsRequired:0, apiWrites:1, credentialBinding:true, nodeSpecificPortainerStorage:true, widths:bootstrapWidths };
  });
  await step('Node exports: complete copied settings connect, pair, and restart unchanged', async () => {
    assert.equal(settings.CANARY_NODE_ID,id);
    const nodePort=await fixture.freePort();
    let node=await fixture.startNode(id,settings,nodePort);
    try {
      const fingerprint=await until(()=>node.fingerprint(),'exported node fingerprint',15000);
      const pending=await until(async()=>{const reply=await fixture.api(`/api/dashboard/invitations/${invitation.id}`,'GET',undefined,owner);return reply.value?.state==='connected'?reply.value:null},'native pinned connection',30000);
      assert.equal(pending.node.fingerprint,fingerprint);
      assert.ok(!(await fixture.api('/api/dashboard/nodes','GET',undefined,owner)).value.some(row=>row.nodeId===id),'connection alone grants no management authority');
      await until(()=>page.eval('!!document.querySelector("dialog[open] .pairing input[type=checkbox]")'),'fingerprint confirmation shown',15000);
      assert.equal(await page.eval('document.querySelector("dialog[open] .pairing dd.fp").innerText.trim()'),fingerprint);
      await page.eval('document.querySelector("dialog[open] .pairing input[type=checkbox]").click()');
      await click('Add “First-start DNS”','dialog[open]');
      await until(()=>page.eval('[...document.querySelectorAll("dialog[open] button")].some(b=>__norm(b.innerText)==="Done")'),'paired confirmation',30000);
      await click('Done','dialog[open]');
      await until(async()=>{const reply=await fixture.api('/api/dashboard/nodes','GET',undefined,owner);return reply.value.some(row=>row.nodeId===id&&row.state==='answering')},'paired node answers',30000);
      assert.equal((await node.request('/api/accounts',{headers:{Authorization:`Bearer ${settings.CANARY_ADMIN_TOKEN}`}})).status,200);
      await node.stop(); node=await fixture.startNode(id,settings,nodePort);
      assert.equal(await until(()=>node.fingerprint(),'same fingerprint after restart',15000),fingerprint);
      await until(async()=>{const response=await fixture.api('/api/overview','GET',undefined,owner,invitation.id);return response.status===200},'restarted node serves a real dashboard read with unchanged environment',30000);
      return {realNodeStarted:true,exportedCredentialsAccepted:true,pinnedNativeTLS:true,unchangedEnvironmentRestart:true};
    } finally {
      await node.stop();
      await until(()=>page.eval('!document.querySelector("dialog[open]")'), 'pairing completed and dialog closed',15000);
    }
  });
}
