import { describe, expect, it } from "vitest";
import { OpenAIAgentsClient } from "./client";
import type { SessionExecutionConfiguration } from "./types";

const id = "013773a9-44b9-4f84-baca-b51c04a01201";
const snapshot: SessionExecutionConfiguration = {
  object: "agent.session.execution_configuration", schema_version: 1, session_id: id,
  model: { value: "frozen-model", source: "session" }, harness: { value: "codex", source: "agent" },
  model_provider: { status: "available", source: "agent", configuration: { protocol: "responses", base_url: "https://model.example/v1", api_key_configured: true } },
};
function clientReturning(value: unknown, seen?: (url: string, init?: RequestInit) => void) {
  return new OpenAIAgentsClient({ token: "project-token", fetch: (async (url: RequestInfo | URL, init?: RequestInit) => {
    seen?.(String(url), init);
    return new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } });
  }) as typeof fetch });
}

describe("frozen execution configuration", () => {
  it("reads a Session-scoped snapshot with auth and abort signal", async () => {
    const abort = new AbortController();
    const client = clientReturning(snapshot, (url, init) => {
      expect(url).toContain(`/agents/sessions/${id}/execution-configuration`);
      expect(init?.signal).toBe(abort.signal);
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer project-token");
    });
    expect(await client.retrieveSessionExecutionConfiguration(id, { signal: abort.signal })).toEqual(snapshot);
  });
  it.each([
    { status: "redacted", source: "deployment", configuration: null },
    { status: "unavailable", source: "unknown", configuration: null },
  ])("preserves explicit $status provider state", async (provider) => {
    const value = { ...snapshot, model_provider: provider };
    expect(await clientReturning(value).retrieveSessionExecutionConfiguration(id)).toEqual(value);
  });
  it.each([
    (value: any) => { value.model_provider.configuration.api_key = "secret-canary"; },
    (value: any) => { value.model_provider.configuration.headers = { Authorization: "secret-canary" }; },
    (value: any) => { value.model_provider.source = "deployment"; },
    (value: any) => { value.model_provider.configuration.base_url = "https://user:secret-canary@host/v1"; },
    (value: any) => { value.model_provider.configuration.base_url += "?key=secret-canary"; },
    (value: any) => { value.session_id = "wrong-session"; },
    (value: any) => { value.model.source = "guessed"; },
  ])("rejects unsafe or inconsistent responses without echoing them", async (mutate) => {
    const value = structuredClone(snapshot); mutate(value);
    await expect(clientReturning(value).retrieveSessionExecutionConfiguration(id)).rejects.toMatchObject({ code: "invalid_execution_configuration" });
    await expect(clientReturning(value).retrieveSessionExecutionConfiguration(id)).rejects.not.toThrow(/secret-canary/u);
  });
});
