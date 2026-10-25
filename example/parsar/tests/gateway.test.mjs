import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { once } from 'node:events';

// Run with PARSAR_TEST_CADDY=/path/to/caddy; no Core or product credentials.
test('Caddy gateway authenticates before forwarding and removes its credential', { skip: !process.env.PARSAR_TEST_CADDY }, async (t) => {
  const token = randomUUID();
  let received = [];
  const upstream = createServer((req, res) => {
    received.push({ url: req.url, headers: req.headers });
    res.setHeader('Content-Type', 'application/json');
    res.end('{"ok":true}');
  });
  await new Promise((resolve) => upstream.listen(0, '127.0.0.1', resolve));
  t.after(() => { upstream.closeAllConnections(); upstream.close(); });
  const address = `127.0.0.1:${upstream.address().port}`;
  const config = fileURLToPath(new URL('../examples/manager/Caddyfile', import.meta.url));
  for (const configured of [token, '']) {
    const child = spawn(process.env.PARSAR_TEST_CADDY, ['run', '--config', config, '--adapter', 'caddyfile'], {
      env: { ...process.env, PARSAR_TOKEN: configured, PARSAR_UPSTREAM: address }, stdio: 'ignore',
    });
    const exited = once(child, 'exit');
    try {
      let ready = false;
      for (let n = 0; n < 50; n++) {
        try { await fetch('http://127.0.0.1:18181/app/models'); ready = true; break; }
        catch { await new Promise((resolve) => setTimeout(resolve, 100)); }
      }
      assert.ok(ready, 'gateway must start');
      const request = (path, authorization, extra = {}) => fetch(`http://127.0.0.1:18181${path}`, { headers: { ...(authorization ? { Authorization: authorization } : {}), ...extra }, redirect: 'manual' });
      received = [];
      for (const authorization of [undefined, 'Bearer wrong', 'Bearer'])
        assert.equal((await request('/app/models', authorization)).status, 401);
      assert.equal((await request('/core/v1/projects', `Bearer ${token}`)).status, 401);
      assert.equal((await request('/', `Bearer ${token}`)).status, 401);
      assert.equal(received.length, 0);
      const response = await request('/app/models', `Bearer ${token}`, { Cookie: 'untrusted=1', Origin: 'https://gateway.example' });
      assert.equal(response.status, configured ? 200 : 401);
      if (configured) {
        assert.equal(received.length, 1);
        assert.equal(received[0].headers.host, address);
        assert.equal(received[0].headers.origin, `http://${address}`);
        assert.equal(received[0].headers.authorization, undefined);
        assert.equal(received[0].headers.cookie, undefined);
      } else assert.equal(received.length, 0);
    } finally { child.kill('SIGTERM'); await exited; }
  }
});
