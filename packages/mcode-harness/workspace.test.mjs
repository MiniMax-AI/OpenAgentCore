import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, mkdir, writeFile, rm, realpath, stat } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { ToolExecutor } from './tool-executor.mjs';

test('worker runs with the initialized workspace and bridge environment', async t => {
  const parent = join(homedir(), '.oac', 'tests');
  await mkdir(parent, { recursive: true });
  const root = await realpath(await mkdtemp(join(parent, 'mcode-workspace-')));
  t.after(() => rm(root, { recursive: true, force: true }));
  const workspace = join(root, 'workspace');
  await mkdir(workspace);
  const worker = join(root, 'worker.mjs');
  await writeFile(worker, `
    import { readFileSync } from 'node:fs';
    const request = JSON.parse(readFileSync(0, 'utf8'));
    console.log(JSON.stringify({tool_name:request.tool,
      text:JSON.stringify({cwd:process.cwd(),root:process.argv[2],value:process.env.OAC_TEST_BRIDGE_ENV}),content:[]}));
  `);
  const scratch = join(root, 'scratch');
  process.env.OAC_TEST_BRIDGE_ENV = 'ordinary';
  t.after(() => { delete process.env.OAC_TEST_BRIDGE_ENV; });
  const profile = { workspace, scratch };
  const executor = new ToolExecutor(profile, worker);
  t.after(() => executor.close());
  assert.ok((await stat(scratch)).isDirectory());
  profile.workspace = root;
  const result = await executor.execute('bash', {});
  assert.deepEqual(JSON.parse(result.text), { cwd: workspace, root: workspace, value: 'ordinary' });
  for (const invalid of ['workspace', workspace + '/../workspace']) {
    assert.throws(() => new ToolExecutor({ workspace: invalid, scratch }, worker), /Invalid workspace profile/);
  }
});
