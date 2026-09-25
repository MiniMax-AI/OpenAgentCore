// Build and serve the production console with private, disposable account state.
import { spawn } from 'node:child_process';
import { mkdir, mkdtemp, writeFile, rm } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { ADMIN_TOKEN } from './fixture-settings.mjs';

const root = join(homedir(), '.parsar', 'tests');
await mkdir(root, { recursive: true });
const directory = await mkdtemp(join(root, 'console-e2e-'));
await mkdir(join(directory, 'account'), { mode: 0o700 });
await writeFile(join(directory, 'admin.key'), ADMIN_TOKEN, { mode: 0o600 });
await mkdir(join(directory, 'payload'));
// Only the installer download/command contract is exercised; never run this payload.
await writeFile(join(directory, 'payload', 'node-install.pyz'), 'Synthetic installer payload for acceptance only.');
let child;
let stopping = false;
async function stop() { if (stopping) return; stopping = true; child?.kill('SIGTERM'); }
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, stop);
function run(command, args, env = process.env) {
  return new Promise((resolve, reject) => {
    child = spawn(command, args, { stdio: 'inherit', env });
    child.on('error', reject);
    child.on('exit', code => code === 0 || stopping ? resolve() : reject(new Error(`${command} exited ${code}`)));
  });
}
try {
  await run(process.env.GO ?? 'go', ['build', '-o', join(directory, 'console'), './services/core-console']);
  const buildArgs = ['--filter', '@agents-core-web/web', 'exec', 'vite', 'build', '--outDir', join(directory, 'dist')];
  if (!stopping) await run(process.env.npm_execpath ? process.execPath : 'pnpm', process.env.npm_execpath ? [process.env.npm_execpath, ...buildArgs] : buildArgs);
  const port = process.env.AGENTS_WEB_PORT ?? '19619';
  if (!stopping) await run(join(directory, 'console'), [], { ...process.env, CORE_CONSOLE_ADDR: `127.0.0.1:${port}`, CORE_CONSOLE_ORIGIN: `http://127.0.0.1:${port}`, CORE_CONSOLE_UPSTREAM: `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? '18611'}`, CORE_CONSOLE_ADMIN_TOKEN_FILE: join(directory, 'admin.key'), CORE_CONSOLE_AUTH_MODE: 'account', CORE_CONSOLE_STATE_DIR: join(directory, 'account'), CORE_CONSOLE_DIST: join(directory, 'dist'), CORE_CONSOLE_NODE_PAYLOAD_DIR: join(directory, 'payload') });
} finally { await rm(directory, { recursive: true, force: true }); }
