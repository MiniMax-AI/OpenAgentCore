import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

test('catalog publishes Linux amd64 and rejects a foreign revision', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'oac-native-catalog-'));
  try {
    const input = join(directory, 'input');
    const output = join(directory, 'output');
    const bundle = join(directory, 'bundle');
    await Promise.all([mkdir(input), mkdir(bundle)]);
    const version = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
    for (const [ci, os, arch] of [['Linux-X64', 'linux', 'amd64']]) {
      await writeFile(join(bundle, 'bundle.json'), JSON.stringify({ daemon_version: version, os, arch }));
      execFileSync('tar', ['-czf', join(input, `oac-native-installer-${ci}.tar.gz`), '-C', bundle, './bundle.json']);
    }
    const script = resolve('scripts/build-native-catalog.mjs');
    execFileSync(process.execPath, [script, input, output]);
    const catalog = JSON.parse(await readFile(join(output, 'catalog.json'), 'utf8'));
    assert.equal(catalog.version, version);
    assert.equal(Object.keys(catalog.artifacts).length, 1);
    const linux = await readFile(join(input, 'oac-native-installer-Linux-X64.tar.gz'));
    assert.deepEqual(await readFile(join(output, 'linux-amd64.tar.gz')), linux);
    assert.equal(catalog.artifacts['linux-amd64'].sha256, createHash('sha256').update(linux).digest('hex'));
    await writeFile(join(bundle, 'bundle.json'), JSON.stringify({ daemon_version: 'foreign', os: 'linux', arch: 'amd64' }));
    execFileSync('tar', ['-czf', join(input, 'oac-native-installer-Linux-X64.tar.gz'), '-C', bundle, './bundle.json']);
    assert.throws(() => execFileSync(process.execPath, [script, input, output], { stdio: 'pipe' }), error => error.stderr.toString().includes('Mismatched native artifact'));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
