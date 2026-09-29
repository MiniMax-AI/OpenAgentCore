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
  it("links to domain setup without exposing installer commands", () => {
    const html = renderToStaticMarkup(<InstallationNotice installation={installation} />);
    expect(html).toContain("Configure domain and HTTPS");
    expect(html).toContain("Configure HTTPS before connecting applications and nodes");
    expect(html).not.toContain("config.json");
    expect(html).not.toContain("oac apply");
  });
  it("does not warn without a Core local_only report", () => {
    expect(renderToStaticMarkup(<InstallationNotice installation={undefined} />)).toBe("");
    expect(renderToStaticMarkup(<InstallationNotice installation={{ ...installation, local_only: false }} />)).toBe("");
  });
});
