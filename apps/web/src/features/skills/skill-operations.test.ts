import { describe, expect, it, vi } from "vitest";

import { AgentCoreError, type AgentCore, type Skill, type SkillList, type SkillVersion, type SkillVersionList } from "@agents-core-web/agents-client";

import i18n from "../../i18n";
import {
  downloadSkillArchive,
  filterSkills,
  isDefaultVersionConflict,
  mapSkillUploadError,
  probeSkillsSupport,
  readSkillsPage,
  setSkillDefaultVersion,
  skillArchiveFilename,
  versionDeleteState,
} from "./skill-operations";
import { skillUploadFailureText } from "./SkillUploadDialog";

const skillId = "skill_3f1c2a9e-7b4d-4e8a-9c21-5d6e7f8a9b0c";

function skillFixture(overrides: Partial<Skill> = {}): Skill {
  return {
    id: skillId,
    object: "skill",
    created_at: 1_790_208_000,
    name: "report",
    description: "Create the report.",
    default_version: "1",
    latest_version: "2",
    ...overrides,
  };
}

function version(number: string, overrides: Partial<SkillVersion> = {}): SkillVersion {
  return {
    id: `skillver_${number}`,
    object: "skill.version",
    skill_id: skillId,
    version: number,
    name: "report",
    description: `Version ${number}.`,
    created_at: 1_790_208_000 + Number(number),
    ...overrides,
  };
}

function coreError(status: number, code: string | null = null, param: string | null = null, message = "Core said no."): AgentCoreError {
  return new AgentCoreError(message, status, code, param);
}

