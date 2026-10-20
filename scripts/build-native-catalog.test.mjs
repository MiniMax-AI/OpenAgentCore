import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

test('catalog lists each platform base and component archive and rejects a foreign revision', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'oac-native-catalog-'));
  try {
    const input = join(directory, 'input');
    const output = join(directory, 'output');
    const bundle = join(directory, 'bundle');
    await Promise.all([mkdir(input), mkdir(bundle)]);
    const version = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
    const pack = (name, daemon_version, os, arch, components) => {
      return writeFile(join(bundle, 'bundle.json'), JSON.stringify({ daemon_version, os, arch, components, filler: 'x'.repeat(2 * 1024 * 1024) })).then(() => {
        execFileSync('tar', ['-czf', join(input, `${name}.tar.gz`), '-C', bundle, 'bundle.json']);
      });
    };
    for (const [os, arch] of [['linux', 'amd64'], ['darwin', 'arm64'], ['windows', 'amd64']]) {
      const components = { node: {}, codex: {} };
      await pack(`${os}-${arch}`, version, os, arch, components);
      for (const name of Object.keys(components)) await pack(`${os}-${arch}-${name}`, version, os, arch, {});
    }
    const script = resolve('scripts/build-native-catalog.mjs');
    execFileSync(process.execPath, [script, input, output]);
    const catalog = JSON.parse(await readFile(join(output, 'catalog.json'), 'utf8'));
    assert.equal(catalog.version, version);
    assert.deepEqual(Object.keys(catalog.artifacts).sort(), ['darwin-arm64', 'darwin-arm64-codex', 'darwin-arm64-node', 'linux-amd64', 'linux-amd64-codex', 'linux-amd64-node', 'windows-amd64', 'windows-amd64-codex', 'windows-amd64-node']);
    const part = await readFile(join(input, 'linux-amd64-codex.tar.gz'));
    assert.deepEqual(await readFile(join(output, 'linux-amd64-codex.tar.gz')), part);
    assert.equal(catalog.artifacts['linux-amd64-codex'].sha256, createHash('sha256').update(part).digest('hex'));
    await rm(join(input, 'linux-amd64-codex.tar.gz'));
    assert.throws(() => execFileSync(process.execPath, [script, input, output], { stdio: 'pipe' }), error => error.stderr.toString().includes('do not match the components'));
    await pack('linux-amd64', 'foreign', 'linux', 'amd64', {});
    assert.throws(() => execFileSync(process.execPath, [script, input, output], { stdio: 'pipe' }), error => error.stderr.toString().includes('Mismatched native artifact'));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
