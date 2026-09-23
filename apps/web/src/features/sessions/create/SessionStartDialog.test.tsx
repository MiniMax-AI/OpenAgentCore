import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { AgentCoreError, type SavedAgent } from "@agents-core-web/agents-client";
import type { VaultCatalog } from "../../vaults/vault-catalog";

import {
  genericSessionStartError,
  safeSessionStartError,
  SessionStartDialog,
} from "./SessionStartDialog";

function agent(id: string, name: string, overrides: Partial<SavedAgent> = {}): SavedAgent {
  return {
    id,
    object: "agent",
    model: "provider/model",
    name,
    instructions: null,
    metadata: {},
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [],
    created_at: 1,
    updated_at: 1,
    ...overrides,
  };
}

const compatible = agent("agent_compatible", "Compatible Agent");
const blocked = agent("agent_blocked", "Blocked Agent", {
  reasoning: { effort: "high" },
});

function render(
  selfHostedEnabled: boolean,
  preselectedAgentId?: string,
  openAIHostedEnabled = false,
  initialAdvancedOpen = false,
) {
  return renderToStaticMarkup(
    <SessionStartDialog
      agents={[compatible, blocked]}
      initialAdvancedOpen={initialAdvancedOpen}
      open
      preselectedAgentId={preselectedAgentId}
      selfHostedEnabled={selfHostedEnabled}
      openAIHostedEnabled={openAIHostedEnabled}
      onClose={() => undefined}
      onSubmit={async () => undefined}
    />,
  );
}

