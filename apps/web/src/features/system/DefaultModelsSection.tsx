import { AgentCoreError, type CoreHarness, type CoreHarnessKind, type ModelProviderInput } from "@agents-core-web/agents-client";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, HelpTip, Section, StatusDot } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { Modal } from "../../components/Modal";
import { TableSkeleton } from "../../components/Skeleton";
import { failedLast, useFailureToast, useToast } from "../../components/Toast";
import { useDeleteFlow } from "../../lib/delete-flow";
import { formatDateTime, formatInteger } from "../../lib/format";
import { harnessNames, protocolNames } from "../../lib/harness-labels";
import { admin } from "../../lib/projects";
import { Fact } from "./Fact";
import { harnessesQuery } from "./harness-queries";

/** A write that gets no answer in this time has an unknown outcome. */
const WRITE_TIMEOUT_MS = 30_000;

type Protocol = ModelProviderInput["protocol"];
/** Where a new provider's protocol starts: the one each harness speaks. Core validates the choice. */
const usualProtocol: Record<CoreHarnessKind, Protocol> = { codex: "responses", claude_sdk: "anthropic", mcode: "anthropic" };

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error ?? "");
}

/** Empty is omitted (undefined); anything but a whole number is a problem (null). */
function wholeNumber(value: string): number | undefined | null {
  const text = value.trim();
  if (!text) return undefined;
  return /^\d+$/u.test(text) && Number.isSafeInteger(Number(text)) ? Number(text) : null;
}

function isUrl(value: string): boolean {
  try {
    new URL(value);
    return true;
  } catch {
    return false;
  }
}

/**
 * System › Default model: each harness's deployment default model provider.
 * It applies to Core-hosted Sessions, after a provider in the request or on
 * the Agent, and is the only source for Sessions without an environment;
 * self-hosted Sessions bring their own. Whether a harness is enabled, and
 * which is the default, is Core's startup configuration and only shown here.
 * A write replaces the whole provider and needs the API key every time; the
 * key lives only in the open form's state, never in the query cache, storage
 * or the URL. Writes are never retried automatically; after each one the
 * harnesses are read again.
 */
export function DefaultModelsSection() {
  const { t } = useTranslation("system");
  const toast = useToast();
  const queryClient = useQueryClient();
  const query = useQuery(harnessesQuery);
  const harnesses = query.data?.data ?? null;
  const reread = useCallback(() => { void queryClient.invalidateQueries({ queryKey: harnessesQuery.queryKey }); }, [queryClient]);
  useFailureToast(harnesses && failedLast(query) ? message(query.error) : null, t("models.refreshFailed"), "harnesses-read");

  const [editing, setEditing] = useState<CoreHarness | null>(null);
  const clear = useDeleteFlow<CoreHarnessKind>(
    async (harness) => {
      await admin.deleteHarnessModelProvider(harness, { signal: AbortSignal.timeout(WRITE_TIMEOUT_MS) });
      toast.show(t("models.cleared", { harness: harnessNames[harness] }), { tone: "success" });
    },
    reread,
    { uncertain: t("models.clearDialog.uncertain") },
  );

  let body: ReactNode;
  if (!harnesses) {
    body = query.isError && !query.isFetching
      ? <ErrorState title={t("models.loadFailed")} description={message(query.error)} onRetry={() => { void query.refetch(); }} />
      : <TableSkeleton rows={3} columns={3} />;
  } else if (!harnesses.length) {
    body = <EmptyState title={t("models.none")} />;
  } else {
    body = (
      <div className="system-models">
        {harnesses.map((harness) => (
          <HarnessCard key={harness.id} harness={harness} busy={clear.busy} onEdit={() => setEditing(harness)} onClear={() => clear.ask(harness.id)} />
        ))}
      </div>
    );
  }

  return (
    <Section headingId="system-models-heading" title={t("models.title")} help={t("models.help")}>
      {body}
      <ModelProviderDialog
        // A new form for every opening: closing it drops whatever was typed, the key included.
        key={editing?.id ?? "closed"}
        harness={editing}
        onClose={() => setEditing(null)}
        onReread={reread}
        onSaved={(harness) => {
          setEditing(null);
          reread();
          toast.show(t("models.saved", { harness: harnessNames[harness] }), { tone: "success" });
        }}
      />
      <ConfirmDialog
        open={clear.target !== null}
        title={t("models.clearDialog.title")}
        confirmLabel={t("models.clearDialog.confirm")}
        busyLabel={t("models.clearDialog.busy")}
        busy={clear.busy}
        error={clear.error}
        onConfirm={() => void clear.confirm()}
        onClose={clear.cancel}
      >
        {clear.target ? (
          <>
            <p>{t("models.clearDialog.prompt", { harness: harnessNames[clear.target] })}</p>
            <p>{t("models.clearDialog.consequence")}</p>
          </>
        ) : null}
      </ConfirmDialog>
    </Section>
  );
}

