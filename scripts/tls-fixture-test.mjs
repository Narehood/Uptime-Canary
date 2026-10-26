// Owned loopback regression for the browser harness's pinned HTTPS client.
import assert from 'node:assert/strict';
import { createHash, X509Certificate } from 'node:crypto';
import { once } from 'node:events';
import { readFileSync } from 'node:fs';
import { createServer } from 'node:https';
import { pinnedFixtureFetch } from './tls-fixture.mjs';

const cert = readFileSync(new URL('../apps/proof/fixtures/localhost-cert.pem', import.meta.url));
const key = readFileSync(new URL('../apps/proof/fixtures/localhost-key.pem', import.meta.url));
const pin = createHash('sha256').update(new X509Certificate(cert).raw).digest('hex');
let requests = 0;
const server = createServer({ cert, key, minVersion: 'TLSv1.3' }, (req, res) => {
  requests++;
  assert.equal(req.headers.authorization, 'Bearer private-fixture-only');
  res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"authenticated":true}');
});
server.on('tlsClientError', () => {});
server.listen(0, '127.0.0.1');
await once(server, 'listening');
try {
  const url = `https://127.0.0.1:${server.address().port}/proof`;
  const options = { headers: { Authorization: 'Bearer private-fixture-only' } };
  await assert.rejects(() => pinnedFixtureFetch(url, '0'.repeat(64), options), /certificate refused/u);
  assert.equal(requests, 0, 'HTTP headers reached a certificate with the wrong pin');
  const response = await pinnedFixtureFetch(url, pin, options);
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { authenticated: true });
  assert.equal(requests, 1);
  console.log('PASS pinned fixture client authenticates the certificate before sending any HTTP credential');
} finally {
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
}
