import type { CoreInstallation } from "@oac/agents-client";
import { useTranslation } from "react-i18next";
import { useConsoleNavigation } from "../lib/console-navigation";
import "./installation-notice.css";

/** Core's local-only API address does not describe the browser's Web listener. */
export function InstallationNotice({ installation }: { installation: CoreInstallation | undefined }) {
  const { t } = useTranslation("common");
  const { view, params, navigate } = useConsoleNavigation();
  if (!installation?.local_only) return null;
  return <aside className="installation-notice" role="status" aria-label={t("installationNotice.title")}>
    <p>{t("installationNotice.body")}</p>
    {view !== "system" || params.id ? <button className="text-action" onClick={() => navigate("system")}>{t("installationNotice.configure")}</button> : null}
  </aside>;
}
