import { isSkillUploadPath, maxSkillUploadFiles } from "@agents-core-web/agents-client";

/**
 * Browser-side Skill bundle checks. They only make mistakes visible before an
 * upload; Core validates every bundle and remains the authority. Files are never
 * filtered or rewritten here.
 */

export const SKILL_ZIP_MAX_BYTES = 5 * 1024 * 1024;
export const SKILL_EXPANDED_MAX_BYTES = 20 * 1024 * 1024;
/** Folder uploads: Core accepts at most 500 `files[]` parts. */
export const SKILL_FOLDER_MAX_FILES = maxSkillUploadFiles;
/** ZIP uploads: Core accepts at most 1,000 archive entries. */
export const SKILL_ZIP_MAX_ENTRIES = 1000;
const MANIFEST_MAX_BYTES = 256 * 1024;
const LISTED_PATHS = 5;
const SUPPORTED_FRONTMATTER_KEYS = new Set(["name", "description", "license", "compatibility", "metadata"]);
const SKILL_NAME_PATTERN = /^[a-z0-9]+(?:[-_][a-z0-9]+)*$/;

export type SkillBundleIssue =
  | { code: "no-files" }
  | { code: "too-many-files"; count: number; limit: number }
  | { code: "too-large"; bytes: number; limit: number }
  | { code: "zip-too-large"; bytes: number; limit: number }
  | { code: "not-zip" }
  | { code: "multiple-top-level"; names: string[] }
  | { code: "outside-folder"; paths: string[]; more: number }
  | { code: "missing-manifest"; folder: string }
  | { code: "invalid-path"; paths: string[]; more: number }
  | { code: "duplicate-path"; paths: string[]; more: number }
  | { code: "hidden-files"; paths: string[]; more: number }
  | { code: "manifest-unreadable" }
  | { code: "zip-unreadable" }
  | { code: "frontmatter-missing" }
  | { code: "frontmatter-invalid" }
  | { code: "frontmatter-field-missing"; field: "name" | "description" }
  | { code: "frontmatter-unsupported"; keys: string[] }
  | { code: "name-format" };

export interface SkillBundlePreview {
  kind: "zip" | "directory";
  topLevel: string | null;
  fileCount: number | null;
  /** Uncompressed bytes; for a ZIP, as declared by its central directory. */
  totalBytes: number | null;
  /** The size of the ZIP file itself. */
  archiveBytes: number | null;
  name: string | null;
  description: string | null;
  /** Problems Core is certain to reject; upload stays disabled. */
  errors: SkillBundleIssue[];
  /** Advice only; the upload remains possible. */
  warnings: SkillBundleIssue[];
}

export interface SkillFolderFile {
  path: string;
  file: Blob;
}

interface BundleEntry {
  path: string;
  size: number;
}

interface EntryInspection {
  topLevel: string | null;
  fileCount: number;
  totalBytes: number;
  manifestPath: string | null;
  issues: SkillBundleIssue[];
  hidden: SkillBundleIssue | null;
}

function listed(paths: string[]): { paths: string[]; more: number } {
  return { paths: paths.slice(0, LISTED_PATHS), more: Math.max(0, paths.length - LISTED_PATHS) };
}

/** Relative paths from a folder picker; each browser reports them as `folder/file`. */
export function folderFilesFromSelection(files: ArrayLike<File>): SkillFolderFile[] {
  return Array.from(files, (file) => ({ path: file.webkitRelativePath || file.name, file }));
}

