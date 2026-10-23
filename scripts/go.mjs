import { existsSync, mkdirSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { resolve, join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const windows = process.platform === 'win32';
const portableGo = join(root, '.canary', 'tools', 'go', 'bin', windows ? 'go.exe' : 'go');
const go = process.env.CANARY_GO || (existsSync(portableGo) ? portableGo : 'go');
const [command, ...args] = process.argv.slice(2);
const binaryDir = join(root, '.canary', 'bin');
mkdirSync(binaryDir, { recursive: true });
const env = { ...process.env, CGO_ENABLED: '0', GOTOOLCHAIN: 'local' };

async function run(executable, argv, overrides = {}) {
  return new Promise((resolveRun, reject) => {
    const child = spawn(executable, argv, {
      cwd: root, env: { ...env, ...overrides }, stdio: 'inherit', windowsHide: true, shell: false,
    });
    const interrupt = () => child.kill('SIGINT');
    const terminate = () => child.kill('SIGTERM');
    process.on('SIGINT', interrupt);
    process.on('SIGTERM', terminate);
    const cleanup = () => {
      process.off('SIGINT', interrupt);
      process.off('SIGTERM', terminate);
    };
    child.once('error', error => {
      cleanup();
      reject(new Error(error.code === 'ENOENT' ? 'Go was not found. Install Go or set CANARY_GO to its executable.' : 'Cannot start Go command.'));
    });
    child.once('exit', (code, signal) => {
      cleanup();
      if (code === 0) resolveRun();
      else reject(Object.assign(new Error(signal ? `Command stopped by ${signal}.` : `Command failed with exit code ${code}.`), { exitCode: code || 1 }));
    });
  });
}

async function main() {
  if (command === 'test') return run(go, ['test', './apps/node-go/...', ...args]);
  if (command === 'vet') return run(go, ['vet', './apps/node-go/...', ...args]);
  if (command === 'version') return run(go, ['version']);
  const name = command === 'serve' ? 'canary-dev' : command === 'cli' ? 'canary-cli' : 'canary';
  const linux = command === 'build-linux';
  const binary = join(binaryDir, linux ? 'canary-linux-amd64' : `${name}${windows ? '.exe' : ''}`);
  if (!['build', 'build-linux', 'serve', 'cli'].includes(command)) throw new Error('Use build, build-linux, serve, cli, test, vet, or version.');
  await run(go, ['build', '-trimpath', '-o', binary, './apps/node-go/cmd/canary'], linux ? { GOOS: 'linux', GOARCH: 'amd64' } : {});
  if (command === 'build' || linux) return;
  if (existsSync(join(root, '.env'))) process.loadEnvFile(join(root, '.env'));
  await run(binary, command === 'serve' ? ['serve', ...args] : args, process.env);
}

main().catch(error => {
  console.error(error.message);
  process.exitCode = error.exitCode || 1;
});
