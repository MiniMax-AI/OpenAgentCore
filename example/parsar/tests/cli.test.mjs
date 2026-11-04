import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { createParsarClient } from "../cli.mjs";
import { createHandler } from "../server.mjs";
import { openStore } from "../server/store.mjs";
import { savedResourcesFetch } from "./core-double.mjs";

const cli = fileURLToPath(new URL('../cli.mjs', import.meta.url));
const configure = fileURLToPath(new URL('../examples/manager/configure.mjs', import.meta.url));
async function fixture(t, fetchImpl = async () => { throw new Error('Unexpected Core request'); }) {
  const store = openStore(':memory:');
  const modelId = randomUUID();
  store.put('models', { id: modelId, name: 'Test model', enabled: true });
  const config = { host: '127.0.0.1', port: 0, target: 'https://core.example', key: 'server-only-core-key' };
  const server = createServer(createHandler(config, { store, fetchImpl: savedResourcesFetch(fetchImpl) }));
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  config.port = server.address().port;
  t.after(() => { server.closeAllConnections(); server.close(); store.close(); });
  return { url: `http://127.0.0.1:${config.port}`, store, modelId };
}
function run(file, args, url, input = '', token) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [file, ...args], { env: { ...process.env, PARSAR_URL: url, PARSAR_TOKEN: token }, stdio: ['pipe', 'pipe', 'pipe'] });
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
  let reached = 0, redirected = 0;
  const destination = createServer((req, res) => { redirected++; res.end('{}'); });
  await new Promise((resolve) => destination.listen(0, '127.0.0.1', resolve));
  t.after(() => { destination.closeAllConnections(); destination.close(); });
  const server = createServer((req, res) => { reached++; res.writeHead(302, { Location: `http://127.0.0.1:${destination.address().port}/private` }); res.end(); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const api = createParsarClient(`http://127.0.0.1:${server.address().port}`, { token: randomUUID() });
  for (const path of ['https://elsewhere.example/app/agents', '//elsewhere.example/app/agents', '/core/v1/projects', '/app/../core/v1/projects', '/app/agents#fragment'])
    await assert.rejects(api('GET', path));
  assert.equal(reached, 0);
  await assert.rejects(api('GET', '/app/agents'));
  assert.equal(reached, 1);
  assert.equal(redirected, 0);
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

test('gateway token is sent only to the configured server and never over remote HTTP', async (t) => {
  const token = randomUUID();
  let headers;
  const server = createServer((req, res) => { headers = req.headers; res.setHeader('Content-Type', 'application/json'); res.end('{}'); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const url = `http://127.0.0.1:${server.address().port}`;
  const result = await run(cli, ['GET', '/app/models'], url, '', token);
  assert.equal(result.code, 0, result.stderr);
  assert.equal(headers.authorization, `Bearer ${token}`);
  assert.ok(!result.stdout.includes(token) && !result.stderr.includes(token));
  assert.throws(() => createParsarClient('http://10.0.0.2:18180', { token }), /HTTPS/);
  assert.throws(() => createParsarClient('https://example.com', { token: 'bad\r\nheader' }), /valid bearer/);
});

test('manager stores gateway access as a redacted environment value and returns the browser URL', async (t) => {
  const { url, store, modelId } = await fixture(t), token = randomUUID();
  const result = await run(configure, ['--url', url, '--agent-url', 'https://gateway.example', '--web-url', 'http://10.0.0.2:18180', '--model', modelId], url, '', token);
  assert.equal(result.code, 0, result.stderr);
  const ids = JSON.parse(result.stdout);
  assert.equal(ids.agent_url, `http://10.0.0.2:18180/#/agents/${ids.agent_id}`);
  const runtime = store.get('runtimes', ids.environment_id);
  assert.equal(runtime.env.PARSAR_TOKEN, token);
  assert.equal(runtime.env.PARSAR_WEB_URL, 'http://10.0.0.2:18180');
  assert.equal((await createParsarClient(url)('GET', `/app/runtimes/${runtime.id}`)).env.PARSAR_TOKEN, null);
  assert.ok(!result.stdout.includes(token) && !result.stderr.includes(token));
});


test('manager CLI registers, resumes and binds a bot without receiving its credentials', async (t) => {
  const calls = [];
  const fetchImpl = async (url, init) => {
    assert.equal(url, 'https://accounts.feishu.cn/oauth/v1/app/registration');
    assert.equal(init.redirect, 'error');
    const action = init.body.get('action');
    calls.push(action);
    if (action === 'begin') return Response.json({ device_code: 'private-device', user_code: 'test-code', interval: 1, expires_in: 300 });
    if (action === 'poll') return Response.json({ client_id: 'cli_0123456789abcdef', client_secret: 'private-bot-secret' });
    assert.equal(action, 'init');
    return Response.json({});
  };
  const { url, store, modelId } = await fixture(t, fetchImpl), api = createParsarClient(url);
  const botId = randomUUID(), runtimeId = randomUUID(), agentId = randomUUID();
  await api('PUT', `/app/runtimes/${runtimeId}`, { name: 'Business environment' });
  await api('PUT', `/app/agents/${agentId}`, { name: 'Business Agent', instructions: 'Help with the requested work.', skill_ids: [], mcp_ids: [], model_id: modelId, runtime_id: runtimeId, harness: 'codex' });
  const provision = (action) => run(cli, ['POST', `/app/feishu/provision/${botId}`, '--file', '-'], url, JSON.stringify({ action, name: 'Business bot' }));
  const begin = await provision('begin');
  assert.equal(begin.code, 0, begin.stderr);
  assert.equal(JSON.parse(begin.stdout).status, 'pending');
  assert.match(JSON.parse(begin.stdout).verification_url, /^https:\/\/open\.feishu\.cn\//);
  const qrPath = JSON.parse(begin.stdout).qr_url;
  const qr = await fetch(url + qrPath);
  assert.equal(qr.status, 200);
  assert.equal(qr.headers.get('content-type'), 'image/png');
  assert.equal(qr.headers.get('cache-control'), 'no-store');
  const png = Buffer.from(await qr.arrayBuffer());
  assert.deepEqual([...png.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10]);
  assert.equal((await fetch(url + qrPath, { headers: { origin: 'https://external.example' } })).status, 403);
  await provision('begin');
  assert.deepEqual(calls, ['init', 'begin']);
  assert.equal((await api('GET', '/app/feishu/bots')).length, 0);
  const poll = await provision('poll');
  assert.equal(poll.code, 0, poll.stderr);
  assert.equal(JSON.parse(poll.stdout).status, 'success');
  assert.equal((await fetch(url + qrPath)).status, 410);
  const bot = await api('GET', `/app/feishu/bots/${botId}`);
  assert.equal(bot.enabled, false);
  assert.equal(bot.has_app_secret, true);
  const enabled = await api('PUT', `/app/feishu/bots/${botId}`, { ...bot, agent_id: agentId, enabled: true, direct: true, groups: false });
  assert.equal(enabled.enabled, true);
  assert.equal(enabled.groups, false);
  assert.equal(store.get('feishu_bots', botId).app_secret, 'private-bot-secret');
  await assert.rejects(api('PUT', `/app/feishu/bots/${botId}`, { ...bot, enabled: false }), { status: 409 });
  await provision('poll');
  assert.deepEqual(calls, ['init', 'begin', 'poll']);
  const bots = await api('GET', '/app/feishu/bots');
  assert.equal(bots[0].health.state, 'stopped');
  assert.deepEqual(await api('GET', '/app/feishu/deliveries'), []);
  const publicOutput = JSON.stringify([begin, poll, bot, enabled, bots]);
  for (const secret of ['private-device', 'private-bot-secret', 'server-only-core-key']) assert.ok(!publicOutput.includes(secret));
});
