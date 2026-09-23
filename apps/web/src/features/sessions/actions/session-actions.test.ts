import { describe, expect, it, vi } from "vitest";

import {
  AgentCoreError,
  type AgentCore,
  type AgentSession,
} from "@agents-core-web/agents-client";

import {
  mergeSessionMetadata,
  reconcileUnknownSessionDelete,
  requestSessionDelete,
  requestSessionDetail,
  requestSessionUpdate,
  rebaseSessionMetadataDraft,
  replaceSessionMetadata,
  requestSessionCancelBeforeDelete,
  selectionAfterSessionDelete,
  SessionActionError,
  SessionMetadataConflictError,
  validateSessionMetadata,
  valuesFromSession,
  waitForSessionIdle,
} from "./session-actions";

function session(id: string, metadata: Record<string, string> = {}): AgentSession {
  return {
    id,
    object: "agent.session",
    agent: {
      id: "agent-fixture",
      model: "fixture/model",
      name: "Fixture",
      instructions: null,
      multi_agent: { enabled: false, max_concurrent_subagents: null },
      reasoning: {},
      service_tier: "auto",
      text: { format: { type: "text" }, verbosity: "medium" },
      tools: [],
    },
    environment: { type: "none" },
    status: "idle",
    error: null,
    metadata,
    required_actions: [],
    vault_ids: [],
    usage: null,
    created_at: 1,
    last_active_at: 2,
  };
}

function deepMalformedSessions(id: string): unknown[] {
  const valid = session(id);
  return [
    { ...valid, agent: { model: valid.agent.model } },
    { ...valid, agent: { ...valid.agent, service_tier: "unbounded" } },
    { ...valid, environment: { type: "self_hosted", id: "environment-1", remote_url: "https://executor.test", workspace_directory: "/workspace" } },
    { ...valid, required_actions: [{}] },
    { ...valid, required_actions: [{ type: "function_call", call_id: "call-1", turn_id: "turn-1", name: "confirm" }] },
    { ...valid, usage: {} },
  ];
}

describe("Session metadata form", () => {
  it("separates title from arbitrary string metadata and removes a blank title", () => {
    const current = session("session-1", { title: "Current", team: "web" });
    expect(valuesFromSession(current)).toEqual({
      title: "Current",
      metadata: '{\n  "team": "web"\n}',
    });
    expect(validateSessionMetadata({ title: "  Renamed  ", metadata: '{"team":"core","note":"safe"}' })).toEqual({
      metadata: { team: "core", note: "safe", title: "Renamed" },
    });
    expect(validateSessionMetadata({ title: "  ", metadata: '{"team":"web"}' })).toEqual({
      metadata: { team: "web" },
    });
  });

  it("rejects invalid JSON, non-object or non-string values, and duplicate title input", () => {
    expect(validateSessionMetadata({ title: "", metadata: "{" }).metadataError).toBe("Metadata must be valid JSON.");
    expect(validateSessionMetadata({ title: "", metadata: "[]" }).metadataError).toBe("Metadata must be a JSON object.");
    expect(validateSessionMetadata({ title: "", metadata: '{"count":1}' }).metadataError).toBe("Every metadata value must be a string.");
    expect(validateSessionMetadata({ title: "", metadata: '{"title":"duplicate"}' }).metadataError).toContain("Title field");
  });

  it("enforces Parsar's 16 pair, 64 character key, and 512 character value limits", () => {
    const sixteen = Object.fromEntries(Array.from({ length: 16 }, (_, index) => [`key-${index}`, "value"]));
    expect(validateSessionMetadata({ title: "", metadata: JSON.stringify(sixteen) }).metadata).toEqual(sixteen);
    expect(validateSessionMetadata({ title: "extra", metadata: JSON.stringify(sixteen) }).metadataError).toContain("at most 16 pairs");
    expect(validateSessionMetadata({ title: "", metadata: JSON.stringify({ ["k".repeat(65)]: "value" }) }).metadataError).toContain("64 characters");
    expect(validateSessionMetadata({ title: "", metadata: JSON.stringify({ key: "v".repeat(513) }) }).metadataError).toContain("512 characters");
  });
});

