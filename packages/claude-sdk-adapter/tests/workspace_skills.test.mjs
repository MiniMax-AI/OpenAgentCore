import assert from 'node:assert/strict';
import test from 'node:test';
import { parseSkills } from '../dist/workspace_skills.js';

test('Runtime Skill paths remain inside the declared package; public metadata is not a path descriptor', () => {
  const skill = { metadata: { type: 'inline', name: 'proof', description: 'A proof.' }, relative_root: 'plugins/0/custom/proof', package_root: 'plugins/0' };
  assert.deepEqual(parseSkills([skill]), [skill]);
  for (const invalid of [
    { ...skill, relative_root: '/private' },
    { ...skill, relative_root: 'plugins/0/../../private' },
    { ...skill, relative_root: 'plugins/1/proof' },
    { ...skill, package_root: '.' },
    { ...skill, metadata: { ...skill.metadata, name: '../escape' } },
    skill.metadata,
  ]) assert.throws(() => parseSkills([invalid]), /invalid_request/);
  assert.throws(() => parseSkills([skill, skill]), /invalid_request/);
});
