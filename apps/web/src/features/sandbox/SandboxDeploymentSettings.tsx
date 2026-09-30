import { useState, type ReactNode } from "react";
import type { UpdateSandboxDeployment, SandboxDeployment, StartSandboxReset } from "@oac/agents-client";
import { useTranslation } from "react-i18next";
import { Modal } from "../../components/Modal";
import { HelpTip, RefreshButton } from "../../components/console-ui";
import { formatBytes, formatPeriod, MISSING } from "../../lib/format";
import type { MessageKey } from "../../lib/locale-strings";
import { sandboxProviderLabel } from "../../lib/sandbox-labels";
import { sandboxSize, templateBuildSize, templateBuildStatus } from "./deployment-specification";
import { SandboxRolloutSummary } from "./SandboxRolloutSummary";
import { SandboxResetControls } from "./SandboxResetControls";
import { SandboxSetupWizard } from "./SandboxSetupWizard";

const MIB = 2 ** 20;
const buildStatusLabel: Record<ReturnType<typeof templateBuildStatus>, MessageKey> = { ready: "Ready", notReady: "Not ready", unknown: "Unknown state" };

export function SandboxDeploymentSettings({ deployment, disabled, fresh, onReset, onCancelReset, onUpdate, writeFailure, onRefresh, refreshing, pending }: {
  deployment: SandboxDeployment;
  disabled: boolean;
  fresh: boolean;
  onReset: (input: StartSandboxReset) => Promise<boolean>;
  onCancelReset: (expectedGeneration: number) => Promise<boolean>;
  onUpdate: (input: UpdateSandboxDeployment) => Promise<boolean>;
  writeFailure?: ReactNode;
  onRefresh: () => void;
  refreshing: boolean;
  pending: boolean;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const { t: tNavigation } = useTranslation("sandboxNavigation");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const [changing, setChanging] = useState(false);
  const [editKey, setEditKey] = useState(0);
  const spec = deployment.specification;
  // An E2B selection may adopt its template build's size instead of saving one.
  const size = sandboxSize(deployment);
  const build = deployment.e2b?.template_build;
  const buildSize = templateBuildSize(deployment);
  const sizeLabel = (value: { cpus: number; memory_mib: number }) => t("{{cpus}} CPU · {{memory}}", { cpus: value.cpus, memory: formatBytes(value.memory_mib * MIB) });
  const canEdit = fresh && !deployment.reset;
  return <section className="sandbox-provider-settings form-stack" aria-labelledby="sandbox-provider-heading">
    <div className="sandbox-provider-title">
      <h2 id="sandbox-provider-heading">{t("Deployment provider")}</h2>
      <HelpTip label={tNavigation("details")}>
        {t("One provider serves this deployment. Reset it before choosing a different backend.")}
        {deployment.provider === "e2b" ? ` ${t("Core creates E2B sandboxes directly. No node enrollment is needed.")} ${t("Saved configuration does not confirm execution readiness. Session and Environment state report actual execution.")}` : ""}
        <dl className="sandbox-summary">
      {spec?.runtime ? <div><dt>{t("Runtime")}</dt><dd><code title={spec.runtime.source_commit}>{spec.runtime.source_commit.slice(0, 12)}</code></dd></div> : null}
      {deployment.suspension ? <div><dt>{t("Idle suspension")}</dt><dd>{t("After {{idle}} · kept {{retention}}", { idle: formatPeriod(deployment.suspension.idle_seconds, i18n.resolvedLanguage), retention: formatPeriod(deployment.suspension.retention_seconds, i18n.resolvedLanguage) })}</dd></div> : null}
      <div><dt>{t("Allocated resources")}</dt><dd>{deployment.resources?.allocations ?? t("Unknown state")}</dd></div>
      <div><dt>{t("Pending environments")}</dt><dd>{deployment.resources?.pending ?? t("Unknown state")}</dd></div>
        </dl>
      </HelpTip>
    </div>
    <dl className="sandbox-summary">
      <div><dt>{t("Provider")}</dt><dd>{sandboxProviderLabel(deployment.provider, locale)}</dd></div>
      <div><dt>{t("Each sandbox")}</dt><dd>{size ? `${sizeLabel(size)}${size.root_disk_mib ? ` · ${t("Root disk {{root}} · data disk {{data}}", { root: formatBytes(size.root_disk_mib * MIB), data: formatBytes((size.environment_disk_mib ?? 0) * MIB) })}` : ""}` : MISSING}</dd></div>
    </dl>
    {deployment.provider === "e2b" ? <div className="sandbox-cloud-summary">
      <dl className="sandbox-summary">
        <div><dt>{t("Sandbox API URL")}</dt><dd><code>{deployment.e2b?.api_url}</code></dd></div>
        <div><dt>{t("Sandbox data-plane domain")}</dt><dd><code>{deployment.e2b?.domain}</code></dd></div>
        <div>
          <dt>{t("Template build")}<HelpTip>{deployment.e2b?.template || t("Unknown state")}</HelpTip></dt>
          <dd>{[
            t(buildStatusLabel[templateBuildStatus(build)]),
            ...(buildSize ? [sizeLabel(buildSize)] : []),
            ...(build?.resources.root_disk_mib != null ? [t("{{disk}} disk", { disk: formatBytes(build.resources.root_disk_mib * MIB) })] : []),
          ].join(" · ")}</dd>
        </div>
        <div><dt>{t("E2B credential")}</dt><dd>{t(deployment.e2b?.credential_configured ? "Configured" : "Not configured")}</dd></div>
      </dl>
    </div> : null}
    <SandboxRolloutSummary deployment={deployment} stale={!fresh} />
    {!deployment.reset ? <>
      <div className="sandbox-actions">
        <button type="button" className="button outline" disabled={disabled || !canEdit || changing} onClick={() => { setEditKey((key) => key + 1); setChanging(true); }}>{t("Change resources")}</button>
        <HelpTip>{t("Changes keep this backend and existing Sessions and nodes. Core prepares the new target for future work; placement may continue on qualified earlier generations.")}</HelpTip>
      </div>

      <Modal open={changing} title={t("Change resources")} wide onClose={() => { if (!pending) setChanging(false); }} footer={<><RefreshButton onClick={onRefresh} refreshing={refreshing} disabled={pending} label={t("Refresh sandbox state")} /><button type="button" className="button outline" disabled={pending} onClick={() => setChanging(false)}>{t("Cancel editing")}</button></>}>
        <SandboxSetupWizard
          key={editKey}
          coreUrl={deployment.core_url}
          expectedGeneration={deployment.generation}
          current={deployment.provider ? { provider: deployment.provider, specification: deployment.specification, e2bTemplate: deployment.e2b?.template, e2bAPIURL: deployment.e2b?.api_url, e2bDomain: deployment.e2b?.domain } : undefined}
          disabled={disabled || !canEdit}
          editing
          onSubmit={async (input) => { if (await onUpdate(input)) setChanging(false); }}
        />
        {writeFailure}
      </Modal>
    </> : null}
    <SandboxResetControls deployment={deployment} disabled={disabled || (changing && !deployment.reset)} stale={!fresh} onStart={onReset} onCancel={onCancelReset} />
  </section>;
}
