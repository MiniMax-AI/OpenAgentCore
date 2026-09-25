import { describe, expect, it, vi } from "vitest";

import { AgentCoreError, type CoreProjectReader, type Skill, type SkillList, type SkillVersion, type SkillVersionList } from "@agents-core-web/agents-client";

import i18n from "../../i18n";
import {
  downloadSkillArchive,
  filterSkills,
  isDefaultVersionConflict,
  readSkillsPage,
  skillArchiveFilename,
  versionDeleteState,
} from "./skill-operations";

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
    } satisfies Pick<CoreProjectReader, "downloadSkill" | "downloadSkillVersion">;
    const save = vi.fn();
    const skill = skillFixture({ default_version: "2", latest_version: "3" });

    await expect(downloadSkillArchive(core, skill, undefined, undefined, save)).resolves.toBe("report-v2.zip");
    await expect(downloadSkillArchive(core, skill, version("1", { name: "report-legacy" }), undefined, save)).resolves.toBe("report-legacy-v1.zip");
    expect(core.downloadSkill).toHaveBeenCalledWith(skillId, { signal: undefined });
    expect(core.downloadSkillVersion).toHaveBeenCalledWith(skillId, "1", { signal: undefined });
    expect(save.mock.calls).toEqual([[data, "report-v2.zip"], [data, "report-legacy-v1.zip"]]);
  });
});

