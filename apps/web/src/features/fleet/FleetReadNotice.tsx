import { useTranslation } from "react-i18next";

import { ReadFailure } from "../../components/ReadFailure";
import type { FleetState } from "./use-sandbox-fleet";

/** Retained observations stay tied to their original generation and read outcome. */
export function FleetReadNotice({ state, onRetry }: { state: FleetState; onRetry: () => void }) {
  const { t } = useTranslation("overview");
  if (state.status !== "ready") return null;
  return <>
    {state.error !== null ? <ReadFailure partial onRetry={onRetry} /> : null}
    {state.targetGeneration !== state.snapshot.deployment.generation
      ? <p className="detail-note" role="status">{t("fleet.previousGeneration", { observed: state.snapshot.deployment.generation, target: state.targetGeneration })}</p>
      : null}
  </>;
}
