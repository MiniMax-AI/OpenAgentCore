import { describe, expect, it, vi } from "vitest";
import { AdminClient } from "./admin-client";

const projectId = "11111111-1111-4111-8111-111111111111";
const sessionId = "22222222-2222-4222-8222-222222222222";
const environmentId = "33333333-3333-4333-8333-333333333333";
const disposition = { session_id: sessionId, environment_id: environmentId, state: "cleanup_pending" };
function setup(value: unknown = disposition, status = 200) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => new Response(JSON.stringify(value), { status }));
  return { client: new AdminClient({ adminToken: "test-admin", fetch }), fetch };
}

describe("administrative Session archive", () => {
  it("posts only the expected generation and reads disposition through the same scoped route", async () => {
    const { client, fetch } = setup();
    const signal = new AbortController().signal;
    await expect(client.archiveSession(projectId, sessionId, { expected_generation: 7 }, { signal })).resolves.toEqual(disposition);
    await expect(client.retrieveSessionArchive(projectId, sessionId, { signal })).resolves.toEqual(disposition);
    expect(fetch).toHaveBeenCalledTimes(2);
    for (const [url, init] of fetch.mock.calls) {
      expect(url).toBe(`/core/v1/admin/projects/${projectId}/sessions/${sessionId}/archive`);
      expect(init).toMatchObject({ signal, credentials: "same-origin", redirect: "error" });
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer test-admin");
      expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
    }
    const post = fetch.mock.calls[0]![1]!;
    expect(post.method).toBe("POST");
    expect(new Headers(post.headers).get("Content-Type")).toBe("application/json");
    expect(JSON.parse(post.body as string)).toEqual({ expected_generation: 7 });
    expect(fetch.mock.calls[1]![1]).toMatchObject({ method: "GET" });
    expect(fetch.mock.calls[1]![1]?.body).toBeUndefined();
  });

  it.each(["active", "cleanup_pending", "released"])("accepts the %s resource disposition", async (state) => {
    const value = { ...disposition, state };
    await expect(setup(value).client.retrieveSessionArchive(projectId, sessionId)).resolves.toEqual(value);
  });

  it.each([
    { ...disposition, session_id: environmentId },
    { ...disposition, environment_id: "" },
    { ...disposition, state: "archived" },
    { ...disposition, state: null },
    { ...disposition, deleted: true },
    { session_id: sessionId, state: "released" },
  ])("rejects a malformed or mismatched response %j", async (value) => {
    const { client } = setup(value);
    await expect(client.archiveSession(projectId, sessionId, { expected_generation: 7 })).rejects.toMatchObject({ code: "invalid_admin_response", status: 502 });
    await expect(client.retrieveSessionArchive(projectId, sessionId)).rejects.toMatchObject({ code: "invalid_admin_response", status: 502 });
  });

  it.each([0, -1, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])("rejects generation %s before sending", async (expected_generation) => {
    const { client, fetch } = setup();
    await expect(client.archiveSession(projectId, sessionId, { expected_generation })).rejects.toBeInstanceOf(TypeError);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("does not retry a conflict or an uncertain mutation", async () => {
    const { client, fetch } = setup({ error: { message: "stale generation", code: "sandbox_deployment_conflict" } }, 409);
    await expect(client.archiveSession(projectId, sessionId, { expected_generation: 7 })).rejects.toMatchObject({ status: 409, code: "sandbox_deployment_conflict" });
    expect(fetch).toHaveBeenCalledOnce();
    fetch.mockClear().mockRejectedValue(new TypeError("connection lost"));
    await expect(client.archiveSession(projectId, sessionId, { expected_generation: 7 })).rejects.toThrow("connection lost");
    expect(fetch).toHaveBeenCalledOnce();
  });
});
