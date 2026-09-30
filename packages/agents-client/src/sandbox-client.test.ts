import { describe, expect, expectTypeOf, it, vi } from "vitest";
import { AgentCoreError, OpenAIAgentsClient } from "./client";
import { SandboxAdminClient, type SandboxNode } from "./sandbox-client";

function response(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status }); }

// Shapes as Core serializes them (store.RuntimeNode, RuntimeNodeDetail, RuntimeNodeAllocation, RuntimeDeploymentView).
const created = "2026-09-25T08:00:00.123456789Z";
/** A ready node: Core omits `diagnostic`. */
const node = {
  rollout: { state: "ready", ready_generation: 1 },
  id: "3b0c1f4e-8a2d-4c6b-9e7f-1a2b3c4d5e6f", name: "core-01", provider: "docker", online: true, provider_ready: true,
  cpu_count: 16, available_memory_bytes: 8589934592, available_disk_bytes: 107374182400, running: 1, snapshots: 0,
  last_seen_at: "2026-09-25T09:00:00Z", max_active: 2, max_retained: 2, active: 1, reserved: 0, retained: 1, cleanup_pending: 0,
  created_at: created, core_url: "https://core.example", enrollment_id: "9d2e6b1a-4c3f-4e8d-a7b6-5c4d3e2f1a0b",
};
/** Never heard from, enrolled before Core recorded enrollment IDs, with a fixed readiness code. */
const unready = {
  ...node, rollout: { state: "unknown", ready_generation: 1 }, id: "7f6e5d4c-3b2a-4190-8f7e-6d5c4b3a2918", online: false, provider_ready: false, diagnostic: "kvm_unavailable",
  cpu_count: null, available_memory_bytes: null, available_disk_bytes: null, running: 0, last_seen_at: null, active: 0, retained: 0, enrollment_id: null,
};
const detail = {
  ...node,
  host: { effective_cpu_cores: 3.5, cpu_utilization: 0.35, total_memory_bytes: 17179869184, available_memory_bytes: 8589934592, available_disk_bytes: 107374182400, observed_at: "2026-09-25T09:00:00Z" },
  history: { resolution_seconds: 60, points: [
    { start: "2026-09-25T08:58:00Z", cpu_utilization_max: 0.4, memory_used_bytes_max: 8589934592, available_disk_bytes_min: 107374182400 },
    { start: "2026-09-25T08:59:00Z", cpu_utilization_max: null, memory_used_bytes_max: null, available_disk_bytes_min: null },
  ] },
};
const unobserved = {
  ...unready,
  host: { effective_cpu_cores: null, cpu_utilization: null, total_memory_bytes: null, available_memory_bytes: null, available_disk_bytes: null, observed_at: null },
  history: { resolution_seconds: 900, points: [] },
};
const allocation = {
  deployment_generation: 1,
  id: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", node_id: node.id, tenant_id: "1b2c3d4e-5f6a-4b7c-8d9e-0f1a2b3c4d5e",
  session_id: "2c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e6f", environment_id: "3d4e5f6a-7b8c-4d9e-8f1a-2b3c4d5e6f7a",
  state: "running", compute_phase: "running", compute_phase_changed_at: null, diagnostic: "", initialization: "ready", created_at: created,
};
const runtime = { source_commit: "a".repeat(40), image_id: "sha256:" + "b".repeat(64), image_manifest_digest: "sha256:" + "c".repeat(64), microsandbox_ref: "oac-runtime@sha256:" + "d".repeat(64), runtime_sha256: "e".repeat(64), firmware_sha256: "f".repeat(64) };
const unconfigured = {
  credential_configured: false, rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: null }, installation_id: "", provider: "", core_url: "https://core.example", reset: null, owner_epoch: 0, generation: 0, mode: "", resources: { allocations: 0, pending: 0 }, suspension: null };
