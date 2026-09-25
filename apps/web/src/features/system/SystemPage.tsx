import type { CoreHarnessKind, CoreStartupConfiguration } from "@agents-core-web/agents-client";
import { useQuery } from "@tanstack/react-query";
import { useCallback, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip, PageBody, PageHeader, RefreshButton, Section, StatusDot } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { CopyableId } from "../../components/list-ui";
import { admin } from "../../lib/projects";
import { sandboxDeploymentQuery } from "../sandbox/sandbox-queries";
import { formatBytes } from "../../lib/format";
import "./system.css";
import { startupConfigurationQuery } from "./system-queries";

const harnessNames: Record<CoreHarnessKind, string> = { claude_sdk: "Claude SDK", codex: "Codex", mcode: "MiniMax Code" };
const MIB = 2 ** 20;
const providerNames: Record<string, string> = { docker: "Docker", microsandbox: "microsandbox", e2b: "E2B" };

export interface HarnessRow {
  harness: CoreHarnessKind;
  enabled: boolean;
  isDefault: boolean;
  /** null when the harness is not enabled: Core reports endpoints only for enabled harnesses. */
  endpointConfigured: boolean | null;
}

/** Every harness this Core build supports, enabled ones first in Core's order. */
export function harnessRows(configuration: CoreStartupConfiguration): HarnessRow[] {
  const { configured, supported } = configuration;
  const endpoints = new Map(configured.model_providers.map((entry) => [entry.harness, entry.endpoint_configured]));
  const ordered = [...configured.enabled_harnesses, ...supported.harnesses.filter((harness) => !configured.enabled_harnesses.includes(harness))];
  return ordered.map((harness) => {
    const enabled = configured.enabled_harnesses.includes(harness);
    return {
      harness,
      enabled,
      isDefault: enabled && configured.default_harness === harness,
      endpointConfigured: enabled ? endpoints.get(harness) ?? null : null,
    };
  });
}

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

const missing = <span className="table-muted">—</span>;

/**
 * Platform › System: deployment-level configuration shared by every project.
 * Read-only facts from Core's startup configuration and the sandbox
 * deployment; anything Core did not report shows "—".
 */
