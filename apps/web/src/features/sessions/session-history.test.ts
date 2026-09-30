import { describe, expect, it } from "vitest";

import { AgentCoreError, type AgentSession, type AgentTurn, type ListPage, type SessionItem } from "@oac/agents-client";

import { classifyDeleteError } from "./SessionDeleteDialog";
import {
  isSessionActive,
  itemsPerTurn,
  loadSessionHistory,
  nextPollDelay,
  readHistoryTail,
  SESSION_POLL_MS,
  settledPrefixLength,
  transcriptGroups,
  turnDurationSeconds,
} from "./session-history";
import { loadSessionRuntimeHistory } from "./session-runtime";

function item(id: string, status: SessionItem["status"], turn = "t1"): SessionItem {
  return { id, turn_id: turn, type: "message", status, role: "assistant", content: [{ type: "output_text", text: id }] } as SessionItem;
}

function turn(id: string, status: AgentTurn["status"], overrides: Partial<AgentTurn> = {}): AgentTurn {
  return { id, agent_id: "agent", session_id: "session", object: "agent.session.turn", status, created_at: 1, started_at: null, completed_at: null, error: null, usage: null, ...overrides };
}

function list<T extends { id: string }>(data: T[], hasMore = false): ListPage<T> {
  return { object: "list", data, has_more: hasMore, first_id: data[0]?.id ?? null, last_id: data.at(-1)?.id ?? null };
}

const session = { id: "session", status: "in_progress" } as AgentSession;

describe("Session polling", () => {
  it("polls while work is in flight and stops once the Session is idle or failed", () => {
    expect(nextPollDelay("in_progress")).toBe(SESSION_POLL_MS);
    expect(nextPollDelay("requires_action")).toBe(SESSION_POLL_MS);
    expect(nextPollDelay("idle")).toBeNull();
    expect(nextPollDelay("failed")).toBeNull();
    // Nothing read yet (for example the first read failed): nothing to poll.
    expect(nextPollDelay(undefined)).toBeNull();
    expect(isSessionActive("in_progress")).toBe(true);
    expect(isSessionActive("idle")).toBe(false);
  });

  it("backs off after consecutive failures, up to a minute", () => {
    expect(nextPollDelay("in_progress", 1)).toBe(10_000);
    expect(nextPollDelay("in_progress", 2)).toBe(20_000);
    expect(nextPollDelay("in_progress", 10)).toBe(60_000);
    expect(nextPollDelay("idle", 3)).toBeNull();
  });
});

describe("Incremental history reads", () => {
  it("keeps the settled prefix and reads again after it", async () => {
    const known = [item("a", "completed"), item("b", "completed"), item("c", "in_progress"), item("d", "completed")];
    expect(settledPrefixLength(known, (value) => value.status !== "in_progress")).toBe(2);
    const cursors: Array<string | undefined> = [];
    const values = await readHistoryTail(async (after) => {
      cursors.push(after);
      return cursors.length === 1 ? list([item("c", "completed"), item("d", "completed")], true) : list([item("e", "in_progress")]);
    }, known, (value) => value.status !== "in_progress");
    expect(cursors).toEqual(["b", "d"]);
    expect(values.map((value) => `${value.id}:${value.status}`)).toEqual(["a:completed", "b:completed", "c:completed", "d:completed", "e:in_progress"]);
  });

  it("reads from the start when nothing is known", async () => {
    const cursors: Array<string | undefined> = [];
    const values = await readHistoryTail(async (after) => { cursors.push(after); return list([item("a", "completed")]); }, [], () => true);
    expect(cursors).toEqual([undefined]);
    expect(values.map((value) => value.id)).toEqual(["a"]);
  });

  it("keeps each part's previous value when its read fails", async () => {
    const previous = { session: { ...session, status: "in_progress" } as AgentSession, items: [item("a", "completed")], turns: [turn("t1", "in_progress")] };
    const client = {
      retrieveSession: async () => ({ ...session, status: "idle" }) as AgentSession,
      listItems: async () => { throw new AgentCoreError("Items unavailable", 503); },
      listTurns: async (_id: string, options?: { after?: string }) => {
        expect(options?.after).toBeUndefined();
        return list([turn("t1", "completed")]);
      },
    };
    const history = await loadSessionHistory(client, "session", previous, { incremental: true });
    expect(history.session?.status).toBe("idle");
    expect(history.items).toBe(previous.items);
    expect(history.itemsError).toBe("Items unavailable");
    expect(history.turns.map((value) => value.status)).toEqual(["completed"]);
    expect(history.sessionError).toBeNull();
  });

  it("keeps the last Session when its read fails and reports why", async () => {
    const failure = new AgentCoreError("gone", 404);
    const client = {
      retrieveSession: async () => { throw failure; },
      listItems: async () => list<SessionItem>([]),
      listTurns: async () => list<AgentTurn>([]),
    };
    const history = await loadSessionHistory(client, "session", { session, items: [], turns: [] });
    expect(history.session).toBe(session);
    expect(history.sessionError).toBe(failure);
  });
});