describe("Session metadata reconciliation", () => {
  it("rejects wrong-id and malformed Session detail responses", async () => {
    for (const invalid of [
      session("another-session"),
      { id: "session-1", object: "agent.session", metadata: {} },
      ...deepMalformedSessions("session-1"),
    ]) {
      const core = { retrieveSession: vi.fn().mockResolvedValue(invalid) } as unknown as AgentCore;
      await expect(requestSessionDetail(core, "session-1")).rejects.toMatchObject({
        kind: "request_failed",
        message: expect.stringContaining("invalid Session retrieval response"),
      });
    }
  });

  it("accepts complete known variants and preserves a safe unknown Environment type", async () => {
    const complete = {
      ...session("session-1"),
      environment: {
        type: "self_hosted",
        id: "environment-1",
        remote_url: "https://executor.test",
        workspace_directory: "/workspace",
        capability_directories: ["/capabilities"],
      },
      required_actions: [
        { type: "function_call", call_id: "call-1", turn_id: "turn-1", name: "confirm", arguments: { safe: true } },
        { type: "environment_connection", environment_id: "environment-1" },
      ],
      usage: {
        input_tokens: 10,
        output_tokens: 2,
        total_tokens: 12,
        input_tokens_details: { cached_tokens: 3 },
        output_tokens_details: { reasoning_tokens: 1 },
      },
    };
    const unknownEnvironment = {
      ...complete,
      environment: { type: "future_remote", region: "test-region", contract_marker: "preserved" },
    };
    for (const current of [complete, unknownEnvironment]) {
      const core = { retrieveSession: vi.fn().mockResolvedValue(current) } as unknown as AgentCore;
      await expect(requestSessionDetail(core, "session-1")).resolves.toEqual(current);
    }
  });

  it("applies only user changes to the latest whole-map replacement", () => {
    expect(mergeSessionMetadata(
      { title: "Old", team: "web", remove: "yes" },
      { title: "New", team: "web", add: "now" },
      { title: "Old", team: "platform", remove: "yes", concurrent: "kept" },
    )).toEqual({ title: "New", team: "platform", concurrent: "kept", add: "now" });
  });

  it("accepts a concurrent change that already equals the draft and rejects same-key divergence", () => {
    expect(mergeSessionMetadata({ title: "Old" }, { title: "New" }, { title: "New", safe: "kept" }))
      .toEqual({ title: "New", safe: "kept" });
    expect(() => mergeSessionMetadata({ title: "Old" }, { title: "Draft" }, { title: "Concurrent" }))
      .toThrow(SessionMetadataConflictError);
  });

  it("rebases a preserved draft onto newly retrieved unrelated keys before an explicit retry", () => {
    expect(rebaseSessionMetadataDraft(
      { fixture: "original" },
      { fixture: "original", title: "My draft" },
      { title: "Concurrent", concurrent: "kept" },
    )).toEqual({ title: "My draft", concurrent: "kept" });
  });

  it("retrieves latest before one update and never retries known or unknown failures", async () => {
    const latest = session("session-1", { title: "Old", concurrent: "kept" });
    const updated = {
      ...session("session-1", { title: "New", concurrent: "kept" }),
      status: "failed" as const,
      error: "response fields outside metadata are not trusted",
    };
    const retrieveSession = vi.fn().mockResolvedValue(latest);
    const updateSession = vi.fn().mockResolvedValue(updated);
    const core = { retrieveSession, updateSession } as unknown as AgentCore;

    await expect(requestSessionUpdate(core, latest.id, { title: "Old" }, { title: "New" })).resolves.toEqual({
      ...latest,
      metadata: { title: "New", concurrent: "kept" },
    });
    expect(retrieveSession).toHaveBeenCalledOnce();
    expect(updateSession).toHaveBeenCalledOnce();
    expect(retrieveSession.mock.invocationCallOrder[0]!).toBeLessThan(updateSession.mock.invocationCallOrder[0]!);
    expect(updateSession).toHaveBeenCalledWith("session-1", { title: "New", concurrent: "kept" });

    updateSession.mockRejectedValueOnce(new TypeError("connection lost"));
    await expect(requestSessionUpdate(core, latest.id, latest.metadata, { title: "Another" }))
      .rejects.toMatchObject({ kind: "unknown_write" });
    expect(updateSession).toHaveBeenCalledTimes(2);
  });

  it("does not write when retrieving latest fails", async () => {
    const retrieveSession = vi.fn().mockRejectedValue(new AgentCoreError("unavailable", 503));
    const updateSession = vi.fn();
    const core = { retrieveSession, updateSession } as unknown as AgentCore;

    await expect(requestSessionUpdate(core, "session-1", {}, { title: "Draft" }))
      .rejects.toMatchObject({ kind: "core_unavailable" });
    expect(updateSession).not.toHaveBeenCalled();
  });

  it.each(["invalid_session_resource", "invalid_session_vaults"])(
    "classifies retrieve-time %s as an invalid contract response and sends no update",
    async (code) => {
      const retrieveSession = vi.fn().mockRejectedValue(new AgentCoreError("invalid", 502, code));
      const updateSession = vi.fn();
      const core = { retrieveSession, updateSession } as unknown as AgentCore;

      await expect(requestSessionUpdate(core, "session-1", {}, { title: "Draft" }))
        .rejects.toMatchObject({
          kind: "request_failed",
          message: expect.stringContaining("invalid Session retrieval response"),
        });
      expect(updateSession).not.toHaveBeenCalled();
    },
  );

  it("does not write when the latest Session response has the wrong identity or shape", async () => {
    const updateSession = vi.fn();
    for (const invalid of [
      session("another-session"),
      { id: "session-1", object: "agent.session", metadata: {} },
      ...deepMalformedSessions("session-1"),
    ]) {
      const core = {
        retrieveSession: vi.fn().mockResolvedValue(invalid),
        updateSession,
      } as unknown as AgentCore;
      await expect(requestSessionUpdate(core, "session-1", {}, { title: "Draft" }))
        .rejects.toMatchObject({ kind: "request_failed" });
    }
    expect(updateSession).not.toHaveBeenCalled();
  });

  it("reports a retrieve-time 409 as a read conflict and sends no update", async () => {
    const retrieveSession = vi.fn().mockRejectedValue(new AgentCoreError("conflict", 409));
    const updateSession = vi.fn();
    const core = { retrieveSession, updateSession } as unknown as AgentCore;

    await expect(requestSessionUpdate(core, "session-1", {}, { title: "Draft" })).rejects.toMatchObject({
      kind: "lifecycle_conflict",
      message: expect.stringContaining("no update request was sent"),
    });
    expect(updateSession).not.toHaveBeenCalled();
  });

  it("treats a malformed success response as unknown and merges only confirmed metadata into live state", async () => {
    const latest = session("session-1", { title: "Old" });
    const core = {
      retrieveSession: vi.fn().mockResolvedValue(latest),
      updateSession: vi.fn().mockResolvedValue({ ...latest, id: "wrong" }),
    } as unknown as AgentCore;
    await expect(requestSessionUpdate(core, latest.id, latest.metadata, { title: "New" }))
      .rejects.toMatchObject({ kind: "unknown_write" });

    core.updateSession = vi.fn().mockResolvedValue({ id: latest.id, object: "agent.session", metadata: { title: "New" } });
    await expect(requestSessionUpdate(core, latest.id, latest.metadata, { title: "New" }))
      .rejects.toMatchObject({ kind: "unknown_write" });

    core.updateSession = vi.fn().mockResolvedValue({ ...latest, metadata: { title: "Different" } });
    await expect(requestSessionUpdate(core, latest.id, latest.metadata, { title: "New" }))
      .rejects.toMatchObject({ kind: "unknown_write" });

    const live = { ...latest, status: "in_progress" as const, usage: { input_tokens: 1 } as AgentSession["usage"] };
    const response = { ...latest, metadata: { title: "New" } };
    expect(replaceSessionMetadata([live], response)).toEqual([{ ...live, metadata: { title: "New" } }]);
  });
});

