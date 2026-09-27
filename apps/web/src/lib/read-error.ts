import { AgentCoreError } from "@agents-core-web/agents-client";
import type { TFunction } from "i18next";

export function readError(error: unknown, t: TFunction<"common">): string {
  return error instanceof AgentCoreError
    ? t("readFailure.request", { status: error.status })
    : t("readFailure.unknown");
}
