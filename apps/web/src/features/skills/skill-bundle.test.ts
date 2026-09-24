import { deflateRawSync } from "node:zlib";
import { describe, expect, it } from "vitest";

import {
  parseSkillFrontmatter,
  previewSkillFolder,
  previewSkillZip,
  readZipEntries,
  SKILL_EXPANDED_MAX_BYTES,
  SKILL_ZIP_MAX_BYTES,
  type SkillFolderFile,
} from "./skill-bundle";

const manifest = "---\nname: report\ndescription: Create the report.\n---\nFollow the report procedure.\n";

function folder(entries: Record<string, string | Blob>): SkillFolderFile[] {
  return Object.entries(entries).map(([path, value]) => ({ path, file: typeof value === "string" ? new Blob([value]) : value }));
}

function codes(issues: ReadonlyArray<{ code: string }>): string[] {
  return issues.map((issue) => issue.code);
}

/** A minimal ZIP writer: stored or raw-deflated entries, CRC left at zero (the preview does not check it). */
function zip(entries: Array<{ name: string; data: string; deflate?: boolean }>): Uint8Array<ArrayBuffer> {
  const encoder = new TextEncoder();
  const locals: Uint8Array[] = [];
  const centrals: Uint8Array[] = [];
  let offset = 0;
  for (const entry of entries) {
    const name = encoder.encode(entry.name);
    const raw = encoder.encode(entry.data);
    const body = entry.deflate ? new Uint8Array(deflateRawSync(raw)) : raw;
    const local = new Uint8Array(30 + name.length + body.length);
    const localView = new DataView(local.buffer);
    localView.setUint32(0, 0x04034b50, true);
    localView.setUint16(4, 20, true);
    localView.setUint16(6, 0x800, true);
    localView.setUint16(8, entry.deflate ? 8 : 0, true);
    localView.setUint32(18, body.length, true);
    localView.setUint32(22, raw.length, true);
    localView.setUint16(26, name.length, true);
    local.set(name, 30);
    local.set(body, 30 + name.length);
    const central = new Uint8Array(46 + name.length);
    const centralView = new DataView(central.buffer);
    centralView.setUint32(0, 0x02014b50, true);
    centralView.setUint16(4, 20, true);
    centralView.setUint16(6, 20, true);
    centralView.setUint16(8, 0x800, true);
    centralView.setUint16(10, entry.deflate ? 8 : 0, true);
    centralView.setUint32(20, body.length, true);
    centralView.setUint32(24, raw.length, true);
    centralView.setUint16(28, name.length, true);
    centralView.setUint32(42, offset, true);
    central.set(name, 46);
    locals.push(local);
    centrals.push(central);
    offset += local.length;
  }
  const directorySize = centrals.reduce((sum, part) => sum + part.length, 0);
  const end = new Uint8Array(22);
  const endView = new DataView(end.buffer);
  endView.setUint32(0, 0x06054b50, true);
  endView.setUint16(8, entries.length, true);
  endView.setUint16(10, entries.length, true);
  endView.setUint32(12, directorySize, true);
  endView.setUint32(16, offset, true);
  const result = new Uint8Array(offset + directorySize + 22);
  let position = 0;
  for (const part of [...locals, ...centrals, end]) {
    result.set(part, position);
    position += part.length;
  }
  return result;
}

describe("Skill folder preflight", () => {
  it("summarizes a valid folder and reads its frontmatter", async () => {
    const preview = await previewSkillFolder(folder({
      "report/SKILL.md": manifest,
      "report/scripts/run.py": "print('report')\n",
    }));
    expect(preview).toMatchObject({
      kind: "directory",
      topLevel: "report",
      fileCount: 2,
      name: "report",
      description: "Create the report.",
      errors: [],
      warnings: [],
    });
    expect(preview.totalBytes).toBe(manifest.length + "print('report')\n".length);
  });

  it("matches SKILL.md case-insensitively, like Core", async () => {
    const preview = await previewSkillFolder(folder({ "report/skill.md": manifest }));
    expect(preview.errors).toEqual([]);
    expect(preview.name).toBe("report");
  });

  it("requires one top-level folder with SKILL.md", async () => {
    const two = await previewSkillFolder(folder({ "report/SKILL.md": manifest, "other/SKILL.md": manifest }));
    expect(two.errors).toContainEqual({ code: "multiple-top-level", names: ["other", "report"] });
    expect(two.topLevel).toBeNull();

    const missing = await previewSkillFolder(folder({ "report/README.md": "# Report" }));
    expect(missing.errors).toEqual([{ code: "missing-manifest", folder: "report" }]);
    expect(missing.name).toBeNull();

    const loose = await previewSkillFolder(folder({ "SKILL.md": manifest }));
    expect(codes(loose.errors)).toEqual(["outside-folder"]);

    expect(codes((await previewSkillFolder([])).errors)).toEqual(["no-files"]);
  });

  it("limits file count to 500 and the uncompressed total to 20 MiB", async () => {
    const many = folder(Object.fromEntries([
      ["report/SKILL.md", manifest],
      ...Array.from({ length: 500 }, (_, index) => [`report/data/${index}.txt`, "x"]),
    ]));
    expect((await previewSkillFolder(many)).errors).toContainEqual({ code: "too-many-files", count: 501, limit: 500 });

    const large = await previewSkillFolder(folder({
      "report/SKILL.md": manifest,
      "report/data.bin": new Blob([new Uint8Array(SKILL_EXPANDED_MAX_BYTES)]),
    }));
    expect(codes(large.errors)).toEqual(["too-large"]);
  });

  it("rejects backslashes, control characters, traversal and duplicate paths", async () => {
    const preview = await previewSkillFolder(folder({
      "report/SKILL.md": manifest,
      "report\\evil.txt": "x",
      "report/tab\tname.txt": "x",
      "report/../escape.txt": "x",
    }));
    const invalid = preview.errors.find((issue) => issue.code === "invalid-path");
    expect(invalid).toEqual({ code: "invalid-path", paths: ["report\\evil.txt", "report/tab\tname.txt", "report/../escape.txt"], more: 0 });

    const duplicate = await previewSkillFolder([
      { path: "report/SKILL.md", file: new Blob([manifest]) },
      { path: "report/SKILL.md", file: new Blob([manifest]) },
    ]);
    expect(duplicate.errors).toContainEqual({ code: "duplicate-path", paths: ["report/SKILL.md"], more: 0 });
  });

  it("keeps hidden files in the upload and lists them as a warning", async () => {
    const preview = await previewSkillFolder(folder({
      "report/SKILL.md": manifest,
      "report/.DS_Store": "x",
      "report/.config/settings.json": "{}",
    }));
    expect(preview.fileCount).toBe(3);
    expect(preview.errors).toEqual([]);
    expect(preview.warnings).toEqual([{ code: "hidden-files", paths: ["report/.DS_Store", "report/.config/settings.json"], more: 0 }]);
  });

  it("warns without blocking when the frontmatter cannot be read", async () => {
    const preview = await previewSkillFolder(folder({ "report/SKILL.md": "# Report\nNo frontmatter." }));
    expect(preview.errors).toEqual([]);
    expect(codes(preview.warnings)).toEqual(["frontmatter-missing"]);
  });
});

