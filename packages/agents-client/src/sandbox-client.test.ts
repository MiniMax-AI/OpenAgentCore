import { describe, expect, expectTypeOf, it, vi } from "vitest";
import { AgentCoreError, OpenAIAgentsClient } from "./client";
import { SandboxAdminClient, type SandboxNode } from "./sandbox-client";

function response(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status }); }

describe("Core sandbox credential boundaries", () => {
  it("defaults to /core/v1/sandbox and is not a /v1 client", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response({ data: [] }));
    const client = new SandboxAdminClient({ fetch });
    expect(client).not.toBeInstanceOf(OpenAIAgentsClient);
    await client.listNodes();
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe("/core/v1/sandbox/nodes");
    expect(init).toMatchObject({ credentials: "same-origin", redirect: "error" });
    expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
  });
  it("uses console authentication without sending a browser bearer and forwards cancellation", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response({ data: [] }));
    const client = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch });
    const controller = new AbortController();
    await client.listNodes({ signal: controller.signal });
    expect(fetch.mock.calls[0]?.[0]).toBe("/core/v1/sandbox/nodes");
    expect(new Headers(fetch.mock.calls[0]?.[1]?.headers).has("Authorization")).toBe(false);
    expect(fetch.mock.calls[0]?.[1]?.signal).toBe(controller.signal);
  });
  it("returns the node's fixed readiness diagnostic unchanged", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ data: [{ id: "node", provider_ready: false, diagnostic: "kvm_unavailable" }] }));
    const { data } = await new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch }).listNodes();
    expect(data[0]?.diagnostic).toBe("kvm_unavailable");
    expectTypeOf<SandboxNode["diagnostic"]>().toEqualTypeOf<undefined | "" | "provider_unavailable" | "docker_unavailable" | "docker_limits_unsupported" | "runtime_image_unavailable" | "kvm_unavailable" | "microsandbox_artifacts_unavailable" | "capacity_insufficient">();
  });
  it("does not retry an enrollment write with an uncertain outcome", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Network failed"));
    const client = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch });
    await expect(client.createEnrollment()).rejects.toThrow("Network failed");
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("initializes using only the explicit provider with cancellation and admin credentials", async () => {
    const deployment = { installation_id: "installation", provider: "docker", core_url: "https://core.example", maintenance: false, owner_epoch: 1 };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response(deployment));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin-only", fetch });
    const controller = new AbortController();
    expect(await admin.initializeDeployment({ provider: "docker" }, { signal: controller.signal })).toEqual(deployment);
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe("/core/v1/sandbox/deployment");
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ provider: "docker" });
    expect(init?.signal).toBe(controller.signal);
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer admin-only");
    expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
  });
  it("forwards one specification with generation and preserves backend conflict details", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { code: "sandbox_specification_mismatch", message: "Node specification differs" } }, 409));
    const admin = new SandboxAdminClient({ token: "admin-only", fetch });
    const input = { provider: "docker" as const, resources: { cpus: 2, memory_mib: 2048 }, runtime: { source_commit: "a".repeat(40), image_id: "sha256:" + "b".repeat(64), image_manifest_digest: "sha256:" + "c".repeat(64), microsandbox_ref: "parsar-core-runtime@sha256:" + "d".repeat(64), runtime_sha256: "e".repeat(64), firmware_sha256: "f".repeat(64) }, expected_generation: 3 };
    await expect(admin.updateDeployment(input)).rejects.toMatchObject({ status: 409, code: "sandbox_specification_mismatch" });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body))).toEqual(input);
  });
  it.each([409, 503])("does not retry initialization after HTTP %s", async (status) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { message: "Setup failed", code: "sandbox_deployment_conflict" } }, status));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin", fetch });
    await expect(admin.initializeDeployment({ provider: "microsandbox" })).rejects.toThrow("Setup failed");
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("does not retry an uncertain initialization transport failure", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Connection lost"));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin", fetch });
    await expect(admin.initializeDeployment({ provider: "docker" })).rejects.toThrow("Connection lost");
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
  it("preserves resource conflict errors without retry or fallback", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { code: "runtime_node_in_use", message: "Node has retained resources.", type: "conflict_error" } }, 409));
    const client = new SandboxAdminClient({ token: "admin-only", fetch });
    await expect(client.removeNode("busy")).rejects.toMatchObject({ status: 409, code: "runtime_node_in_use", message: "Node has retained resources." });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});

