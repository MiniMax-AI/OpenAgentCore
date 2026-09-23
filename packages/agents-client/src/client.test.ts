import { afterEach, describe, expect, it, vi } from "vitest";

import { AgentCoreError, CreationStreamRetryError, createIdempotencyKey, isSessionDeletionConflict, OpenAIAgentsClient } from "./client";
import hostedDadf64 from "./fixtures/parsar-dadf64a7/openai-hosted.json";
import eventBatchDadf64 from "./fixtures/parsar-dadf64a7/session-event-batch.json";
import type {
  AgentEnvironmentInput,
  EnvironmentFileListOptions,
  EnvironmentResourceStatus,
  SessionEvent,
  SessionInputEvent,
} from "./types";

interface FetchCall {
  input: RequestInfo | URL;
  init?: RequestInit;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function recordingFetch(response: Response, calls: FetchCall[]): typeof fetch {
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ input, init });
    return response;
  }) as typeof fetch;
}

function streamResponse(chunks: string[], status = 200): Response {
  const encoder = new TextEncoder();
  return new Response(new ReadableStream<Uint8Array>({
    start(controller) {
      chunks.forEach((chunk) => controller.enqueue(encoder.encode(chunk)));
      controller.close();
    },
  }), { status, headers: { "Content-Type": "text/event-stream" } });
}

function ephemeralBearer(): string {
  return globalThis.crypto.randomUUID().replaceAll("-", "");
}

function agentSnapshot(): Record<string, unknown> {
  return {
    id: "agent",
    model: "provider/model",
    name: null,
    instructions: null,
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [],
  };
}

function sessionResource(vaultIds: unknown = []): Record<string, unknown> {
  return {
    id: "session",
    object: "agent.session",
    agent: agentSnapshot(),
    environment: { type: "none" },
    status: "idle",
    error: null,
    metadata: {},
    required_actions: [],
    vault_ids: vaultIds,
    usage: null,
    created_at: 1,
    last_active_at: 1,
  };
}

function turnResource(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: "turn_1",
    agent_id: "agent",
    session_id: "session",
    object: "agent.session.turn",
    status: "queued",
    created_at: 1,
    started_at: null,
    completed_at: null,
    error: null,
    usage: null,
    ...overrides,
  };
}

function messageItem(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: "item_1",
    turn_id: "turn_1",
    type: "message",
    status: "in_progress",
    role: "assistant",
    content: [{ type: "output_text", text: "" }],
    ...overrides,
  };
}

const runtimeSessionId = "11111111-1111-4111-8111-111111111111";
const runtimeEnvironmentId = "22222222-2222-4222-8222-222222222222";
const runtimeAllocationId = "33333333-3333-4333-8333-333333333333";
const runtimeDeviceId = "44444444-4444-4444-8444-444444444444";

function runtimeObservation(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: runtimeSessionId,
    object: "agent.runtime_observation",
    session_id: runtimeSessionId,
    environment_id: runtimeEnvironmentId,
    mode: "openai_hosted",
    provider_type: "docker",
    instance: {
      kind: "managed_allocation",
      allocation_id: runtimeAllocationId,
      device_id: runtimeDeviceId,
      connection_generation: null,
    },
    status: "observed",
    reason: null,
    allocation_created_at: 10,
    resolved_at: 30,
    observed_at: 20,
    started_at: 10,
    cpu: {
      usage_seconds_total: 0,
      capacity_cores: 2,
      usage_cores: null,
      utilization_ratio: null,
    },
    memory: { usage_bytes: 0, limit_bytes: 1024 },
    ...overrides,
  };
}

