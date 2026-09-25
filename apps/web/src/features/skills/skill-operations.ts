import { AgentCoreError, type CoreProjectReader, type Skill, type SkillVersion } from "@agents-core-web/agents-client";

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

/**
 * Hands a downloaded Blob to the browser's save flow. The object URL is
 * revoked after a while, or earlier through the returned function.
 */
export function saveBlob(blob: Blob, filename: string): () => void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.rel = "noopener";
  link.style.display = "none";
  document.body.append(link);
  link.click();
  link.remove();
  let revoked = false;
  const revoke = () => {
    if (revoked) return;
    revoked = true;
    URL.revokeObjectURL(url);
  };
  // Give the browser time to start the download before the URL is revoked.
  window.setTimeout(revoke, 30_000);
  return revoke;
}

/** Downloads the default version, or one exact version, through the client. */
export async function downloadSkillArchive(
  core: Pick<CoreProjectReader, "downloadSkill" | "downloadSkillVersion">,
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
  core: Pick<CoreProjectReader, "listSkills">,
  loaded: readonly Skill[],
  after: string | undefined,
  signal?: AbortSignal,
): Promise<{ values: Skill[]; nextAfter: string | null }> {
  const page = await core.listSkills({ after, limit: SKILLS_PAGE_SIZE, order: "desc", signal });
  return appendCollectionPage(loaded, page, after);
}

/** Reads the next version page (highest version first) and appends it to the loaded rows. */
export async function readSkillVersionsPage(
  core: Pick<CoreProjectReader, "listSkillVersions">,
  skillId: string,
  loaded: readonly SkillVersion[],
  after: string | undefined,
  signal?: AbortSignal,
): Promise<{ values: SkillVersion[]; nextAfter: string | null }> {
  const page = await core.listSkillVersions(skillId, { after, limit: SKILLS_PAGE_SIZE, order: "desc", signal });
  return appendCollectionPage(loaded, page, after);
}

