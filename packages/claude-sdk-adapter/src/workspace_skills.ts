import { linkSync, lstatSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join, posix } from "node:path";

export type WorkspaceSkill = {
  metadata: { type: "inline"; name: string; description: string };
  relative_root: string;
  package_root: string;
};
export const capabilityRoot = "/environment/initialization/capabilities";
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

// The common Runtime has already validated these immutable packages. Native
// explicit component paths must remain within the plugin root after realpath,
// so preserve the tree with hardlinks rather than outward directory symlinks.
function projectPackage(source: string, destination: string): void {
  mkdirSync(destination, { recursive: true, mode: 0o700 });
  if (!lstatSync(destination).isDirectory()) throw new Error("invalid native Skill root");
  for (const entry of readdirSync(source, { withFileTypes: true })) {
    const from = join(source, entry.name), to = join(destination, entry.name);
    if (entry.isDirectory()) {
      projectPackage(from, to);
    } else if (entry.isFile()) {
      try { linkSync(from, to); }
      catch (error) {
        if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
        const sourceInfo = lstatSync(from), installed = lstatSync(to);
        if (!installed.isFile() || installed.dev !== sourceInfo.dev || installed.ino !== sourceInfo.ino) throw new Error("invalid native Skill root");
      }
    } else { throw new Error("invalid native Skill root"); }
  }
}

// Only the generated envelope is activated. Original plugin control files stay
// beneath content; exact Skill roots, not public plugin manifests, drive loading.
export function workspaceSkills(skills: readonly WorkspaceSkill[]): { paths: string[]; names: string[] } | undefined {
  if (!skills.length) return undefined;
  const packages = new Map<string, WorkspaceSkill[]>();
  for (const skill of skills) {
    const body = readFileSync(join(capabilityRoot, skill.relative_root, "SKILL.md"), "utf8");
    if (/(?<=^|\s)!`[^`]+`/m.test(body) || /```!\s*\n?[\s\S]*?\n?```/.test(body)) {
      throw new Error("unsupported native Skill activation");
    }
    const selected = packages.get(skill.package_root) ?? [];
    selected.push(skill);
    packages.set(skill.package_root, selected);
  }
  const result = { paths: [] as string[], names: [] as string[] };
  for (const [root, selected] of packages) {
    const name = `environment-skills-${result.paths.length}`;
    const path = join(capabilityRoot, "native", "claude", name);
    projectPackage(join(capabilityRoot, root), join(path, "content"));
    mkdirSync(join(path, ".claude-plugin"), { recursive: true, mode: 0o700 });
    const manifest = JSON.stringify({ name, description: "Environment Skills", skills: selected.map(skill =>
      "./" + posix.join("content", posix.relative(root, skill.relative_root))) });
    const target = join(path, ".claude-plugin", "plugin.json");
    try { writeFileSync(target, manifest, { flag: "wx", mode: 0o400 }); }
    catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "EEXIST" || !lstatSync(target).isFile() || readFileSync(target, "utf8") !== manifest) throw error;
    }
    result.paths.push(path);
    result.names.push(...selected.map(skill => `${name}:${skill.metadata.name}`));
  }
  return result;
}
