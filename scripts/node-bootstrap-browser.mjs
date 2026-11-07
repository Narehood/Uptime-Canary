// Actual React export controls against the private dashboard. Clipboard and
// downloads stay in page memory; no user deployment or external node is used.
import assert from 'node:assert/strict';
import { writeFileSync } from 'node:fs';

export async function reviewNodeBootstrap({ fixture, page, step, until, fill, click, ready, owner, secrets }) {
  const q = JSON.stringify, id = 'review-unpaired-dns';
  const caps = await fixture.api('/api/dashboard/onboarding', 'GET', undefined, owner);
  const writes = () => fixture.stats().paths.filter(row => row.method !== 'GET').length;
  let settings, bootstrapWidths;
  await step('Node exports: all platforms copy and download without dashboard setup or hidden inputs', async () => {
    try {
      fixture.previewOnboarding({ ...caps.value, outboundEnabled: false, connectionUrl: undefined, connectionTlsFingerprint: undefined });
      await page.eval('location.hash="#/nodes"'); await ready();
      await click('Add node');
      await until(() => page.eval('!!document.querySelector("dialog[open]")'), 'Add node opened');
      const pick = async label => {
        await ready();
        await page.eval(`(() => {const e=[...document.querySelectorAll('dialog[open] label')].find(e=>e.querySelector('input[type=radio]')&&__norm(e.innerText).startsWith(${q(label)}));if(!e)throw Error('choice missing');e.querySelector('input').click();})()`);
      };
      await pick('Deploy a new node'); await pick('The node reaches this dashboard');
      await fill('Name on this dashboard', 'Unpaired DNS', 'dialog[open]');
      await fill('Node ID', id, 'dialog[open]');
      await until(() => page.eval('document.querySelector("dialog[open]").innerText.includes("You can deploy this node now")'), 'separate deployment notice');
      assert.equal(await page.eval(`[...document.querySelectorAll('dialog[open] label')].some(e=>e.textContent.includes('Dashboard IP or HTTPS address'))`), false, 'no unrelated address input');
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
      let pair;
      const before = writes();
      for (const platform of ['Docker Compose', 'Portainer stack', 'Linux VM', 'Docker run']) {
        await click(platform, 'dialog[open]');
        const files = await page.eval('[...document.querySelectorAll("dialog[open] .guide button")].map(b=>b.getAttribute("aria-label")||"").filter(l=>l.startsWith("Download ")).map(l=>l.slice(9))');
        assert.ok(files.some(name => name.endsWith('.env')));
        for (const file of files) {
          const count = await page.eval('window.__bootstrapCopies.length');
          await action(`Copy ${file}`);
          const copied = await until(() => page.eval(`window.__bootstrapCopies.length>${count}&&window.__bootstrapCopies.at(-1)`), 'file copied');
          const saved = await page.eval('window.__bootstrapFiles.length');
          await action(`Download ${file}`);
          await until(() => page.eval(`window.__bootstrapFiles.length>${saved}`), 'file downloaded');
          assert.equal(await page.eval('window.__bootstrapFiles.at(-1).text()'), copied, `${platform}: exact downloaded bytes`);
          assert.doesNotMatch(copied, /INVITATION_TOKEN|\u2022{4}|CANARY_DASHBOARD_(?:URL|ENROLL_TOKEN)=/u);
          if (file.endsWith('.env')) {
            const env = Object.fromEntries(copied.split('\n').filter(line=>line&&!line.startsWith('#')).map(line=>{const n=line.indexOf('=');return [line.slice(0,n),line.slice(n+1).replace(/^"|"$/gu,'')]}));
            const tokens = [env.CANARY_ADMIN_TOKEN, env.CANARY_CLUSTER_TOKEN];
            assert.ok(tokens.every(token=>token?.length>=24));
            tokens.forEach(token=>secrets.add(token));
            if (!pair) pair=tokens; else assert.deepEqual(tokens,pair,'same credentials on every platform');
            assert.ok(!(await page.eval('document.querySelector("dialog[open] .guide").innerText')).includes(tokens[0]), 'preview masks the real token');
            await action(`Show private values in ${file}`);
            assert.ok((await page.eval('document.querySelector("dialog[open] .guide").innerText')).includes(tokens[0]), 'Show reveals the real token');
            await action(`Hide private values in ${file}`);
            assert.ok(!(await page.eval('document.querySelector("dialog[open] .guide").innerText')).includes(tokens[0]), 'Hide masks the real token again');
            settings = env;
          }
        }
      }
      // A used pair belongs to its ID, including while exporting without pairing.
      await fill('Node ID', 'review-unpaired-other', 'dialog[open]');
      const otherFile = await page.eval('[...document.querySelectorAll("dialog[open] .guide button")].find(b=>(b.getAttribute("aria-label")||"").startsWith("Download ")&&(b.getAttribute("aria-label")||"").endsWith(".env")).getAttribute("aria-label").slice(9)');
      await action(`Copy ${otherFile}`);
      const other = await page.eval('window.__bootstrapCopies.at(-1)');
      assert.ok(pair.every(token=>!other.includes(token)), 'changed ID gets different credentials');
      other.split('\n').filter(line=>/^CANARY_(?:ADMIN|CLUSTER)_TOKEN=/u.test(line)).forEach(line=>secrets.add(line.slice(line.indexOf('=')+1)));
      await fill('Name on this dashboard', '', 'dialog[open]');
      assert.ok(await page.eval('[...document.querySelectorAll("dialog[open] .guide button:not([role=tab])")].every(button=>button.disabled)'), 'clearing a required local field disables exports');
      await fill('Name on this dashboard', 'Unpaired DNS', 'dialog[open]');
      await fill('Node ID', id, 'dialog[open]');
      const restoredFile = await page.eval('[...document.querySelectorAll("dialog[open] .guide button")].find(b=>(b.getAttribute("aria-label")||"").startsWith("Download ")&&(b.getAttribute("aria-label")||"").endsWith(".env")).getAttribute("aria-label").slice(9)');
      await action(`Copy ${restoredFile}`);
      assert.ok(await page.eval(`window.__bootstrapCopies.at(-1).includes(${q(pair[0])})&&window.__bootstrapCopies.at(-1).includes(${q(pair[1])})`), 'original ID restores original credentials');
      assert.equal(writes(), before, 'all exports make zero API writes');
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
          document.querySelector('dialog[open] button[aria-label="Close"]')?.click();
        })()`);
        await until(()=>page.eval('!document.querySelector("dialog[open]")'),'bootstrap dialog closed');
      } finally { await page.send('Emulation.clearDeviceMetricsOverride'); }
    }
    return { platforms:4, copyMatchesDownload:true, hiddenInputsRequired:0, apiWrites:0, credentialBinding:true, widths:bootstrapWidths };
  });
  await step('Node exports: the exported standalone environment starts a real private node', async () => {
    assert.equal(settings.CANARY_NODE_ID,id);
    const node=await fixture.startNode(id,settings,await fixture.freePort());
    try {
      const result=await node.request('/api/accounts',{headers:{Authorization:`Bearer ${settings.CANARY_ADMIN_TOKEN}`}});
      assert.equal(result.status,200,'exported administrator token works');
      assert.equal(node.fingerprint(),null,'no pretend dashboard connection');
      return { realNodeStarted:true, exportedCredentialsAccepted:true, dashboardConnection:false };
    } finally { await node.stop(); }
  });
}