function inspectEntries(entries: readonly BundleEntry[], maxFiles: number): EntryInspection {
  const issues: SkillBundleIssue[] = [];
  const totalBytes = entries.reduce((sum, entry) => sum + entry.size, 0);
  if (entries.length === 0) issues.push({ code: "no-files" });
  if (entries.length > maxFiles) issues.push({ code: "too-many-files", count: entries.length, limit: maxFiles });
  if (totalBytes > SKILL_EXPANDED_MAX_BYTES) issues.push({ code: "too-large", bytes: totalBytes, limit: SKILL_EXPANDED_MAX_BYTES });

  const invalid: string[] = [];
  const outside: string[] = [];
  const duplicate: string[] = [];
  const hidden: string[] = [];
  const seen = new Set<string>();
  const topLevels = new Set<string>();
  for (const entry of entries) {
    if (!isSkillUploadPath(entry.path)) {
      if (entry.path && !/[/\\]/.test(entry.path)) outside.push(entry.path);
      else invalid.push(entry.path);
      continue;
    }
    if (seen.has(entry.path)) duplicate.push(entry.path);
    seen.add(entry.path);
    topLevels.add(entry.path.slice(0, entry.path.indexOf("/")));
    if (entry.path.split("/").some((segment) => segment.startsWith("."))) hidden.push(entry.path);
  }
  if (outside.length) issues.push({ code: "outside-folder", ...listed(outside) });
  if (invalid.length) issues.push({ code: "invalid-path", ...listed(invalid) });
  if (duplicate.length) issues.push({ code: "duplicate-path", ...listed(duplicate) });
  if (topLevels.size > 1) issues.push({ code: "multiple-top-level", names: [...topLevels].sort().slice(0, LISTED_PATHS) });

  const topLevel = topLevels.size === 1 ? [...topLevels][0]! : null;
  // Core matches the manifest name case-insensitively.
  const manifestPath = topLevel === null
    ? null
    : [...seen].find((path) => path.toLowerCase() === `${topLevel.toLowerCase()}/skill.md`) ?? null;
  if (topLevel !== null && manifestPath === null) issues.push({ code: "missing-manifest", folder: topLevel });
  return {
    topLevel,
    fileCount: entries.length,
    totalBytes,
    manifestPath,
    issues,
    hidden: hidden.length ? { code: "hidden-files", ...listed(hidden) } : null,
  };
}

