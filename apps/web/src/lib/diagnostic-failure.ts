import type { DiagnosticFailure } from "@oac/agents-client";
import type { TFunction } from "i18next";

/** Translate the catalog, never native error text or guessed message categories. */
export function diagnosticFailure(failure: DiagnosticFailure, t: TFunction<"diagnostics">): string {
  let message: string = t(`failure.${failure.code}`, { defaultValue: t("failure.internal_error") });
  if (failure.code === "environment_provisioning_failed" && "step" in failure.params && failure.params.step) {
    message = t("provisioningStep", { step: t(`step.${failure.params.step}`) });
    if (failure.params.index !== null) message += ` ${t("setupIndex", { number: failure.params.index + 1 })}`;
    if (failure.params.exit_code !== null) message += ` ${t("exitCode", { code: failure.params.exit_code })}`;
  }
  if (failure.code === "connection_failed" && "http_status" in failure.params && failure.params.http_status !== null) {
    message += ` ${t("httpStatus", { status: failure.params.http_status })}`;
  }
  return message;
}