describe("OpenAIAgentsClient", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("binds the host fetch implementation before storing it", async () => {
    let receiver: unknown;
    vi.stubGlobal("fetch", (function (this: unknown) {
      receiver = this;
      return Promise.resolve(jsonResponse({ data: [], has_more: false }));
    }) as typeof fetch);

    const client = new OpenAIAgentsClient();
    await client.listAgents();

    expect(receiver).toBe(globalThis);
  });

  it("builds encoded list queries and required beta/auth headers", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      token: () => "tenant-key",
      fetch: recordingFetch(jsonResponse({ data: [], has_more: false }), calls),
    });

    await client.listSessions({ after: "sess/one", limit: 10, order: "asc", agentId: "agent one" });

    expect(String(calls[0]?.input)).toBe(
      "https://core.example/v1/agents/sessions?after=sess%2Fone&limit=10&order=asc&agent_id=agent+one",
    );
    const headers = new Headers(calls[0]?.init?.headers);
    expect(headers.get("Accept")).toBe("application/json");
    expect(headers.get("Authorization")).toBe("Bearer tenant-key");
    expect(headers.get("OpenAI-Beta")).toBe("agents=v1");
  });

  it("sends exact encoded Session update and delete requests without idempotency or delete body", async () => {
    const calls: FetchCall[] = [];
    const session = {
      id: "session/one",
      object: "agent.session",
      agent: agentSnapshot(),
      environment: { type: "none" },
      status: "idle",
      error: null,
      metadata: { title: "Renamed", team: "web" },
      required_actions: [],
      vault_ids: [],
      usage: null,
      created_at: 1,
      last_active_at: 1,
    };
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      token: "tenant-key",
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        return init?.method === "DELETE"
          ? jsonResponse({ id: "session/one", object: "agent.session.deleted", deleted: true })
          : jsonResponse(session);
      }) as typeof fetch,
    });

    await client.updateSession("session/one", session.metadata);
    await client.deleteSession("session/one");

    expect(calls).toHaveLength(2);
    expect(String(calls[0]?.input)).toBe("https://core.example/v1/agents/sessions/session%2Fone");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({ metadata: session.metadata });
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBeNull();
    expect(String(calls[1]?.input)).toBe("https://core.example/v1/agents/sessions/session%2Fone");
    expect(calls[1]?.init?.method).toBe("DELETE");
    expect(calls[1]?.init?.body).toBeUndefined();
    for (const call of calls) {
      const headers = new Headers(call.init?.headers);
      expect(headers.get("Authorization")).toBe("Bearer tenant-key");
      expect(headers.get("OpenAI-Beta")).toBe("agents=v1");
    }
  });

  it("surfaces the busy-Session deletion conflict with its official fields", async () => {
    const calls: FetchCall[] = [];
    const message = "session must be durably idle or failed without required actions before deletion";
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1",
      token: "tenant-key",
      fetch: recordingFetch(jsonResponse({
        error: { type: "conflict_error", code: "conflict_error", message, param: null },
      }, 409), calls),
    });

    const error = await client.deleteSession("session_busy").catch((value: unknown) => value);
    expect(error).toBeInstanceOf(AgentCoreError);
    expect(error).toMatchObject({ status: 409, code: "conflict_error", errorType: "conflict_error", param: null, message });
    expect(isSessionDeletionConflict(error)).toBe(true);
    expect(calls).toHaveLength(1);
    expect(calls[0]?.init?.method).toBe("DELETE");

    expect(isSessionDeletionConflict(new AgentCoreError("Resource not found.", 404, "not_found_error"))).toBe(false);
    expect(isSessionDeletionConflict(new AgentCoreError("Other conflict.", 409, "turn_conflict"))).toBe(false);
    expect(isSessionDeletionConflict(new CreationStreamRetryError())).toBe(false);
    expect(isSessionDeletionConflict(new Error(message))).toBe(false);
  });

  it("preserves the event-stream Accept header and decodes streamed events", async () => {
    const calls: FetchCall[] = [];
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode(": connected\n\nevent: agent.session.turn.output_text.delt"));
        controller.enqueue(
          encoder.encode(
            'a\ndata: {"type":"agent.session.turn.output_text.delta","event_id":"evt_1","session_id":"session/one","turn_id":"turn_1","item_id":"item_1","output_index":0,"content_index":0,"delta":"hello"}\n\n',
          ),
        );
        controller.close();
      },
    });
    const events: SessionEvent[] = [];
    const lifecycle: string[] = [];
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1",
      fetch: recordingFetch(new Response(body, { headers: { "Content-Type": "text/event-stream" } }), calls),
    });

    await client.streamEvents("session/one", {
      onOpen: () => lifecycle.push("open"),
      onEvent: (event) => {
        lifecycle.push("event");
        events.push(event);
      },
    });

    expect(String(calls[0]?.input)).toBe("https://core.example/v1/agents/sessions/session%2Fone/events");
    const headers = new Headers(calls[0]?.init?.headers);
    expect(headers.get("Accept")).toBe("text/event-stream");
    expect(headers.get("OpenAI-Beta")).toBe("agents=v1");
    expect(lifecycle).toEqual(["open", "event"]);
    expect(events).toEqual([
      {
        type: "agent.session.turn.output_text.delta",
        event_id: "evt_1",
        session_id: "session/one",
        turn_id: "turn_1",
        item_id: "item_1",
        output_index: 0,
        content_index: 0,
        delta: "hello",
      },
    ]);
  });

  it("turns an in-band stream error into a retryable failure without forwarding it as data", async () => {
    let cancelled = false;
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(
          'event: error\ndata: {"type":"error","event_id":"evt_error","session_id":"session","error":{"code":"stream_interrupted","type":"server_error","message":"safe"}}\n\n',
        ));
      },
      cancel() {
        cancelled = true;
      },
    });
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(body), []) });

    await expect(client.streamEvents("session", { onEvent })).rejects.toMatchObject({
      status: 503,
      code: "stream_interrupted",
      errorType: "server_error",
      message: "Agent Core interrupted the live event stream. Reconnect and retrieve durable state.",
    });
    expect(onEvent).not.toHaveBeenCalled();
    expect(cancelled).toBe(true);
  });

  it("creates a Session through chunked SSE and publishes its validated leading snapshot exactly once", async () => {
    const calls: FetchCall[] = [];
    const controller = new AbortController();
    const session = { ...sessionResource(), id: "session/created" };
    const created = JSON.stringify({
      type: "agent.session.created",
      event_id: "evt_created",
      session_id: "session/created",
      session,
    });
    const later = JSON.stringify({
      type: "agent.session.future_event",
      event_id: "evt_later",
      session_id: "session/created",
      delta: "preserved",
    });
    const lifecycle: string[] = [];
    const onSession = vi.fn(() => lifecycle.push("session"));
    const onEvent = vi.fn((event: SessionEvent) => lifecycle.push(`event:${event.type}`));
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      token: "tenant-key",
      fetch: recordingFetch(streamResponse([
        ": connected\n\nevent: agent.session.cre",
        `ated\ndata: ${created}\n\nevent: agent.session.future_event\ndata: ${later}\n\ndata: [DONE]\n\n`,
      ], 201), calls),
    });

    await client.createSessionStream(
      { environment: { type: "none" }, metadata: { source: "web" } },
      "create-key",
      {
        signal: controller.signal,
        onOpen: () => lifecycle.push("open"),
        onSession,
        onEvent,
      },
    );

    expect(calls).toHaveLength(1);
    expect(String(calls[0]?.input)).toBe("https://core.example/v1/agents/sessions");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(calls[0]?.init?.signal).toBe(controller.signal);
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({
      environment: { type: "none" },
      metadata: { source: "web" },
      stream: true,
    });
    const headers = new Headers(calls[0]?.init?.headers);
    expect(headers.get("Accept")).toBe("text/event-stream");
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(headers.get("Idempotency-Key")).toBe("create-key");
    expect(headers.get("Authorization")).toBe("Bearer tenant-key");
    expect(headers.get("OpenAI-Beta")).toBe("agents=v1");
    expect(onSession).toHaveBeenCalledTimes(1);
    expect(onSession).toHaveBeenCalledWith(expect.objectContaining({ id: "session/created" }));
    expect(onEvent).toHaveBeenCalledTimes(2);
    expect(onEvent.mock.calls[0]?.[0]).toMatchObject({
      type: "agent.session.created",
      session: { id: "session/created" },
    });
    expect(onEvent.mock.calls[1]?.[0]).toEqual({
      type: "agent.session.future_event",
      event_id: "evt_later",
      session_id: "session/created",
    });
    expect(lifecycle).toEqual([
      "open",
      "session",
      "event:agent.session.created",
      "event:agent.session.future_event",
    ]);
  });

  it("completes a creation stream that ends right after its initial Turn settles", async () => {
    const admitted = { ...sessionResource(), status: "in_progress", last_active_at: 2 };
    const cancelled = turnResource({ status: "cancelled", completed_at: 3 });
    const frames = [
      { type: "agent.session.created", event_id: "created", session: admitted },
      { type: "agent.session.turn.created", event_id: "turn", session_id: "session", turn_id: "turn_1", turn: turnResource() },
      {
        type: "agent.session.turn.item.added", event_id: "input", session_id: "session", turn_id: "turn_1",
        item: messageItem({ status: "completed", role: "user", content: [{ type: "input_text", text: "First" }] }),
      },
      { type: "agent.session.in_progress", event_id: "progress", session: admitted },
      { type: "agent.session.turn.cancelled", event_id: "done", session_id: "session", turn_id: "turn_1", turn: cancelled, usage: null },
      { type: "agent.session.idle", event_id: "idle", session: { ...sessionResource(), last_active_at: 3 } },
    ];
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([
        ": connected\n\n",
        ...frames.map((frame) => `event: ${frame.type}\ndata: ${JSON.stringify(frame)}\n\n`),
      ], 201), []),
    });

    await client.createSessionStream({ environment: { type: "none" }, input: "First" }, "create-key", { onSession, onEvent });

    expect(onSession).toHaveBeenCalledWith(expect.objectContaining({ id: "session", status: "in_progress" }));
    expect(onEvent.mock.calls.map((call) => call[0]?.type)).toEqual(frames.map((frame) => frame.type));
    expect(onEvent.mock.calls[4]?.[0]).toMatchObject({ usage: null, turn: { status: "cancelled", usage: null } });
  });

  it.each([
    ["default", { type: "openai_hosted" }],
    ["enabled", { type: "openai_hosted", network: { access: "enabled" } }],
    ["disabled", { type: "openai_hosted", network: { access: "disabled" } }],
  ] as Array<[string, AgentEnvironmentInput]>)
  ("sends the dadf64a7 managed Environment %s network input and validates its exact output", async (_label, environment) => {
    const calls: FetchCall[] = [];
    const output = {
      ...sessionResource(),
      environment: {
        ...hostedDadf64.session_environment,
        network: {
          ...hostedDadf64.session_environment.network,
          access: environment.type === "openai_hosted" ? environment.network?.access ?? "enabled" : "enabled",
        },
      },
    };
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(output), calls) });

    await expect(client.createSession({ environment, stream: false }, "hosted-key"))
      .resolves.toMatchObject({ environment: output.environment });
    expect(JSON.parse(String(calls[0]?.init?.body)).environment).toEqual(environment);
  });

  it.each([
    {
      label: "default hosted changed to disabled",
      input: { type: "openai_hosted" },
      output: { ...hostedDadf64.session_environment, network: { access: "disabled", allowed_domains: [] } },
    },
    {
      label: "disabled hosted changed to enabled",
      input: { type: "openai_hosted", network: { access: "disabled" } },
      output: hostedDadf64.session_environment,
    },
    {
      label: "none changed to self hosted",
      input: { type: "none" },
      output: {
        type: "self_hosted",
        id: "environment",
        remote_url: "https://executor.example.test",
        workspace_directory: "/workspace",
        capability_directories: [],
      },
    },
    {
      label: "self-hosted workspace changed",
      input: { type: "self_hosted", workspace_directory: "/requested", capability_directories: ["/capability"] },
      output: {
        type: "self_hosted",
        id: "environment",
        remote_url: "https://executor.example.test",
        workspace_directory: "/different",
        capability_directories: ["/capability"],
      },
    },
    {
      label: "self-hosted capability order changed",
      input: { type: "self_hosted", workspace_directory: "/workspace", capability_directories: ["/a", "/b"] },
      output: {
        type: "self_hosted",
        id: "environment",
        remote_url: "https://executor.example.test",
        workspace_directory: "/workspace",
        capability_directories: ["/b", "/a"],
      },
    },
  ] as Array<{ label: string; input: AgentEnvironmentInput; output: unknown }>)
  ("rejects a JSON creation response whose Environment differs from $label", async ({ input, output }) => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({ ...sessionResource(), environment: output }), calls),
    });

    await expect(client.createSession({ environment: input }, "environment-binding"))
      .rejects.toMatchObject({ status: 502, code: "invalid_session_resource" });
    expect(calls).toHaveLength(1);
  });

  it("normalizes null self-hosted capabilities and null hosted network before binding output", async () => {
    const responses = [
      {
        ...sessionResource(),
        environment: {
          type: "self_hosted",
          id: "environment",
          remote_url: "https://executor.example.test",
          workspace_directory: "/workspace",
          capability_directories: [],
        },
      },
      { ...sessionResource(), environment: hostedDadf64.session_environment },
    ];
    const client = new OpenAIAgentsClient({
      fetch: (async () => jsonResponse(responses.shift())) as typeof fetch,
    });

    await expect(client.createSession({
      environment: { type: "self_hosted", workspace_directory: "/workspace", capability_directories: null },
    })).resolves.toMatchObject({ environment: { type: "self_hosted", capability_directories: [] } });
    await expect(client.createSession({
      environment: { type: "openai_hosted", network: null },
    })).resolves.toMatchObject({ environment: { type: "openai_hosted", network: { access: "enabled" } } });
  });

  it.each(Object.entries(hostedDadf64.malformed_session_environments))(
    "rejects dadf64a7 managed Environment output variant %s",
    async (_label, environment) => {
      const client = new OpenAIAgentsClient({
        fetch: recordingFetch(jsonResponse({ ...sessionResource(), environment }), []),
      });
      await expect(client.retrieveSession("session"))
        .rejects.toMatchObject({ status: 502, code: "invalid_session_resource" });
    },
  );

  it.each(Object.entries(hostedDadf64.malformed_environment_resources))(
    "rejects dadf64a7 managed Environment resource variant %s",
    async (_label, resource) => {
      const calls: FetchCall[] = [];
      const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(resource), calls) });
      await expect(client.retrieveEnvironment(hostedDadf64.environment_resource.id))
        .rejects.toMatchObject({ status: 502, code: "invalid_environment_resource" });
      expect(calls).toHaveLength(1);
    },
  );

  it("retains the current self-hosted resource collection contract separately", async () => {
    const resource = {
      id: "environment",
      object: "agent.environment",
      type: "self_hosted",
      status: "connected",
      files: [{ path: "/workspace/file" }],
      plugins: [{ name: "plugin" }],
      skills: [{ name: "skill" }],
    };
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(resource), []) });
    await expect(client.retrieveEnvironment("environment")).resolves.toEqual(resource);
  });

  it("rejects a caller connection action on a managed Environment Session", async () => {
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({
        ...sessionResource(),
        environment: hostedDadf64.session_environment,
        required_actions: [{ type: "environment_connection", environment_id: hostedDadf64.session_environment.id }],
      }), []),
    });

    await expect(client.retrieveSession("session"))
      .rejects.toMatchObject({ status: 502, code: "invalid_session_resource" });
  });

  it("binds required actions to requires_action state and the exact self-hosted Environment", async () => {
    const environment = {
      type: "self_hosted",
      id: "environment",
      remote_url: "https://executor.example.test",
      workspace_directory: "/workspace",
      capability_directories: [],
    } as const;
    const connection = { type: "environment_connection", environment_id: environment.id } as const;
    const functionCall = {
      type: "function_call",
      call_id: "call_1",
      turn_id: "turn_1",
      name: "lookup",
      arguments: {},
    } as const;

    const connectionClient = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({
      ...sessionResource(), environment, status: "requires_action", required_actions: [connection],
    }), []) });
    await expect(connectionClient.retrieveSession("session")).resolves.toMatchObject({ required_actions: [connection] });

    const functionClient = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({
      ...sessionResource(), status: "requires_action", required_actions: [functionCall],
    }), []) });
    await expect(functionClient.retrieveSession("session")).resolves.toMatchObject({ required_actions: [functionCall] });

    const malformed = [
      { ...sessionResource(), status: "requires_action", required_actions: [] },
      { ...sessionResource(), status: "idle", required_actions: [functionCall] },
      { ...sessionResource(), status: "requires_action", required_actions: [connection] },
      {
        ...sessionResource(), environment, status: "requires_action",
        required_actions: [{ ...connection, environment_id: "another-environment" }],
      },
      { ...sessionResource(), environment, status: "requires_action", required_actions: [connection, functionCall] },
    ];
    for (const body of malformed) {
      const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(body), []) });
      await expect(client.retrieveSession("session"))
        .rejects.toMatchObject({ status: 502, code: "invalid_session_resource" });
    }
  });

  it("projects an explicit Core harness from an effective Session Agent", async () => {
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({
      ...sessionResource(),
      agent: { ...agentSnapshot(), x_agents_core: { harness: "claude_sdk" } },
    }), []) });

    await expect(client.retrieveSession("session")).resolves.toMatchObject({
      agent: { x_agents_core: { harness: "claude_sdk" } },
    });
  });

  it("preserves a nullable resource error code", async () => {
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({ error: {
        code: null,
        message: "Resource not found.",
        type: "invalid_request_error",
        param: null,
      } }, 404), []),
    });
    await expect(client.retrieveSourceFile("file-123e4567-e89b-42d3-a456-426614174000")).rejects.toMatchObject({
      status: 404, code: null, param: null, errorType: "invalid_request_error",
    });
  });

  it("preserves a pre-stream Session creation API error without opening or retrying", async () => {
    const calls: FetchCall[] = [];
    const onOpen = vi.fn();
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({ error: {
        code: "idempotency_conflict",
        message: "Creation key conflicts with another request.",
        type: "invalid_request_error",
      } }, 409), calls),
    });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onOpen, onSession, onEvent },
    )).rejects.toMatchObject({
      status: 409,
      code: "idempotency_conflict",
      errorType: "invalid_request_error",
    });
    expect(calls).toHaveLength(1);
    expect(onOpen).not.toHaveBeenCalled();
    expect(onSession).not.toHaveBeenCalled();
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("rejects a creation stream whose first data event is not agent.session.created", async () => {
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const response = streamResponse([
      'event: agent.session.idle\ndata: {"type":"agent.session.idle","event_id":"evt_idle","session_id":"session"}\n\n',
    ]);
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(response, []) });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onSession, onEvent },
    )).rejects.toMatchObject({ status: 502, code: "invalid_stream_event" });
    expect(onSession).not.toHaveBeenCalled();
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("rejects a malformed leading Session snapshot before publishing creation", async () => {
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const malformed = { ...sessionResource(), agent: {} };
    const response = streamResponse([
      `event: agent.session.created\ndata: ${JSON.stringify({
        type: "agent.session.created",
        event_id: "evt_created",
        session: malformed,
      })}\n\n`,
    ]);
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(response, []) });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onSession, onEvent },
    )).rejects.toMatchObject({ status: 502, code: "invalid_session_resource" });
    expect(onSession).not.toHaveBeenCalled();
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("binds the leading creation-stream Environment to the normalized request", async () => {
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const response = streamResponse([`data: ${JSON.stringify({
      type: "agent.session.created",
      event_id: "evt_created",
      session: {
        ...sessionResource(),
        environment: hostedDadf64.session_environment,
      },
    })}\n\n`]);
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(response, []) });

    await expect(client.createSessionStream(
      { environment: { type: "openai_hosted", network: { access: "disabled" } } },
      "create-key",
      { onSession, onEvent },
    )).rejects.toMatchObject({ status: 502, code: "invalid_session_resource" });
    expect(onSession).not.toHaveBeenCalled();
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("allows mutable Session fields to advance while keeping creation configuration immutable", async () => {
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const createdSession = sessionResource();
    const laterSession = {
      ...sessionResource(),
      metadata: { title: "Updated" },
      last_active_at: 2,
      usage: {
        input_tokens: 1,
        output_tokens: 2,
        total_tokens: 3,
        input_tokens_details: { cached_tokens: 0 },
        output_tokens_details: { reasoning_tokens: 1 },
      },
    };
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([
        `data: ${JSON.stringify({ type: "agent.session.created", event_id: "created", session: createdSession })}\n\n`,
        `data: ${JSON.stringify({ type: "agent.session.idle", event_id: "idle", session: laterSession })}\n\n`,
      ]), []),
    });

    await client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onSession, onEvent },
    );

    expect(onSession).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledTimes(2);
    expect(onEvent.mock.calls[1]?.[0]).toMatchObject({
      type: "agent.session.idle",
      session: { metadata: { title: "Updated" }, last_active_at: 2, usage: { total_tokens: 3 } },
    });
  });

  it.each([
    ["Agent", { ...sessionResource(), agent: { ...agentSnapshot(), model: "provider/changed" } }],
    ["Environment", { ...sessionResource(), environment: hostedDadf64.session_environment }],
    ["Vault attachments", { ...sessionResource(["11111111-1111-4111-8111-111111111111"]) }],
  ])("rejects a later creation-stream snapshot that changes immutable %s", async (_label, laterSession) => {
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([
        `data: ${JSON.stringify({ type: "agent.session.created", event_id: "created", session: sessionResource() })}\n\n`,
        `data: ${JSON.stringify({ type: "agent.session.idle", event_id: "idle", session: laterSession })}\n\n`,
      ]), []),
    });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onSession, onEvent },
    )).rejects.toMatchObject({ status: 502, code: "invalid_session_resource" });
    expect(onSession).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["explicit", { type: "agent.session.future", event_id: "evt_cross", session_id: "other" }],
    ["embedded", { type: "agent.session.idle", event_id: "evt_cross", session: { ...sessionResource(), id: "other" } }],
  ])("rejects a later creation-stream event with a cross-Session %s ID", async (_label, crossEvent) => {
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const created = {
      type: "agent.session.created",
      event_id: "evt_created",
      session_id: "session",
      session: sessionResource(),
    };
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([
        `data: ${JSON.stringify(created)}\n\ndata: ${JSON.stringify(crossEvent)}\n\n`,
      ]), []),
    });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onSession, onEvent },
    )).rejects.toMatchObject({ status: 502 });
    expect(onSession).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledTimes(1);
  });

  it("uses the safe in-band error for Session creation streams without forwarding it", async () => {
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([
        'event: error\ndata: {"type":"error","event_id":"evt_error","session_id":"session","error":{"code":"stream_interrupted","type":"server_error","message":"private"}}\n\n',
      ]), []),
    });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onSession, onEvent },
    )).rejects.toMatchObject({
      status: 503,
      code: "stream_interrupted",
      message: "Agent Core interrupted the live event stream. Reconnect and retrieve durable state.",
    });
    expect(onSession).not.toHaveBeenCalled();
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("preserves an AbortError and cancels the creation stream reader", async () => {
    const controller = new AbortController();
    const reason = new DOMException("Stop streaming", "AbortError");
    let cancelled = false;
    const body = new ReadableStream<Uint8Array>({
      cancel() {
        cancelled = true;
      },
    });
    controller.abort(reason);
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(new Response(body), []),
    });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { signal: controller.signal, onSession: vi.fn(), onEvent: vi.fn() },
    )).rejects.toBe(reason);
    expect(cancelled).toBe(true);
  });

  it("turns a creation response without a stream body into empty_stream", async () => {
    const onOpen = vi.fn();
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 201 }), []) });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onOpen, onSession: vi.fn(), onEvent: vi.fn() },
    )).rejects.toMatchObject({ status: 502, code: "empty_stream" });
    expect(onOpen).not.toHaveBeenCalled();
  });

  it.each([
    ["the connection comment only", [": connected\n\n"]],
    ["comments only", [": connected\n\n: keepalive\n\n"]],
    ["DONE only", ["data: [DONE]\n\n"]],
  ])("reports a creation stream with %s as a same-key retry to recover with stream=false", async (_label, chunks) => {
    const onOpen = vi.fn();
    const onSession = vi.fn();
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(streamResponse(chunks, 201), []) });

    const failure = client.createSessionStream(
      { environment: { type: "none" }, input: "First" },
      "create-key",
      { onOpen, onSession, onEvent },
    );
    await expect(failure).rejects.toBeInstanceOf(CreationStreamRetryError);
    await expect(failure).rejects.toMatchObject({ status: 409, code: "creation_stream_retry", message: expect.stringContaining("stream=false") });
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onSession).not.toHaveBeenCalled();
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("keeps empty_stream for a live events stream that ends without events", async () => {
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(streamResponse([": connected\n\n"]), []) });

    await expect(client.streamEvents("session", { onEvent: vi.fn() }))
      .rejects.toMatchObject({ status: 502, code: "empty_stream" });
  });

  it.each([
    ["malformed JSON", "data: {\n\n"],
    ["missing event type", 'data: {"event_id":"evt"}\n\n'],
    ["header-only event type", 'event: agent.session.future\ndata: {"event_id":"evt"}\n\n'],
    ["missing event ID", 'event: agent.session.created\ndata: {"session":{}}\n\n'],
  ])("turns %s into a typed stream error", async (_label, payload) => {
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([payload]), []),
    });

    await expect(client.createSessionStream(
      { environment: { type: "none" } },
      "create-key",
      { onSession: vi.fn(), onEvent: vi.fn() },
    )).rejects.toMatchObject({ status: 502, code: "invalid_stream_event" });
  });

  it.each([
    ["missing Session ID", {
      type: "error", event_id: "event",
      error: { code: "stream_interrupted", type: "server_error", message: "safe" },
    }],
    ["cross-Session ID", {
      type: "error", event_id: "event", session_id: "other",
      error: { code: "stream_interrupted", type: "server_error", message: "safe" },
    }],
    ["extra outer field", {
      type: "error", event_id: "event", session_id: "session", extra: true,
      error: { code: "stream_interrupted", type: "server_error", message: "safe" },
    }],
  ])("rejects an in-band error with %s", async (_label, event) => {
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([`event: error\ndata: ${JSON.stringify(event)}\n\n`]), []),
    });

    await expect(client.streamEvents("session", { onEvent: vi.fn() }))
      .rejects.toMatchObject({ status: 502, code: "invalid_stream_event" });
  });

  it.each([
    ["explicit", { type: "agent.session.future", event_id: "evt_cross", session_id: "other" }],
    ["embedded", { type: "agent.session.idle", event_id: "evt_cross", session: { ...sessionResource(), id: "other" } }],
  ])("rejects a GET stream event with a cross-Session %s ID", async (_label, event) => {
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([`data: ${JSON.stringify(event)}\n\n`]), []),
    });

    await expect(client.streamEvents("session", { onEvent })).rejects.toMatchObject({ status: 502 });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("projects the dadf64a7 known SSE payload families with exact cross references", async () => {
    const events = [
      { type: "agent.session.idle", event_id: "session", session_id: "session", session: sessionResource() },
      {
        type: "agent.session.turn.created",
        event_id: "turn",
        session_id: "session",
        turn_id: "turn_1",
        turn: turnResource(),
      },
      {
        type: "agent.session.turn.item.added",
        event_id: "item",
        session_id: "session",
        turn_id: "turn_1",
        item_id: "item_1",
        output_index: 0,
        item: messageItem(),
      },
      {
        type: "agent.session.turn.item.added",
        event_id: "web-search-item",
        session_id: "session",
        turn_id: "turn_1",
        output_index: 1,
        item: {
          id: "web_search_1",
          turn_id: "turn_1",
          type: "web_search_call",
          status: "in_progress",
        },
      },
      {
        type: "agent.session.turn.content_part.added",
        event_id: "part",
        session_id: "session",
        turn_id: "turn_1",
        item_id: "item_1",
        output_index: 0,
        content_index: 0,
        part: { type: "output_text", text: "" },
      },
      {
        type: "agent.session.turn.output_text.delta",
        event_id: "text-delta",
        session_id: "session",
        turn_id: "turn_1",
        item_id: "item_1",
        output_index: 0,
        content_index: 0,
        delta: "next",
      },
      {
        type: "agent.session.turn.output_text.done",
        event_id: "text-done",
        session_id: "session",
        turn_id: "turn_1",
        item_id: "item_1",
        output_index: 0,
        content_index: 0,
        text: "next",
      },
      {
        type: "agent.output.command_execution_output.delta",
        event_id: "command-delta",
        session_id: "session",
        turn_id: "turn_1",
        item_id: "command_1",
        output_index: 1,
        delta: "stdout",
      },
      {
        type: "agent.session.environment.connected",
        event_id: "environment",
        session_id: "session",
        environment: { id: "environment", type: "self_hosted", status: "connected", error: null },
      },
    ];
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse(events.map((event) => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`)), []),
    });

    await client.streamEvents("session", { onEvent });

    expect(onEvent).toHaveBeenCalledTimes(events.length);
    expect(onEvent.mock.calls.map((call) => call[0]?.type)).toEqual(events.map((event) => event.type));
    expect(onEvent.mock.calls[2]?.[0]).toMatchObject({ item_id: "item_1", item: { id: "item_1", turn_id: "turn_1" } });
  });

  const measuredUsage = {
    input_tokens: 7,
    input_tokens_details: { cached_tokens: 2 },
    output_tokens: 3,
    output_tokens_details: { reasoning_tokens: 1 },
    total_tokens: 10,
  };

  function terminalTurnEvent(status: string, fields: Record<string, unknown> = {}): Record<string, unknown> {
    return {
      type: `agent.session.turn.${status}`,
      event_id: `turn-${status}`,
      session_id: "session",
      turn_id: "turn_1",
      turn: turnResource({
        status,
        started_at: 1,
        completed_at: 2,
        error: status === "failed" ? { code: "internal_error", message: "The execution could not complete." } : null,
      }),
      ...fields,
    };
  }

  it.each([
    ["measured", "completed", measuredUsage],
    ["unknown", "cancelled", null],
    ["unknown failed", "failed", null],
  ])("preserves %s top-level usage on a terminal Turn event", async (_label, status, usage) => {
    const event = terminalTurnEvent(status, { turn: { ...terminalTurnEvent(status).turn as object, usage }, usage });
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`]), []),
    });

    await client.streamEvents("session", { onEvent });

    expect(onEvent).toHaveBeenCalledTimes(1);
    const projected = onEvent.mock.calls[0]?.[0] as SessionEvent;
    expect(projected).toMatchObject({ type: event.type, turn_id: "turn_1", turn: { status, usage } });
    expect(Object.prototype.hasOwnProperty.call(projected, "usage")).toBe(true);
    expect(projected.usage).toEqual(usage);
  });

  it("accepts a terminal Turn event without top-level usage", async () => {
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([`data: ${JSON.stringify(terminalTurnEvent("completed"))}\n\n`]), []),
    });

    await client.streamEvents("session", { onEvent });

    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(Object.prototype.hasOwnProperty.call(onEvent.mock.calls[0]?.[0], "usage")).toBe(false);
  });

  it.each([
    ["a non-terminal Turn event", {
      type: "agent.session.turn.in_progress", event_id: "event", session_id: "session", turn_id: "turn_1",
      turn: turnResource({ status: "in_progress", started_at: 1 }), usage: null,
    }],
    ["a malformed terminal value", terminalTurnEvent("completed", { usage: { input_tokens: 1 } })],
    ["an estimated terminal value", terminalTurnEvent("completed", { usage: 0 })],
  ])("rejects top-level usage on %s", async (_label, event) => {
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([`data: ${JSON.stringify(event)}\n\n`]), []),
    });

    await expect(client.streamEvents("session", { onEvent }))
      .rejects.toMatchObject({ status: 502, code: "invalid_stream_event" });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it.each([
    ["Turn Session", {
      type: "agent.session.turn.created", event_id: "event", session_id: "session", turn_id: "turn_1",
      turn: turnResource({ session_id: "other" }),
    }],
    ["Turn ID", {
      type: "agent.session.turn.created", event_id: "event", session_id: "session", turn_id: "other",
      turn: turnResource(),
    }],
    ["Item Turn", {
      type: "agent.session.turn.item.added", event_id: "event", session_id: "session", turn_id: "turn_1",
      item: messageItem({ turn_id: "other" }),
    }],
    ["Item ID", {
      type: "agent.session.turn.item.added", event_id: "event", session_id: "session", turn_id: "turn_1",
      item_id: "other", item: messageItem(),
    }],
  ])("rejects a known SSE variant with a mismatched %s reference", async (_label, event) => {
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([`data: ${JSON.stringify(event)}\n\n`]), []),
    });

    await expect(client.streamEvents("session", { onEvent }))
      .rejects.toMatchObject({ status: 502, code: "invalid_stream_event" });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("keeps unknown-event metadata while removing every known executable payload field", async () => {
    const onEvent = vi.fn();
    const event = {
      type: "agent.session.future_metadata",
      event_id: "future",
      session_id: "session",
      metadata: { safe: true },
      contract_marker: "future",
      session: sessionResource(),
      turn: turnResource(),
      turn_id: "turn_1",
      item: messageItem(),
      item_id: "item_1",
      output_index: 0,
      content_index: 0,
      part: { type: "output_text", text: "unsafe" },
      delta: "unsafe",
      text: "unsafe",
      environment: { id: "environment" },
      error: { code: "unsafe" },
    };
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(streamResponse([`data: ${JSON.stringify(event)}\n\n`]), []),
    });

    await client.streamEvents("session", { onEvent });

    expect(onEvent).toHaveBeenCalledWith({
      type: "agent.session.future_metadata",
      event_id: "future",
      session_id: "session",
      metadata: { safe: true },
      contract_marker: "future",
    });
  });

  it.each([
    ["known extra field", `data: ${JSON.stringify({
      type: "agent.session.turn.created",
      event_id: "event",
      session_id: "session",
      turn_id: "turn_1",
      turn: turnResource(),
      extra: true,
    })}\n\n`],
    ["mismatched SSE type", `event: agent.session.idle\ndata: ${JSON.stringify({
      type: "agent.session.in_progress",
      event_id: "event",
      session: { ...sessionResource(), status: "in_progress" },
    })}\n\n`],
  ])("rejects $label before forwarding the SSE event", async (_label, payload) => {
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(streamResponse([payload]), []) });
    await expect(client.streamEvents("session", { onEvent }))
      .rejects.toMatchObject({ status: 502, code: "invalid_stream_event" });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("preserves the nested API error fields", async () => {
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(
        jsonResponse(
          {
            error: {
              message: "A valid key is required.",
              type: "invalid_request_error",
              code: "invalid_api_key",
              param: "Authorization",
            },
          },
          401,
        ),
        [],
      ),
    });

    const error = await client.listAgents().catch((reason: unknown) => reason);

    expect(error).toBeInstanceOf(AgentCoreError);
    expect(error).toMatchObject({
      message: "A valid key is required.",
      status: 401,
      code: "invalid_api_key",
      param: "Authorization",
      errorType: "invalid_request_error",
    });
  });

  it("passes AbortSignal to durable Session, Environment, and Item reads", async () => {
    const calls: FetchCall[] = [];
    const controller = new AbortController();
    const client = new OpenAIAgentsClient({
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        if (String(input).includes("/environments/")) {
          return jsonResponse({
            id: "environment",
            object: "agent.environment",
            type: "self_hosted",
            status: "pending",
            files: [],
            plugins: [],
            skills: [],
          });
        }
        if (String(input).includes("/sessions/") && !String(input).includes("/items")) {
          return jsonResponse({
            id: "session",
            object: "agent.session",
            agent: agentSnapshot(),
            environment: { type: "none" },
            status: "idle",
            error: null,
            metadata: {},
            required_actions: [],
            vault_ids: [],
            usage: null,
            created_at: 1,
            last_active_at: 1,
          });
        }
        return jsonResponse({ data: [], has_more: false });
      }) as typeof fetch,
    });

    await client.retrieveSession("session", { signal: controller.signal });
    await client.retrieveEnvironment("environment", { signal: controller.signal });
    await client.listItems("session", { signal: controller.signal, limit: 100 });

    expect(calls).toHaveLength(3);
    expect(calls[0]?.init?.signal).toBe(controller.signal);
    expect(calls[1]?.init?.signal).toBe(controller.signal);
    expect(calls[2]?.init?.signal).toBe(controller.signal);
  });

  it.each(["pending", "connected", "disconnected", "expired", "failed"] as EnvironmentResourceStatus[])(
    "retrieves and projects the exact %s Environment resource",
    async (status) => {
      const calls: FetchCall[] = [];
      const controller = new AbortController();
      const resource = {
        id: "environment/one",
        object: "agent.environment",
        type: "self_hosted",
        status,
        files: [],
        plugins: [],
        skills: [],
      } as const;
      const client = new OpenAIAgentsClient({
        baseUrl: "https://core.example/v1/",
        token: "tenant-key",
        fetch: recordingFetch(jsonResponse(resource), calls),
      });

      await expect(client.retrieveEnvironment("environment/one", { signal: controller.signal })).resolves.toEqual(resource);

      expect(calls).toHaveLength(1);
      expect(String(calls[0]?.input)).toBe("https://core.example/v1/agents/environments/environment%2Fone");
      expect(calls[0]?.init?.method).toBeUndefined();
      expect(calls[0]?.init?.body).toBeUndefined();
      expect(calls[0]?.init?.signal).toBe(controller.signal);
      const headers = new Headers(calls[0]?.init?.headers);
      expect(headers.get("Authorization")).toBe("Bearer tenant-key");
      expect(headers.get("OpenAI-Beta")).toBe("agents=v1");
    },
  );

  it("retrieves the exact openai_hosted Environment resource without inferring readiness", async () => {
    const calls: FetchCall[] = [];
    const resource = {
      id: "environment",
      object: "agent.environment",
      type: "openai_hosted",
      status: "pending",
      files: [],
      plugins: [],
      skills: [],
    } as const;
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(resource), calls) });

    await expect(client.retrieveEnvironment("environment")).resolves.toEqual(resource);
    expect(calls).toHaveLength(1);
  });

  it("accepts a canonical Environment UUID response for an uppercase request ID", async () => {
    const calls: FetchCall[] = [];
    const responseId = "0f745b0d-b545-49cd-8d7e-4c31c80dc564";
    const requestId = responseId.toUpperCase();
    const resource = {
      id: responseId,
      object: "agent.environment",
      type: "self_hosted",
      status: "pending",
      files: [],
      plugins: [],
      skills: [],
    } as const;
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1",
      fetch: recordingFetch(jsonResponse(resource), calls),
    });

    await expect(client.retrieveEnvironment(requestId)).resolves.toEqual(resource);
    expect(String(calls[0]?.input)).toBe(`https://core.example/v1/agents/environments/${requestId}`);
    expect(calls).toHaveLength(1);
  });

  it("lists a strict Environment files page with the pinned path/order/limit/page query", async () => {
    const calls: FetchCall[] = [];
    const controller = new AbortController();
    const page = {
      object: "page",
      data: [
        {
          environment_id: "environment/one",
          object: "agent.environment.file",
          path: "/workspace/project/report.json",
          size_bytes: 2048,
        },
      ],
      next: null,
      has_more: false,
    } as const;
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      token: "tenant-key",
      fetch: recordingFetch(jsonResponse(page), calls),
    });

    await expect(client.listEnvironmentFiles("environment/one", {
      path: "/workspace/project",
      limit: 20,
      order: "asc",
      page: "opaque/current page",
      signal: controller.signal,
    })).resolves.toEqual(page);

    expect(String(calls[0]?.input)).toBe(
      "https://core.example/v1/agents/environments/environment%2Fone/files?path=%2Fworkspace%2Fproject&limit=20&order=asc&page=opaque%2Fcurrent+page",
    );
    expect(calls[0]?.init?.signal).toBe(controller.signal);
    expect(new Headers(calls[0]?.init?.headers).get("OpenAI-Beta")).toBe("agents=v1");
  });

  it("uses the fixed public /workspace root when path is omitted and validates direct children", async () => {
    const calls: FetchCall[] = [];
    const page = { object: "page", data: [{
      environment_id: "environment",
      object: "agent.environment.file",
      path: "/workspace/report.json",
      size_bytes: 3,
    }], next: null, has_more: false };
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(page), calls) });

    await expect(client.listEnvironmentFiles("environment", { order: "asc" }))
      .resolves.toEqual(page);
    expect(new URL(String(calls[0]?.input), "https://web.example").searchParams.has("path")).toBe(false);

    const nested = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({
      ...page, data: [{ ...page.data[0], path: "/workspace/nested/report.json" }],
    }), []) });
    await expect(nested.listEnvironmentFiles("environment", { order: "asc" }))
      .rejects.toMatchObject({ status: 502, code: "invalid_environment_files" });
  });

  it("accepts an explicit self-hosted Workspace root and validates direct children there", async () => {
    const calls: FetchCall[] = [];
    const page = { object: "page", data: [{
      environment_id: "environment",
      object: "agent.environment.file",
      path: "/test/report.json",
      size_bytes: 3,
    }], next: null, has_more: false };
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(page), calls) });

    await expect(client.listEnvironmentFiles("environment", { path: "/test", order: "asc" }))
      .resolves.toEqual(page);
    expect(new URL(String(calls[0]?.input), "https://web.example").searchParams.get("path")).toBe("/test");
  });

  it("preserves an unsupported Environment Files response without retrying", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({
        error: {
          type: "invalid_request_error",
          code: "unsupported_operation",
          message: "This API operation is not supported.",
          param: null,
        },
      }, 404), calls),
    });

    await expect(client.listEnvironmentFiles("environment", { path: "/test" }))
      .rejects.toMatchObject({ status: 404, code: "unsupported_operation" });
    expect(calls).toHaveLength(1);
  });

  it.each([
    null,
    {},
    { data: [], next: null },
    { object: "list", data: [], next: null, has_more: false },
    { data: [], next: null, has_more: false },
    { object: "page", data: [], next: null },
    { object: "page", data: [], next: null, has_more: true },
    { object: "page", data: [], next: null, has_more: "false" },
    { object: "page", data: [], next: null, has_more: false, extra: true },
    { object: "page", data: [], next: "", has_more: true },
    { object: "page", data: [], next: "opaque-next", has_more: true },
    { object: "page", data: [{ environment_id: "environment", object: "agent.environment.file", path: "/workspace/file", size_bytes: 1 }], next: "opaque-next", has_more: true },
    { object: "page", data: [{ environment_id: "environment", object: "agent.environment.file", path: "/workspace/file", size_bytes: 1 }], next: "x".repeat(1025), has_more: true },
    { object: "page", data: null, next: null, has_more: false },
    { object: "page", data: [{ environment_id: "other", object: "agent.environment.file", path: "/workspace/file", size_bytes: 1 }], next: null, has_more: false },
    { object: "page", data: [{ environment_id: "environment", object: "file", path: "/workspace/file", size_bytes: 1 }], next: null, has_more: false },
    { object: "page", data: [{ environment_id: "environment", object: "agent.environment.file", path: "relative", size_bytes: 1 }], next: null, has_more: false },
    { object: "page", data: [{ environment_id: "environment", object: "agent.environment.file", path: "/workspace/../secret", size_bytes: 1 }], next: null, has_more: false },
    { object: "page", data: [{ environment_id: "environment", object: "agent.environment.file", path: "/workspace/file", size_bytes: -1 }], next: null, has_more: false },
    { object: "page", data: [{ environment_id: "environment", object: "agent.environment.file", path: "/workspace/file", size_bytes: 1, extra: true }], next: null, has_more: false },
  ])("rejects a malformed Environment files page without retrying", async (page) => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(page), calls) });

    await expect(client.listEnvironmentFiles("environment", {})).rejects.toMatchObject({
      status: 502,
      code: "invalid_environment_files",
      message: "Agent Core returned an invalid Environment files page.",
    });
    expect(calls).toHaveLength(1);
  });

  it("rejects out-of-directory, nested, duplicate, unsorted, or over-limit Environment file pages", async () => {
    const file = (path: string) => ({
      environment_id: "environment",
      object: "agent.environment.file",
      path,
      size_bytes: 1,
    });
    const final = (data: unknown[]) => ({ object: "page", data, next: null, has_more: false });
    const cases: Array<{ page: unknown; options: EnvironmentFileListOptions }> = [
      { page: final([file("/other/file")]), options: { path: "/workspace", order: "asc" } },
      { page: final([file("/workspace/nested/file")]), options: { path: "/workspace", order: "asc" } },
      { page: final([file("/workspace//file")]), options: { path: "/workspace", order: "asc" } },
      { page: final([file("/workspace/a"), file("/workspace/a")]), options: { path: "/workspace", order: "asc" } },
      { page: final([file("/workspace/b"), file("/workspace/a")]), options: { path: "/workspace", order: "asc" } },
      { page: final([file("/workspace/a"), file("/workspace/b")]), options: { path: "/workspace", order: "asc", limit: 1 } },
    ];

    for (const entry of cases) {
      const calls: FetchCall[] = [];
      const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(entry.page), calls) });
      await expect(client.listEnvironmentFiles("environment", entry.options)).rejects.toMatchObject({
        status: 502,
        code: "invalid_environment_files",
      });
      expect(calls).toHaveLength(1);
    }
  });

  it("rejects a non-canonical uppercase Environment UUID response without retrying", async () => {
    const calls: FetchCall[] = [];
    const requestId = "0f745b0d-b545-49cd-8d7e-4c31c80dc564";
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(
        jsonResponse({
          id: requestId.toUpperCase(),
          object: "agent.environment",
          type: "self_hosted",
          status: "pending",
          files: [],
          plugins: [],
          skills: [],
        }),
        calls,
      ),
    });

    await expect(client.retrieveEnvironment(requestId)).rejects.toMatchObject({
      status: 502,
      code: "invalid_environment_resource",
    });
    expect(calls).toHaveLength(1);
  });

  it.each([
    null,
    {},
    { id: "environment", object: "agent.environment", type: "self_hosted", status: "ready", files: [], plugins: [], skills: [] },
    { id: "another", object: "agent.environment", type: "self_hosted", status: "pending", files: [], plugins: [], skills: [] },
    { id: "environment", object: "agent.environment", type: "hosted", status: "pending", files: [], plugins: [], skills: [] },
    { id: "environment", object: "agent.environment", type: "self_hosted", status: "pending", files: null, plugins: [], skills: [] },
    { id: "environment", object: "agent.environment", type: "self_hosted", status: "pending", files: [], plugins: [], skills: [], extra: true },
  ])("rejects a malformed or unsupported Environment resource without retrying", async (resource) => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(resource), calls) });

    await expect(client.retrieveEnvironment("environment")).rejects.toMatchObject({
      status: 502,
      code: "invalid_environment_resource",
      message: "Agent Core returned an invalid Environment resource.",
    });
    expect(calls).toHaveLength(1);
  });

  it.each([400, 401, 404, 405, 500])("preserves Environment retrieve HTTP %s without retrying", async (status) => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({ error: { code: "fixture_error", message: "Safe failure." } }, status), calls),
    });

    await expect(client.retrieveEnvironment("environment")).rejects.toMatchObject({
      status,
      code: "fixture_error",
      message: "Safe failure.",
    });
    expect(calls).toHaveLength(1);
  });

  it("uploads Source File multipart without an Agents beta header and projects exact metadata", async () => {
    const calls: FetchCall[] = [];
    const id = "file-16e1f26e-8cf6-4272-9c31-d470b08d31af";
    const metadata = {
      id,
      object: "file",
      bytes: 3,
      created_at: 123,
      filename: "notes.txt",
      purpose: "user_data",
      status: "processed",
      expires_at: null,
      status_details: null,
    } as const;
    const controller = new AbortController();
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      token: "tenant-key",
      fetch: recordingFetch(jsonResponse(metadata), calls),
    });

    await expect(client.uploadSourceFile({
      file: new Blob(["abc"], { type: "text/plain" }),
      filename: "notes.txt",
    }, { signal: controller.signal })).resolves.toEqual(metadata);

    expect(String(calls[0]?.input)).toBe("https://core.example/v1/files");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(calls[0]?.init?.signal).toBe(controller.signal);
    const headers = new Headers(calls[0]?.init?.headers);
    expect(headers.get("Authorization")).toBe("Bearer tenant-key");
    expect(headers.get("OpenAI-Beta")).toBeNull();
    expect(headers.get("Content-Type")).toBeNull();
    const body = calls[0]?.init?.body as FormData;
    expect(Array.from(body.keys())).toEqual(["file", "purpose"]);
    expect(body.get("purpose")).toBe("user_data");
    const file = body.get("file") as File;
    expect(file.name).toBe("notes.txt");
    expect(await file.text()).toBe("abc");
  });

  it("accepts an exact zero-byte Unicode Source File upload response", async () => {
    const metadata = {
      id: "file-16e1f26e-8cf6-4272-9c31-d470b08d31af",
      object: "file",
      bytes: 0,
      created_at: 123,
      filename: "空.txt",
      purpose: "user_data",
      status: "processed",
      expires_at: null,
      status_details: null,
    } as const;
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(metadata), []) });

    await expect(client.uploadSourceFile({ file: new Blob([]), filename: metadata.filename }))
      .resolves.toEqual(metadata);
  });

  it.each([
    { label: "filename", change: { filename: "different.txt" } },
    { label: "byte count", change: { bytes: 2 } },
  ])("rejects Source File upload metadata with a mismatched $label", async ({ change }) => {
    const calls: FetchCall[] = [];
    const metadata = {
      id: "file-16e1f26e-8cf6-4272-9c31-d470b08d31af",
      object: "file",
      bytes: 3,
      created_at: 123,
      filename: "notes.txt",
      purpose: "user_data",
      status: "processed",
      expires_at: null,
      status_details: null,
      ...change,
    };
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(metadata), calls) });

    await expect(client.uploadSourceFile({ file: new Blob(["abc"]), filename: "notes.txt" }))
      .rejects.toMatchObject({ status: 502, code: "invalid_source_file" });
    expect(calls).toHaveLength(1);
  });

  it("retrieves, downloads, and deletes a Source File by ID without Beta or retries", async () => {
    const calls: FetchCall[] = [];
    const id = "file-16e1f26e-8cf6-4272-9c31-d470b08d31af";
    const metadata = {
      id,
      object: "file",
      bytes: 3,
      created_at: 123,
      filename: "notes.txt",
      purpose: "user_data",
      status: "processed",
      expires_at: null,
      status_details: null,
    } as const;
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1",
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        if (String(input).endsWith("/content")) {
          return new Response(new Uint8Array([1, 2, 3]), {
            status: 200,
            headers: {
              "Content-Type": "application/octet-stream",
              "Content-Disposition": 'attachment; filename="notes.txt"',
              "Content-Length": "3",
              "Cache-Control": "no-store",
              "X-Content-Type-Options": "nosniff",
            },
          });
        }
        if (init?.method === "DELETE") return jsonResponse({ id, object: "file", deleted: true });
        return jsonResponse(metadata);
      }) as typeof fetch,
    });

    await expect(client.retrieveSourceFile(id)).resolves.toEqual(metadata);
    await expect(client.downloadSourceFile(id)).resolves.toMatchObject({
      bytes: 3,
      content_type: "application/octet-stream",
      content_disposition: 'attachment; filename="notes.txt"',
    });
    await expect(client.deleteSourceFile(id)).resolves.toEqual({ id, object: "file", deleted: true });

    expect(Array.from((await client.downloadSourceFile(id)).data)).toEqual([1, 2, 3]);
    expect(calls).toHaveLength(4);
    for (const call of calls) expect(new Headers(call.init?.headers).get("OpenAI-Beta")).toBeNull();
    expect(new Headers(calls[1]?.init?.headers).get("Accept")).toBe("application/octet-stream");
  });

  it("reads Source File content as an exact bounded stream without arrayBuffer", async () => {
    const response = new Response(new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array([1, 2]));
        controller.enqueue(new Uint8Array([3]));
        controller.close();
      },
    }), {
      headers: {
        "Content-Type": "application/octet-stream",
        "Content-Disposition": "attachment",
        "Content-Length": "3",
        "Cache-Control": "no-store",
        "X-Content-Type-Options": "nosniff",
      },
    });
    const arrayBuffer = vi.spyOn(response, "arrayBuffer");
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(response, []) });

    const content = await client.downloadSourceFile("file-16e1f26e-8cf6-4272-9c31-d470b08d31af");
    expect(Array.from(content.data)).toEqual([1, 2, 3]);
    expect(arrayBuffer).not.toHaveBeenCalled();
  });

  it.each([
    { contentType: "application/json", contentLength: "3", body: "abc" },
    { contentType: "application/octet-stream", contentLength: null, body: "abc" },
    { contentType: "application/octet-stream", contentLength: "4", body: "abc" },
    { contentType: "application/octet-stream", contentLength: "2", body: "abc" },
    { contentType: "application/octet-stream", contentLength: String(512 * 1024 * 1024 + 1), body: "" },
  ])("rejects malformed Source File content without accepting bytes", async ({ contentType, contentLength, body }) => {
    const headers: Record<string, string> = {
      "Content-Type": contentType,
      "Content-Disposition": "attachment",
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
    };
    if (contentLength !== null) headers["Content-Length"] = contentLength;
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(body, { headers }), calls) });

    await expect(client.downloadSourceFile("file-16e1f26e-8cf6-4272-9c31-d470b08d31af")).rejects.toMatchObject({
      status: 502,
      code: "invalid_source_file_content",
    });
    expect(calls).toHaveLength(1);
  });

  it("creates an Environment file only with the exact file_id union and validates the response", async () => {
    const calls: FetchCall[] = [];
    const controller = new AbortController();
    const fileId = "file-16e1f26e-8cf6-4272-9c31-d470b08d31af";
    const result = {
      environment_id: "environment/one",
      object: "agent.environment.file",
      path: "/workspace/input/notes.txt",
      size_bytes: 3,
    } as const;
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      fetch: recordingFetch(jsonResponse(result, 201), calls),
    });

    await expect(client.createEnvironmentFile("environment/one", {
      type: "file_id",
      file_id: fileId,
      path: result.path,
    }, { signal: controller.signal })).resolves.toEqual(result);

    expect(String(calls[0]?.input)).toBe("https://core.example/v1/agents/environments/environment%2Fone/files");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(calls[0]?.init?.signal).toBe(controller.signal);
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({
      type: "file_id",
      file_id: fileId,
      path: result.path,
    });
    const headers = new Headers(calls[0]?.init?.headers);
    expect(headers.get("OpenAI-Beta")).toBe("agents=v1");
    expect(headers.get("Content-Type")).toBe("application/json");
  });

  it("lists a continued Environment files page and requires the official has_more flag", async () => {
    const file = (name: string) => ({
      environment_id: "environment",
      object: "agent.environment.file",
      path: `/workspace/${name}`,
      size_bytes: 1,
    });
    const page = { object: "page", data: [file("a"), file("b")], next: "opaque-next", has_more: true } as const;
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(page), []) });
    await expect(client.listEnvironmentFiles("environment", { order: "asc", limit: 2 })).resolves.toEqual(page);

    const empty = { object: "page", data: [], next: null, has_more: false } as const;
    const missing = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(empty), []) });
    await expect(missing.listEnvironmentFiles("environment", { path: "/workspace/missing" })).resolves.toEqual(empty);
  });

  it("requires 201 Created for an Environment file and never retries another success status", async () => {
    const calls: FetchCall[] = [];
    const result = {
      environment_id: "environment",
      object: "agent.environment.file",
      path: "/workspace/notes.txt",
      size_bytes: 1,
    };
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(result, 200), calls) });

    await expect(client.createEnvironmentFile("environment", { type: "inline", data: "YQ==", path: result.path }))
      .rejects.toMatchObject({ status: 200 });
    expect(calls).toHaveLength(1);
  });

  it("rejects invalid Source and Environment file inputs before making a request", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({}), calls) });

    await expect(client.retrieveSourceFile("notes.txt")).rejects.toThrow("Invalid Source File ID");
    await expect(client.uploadSourceFile({ file: new Blob(["x"]), filename: "" })).rejects.toThrow("valid filename");
    await expect(client.listEnvironmentFiles("environment", { path: "relative" }))
      .rejects.toThrow("absolute directory without parent traversal");
    await expect(client.listEnvironmentFiles("environment", { path: "/executor/../secret" }))
      .rejects.toThrow("absolute directory without parent traversal");
    await expect(client.createEnvironmentFile("environment", {
      type: "inline",
      data: "YR==",
      path: "/workspace/file.txt",
    })).rejects.toThrow("strict standard Base64");
    await expect(client.createEnvironmentFile("environment", {
      type: "file_id",
      file_id: "file-16e1f26e-8cf6-4272-9c31-d470b08d31af",
      path: "/workspace/../secret",
    })).rejects.toThrow("canonical absolute paths");
    expect(calls).toHaveLength(0);
  });

  it.each([404, 413, 503])("preserves Source upload HTTP %s after one attempt", async (status) => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({ error: { code: "fixture_error", message: "Private detail." } }, status), calls),
    });

    await expect(client.uploadSourceFile({ file: new Blob(["x"]), filename: "x.txt" })).rejects.toMatchObject({
      status,
      code: "fixture_error",
    });
    expect(calls).toHaveLength(1);
  });

  it.each([201, 202, 204, 206])("rejects Environment retrieve HTTP %s without retrying", async (status) => {
    const calls: FetchCall[] = [];
    const resource = {
      id: "environment",
      object: "agent.environment",
      type: "self_hosted",
      status: "pending",
      files: [],
      plugins: [],
      skills: [],
    };
    const response = status === 204 ? new Response(null, { status }) : jsonResponse(resource, status);
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(response, calls) });

    await expect(client.retrieveEnvironment("environment")).rejects.toMatchObject({
      status,
      message: `Agent core request failed (${status}).`,
    });
    expect(calls).toHaveLength(1);
  });

  it("propagates an Environment retrieve network failure without retrying", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        throw new TypeError("Failed to fetch");
      }) as typeof fetch,
    });

    await expect(client.retrieveEnvironment("environment")).rejects.toThrow("Failed to fetch");
    expect(calls).toHaveLength(1);
  });

  it("cancels the response body when a stream callback fails", async () => {
    let cancelled = false;
    const body = new ReadableStream<Uint8Array>({
      cancel() {
        cancelled = true;
      },
    });
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(new Response(body), []),
    });

    await expect(
      client.streamEvents("session", {
        onOpen: () => {
          throw new Error("consumer failed");
        },
        onEvent: () => undefined,
      }),
    ).rejects.toThrow("consumer failed");
    expect(cancelled).toBe(true);
  });

  it("lists Turns with encoded Session scope, pagination, ordering, and cancellation", async () => {
    const calls: FetchCall[] = [];
    const controller = new AbortController();
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example.test/v1/",
      fetch: recordingFetch(jsonResponse({ data: [], has_more: false }), calls),
    });

    await client.listTurns("session/one", {
      after: "turn/previous",
      limit: 100,
      order: "asc",
      signal: controller.signal,
    });

    expect(calls).toHaveLength(1);
    expect(String(calls[0]?.input)).toBe("https://core.example.test/v1/agents/sessions/session%2Fone/turns?after=turn%2Fprevious&limit=100&order=asc");
    expect(calls[0]?.init?.method).toBeUndefined();
    expect(calls[0]?.init?.signal).toBe(controller.signal);
    expect(new Headers(calls[0]?.init?.headers).get("OpenAI-Beta")).toBe("agents=v1");
  });

  it("submits typed function-result parts with an explicit idempotency key", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });

    await client.submitFunctionResult(
      "session",
      {
        callId: "call_1",
        turnId: "turn_1",
        success: true,
        output: [
          { type: "input_text", text: "done" },
          { type: "input_image", image_url: "data:image/png;base64,AA==" },
        ],
      },
      "retry-1",
    );

    expect(calls[0]?.init?.method).toBe("POST");
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("retry-1");
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({
      events: [
        {
          type: "agent.session.input.tool_result",
          call_id: "call_1",
          turn_id: "turn_1",
          success: true,
          output: [
            { type: "input_text", text: "done" },
            { type: "input_image", image_url: "data:image/png;base64,AA==" },
          ],
        },
      ],
    });
  });

  it("submits one ordered mixed event batch with one caller retry identity", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example.test/v1/",
      fetch: recordingFetch(new Response(null, { status: 202 }), calls),
    });
    const events = eventBatchDadf64.request.events as SessionInputEvent[];

    await client.submitEvents("session/one", events, "mixed-batch-key");

    expect(calls).toHaveLength(1);
    expect(String(calls[0]?.input)).toBe("https://core.example.test/v1/agents/sessions/session%2Fone/events");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("mixed-batch-key");
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual(eventBatchDadf64.request);
  });

  it("preserves event order when the same retry key is deliberately reused", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });
    const message: SessionInputEvent = {
      type: "agent.session.input.message",
      input: [{ role: "user", content: [{ type: "input_text", text: "hello" }] }],
    };
    const cancel: SessionInputEvent = { type: "agent.session.input.cancel" };

    await client.submitEvents("session", [message, cancel], "same-key");
    await client.submitEvents("session", [cancel, message], "same-key");

    expect(calls).toHaveLength(2);
    expect(calls.map((call) => new Headers(call.init?.headers).get("Idempotency-Key")))
      .toEqual(["same-key", "same-key"]);
    expect(calls.map((call) => (JSON.parse(String(call.init?.body)) as { events: SessionInputEvent[] }).events.map((event) => event.type)))
      .toEqual([
        ["agent.session.input.message", "agent.session.input.cancel"],
        ["agent.session.input.cancel", "agent.session.input.message"],
      ]);
  });

  it("preserves Core-permitted empty Function values and ordered rich output parts", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });
    const events: SessionInputEvent[] = [
      {
        type: "agent.session.input.tool_result",
        call_id: "call-empty",
        turn_id: "turn-empty",
        success: false,
        output: "",
        error: "",
      },
      {
        type: "agent.session.input.tool_result",
        call_id: "call-parts",
        turn_id: "turn-parts",
        success: true,
        output: [
          { type: "input_text", text: "" },
          { type: "input_image", image_url: "" },
          { type: "input_text", text: "last" },
        ],
        error: null,
      },
      {
        type: "agent.session.input.tool_result",
        call_id: "call-empty-parts",
        turn_id: "turn-empty-parts",
        success: true,
        output: [],
      },
    ];

    await client.submitEvents("session", events, "rich-results");

    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({ events });
  });

  it.each([
    { label: "non-array", events: {} },
    { label: "sparse", events: Array(1) },
    { label: "65 events", events: Array.from({ length: 65 }, () => ({ type: "agent.session.input.cancel" })) },
    { label: "unknown variant", events: [{ type: "agent.session.input.future" }] },
    { label: "extra event field", events: [{ type: "agent.session.input.cancel", input: null }] },
    { label: "empty message input", events: [{ type: "agent.session.input.message", input: [] }] },
    { label: "non-user message", events: [{
      type: "agent.session.input.message",
      input: [{ role: "assistant", content: [{ type: "input_text", text: "hello" }] }],
    }] },
    { label: "empty text message", events: [{
      type: "agent.session.input.message",
      input: [{ role: "user", content: [{ type: "input_text", text: "" }] }],
    }] },
    { label: "all-empty text parts", events: [{
      type: "agent.session.input.message",
      input: [{ role: "user", content: [{ type: "input_text", text: "" }, { type: "input_text", text: "" }] }],
    }] },
    { label: "sparse message content", events: [{
      type: "agent.session.input.message",
      input: [{ role: "user", content: Array(1) }],
    }] },
    { label: "extra message field", events: [{
      type: "agent.session.input.message",
      input: [{ role: "user", content: [{ type: "input_text", text: "hello" }], name: "extra" }],
    }] },
    { label: "message image", events: [{
      type: "agent.session.input.message",
      input: [{ role: "user", content: [{ type: "input_image", image_url: "https://example.test/a.png" }] }],
    }] },
    { label: "empty call id", events: [{
      type: "agent.session.input.tool_result", call_id: "", turn_id: "turn", success: true,
    }] },
    { label: "empty turn id", events: [{
      type: "agent.session.input.tool_result", call_id: "call", turn_id: "", success: true,
    }] },
    { label: "non-boolean success", events: [{
      type: "agent.session.input.tool_result", call_id: "call", turn_id: "turn", success: "true",
    }] },
    { label: "unknown result field", events: [{
      type: "agent.session.input.tool_result", call_id: "call", turn_id: "turn", success: true, extra: null,
    }] },
    { label: "malformed output scalar", events: [{
      type: "agent.session.input.tool_result", call_id: "call", turn_id: "turn", success: true, output: 1,
    }] },
    { label: "malformed rich part", events: [{
      type: "agent.session.input.tool_result",
      call_id: "call",
      turn_id: "turn",
      success: true,
      output: [{ type: "input_image", image_url: "url", text: "extra" }],
    }] },
  ])("rejects malformed Session event batch locally: $label", ({ events }) => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });

    expect(() => client.submitEvents("session", events as unknown as SessionInputEvent[], "invalid"))
      .toThrow(TypeError);
    expect(calls).toHaveLength(0);
  });

  it("enforces only Core's 1 MiB HTTP wire limit and leaves canonical internal sizing to Core", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });
    const makeEvent = (text: string): SessionInputEvent => ({
      type: "agent.session.input.message",
      input: [{ role: "user", content: [{ type: "input_text", text }] }],
    });
    const emptyWireBytes = new TextEncoder().encode(JSON.stringify({ events: [makeEvent("")] })).length;
    const exact = makeEvent("x".repeat(1024 * 1024 - emptyWireBytes));

    await client.submitEvents("session", [exact], "exact-boundary");

    expect(new TextEncoder().encode(String(calls[0]?.init?.body))).toHaveLength(1024 * 1024);
    expect(() => client.submitEvents("session", [makeEvent(`${"x".repeat(1024 * 1024 - emptyWireBytes)}x`)], "over"))
      .toThrow("Session input event request exceeds 1 MiB.");

    const canonicalDifference = makeEvent("<>&/\u2028".repeat(80_000));
    expect(new TextEncoder().encode(JSON.stringify({ events: [canonicalDifference] })).length).toBeGreaterThan(512 * 1024);
    await client.submitEvents("session", [canonicalDifference], "core-canonicalizes");

    const unicodeOversize = makeEvent("🙂".repeat(262_150));
    expect(JSON.stringify({ events: [unicodeOversize] }).length).toBeLessThan(1024 * 1024);
    expect(() => client.submitEvents("session", [unicodeOversize], "utf8-over"))
      .toThrow("Session input event request exceeds 1 MiB.");
    expect(calls).toHaveLength(2);
  });

  it("sends whitespace-only message text verbatim, as Core admits it", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });
    const event: SessionInputEvent = {
      type: "agent.session.input.message",
      input: [
        { role: "user", content: [{ type: "input_text", text: "   " }] },
        { role: "user", content: [{ type: "input_text", text: "\n\t" }] },
        { role: "user", content: [{ type: "input_text", text: "\u0085" }] },
      ],
    };

    await client.submitEvents("session", [event], "whitespace-text");

    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({ events: [event] });
  });

  it("does not invent JavaScript-only whitespace restrictions for message text", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });
    const event: SessionInputEvent = {
      type: "agent.session.input.message",
      input: [{ role: "user", content: [
        { type: "input_text", text: "" },
        { type: "input_text", text: "\ufeff" },
      ] }],
    };

    await client.submitEvents("session", [event], "go-trim-space");

    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({ events: [event] });
  });

  it("sends an empty event batch once and accepts the official empty 202 response", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });
    await expect(client.submitEvents("session", [], "empty-key")).resolves.toBeUndefined();
    expect(calls).toHaveLength(1);
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({ events: [] });
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("empty-key");
  });

  it("does not retry a failed public batch submission or reinterpret non-202 success", async () => {
    const event: SessionInputEvent = { type: "agent.session.input.cancel" };
    for (const status of [200, 204, 409, 500]) {
      const calls: FetchCall[] = [];
      const client = new OpenAIAgentsClient({
        fetch: recordingFetch(status === 204 ? new Response(null, { status }) : jsonResponse({ error: { message: "rejected" } }, status), calls),
      });

      await expect(client.submitEvents("session", [event], "one-attempt")).rejects.toMatchObject({ status });
      expect(calls).toHaveLength(1);
    }
  });

  it("lets the caller hold and reuse one event-write idempotency key before an uncertain retry", async () => {
    const calls: FetchCall[] = [];
    const key = createIdempotencyKey();
    const client = new OpenAIAgentsClient({
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        return jsonResponse({ error: { code: "temporary_failure", message: "Retry explicitly." } }, 500);
      }) as typeof fetch,
    });
    const events: SessionInputEvent[] = [{ type: "agent.session.input.cancel" }];

    expect(key.trim()).not.toBe("");
    expect(new TextEncoder().encode(key).length).toBeLessThanOrEqual(128);
    await expect(client.submitEvents("session", events, key)).rejects.toMatchObject({ status: 500 });
    await expect(client.submitEvents("session", events, key)).rejects.toMatchObject({ status: 500 });
    expect(calls).toHaveLength(2);
    expect(calls.map((call) => new Headers(call.init?.headers).get("Idempotency-Key")))
      .toEqual([key, key]);
  });

  it.each([
    undefined,
    "",
    " \u0085 ",
    "x".repeat(129),
    "🙂".repeat(33),
  ])("rejects invalid event-write idempotency key %j before fetch", (key) => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });

    expect(() => client.submitEvents(
      "session",
      [{ type: "agent.session.input.cancel" }],
      key as unknown as string,
    )).toThrow("Idempotency key must be non-blank and at most 128 UTF-8 bytes.");
    expect(calls).toHaveLength(0);
  });

  it("keeps the three legacy single-event helpers on the public batch wire", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(null, { status: 202 }), calls) });

    await client.sendMessage("session", "hello", "message-key");
    await client.cancelTurn("session", "cancel-key");
    await client.submitFunctionResult("session", {
      callId: "call",
      turnId: "turn",
      success: false,
      error: null,
    }, "result-key");

    expect(calls.map((call) => JSON.parse(String(call.init?.body)))).toEqual([
      { events: [{
        type: "agent.session.input.message",
        input: [{ role: "user", content: [{ type: "input_text", text: "hello" }] }],
      }] },
      { events: [{ type: "agent.session.input.cancel" }] },
      { events: [{
        type: "agent.session.input.tool_result",
        call_id: "call",
        turn_id: "turn",
        success: false,
        error: null,
      }] },
    ]);
    expect(calls.map((call) => new Headers(call.init?.headers).get("Idempotency-Key")))
      .toEqual(["message-key", "cancel-key", "result-key"]);
  });

  it.each([
    ["message", (client: OpenAIAgentsClient) => client.sendMessage("session", "hello", "event-key")],
    ["cancel", (client: OpenAIAgentsClient) => client.cancelTurn("session", "event-key")],
    ["tool result", (client: OpenAIAgentsClient) => client.submitFunctionResult("session", {
      callId: "call_1",
      turnId: "turn_1",
      success: false,
      error: "safe failure",
    }, "event-key")],
  ])("requires HTTP 202 for %s event submission without retrying", async (_label, submit) => {
    const successCalls: FetchCall[] = [];
    const successClient = new OpenAIAgentsClient({
      fetch: recordingFetch(new Response(null, { status: 202 }), successCalls),
    });

    await expect(submit(successClient)).resolves.toBeUndefined();
    expect(successCalls).toHaveLength(1);

    for (const status of [200, 204]) {
      const calls: FetchCall[] = [];
      const client = new OpenAIAgentsClient({
        fetch: recordingFetch(status === 204 ? new Response(null, { status }) : jsonResponse({ accepted: true }, status), calls),
      });

      await expect(submit(client)).rejects.toMatchObject({
        status,
        message: `Agent core request failed (${status}).`,
      });
      expect(calls).toHaveLength(1);
    }
  });

  it("projects and routes the complete Vault and static bearer Credential contract", async () => {
    const calls: FetchCall[] = [];
    const createBearer = ephemeralBearer();
    const replacementBearer = ephemeralBearer();
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const credentialId = "22222222-2222-4222-8222-222222222222";
    const vault = { id: vaultId, object: "vault", created_at: 20, name: "Runtime", metadata: {} } as const;
    const credential = {
      id: credentialId,
      vault_id: vaultId,
      name: "Internal MCP",
      object: "vault.credential",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
      created_at: 21,
      updated_at: 21,
    } as const;
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        const path = new URL(String(input)).pathname;
        if (init?.method === "DELETE") {
          return path.endsWith(`/credentials/${credentialId}`)
            ? jsonResponse({ id: credentialId, object: "vault.credential.deleted", deleted: true })
            : jsonResponse({ id: vaultId, object: "vault.deleted", deleted: true });
        }
        if (path.endsWith(`/credentials/${credentialId}`)) return jsonResponse(credential);
        if (path.endsWith("/credentials")) {
          return init?.method === "POST"
            ? jsonResponse(credential, 201)
            : jsonResponse({ object: "list", data: [credential], has_more: false, first_id: credentialId, last_id: credentialId });
        }
        if (path.endsWith(`/vaults/${vaultId}`)) return jsonResponse(vault);
        return init?.method === "POST"
          ? jsonResponse(vault, 201)
          : jsonResponse({ object: "list", data: [vault], has_more: false, first_id: vaultId, last_id: vaultId });
      }) as typeof fetch,
    });

    await expect(client.listVaults({ limit: 100, order: "desc", status: ["active", "archived"] }))
      .resolves.toMatchObject({ data: [vault] });
    await expect(client.createVault({ name: "Runtime", metadata: {} })).resolves.toEqual(vault);
    await expect(client.retrieveVault(vaultId)).resolves.toEqual(vault);
    await expect(client.listVaultCredentials(vaultId, { limit: 100, order: "desc" }))
      .resolves.toMatchObject({ data: [credential] });
    await expect(client.createVaultCredential(vaultId, {
      name: "Internal MCP",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools", token: createBearer },
    })).resolves.toEqual(credential);
    await expect(client.retrieveVaultCredential(vaultId, credentialId)).resolves.toEqual(credential);
    await expect(client.replaceVaultCredentialToken(vaultId, credentialId, {
      auth: { type: "static_bearer", token: replacementBearer },
    })).resolves.toEqual(credential);
    await expect(client.deleteVaultCredential(vaultId, credentialId)).resolves.toEqual({
      id: credentialId, object: "vault.credential.deleted", deleted: true,
    });
    await expect(client.deleteVault(vaultId)).resolves.toEqual({ id: vaultId, object: "vault.deleted", deleted: true });

    expect(String(calls[0]?.input)).toContain("status%5B%5D=active&status%5B%5D=archived");
    const posts = calls.filter((call) => call.init?.method === "POST");
    expect(posts).toHaveLength(3);
    const createBody = JSON.parse(String(calls[4]?.init?.body)) as Record<string, unknown>;
    expect(createBody).toMatchObject({
      name: "Internal MCP",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
    });
    expect((createBody.auth as Record<string, unknown>).token === createBearer).toBe(true);
    const replaceBody = JSON.parse(String(posts[2]?.init?.body)) as Record<string, unknown>;
    expect((replaceBody.auth as Record<string, unknown>).token === replacementBearer).toBe(true);
    expect(JSON.stringify(credential).includes(createBearer)).toBe(false);
    expect(JSON.stringify(credential).includes(replacementBearer)).toBe(false);
    expect(calls.filter((call) => call.init?.method === "DELETE").every((call) => call.init?.body === undefined)).toBe(true);
  });

  it("accepts same-second Vault rows when the private timestamp tie-break is not visible", async () => {
    const first = { id: "ffffffff-ffff-4fff-8fff-ffffffffffff", object: "vault", created_at: 20, name: "First", metadata: {} };
    const second = { id: "11111111-1111-4111-8111-111111111111", object: "vault", created_at: 20, name: "Second", metadata: {} };
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({
        object: "list",
        data: [first, second],
        has_more: false,
        first_id: first.id,
        last_id: second.id,
      }), []),
    });

    await expect(client.listVaults({ limit: 100, order: "desc" }))
      .resolves.toMatchObject({ data: [first, second] });
  });

  it.each([
    { label: "unknown envelope field", body: { object: "list", data: [], has_more: false, first_id: null, last_id: null, extra: true } },
    { label: "duplicate identity", body: { object: "list", data: [
      { id: "11111111-1111-4111-8111-111111111111", object: "vault", created_at: 2, name: "One", metadata: {} },
      { id: "11111111-1111-4111-8111-111111111111", object: "vault", created_at: 1, name: "One again", metadata: {} },
    ], has_more: false, first_id: "11111111-1111-4111-8111-111111111111", last_id: "11111111-1111-4111-8111-111111111111" } },
    { label: "wrong boundary IDs", body: { object: "list", data: [
      { id: "11111111-1111-4111-8111-111111111111", object: "vault", created_at: 2, name: "One", metadata: {} },
    ], has_more: false, first_id: null, last_id: null } },
    { label: "over limit", body: { object: "list", data: [
      { id: "11111111-1111-4111-8111-111111111111", object: "vault", created_at: 2, name: "One", metadata: {} },
      { id: "22222222-2222-4222-8222-222222222222", object: "vault", created_at: 1, name: "Two", metadata: {} },
    ], has_more: false, first_id: "11111111-1111-4111-8111-111111111111", last_id: "22222222-2222-4222-8222-222222222222" } },
  ])("rejects malformed Vault list: $label", async ({ body, label }) => {
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(body), []) });
    const limit = label === "over limit" ? 1 : 100;
    await expect(client.listVaults({ limit })).rejects.toMatchObject({ status: 502, code: "invalid_vault_list" });
  });

  it("rejects malformed deletion receipts and accepts canonical Core IDs for uppercase request paths", async () => {
    const uppercase = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA";
    const canonical = uppercase.toLowerCase();
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: (async (input, init) => {
      calls.push({ input, init });
      return init?.method === "DELETE"
        ? jsonResponse({ id: canonical, object: "vault.deleted", deleted: true })
        : jsonResponse({ id: canonical, object: "vault", created_at: 1, name: "Safe", metadata: {} });
    }) as typeof fetch });
    await expect(client.retrieveVault(uppercase)).resolves.toMatchObject({ id: canonical });
    await expect(client.deleteVault(uppercase)).resolves.toEqual({ id: canonical, object: "vault.deleted", deleted: true });

    const malformed = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({
      id: canonical, object: "vault.deleted", deleted: true, status: "deleted",
    }), []) });
    await expect(malformed.deleteVault(canonical)).rejects.toMatchObject({ status: 502, code: "invalid_vault_deletion" });
  });

  it.each([
    { label: "wrong parent", mutate: (value: Record<string, unknown>) => ({ ...value, vault_id: "33333333-3333-4333-8333-333333333333" }) },
    { label: "wrong identity", mutate: (value: Record<string, unknown>) => ({ ...value, id: "33333333-3333-4333-8333-333333333333" }) },
    { label: "invalid timestamps", mutate: (value: Record<string, unknown>) => ({ ...value, updated_at: 0 }) },
    { label: "unsafe URL", mutate: (value: Record<string, unknown>) => ({ ...value, auth: { type: "static_bearer", mcp_server_url: "http://mcp.example" } }) },
  ])("rejects Credential metadata with $label", async ({ mutate }) => {
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const credentialId = "22222222-2222-4222-8222-222222222222";
    const valid: Record<string, unknown> = {
      id: credentialId, vault_id: vaultId, name: "MCP", object: "vault.credential",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
      created_at: 2, updated_at: 3,
    };
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(mutate(valid)), []) });
    await expect(client.retrieveVaultCredential(vaultId, credentialId))
      .rejects.toMatchObject({ status: 502, code: "invalid_vault_credential" });
  });

  it.each(["vault", "credential"] as const)("rejects %s responses that expose secret or unknown fields", async (kind) => {
    const responseBearer = ephemeralBearer();
    const body = kind === "vault"
      ? { id: "11111111-1111-4111-8111-111111111111", object: "vault", created_at: 1, name: "Safe", metadata: {}, token: responseBearer }
      : { id: "22222222-2222-4222-8222-222222222222", vault_id: "11111111-1111-4111-8111-111111111111", name: "Safe", object: "vault.credential", auth: { type: "static_bearer", mcp_server_url: "https://mcp.example", token: responseBearer }, created_at: 1, updated_at: 1 };
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(body), calls) });
    const operation = kind === "vault"
      ? client.retrieveVault("11111111-1111-4111-8111-111111111111")
      : client.retrieveVaultCredential(
        "11111111-1111-4111-8111-111111111111",
        "22222222-2222-4222-8222-222222222222",
      );
    await expect(operation).rejects.toMatchObject({ status: 502 });
    expect(calls).toHaveLength(1);
  });

  it.each([
    { label: "name", change: { name: "Different" } },
    { label: "destination", change: { auth: { type: "static_bearer", mcp_server_url: "https://other.example/tools" } } },
  ])("rejects a created Credential whose $label does not match the request", async ({ change }) => {
    const calls: FetchCall[] = [];
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const createBearer = ephemeralBearer();
    const credential = {
      id: "22222222-2222-4222-8222-222222222222",
      vault_id: vaultId,
      name: "Internal MCP",
      object: "vault.credential",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
      created_at: 2,
      updated_at: 2,
      ...change,
    };
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(credential, 201), calls) });

    await expect(client.createVaultCredential(vaultId, {
      name: "Internal MCP",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools", token: createBearer },
    })).rejects.toMatchObject({ status: 502, code: "invalid_vault_credential" });
    expect(calls).toHaveLength(1);
  });

  it.each([
    { label: "name", change: { name: "Different" } },
    { label: "destination", change: { auth: { type: "static_bearer", mcp_server_url: "https://other.example/tools" } } },
    { label: "creation timestamp", change: { created_at: 1 } },
    { label: "update timestamp", change: { updated_at: 1 } },
  ])("rejects a replaced Credential whose $label changed", async ({ change }) => {
    const calls: FetchCall[] = [];
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const credentialId = "22222222-2222-4222-8222-222222222222";
    const replacementBearer = ephemeralBearer();
    const baseline = {
      id: credentialId,
      vault_id: vaultId,
      name: "Internal MCP",
      object: "vault.credential",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
      created_at: 2,
      updated_at: 2,
    };
    const client = new OpenAIAgentsClient({
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        return jsonResponse(init?.method === "POST" ? { ...baseline, updated_at: 3, ...change } : baseline);
      }) as typeof fetch,
    });

    await expect(client.replaceVaultCredentialToken(vaultId, credentialId, {
      auth: { type: "static_bearer", token: replacementBearer },
    })).rejects.toMatchObject({ status: 502, code: "invalid_vault_credential" });
    expect(calls.filter((call) => call.init?.method === "POST")).toHaveLength(1);
  });

  it("returns a fixed safe Credential-create error without parsing a secret-echoing body", async () => {
    const calls: FetchCall[] = [];
    const createBearer = ephemeralBearer();
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({ error: {
        code: createBearer,
        message: createBearer,
        param: createBearer,
        type: createBearer,
      } }, 503), calls),
    });
    const error = await client.createVaultCredential("11111111-1111-4111-8111-111111111111", {
      name: "Internal MCP",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example", token: createBearer },
    }).then(() => null, (reason: unknown) => reason as AgentCoreError);
    expect(error).toMatchObject({
      status: 503,
      code: "credential_write_failed",
      message: "Agent Core Credential creation failed.",
      param: undefined,
      errorType: undefined,
    });
    expect([error?.message, error?.code, error?.param, error?.errorType].join(" ")).not.toContain(createBearer);
    expect(calls).toHaveLength(1);
  });

  it("returns a fixed safe Credential-replacement error after one metadata read and one write", async () => {
    const calls: FetchCall[] = [];
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const credentialId = "22222222-2222-4222-8222-222222222222";
    const replacementBearer = ephemeralBearer();
    const baseline = {
      id: credentialId,
      vault_id: vaultId,
      name: "Internal MCP",
      object: "vault.credential",
      auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
      created_at: 2,
      updated_at: 2,
    };
    const client = new OpenAIAgentsClient({
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        return init?.method === "POST"
          ? jsonResponse({ error: {
              code: replacementBearer,
              message: replacementBearer,
              param: replacementBearer,
              type: replacementBearer,
            } }, 503)
          : jsonResponse(baseline);
      }) as typeof fetch,
    });

    const error = await client.replaceVaultCredentialToken(vaultId, credentialId, {
      auth: { type: "static_bearer", token: replacementBearer },
    }).then(() => null, (reason: unknown) => reason as AgentCoreError);
    expect(error).toMatchObject({
      status: 503,
      code: "credential_write_failed",
      message: "Agent Core Credential token replacement failed.",
      param: undefined,
      errorType: undefined,
    });
    expect([error?.message, error?.code, error?.param, error?.errorType].join(" ")).not.toContain(replacementBearer);
    expect(calls).toHaveLength(2);
    expect(calls.filter((call) => call.init?.method === "POST")).toHaveLength(1);
  });

  it("sends deterministic Session vault_ids and rejects mismatched Core attachments", async () => {
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const session = {
      id: "session",
      object: "agent.session",
      agent: agentSnapshot(),
      environment: { type: "none" },
      status: "idle",
      error: null,
      metadata: {},
      required_actions: [],
      vault_ids: [vaultId],
      usage: null,
      created_at: 1,
      last_active_at: 1,
    };
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(session), calls) });
    await expect(client.createSession({ environment: { type: "none" }, stream: false, vault_ids: [vaultId] }, "key"))
      .resolves.toMatchObject({ vault_ids: [vaultId] });
    expect(JSON.parse(String(calls[0]?.init?.body)).vault_ids).toEqual([vaultId]);

    const mismatched = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({ ...session, vault_ids: [] }), []) });
    await expect(mismatched.createSession({ environment: { type: "none" }, vault_ids: [vaultId] }))
      .rejects.toMatchObject({ status: 502, code: "invalid_session_vaults" });
  });

  it.each([null, 7, "invalid", {}])("turns malformed Session list %j into a typed 502", async (body) => {
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(body), []) });
    await expect(client.listSessions()).rejects.toMatchObject({
      status: 502,
      code: "invalid_session_vaults",
    });
  });

  it("accepts Core-valid uppercase and repeated Session Vault IDs from other clients", async () => {
    const uppercase = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA";
    const resource = sessionResource([uppercase, uppercase]);
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(resource), []) });
    await expect(client.retrieveSession("session")).resolves.toMatchObject({ vault_ids: [uppercase, uppercase] });

    const listed = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({ data: [resource], has_more: false }), []) });
    await expect(listed.listSessions()).resolves.toMatchObject({ data: [{ vault_ids: [uppercase, uppercase] }] });
  });

  it.each([
    { label: "retrieve", run: (client: OpenAIAgentsClient) => client.retrieveSession("session") },
    { label: "update", run: (client: OpenAIAgentsClient) => client.updateSession("session", {}) },
    { label: "list", run: (client: OpenAIAgentsClient) => client.listSessions() },
  ])("rejects missing or invalid vault_ids from Session $label projection", async ({ label, run }) => {
    const invalid = label === "list"
      ? { data: [sessionResource(["00000000-0000-0000-0000-000000000000"])], has_more: false }
      : sessionResource(["00000000-0000-0000-0000-000000000000"]);
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(invalid), []) });
    await expect(run(client)).rejects.toMatchObject({ status: 502, code: "invalid_session_vaults" });
  });

  it("rejects an embedded streamed Session with invalid Vault attachments before forwarding", async () => {
    const onEvent = vi.fn();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(`event: agent.session.idle\ndata: ${JSON.stringify({
          type: "agent.session.idle",
          event_id: "evt",
          session: sessionResource(["not-a-uuid"]),
        })}\n\n`));
        controller.close();
      },
    });
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(new Response(body), []) });
    await expect(client.streamEvents("session", { onEvent })).rejects.toMatchObject({
      status: 502,
      code: "invalid_session_vaults",
    });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("rejects stream=true before the JSON create method performs a request", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      fetch: recordingFetch(jsonResponse({}), calls),
    });

    await expect(
      client.createSession({ environment: { type: "none" }, stream: true } as never),
    ).rejects.toThrow("createSession only supports the JSON response");
    expect(calls).toHaveLength(0);
  });

  it("retrieves a Runtime observation, preserves observed zeroes, and encodes the Session ID", async () => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1",
      fetch: recordingFetch(jsonResponse(runtimeObservation()), calls),
    });

    await expect(client.retrieveRuntimeObservation(runtimeSessionId)).resolves.toMatchObject({
      id: runtimeSessionId,
      cpu: { usage_seconds_total: 0 },
      memory: { usage_bytes: 0 },
    });
    expect(String(calls[0]?.input)).toBe(
      `https://core.example/v1/agents/sessions/${runtimeSessionId}/runtime-observation`,
    );
  });

  it("lists Runtime observations with stable pagination metadata and query serialization", async () => {
    const calls: FetchCall[] = [];
    const body = {
      object: "list",
      data: [runtimeObservation()],
      has_more: true,
      first_id: runtimeSessionId,
      last_id: runtimeSessionId,
    };
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      fetch: recordingFetch(jsonResponse(body), calls),
    });

    await expect(client.listRuntimeObservations({
      after: runtimeSessionId, limit: 1, order: "asc",
    })).resolves.toMatchObject(body);
    expect(String(calls[0]?.input)).toBe(
      `https://core.example/v1/agents/runtime-observations?after=${runtimeSessionId}&limit=1&order=asc`,
    );
  });

  it("accepts an unsupported none-mode Runtime observation with explicit nulls", async () => {
    const value = runtimeObservation({
      environment_id: null,
      mode: "none",
      provider_type: null,
      instance: { kind: "none", allocation_id: null, device_id: null, connection_generation: null },
      status: "unsupported",
      reason: "runtime_mode_not_observable",
      allocation_created_at: null,
      observed_at: null,
      started_at: null,
      cpu: null,
      memory: null,
    });
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(value), []) });
    await expect(client.retrieveRuntimeObservation(runtimeSessionId)).resolves.toMatchObject(value);
  });

  it.each([
    ["unknown field", () => ({ ...runtimeObservation(), provider_native_id: "hidden" })],
    ["foreign Session", () => ({ ...runtimeObservation(), session_id: "55555555-5555-4555-8555-555555555555" })],
    ["invalid status/reason", () => ({ ...runtimeObservation(), status: "observed", reason: "sample_timeout" })],
    ["invalid mode/instance", () => ({ ...runtimeObservation(), mode: "none" })],
    ["negative CPU", () => ({ ...runtimeObservation(), cpu: {
      usage_seconds_total: -1, capacity_cores: 2, usage_cores: null, utilization_ratio: null,
    } })],
    ["non-numeric CPU", () => ({ ...runtimeObservation(), cpu: {
      usage_seconds_total: "NaN", capacity_cores: 2, usage_cores: null, utilization_ratio: null,
    } })],
    ["zero CPU capacity", () => ({ ...runtimeObservation(), cpu: {
      usage_seconds_total: 1, capacity_cores: 0, usage_cores: null, utilization_ratio: null,
    } })],
    ["unsafe memory", () => ({ ...runtimeObservation(), memory: {
      usage_bytes: Number.MAX_SAFE_INTEGER + 1, limit_bytes: 1024,
    } })],
    ["zero memory limit", () => ({ ...runtimeObservation(), memory: {
      usage_bytes: 1, limit_bytes: 0,
    } })],
  ])("rejects a Runtime observation with %s", async (_label, build) => {
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(build()), []) });
    await expect(client.retrieveRuntimeObservation(runtimeSessionId)).rejects.toMatchObject({
      status: 502,
      code: "invalid_runtime_observation",
    });
  });

  it.each([
    ["mismatched first_id", {
      object: "list", data: [runtimeObservation()], has_more: false,
      first_id: runtimeEnvironmentId, last_id: runtimeSessionId,
    }],
    ["duplicate IDs", {
      object: "list", data: [runtimeObservation(), runtimeObservation()], has_more: false,
      first_id: runtimeSessionId, last_id: runtimeSessionId,
    }],
    ["empty continuation", {
      object: "list", data: [], has_more: true, first_id: null, last_id: null,
    }],
  ])("rejects a Runtime observation list with %s", async (_label, body) => {
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse(body), []) });
    await expect(client.listRuntimeObservations()).rejects.toMatchObject({
      status: 502,
      code: "invalid_runtime_observation",
    });
  });

  it.each([
    { after: "not-a-uuid" },
    { limit: 0 },
    { limit: 101 },
    { order: "sideways" },
  ])("rejects invalid Runtime observation pagination before fetch", async (options) => {
    const calls: FetchCall[] = [];
    const client = new OpenAIAgentsClient({ fetch: recordingFetch(jsonResponse({}), calls) });
    await expect(client.listRuntimeObservations(options as never)).rejects.toThrow(TypeError);
    expect(calls).toHaveLength(0);
  });
});
