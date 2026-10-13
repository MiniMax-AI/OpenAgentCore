import { AgentCoreError, deploymentContract, modelProviderProtocols } from "@oac/agents-client";
import type { TFunction } from "i18next";
import { coreErrors } from "../i18n/locales/en/core-errors";

const protocols: ReadonlySet<unknown> = new Set(modelProviderProtocols);
const resourceParams: ReadonlySet<unknown> = new Set(deploymentContract.resources.map(({ name }) => `resources.${name}`));

/** Only catalogued, correctly typed detail keys can enter localized text. */
export function knownCoreError(error: unknown, t: TFunction<"common">, nameUnit: "characters" | "bytes" = "characters"): string | null {
  if (!(error instanceof AgentCoreError) || !error.code || !Object.hasOwn(coreErrors, error.code)) return null;
  const number = (key: string) => {
    const value = error.details?.[key];
    return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
  };
  const maxLength = number("max_length");
  if (error.code === "invalid_name" && maxLength !== undefined) return t(nameUnit === "bytes" ? "coreErrorDetails.nodeNameLimit" : "coreErrorDetails.nameLimit", { max: maxLength });
  if (error.code === "model_provider_api_key_invalid" && maxLength !== undefined) return t("coreErrorDetails.keyLimit", { max: maxLength });
  const min = number("min"), max = number("max");
  if (error.code === "invalid_node_capacity" && min !== undefined && max !== undefined && min <= max) return t("coreErrorDetails.capacityRange", { min, max });
  if (error.code === "invalid_sandbox_configuration") {
    if (error.param === "runtime") return t("coreErrorDetails.runtime");
    if (resourceParams.has(error.param) && min !== undefined) {
      if (max !== undefined && min <= max) return t("coreErrorDetails.resourceRange", { min, max });
      if (max === undefined) return t("coreErrorDetails.resourceMin", { min });
    }
  }
  if (error.code === "model_provider_protocol_unsupported") {
    const allowed = error.details?.allowed_protocols;
    if (Array.isArray(allowed) && allowed.length > 0 && allowed.every((value) => protocols.has(value))) return t("coreErrorDetails.protocols", { protocols: allowed.join(", ") });
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
