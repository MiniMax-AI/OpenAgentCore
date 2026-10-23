import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { createParsarClient } from "../cli.mjs";
import { createHandler } from "../server.mjs";
import { openStore } from "../server/store.mjs";

const cli = fileURLToPath(new URL('../cli.mjs', import.meta.url));
const configure = fileURLToPath(new URL('../examples/manager/configure.mjs', import.meta.url));
async function fixture(t) {
  const store = openStore(':memory:');
  const modelId = randomUUID();
  store.put('models', { id: modelId, name: 'Test model', enabled: true });
  const config = { host: '127.0.0.1', port: 0, target: 'https://core.example', key: 'server-only-core-key' };
  const server = createServer(createHandler(config, { store, fetchImpl: async () => { throw new Error('Unexpected Core request'); } }));
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  config.port = server.address().port;
  t.after(() => { server.closeAllConnections(); server.close(); store.close(); });
  return { url: `http://127.0.0.1:${config.port}`, store, modelId };
}
function run(file, args, url, input = '') {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [file, ...args], { env: { ...process.env, PARSAR_URL: url }, stdio: ['pipe', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    child.stdout.on('data', (chunk) => { stdout += chunk; });
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.on('error', reject);
    child.on('close', (code) => resolve({ code, stdout, stderr }));
    child.stdin.end(input);
  });
}

test('SDK uses real product validation, redaction and optimistic revisions', async (t) => {
  const { url, store, modelId } = await fixture(t), api = createParsarClient(url);
  const runtimeId = randomUUID(), agentId = randomUUID();
  const runtime = await api('PUT', `/app/runtimes/${runtimeId}`, { name: 'Analysis', env: { PRIVATE_VALUE: 'keep-private' }, files: [{ path: '/workspace/input.txt', content: 'preserve' }] });
  assert.deepEqual(runtime.env, { PRIVATE_VALUE: null });
  const agent = await api('PUT', `/app/agents/${agentId}`, { name: 'Analyst', instructions: 'Analyze files', model_id: modelId, runtime_id: runtimeId, harness: 'codex', mcp_ids: [], skill_ids: [] });
  assert.equal(agent.runtime_id, runtimeId);
  const loaded = await api('GET', `/app/runtimes/${runtimeId}`);
  await api('PUT', `/app/runtimes/${runtimeId}`, { ...loaded, name: 'Renamed' });
  assert.equal(store.get('runtimes', runtimeId).env.PRIVATE_VALUE, 'keep-private');
  assert.equal(store.get('runtimes', runtimeId).files[0].content, 'preserve');
  await assert.rejects(api('PUT', `/app/runtimes/${runtimeId}`, loaded), { status: 409 });
  await assert.rejects(api('PUT', `/app/agents/${randomUUID()}`, { ...agent, runtime_id: randomUUID() }), { status: 400 });
  await assert.rejects(api('DELETE', `/app/runtimes/${runtimeId}`), { status: 409 });
});

test('CLI accepts JSON stdin and preserves server errors without echoing malformed input', async (t) => {
  const { url } = await fixture(t), id = randomUUID();
  const created = await run(cli, ['PUT', `/app/runtimes/${id}`, '--file', '-'], url, JSON.stringify({ name: 'CLI environment' }));
  assert.equal(created.code, 0, created.stderr);
  assert.equal(created.stderr, '');
  assert.equal(JSON.parse(created.stdout).id, id);
  const missing = await run(cli, ['GET', `/app/agents/${randomUUID()}`], url);
  assert.equal(missing.code, 1);
  assert.equal(missing.stdout, '');
  assert.equal(JSON.parse(missing.stderr).error.status, 404);
  const invalid = await run(cli, ['PUT', `/app/runtimes/${id}`, '--file', '-'], url, '{"secret":"keep-private",bad-json');
  assert.equal(invalid.code, 1);
  assert.ok(!invalid.stderr.includes('keep-private'));
  const help = await run(cli, ['--help'], url);
  assert.equal(help.code, 0);
  assert.match(help.stdout, /revision/);
});

test('SDK never follows redirects or accepts another origin or administration path', async (t) => {
  let reached = 0;
  const server = createServer((req, res) => { reached++; res.writeHead(302, { Location: 'http://127.0.0.1:1/private' }); res.end(); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const api = createParsarClient(`http://127.0.0.1:${server.address().port}`);
  for (const path of ['https://elsewhere.example/app/agents', '//elsewhere.example/app/agents', '/core/v1/projects', '/app/../core/v1/projects', '/app/agents#fragment'])
    await assert.rejects(api('GET', path));
  assert.equal(reached, 0);
  await assert.rejects(api('GET', '/app/agents'));
  assert.equal(reached, 1);
  for (const base of ['https://user:secret@example.com', 'https://example.com/path', 'file:///tmp/file'])
    assert.throws(() => createParsarClient(base));
});

test('manager example creates ordinary resources and never overwrites them on rerun', async (t) => {
  const { url, store, modelId } = await fixture(t);
  const agentId = randomUUID(), runtimeId = randomUUID();
  const args = ['--url', url, '--agent-url', 'http://10.0.0.2:18180', '--model', modelId, '--agent-id', agentId, '--environment-id', runtimeId];
  const result = await run(configure, args, url);
  assert.equal(result.code, 0, result.stderr);
  assert.equal(JSON.parse(result.stdout).agent_id, agentId);
  assert.equal(store.get('agents', agentId).name, 'parsar他爹');
  assert.equal(store.get('agents', agentId).runtime_id, runtimeId);
  const runtime = store.get('runtimes', runtimeId);
  assert.equal(runtime.env.PARSAR_URL, 'http://10.0.0.2:18180');
  assert.equal(runtime.files.length, 2);
  assert.ok(!JSON.stringify(runtime).includes('server-only-core-key'));
  const again = await run(configure, args, url);
  assert.equal(again.code, 1);
  assert.match(again.stderr, /already exists/);
  assert.deepEqual(store.get('runtimes', runtimeId), runtime);
});
