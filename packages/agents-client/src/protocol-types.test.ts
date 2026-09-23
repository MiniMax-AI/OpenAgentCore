import { describe, expect, expectTypeOf, it } from "vitest";

import fixture from "./fixtures/parsar-8cc2898c/environment-protocol.json";
import environmentResources from "./fixtures/parsar-0438880a/environment-resources.json";
import environmentFiles from "./fixtures/parsar-c31f8167/environment-files.json";
import files182d from "./fixtures/parsar-182d333d/files.json";
import hostedDadf64 from "./fixtures/parsar-dadf64a7/openai-hosted.json";
import selfHostedFilesDadf64 from "./fixtures/parsar-dadf64a7/environment-files-self-hosted.json";
import eventBatchDadf64 from "./fixtures/parsar-dadf64a7/session-event-batch.json";
import vaultCredentials182d from "./fixtures/parsar-182d333d/vault-credentials.json";
import turnResources from "./fixtures/parsar-0438880a/turn-resources.json";
import toolProfiles from "./fixtures/parsar-2b34ea46/tool-profiles.json";
import type {
  AnonymousHttpMcpToolInput,
  AgentsCoreSelection,
  SavedAgentCoreInput,
  SavedAgentCore,
  SavedAgent,
  AgentSnapshot,
  ModelProviderInput,
  ModelProviderView,
  AgentCore,
  AgentSession,
  AgentEnvironmentResource,
  AgentEnvironment,
  AgentTurn,
  AgentSessionEnvironmentEvent,
  CreateAgentInput,
  EnvironmentConnectionAction,
  EnvironmentFileList,
  EnvironmentFileCreateInput,
  EnvironmentResourceStatus,
  FunctionCallAction,
  InlineAgentInput,
  OpenAIHostedAgentEnvironment,
  OpenAIHostedAgentEnvironmentInput,
  OpenAIHostedAgentEnvironmentResource,
  RequiredAction,
  RuntimeObservation,
  RuntimeUnavailableReason,
  SelfHostedAgentEnvironment,
  SavedAgentToolInput,
  SourceFile,
  SourceFileDeleted,
  UnknownAgentEnvironment,
  UnknownSessionEvent,
  UnknownSessionItem,
  SessionItem,
  SessionInputEvent,
  SessionMessageInputEvent,
  SessionToolResultInputEvent,
  SessionEnvironmentStatus,
  TokenUsage,
  TurnStatus,
  UpdateAgentInput,
  Vault,
  VaultCredential,
} from "./types";

describe("Runtime Observation discriminated contract", () => {
  it("narrows status, reason, mode, instance, and sample presence together", () => {
    type Observed = Extract<RuntimeObservation, { status: "observed" }>;
    type Unavailable = Extract<RuntimeObservation, { status: "unavailable" }>;
    type NoneMode = Extract<RuntimeObservation, { mode: "none" }>;
    type SelfHosted = Extract<RuntimeObservation, { mode: "self_hosted" }>;

    expectTypeOf<Observed["mode"]>().toEqualTypeOf<"openai_hosted">();
    expectTypeOf<Observed["reason"]>().toEqualTypeOf<null>();
    expectTypeOf<Observed["observed_at"]>().toEqualTypeOf<number>();
    expectTypeOf<Observed["instance"]["allocation_id"]>().toEqualTypeOf<string>();
    expectTypeOf<Unavailable["reason"]>().toEqualTypeOf<RuntimeUnavailableReason>();
    expectTypeOf<Unavailable["observed_at"]>().toEqualTypeOf<null>();
    expectTypeOf<NoneMode["environment_id"]>().toEqualTypeOf<null>();
    expectTypeOf<NoneMode["instance"]["kind"]>().toEqualTypeOf<"none">();
    expectTypeOf<SelfHosted["instance"]["kind"]>().toEqualTypeOf<"self_hosted_connection">();
  });
});