function HarnessCard({ harness, busy, onEdit, onClear }: { harness: CoreHarness; busy: boolean; onEdit: () => void; onClear: () => void }) {
  const { t, i18n } = useTranslation("system");
  const locale = i18n.resolvedLanguage;
  const headingId = useId();
  const name = harnessNames[harness.id];
  const provider = harness.model_provider;
  return (
    <article className="system-model" aria-labelledby={headingId}>
      <header className="system-model-header">
        <h3 id={headingId}>{name}</h3>
        <div className="system-model-actions">
          {/* A disabled harness may still be configured; Core keeps the provider until it is enabled. */}
          <button className="button outline" type="button" aria-label={t(provider ? "models.replaceLabel" : "models.setLabel", { harness: name })} disabled={busy} onClick={onEdit}>
            {provider ? t("models.replace") : t("models.set")}
          </button>
          {provider ? (
            <button className="button outline" type="button" aria-label={t("models.clearLabel", { harness: name })} disabled={busy} onClick={onClear}>
              {t("models.clear")}
            </button>
          ) : null}
        </div>
      </header>
      <dl className="system-model-facts">
        <Fact label={t("models.harness")} help={t("models.startupHelp")}>
          <span className="system-model-state">
            <StatusDot tone={harness.enabled ? "ok" : "neutral"} label={harness.enabled ? t("models.enabled") : t("models.disabled")} />
            {harness.default ? <span className="pill">{t("models.default")}</span> : null}
          </span>
        </Fact>
        {provider ? <>
          <Fact label={t("models.protocol")}>{protocolNames[provider.protocol]}</Fact>
          <Fact label={t("models.baseUrl")}><code className="system-code">{provider.base_url}</code></Fact>
          <Fact label={t("models.apiKey")}>{provider.api_key_configured ? t("models.keyConfigured") : t("models.keyNotConfigured")}</Fact>
          {provider.context_window !== undefined ? <Fact label={t("models.contextWindow")}>{formatInteger(provider.context_window, locale)}</Fact> : null}
          {provider.max_output_tokens !== undefined ? <Fact label={t("models.maxOutputTokens")}>{formatInteger(provider.max_output_tokens, locale)}</Fact> : null}
          <Fact label={t("models.updated")}>{formatDateTime(Math.floor(Date.parse(provider.updated_at) / 1000), locale)}</Fact>
        </> : (
          <Fact label={t("models.provider")}><span className="system-muted">{t("models.notSet")}</span></Fact>
        )}
      </dl>
    </article>
  );
}

/**
 * Sets or replaces one harness's provider. Non-secret fields start from the
 * current provider; the API key never does. The form checks only that the
 * base URL is a URL and the limits are whole numbers; Core's other rules come
 * back as its 400 message, shown beside the form.
 */
