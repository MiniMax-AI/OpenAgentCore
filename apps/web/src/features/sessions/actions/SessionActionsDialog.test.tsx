import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { AgentSession } from "@agents-core-web/agents-client";

import {
  SessionDeleteConfirmation,
  SessionDetails,
  SessionMetadataForm,
} from "./SessionActionsDialog";

const session: AgentSession = {
  id: "session_1",
  object: "agent.session",
  agent: {
    id: "agent_1",
    model: "provider/model",
    name: "Builder",
    instructions: null,
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [],
  },
  environment: {
    type: "self_hosted",
    id: "environment_1",
    remote_url: "https://executor.example.test/connect",
    workspace_directory: "/workspace/project",
    capability_directories: [],
  },
  status: "requires_action",
  error: null,
  metadata: { title: "Release review", team: "web" },
  required_actions: [],
  vault_ids: [],
  usage: null,
  created_at: 1_700_000_000,
  last_active_at: 1_700_000_100,
};

describe("Session actions dialog content", () => {
  it("renders complete durable details without editing controls", () => {
    const html = renderToStaticMarkup(<SessionDetails session={session} />);
    expect(html).toContain("session_1");
    expect(html).toContain("requires action");
    expect(html).toContain("Release review");
    expect(html).toContain("Never store credentials");
    expect(html).not.toMatch(/<(input|textarea|select)/);
  });

  it("renders accessible title and arbitrary string metadata controls with a secrets warning", () => {
    const html = renderToStaticMarkup(
      <SessionMetadataForm formId="edit-session" session={session} onSubmit={() => undefined} />,
    );
    expect(html).toContain('<form class="form-stack" id="edit-session"');
    expect(html).toContain(">Title</span>");
    expect(html).toContain("Additional metadata");
    expect(html).toContain('&quot;team&quot;: &quot;web&quot;');
    expect(html).not.toContain('&quot;title&quot;:');
    expect(html).toContain("Never store credentials");
  });

  it("states confirmation, no-retry, lifecycle, erasure, and Workspace boundaries", () => {
    const html = renderToStaticMarkup(<SessionDeleteConfirmation session={session} />);
    expect(html).toContain("Delete <strong>Release review</strong> from Agent Core?");
    expect(html).toContain("Exact Session: <code>session_1</code>");
    expect(html).toContain("only after Core confirms success");
    expect(html).toContain("never retried automatically");
    expect(html).toContain("server lifecycle semantics");
    expect(html).toContain("not a promise of physical history erasure");
    expect(html).toContain("Workspace files");
    expect(html).not.toContain("Cancel work and delete");
  });

  it("explains cancel-then-delete only after Core reports a busy Session", () => {
    const html = renderToStaticMarkup(<SessionDeleteConfirmation session={session} busy />);
    expect(html).toContain("Cancel work and delete sends one cancellation");
    expect(html).toContain("waits until Core reports the Session idle or failed");
    expect(html).toContain("then sends one deletion");
    expect(html).toContain("cannot be cancelled");
  });
});
