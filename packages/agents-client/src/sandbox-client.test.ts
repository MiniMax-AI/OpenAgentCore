import { describe, expect, it, vi } from "vitest";
import { SandboxAdminClient, SandboxProjectClient } from "./sandbox-client";

function response(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status }); }

describe("Core sandbox credential boundaries", () => {
  it("uses the explicit admin credential and admin routes without a project beta header", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response({ data: [] }));
    const admin = new SandboxAdminClient({ baseUrl: "https://core.example/core/v1/sandbox", token: "admin-only", fetch });
    await admin.retrieveDeployment(); await admin.listNodes(); await admin.listAllocations("node/a"); await admin.createEnrollment(); await admin.removeNode("node/a");
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([
      "https://core.example/core/v1/sandbox/deployment", "https://core.example/core/v1/sandbox/nodes",
      "https://core.example/core/v1/sandbox/nodes/node%2Fa/allocations", "https://core.example/core/v1/sandbox/enrollment-tokens",
      "https://core.example/core/v1/sandbox/nodes/node%2Fa",
    ]);
    for (const [, init] of fetch.mock.calls) {
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer admin-only");
      expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
    }
    expect(fetch.mock.calls[3]?.[1]?.body).toBe("{}");
    expect(fetch.mock.calls[4]?.[1]?.method).toBe("DELETE");
  });
  it("keeps project extension reads on project routes and forwards cancellation", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response({ data: [] }));
    const client = new SandboxProjectClient({ baseUrl: "/v1", token: () => "project-only", fetch });
    const controller = new AbortController();
    await client.listSandboxNodes({ signal: controller.signal });
    await client.retrieveSandboxPlacement("session/a");
    expect(fetch.mock.calls[0]?.[0]).toBe("/v1/sandbox/nodes");
    expect(fetch.mock.calls[0]?.[1]?.signal).toBe(controller.signal);
    expect(fetch.mock.calls[1]?.[0]).toBe("/v1/agents/sessions/session%2Fa/sandbox-placement");
    for (const [, init] of fetch.mock.calls) {
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer project-only");
      expect(new Headers(init?.headers).get("OpenAI-Beta")).toBe("agents=v1");
    }
  });
  it("preserves resource conflict errors without retry or fallback", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { code: "runtime_node_in_use", message: "Node has retained resources.", type: "conflict_error" } }, 409));
    const client = new SandboxAdminClient({ token: "admin-only", fetch });
    await expect(client.removeNode("busy")).rejects.toMatchObject({ status: 409, code: "runtime_node_in_use", message: "Node has retained resources." });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
