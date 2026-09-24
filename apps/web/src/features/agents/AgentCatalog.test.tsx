import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { SavedAgent } from "@agents-core-web/agents-client";

import { AgentCatalog } from "./AgentCatalog";
import type { AgentUsageModel } from "./AgentUsage";
import { aggregateAgentUsage, type UsageSessionRecord } from "./agent-usage";
import { type AgentUsageController, initialAgentUsageState } from "./use-agent-usage";

function savedAgent(index: number): SavedAgent {
  return {
    id: `agent_${index}`,
    object: "agent",
    model: "provider/model",
    name: `Agent ${index}`,
    instructions: `Instructions ${index}`,
    metadata: {},
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [],
    created_at: 1_700_000_000 + index,
    updated_at: 1_700_000_100 + index,
  };
}

function controller(overrides: Partial<AgentUsageController> = {}): AgentUsageController {
  return {
    ...initialAgentUsageState,
    setRange: () => undefined,
    cancel: () => undefined,
    resume: () => undefined,
    reload: () => undefined,
    ...overrides,
  };
}

function renderCatalog(agents: SavedAgent[], options: { usage?: AgentUsageModel | null; hasSavedAgents?: boolean; isFiltering?: boolean } = {}): string {
  return renderToStaticMarkup(
    <AgentCatalog
      agents={agents}
      busy={false}
      coreReady
      hasSavedAgents={options.hasSavedAgents ?? agents.length > 0}
      isFiltering={options.isFiltering ?? false}
      openingAgentId={null}
      usage={options.usage}
      vaultCatalog={null}
      onClearSearch={() => undefined}
      onCreate={() => undefined}
      onEdit={() => undefined}
      onStartSession={() => undefined}
    />,
  );
}

describe("Agent catalog", () => {
  it("lists every saved Agent as a table row that opens its setup view", () => {
    const agents = Array.from({ length: 12 }, (_, index) => savedAgent(index));
    agents[1] = { ...agents[1]!, x_agents_core: { harness: "claude_sdk" }, tools: [{ type: "function" }, { type: "function" }] };
    const html = renderCatalog(agents);

    expect(html).toContain('<table class="data-table agent-table" aria-label="Agents">');
    for (const column of ["Agent", "Model", "Harness", "Tools", "Updated"]) expect(html).toContain(`<th scope="col"${column === "Tools" || column === "Updated" ? ' class="numeric"' : ""}>${column}</th>`);
    expect(html.match(/<tr class="clickable-row"/g)).toHaveLength(12);
    expect(html).toContain('aria-label="Edit Agent 11 (agent_11)" data-agent-id="agent_11"');
    expect(html).toContain('aria-label="Copy Agent ID agent_0"');
    expect(html).toContain("Claude SDK");
    expect(html).toContain("Core default");
    expect(html).toContain('aria-label="Start a Session with Agent 0 (agent_0)"');
    expect(html).not.toContain("Starter templates");
    // Usage columns appear only when the page can read Sessions.
    expect(html).not.toContain("Coverage");
  });

  it("shows usage columns as pending while Sessions are read and as figures once ready", () => {
    const agents = [savedAgent(0), savedAgent(1), savedAgent(2)];
    const pending = renderCatalog(agents, { usage: { controller: controller({ status: "loading" }), report: null } });
    expect(pending).toContain('<th scope="col" class="numeric">Sessions</th>');
    expect(pending).toContain("Coverage");
    expect(pending.match(/agent-usage-pending">…</g)).toHaveLength(12);

    const now = Math.floor(Date.now() / 1_000);
    const records: UsageSessionRecord[] = [
      { id: "s1", agentId: "agent_0", status: "idle", createdAt: now - 100, lastActiveAt: now - 50, usage: { input: 900, output: 300, total: 1_200, cached: 0, reasoning: 0 } },
      { id: "s2", agentId: "agent_0", status: "failed", createdAt: now - 200, lastActiveAt: now - 150, usage: null },
      { id: "s3", agentId: "agent_1", status: "idle", createdAt: now - 300, lastActiveAt: now - 250, usage: null },
    ];
    const ready = renderCatalog(agents, {
      usage: { controller: controller({ status: "ready" }), report: aggregateAgentUsage(records, agents.map((agent) => agent.id), null) },
    });
    expect(ready).toContain('<td class="numeric" title="2">2</td>');
    expect(ready).toContain('<td class="numeric" title="1,200">1,200</td>');
    expect(ready).toContain('<td class="numeric">50%</td>');
    // Reported usage is missing, not zero.
    expect(ready).toContain('<td class="numeric">No data</td>');
    expect(ready).not.toContain('<td class="numeric">0%</td>');
    // An Agent without Sessions shows a real zero count and no derived figures.
    expect(ready).toContain('<td class="numeric" title="0">0</td><td class="numeric">—</td><td class="numeric">—</td><td class="numeric">—</td>');
  });

  it("offers creation when nothing is saved and clearing when a search matches nothing", () => {
    const empty = renderCatalog([], { hasSavedAgents: false });
    expect(empty).toContain("No saved Agents");
    expect(empty).toContain("Create agent");

    const noMatch = renderCatalog([], { hasSavedAgents: true, isFiltering: true });
    expect(noMatch).toContain("No matching Agents");
    expect(noMatch).toContain("Clear search");
  });
});
