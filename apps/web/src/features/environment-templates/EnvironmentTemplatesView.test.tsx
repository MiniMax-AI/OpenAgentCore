import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { EnvironmentTemplate } from "@agents-core-web/agents-client";
import { EnvironmentTemplatesView, type EnvironmentTemplatesViewProps } from "./EnvironmentTemplatesView";
import { TemplateForm } from "./TemplateForm";

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