describe("Parsar dadf64a7 basic managed Environment profile", () => {
  it("pins omitted/default, explicit-enabled, and explicit-disabled network input", () => {
    const inputs = hostedDadf64.inputs as Record<string, OpenAIHostedAgentEnvironmentInput>;

    expect(hostedDadf64.revision).toBe("dadf64a76bde58255281f3b6c3e939f8b556be09");
    expect(inputs.default_enabled).toEqual({ type: "openai_hosted" });
    expect(inputs.explicit_enabled?.network?.access).toBe("enabled");
    expect(inputs.explicit_disabled?.network?.access).toBe("disabled");
    expectTypeOf<NonNullable<OpenAIHostedAgentEnvironmentInput["network"]>["access"]>()
      .toEqualTypeOf<"enabled" | "disabled">();
  });

  it("pins the exact empty-installation Session output separately from the seven-field resource", () => {
    const environment = hostedDadf64.session_environment as OpenAIHostedAgentEnvironment;
    const resource = hostedDadf64.environment_resource as AgentEnvironmentResource;

    expect(Object.keys(environment).sort()).toEqual([
      "capability_directories", "files", "id", "network", "packages", "plugins", "skills", "type",
    ]);
    expect(environment.network).toEqual({ access: "enabled", allowed_domains: [] });
    expect(environment.packages).toEqual({ npm: [], python: [], system: [] });
    expect(Object.keys(resource).sort()).toEqual([
      "files", "id", "object", "plugins", "skills", "status", "type",
    ]);
    expectTypeOf<OpenAIHostedAgentEnvironmentResource["files"]>().toEqualTypeOf<[]>();
    expectTypeOf<OpenAIHostedAgentEnvironmentResource["plugins"]>().toEqualTypeOf<[]>();
    expectTypeOf<OpenAIHostedAgentEnvironmentResource["skills"]>().toEqualTypeOf<[]>();
  });
});

describe("Parsar dadf64a7 ordered Session input batch", () => {
  it("pins the three exact public wire variants and their ordered nested inputs", () => {
    const events = eventBatchDadf64.request.events as SessionInputEvent[];
    const message = events[0] as SessionMessageInputEvent;
    const result = events[2] as SessionToolResultInputEvent;

    expect(eventBatchDadf64.revision).toBe("dadf64a76bde58255281f3b6c3e939f8b556be09");
    expect(events.map((event) => event.type)).toEqual([
      "agent.session.input.message",
      "agent.session.input.cancel",
      "agent.session.input.tool_result",
    ]);
    expect(message.input.map((input) => input.content.map((part) => part.text).join(""))).toEqual([
      "First message",
      "Second message",
    ]);
    expect(result.output).toEqual([
      { type: "input_text", text: "" },
      { type: "input_image", image_url: "data:image/png;base64,AA==" },
    ]);
    expectTypeOf<SessionMessageInputEvent["input"]>().toEqualTypeOf<import("./types").InputMessage[]>();
    expectTypeOf<SessionToolResultInputEvent["output"]>()
      .toEqualTypeOf<string | import("./types").FunctionResultContent[] | null | undefined>();
  });

  it("requires caller-owned idempotency keys for every event write helper", () => {
    expectTypeOf<Parameters<AgentCore["submitEvents"]>[2]>().toEqualTypeOf<string>();
    expectTypeOf<Parameters<AgentCore["sendMessage"]>[2]>().toEqualTypeOf<string>();
    expectTypeOf<Parameters<AgentCore["cancelTurn"]>[1]>().toEqualTypeOf<string>();
    expectTypeOf<Parameters<AgentCore["submitFunctionResult"]>[2]>().toEqualTypeOf<string>();
  });
});