describe("hosted provider configuration", () => {
  const e2b = { api_key: "test-only-secret", template: "runtime:00000000-0000-0000-0000-000000000001" };
  it("writes E2B configuration and generation without beta headers or browser credentials", async () => {
    const deployment = {
      generation: 2, mode: "direct", resources: { allocations: 0, pending: 0 }, suspension: null,
      specification: { resources: { cpus: 2, memory_mib: 2048 } },
      e2b: { template: e2b.template, credential_configured: true, template_build: { status: "ready", resources: { cpus: 2, memory_mib: 2048, root_disk_mib: 24063 } } },
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response(deployment));
    const client = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch });
    const controller = new AbortController();
    // Omitted E2B resources are filled from the validated template build.
    expect(await client.initializeDeployment({ provider: "e2b", e2b })).toEqual(deployment);
    await client.updateDeployment({ provider: "e2b", e2b, expected_generation: 1 }, { signal: controller.signal });
    await client.setMaintenance({ maintenance: false, expected_generation: 2 });
    expect(fetch.mock.calls.map(([url, init]) => [url, init?.method])).toEqual([
      ["/core/v1/sandbox/deployment", "POST"], ["/core/v1/sandbox/deployment", "PUT"], ["/core/v1/sandbox/deployment/maintenance", "PATCH"],
    ]);
    expect(JSON.parse(String(fetch.mock.calls[1]?.[1]?.body))).toEqual({ provider: "e2b", e2b, expected_generation: 1 });
    expect(fetch.mock.calls[1]?.[1]?.signal).toBe(controller.signal);
    expect(JSON.parse(String(fetch.mock.calls[2]?.[1]?.body))).toEqual({ maintenance: false, expected_generation: 2 });
    for (const [, init] of fetch.mock.calls) {
      expect(new Headers(init?.headers).has("Authorization")).toBe(false);
      expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
    }
  });
  it.each([409, 503])("does not expose reflected E2B keys or retry configuration after HTTP %s", async (status) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { message: e2b.api_key, code: e2b.api_key, param: e2b.api_key } }, status));
    const client = new SandboxAdminClient({ fetch });
    await expect(client.updateDeployment({ provider: "e2b", e2b, expected_generation: 1 })).rejects.toMatchObject({ code: "sandbox_configuration_unconfirmed", status });
    await client.initializeDeployment({ provider: "e2b", e2b }).catch((error) => {
      expect(JSON.stringify(error)).not.toContain(e2b.api_key);
      expect(error.message).not.toContain(e2b.api_key);
    });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
  it("shows Core's public-URL rejection unless it reflects the E2B key", async () => {
    const rejection = { message: "E2B sandboxes reach Core over the internet. Set an HTTPS public URL that is not loopback.", code: "sandbox_configuration_error", param: null, type: "reflected" };
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(response({ error: rejection }, 409))
      .mockResolvedValueOnce(response({ error: { ...rejection, message: rejection.message + e2b.api_key } }, 409));
    const client = new SandboxAdminClient({ fetch });
    const shown = await client.initializeDeployment({ provider: "e2b", e2b }).catch((error: unknown) => error);
    expect(shown).toMatchObject({ status: 409, code: "sandbox_configuration_error", message: rejection.message, param: null });
    expect((shown as AgentCoreError).errorType).toBeUndefined();
    await expect(client.initializeDeployment({ provider: "e2b", e2b })).rejects.toMatchObject({ status: 409, code: "sandbox_configuration_unconfirmed" });
  });
  it("never retries uncertain switch or maintenance writes", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Connection lost"));
    const client = new SandboxAdminClient({ fetch });
    await expect(client.updateDeployment({ provider: "docker", expected_generation: 1 })).rejects.toThrow();
    await expect(client.setMaintenance({ maintenance: true, expected_generation: 1 })).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
