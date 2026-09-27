import { useTranslation } from "react-i18next";
import { ErrorState } from "./ErrorState";

/** Keep a failed or incomplete read visible beside the data it qualifies. */
export function ReadFailure({ onRetry, partial = false }: { onRetry: () => void; partial?: boolean }) {
  const { t } = useTranslation("common");
  return <ErrorState title={t("readFailure.title")} description={partial ? t("readFailure.partial") : undefined} onRetry={onRetry} />;
}
