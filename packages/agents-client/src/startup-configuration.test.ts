import { describe, expect, it } from "vitest";

import { OpenAIAgentsClient } from "./client";

function response(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}

function fixture(): Record<string, unknown> {
  return {
    object: "agents.core.startup_configuration",
    schema_version: 1,
    supported: {
      harnesses: ["claude_sdk", "codex", "mcode"],
      managed_sandbox_providers: ["docker", "microsandbox"],
    },
    configured: {
      default_harness: "codex",
      enabled_harnesses: ["claude_sdk", "codex"],
      daemon_gateway: true,
      self_hosted: true,
      managed_sandbox: { enabled: true, provider: "docker", maintenance: false },
      model_providers: [
        { harness: "claude_sdk", endpoint_configured: false },
        { harness: "codex", endpoint_configured: true },
      ],
    },
  };
}

describe("Core startup configuration", () => {
  it("reads the exact authenticated extension path and returns a defensive projection", async () => {
    const calls: Array<{ input: RequestInfo | URL; init?: RequestInit }> = [];
    const body = fixture();
    const client = new OpenAIAgentsClient({
      baseUrl: "https://core.example/v1/",
      token: "project-key",
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ input, init });
        return response(body);
      }) as typeof fetch,
    });

    const abort = new AbortController();
    const result = await client.retrieveStartupConfiguration({ signal: abort.signal });
    (body.supported as { harnesses: string[] }).harnesses[0] = "mutated";

    expect(String(calls[0]?.input)).toBe("https://core.example/v1/agents/core/startup-configuration");
    expect(calls[0]?.init?.signal).toBe(abort.signal);
    expect(new Headers(calls[0]?.init?.headers).get("Authorization")).toBe("Bearer project-key");
    expect(result.supported.harnesses).toEqual(["claude_sdk", "codex", "mcode"]);
    expect(result.configured.managed_sandbox).toEqual({ enabled: true, provider: "docker", maintenance: false });
  });

  it.each([
    ["removed configuration discovery", (value: any) => { value.configuration_capabilities = {}; }],
    ["unknown field", (value: any) => { value.secret = "private"; }],
    ["unknown harness", (value: any) => { value.supported.harnesses[0] = "future"; }],
    ["unsorted harnesses", (value: any) => { value.supported.harnesses.reverse(); }],
    ["enabled harness missing from supported", (value: any) => { value.supported.harnesses = ["codex", "mcode"]; }],
    ["default not enabled", (value: any) => { value.configured.enabled_harnesses = ["claude_sdk"]; value.configured.model_providers = [{ harness: "claude_sdk", endpoint_configured: false }]; }],
    ["enabled harness without gateway", (value: any) => {
      value.configured.daemon_gateway = false;
      value.configured.self_hosted = false;
      value.configured.managed_sandbox = { enabled: false, provider: null, maintenance: false };
    }],
    ["managed provider without gateway", (value: any) => { value.configured.daemon_gateway = false; value.configured.self_hosted = false; }],
    ["managed provider missing from supported", (value: any) => { value.supported.managed_sandbox_providers = ["microsandbox"]; }],
    ["mismatched provider projection", (value: any) => { value.configured.model_providers[0].harness = "codex"; }],
  ])("rejects %s", async (_name, mutate) => {
    const body = fixture();
    mutate(body);
    const client = new OpenAIAgentsClient({ fetch: (async () => response(body)) as typeof fetch });
    await expect(client.retrieveStartupConfiguration()).rejects.toMatchObject({
      code: "invalid_startup_configuration",
      status: 502,
    });
  });

  it("does not reflect rejected private fields in its error", async () => {
    const body = fixture() as any;
    body.configured.private_endpoint = "https://user:secret@example.test/v1?token=private";
    const client = new OpenAIAgentsClient({ fetch: (async () => response(body)) as typeof fetch });

    await expect(client.retrieveStartupConfiguration()).rejects.not.toThrow(/user:secret|example\.test|token=private/u);
  });

  it("accepts a fully unconfigured execution surface", async () => {
    const body = fixture() as any;
    body.configured.daemon_gateway = false;
    body.configured.self_hosted = false;
    body.configured.managed_sandbox = { enabled: false, provider: null, maintenance: false };
    body.configured.enabled_harnesses = [];
    body.configured.model_providers = [];
    const client = new OpenAIAgentsClient({ fetch: (async () => response(body)) as typeof fetch });

    await expect(client.retrieveStartupConfiguration()).resolves.toMatchObject({
      configured: { daemon_gateway: false, enabled_harnesses: [], model_providers: [] },
    });
  });
});
