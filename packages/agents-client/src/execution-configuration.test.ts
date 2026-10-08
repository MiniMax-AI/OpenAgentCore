import { describe, expect, it } from "vitest";
import { AdminClient } from "./admin-client";
import type { SessionExecutionConfiguration } from "./types";

const projectId = "11111111-1111-4111-8111-111111111111";
const id = "013773a9-44b9-4f84-baca-b51c04a01201";
const snapshot: SessionExecutionConfiguration = {
  object: "agent.session.execution_configuration", schema_version: 1, session_id: id,
  model: { value: "frozen-model", source: "session" }, harness: { value: "codex", source: "agent" },
  harness_config: { value: { model_reasoning_effort: "high" }, source: "agent" },
  model_provider: { status: "available", source: "agent", configuration: { protocol: "responses", base_url: "https://model.example/v1", api_key_configured: true } },
};
function clientReturning(value: unknown, seen?: (url: string, init?: RequestInit) => void) {
  return new AdminClient({ adminToken: "admin-token", fetch: (async (url: RequestInfo | URL, init?: RequestInit) => {
    seen?.(String(url), init);
    return new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } });
  }) as typeof fetch });
}

describe("frozen execution configuration", () => {
  it("reads a Session-scoped snapshot with auth and abort signal", async () => {
    const abort = new AbortController();
    const client = clientReturning(snapshot, (url, init) => {
      expect(url).toBe(`/core/v1/projects/${projectId}/sessions/${id}/execution-configuration`);
      expect(init?.signal).toBe(abort.signal);
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer admin-token");
    });
    expect(await client.retrieveSessionExecutionConfiguration(projectId, id, { signal: abort.signal })).toEqual(snapshot);
  });
  it.each(["anthropic", "responses", "chat_completions"] as const)("preserves the %s upstream protocol for every harness", async (protocol) => {
    for (const harness of ["codex", "claude_sdk", "mcode"]) {
      const value = structuredClone(snapshot);
      value.harness.value = harness;
      value.model_provider.configuration!.protocol = protocol;
      value.model_provider.configuration!.context_window = 200000;
      value.model_provider.configuration!.max_output_tokens = 8000;
      expect(await clientReturning(value).retrieveSessionExecutionConfiguration(projectId, id)).toEqual(value);
    }
  });
  it.each(["http://model.example/v1", "https://model.example/v1", "http://192.168.20.15:3721"])(
    "accepts the operator-chosen provider scheme %s", async (baseURL) => {
      const value = structuredClone(snapshot);
      value.model_provider.configuration!.base_url = baseURL;
      expect(await clientReturning(value).retrieveSessionExecutionConfiguration(projectId, id)).toEqual(value);
    });
  it.each([
    { status: "redacted", source: "deployment", configuration: null },
    { status: "available", source: "deployment", configuration: { protocol: "responses", base_url: "https://deployment.example/v1", api_key_configured: true } },
    { status: "unavailable", source: "unknown", configuration: null },
  ])("preserves explicit $status $source provider state", async (provider) => {
    const value = { ...snapshot, model_provider: provider };
    expect(await clientReturning(value).retrieveSessionExecutionConfiguration(projectId, id)).toEqual(value);
  });
  it.each([
    (value: any) => { value.model_provider.configuration.api_key = "secret-canary"; },
    (value: any) => { value.model_provider.configuration.headers = { Authorization: "secret-canary" }; },
    (value: any) => { value.model_provider.source = "deployment"; value.model_provider.configuration = null; },
    (value: any) => { value.model_provider.configuration.base_url = "https://user:secret-canary@host/v1"; },
    (value: any) => { value.model_provider.configuration.base_url += "?key=secret-canary"; },
    (value: any) => { value.session_id = "wrong-session"; },
    (value: any) => { value.model.source = "guessed"; },
    (value: any) => { value.harness_config.value = []; },
    (value: any) => { value.harness_config.source = "unknown"; },
    (value: any) => { value.harness_config.source = "guessed"; },
    (value: any) => { value.model_provider.configuration.protocol = "chat-completions"; },
  ])("rejects unsafe or inconsistent responses without echoing them", async (mutate) => {
    const value = structuredClone(snapshot); mutate(value);
    await expect(clientReturning(value).retrieveSessionExecutionConfiguration(projectId, id)).rejects.toMatchObject({ code: "invalid_admin_response" });
    await expect(clientReturning(value).retrieveSessionExecutionConfiguration(projectId, id)).rejects.not.toThrow(/secret-canary/u);
  });
});
