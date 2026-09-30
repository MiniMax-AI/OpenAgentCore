import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { EnvironmentTemplate, EnvironmentTemplateResource } from "@oac/agents-client";
import { filterTemplates } from "./template-name";
import { TemplateDetailPage, skillVersionLabel } from "./TemplateDetail";
import i18n from "../../i18n";

const template: EnvironmentTemplate = {
  id: "eb4fa61b-6c45-4b4c-a33b-6d9d1f05fd21", object: "agent.environment.template", name: "Restricted",
  network: { access: "disabled", allowed_domains: [] }, capability_directories: [],
  packages: { npm: [], python: [], system: [] }, files: [], plugins: [], skills: [], created_at: 1, updated_at: 2,
};

const advanced: EnvironmentTemplate = {
  id: "6a3e1f0c-2b4d-4c8e-9f10-7a6b5c4d3e2f", object: "agent.environment.template", name: "Report builder",
  created_at: 1_790_208_300, updated_at: 1_790_208_400,
  capability_directories: ["/workspace/capabilities"],
  network: { access: "restricted", allowed_domains: ["pypi.org", "files.pythonhosted.org"] },
  packages: { npm: ["typescript@5.8.3"], python: ["packaging==26.0"], system: ["jq"] },
  files: [
    { type: "inline", path: "/workspace/config/settings.json", size_bytes: 128 },
    { type: "file_id", path: "/workspace/data/input.csv", file_id: "file-2b7c9d10-4e5f-4a6b-8c7d-9e0f1a2b3c4d" },
  ],
  plugins: [{ type: "inline", name: "release-notes", description: "Draft release notes from merged changes." }],
  skills: [
    { type: "skill_reference", skill_id: "skill_3f1c2a9e-7b4d-4e8a-9c21-5d6e7f8a9b0c", version: null },
    { type: "skill_reference", skill_id: "skill_9d8c7b6a-5e4f-4d3c-8b2a-1f0e9d8c7b6a", version: "latest" },
    { type: "skill_reference", skill_id: "skill_1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", version: "2" },
    { type: "inline", name: "triage", description: "Sort incoming issues." },
  ],
};

const partial: EnvironmentTemplateResource = {
  id: "7b4f2a1d-3c5e-4d9f-8a21-8b7c6d5e4f30", object: "agent.environment.template", name: "Future profile",
  created_at: 1_790_208_200, updated_at: 1_790_208_200,
  capability_directories: [], packages: { npm: [], python: [], system: [] }, plugins: [],
  skills: [{ type: "skill_reference", skill_id: "skill_3f1c2a9e-7b4d-4e8a-9c21-5d6e7f8a9b0c", version: null }],
  unrecognized: ["network", "files"],
};

function detail(value: EnvironmentTemplateResource, links = true): string {
  return renderToStaticMarkup(
    <TemplateDetailPage
      template={value}
      blocked={false}
      refreshing={false}
      onBack={() => undefined}
      onRefresh={() => undefined}
      onDelete={() => undefined}
      onOpenFile={links ? () => undefined : undefined}
      onOpenSkill={links ? () => undefined : undefined}
    />,
  );
}

describe("Environment Template full configuration", () => {


  it("filters by name or ID", () => {
    expect(filterTemplates([advanced, partial, template], "report").map((entry) => entry.id)).toEqual([advanced.id]);
    expect(filterTemplates([advanced, partial, template], template.id.slice(0, 8))).toEqual([template]);
  });

  it("shows every section of an advanced Template", () => {
    const html = detail(advanced);
    for (const value of [
      "pypi.org", "files.pythonhosted.org", "typescript@5.8.3", "packaging==26.0", "jq", "/workspace/capabilities",
      "/workspace/config/settings.json", "128 B", "/workspace/data/input.csv",
      "Open file-2b7c9d10-4e5f-4a6b-8c7d-9e0f1a2b3c4d in Files",
      "Open skill_3f1c2a9e-7b4d-4e8a-9c21-5d6e7f8a9b0c in Skills",
      ">Default<", ">latest<", ">v2<", "triage", "Sort incoming issues.",
      "release-notes", "Draft release notes from merged changes.",
    ]) expect(html).toContain(value);
    expect(html).not.toContain("Not recognized");
  });

  it("states why env and setup commands are not shown, without claiming they are unset", () => {
    const html = detail(advanced);
    expect(html).toContain("Environment variables and setup commands");
    expect(html).toContain("Core never returns them, so this console cannot tell whether they are configured.");
    expect(html).not.toMatch(/not configured|未配置/iu);
  });

  it("keeps File and Skill IDs copyable when no page link is wired", () => {
    const html = detail(advanced, false);
    expect(html).not.toContain("in Files");
    expect(html).toContain("file-2b7c9d10-4e5f-4a6b-8c7d-9e0f1a2b3c4d");
    expect(html).toContain("Copy File ID");
  });

  it("marks unrecognized sections instead of guessing them", () => {
    const html = detail(partial);
    expect(html).toContain("Not recognized by this console: Network, Files");
    expect(html.match(/Not recognized</g)?.length).toBe(2);
    expect(html).toContain("Open skill_3f1c2a9e-7b4d-4e8a-9c21-5d6e7f8a9b0c in Skills");
  });

  it("labels Skill version selectors", () => {
    const t = i18n.getFixedT("en", "templates") as never;
    expect([null, "latest", "12"].map((version) => skillVersionLabel(version, t))).toEqual(["Default", "latest", "v12"]);
  });





  it("renders the Chinese detail with API terms kept in English", async () => {
    await i18n.changeLanguage("zh-CN");
    try {
      const html = detail(advanced);
      expect(html).toContain("环境变量和启动命令");
      expect(html).toContain("Core 不返回它们");
      expect(html).toContain(">默认<");
      expect(html).toContain("Skill");
    } finally {
      await i18n.changeLanguage("en");
    }
  });
});
