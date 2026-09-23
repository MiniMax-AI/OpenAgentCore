import { SandboxDiagnostic } from "./SandboxDiagnostic";
import { useLocale } from "../../lib/LocaleProvider";
import type { Locale } from "../../lib/locale";
import type { SandboxNode } from "@agents-core-web/agents-client";
function bytes(value: number, locale: Locale): string {
  return `${(value / 1024 ** 3).toLocaleString(locale, { maximumFractionDigits: 1 })} GiB`;
}
export function NodeHealth({ node }: { node: SandboxNode }) {
  const { t, locale } = useLocale();
  const metric = (value: number | null) => value === null ? t("Unavailable") : bytes(value, locale);
  return <div>
    <strong>{node.online ? t("Online") : t("Offline")} · {!node.online ? t("Provider status unconfirmed") : node.provider_ready ? t("Provider ready") : t("Provider unavailable")}</strong>
    <SandboxDiagnostic diagnostic={!node.online ? "node_unavailable" : !node.provider_ready ? "provider_unavailable" : node.diagnostic} />
    <small>{t("Last seen")}: {node.last_seen_at ? new Date(node.last_seen_at).toLocaleString(locale) : t("Never")}</small>
    {node.online ? <small>{node.cpu_count ?? t("Unavailable")} {t("CPUs")} · {metric(node.available_memory_bytes)} {t("memory free")} · {metric(node.available_disk_bytes)} {t("disk free")}</small> : <small>{t("Host metrics unavailable (stale heartbeat)")}</small>}
  </div>;
}