const busyMessage = "session must be durably idle or failed without required actions before deletion";

describe("Session deletion", () => {
  it("sends one delete, validates confirmation, and classifies 404, 409, 503, and network failures", async () => {
    const deleteSession = vi.fn().mockResolvedValue({
      id: "session-1",
      object: "agent.session.deleted",
      deleted: true,
    });
    const core = { deleteSession } as unknown as AgentCore;
    await expect(requestSessionDelete(core, "session-1")).resolves.toMatchObject({ deleted: true });

    for (const [error, kind] of [
      [new AgentCoreError("missing", 404), "not_found"],
      [new AgentCoreError("active conflict", 409), "lifecycle_conflict"],
      [new AgentCoreError(busyMessage, 409, "conflict_error", null, "conflict_error"), "session_busy"],
      [new AgentCoreError("unavailable", 503), "unknown_write"],
      [new TypeError("connection lost"), "unknown_write"],
    ] as const) {
      deleteSession.mockRejectedValueOnce(error);
      await expect(requestSessionDelete(core, "session-1")).rejects.toMatchObject({ kind });
    }
    expect(deleteSession).toHaveBeenCalledTimes(6);
  });

  it("explains a busy-Session conflict and offers cancel-then-delete without retrying", async () => {
    const deleteSession = vi.fn().mockRejectedValue(
      new AgentCoreError(busyMessage, 409, "conflict_error", null, "conflict_error"),
    );
    const cancelTurn = vi.fn();
    const retrieveSession = vi.fn().mockResolvedValue({ ...session("session-1"), status: "in_progress" });
    const core = { deleteSession, cancelTurn, retrieveSession } as unknown as AgentCore;
    const error = await requestSessionDelete(core, "session-1").catch((value: unknown) => value);
    expect(error).toBeInstanceOf(SessionActionError);
    expect((error as SessionActionError).message).toContain("only when it is idle or failed without required actions");
    expect((error as SessionActionError).message).toContain("nothing was changed");
    expect((error as SessionActionError).message).toContain("Cancel work and delete");
    expect(deleteSession).toHaveBeenCalledTimes(1);
    expect(cancelTurn).not.toHaveBeenCalled();
  });

  it("does not offer cancellation when only input waiting for its Environment blocks deletion", async () => {
    const deleteSession = vi.fn().mockRejectedValue(
      new AgentCoreError(busyMessage, 409, "conflict_error", null, "conflict_error"),
    );
    const cancelTurn = vi.fn();
    const awaitingConnection = {
      ...session("session-1"),
      status: "requires_action" as const,
      required_actions: [{ type: "environment_connection" as const, environment_id: "environment-1" }],
    };
    const functionAction = {
      ...session("session-1"),
      status: "requires_action" as const,
      required_actions: [{ type: "function_call" as const, call_id: "call-1", turn_id: "turn-1", name: "lookup", arguments: {} }],
    };
    const retrieveSession = vi.fn()
      .mockResolvedValueOnce(session("session-1"))
      .mockResolvedValueOnce({ ...session("session-1"), status: "failed", error: "The environment is no longer available for this input." })
      .mockResolvedValueOnce(awaitingConnection)
      .mockResolvedValueOnce(functionAction)
      .mockRejectedValueOnce(new AgentCoreError("unavailable", 503));
    const core = { deleteSession, cancelTurn, retrieveSession } as unknown as AgentCore;

    for (const kind of ["session_input_pending", "session_input_pending", "session_input_pending", "session_busy", "session_busy"]) {
      const failure = await requestSessionDelete(core, "session-1").catch((value: unknown) => value);
      expect(failure).toMatchObject({ kind });
      if (kind === "session_input_pending") {
        expect((failure as Error).message).toContain("cannot be cancelled");
        expect((failure as Error).message).toContain("starts, expires or fails");
        expect((failure as Error).message).not.toContain("Cancel work and delete");
      }
    }
    expect(deleteSession).toHaveBeenCalledTimes(5);
    expect(retrieveSession).toHaveBeenCalledTimes(5);
    expect(cancelTurn).not.toHaveBeenCalled();
  });

  it("sends one explicit cancellation before delete and keeps the Session on failure", async () => {
    const cancelTurn = vi.fn().mockResolvedValue(undefined);
    const deleteSession = vi.fn();
    const core = { cancelTurn, deleteSession } as unknown as AgentCore;
    await requestSessionCancelBeforeDelete(core, "session-1", "cancel-key");
    expect(cancelTurn).toHaveBeenCalledWith("session-1", "cancel-key");

    // Current Cores report a rejected cancellation with the official conflict
    // fields; earlier Cores used turn_conflict. Neither is a deletion conflict.
    for (const [error, kind, text] of [
      [new AgentCoreError("Earlier input to this Session is still pending.", 409, "conflict_error", null, "conflict_error"), "request_failed", "rejected the cancellation (409): Earlier input to this Session is still pending."],
      [new AgentCoreError("pending input", 409, "turn_conflict"), "request_failed", "rejected the cancellation (409)"],
      [new TypeError("connection lost"), "request_failed", "result is unknown"],
      [new AgentCoreError("missing", 404), "not_found", "not found"],
    ] as const) {
      cancelTurn.mockRejectedValueOnce(error);
      const failure = await requestSessionCancelBeforeDelete(core, "session-1", "cancel-key").catch((value: unknown) => value);
      expect(failure).toMatchObject({ kind });
      expect((failure as Error).message).toContain(text);
    }
    expect(cancelTurn).toHaveBeenCalledTimes(5);
    expect(deleteSession).not.toHaveBeenCalled();
  });

  it("reads until the Session is idle or failed without required actions", async () => {
    const pendingAction = {
      ...session("session-1"),
      status: "requires_action" as const,
      required_actions: [{ type: "function_call" as const, call_id: "call-1", turn_id: "turn-1", name: "lookup", arguments: {} }],
    };
    const retrieveSession = vi.fn()
      .mockResolvedValueOnce({ ...session("session-1"), status: "in_progress" })
      .mockResolvedValueOnce(pendingAction)
      .mockResolvedValueOnce(session("session-1"))
      .mockResolvedValueOnce({ ...session("session-1"), status: "failed", error: "The execution could not complete." });
    const sleep = vi.fn().mockResolvedValue(undefined);
    const core = { retrieveSession, deleteSession: vi.fn(), cancelTurn: vi.fn() } as unknown as AgentCore;

    await expect(waitForSessionIdle(core, "session-1", { sleep, intervalMs: 5 })).resolves.toBe("idle");
    expect(sleep).toHaveBeenCalledTimes(2);
    expect(sleep).toHaveBeenCalledWith(5);
    await expect(waitForSessionIdle(core, "session-1", { sleep })).resolves.toBe("idle");
    expect(retrieveSession).toHaveBeenCalledTimes(4);

    retrieveSession.mockRejectedValueOnce(new AgentCoreError("missing", 404));
    await expect(waitForSessionIdle(core, "session-1", { sleep })).resolves.toBe("missing");
    retrieveSession.mockRejectedValueOnce(new AgentCoreError("unavailable", 503));
    await expect(waitForSessionIdle(core, "session-1", { sleep })).rejects.toMatchObject({ kind: "request_failed" });
    await expect(waitForSessionIdle(core, "session-1", { sleep, isCurrent: () => false })).resolves.toBe("stale");
    expect(retrieveSession).toHaveBeenCalledTimes(6);
    expect((core.deleteSession as ReturnType<typeof vi.fn>)).not.toHaveBeenCalled();
    expect((core.cancelTurn as ReturnType<typeof vi.fn>)).not.toHaveBeenCalled();
  });

  it("stops waiting at the deadline without deleting", async () => {
    let clock = 0;
    const retrieveSession = vi.fn().mockResolvedValue({ ...session("session-1"), status: "in_progress" });
    const sleep = vi.fn(async (ms: number) => { clock += ms; });
    const core = { retrieveSession } as unknown as AgentCore;
    const failure = await waitForSessionIdle(core, "session-1", {
      sleep, now: () => clock, intervalMs: 1_000, timeoutMs: 3_000,
    }).catch((value: unknown) => value);
    expect(failure).toMatchObject({ kind: "session_busy" });
    expect((failure as Error).message).toContain("still in progress after 3 seconds");
    expect(retrieveSession).toHaveBeenCalledTimes(4);
  });

  it("keeps the Session when Core returns a malformed confirmation", async () => {
    const core = {
      deleteSession: vi.fn().mockResolvedValue({ id: "other", object: "agent.session.deleted", deleted: true }),
    } as unknown as AgentCore;
    await expect(requestSessionDelete(core, "session-1")).rejects.toBeInstanceOf(SessionActionError);
  });

  it("reconciles an unknown delete once without resending it", async () => {
    const retrieveSession = vi.fn()
      .mockResolvedValueOnce(session("session-1"))
      .mockResolvedValueOnce(session("another-session"))
      .mockResolvedValueOnce({ id: "session-1", object: "agent.session", metadata: {} })
      .mockRejectedValueOnce(new AgentCoreError("missing", 404))
      .mockRejectedValueOnce(new AgentCoreError("unavailable", 503));
    const core = { retrieveSession } as unknown as AgentCore;

    await expect(reconcileUnknownSessionDelete(core, "session-1")).resolves.toMatchObject({
      state: "present",
      session: { id: "session-1" },
    });
    for (let index = 0; index < 2; index += 1) {
      await expect(reconcileUnknownSessionDelete(core, "session-1")).resolves.toMatchObject({
        state: "unknown",
        error: { kind: "request_failed" },
      });
    }
    await expect(reconcileUnknownSessionDelete(core, "session-1")).resolves.toEqual({ state: "missing" });
    await expect(reconcileUnknownSessionDelete(core, "session-1")).resolves.toMatchObject({
      state: "unknown",
      error: { status: 503 },
    });
    expect(retrieveSession).toHaveBeenCalledTimes(5);
  });

  it("keeps exact-ID deep-malformed Session reconciliation unknown", async () => {
    for (const invalid of deepMalformedSessions("session-1")) {
      const core = { retrieveSession: vi.fn().mockResolvedValue(invalid) } as unknown as AgentCore;
      await expect(reconcileUnknownSessionDelete(core, "session-1")).resolves.toMatchObject({
        state: "unknown",
        error: { kind: "request_failed" },
      });
    }
  });

  it("selects next, then previous, then empty while leaving non-selected selection untouched", () => {
    const sessions = [session("a"), session("b"), session("c")];
    expect(selectionAfterSessionDelete(sessions, "b", "b")).toBe("c");
    expect(selectionAfterSessionDelete(sessions, "c", "c")).toBe("b");
    expect(selectionAfterSessionDelete([session("a")], "a", "a")).toBeNull();
    expect(selectionAfterSessionDelete(sessions, "a", "c")).toBe("a");
  });
});
