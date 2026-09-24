import { AgentCoreError, type AgentCore, type Skill, type SkillVersion } from "@agents-core-web/agents-client";

import { appendCollectionPage } from "../../lib/collection-pagination";

/**
 * Skill navigation support, from the first Skill list request:
 * - `supported`: the list succeeded;
 * - `unsupported`: 404 or 405, an older Core without Skills (hide the entry);
 * - `storage-unavailable`: 503 `skill_storage_unavailable` (show the entry with an explanation);
 * - `error`: anything else (show the entry with a retry).
 */
export type SkillsSupport = "supported" | "unsupported" | "storage-unavailable" | "error";

export const SKILLS_PAGE_SIZE = 20;

export function isAbortError(error: unknown): boolean {
  return (error instanceof DOMException || error instanceof Error) && error.name === "AbortError";
}

export function classifySkillsError(error: unknown): Exclude<SkillsSupport, "supported"> {
  if (error instanceof AgentCoreError) {
    if (error.status === 404 || error.status === 405) return "unsupported";
    if (error.status === 503 && error.code === "skill_storage_unavailable") return "storage-unavailable";
  }
  return "error";
}

/**
 * Probes whether the connected Core offers Skills, independent of the hosted
 * Session build flag: administrators must see Skills uploaded through the API.
 * Cancellation rejects with the AbortError so callers can ignore it.
 */
export async function probeSkillsSupport(
  client: Pick<AgentCore, "listSkills">,
  signal?: AbortSignal,
): Promise<SkillsSupport> {
  try {
    await client.listSkills({ limit: 1, signal });
    return "supported";
  } catch (error) {
    if (isAbortError(error) || signal?.aborted) throw error;
    return classifySkillsError(error);
  }
}

export function coreErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message) return error.message;
  return String(error);
}

export type SkillUploadFailure =
  | { kind: "invalid"; message: string }
  | { kind: "too-large" }
  | { kind: "storage-unavailable" }
  | { kind: "interrupted"; cancelled: boolean }
  | { kind: "other"; message: string };

/** Maps an upload failure to what the dialog shows; the selection is always kept. */
export function mapSkillUploadError(error: unknown, cancelled = false): SkillUploadFailure {
  if (cancelled || isAbortError(error)) return { kind: "interrupted", cancelled: true };
  if (error instanceof AgentCoreError) {
    if (error.status === 413) return { kind: "too-large" };
    if (error.status === 503 && error.code === "skill_storage_unavailable") return { kind: "storage-unavailable" };
    if (error.status === 400) return { kind: "invalid", message: error.message };
    return { kind: "other", message: error.message };
  }
  // fetch rejects with a TypeError when the request never completes.
  if (error instanceof TypeError) return { kind: "interrupted", cancelled: false };
  return { kind: "other", message: coreErrorMessage(error) };
}

/** Core rejects deleting the default while other versions remain (400 invalid_value on `version`). */
export function isDefaultVersionConflict(error: unknown): boolean {
  return error instanceof AgentCoreError && error.status === 400 && error.code === "invalid_value" && error.param === "version";
}

export interface LoadedVersions {
  count: number;
  /** True once Core reported the end of the version list. */
  complete: boolean;
}

/**
 * The delete control of one version row:
 * - `enabled`: a non-default version;
 * - `blocked-default`: the default while other versions (may) remain; Core would reject it;
 * - `only-version`: the sole version; deleting it deletes the Skill.
 */
export type VersionDeleteState = "enabled" | "blocked-default" | "only-version";

export function versionDeleteState(version: SkillVersion, skill: Skill, versions: LoadedVersions): VersionDeleteState {
  if (version.version !== skill.default_version) return "enabled";
  return versions.complete && versions.count === 1 ? "only-version" : "blocked-default";
}

/** Only the loaded Skills are filtered: Core has no search parameter. */
export function filterSkills(skills: readonly Skill[], query: string): Skill[] {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return [...skills];
  return skills.filter((skill) => skill.name.toLocaleLowerCase().includes(needle) || skill.description.toLocaleLowerCase().includes(needle));
}

/** `<name>-v<version>.zip`, with characters that file systems reject replaced. */
export function skillArchiveFilename(name: string, version: string): string {
  const safe = name.replace(/[\u0000-\u001f\u007f<>:"/\\|?*]+/g, "-").replace(/^[.\s-]+|[.\s]+$/g, "") || "skill";
  return `${safe}-v${version}.zip`;
}

/** Hands a downloaded Blob to the browser's save flow. */
export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.rel = "noopener";
  link.style.display = "none";
  document.body.append(link);
  link.click();
  link.remove();
  // Give the browser time to start the download before the URL is revoked.
  window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
}

/** Downloads the default version, or one exact version, through the client. */
export async function downloadSkillArchive(
  core: Pick<AgentCore, "downloadSkill" | "downloadSkillVersion">,
  skill: Skill,
  version?: SkillVersion,
  signal?: AbortSignal,
  save: (blob: Blob, filename: string) => void = saveBlob,
): Promise<string> {
  const content = version
    ? await core.downloadSkillVersion(skill.id, version.version, { signal })
    : await core.downloadSkill(skill.id, { signal });
  const filename = version
    ? skillArchiveFilename(version.name, version.version)
    : skillArchiveFilename(skill.name, skill.default_version);
  save(content.data, filename);
  return filename;
}

/** Reads the next Skill page (newest first) and appends it to the loaded rows. */
export async function readSkillsPage(
  core: Pick<AgentCore, "listSkills">,
  loaded: readonly Skill[],
  after: string | undefined,
  signal?: AbortSignal,
): Promise<{ values: Skill[]; nextAfter: string | null }> {
  const page = await core.listSkills({ after, limit: SKILLS_PAGE_SIZE, order: "desc", signal });
  return appendCollectionPage(loaded, page, after);
}

/** Reads the next version page (highest version first) and appends it to the loaded rows. */
export async function readSkillVersionsPage(
  core: Pick<AgentCore, "listSkillVersions">,
  skillId: string,
  loaded: readonly SkillVersion[],
  after: string | undefined,
  signal?: AbortSignal,
): Promise<{ values: SkillVersion[]; nextAfter: string | null }> {
  const page = await core.listSkillVersions(skillId, { after, limit: SKILLS_PAGE_SIZE, order: "desc", signal });
  return appendCollectionPage(loaded, page, after);
}

/**
 * Moves the default pointer, then reloads the first version page so the
 * Default and Latest marks match Core. The returned Skill carries the new
 * default version's name and description.
 */
export async function setSkillDefaultVersion(
  core: Pick<AgentCore, "updateSkillDefaultVersion" | "listSkillVersions">,
  skillId: string,
  version: string,
  signal?: AbortSignal,
): Promise<{ skill: Skill; versions: SkillVersion[]; nextAfter: string | null }> {
  const skill = await core.updateSkillDefaultVersion(skillId, version, { signal });
  const page = await readSkillVersionsPage(core, skillId, [], undefined, signal);
  return { skill, versions: page.values, nextAfter: page.nextAfter };
}
