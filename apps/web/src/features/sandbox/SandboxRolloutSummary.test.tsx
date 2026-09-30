import type { SandboxDeployment } from "@oac/agents-client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { node } from "../overview/test-fixtures";
import { SandboxRolloutSummary } from "./SandboxRolloutSummary";
import { NodeRolloutStatus } from "./NodeRolloutStatus";
import { SandboxDeploymentSettings } from "./SandboxDeploymentSettings";

const deployment: SandboxDeployment = { credential_configured: false, configuration: {}, metadata: {}, installation_id: "i", owner_epoch: 1, generation: 4, provider: "docker", mode: "nodes", core_url: "https://core.example", resources: { allocations: 8, pending: 2 }, reset: null, suspension: null,
  rollout: { state: "settled", previous_generation_sandboxes: 8, nodes: { ready: 1, preparing: 0, failed: 2, update_required: 3, unknown: 4 } } };

describe("authoritative configuration rollout", () => {
  it("keeps attention visible while moving detailed counts off the main page", () => {
    const html = renderToStaticMarkup(<SandboxRolloutSummary deployment={deployment} />);
    expect(html).toContain("Needs attention");
    expect(html).toContain("View rollout details");
    expect(html).not.toContain("<dl");
    expect(html).not.toContain("Target generation");
    expect(html).not.toContain("Previous-generation sandboxes");
    expect(html).not.toContain("Review affected nodes");
    expect(html).not.toContain("No active preparation");
  });

  it("makes stale state explicit instead of repeating the last preparation state", () => {
    const html = renderToStaticMarkup(<SandboxRolloutSummary deployment={{ ...deployment, rollout: { ...deployment.rollout, state: "preparing" } }} stale />);
    expect(html).toContain('role="alert"');
    expect(html).toContain("Rollout state unconfirmed");
    expect(html).not.toContain("Preparing configuration");
  });

  it("does not call settled nodes ready when Core reports unknown target readiness", () => {
    const html = renderToStaticMarkup(<SandboxRolloutSummary deployment={{ ...deployment, rollout: { ...deployment.rollout, nodes: { ready: 2, preparing: 0, failed: 0, update_required: 0, unknown: 1 } } }} compact />);
    expect(html).toContain("Target readiness unknown");
    expect(html).not.toContain("No active preparation");
    expect(html).not.toContain("Ready for target");
  });

  it("reports preparing and settled without turning settled into readiness", () => {
    const quiet = { ...deployment, rollout: { state: "settled" as const, previous_generation_sandboxes: 0, nodes: { ready: 2, preparing: 0, failed: 0, update_required: 0, unknown: 0 } } };
    expect(renderToStaticMarkup(<SandboxRolloutSummary deployment={quiet} compact />)).toContain("No active preparation");
    expect(renderToStaticMarkup(<SandboxRolloutSummary deployment={{ ...quiet, rollout: { ...quiet.rollout, state: "preparing" } }} />)).toContain("Preparing configuration");
    expect(renderToStaticMarkup(<SandboxRolloutSummary deployment={{ ...quiet, provider: "" }} />)).toBe("");
  });

  it("never promotes an offline durable serving pin into ready target status", () => {
    const pinned = { ...node("offline"), online: false, rollout: { state: "ready" as const, ready_generation: 4 } };
    const html = renderToStaticMarkup(<NodeRolloutStatus node={pinned} />);
    expect(html).toContain("Target readiness unknown");
    expect(html).not.toContain("Ready for target");
    expect(renderToStaticMarkup(<NodeRolloutStatus node={{ ...pinned, online: true }} stale />)).toContain("Target readiness unknown");
  });

  it("allows same-provider online editing with held resources but keeps reset active as a boundary", () => {
    const render = (value: SandboxDeployment) => renderToStaticMarkup(<SandboxDeploymentSettings deployment={value} fresh disabled={false} onReset={async () => true} onCancelReset={async () => true} onUpdate={async () => true} onRefresh={() => undefined} refreshing={false} pending={false} />);
    const html = render(deployment);
    expect(html).toMatch(/<button[^>]*class="button outline"[^>]*>Change resources<\/button>/);
    expect(html).not.toMatch(/<button[^>]*disabled[^>]*>Change resources<\/button>/);
    expect(html).not.toContain("Add the nodes again after saving");
    const reset = { clear: "force" as const, requested_at: "2026-09-27T10:00:00Z", deadline_at: null, forced_at: "2026-09-27T10:00:00Z", remaining: { busy: 0, idle: 0, cleanup: 1, on_offline_nodes: 0, offline_nodes: [] } };
    expect(render({ ...deployment, reset })).not.toContain("Change resources");
  });
});
