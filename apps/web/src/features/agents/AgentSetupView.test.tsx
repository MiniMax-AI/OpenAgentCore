import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { SavedAgent } from "@agents-core-web/agents-client";

import { AgentSetupView } from "./AgentSetupView";
import { AGENT_TEMPLATES, valuesFromAgentTemplate } from "./agent-templates";

const savedAgent: SavedAgent = {
  id: "agent/with space",
  object: "agent",
  model: "provider/model",
  name: "Existing Agent",
  instructions: "Use the saved definition.",
  metadata: {},
  multi_agent: { enabled: false, max_concurrent_subagents: null },
  reasoning: {},
  service_tier: "auto",
  text: { format: { type: "text" }, verbosity: "medium" },
  tools: [],
  created_at: 1_700_000_000,
  updated_at: 1_700_000_100,
};

describe("Agent setup page", () => {
  it("renders a dedicated setup workflow and credential-safe request preview", () => {
    const html = renderToStaticMarkup(
      <AgentSetupView
        actionError={null}
        baseUrl="/v1"
        busy={false}
        knownModels={["provider/model"]}
        onBack={() => undefined}
        onCreate={async () => undefined}
        onStartSession={() => undefined}
      />,
    );

    expect(html).toContain('aria-label="Breadcrumb"');
    expect(html).toContain("New Agent");
    expect(html).toContain("Request preview");
    expect(html).toContain("${AGENTS_CORE_API_KEY}");
    expect(html).toContain("Get started creating an Agent");
    expect(html).toContain("Text format");
    expect(html).toContain("Existing JSON schemas are preserved read-only and block Session start");
    expect(html).toContain("Reasoning effort");
    expect(html).toContain("Text verbosity");
    expect(html).toContain("Creation is locked to the current Session-compatible profile");
    expect(html).toContain(">Tools<");
    expect(html).toContain("Add Function");
    expect(html).toContain("Add HTTP MCP");
    expect(html).toContain("exact Turn and call identity");
    expect(html).toContain("MCP never runs in this browser or that executor");
    expect(html).not.toContain("Web Search switch");
    expect(html).not.toContain("Code Mode toggle");
    expect(html).not.toContain("&quot;reasoning&quot;");
    expect(html.indexOf(">Name<")).toBeLessThan(html.indexOf(">Instructions<"));
    expect(html.indexOf(">Instructions<")).toBeLessThan(html.indexOf(">Model<"));
    expect(html).not.toContain("manual-token");
  });

  it("offers only startup-enabled harnesses and defaults new Agents to the Core default", () => {
    const html = renderToStaticMarkup(
      <AgentSetupView
        actionError={null}
        baseUrl="/v1"
        busy={false}
        defaultHarness="codex"
        enabledHarnesses={["claude_sdk", "codex"]}
        knownModels={["provider/model"]}
        onBack={() => undefined}
        onCreate={async () => undefined}
        onStartSession={() => undefined}
      />,
    );

    expect(html).toContain(">Harness<");
    expect(html).toContain('<option value="codex" selected="">Codex</option>');
    expect(html).toContain('<option value="claude_sdk">Claude SDK</option>');
    expect(html).not.toContain("MiniMax Code");
    expect(html).toContain('&quot;x_agents_core&quot;: {');
    expect(html).toContain('&quot;harness&quot;: &quot;codex&quot;');
  });

  it("locks the submitted draft while Core creation is in flight", () => {
    const html = renderToStaticMarkup(
      <AgentSetupView
        actionError={null}
        baseUrl="/v1"
        busy
        knownModels={["provider/model"]}
        onBack={() => undefined}
        onCreate={async () => undefined}
        onStartSession={() => undefined}
      />,
    );

    expect(html).toContain('<fieldset class="agent-form-fields" disabled=""');
    expect(html).toContain("Saving…");
  });

  it("prefills a create-only starter template in both the form and request preview", () => {
    const template = AGENT_TEMPLATES[0]!;
    const html = renderToStaticMarkup(
      <AgentSetupView
        actionError={null}
        baseUrl="/v1"
        busy={false}
        initialValues={valuesFromAgentTemplate(template)}
        knownModels={["provider/model"]}
        onBack={() => undefined}
        onCreate={async () => undefined}
        onStartSession={() => undefined}
      />,
    );

    expect(html).toContain(`value="${template.name}"`);
    expect(html).toContain(template.instructions);
    expect(html).toContain(`&quot;name&quot;: &quot;${template.name}&quot;`);
    expect(html).not.toContain("Save changes");
    expect(html).not.toContain("Saved definition");
  });

  it("renders an existing Agent as an editable resource instead of a details step", () => {
    const html = renderToStaticMarkup(
      <AgentSetupView
        actionError={null}
        agent={savedAgent}
        baseUrl="/v1"
        busy={false}
        knownModels={[savedAgent.model]}
        onBack={() => undefined}
        onCreate={async () => undefined}
        onDeleteRequest={() => undefined}
        onStartSession={() => undefined}
        onUpdate={async () => savedAgent}
      />,
    );

    expect(html).toContain("Existing Agent");
    expect(html).toContain("/agents/agent%2Fwith%20space");
    expect(html).toContain("Save changes");
    expect(html).toContain("Delete Agent");
    expect(html).toContain("Saved definition");
    expect(html).toContain("<code>agent/with space</code>");
  });
});