export function SystemPage() {
  const { t } = useTranslation("system");
  const { t: tNav } = useTranslation("navigation");
  // Both reads come from the cache; a refresh keeps the last values on screen.
  const startup = useQuery(startupConfigurationQuery);
  const deployment = useQuery(sandboxDeploymentQuery);
  const { refetch: refetchStartup } = startup;
  const { refetch: refetchDeployment } = deployment;
  const refresh = useCallback(() => { void refetchStartup(); void refetchDeployment(); }, [refetchStartup, refetchDeployment]);
  const startupFailed = startup.isError && !startup.isFetching;
  const deploymentFailed = deployment.isError && !deployment.isFetching;
  const configuration: CoreStartupConfiguration | null = startup.data ?? null;
  const managed = configuration?.configured.managed_sandbox ?? null;
  const rows = configuration ? harnessRows(configuration) : [];
  const enabled = (value: boolean | null | undefined) => (value == null ? missing : value ? t("values.enabled") : t("values.disabled"));
  const provider = deployment.data?.provider || managed?.provider || "";
  const maintenance = deployment.data?.maintenance ?? managed?.maintenance ?? null;
  const mode = deployment.data?.mode || "";
  const spec = deployment.data?.specification ?? null;

  return (
    <section className="page-section console-page system-page" aria-labelledby="system-heading">
      <PageHeader
        headingId="system-heading"
        title={tNav("views.system")}
        help={t("help")}
        actions={<RefreshButton onClick={refresh} refreshing={startup.isFetching || deployment.isFetching} label={t("refresh")} />}
      />
      <PageBody>
        {startupFailed && !configuration ? (
          <ErrorState title={t("startupFailed")} onRetry={refresh} />
        ) : null}

        <Section headingId="system-harnesses-heading" title={t("harnesses.title")} help={t("harnesses.help")}>
          <div className="table-frame">
            <table className="data-table system-harnesses" aria-label={t("harnesses.title")}>
              <thead>
                <tr>
                  <th scope="col">{t("harnesses.harness")}</th>
                  <th scope="col">{t("harnesses.status")}</th>
                  <th scope="col"><span className="column-help">{t("harnesses.endpoint")}<HelpTip>{t("harnesses.endpointHelp")}</HelpTip></span></th>
                </tr>
              </thead>
              <tbody>
                {rows.length ? rows.map((row) => (
                  <tr key={row.harness}>
                    <th scope="row">
                      <span className="system-harness">
                        <span>{harnessNames[row.harness]}</span>
                        {row.isDefault ? <span className="system-flag">{t("harnesses.default")}</span> : null}
                      </span>
                    </th>
                    <td>{row.enabled ? <StatusDot tone="ok" label={t("values.enabled")} /> : <StatusDot tone="neutral" label={t("values.notEnabled")} />}</td>
                    <td>
                      {row.endpointConfigured === null ? missing
                        : row.endpointConfigured ? <StatusDot tone="ok" label={t("values.configured")} />
                          : <StatusDot tone="neutral" label={t("values.notConfigured")} />}
                    </td>
                  </tr>
                )) : (
                  <tr>
                    <td colSpan={3} className="table-muted">{startup.isFetching || startup.isPending ? t("loading") : "—"}</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </Section>

        <Section headingId="system-sandbox-heading" title={t("sandbox.title")} help={t("sandbox.help")}>
          {deploymentFailed && !deployment.data ? <p className="coverage-note coverage-note-error" role="alert">{t("sandbox.deploymentFailed")}</p> : null}
          <dl className="system-facts">
            <Fact label={t("sandbox.managed")} help={t("sandbox.managedHelp")}>{enabled(managed?.enabled)}</Fact>
            <Fact label={t("sandbox.provider")}>{provider ? providerNames[provider] ?? provider : missing}</Fact>
            <Fact label={t("sandbox.mode")}>{mode ? t(`sandbox.modes.${mode}`) : missing}</Fact>
            <Fact label={t("sandbox.maintenance")} help={t("sandbox.maintenanceHelp")}>
              {maintenance === null ? missing : maintenance ? <StatusDot tone="warning" label={t("values.on")} /> : t("values.off")}
            </Fact>
            <Fact label={t("sandbox.installation")}>{deployment.data?.installation_id ? <CopyableId id={deployment.data.installation_id} /> : missing}</Fact>
            <Fact label={t("sandbox.coreOrigin")} help={t("sandbox.coreOriginHelp")}>{deployment.data?.core_url ? <code className="system-code">{deployment.data.core_url}</code> : missing}</Fact>
            {deployment.data?.e2b ? <Fact label={t("sandbox.e2bTemplate")}><code className="system-code">{deployment.data.e2b.template || "—"}</code></Fact> : null}
            {/* The per-sandbox specification saved with the deployment: every sandbox gets these limits. */}
            <Fact label={t("sandbox.cpus")}>{spec ? t("sandbox.cores", { count: spec.resources.cpus }) : missing}</Fact>
            <Fact label={t("sandbox.memory")}>{spec ? formatBytes(spec.resources.memory_mib * MIB) : missing}</Fact>
            {spec?.resources.root_disk_mib ? <Fact label={t("sandbox.rootDisk")}>{formatBytes(spec.resources.root_disk_mib * MIB)}</Fact> : null}
            {spec?.resources.environment_disk_mib ? <Fact label={t("sandbox.dataDisk")} help={t("sandbox.dataDiskHelp")}>{formatBytes(spec.resources.environment_disk_mib * MIB)}</Fact> : null}
            {spec?.runtime ? <Fact label={t("sandbox.runtime")} help={t("sandbox.runtimeHelp")}><code className="system-code" title={spec.runtime.source_commit}>{spec.runtime.source_commit.slice(0, 12)}</code></Fact> : null}
          </dl>
        </Section>

        <Section headingId="system-gateway-heading" title={t("gateway.title")}>
          <dl className="system-facts">
            <Fact label={t("gateway.daemon")} help={t("gateway.daemonHelp")}>{enabled(configuration?.configured.daemon_gateway)}</Fact>
            <Fact label={t("gateway.selfHosted")} help={t("gateway.selfHostedHelp")}>{enabled(configuration?.configured.self_hosted)}</Fact>
          </dl>
        </Section>
      </PageBody>
    </section>
  );
}