describe("SKILL.md frontmatter preview", () => {
  it("reads plain, quoted and block values", () => {
    expect(parseSkillFrontmatter("---\r\nname: 'report'\r\ndescription: \"Create the \\\"weekly\\\" report.\"\r\n---\r\n")).toEqual({
      name: "report",
      description: "Create the \"weekly\" report.",
      warnings: [],
    });
    expect(parseSkillFrontmatter("---\nname: report\ndescription: >\n  Create the\n  weekly report.\nlicense: MIT\nmetadata:\n  owner: ops\n---\n")).toEqual({
      name: "report",
      description: "Create the weekly report.",
      warnings: [],
    });
    expect(parseSkillFrontmatter("---\nname: report # the id\ndescription: |\n  Line one\n  Line two\n---\n").description).toBe("Line one\nLine two");
  });

  it("warns about fields Core rejects, missing fields and name format", () => {
    expect(parseSkillFrontmatter("---\nname: report\ndescription: x\nhooks: []\nallowed-tools: Bash\n---\n").warnings)
      .toEqual([{ code: "frontmatter-unsupported", keys: ["hooks", "allowed-tools"] }]);
    expect(codes(parseSkillFrontmatter("---\nname: Weekly Report\n---\n").warnings))
      .toEqual(["name-format", "frontmatter-field-missing"]);
    expect(codes(parseSkillFrontmatter("---\nname: report\ndescription: x\n").warnings)).toEqual(["frontmatter-invalid"]);
    expect(codes(parseSkillFrontmatter("---\nname: report\nname: again\ndescription: x\n---\n").warnings)).toEqual(["frontmatter-invalid"]);
  });

  it("returns frontmatter text verbatim for plain-text display", () => {
    const parsed = parseSkillFrontmatter("---\nname: report\ndescription: <img src=x onerror=alert(1)> **bold**\n---\n");
    expect(parsed.description).toBe("<img src=x onerror=alert(1)> **bold**");
  });
});

describe("Skill ZIP preflight", () => {
  it("reads the central directory and a deflated SKILL.md", async () => {
    const bytes = zip([
      { name: "report/", data: "" },
      { name: "report/SKILL.md", data: manifest, deflate: true },
      { name: "report/notes.txt", data: "notes" },
    ]);
    expect(readZipEntries(bytes)?.map((entry) => entry.name)).toEqual(["report/", "report/SKILL.md", "report/notes.txt"]);
    const preview = await previewSkillZip(new Blob([bytes]), "report.zip");
    expect(preview).toMatchObject({
      kind: "zip",
      topLevel: "report",
      fileCount: 2,
      totalBytes: manifest.length + 5,
      archiveBytes: bytes.byteLength,
      name: "report",
      description: "Create the report.",
      errors: [],
      warnings: [],
    });
  });

  it("blocks only a non-ZIP name or an archive over 5 MiB", async () => {
    expect(codes((await previewSkillZip(new Blob(["x"]), "report.tar")).errors)).toEqual(["not-zip"]);
    const large = await previewSkillZip(new Blob([new Uint8Array(SKILL_ZIP_MAX_BYTES + 1)]), "report.zip");
    expect(large.errors).toEqual([{ code: "zip-too-large", bytes: SKILL_ZIP_MAX_BYTES + 1, limit: SKILL_ZIP_MAX_BYTES }]);
  });

  it("reports archive structure problems as warnings because Core re-checks the archive", async () => {
    const preview = await previewSkillZip(new Blob([zip([
      { name: "report/SKILL.md", data: manifest },
      { name: "__MACOSX/report/._SKILL.md", data: "x" },
    ])]), "report.ZIP");
    expect(preview.errors).toEqual([]);
    expect(codes(preview.warnings)).toContain("multiple-top-level");

    const unreadable = await previewSkillZip(new Blob(["not a zip archive at all"]), "report.zip");
    expect(unreadable.errors).toEqual([]);
    expect(codes(unreadable.warnings)).toEqual(["zip-unreadable"]);
  });
});
