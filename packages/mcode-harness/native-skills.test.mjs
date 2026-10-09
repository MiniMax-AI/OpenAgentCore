import test from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { homedir } from 'node:os';
import { pathToFileURL } from 'node:url';

const root = process.env.MCODE_SOURCE;
test('native Skill refresh bounds independent reads and retains file identity and snapshot order', { skip: !root }, async t => {
  const require = createRequire(join(root, 'package.json'));
  const { build } = require('esbuild');
  const artifacts = join(homedir(), '.oac', 'tests');
  await fs.mkdir(artifacts, { recursive: true });
  const directory = await fs.mkdtemp(join(artifacts, 'mcode-skills-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const result = await build({
    entryPoints: [join(root, 'packages/agent-modules/skills/src/registry.ts')],
    bundle: true, write: false, platform: 'node', format: 'esm', target: 'node22',
    nodePaths: [join(root, 'node_modules')],
  });
  const bundle = join(directory, 'skills.mjs');
  await fs.writeFile(bundle, result.outputFiles[0].text);
  const { SkillRegistry } = await import(pathToFileURL(bundle).href);
  const global = join(directory, 'global'), project = join(directory, 'project');
  const outside = join(directory, 'outside');
  for (const path of [global, project, outside]) await fs.mkdir(path);
  const body = name => `---\nname: ${name}\ndescription: Test ${name}.\n---\n# ${name}\nTest body.\n`;
  for (let i = 0; i < 9; i++) {
    await fs.mkdir(join(global, `skill-${i}`));
    await fs.writeFile(join(global, `skill-${i}`, 'SKILL.md'), body(`skill-${i}`));
  }
  await fs.mkdir(join(project, 'duplicate'));
  await fs.writeFile(join(project, 'duplicate/SKILL.md'), body('skill-0'));
  await fs.writeFile(join(outside, 'SKILL.md'), body('outside'));
  await fs.symlink(outside, join(global, 'linked-directory'));
  await fs.mkdir(join(global, 'linked-file'));
  await fs.symlink(join(outside, 'SKILL.md'), join(global, 'linked-file/SKILL.md'));
  const roots = [
    { id: 'global', kind: 'global', rootPath: global },
    { id: 'project', kind: 'project', rootPath: project },
    { id: 'absent', kind: 'global', rootPath: join(directory, 'absent') },
  ];
  const registry = new SkillRegistry(roots);
  const first = await registry.refresh();
  assert.equal(first.metrics.filesRead, 11);
  assert.equal(first.metrics.winners, 10);
  assert.equal(first.winners.find(entry => entry.name === 'skill-0').rootId, 'project');
  assert.deepEqual(first.diagnostics.map(value => value.code), ['skill_symlink_rejected', 'root_unreadable']);
  const cached = await registry.refresh();
  assert.deepEqual(cached.entries, first.entries);
  assert.deepEqual(cached.diagnostics, first.diagnostics);
  assert.equal(cached.metrics.filesRead, 0);
  assert.equal(cached.metrics.filesReused, 11);

  await fs.writeFile(join(global, 'skill-3/SKILL.md'), body('skill-3') + 'Changed.\n');
  await fs.rm(join(global, 'skill-1'), { recursive: true });
  await fs.rm(join(global, 'skill-2/SKILL.md'));
  await fs.symlink(join(outside, 'SKILL.md'), join(global, 'skill-2/SKILL.md'));
  const changed = await registry.refresh();
  assert.equal(changed.metrics.filesRead, 1);
  assert.equal(changed.metrics.filesReused, 8);
  assert.equal(changed.entries.some(entry => ['skill-1', 'skill-2'].includes(entry.name)), false);
  assert.match(changed.entries.find(entry => entry.name === 'skill-3').content, /Changed\./);

  // Deliberately reverse completion order; result and diagnostic order must
  // remain the native candidate order, with no more than four opens in flight.
  const open = fs.open;
  let active = 0, maximum = 0;
  fs.open = async (path, ...args) => {
    active++;
    maximum = Math.max(maximum, active);
    try {
      await new Promise(resolve => setTimeout(resolve, String(path).includes('skill-4') ? 20 : 5));
      return await open(path, ...args);
    } finally { active--; }
  };
  try {
    const reordered = await new SkillRegistry(roots).refresh();
    assert.deepEqual(reordered.entries, changed.entries);
    assert.deepEqual(reordered.diagnostics, changed.diagnostics);
    assert.ok(maximum > 1 && maximum <= 4, `concurrent opens: ${maximum}`);
  } finally { fs.open = open; }

  // Candidate inspection failures finish in reverse order within a batch.
  // Preserve the original diagnostic order, not completion order.
  const completed = [];
  fs.open = async (path, ...args) => {
    const name = ['skill-6', 'skill-7'].find(name => String(path) === join(global, name, 'SKILL.md'));
    if (!name) return open(path, ...args);
    await new Promise(resolve => setTimeout(resolve, name === 'skill-6' ? 20 : 1));
    completed.push(name);
    throw Object.assign(new Error('Fixture permission denied'), { code: 'EACCES' });
  };
  try {
    const failed = await new SkillRegistry(roots).refresh();
    assert.deepEqual(completed, ['skill-7', 'skill-6']);
    const failures = failed.diagnostics.filter(value => value.code === 'skill_stat_failed');
    assert.deepEqual(failures.map(value => value.locationUri),
      ['skill-6', 'skill-7'].map(name => changed.entries.find(entry => entry.name === name).locationUri));
  } finally { fs.open = open; }

  // The same open-file identity check must still reject replacement between
  // inspection and content read, even when other files finish independently.
  let opens = 0;
  fs.open = async (path, ...args) => {
    const handle = await open(path, ...args);
    if (String(path) !== join(global, 'skill-3/SKILL.md') || ++opens !== 2) return handle;
    return new Proxy(handle, { get(target, key) {
      if (key === 'stat') return async () => { const stat = await target.stat(); stat.ino++; return stat; };
      const value = Reflect.get(target, key);
      return typeof value === 'function' ? value.bind(target) : value;
    } });
  };
  try {
    const replaced = await new SkillRegistry(roots).refresh();
    assert.ok(replaced.diagnostics.some(value => value.code === 'skill_changed_during_read'));
    assert.equal(replaced.entries.some(entry => entry.name === 'skill-3'), false);
  } finally { fs.open = open; }
});
