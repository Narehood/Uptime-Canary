import { randomBytes } from 'node:crypto';
import { writeFile, mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const root = new URL('../', import.meta.url);
await mkdir(new URL('.canary/', root), { recursive: true });
const destination = new URL('.env', root);
const contents = [
  '# Generated locally. Keep this file private; it is ignored by Git.',
  'CANARY_NODE_ID=canary-1',
  'CANARY_NODE_NAME=Canary One',
  'CANARY_HOST=127.0.0.1',
  'CANARY_PORT=3100',
  'CANARY_DB=.canary/canary.db',
  `CANARY_ADMIN_TOKEN=${randomBytes(32).toString('hex')}`,
  `CANARY_CLUSTER_TOKEN=${randomBytes(32).toString('hex')}`,
  'CANARY_PEERS=[]',
  'CANARY_CORS_ORIGINS=http://localhost:5173,http://127.0.0.1:5173,http://localhost:8080,http://127.0.0.1:8080',
  '',
].join('\n');
try {
  await writeFile(destination, contents, { encoding: 'utf8', flag: 'wx', mode: 0o600 });
  console.log(`Created ${fileURLToPath(destination)} with private random credentials.`);
  console.log('Run npm run dev to start the headless node. npm run canary -- status uses its local credentials.');
} catch (error) {
  if (error?.code !== 'EEXIST') throw error;
  console.error('An .env file already exists. It was left unchanged.');
  process.exitCode = 1;
}
