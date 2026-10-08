import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { chmod, copyFile, cp, lstat, mkdir, mkdtemp, readFile, rename, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import test from 'node:test';
import { buildBundle, copyComponent, prepareNodeEntrypoints, validateNodeCommands } from './build-native-installer.mjs';

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'oac-native-bundle-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = join(root, 'source'); await mkdir(source);
  return { root, source, output: join(root, 'output') };
}

test('component preserves bytes and executable metadata with contained links flattened', async t => {
  const { source, output } = await fixture(t);
  await mkdir(join(source, 'lib'));
  await writeFile(join(source, 'lib', 'native'), 'native bytes');
  await chmod(join(source, 'lib', 'native'), 0o755);
  await symlink(join(source, 'lib'), join(source, 'linked'), process.platform === 'win32' ? 'junction' : 'dir');
  const files = await copyComponent(source, output);
  assert.equal(await readFile(join(output, 'linked/native'), 'utf8'), 'native bytes');
  assert.equal((await lstat(join(output, 'linked'))).isSymbolicLink(), false);
  assert.equal(files['linked/native'].sha256, createHash('sha256').update('native bytes').digest('hex'));
  if (process.platform !== 'win32') assert.equal(files['linked/native'].executable, true);
  assert.deepEqual(Object.keys(files).sort(), ['lib/native', 'linked/native']);
});

test('component rejects escaping directory links', async t => {
  const { root, source, output } = await fixture(t);
  await mkdir(join(root, 'outside')); await writeFile(join(root, 'outside/secret'), 'not bundled');
  await symlink(join(root, 'outside'), join(source, 'external'), process.platform === 'win32' ? 'junction' : 'dir');
  await assert.rejects(copyComponent(source, output), /escapes/);
});

test('component rejects cyclic links without recursing forever', async t => {
  const { source, output } = await fixture(t);
  await symlink(source, join(source, 'cycle'), process.platform === 'win32' ? 'junction' : 'dir');
  await assert.rejects(copyComponent(source, output), /cycle/);
});

test('bundle does not replace an existing output or accept relative paths', async t => {
  const { source, output } = await fixture(t);
  await mkdir(output); await writeFile(join(output, 'keep'), 'existing install');
  await assert.rejects(buildBundle({ daemon: join(source, 'daemon'), node: source, codex: source, output }), /already exists/);
  assert.equal(await readFile(join(output, 'keep'), 'utf8'), 'existing install');
  await assert.rejects(buildBundle({ daemon: 'relative', node: source, codex: source, output }), /absolute/);
});

test('bundle requires a selected harness and rejects output inside sources', async t => {
  const { source, output } = await fixture(t);
  await assert.rejects(buildBundle({ daemon: join(source, 'daemon'), node: source, output }), /Harness source/);
  await assert.rejects(buildBundle({ daemon: join(source, 'daemon'), node: source, codex: source, output: join(source, 'out') }), /outside/);
});

test('bundle carries oac-sandbox-io exactly on Linux', async t => {
  const { source, output } = await fixture(t);
  const options = { daemon: join(source, 'daemon'), node: source, codex: source, output };
  if (process.platform === 'linux') await assert.rejects(buildBundle(options), /Missing --sandbox-io/);
  else await assert.rejects(buildBundle({ ...options, sandboxIo: join(source, 'oac-sandbox-io') }), /Linux-only/);
});

test('component rejects nonportable paths', { skip: process.platform === 'win32' }, async t => {
  const { source, output } = await fixture(t);
  await writeFile(join(source, 'bad:name'), 'bad');
  await assert.rejects(copyComponent(source, output), /non-portable/);
});

test('bundle rejects output entering a component through an aliased parent', async t => {
  const { root, source } = await fixture(t);
  const alias = join(root, 'alias');
  await symlink(source, alias, process.platform === 'win32' ? 'junction' : 'dir');
  await assert.rejects(buildBundle({ daemon: join(source, 'daemon'), node: source, codex: source, output: join(alias, 'new-parent', 'out') }), /outside/);
});

test('real npm and npx remain runnable from PATH after regular-file copying and relocation', async t => {
  const { root, source, output } = await fixture(t);
  const windows = process.platform === 'win32';
  const nodeRoot = windows ? dirname(process.execPath) : dirname(dirname(process.execPath));
  const bin = windows ? source : join(source, 'bin');
  const npmRelative = windows ? 'node_modules/npm' : 'lib/node_modules/npm';
  await mkdir(bin, { recursive: true });
  await copyFile(process.execPath, join(bin, windows ? 'node.exe' : 'node'));
  await chmod(join(bin, windows ? 'node.exe' : 'node'), 0o755);
  await cp(join(nodeRoot, npmRelative), join(source, npmRelative), { recursive: true });
  for (const name of ['npm', 'npx']) {
    if (windows) await copyFile(join(nodeRoot, `${name}.cmd`), join(bin, `${name}.cmd`));
    else await symlink(`../lib/node_modules/npm/bin/${name}-cli.js`, join(bin, name));
  }
  const files = await copyComponent(source, output);
  // This is the original failure: the copied Unix JS entrypoint lost its scope.
  if (!windows) await assert.rejects(validateNodeCommands(output, root), /compatibility probe/);
  await prepareNodeEntrypoints(output, files);
  const relocated = join(root, 'relocated node');
  await rename(output, relocated);
  await validateNodeCommands(relocated, root);
  for (const name of ['npm', 'npx']) {
    const path = windows ? `${name}.cmd` : `bin/${name}`;
    assert.equal((await lstat(join(relocated, path))).isSymbolicLink(), false);
    assert.equal(files[path].sha256, createHash('sha256').update(await readFile(join(relocated, path))).digest('hex'));
    assert.equal(files[path].executable, true);
  }
});
