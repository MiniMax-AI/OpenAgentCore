import type { CoreHarness } from "@oac/agents-client";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { HelpTip, StatusDot } from "../../components/console-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatDateTime, formatInteger } from "../../lib/format";
import { harnessNames, protocolNames } from "../../lib/harness-labels";
import { Fact } from "./Fact";
import { harnessReady } from "./harness-queries";
import { ProviderObservations } from "./ProviderObservations";

export function HarnessCard({ harness, busy, stale, onEdit, onClear }: { harness: CoreHarness; busy: boolean; stale: boolean; onEdit: () => void; onClear: () => void }) {
  const { t, i18n } = useTranslation("system");
  const { navigate } = useConsoleNavigation();
  const locale = i18n.resolvedLanguage;
  const headingId = useId();
  const name = harnessNames[harness.id];
  const configuration = harness.model_configuration;
  const provider = configuration?.model_provider;
  const status = harnessReady(harness) ? "ready" : harness.enabled ? "needsProvider" : "disabled";
  return (
    <article className="settings-rows system-model" aria-labelledby={headingId} data-default={harness.default ? "" : undefined}>
      <header className="system-model-header">
        <div className="system-model-title">
          <h3 id={headingId}>{name}</h3>
          <StatusDot tone={status === "ready" ? "ok" : status === "needsProvider" ? "warning" : "neutral"} label={t(`models.status.${status}`)} />
          {harness.default ? <span className="pill">{t("models.default")}</span> : null}
          <HelpTip>{t(`models.statusHelp.${status}`)}</HelpTip>
          {/* Enabling a harness is a startup setting; Service and deployment shows it and how to change it. */}
          {status === "disabled" ? <button className="text-action" type="button" onClick={() => navigate("settings", { id: "service" })}>{t("models.startupSettings")}</button> : null}
        </div>
        <div className="system-model-actions">
          {/* A disabled harness may still be configured; Core keeps the provider until it is enabled. */}
          <button className={status === "needsProvider" ? "button primary" : "button outline"} type="button" aria-label={t(provider ? "models.replaceLabel" : "models.setLabel", { harness: name })} disabled={busy} onClick={onEdit}>
            {provider ? t("models.replace") : t("models.set")}
          </button>
          {provider ? (
            <button className="button outline" type="button" aria-label={t("models.clearLabel", { harness: name })} disabled={busy} onClick={onClear}>
              {t("models.clear")}
            </button>
          ) : null}
        </div>
      </header>
      {provider && configuration ? <>
        <Fact label={t("models.model")}><code className="system-code">{configuration.model}</code></Fact>
        <Fact label={t("models.protocol")}>{protocolNames[provider.protocol]}</Fact>
        <Fact label={t("models.baseUrl")}><code className="system-code">{provider.base_url}</code></Fact>
        <Fact label={t("models.apiKey")}>{provider.api_key_configured ? t("models.keyConfigured") : t("models.keyNotConfigured")}</Fact>
        {provider.context_window !== undefined ? <Fact label={t("models.contextWindow")}>{formatInteger(provider.context_window, locale)}</Fact> : null}
        {provider.max_output_tokens !== undefined ? <Fact label={t("models.maxOutputTokens")}>{formatInteger(provider.max_output_tokens, locale)}</Fact> : null}
        <Fact label={t("models.updated")}>{formatDateTime(Math.floor(Date.parse(configuration.updated_at) / 1000), locale)}</Fact>
      </> : (
        <Fact label={t("models.provider")}><span className="system-muted">{t("models.notSet")}</span></Fact>
      )}
      {configuration ? <ProviderObservations provider={configuration} name={name} stale={stale} /> : null}
    </article>
  );
}
