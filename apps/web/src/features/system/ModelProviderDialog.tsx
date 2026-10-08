import { AgentCoreError, type CoreHarness, type CoreHarnessKind, type ModelProviderInput } from "@oac/agents-client";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ConsoleSelect } from "../../components/console-select";
import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { ModelCombobox, type ModelOption } from "../../components/model-combobox";
import { coreError, coreFieldError } from "../../lib/core-error";
import { harnessNames, protocolNames } from "../../lib/harness-labels";
import { isOrcarouterBase, orcarouterInferenceBase, orcarouterProviderLabel, type OrcarouterConfiguration, type OrcarouterCredential, type OrcarouterCredentialSource } from "../../lib/orcarouter";
import { createCredentialAttempts } from "../../lib/orcarouter-attempts";
import { readOrcarouterCatalog, type OrcarouterCatalog } from "../../lib/orcarouter-catalog";
import { apiKeyAdapter, credentialFailure, pkceAdapter, readOrcarouterConfiguration } from "../../lib/orcarouter-credential";
import { startOrcarouterLogin, type OrcarouterLogin } from "../../lib/orcarouter-pkce";
import { admin } from "../../lib/projects";

/** A write that gets no answer in this time has an unknown outcome. */
const WRITE_TIMEOUT_MS = 30_000;