describe("Skill navigation support", () => {
  it("classifies the first list request", async () => {
    const probe = (result: () => Promise<SkillList>) => probeSkillsSupport({ listSkills: vi.fn(result) });
    await expect(probe(async () => ({ object: "list", data: [], has_more: false, first_id: null, last_id: null }))).resolves.toBe("supported");
    await expect(probe(async () => { throw coreError(404); })).resolves.toBe("unsupported");
    await expect(probe(async () => { throw coreError(405); })).resolves.toBe("unsupported");
    await expect(probe(async () => { throw coreError(503, "skill_storage_unavailable"); })).resolves.toBe("storage-unavailable");
    await expect(probe(async () => { throw coreError(503, "credential_storage_unavailable"); })).resolves.toBe("error");
    await expect(probe(async () => { throw coreError(500); })).resolves.toBe("error");
    await expect(probe(async () => { throw new TypeError("Failed to fetch"); })).resolves.toBe("error");
  });

  it("asks for one entry and propagates cancellation", async () => {
    const listSkills = vi.fn(async () => { throw new DOMException("Aborted", "AbortError"); });
    const controller = new AbortController();
    await expect(probeSkillsSupport({ listSkills }, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
    expect(listSkills).toHaveBeenCalledWith({ limit: 1, signal: controller.signal });
  });
});

describe("Skill upload error mapping", () => {
  const t = i18n.getFixedT("en", "skills");

  it("maps each documented Core answer to its message", () => {
    const cases: Array<[unknown, string]> = [
      [coreError(400, "invalid_request", null, "Invalid resource identifier or request limits."), "Core rejected the Skill: Invalid resource identifier or request limits."],
      [coreError(413, "request_too_large"), "The files exceed the size limit."],
      [coreError(503, "skill_storage_unavailable"), "Core has no Skill storage configured."],
      [new TypeError("Failed to fetch"), "The upload did not finish. Your selection is kept; Core may have stored it anyway, so check the list before trying again."],
      [new DOMException("Aborted", "AbortError"), "Upload cancelled. Your selection is kept; Core may have stored it anyway, so check the list before trying again."],
      [coreError(404, null, null, "Resource not found."), "The upload failed: Resource not found."],
    ];
    for (const [error, message] of cases) expect(skillUploadFailureText(t, mapSkillUploadError(error))).toBe(message);
  });

  it("treats any failure after the user cancelled as a cancellation", () => {
    expect(mapSkillUploadError(new TypeError("network"), true)).toEqual({ kind: "interrupted", cancelled: true });
    expect(mapSkillUploadError(coreError(400))).toEqual({ kind: "invalid", message: "Core said no." });
  });

  it("recognizes the default-version deletion rule", () => {
    expect(isDefaultVersionConflict(coreError(400, "invalid_value", "version"))).toBe(true);
    expect(isDefaultVersionConflict(coreError(400, "invalid_value", "after"))).toBe(false);
    expect(isDefaultVersionConflict(coreError(404))).toBe(false);
  });
});

describe("Skill version deletion states", () => {
  const skill = skillFixture({ default_version: "2", latest_version: "3" });

  it("allows non-default versions", () => {
    expect(versionDeleteState(version("1"), skill, { count: 3, complete: true })).toBe("enabled");
    expect(versionDeleteState(version("3"), skill, { count: 1, complete: false })).toBe("enabled");
  });

  it("blocks the default while other versions remain or may remain", () => {
    expect(versionDeleteState(version("2"), skill, { count: 3, complete: true })).toBe("blocked-default");
    expect(versionDeleteState(version("2"), skill, { count: 1, complete: false })).toBe("blocked-default");
  });

  it("marks the only version, whose deletion deletes the Skill", () => {
    expect(versionDeleteState(version("2"), skill, { count: 1, complete: true })).toBe("only-version");
  });
});

describe("Skill list helpers", () => {
  it("filters loaded Skills by name or description", () => {
    const skills = [skillFixture(), skillFixture({ id: "skill_b", name: "triage", description: "Sort incoming issues." })];
    expect(filterSkills(skills, "  REPORT ").map((skill) => skill.name)).toEqual(["report"]);
    expect(filterSkills(skills, "issues").map((skill) => skill.name)).toEqual(["triage"]);
    expect(filterSkills(skills, "")).toHaveLength(2);
  });

  it("reads pages newest first with the Skill cursor", async () => {
    const listSkills = vi.fn(async (): Promise<SkillList> => ({
      object: "list", data: [skillFixture({ id: "skill_c" })], has_more: true, first_id: "skill_c", last_id: "skill_c",
    }));
    const page = await readSkillsPage({ listSkills }, [skillFixture({ id: "skill_d" })], "skill_d");
    expect(listSkills).toHaveBeenCalledWith({ after: "skill_d", limit: 20, order: "desc", signal: undefined });
    expect(page.values.map((skill) => skill.id)).toEqual(["skill_d", "skill_c"]);
    expect(page.nextAfter).toBe("skill_c");
  });

  it("names archives after the Skill and version", () => {
    expect(skillArchiveFilename("report", "2")).toBe("report-v2.zip");
    expect(skillArchiveFilename("a/b:c", "1")).toBe("a-b-c-v1.zip");
    expect(skillArchiveFilename("..", "3")).toBe("skill-v3.zip");
  });

  it("downloads through the client with the documented file names", async () => {
    const data = new Blob(["zip"]);
    const content = { data, bytes: 3, content_type: "application/octet-stream" as const, content_disposition: "attachment" };
    const core = {
      downloadSkill: vi.fn(async () => content),
      downloadSkillVersion: vi.fn(async () => content),
    } satisfies Pick<AgentCore, "downloadSkill" | "downloadSkillVersion">;
    const save = vi.fn();
    const skill = skillFixture({ default_version: "2", latest_version: "3" });

    await expect(downloadSkillArchive(core, skill, undefined, undefined, save)).resolves.toBe("report-v2.zip");
    await expect(downloadSkillArchive(core, skill, version("1", { name: "report-legacy" }), undefined, save)).resolves.toBe("report-legacy-v1.zip");
    expect(core.downloadSkill).toHaveBeenCalledWith(skillId, { signal: undefined });
    expect(core.downloadSkillVersion).toHaveBeenCalledWith(skillId, "1", { signal: undefined });
    expect(save.mock.calls).toEqual([[data, "report-v2.zip"], [data, "report-legacy-v1.zip"]]);
  });
});

describe("Making a version the default", () => {
  it("moves the pointer, then reloads the versions so the new name and marks show", async () => {
    const updated = skillFixture({ name: "report-v2", description: "Create the quarterly report.", default_version: "2", latest_version: "2" });
    const calls: string[] = [];
    const core = {
      updateSkillDefaultVersion: vi.fn(async () => { calls.push("update"); return updated; }),
      listSkillVersions: vi.fn(async (): Promise<SkillVersionList> => {
        calls.push("list");
        return { object: "list", data: [version("2", { name: "report-v2" }), version("1")], has_more: false, first_id: "skillver_2", last_id: "skillver_1" };
      }),
    };
    const result = await setSkillDefaultVersion(core, skillId, "2");
    expect(calls).toEqual(["update", "list"]);
    expect(core.updateSkillDefaultVersion).toHaveBeenCalledWith(skillId, "2", { signal: undefined });
    expect(result.skill.name).toBe("report-v2");
    expect(result.versions.map((entry) => entry.version)).toEqual(["2", "1"]);
    expect(result.nextAfter).toBeNull();
  });
});
