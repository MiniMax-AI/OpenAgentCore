import { describe, expect, it } from "vitest";

import { OpenAIAgentsClient } from "./client";
import templates from "./fixtures/parsar-d3f55046/environment-templates.json";

interface FetchCall {
  input: RequestInfo | URL;
  init?: RequestInit;
}

function recordingClient(...bodies: unknown[]): { client: OpenAIAgentsClient; calls: FetchCall[] } {
  const calls: FetchCall[] = [];
  const client = new OpenAIAgentsClient({
    token: "test-token",
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init });
      const body = bodies[Math.min(calls.length - 1, bodies.length - 1)];
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    }) as typeof fetch,
  });
  return { client, calls };
}

const ids = [
  "11111111-1111-4111-8111-111111111111",
  "22222222-2222-4222-8222-222222222222",
  "33333333-3333-4333-8333-333333333333",
  "44444444-4444-4444-8444-444444444444",
  "55555555-5555-4555-8555-555555555555",
] as const;

function session(id: string, createdAt: number, overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id,
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
    environment: { type: "none" },
    status: "idle",
    error: null,
    metadata: {},
    required_actions: [],
    vault_ids: [],
    usage: null,
    created_at: createdAt,
    last_active_at: createdAt,
    ...overrides,
  };
}

/** A Core page whose cursors name the first and last entries as returned. */
function page(data: unknown[], hasMore = false): Record<string, unknown> {
  const entryId = (entry: unknown) => (entry as { id?: unknown } | null)?.id ?? null;
  return {
    object: "list",
    data,
    has_more: hasMore,
    first_id: data.length > 0 ? entryId(data[0]) : null,
    last_id: data.length > 0 ? entryId(data.at(-1)) : null,
  };
}

const newest = session(ids[0], 500);
const malformed = session(ids[1], 400, { status: "paused" });
const older = session(ids[2], 300);

describe("tolerant Session list", () => {
  it("keeps the other Sessions of a page and reports only the malformed entry", async () => {
    const { client, calls } = recordingClient(page([newest, malformed, older]));

    const listed = await client.listSessionsTolerant({ limit: 3, agentId: "agent" });

    expect(String(calls[0]?.input)).toBe("/v1/agents/sessions?limit=3&agent_id=agent");
    expect(new Headers(calls[0]?.init?.headers).get("OpenAI-Beta")).toBe("agents=v1");
    expect(listed.data.map((entry) => entry.id)).toEqual([ids[0], ids[2]]);
    expect(listed.unrecognized).toEqual([{ index: 1, id: ids[1] }]);
    expect(listed).toMatchObject({ object: "list", has_more: false, first_id: ids[0], last_id: ids[2] });
    expect(JSON.stringify(listed)).not.toContain("paused");
  });

  it("reports an entry without a Session ID by index only", async () => {
    const { client } = recordingClient(page([
      newest,
      { ...older, id: "notes.txt", object: "file" },
      null,
      session(ids[3], 200),
    ]));

    const listed = await client.listSessionsTolerant();

    expect(listed.data.map((entry) => entry.id)).toEqual([ids[0], ids[3]]);
    expect(listed.unrecognized).toEqual([{ index: 1, id: null }, { index: 2, id: null }]);
  });

  it("continues pagination from a page that ends in an unrecognized Session", async () => {
    const trailing = session(ids[2], 300, { environment: { type: "openai_hosted", id: "environment" } });
    const { client, calls } = recordingClient(
      page([newest, session(ids[1], 400), trailing], true),
      page([session(ids[3], 200), session(ids[4], 100)]),
    );

    const first = await client.listSessionsTolerant({ limit: 3 });
    const second = await client.listSessionsTolerant({ limit: 3, after: first.last_id! });

    expect(first.unrecognized).toEqual([{ index: 2, id: ids[2] }]);
    expect(first).toMatchObject({ has_more: true, last_id: ids[2] });
    expect(String(calls[1]?.input)).toBe(`/v1/agents/sessions?after=${ids[2]}&limit=3`);
    expect(second.data.map((entry) => entry.id)).toEqual([ids[3], ids[4]]);
    expect(second.unrecognized).toEqual([]);
    expect(second.has_more).toBe(false);
  });

  it("keeps a page of only unrecognized Sessions readable", async () => {
    const unknownFirst = { ...newest, future: true };
    const idless = { ...older, id: 7 };
    const { client } = recordingClient({ object: "list", data: [unknownFirst, idless], has_more: true, first_id: ids[0], last_id: ids[4] });

    const listed = await client.listSessionsTolerant();

    expect(listed.data).toEqual([]);
    expect(listed.unrecognized).toEqual([{ index: 0, id: ids[0] }, { index: 1, id: null }]);
    expect(listed.last_id).toBe(ids[4]);
  });

  it("recognizes a Session created from an advanced Template", async () => {
    const environment = templates.responses.session_environment_from_advanced_template.body;
    const { client } = recordingClient(page([session(ids[0], 500, { environment })]));

    const listed = await client.listSessionsTolerant();

    expect(listed.unrecognized).toEqual([]);
    expect(listed.data[0]?.environment).toEqual(environment);
  });

  it.each([
    ["a missing has_more", { object: "list", data: [newest], first_id: ids[0], last_id: ids[0] }],
    ["an unexpected envelope field", { ...page([newest]), next: null }],
    ["another object type", { ...page([newest]), object: "page" }],
    ["data that is not a list", { ...page([]), data: {} }],
    ["a first_id that differs from the first Session", { ...page([newest, older]), first_id: ids[2] }],
    ["a last_id that differs from an unrecognized last entry", { ...page([newest, malformed]), last_id: ids[0] }],
    ["a missing cursor beside an unidentified entry", { ...page([newest, null]), last_id: null }],
    ["cursors on an empty page", { ...page([]), first_id: ids[0] }],
    ["more after an empty page", page([], true)],
    ["a duplicate Session", page([newest, newest])],
    ["a duplicate unrecognized Session", page([newest, { ...malformed, id: ids[0] }])],
    ["Sessions out of creation order", page([older, newest])],
  ])("still fails the whole page for %s", async (_label, body) => {
    const { client } = recordingClient(body);

    await expect(client.listSessionsTolerant()).rejects.toMatchObject({ status: 502, code: "invalid_session_list" });
  });

  it("checks the page size and order against the request", async () => {
    const oversized = recordingClient(page([newest, older]));
    await expect(oversized.client.listSessionsTolerant({ limit: 1 })).rejects.toMatchObject({ code: "invalid_session_list" });

    const ascending = recordingClient(page([older, newest]));
    await expect(ascending.client.listSessionsTolerant({ order: "asc" })).resolves.toMatchObject({ data: [{ id: ids[2] }, { id: ids[0] }] });
  });

  it.each([[{ limit: 0 }], [{ limit: 101 }], [{ limit: 1.5 }], [{ order: "newest" }]])(
    "refuses %j before any request",
    async (options) => {
      const { client, calls } = recordingClient(page([]));

      await expect(client.listSessionsTolerant(options as never)).rejects.toBeInstanceOf(TypeError);
      expect(calls).toHaveLength(0);
    },
  );

  it("leaves the strict list failing on the same malformed Session", async () => {
    const { client } = recordingClient(page([newest, malformed, older]));

    await expect(client.listSessions()).rejects.toMatchObject({ status: 502, code: "invalid_session_resource" });
  });
});
