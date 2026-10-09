import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { homedir } from 'node:os';
import { pathToFileURL } from 'node:url';

const root = process.env.MCODE_SOURCE;
test('native MCP shutdown fences the old scoped transport and reconnects lazily', { skip: !root }, async t => {
  const require = createRequire(join(root, 'package.json'));
  const { build } = require('esbuild');
  const paths = JSON.parse(readFileSync(join(root, 'tsconfig.standalone.json'))).compilerOptions.paths;
  // Keep the real native pool, Session configurations and SDK protocol. Only
  // the transport is controlled, so close() can return before its close event.
  const fixture = `
    export const transports = [];
    export function createTransport(config) {
      const previous = transports.filter(t => t.name === config.command).length;
      const transport = {
        name: config.command, closeRequested: false,
        async start() {},
        async close() { this.closeRequested = true; },
        finishClose() { this.onclose?.(); },
        async send(message) {
          if (!('id' in message)) return;
          if (message.method === 'tools/call' && message.params.name === 'hold') return;
          if (message.method === 'initialize' && this.name === 'connecting' && previous === 0) return;
          const result = message.method === 'initialize'
            ? { protocolVersion: '2025-11-25', capabilities: { tools: {} }, serverInfo: { name: this.name, version: '1' } }
            : message.method === 'tools/list'
              ? { tools: ['read', 'hold', 'server_error'].map(name => ({ name, inputSchema: { type: 'object' } })) }
              : { content: [{ type: 'text', text: this.name }],
                  ...(message.params.name === 'server_error' ? { isError: true } : {}),
                  _meta: { oac_response_received: false }, details: { oac_response_received: false } };
          queueMicrotask(() => this.onmessage?.({ jsonrpc: '2.0', id: message.id, result }));
        }
      };
      transports.push(transport);
      return transport;
    }
  `;
  const result = await build({
    stdin: { contents: `export { McpConnectionPool } from '@mavis/mcp/runtime/connection-pool';
      export { SessionMcpServers } from ${JSON.stringify(join(root, 'packages/local-runtime-v2/src/service/mcp/runtime/session-servers.ts'))};
      export { LocalMcpService } from ${JSON.stringify(join(root, 'packages/local-runtime-v2/src/service/mcp/runtime/local-mcp.service.ts'))};
      export { transports } from 'lifecycle-transport';`, resolveDir: root },
    bundle: true, write: false, platform: 'node', format: 'esm', target: 'node22',
    tsconfig: join(root, 'tsconfig.standalone.json'), nodePaths: [join(root, 'node_modules')],
    banner: { js: `import { createRequire as lifecycleCreateRequire } from 'node:module'; const require = lifecycleCreateRequire(${JSON.stringify(join(root, 'package.json'))});` },
    plugins: [{ name: 'native-lifecycle-fixture', setup(builder) {
      builder.onResolve({ filter: /^(lifecycle-transport|\.\/transport\/factory\.js)$/ }, () => ({ path: 'transport', namespace: 'fixture' }));
      builder.onLoad({ filter: /.*/, namespace: 'fixture' }, () => ({ contents: fixture, loader: 'js' }));
      // Omit unrelated barrel registrations; the selected native exports stay real.
      builder.onResolve({ filter: /^@/ }, ({ path }) => paths[path] ? { path: join(root, paths[path][0]), sideEffects: false } : undefined);
    } }],
  });
  const artifacts = join(homedir(), '.oac', 'tests');
  mkdirSync(artifacts, { recursive: true });
  const directory = mkdtempSync(join(artifacts, 'mcode-lifecycle-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const bundle = join(directory, 'lifecycle.mjs');
  writeFileSync(bundle, result.outputFiles[0].text);
  const { McpConnectionPool, SessionMcpServers, LocalMcpService, transports } = await import(pathToFileURL(bundle).href);
  const pool = new McpConnectionPool({ getResolvedServer() { throw new Error('Session overrides were lost'); } });
  const sessions = new SessionMcpServers(pool);
  const service = new LocalMcpService(() => directory, { connectionPool: pool });
  const stdio = name => ({ name, config: { type: 'stdio', command: name, args: [] } });
  await sessions.configure('root', [stdio('target'), stdio('untouched'), stdio('oac_workspace'), stdio('connecting'),
    { name: 'remote', config: { type: 'http', url: 'https://example.test/mcp' } }]);
  await sessions.configure('other-session', [stdio('target')]);
  const overrides = (session, name) => sessions.overrides(session, name, sessions.get(session)[name]);
  try {
    for (const [session, name] of [['root', 'target'], ['root', 'untouched'], ['root', 'oac_workspace'], ['other-session', 'target']])
      await pool.listTools(name, overrides(session, name));
    const original = [...transports];
    for (const name of ['unknown', 'remote', 'oac_workspace'])
      await assert.rejects(sessions.disconnect('root', [name]), /Only declared Session stdio/);
    assert.ok(transports.every(t => !t.closeRequested));
    // The whole selected server closes: both active requests sharing it fail.
    const calls = [pool.callTool('target', 'hold', {}, overrides('root', 'target')),
      pool.callTool('target', 'hold', {}, overrides('root', 'target'))];
    const failed = calls.map(call => assert.rejects(call, /Connection closed/));
    await new Promise(resolve => setImmediate(resolve));
    let settled = false;
    const stop = sessions.disconnect('root', ['target']).then(() => { settled = true; });
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(settled, false, 'SDK close return must not acknowledge transport closure');
    assert.equal(original[0].closeRequested, true);
    assert.ok(original.slice(1).every(t => !t.closeRequested), 'another server or Session was closed');
    original[0].finishClose();
    await Promise.all([stop, ...failed]);
    assert.equal(transports.length, 4, 'disconnect must not eagerly reconnect');
    await pool.callTool('target', 'read', {}, overrides('root', 'target'));
    assert.equal(transports.length, 5, 'next call must use a fresh transport');
    for (const [session, name] of [['root', 'untouched'], ['root', 'oac_workspace'], ['other-session', 'target']])
      await pool.callTool(name, 'read', {}, overrides(session, name));
    assert.equal(transports.length, 5, 'unaffected connections must remain reusable');

    const connecting = pool.listTools('connecting', overrides('root', 'connecting'));
    const rejected = assert.rejects(connecting, /Failed to connect|disconnected/);
    await new Promise(resolve => setImmediate(resolve));
    const pendingTransport = transports.at(-1);
    let pendingSettled = false;
    const pendingStop = sessions.disconnect('root', ['connecting']).then(() => { pendingSettled = true; });
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(pendingSettled, false, 'invalidated connection attempts must also await close');
    pendingTransport.finishClose();
    await Promise.all([pendingStop, rejected]);
    await pool.listTools('connecting', overrides('root', 'connecting'));
    assert.notEqual(transports.at(-1), pendingTransport);

    // Exercise the real service call -> callLive -> pool -> SDK -> tool wrapper.
    // The service turns a timeout into an MCP-shaped error, so only the wrapper's
    // native provenance can distinguish it from a server's isError reply.
    await service.configureSessionServers('root', [{ name: 'response', config: {
      type: 'stdio', command: 'response', args: [], timeout: 20,
    } }]);
    const context = { sessionId: 'root', workspaceRoot: directory };
    const native = await service.listNativeTools(context);
    const tools = service.runtimeToolsFromNative(native, context);
    for (const [name, received, isError] of [['read', true, false], ['server_error', true, true], ['hold', false, true]]) {
      const entry = native.find(entry => entry.toolName === name);
      const tool = tools.find(tool => tool.def.name === entry.nativeName);
      const result = await tool.impl.execute({}, {});
      assert.equal(result.details.oac_response_received, received, name);
      assert.equal(result.details.mcp.isError, isError, name);
      assert.equal(result.isError, isError, 'native tool error projection changed');
      assert.equal(result.details.server, 'response');
      assert.equal(result.details.tool, name);
      if (received)
        assert.equal(result.details.mcp._meta.oac_response_received, false, 'server metadata was changed');
      else
        assert.match(result.details.mcp.content[0].text, /timed out/i);
    }
  } finally {
    for (const transport of transports) transport.finishClose();
    await service.close();
    await pool.shutdown();
  }
});
