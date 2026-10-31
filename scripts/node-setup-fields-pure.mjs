// Regression coverage for the user's Portainer setup report: the node URL is a remote destination, not a
// local socket binding. Portainer's basic editor has separate variable-name and value inputs.
// Run only through scripts/bounded_validation.py. No browser, files, storage, network or real credentials.
import assert from 'node:assert/strict';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
process.env.TSX_DISABLE_CACHE = '1';
globalThis.document = { addEventListener() {}, hidden: false };
globalThis.location = { origin: 'https://dashboard.example.test', protocol: 'https:', hash: '', pathname: '/' };
globalThis.fetch = () => { throw new Error('This fixture must not contact a network'); };
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(import.meta.url);
const React = require('react');
const { renderToStaticMarkup } = await import('react-dom/server');
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'node-setup-fields', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = path => scope.import(pathToFileURL(resolve(root, 'apps/web/src', path)).href, import.meta.url);
const onboarding = await load('onboarding.ts');
const wizard = await load('views/NodeWizard.tsx');
const ADMIN = 'fixture-only-admin-abcdefghijklmnopqrstuvwxyz';
const CLUSTER = 'fixture-only-cluster-0123456789abcdefghijkl';
const ENROLL = 'fixture-only-enrollment-0123456789abcdefgh';
const secrets = { admin: ADMIN, cluster: CLUSTER };
const base = { nodeId: 'DOCKER02', name: 'Office', imageTag: 'latest', secrets };
const target = (url, bind) => onboarding.directTarget(url, bind);
const blocks = (direct, platform, reveal = true) => onboarding.setupGuide({ ...base, direct, platform }, reveal);
const source = name => readFileSync(resolve(root, name), 'utf8');
const noop = () => {};
let failures = 0;
async function check(name, fn) {
  try { await fn(); console.log(`PASS ${name}`); }
  catch (error) { failures++; console.error(`FAIL ${name}: ${String(error.message).replaceAll(ADMIN, '[redacted]').replaceAll(CLUSTER, '[redacted]').replaceAll(ENROLL, '[redacted]')}`); }
}

await check('remote IPv4, DNS and IPv6 destinations never become a local bind address implicitly', () => {
  for (const url of ['https://100.99.98.102:3100', 'https://node.example.test:7443', 'https://[fd00::9]:3100']) {
    const t = target(url);
    assert.equal(t.url, url);
    assert.equal(t.host, '0.0.0.0');
    assert.equal(t.listen, '0.0.0.0');
    assert.equal(t.allInterfaces, true);
  }
});

await check('explicit local bindings are separate, IPv6 is quoted and the verification URL stays the node URL', () => {
  const url = 'https://node-via-vpn.example.test:7443';
  for (const [input, host, listen] of [['192.168.10.20', '192.168.10.20', '192.168.10.20'], ['::', '[::]', '::'], ['[fd00::1]', '[fd00::1]', 'fd00::1']]) {
    const t = target(url, input);
    assert.deepEqual([t.url, t.host, t.listen], [url, host, listen]);
    for (const platform of ['docker', 'compose', 'portainer', 'vm']) {
      const all = blocks(t, platform).map(block => block.text).join('\n');
      if (platform === 'docker') assert.ok(all.includes(`-p ${host.startsWith('[') ? `'${host}:7443:3100'` : `${host}:7443:3100`}`));
      else if (platform === 'vm') assert.ok(all.includes(`CANARY_HOST="${listen}"`));
      else assert.ok(all.includes(`"${host}:7443:3100"`));
      assert.ok(!all.includes('node-via-vpn.example.test:7443:3100'));
      assert.equal(t.url, url);
    }
  }
});

await check('local binding validation refuses names, ports, URLs and shell or YAML operators', () => {
  for (const bad of ['node.example.test', 'https://10.0.0.1', '10.0.0.1:3100', '1.2.3.999', '-p', '0.0.0.0\nX=x', '$(echo x)', '::1%eth0', '[::1]:3100', '0x7f000001']) {
    assert.equal(typeof target('https://node.example.test:3100', bad), 'string', 'invalid local binding accepted');
  }
  assert.equal(target('https://node.example.test:3100', '').host, '0.0.0.0');
});

await check('every new-node platform publishes the independent bind address and preserves TLS and credentials', () => {
  const t = target('https://100.99.98.102:3100');
  for (const platform of ['docker', 'compose', 'portainer', 'vm']) {
    const all = blocks(t, platform).map(block => block.text).join('\n');
    assert.ok(!all.includes('100.99.98.102:3100:3100'));
    assert.ok(!all.includes('CANARY_HOST="100.99.98.102"'));
    assert.ok(all.includes('CANARY_TLS_MODE'));
    assert.ok(all.includes(ADMIN) && all.includes(CLUSTER), 'bootstrap credentials changed');
    const hidden = JSON.stringify(blocks(t, platform, false));
    assert.ok(!hidden.includes(ADMIN) && !hidden.includes(CLUSTER), 'masked output leaked a credential');
  }
});

