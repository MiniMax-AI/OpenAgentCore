import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readdir, readFile, lstat, rm } from "node:fs/promises";
import { join } from "node:path";
import { homedir } from "node:os";
import { zipSync } from "fflate";
import { AppError } from "./store.mjs";

const execute = promisify(execFile);
const part = (value) =>
  typeof value === "string" && /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/.test(value);
// Reuse the original product's pinned upstream installer rather than implement
// repository discovery, cloning, and Skills layout resolution ourselves.
export async function previewRegistrySkill(body, run = execute) {
  const pieces = typeof body.source === "string" ? body.source.split("/") : [];
  if (pieces.length !== 2 || !pieces.every(part) || !part(body.slug))
    throw new AppError(400, "请填写 GitHub owner/repo 和有效的 Skill 名称。");
  const root = join(homedir(), ".oac", "cache", "parsar-skill-imports");
  await mkdir(root, { recursive: true, mode: 0o700 });
  const directory = await mkdtemp(join(root, "import-"));
  try {
    try {
      await run(
        "npx",
        [
          "--yes",
          "skills@1.5.23",
          "add",
          body.source,
          "--skill",
          body.slug,
          "--agent",
          "claude-code",
          "--copy",
          "--yes",
        ],
        {
          cwd: directory,
          timeout: 120000,
          maxBuffer: 1024 * 1024,
          env: { ...process.env, DISABLE_TELEMETRY: "1" },
        },
      );
    } catch {
      throw new AppError(
        502,
        "下载 Skill 失败。请检查名称、仓库访问及服务器上的 Node.js、npx 和 Git。",
      );
    }
    const skill = join(directory, ".claude", "skills", body.slug);
    const files = {};
    let total = 0,
      count = 0;
    const walk = async (path, prefix = "") => {
      for (const entry of await readdir(path, { withFileTypes: true })) {
        const absolute = join(path, entry.name),
          name = prefix + entry.name;
        const stat = await lstat(absolute);
        if (stat.isSymbolicLink() || (!stat.isDirectory() && !stat.isFile()))
          throw new AppError(400, "Skill 包含不支持的文件类型。");
        if (stat.isDirectory()) {
          await walk(absolute, `${name}/`);
          continue;
        }
        total += stat.size;
        count++;
        if (total > 32 * 1024 * 1024 || count > 1000)
          throw new AppError(
            413,
            "Skill 最多包含 1000 个文件，合计不超过 32 MiB。",
          );
        files[`${body.slug}/${name}`] = new Uint8Array(
          await readFile(absolute),
        );
      }
    };
    try {
      if (!(await lstat(skill)).isDirectory())
        throw new AppError(400, "Skill 根路径必须是目录。");
      await walk(skill);
    } catch (error) {
      if (error instanceof AppError) throw error;
      throw new AppError(400, "仓库未返回所选 Skill 的完整目录。");
    }
    const instructions = files[`${body.slug}/SKILL.md`];
    if (!instructions) throw new AppError(400, "所选 Skill 缺少 SKILL.md。");
    return {
      source: body.source,
      slug: body.slug,
      archive: Buffer.from(zipSync(files)).toString("base64"),
      files: Object.keys(files),
      instructions: Buffer.from(instructions).toString("utf8").slice(0, 256000),
    };
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}
