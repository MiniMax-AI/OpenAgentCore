import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { SavedAgent } from "@agents-core-web/agents-client";

import { AgentForm } from "./AgentForm";
import { AgentDeleteConfirmation, AgentDetails, AgentsView } from "./AgentsView";

const agent: SavedAgent = {
  id: "agent_1",
  object: "agent",
  model: "provider/model",
  name: null,
  instructions: null,
  metadata: { team: "web" },
  multi_agent: { enabled: true, max_concurrent_subagents: 2 },
  reasoning: { effort: "high", summary: "concise" },
  service_tier: "priority",
  text: { format: { type: "json_schema", schema: { type: "object" } }, verbosity: "high" },
  tools: [{ type: "tool_search" }],
  created_at: 1_700_000_000,
  updated_at: 1_700_000_100,
};

describe("Agents view", () => {
  it("renders complete saved details and advanced fields as read-only capability information", () => {
    const html = renderToStaticMarkup(<AgentDetails agent={agent} />);

    expect(html).toContain("agent_1");
    expect(html).toContain("Created");
    expect(html).toContain("Updated");
    expect(html).toContain("Metadata");
    expect(html).toContain("Tools");
    expect(html).toContain("tool_search is saved-only and cannot run in a Session");
    expect(html).toContain("Reasoning");
    expect(html).toContain("Text");
    expect(html).toContain("Service tier");
    expect(html).toContain("Multi-agent");
    expect(html).toContain("known Core Session profile cannot start it");
    expect(html).not.toMatch(/<(input|textarea|select)/);
  });

  it("uses the same keyboard-submittable, focus-ready form for existing Agent values", () => {
    const html = renderToStaticMarkup(
      <AgentForm agent={agent} formId="edit-agent" knownModels={[agent.model]} onSubmit={async () => undefined} />,
    );

    expect(html).toContain('<form id="edit-agent"');
    expect(html).toContain('data-agent-initial-focus="true"');
    expect(html).toContain("Web-side suggestions, not a discovered Core catalog");
    expect(html).toContain("Never store secrets in Agent metadata");
    expect(html).toContain("Read-only saved tool");
    expect(html).toContain("tool_search is saved-only and cannot run in a Session");
    expect(html).toContain("provider/model");
  });

  it("strictly summarizes supported tools and hides malformed or credentialed raw values", () => {
    const protectedAgent: SavedAgent = {
      ...agent,
      tools: [
        { type: "function", name: "lookup", description: "Lookup", parameters: { type: "object" }, defer_loading: false },
        {
          type: "mcp",
          server_label: "docs",
          transport: { type: "http", server_url: "https://mcp.example/tools" },
          connection_origin: "service",
          allowed_tools: null,
          required: true,
        },
        { type: "function", name: "unsafe", description: "", parameters: {}, defer_loading: false, extra: "secret-value" },
        { type: "mcp", credential_id: "vault_opaque", request_metadata: { authorization: "secret-value" } },
        { type: "future_tool", opaque: "secret-value" },
      ],
    };
    const details = renderToStaticMarkup(<AgentDetails agent={protectedAgent} />);
    const form = renderToStaticMarkup(
      <AgentForm agent={protectedAgent} formId="edit-protected-agent" knownModels={[agent.model]} onSubmit={async () => undefined} />,
    );

    expect(details).toContain("Function <code>lookup</code>");
    expect(details).toContain("Anonymous service-origin HTTP MCP <code>docs</code>");
    expect(details).toContain("Unsupported Function tool definition · read only");
    expect(details).toContain("Credentialed MCP is unresolved or URL-mismatched in the current Vault catalog · read only");
    expect(details).toContain("Unsupported saved tool definition · read only");
    expect(form).toContain("Credentialed MCP is unresolved or URL-mismatched in the current Vault catalog");
    expect(details + form).not.toContain("vault_opaque");
    expect(details + form).not.toContain("secret-value");
    expect(details + form).not.toContain("future_tool");
  });

  it("opens saved Agents through the edit affordance and fails closed for an unsupported Session profile", () => {
    const html = renderToStaticMarkup(
      <AgentsView
        agents={[agent]}
        busy={false}
        coreError={null}
        coreState="ready"
        onCreate={async () => undefined}
        onRefresh={() => undefined}
        onStartSession={async () => undefined}
      />,
    );

    expect(html).toContain('<table class="data-table agent-table" aria-label="Agents">');
    expect(html).toContain('aria-label="Edit Untitled Agent (agent_1)" data-agent-id="agent_1"');
    expect(html).toContain('aria-disabled="true" aria-label="Start a Session with Untitled Agent (agent_1)"');
    expect(html).toContain('data-create-agent-entry="true"><svg');
    expect(html).toContain("Create agent</button>");
    expect(html).not.toContain("Starter templates");
    expect(html).toContain('<span>Unavailable</span>');
    expect(html).toContain("Session unavailable: Current Core Session admission requires");
    // Search lives in the page body filter bar, not in the header actions.
    expect(html.indexOf('aria-label="Search Agents"')).toBeGreaterThan(html.indexOf('class="console-page-body"'));
    // Without a Session source the page shows no usage statistics.
    expect(html).not.toContain("Sessions created");
    expect(html).not.toContain("not a basis for billing");
  });

  it("adds the usage range, caveat and pending usage columns when it can read Sessions", () => {
    const html = renderToStaticMarkup(
      <AgentsView
        agents={[agent]}
        busy={false}
        coreError={null}
        coreState="ready"
        onCreate={async () => undefined}
        onRefresh={() => undefined}
        onStartSession={async () => undefined}
        usageSource={{ listSessionsTolerant: async () => ({ object: "list", data: [], unrecognized: [], has_more: false, first_id: null, last_id: null }) }}
      />,
    );

    expect(html).toContain('role="radiogroup" aria-label="Sessions created"');
    expect(html).toContain("Data comes from cumulative Session usage reported by Core. It excludes unreported usage and is not a basis for billing.");
    expect(html).toContain('<th scope="col" class="numeric">Sessions</th>');
    expect(html).toContain('agent-usage-pending">…</td>');
  });

  it("keeps Session start available for the known admission profile", () => {
    const compatible = {
      ...agent,
      multi_agent: { enabled: false, max_concurrent_subagents: null },
      reasoning: {},
      service_tier: "auto" as const,
      text: { format: { type: "text" as const }, verbosity: "medium" as const },
      tools: [],
    };
    const html = renderToStaticMarkup(
      <AgentsView
        agents={[compatible]}
        busy={false}
        coreError={null}
        coreState="ready"
        onCreate={async () => undefined}
        onRefresh={() => undefined}
        onStartSession={async () => undefined}
      />,
    );

    expect(html).toContain('type="button" aria-label="Start a Session with Untitled Agent (agent_1)"');
    expect(html).toContain('<span>Start Session</span>');
    expect(html).not.toContain("Session unavailable:");
  });

  it("states the durable delete boundary before confirmation", () => {
    const html = renderToStaticMarkup(<AgentDeleteConfirmation agent={agent} />);

    expect(html).toContain("Delete <strong>Untitled Agent</strong> from Agent Core?");
    expect(html).toContain("Exact Agent ID: <code>agent_1</code>");
    expect(html).toContain("only after Core confirms success");
    expect(html).toContain("Existing Sessions keep their durable Agent snapshots");
  });
});
