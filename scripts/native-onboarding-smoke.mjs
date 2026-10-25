#!/usr/bin/env node
// Native end-to-end bootstrap against a local transport fixture. No model calls.
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn, execFileSync } from 'node:child_process';
import { createReadStream, existsSync } from 'node:fs';
import { readFile, readdir } from 'node:fs/promises';
import { join, resolve } from 'node:path';

const root = process.env.RUNNER_TEMP;
if (!root) throw new Error('RUNNER_TEMP is required');
const windows = process.platform === 'win32';
const bundle = join(root, 'native-installer');
const manifest = JSON.parse(await readFile(join(bundle, 'bundle.json'), 'utf8'));
const protocol = (await readFile('internal/agentdaemon/proto/version.go', 'utf8')).match(/const Version = "([^"]+)"/)[1];
const environment = randomUUID(), session = randomUUID(), device = randomUUID();
const workspace = join(root, 'onboarding-workspace');
const installation = join(root, 'onboarding-installation');
// Core serves the base archive and one archive per component, as the release publishes them.
const archives = join(root, 'native-archives');
const platform = `${manifest.os}-${manifest.arch}`;
const checksums = new Map(), downloaded = new Set();
let componentRequests = 0;
async function checksum(name) {
  if (!checksums.has(name)) {
    const digest = createHash('sha256');
    for await (const chunk of createReadStream(join(archives, `${name}.tar.gz`))) digest.update(chunk);
    checksums.set(name, digest.digest('hex'));
  }
  return checksums.get(name);
}
let secret, connected = false, connections = 0, failClaim = true;
const authorization = 'fixture-install-authorization';
const server = createServer(async (request, response) => {
  const url = new URL(request.url, origin);
  const json = (status, value) => { response.writeHead(status, { 'Content-Type': 'application/json' }); response.end(JSON.stringify(value)); };
  const artifact = url.pathname.match(/\/install\/[^/]+\/([a-z0-9-]+)\.(sha256|tar\.gz)$/);
  if (artifact) {
    if (!existsSync(join(archives, `${artifact[1]}.tar.gz`))) return json(404, {});
    if (artifact[2] === 'sha256') { response.setHeader('Content-Type', 'text/plain; charset=utf-8'); return response.end(await checksum(artifact[1])+'\n'); }
    downloaded.add(artifact[1]); if (artifact[1] !== platform) componentRequests++;
    return createReadStream(join(archives, `${artifact[1]}.tar.gz`)).pipe(response);
  }
  if (url.pathname.endsWith('bootstrap.sh') || url.pathname.endsWith('bootstrap.ps1')) return createReadStream(resolve('services/core/internal/nativeinstaller/assets', url.pathname.split('/').at(-1))).pipe(response);
  const body = []; for await (const chunk of request) body.push(chunk);
  if (url.pathname.endsWith('/installation') || url.pathname.endsWith('/claim')) {
    if (request.headers.authorization !== `Bearer ${authorization}`) return json(401, {});
    if (url.pathname.endsWith('/installation')) return json(200, { version: manifest.daemon_version, protocol_version: protocol, environment_id: environment, remote_url: remote, workspace_directory: workspace, harness: 'codex' });
    const input = JSON.parse(Buffer.concat(body));
    if (secret && secret !== input.executor_token) return json(409, {});
    secret = input.executor_token;
    if (failClaim) { failClaim = false; request.socket.destroy(); return; }
    response.writeHead(204); return response.end();
  }
  if (!secret || request.headers.authorization !== `Bearer ${secret}`) return json(401, {});
  if (url.pathname.endsWith('/enroll')) return json(200, { device_id: device, session_id: session, environment_id: environment, workspace_directory: workspace });
  if (url.pathname.endsWith('/bootstrap')) return json(200, { device_id: device, ws_url: remote, heartbeat_seconds: 15 });
  if (url.pathname.endsWith('/connection')) return json(200, { environment_id: environment, status: connected ? 'connected' : 'disconnected' });
  return json(404, {});
});
const sockets = new Set();
server.on('upgrade', (request, socket) => {
  if (request.headers.authorization !== `Bearer ${secret}`) { socket.destroy(); return; }
  const accept = createHash('sha1').update(request.headers['sec-websocket-key']+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
  socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
  sockets.add(socket); connected = true; connections++;
  socket.on('data', () => {});
  socket.on('error', () => {});
  socket.on('close', () => { sockets.delete(socket); connected = sockets.size > 0; });
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const remote = origin.replace('http:', 'ws:')+'/api/v1/agent-daemon/ws';
const base = `${origin}/api/v1/agent-daemon/install/${manifest.daemon_version}`;
function run(executable, args, input = '', env = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, { env: { ...process.env, OAC_RUNTIME_HOME: join(root, 'unused-onboarding-home'), ...env }, stdio: ['pipe', 'pipe', 'pipe'] });
    let output = '';
    child.stdout.on('data', chunk => { output += chunk; }); child.stderr.on('data', chunk => { output += chunk; });
    child.stdin.on('error', () => {}); child.stdin.end(input);
    const timer = setTimeout(() => { child.kill(); reject(new Error('Native onboarding did not settle: '+(secret ? output.replaceAll(secret, '[redacted]') : output).slice(-4096))); }, 180000);
    child.on('error', reject);
    child.on('close', code => { clearTimeout(timer); if (secret) assert.ok(!output.includes(secret), 'Credential leaked into terminal'); resolve({ code, output }); });
  });
}
const executable = join(bundle, windows ? 'oac-daemon.exe' : 'oac-daemon');
const args = ['install', '--onboard-url', `${origin}/api/v1/agent-daemon/installation`, '--install-dir', installation];
process.env.OAC_INSTALL_GRANT = authorization;
try {
  assert.notEqual((await run(executable, [...args, '--non-interactive', '--harness', 'codex'], '', { OAC_INSTALL_GRANT: 'expired' })).code, 0);
  assert.notEqual((await run(executable, [...args, '--non-interactive'])).code, 0, 'Missing Harness must not prompt');
  assert.notEqual((await run(executable, [...args, '--non-interactive', '--harness', 'codex'])).code, 0, 'Lost claim response should fail safely');
  const saved = JSON.parse(await readFile(join(installation, 'daemon', 'executor-credential.json'), 'utf8'));
  assert.equal(saved.executor_token, secret, 'Secret must survive a lost response');
  // A failed first installation leaves no user entry, so it pins nothing; its components stay for the next run.
  const store = join(root, 'unused-onboarding-home', 'components', manifest.daemon_version);
  assert.deepEqual(existsSync(join(store, 'users')) ? await readdir(join(store, 'users')) : [], [], 'A failed installation is still registered as a user');
  const script = resolve(`services/core/internal/nativeinstaller/assets/bootstrap.${windows ? 'ps1' : 'sh'}`);
  const bootstrapArgs = windows ? ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', script, '-Base', base] : [script, base];
  // Select every Harness of this platform so each Harness archive passes the installer's extraction and readiness checks.
  const harnesses = windows ? ['codex', 'claude'] : ['codex', 'claude', 'minimax'];
  const result = await run(windows ? 'powershell.exe' : 'bash', [...bootstrapArgs, '--install-dir', installation], harnesses.join(',') + '\n\n');
  assert.equal(result.code, 0, `Interactive bootstrap failed: ${result.output}`);
  assert.match(result.output, /Daemon connection: connected/);
  // The failed runs installed Node and Codex from the bundle; the bootstrap reuses them.
  assert.deepEqual([...downloaded].sort(), [platform, ...harnesses.filter(name => name !== 'codex').map(name => `${platform}-${name}`)].sort(), 'Only the base and the missing Harnesses may be downloaded');
  assert.match(result.output, /Model configuration: not checked/);
  // Components live once per machine and version, outside the installation.
  assert.ok(existsSync(join(store, 'codex', '.oac-install.json')), 'Shared Codex component is missing');
  assert.ok(!existsSync(join(installation, 'components')), 'The installation holds its own component copy');
  assert.ok(!existsSync(join(root, 'unused-onboarding-home', 'native-download', manifest.daemon_version, `${platform}-codex.tar.gz.part`)), 'An archive remains after installation');
  assert.equal(connections, 1);
  const downloads = componentRequests;
  const repeat = await run(windows ? 'powershell.exe' : 'bash', [...bootstrapArgs, '--install-dir', installation, '--non-interactive', '--harness', 'codex']);
  assert.equal(repeat.code, 0, `Repeat failed: ${repeat.output}`);
  assert.equal(connections, 1, 'Repeated installation created a duplicate daemon');
  assert.equal(componentRequests, downloads, 'A repeated installation downloaded a component');
  const uninstall = await run(join(installation, 'bin', windows ? 'oac-daemon.exe' : 'oac-daemon'), ['uninstall'], 'y\n');
  assert.equal(uninstall.code, 0, `Uninstall failed: ${uninstall.output}`);
  assert.match(uninstall.output, /Revoke/);
  assert.match(uninstall.output, /Shared components removed/);
  assert.ok(!existsSync(store), 'Uninstall left the shared components');
  // Windows finishes deleting its own executable after the command exits.
  for (let wait = 0; existsSync(installation) && wait < 100; wait++) await new Promise(resolve => setTimeout(resolve, 100));
  assert.ok(!existsSync(installation), 'Uninstall left the installation');
  assert.ok(existsSync(workspace), 'Uninstall deleted the workspace');
  console.log(JSON.stringify({ installation: 'passed', connection: 'passed', interactive: 'passed', retry: 'passed', repeat: 'passed', uninstall: 'passed', authentication: 'passed', model_requests: 0 }));
} finally {
  // The installed executable always acts on its own installation.
  delete process.env.OAC_RUNTIME_HOME;
  const installed = join(installation, 'bin', windows ? 'oac-daemon.exe' : 'oac-daemon');
  try { execFileSync(installed, ['stop'], { stdio: 'ignore', timeout: 15000 }); } catch { /* A failed installation may never have started. */ }
  for (const socket of sockets) socket.destroy();
  await new Promise(resolve => server.close(resolve));
}
