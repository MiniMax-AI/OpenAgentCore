import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { buildBundle } from './build-native-installer.mjs';

async function fixture(t) {
  const parent = join(homedir(), '.oac', 'tests');
  await mkdir(parent, { recursive: true });
  const root = await mkdtemp(join(parent, 'native-bundle-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const daemon = join(root, 'daemon');
  const sandboxIo = join(root, 'sandbox-io');
  await writeFile(daemon, '#!/bin/sh\necho test-version\n', { mode: 0o755 });
  await writeFile(sandboxIo, 'sandbox bytes', { mode: 0o755 });
  return { daemon, sandboxIo, output: join(root, 'output') };
}

test('bundle contains only launcher, Sandbox I/O and platform metadata', async t => {
  const options = await fixture(t);
  if (process.platform !== 'linux' || process.arch !== 'x64') {
    await assert.rejects(buildBundle(options), /Linux amd64/);
    return;
  }
  const manifest = await buildBundle(options);
  assert.deepEqual(await readdir(options.output), ['bundle.json', 'oac-daemon', 'oac-sandbox-io']);
  assert.deepEqual(manifest, { schema: 1, daemon_version: 'test-version', os: 'linux', arch: 'amd64' });
  assert.equal(await readFile(join(options.output, 'oac-sandbox-io'), 'utf8'), 'sandbox bytes');
  await assert.rejects(buildBundle(options), /already exists/);
});

test('bundle rejects missing programs, relative paths and obsolete components', { skip: process.platform !== 'linux' || process.arch !== 'x64' }, async t => {
  const options = await fixture(t);
  await assert.rejects(buildBundle({ ...options, sandboxIo: undefined }), /Missing --sandbox-io/);
  await assert.rejects(buildBundle({ ...options, daemon: 'relative' }), /absolute/);
  await assert.rejects(buildBundle({ ...options, node: options.daemon }), /Unknown bundle option/);
});
