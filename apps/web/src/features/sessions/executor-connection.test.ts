import type { ExecutorCredentialList } from "@oac/agents-client";
import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";

import { boundExecutorCredential, executorConnectionState } from "./executor-connection";
import { executorConnectionQuery, EXECUTOR_CONNECTION_POLL_MS } from "./executor-connection-query";

const list = vi.hoisted(() => vi.fn());
vi.mock("../../lib/projects", () => ({ admin: { listExecutorCredentials: list } }));

const active = { key_id: "bound-key", created_at: "2026-09-28T01:00:00Z", revoked_at: null };
function read(status: ExecutorCredentialList["connection"]["status"] = "disconnected"): ExecutorCredentialList {
  return { data: [active], connection: { status, bound_key_id: status === "never_enrolled" ? null : "BOUND-KEY", enrolled_at: status === "never_enrolled" ? null : "2026-09-28T01:00:00Z", last_seen_at: null } };
}

afterEach(() => vi.clearAllMocks());

describe("executor connection evidence", () => {
  it("never treats an active credential or a recent heartbeat as a connection", () => {
    expect(executorConnectionState(read("never_enrolled"), false)).toBe("never_enrolled");
    const disconnected = read();
    disconnected.connection.last_seen_at = new Date().toISOString();
    expect(executorConnectionState(disconnected, false)).toBe("disconnected");
  });
  it("accepts Core connectivity even without a recorded heartbeat", () => {
    expect(executorConnectionState(read("connected"), false)).toBe("connected");
  });
  it("distinguishes bound-key revocation from unrelated revoked keys", () => {
    const result = read();
    result.data.push({ ...active, key_id: "unrelated", revoked_at: "2026-09-28T02:00:00Z" });
    expect(executorConnectionState(result, false)).toBe("disconnected");
    result.data[0] = { ...active, revoked_at: "2026-09-28T02:00:00Z" };
    expect(boundExecutorCredential(result)?.key_id).toBe("bound-key");
    expect(executorConnectionState(result, false)).toBe("revoked");
    result.connection.status = "connected";
    expect(executorConnectionState(result, false)).toBe("connected");
  });
  it("does not guess a binding from credential order", () => {
    const result = read();
    result.connection.bound_key_id = null;
    expect(boundExecutorCredential(result)).toBeUndefined();
    expect(executorConnectionState(result, false)).toBe("disconnected");
  });
  it("withholds connected completion on stale or unread evidence", () => {
    expect(executorConnectionState(read("connected"), true)).toBe("unknown");
    expect(executorConnectionState(undefined, false)).toBe("unknown");
  });
});

describe("executor connection read", () => {
  it("keeps the list and connection together, retains failed reads, and recovers without writes", async () => {
    const client = new QueryClient();
    try {
      const options = executorConnectionQuery("project", "session", "environment");
      const response = read("connected");
      response.data.unshift({ ...active, key_id: "old-revoked", revoked_at: "2026-09-28T02:00:00Z" });
      list.mockResolvedValueOnce(response);
      const result = await client.fetchQuery(options);
      expect(result.connection).toEqual(response.connection);
      expect(result.data.map((credential) => credential.key_id)).toEqual(["bound-key", "old-revoked"]);
      expect(response.data[0]?.key_id).toBe("old-revoked");
      list.mockRejectedValueOnce(new Error("unavailable"));
      await expect(client.fetchQuery({ ...options, staleTime: 0 })).rejects.toThrow("unavailable");
      expect(client.getQueryData(options.queryKey)).toEqual(result);
      expect(list).toHaveBeenCalledTimes(2);
      list.mockResolvedValueOnce(read("disconnected"));
      expect((await client.fetchQuery({ ...options, staleTime: 0 })).connection.status).toBe("disconnected");
    } finally { client.clear(); }
  });
  it("bounds polling to five seconds and disables background polls and retries", () => {
    const options = executorConnectionQuery("project", "session", "environment");
    expect(EXECUTOR_CONNECTION_POLL_MS).toBeGreaterThanOrEqual(5_000);
    expect(options.refetchInterval).toBe(EXECUTOR_CONNECTION_POLL_MS);
    expect(options.refetchIntervalInBackground).toBe(false);
    expect(options.retry).toBe(false);
  });
});
