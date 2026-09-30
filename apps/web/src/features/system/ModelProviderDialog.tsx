import { AgentCoreError, type CoreHarness, type CoreHarnessKind, type ModelProviderInput } from "@oac/agents-client";
import { useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ConsoleSelect } from "../../components/console-select";
import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { coreError, coreFieldError } from "../../lib/core-error";
import { harnessNames, protocolNames } from "../../lib/harness-labels";
import { admin } from "../../lib/projects";

/** A write that gets no answer in this time has an unknown outcome. */
const WRITE_TIMEOUT_MS = 30_000;

type Protocol = ModelProviderInput["protocol"];
/** Core stores both token limits as 32-bit integers. */
const INT32_MAX = 2_147_483_647;

/** A token limit: empty is omitted; anything but a whole number within Core's range is a problem. */
function tokenLimit(text: string): { value: number | undefined; problem: "whole" | "large" | null } {
  const value = text.trim();
  if (!value) return { value: undefined, problem: null };
  if (!/^\d+$/u.test(value)) return { value: undefined, problem: "whole" };
  const number = Number(value);
  return number > INT32_MAX ? { value: undefined, problem: "large" } : { value: number, problem: null };
}

function isProviderUrl(value: string): boolean {
  try {
    const url = new URL(value);
    const authority = /^https:\/\/([^/]+)/iu.exec(value)?.[1];
    return authority !== undefined && url.protocol === "https:" && url.hostname !== "" && !authority.includes("@")
      && !/[\\\s?#]/u.test(value);
  } catch {
    return false;
  }
}

/**
 * Sets or replaces one harness's model configuration. Non-secret fields start from the
 * current provider; the API key never does. The protocol describes the upstream
 * model provider. The form checks the HTTPS provider URL and whole-number
 * limits within Core's range, with max output no larger than the context window.
 * Core's typed rejection is shown beside its field, or beside the form
 * when no editable field applies. Enter saves; a save in flight blocks another.
 */
export function ModelProviderDialog({ harness, onClose, onSaved, onReread }: {
  harness: CoreHarness | null;
  onClose: () => void;
  onSaved: (harness: CoreHarnessKind) => void;
  onReread: () => void;
}) {
  const { t } = useTranslation("system");
  const { t: tCommon } = useTranslation();
  const id = useId();
  const formId = `${id}-form`;
  const configuration = harness?.model_configuration ?? null;
  const current = configuration?.model_provider ?? null;
  const support = harness?.model_configuration_support;
  const limitsRequired = support?.token_limits_required ?? false;
  const protocolOptions = (support?.protocols ?? []).map((value) => ({ value, label: protocolNames[value] }));
  const [protocol, setProtocol] = useState<Protocol | "">(current?.protocol ?? support?.protocols[0] ?? "");
  const [baseUrl, setBaseUrl] = useState(current?.base_url ?? "");
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState(configuration?.model ?? "");
  const [configText, setConfigText] = useState(JSON.stringify(configuration?.harness_config ?? {}, null, 2));
  const [configTouched, setConfigTouched] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(Boolean(configuration && (Object.keys(configuration.harness_config ?? {}).length || current?.context_window || current?.max_output_tokens)) || limitsRequired);
  let nativeConfig: Record<string, unknown> | null = null;
  try {
    const parsed: unknown = configText.trim() ? JSON.parse(configText) : {};
    if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) nativeConfig = parsed as Record<string, unknown>;
  } catch { /* Keep the draft intact; only an object can be saved. */ }
  const [contextWindow, setContextWindow] = useState(current?.context_window === undefined ? "" : String(current.context_window));
  const [maxOutputTokens, setMaxOutputTokens] = useState(current?.max_output_tokens === undefined ? "" : String(current.max_output_tokens));
  const [busy, setBusy] = useState(false);
  const saving = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const [rejection, setRejection] = useState<unknown>(null);
  const fieldError = (param: string) => coreFieldError(rejection, param, tCommon) ?? coreFieldError(rejection, `model_provider.${param}`, tCommon);

  const name = harness ? harnessNames[harness.id] : "";
  const protocolProblem = !protocol ? t("models.form.protocolUnavailable") : !support?.protocols.includes(protocol) ? t("models.form.protocolUnsupported", { protocol: protocolNames[protocol] }) : null;
  const configTooLarge = nativeConfig !== null && new TextEncoder().encode(JSON.stringify(nativeConfig)).length > 16 * 1024;
  const url = baseUrl.trim();
  const urlProblem = url && !isProviderUrl(url) ? t("models.form.baseUrlInvalid") : null;
  const context = tokenLimit(contextWindow);
  const output = tokenLimit(maxOutputTokens);
  const limitProblem = (limit: ReturnType<typeof tokenLimit>) => (limit.problem === "whole" ? t("models.form.wholeNumber") : limit.problem === "large" ? t("models.form.tooLarge") : null);
  const outputProblem = limitProblem(output);
  // The missing or undersized window is the field the administrator must fix.
  const contextProblem = limitProblem(context) ?? (!outputProblem && output.value !== undefined && output.value > (context.value ?? 0) ? t("models.form.needsContext") : null);
  const ready = harness !== null && !busy && url !== "" && !urlProblem && apiKey.trim() !== "" && model.trim() !== "" && nativeConfig !== null && !configTooLarge && !protocolProblem && (!limitsRequired || Boolean(context.value && output.value)) && !contextProblem && !outputProblem;

  function clearNativeConfig() {
    setConfigText("{}");
    setConfigTouched(false);
  }

  async function save() {
    if (!ready || !harness || !protocol || !nativeConfig || saving.current) return;
    saving.current = true;
    setBusy(true);
    setError(null); setRejection(null);
    try {
      await admin.setHarnessModelConfiguration(harness.id, {
        model: model.trim(), harness_config: nativeConfig,
        model_provider: { protocol, base_url: url, api_key: apiKey.trim(),
        ...(context.value === undefined ? {} : { context_window: context.value }),
        ...(output.value === undefined ? {} : { max_output_tokens: output.value }),
        },
      }, { signal: AbortSignal.timeout(WRITE_TIMEOUT_MS) });
      onSaved(harness.id);
    } catch (caught) {
      // Never retried: a rejection shows Core's reason; an unknown outcome is read again first.
      if (caught instanceof AgentCoreError && caught.status >= 400 && caught.status < 500 && caught.status !== 408) {
        setRejection(caught);
        if (caught.param === "harness_config" || ["context_window", "max_output_tokens", "model_provider.context_window", "model_provider.max_output_tokens"].includes(caught.param ?? "")) setAdvancedOpen(true);
        setError(coreError(caught, tCommon));
      } else if (caught instanceof AgentCoreError && caught.code === "credential_storage_unavailable") {
        // A deployment without a credential key stores nothing: a configuration error, not an unknown outcome.
        setError(t("models.form.noCredentialKey"));
      } else {
        setError(t("models.form.uncertain"));
        onReread();
      }
    } finally {
      saving.current = false;
      setBusy(false);
    }
  }

  const limitField = (field: "context" | "output", value: string, setValue: (value: string) => void, problem: string | null) => {
    const inputId = `${id}-${field}`;
    problem = problem ?? fieldError(field === "context" ? "context_window" : "max_output_tokens");
    return (
      <div className="field">
        <span className="field-label-row">
          <label htmlFor={inputId}>{t(field === "context" ? "models.contextWindow" : "models.maxOutputTokens")}</label>
          <HelpTip id={`${inputId}-help`}>{t(`models.form.${field}Help${limitsRequired ? "Required" : ""}`)}</HelpTip>
        </span>
        <input
          id={inputId}
          inputMode="numeric"
          autoComplete="off"
          value={value}
          onChange={(event) => { setValue(event.target.value); setRejection(null); setError(null); }}
          aria-required={limitsRequired}
          aria-invalid={problem ? true : undefined}
          aria-describedby={`${inputId}-help${problem ? ` ${inputId}-problem` : ""}`}
        />
        {problem ? <span id={`${inputId}-problem`} className="field-error">{problem}</span> : null}
      </div>
    );
  };

  const baseUrlError = urlProblem ?? fieldError("base_url");
  const apiKeyError = fieldError("api_key");
  const configError = (configTouched && configTooLarge ? t("models.form.configTooLarge") : configTouched && nativeConfig === null ? t("models.form.configInvalid") : null) ?? fieldError("harness_config");
  const modelError = fieldError("model");
  const protocolError = protocolProblem ?? fieldError("protocol");
  const fieldRejected = ["base_url", "api_key", "context_window", "max_output_tokens", "protocol", "model", "harness_config"].some((param) => fieldError(param));
  return (
    <Modal
      open={harness !== null}
      title={t(current ? "models.form.replaceTitle" : "models.form.setTitle", { harness: name })}
      onClose={() => { if (!busy) onClose(); }}
      footer={(
        <>
          <button type="button" className="button outline" disabled={busy} onClick={onClose}>{tCommon("actions.cancel")}</button>
          <button type="submit" form={formId} className="button primary" disabled={!ready}>{busy ? t("models.form.saving") : t("models.form.save")}</button>
        </>
      )}
    >
      <form id={formId} className="form-stack" autoComplete="off" onSubmit={(event) => { event.preventDefault(); void save(); }}>
        <div className="field">
          <span className="field-label-row">
            <span>{t("models.protocol")}</span>
            <HelpTip>{t("models.form.protocolHelp")}</HelpTip>
          </span>
          <ConsoleSelect label={t("models.protocol")} placeholder={t("models.form.selectProtocol")} value={protocolProblem ? "" : protocol} options={protocolOptions} disabled={busy} onChange={(value) => {
            const option = protocolOptions.find((option) => option.value === value);
            if (option) {
              if (option.value !== protocol) clearNativeConfig();
              setProtocol(option.value); setRejection(null); setError(null);
            }
          }} />
          {protocolError ? <span className="field-error" role="alert">{protocolError}</span> : null}
        </div>
        <div className="field">
          <span className="field-label-row"><label htmlFor={`${id}-url`}>{t("models.baseUrl")}</label></span>
          <input
            id={`${id}-url`}
            value={baseUrl}
            onChange={(event) => { if (event.target.value.trim() !== baseUrl.trim()) clearNativeConfig(); setBaseUrl(event.target.value); setRejection(null); setError(null); }}
            autoComplete="off"
            spellCheck={false}
            aria-invalid={baseUrlError ? true : undefined}
            aria-describedby={baseUrlError ? `${id}-url-problem` : undefined}
          />
          {baseUrlError ? <span id={`${id}-url-problem`} className="field-error">{baseUrlError}</span> : null}
        </div>
        <div className="field">
          <span className="field-label-row">
            <label htmlFor={`${id}-key`}>{t("models.apiKey")}</label>
            <HelpTip id={`${id}-key-help`}>{t("models.form.apiKeyHelp")}</HelpTip>
          </span>
          <input id={`${id}-key`} type="password" autoComplete="off" spellCheck={false} value={apiKey} onChange={(event) => { setApiKey(event.target.value); setRejection(null); setError(null); }} aria-required="true" aria-invalid={apiKeyError ? true : undefined} aria-describedby={`${id}-key-help${apiKeyError ? ` ${id}-key-problem` : ""}`} />
          {apiKeyError ? <span id={`${id}-key-problem`} className="field-error">{apiKeyError}</span> : null}
        </div>
        <div className="field">
          <span className="field-label-row"><label htmlFor={`${id}-model`}>{t("models.model")}</label><HelpTip>{t("models.form.modelName")}</HelpTip></span>
          <input id={`${id}-model`} value={model} disabled={busy} spellCheck={false} autoComplete="off" aria-required="true" aria-invalid={Boolean(modelError)} aria-describedby={modelError ? `${id}-model-error` : undefined}
            onChange={(event) => { if (event.target.value.trim() !== model.trim()) clearNativeConfig(); setModel(event.target.value); setRejection(null); setError(null); }} />
          {modelError ? <span id={`${id}-model-error`} className="field-error" role="alert">{modelError}</span> : null}
        </div>
        <details className="system-model-advanced" open={advancedOpen} onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}>
          <summary>{t("models.form.advanced")}</summary>
          <div className="form-stack">
            {support?.accepts_harness_config ? <div className="field">
              <span className="field-label-row"><label htmlFor={`${id}-config`}>{t("models.form.harnessConfig")}</label><HelpTip>{t("models.form.configHelp", { harness: name })}</HelpTip></span>
              <textarea id={`${id}-config`} className="system-model-json" rows={7} value={configText} disabled={busy} spellCheck={false} autoComplete="off" autoCapitalize="off" aria-invalid={Boolean(configError)} aria-describedby={configError ? `${id}-config-error` : undefined}
                onBlur={() => setConfigTouched(true)} onChange={(event) => { setConfigText(event.target.value); setRejection(null); setError(null); }} />
              {configError ? <span id={`${id}-config-error`} className="field-error" role="alert">{configError}</span> : null}
              <button type="button" className="text-action system-model-format" disabled={busy || nativeConfig === null} onClick={() => setConfigText(JSON.stringify(nativeConfig, null, 2))}>{t("models.form.formatJson")}</button>
            </div> : null}
            <div className="system-model-limits">
              {limitField("context", contextWindow, setContextWindow, contextProblem)}
              {limitField("output", maxOutputTokens, setMaxOutputTokens, outputProblem)}
            </div>
          </div>
        </details>
        {error && !fieldRejected ? <p className="confirm-dialog-error" role="alert">{error}</p> : null}
      </form>
    </Modal>
  );
}
