import { describe, expect, it, vi } from "vitest";
import type { EnvironmentTemplate, EnvironmentTemplateResource } from "@agents-core-web/agents-client";
import { createTemplateWriteScope, draftFromTemplate, parseDomains, templatePatch, type TemplateDraft } from "./template-editor";

const template = {
  id: "template-id", name: "Original", network: { access: "enabled", allowed_domains: [] },
} as unknown as EnvironmentTemplate;
const restricted = {
  ...template, network: { access: "restricted", allowed_domains: ["pypi.org", "files.pythonhosted.org"] },
} as unknown as EnvironmentTemplate;

function draft(overrides: Partial<TemplateDraft>): TemplateDraft {
  return { name: "Original", access: "enabled", domains: "", ...overrides };
}

describe("Template updates", () => {
  it("omits unchanged fields and preserves network configuration on a rename", () => {
    expect(templatePatch(template, draft({ name: " Renamed " }))).toEqual({ name: "Renamed" });
    expect(templatePatch(template, draft({}))).toEqual({});
    expect(templatePatch(template, draft({ name: " " }))).toEqual({ name: null });
  });

  it("sends only name and network, never another section", () => {
    const advanced = {
      ...restricted,
      packages: { npm: ["typescript"], python: [], system: ["jq"] },
      files: [{ type: "inline", path: "/workspace/a", size_bytes: 1 }],
      skills: [{ type: "skill_reference", skill_id: "skill_1", version: null }],
    } as unknown as EnvironmentTemplate;
    const patch = templatePatch(advanced, { ...draftFromTemplate(advanced), name: "Renamed", access: "disabled" });
    expect(Object.keys(patch).sort()).toEqual(["name", "network"]);
    expect(patch).toEqual({ name: "Renamed", network: { access: "disabled" } });
  });

  it("keeps a restricted policy on a rename and edits its domain list", () => {
    const current = draftFromTemplate(restricted);
    expect(current).toEqual({ name: "Original", access: "restricted", domains: "pypi.org\nfiles.pythonhosted.org" });
    expect(templatePatch(restricted, { ...current, name: "Renamed" })).toEqual({ name: "Renamed" });
    expect(templatePatch(restricted, { ...current, domains: "pypi.org\n\n  files.pythonhosted.org  \n" })).toEqual({});
    expect(templatePatch(restricted, { ...current, domains: "pypi.org" }))
      .toEqual({ network: { access: "restricted", allowed_domains: ["pypi.org"] } });
    expect(templatePatch(template, draft({ access: "restricted", domains: "example.com\nexample.com" })))
      .toEqual({ network: { access: "restricted", allowed_domains: ["example.com", "example.com"] } });
    expect(() => templatePatch(template, draft({ access: "restricted", domains: " \n " }))).toThrow("at least one hostname");
  });

  it("switches basic access modes without domains", () => {
    expect(templatePatch(template, draft({ access: "disabled" }))).toEqual({ network: { access: "disabled" } });
    expect(templatePatch(restricted, { ...draftFromTemplate(restricted), access: "enabled" })).toEqual({ network: { access: "enabled" } });
  });

  it("never writes a network it does not recognize", () => {
    const unrecognized = {
      id: "template-id", object: "agent.environment.template", name: "Future", created_at: 1, updated_at: 1,
      unrecognized: ["network"],
    } as EnvironmentTemplateResource;
    expect(draftFromTemplate(unrecognized)).toEqual({ name: "Future", access: "enabled", domains: "" });
    expect(templatePatch(unrecognized, { name: "Renamed", access: "disabled", domains: "" })).toEqual({ name: "Renamed" });
  });

  it("parses one hostname per line and keeps spelling, order and duplicates", () => {
    expect(parseDomains("Example.com\r\n\n b.example \nExample.com")).toEqual(["Example.com", "b.example", "Example.com"]);
  });

  it.each(["  Existing name  ", "   "])("preserves the untouched name %j", (name) => {
    const original = { ...template, name };
    expect(templatePatch(original, draft({ name }))).toEqual({});
    expect(templatePatch(original, draft({ name, access: "disabled" }))).toEqual({ network: { access: "disabled" } });
  });

  it("counts Unicode characters rather than UTF-16 code units", () => {
    expect(templatePatch(template, draft({ name: "😀".repeat(256) })).name).toHaveLength(512);
    expect(() => templatePatch(template, draft({ name: "😀".repeat(257) }))).toThrow("256");
  });
});

describe("Template write lifecycle", () => {
  it("prevents duplicate writes and refreshes only after the receipt", async () => {
    let resolve!: () => void;
    const write = vi.fn(() => new Promise<void>((done) => { resolve = done; }));
    const refresh = vi.fn(async () => undefined);
    const scope = createTemplateWriteScope();
    const result = scope.run(write, refresh);
    await expect(scope.run(write, refresh)).resolves.toEqual({ kind: "ignored" });
    expect(write).toHaveBeenCalledOnce();
    expect(refresh).not.toHaveBeenCalled();
    resolve();
    await expect(result).resolves.toEqual({ kind: "saved" });
    expect(refresh).toHaveBeenCalledOnce();
  });

  it("aborts on disposal and never refreshes after a late mutation receipt", async () => {
    let resolve!: () => void;
    let signal!: AbortSignal;
    const refresh = vi.fn(async () => undefined);
    const scope = createTemplateWriteScope();
    const result = scope.run((value) => { signal = value; return new Promise<void>((done) => { resolve = done; }); }, refresh);
    scope.dispose();
    expect(signal.aborted).toBe(true);
    resolve();
    await expect(result).resolves.toEqual({ kind: "ignored" });
    expect(refresh).not.toHaveBeenCalled();
  });

  it("discards a refresh result after unmount", async () => {
    let resolve!: () => void;
    const scope = createTemplateWriteScope();
    const refresh = vi.fn(() => new Promise<void>((done) => { resolve = done; }));
    const result = scope.run(async () => undefined, refresh);
    await Promise.resolve();
    expect(refresh).toHaveBeenCalledOnce();
    scope.dispose();
    resolve();
    await expect(result).resolves.toEqual({ kind: "ignored" });
  });

  it("separates a saved write from a failed refresh without replaying it", async () => {
    const write = vi.fn(async () => undefined);
    const scope = createTemplateWriteScope();
    await expect(scope.run(write, async () => { throw new Error("private"); })).resolves.toEqual({ kind: "saved-refresh-failed" });
    expect(write).toHaveBeenCalledOnce();
  });

  it("does not replay uncertain writes or expose raw failures", async () => {
    const write = vi.fn(async () => { throw new Error("secret-token-and-hostname"); });
    const refresh = vi.fn(async () => undefined);
    const result = await createTemplateWriteScope().run(write, refresh);
    expect(result.kind).toBe("failed");
    expect(JSON.stringify(result)).not.toContain("secret-token");
    expect(write).toHaveBeenCalledOnce();
    expect(refresh).not.toHaveBeenCalled();
  });
});