const docker = {
  configuration: {}, metadata: {},
  ...unconfigured, installation_id: "94be54a1-138c-4f30-bc87-b13686272dbe", provider: "docker", rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, owner_epoch: 1, generation: 1, mode: "nodes",
  resources: { allocations: 2, pending: 1 }, specification: { resources: { cpus: 2, memory_mib: 2048 }, runtime }, specification_digest: "0".repeat(64),
};
const microsandbox = {
  ...docker, provider: "microsandbox", specification: { resources: { cpus: 2, memory_mib: 2048, root_disk_mib: 8192, environment_disk_mib: 8192 }, runtime },
  suspension: { idle_seconds: 300, retention_seconds: 86400 },
};
/** An E2B selection saved before Core recorded its template build. */
const e2bDeployment = {
  ...docker, provider: "e2b", rollout: unconfigured.rollout, mode: "direct", specification: { resources: { cpus: 2, memory_mib: 2048 } },
  configuration: { template: "runtime:00000000-0000-0000-0000-000000000001", api_url: "https://api.e2b.app", domain: "e2b.app" } , credential_configured: true, metadata: { template_build: { status: null, resources: { cpus: null, memory_mib: null, root_disk_mib: null } } },
};
const reads: Record<string, (client: SandboxAdminClient) => Promise<unknown>> = {
  nodes: (client) => client.listNodes(),
  detail: (client) => client.retrieveNode(node.id, "1h"),
  unobserved: (client) => client.retrieveNode(unready.id, "24h"),
  allocations: (client) => client.listAllocations(node.id),
  deployment: (client) => client.retrieveDeployment(),
};
function read(name: string, body: unknown) {
  return reads[name]!(new SandboxAdminClient({ fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(response(body)) }));
}
const { enrollment_id: _enrollment, ...unenrolled } = node;
const { suspension: _suspension, ...unsuspended } = docker;
const { specification_digest: _digest, ...undigested } = docker;
const { compute_phase_changed_at: _changed, ...unphased } = allocation;

describe("strict sandbox administration projections", () => {
  it.each([
    ["nodes", "ready and unready", { data: [node, unready] }], ["nodes", "empty", { data: [] }],
    ["detail", "observed", detail], ["unobserved", "never observed", unobserved],
    ["allocations", "known and unknown phase times", { data: [allocation, { ...allocation, compute_phase: "suspended", compute_phase_changed_at: created, diagnostic: "node_unavailable" }] }],
    ["deployment", "unconfigured", unconfigured], ["deployment", "Docker", docker], ["deployment", "microsandbox", microsandbox], ["deployment", "E2B", e2bDeployment],
  ])("accepts %s as Core serializes it: %s", async (name, _, body) => {
    expect(await read(name, body)).toEqual(body);
  });

  it.each([
    ["nodes", "an unknown member", { data: [{ ...node, credential: "node-secret" }] }],
    ["nodes", "a missing enrollment_id", { data: [unenrolled] }],
    ["nodes", "an empty diagnostic", { data: [{ ...node, diagnostic: "" }] }],
    ["nodes", "a string count", { data: [{ ...node, cpu_count: "16" }] }],
    ["nodes", "a numeric timestamp", { data: [{ ...node, last_seen_at: 0 }] }],
    ["nodes", "a null list", { data: null }],
    ["detail", "a missing host", { ...node, history: detail.history }],
    ["detail", "an unknown host member", { ...detail, host: { ...detail.host, hostname: "core-01" } }],
    ["detail", "a string history point", { ...detail, history: { resolution_seconds: 60, points: [{ ...detail.history.points[0], cpu_utilization_max: "0.4" }] } }],
    ["detail", "another node", { ...detail, id: unready.id }],
    ["allocations", "a missing compute_phase_changed_at", { data: [unphased] }],
    ["allocations", "an unknown diagnostic", { data: [{ ...allocation, diagnostic: "raw provider text" }] }],
    ["allocations", "another node's allocation", { data: [{ ...allocation, node_id: unready.id }] }],
    ["deployment", "a missing suspension", unsuspended],
    ["deployment", "a specification without its digest", undigested],
    ["deployment", "an e2b member for Docker", { ...docker, configuration: e2bDeployment.configuration }],
    ["deployment", "E2B without its e2b member", { ...docker, provider: "e2b", mode: "direct" }],
    ["deployment", "an unknown mode", { ...docker, mode: "hybrid" }],
  ])("rejects %s with %s", async (name, _, body) => {
    await expect(read(name, body)).rejects.toMatchObject({ status: 502, code: "invalid_admin_response" });
  });

  it("never passes a reflected E2B key through", async () => {
    const reflected = { ...e2bDeployment, configuration: { ...e2bDeployment.configuration, api_key: "e2b-private-key" } };
    const error = await read("deployment", reflected).catch((caught: unknown) => caught);
    expect(error).toMatchObject({ status: 502, code: "invalid_admin_response" });
    expect(JSON.stringify(error) + String(error)).not.toContain("e2b-private-key");
  });
});

