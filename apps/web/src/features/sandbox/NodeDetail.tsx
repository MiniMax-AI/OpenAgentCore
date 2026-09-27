import type { SandboxAllocation, SandboxDeployment, SandboxNode } from "@agents-core-web/agents-client";
import { suspendedSandboxes } from "../fleet/fleet-model";
import { useTranslation } from "react-i18next";

import { EmptyState, HelpTip, Kpi, KpiStrip, Section } from "../../components/console-ui";
import { CopyableId } from "../../components/list-ui";
import { formatDateTime, formatInteger, formatRelative, formatSpan, MISSING } from "../../lib/format";
import { nodeProviderDiagnostic, sandboxDiagnosticMessage } from "../../lib/sandbox-diagnostic";
import { sandboxStateLabel } from "../../lib/sandbox-labels";
import { DiagnosticTip } from "../fleet/DiagnosticTip";
import { phaseTiming } from "./allocation-phase";
import { nodeState, NodeStatus, OldAddressHint, seconds } from "./NodeList";

/** Why a node is not serving: disconnected, or the reason its provider is not ready. */
function nodeDiagnostic(node: SandboxNode): string {
  return node.online ? nodeProviderDiagnostic(node) : "node_unavailable";
}

function Diagnostic({ value }: { value: string }) {
  const { i18n } = useTranslation("sandbox");
  const message = sandboxDiagnosticMessage(value, i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en");
  if (!message) return <>{MISSING}</>;
  return <span className="node-diagnostic">{message.label}<HelpTip label={message.label}>{message.advice}</HelpTip></span>;
}

/** How long the allocation has been in its compute phase and, while suspended, about when Core reclaims it. */
function PhaseTime({ allocation, retentionSeconds, now }: { allocation: SandboxAllocation; retentionSeconds: number; now: number }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage;
  const timing = phaseTiming(allocation.compute_phase, allocation.compute_phase_changed_at, retentionSeconds, now);
  if (!timing) return <td>{MISSING}</td>;
  const elapsed = formatSpan(timing.elapsed, locale);
  const text = timing.reclaimIn === null ? t("Since {{time}}", { time: formatRelative(timing.since, now, locale) })
    : timing.reclaimIn > 0 ? t("Suspended for {{elapsed}} · reclaimed in about {{remaining}}", { elapsed, remaining: formatSpan(timing.reclaimIn, locale) })
      : t("Suspended for {{elapsed}} · reclaim due", { elapsed });
  return <td className="nodes-nowrap" title={formatDateTime(timing.since, locale)}>{text}</td>;
}

export function NodeDetail({ node, allocations, coreUrl, stale, suspension }: {
  node: SandboxNode;
  allocations: readonly SandboxAllocation[];
  /** The deployment's address; a node enrolled with another one is on an old address. */
  coreUrl: string;
  stale: boolean;
  /** The deployment's idle suspension policy; only microsandbox has one. */
  suspension: SandboxDeployment["suspension"];
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage;
  const shortLocale = locale?.startsWith("zh") ? "zh" : "en";
  const now = Math.floor(Date.now() / 1000);
  const own = allocations.filter((allocation) => allocation.node_id === node.id);
  const reporting = !stale && node.online;
  // Only microsandbox suspends sandboxes into snapshots; Docker retains nothing.
  const suspends = node.provider === "microsandbox";
  const state = nodeState(node, own, stale, coreUrl);
  // As in the list, an old address is the status to act on; the node's health would only distract.
  const diagnostic = stale || state === "old_address" ? "" : nodeDiagnostic(node);
  const count = (value: number) => formatInteger(value, locale);
  return (
    <>
      <dl className="resource-facts">
        <div><dt>{t("Node ID")}</dt><dd><CopyableId id={node.id} label={t("Node ID")} /></dd></div>
        <div>
          <dt>{t("Status")}</dt>
          <dd className="node-status-fact">
            <NodeStatus state={state} />
            {diagnostic ? <DiagnosticTip code={diagnostic} /> : null}
            {state === "old_address" ? <OldAddressHint /> : null}
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

      {/* Cleanup and host resources are on Sandbox metrics; the node's limit is here as well, beside Edit node that sets it. */}
      <Section headingId="node-capacity-heading" title={t("Capacity")}>
        <KpiStrip label={t("Capacity")}>
          <Kpi label={t("Active / limit")} help={t("Sandboxes Core has placed on this node, against the most it places here at once.")} value={`${count(node.active)} / ${count(node.max_active)}`} />
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
                  {/* Only microsandbox changes compute phase; under Docker it is always disabled. */}
                  {suspension ? <th scope="col"><span className="column-help">{t("In this state")}<HelpTip>{t("How long the sandbox has been in its compute state. For a suspended one, the reclaim time is estimated from when it was suspended and the deployment's retention; Core reclaims it around then. Older allocations show a dash until their state next changes.")}</HelpTip></span></th> : null}
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
                    {suspension ? <PhaseTime allocation={allocation} retentionSeconds={suspension.retention_seconds} now={now} /> : null}
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
