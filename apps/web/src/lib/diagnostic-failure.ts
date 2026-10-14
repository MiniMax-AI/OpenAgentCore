import type { DiagnosticFailure } from "@oac/agents-client";
import type { TFunction } from "i18next";

/** The provider's HTTP status on a connection failure; null when no response arrived. */
function connectionStatus(failure: DiagnosticFailure): number | null {
  return failure.code === "connection_failed" && "http_status" in failure.params ? failure.params.http_status : null;
}

/** Translate the catalog, never native error text or guessed message categories. */
export function diagnosticReason(failure: DiagnosticFailure, t: TFunction<"diagnostics">): string {
  const gap = t("sentenceGap");
  let message: string = t(`failure.${failure.code}`, { defaultValue: t("failure.internal_error") });
  if (failure.code === "environment_provisioning_failed" && "step" in failure.params && failure.params.step) {
    message = t("provisioningStep", { step: t(`step.${failure.params.step}`) });
    if (failure.params.index !== null) message += gap + t("setupIndex", { number: failure.params.index + 1 });
    if (failure.params.exit_code !== null) message += gap + t("exitCode", { code: failure.params.exit_code });
  }
  const status = connectionStatus(failure);
  if (status !== null) message += gap + t("httpStatus", { status });
  return message;
}

/** The catalog's next step for the reason; empty when it has none. */
export function diagnosticNext(failure: DiagnosticFailure, t: TFunction<"diagnostics">): string {
  const next: string = t(`next.${failure.code}`, { defaultValue: "" });
  // Without an HTTP status no provider answered, so its host may not resolve or be reachable.
  return failure.code === "connection_failed" && connectionStatus(failure) === null ? next + t("sentenceGap") + t("unreachableHost") : next;
}

/** The classified reason, then its next step when the catalog has one. */
export function diagnosticFailure(failure: DiagnosticFailure, t: TFunction<"diagnostics">): string {
  const reason = diagnosticReason(failure, t);
  const next = diagnosticNext(failure, t);
  return next ? reason + t("sentenceGap") + next : reason;
}
