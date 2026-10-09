import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join } from 'node:path';

// The native build supplies its pinned source and dependencies. No model is used.
const root = process.env.MCODE_SOURCE;
test('native prompt guidance follows the protected tool policy', { skip: !root }, async () => {
  const require = createRequire(join(root, 'package.json'));
  const { build } = require('esbuild');
  const capabilities = join(root, 'packages/config/src/agent-capabilities.ts');
  const renderer = join(root, 'packages/local-runtime-v2/src/service/agent/builtin/prompt-renderer.ts');
  const paths = JSON.parse(readFileSync(join(root, 'tsconfig.standalone.json'))).compilerOptions.paths;
  const result = await build({
    stdin: { contents: `export * from ${JSON.stringify(capabilities)}; export * from ${JSON.stringify(renderer)};`, resolveDir: root },
    bundle: true, write: false, platform: 'node', format: 'esm', nodePaths: [join(root, 'node_modules')],
    banner: { js: `import { createRequire } from 'node:module'; const require = createRequire(${JSON.stringify(join(root, 'package.json'))});` },
    plugins: [{ name: 'native-prompt-imports', setup(builder) {
      builder.onResolve({ filter: /^@mavis\// }, ({ path }) => {
        // Import the same config implementation without unrelated barrel exports.
        if (path === '@mavis/config') return { path: capabilities };
        if (paths[path]) return { path: join(root, paths[path][0]) };
      });
    } }],
  });
  const native = await import('data:text/javascript;base64,' + Buffer.from(result.outputFiles[0].text).toString('base64'));
  const templates = new Map(['AGENT_CONTEXT.md.hbs', 'tui/SYSTEM.md.hbs', 'explore.md.hbs'].map(file =>
    [file, readFileSync(join(root, 'packages/local-runtime-v2/assets/agents/_v2', file), 'utf8')]));
  const context = (tools, child = false) => ({
    ...native.createBuiltinPromptContext({ capabilities: native.resolveAgentCapabilities({ tools }),
      promptProfile: 'tui', surface: child ? 'task-child' : 'cli' }, {}),
    layer: { base: true, worker: false, root: false, branch: false },
  });
  const render = (file, value) => native.renderBuiltinTemplate(templates.get(file), value, file);
  const previous = process.env.OAC_RUNTIME_MCODE_TOOL_POLICY;
  try {
    delete process.env.OAC_RUNTIME_MCODE_TOOL_POLICY;
    const normal = context(undefined);
    assert.equal(normal.tools.bash, true);
    for (const file of ['AGENT_CONTEXT.md.hbs', 'tui/SYSTEM.md.hbs']) {
      assert.match(render(file, normal), /Prefer dedicated tools over `bash` whenever one fits\. Use `grep`/);
      assert.match(render(file, normal), /Reserve `bash` for shell-only operations/);
    }
    const withoutBash = context(['read', 'grep', 'glob']);
    for (const file of templates.keys()) assert.doesNotMatch(render(file, withoutBash), /`bash`/);

    process.env.OAC_RUNTIME_MCODE_TOOL_POLICY = 'protected-mcp-v1';
    for (const value of [context([]), context(undefined), context(['bash', 'read', 'grep', 'glob'], true)]) {
      for (const name of ['bash', 'read', 'write', 'edit', 'grep', 'glob']) {
        assert.equal(value.tools[name], false, name);
        assert.equal(native.isProtectedWorkspaceToolAllowed(name, 'builtin'), false, name);
      }
      for (const file of templates.keys()) assert.doesNotMatch(render(file, value), /`(?:bash|read|write|edit|grep|glob)`/, file);
    }
    assert.equal(native.isProtectedWorkspaceToolAllowed('web_search', 'builtin-matrix'), false);
    for (const name of ['skill', 'task', 'task_append', 'task_query', 'task_output', 'task_stop']) {
      assert.equal(native.isProtectedWorkspaceToolAllowed(name, 'builtin'), true, name);
    }
    // The policy does not rename, alias or restrict configured MCP definitions.
    for (const name of ['mcp__oac_workspace__workspace_bash', 'mcp__other__lookup']) {
      assert.equal(native.isProtectedWorkspaceToolAllowed(name, 'configured'), true, name);
    }
  } finally {
    if (previous === undefined) delete process.env.OAC_RUNTIME_MCODE_TOOL_POLICY;
    else process.env.OAC_RUNTIME_MCODE_TOOL_POLICY = previous;
  }
});
