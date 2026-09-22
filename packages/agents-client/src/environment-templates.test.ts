import { describe, expect, it } from "vitest";

import { AgentCoreError, OpenAIAgentsClient } from "./client";

interface FetchCall {
  input: RequestInfo | URL;
  init?: RequestInit;
}

const templateId = "3f9c1d2e-4b5a-4c7d-8e9f-0a1b2c3d4e5f";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function recordingClient(response: Response): { client: OpenAIAgentsClient; calls: FetchCall[] } {
  const calls: FetchCall[] = [];
  const client = new OpenAIAgentsClient({
    token: "test-token",
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init });
      return response;
    }) as typeof fetch,
  });
  return { client, calls };
}

function template(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: templateId,
    object: "agent.environment.template",
    name: "Restricted outbound access",
    network: { access: "disabled", allowed_domains: [] },
    capability_directories: [],
    packages: { npm: [], python: [], system: [] },
    files: [],
    plugins: [],
    skills: [],
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
    ...overrides,
  };
}

describe("Environment Template resource", () => {
  it("sends the pinned beta collection request and projects safe metadata", async () => {
    const { client, calls } = recordingClient(jsonResponse({
      object: "list",
      data: [template()],
      has_more: false,
      first_id: templateId,
      last_id: templateId,
    }));

    const page = await client.listEnvironmentTemplates({ limit: 20, order: "desc" });

    expect(String(calls[0]?.input)).toBe("/v1/agents/environments/templates?limit=20&order=desc");
    expect(new Headers(calls[0]?.init?.headers).get("OpenAI-Beta")).toBe("agents=v1");
    expect(page.data).toEqual([{
      id: templateId,
      object: "agent.environment.template",
      name: "Restricted outbound access",
      network: { access: "disabled", allowed_domains: [] },
      capability_directories: [],
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
      created_at: 1_700_000_000,
      updated_at: 1_700_000_001,
    }]);
  });

  it("rejects a list page that exceeds the requested limit", async () => {
    const { client } = recordingClient(jsonResponse({
      object: "list",
      data: [template(), template({ id: "4f9c1d2e-4b5a-4c7d-8e9f-0a1b2c3d4e5f" })],
      has_more: false,
      first_id: templateId,
      last_id: "4f9c1d2e-4b5a-4c7d-8e9f-0a1b2c3d4e5f",
    }));

    await expect(client.listEnvironmentTemplates({ limit: 1 })).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("creates a Template with the supported fields only", async () => {
    const { client, calls } = recordingClient(jsonResponse(template(), 201));

    const created = await client.createEnvironmentTemplate({
      name: "Restricted outbound access",
      network: { access: "disabled" },
    });

    expect(calls[0]?.init?.method).toBe("POST");
    expect(calls[0]?.init?.body).toBe(JSON.stringify({
      name: "Restricted outbound access",
      network: { access: "disabled" },
    }));
    expect(created.id).toBe(templateId);
  });

  it("rejects unsupported installation fields before any request", async () => {
    const { client, calls } = recordingClient(jsonResponse(template()));

    await expect(client.createEnvironmentTemplate({
      name: "Populated",
      // Populated initialization is not part of the qualified profile.
      setup_commands: ["npm install"],
    } as never)).rejects.toBeInstanceOf(TypeError);
    expect(calls).toHaveLength(0);
  });

  it("rejects a created Template whose configuration differs from the request", async () => {
    const { client } = recordingClient(jsonResponse(template({ network: { access: "enabled", allowed_domains: [] } }), 201));

    await expect(client.createEnvironmentTemplate({ network: { access: "disabled" } }))
      .rejects.toBeInstanceOf(AgentCoreError);
  });

  it("rejects restricted network access in a response", async () => {
    const { client } = recordingClient(jsonResponse(template({ network: { access: "restricted", allowed_domains: [] } })));

    await expect(client.retrieveEnvironmentTemplate(templateId)).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("rejects a response carrying confidential fields", async () => {
    const { client } = recordingClient(jsonResponse(template({ env: { TOKEN: "leaked" } })));

    await expect(client.retrieveEnvironmentTemplate(templateId)).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("rejects a retrieved Template with a different identity", async () => {
    const { client } = recordingClient(jsonResponse(template()));

    await expect(client.retrieveEnvironmentTemplate("4f9c1d2e-4b5a-4c7d-8e9f-0a1b2c3d4e5f"))
      .rejects.toBeInstanceOf(AgentCoreError);
  });

  it("keeps an update replacement check on supplied fields only", async () => {
    const { client, calls } = recordingClient(jsonResponse(template({ name: null })));

    const updated = await client.updateEnvironmentTemplate(templateId, { name: null });

    expect(calls[0]?.init?.body).toBe(JSON.stringify({ name: null }));
    expect(updated.name).toBeNull();
    expect(updated.network.access).toBe("disabled");
  });

  it("projects an exact deletion receipt", async () => {
    const { client, calls } = recordingClient(jsonResponse({
      id: templateId,
      object: "agent.environment.template.deleted",
      deleted: true,
    }));

    await expect(client.deleteEnvironmentTemplate(templateId)).resolves.toEqual({
      id: templateId,
      object: "agent.environment.template.deleted",
      deleted: true,
    });
    expect(calls[0]?.init?.method).toBe("DELETE");
  });
});

describe("Session creation with a referenced Template", () => {
  function sessionResponse(access: "enabled" | "disabled"): Record<string, unknown> {
    return {
      id: "5f9c1d2e-4b5a-4c7d-8e9f-0a1b2c3d4e5f",
      object: "agent.session",
      agent: {
        id: "agent",
        model: "provider/model",
        name: null,
        instructions: null,
        multi_agent: { enabled: false, max_concurrent_subagents: null },
        reasoning: {},
        service_tier: "auto",
        text: { format: { type: "text" }, verbosity: "medium" },
        tools: [],
      },
      environment: {
        type: "openai_hosted",
        id: "6f9c1d2e-4b5a-4c7d-8e9f-0a1b2c3d4e5f",
        capability_directories: [],
        network: { access, allowed_domains: [] },
        packages: { npm: [], python: [], system: [] },
        files: [],
        plugins: [],
        skills: [],
      },
      status: "idle",
      error: null,
      metadata: {},
      required_actions: [],
      vault_ids: [],
      usage: null,
      created_at: 1_700_000_000,
      last_active_at: 1_700_000_000,
    };
  }

  it("accepts an inherited Template network policy that Web never guessed", async () => {
    const { client, calls } = recordingClient(jsonResponse(sessionResponse("disabled")));

    const session = await client.createSession({
      agent: { model: "provider/model" },
      environment: { type: "openai_hosted", environment_template_id: templateId },
    });

    expect(JSON.parse(String(calls[0]?.init?.body))).toMatchObject({
      environment: { type: "openai_hosted", environment_template_id: templateId },
    });
    expect(session.environment).toMatchObject({ type: "openai_hosted", network: { access: "disabled" } });
  });

  it("still binds an explicitly requested network policy", async () => {
    const { client } = recordingClient(jsonResponse(sessionResponse("enabled")));

    await expect(client.createSession({
      agent: { model: "provider/model" },
      environment: {
        type: "openai_hosted",
        environment_template_id: templateId,
        network: { access: "disabled" },
      },
    })).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("refuses an explicit null network beside a reference", async () => {
    const { client, calls } = recordingClient(jsonResponse(sessionResponse("enabled")));

    await expect(client.createSession({
      agent: { model: "provider/model" },
      environment: { type: "openai_hosted", environment_template_id: templateId, network: null },
    })).rejects.toBeInstanceOf(TypeError);
    expect(calls).toHaveLength(0);
  });

  it("refuses a malformed Template reference", async () => {
    const { client, calls } = recordingClient(jsonResponse(sessionResponse("enabled")));

    await expect(client.createSession({
      agent: { model: "provider/model" },
      environment: { type: "openai_hosted", environment_template_id: "template-1" },
    })).rejects.toBeInstanceOf(TypeError);
    expect(calls).toHaveLength(0);
  });
});
