import { afterEach, describe, expect, it, vi } from "vitest";
import { createConsoleKey, listConsoleKeys, revokeConsoleKey, safeKey } from "./api-keys";
const metadata = { id: "11111111-1111-4111-8111-111111111111", name: "Laptop", prefix: "pc_example", created_at: "2026-09-24T00:00:00Z", revoked_at: null };
afterEach(() => vi.unstubAllGlobals());
describe("console API key client", () => {
  it("projects only metadata even if a server response unexpectedly includes private fields", () => {
    expect(safeKey({ ...metadata, key: "never-retain", token_sha256: "never-retain", binding_digest: "never-retain" })).toEqual(metadata);
    expect(() => safeKey({ ...metadata, prefix: "a".repeat(40) })).toThrow();
    expect(() => safeKey({ ...metadata, created_at: "bad" })).toThrow();
  });
  it("uses the fixed same-origin console endpoint and never accepts a caller-selected parent", async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ ...metadata, key: "pc_" + "x".repeat(43) })));
    vi.stubGlobal("fetch", fetcher);
    const issued = await createConsoleKey(metadata.id, "Laptop");
    expect(issued.key).toHaveLength(46);
    expect(fetcher).toHaveBeenCalledWith("/console/api-keys", expect.objectContaining({ method: "POST", credentials: "same-origin", body: JSON.stringify({ id: metadata.id, name: "Laptop" }) }));
  });
  it("does not repeat an uncertain issuance or expose an error response body", async () => {
    const fetcher = vi.fn(async () => new Response('secret-error-body', { status: 502 }));
    vi.stubGlobal("fetch", fetcher);
    await expect(createConsoleKey(metadata.id, "Laptop")).rejects.toMatchObject({ status: 502, message: "API key operation failed." });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("checks issuance and revocation identities rather than claiming another key succeeded", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ ...metadata, key: "pc_" + "x".repeat(43) }))));
    await expect(createConsoleKey("22222222-2222-4222-8222-222222222222", "Laptop")).rejects.toThrow();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ id: "another", deleted: true }))));
    await expect(revokeConsoleKey(metadata.id)).rejects.toThrow();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ data: [{ ...metadata, key: "never-retain" }] }))));
    expect(await listConsoleKeys()).toEqual([metadata]);
  });
});
