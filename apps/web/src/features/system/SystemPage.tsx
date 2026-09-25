import type { SandboxResources } from "@agents-core-web/agents-client";
import { useQuery } from "@tanstack/react-query";
import { Server } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, Section, StatusDot } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { DetailSkeleton } from "../../components/Skeleton";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatBytes } from "../../lib/format";
import { sandboxDeploymentQuery } from "../sandbox/sandbox-queries";
import "./system.css";

const MIB = 2 ** 20;
const providerNames: Record<string, string> = { docker: "Docker", microsandbox: "microsandbox" };

function Fact({ label, help, children }: { label: string; help?: string; children: ReactNode }) {
  return (
    <div className="system-fact">
      <dt>
        <span>{label}</span>
        {help ? <HelpTip>{help}</HelpTip> : null}
      </dt>
      <dd>{children}</dd>
    </div>
  );
}

/**
 * Platform › System: the sandbox configuration every project shares — where
 * sandboxes run, how big each one is, the Runtime or E2B template they run,
 * the Core address and maintenance. Nothing else: harnesses and gateway flags
 * are Core's own startup settings, not something an administrator acts on here.
 */
export function SystemPage() {
  const { t } = useTranslation("system");
  const { t: tNav } = useTranslation("navigation");
  const { navigate } = useConsoleNavigation();
  const deployment = useQuery(sandboxDeploymentQuery);
  const data = deployment.data ?? null;
  const refresh = () => { void deployment.refetch(); };
  const spec = data?.specification ?? null;
  const size = (resources: SandboxResources) => t("sandbox.size", { count: resources.cpus, memory: formatBytes(resources.memory_mib * MIB) });

  let body: ReactNode;
  if (!data) {
    body = deployment.isError && !deployment.isFetching
      ? <ErrorState title={t("sandbox.deploymentFailed")} onRetry={refresh} />
      : <DetailSkeleton label={t("loading")} facts={5} />;
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
      <Section headingId="system-sandbox-heading" title={t("sandbox.title")}>
        <dl className="system-facts">
          <Fact label={t("sandbox.runsOn")}>{data.provider === "e2b" ? t("sandbox.e2b") : t("sandbox.ownMachines", { provider: providerNames[data.provider] ?? data.provider })}</Fact>
          {spec ? (
            <Fact label={t("sandbox.each")}>
              {size(spec.resources)}
              {spec.resources.root_disk_mib ? <span className="system-sub">{t("sandbox.disks", { root: formatBytes(spec.resources.root_disk_mib * MIB), data: formatBytes((spec.resources.environment_disk_mib ?? 0) * MIB) })}</span> : null}
            </Fact>
          ) : null}
          {spec?.runtime ? <Fact label={t("sandbox.runtime")} help={t("sandbox.runtimeHelp")}><code className="system-code" title={spec.runtime.source_commit}>{spec.runtime.source_commit.slice(0, 12)}</code></Fact> : null}
          {data.e2b?.template ? <Fact label={t("sandbox.e2bTemplate")}><code className="system-code">{data.e2b.template}</code></Fact> : null}
          {data.core_url ? <Fact label={t("sandbox.coreOrigin")} help={t("sandbox.coreOriginHelp")}><code className="system-code">{data.core_url}</code></Fact> : null}
          <Fact label={t("sandbox.maintenance")} help={t("sandbox.maintenanceHelp")}>
            {data.maintenance ? <StatusDot tone="warning" label={t("values.on")} /> : t("values.off")}
          </Fact>
        </dl>
      </Section>
    );
  }

  return (
    <section className="page-section console-page system-page" aria-labelledby="system-heading">
      <PageHeader
        headingId="system-heading"
        title={tNav("views.system")}
        help={t("help")}
        actions={<RefreshButton onClick={refresh} refreshing={deployment.isFetching} label={t("refresh")} />}
      />
      <PageBody>{body}</PageBody>
    </section>
  );
}
