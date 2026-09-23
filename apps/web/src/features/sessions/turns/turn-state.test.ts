import { describe, expect, it, vi } from "vitest";

import type { AgentCore, AgentTurn, SessionEvent, SessionItem } from "@agents-core-web/agents-client";

import {
  addChildTurnIds,
  childTurnSnapshotId,
  listAllTurns,
  matchingTurnSnapshot,
  mergeDurableAndLiveTurns,
  rootSessionItems,
  turnReadIsCurrent,
  upsertTurn,
} from "./turn-state";

function turn(id: string, status: AgentTurn["status"] = "queued", sessionId = "session-1"): AgentTurn {
  return {
    id,
    agent_id: "agent-1",
    session_id: sessionId,
    object: "agent.session.turn",
    status,
    created_at: 1,
    started_at: null,
    completed_at: null,
    error: null,
    usage: null,
  };
}

describe("durable Turn loading", () => {
  it("loads every page in ascending order and passes one abort signal", async () => {
    const signal = new AbortController().signal;
    const listTurns = vi.fn(async (_sessionId: string, options?: { after?: string }) => {
      if (!options?.after) return { data: [turn("turn-1")], has_more: true, last_id: "turn-1" };
      if (options.after === "turn-1") return { data: [turn("turn-2", "waiting")], has_more: true };
      return { data: [turn("turn-3", "completed")], has_more: false };
    });

    await expect(listAllTurns({ listTurns } as unknown as AgentCore, "session-1", signal)).resolves.toEqual([
      turn("turn-1"),
      turn("turn-2", "waiting"),
      turn("turn-3", "completed"),
    ]);
    expect(listTurns).toHaveBeenNthCalledWith(1, "session-1", { after: undefined, limit: 100, order: "asc", signal });
    expect(listTurns).toHaveBeenNthCalledWith(2, "session-1", { after: "turn-1", limit: 100, order: "asc", signal });
    expect(listTurns).toHaveBeenNthCalledWith(3, "session-1", { after: "turn-2", limit: 100, order: "asc", signal });
  });

  it("rejects repeated or cyclic cursors and cross-Session Turn data", async () => {
    const repeated = { listTurns: async () => ({ data: [turn("same")], has_more: true, last_id: "same" }) } as unknown as AgentCore;
    await expect(listAllTurns(repeated, "session-1")).rejects.toThrow("invalid Turns pagination cursor");

    const cyclic = {
      listTurns: async (_sessionId: string, options?: { after?: string }) => ({
        data: [turn(options?.after === "turn-a" ? "turn-b" : "turn-a")],
        has_more: true,
      }),
    } as unknown as AgentCore;
    await expect(listAllTurns(cyclic, "session-1")).rejects.toThrow("invalid Turns pagination cursor");

    const foreign = { listTurns: async () => ({ data: [turn("foreign", "queued", "session-2")], has_more: false }) } as unknown as AgentCore;
    await expect(listAllTurns(foreign, "session-1")).rejects.toThrow("outside the selected Session");
  });

  it("keeps the timeline root-only when an earlier Core lists Subagent Turns", async () => {
    const child = { ...turn("child-turn", "completed"), subagent_id: "subagent-1" };
    const root = { ...turn("root-turn", "completed"), subagent_id: null };
    const listTurns = vi.fn(async (_sessionId: string, options?: { after?: string }) => options?.after
      ? { data: [turn("turn-2")], has_more: false }
      : { data: [root, child], has_more: true, last_id: "child-turn" });

    const childTurnIds = new Set<string>();
    await expect(listAllTurns({ listTurns } as unknown as AgentCore, "session-1", undefined, childTurnIds)).resolves.toEqual([root, turn("turn-2")]);
    expect(listTurns).toHaveBeenNthCalledWith(2, "session-1", { after: "child-turn", limit: 100, order: "asc", signal: undefined });
    expect([...childTurnIds]).toEqual(["child-turn"]);
  });

  it("deduplicates overlapping pages without regressing a terminal Turn", async () => {
    const listTurns = vi.fn(async (_sessionId: string, options?: { after?: string }) => options?.after
      ? { data: [turn("turn-1", "in_progress"), turn("turn-2")], has_more: false }
      : { data: [turn("turn-1", "completed")], has_more: true, last_id: "page-1" });

    await expect(listAllTurns({ listTurns } as unknown as AgentCore, "session-1")).resolves.toEqual([
      turn("turn-1", "completed"),
      turn("turn-2"),
    ]);
  });
});

