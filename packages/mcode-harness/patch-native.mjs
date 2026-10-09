import { readFileSync, writeFileSync, copyFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = process.env.MCODE_SOURCE;
const pin = JSON.parse(readFileSync(join(here, 'source.json'), 'utf8'));
if (!root || readFileSync(join(root, '.oac-source-revision'), 'utf8').trim() !== pin.revision)
  throw new Error('Native source revision mismatch');
const target = join(root, 'packages/local-runtime/src/background-task/store.ts');
let source = readFileSync(target, 'utf8');
const before = source.slice(source.indexOf('  async create('), source.indexOf('  async get('));
if (!before.includes('return this.withDb((db) => {') || !before.endsWith('    });\n  }\n\n'))
  throw new Error('Pinned native task admission source changed');
const after = before.replace('return this.withDb((db) => {', 'return this.withDb((db) => runInImmediateTransaction(db, () => {')
  .replace('      db.prepare(', '      enforceSubagentAdmission(db, task);\n      db.prepare(')
  .replace('    });\n  }\n\n', '    }));\n  }\n\n');
source = "import { enforceSubagentAdmission } from './oac-subagent-admission.mjs';\n" + source.replace(before, after);
writeFileSync(target, source);
const acpPath = join(root, 'packages/tui/src/acp/agent.ts');
const acp = readFileSync(acpPath, 'utf8');
const marker = "      _meta: {\n        'minimax-code/extensions': {";
if (acp.split(marker).length !== 2) throw new Error('Pinned native ACP initialization changed');
writeFileSync(acpPath, acp.replace(marker, "      _meta: {\n        'oac/subagents': { version: 1, workspaceTools: process.env.OAC_RUNTIME_MCODE_TOOL_POLICY ?? null, maxConcurrent: Number(process.env.OAC_RUNTIME_MCODE_MAX_SUBAGENTS ?? 0) },\n        'minimax-code/extensions': {"));
const catalogPath = join(root, 'packages/local-runtime-v2/src/service/turn-system/agent-host/assembly/local-turn-tool-catalog.ts');
const catalog = readFileSync(catalogPath, 'utf8');
const capabilityPath = join(root, 'packages/config/src/agent-capabilities.ts');
const capabilities = readFileSync(capabilityPath, 'utf8');
const enabled = '  return capabilities.tools === undefined || capabilities.tools.includes(toolName);';
if (capabilities.split(enabled).length !== 2) throw new Error('Pinned native tool capability predicate changed');
writeFileSync(capabilityPath, capabilities.replace(enabled,
  "  return isProtectedWorkspaceToolAllowed(toolName, 'builtin') && (capabilities.tools === undefined || capabilities.tools.includes(toolName));") + `
// The protected workspace policy applies to both tool admission and prompt capabilities.
export function isProtectedWorkspaceToolAllowed(toolName: string, source: string): boolean {
  if (process.env.OAC_RUNTIME_MCODE_TOOL_POLICY !== 'protected-mcp-v1') return true;
  if (source === 'builtin-matrix') return false;
  return source !== 'builtin' || ['skill', 'task', 'task_append', 'task_query', 'task_output', 'task_stop'].includes(toolName);
}
`);
const configIndexPath = join(root, 'packages/config/src/index.ts');
const configIndex = readFileSync(configIndexPath, 'utf8');
if (configIndex.split('  isAgentBuiltinToolEnabled,').length !== 2) throw new Error('Pinned native config exports changed');
writeFileSync(configIndexPath, configIndex.replace('  isAgentBuiltinToolEnabled,', '  isAgentBuiltinToolEnabled,\n  isProtectedWorkspaceToolAllowed,'));
const gate = "  if (!isFeatureEnabled(ceiling, toolName)) return false;";
if (catalog.split(gate).length !== 2) throw new Error('Pinned native tool capability gate changed');
const selectorGate = "  if (!input.selector.allowsTool(input.tool.def.name, input.options.selectorAlias)) return false;\n  if (!isMcpServerAllowed(input.selector, input.options)) return false;";
if (catalog.split(selectorGate).length !== 2) throw new Error('Pinned native selector gate changed');
const withWorkspace = catalog.replace(selectorGate, `  const protectedWorkspace = process.env.OAC_RUNTIME_MCODE_TOOL_POLICY === 'protected-mcp-v1' &&
    input.source === 'configured' && input.options.mcp === true && input.options.serverName === 'oac_workspace';
  if (!protectedWorkspace && !input.selector.allowsTool(input.tool.def.name, input.options.selectorAlias)) return false;
  if (!protectedWorkspace && !isMcpServerAllowed(input.selector, input.options)) return false;`);
writeFileSync(catalogPath, "import { isProtectedWorkspaceToolAllowed } from '@mavis/config';\n" + withWorkspace.replace(gate, `  if (!isProtectedWorkspaceToolAllowed(toolName, source)) return false;
${gate}`));
const rendererPath = join(root, 'packages/local-runtime-v2/src/service/agent/builtin/prompt-renderer.ts');
const renderer = readFileSync(rendererPath, 'utf8');
if (renderer.split('    tools,').length !== 2) throw new Error('Pinned native prompt context changed');
writeFileSync(rendererPath, renderer.replace('    tools,', `    tools,
    nativeWorkspaceTools: tools.bash && tools.grep && tools.glob && tools.read && tools.edit && tools.write,`));
for (const file of ['AGENT_CONTEXT.md.hbs', 'tui/SYSTEM.md.hbs']) {
  const path = join(root, 'packages/local-runtime-v2/assets/agents/_v2', file);
  const template = readFileSync(path, 'utf8');
  const guidance = /- Prefer dedicated tools over `bash` whenever one fits\.[\s\S]*?no available dedicated tool can complete the task\./g;
  const matches = [...template.matchAll(guidance)];
  if (matches.length !== (file === 'AGENT_CONTEXT.md.hbs' ? 2 : 1)) throw new Error('Pinned native workspace guidance changed');
  writeFileSync(path, template.replace(guidance, paragraph => '{{#if nativeWorkspaceTools}}' + paragraph + '{{else}}- Prefer available dedicated tools for file operations and search. Use the tools declared for this turn.{{/if}}')
    .replace('- For unfamiliar project-specific concepts, search the workspace with `grep` or `glob` first.', '{{#if tools.grep}}{{#if tools.glob}}- For unfamiliar project-specific concepts, search the workspace with `grep` or `glob` first.{{/if}}{{/if}}'));
}
const explorePath = join(root, 'packages/local-runtime-v2/assets/agents/_v2/explore.md.hbs');
const explore = readFileSync(explorePath, 'utf8');
const bashAdvice = 'Use `bash` only to inspect existing code or Git state. ';
const backgroundAdvice = 'Explore does not have `task_output`, so do not use `run_in_background` with\n`bash`. Run each Bash command in the foreground and return its result.';
if (!explore.includes('# Read-only Bash') || !explore.includes(bashAdvice) || !explore.includes(backgroundAdvice)) throw new Error('Pinned native Explore guidance changed');
writeFileSync(explorePath, explore.replace('# Read-only Bash', '# Read-only {{#if tools.bash}}Bash{{else}}execution{{/if}}').replace(bashAdvice, '{{#if tools.bash}}' + bashAdvice + '{{/if}}')
  .replace(backgroundAdvice, '{{#if tools.bash}}' + backgroundAdvice + '{{/if}}'));
const projectPath = join(root, 'packages/local-runtime-v2/src/service/mcp/project-mcp.service.ts');
const project = readFileSync(projectPath, 'utf8');
const projectGate = '    if (!context?.workspaceRoot || !context.sessionId) return {};';
if (project.split(projectGate).length !== 2) throw new Error('Pinned native project MCP source changed');
writeFileSync(projectPath, project.replace(projectGate, "    if (process.env.OAC_RUNTIME_MCODE_TOOL_POLICY === 'protected-mcp-v1' || !context?.workspaceRoot || !context.sessionId) return {};"));
copyFileSync(join(here, 'native-subagent-admission.mjs'), join(root, 'packages/local-runtime/src/background-task/oac-subagent-admission.mjs'));
// Preserve the native owner and its connection keys. This control request is
// called only after the Runtime has closed the selected sandbox process scopes.
function replaceNative(file, before, after) {
  const path = join(root, file);
  const source = readFileSync(path, 'utf8');
  if (source.split(before).length !== 2) throw new Error('Pinned native MCP lifecycle source changed: ' + file);
  writeFileSync(path, source.replace(before, after));
}
const poolFile = 'packages/agent-modules/mcp/src/runtime/connection-pool.ts';
replaceNative(poolFile, '  private connectionServerNames = new Map<McpConnectionKey, string>();',
  `  private connectionServerNames = new Map<McpConnectionKey, string>();
  private transportClosures = new Map<McpConnectionKey, Set<Promise<void>>>();`);
replaceNative(poolFile, '    const client = new Client(', `    // SDK close() may return after sending SIGKILL, before the child closes.
    // Retain each old transport until its actual close event, including connects
    // invalidated before they could enter the pool.
    const closures = this.transportClosures.get(connectionKey) ?? new Set<Promise<void>>();
    this.transportClosures.set(connectionKey, closures);
    const closed = new Promise<void>((resolve) => {
      const previous = transport.onclose;
      transport.onclose = () => { try { previous?.(); } finally { resolve(); } };
    });
    closures.add(closed);
    void closed.then(() => {
      closures.delete(closed);
      if (closures.size === 0 && this.transportClosures.get(connectionKey) === closures)
        this.transportClosures.delete(connectionKey);
    });
    const client = new Client(`);
replaceNative(poolFile, '  async reconnect(\n', `  async disconnectAndWait(serverName: string, overrides: McpConnectionTokenOverrides): Promise<void> {
    if (!overrides.connectionKey || overrides.serverOverride?.transport.type !== 'stdio')
      throw new Error('A scoped stdio MCP connection is required.');
    const key = this.resolveConnectionKey(serverName, overrides);
    const closed = [...(this.transportClosures.get(key) ?? [])];
    await this.disconnect(serverName, overrides);
    await Promise.all(closed);
  }

  async reconnect(
`);
const sessionServersFile = 'packages/local-runtime-v2/src/service/mcp/runtime/session-servers.ts';
replaceNative(sessionServersFile, '  async remove(sessionId: string): Promise<void> {', `  async disconnect(sessionId: string, names: readonly string[]): Promise<void> {
    const id = requireSessionId(sessionId);
    const current = this.servers.get(id);
    if (names.length === 0 || new Set(names).size !== names.length || !this.pool)
      throw new Error('Invalid MCP disconnect request.');
    const selected = names.map((name) => {
      const config = current?.[name];
      if (name === 'oac_workspace' || !config || config.type !== 'stdio')
        throw new Error('Only declared Session stdio MCP servers can be disconnected.');
      return { name, overrides: this.overrides(id, name, config)! };
    });
    for (const { name, overrides } of selected)
      await this.pool.disconnectAndWait(name, overrides);
  }

  async remove(sessionId: string): Promise<void> {`);
replaceNative('packages/local-runtime-v2/src/service/mcp/runtime/local-mcp.service.ts',
  '  async clearSessionServers(sessionId: string): Promise<void> {',
  `  disconnectSessionServers(sessionId: string, names: readonly string[]): Promise<void> {
    this.assertOpen();
    return this.enqueueMutation(() => this.sessionServers.disconnect(sessionId, names));
  }

  async clearSessionServers(sessionId: string): Promise<void> {`);
const facadeFile = 'packages/local-runtime-v2/src/service/mcp/tools/public-facade.ts';
replaceNative(facadeFile, "  | 'clearSessionServers'", "  | 'clearSessionServers'\n  | 'disconnectSessionServers'");
replaceNative(facadeFile, '  clearSessionServers(sessionId: string): Promise<void> {',
  `  disconnectSessionServers(input: { sessionId: string; servers: readonly string[] }): Promise<void> {
    return this.owner.disconnectSessionServers(input.sessionId, input.servers);
  }

  clearSessionServers(sessionId: string): Promise<void> {`);
replaceNative('packages/local-runtime-v2/src/application/session/process-local-application-contract.ts',
  '    | "clearSessionServers"', '    | "clearSessionServers"\n    | "disconnectSessionServers"');
replaceNative('packages/local-runtime-v2/src/local/cli-service.ts',
  '  configureSessionMcpServers(\n',
  `  disconnectSessionMcpServers(sessionId: string, servers: readonly string[]): Promise<void> {
    return this.requireCapability("mcp", "MCP").disconnectSessionServers({ sessionId, servers });
  }

  configureSessionMcpServers(
`);
replaceNative('packages/tui/src/runtime/port.ts',
  '  clearSessionMcpServers(sessionId: string): Promise<void>;',
  '  clearSessionMcpServers(sessionId: string): Promise<void>;\n  disconnectSessionMcpServers(sessionId: string, servers: readonly string[]): Promise<void>;');
for (const [file, owner] of [
  ['packages/tui/src/runtime/adapter.ts', 'sessionAccess'],
  ['packages/tui/src/runtime/adapters/session-access.ts', 'cliService'],
]) {
  replaceNative(file, '  clearSessionMcpServers(sessionId: string): Promise<void> {',
    `  disconnectSessionMcpServers(sessionId: string, servers: readonly string[]): Promise<void> {
    return this.${owner}.disconnectSessionMcpServers(sessionId, servers);
  }

  clearSessionMcpServers(sessionId: string): Promise<void> {`);
}
replaceNative('packages/tui/src/acp/agent.ts', "        'oac/subagents': {", "        'oac/mcp-lifecycle': { version: 1 },\n        'oac/subagents': {");
replaceNative('packages/tui/src/acp/agent.ts',
  '  app.onNotification(acp.methods.agent.session.cancel, async ({ params }) => {',
  `  app.onRequest('oac/session/mcp/disconnect', (value: unknown) => {
    const request = value as { sessionId?: unknown; servers?: unknown } | null;
    if (!request || typeof request.sessionId !== 'string' || !request.sessionId ||
        !Array.isArray(request.servers) || request.servers.length === 0 ||
        request.servers.some((name: unknown) => typeof name !== 'string' || !name || name.trim() !== name))
      throw acp.RequestError.invalidParams(undefined, 'Invalid MCP disconnect request.');
    return { sessionId: request.sessionId, servers: request.servers as string[] };
  }, async ({ params }) => {
    await runSessionMcpMutation(params.sessionId, async (signal) => {
      assertLifecycleActive(signal);
      const active = requireAttachedSession(sessions, params.sessionId);
      if (active.activePrompt)
        throw acp.RequestError.invalidParams(undefined, 'MCP disconnect requires a settled prompt.');
      await options.runtime.disconnectSessionMcpServers(params.sessionId, params.servers);
      assertLifecycleActive(signal);
    });
    return {};
  });

  app.onNotification(acp.methods.agent.session.cancel, async ({ params }) => {`);
const digest = name => createHash('sha256').update(readFileSync(join(here, name))).digest('hex');
writeFileSync(join(root, '.oac-native-patch.json'), JSON.stringify({ revision: pin.revision,
  files: { 'patch-native.mjs':digest('patch-native.mjs'), 'native-subagent-admission.mjs':digest('native-subagent-admission.mjs') } }, null, 2)+'\n');
