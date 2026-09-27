import type { SandboxResources } from "@agents-core-web/agents-client";
import { useQuery } from "@tanstack/react-query";
import { Server } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { EmptyState, PageBody, PageHeader, RefreshButton, Section, StatusDot } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { CopyableId } from "../../components/list-ui";
import { TableSkeleton } from "../../components/Skeleton";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatBytes, formatPeriod } from "../../lib/format";
import { InstallationNotice } from "../../components/InstallationNotice";
import { installationQuery } from "../../lib/installation";
import { sandboxSize, templateBuildSize, templateBuildStatus } from "../sandbox/deployment-specification";
import { sandboxDeploymentQuery } from "../sandbox/sandbox-queries";
import { DefaultModelsSection } from "./DefaultModelsSection";
import { Fact } from "./Fact";
import { harnessesQuery } from "./harness-queries";
import { StartupSettings } from "./StartupSettings";
import "./system.css";

const MIB = 2 ** 20;
const providerNames: Record<string, string> = { docker: "Docker", microsandbox: "microsandbox" };

/** A facts card before its first read; the page announces its loading once. */
function FactsSkeleton({ facts }: { facts: number }) {
  return (
    <dl className="system-facts" aria-hidden="true">
      {Array.from({ length: facts }, (_, index) => (
        <div key={index} className="system-fact">
          <dt><span className="skeleton-bar" style={{ width: `${40 + (index * 13) % 24}%` }} /></dt>
          <dd><span className="skeleton-bar" style={{ width: `${30 + (index * 17) % 30}%` }} /></dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * Platform › System: this installation's addresses; each harness's default
 * model for Core-hosted Sessions and Sessions without an environment, set
 * here; the sandbox configuration every project shares — where sandboxes run,
 * how big each one is, the Runtime or E2B template build they run,
 * microsandbox's idle suspension and maintenance — and Core's startup
 * settings. Each says where it is changed: sandboxes on the Nodes page,
 * startup settings in config.json.
 */
export function SystemPage() {
  const { t, i18n } = useTranslation("system");
  const locale = i18n.resolvedLanguage;
  const { t: tNav } = useTranslation("navigation");
  const { navigate } = useConsoleNavigation();
  const deployment = useQuery(sandboxDeploymentQuery);
  const installation = useQuery(installationQuery);
  const harnesses = useQuery(harnessesQuery);
  const data = deployment.data ?? null;
  const refresh = () => { void deployment.refetch(); void installation.refetch(); void harnesses.refetch(); };
  const spec = data?.specification ?? null;
  // An E2B selection may adopt its template build's size instead of saving one.
  const each = data ? sandboxSize(data) : null;
  const build = data?.e2b?.template_build;
  const buildSize = data ? templateBuildSize(data) : null;
  const buildStatus = templateBuildStatus(build);
  const size = (resources: SandboxResources) => t("sandbox.size", { count: resources.cpus, memory: formatBytes(resources.memory_mib * MIB) });

  let body: ReactNode;
  if (!data) {
    body = deployment.isError && !deployment.isFetching
      ? <ErrorState title={t("sandbox.deploymentFailed")} onRetry={refresh} />
      : <FactsSkeleton facts={5} />;
  } else if (!data.provider) {
    body = (
      <EmptyState
        icon={Server}
        title={t("sandbox.unconfigured")}
        action={<button className="button outline" type="button" onClick={() => navigate("nodes")}>{t("sandbox.setUp")}</button>}
      />
    );
  } else {
    body = (
      <Section headingId="system-sandbox-heading" title={t("sandbox.title")} actions={<button className="text-action" type="button" onClick={() => navigate("nodes")}>{t("sandbox.change")}</button>}>
        <dl className="system-facts">
          <Fact label={t("sandbox.runsOn")}>{data.provider === "e2b" ? t("sandbox.e2b") : t("sandbox.ownMachines", { provider: providerNames[data.provider] ?? data.provider })}</Fact>
          {each ? (
            <Fact label={t("sandbox.each")}>
              {size(each)}
              {each.root_disk_mib ? <span className="system-sub">{t("sandbox.disks", { root: formatBytes(each.root_disk_mib * MIB), data: formatBytes((each.environment_disk_mib ?? 0) * MIB) })}</span> : null}
            </Fact>
          ) : null}
          {spec?.runtime ? <Fact label={t("sandbox.runtime")} help={t("sandbox.runtimeHelp")}><code className="system-code" title={spec.runtime.source_commit}>{spec.runtime.source_commit.slice(0, 12)}</code></Fact> : null}
          {data.e2b?.template ? <Fact label={t("sandbox.e2bTemplate")}><code className="system-code">{data.e2b.template}</code></Fact> : null}
          {data.e2b ? (
            <Fact label={t("sandbox.templateBuild")} help={t("sandbox.templateBuildHelp")}>
              {buildStatus === "notReady" ? <StatusDot tone="warning" label={t("sandbox.buildStatus.notReady")} /> : t(`sandbox.buildStatus.${buildStatus}`)}
              {buildSize ? (
                <span className="system-sub">
                  {size(buildSize)}
                  {build?.resources.root_disk_mib != null ? ` · ${t("sandbox.buildDisk", { disk: formatBytes(build.resources.root_disk_mib * MIB) })}` : null}
                </span>
              ) : null}
            </Fact>
          ) : null}
          {data.suspension ? <>
            <Fact label={t("sandbox.suspendAfter")} help={t("sandbox.suspendAfterHelp")}>{formatPeriod(data.suspension.idle_seconds, locale)}</Fact>
            <Fact label={t("sandbox.keepSuspended")} help={t("sandbox.keepSuspendedHelp")}>{formatPeriod(data.suspension.retention_seconds, locale)}</Fact>
          </> : null}
          <Fact label={t("sandbox.maintenance")} help={t("sandbox.maintenanceHelp")}>
            {data.maintenance ? <StatusDot tone="warning" label={t("values.on")} /> : t("values.off")}
          </Fact>
        </dl>
      </Section>
    );
  }

  const about = installation.data;
  const facts = about ? (
    <Section headingId="system-installation-heading" title={t("installation.title")}>
      <dl className="system-facts">
        <Fact label={t("installation.publicUrl")} help={t("installation.publicUrlHelp")}>{about.public_url ? <code className="system-code">{about.public_url}</code> : <span className="system-muted">{t("installation.notSet")}</span>}</Fact>
        <Fact label={t("installation.apiBaseUrl")} help={t("installation.apiBaseUrlHelp")}>
          {about.api_base_url ? <CopyableId id={about.api_base_url} label={t("installation.copyApiBaseUrl")} /> : <span className="system-muted">{t("installation.notSet")}</span>}
          {about.local_only ? <span className="system-sub">{t("installation.localOnly")}</span> : null}
        </Fact>
        <Fact label={t("installation.id")}>{about.installation_id ? <CopyableId id={about.installation_id} /> : <span className="system-muted">{t("installation.unknown")}</span>}</Fact>
        <Fact label={t("installation.sourceCommit")}>{about.source_commit ? <code className="system-code" title={about.source_commit}>{about.source_commit.slice(0, 12)}</code> : <span className="system-muted">{t("installation.unknown")}</span>}</Fact>
      </dl>
    </Section>
  ) : installation.isError && !installation.isFetching
    ? <ErrorState title={t("installation.failed")} onRetry={() => void installation.refetch()} />
    : <FactsSkeleton facts={4} />;
  // One announcement while either first read is still out; each slot shows only its own placeholder.
  const reading = (!data && !(deployment.isError && !deployment.isFetching)) || (!about && !(installation.isError && !installation.isFetching)) ||
    (!harnesses.data && !(harnesses.isError && !harnesses.isFetching));

  return (
    <section className="page-section console-page system-page" aria-labelledby="system-heading">
      <PageHeader
        headingId="system-heading"
        title={tNav("views.system")}
        help={t("help")}
        actions={<RefreshButton onClick={refresh} refreshing={deployment.isFetching || installation.isFetching || harnesses.isFetching} label={t("refresh")} />}
      />
      <PageBody>
        <InstallationNotice installation={about} />
        {reading ? <p className="visually-hidden" role="status">{t("loading")}</p> : null}
        {facts}
        <DefaultModelsSection />
        {body}
        {about ? <StartupSettings configuration={about.configuration} /> : installation.isError ? null : <TableSkeleton rows={4} columns={3} />}
      </PageBody>
    </section>
  );
}
