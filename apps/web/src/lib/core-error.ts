import { AgentCoreError } from "@oac/agents-client";
import type { TFunction } from "i18next";
import type { coreErrors } from "../i18n/locales/en/core-errors";

const codes = new Set<keyof typeof coreErrors>([
  "sandbox_credential_ownership", "sandbox_credential_invalid", "sandbox_configuration_invalid", "sandbox_verification_unconfirmed", "sandbox_configuration_error",
  "sandbox_generation_stale", "sandbox_reset_required", "sandbox_reset_in_progress", "sandbox_not_configured", "sandbox_in_use", "runtime_node_in_use", "runtime_node_unavailable", "sandbox_admin_not_configured",
  "invalid_admin_key", "console_sign_in_required", "console_origin_rejected", "console_request_invalid", "core_unreachable",
  "invalid_name", "invalid_node_capacity", "invalid_model_provider", "model_configuration_model_invalid", "harness_config_invalid", "model_provider_base_url_invalid",
  "model_provider_protocol_unsupported", "model_provider_api_key_invalid", "model_provider_token_limits_invalid",
  "invalid_sandbox_configuration", "project_archived", "project_exists", "project_api_key_exists",
  "executor_credential_exists", "credential_storage_unavailable", "diagnostics_unavailable", "internal_error",
]);

/** Only catalogued, correctly typed detail keys can enter localized text. */
export function knownCoreError(error: unknown, t: TFunction<"common">, nameUnit: "characters" | "bytes" = "characters"): string | null {
  if (!(error instanceof AgentCoreError) || !codes.has(error.code as keyof typeof coreErrors)) return null;
  const number = (key: string) => {
    const value = error.details?.[key];
    return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
  };
  const maxLength = number("max_length");
  if (error.code === "invalid_name" && maxLength !== undefined) return t(nameUnit === "bytes" ? "coreErrors.nodeNameLimit" : "coreErrors.nameLimit", { max: maxLength });
  if (error.code === "model_provider_api_key_invalid" && maxLength !== undefined) return t("coreErrors.keyLimit", { max: maxLength });
  const min = number("min"), max = number("max");
  if (error.code === "invalid_node_capacity" && min !== undefined && max !== undefined && min <= max) return t("coreErrors.capacityRange", { min, max });
  if (error.code === "invalid_sandbox_configuration") {
    if (error.param === "runtime") return t("coreErrors.runtime");
    if (["resources.cpus", "resources.memory_mib", "resources.root_disk_mib", "resources.environment_disk_mib"].includes(error.param ?? "") && min !== undefined) {
      if (max !== undefined && min <= max) return t("coreErrors.resourceRange", { min, max });
      if (max === undefined) return t("coreErrors.resourceMin", { min });
    }
  }
  if (error.code === "model_provider_protocol_unsupported") {
    const protocols = error.details?.allowed_protocols;
    if (Array.isArray(protocols) && protocols.length > 0 && protocols.every((value) => ["responses", "anthropic"].includes(value))) return t("coreErrors.protocols", { protocols: protocols.join(", ") });
  }
  return t(`coreErrors.${error.code as keyof typeof coreErrors}`);
}

export function coreError(error: unknown, t: TFunction<"common">): string {
  return knownCoreError(error, t) ?? (error instanceof AgentCoreError && error.message ? error.message : t("readFailure.unknown"));
}

/** Exact field paths only; timeouts and unknown write outcomes are form-level errors. */
export function coreFieldError(error: unknown, param: string, t: TFunction<"common">, nameUnit: "characters" | "bytes" = "characters"): string | null {
  if (!(error instanceof AgentCoreError) || error.status < 400 || error.status >= 500 || error.status === 408 || error.param !== param) return null;
  return knownCoreError(error, t, nameUnit) ?? error.message;
}
