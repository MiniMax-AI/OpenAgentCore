#!/usr/bin/env node
// No model requests: validate installed native engines and their startup protocols.
import { spawn, spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, realpath, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';

const options = {};
for (let i = 2; i < process.argv.length; i += 2) {
  const key = process.argv[i];
  if (!['--codex-binary', '--claude-runtime'].includes(key) || !process.argv[i + 1]) {
    throw new Error('Expected --codex-binary PATH or --claude-runtime PATH');
  }
  options[key] = process.argv[i + 1];
}
const limit = 1024 * 1024;
const failure = code => Object.assign(new Error(code), { safeCode: code });
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

async function codexSmoke(root) {
  const binary = options['--codex-binary'] ?? 'codex';
  const home = join(root, 'codex-home');
  const workspace = join(root, 'workspace');
  await mkdir(home); await mkdir(workspace);
  const env = { ...process.env, CODEX_HOME: home };
  delete env.OPENAI_API_KEY; delete env.CODEX_API_KEY;
  const version = spawnSync(binary, ['--version'], { env, encoding: 'utf8', timeout: 15000, maxBuffer: limit });
  if (version.error || version.status !== 0 || version.stdout.trim() !== 'codex-cli 0.153.4') throw failure('codex_native_version');
  const child = spawn(binary, ['app-server', '--listen', 'stdio://'], { env, cwd: workspace, stdio: ['pipe', 'pipe', 'pipe'] });
  const closed = new Promise(resolve => child.once('close', resolve));
  let pending;
  let bytes = 0;
  let broken;
  const rejectPending = code => { broken = failure(code); pending?.reject(broken); };
  child.on('error', () => rejectPending('codex_spawn'));
  child.stdin.on('error', () => rejectPending('codex_stdin'));
  child.stderr.resume();
  child.stdout.on('data', chunk => { bytes += chunk.length; if (bytes > limit) rejectPending('codex_output_limit'); });
  const lines = createInterface({ input: child.stdout });
  lines.on('line', line => {
    try {
      const message = JSON.parse(line);
      if (pending && message.id === pending.id) {
        if (message.error) pending.reject(failure('codex_rpc'));
        else pending.resolve(message.result);
      }
    } catch { rejectPending('codex_invalid_json'); }
  });
  child.on('close', () => rejectPending('codex_early_exit'));
  let sequence = 0;
  const request = async (method, params) => {
    if (broken) throw broken;
    const id = ++sequence;
    let timer;
    try {
      return await new Promise((resolve, reject) => {
        pending = { id, resolve, reject };
        timer = setTimeout(() => reject(failure('codex_rpc_timeout')), 15000);
        child.stdin.write(JSON.stringify({ id, method, params }) + '\n');
      });
    } finally { clearTimeout(timer); pending = undefined; }
  };
  try {
    await request('initialize', { clientInfo: { name: 'oac-native-smoke', version: '1' }, capabilities: { experimentalApi: true } });
    child.stdin.write(JSON.stringify({ method: 'initialized' }) + '\n');
    const result = await request('thread/start', { cwd: workspace, approvalPolicy: 'never', sandbox: 'danger-full-access', persistExtendedHistory: true });
    if (typeof result?.thread?.id !== 'string' || !result.thread.id) throw failure('codex_thread_identity');
    return { status: 'passed', version: '0.153.4', initialize: true, thread_start: true };
  } finally {
    child.stdin.end();
    if (await Promise.race([closed.then(() => true), delay(5000).then(() => false)]) === false) {
      if (process.platform === 'win32' && child.pid) spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', timeout: 5000 });
      else child.kill('SIGKILL');
      await Promise.race([closed, delay(5000)]);
    }
    lines.close();
  }
}

async function claudeSmoke() {
  const root = resolve(options['--claude-runtime'] ?? 'packages/claude-sdk-adapter');
  const manifest = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'));
  if (manifest.dependencies?.['@anthropic-ai/claude-agent-sdk']?.split('(')[0] !== '0.3.269') throw failure('claude_package_version');
  const result = spawnSync(process.execPath, [join(root, 'dist', 'runtime_check.js')], {
    cwd: root, encoding: 'utf8', timeout: 20000, maxBuffer: limit,
  });
  if (result.error || result.status !== 0) throw failure('claude_runtime_unavailable');
  let info;
  try { info = JSON.parse(result.stdout); } catch { throw failure('claude_invalid_receipt'); }
  if (info.type !== 'runtime_ready' || info.sdk !== '0.3.269' || info.native !== '2.1.269 (Claude Code)' || !info.features?.includes('local_runtime_v2')) throw failure('claude_runtime_identity');
  return { status: 'passed', sdk: info.sdk, native: info.native };
}

const root = await realpath(await mkdtemp(join(tmpdir(), 'oac-native-smoke-')));
const report = { platform: process.platform, arch: process.arch, model_requests: 0 };
try {
  for (const [name, run] of [['codex', () => codexSmoke(root)], ['claude', claudeSmoke]]) {
    try { report[name] = await run(); }
    catch (error) { report[name] = { status: 'failed', code: error.safeCode ?? 'native_smoke_unavailable' }; process.exitCode = 1; }
  }
} finally {
  await rm(root, { recursive: true, force: true });
}
// Deliberately exclude child stdout/stderr and request contents from CI output.
console.log(JSON.stringify(report));
