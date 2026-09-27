import type { CoreInstallation } from "@agents-core-web/agents-client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { InstallationNotice } from "./InstallationNotice";

const installation: CoreInstallation = {
  object: "core.installation", installation_id: null, public_url: "http://127.0.0.1:8091", api_base_url: "http://127.0.0.1:8091/v1",
  source_commit: null, local_only: true, configuration: null,
  address_bindings: { nodes: 0, nodes_on_other_address: 0, hosted_sandboxes: 0, self_hosted_executors: 0 },
};

describe("local-only installation notice", () => {
  it("keeps missing configuration explicit and never invents a command", () => {
    const html = renderToStaticMarkup(<InstallationNotice installation={installation} />);
    expect(html).toContain("Core has not reported the configuration path or apply command");
    expect(html).not.toContain("sudo");
    expect(html).not.toContain("/opt/oac");
  });
  it.each([
    { path: "/opt/oac/config.json", apply_command: "sudo oac apply" },
    { path: "/srv/custom/config.json", apply_command: "/srv/custom/bin/core-wrapper apply --config /srv/custom/config.json" },
  ])("uses the supplied path and command verbatim: $apply_command", ({ path, apply_command }) => {
    const html = renderToStaticMarkup(<InstallationNotice installation={{ ...installation, configuration: {
      path, apply_command, applied_at: "", settings: [],
    } }} />);
    expect(html).toContain(path);
    expect(html).toContain(apply_command);
    expect(html).toContain("Copy apply command");
  });
  it("does not warn without a Core local_only report", () => {
    expect(renderToStaticMarkup(<InstallationNotice installation={undefined} />)).toBe("");
    expect(renderToStaticMarkup(<InstallationNotice installation={{ ...installation, local_only: false }} />)).toBe("");
  });
});
