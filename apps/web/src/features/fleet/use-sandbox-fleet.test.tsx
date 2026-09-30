import type { SandboxDeployment } from "@oac/agents-client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { gettingStartedSteps } from "../overview/getting-started";
import { node } from "../overview/test-fixtures";
import type { SandboxConsoleConfig } from "../sandbox/console-config";
import { sandboxDeploymentQuery } from "../sandbox/sandbox-queries";
import { consoleConfigQuery, fleetQuery, type FleetSnapshot } from "./fleet-queries";
import { FleetReadNotice } from "./FleetReadNotice";
import { fleetSnapshot, useSandboxFleet } from "./use-sandbox-fleet";

const configured: SandboxDeployment = { rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, installation_id: "i", provider: "docker", core_url: "http://core", reset: null, owner_epoch: 1, generation: 1, mode: "nodes", resources: { allocations: 1, pending: 0 }, suspension: null };

function Probe() {
  const { state, deployment } = useSandboxFleet();
  const snapshot = fleetSnapshot(state);
  const sandboxes = gettingStartedSteps({ fleet: state, sandboxReset: deployment.data ? deployment.data.reset !== null : undefined, localOnly: false, projects: [], sessions: 0, harnesses: [] }).sandboxes;
  return <div data-fleet={state.status} data-sandbox={sandboxes.state ?? "checking"}>{snapshot ? `${snapshot.nodes.length} nodes / ${snapshot.deployment.resources.allocations} allocations` : "No current fleet evidence"}<FleetReadNotice state={state} onRetry={() => {}} /></div>;
}

function render(latest: SandboxDeployment, previous = configured, failed = false) {
  const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const snapshot: FleetSnapshot = { deployment: previous, nodes: [node("n1")], allocations: [], loadedAt: 1 };
  const config: SandboxConsoleConfig = { sandbox_admin: true, node_installer: false, node_installer_sha256: "" };
  cache.setQueryData<SandboxConsoleConfig | null>(consoleConfigQuery.queryKey, () => config);
  cache.setQueryData(fleetQuery(false).queryKey, snapshot);
  cache.setQueryData(sandboxDeploymentQuery.queryKey, latest);
  if (failed) cache.getQueryCache().find({ queryKey: fleetQuery(false).queryKey })?.setState({ status: "error", error: new Error("inventory read failed") });
  const html = renderToStaticMarkup(<QueryClientProvider client={cache}><Probe /></QueryClientProvider>);
  cache.clear();
  return html;
}

describe("fleet evidence after deployment changes", () => {
  it("cannot complete onboarding or show old resource counts when reset leaves an unconfigured deployment", () => {
    const resetting = { ...configured, reset: { clear: "auto", requested_at: "2026-09-27T10:00:00Z", deadline_at: "2026-09-27T11:00:00Z", forced_at: null, remaining: { busy: 1, idle: 0, cleanup: 0, on_offline_nodes: 0, offline_nodes: [] } } } satisfies SandboxDeployment;
    const completed = { ...configured, provider: "", mode: "", generation: 2, resources: { allocations: 0, pending: 0 } } satisfies SandboxDeployment;
    const html = render(completed, resetting);
    expect(html).toContain('data-fleet="loading"');
    expect(html).toContain('data-sandbox="checking"');
    expect(html).not.toContain("1 nodes");
    expect(html).not.toContain("1 allocations");
  });

  it("also rejects prior installation, owner and provider evidence", () => {
    for (const change of [{ installation_id: "next" }, { owner_epoch: 2 }, { provider: "e2b" as const }]) {
      const html = render({ ...configured, ...change });
      expect(html).toContain('data-fleet="loading"');
      expect(html).not.toContain('data-sandbox="done"');
    }
    expect(render(configured)).toContain('data-sandbox="done"');
  });

  it("retains older resource evidence across an online generation change with an explicit qualification", () => {
    const html = render({ ...configured, generation: 2, rollout: { state: "preparing", previous_generation_sandboxes: 1, nodes: { ready: 0, preparing: 1, failed: 0, update_required: 0, unknown: 0 } } });
    expect(html).toContain('data-fleet="ready"');
    expect(html).toContain("1 nodes / 1 allocations");
    expect(html).toContain("configuration generation 1; the current target is 2");
    expect(html).toContain('data-sandbox="done"');
  });

  it("discards inventory across reset start even when provider and generation match", () => {
    const html = render({ ...configured, reset: { clear: "auto", requested_at: "2026-09-27T10:00:00Z", deadline_at: "2026-09-27T11:00:00Z", forced_at: null, remaining: { busy: 1, idle: 0, cleanup: 0, on_offline_nodes: 0, offline_nodes: [] } } });
    expect(html).toContain('data-fleet="loading"');
    expect(html).not.toContain("1 allocations");
    expect(html).toContain('data-sandbox="todo"');
  });

  it("keeps both generation and read-failure qualifications when an online refresh fails", () => {
    const html = render({ ...configured, generation: 2 }, configured, true);
    expect(html).toContain("1 nodes / 1 allocations");
    expect(html).toContain("configuration generation 1; the current target is 2");
    expect(html).toContain("Retry");
    expect(html).toContain('data-sandbox="unknown"');
  });

});
