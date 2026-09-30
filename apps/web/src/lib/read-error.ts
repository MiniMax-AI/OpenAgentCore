import { AgentCoreError } from "@oac/agents-client";
import type { TFunction } from "i18next";
import { coreError } from "./core-error";

export function readError(error: unknown, t: TFunction<"common">): string {
  if (error instanceof AgentCoreError) return error.code ? coreError(error, t) : t("readFailure.request", { status: error.status });
  return t("readFailure.unknown");
}
