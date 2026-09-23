import { describe, expect, it, vi } from "vitest";
import { SandboxAdminClient, SandboxProjectClient } from "./sandbox-client";

function response(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status }); }

describe("Core sandbox credential boundaries", () => {
  it("uses console authentication without sending a browser bearer and forwards cancellation", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response({ data: [] }));
    const client = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch });
    const controller = new AbortController();
    await client.listNodes({ signal: controller.signal });
    expect(fetch.mock.calls[0]?.[0]).toBe("/core/v1/sandbox/nodes");
    expect(new Headers(fetch.mock.calls[0]?.[1]?.headers).has("Authorization")).toBe(false);
    expect(fetch.mock.calls[0]?.[1]?.signal).toBe(controller.signal);
  });
  it("does not retry an enrollment write with an uncertain outcome", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Network failed"));
    const client = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch });
    await expect(client.createEnrollment()).rejects.toThrow("Network failed");
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("initializes using only the explicit provider and origin with cancellation and admin credentials", async () => {
    const deployment = { installation_id: "installation", provider: "docker", core_url: "https://core.example", maintenance: false, owner_epoch: 1 };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response(deployment));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin-only", fetch });
    const controller = new AbortController();
    expect(await admin.initializeDeployment({ provider: "docker", core_url: "https://core.example" }, { signal: controller.signal })).toEqual(deployment);
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe("/core/v1/sandbox/deployment");
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ provider: "docker", core_url: "https://core.example" });
    expect(init?.signal).toBe(controller.signal);
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer admin-only");
    expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
  });
  it.each([409, 503])("does not retry initialization after HTTP %s", async (status) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { message: "Setup failed", code: "sandbox_deployment_conflict" } }, status));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin", fetch });
    await expect(admin.initializeDeployment({ provider: "microsandbox", core_url: "https://core.example" })).rejects.toThrow("Setup failed");
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("does not retry an uncertain initialization transport failure", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Connection lost"));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin", fetch });
    await expect(admin.initializeDeployment({ provider: "docker", core_url: "https://core.example" })).rejects.toThrow("Connection lost");
    expect(fetch).toHaveBeenCalledTimes(1);
  });
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
