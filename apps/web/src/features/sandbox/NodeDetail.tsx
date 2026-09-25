import type { SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";
import { suspendedSandboxes } from "../fleet/fleet-model";
import { useTranslation } from "react-i18next";

import { EmptyState, HelpTip, Kpi, KpiStrip, Section } from "../../components/console-ui";
import { CopyableId } from "../../components/list-ui";
import { formatDateTime, formatInteger, formatRelative, MISSING } from "../../lib/format";
import { sandboxDiagnosticMessage } from "../../lib/sandbox-diagnostic";
import { sandboxStateLabel } from "../../lib/sandbox-labels";
import { nodeState, NodeStatus, seconds } from "./NodeList";

/** Why a node is not serving: disconnected, provider down, or a reported diagnostic. */
function nodeDiagnostic(node: SandboxNode): string {
  if (!node.online) return "node_unavailable";
  if (!node.provider_ready) return "provider_unavailable";
  return node.diagnostic;
}

function Diagnostic({ value }: { value: string }) {
  const { i18n } = useTranslation("sandbox");
  const message = sandboxDiagnosticMessage(value, i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en");
  if (!message) return <>{MISSING}</>;
  return <span className="node-diagnostic">{message.label}<HelpTip label={message.label}>{message.advice}</HelpTip></span>;
}

export function NodeDetail({ node, allocations, stale }: { node: SandboxNode; allocations: readonly SandboxAllocation[]; stale: boolean }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage;
  const shortLocale = locale?.startsWith("zh") ? "zh" : "en";
  const now = Math.floor(Date.now() / 1000);
  const own = allocations.filter((allocation) => allocation.node_id === node.id);
  const reporting = !stale && node.online;
  // Only microsandbox suspends sandboxes into snapshots; Docker retains nothing.
  const suspends = node.provider === "microsandbox";
  const diagnostic = stale ? "" : nodeDiagnostic(node);
  const count = (value: number) => formatInteger(value, locale);
  return (
    <>
      <dl className="resource-facts">
        <div><dt>{t("Node ID")}</dt><dd><CopyableId id={node.id} label={t("Node ID")} /></dd></div>
        <div>
          <dt>{t("Status")}</dt>
          <dd className="node-status-fact">
            <NodeStatus state={nodeState(node, own, stale)} />
            {diagnostic ? <HelpTip>{sandboxDiagnosticMessage(diagnostic, shortLocale)?.advice}</HelpTip> : null}
          </dd>
        </div>
        <div>
          <dt>{t("Last seen")}</dt>
          <dd title={node.last_seen_at ? formatDateTime(seconds(node.last_seen_at), locale) : undefined}>
            {node.last_seen_at ? formatRelative(seconds(node.last_seen_at), now, locale) : t("Never")}
          </dd>
        </div>
        <div><dt>{t("Added")}</dt><dd>{formatDateTime(seconds(node.created_at), locale)}</dd></div>
      </dl>

      {/* Active slots, cleanup and host resources are on Sandbox metrics; only what it does not show is here. */}
      <Section headingId="node-capacity-heading" title={t("Capacity")}>
        <KpiStrip label={t("Capacity")}>
          <Kpi label={t("Running")} value={reporting ? count(node.running) : MISSING} />
          {suspends ? <Kpi label={t("Suspended")} help={t("Suspended sandboxes keep their state as a snapshot on the node and resume on the Session's next Turn. They count toward the retained limit, not the active one.")} value={count(suspendedSandboxes(node))} /> : null}
          {suspends ? <Kpi label={t("Retained / limit")} help={t("Sandboxes this node holds, active and suspended, against its retained limit.")} value={`${count(node.retained)} / ${count(node.max_retained)}`} /> : null}
          {suspends ? <Kpi label={t("Snapshots")} value={reporting ? count(node.snapshots) : MISSING} /> : null}
          <Kpi label={t("Reserved")} value={count(node.reserved)} />
        </KpiStrip>
      </Section>

      <Section
        headingId="node-allocations-heading"
        title={<>{t("Allocations")}<span className="heading-count">{own.length}</span></>}
      >
        {own.length ? (
          <div className="table-frame">
            <table className="data-table" aria-label={t("Sandbox allocations")}>
              <thead>
                <tr>
                  <th scope="col">{t("Session")}</th>
                  <th scope="col">{t("Recorded state")}</th>
                  <th scope="col">{t("Recorded compute")}</th>
                  <th scope="col">{t("Issue")}</th>
                  <th scope="col">{t("Created")}</th>
                </tr>
              </thead>
              <tbody>
                {own.map((allocation) => (
                  <tr key={allocation.id}>
                    <th scope="row"><CopyableId id={allocation.session_id} label={t("Session")} /></th>
                    <td>{sandboxStateLabel(allocation.state, shortLocale)}</td>
                    <td>{allocation.compute_phase ? sandboxStateLabel(allocation.compute_phase, shortLocale) : MISSING}</td>
                    <td>{allocation.diagnostic ? <Diagnostic value={allocation.diagnostic} /> : MISSING}</td>
                    <td className="nodes-nowrap">{formatDateTime(seconds(allocation.created_at), locale)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : <EmptyState title={t("No sandbox allocations.")} />}
      </Section>
    </>
  );
}
