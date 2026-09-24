import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { AgentSession, RuntimeObservation } from "@agents-core-web/agents-client";

import { OverviewView } from "../features/overview/OverviewView";
import { SessionsLogView } from "../features/resources/SessionsLogView";
import { HelpTip, Kpi, KpiStrip, PageHeader } from "./console-ui";

describe("console grammar", () => {
  it("keeps help text in the document and links it to the help button", () => {
    const html = renderToStaticMarkup(<HelpTip>Saving does not start a Runtime.</HelpTip>);
    const describedBy = html.match(/aria-describedby="([^"]+)"/)?.[1];
    expect(describedBy).toBeTruthy();
    expect(html).toContain('aria-label="Help"');
    expect(html).toContain(`id="${describedBy}" class="visually-hidden">Saving does not start a Runtime.</span>`);
    expect(html).not.toContain('role="tooltip"');
  });

  it("renders KPI labels with values and hides details behind help", () => {
    const html = renderToStaticMarkup(
      <KpiStrip label="Health">
        <Kpi label="Needs attention" value="5" tone="danger" help="Failed 3 · Waiting 2" />
      </KpiStrip>,
    );
    expect(html).toContain('<dl class="kpi-strip" aria-label="Health">');
    expect(html).toContain("kpi kpi-danger");
    expect(html).toContain('<span class="kpi-value">5</span>');
    expect(html).not.toContain("<small>");
  });

  it("renders page headers without description paragraphs", () => {
    const html = renderToStaticMarkup(<PageHeader title="Overview" help="Health and capacity." headingId="h" />);
    expect(html).toContain('<h1 id="h">Overview</h1>');
    expect(html).not.toContain("<p>");
  });
});

describe("OverviewView", () => {
  it("shows missing values instead of zero while collections load", () => {
    const html = renderToStaticMarkup(
      <OverviewView
        coreBaseUrl="/v1"
        coreState="connecting"
        agentsState="connecting"
        agentsError={null}
        sessions={[]}
        sessionsState="connecting"
        sessionsError={null}
        runtimeSnapshot={null}
        runtimeState="connecting"
        runtimeError={null}
        onRefresh={() => undefined}
        onOpenSession={() => undefined}
        onNavigate={() => undefined}
        onConfigureConnection={() => undefined}
      />,
    );
    expect(html).toContain("Overview");
    expect(html).not.toMatch(/kpi-value">0</);
    expect(html).toContain('kpi-value">—<');
    expect(html).not.toContain("recovery-banner");
  });

  it("offers backend recovery only when both collections fail with a gateway status", () => {
    const html = renderToStaticMarkup(
      <OverviewView
        coreBaseUrl="/v1"
        coreState="failed"
        agentsState="failed"
        agentsError="Agent Core request failed (502)."
        sessions={[]}
        sessionsState="failed"
        sessionsError="Agent Core request failed (503)."
        runtimeSnapshot={null}
        runtimeState="failed"
        runtimeError={null}
        onRefresh={() => undefined}
        onOpenSession={() => undefined}
        onNavigate={() => undefined}
        onConfigureConnection={() => undefined}
      />,
    );
    expect(html).toContain("recovery-banner");
    expect(html).toContain("Agent Core backend is not ready. Open Docker startup guide");
  });
});

describe("resource and overview views", () => {
  const session = (id: string, overrides: Partial<AgentSession> = {}): AgentSession => ({
    id, object: "agent.session",
    agent: { id: "agent_a", name: "Reviewer", model: "model-a" } as AgentSession["agent"],
    environment: { type: "none" } as AgentSession["environment"],
    status: "idle", error: null, metadata: {}, required_actions: [], vault_ids: [], usage: null,
    created_at: 1_000, last_active_at: 2_000,
    ...overrides,
  });

  it("lists Sessions with status, ID and an error tip for failures", () => {
    const html = renderToStaticMarkup(
      <SessionsLogView
        sessions={[session("sess_failed_0001", { status: "failed", error: "Model provider returned 429." }), session("sess_idle_0002")]}
        state="ready"
        error={null}
        onRefresh={() => undefined}
        onOpenSession={() => undefined}
      />,
    );
    expect(html).toContain("Session log");
    expect(html).toContain("Model provider returned 429.");
    expect(html).toContain('aria-label="Error"');
    expect(html).toContain('title="sess_failed_0001"');
    expect(html).not.toContain("table-error");
  });

  it("marks retained runtime figures as stale after a refresh failure", () => {
    const hosted = session("s_hosted", { environment: { type: "openai_hosted" } as AgentSession["environment"] });
    const observation = {
      id: "s_hosted", object: "agent.runtime_observation", session_id: "s_hosted", resolved_at: 1, environment_id: "env",
      mode: "openai_hosted", provider_type: "docker",
      instance: { kind: "managed_allocation", allocation_id: "alloc", device_id: null, connection_generation: null },
      lifecycle_state: "active", status: "observed", reason: null, allocation_created_at: 1, observed_at: 1, started_at: 1,
      cpu: { usage_seconds_total: 1, capacity_cores: 2, usage_cores: 0.5, utilization_ratio: null },
      memory: { usage_bytes: 100, limit_bytes: 400 },
    } as RuntimeObservation;
    const html = renderToStaticMarkup(
      <OverviewView
        coreBaseUrl="/v1" coreState="ready" agentsState="ready" agentsError={null}
        sessions={[hosted]} sessionsState="ready" sessionsError={null}
        runtimeSnapshot={{ sessions: [hosted], observations: [observation], loadedAt: 1 }}
        runtimeState="failed" runtimeError="Runtime snapshot exceeded the Web target budget."
        onRefresh={() => undefined} onOpenSession={() => undefined} onNavigate={() => undefined} onConfigureConnection={() => undefined}
      />,
    );
    expect(html).toContain("Stale");
    expect(html).toContain("Runtime: Runtime snapshot exceeded the Web target budget.");
    expect(html).not.toContain("recovery-banner");
  });
});
