import { useState } from "react";
import type { InitializeSandboxDeployment, SandboxDeployment } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";
import { HelpTip } from "../../components/console-ui";
import { sandboxProviderLabel } from "../../lib/sandbox-labels";
import { SandboxSetup } from "./SandboxSetup";

export function SandboxDeploymentSettings({ deployment, disabled, fresh, error, onMaintenance, onUpdate, onRefresh }: {
  deployment: SandboxDeployment;
  disabled: boolean;
  fresh: boolean;
  error: string | null;
  onMaintenance: (maintenance: boolean) => Promise<void>;
  onUpdate: (input: InitializeSandboxDeployment) => Promise<void>;
  onRefresh: () => void;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const [changing, setChanging] = useState(false);
  const clean = fresh && deployment.resources?.allocations === 0 && deployment.resources?.pending === 0;
  return <section className="sandbox-provider-settings form-stack" aria-labelledby="sandbox-provider-heading">
    <div className="sandbox-provider-title">
      <h2 id="sandbox-provider-heading">{t("Deployment provider")}</h2>
      <HelpTip>
        {t("One provider serves this entire Core deployment. Switching requires maintenance and verified cleanup of all existing execution resources.")}
        {deployment.provider === "e2b" ? ` ${t("Core creates E2B sandboxes directly. No node enrollment is needed.")} ${t("Saved configuration does not confirm execution readiness. Session and Environment state report actual execution.")}` : ""}
      </HelpTip>
    </div>
    <dl className="sandbox-summary">
      <div><dt>{t("Provider")}</dt><dd>{sandboxProviderLabel(deployment.provider, locale)}</dd></div>
      {deployment.specification ? <><div><dt>{t("CPU cores per sandbox")}</dt><dd>{deployment.specification.resources.cpus}</dd></div><div><dt>{t("Memory per sandbox (MiB)")}</dt><dd>{deployment.specification.resources.memory_mib}</dd></div></> : null}
      <div><dt>{t("Allocated resources")}</dt><dd>{deployment.resources?.allocations ?? t("Unknown state")}</dd></div>
      <div><dt>{t("Pending environments")}</dt><dd>{deployment.resources?.pending ?? t("Unknown state")}</dd></div>
    </dl>
    {deployment.provider === "e2b" ? <div className="sandbox-cloud-summary">
      <dl className="sandbox-summary"><div><dt>{t("Immutable Runtime template")}</dt><dd>{deployment.e2b?.template || t("Unknown state")}</dd></div><div><dt>{t("E2B credential")}</dt><dd>{t(deployment.e2b?.credential_configured ? "Configured" : "Not configured")}</dd></div></dl>
    </div> : null}
    {!fresh ? <p role="status">{t("Previously loaded state is shown below.")}</p> : null}
    {error ? <p role="alert" className="sandbox-error">{error}</p> : null}
    {!deployment.maintenance ? <button type="button" className="button outline" disabled={disabled} onClick={() => void onMaintenance(true)}>{t("Enter maintenance to change provider")}</button> : <>
      <div className="sandbox-actions"><a className="button outline" href="#sessions">{t("Open Sessions")}</a><button type="button" className="button outline" disabled={disabled} onClick={onRefresh}>{t("Check cleanup")}</button></div>
      <p role="status" className="sandbox-provider-status">
        {t(clean ? "Core reports no remaining execution resources. You can choose the next provider." : "Provider changes are blocked until Core confirms complete cleanup.")}
        <HelpTip>{t("Maintenance pauses new hosted placement. Clean up existing execution resources before changing provider; historical Sessions and results are preserved by the switch.")} {t("Stopped or offline resources, snapshots, uncertain creates and pending environments still block switching. Deleting a Session alone does not prove cleanup; Core must confirm both counts are zero.")}</HelpTip>
      </p>
      {!changing ? <button type="button" className="button outline" disabled={disabled || !clean} onClick={() => setChanging(true)}>{t("Change provider or resources")}</button> : <>
        <SandboxSetup initialCoreUrl={deployment.core_url} disabled={disabled || !clean} switching savedProvider={deployment.provider} initialSpecification={deployment.specification} onInitialize={onUpdate} />
        <span className="sandbox-provider-actions">
          <button type="button" className="button outline" disabled={disabled} onClick={() => setChanging(false)}>{t("Cancel")}</button>
          <HelpTip>{t("Saving retires old node identities and enrollment credentials. It does not migrate Sessions or resume placement automatically.")}</HelpTip>
        </span>
      </>}
      <span className="sandbox-provider-actions">
        <button type="button" className="button primary" disabled={disabled || changing} onClick={() => void onMaintenance(false)}>{t("Resume hosted placement")}</button>
        <HelpTip>{t("Resume is explicit and succeeds only when Core activates the saved configuration. A failed activation leaves maintenance enabled.")}</HelpTip>
      </span>
    </>}
  </section>;
}
