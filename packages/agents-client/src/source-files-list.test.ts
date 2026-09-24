import { describe, expect, it } from "vitest";

import { AgentCoreError, OpenAIAgentsClient } from "./client";
import fixture from "./fixtures/parsar-d3f55046/source-files-list.json";

interface FetchCall {
  input: RequestInfo | URL;
  init?: RequestInit;
}

type Recorded = { status: number; body: unknown };

function recordingClient(...responses: Recorded[]): { client: OpenAIAgentsClient; calls: FetchCall[] } {
  const calls: FetchCall[] = [];
  const client = new OpenAIAgentsClient({
    token: "test-token",
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init });
      const next = responses[Math.min(calls.length - 1, responses.length - 1)]!;
      return new Response(JSON.stringify(next.body), { status: next.status, headers: { "Content-Type": "application/json" } });
    }) as typeof fetch,
  });
  return { client, calls };
}

const { responses } = fixture;
const [newest, middle] = responses.list_first_page.body.data;
const oldest = responses.list_second_page.body.data[0]!;

function page(data: unknown[], hasMore = false, ids = data as Array<{ id: string }>) {
  return { object: "list", data, has_more: hasMore, first_id: ids[0]?.id ?? null, last_id: ids.at(-1)?.id ?? null };
}

describe("Files list", () => {
  it("reads cursor pages without the Agents Beta header", async () => {
    const { client, calls } = recordingClient(responses.list_first_page, responses.list_second_page);

    const first = await client.listSourceFiles({ limit: 2 });
    const second = await client.listSourceFiles({ limit: 2, after: first.last_id! });

    expect(String(calls[0]?.input)).toBe(`/v1/files?${responses.list_first_page.request.query}`);
    expect(String(calls[1]?.input)).toBe(`/v1/files?${responses.list_second_page.request.query}`);
    for (const call of calls) {
      const headers = new Headers(call.init?.headers);
      expect(headers.has("OpenAI-Beta")).toBe(false);
      expect(headers.get("Authorization")).toBe("Bearer test-token");
    }
    expect(first).toEqual(responses.list_first_page.body);
    expect(second).toEqual(responses.list_second_page.body);
  });

  it("sends order and purpose and checks the ascending order", async () => {
    const { client, calls } = recordingClient(responses.list_ascending);

    const listed = await client.listSourceFiles({ limit: 100, order: "asc", purpose: "user_data" });

    expect(String(calls[0]?.input)).toBe("/v1/files?limit=100&order=asc&purpose=user_data");
    expect(listed.data.map((file) => file.id)).toEqual([oldest.id, middle!.id, newest!.id]);

    const reversed = recordingClient(responses.list_ascending);
    await expect(reversed.client.listSourceFiles({ order: "desc" })).rejects.toMatchObject({ code: "invalid_source_file_list" });
  });

  it("projects an empty list, including an empty file", async () => {
    const { client } = recordingClient(responses.list_empty);
    await expect(client.listSourceFiles({ limit: 100 })).resolves.toEqual(responses.list_empty.body);
    expect(newest?.bytes).toBe(0);
  });

  it("keeps a listed File with unsupported metadata as unrecognized, without guessing", async () => {
    const assistants = { ...middle, purpose: "assistants", filename: "private-name.txt" };
    const { client } = recordingClient({ status: 200, body: page([newest, assistants, oldest]) });

    const listed = await client.listSourceFiles();

    expect(listed.data).toEqual([newest, { id: middle!.id, object: "file", unrecognized: true }, oldest]);
    expect(JSON.stringify(listed)).not.toContain("private-name.txt");
  });

  it.each([
    ["an entry without a File ID", page([{ ...newest, id: "notes.txt" }])],
    ["a mismatched first_id", { ...page([newest, middle]), first_id: middle!.id }],
    ["a duplicate File", page([newest, newest])],
    ["more after an empty page", page([], true)],
    ["an unexpected envelope field", { ...page([newest]), next: null }],
    ["a missing has_more", { object: "list", data: [], first_id: null, last_id: null }],
  ])("rejects %s", async (_label, body) => {
    const { client } = recordingClient({ status: 200, body });

    await expect(client.listSourceFiles()).rejects.toMatchObject({ status: 502, code: "invalid_source_file_list" });
  });

  it("rejects a page larger than the requested limit", async () => {
    const { client } = recordingClient({ status: 200, body: page([newest, middle, oldest]) });

    await expect(client.listSourceFiles({ limit: 2 })).rejects.toBeInstanceOf(AgentCoreError);
  });

  it.each([
    [{ limit: 0 }],
    [{ limit: 10001 }],
    [{ limit: 1.5 }],
    [{ order: "newest" }],
    [{ after: "notes.txt" }],
    [{ purpose: "assistants" }],
  ])("refuses %j before any request", async (options) => {
    const { client, calls } = recordingClient(responses.list_empty);

    await expect(client.listSourceFiles(options as never)).rejects.toBeInstanceOf(TypeError);
    expect(calls).toHaveLength(0);
  });

  it("maps Core errors", async () => {
    const unavailable = recordingClient(responses.storage_unavailable);
    await expect(unavailable.client.listSourceFiles({ limit: 100 })).rejects.toMatchObject({
      status: 503, code: "file_storage_unavailable", errorType: "server_error",
    });

    const invalid = recordingClient(responses.list_invalid_purpose);
    await expect(invalid.client.listSourceFiles()).rejects.toMatchObject({ status: 400, param: "purpose", code: null });

    const denied = recordingClient(responses.content_denied);
    await expect(denied.client.downloadSourceFile(middle!.id)).rejects.toMatchObject({
      status: 400, message: "Not allowed to download files of purpose: user_data",
    });
  });

  it("projects upload and deletion receipts from the same fixture", async () => {
    const { client, calls } = recordingClient(responses.uploaded, responses.deleted);

    const uploaded = await client.uploadSourceFile({ file: new Blob(["x".repeat(42)]), filename: "input.csv" });
    const deleted = await client.deleteSourceFile(uploaded.id);

    expect(uploaded).toEqual(responses.uploaded.body);
    expect(deleted).toEqual(responses.deleted.body);
    expect(calls.map((call) => new Headers(call.init?.headers).has("OpenAI-Beta"))).toEqual([false, false]);
  });
});
