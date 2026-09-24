import { SandboxAdminClient, type CoreHarnessKind, type CoreStartupConfiguration, type SandboxDeployment } from "@agents-core-web/agents-client";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip, PageBody, PageHeader, RefreshButton, Section, StatusDot } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { CopyableId } from "../../components/list-ui";
import { admin } from "../../lib/projects";
import "./system.css";

type Loaded<T> = { status: "loading"; value: T | null } | { status: "ready"; value: T } | { status: "failed"; value: T | null };

const harnessNames: Record<CoreHarnessKind, string> = { claude_sdk: "Claude SDK", codex: "Codex", mcode: "MiniMax Code" };
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
  const sandbox = useMemo(() => new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" }), []);
  const [revision, setRevision] = useState(0);
  const [startup, setStartup] = useState<Loaded<CoreStartupConfiguration>>({ status: "loading", value: null });
  const [deployment, setDeployment] = useState<Loaded<SandboxDeployment>>({ status: "loading", value: null });

  useEffect(() => {
    const controller = new AbortController();
    setStartup((current) => ({ status: "loading", value: current.value }));
    setDeployment((current) => ({ status: "loading", value: current.value }));
    admin.retrieveStartupConfiguration({ signal: controller.signal }).then(
      (value) => setStartup({ status: "ready", value }),
      () => { if (!controller.signal.aborted) setStartup((current) => ({ status: "failed", value: current.value })); },
    );
    sandbox.retrieveDeployment({ signal: controller.signal }).then(
      (value) => setDeployment({ status: "ready", value }),
      () => { if (!controller.signal.aborted) setDeployment((current) => ({ status: "failed", value: current.value })); },
    );
    return () => controller.abort();
  }, [revision, sandbox]);

  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const configuration = startup.value;
  const managed = configuration?.configured.managed_sandbox ?? null;
  const rows = configuration ? harnessRows(configuration) : [];
  const enabled = (value: boolean | null | undefined) => (value == null ? missing : value ? t("values.enabled") : t("values.disabled"));
  const provider = deployment.value?.provider || managed?.provider || "";
  const maintenance = deployment.value?.maintenance ?? managed?.maintenance ?? null;
  const mode = deployment.value?.mode || "";

  return (
    <section className="page-section console-page system-page" aria-labelledby="system-heading">
      <PageHeader
        headingId="system-heading"
        title={tNav("views.system")}
        help={t("help")}
        actions={<RefreshButton onClick={refresh} refreshing={startup.status === "loading" || deployment.status === "loading"} label={t("refresh")} />}
      />
      <PageBody>
        {startup.status === "failed" && !configuration ? (
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
                    <td colSpan={3} className="table-muted">{startup.status === "loading" ? t("loading") : "—"}</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </Section>

        <Section headingId="system-sandbox-heading" title={t("sandbox.title")} help={t("sandbox.help")}>
          {deployment.status === "failed" && !deployment.value ? <p className="coverage-note coverage-note-error" role="alert">{t("sandbox.deploymentFailed")}</p> : null}
          <dl className="system-facts">
            <Fact label={t("sandbox.managed")} help={t("sandbox.managedHelp")}>{enabled(managed?.enabled)}</Fact>
            <Fact label={t("sandbox.provider")}>{provider ? providerNames[provider] ?? provider : missing}</Fact>
            <Fact label={t("sandbox.mode")}>{mode ? t(`sandbox.modes.${mode}`) : missing}</Fact>
            <Fact label={t("sandbox.maintenance")} help={t("sandbox.maintenanceHelp")}>
              {maintenance === null ? missing : maintenance ? <StatusDot tone="warning" label={t("values.on")} /> : t("values.off")}
            </Fact>
            <Fact label={t("sandbox.installation")}>{deployment.value?.installation_id ? <CopyableId id={deployment.value.installation_id} /> : missing}</Fact>
            <Fact label={t("sandbox.coreOrigin")} help={t("sandbox.coreOriginHelp")}>{deployment.value?.core_url ? <code className="system-code">{deployment.value.core_url}</code> : missing}</Fact>
            {deployment.value?.e2b ? <Fact label={t("sandbox.e2bTemplate")}><code className="system-code">{deployment.value.e2b.template || "—"}</code></Fact> : null}
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
