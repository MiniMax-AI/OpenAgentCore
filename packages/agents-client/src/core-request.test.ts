import { describe, expect, it, vi } from "vitest";
import { AgentCoreError, OpenAIAgentsClient } from "./client";
import { CoreRequester } from "./core-request";

function requester(details: unknown) {
  const fetch = vi.fn(async () => ({
    ok: false, status: 409,
    json: async () => ({ error: { message: "Refresh the configuration.", type: "conflict_error", code: "sandbox_generation_stale", param: "expected_generation", details } }),
  }) as Response);
  return { fetch, request: new CoreRequester("/core/v1", undefined, fetch, () => { throw new Error("invalid success body"); }) };
}

describe("Core error details", () => {
  it("projects documented flat values and retains one-request semantics", async () => {
    const details = { current_generation: 4, supported: ["docker", "microsandbox"], enabled: true, missing: null, label: "ready", empty: [] };
    const { fetch, request } = requester(details);
    const error = await request.response("/sandbox/deployment", undefined, "PUT", { expected_generation: 3 }).catch((value: unknown) => value);
    expect(error).toBeInstanceOf(AgentCoreError);
    expect(error).toMatchObject({ status: 409, code: "sandbox_generation_stale", param: "expected_generation", errorType: "conflict_error", details });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0]).toEqual(["/core/v1/sandbox/deployment", expect.objectContaining({ method: "PUT", credentials: "same-origin", redirect: "error" })]);
    details.supported[0] = "mutated";
    expect((error as AgentCoreError).details?.supported).toEqual(["docker", "microsandbox"]);
  });

  it.each([
    undefined, null, {}, [], "private native text", 5, true, new Date(),
    { nested: { secret: "private" } }, { values: ["safe", 1] }, { values: [null] },
    { value: Infinity }, { value: NaN }, { value: undefined }, { "": "invalid key" },
    { good: 3, bad: { nested: true } }, { values: Array(2) },
  ])("ignores malformed or empty optional details (%j)", async (details) => {
    const { fetch, request } = requester(details);
    const error = await request.response("/projects").catch((value: unknown) => value);
    expect(error).toBeInstanceOf(AgentCoreError);
    expect(error).toMatchObject({ status: 409, code: "sandbox_generation_stale", message: "Refresh the configuration." });
    expect((error as AgentCoreError).details).toBeUndefined();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("preserves literal keys without changing object prototypes", async () => {
    const details = JSON.parse('{"__proto__":"literal","constructor":null}');
    const error = await requester(details).request.response("/projects").catch((value: unknown) => value) as AgentCoreError;
    expect(error.details).toEqual(details);
    expect(Object.getPrototypeOf(error.details)).toBe(Object.prototype);
    expect(Object.prototype.hasOwnProperty.call(error.details, "__proto__")).toBe(true);
  });

  it("does not add Core details to public-client errors", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ error: { message: "Rejected.", type: "invalid_request_error", code: "invalid_request", param: null, details: { max_length: 128 } } }), { status: 400 }));
    const error = await new OpenAIAgentsClient({ fetch }).listAgents().catch((value: unknown) => value) as AgentCoreError;
    expect(error).toBeInstanceOf(AgentCoreError);
    expect(error.details).toBeUndefined();
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
