import { describe, expect, it } from "vitest";

import { AgentCoreError, OpenAIAgentsClient } from "./client";
import { isRecognizedEnvironmentTemplate } from "./environment-template-projection";
import { isOpenAIHostedSessionEnvironment } from "./session-environment-projection";
import templates from "./fixtures/parsar-d3f55046/environment-templates.json";

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

  it("marks restricted access without domains as unrecognized instead of guessing", async () => {
    const { client } = recordingClient(jsonResponse(template({ network: { access: "restricted", allowed_domains: [] } })));

    const retrieved = await client.retrieveEnvironmentTemplate(templateId);

    expect(retrieved.unrecognized).toEqual(["network"]);
    expect(retrieved.network).toBeUndefined();
  });

  it("never projects a confidential field, and marks only that Template", async () => {
    const { client } = recordingClient(jsonResponse(template({ env: { TOKEN: "leaked" }, setup_commands: ["private-command"] })));

    const retrieved = await client.retrieveEnvironmentTemplate(templateId);

    expect(retrieved.unrecognized).toEqual(["env", "setup_commands"]);
    expect(JSON.stringify(retrieved)).not.toContain("leaked");
    expect(JSON.stringify(retrieved)).not.toContain("private-command");
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
    expect(updated.network?.access).toBe("disabled");
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

const advanced = templates.responses.template_advanced.body;
const unrecognized = templates.responses.template_unrecognized.body;

describe("Environment Template full configuration", () => {
  it("projects every section of an advanced Template exactly", async () => {
    const { client, calls } = recordingClient(jsonResponse(advanced));

    const retrieved = await client.retrieveEnvironmentTemplate(advanced.id);

    expect(new Headers(calls[0]?.init?.headers).get("OpenAI-Beta")).toBe("agents=v1");
    expect(isRecognizedEnvironmentTemplate(retrieved)).toBe(true);
    expect(retrieved).toEqual(advanced);
    expect(retrieved.network).toEqual({ access: "restricted", allowed_domains: ["pypi.org", "files.pythonhosted.org"] });
    expect(retrieved.files).toEqual([
      { type: "inline", path: "/workspace/config/settings.json", size_bytes: 128 },
      { type: "file_id", path: "/workspace/data/input.csv", file_id: "file-2b7c9d10-4e5f-4a6b-8c7d-9e0f1a2b3c4d" },
    ]);
    expect(retrieved.skills?.map((skill) => skill.type === "skill_reference" ? skill.version : skill.name))
      .toEqual([null, "latest", "2", "triage"]);
  });

  it("marks only the Template with an unrecognized entry in a list", async () => {
    const { client } = recordingClient(jsonResponse(templates.responses.template_list.body));

    const page = await client.listEnvironmentTemplates({ limit: 100, order: "desc" });

    expect(page.data.map((entry) => entry.unrecognized ?? [])).toEqual([[], ["files"], []]);
    const [first, marked, basic] = page.data;
    expect(first).toEqual(advanced);
    expect(basic?.name).toBeNull();
    // Recognized sections of the marked Template stay visible; the unknown one is omitted.
    expect(marked).toMatchObject({ id: unrecognized.id, name: "Future profile", skills: unrecognized.skills });
    expect(marked?.files).toBeUndefined();
    expect(JSON.stringify(marked)).not.toContain("archive_0f1e2d3c");
  });

  it.each([
    ["network", { network: { access: "proxy", allowed_domains: [] } }],
    ["network", { network: { access: "enabled", allowed_domains: ["example.com"] } }],
    ["packages", { packages: { npm: [], python: [], system: [], cargo: [] } }],
    ["capability_directories", { capability_directories: ["/etc"] }],
    ["files", { files: [{ type: "inline", path: "/workspace/a", size_bytes: 1, data: "YQ==" }] }],
    ["files", { files: [{ type: "file_id", path: "relative", file_id: "file-1" }] }],
    ["skills", { skills: [{ type: "skill_reference", skill_id: "skill_1", version: "v2" }] }],
    ["skills", { skills: [{ type: "skill_reference", skill_id: "skill_1", version: null, name: "resolved" }] }],
    ["skills", { skills: [{ type: "anthropic", name: "a", description: "b" }] }],
    ["plugins", { plugins: [{ type: "inline", name: "", description: "b" }] }],
  ])("marks a malformed %s section without rejecting the Template", async (section, overrides) => {
    const { client } = recordingClient(jsonResponse(template(overrides)));

    const retrieved = await client.retrieveEnvironmentTemplate(templateId);

    expect(retrieved.unrecognized).toEqual([section]);
    expect(retrieved[section as "network"]).toBeUndefined();
    expect(retrieved.name).toBe("Restricted outbound access");
  });

  it("marks a missing section as unrecognized", async () => {
    const value = template();
    delete value.plugins;
    const { client } = recordingClient(jsonResponse(value));

    await expect(client.retrieveEnvironmentTemplate(templateId)).resolves.toMatchObject({ unrecognized: ["plugins"] });
  });

  it("still rejects a list entry without a valid identity", async () => {
    const { client } = recordingClient(jsonResponse({
      object: "list",
      data: [template({ id: "not-a-template" })],
      has_more: false,
      first_id: "not-a-template",
      last_id: "not-a-template",
    }));

    await expect(client.listEnvironmentTemplates()).rejects.toMatchObject({ status: 502, code: "invalid_environment_template" });
  });

  it("renames an advanced Template with a name-only body and keeps its configuration", async () => {
    const response = templates.responses.template_renamed;
    const { client, calls } = recordingClient(jsonResponse(response.body));

    const updated = await client.updateEnvironmentTemplate(advanced.id, { name: "Report builder v2" });

    expect(calls[0]?.init?.method).toBe("POST");
    expect(String(calls[0]?.input)).toBe(`/v1${response.request.path.slice(3)}`);
    expect(calls[0]?.init?.body).toBe(JSON.stringify(response.request.body));
    expect(updated).toEqual({ ...advanced, name: "Report builder v2", updated_at: response.body.updated_at });
  });

  it("renames a Template with unrecognized configuration without touching it", async () => {
    const { client, calls } = recordingClient(jsonResponse({ ...unrecognized, name: "Renamed" }));

    const updated = await client.updateEnvironmentTemplate(unrecognized.id, { name: "Renamed" });

    expect(calls[0]?.init?.body).toBe(JSON.stringify({ name: "Renamed" }));
    expect(updated).toMatchObject({ name: "Renamed", unrecognized: ["files"] });
  });

  it("writes restricted access with its domains and binds the stored list exactly", async () => {
    const response = templates.responses.template_restricted_update;
    const { client, calls } = recordingClient(jsonResponse(response.body));

    const updated = await client.updateEnvironmentTemplate(advanced.id, {
      network: { access: "restricted", allowed_domains: ["pypi.org"] },
    });

    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual(response.request.body);
    expect(updated.network).toEqual({ access: "restricted", allowed_domains: ["pypi.org"] });

    const mismatched = recordingClient(jsonResponse(response.body));
    await expect(mismatched.client.updateEnvironmentTemplate(advanced.id, {
      network: { access: "restricted", allowed_domains: ["example.com"] },
    })).rejects.toMatchObject({ code: "invalid_environment_template" });
  });

  it("maps a rejected network write to the Core error", async () => {
    const response = templates.responses.template_invalid_network;
    const { client } = recordingClient(jsonResponse(response.body, response.status));

    await expect(client.createEnvironmentTemplate(response.request.body as never))
      .rejects.toMatchObject({ status: 400, errorType: "invalid_request_error", code: "invalid_request_error", param: null });
  });

  it.each([
    [{ network: { access: "restricted" } }],
    [{ network: { access: "restricted", allowed_domains: "example.com" } }],
    [{ network: { access: "enabled", allowed_domains: [] } }],
    [{ packages: { npm: ["left-pad"] } }],
    [{ files: [] }],
  ])("refuses an unsupported write body %j before any request", async (input) => {
    const { client, calls } = recordingClient(jsonResponse(template()));

    await expect(client.updateEnvironmentTemplate(templateId, input as never)).rejects.toBeInstanceOf(TypeError);
    expect(calls).toHaveLength(0);
  });

  it("requires a fully recognized Template after a basic create", async () => {
    const { client } = recordingClient(jsonResponse({ ...unrecognized, name: "Future profile" }, 201));

    await expect(client.createEnvironmentTemplate({ name: "Future profile", network: { access: "disabled" } }))
      .rejects.toMatchObject({ code: "invalid_environment_template" });
  });
});

describe("Sessions created from an advanced Template", () => {
  const environment = templates.responses.session_environment_from_advanced_template.body;
  const session = {
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
    environment,
    status: "idle",
    error: null,
    metadata: {},
    required_actions: [],
    vault_ids: [],
    usage: null,
    created_at: 1_790_208_600,
    last_active_at: 1_790_208_600,
  };

  it("accepts the frozen installation metadata of a referenced advanced Template", async () => {
    const { client } = recordingClient(jsonResponse(session));

    const created = await client.createSession({
      agent: { model: "provider/model" },
      environment: { type: "openai_hosted", environment_template_id: advanced.id },
    });

    expect(created.environment).toEqual(environment);
  });

  it("keeps a listed advanced Session instead of failing the page", async () => {
    const { client } = recordingClient(jsonResponse({
      object: "list", data: [session], has_more: false, first_id: session.id, last_id: session.id,
    }));

    const page = await client.listSessions();

    expect(page.data[0]?.environment).toEqual(environment);
  });

  it("classifies exactly the managed Session Environment shapes the client projects", async () => {
    const { client } = recordingClient(jsonResponse(session));
    const projected = await client.retrieveSession(session.id);
    const basic = {
      type: "openai_hosted",
      id: environment.id,
      capability_directories: [],
      network: { access: "enabled", allowed_domains: [] },
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
    };

    expect(isOpenAIHostedSessionEnvironment(projected.environment)).toBe(true);
    expect(isOpenAIHostedSessionEnvironment(environment)).toBe(true);
    expect(isOpenAIHostedSessionEnvironment(basic)).toBe(true);
    for (const unknown of [
      { ...environment, environment_template_id: "future" },
      { ...environment, network: { access: "restricted", allowed_domains: [] } },
      { ...environment, network: { access: "enabled", allowed_domains: ["pypi.org"] } },
      { ...environment, network: { access: "future", allowed_domains: [] } },
      { ...environment, packages: { ...environment.packages, cargo: [] } },
      { ...environment, packages: { npm: [], python: [] } },
      { ...environment, capability_directories: [1] },
      { ...environment, files: ["settings.json"] },
      { ...environment, skills: null },
      { ...environment, id: " " },
      { type: "openai_hosted", id: environment.id },
      { ...basic, type: "self_hosted" },
      null,
    ]) expect(isOpenAIHostedSessionEnvironment(unknown)).toBe(false);
  });

  it("still rejects a restricted Session network without domains", async () => {
    const { client } = recordingClient(jsonResponse({
      ...session,
      environment: { ...environment, network: { access: "restricted", allowed_domains: [] } },
    }));

    await expect(client.retrieveSession(session.id)).rejects.toMatchObject({ code: "invalid_session_resource" });
  });
});
