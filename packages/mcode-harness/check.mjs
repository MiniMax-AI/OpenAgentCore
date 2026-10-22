import { accessSync, constants, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { delimiter, join } from 'node:path';

if (!['linux', 'darwin', 'win32'].includes(process.platform)) throw new Error('MiniMax native execution requires Linux, macOS or Windows');
for (const file of ['subagent-snapshot.mjs', 'native/cli.js', 'native-patch.json']) accessSync(new URL(file, import.meta.url));
// Native file search runs rg. Native shell commands run Bash on Unix; on Windows
// MiniMax Code selects PowerShell itself, as in a local run.
for (const command of process.platform === 'win32' ? ['rg'] : ['bash', 'rg']) requireCommand(command);
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

// requireCommand finds an executable on PATH as the native process will.
function requireCommand(name) {
  const names = process.platform === 'win32' ? (process.env.PATHEXT ?? '.EXE').split(';').filter(Boolean).map(ext => name + ext) : [name];
  for (const directory of (process.env.PATH ?? '').split(delimiter).filter(Boolean)) {
    for (const candidate of names) {
      try { accessSync(join(directory, candidate), process.platform === 'win32' ? constants.F_OK : constants.X_OK); return; } catch { /* Try the next PATH entry. */ }
    }
  }
  throw new Error(`Required command ${name} is missing`);
}
