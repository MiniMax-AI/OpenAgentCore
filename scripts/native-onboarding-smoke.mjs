#!/usr/bin/env node
// Native end-to-end bootstrap on Linux against a local transport fixture. No model calls.
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn, execFileSync } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { mkdir, readFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';

const root = process.env.RUNNER_TEMP;
if (!root) throw new Error('RUNNER_TEMP is required');
const bundle = join(root, 'native-installer');
const manifest = JSON.parse(await readFile(join(bundle, 'bundle.json'), 'utf8'));
const protocol = (await readFile('internal/agentdaemon/proto/version.go', 'utf8')).match(/const Version = "([^"]+)"/)[1];
const environment = randomUUID();
const resource = { tenant_id: randomUUID(), environment_id: environment, kind: 'enrollment', id: randomUUID(), generation: 1 };
const workspace = join(root, 'onboarding-workspace');
const installation = join(root, 'onboarding-installation');
const archive = join(root, 'onboarding.tar.gz');
execFileSync('tar', ['-czf', archive, '-C', bundle, '.']);
const digest = createHash('sha256');
for await (const chunk of createReadStream(archive)) digest.update(chunk);
const checksum = digest.digest('hex');
let secret, links = 0, unexpected = 0, failClaim = true;
const authorization = 'fixture-install-authorization';
const server = createServer(async (request, response) => {
  const url = new URL(request.url, origin);
  const json = (status, value) => { response.writeHead(status, { 'Content-Type': 'application/json' }); response.end(JSON.stringify(value)); };
  if (url.pathname.endsWith('.sha256')) { response.setHeader('Content-Type', 'text/plain; charset=utf-8'); return response.end(checksum+'\n'); }
  if (url.pathname.endsWith('.tar.gz')) return createReadStream(archive).pipe(response);
  if (url.pathname.endsWith('bootstrap.sh')) return createReadStream(resolve('services/core/internal/nativeinstaller/assets', url.pathname.split('/').at(-1))).pipe(response);
  const body = []; for await (const chunk of request) body.push(chunk);
  if (url.pathname.endsWith('/installation') || url.pathname.endsWith('/claim')) {
    if (request.headers.authorization !== `Bearer ${authorization}`) return json(401, {});
    if (url.pathname.endsWith('/installation')) return json(200, { version: manifest.daemon_version, protocol_version: protocol, environment_id: environment, remote_url: remote, workspace_directory: workspace });
    const input = JSON.parse(Buffer.concat(body));
    if (secret && secret !== input.executor_token) return json(409, {});
    secret = input.executor_token;
    if (failClaim) { failClaim = false; request.socket.destroy(); return; }
    response.writeHead(204); return response.end();
  }
  if (!secret || request.headers.authorization !== `Bearer ${secret}`) return json(401, {});
  if (url.pathname.endsWith('/enroll')) return json(200, { link_url: origin.replace('http:', 'ws:')+'/api/v1/sandbox-link', resource });
  if (url.pathname.endsWith('/connection')) return json(200, { environment_id: environment, status: sockets.size ? 'connected' : 'disconnected' });
  return json(404, {});
});
const sockets = new Set();
// oac-sandbox-io dials the Sandbox Link. The fixture never answers its Hello,
// so it redials after each handshake deadline; one live link is one service.
server.on('upgrade', (request, socket) => {
  if (new URL(request.url, origin).pathname !== '/api/v1/sandbox-link') { unexpected++; socket.destroy(); return; }
  const accept = createHash('sha1').update(request.headers['sec-websocket-key']+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
  socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
  sockets.add(socket); links = Math.max(links, sockets.size);
  socket.on('data', () => {});
  socket.on('error', () => {});
  socket.on('end', () => socket.destroy());
  socket.on('close', () => sockets.delete(socket));
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const remote = origin.replace('http:', 'ws:')+'/api/v1/agent-daemon/ws';
const base = `${origin}/api/v1/agent-daemon/install/${manifest.daemon_version}`;
function run(executable, args, input = '') {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, { env: { ...process.env, OAC_RUNTIME_HOME: join(root, 'unused-onboarding-home') }, stdio: ['pipe', 'pipe', 'pipe'] });
    let output = '';
    child.stdout.on('data', chunk => { output += chunk; }); child.stderr.on('data', chunk => { output += chunk; });
    child.stdin.on('error', () => {}); child.stdin.end(input);
    const timer = setTimeout(() => { child.kill(); reject(new Error('Native onboarding did not settle: '+(secret ? output.replaceAll(secret, '[redacted]') : output).slice(-4096))); }, 180000);
    child.on('error', reject);
    child.on('close', code => { clearTimeout(timer); if (secret) assert.ok(!output.includes(secret), 'Credential leaked into terminal'); resolve({ code, output }); });
  });
}
const executable = join(bundle, 'oac-daemon');
const args = ['install', '--onboard-url', `${origin}/api/v1/agent-daemon/installation`, '--authorization', authorization, '--install-dir', installation];
try {
  assert.notEqual((await run(executable, [...args, '--authorization', 'expired', '--non-interactive'])).code, 0);
  assert.notEqual((await run(executable, [...args, '--non-interactive'])).code, 0, 'Lost claim response should fail safely');
  const saved = JSON.parse(await readFile(join(installation, 'daemon', 'executor-credential.json'), 'utf8'));
  assert.equal(saved.executor_token, secret, 'Secret must survive a lost response');
  const script = resolve('services/core/internal/nativeinstaller/assets/bootstrap.sh');
  const result = await run('bash', [script, base, authorization, '--install-dir', installation], '\n');
  assert.equal(result.code, 0, `Interactive bootstrap failed: ${result.output}`);
  assert.match(result.output, /Host connection: connected/);
  assert.match(result.output, /Model configuration: not checked/);
  assert.equal(links, 1);
  const repeat = await run(executable, [...args, '--non-interactive']);
  assert.equal(repeat.code, 0, `Repeat failed: ${repeat.output}`);
  assert.equal(links, 1, 'Repeated installation created a duplicate daemon');
  assert.equal(unexpected, 0, 'The daemon opened a connection other than the Sandbox Link');
  console.log(JSON.stringify({ installation: 'passed', connection: 'passed', interactive: 'passed', retry: 'passed', repeat: 'passed', authentication: 'passed', model_requests: 0 }));
} finally {
  // The installed executable resolves its own home unless explicitly overridden.
  delete process.env.OAC_RUNTIME_HOME;
  const installed = join(installation, 'bin', 'oac-daemon');
  try { execFileSync(installed, ['stop'], { env: { ...process.env, OAC_RUNTIME_HOME: installation }, stdio: 'ignore', timeout: 15000 }); } catch { /* A failed installation may never have started. */ }
  for (const socket of sockets) socket.destroy();
  await new Promise(resolve => server.close(resolve));
}