function frontmatterValue(raw: string, continuation: string[]): string | null {
  const value = raw.trim();
  if (value.startsWith("|") || value.startsWith(">")) {
    const lines = continuation.map((line) => line.trim());
    return value.startsWith("|") ? lines.join("\n") : lines.join(" ").replace(/\s+/g, " ").trim();
  }
  if (value.startsWith('"')) {
    if (continuation.length) return null;
    try {
      const parsed: unknown = JSON.parse(value);
      return typeof parsed === "string" ? parsed : null;
    } catch {
      return null;
    }
  }
  if (value.startsWith("'")) {
    if (continuation.length || value.length < 2 || !value.endsWith("'")) return null;
    return value.slice(1, -1).replaceAll("''", "'");
  }
  const plain = [value.replace(/\s+#.*$/, ""), ...continuation.map((line) => line.trim())].filter(Boolean).join(" ");
  return plain;
}

/**
 * Reads `name` and `description` from SKILL.md frontmatter for display. This is
 * a small YAML subset; anything it cannot read becomes a warning, never a block.
 */
export function parseSkillFrontmatter(source: string): {
  name: string | null;
  description: string | null;
  warnings: SkillBundleIssue[];
} {
  const text = source.replace(/^\uFEFF/, "").replaceAll("\r\n", "\n");
  if (!text.startsWith("---\n")) return { name: null, description: null, warnings: [{ code: "frontmatter-missing" }] };
  const end = text.indexOf("\n---", 4);
  const after = end < 0 ? undefined : text[end + 4];
  if (end < 0 || (after !== undefined && after !== "\n")) {
    return { name: null, description: null, warnings: [{ code: "frontmatter-invalid" }] };
  }
  const lines = text.slice(4, end).split("\n");
  const fields = new Map<string, { raw: string; continuation: string[] }>();
  let current: { raw: string; continuation: string[] } | null = null;
  let invalid = false;
  for (const line of lines) {
    if (!line.trim() || line.trimStart().startsWith("#")) {
      if (current && !line.trim()) current.continuation.push("");
      continue;
    }
    if (/^\s/.test(line)) {
      if (!current) invalid = true;
      else current.continuation.push(line);
      continue;
    }
    const match = /^([^\s:#][^:]*):(?:\s+(.*)|)$/.exec(line);
    if (!match || fields.has(match[1]!)) {
      invalid = true;
      current = null;
      continue;
    }
    current = { raw: match[2] ?? "", continuation: [] };
    fields.set(match[1]!, current);
  }
  const warnings: SkillBundleIssue[] = [];
  if (invalid) warnings.push({ code: "frontmatter-invalid" });
  const read = (key: "name" | "description"): string | null => {
    const field = fields.get(key);
    if (!field) return null;
    const continuation = field.continuation.filter((line, index, all) => line || all.slice(index).some(Boolean));
    const value = frontmatterValue(field.raw, continuation);
    if (value === null && !invalid) {
      invalid = true;
      warnings.push({ code: "frontmatter-invalid" });
    }
    return value;
  };
  const name = read("name");
  const description = read("description");
  const unsupported = [...fields.keys()].filter((key) => !SUPPORTED_FRONTMATTER_KEYS.has(key));
  if (unsupported.length) warnings.push({ code: "frontmatter-unsupported", keys: unsupported });
  if (!name) warnings.push({ code: "frontmatter-field-missing", field: "name" });
  else if (!SKILL_NAME_PATTERN.test(name) || name.length > 64) warnings.push({ code: "name-format" });
  if (!description) warnings.push({ code: "frontmatter-field-missing", field: "description" });
  return { name: name || null, description: description || null, warnings };
}

async function readManifest(read: () => Promise<Uint8Array | null>) {
  const bytes = await read().catch(() => null);
  if (bytes === null) return { name: null, description: null, warnings: [{ code: "manifest-unreadable" } as SkillBundleIssue] };
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return { name: null, description: null, warnings: [{ code: "frontmatter-invalid" } as SkillBundleIssue] };
  }
  return parseSkillFrontmatter(text);
}

/** Checks a picked folder and reads its SKILL.md for the preview. */
export async function previewSkillFolder(files: readonly SkillFolderFile[]): Promise<SkillBundlePreview> {
  const inspection = inspectEntries(files.map((entry) => ({ path: entry.path, size: entry.file.size })), SKILL_FOLDER_MAX_FILES);
  const manifest = inspection.manifestPath === null
    ? null
    : files.find((entry) => entry.path === inspection.manifestPath)?.file ?? null;
  const parsed = manifest === null
    ? { name: null, description: null, warnings: [] }
    : await readManifest(async () => manifest.size > MANIFEST_MAX_BYTES ? null : new Uint8Array(await manifest.arrayBuffer()));
  return {
    kind: "directory",
    topLevel: inspection.topLevel,
    fileCount: inspection.fileCount,
    totalBytes: inspection.totalBytes,
    archiveBytes: null,
    name: parsed.name,
    description: parsed.description,
    errors: inspection.issues,
    warnings: [...(inspection.hidden ? [inspection.hidden] : []), ...parsed.warnings],
  };
}

export interface ZipEntry {
  name: string;
  size: number;
  compressedSize: number;
  method: number;
  flags: number;
  offset: number;
}

const EOCD_SIGNATURE = 0x06054b50;
const CENTRAL_SIGNATURE = 0x02014b50;
const LOCAL_SIGNATURE = 0x04034b50;

/**
 * Lists a ZIP's central directory. Returns null for anything it does not
 * understand, including ZIP64; the preview then says so and Core decides.
 */
export function readZipEntries(bytes: Uint8Array): ZipEntry[] | null {
  if (bytes.byteLength < 22) return null;
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  let eocd = -1;
  for (let index = bytes.byteLength - 22; index >= Math.max(0, bytes.byteLength - 22 - 0xffff); index -= 1) {
    if (view.getUint32(index, true) === EOCD_SIGNATURE) {
      eocd = index;
      break;
    }
  }
  if (eocd < 0) return null;
  const count = view.getUint16(eocd + 10, true);
  const directorySize = view.getUint32(eocd + 12, true);
  const directoryOffset = view.getUint32(eocd + 16, true);
  if (count === 0xffff || directoryOffset === 0xffffffff || directoryOffset + directorySize > eocd) return null;
  const decoder = new TextDecoder("utf-8");
  const entries: ZipEntry[] = [];
  let position = directoryOffset;
  for (let index = 0; index < count; index += 1) {
    if (position + 46 > eocd || view.getUint32(position, true) !== CENTRAL_SIGNATURE) return null;
    const nameLength = view.getUint16(position + 28, true);
    const extraLength = view.getUint16(position + 30, true);
    const commentLength = view.getUint16(position + 32, true);
    const entry: ZipEntry = {
      flags: view.getUint16(position + 8, true),
      method: view.getUint16(position + 10, true),
      compressedSize: view.getUint32(position + 20, true),
      size: view.getUint32(position + 24, true),
      offset: view.getUint32(position + 42, true),
      name: decoder.decode(bytes.subarray(position + 46, position + 46 + nameLength)),
    };
    if (entry.compressedSize === 0xffffffff || entry.size === 0xffffffff || entry.offset === 0xffffffff) return null;
    entries.push(entry);
    position += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}

/** Reads one stored or deflated entry, or null when that is not possible here. */
export async function readZipEntry(bytes: Uint8Array, entry: ZipEntry, maxBytes: number): Promise<Uint8Array | null> {
  if (entry.flags & 1 || entry.size > maxBytes) return null;
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  if (entry.offset + 30 > bytes.byteLength || view.getUint32(entry.offset, true) !== LOCAL_SIGNATURE) return null;
  const start = entry.offset + 30 + view.getUint16(entry.offset + 26, true) + view.getUint16(entry.offset + 28, true);
  const data = bytes.slice(start, start + entry.compressedSize);
  if (data.byteLength !== entry.compressedSize) return null;
  if (entry.method === 0) return data.byteLength === entry.size ? data : null;
  if (entry.method !== 8 || typeof DecompressionStream === "undefined") return null;
  const stream = new Blob([data]).stream().pipeThrough(new DecompressionStream("deflate-raw"));
  const inflated = new Uint8Array(await new Response(stream).arrayBuffer());
  return inflated.byteLength === entry.size ? inflated : null;
}

/**
 * Checks a picked ZIP. Only the extension and the compressed size block the
 * upload; what the central directory shows is advice because Core re-reads it.
 */
export async function previewSkillZip(file: Blob, filename: string): Promise<SkillBundlePreview> {
  const errors: SkillBundleIssue[] = [];
  if (!filename.toLowerCase().endsWith(".zip")) errors.push({ code: "not-zip" });
  if (file.size > SKILL_ZIP_MAX_BYTES) errors.push({ code: "zip-too-large", bytes: file.size, limit: SKILL_ZIP_MAX_BYTES });
  const preview: SkillBundlePreview = {
    kind: "zip",
    topLevel: null,
    fileCount: null,
    totalBytes: null,
    archiveBytes: file.size,
    name: null,
    description: null,
    errors,
    warnings: [],
  };
  if (errors.length) return preview;
  const bytes = new Uint8Array(await file.arrayBuffer());
  const entries = readZipEntries(bytes);
  if (entries === null) return { ...preview, warnings: [{ code: "zip-unreadable" }] };
  const files = entries.filter((entry) => !entry.name.endsWith("/"));
  const inspection = inspectEntries(files.map((entry) => ({ path: entry.name, size: entry.size })), SKILL_ZIP_MAX_ENTRIES);
  const manifest = files.find((entry) => entry.name === inspection.manifestPath);
  const parsed = manifest
    ? await readManifest(() => readZipEntry(bytes, manifest, MANIFEST_MAX_BYTES))
    : { name: null, description: null, warnings: [] };
  return {
    ...preview,
    topLevel: inspection.topLevel,
    fileCount: inspection.fileCount,
    totalBytes: inspection.totalBytes,
    name: parsed.name,
    description: parsed.description,
    warnings: [...inspection.issues, ...(inspection.hidden ? [inspection.hidden] : []), ...parsed.warnings],
  };
}
