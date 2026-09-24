import { describe, expect, it } from "vitest";

import { AgentCoreError, OpenAIAgentsClient } from "./client";
import skillsD3f55046 from "./fixtures/parsar-d3f55046/skills.json";
import { isSkillUploadPath } from "./skill-projection";
import type { SkillUploadInput } from "./types";

type FixtureResponse = {
  status: number;
  headers: Record<string, string>;
  body?: unknown;
  body_base64?: string;
};

const fixtures = skillsD3f55046.responses as Record<keyof typeof skillsD3f55046.responses, FixtureResponse>;
const skill = fixtures.skill.body as Record<string, unknown>;
const skillId = skill.id as string;
const version1 = (fixtures.skill_version_list.body as { data: Array<Record<string, unknown>> }).data[1]!;
const version2 = fixtures.skill_version.body as Record<string, unknown>;

interface Call {
  url: string;
  init: RequestInit;
}

function toResponse(fixture: FixtureResponse): Response {
  const body = fixture.body_base64 !== undefined
    ? Uint8Array.from(atob(fixture.body_base64), (character) => character.charCodeAt(0))
    : JSON.stringify(fixture.body);
  return new Response(body, { status: fixture.status, headers: fixture.headers });
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function clientFor(...responses: Array<Response | FixtureResponse>): { client: OpenAIAgentsClient; calls: Call[] } {
  const calls: Call[] = [];
  const queue = [...responses];
  const client = new OpenAIAgentsClient({
    baseUrl: "https://core.example/v1",
    token: "test-token",
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} });
      const next = queue.shift();
      if (!next) throw new Error("Unexpected request.");
      return next instanceof Response ? next : toResponse(next);
    }) as typeof fetch,
  });
  return { client, calls };
}

function headerOf(call: Call | undefined, name: string): string | null {
  return new Headers(call?.init.headers).get(name);
}

async function wireText(body: BodyInit | null | undefined): Promise<string> {
  return new Response(body as FormData).text();
}

describe("Skill fixtures at Core d3f55046", () => {
  it("records the Core revision the raw responses describe", () => {
    expect(skillsD3f55046.revision).toBe("d3f55046717264876157acd567646d665997b040");
  });

  it("projects every success response the console uses without the Agents Beta header", async () => {
    const { client, calls } = clientFor(
      fixtures.skill,
      fixtures.skill_list,
      fixtures.skill_list_empty,
      fixtures.skill_version_list,
      fixtures.skill_default_updated,
      fixtures.skill_deleted,
      fixtures.skill_version_deleted,
    );

    await expect(client.retrieveSkill(skillId)).resolves.toEqual(skill);
    const page = await client.listSkills({ limit: 2 });
    expect(page).toMatchObject({ object: "list", has_more: true, first_id: skillId });
    expect(page.data.map((entry) => entry.name)).toEqual(["report", "triage"]);
    await expect(client.listSkills()).resolves.toEqual({ object: "list", data: [], has_more: false, first_id: null, last_id: null });
    const versions = await client.listSkillVersions(skillId);
    expect(versions.data.map((entry) => entry.version)).toEqual(["2", "1"]);
    const updated = await client.updateSkillDefaultVersion(skillId, "2");
    expect(updated).toMatchObject({ default_version: "2", description: "Create the quarterly report." });
    await expect(client.deleteSkill(skillId)).resolves.toEqual({ id: skillId, object: "skill.deleted", deleted: true });
    await expect(client.deleteSkillVersion(skillId, "1")).resolves.toEqual({
      id: version1.id, object: "skill.version.deleted", deleted: true, version: "1",
    });

    expect(calls.map((call) => `${call.init.method ?? "GET"} ${call.url}`)).toEqual([
      `GET https://core.example/v1/skills/${skillId}`,
      "GET https://core.example/v1/skills?limit=2",
      "GET https://core.example/v1/skills",
      `GET https://core.example/v1/skills/${skillId}/versions`,
      `POST https://core.example/v1/skills/${skillId}`,
      `DELETE https://core.example/v1/skills/${skillId}`,
      `DELETE https://core.example/v1/skills/${skillId}/versions/1`,
    ]);
    for (const call of calls) {
      expect(headerOf(call, "OpenAI-Beta")).toBeNull();
      expect(headerOf(call, "Authorization")).toBe("Bearer test-token");
    }
    expect(JSON.parse(String(calls[4]?.init.body))).toEqual({ default_version: "2" });
    expect(headerOf(calls[4], "Content-Type")).toBe("application/json");
  });

  it("surfaces Core error envelopes as typed errors", async () => {
    const { client } = clientFor(
      fixtures.error_not_found,
      fixtures.error_storage_unavailable,
      fixtures.error_request_too_large,
      fixtures.error_invalid_upload,
      fixtures.error_default_version_delete,
    );
    const upload: SkillUploadInput = { kind: "zip", file: new Blob(["zip"]), filename: "report.zip" };

    await expect(client.retrieveSkill("skill_missing")).rejects.toMatchObject({ status: 404, code: null });
    await expect(client.listSkills()).rejects.toMatchObject({ status: 503, code: "skill_storage_unavailable" });
    await expect(client.uploadSkill(upload)).rejects.toMatchObject({ status: 413, code: "request_too_large" });
    await expect(client.uploadSkill(upload)).rejects.toMatchObject({ status: 400, message: "Invalid resource identifier or request limits." });
    await expect(client.deleteSkillVersion(skillId, "2")).rejects.toMatchObject({ status: 400, code: "invalid_value", param: "version" });
  });
});

