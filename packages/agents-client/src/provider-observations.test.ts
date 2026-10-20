import { describe, expect, it } from "vitest";
import { projectCoreHarnessList, projectHarnessModelConfiguration } from "./admin-projection";

const provider = { object: "core.model_configuration", harness: "codex", model: "test-model", harness_config: {}, model_provider: { protocol: "responses", base_url: "https://model.example/v1", api_key_configured: true }, updated_at: "2026-09-26T08:00:00Z", last_used_at: null, last_error_code: null, last_error_at: null };
describe("deployment default observations", () => {
  it("preserves null and safe observations", () => {
    expect(projectHarnessModelConfiguration(provider)).toEqual(provider);
    const observed = { ...provider, last_used_at: "2026-09-26T08:01:00Z", last_error_at: "2026-09-26T08:00:30Z", last_error_code: "authentication_error" };
    expect(projectHarnessModelConfiguration(observed)).toEqual(observed);
  });
  it("rejects private state, raw codes and inconsistent pairs", () => {
    for (const extra of [{ revision: "private" }, { recovery_pending: true }, { last_error_code: "raw provider secret", last_error_at: provider.updated_at }, { last_error_code: "context_length_exceeded", last_error_at: provider.updated_at }, { last_error_code: "server_error" }, { last_error_at: provider.updated_at }, { last_used_at: "invalid" }, { last_used_at: undefined }]) {
      expect(() => projectHarnessModelConfiguration({ ...provider, ...extra })).toThrow();
    }
  });
});

describe("adapter model configuration support", () => {
  const support = { protocols: ["responses"], accepts_harness_config: true, accepts_max_output_tokens: false };
  const harness = { object: "core.harness", id: "codex", enabled: false, default: false, model_configuration: null, model_configuration_support: support };
  it("preserves build declarations and native default order without inferring readiness", () => {
    const claude = { ...harness, id: "claude_sdk", model_configuration_support: { ...support, protocols: ["anthropic"] } };
    const minimax = { ...harness, id: "mcode", model_configuration_support: { protocols: ["anthropic", "responses", "chat_completions"], accepts_harness_config: false, accepts_max_output_tokens: true } };
    const value = { object: "list", data: [harness, claude, minimax] };
    expect(projectCoreHarnessList(value)).toEqual(value);
    const projected = projectCoreHarnessList(value).data[2]!.model_configuration_support.protocols;
    expect(projected).not.toBe(minimax.model_configuration_support.protocols);
    expect(projected[0]).toBe("anthropic");
  });
  it("rejects missing, malformed or obsolete declarations", () => {
    for (const value of [undefined, null, {}, { ...support, protocols: undefined }, { ...support, protocols: null },
      { ...support, protocols: "responses" }, { ...support, protocols: [] }, { ...support, protocols: ["unknown"] },
      { ...support, protocols: [null] }, { ...support, protocols: ["responses", "responses"] },
      { ...support, native_protocols: ["responses"] }, { ...support, accepts_harness_config: "true" },
      { ...support, accepts_harness_config: undefined }, { ...support, ready: true }]) {
      expect(() => projectCoreHarnessList({ object: "list", data: [{ ...harness, model_configuration_support: value }] })).toThrow();
    }
  });
});
