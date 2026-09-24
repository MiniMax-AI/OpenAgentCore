import { Server } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  EmptyState,
  Kpi,
  KpiStrip,
  Meter,
  PageBody,
  PageHeader,
  RefreshButton,
  Section,
  StatusDot,
} from "../../components/console-ui";
import type { CoreConnectionState } from "../../lib/connection";
import type { ConsoleView } from "../../lib/console-routes";
import { formatBytes, formatClock, formatInteger, formatRelative, MISSING, shortId } from "../../lib/format";
import { RuntimeObservabilityContent, type RuntimeHistoryLoader } from "../dashboard/RuntimeObservabilityContent";
import type { RuntimeDashboardSnapshot } from "../dashboard/runtime-snapshot";
import { fleetSnapshot, useSandboxFleet } from "../fleet/use-sandbox-fleet";
import { capacitySummary, nodeHealth } from "../overview/overview-model";
import "./MetricsView.css";

export function SandboxMetricsView({
  coreBaseUrl,
  runtimeSnapshot,
  runtimeState,
  runtimeError,
  loadRuntimeHistory,
  onRefresh,
  onOpenSession,
  onNavigate,
}: {
  coreBaseUrl: string;
  runtimeSnapshot: RuntimeDashboardSnapshot | null;
  runtimeState: CoreConnectionState;
  runtimeError: string | null;
  loadRuntimeHistory: RuntimeHistoryLoader;
  onRefresh: () => void;
  onOpenSession: (sessionId: string) => void;
  onNavigate: (view: ConsoleView) => void;
}) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { state: fleetState, refresh: refreshFleet } = useSandboxFleet(coreBaseUrl);
  const fleet = fleetSnapshot(fleetState);
  const capacity = fleet ? capacitySummary(fleet.nodes) : null;
  const now = Math.floor(Date.now() / 1000);
  const refreshing = runtimeState === "connecting" || (fleetState.status === "ready" && fleetState.refreshing);
  const updatedAt = runtimeSnapshot?.loadedAt ?? fleet?.loadedAt ?? null;
  const fleetMessage = fleetState.status === "remote"
    ? t("sandbox.fleetRemote")
    : fleetState.status === "unconfigured"
      ? t("sandbox.fleetUnconfigured")
      : fleetState.status === "failed"
        ? t("sandbox.fleetFailed")
        : t("sandbox.fleetLoading");

  return (
    <section className="page-section console-page metrics-page" aria-labelledby="sandbox-metrics-heading">
      <PageHeader
        headingId="sandbox-metrics-heading"
        title={t("sandbox.title")}
        help={t("sandbox.description")}
        actions={<RefreshButton refreshing={refreshing} updatedAt={updatedAt ? formatClock(updatedAt, locale) : null} onClick={() => { onRefresh(); refreshFleet(); }} />}
      />
      <PageBody>
        <KpiStrip label={t("sandbox.kpiLabel")}>
          <Kpi
            label={t("sandbox.nodesOnline")}
            value={capacity ? `${capacity.online} / ${capacity.nodes}` : MISSING}
            tone={capacity && capacity.nodes ? capacity.online < capacity.nodes ? "danger" : capacity.available < capacity.online ? "warning" : "ok" : undefined}
            help={capacity ? t("sandbox.nodesAvailable", { count: capacity.available }) : fleetMessage}
          />
          <Kpi label={t("sandbox.activeSandboxes")} value={capacity ? `${formatInteger(capacity.active, locale)} / ${formatInteger(capacity.maxActive, locale)}` : MISSING} help={t("sandbox.activeDetail")} />
          <Kpi label={t("sandbox.retained")} value={capacity ? `${formatInteger(capacity.retained, locale)} / ${formatInteger(capacity.maxRetained, locale)}` : MISSING} help={t("sandbox.retainedDetail")} />
          <Kpi label={t("sandbox.reserved")} value={capacity ? formatInteger(capacity.reserved, locale) : MISSING} help={t("sandbox.reservedDetail")} />
          <Kpi
            label={t("sandbox.cleanupPending")}
            value={capacity ? formatInteger(capacity.cleanupPending, locale) : MISSING}
            tone={capacity ? capacity.cleanupPending > 0 ? "warning" : "ok" : undefined}
            help={t("sandbox.cleanupDetail")}
          />
          <Kpi label={t("sandbox.freeMemory")} value={capacity ? formatBytes(capacity.availableMemoryBytes) : MISSING} help={capacity ? t("sandbox.freeDisk", { value: formatBytes(capacity.availableDiskBytes) }) : fleetMessage} />
        </KpiStrip>

        <Section
          headingId="node-capacity-heading"
          title={t("sandbox.nodesSection")}
          help={t("sandbox.nodesSectionDetail")}
          actions={<button className="text-action" type="button" onClick={() => onNavigate("nodes")}>{t("sandbox.manageNodes")}</button>}
        >
          {fleet ? fleet.nodes.length ? (
            <div className="table-frame">
              <table className="data-table">
                <thead>
                  <tr>
                    <th scope="col">{t("sandbox.node")}</th>
                    <th scope="col">{t("sandbox.status")}</th>
                    <th scope="col">{t("sandbox.slots")}</th>
                    <th scope="col" className="numeric">{t("sandbox.retained")}</th>
                    <th scope="col" className="numeric">{t("sandbox.reserved")}</th>
                    <th scope="col" className="numeric">{t("sandbox.cleanupPending")}</th>
                    <th scope="col" className="numeric">{t("sandbox.cpus")}</th>
                    <th scope="col" className="numeric">{t("sandbox.freeMemory")}</th>
                    <th scope="col" className="numeric">{t("sandbox.freeDiskColumn")}</th>
                    <th scope="col" className="numeric">{t("sandbox.lastSeen")}</th>
                  </tr>
                </thead>
                <tbody>
                  {fleet.nodes.map((node) => {
                    const health = nodeHealth(node);
                    return (
                      <tr key={node.id}>
                        <th scope="row" title={node.id}><span className="table-primary">{node.name || shortId(node.id)}</span></th>
                        <td><StatusDot tone={health === "available" ? "ok" : health === "degraded" ? "warning" : "danger"} label={t(`sandbox.health.${health}`)} /></td>
                        <td>
                          <span className="table-meter">
                            <Meter value={node.active} limit={node.max_active} label={t("sandbox.slotsOf", { name: node.name })} />
                            <span>{node.active} / {node.max_active}</span>
                          </span>
                        </td>
                        <td className="numeric">{node.retained} / {node.max_retained}</td>
                        <td className="numeric">{node.reserved}</td>
                        <td className="numeric">{node.cleanup_pending}</td>
                        <td className="numeric">{node.online ? node.cpu_count ?? MISSING : MISSING}</td>
                        <td className="numeric">{node.online ? formatBytes(node.available_memory_bytes) : MISSING}</td>
                        <td className="numeric">{node.online ? formatBytes(node.available_disk_bytes) : MISSING}</td>
                        <td className="numeric">{formatRelative(node.last_seen_at ? Date.parse(node.last_seen_at) / 1000 : null, now, locale)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <EmptyState
              icon={Server}
              title={t("sandbox.noNodesTitle")}
              description={t("sandbox.noNodesDescription")}
              action={<button className="button primary" type="button" onClick={() => onNavigate("nodes")}>{t("sandbox.addNode")}</button>}
            />
          ) : <p className="page-status" role={fleetState.status === "failed" ? "alert" : "status"}>{fleetMessage}</p>}
        </Section>

        <Section headingId="runtime-heading" title={t("sandbox.runtimeSection")} help={t("sandbox.runtimeSectionDetail")}>
          {runtimeSnapshot ? (
            runtimeSnapshot.observations.some((observation) => observation.mode === "openai_hosted") ? (
              <div className="runtime-embed">
                <RuntimeObservabilityContent
                  snapshot={runtimeSnapshot}
                  stale={runtimeState === "failed"}
                  loadRuntimeHistory={loadRuntimeHistory}
                  onOpenSession={onOpenSession}
                />
              </div>
            ) : <EmptyState title={t("sandbox.noRuntimeTitle")} description={t("sandbox.noRuntimeDescription")} />
          ) : (
            <p className="page-status" role={runtimeState === "failed" ? "alert" : "status"}>
              {runtimeState === "connecting" ? t("sandbox.runtimeLoading") : t("sandbox.runtimeUnavailable", { reason: runtimeError ?? "" })}
            </p>
          )}
        </Section>
      </PageBody>
    </section>
  );
}