await check('Portainer structured names and values match the bulk env file without equals or newline in a value', () => {
  for (const setup of [{ ...base, direct: target('https://node.example.test:3100') }, { ...base, outbound: { connectionUrl: 'https://connect.example.test', token: ENROLL } }, { ...base, secrets: null, outbound: { connectionUrl: 'https://connect.example.test', token: ENROLL } }]) {
    const real = onboarding.setupGuide({ ...setup, platform: 'portainer' }, true).find(block => block.key === (setup.secrets ? 'variables' : 'env'));
    const masked = onboarding.setupGuide({ ...setup, platform: 'portainer' }, false).find(block => block.key === real.key);
    assert.ok(Array.isArray(real.variables), 'missing individual variable fields');
    const expected = setup.secrets ? [['CANARY_ADMIN_TOKEN', ADMIN], ['CANARY_CLUSTER_TOKEN', CLUSTER], ...(setup.outbound ? [['CANARY_DASHBOARD_ENROLL_TOKEN', ENROLL]] : [])] : [['CANARY_DASHBOARD_ENROLL_TOKEN', ENROLL]];
    assert.equal(real.variables.length, expected.length);
    for (let index = 0; index < expected.length; index++) {
      const [name, value] = expected[index];
      const row = real.variables[index];
      assert.ok(row.name === name && row.value === value && row.secret === true, 'a variable name/value was not copied verbatim');
      assert.ok(!row.value.startsWith(`${name}=`) && !row.value.includes('\n'), 'value contains env-file syntax');
      assert.ok(real.text.includes(`${name}=${value}\n`), 'bulk env bytes differ from row values');
      assert.ok(masked.variables[index].name === name && masked.variables[index].value === onboarding.MASK, 'row preview must be masked');
    }
  }
});

await check('Portainer variable controls expose separate names and masked values without revealing credentials', () => {
  const markup = renderToStaticMarkup(React.createElement(wizard.SetupGuide, { setup: { ...base, direct: target('https://node.example.test:3100') }, platform: 'portainer', onPlatform: noop, onHandled: noop }));
  for (const value of [ADMIN, CLUSTER, ENROLL]) assert.ok(!markup.includes(value), 'credential present in static markup');
  for (const name of ['CANARY_ADMIN_TOKEN', 'CANARY_CLUSTER_TOKEN']) {
    assert.ok(markup.includes(`Copy name for ${name}`), 'missing individual name copy');
    assert.ok(markup.includes(`Copy value for ${name}`), 'missing individual value copy');
  }
  assert.ok(markup.includes('Name') && markup.includes('Value'));
});

await check('wizard labels the destination as the node URL, with an independent local binding input', () => {
  const markup = renderToStaticMarkup(React.createElement(wizard.NewNodeDeploy, { draft: { label: 'Office', nodeId: 'DOCKER02', address: 'https://100.99.98.102:3100', bindAddress: '192.168.10.20', platform: 'portainer' }, onDraft: noop, secrets, onboarding: { data: { imageTag: 'latest', containerPlatforms: ['linux/amd64'], nativeDownloadAvailable: false }, loading: false, reload: noop }, taken: { nodes: [], invitations: [] }, onNext: noop, onUse: noop, onCancel: noop }));
  assert.ok(markup.includes('Node URL'));
  assert.ok(markup.includes('Publish or listen address on the node host'));
  assert.ok(markup.includes('192.168.10.20:3100:3100'));
  assert.ok(!markup.includes('100.99.98.102:3100:3100'));
  const registry = source('apps/web/src/views/Registry.tsx');
  assert.ok(registry.includes('initialUrl={deployed.url}'), 'verification must start with the node URL');
  const view = source('apps/web/src/views/NodeWizard.tsx');
  assert.ok(view.includes('directTarget(draft.address, draft.bindAddress)'), 'binding must be passed independently');
});

await check('copy controls take a raw row value and only report handling after a successful secret copy', () => {
  const view = source('apps/web/src/views/NodeWizard.tsx');
  assert.ok(/value=\{realVariable\.name\}/u.test(view), 'name copy must take only the raw variable name');
  assert.ok(/value=\{realVariable\.value\}/u.test(view), 'value copy must take only the raw variable value');
  assert.ok(view.includes("variable.secret && (!includesInvitation || variable.name === 'CANARY_DASHBOARD_ENROLL_TOKEN')"), 'a row copy counts as handling the invitation only when it carries the enrollment token; direct bootstrap copies still bind their own ID');
  assert.doesNotMatch(source('apps/web/src/onboarding.ts'), /localStorage|sessionStorage|fetch\(/u);
});
await check('Docker filtering guidance differs from native VM firewall guidance and keeps default IPv4 explicit', () => {
  for (const platform of ['docker', 'compose', 'portainer', 'vm']) {
    const markup = renderToStaticMarkup(React.createElement(wizard.NewNodeDeploy, { draft: { label: 'Office', nodeId: 'DOCKER02', address: 'https://100.99.98.102:3100', platform }, onDraft: noop, secrets, onboarding: { data: { imageTag: 'latest', containerPlatforms: ['linux/amd64'], nativeDownloadAvailable: false }, loading: false, reload: noop }, taken: { nodes: [], invitations: [] }, onNext: noop, onUse: noop, onCancel: noop }));
    // Compare visible words: renderToStaticMarkup need not emit React's hydration comment separators.
    const words = markup.replace(/<!--[\s\S]*?-->/gu, '').replace(/<[^>]+>/gu, ' ').replace(/\s+/gu, ' ');
    const advice = onboarding.bindingAccessNote(platform);
    if (platform === 'vm') {
      assert.ok(advice.includes('host’s firewall') && !advice.includes('DOCKER-USER'));
      assert.ok(!markup.includes('INPUT/UFW'), 'native listener must not inherit the Docker warning');
    } else {
      assert.ok(markup.includes('INPUT/UFW') && markup.includes('DOCKER-USER') && markup.includes('iptables backend'));
      assert.ok(words.includes('every IPv4 interface'), 'default Docker mapping is explicitly IPv4');
      assert.ok(!words.includes('every IPv6 interface'), 'no default IPv6 exposure claim');
    }
  }
  const guide = source('docs/node-onboarding.md');
  assert.ok(guide.includes('INPUT/UFW') && guide.includes('DOCKER-USER') && guide.includes('nftables backend'));
});
scope.unregister();
process.exitCode = failures ? 1 : 0;
