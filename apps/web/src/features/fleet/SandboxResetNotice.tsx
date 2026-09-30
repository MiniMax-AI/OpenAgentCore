import type { SandboxDeployment } from "@oac/agents-client";
import { useTranslation } from "react-i18next";

import { Section, StatusDot } from "../../components/console-ui";
import { ReadFailure } from "../../components/ReadFailure";
import { useConsoleNavigation } from "../../lib/console-navigation";

/** A reset is a deployment operation, separate from Core health and node reachability. */
export function SandboxResetNotice({ deployment, failed, onRetry }: {
  deployment: SandboxDeployment | undefined;
  failed: boolean;
  onRetry: () => void;
}) {
  const { t } = useTranslation("overview");
  const { navigate } = useConsoleNavigation();
  return <>
    {failed ? <ReadFailure partial={deployment !== undefined} onRetry={onRetry} /> : null}
    {deployment?.reset ? <Section headingId="sandbox-reset-notice-heading" title={t("reset.title")} actions={<button className="button outline" type="button" onClick={() => navigate("system", { id: "sandbox" })}>{t("reset.view")}</button>}>
      <div role="status"><StatusDot tone="warning" label={t(`reset.${deployment.reset.clear}`)} /></div>
      <p>{t("reset.body")}</p>
    </Section> : null}
  </>;
}
