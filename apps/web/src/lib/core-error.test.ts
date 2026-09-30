import { AgentCoreError } from "@oac/agents-client";
import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import { coreErrors as english } from "../i18n/locales/en/core-errors";
import { coreErrors as chinese } from "../i18n/locales/zh-CN/core-errors";
import { coreError, coreFieldError, knownCoreError } from "./core-error";
import { readError } from "./read-error";

const en = i18n.getFixedT("en", "common");
const zh = i18n.getFixedT("zh-CN", "common");
const failure = (code: string, param?: string, details?: AgentCoreError["details"], status = 400) => new AgentCoreError("unparsed backend prose", status, code, param, undefined, details);

describe("Core error catalog localization", () => {
  it("covers both languages, without relying on backend prose", () => {
    expect(Object.keys(chinese).sort()).toEqual(Object.keys(english).sort());
    for (const code of ["invalid_admin_key", "console_sign_in_required", "console_origin_rejected", "console_request_invalid", "core_unreachable", "invalid_name", "invalid_node_capacity", "invalid_model_provider", "model_provider_base_url_invalid", "model_provider_protocol_unsupported", "model_provider_api_key_invalid", "model_provider_token_limits_invalid", "invalid_sandbox_configuration", "e2b_api_key_invalid", "e2b_template_build_invalid", "e2b_team_mismatch", "e2b_request_unconfirmed", "sandbox_generation_stale", "sandbox_admin_not_configured", "project_archived", "project_exists", "project_api_key_exists"]) {
      expect(knownCoreError(failure(code), en)).not.toBeNull();
      expect(coreError(failure(code), en)).not.toContain("backend prose");
      expect(coreError(failure(code), zh)).toMatch(/[\u4e00-\u9fff]/);
    }
    expect(coreError(failure("console_sign_in_required"), en)).not.toEqual(coreError(failure("invalid_admin_key"), en));
  });

  it("uses only typed bounds and exact resource field paths", () => {
    expect(coreError(failure("invalid_name", "name", { max_length: 80, ignored: "SECRET" }), en)).toContain("80 characters");
    expect(coreFieldError(failure("invalid_name", "name", { max_length: 128 }), "name", en, "bytes")).toContain("128 UTF-8 bytes");
    expect(coreError(failure("invalid_node_capacity", "max_retained", { min: 1, max: 1000000 }), zh)).toContain("1000000");
    expect(coreError(failure("invalid_sandbox_configuration", "resources.cpus", { min: 1, max: 255 }), en)).toContain("1 to 255");
    expect(coreError(failure("invalid_sandbox_configuration", "resources.root_disk_mib", { min: 1024 }), en)).toContain("at least 1024");
    for (const max_length of ["SECRET", NaN, Infinity, -1, 1.5, null, ["SECRET"]]) {
      expect(coreError(failure("invalid_name", "name", { max_length }), en)).toBe(english.invalid_name);
    }
    expect(coreError(failure("invalid_sandbox_configuration", "unknown", { min: 99, max: 100 }), en)).toBe(english.invalid_sandbox_configuration);
    expect(coreError(failure("invalid_sandbox_configuration", "resources.cpus", { min: 255, max: 1 }), en)).toBe(english.invalid_sandbox_configuration);
  });

  it("allows catalog protocol values and never reflects arbitrary detail text", () => {
    expect(coreError(failure("model_provider_protocol_unsupported", "protocol", { allowed_protocols: ["responses"] }), en)).toBe("Allowed protocols: responses.");
    expect(coreError(failure("model_provider_protocol_unsupported", "protocol", { allowed_protocols: ["SECRET"], harness: "SECRET" }), en)).toBe(english.model_provider_protocol_unsupported);
  });

  it("attaches exact rejected fields without treating unknown outcomes as field validation", () => {
    const error = failure("model_provider_base_url_invalid", "base_url");
    expect(coreFieldError(error, "base_url", zh)).toBe(chinese.model_provider_base_url_invalid);
    expect(coreFieldError(error, "api_key", zh)).toBeNull();
    for (const status of [0, 408, 502]) expect(coreFieldError(failure("invalid_name", "name", undefined, status), "name", en)).toBeNull();
    expect(coreFieldError(failure("new_code", "name"), "name", en)).toBe("unparsed backend prose");
    expect(coreFieldError(new Error("invalid_name name"), "name", en)).toBeNull();
  });

  it("retains unknown-code messages without parsing them", () => {
    const error = failure("future_error", undefined, { max_length: 999 });
    expect(coreError(error, zh)).toBe(error.message);
    expect(readError(error, zh)).toBe(error.message);
    expect(coreError(new Error("private transport data"), zh)).not.toContain("private");
  });
});
