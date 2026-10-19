import test from 'node:test';
import assert from 'node:assert/strict';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { execFileSync } from 'node:child_process';

// Opt in with a checkout of the revision pinned in source.json.
const checkout = process.env.OAC_TEST_MCODE_SOURCE;
const pin = JSON.parse(readFileSync(new URL('source.json', import.meta.url), 'utf8'));
const files = ['packages/local-runtime/src/background-task/store.ts', 'packages/tui/src/acp/agent.ts',
  'packages/local-runtime-v2/src/service/turn-system/agent-host/execution/executor.ts'];

test('the patch applies to the pinned native source', { skip: !checkout }, () => {
  const root = mkdtempSync(join(tmpdir(), 'oac-mcode-patch-'));
  try {
    for (const file of files) {
      mkdirSync(dirname(join(root, file)), { recursive: true });
      cpSync(join(checkout, file), join(root, file));
    }
    writeFileSync(join(root, '.oac-source-revision'), pin.revision + '\n');
    execFileSync(process.execPath, [new URL('patch-native.mjs', import.meta.url).pathname], { env: { ...process.env, MCODE_SOURCE: root } });
    const read = file => readFileSync(join(root, file), 'utf8');
    assert.match(read(files[0]), /enforceSubagentAdmission\(db, task\)/);
    assert.match(read(files[1]), /version: 2, backgroundBash: process\.env\.OAC_RUNTIME_MCODE_BACKGROUND_BASH !== 'off'/);
    assert.match(read(files[2]), /allowBashAutoPromotion: backgroundBash,\n\s+canConsumeBackgroundBashOutput: backgroundBash,/);
    assert.equal(JSON.parse(read('.oac-native-patch.json')).revision, pin.revision);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