describe("Parsar 2b34ea46 bounded tool profiles", () => {
  it("keeps saved-only tools in create/update while limiting inline execution profiles", () => {
    expectTypeOf<CreateAgentInput["tools"]>().toEqualTypeOf<SavedAgentToolInput[] | null | undefined>();
    expectTypeOf<UpdateAgentInput["tools"]>().toEqualTypeOf<SavedAgentToolInput[] | null | undefined>();
    expectTypeOf<NonNullable<InlineAgentInput["tools"]>[number]>().not.toEqualTypeOf<SavedAgentToolInput>();
  });

  it("captures the two Web-configurable write shapes without credentials or browser MCP", () => {
    const functionTool = toolProfiles.write_profiles.function;
    const mcpTool = toolProfiles.write_profiles.anonymous_http_mcp as AnonymousHttpMcpToolInput;

    expect(functionTool.defer_loading).toBe(false);
    expect(functionTool.parameters.type).toBe("object");
    expect(mcpTool).toEqual({
      type: "mcp",
      server_label: "docs",
      transport: { type: "http", server_url: "https://mcp.example/tools" },
      allowed_tools: [],
      connection_origin: "service",
      required: true,
    });
    expect("credential_id" in mcpTool).toBe(false);
    expect("headers" in mcpTool.transport).toBe(false);
  });

  it("pins Function identity and nullable MCP result fields without inventing Function errors", () => {
    const items = Object.values(toolProfiles.items) as SessionItem[];
    const functions = items.filter((item) => item.type === "function_call");
    const mcp = items.filter((item) => item.type === "mcp_call");

    expect(items.map((item) => [item.type, item.status])).toEqual([
      ["function_call", "completed"],
      ["function_call", "failed"],
      ["mcp_call", "completed"],
      ["mcp_call", "failed"],
    ]);
    expect(functions.every((item) => item.call_id === item.id)).toBe(true);
    expect(functions.every((item) => !("error" in item))).toBe(true);
    expect(mcp.map((item) => [item.output, item.error])).toEqual([
      [{ title: "Guide" }, null],
      [null, { message: "Core could not call the MCP server." }],
    ]);
  });
});

describe("Core harness selection extension", () => {
  it("separates saved provider inputs and safe reads from Session harness selection", () => {
    expectTypeOf<CreateAgentInput["x_agents_core"]>().toEqualTypeOf<SavedAgentCoreInput | null | undefined>();
    expectTypeOf<UpdateAgentInput["x_agents_core"]>().toEqualTypeOf<SavedAgentCoreInput | null | undefined>();
    expectTypeOf<SavedAgent["x_agents_core"]>().toEqualTypeOf<SavedAgentCore | null | undefined>();
    expectTypeOf<AgentSnapshot["x_agents_core"]>().toEqualTypeOf<AgentsCoreSelection | null | undefined>();
    expectTypeOf<ModelProviderView>().not.toExtend<ModelProviderInput>();
    expectTypeOf<ModelProviderInput>().not.toExtend<ModelProviderView>();
    expectTypeOf<SavedAgentCore>().not.toExtend<SavedAgentCoreInput>();
    expectTypeOf<InlineAgentInput["x_agents_core"]>().toEqualTypeOf<AgentsCoreSelection | null | undefined>();
  });
});

describe("Parsar c31f8167 Environment files list", () => {
  it("pins the direct-file metadata page and opaque cursor shape", () => {
    const first = environmentFiles.pages[0] as EnvironmentFileList;
    const final = environmentFiles.pages[1] as EnvironmentFileList;

    expect(environmentFiles.revision).toBe("c31f81677a8b16c53b665de9075181df837a0032");
    expect(first.data.map((file) => [file.path, file.size_bytes])).toEqual([
      ["/workspace/project/README.md", 128],
      ["/workspace/project/report.json", 2048],
    ]);
    expect(first.next).toBe("opaque-page-token");
    expect(final).toEqual({ data: [], next: null });
  });
});

describe("Parsar dadf64a7 self-hosted Environment files list", () => {
  it("pins the physical workspace_directory as the current self-hosted API root", () => {
    const page = selfHostedFilesDadf64.page as EnvironmentFileList;

    expect(selfHostedFilesDadf64.revision).toBe("dadf64a76bde58255281f3b6c3e939f8b556be09");
    expect(selfHostedFilesDadf64.workspace_directory).toBe("/test");
    expect(selfHostedFilesDadf64.request.path).toBe("/test");
    expect(page.data.map((file) => file.path)).toEqual(["/test/README.md"]);
  });
});

