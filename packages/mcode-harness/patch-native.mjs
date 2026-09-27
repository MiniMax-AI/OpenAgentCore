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
const gate = "  if (!isFeatureEnabled(ceiling, toolName)) return false;";
if (catalog.split(gate).length !== 2) throw new Error('Pinned native tool capability gate changed');
const selectorGate = "  if (!input.selector.allowsTool(input.tool.def.name, input.options.selectorAlias)) return false;\n  if (!isMcpServerAllowed(input.selector, input.options)) return false;";
if (catalog.split(selectorGate).length !== 2) throw new Error('Pinned native selector gate changed');
const withWorkspace = catalog.replace(selectorGate, `  const protectedWorkspace = process.env.OAC_RUNTIME_MCODE_TOOL_POLICY === 'protected-mcp-v1' &&
    input.source === 'configured' && input.options.mcp === true && input.options.serverName === 'oac_workspace';
  if (!protectedWorkspace && !input.selector.allowsTool(input.tool.def.name, input.options.selectorAlias)) return false;
  if (!protectedWorkspace && !isMcpServerAllowed(input.selector, input.options)) return false;`);
writeFileSync(catalogPath, withWorkspace.replace(gate, `  if (process.env.OAC_RUNTIME_MCODE_TOOL_POLICY === 'protected-mcp-v1') {
    if (source === 'builtin' && !DELEGATION_TOOL_NAMES.has(toolName) && !TASK_CONTROL_TOOL_NAMES.has(toolName)) return false;
    if (source === 'builtin-matrix') return false;
  }
${gate}`));
copyFileSync(join(here, 'native-subagent-admission.mjs'), join(root, 'packages/local-runtime/src/background-task/oac-subagent-admission.mjs'));
const digest = name => createHash('sha256').update(readFileSync(join(here, name))).digest('hex');
writeFileSync(join(root, '.oac-native-patch.json'), JSON.stringify({ revision: pin.revision,
  files: { 'patch-native.mjs':digest('patch-native.mjs'), 'native-subagent-admission.mjs':digest('native-subagent-admission.mjs') } }, null, 2)+'\n');
