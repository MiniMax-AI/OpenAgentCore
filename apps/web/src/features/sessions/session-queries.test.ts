import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AgentCoreError, type AgentSession, type AgentTurn, type ListPage, type SessionItem } from "@agents-core-web/agents-client";

import { retryTransient } from "../resources/detail-queries";
import { SESSION_POLL_MS } from "./session-history";
import { requestFullHistoryRead, sessionHistoryQuery, sessionPollInterval, type SessionHistoryRead } from "./session-queries";

const reads: Array<{ list: "items" | "turns"; after: string | undefined }> = [];
let sessionAnswer: () => Promise<AgentSession>;

const fake = {
  retrieveSession: () => sessionAnswer(),
  listItems: async (_id: string, options?: { after?: string }) => {
    reads.push({ list: "items", after: options?.after });
    return list([item("a", "completed")]);
  },
  listTurns: async (_id: string, options?: { after?: string }) => {
    reads.push({ list: "turns", after: options?.after });
    return list([turn("t1", "completed")]);
  },
};

vi.mock(import("../../lib/projects"), async (importOriginal) => ({ ...(await importOriginal()), projectClient: () => fake as never }));

function item(id: string, status: SessionItem["status"]): SessionItem {
  return { id, turn_id: "t1", type: "message", status, role: "assistant", content: [{ type: "output_text", text: id }] } as SessionItem;
}

function turn(id: string, status: AgentTurn["status"]): AgentTurn {
  return { id, agent_id: "agent", session_id: "session", object: "agent.session.turn", status, created_at: 1, started_at: null, completed_at: null, error: null, usage: null };
}

function list<T extends { id: string }>(data: T[]): ListPage<T> {
  return { object: "list", data, has_more: false, first_id: data[0]?.id ?? null, last_id: data.at(-1)?.id ?? null };
}

function session(status: string): AgentSession {
  return { id: "session", status } as AgentSession;
}

describe("Session history cache", () => {
  let client: QueryClient;
  const options = sessionHistoryQuery("project", "session");
  const read = () => client.fetchQuery(options);

  beforeEach(() => {
    client = new QueryClient();
    reads.length = 0;
    sessionAnswer = async () => session("in_progress");
  });

  it("reads everything first, polls incrementally, and reads everything again on a manual refresh", async () => {
    await read();
    await read();
    requestFullHistoryRead("project", "session");
    await read();
    expect(reads.filter((entry) => entry.list === "items").map((entry) => entry.after)).toEqual([undefined, "a", undefined]);
  });

  it("fails the first read with the Session's error; nothing is shown and nothing polls", async () => {
    const missing = new AgentCoreError("missing", 404);
    sessionAnswer = async () => { throw missing; };
    await expect(read()).rejects.toBe(missing);
    expect(client.getQueryData(options.queryKey)).toBeUndefined();
  });

  it("keeps the last history when the Session later fails, backs off, and stops once it is gone", async () => {
    const first = await read();
    sessionAnswer = async () => { throw new AgentCoreError("down", 503); };
    const stale = await read();
    expect(stale.history.session).toBe(first.history.session);
    expect(stale.gone).toBe(false);
    expect(sessionPollInterval(stale)).toBe(SESSION_POLL_MS * 2);

    sessionAnswer = async () => { throw new AgentCoreError("gone", 404); };
    const gone = await read();
    expect(gone.history.session).toBe(first.history.session);
    expect(gone.gone).toBe(true);
    expect(sessionPollInterval(gone)).toBe(false);

    sessionAnswer = async () => session("idle");
    const idle = await read();
    expect(idle.failures).toBe(0);
    expect(idle.error).toBeNull();
    expect(sessionPollInterval(idle)).toBe(false);
  });

  it("polls only while work is in flight", () => {
    const at = (status: string, failures = 0): SessionHistoryRead => ({
      history: { session: session(status), items: [], turns: [], sessionError: null, itemsError: null, turnsError: null, loadedAt: 0 },
      error: null, gone: false, failures,
    });
    expect(sessionPollInterval(undefined)).toBe(false);
    expect(sessionPollInterval(at("in_progress"))).toBe(SESSION_POLL_MS);
    expect(sessionPollInterval(at("requires_action", 1))).toBe(SESSION_POLL_MS * 2);
    expect(sessionPollInterval(at("idle"))).toBe(false);
  });
});

describe("Detail read retries", () => {
  it("does not retry a definite 4xx answer and retries anything else once", () => {
    expect(retryTransient(0, new AgentCoreError("missing", 404))).toBe(false);
    expect(retryTransient(0, new AgentCoreError("down", 503))).toBe(true);
    expect(retryTransient(1, new AgentCoreError("down", 503))).toBe(false);
    expect(retryTransient(0, new TypeError("Failed to fetch"))).toBe(true);
  });
});
