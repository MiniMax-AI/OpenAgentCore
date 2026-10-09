import assert from 'node:assert/strict';
import test from 'node:test';
import { existsSync } from 'node:fs';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const artifact = process.env.OAC_TEST_MCODE_CLEANUP_ARTIFACT ?? dirname(fileURLToPath(import.meta.url));
const available = existsSync(join(artifact, 'dist/worker.mjs'));
const quote = value => "'" + value.replaceAll("'", "'\\''") + "'";

for (const operation of ['cancel', 'close']) {
  test(`native Bash ${operation} settles detached parent and child before returning`, { skip: !available, timeout: 20_000 }, async t => {
    const root = await mkdtemp(join(tmpdir(), 'oac-mcode-cancel-'));
    const scratch = join(root, 'scratch');
    await mkdir(scratch);
    const pids = [];
    let cleaned = false;
    t.after(async () => {
      if (!cleaned) for (const pid of pids) { try { process.kill(pid, 'SIGKILL'); } catch {} }
      await rm(root, { recursive: true, force: true });
    });
    await writeFile(join(root, 'ticks.mjs'), `
      import { spawn } from 'node:child_process';
      import { appendFileSync, writeFileSync } from 'node:fs';
      const name=process.argv[2];
      process.on('SIGTERM',()=>{});
      writeFileSync(name+'.pid',String(process.pid));
      if(name==='parent') spawn(process.execPath,['ticks.mjs','child'],{stdio:'inherit'});
      setInterval(()=>appendFileSync(name+'.ticks','tick\\n'),20);
    `);
    const { ToolExecutor } = await import(pathToFileURL(join(artifact, 'tool-executor.mjs')));
    const executor = new ToolExecutor({ workspace: root, scratch });
    t.after(() => executor.close());
    const abort = new AbortController();
    const finished = executor.execute('bash', { command: `${quote(process.execPath)} ticks.mjs parent` }, abort.signal);
    const rejected = assert.rejects(finished);
    for (const name of ['parent', 'child']) {
      let started = false;
      for (let i = 0; i < 500; i++) {
        try {
          if ((await readFile(join(root, name+'.ticks'))).length > 0) {
            pids.push(Number(await readFile(join(root, name+'.pid'), 'utf8')));
            started = true; break;
          }
        } catch {}
        await delay(10);
      }
      assert.equal(started, true, name+' began writing');
    }
    if (operation === 'cancel') abort.abort(); else await executor.close();
    await rejected;
    const before = await Promise.all(['parent','child'].map(name => readFile(join(root,name+'.ticks'),'utf8')));
    await delay(300);
    const after = await Promise.all(['parent','child'].map(name => readFile(join(root,name+'.ticks'),'utf8')));
    assert.deepEqual(after, before, 'cancel receipt preceded native descendant cleanup');
    cleaned = true;
  });
}
