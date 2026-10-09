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
const digest = name => createHash('sha256').update(readFileSync(join(here, name))).digest('hex');
writeFileSync(join(root, '.oac-native-patch.json'), JSON.stringify({ revision: pin.revision,
  files: { 'patch-native.mjs':digest('patch-native.mjs'), 'native-subagent-admission.mjs':digest('native-subagent-admission.mjs') } }, null, 2)+'\n');