type Protocol = ModelProviderInput["protocol"];
/** Core stores both token limits as 32-bit integers. */
const INT32_MAX = 2_147_483_647;
/** The console's only provider beyond the operator's own OpenAI-compatible endpoint. */
const orcarouterPreset = "orcarouter";
const genericPreset = "openai_compatible";

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
 *
 * Two named presets are offered. `OrcaRouter` is a first-class provider: its
 * inference base is the gateway's own and is never typed by hand, and the
 * default model is chosen from the workspace's live catalog instead of being
 * entered. Both credential entrances — a pasted API key and `Connect with
 * OrcaRouter` — write the same ordinary key into the same field.
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
  // A saved OrcaRouter configuration is recognised again rather than silently
  // reading as a generic endpoint.
  const [preset, setPreset] = useState<string>(current !== null && isOrcarouterBase(current.base_url) ? orcarouterPreset : genericPreset);
  const [protocol, setProtocol] = useState<Protocol | "">(current?.protocol ?? support?.protocols[0] ?? "");
  const [baseUrl, setBaseUrl] = useState(current?.base_url ?? "");
  const [apiKey, setApiKey] = useState("");
  const [credentialSource, setCredentialSource] = useState<OrcarouterCredentialSource>("api_key");
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

  const isOrcaRouter = preset === orcarouterPreset;

  // The catalog of the operator's own workspace. Its options are the only models
  // this form can save while OrcaRouter is selected.
  const [orcaConfiguration, setConfiguration] = useState<OrcarouterConfiguration | null>(null);
  const [catalog, setCatalog] = useState<OrcarouterCatalog | null>(null);
  const [catalogBusy, setCatalogBusy] = useState(false);
  const [catalogReason, setCatalogReason] = useState<"unavailable" | "refused" | null>(null);
  const requests = useRef(0);

  // One credential seam, two adapters: the pasted key and the PKCE result land
  // in the same field, and nothing downstream knows which one produced it.
  const attempts = useRef(createCredentialAttempts((view) => { setConnectBusy(view.busy); setConnectHint(view.hint); }));
  const [connectBusy, setConnectBusy] = useState(false);
  const [connectHint, setConnectHint] = useState<string | null>(null);
  const [login, setLogin] = useState<OrcarouterLogin | null>(null);
  const [code, setCode] = useState("");
  const catalogTimer = useRef<number | null>(null);

  useEffect(() => {
    if (!isOrcaRouter) return;
    void readOrcarouterConfiguration().then(setConfiguration);
  }, [isOrcaRouter]);

  // Leaving the page releases the login synchronously; the controller aborts the
  // exchange too, so no answer can land after the page is gone and a later
  // sign-in needs no remount.
  useEffect(() => {
    const leave = () => { attempts.current.pagehide(); };
    window.addEventListener("pagehide", leave);
    return () => {
      window.removeEventListener("pagehide", leave);
      // A pending catalog read is the operator's own key on its way out of the form.
      if (catalogTimer.current !== null) window.clearTimeout(catalogTimer.current);
      attempts.current.abandon();
    };
  }, []);

  const inferenceBase = orcaConfiguration?.inferenceBase ?? orcarouterInferenceBase();

  /**
   * Reads the catalog for the selected provider. The selection is re-validated
   * against what comes back: a model the current entrance no longer admits is
   * cleared rather than kept as a value the provider would reject.
   */
  async function loadCatalog(key: string, source: OrcarouterCredentialSource) {
    if (key.trim() === "") { setCatalog(null); setCatalogReason(null); return; }
    const ticket = requests.current + 1;
    requests.current = ticket;
    setCatalogBusy(true);
    try {
      const answer = await readOrcarouterCatalog({ selector: { capability: "chat" }, key });
      if (requests.current !== ticket) return;
      setCatalog(answer);
      setCatalogReason(answer.unauthorized ? "refused" : answer.degraded ? "unavailable" : null);
      if (answer.unauthorized) { setError(t("models.orcarouter.keyRefused")); return; }
      setCredentialSource(source);
      if (model !== "" && !answer.models.some((entry) => entry.id === model)) {
        setModel("");
        setError(t("models.orcarouter.modelInvalid"));
      }
    } finally {
      if (requests.current === ticket) setCatalogBusy(false);
    }
  }

  async function connect() {
    const attempt = attempts.current.begin();
    try {
      const configuration = orcaConfiguration ?? (await readOrcarouterConfiguration(attempt.signal));
      const started = await startOrcarouterLogin(configuration);
      if (!attempts.current.settle(attempt, { ok: true })) return;
      setConfiguration(configuration);
      setLogin(started);
      window.open(started.authorizeUrl, "_blank", "noopener,noreferrer");
    } catch (caught) {
      attempts.current.settle(attempt, { ok: false, message: messageOf(caught) });
    }
  }

  async function submitCode() {
    if (!login) return;
    const attempt = attempts.current.begin();
    try {
      const credential = await pkceAdapter({ code, login, state: login.state, signal: attempt.signal }).obtain();
      attempts.current.settle(attempt, { ok: true });
      setApiKey(credential.key);
      setCode("");
      setLogin(null);
      await loadCatalog(credential.key, credential.source);
    } catch (caught) {
      // The pasted code is refused, reused or expired: say what the operator does
      // next and keep the login open so the same screen can take another code.
      attempts.current.settle(attempt, { ok: false, message: messageOf(caught) });
    }
  }

  async function save() {
    if (!ready || !harness || !protocol || !nativeConfig || saving.current) return;
    saving.current = true;
    setBusy(true);
    setError(null); setRejection(null);
    try {
      // Both entrances go through the same adapter seam before anything is written.
      const adapter = isOrcaRouter && credentialSource === "api_key" ? apiKeyAdapter(apiKey) : null;
      const credential = adapter === null ? null : await adapter.obtain();
      const key = credential?.key ?? apiKey.trim();
      await admin.setHarnessModelConfiguration(harness.id, {
        model: model.trim(), harness_config: nativeConfig,
        model_provider: { protocol, base_url: isOrcaRouter ? inferenceBase : url, api_key: key,
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
      } else {
        setError(t("models.form.uncertain"));
        onReread();
      }
    } finally {
      saving.current = false;
      setBusy(false);
    }
  }

  /** One failure vocabulary for both entrances, in the operator's language. */
  function messageOf(cause: unknown): string {
    return t(`models.orcarouter.connectFailure.${credentialFailure(cause)}`);
  }

  const name = harness ? harnessNames[harness.id] : "";
  const protocolProblem = !protocol ? t("models.form.protocolUnavailable") : !support?.protocols.includes(protocol) ? t("models.form.protocolUnsupported", { protocol: protocolNames[protocol] }) : null;
  const configTooLarge = nativeConfig !== null && new TextEncoder().encode(JSON.stringify(nativeConfig)).length > 16 * 1024;
  const url = baseUrl.trim();
  const urlProblem = !isOrcaRouter && url && !isProviderUrl(url) ? t("models.form.baseUrlInvalid") : null;
  const context = tokenLimit(contextWindow);
  const output = tokenLimit(maxOutputTokens);
  const limitProblem = (limit: ReturnType<typeof tokenLimit>) => (limit.problem === "whole" ? t("models.form.wholeNumber") : limit.problem === "large" ? t("models.form.tooLarge") : null);
  const outputProblem = limitProblem(output);
  // The missing or undersized window is the field the administrator must fix.
  const contextProblem = limitProblem(context) ?? (!outputProblem && output.value !== undefined && output.value > (context.value ?? 0) ? t("models.form.needsContext") : null);
  const options: ModelOption[] = useMemo(() => (catalog?.models ?? []).map((entry) => ({ value: entry.id, label: `${entry.name} · ${entry.id}` })), [catalog]);
  // The model control is a list from the catalog while OrcaRouter is selected;
  // the operator never types an identifier the workspace's plan may not serve.
  const modelReady = isOrcaRouter ? options.some((option) => option.value === model) : model.trim() !== "";
  const ready = harness !== null && !busy && (isOrcaRouter || url !== "") && !urlProblem && apiKey.trim() !== "" && modelReady && nativeConfig !== null && !configTooLarge && !protocolProblem && (!limitsRequired || Boolean(context.value && output.value)) && !contextProblem && !outputProblem;

  function clearNativeConfig() {
    setConfigText("{}");
    setConfigTouched(false);
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
          <span className="field-label-row"><label htmlFor={`${id}-preset`}>{t("models.orcarouter.preset")}</label><HelpTip>{t("models.orcarouter.presetHelp")}</HelpTip></span>
          <ConsoleSelect
            label={t("models.orcarouter.preset")}
            value={preset}
            disabled={busy}
            options={[{ value: orcarouterPreset, label: orcarouterProviderLabel }, { value: genericPreset, label: t("models.orcarouter.presetGeneric") }]}
            onChange={(value) => {
              setPreset(value);
              setRejection(null); setError(null);
              setCatalog(null); setCatalogReason(null);
              attempts.current.abandon();
              setLogin(null); setCode("");
              if (value === orcarouterPreset) { setBaseUrl(""); clearNativeConfig(); } else if (isOrcaRouter) { setBaseUrl(""); setModel(""); }
              setApiKey("");
            }}
          />
        </div>
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
        {isOrcaRouter ? (
          <div className="field">
            <span className="field-label-row"><span>{t("models.baseUrl")}</span><HelpTip>{t("models.orcarouter.baseUrlHelp")}</HelpTip></span>
            {/* A value, not a control: the gateway's own inference base. */}
            <p className="system-model-protocol" data-orcarouter-inference-base="true">{inferenceBase}</p>
          </div>
        ) : (
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
        )}
        <div className="field">
          <span className="field-label-row">
            <label htmlFor={`${id}-key`}>{isOrcaRouter ? t("models.orcarouter.apiKeyLabel") : t("models.apiKey")}</label>
            <HelpTip id={`${id}-key-help`}>{isOrcaRouter ? t("models.orcarouter.apiKeyHelp") : t("models.form.apiKeyHelp")}</HelpTip>
          </span>
          <input id={`${id}-key`} type="password" autoComplete="off" spellCheck={false} value={apiKey} placeholder={isOrcaRouter ? "sk-orca-…" : undefined} onChange={(event) => {
            const value = event.target.value;
            setApiKey(value); setCredentialSource("api_key"); setRejection(null); setError(null);
            // A pasted key is one of the two entrances; reading the catalog as soon
            // as the field settles keeps both entrances on one credential seam and
            // one model list.
            if (isOrcaRouter) {
              if (catalogTimer.current !== null) window.clearTimeout(catalogTimer.current);
              catalogTimer.current = window.setTimeout(() => { const pending = value.trim(); if (pending !== "") void loadCatalog(pending, "api_key"); }, 400);
            }
          }} aria-required="true" aria-invalid={apiKeyError ? true : undefined} aria-describedby={`${id}-key-help${apiKeyError ? ` ${id}-key-problem` : ""}`} />
          {apiKeyError ? <span id={`${id}-key-problem`} className="field-error">{apiKeyError}</span> : null}
          {isOrcaRouter ? (
            // The second entrance sits beside the first: an operator without a key
            // connects, an operator with one pastes it, and both fill this field.
            <div className="orcarouter-connect" data-orcarouter-connect="true">
              <div className="orcarouter-connect-actions">
                <button type="button" className="button outline" disabled={busy || connectBusy} onClick={() => void connect()}>
                  {connectBusy ? t("models.orcarouter.connecting") : t("models.orcarouter.connect")}
                </button>
                {login ? (
                  <>
                    <label className="orcarouter-connect-code" htmlFor={`${id}-code`}>
                      <span>{t("models.orcarouter.codeLabel")}</span>
                      <input id={`${id}-code`} value={code} autoComplete="off" spellCheck={false} onChange={(event) => { setCode(event.target.value); setConnectHint(null); }} />
                    </label>
                    <button type="button" className="button primary" disabled={busy || connectBusy || code.trim() === ""} onClick={() => void submitCode()}>{t("models.orcarouter.submitCode")}</button>
                    <button type="button" className="text-action" disabled={busy} onClick={() => { attempts.current.abandon(); setLogin(null); setCode(""); }}>{tCommon("actions.cancel")}</button>
                    <a className="text-action" href={login.authorizeUrl} target="_blank" rel="noopener noreferrer">{t("models.orcarouter.reopen")}</a>
                  </>
                ) : null}
              </div>
              <p className="orcarouter-connect-hint" role={connectHint === null ? "status" : "alert"} data-orcarouter-credential-source={credentialSource}>
                {connectHint === null ? t(`models.orcarouter.hint.${credentialSource}`) : connectHint}
              </p>
              <p className="orcarouter-connect-help">{t("models.orcarouter.keyConsoleHelp")} <a href={orcaConfiguration?.keyConsole ?? "https://www.orcarouter.ai/console/authorized-apps"} target="_blank" rel="noopener noreferrer">{t("models.orcarouter.keyConsole")}</a></p>
            </div>
          ) : null}
        </div>
        <div className="field">
          <span className="field-label-row"><label htmlFor={`${id}-model`}>{t("models.model")}</label><HelpTip>{isOrcaRouter ? t("models.orcarouter.modelHelp") : t("models.form.modelName")}</HelpTip></span>
          {isOrcaRouter ? (
            <ModelCombobox
              value={model}
              onChange={(value) => { if (value.trim() !== model.trim()) clearNativeConfig(); setModel(value); setRejection(null); setError(null); }}
              options={options}
              label={t("models.model")}
              placeholder={t("models.orcarouter.modelPlaceholder")}
              disabled={busy || catalogBusy || apiKey.trim() === ""}
              empty={t("models.orcarouter.modelEmpty")}
            />
          ) : (
            <input id={`${id}-model`} value={model} disabled={busy} spellCheck={false} autoComplete="off" aria-required="true" aria-invalid={Boolean(modelError)} aria-describedby={modelError ? `${id}-model-error` : undefined}
              onChange={(event) => { if (event.target.value.trim() !== model.trim()) clearNativeConfig(); setModel(event.target.value); setRejection(null); setError(null); }} />
          )}
          {isOrcaRouter ? (
            <>
              <span className="orcarouter-catalog-state" role="status" data-orcarouter-catalog={catalogReason ?? (catalog === null ? "idle" : "live")} data-orcarouter-catalog-source={catalog?.origin ?? ""}>
                {catalogBusy ? t("models.orcarouter.catalogLoading") : catalogReason === "refused" ? t("models.orcarouter.catalogRefused") : catalogReason === "unavailable" ? t("models.orcarouter.catalogFallback") : catalog === null ? t("models.orcarouter.catalogIdle") : t("models.orcarouter.catalogLive", { count: options.length })}
              </span>
              {catalog !== null && !catalogBusy && apiKey.trim() !== "" ? (
                <button type="button" className="text-action" disabled={busy} onClick={() => void loadCatalog(apiKey, credentialSource)}>{t("models.orcarouter.catalogRefresh")}</button>
              ) : null}
            </>
          ) : null}
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
