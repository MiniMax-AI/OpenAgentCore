import { useState, type ReactNode } from "react";
import type { UpdateSandboxDeployment, SandboxDeployment, SandboxRuntimeRelease } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { Trans, useTranslation } from "react-i18next";
import { Modal } from "../../components/Modal";
import { HelpTip, RefreshButton } from "../../components/console-ui";
import { formatBytes, formatPeriod, MISSING } from "../../lib/format";
import type { ParseKeys } from "i18next";
import { sandboxProviderLabel } from "../../lib/sandbox-labels";
import { Fact } from "../system/Fact";
import { RUNTIME_RELEASE_FIELDS, sandboxSize, templateBuildSize, templateBuildStatus } from "./deployment-specification";
import { distributionRuntimeQuery } from "./sandbox-queries";
import { SandboxRolloutSummary } from "./SandboxRolloutSummary";
import { SandboxSetupWizard } from "./SandboxSetupWizard";

const MIB = 2 ** 20;
const buildStatusLabel: Record<ReturnType<typeof templateBuildStatus>, ParseKeys<"sandbox">> = { ready: "Ready", notReady: "Not ready", unknown: "Unknown state" };

export function SandboxDeploymentSettings({ deployment, disabled, fresh, onUpdate, writeFailure, onRefresh, refreshing, pending }: {
  deployment: SandboxDeployment;
  disabled: boolean;
  fresh: boolean;
  onUpdate: (input: UpdateSandboxDeployment) => Promise<boolean>;
  writeFailure?: ReactNode;
  onRefresh: () => void;
  refreshing: boolean;
  pending: boolean;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const { t: tNavigation } = useTranslation("sandboxNavigation");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const [changing, setChanging] = useState(false);
  const [editKey, setEditKey] = useState(0);
  const [proposedRuntime, setProposedRuntime] = useState<SandboxRuntimeRelease>();
  const spec = deployment.specification;
  // Only Docker and microsandbox save a Runtime. Changes keep it; moving to the
  // one this console serves is an explicit change of its own.
  const savedRuntime = spec?.runtime;
  const served = useQuery({ ...distributionRuntimeQuery, enabled: Boolean(savedRuntime) }).data;
  const offered = savedRuntime && served && !deployment.reset && RUNTIME_RELEASE_FIELDS.some((field) => savedRuntime[field] !== served[field]) ? served : null;
  // An E2B selection may adopt its template build's size instead of saving one.
  const size = sandboxSize(deployment);
  const build = deployment.metadata?.template_build;
  const buildSize = templateBuildSize(deployment);
  const sizeLabel = (value: { cpus: number; memory_mib: number }) => t("{{cpus}} CPU · {{memory}}", { cpus: value.cpus, memory: formatBytes(value.memory_mib * MIB) });
  const canEdit = fresh && !deployment.reset;
  const change = (runtime?: SandboxRuntimeRelease) => { setEditKey((key) => key + 1); setProposedRuntime(runtime); setChanging(true); };
  const short = (release: SandboxRuntimeRelease) => <code className="system-code" title={release.source_commit}>{release.source_commit.slice(0, 12)}</code>;
  return <section className="console-section" aria-labelledby="sandbox-provider-heading">
    <header className="console-section-header">
      <div className="console-section-title">
        <h2 id="sandbox-provider-heading">{t("Deployment provider")}</h2>
        <HelpTip label={tNavigation("details")}>
          {t("One provider serves this deployment. Reset it before choosing a different backend.")}
          {deployment.mode === "direct" ? ` ${t("Core creates E2B sandboxes directly. No node enrollment is needed.")} ${t("Saved configuration does not confirm execution readiness. Session and Environment state report actual execution.")}` : ""}
          <dl className="sandbox-summary">
            {spec?.runtime ? <div><dt>{t("Runtime")}</dt><dd><code title={spec.runtime.source_commit}>{spec.runtime.source_commit.slice(0, 12)}</code></dd></div> : null}
            {deployment.suspension ? <div><dt>{t("Idle suspension")}</dt><dd>{t("After {{idle}} · kept {{retention}}", { idle: formatPeriod(deployment.suspension.idle_seconds, i18n.resolvedLanguage), retention: formatPeriod(deployment.suspension.retention_seconds, i18n.resolvedLanguage) })}</dd></div> : null}
            <div><dt>{t("Allocated resources")}</dt><dd>{deployment.resources?.allocations ?? t("Unknown state")}</dd></div>
            <div><dt>{t("Pending environments")}</dt><dd>{deployment.resources?.pending ?? t("Unknown state")}</dd></div>
          </dl>
        </HelpTip>
      </div>
      {!deployment.reset ? <div className="console-section-actions">
        <HelpTip>{t("Changes keep this backend and existing Sessions and nodes. Core prepares the new target for future work; placement may continue on qualified earlier generations.")}</HelpTip>
        <button type="button" className="button outline" disabled={disabled || !canEdit || changing} onClick={() => change()}>{t("Change resources")}</button>
      </div> : null}
    </header>
    <div className="settings-rows">
      <Fact label={t("Provider")}>{sandboxProviderLabel(deployment.provider, locale)}</Fact>
      <Fact label={t("Each sandbox")} help={spec?.workspace && !spec.workspace.capacity_quota ? t("Workspace storage is configured by the operator; no capacity quota is enforced.") : undefined}>{size ? `${sizeLabel(size)}${size.root_disk_mib ? ` · ${spec?.workspace ? t("Root disk {{root}} · external workspace", { root: formatBytes(size.root_disk_mib * MIB) }) : t("Root disk {{root}} · data disk {{data}}", { root: formatBytes(size.root_disk_mib * MIB), data: formatBytes((size.environment_disk_mib ?? 0) * MIB) })}` : ""}` : MISSING}</Fact>
      {savedRuntime && offered ? <Fact label={t("Runtime")} help={t("Other changes keep the saved Runtime. Existing sandboxes keep their Runtime while nodes prepare the new one.")} action={<button type="button" className="button outline" disabled={disabled || !canEdit || changing} onClick={() => change(offered)}>{t("Use this installation's Runtime")}</button>}>
        <Trans t={t} i18nKey="Saved <saved/> · this installation serves <served/>" components={{ saved: short(savedRuntime), served: short(offered) }} />
      </Fact> : null}
      {deployment.provider === "e2b" ? <>
        <Fact label={t("Sandbox API URL")}><code className="system-code">{deployment.configuration?.api_url}</code></Fact>
        <Fact label={t("Sandbox data-plane domain")}><code className="system-code">{deployment.configuration?.domain}</code></Fact>
        <Fact label={t("Template build")} help={deployment.configuration?.template || t("Unknown state")}>{[
          t(buildStatusLabel[templateBuildStatus(build)]),
          ...(buildSize ? [sizeLabel(buildSize)] : []),
          ...(build?.resources.root_disk_mib != null ? [t("{{disk}} disk", { disk: formatBytes(build.resources.root_disk_mib * MIB) })] : []),
        ].join(" · ")}</Fact>
        <Fact label={t("E2B credential")}>{t(deployment.credential_configured ? "Configured" : "Not configured")}</Fact>
      </> : null}
      <SandboxRolloutSummary deployment={deployment} stale={!fresh} />
    </div>
    {!deployment.reset ? <Modal open={changing} title={t("Change resources")} wide onClose={() => { if (!pending) setChanging(false); }} footer={<><RefreshButton onClick={onRefresh} refreshing={refreshing} disabled={pending} label={t("Refresh sandbox state")} /><button type="button" className="button outline" disabled={pending} onClick={() => setChanging(false)}>{t("Cancel editing")}</button></>}>
      <SandboxSetupWizard
        key={editKey}
        coreUrl={deployment.core_url}
        expectedGeneration={deployment.generation}
        current={deployment.provider ? { provider: deployment.provider, specification: deployment.specification, e2bTemplate: deployment.configuration?.template, e2bAPIURL: deployment.configuration?.api_url, e2bDomain: deployment.configuration?.domain } : undefined}
        disabled={disabled || !canEdit}
        editing
        proposedRuntime={proposedRuntime}
        onSubmit={async (input) => { if (await onUpdate(input)) setChanging(false); }}
      />
      {writeFailure}
    </Modal> : null}
  </section>;
}
