import { describe, expect, it } from "vitest";

import {
  beginSessionCreateAttempt,
  sessionCreateFingerprint,
  sessionCreateRequestPayload,
} from "./session-create-attempt";

describe("Session create attempts", () => {
  it("reuses the original key only while the exact request stays unchanged", () => {
    const draft = {
      agentId: "agent-a",
      environment: { type: "none" } as const,
      metadata: {},
      stream: false,
    };
    const first = beginSessionCreateAttempt(draft, null, () => "key-1");
    const retry = beginSessionCreateAttempt(draft, first, () => "key-2");
    const changedAgent = beginSessionCreateAttempt({ ...draft, agentId: "agent-b" }, first, () => "key-3");
    const changedEnvironment = beginSessionCreateAttempt({
      agentId: "agent-a",
      environment: {
        type: "self_hosted" as const,
        workspace_directory: "/workspace",
        capability_directories: [],
      },
      metadata: {},
      stream: false,
    }, first, () => "key-4");

    expect(first.idempotencyKey).toBe("key-1");
    expect(retry.idempotencyKey).toBe("key-1");
    expect(changedAgent.idempotencyKey).toBe("key-3");
    expect(changedEnvironment.idempotencyKey).toBe("key-4");
  });

  it("fingerprints the exact payload sent to Core", () => {
    expect(sessionCreateFingerprint({
      agentId: "agent-a",
      environment: {
        type: "self_hosted",
        workspace_directory: "/workspace/project",
        capability_directories: [],
      },
      metadata: {},
      stream: false,
    })).toBe(JSON.stringify({
      agent_id: "agent-a",
      environment: {
        capability_directories: [],
        type: "self_hosted",
        workspace_directory: "/workspace/project",
      },
      metadata: {},
      stream: false,
      vault_ids: [],
    }));
  });

  it("omits agent_id for a standalone inline Agent", () => {
    const draft = {
      agent: { model: "provider/inline", instructions: "Work." },
      environment: { type: "none" as const },
      metadata: {},
      stream: true,
    };
    const request = sessionCreateRequestPayload(draft);
    const fingerprint = sessionCreateFingerprint(draft);
    expect(request).toEqual({
      agent: { model: "provider/inline", instructions: "Work." },
      environment: { type: "none" },
      metadata: {},
      vault_ids: [],
    });
    expect(JSON.stringify({ ...request, stream: true })).not.toContain("agent_id");
    expect(JSON.parse(fingerprint)).toEqual({
      agent: { instructions: "Work.", model: "provider/inline" },
      environment: { type: "none" },
      metadata: {},
      stream: true,
      vault_ids: [],
    });
    expect(fingerprint).not.toContain("agent_id");
  });

  it("recursively stabilizes object key order while preserving array order", () => {
    const first = {
      agentId: "agent-a",
      agent: {
        model: "provider/model",
        reasoning: { summary: "auto" as const, effort: "low" as const },
      },
      environment: { type: "none" } as const,
      metadata: { zeta: "last", alpha: "first" },
      stream: false,
    };
    const reordered = {
      ...first,
      agent: {
        reasoning: { effort: "low" as const, summary: "auto" as const },
        model: "provider/model",
      },
      metadata: { alpha: "first", zeta: "last" },
    };
    const reorderedArrays = {
      ...first,
      agent: {
        tools: [
          { type: "function" as const, name: "second", description: "", parameters: {} },
          { type: "function" as const, name: "first", description: "", parameters: {} },
        ],
      },
    };
    const originalArrays = {
      ...reorderedArrays,
      agent: { tools: [...reorderedArrays.agent.tools].reverse() },
    };

    expect(sessionCreateFingerprint(reordered)).toBe(sessionCreateFingerprint(first));
    expect(sessionCreateFingerprint(reorderedArrays)).not.toBe(sessionCreateFingerprint(originalArrays));
  });

  it("rotates the key when input, metadata, override, or stream semantics change", () => {
    const draft = {
      agentId: "agent-a",
      environment: { type: "none" } as const,
      metadata: {},
      stream: false,
    };
    const first = beginSessionCreateAttempt(draft, null, () => "key-1");

    expect(beginSessionCreateAttempt({ ...draft, input: "hello" }, first, () => "key-input").idempotencyKey)
      .toBe("key-input");
    expect(beginSessionCreateAttempt({ ...draft, metadata: { team: "web" } }, first, () => "key-metadata").idempotencyKey)
      .toBe("key-metadata");
    expect(beginSessionCreateAttempt({ ...draft, agent: { model: "provider/other" } }, first, () => "key-agent").idempotencyKey)
      .toBe("key-agent");
    expect(beginSessionCreateAttempt({ ...draft, stream: true }, first, () => "key-stream").idempotencyKey)
      .toBe("key-stream");
  });

  it("preserves message and text-part array order and grouping in the fingerprint", () => {
    const base = {
      agent: { model: "provider/inline" },
      environment: { type: "none" } as const,
      metadata: {},
      stream: true,
    };
    const grouped = {
      ...base,
      input: [{
        type: "message" as const,
        role: "user" as const,
        content: [
          { type: "input_text" as const, text: "first" },
          { type: "input_text" as const, text: "second" },
        ],
      }],
    };
    const split = {
      ...base,
      input: [
        { type: "message" as const, role: "user" as const, content: [{ type: "input_text" as const, text: "first" }] },
        { type: "message" as const, role: "user" as const, content: [{ type: "input_text" as const, text: "second" }] },
      ],
    };
    const reordered = {
      ...grouped,
      input: [{
        type: "message" as const,
        role: "user" as const,
        content: [...grouped.input[0]!.content].reverse(),
      }],
    };
    expect(sessionCreateFingerprint(grouped)).not.toBe(sessionCreateFingerprint(split));
    expect(sessionCreateFingerprint(grouped)).not.toBe(sessionCreateFingerprint(reordered));
  });

  it("sorts Vault attachments into the durable request fingerprint", () => {
    const draft = {
      agentId: "agent-a",
      environment: { type: "none" } as const,
      metadata: {},
      stream: false,
      vaultIds: ["bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"],
    };
    const first = beginSessionCreateAttempt(draft, null, () => "key-1");
    const reordered = beginSessionCreateAttempt({ ...draft, vaultIds: [...draft.vaultIds].reverse() }, first, () => "key-2");
    const changed = beginSessionCreateAttempt({ ...draft, vaultIds: [draft.vaultIds[0]!] }, first, () => "key-3");

    expect(reordered.idempotencyKey).toBe("key-1");
    expect(changed.idempotencyKey).toBe("key-3");
    expect(first.fingerprint).toContain('"vault_ids":["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"]');
  });
});

it("keeps explicit sandbox placement in both the request and creation retry identity", () => {
  const automatic = { environment: { type: "openai_hosted" as const }, metadata: {}, stream: false };
  expect(sessionCreateRequestPayload(automatic)).not.toHaveProperty("x_agents_core");
  const explicit = { ...automatic, sandboxNodeId: "node-a" };
  expect(sessionCreateRequestPayload(explicit).x_agents_core).toEqual({ sandbox_node_id: "node-a" });
  const attempt = beginSessionCreateAttempt(explicit, null, () => "first");
  expect(beginSessionCreateAttempt(explicit, attempt, () => "second").idempotencyKey).toBe("first");
  expect(beginSessionCreateAttempt({ ...explicit, sandboxNodeId: "node-b" }, attempt, () => "second").idempotencyKey).toBe("second");
});