describe("Core sandbox credential boundaries", () => {
  it("uses Core-key routes for transient template discovery and projects safe fields", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(response({ templates: [{ id: "tpl_123", names: ["runtime"] }] }))
      .mockResolvedValueOnce(response({ builds: [{ id: "00000000-0000-0000-0000-000000000001", cpus: 2, memory_mib: 2048 }] }));
    const client = new SandboxAdminClient({ fetch });
    const input = { api_key: "private-test-key", api_url: "https://sandbox.sandbase.ai", domain: "sandbox.sandbase.ai" };
    expect(await client.listE2BTemplates(input)).toHaveLength(1);
    expect(await client.listE2BReadyBuilds("tpl_123", input)).toHaveLength(1);
    expect(fetch.mock.calls.map(([url]) => url)).toEqual(["/core/v1/sandbox/providers/e2b/discovery", "/core/v1/sandbox/providers/e2b/discovery"]);
    expect(fetch.mock.calls.every(([, init]) => init?.method === "POST" && !new Headers(init?.headers).has("Authorization") && String(init?.body).includes("private-test-key"))).toBe(true);
  });

  it("rejects extra fields in credentialed discovery responses", async () => {
    const input = { api_key: "private-test-key" };
    const templates = new SandboxAdminClient({ fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ templates: [], builds: [] })) });
    const builds = new SandboxAdminClient({ fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ builds: [], templates: [] })) });
    await expect(templates.listE2BTemplates(input)).rejects.toMatchObject({ code: "invalid_admin_response" });
    await expect(builds.listE2BReadyBuilds("tpl_123", input)).rejects.toMatchObject({ code: "invalid_admin_response" });
  });

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
  it("returns the node's fixed readiness diagnostic unchanged and reads an unknown code as provider_unavailable", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ data: [unready, { ...unready, diagnostic: "future_code" }, node] }));
    const { data } = await new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch }).listNodes();
    expect(data.map((entry) => entry.diagnostic)).toEqual(["kvm_unavailable", "provider_unavailable", undefined]);
    expectTypeOf<SandboxNode["diagnostic"]>().toEqualTypeOf<undefined | "" | "provider_unavailable" | "docker_unavailable" | "docker_limits_unsupported" | "runtime_download_failed" | "runtime_image_unavailable" | "kvm_unavailable" | "microsandbox_artifacts_unavailable" | "capacity_insufficient">();
  });
  it("requires each node's enrollment ID: a string, or null for nodes enrolled before Core recorded it", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ data: [node, unready] }));
    const { data } = await new SandboxAdminClient({ fetch }).listNodes();
    expect(data.map((entry) => entry.enrollment_id)).toEqual([node.enrollment_id, null]);
    expectTypeOf<SandboxNode["enrollment_id"]>().toEqualTypeOf<string | null>();
    expectTypeOf<{ enrollment_id: string }>().toExtend<Pick<SandboxNode, "enrollment_id">>();
    expectTypeOf<{ enrollment_id: null }>().toExtend<Pick<SandboxNode, "enrollment_id">>();
    expectTypeOf<Omit<SandboxNode, "enrollment_id">>().not.toExtend<SandboxNode>();
  });
  it("does not retry an enrollment write with an uncertain outcome", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Network failed"));
    const client = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch });
    await expect(client.createEnrollment()).rejects.toThrow("Network failed");
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("initializes using only the explicit provider with cancellation and admin credentials", async () => {
    const deployment = docker;
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response(deployment));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin-only", fetch });
    const controller = new AbortController();
    expect(await admin.initializeDeployment({ expected_generation: 0, provider: "docker" }, { signal: controller.signal })).toEqual(deployment);
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe("/core/v1/sandbox/deployment");
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ expected_generation: 0, provider: "docker" });
    expect(init?.signal).toBe(controller.signal);
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer admin-only");
    expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
  });
  it("forwards one specification with generation and preserves backend conflict details", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { code: "sandbox_specification_mismatch", message: "Node specification differs" } }, 409));
    const admin = new SandboxAdminClient({ token: "admin-only", fetch });
    const input = { provider: "docker" as const, resources: { cpus: 2, memory_mib: 2048 }, runtime: { source_commit: "a".repeat(40), image_id: "sha256:" + "b".repeat(64), image_manifest_digest: "sha256:" + "c".repeat(64), microsandbox_ref: "oac-runtime@sha256:" + "d".repeat(64), runtime_sha256: "e".repeat(64), firmware_sha256: "f".repeat(64) }, expected_generation: 3 };
    await expect(admin.updateDeployment(input)).rejects.toMatchObject({ status: 409, code: "sandbox_specification_mismatch" });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body))).toEqual(input);
  });
  it.each([409, 503])("does not retry initialization after HTTP %s", async (status) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { message: "Setup failed", code: "sandbox_deployment_conflict" } }, status));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin", fetch });
    await expect(admin.initializeDeployment({ expected_generation: 0, provider: "microsandbox" })).rejects.toThrow("The sandbox deployment cannot change in its current state.");
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("does not retry an uncertain initialization transport failure", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Connection lost"));
    const admin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", token: "admin", fetch });
    await expect(admin.initializeDeployment({ expected_generation: 0, provider: "docker" })).rejects.toThrow("Sandbox configuration could not be confirmed.");
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("uses the explicit admin credential and admin routes without a project beta header", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (url) => response(String(url).endsWith("/deployment") ? docker : { data: [] }));
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
      ...e2bDeployment, generation: 2, resources: { allocations: 0, pending: 0 },
      configuration: { template: e2b.template, api_url: "https://api.e2b.app", domain: "e2b.app" } , credential_configured: true, metadata: { template_build: { status: "ready", resources: { cpus: 2, memory_mib: 2048, root_disk_mib: 24063 } } },
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response(deployment));
    const client = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox", fetch });
    const controller = new AbortController();
    // Omitted E2B resources are filled from the validated template build.
    expect(await client.initializeDeployment({ expected_generation: 0, provider: "e2b", configuration: { template: e2b.template }, credential: { api_key: e2b.api_key } })).toEqual(deployment);
    await client.updateDeployment({ provider: "e2b", configuration: { template: e2b.template }, credential: { api_key: e2b.api_key }, expected_generation: 1 }, { signal: controller.signal });
    await client.cancelReset(2);
    expect(fetch.mock.calls.map(([url, init]) => [url, init?.method])).toEqual([
      ["/core/v1/sandbox/deployment", "POST"], ["/core/v1/sandbox/deployment", "PUT"], ["/core/v1/sandbox/deployment/reset?expected_generation=2", "DELETE"],
    ]);
    expect(JSON.parse(String(fetch.mock.calls[1]?.[1]?.body))).toEqual({ provider: "e2b", configuration: { template: e2b.template }, credential: { api_key: e2b.api_key }, expected_generation: 1 });
    expect(fetch.mock.calls[1]?.[1]?.signal).toBe(controller.signal);
    expect(fetch.mock.calls[2]?.[1]?.body).toBeUndefined();
    for (const [, init] of fetch.mock.calls) {
      expect(new Headers(init?.headers).has("Authorization")).toBe(false);
      expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
    }
  });
  it.each([409, 503])("does not expose reflected E2B keys or retry configuration after HTTP %s", async (status) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { message: e2b.api_key, code: e2b.api_key, param: e2b.api_key } }, status));
    const client = new SandboxAdminClient({ fetch });
    await expect(client.updateDeployment({ provider: "e2b", configuration: { template: e2b.template }, credential: { api_key: e2b.api_key }, expected_generation: 1 })).rejects.toMatchObject({ code: "sandbox_configuration_unconfirmed", status });
    await client.initializeDeployment({ expected_generation: 0, provider: "e2b", configuration: { template: e2b.template }, credential: { api_key: e2b.api_key } }).catch((error) => {
      expect(JSON.stringify(error)).not.toContain(e2b.api_key);
      expect(error.message).not.toContain(e2b.api_key);
    });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
  it("projects public-URL rejection to fixed copy even when the upstream message reflects a key", async () => {
    const rejection = { message: "E2B sandboxes reach Core over the internet. Set an HTTPS public URL that is not loopback.", code: "sandbox_configuration_error", param: null, type: "reflected" };
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(response({ error: rejection }, 409))
      .mockResolvedValueOnce(response({ error: { ...rejection, message: rejection.message + e2b.api_key } }, 409));
    const client = new SandboxAdminClient({ fetch });
    const shown = await client.initializeDeployment({ expected_generation: 0, provider: "e2b", configuration: { template: e2b.template }, credential: { api_key: e2b.api_key } }).catch((error: unknown) => error);
    expect(shown).toMatchObject({ status: 409, code: "sandbox_configuration_error", message: rejection.message, param: null });
    expect((shown as AgentCoreError).errorType).toBeUndefined();
    await expect(client.initializeDeployment({ expected_generation: 0, provider: "e2b", configuration: { template: e2b.template }, credential: { api_key: e2b.api_key } })).rejects.toMatchObject({ status: 409, code: "sandbox_configuration_error", message: rejection.message, param: null });
  });
  it("keeps legacy E2B ownership reset actionable without reflecting either key", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: {
      code: "sandbox_reset_required", message: "revoked-old-key " + e2b.api_key,
      param: "revoked-old-key", details: { current_provider: "e2b", requested_provider: "e2b", secret: e2b.api_key },
    } }, 409));
    const client = new SandboxAdminClient({ fetch });
    const error = await client.updateDeployment({ provider: "e2b", configuration: { template: e2b.template }, credential: { api_key: e2b.api_key }, expected_generation: 1 }).catch((error: unknown) => error);
    expect(error).toMatchObject({ status: 409, code: "sandbox_reset_required", message: "Reset the sandbox deployment before changing this configuration.", param: null });
    expect((error as AgentCoreError).details).toBeUndefined();
    expect(JSON.stringify(error)).not.toContain("revoked-old-key");
    expect(JSON.stringify(error)).not.toContain(e2b.api_key);
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("never retries uncertain switch or reset writes", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("Connection lost"));
    const client = new SandboxAdminClient({ fetch });
    await expect(client.updateDeployment({ provider: "docker", expected_generation: 1 })).rejects.toThrow();
    await expect(client.startReset({ clear: "auto", expected_generation: 1 })).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});

