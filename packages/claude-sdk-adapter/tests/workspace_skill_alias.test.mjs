import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { WorkspaceProfile } from "../dist/workspace.js";

test("selected public Skill names resolve to their native plugin identities", async t => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "oac-skill-alias-")));
  const previous = process.env;
  process.env = { ...previous, HOME: root, CLAUDE_CONFIG_DIR: root };
  delete process.env.CLAUDE_CODE_PROJECT_DIR_NAME;
  t.after(() => { process.env = previous; rmSync(root, { recursive: true, force: true }); });
  const skills = ["first", "second"].map((name, index) => {
    const package_root = `plugins/${index}`;
    const relative_root = `${package_root}/skills/${name}`;
    mkdirSync(join(root, relative_root), { recursive: true });
    writeFileSync(join(root, relative_root, "SKILL.md"), `---\nname: ${name}\ndescription: Selected fixture.\n---\nFixture.`);
    return { metadata: { type: "inline", name, description: "Selected fixture." }, package_root, relative_root };
  });
  const profile = new WorkspaceProfile(root, { home: root, state: root, scratch: root, env_names: [], skills, capability_root: root });
  const context = { signal: new AbortController().signal };
  for (const [index, name] of ["first", "second"].entries()) {
    const qualified = `environment-skills-${index}:${name}`;
    for (const skill of [name, qualified]) {
      const input = { skill, args: "fixture argument" };
      const result = await profile.beforeTool({ hook_event_name: "PreToolUse", tool_use_id: "call", tool_name: "Skill", tool_input: input }, "call", context);
      assert.deepEqual(result.hookSpecificOutput.updatedInput, { ...input, skill: qualified });
    }
  }
  for (const skill of ["unselected", "other:first", "environment-skills-0:second"]) {
    assert.deepEqual(await profile.beforeTool({ hook_event_name: "PreToolUse", tool_use_id: "call", tool_name: "Skill", tool_input: { skill } }, "call", context), {});
  }
});
