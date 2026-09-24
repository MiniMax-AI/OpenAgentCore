import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { EnvironmentTemplate, EnvironmentTemplateResource } from "@agents-core-web/agents-client";
import { EnvironmentTemplatesView, filterTemplates, type EnvironmentTemplatesViewProps } from "./EnvironmentTemplatesView";
import { TemplateDetailPage, skillVersionLabel } from "./TemplateDetail";
import { TemplateForm } from "./TemplateForm";
import i18n from "../../i18n";

const template: EnvironmentTemplate = {
  id: "eb4fa61b-6c45-4b4c-a33b-6d9d1f05fd21", object: "agent.environment.template", name: "Restricted",
  network: { access: "disabled", allowed_domains: [] }, capability_directories: [],
  packages: { npm: [], python: [], system: [] }, files: [], plugins: [], skills: [], created_at: 1, updated_at: 2,
};
function render(catalog: EnvironmentTemplatesViewProps["catalog"]) {
  const operations = {
    createEnvironmentTemplate: vi.fn(), updateEnvironmentTemplate: vi.fn(), deleteEnvironmentTemplate: vi.fn(),
  };
  const html = renderToStaticMarkup(<EnvironmentTemplatesView catalog={catalog} operations={operations} onRefresh={async () => undefined} onConfigureConnection={() => undefined} />);
  expect(operations.createEnvironmentTemplate).not.toHaveBeenCalled();
  expect(operations.updateEnvironmentTemplate).not.toHaveBeenCalled();
  expect(operations.deleteEnvironmentTemplate).not.toHaveBeenCalled();
  return html;
}

describe("Environment Templates view", () => {
  it("distinguishes unread, unsupported, failed and empty catalogs", () => {
    expect(render(null)).toContain("Loading Environment Templates");
    expect(render({ state: "unsupported" })).toContain("does not expose the Template resource");
    const failed = render({ state: "failed", message: "secret-token" });
    expect(failed).toContain("could not be loaded");
    expect(failed).not.toContain("No Environment Templates yet");
    expect(failed).not.toContain("secret-token");
    expect(render({ state: "ready", templates: [] })).toContain("No Environment Templates yet");
  });

  it("shows safe configuration and actions without execution readiness claims", () => {
    const html = render({ state: "ready", templates: [template] });
    expect(html).toContain("Environment Templates</h1>");
    expect(html).toContain("Filter Templates");
    expect(html).toContain("Edit Restricted");
    expect(html).toContain("Delete Restricted");
    expect(html).toContain(template.id);
    expect(html).toContain("does not start a Runtime");
    expect(html).not.toContain("connected");
  });

  it("does not render unknown fields or private configuration", () => {
    const value = { ...template, env: { KEY: "secret-canary" }, setup_commands: ["private-command"] };
    const html = render({ state: "ready", templates: [value] });
    expect(html).not.toContain("secret-canary");
    expect(html).not.toContain("private-command");
  });

  it("provides explicit labels and blocks an unchanged edit", () => {
    const html = renderToStaticMarkup(<TemplateForm template={template} busy={false} onSave={() => undefined} onCancel={() => undefined} />);
    expect(html).toContain("Network access</label>");
    expect(html).toContain("Existing Sessions keep their configuration");
    expect(html).toContain('type="submit" disabled=""');
  });
});

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
      onEdit={() => undefined}
      onDelete={() => undefined}
      onOpenFile={links ? () => undefined : undefined}
      onOpenSkill={links ? () => undefined : undefined}
    />,
  );
}

describe("Environment Template full configuration", () => {
  it("lists advanced and partly unrecognized Templates side by side", () => {
    const html = render({ state: "ready", templates: [advanced, partial, template] });
    expect(html).toContain("Report builder");
    expect(html).toContain("Restricted");
    expect(html).toContain("(2)");
    expect(html).toContain("Unrecognized configuration");
    expect(html.match(/Unrecognized configuration/g)).toHaveLength(1);
    expect(html).toContain("Edit Future profile");
    expect(html).toContain("3 of 3 Templates");
  });

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

  it("edits a restricted network with its domains", () => {
    const html = renderToStaticMarkup(<TemplateForm template={advanced} busy={false} onSave={() => undefined} onCancel={() => undefined} />);
    expect(html).toContain('<option value="restricted" selected="">Restricted</option>');
    expect(html).toContain("pypi.org\nfiles.pythonhosted.org</textarea>");
    expect(html).toContain("Only the name and network are saved.");
  });

  it("locks a network policy it does not recognize", () => {
    const html = renderToStaticMarkup(<TemplateForm template={partial} busy={false} onSave={() => undefined} onCancel={() => undefined} />);
    expect(html).toMatch(/<select[^>]*disabled=""/u);
    expect(html).toContain("cannot be edited here");
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