const resetting = {
  ...docker,
  reset: { clear: "auto", requested_at: created, deadline_at: "2026-09-25T09:00:00Z", forced_at: null,
    remaining: { busy: 1, idle: 1, cleanup: 1, on_offline_nodes: 2,
      offline_nodes: [{ node_id: node.id, name: node.name, resources: 2 }] } },
};
describe("durable reset projection", () => {
  it("preserves Core's complete remaining snapshot and copies blocker identities", async () => {
    const result = await read("deployment", resetting) as typeof resetting;
    expect(result).toEqual(resetting);
    expect(result.reset.remaining.offline_nodes).not.toBe(resetting.reset.remaining.offline_nodes);
  });
  it.each([
    { ...resetting, maintenance: true },
    { ...resetting, rollout: { nodes: { ready: 1 } } },
    { ...resetting, reset: { ...resetting.reset, clear: "unknown" } },
    { ...resetting, reset: { ...resetting.reset, deadline_at: null } },
    { ...resetting, reset: { ...resetting.reset, forced_at: created } },
    { ...resetting, reset: { ...resetting.reset, remaining: { ...resetting.reset.remaining, busy: 2 } } },
    { ...resetting, reset: { ...resetting.reset, remaining: { ...resetting.reset.remaining, on_offline_nodes: 1 } } },
    { ...resetting, reset: { ...resetting.reset, remaining: { ...resetting.reset.remaining, offline_nodes: [
      { node_id: node.id, name: node.name, resources: 1 }, { node_id: node.id, name: node.name, resources: 1 },
    ] } } },
  ])("rejects retired/fictional fields or an inconsistent reset snapshot", async value => {
    await expect(read("deployment", value)).rejects.toMatchObject({ code: "invalid_admin_response" });
  });
  it("posts the explicit reset once with the requested deadline", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response(resetting));
    const client = new SandboxAdminClient({ fetch });
    await expect(client.startReset({ expected_generation: 1, clear: "auto", deadline_seconds: 300 })).resolves.toEqual(resetting);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(String(fetch.mock.calls[0]?.[0])).toBe("/core/v1/sandbox/deployment/reset");
    expect(fetch.mock.calls[0]?.[1]?.method).toBe("POST");
    expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body))).toEqual({ expected_generation: 1, clear: "auto", deadline_seconds: 300 });
  });
  it("keeps safe E2B reset errors without reflecting credential-bearing error fields", async () => {
    const secret = "test-only-key";
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: {
      message: secret, param: secret, type: secret, code: "sandbox_generation_stale",
      details: { current_generation: 7, arbitrary: secret },
    } }, 409));
    const error = await new SandboxAdminClient({ fetch }).updateDeployment({ provider: "e2b", expected_generation: 1,
      credential: { api_key: secret }, configuration: { template: "runtime:00000000-0000-0000-0000-000000000001" } }).catch(error => error);
    expect(error).toMatchObject({ code: "sandbox_generation_stale", status: 409, details: { current_generation: 7 } });
    expect(JSON.stringify(error)).not.toContain(secret);
    expect(error.message).not.toContain(secret);
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});

