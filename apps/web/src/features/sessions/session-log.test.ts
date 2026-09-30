import { describe, expect, it } from "vitest";

import type { AgentSession, TolerantSessionList } from "@oac/agents-client";

import type { Owned } from "../../lib/projects";
import { type Project } from "../../lib/admin-view";
import {
  agentOptions,
  environmentKind,
  filterSessionLog,
  initialSessionLogFilters,
  isDeletable,
  isLogTruncated,
  readSessionLog,
  statusCounts,
  type SessionLogEntry,
  type SessionLogFilters,
} from "./session-log";

function project(id: string, name: string): Project {
  return { id, name, source: "console", status: "active", created_at: 1, archived_at: null, active_key_count: 1 } as Project;
}

const production = project("proj_prod", "Production");
const data = project("proj_data", "Data team");

function session(id: string, overrides: Partial<AgentSession> & { agentId?: string; agentName?: string | null } = {}): AgentSession {
  const { agentId = "agent_a", agentName = "Researcher", ...rest } = overrides;
  return {
    id,
    object: "agent.session",
    agent: {
      id: agentId,
      model: "gpt-5",
      name: agentName,
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
    metadata: {},
    required_actions: [],
    vault_ids: [],
    usage: null,
    created_at: 1,
    last_active_at: 1,
    ...rest,
  };
}

function row(owner: Project, value: AgentSession): Owned<SessionLogEntry> {
  return { project: owner, value: { kind: "session", session: value } };
}

function ids(rows: readonly Owned<SessionLogEntry>[]): string[] {
  return rows.map((entry) => (entry.value.kind === "session" ? entry.value.session.id : `?${entry.value.id ?? entry.value.key}`));
}

const filters = (patch: Partial<SessionLogFilters> = {}): SessionLogFilters => ({ ...initialSessionLogFilters, ...patch });

describe("Session log across projects", () => {
  const rows: Owned<SessionLogEntry>[] = [
    row(production, session("s1", { status: "failed", error: "Sandbox allocation failed", last_active_at: 30 })),
    row(production, session("s2", { status: "in_progress", last_active_at: 50, environment: { type: "openai_hosted" } as AgentSession["environment"] })),
    row(data, session("s3", { status: "idle", last_active_at: 40, agentId: "agent_b", agentName: "Analyst" })),
    row(data, session("s4", { status: "requires_action", last_active_at: 50, agentId: "agent_b", agentName: "Analyst" })),
    { project: data, value: { kind: "unrecognized", id: "5f0c0e0e-0000-4000-8000-000000000000", key: "5f0c0e0e-0000-4000-8000-000000000000" } },
  ];

  it("merges every project's Sessions by most recent activity, ties by ID, unrecognized last", () => {
    expect(ids(filterSessionLog(rows, filters()))).toEqual(["s2", "s4", "s3", "s1", "?5f0c0e0e-0000-4000-8000-000000000000"]);
  });

  it("filters by status, Agent, environment and search", () => {
    expect(ids(filterSessionLog(rows, filters({ status: "failed" })))).toEqual(["s1"]);
    expect(ids(filterSessionLog(rows, filters({ agentId: "agent_b" })))).toEqual(["s4", "s3"]);
    expect(ids(filterSessionLog(rows, filters({ environment: "openai_hosted" })))).toEqual(["s2"]);
    expect(ids(filterSessionLog(rows, filters({ query: "ALLOCATION" })))).toEqual(["s1"]);
    expect(ids(filterSessionLog(rows, filters({ query: "analyst" })))).toEqual(["s4", "s3"]);
    // An unrecognized entry has no status, Agent or environment; only its ID can match.
    expect(ids(filterSessionLog(rows, filters({ query: "5f0c0e0e" })))).toEqual(["?5f0c0e0e-0000-4000-8000-000000000000"]);
    expect(ids(filterSessionLog(rows, filters({ status: "idle" })))).toEqual(["s3"]);
  });

  it("counts statuses for the rows the other filters keep", () => {
    expect(statusCounts(rows, filters())).toEqual({ all: 5, in_progress: 1, requires_action: 1, failed: 1, idle: 1 });
    expect(statusCounts(rows, filters({ agentId: "agent_b", status: "failed" }))).toEqual({ all: 2, in_progress: 0, requires_action: 1, failed: 0, idle: 1 });
  });

  it("names Agents once and tells same-named Agents of different projects apart", () => {
    const shared = [
      ...rows,
      row(data, session("s5", { agentId: "agent_c", agentName: "Researcher" })),
      row(production, session("s6", { agentId: "agent_d", agentName: null })),
    ];
    expect(agentOptions(shared, "Unnamed Agent")).toEqual([
      { id: "agent_b", label: "Analyst" },
      { id: "agent_c", label: "Researcher · Data team" },
      { id: "agent_a", label: "Researcher · Production" },
      { id: "agent_d", label: "Unnamed Agent" },
    ]);
  });

  it("offers deletion only for idle or failed Sessions without pending actions", () => {
    expect(isDeletable(session("a", { status: "idle" }))).toBe(true);
    expect(isDeletable(session("b", { status: "failed" }))).toBe(true);
    expect(isDeletable(session("c", { status: "in_progress" }))).toBe(false);
    expect(isDeletable(session("d", { status: "requires_action" }))).toBe(false);
    expect(isDeletable(session("e", { status: "failed", required_actions: [{ type: "environment_connection", environment_id: "env" }] }))).toBe(false);
  });

  it("classifies environments and keeps unknown types as other", () => {
    expect(environmentKind(session("x", { environment: { type: "self_hosted" } as AgentSession["environment"] }))).toBe("self_hosted");
    expect(environmentKind(session("y", { environment: { type: "future" } as unknown as AgentSession["environment"] }))).toBe("other");
  });
});

describe("Reading a project's Session log", () => {
  function page(data: AgentSession[], unrecognized: TolerantSessionList["unrecognized"], hasMore: boolean, lastId: string | null): TolerantSessionList {
    return { object: "list", data, unrecognized, has_more: hasMore, first_id: data[0]?.id ?? null, last_id: lastId };
  }

  it("walks every page and lists unreadable entries without failing", async () => {
    const calls: Array<string | undefined> = [];
    const pages = [
      page([session("a"), session("b")], [{ index: 2, id: null }], true, "c"),
      page([session("d")], [{ index: 0, id: "e" }], false, "d"),
    ];
    const client = { listSessionsTolerant: async (options?: { after?: string }) => { calls.push(options?.after); return pages[calls.length - 1]!; } };
    const entries = await readSessionLog(client);
    expect(calls).toEqual([undefined, "c"]);
    expect(entries.map((entry) => (entry.kind === "session" ? entry.session.id : `?${entry.key}`))).toEqual(["a", "b", "?0:2", "d", "?e"]);
  });

  it("stops at the read bound and reports it", async () => {
    let calls = 0;
    const client = { listSessionsTolerant: async () => { calls += 1; return page([session(`s${calls}a`), session(`s${calls}b`)], [], true, `s${calls}b`); } };
    const entries = await readSessionLog(client, undefined, 3);
    expect(entries).toHaveLength(3);
    expect(calls).toBe(2);
    expect(isLogTruncated(entries.map((value) => ({ project: production, value })), 3)).toBe(true);
    expect(isLogTruncated(entries.slice(0, 2).map((value) => ({ project: production, value })), 3)).toBe(false);
  });
});
