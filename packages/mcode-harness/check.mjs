import { accessSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';

if (!['linux', 'darwin'].includes(process.platform)) throw new Error('MiniMax native execution requires Linux or macOS');
for (const file of ['subagent-snapshot.mjs', 'native/cli.js', 'native-patch.json']) accessSync(new URL(file, import.meta.url));
for (const command of ['bash', 'rg']) execFileSync('which', [command], { stdio: 'ignore' });
const pin = JSON.parse(readFileSync(new URL('source.json', import.meta.url)));
const patch = JSON.parse(readFileSync(new URL('native-patch.json', import.meta.url)));
if (patch.revision !== pin.revision || !/^[a-f0-9]{64}$/.test(patch.files?.['patch-native.mjs'] ?? '') ||
    !/^[a-f0-9]{64}$/.test(patch.files?.['native-subagent-admission.mjs'] ?? '')) throw new Error('Native patch provenance mismatch');
// Load the installed native closure, never a global addon or user database.
const require = createRequire(import.meta.url);
try {
  const Database = require(fileURLToPath(new URL('./native/node_modules/better-sqlite3', import.meta.url)));
  const database = new Database(':memory:');
  database.close();
} catch { throw new Error('Native database dependency check failed'); }
process.stdout.write(JSON.stringify({ protocol: 3, native: pin.version, source: pin.revision }) + '\n');