it("uses the authoritative rollout signal instead of retained old Sessions for polling", async () => {
  const settled = { ...e2bDeployment, rollout: { state: "settled", previous_generation_sandboxes: 2, nodes: null } };
  const client = new SandboxAdminClient({ fetch: async () => response(settled) });
  expect((await client.retrieveDeployment()).rollout).toEqual(settled.rollout);
  for (const rollout of [
    { ...settled.rollout, state: "preparing" },
    { ...settled.rollout, previous_generation_sandboxes: 4 },
    { ...docker.rollout, state: "preparing" },
    { ...docker.rollout, nodes: { ...docker.rollout.nodes, unknown: -1 } },
  ]) {
    const invalid = new SandboxAdminClient({ fetch: async () => response({ ...docker, rollout }) });
    await expect(invalid.retrieveDeployment()).rejects.toMatchObject({ code: "invalid_admin_response" });
  }
});

it("omits a preserved key and submits an explicit same key once without retry", async () => {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response(e2bDeployment));
  const client = new SandboxAdminClient({ fetch });
  await client.updateDeployment({ provider: "e2b", expected_generation: 1, configuration: { template: e2bDeployment.configuration.template } });
  await client.updateDeployment({ provider: "e2b", expected_generation: 1, configuration: { template: e2bDeployment.configuration.template }, credential: { api_key: "same-key" } });
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(JSON.parse(String(fetch.mock.calls[0]![1]!.body))).not.toHaveProperty("credential");
  expect(JSON.parse(String(fetch.mock.calls[1]![1]!.body)).credential.api_key).toBe("same-key");
});