describe("SessionStartDialog", () => {
  it("does not mount dialog content while closed", () => {
    const html = renderToStaticMarkup(
      <SessionStartDialog
        agents={[compatible]}
        open={false}
        selfHostedEnabled
        onClose={() => undefined}
        onSubmit={async () => undefined}
      />,
    );
    expect(html).toBe("");
  });

  it("keeps self-hosted creation hidden by default and selects environment:none", () => {
    const html = render(false);
    expect(html).toContain("Create a Session");
    expect(html).toContain("Execution environment");
    expect(html).toContain("No environment");
    expect(html).toContain("A nonblank message starts the first Turn");
    expect(html).toMatch(/<input[^>]+checked=""[^>]+value="none"/);
    expect(html).not.toContain("Self-hosted");
    expect(html).not.toContain("Workspace directory");
    expect(html).not.toContain("Environment key</");
  });

  it("offers the operator-gated self-hosted choice without collecting a key", () => {
    const html = render(true);
    expect(html).toContain('value="self_hosted"');
    expect(html).toContain("operator-managed Linux executor");
    expect(html).not.toContain('name="environment_key"');
    expect(html).not.toContain('type="password"');
    expect(html).not.toContain("executor_token");
  });

  it("keeps managed hosted default-off and exposes only the reviewed label when enabled", () => {
    expect(render(false)).not.toContain('value="openai_hosted"');
    const html = render(false, undefined, true);
    expect(html).toContain('value="openai_hosted"');
    expect(html).toContain("Managed hosted");
    expect(html).toContain("operator-qualified Core");
    expect(html).not.toContain("Network access");
    expect(html).not.toContain("template_id");
  });

  it("requires a first message for the default no-Environment selection", () => {
    const html = render(false, compatible.id);
    expect(html).toContain("Required without an Environment.");
    expect(html).toContain('aria-required="true"');
    expect(html).toMatch(/<button[^>]+disabled=""[^>]*>Create Session<\/button>/u);
  });

  it("honors a compatible preselected Agent and exposes whole-field overrides", () => {
    const html = render(true, compatible.id, false, true);
    expect(html).toContain('<option value="agent_compatible" selected="">Compatible Agent');
    expect(html).toContain('<option value="agent_blocked">Blocked Agent');
    expect(html).toContain("Configure Session-only overrides");
    expect(html).toContain("Create Session");
  });

  it("keeps a blocked preselection available so Session overrides can repair it", () => {
    const html = render(true, blocked.id);
    expect(html).toContain('<option value="agent_blocked" selected="">Blocked Agent');
    expect(html).toContain("explicit reasoning options are saved-only");
  });

  it("keeps the common path visible and advanced controls collapsed by default", () => {
    const html = render(false);
    expect(html).toContain(">Title</span>");
    expect(html).toMatch(/<input[^>]+maxLength="512"/u);
    expect(html).toContain(">First message</span>");
    expect(html).toContain("Execution environment");
    expect(html).toContain("Advanced settings");
    expect(html).toContain('aria-expanded="false"');
    expect(html).not.toContain("Additional metadata");
    expect(html).not.toContain("Message array");
    expect(html).not.toContain("Stream idle creation events");
  });

  it("renders structured input and source selection without expert-only metadata or idle streaming controls", () => {
    const html = render(false, undefined, false, true);
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain("Initial input");
    expect(html).toContain("Message array");
    expect(html).toContain("Agent source");
    expect(html).not.toContain("Additional metadata");
    expect(html).not.toContain("Stream idle creation events");
    expect(html).not.toContain("Creation events stream automatically");
  });

  it("offers standalone inline Agent creation even when no saved Agents exist", () => {
    const html = renderToStaticMarkup(
      <SessionStartDialog
        agents={[]}
        initialAdvancedOpen
        open
        selfHostedEnabled={false}
        onClose={() => undefined}
        onSubmit={async () => undefined}
      />,
    );
    expect(html).toMatch(/<input[^>]+checked=""[^>]+value="inline"/);
    expect(html).toContain("Inline model");
    expect(html).toContain("no <code>agent_id</code> is sent");
    expect(html).toContain("Inline tools");
  });

  it("does not show Vault controls for an Agent without HTTP MCP tools", () => {
    const html = render(false, compatible.id, false, true);
    expect(html).not.toContain("session-vault-plan");
    expect(html).not.toContain("Anonymous for this Session");
  });

  it("locks the owning Vault for an explicit exact-URL MCP Credential", () => {
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const credentialId = "22222222-2222-4222-8222-222222222222";
    const serverUrl = "https://mcp.example/tools";
    const catalog: VaultCatalog = {
      vaults: [{ id: vaultId, object: "vault", created_at: 1, name: "Runtime", metadata: {} }],
      credentials: [{
        id: credentialId,
        vault_id: vaultId,
        object: "vault.credential",
        name: "Runtime MCP",
        auth: { type: "static_bearer", mcp_server_url: serverUrl },
        created_at: 1,
        updated_at: 1,
      }],
    };
    const credentialed = agent("agent_credentialed", "Credentialed", {
      tools: [{
        type: "mcp",
        server_label: "docs",
        transport: { type: "http", server_url: serverUrl },
        connection_origin: "service",
        credential_id: credentialId,
      }],
    });
    const html = renderToStaticMarkup(
      <SessionStartDialog
        agents={[credentialed]}
        initialAdvancedOpen
        open
        selfHostedEnabled={false}
        vaultCatalog={catalog}
        onClose={() => undefined}
        onSubmit={async () => undefined}
      />,
    );
    expect(html).toContain("Runtime · attached automatically");
    expect(html).toContain('<input type="checkbox" disabled="" checked=""/>');
    expect(html).toContain("Explicit · Runtime MCP · Runtime");
  });

  it("shows only Core request errors and hides arbitrary exception content", () => {
    expect(safeSessionStartError(new AgentCoreError("Workspace admission is unavailable.", 503)))
      .toBe("Workspace admission is unavailable.");
    expect(safeSessionStartError(new Error("Authorization: Bearer private-value")))
      .toBe(genericSessionStartError);
    expect(safeSessionStartError({ message: "private-value" })).toBe(genericSessionStartError);
  });
});
