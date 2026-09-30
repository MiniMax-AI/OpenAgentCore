import type { HarnessModelConfiguration } from "@oac/agents-client";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { HelpTip, StatusDot } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { diagnosticFailure } from "../../lib/diagnostic-failure";
import { formatDateTime } from "../../lib/format";
import { Fact } from "./Fact";

/** Only a newer Core error remains actionable after a successful use. */
export function activeProviderError(provider: HarnessModelConfiguration) {
  return provider.last_error_code !== null && provider.last_error_at !== null &&
    (provider.last_used_at === null || Date.parse(provider.last_error_at) > Date.parse(provider.last_used_at));
}

export function ProviderObservations({ provider, name, stale }: { provider: HarnessModelConfiguration; name: string; stale: boolean }) {
  const { t, i18n } = useTranslation("diagnostics");
  const [open, setOpen] = useState(false);
  const activeError = activeProviderError(provider);
  const date = (value: string | null) => value === null ? t("providerUnknown") : formatDateTime(Date.parse(value) / 1000, i18n.resolvedLanguage);
  return <>
    <div className="system-model-observations">
      {stale ? <StatusDot tone="neutral" label={t("providerStale")} /> : activeError ? <StatusDot tone="warning" label={t("providerError")} /> : null}
      <button type="button" className="text-action" onClick={() => setOpen(true)}>{t("providerDetails")}</button>
    </div>
    <Modal open={open} title={t("providerTitle", { name })} onClose={() => setOpen(false)}>
      <dl className="system-facts">
        <Fact label={t("providerLastUsed")} help={t("providerHelp")}>{date(provider.last_used_at)}</Fact>
        <Fact label={t("providerLastError")}>
          {activeError && provider.last_error_code ? <>{diagnosticFailure({ code: provider.last_error_code, params: {}, failed_at: provider.last_error_at }, t)} <HelpTip>{date(provider.last_error_at)}</HelpTip></> : t("providerNoError")}
        </Fact>
      </dl>
      {stale ? <p role="status">{t("providerStaleHelp")}</p> : null}
    </Modal>
  </>;
}
