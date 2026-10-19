import { existsSync, mkdirSync, readlinkSync, symlinkSync } from "node:fs";
import { join, posix } from "node:path";

export type WorkspaceSkill = {
  metadata: { type: "inline"; name: string; description: string };
  relative_root: string;
  package_root: string;
};
const relativePath = (value: unknown): value is string => typeof value === "string" && value !== "." &&
  value.length > 0 && !value.startsWith("/") && !/[\\\x00-\x1f\x7f]/.test(value) &&
  posix.normalize(value) === value && !value.split("/").includes("..");

export function parseSkills(value: unknown): WorkspaceSkill[] {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > 50) throw new Error("invalid_request");
  const seen = new Set<string>();
  for (const skill of value) {
    if (!skill || typeof skill !== "object" || Array.isArray(skill) ||
        Object.keys(skill).some(key => !["metadata", "relative_root", "package_root"].includes(key)) ||
        !relativePath(skill.relative_root) || !relativePath(skill.package_root) ||
        (skill.relative_root !== skill.package_root && !skill.relative_root.startsWith(skill.package_root + "/"))) throw new Error("invalid_request");
    const metadata = skill.metadata;
    if (!metadata || typeof metadata !== "object" || Array.isArray(metadata) ||
        Object.keys(metadata).some(key => !["type", "name", "description"].includes(key)) ||
        metadata.type !== "inline" || typeof metadata.name !== "string" || !/^[a-z0-9]+(?:[-_][a-z0-9]+)*$/.test(metadata.name) ||
        metadata.name.length > 64 || typeof metadata.description !== "string" || !metadata.description || seen.has(metadata.name)) throw new Error("invalid_request");
    seen.add(metadata.name);
  }
  return value as WorkspaceSkill[];
}

// Skills install as user Skills under the private state path, as a local ~/.claude/skills does, so
// native lists their bare names. The common Runtime has already validated the immutable packages.
export function installSkills(skills: readonly WorkspaceSkill[], capabilityRoot: string, state: string): string[] {
  if (!skills.length) return [];
  const directory = join(state, "skills");
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  for (const skill of skills) {
    const link = join(directory, skill.metadata.name), target = join(capabilityRoot, skill.relative_root);
    // Native loads a user Skill with a plugin manifest as a plugin, with its hooks and MCP servers.
    if (existsSync(join(target, ".claude-plugin"))) throw new Error("invalid_request");
    // A junction needs no Windows symlink privilege; POSIX ignores the type.
    try { symlinkSync(target, link, "junction"); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== "EEXIST" || readlinkSync(link) !== target) throw error; }
  }
  return skills.map(skill => skill.metadata.name);
}