describe("Turn facts", () => {
  it("counts Items per Turn and those without a known Turn", () => {
    const { counts, unassociated } = itemsPerTurn([turn("t1", "completed")], [item("a", "completed", "t1"), item("b", "completed", "t1"), item("c", "completed", "t9")]);
    expect(counts.get("t1")).toBe(2);
    expect(unassociated).toBe(1);
  });

  it("measures finished and running Turns and leaves unreported bounds missing", () => {
    expect(turnDurationSeconds(turn("a", "completed", { started_at: 10, completed_at: 25 }), 100)).toBe(15);
    expect(turnDurationSeconds(turn("b", "in_progress", { started_at: 90 }), 100)).toBe(10);
    expect(turnDurationSeconds(turn("c", "completed", { started_at: null, completed_at: 25 }), 100)).toBeNull();
    expect(turnDurationSeconds(turn("d", "failed", { started_at: 10 }), 100)).toBeNull();
  });
});

describe("Session deletion and runtime answers", () => {
  it("tells a busy Session, a missing one, a refusal and an unconfirmed deletion apart", () => {
    expect(classifyDeleteError(new AgentCoreError("busy", 409, "conflict_error"))).toEqual({ kind: "not-idle" });
    expect(classifyDeleteError(new AgentCoreError("missing", 404))).toEqual({ kind: "missing" });
    expect(classifyDeleteError(new AgentCoreError("nope", 403))).toEqual({ kind: "rejected", reason: "nope" });
    expect(classifyDeleteError(new AgentCoreError("down", 503))).toEqual({ kind: "uncertain" });
    expect(classifyDeleteError(new TypeError("Failed to fetch"))).toEqual({ kind: "uncertain" });
  });

  it("treats a Session without retained runtime history as no history, not an error", async () => {
    const client = { retrieveRuntimeHistory: async () => { throw new AgentCoreError("No hosted runtime.", 404); } };
    await expect(loadSessionRuntimeHistory(client, session, 3_600_000)).resolves.toBeNull();
    const failing = { retrieveRuntimeHistory: async () => { throw new AgentCoreError("down", 503); } };
    await expect(loadSessionRuntimeHistory(failing, session, 3_600_000)).rejects.toThrow("down");
  });
});

describe("transcript groups", () => {
  it("groups Items under their Turn in Turn order, keeps empty Turns and puts unknown Items last", () => {
    const turns = [turn("t1", "completed"), turn("t2", "failed"), turn("t3", "queued")];
    const items = [item("a", "completed", "t1"), item("x", "completed", "gone"), item("b", "completed", "t2"), item("c", "completed", "t1")];
    const groups = transcriptGroups(turns, items);
    expect(groups.map((group) => [group.turn?.id ?? null, group.number, group.items.map((entry) => entry.id)])).toEqual([
      ["t1", 1, ["a", "c"]],
      ["t2", 2, ["b"]],
      ["t3", 3, []],
      [null, null, ["x"]],
    ]);
  });
});