describe("Turn live reconciliation", () => {
  it("preserves durable ordering and lets terminal live snapshots win", () => {
    expect(mergeDurableAndLiveTurns(
      [turn("one", "in_progress"), turn("two", "completed")],
      [turn("one", "failed"), turn("two", "waiting"), turn("three", "queued")],
    )).toEqual([
      turn("one", "failed"),
      turn("two", "completed"),
      turn("three", "queued"),
    ]);
    expect(upsertTurn([turn("one", "cancelled")], turn("one", "in_progress"))).toEqual([
      turn("one", "cancelled"),
    ]);
  });

  it("accepts only exact scoped lifecycle event-to-status pairs", () => {
    const statuses: AgentTurn["status"][] = ["queued", "in_progress", "waiting", "completed", "failed", "cancelled"];
    for (const status of statuses) {
      const snapshot = turn(`turn-${status}`, status);
      const event = {
        type: `agent.session.turn.${status === "queued" ? "created" : status}`,
        event_id: `event-${status}`,
        session_id: "session-1",
        turn_id: snapshot.id,
        turn: snapshot,
      } as SessionEvent;
      expect(matchingTurnSnapshot(event, "session-1")).toEqual(snapshot);
    }
    expect(matchingTurnSnapshot({
      type: "agent.session.turn.completed",
      event_id: "foreign",
      session_id: "session-1",
      turn_id: "foreign",
      turn: turn("foreign", "completed", "session-2"),
    } as SessionEvent, "session-1")).toBeNull();
    expect(matchingTurnSnapshot({
      type: "agent.session.turn.paused",
      event_id: "unknown",
      turn: turn("unknown", "completed"),
    } as unknown as SessionEvent, "session-1")).toBeNull();
    expect(matchingTurnSnapshot({
      type: "agent.session.turn.item.done",
      event_id: "item-event",
      turn_id: "item-turn",
      turn: turn("item-turn", "completed"),
    } as SessionEvent, "session-1")).toBeNull();
    for (const type of ["agent.session.turn.created", "agent.session.turn.completed"]) {
      expect(matchingTurnSnapshot({
        type,
        event_id: `child-${type}`,
        session_id: "session-1",
        turn_id: "child-turn",
        turn: { ...turn("child-turn", "completed"), subagent_id: "subagent-1" },
      } as SessionEvent, "session-1")).toBeNull();
    }
    const root = { ...turn("root-turn", "completed"), subagent_id: null };
    expect(matchingTurnSnapshot({
      type: "agent.session.turn.completed",
      event_id: "root",
      session_id: "session-1",
      turn_id: "root-turn",
      turn: root,
    } as SessionEvent, "session-1")).toEqual(root);
    expect(matchingTurnSnapshot({
      type: "agent.session.turn.completed",
      event_id: "mismatched-status",
      turn_id: "still-running",
      turn: turn("still-running", "in_progress"),
    } as SessionEvent, "session-1")).toBeNull();
  });

  it("fences stale request, Core, and selected-Session continuations", () => {
    const read = { coreGeneration: 2, request: 4, sessionId: "session-1" };
    expect(turnReadIsCurrent(read, { ...read, selectedSessionId: "session-1" })).toBe(true);
    expect(turnReadIsCurrent(read, { ...read, request: 5, selectedSessionId: "session-1" })).toBe(false);
    expect(turnReadIsCurrent(read, { ...read, coreGeneration: 3, selectedSessionId: "session-1" })).toBe(false);
    expect(turnReadIsCurrent(read, { ...read, selectedSessionId: "session-2" })).toBe(false);
  });
});

describe("Subagent work from an earlier Core", () => {
  function item(id: string, turnId: string): SessionItem {
    return { id, turn_id: turnId, type: "message", role: "assistant", status: "completed", content: [{ type: "output_text", text: id }] } as SessionItem;
  }

  it("recognizes streamed Subagent Turns, including a terminal first snapshot", () => {
    const child = { ...turn("child-turn", "completed"), subagent_id: "subagent-1" };
    const event = (type: string, value: AgentTurn, sessionId = "session-1") => ({
      type, event_id: `${type}-${value.id}`, session_id: sessionId, turn_id: value.id, turn: value,
    } as SessionEvent);
    expect(childTurnSnapshotId(event("agent.session.turn.created", child), "session-1")).toBe("child-turn");
    expect(childTurnSnapshotId(event("agent.session.turn.completed", child), "session-1")).toBe("child-turn");
    expect(childTurnSnapshotId(event("agent.session.turn.completed", turn("root", "completed")), "session-1")).toBeNull();
    expect(childTurnSnapshotId(event("agent.session.turn.completed", child), "session-2")).toBeNull();
    expect(childTurnSnapshotId({ ...event("agent.session.turn.completed", child), turn_id: "other" }, "session-1")).toBeNull();
    expect(childTurnSnapshotId({ ...event("agent.session.turn.item.added", child) }, "session-1")).toBeNull();
  });

  it("keeps live Items of known Subagent Turns out of the Session timeline", () => {
    const empty: ReadonlyMap<string, ReadonlySet<string>> = new Map();
    const known = addChildTurnIds(empty, "session-1", ["child-turn"]);
    expect(addChildTurnIds(known, "session-1", ["child-turn"])).toBe(known);
    expect(known.get("session-2")).toBeUndefined();

    const rootItem = item("root-answer", "root-turn");
    // An earlier Core streamed a child Item and its text; neither may surface as
    // an unassociated Item beside the root Turns.
    const items = [rootItem, item("child-answer", "child-turn"), item("stream:child-turn:0:0", "child-turn")];
    expect(rootSessionItems(items, known.get("session-1"))).toEqual([rootItem]);
    expect(rootSessionItems(items, known.get("session-2"))).toBe(items);
    const rootOnly = [rootItem];
    expect(rootSessionItems(rootOnly, known.get("session-1"))).toBe(rootOnly);
  });
});
