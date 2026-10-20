import type { SandboxNode, SandboxNodeRollout } from "@oac/agents-client";
import { useTranslation } from "react-i18next";
import { StatusDot, type Tone } from "../../components/console-ui";
import type { ParseKeys } from "i18next";
import { DiagnosticTip } from "../fleet/DiagnosticTip";

const labels: Record<SandboxNodeRollout["state"], ParseKeys<"sandbox">> = { ready: "Ready for target", preparing: "Preparing target", failed: "Preparation failed", unknown: "Target readiness unknown" };
const tones: Record<SandboxNodeRollout["state"], Tone> = { ready: "ok", preparing: "neutral", failed: "warning", unknown: "neutral" };

/** A serving pin is historical ownership, not proof of a live connection or capacity. */
export function NodeRolloutStatus({ node, stale = false }: { node: SandboxNode; stale?: boolean }) {
  const { t } = useTranslation("sandbox");
  const state = stale || !node.online ? "unknown" : node.rollout.state;
  return <span className="status-with-help">
    <StatusDot tone={tones[state]} label={t(labels[state])} />
    {state === "failed" && node.rollout.diagnostic ? <DiagnosticTip code={node.rollout.diagnostic} /> : null}
  </span>;
}
