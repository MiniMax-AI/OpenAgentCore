import type { CoreInstallation } from "@agents-core-web/agents-client";
import { Trans, useTranslation } from "react-i18next";

import { CopyableId } from "./list-ui";
import "./installation-notice.css";

/** Core owns the address and repair instructions; missing configuration stays missing. */
export function InstallationNotice({ installation }: { installation: CoreInstallation | undefined }) {
  const { t } = useTranslation("common");
  if (!installation?.local_only) return null;
  const configuration = installation.configuration;
  return <aside className="installation-notice" role="status" aria-label={t("installationNotice.title")}>
    <p>{t("installationNotice.body")}</p>
    <p>{configuration ? <Trans t={t} i18nKey="installationNotice.repair" components={{
      path: <CopyableId id={configuration.path} label={t("installationNotice.copyPath")} />,
      command: <CopyableId id={configuration.apply_command} label={t("installationNotice.copyCommand")} />,
    }} /> : t("installationNotice.noConfiguration")}</p>
  </aside>;
}
