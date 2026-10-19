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
writeFileSync(acpPath, acp.replace(marker, "      _meta: {\n        'oac/subagents': { version: 2, backgroundBash: process.env.OAC_RUNTIME_MCODE_BACKGROUND_BASH !== 'off', maxConcurrent: Number(process.env.OAC_RUNTIME_MCODE_MAX_SUBAGENTS ?? 0) },\n        'minimax-code/extensions': {"));
// Background bash outlives the Turn. With the adapter's switch set, bash runs in the foreground only;
// task_output stays admitted, so Subagent results remain readable.
const executorPath = join(root, 'packages/local-runtime-v2/src/service/turn-system/agent-host/execution/executor.ts');
const executor = readFileSync(executorPath, 'utf8');
const context = "    const toolContext = {\n      ...toolResolution.context,";
const promotion = "      allowBashAutoPromotion: canConsumeBackgroundBashOutput,\n      canConsumeBackgroundBashOutput,\n";
if (executor.split(context).length !== 2 || executor.split(promotion).length !== 2) throw new Error('Pinned native background bash gate changed');
writeFileSync(executorPath, executor.replace(context, "    const backgroundBash =\n      canConsumeBackgroundBashOutput && process.env.OAC_RUNTIME_MCODE_BACKGROUND_BASH !== 'off';\n" + context)
  .replace(promotion, "      allowBashAutoPromotion: backgroundBash,\n      canConsumeBackgroundBashOutput: backgroundBash,\n"));
copyFileSync(join(here, 'native-subagent-admission.mjs'), join(root, 'packages/local-runtime/src/background-task/oac-subagent-admission.mjs'));
const digest = name => createHash('sha256').update(readFileSync(join(here, name))).digest('hex');
writeFileSync(join(root, '.oac-native-patch.json'), JSON.stringify({ revision: pin.revision,
  files: { 'patch-native.mjs':digest('patch-native.mjs'), 'native-subagent-admission.mjs':digest('native-subagent-admission.mjs') } }, null, 2)+'\n');
