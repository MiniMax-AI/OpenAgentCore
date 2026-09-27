import type {
  PageOrder,
  Skill,
  SkillDeleted,
  SkillList,
  SkillListOptions,
  SkillUploadInput,
  SkillVersion,
  SkillVersionDeleted,
  SkillVersionList,
} from "./types";
import { exactFields, hasOwn, isNonnegativeInteger, isRecord, onlyFields } from "./response-projection";

type Invalid = (message: string) => never;

const skillFields = new Set(["id", "object", "created_at", "name", "description", "default_version", "latest_version"]);
const skillVersionFields = new Set(["id", "object", "created_at", "skill_id", "version", "name", "description"]);
const skillListFields = new Set(["object", "data", "has_more", "first_id", "last_id"]);
const skillDeletedFields = new Set(["id", "object", "deleted"]);
const skillVersionDeletedFields = new Set(["id", "object", "deleted", "version"]);

const skillIdPattern = /^skill_[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const skillVersionIdPattern = /^skillver_[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
// Version numbers are positive decimal strings without leading zeros ("1", "2").
const skillVersionNumberPattern = /^[1-9][0-9]{0,18}$/;
// C0 and C1 controls, including CR/LF and NUL.
const controlCharacterPattern = /[\u0000-\u001f\u007f-\u009f]/;
const loneSurrogatePattern = /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/;

/** Core accepts at most 500 folder files per upload. */
export const maxSkillUploadFiles = 500;
/** A generous bound for one downloaded bundle; Core stores at most 5 MiB per version. */
export const maxSkillContentBytes = 64 * 1024 * 1024;
const defaultSkillPageLimit = 20;
const maxUploadFilenameBytes = 1024;
const maxUploadPathBytes = 4096;

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}

export function isSkillId(value: unknown): value is string {
  return typeof value === "string" && skillIdPattern.test(value);
}

export function isSkillVersionId(value: unknown): value is string {
  return typeof value === "string" && skillVersionIdPattern.test(value);
}

export function isSkillVersionNumber(value: unknown): value is string {
  return typeof value === "string" && skillVersionNumberPattern.test(value);
}

/** Orders canonical positive integer strings without losing precision. */
export function compareSkillVersionNumbers(left: string, right: string): number {
  if (left.length !== right.length) return left.length - right.length;
  return left < right ? -1 : left > right ? 1 : 0;
}

/**
 * A folder upload path: relative, clean, at least one parent folder, no
 * backslash, control character, `.`/`..` or empty segment, and valid UTF-8.
 * The browser sends it verbatim as the multipart filename.
 */
export function isSkillUploadPath(value: unknown): value is string {
  if (typeof value !== "string" || value.length === 0) return false;
  if (utf8Length(value) > maxUploadPathBytes || loneSurrogatePattern.test(value)) return false;
  if (value.includes("\\") || controlCharacterPattern.test(value)) return false;
  const segments = value.split("/");
  if (segments.length < 2) return false;
  return segments.every((segment) => segment !== "" && segment !== "." && segment !== "..");
}

function validZipFilename(value: unknown): value is string {
  if (typeof value !== "string" || value.length === 0 || utf8Length(value) > maxUploadFilenameBytes) return false;
  return !loneSurrogatePattern.test(value) && !controlCharacterPattern.test(value) && !/[\\/]/.test(value);
}

export function requireSkillId(value: unknown): asserts value is string {
  if (!isSkillId(value)) throw new TypeError("Invalid Skill ID.");
}

export function requireSkillVersionNumber(value: unknown): asserts value is string {
  if (!isSkillVersionNumber(value)) throw new TypeError("A Skill version must be a positive integer string.");
}

/** Rejects invalid list parameters before any request is sent. */
export function validateSkillListOptions(
  options: SkillListOptions | undefined,
  validCursor: (value: unknown) => boolean,
  label: string,
): void {
  if (options?.after !== undefined && !validCursor(options.after)) {
    throw new TypeError(`${label} list cursor is not a valid resource ID.`);
  }
  if (options?.limit !== undefined && (!Number.isSafeInteger(options.limit) || options.limit < 0 || options.limit > 100)) {
    throw new TypeError(`${label} list limit must be an integer from 0 through 100.`);
  }
  if (options?.order !== undefined && options.order !== "asc" && options.order !== "desc") {
    throw new TypeError(`${label} list order must be asc or desc.`);
  }
}

/**
 * Builds the multipart body. A ZIP is one `files` part; a folder is one
 * `files[]` part per file named by its relative path. `default` appears at most once.
 */
export function skillUploadBody(input: SkillUploadInput, setDefault?: boolean): FormData {
  if (setDefault !== undefined && typeof setDefault !== "boolean") {
    throw new TypeError("setDefault must be a boolean.");
  }
  const body = new FormData();
  if (isRecord(input) && input.kind === "zip") {
    if (!(input.file instanceof Blob) || !validZipFilename(input.filename)) {
      throw new TypeError("A Skill ZIP upload requires a Blob and a plain filename.");
    }
    body.append("files", input.file, input.filename);
  } else if (isRecord(input) && input.kind === "directory") {
    if (!Array.isArray(input.files) || input.files.length === 0 || input.files.length > maxSkillUploadFiles) {
      throw new TypeError(`A Skill folder upload requires 1 through ${maxSkillUploadFiles} files.`);
    }
    const paths = new Set<string>();
    for (const entry of input.files) {
      if (!isRecord(entry) || !(entry.file instanceof Blob) || !isSkillUploadPath(entry.path)) {
        throw new TypeError("Every Skill folder file requires a Blob and a clean relative path inside a folder.");
      }
      if (paths.has(entry.path)) throw new TypeError(`Duplicate Skill folder path: ${entry.path}`);
      paths.add(entry.path);
    }
    for (const entry of input.files) body.append("files[]", entry.file, entry.path);
  } else {
    throw new TypeError("A Skill upload is either a ZIP or a folder.");
  }
  if (setDefault !== undefined) body.append("default", setDefault ? "true" : "false");
  return body;
}

export function projectSkill(value: unknown, invalid: Invalid, expectedId?: string): Skill {
  const message = "OpenAgentCore returned an invalid Skill.";
  if (!isRecord(value) || !exactFields(value, skillFields)) return invalid(message);
  if (
    !isSkillId(value.id) || (expectedId !== undefined && value.id !== expectedId) ||
    value.object !== "skill" ||
    !isNonnegativeInteger(value.created_at) ||
    typeof value.name !== "string" || value.name.length === 0 ||
    typeof value.description !== "string" ||
    !isSkillVersionNumber(value.default_version) || !isSkillVersionNumber(value.latest_version) ||
    compareSkillVersionNumbers(value.default_version, value.latest_version) > 0
  ) return invalid(message);
  return {
    id: value.id,
    object: "skill",
    created_at: Number(value.created_at),
    name: value.name,
    description: value.description,
    default_version: value.default_version,
    latest_version: value.latest_version,
  };
}

export function projectSkillVersion(
  value: unknown,
  invalid: Invalid,
  expectedSkillId: string,
  expectedVersion?: string,
): SkillVersion {
  const message = "OpenAgentCore returned an invalid Skill version.";
  if (!isRecord(value) || !exactFields(value, skillVersionFields)) return invalid(message);
  if (
    !isSkillVersionId(value.id) ||
    value.object !== "skill.version" ||
    value.skill_id !== expectedSkillId ||
    !isSkillVersionNumber(value.version) || (expectedVersion !== undefined && value.version !== expectedVersion) ||
    !isNonnegativeInteger(value.created_at) ||
    typeof value.name !== "string" || value.name.length === 0 ||
    typeof value.description !== "string"
  ) return invalid(message);
  return {
    id: value.id,
    object: "skill.version",
    skill_id: value.skill_id,
    version: value.version,
    name: value.name,
    description: value.description,
    created_at: Number(value.created_at),
  };
}

function projectSkillPage<T extends { id: string }>(
  value: unknown,
  options: SkillListOptions | undefined,
  project: (entry: unknown) => T,
  inOrder: (previous: T, next: T, order: PageOrder) => boolean,
  invalid: Invalid,
  message: string,
): { object: "list"; data: T[]; has_more: boolean; first_id: string | null; last_id: string | null } {
  if (
    !isRecord(value) || !onlyFields(value, skillListFields) ||
    value.object !== "list" || !Array.isArray(value.data) || typeof value.has_more !== "boolean"
  ) return invalid(message);
  const limit = options?.limit ?? defaultSkillPageLimit;
  const order = options?.order ?? "desc";
  if (value.data.length > limit) return invalid(message);
  const data = value.data.map(project);
  const firstId = data[0]?.id ?? null;
  const lastId = data[data.length - 1]?.id ?? null;
  if (
    new Set(data.map((entry) => entry.id)).size !== data.length ||
    (hasOwn(value, "first_id") && value.first_id !== firstId) ||
    (hasOwn(value, "last_id") && value.last_id !== lastId) ||
    // Limit 0 returns an empty page whose has_more reports whether entries follow.
    (value.has_more && data.length === 0 && limit !== 0) ||
    data.some((entry, index) => index > 0 && !inOrder(data[index - 1]!, entry, order))
  ) return invalid(message);
  return { object: "list", data, has_more: value.has_more, first_id: firstId, last_id: lastId };
}

export function projectSkillList(value: unknown, invalid: Invalid, options?: SkillListOptions): SkillList {
  return projectSkillPage(
    value,
    options,
    (entry) => projectSkill(entry, invalid),
    // Public timestamps are whole seconds, so equal neighbours cannot prove the private tie-break.
    (previous, next, order) => order === "asc" ? previous.created_at <= next.created_at : previous.created_at >= next.created_at,
    invalid,
    "OpenAgentCore returned an invalid Skill list.",
  );
}

export function projectSkillVersionList(
  value: unknown,
  invalid: Invalid,
  skillId: string,
  options?: SkillListOptions,
): SkillVersionList {
  return projectSkillPage(
    value,
    options,
    (entry) => projectSkillVersion(entry, invalid, skillId),
    (previous, next, order) => {
      const comparison = compareSkillVersionNumbers(previous.version, next.version);
      return order === "asc" ? comparison < 0 : comparison > 0;
    },
    invalid,
    "OpenAgentCore returned an invalid Skill version list.",
  );
}

export function projectSkillDeleted(value: unknown, invalid: Invalid, skillId: string): SkillDeleted {
  if (
    !isRecord(value) || !exactFields(value, skillDeletedFields) ||
    value.id !== skillId || value.object !== "skill.deleted" || value.deleted !== true
  ) return invalid("OpenAgentCore returned an invalid Skill deletion receipt.");
  return { id: skillId, object: "skill.deleted", deleted: true };
}

export function projectSkillVersionDeleted(value: unknown, invalid: Invalid, version: string): SkillVersionDeleted {
  if (
    !isRecord(value) || !exactFields(value, skillVersionDeletedFields) ||
    !isSkillVersionId(value.id) || value.object !== "skill.version.deleted" ||
    value.deleted !== true || value.version !== version
  ) return invalid("OpenAgentCore returned an invalid Skill version deletion receipt.");
  return { id: value.id, object: "skill.version.deleted", deleted: true, version };
}
