import { useEffect, useState } from "react";
import { AgentCoreError, type SandboxPlacement } from "@agents-core-web/agents-client";
import { SandboxDiagnostic } from "./SandboxDiagnostic";
import { useLocale } from "../../lib/LocaleProvider";
import { sandboxStateLabel } from "../../lib/sandbox-labels";
import { useSandboxClient } from "./SandboxContext";

export function SessionPlacement({ sessionId }: { sessionId: string }) {
  const { t, locale } = useLocale();
  const client = useSandboxClient();
  const [result, setResult] = useState<{ client: typeof client; sessionId: string; placement?: SandboxPlacement; error?: boolean; absent?: boolean } | null>(null);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!client) return;
    const controller = new AbortController();
    setResult(null);
    void client.retrieveSandboxPlacement(sessionId, { signal: controller.signal }).then((placement) => {
      if (!controller.signal.aborted) setResult({ client, sessionId, placement });
    }).catch((error) => {
      if (!controller.signal.aborted) setResult({ client, sessionId, ...(error instanceof AgentCoreError && error.status === 404 ? { absent: true } : { error: true }) });
    });
    return () => controller.abort();
  }, [client, sessionId, revision]);
  if (!client) return null;
  const current = result?.client === client && result.sessionId === sessionId ? result : null;
  return <div lang={locale}><dt>{t("Sandbox node")}</dt><dd>
    {!current ? <span role="status">{t("Loading placement…")}</span> : current.placement ? <>
      <strong>{current.placement.node_name}</strong> <code>{current.placement.node_id}</code>
      <div>{current.placement.available ? t("Available") : t("Unavailable")} · {t("Recorded allocation")}: {sandboxStateLabel(current.placement.state, locale)} · {t("Recorded compute")}: {sandboxStateLabel(current.placement.compute_phase, locale)}</div>
      <SandboxDiagnostic diagnostic={current.placement.diagnostic} />
      <button type="button" className="button" onClick={() => setRevision((v) => v + 1)}>{t("Refresh placement")}</button>
    </> : current.absent ? t("No hosted placement") : <span role="alert">{t("Sandbox placement could not be loaded.")} <button type="button" onClick={() => setRevision((v) => v + 1)}>{t("Retry placement")}</button></span>}
  </dd></div>;
}
