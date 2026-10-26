// Test client for owned loopback fixtures only. The supplied pin is read from
// the private child process log. No HTTP bytes are written before the exact
// certificate and its lifetime have been checked on this fresh TLS connection.
import { createHash, timingSafeEqual } from 'node:crypto';
import { request } from 'node:https';

export function pinnedFixtureFetch(address, pin, options = {}) {
  if (!/^[0-9a-f]{64}$/u.test(pin)) throw Error('Fixture needs its complete certificate fingerprint');
  const url = new URL(address);
  if (url.protocol !== 'https:' || url.hostname !== '127.0.0.1') throw Error('TLS fixture must be owned loopback');
  return new Promise((resolve, reject) => {
    const req = request(url, { agent: false, rejectUnauthorized: false, minVersion: 'TLSv1.3', method: options.method ?? 'GET', headers: options.headers ?? {}, timeout: 6000 }, res => {
      const chunks = []; let size = 0;
      res.on('data', chunk => { size += chunk.length; if (size > 65536) res.destroy(Error('TLS fixture response exceeded bound')); else chunks.push(chunk); });
      res.on('error', reject);
      res.on('end', () => {
        const body = Buffer.concat(chunks).toString('utf8');
        resolve({ status: res.statusCode, ok: res.statusCode >= 200 && res.statusCode < 300, text: async () => body, json: async () => JSON.parse(body) });
      });
    });
    req.on('error', reject);
    req.on('timeout', () => req.destroy(Error('TLS fixture timed out')));
    req.on('socket', socket => socket.once('secureConnect', () => {
      const cert = socket.getPeerCertificate();
      const now = Date.now();
      if (!cert.raw || !timingSafeEqual(createHash('sha256').update(cert.raw).digest(), Buffer.from(pin, 'hex')) || !(Date.parse(cert.valid_from) <= now && now < Date.parse(cert.valid_to))) {
        req.destroy(Error('TLS fixture certificate refused'));
        return;
      }
      req.end(options.body);
    }));
  });
}