describe("Parsar 182d333d Source and Environment Files", () => {
  it("pins the complete Source File metadata and deletion shapes", () => {
    const file = files182d.source_file as SourceFile;
    const deleted = files182d.deleted as SourceFileDeleted;

    expect(files182d.revision).toBe("182d333db6681e3e1ea7de26672f43daceef793d");
    expect(Object.keys(file).sort()).toEqual([
      "bytes", "created_at", "expires_at", "filename", "id", "object", "purpose", "status", "status_details",
    ]);
    expect(file.purpose).toBe("user_data");
    expect(file.expires_at).toBeNull();
    expect(deleted).toEqual({ id: file.id, object: "file", deleted: true });
  });

  it("pins file_id copy separately from local paths and hosted discovery", () => {
    const request = files182d.environment_create.request as EnvironmentFileCreateInput;
    const hosted = files182d.hosted_environment as AgentEnvironmentResource;

    expect(request).toEqual({
      type: "file_id",
      file_id: files182d.source_file.id,
      path: "/workspace/input/notes.txt",
    });
    expect(request.type === "file_id" ? request.file_id : "").toMatch(/^file-/);
    expect(hosted.type).toBe("openai_hosted");
    expect(hosted.status).toBe("connected");
  });
});

describe("Parsar 182d333d Vault Credentials and Session attachments", () => {
  it("pins safe Vault and Credential metadata without a token field", () => {
    const vault = vaultCredentials182d.vault_list.data[0] as Vault;
    const credential = vaultCredentials182d.credential_list.data[0] as VaultCredential;

    expect(vaultCredentials182d.revision).toBe("182d333db6681e3e1ea7de26672f43daceef793d");
    expect(vault.object).toBe("vault");
    expect(credential.vault_id).toBe(vault.id);
    expect(credential.auth).toEqual({
      type: "static_bearer",
      mcp_server_url: "https://mcp.example.com/endpoint",
    });
    expect("token" in credential.auth).toBe(false);
    expect(vaultCredentials182d.write_evidence).toEqual({
      credential_token_present: true,
      credential_token_returned: false,
      credential_token_recorded: false,
    });
  });

  it("pins the owning Vault attachment on the public Session resource", () => {
    const session = vaultCredentials182d.session as AgentSession;
    expect(session.vault_ids).toEqual([vaultCredentials182d.vault_list.data[0]?.id]);
    expect(session.environment).toEqual({ type: "none" });
  });
});

describe("Parsar 8cc2898c Environment protocol types", () => {
  it("models none and the safe self_hosted Session response projection", () => {
    const none = fixture.environments.none as AgentEnvironment;
    const selfHosted = fixture.environments.self_hosted as SelfHostedAgentEnvironment;

    expect(none).toEqual({ type: "none" });
    expect(selfHosted).toEqual({
      type: "self_hosted",
      id: "environment_01",
      remote_url: "https://executor.example.test",
      workspace_directory: "/workspace/project",
      capability_directories: [],
    });
  });

  it("models both required action variants without inventing shared fields", () => {
    const actions = fixture.required_actions as RequiredAction[];
    const functionCall = actions[0] as FunctionCallAction;
    const environmentConnection = actions[1] as EnvironmentConnectionAction;

    expect(functionCall.type).toBe("function_call");
    expect(functionCall.arguments).toEqual({ query: "fixture" });
    expect(environmentConnection).toEqual({
      type: "environment_connection",
      environment_id: "environment_01",
    });
    expect("call_id" in environmentConnection).toBe(false);
  });

  it("covers every pinned Environment event state and the nullable safe error", () => {
    const events = fixture.environment_events as AgentSessionEnvironmentEvent[];

    expect(events.map((event) => event.environment.status)).toEqual([
      "pending",
      "ready",
      "connected",
      "disconnected",
      "failed",
    ] satisfies SessionEnvironmentStatus[]);
    expect(events[4]?.environment.error).toEqual({
      code: "environment_failed",
      type: "environment_error",
      message: "The Environment could not become available.",
    });
    expectTypeOf<AgentSessionEnvironmentEvent["type"]>().toEqualTypeOf<
      `agent.session.environment.${SessionEnvironmentStatus}`
    >();
  });

  it("keeps unknown Environment, Item, and Event payloads inspectable", () => {
    const environment = fixture.environments.unknown as unknown as UnknownAgentEnvironment;
    const item = fixture.unknown_item as unknown as UnknownSessionItem;
    const event = fixture.unknown_event as unknown as UnknownSessionEvent;
    const expired = fixture.unknown_expired_event as unknown as UnknownSessionEvent;

    expect(environment.type).toBe("future_remote");
    expect(environment.contract_marker).toBe("preserved");
    expect(item.type).toBe("computer_use_call");
    expect(item.contract_marker).toBe("preserved");
    expect(event.type).toBe("agent.session.environment.paused");
    expect(event.contract_marker).toBe("preserved");
    expect(expired.type).toBe("agent.session.environment.expired");
    expect(expired.contract_marker).toBe("unsupported_session_event_status");
  });
});

