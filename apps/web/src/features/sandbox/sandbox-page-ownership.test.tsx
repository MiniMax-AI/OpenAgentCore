import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { SandboxDeployment } from "@oac/agents-client";
import { describe, expect, it, vi } from "vitest";
import type { SandboxConsoleConfig } from "./console-config";
import { SandboxManagerView } from "./SandboxManagerView";
import { SandboxDeploymentPage } from "./SandboxDeploymentPage";
import { SystemPage } from "../system/SystemPage";
import { sandboxConsoleConfigQuery, sandboxDeploymentQuery, sandboxSnapshotQuery } from "./sandbox-queries";
import { beginSandboxWrite, settleSandboxWrite } from "./sandbox-write-ownership";

// Portaled interactive flows are covered by browser acceptance; identify their page owner here.
vi.mock("./SandboxSetupWizard", () => ({ SandboxSetupWizard: () => <div>setup-flow</div> }));
vi.mock("./NodeEnrollment", () => ({ NodeEnrollment: () => <div>node-enrollment</div> }));

function cache(provider: SandboxDeployment["provider"]) {
  const client = new QueryClient({ defaultOptions: { queries: { enabled: false, retry: false, gcTime: Infinity } } });
  const deployment: SandboxDeployment = {
    installation_id: "install", owner_epoch: 1, generation: 2, provider, core_url: "https://core.example",
    mode: provider === "e2b" ? "direct" : "nodes", reset: null, resources: { allocations: 0, pending: 0 }, suspension: null,
    rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: null },
  };
  const config: SandboxConsoleConfig = { sandbox_admin: true, node_installer: false, node_installer_sha256: "" };
  client.setQueryData(sandboxConsoleConfigQuery.queryKey, () => config);
  client.setQueryData(sandboxDeploymentQuery.queryKey, deployment);
  client.setQueryData(sandboxSnapshotQuery.queryKey, { deployment, nodes: [], allocations: [], nodesError: null, readAt: 0 });
  return client;
}
const render = (page: ReactElement, client: QueryClient) => renderToStaticMarkup(<QueryClientProvider client={client}>{page}</QueryClientProvider>);

describe("sandbox page ownership", () => {
  it("keeps setup in System's secondary page and sends empty Nodes there", () => {
    const client = cache("");
    const nodes = render(<SandboxManagerView />, client);
    expect(nodes).not.toContain("setup-flow");
    expect(nodes).toContain("Set up sandbox hosting in System before adding nodes.");
    expect(nodes).toContain("Manage sandbox configuration");
    const configuration = render(<SandboxDeploymentPage />, client);
    expect(configuration).toContain("setup-flow");
    expect(configuration).toContain("Back to System");
    expect(configuration).not.toContain("node-enrollment");
  });

  it("keeps Nodes as a node page for both cloud and host deployments", () => {
    const cloud = render(<SandboxManagerView />, cache("e2b"));
    expect(cloud).toContain("Nodes</h1>");
    expect(cloud).toContain("There are no nodes to manage.");
    expect(cloud).not.toContain("Change resources");
    const hosts = render(<SandboxManagerView />, cache("docker"));
    expect(hosts).toContain("node-enrollment");
    expect(hosts).toContain("Add your first node");
    expect(hosts).not.toContain("Reset deployment");
  });

  it("opens editing deliberately and keeps the System landing page to one entry", () => {
    const client = cache("docker");
    const configuration = render(<SandboxDeploymentPage />, client);
    expect(configuration).toContain("Change resources");
    expect(configuration).toContain("Reset deployment");
    expect(configuration).not.toContain("setup-flow");
    const system = render(<SystemPage />, client);
    expect(system).toContain("Manage sandbox configuration");
    expect(system).not.toContain("Change resources");
    expect(system).not.toContain("Reset deployment");
  });

  it("shares pending and uncertain write ownership across both pages", () => {
    const client = cache("docker");
    const attempt = beginSandboxWrite(client)!;
    expect(render(<SandboxManagerView />, client)).toContain("Saving sandbox change");
    expect(render(<SandboxDeploymentPage />, client)).toContain("Saving sandbox change");
    settleSandboxWrite(client, attempt);
    for (const page of [<SandboxManagerView />, <SandboxDeploymentPage />]) {
      expect(render(page, client)).toContain("Refresh sandbox state to confirm whether the change was saved before submitting again.");
    }
  });
});
