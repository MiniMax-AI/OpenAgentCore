import { describe, expect, it, vi } from "vitest";
import { AdminClient } from "./admin-client";

const selection = { id: "11111111-1111-4111-8111-111111111111", adapter: "custom-fs", parameters: { root: "/storage" }, tenant_capacity_mib: 1024 };
function fixture(value: unknown = selection) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } }));
  return { fetch, client: new AdminClient({ fetch }) };
}
describe("workspace storage administration", () => {
  it("uses the single selection endpoint and omits credentials for capacity edits", async () => {
    const { client, fetch } = fixture();
    expect(await client.retrieveWorkspaceStorage()).toEqual(selection);
    await client.configureWorkspaceStorage(selection);
    expect(String(fetch.mock.calls[0]![0])).toBe("/core/v1/workspace-storage");
    expect(fetch.mock.calls[1]![1]?.method).toBe("PUT");
    expect(JSON.parse(String(fetch.mock.calls[1]![1]?.body))).toEqual(selection);
  });
  it("sends explicit credentials only in the write body", async () => {
    const { client, fetch } = fixture();
    const result = await client.configureWorkspaceStorage({ ...selection, credential: { secret: "private" } });
    expect(JSON.parse(String(fetch.mock.calls[0]![1]?.body)).credential).toEqual({ secret: "private" });
    expect(result).not.toHaveProperty("credential");
  });
  it.each([
    { ...selection, credential: { secret: "leaked" } },
    { ...selection, parameters: null },
    { ...selection, tenant_capacity_mib: -1 },
    { ...selection, tenant_capacity_mib: 4294967296 },
    { ...selection, id: "bad-id" },
    { ...selection, id: null },
  ])("rejects malformed or credential-bearing responses", async (value) => {
    await expect(fixture(value).client.retrieveWorkspaceStorage()).rejects.toThrow();
  });
});