function ModelProviderDialog({ harness, onClose, onSaved, onReread }: {
  harness: CoreHarness | null;
  onClose: () => void;
  onSaved: (harness: CoreHarnessKind) => void;
  onReread: () => void;
}) {
  const { t } = useTranslation("system");
  const { t: tCommon } = useTranslation();
  const id = useId();
  const current = harness?.model_provider ?? null;
  const [protocol, setProtocol] = useState<Protocol>(current?.protocol ?? (harness ? usualProtocol[harness.id] : "responses"));
  const [baseUrl, setBaseUrl] = useState(current?.base_url ?? "");
  const [apiKey, setApiKey] = useState("");
  const [contextWindow, setContextWindow] = useState(current?.context_window === undefined ? "" : String(current.context_window));
  const [maxOutputTokens, setMaxOutputTokens] = useState(current?.max_output_tokens === undefined ? "" : String(current.max_output_tokens));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const name = harness ? harnessNames[harness.id] : "";
  const limitsRequired = harness?.id === "mcode";
  const url = baseUrl.trim();
  const urlProblem = url && !isUrl(url) ? t("models.form.baseUrlInvalid") : null;
  const context = wholeNumber(contextWindow);
  const output = wholeNumber(maxOutputTokens);
  const ready = harness !== null && !busy && url !== "" && !urlProblem && apiKey.trim() !== "" && context !== null && output !== null;

  async function save() {
    if (!ready || !harness) return;
    setBusy(true);
    setError(null);
    try {
      await admin.setHarnessModelProvider(harness.id, {
        protocol, base_url: url, api_key: apiKey.trim(),
        ...(context === undefined ? {} : { context_window: context }),
        ...(output === undefined ? {} : { max_output_tokens: output }),
      }, { signal: AbortSignal.timeout(WRITE_TIMEOUT_MS) });
      onSaved(harness.id);
    } catch (caught) {
      // Never retried: a rejection shows Core's reason; an unknown outcome is read again first.
      if (caught instanceof AgentCoreError && caught.status >= 400 && caught.status < 500 && caught.status !== 408) {
        setError(caught.message);
      } else if (caught instanceof AgentCoreError && caught.code === "credential_storage_unavailable") {
        // A deployment without a credential key stores nothing: a configuration error, not an unknown outcome.
        setError(t("models.form.noCredentialKey"));
      } else {
        setError(t("models.form.uncertain"));
        onReread();
      }
    } finally {
      setBusy(false);
    }
  }

  const limitField = (field: "context" | "output", value: string, setValue: (value: string) => void, parsed: number | undefined | null) => (
    <div className="field">
      <span className="field-label-row">
        <label htmlFor={`${id}-${field}`}>{t(field === "context" ? "models.contextWindow" : "models.maxOutputTokens")}</label>
        <HelpTip>{t(`models.form.${field}Help${limitsRequired ? "Required" : ""}`)}</HelpTip>
      </span>
      <input id={`${id}-${field}`} inputMode="numeric" autoComplete="off" value={value} onChange={(event) => setValue(event.target.value)} aria-required={limitsRequired} aria-invalid={parsed === null} />
      {parsed === null ? <span className="field-error">{t("models.form.wholeNumber")}</span> : null}
    </div>
  );

  return (
    <Modal
      open={harness !== null}
      title={t(current ? "models.form.replaceTitle" : "models.form.setTitle", { harness: name })}
      onClose={() => { if (!busy) onClose(); }}
      footer={(
        <>
          <button type="button" className="button outline" disabled={busy} onClick={onClose}>{tCommon("actions.cancel")}</button>
          <button type="button" className="button primary" disabled={!ready} onClick={() => void save()}>{busy ? t("models.form.saving") : t("models.form.save")}</button>
        </>
      )}
    >
      <form className="form-stack" autoComplete="off" onSubmit={(event) => { event.preventDefault(); void save(); }}>
        <label className="field" htmlFor={`${id}-protocol`}>
          <span>{t("models.protocol")}</span>
          <select id={`${id}-protocol`} value={protocol} onChange={(event) => setProtocol(event.target.value === "anthropic" ? "anthropic" : "responses")}>
            {(["anthropic", "responses"] as const).map((value) => <option key={value} value={value}>{protocolNames[value]}</option>)}
          </select>
        </label>
        <label className="field" htmlFor={`${id}-url`}>
          <span>{t("models.baseUrl")}</span>
          <input id={`${id}-url`} value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} autoComplete="off" spellCheck={false} aria-invalid={Boolean(urlProblem)} />
          {urlProblem ? <span className="field-error">{urlProblem}</span> : null}
        </label>
        <div className="field">
          <span className="field-label-row">
            <label htmlFor={`${id}-key`}>{t("models.apiKey")}</label>
            <HelpTip>{t("models.form.apiKeyHelp")}</HelpTip>
          </span>
          <input id={`${id}-key`} type="password" autoComplete="off" spellCheck={false} value={apiKey} onChange={(event) => setApiKey(event.target.value)} aria-required="true" />
        </div>
        <div className="system-model-limits">
          {limitField("context", contextWindow, setContextWindow, context)}
          {limitField("output", maxOutputTokens, setMaxOutputTokens, output)}
        </div>
        {error ? <p className="confirm-dialog-error" role="alert">{error}</p> : null}
      </form>
    </Modal>
  );
}