describe("Skill response validation", () => {
  const invalidSkills: Array<[string, Record<string, unknown>]> = [
    ["an unknown object", { ...skill, object: "skill.version" }],
    ["a missing field", Object.fromEntries(Object.entries(skill).filter(([key]) => key !== "latest_version"))],
    ["an extra field", { ...skill, created_by: "someone" }],
    ["a wrong ID prefix", { ...skill, id: "skillver_3f1c2a9e" }],
    ["a non-integer version", { ...skill, default_version: "v1" }],
    ["a zero version", { ...skill, default_version: "0" }],
    ["a leading-zero version", { ...skill, latest_version: "02" }],
    ["a numeric version", { ...skill, default_version: 1 }],
    ["a default newer than latest", { ...skill, default_version: "3" }],
    ["an empty name", { ...skill, name: "" }],
    ["a fractional timestamp", { ...skill, created_at: 1.5 }],
  ];

  for (const [label, body] of invalidSkills) {
    it(`rejects a Skill with ${label}`, async () => {
      const { client } = clientFor(json(body));
      await expect(client.retrieveSkill(skillId)).rejects.toMatchObject({ status: 502, code: "invalid_skill_resource" });
    });
  }

  it("rejects a Skill for another ID", async () => {
    const { client } = clientFor(json({ ...skill, id: "skill_other" }));
    await expect(client.retrieveSkill(skillId)).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("rejects versions of another Skill, wrong prefixes and wrong version numbers", async () => {
    const { client } = clientFor(
      json({ ...version2, skill_id: "skill_other" }),
      json({ ...version2, id: "skill_1b2c3d4e" }),
      json({ ...version2, object: "skill" }),
      json(version2),
    );
    await expect(client.uploadSkillVersion(skillId, { kind: "zip", file: new Blob(["z"]), filename: "a.zip" })).rejects.toMatchObject({ status: 502 });
    await expect(client.retrieveSkillVersion(skillId, "2")).rejects.toMatchObject({ status: 502 });
    await expect(client.retrieveSkillVersion(skillId, "2")).rejects.toMatchObject({ status: 502 });
    await expect(client.retrieveSkillVersion(skillId, "1")).rejects.toMatchObject({ status: 502 });
  });

  it("rejects inconsistent list pages", async () => {
    const list = fixtures.skill_version_list.body as Record<string, unknown>;
    const { client } = clientFor(
      json({ ...list, data: [version1, version2] }),
      json({ ...list, first_id: version1.id }),
      json({ ...list, data: [version2, version2] }),
      json({ ...list, data: [], first_id: null, last_id: null, has_more: true }),
      json({ ...list, object: "page" }),
      json({ ...list, next: null }),
      json(list),
      json({ object: "list", data: [], has_more: true }),
    );
    for (let index = 0; index < 6; index += 1) {
      await expect(client.listSkillVersions(skillId)).rejects.toMatchObject({ code: "invalid_skill_resource" });
    }
    await expect(client.listSkillVersions(skillId, { limit: 1 })).rejects.toMatchObject({ code: "invalid_skill_resource" });
    // Limit 0 reports only whether entries follow; first_id/last_id are optional.
    await expect(client.listSkillVersions(skillId, { limit: 0 })).resolves.toMatchObject({ data: [], has_more: true, first_id: null });
  });

  it("requires a new Skill to start with one default version and an update to move the default", async () => {
    const { client } = clientFor(json(skill), json(skill));
    await expect(client.uploadSkill({ kind: "zip", file: new Blob(["z"]), filename: "a.zip" })).rejects.toMatchObject({ status: 502 });
    await expect(client.updateSkillDefaultVersion(skillId, "2")).rejects.toMatchObject({ status: 502 });
  });

  it("rejects deletion receipts that do not match the request", async () => {
    const { client } = clientFor(
      json({ id: "skill_other", object: "skill.deleted", deleted: true }),
      json({ id: version1.id, object: "skill.version.deleted", deleted: true, version: "2" }),
      json({ id: skillId, object: "skill.version.deleted", deleted: true, version: "1" }),
    );
    await expect(client.deleteSkill(skillId)).rejects.toMatchObject({ status: 502 });
    await expect(client.deleteSkillVersion(skillId, "1")).rejects.toMatchObject({ status: 502 });
    await expect(client.deleteSkillVersion(skillId, "1")).rejects.toMatchObject({ status: 502 });
  });
});

describe("Skill request validation", () => {
  it("rejects invalid query parameters and identifiers before sending", async () => {
    const { client, calls } = clientFor();
    await expect(client.listSkills({ limit: 101 })).rejects.toThrow(TypeError);
    await expect(client.listSkills({ limit: -1 })).rejects.toThrow(TypeError);
    await expect(client.listSkills({ limit: 1.5 })).rejects.toThrow(TypeError);
    await expect(client.listSkills({ order: "" as "asc" })).rejects.toThrow("asc or desc");
    await expect(client.listSkills({ after: "skillver_1" })).rejects.toThrow("cursor");
    await expect(client.listSkillVersions(skillId, { after: "2" })).rejects.toThrow("cursor");
    await expect(client.listSkillVersions(skillId, { after: skillId })).rejects.toThrow("cursor");
    await expect(client.retrieveSkill("../agents")).rejects.toThrow("Skill ID");
    await expect(client.retrieveSkillVersion(skillId, "latest")).rejects.toThrow("positive integer");
    await expect(client.deleteSkillVersion(skillId, "0")).rejects.toThrow("positive integer");
    await expect(client.downloadSkillVersion(skillId, "01")).rejects.toThrow("positive integer");
    await expect(client.updateSkillDefaultVersion(skillId, "")).rejects.toThrow("positive integer");
    expect(calls).toHaveLength(0);
  });

  it("sends validated list parameters", async () => {
    const { client, calls } = clientFor(fixtures.skill_list_empty, fixtures.skill_list_empty);
    await client.listSkills({ after: skillId, limit: 0, order: "asc" });
    await client.listSkillVersions(skillId, { after: version1.id as string, limit: 100, order: "desc" });
    expect(calls[0]?.url).toBe(`https://core.example/v1/skills?after=${skillId}&limit=0&order=asc`);
    expect(calls[1]?.url).toBe(`https://core.example/v1/skills/${skillId}/versions?after=${version1.id as string}&limit=100&order=desc`);
  });

  it("accepts clean folder paths and rejects unsafe ones", () => {
    expect(isSkillUploadPath("report/SKILL.md")).toBe(true);
    expect(isSkillUploadPath("report/scripts/run.py")).toBe(true);
    expect(isSkillUploadPath("report/.DS_Store")).toBe(true);
    expect(isSkillUploadPath("报告/SKILL.md")).toBe(true);
    for (const path of [
      "", "SKILL.md", "/report/SKILL.md", "report/", "report//SKILL.md", "report/../SKILL.md",
      "../report/SKILL.md", "report/./SKILL.md", "report\\SKILL.md", "report/SKILL\u0000.md",
      "report/line\nbreak", "report/\ud800.md",
    ]) {
      expect(isSkillUploadPath(path), JSON.stringify(path)).toBe(false);
    }
  });

  it("rejects unsafe folder uploads before sending", async () => {
    const { client, calls } = clientFor();
    const file = new Blob(["x"]);
    const tooMany = Array.from({ length: 501 }, (_, index) => ({ path: `report/${index}.txt`, file }));
    await expect(client.uploadSkill({ kind: "directory", files: [] })).rejects.toThrow(TypeError);
    await expect(client.uploadSkill({ kind: "directory", files: tooMany })).rejects.toThrow("500");
    await expect(client.uploadSkill({ kind: "directory", files: [{ path: "report/../x", file }] })).rejects.toThrow(TypeError);
    await expect(client.uploadSkill({ kind: "directory", files: [{ path: "report\\SKILL.md", file }] })).rejects.toThrow(TypeError);
    await expect(client.uploadSkill({
      kind: "directory",
      files: [{ path: "report/SKILL.md", file }, { path: "report/SKILL.md", file }],
    })).rejects.toThrow("Duplicate");
    await expect(client.uploadSkill({ kind: "zip", file, filename: "" })).rejects.toThrow(TypeError);
    await expect(client.uploadSkill({ kind: "zip", file, filename: "dir/a.zip" })).rejects.toThrow(TypeError);
    await expect(client.uploadSkill({ kind: "zip", file: "x" as unknown as Blob, filename: "a.zip" })).rejects.toThrow(TypeError);
    expect(calls).toHaveLength(0);
  });
});

describe("Skill multipart uploads", () => {
  it("sends a ZIP as one files part", async () => {
    const { client, calls } = clientFor(fixtures.skill_created);
    const created = await client.uploadSkill({ kind: "zip", file: new Blob(["zip-bytes"]), filename: "report.zip" });
    expect(created.default_version).toBe("1");
    const body = calls[0]?.init.body as FormData;
    expect(calls[0]?.url).toBe("https://core.example/v1/skills");
    expect(calls[0]?.init.method).toBe("POST");
    expect(headerOf(calls[0], "OpenAI-Beta")).toBeNull();
    // The browser supplies the multipart boundary.
    expect(headerOf(calls[0], "Content-Type")).toBeNull();
    expect([...body.keys()]).toEqual(["files"]);
    expect((body.get("files") as File).name).toBe("report.zip");
  });

  it("sends a folder as files[] parts named by relative path, without a default field on new Skills", async () => {
    const { client, calls } = clientFor(fixtures.skill_created);
    await client.uploadSkill({
      kind: "directory",
      files: [
        { path: "report/SKILL.md", file: new Blob(["---\nname: report\n---\n"]) },
        { path: "report/scripts/run.py", file: new Blob(["print(1)"]) },
      ],
    });
    const body = calls[0]?.init.body as FormData;
    expect([...body.keys()]).toEqual(["files[]", "files[]"]);
    expect(body.getAll("files[]").map((entry) => (entry as File).name)).toEqual(["report/SKILL.md", "report/scripts/run.py"]);
    const wire = await wireText(body);
    expect(wire).toContain('name="files[]"; filename="report/SKILL.md"');
    expect(wire).toContain('name="files[]"; filename="report/scripts/run.py"');
    expect(wire).not.toContain('name="default"');
  });

  it("adds the default field exactly once for a new version and omits it when unset", async () => {
    const { client, calls } = clientFor(fixtures.skill_version, fixtures.skill_version, fixtures.skill_version);
    const input: SkillUploadInput = { kind: "directory", files: [{ path: "report/SKILL.md", file: new Blob(["x"]) }] };

    await expect(client.uploadSkillVersion(skillId, input, { setDefault: true })).resolves.toMatchObject({ version: "2", skill_id: skillId });
    await client.uploadSkillVersion(skillId, input, { setDefault: false });
    await client.uploadSkillVersion(skillId, input);

    expect(calls[0]?.url).toBe(`https://core.example/v1/skills/${skillId}/versions`);
    expect((calls[0]?.init.body as FormData).getAll("default")).toEqual(["true"]);
    expect((calls[1]?.init.body as FormData).getAll("default")).toEqual(["false"]);
    expect((calls[2]?.init.body as FormData).getAll("default")).toEqual([]);
    expect((await wireText(calls[0]?.init.body)).match(/name="default"/g)).toHaveLength(1);
  });
});

describe("Skill downloads", () => {
  it("returns the exact ZIP bytes of a version and the default version without the Beta header", async () => {
    const { client, calls } = clientFor(fixtures.skill_content, fixtures.skill_content);
    const content = await client.downloadSkillVersion(skillId, "1");
    const expected = Uint8Array.from(atob(fixtures.skill_content.body_base64!), (character) => character.charCodeAt(0));
    expect(content.bytes).toBe(expected.byteLength);
    expect(content.content_disposition).toBe("attachment; filename=report.zip");
    expect(content.data.type).toBe("application/zip");
    expect(new Uint8Array(await content.data.arrayBuffer())).toEqual(expected);
    await client.downloadSkill(skillId);

    expect(calls.map((call) => call.url)).toEqual([
      `https://core.example/v1/skills/${skillId}/versions/1/content`,
      `https://core.example/v1/skills/${skillId}/content`,
    ]);
    expect(headerOf(calls[0], "OpenAI-Beta")).toBeNull();
    expect(headerOf(calls[0], "Accept")).toBe("application/octet-stream");
  });

  it("rejects unexpected headers and mismatched lengths", async () => {
    const headers = fixtures.skill_content.headers;
    const bytes = new Uint8Array([80, 75, 5, 6]);
    const { client } = clientFor(
      new Response(bytes, { headers: { ...headers, "content-type": "text/html" } }),
      new Response(bytes, { headers: { ...headers, "x-content-type-options": "" } }),
      new Response(bytes, { headers: { ...headers, "content-disposition": "inline" } }),
      new Response(bytes, { headers: { ...headers, "content-length": "5" } }),
      new Response(bytes, { headers: { ...headers, "content-length": "3" } }),
      new Response(bytes, { headers: { ...headers, "content-length": String(65 * 1024 * 1024) } }),
      toResponse(fixtures.error_not_found),
    );
    for (let index = 0; index < 6; index += 1) {
      await expect(client.downloadSkill(skillId)).rejects.toMatchObject({ status: 502, code: "invalid_skill_content" });
    }
    await expect(client.downloadSkill(skillId)).rejects.toMatchObject({ status: 404 });
  });
});