it("keeps omitted-key public-URL errors actionable without reflecting a stored key", async () => {
  const secret = "previously-stored-secret";
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response({ error: { code: "sandbox_configuration_error", message: `arbitrary upstream ${secret}`, param: secret, details: { credential: secret } } }, 409));
  const client = new SandboxAdminClient({ fetch });
  const error = await client.updateDeployment({ provider: "e2b", expected_generation: 1, configuration: { template: e2bDeployment.configuration.template } }).catch(error => error);
  expect(error).toMatchObject({ status: 409, code: "sandbox_configuration_error", param: null, message: "E2B sandboxes reach Core over the internet. Set an HTTPS public URL that is not loopback." });
  expect(JSON.stringify(error)).not.toContain(secret);
  expect(error.message).not.toContain(secret);
  expect(error.details).toBeUndefined();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it.each(["unknown", "preparing", "failed", "update_required"])("does not erase old serving readiness for target %s", async (state) => {
  const value = { ...node, rollout: { state, ready_generation: 1, ...(state === "failed" ? { diagnostic: "runtime_download_failed" } : {}) } };
  const client = new SandboxAdminClient({ fetch: async () => response({ data: [value] }) });
  expect((await client.listNodes()).data[0]).toEqual(value);
});

 it("preserves artifact transfer diagnostics on node and target without exposing transport details", async () => {
 const value = { ...unready, online: true, diagnostic: "runtime_download_failed", rollout: { state: "failed", ready_generation: 1, diagnostic: "runtime_download_failed" } };
 const client = new SandboxAdminClient({ fetch: async () => response({ data: [value] }) });
 expect((await client.listNodes()).data[0]).toEqual(value);
 });