describe("Parsar 0438880a Environment retrieve resource", () => {
  it("models every durable resource status independently from live ready", () => {
    const resources = environmentResources.resources as AgentEnvironmentResource[];
    expect(resources.map((resource) => resource.status)).toEqual([
      "pending",
      "connected",
      "disconnected",
      "expired",
      "failed",
    ] satisfies EnvironmentResourceStatus[]);
    for (const resource of resources) {
      expect(Object.keys(resource).sort()).toEqual([
        "files", "id", "object", "plugins", "skills", "status", "type",
      ]);
      expect(resource.files).toEqual([]);
      expect(resource.plugins).toEqual([]);
      expect(resource.skills).toEqual([]);
    }
    expectTypeOf<EnvironmentResourceStatus>().not.toEqualTypeOf<SessionEnvironmentStatus>();
  });

  it("retains raw malformed and unsupported fixtures as untrusted test inputs", () => {
    expect(environmentResources.unsupported.ready.status).toBe("ready");
    expect(environmentResources.unsupported.unknown_type.type).toBe("openai_hosted");
    expect("skills" in environmentResources.malformed.missing_skills).toBe(false);
  });

  it("pins uppercase UUID lookup to the canonical response identity", () => {
    const retrieval = environmentResources.canonical_retrieve;
    const resource = retrieval.response as AgentEnvironmentResource;

    expect(retrieval.request_id.toLowerCase()).toBe(resource.id);
    expect(resource.object).toBe("agent.environment");
  });
});

describe("Parsar 0438880a Turn observability resources", () => {
  it("models every durable lifecycle state and nullable measurements", () => {
    const turns = turnResources.turns as AgentTurn[];

    expect(turns.map((turn) => turn.status)).toEqual([
      "queued",
      "in_progress",
      "waiting",
      "completed",
      "failed",
      "cancelled",
    ] satisfies TurnStatus[]);
    expect(turns[0]?.started_at).toBeNull();
    expect(turns[0]?.usage).toBeNull();
    expect(turns[3]?.completed_at).toBe(1700000068);
    expect(turns[4]?.error).toEqual({
      code: "internal_error",
      message: "The execution could not complete.",
    });
  });

  it("keeps aggregate Session Usage distinct from one Turn measurement", () => {
    const turns = turnResources.turns as AgentTurn[];
    const aggregate = turnResources.session_usage as TokenUsage;

    expect(turns[3]?.usage?.total_tokens).toBe(13);
    expect(aggregate.total_tokens).toBe(26);
    expect(aggregate.input_tokens_details.cached_tokens).toBe(8);
    expect(aggregate.output_tokens_details.reasoning_tokens).toBe(4);
  });
});
