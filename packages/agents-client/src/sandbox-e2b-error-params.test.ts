import { describe, expect, it, vi } from "vitest";
import { AgentCoreError } from "./client";
import { SandboxAdminClient } from "./sandbox-client";

const cases = [
  { status: 400, code: "sandbox_credential_invalid", param: "credential", message: "The E2B API key was rejected." },
  { status: 400, code: "sandbox_configuration_invalid", param: "configuration", message: "Select a ready immutable E2B template build with matching resources." },
  { status: 409, code: "sandbox_credential_ownership", param: "credential", message: "This E2B key cannot manage the retained deployment. Reset before changing teams." },
] as const;
const currentKey = "current-secret-canary";
const storedKey = "stored-secret-canary";
const reflected = `${currentKey} ${storedKey}`;

type Rejection = { status: number; code: string; param?: unknown; details?: unknown };
async function reject(input: Rejection, method: "POST" | "PUT" = "PUT", includeKey = true) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify({ error: {
    ...input, message: reflected, type: reflected,
  } }), { status: input.status, headers: { "Content-Type": "application/json" } }));
  const client = new SandboxAdminClient({ fetch });
  const e2b = { template: "runtime:00000000-0000-0000-0000-000000000001", ...(includeKey ? { api_key: currentKey } : {}) };
  const result = await (method === "POST"
    ? client.initializeDeployment({ provider: "e2b", expected_generation: 0, configuration: { template: e2b.template }, credential: { api_key: currentKey } })
    : client.updateDeployment({ provider: "e2b", expected_generation: 1, configuration: { template: e2b.template }, ...(e2b.api_key ? { credential: { api_key: e2b.api_key } } : {}) })).catch((error: unknown) => error);
  expect(result).toBeInstanceOf(AgentCoreError);
  const error = result as AgentCoreError;
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(error.errorType).toBeUndefined();
  for (const secret of [currentKey, storedKey]) {
    expect(error.message).not.toContain(secret);
    expect(JSON.stringify(error)).not.toContain(secret);
  }
  return error;
}

describe("safe E2B deployment error parameters", () => {
  it.each(cases)("preserves only the exact $code field on both write methods", async (input) => {
    for (const method of ["POST", "PUT"] as const) {
      const error = await reject({ ...input, details: { secret: reflected, allocations: 2, pending: 1, current_generation: 4 } }, method);
      expect(error).toMatchObject(input);
      expect(error.details).toBeUndefined();
    }
  });
  it.each(cases)("preserves $code when PUT omits its stored key", async (input) => {
    expect(await reject(input, "PUT", false)).toMatchObject(input);
  });
  it.each(cases)("drops mismatched or reflected parameters for $code", async (input) => {
    for (const param of [undefined, null, "", "e2b", "e2b.api_key.extra", input.param === "credential" ? "configuration" : "credential", currentKey, storedKey, [input.param], { field: input.param }]) {
      for (const includeKey of [true, false]) {
        expect(await reject({ ...input, param }, "PUT", includeKey)).toMatchObject({ status: input.status, code: input.code, message: input.message, param: null });
      }
    }
  });
  it.each(cases)("drops an otherwise valid $code field under the wrong status", async (input) => {
    for (const status of [400, 409, 503].filter(value => value !== input.status)) {
      expect(await reject({ ...input, status })).toMatchObject({ status, code: input.code, message: input.message, param: null });
    }
  });
  it.each(["credential", "configuration", reflected])("keeps unconfirmed requests unscoped despite parameter %s", async (param) => {
    expect(await reject({ status: 503, code: "sandbox_verification_unconfirmed", param })).toMatchObject({ code: "sandbox_verification_unconfirmed", param: null });
  });
  it.each(["unknown", currentKey, storedKey, "constructor", "__proto__", "toString"])("replaces unknown credential-bearing code %s without replay", async (code) => {
    for (const includeKey of [true, false]) {
      const error = await reject({ status: 400, code, param: "credential", details: { secret: reflected } }, "PUT", includeKey);
      expect(error).toMatchObject({ code: "sandbox_configuration_unconfirmed" });
      expect(error.param).toBeUndefined();
      expect(error.details).toBeUndefined();
    }
  });
  it.each([
    { code: "sandbox_generation_stale", details: { current_generation: 4, allocations: 2, pending: 1, secret: reflected }, expected: { current_generation: 4 } },
    { code: "sandbox_in_use", details: { current_generation: 4, allocations: 2, pending: 1, secret: reflected }, expected: { allocations: 2, pending: 1 } },
    { code: "sandbox_in_use", details: { allocations: -1, pending: reflected }, expected: undefined },
  ])("retains the numeric detail allowlist for $code", async ({ code, details, expected }) => {
    const error = await reject({ status: 409, code, param: "credential", details });
    expect(error.param).toBeNull();
    expect(error.details).toEqual(expected);
  });
});
