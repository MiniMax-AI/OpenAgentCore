import { ArrowRight, Check, CircleAlert, Copy, Send } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import {
  AgentCoreError,
  createIdempotencyKey,
  type AgentCore,
  type AgentSession,
  type CoreHarnessKind,
  type CreateAgentInput,
  type CreateSessionInput,
  type SavedAgent,
  type SessionMessageInputEvent,
} from "@agents-core-web/agents-client";

import { PageBody, PageHeader, SegmentedControl, StatusDot } from "../../components/console-ui";
import { formatClock, formatDuration } from "../../lib/format";
import { buildModelOptionGroups } from "../../lib/model-options";
import type { EnvironmentTemplateCatalog } from "../sessions/environment/environment-templates";
import type { VaultCatalog } from "../vaults/vault-catalog";
import { AgentFields, LookupFields, MessageFields, SessionFields } from "./WorkbenchFields";
import {
  buildWorkbenchRequest,
  emptyWorkbenchForm,
  MISSING_VALUE_PROBLEMS,
  workbenchCurl,
  workbenchJson,
  type LookupKind,
  type WorkbenchContext,
  type WorkbenchForm,
  type WorkbenchKind,
  type WorkbenchRequest,
} from "./workbench-requests";
import "./workbench.css";

export type WorkbenchCore = Pick<
  AgentCore,
  | "createAgent" | "createSession" | "submitEvents"
  | "retrieveAgent" | "retrieveSession" | "retrieveTurn" | "retrieveSessionExecutionConfiguration"
  | "retrieveEnvironment" | "retrieveEnvironmentTemplate" | "retrieveVault" | "retrieveVaultCredential"
  | "retrieveSkill" | "retrieveSourceFile"
>;

interface Timing {
  /** Wall clock when the request started, in epoch milliseconds. */
  at: number;
  /** Round trip in seconds. */
  seconds: number;
}

type Outcome =
  | (Timing & { kind: "ok"; request: WorkbenchKind; status: number; value: unknown; sessionId: string | null; agent: SavedAgent | null })
  | (Timing & { kind: "rejected"; status: number; message: string; error: Record<string, string>; uncertain: boolean })
  | (Timing & { kind: "unknown"; message: string });

type Format = "curl" | "json";

const KINDS: readonly WorkbenchKind[] = ["agent.create", "session.create", "session.message", "object.retrieve"];

/** Standard reason phrases, so a status reads the same in every language. */
const REASONS: Record<number, string> = {
  200: "OK", 201: "Created", 202: "Accepted", 400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found",
  405: "Method Not Allowed", 409: "Conflict", 413: "Payload Too Large", 415: "Unsupported Media Type", 422: "Unprocessable Entity",
  429: "Too Many Requests", 500: "Internal Server Error", 502: "Bad Gateway", 503: "Service Unavailable", 504: "Gateway Timeout",
};
const statusLabel = (status: number) => `${status} ${REASONS[status] ?? ""}`.trim();

/**
 * Playground › API workbench: a fixed-format request builder. The form on the
 * left produces the exact request on the right; copy it, or send it through
 * the console connection and read the response below it.
 */
export function ApiWorkbenchView({
  core,
  baseUrl,
  agents,
  sessions,
  harnesses,
  defaultHarness,
  vaultCatalog = null,
  environmentTemplates = null,
  onOpenSession,
  onChanged,
}: {
  core: WorkbenchCore;
  baseUrl: string;
  agents: readonly SavedAgent[];
  sessions: readonly AgentSession[];
  harnesses: readonly CoreHarnessKind[];
  defaultHarness?: CoreHarnessKind;
  /** The complete Vault catalog, or null while it is not loaded. */
  vaultCatalog?: VaultCatalog | null;
  environmentTemplates?: EnvironmentTemplateCatalog | null;
  onOpenSession: (sessionId: string) => void;
  /** A write succeeded; collections may refresh. */
  onChanged: () => void;
}) {
  const { t, i18n } = useTranslation("workbench");
  const locale = i18n.resolvedLanguage;
  const models = useMemo(() => {
    const groups = buildModelOptionGroups(agents.map((agent) => agent.model), import.meta.env.VITE_AGENT_MODEL_PRESETS, import.meta.env.VITE_AGENT_DEFAULT_MODEL);
    return { defaultModel: groups.defaultModel, all: [...groups.configured, ...groups.previouslyUsed] };
  }, [agents]);
  const [kind, setKind] = useState<WorkbenchKind>("agent.create");
  const [form, setForm] = useState<WorkbenchForm>(() => emptyWorkbenchForm(models.defaultModel));
  const [format, setFormat] = useState<Format>("curl");
  // One key per request instance: it only changes once an outcome is known, so
  // sending again after an unconfirmed attempt cannot create a duplicate.
  const [idempotencyKey, setIdempotencyKey] = useState(createIdempotencyKey);
  const [sending, setSending] = useState(false);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  // Agents created here are usable before the collection refresh lands.
  const [createdAgents, setCreatedAgents] = useState<SavedAgent[]>([]);
  const live = useRef(true);
  useEffect(() => () => { live.current = false; }, []);
  useEffect(() => { setOutcome(null); }, [kind]);

  const knownAgents = useMemo(
    () => [...agents, ...createdAgents.filter((created) => !agents.some((agent) => agent.id === created.id))],
    [agents, createdAgents],
  );
  const templates = environmentTemplates?.state === "ready" ? environmentTemplates.templates : null;
  const context = useMemo<WorkbenchContext>(() => ({
    agents: knownAgents,
    vaultCatalog,
    templates: templates ?? [],
    environments: { self_hosted: __AGENTS_CORE_WEB_SELF_HOSTED_SESSIONS__, openai_hosted: __AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__ },
  }), [knownAgents, templates, vaultCatalog]);
  const recentSessions = useMemo(() => [...sessions].sort((left, right) => right.last_active_at - left.last_active_at).slice(0, 50), [sessions]);

  const request = useMemo(() => buildWorkbenchRequest(kind, form, idempotencyKey, context), [kind, form, idempotencyKey, context]);
  const json = workbenchJson(request);
  const curl = workbenchCurl(request, baseUrl);
  const shown = format === "json" && json !== null ? "json" : "curl";
  const code = shown === "json" ? json ?? "" : curl;
  const problem = request.problem;
  const problemText = problem ? problem.message ?? t(`problems.${problem.code}`) : null;

  const update = <K extends keyof WorkbenchForm>(section: K) => (patch: Partial<WorkbenchForm[K]>) =>
    setForm((current) => ({ ...current, [section]: { ...current[section], ...patch } }));

  const reset = () => {
    const empty = emptyWorkbenchForm(models.defaultModel);
    const section = ({ "agent.create": "agent", "session.create": "session", "session.message": "message", "object.retrieve": "lookup" } as const)[kind];
    setForm((current) => ({ ...current, [section]: empty[section] }));
    setOutcome(null);
  };

  async function copy(id: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      if (!live.current) return;
      setCopied(id);
      window.setTimeout(() => { if (live.current) setCopied((value) => (value === id ? null : value)); }, 1600);
    } catch {
      if (live.current) setCopied(null);
    }
  }

  async function send() {
    if (request.problem || sending) return;
    setSending(true);
    const at = Date.now();
    const started = performance.now();
    const elapsed = () => (performance.now() - started) / 1000;
    try {
      const result = await execute(core, kind, form, request);
      if (!live.current) return;
      setOutcome({ kind: "ok", request: kind, ...result, at, seconds: elapsed() });
      if (result.agent) setCreatedAgents((current) => [...current.filter((agent) => agent.id !== result.agent!.id), result.agent!]);
      setIdempotencyKey(createIdempotencyKey());
      if (kind !== "object.retrieve") onChanged();
    } catch (error) {
      if (!live.current) return;
      const seconds = elapsed();
      if (error instanceof AgentCoreError) {
        // A 4xx is a definite rejection; a 5xx write may or may not have happened,
        // so its Idempotency-Key is kept for a safe resend.
        const definite = error.status >= 400 && error.status < 500;
        const envelope = Object.fromEntries(Object.entries({ message: error.message, type: error.errorType, code: error.code, param: error.param })
          .filter((entry): entry is [string, string] => typeof entry[1] === "string"));
        setOutcome({ kind: "rejected", status: error.status, message: error.message, error: envelope, uncertain: !definite && kind !== "object.retrieve", at, seconds });
        if (definite || kind === "object.retrieve") setIdempotencyKey(createIdempotencyKey());
      } else {
        setOutcome({ kind: "unknown", message: error instanceof Error ? error.message : String(error), at, seconds });
      }
    } finally {
      if (live.current) setSending(false);
    }
  }

  const continueWith = (next: WorkbenchKind, patch: () => void) => { patch(); setKind(next); };

  return (
    <section className="page-section console-page workbench-page" aria-labelledby="workbench-heading">
      <PageHeader headingId="workbench-heading" title={t("title")} help={t("help")} />
      <PageBody>
        <div className="wb">
          <section className="wb-card wb-form" aria-labelledby="workbench-form-heading">
            <header className="wb-card-head">
              <h2 id="workbench-form-heading" className="visually-hidden">{t("kindLabel")}</h2>
              <SegmentedControl
                label={t("kindLabel")}
                value={kind}
                options={KINDS.map((value) => ({ value, label: t(`kinds.${value}`) }))}
                onChange={(next) => { if (!sending) setKind(next); }}
              />
              <button className="button ghost wb-reset" type="button" onClick={reset} disabled={sending} aria-label={t("resetLabel")} title={t("resetLabel")}>{t("reset")}</button>
            </header>
            <fieldset className="wb-fields" disabled={sending}>
              {kind === "agent.create" ? (
                <AgentFields value={form.agent} onChange={update("agent")} harnesses={harnesses} defaultHarness={defaultHarness} models={models.all} vaultCatalog={vaultCatalog} />
              ) : kind === "session.create" ? (
                <SessionFields value={form.session} onChange={update("session")} agents={knownAgents} vaultCatalog={vaultCatalog} templates={templates} environments={context.environments} />
              ) : kind === "session.message" ? (
                <MessageFields value={form.message} onChange={update("message")} sessions={recentSessions} />
              ) : (
                <LookupFields value={form.lookup} onChange={update("lookup")} />
              )}
            </fieldset>
          </section>

          <div className="wb-side">
            <section className="wb-card wb-request" aria-labelledby="workbench-request-heading">
              <header className="wb-card-head">
                <h2 id="workbench-request-heading" className="wb-endpoint">
                  <span className="visually-hidden">{t("request.title")}: </span>
                  <span className="wb-method">{request.method}</span>
                  <code>/v1{request.path}</code>
                </h2>
                {json !== null ? (
                  <SegmentedControl<Format>
                    label={t("request.format")}
                    value={shown}
                    options={[{ value: "curl", label: "cURL" }, { value: "json", label: "JSON" }]}
                    onChange={setFormat}
                  />
                ) : null}
                <CopyButton copied={copied === "request"} label={t("request.copy", { name: shown === "json" ? "JSON" : "cURL" })} onCopy={() => void copy("request", code)} />
              </header>
              <CodeBlock label={shown === "json" ? "JSON" : "cURL"} text={code} />
              <footer className="wb-card-foot">
                {problemText ? (
                  <p className={problem && MISSING_VALUE_PROBLEMS.has(problem.code) ? "wb-problem" : "wb-problem wb-problem-invalid"} role="status">
                    <CircleAlert size={14} strokeWidth={1.7} aria-hidden="true" />
                    <span>{problemText}</span>
                  </p>
                ) : <span />}
                <button className="button primary" type="button" onClick={() => void send()} disabled={Boolean(problem) || sending}>
                  <Send size={14} strokeWidth={1.7} aria-hidden="true" />{t(sending ? "request.sending" : "request.send")}
                </button>
              </footer>
            </section>

            <section className="wb-card wb-response" aria-labelledby="workbench-response-heading" aria-busy={sending}>
              <header className="wb-card-head">
                <h2 id="workbench-response-heading">{t("response.title")}</h2>
                {/* Only the status line is announced, not the whole body. */}
                <div className="wb-status" role="status">
                  {sending ? <StatusDot tone="pending" label={t("response.waiting")} />
                    : !outcome ? <span className="wb-muted">{t("response.idle")}</span>
                      : outcome.kind === "ok" ? <StatusDot tone="ok" label={statusLabel(outcome.status)} />
                        : outcome.kind === "rejected" ? <StatusDot tone="danger" label={statusLabel(outcome.status)} />
                          : <StatusDot tone="warning" label={t("response.notConfirmed")} />}
                  {outcome && !sending ? (
                    <span className="wb-timing">{formatDuration(outcome.seconds)} · <time>{formatClock(outcome.at, locale)}</time></span>
                  ) : null}
                </div>
                {outcome && !sending ? (
                  <div className="wb-response-actions">
                    {outcome.kind === "ok" && outcome.agent ? (
                      <NextAction label={t("response.createSession")} onClick={() => continueWith("session.create", () => update("session")({ agentId: outcome.agent!.id }))} />
                    ) : null}
                    {outcome.kind === "ok" && outcome.request === "session.create" && outcome.sessionId ? (
                      <NextAction label={t("response.sendMessage")} onClick={() => continueWith("session.message", () => update("message")({ sessionId: outcome.sessionId! }))} />
                    ) : null}
                    {outcome.kind === "ok" && outcome.sessionId ? (
                      <NextAction label={t("response.openSession")} onClick={() => onOpenSession(outcome.sessionId!)} />
                    ) : null}
                    {outcome.kind !== "unknown" && responseText(outcome) ? (
                      <CopyButton copied={copied === "response"} label={t("response.copy")} onCopy={() => void copy("response", responseText(outcome)!)} />
                    ) : null}
                  </div>
                ) : null}
              </header>
              {outcome && !sending ? (
                outcome.kind === "ok" ? (
                  outcome.value === undefined
                    ? <p className="wb-note">{t("response.noBody")}</p>
                    : <CodeBlock label={t("response.title")} text={responseText(outcome)!} />
                ) : outcome.kind === "rejected" ? <>
                  <p className="wb-note wb-note-danger" role="alert">{outcome.message}</p>
                  {outcome.uncertain ? <p className="wb-note wb-note-warning">{t("response.uncertain")}</p> : null}
                  <CodeBlock label={t("response.title")} text={responseText(outcome)!} />
                </> : (
                  <p className="wb-note wb-note-warning" role="alert">{t("response.unknown", { reason: outcome.message })}</p>
                )
              ) : null}
            </section>
          </div>
        </div>
      </PageBody>
    </section>
  );
}

function responseText(outcome: Outcome): string | null {
  if (outcome.kind === "ok") return outcome.value === undefined ? null : JSON.stringify(outcome.value, null, 2);
  if (outcome.kind === "rejected") return JSON.stringify({ error: outcome.error }, null, 2);
  return null;
}

/**
 * Code with a hanging indent: a long line wraps under its own indentation
 * instead of at the left edge. The copy buttons copy the raw text.
 */
function CodeBlock({ label, text }: { label: string; text: string }) {
  return (
    <pre className="wb-code" tabIndex={0} aria-label={label}>
      {text.split("\n").map((line, index) => {
        const indent = line.length - line.trimStart().length + 2;
        return <span key={index} className="wb-line" style={{ paddingLeft: `${indent}ch`, textIndent: `-${indent}ch` }}>{line}{"\n"}</span>;
      })}
    </pre>
  );
}

function CopyButton({ copied, label, onCopy }: { copied: boolean; label: string; onCopy: () => void }) {
  const { t } = useTranslation("workbench");
  return (
    <button className="icon-button wb-copy" type="button" aria-label={copied ? t("request.copied") : label} title={copied ? t("request.copied") : label} onClick={onCopy}>
      {copied ? <Check size={14} strokeWidth={1.8} aria-hidden="true" /> : <Copy size={14} strokeWidth={1.6} aria-hidden="true" />}
    </button>
  );
}

function NextAction({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button className="text-action" type="button" onClick={onClick}>
      {label}<ArrowRight size={13} strokeWidth={1.7} aria-hidden="true" />
    </button>
  );
}

async function execute(
  core: WorkbenchCore,
  kind: WorkbenchKind,
  form: WorkbenchForm,
  request: WorkbenchRequest,
): Promise<{ status: number; value: unknown; sessionId: string | null; agent: SavedAgent | null }> {
  // Every write sends `request.body`, the object the preview renders.
  if (kind === "agent.create") {
    const agent = await core.createAgent(request.body as CreateAgentInput);
    return { status: 201, value: agent, sessionId: null, agent };
  }
  if (kind === "session.create") {
    const session = await core.createSession(request.body as CreateSessionInput, request.idempotencyKey);
    return { status: 201, value: session, sessionId: session.id, agent: null };
  }
  if (kind === "session.message") {
    const sessionId = form.message.sessionId.trim();
    await core.submitEvents(sessionId, (request.body as { events: SessionMessageInputEvent[] }).events, request.idempotencyKey!);
    return { status: 202, value: undefined, sessionId, agent: null };
  }
  const id = form.lookup.id.trim();
  const parent = form.lookup.parentId.trim();
  const value = await ({
    agent: () => core.retrieveAgent(id),
    session: () => core.retrieveSession(id),
    turn: () => core.retrieveTurn(parent, id),
    execution_configuration: () => core.retrieveSessionExecutionConfiguration(id),
    environment: () => core.retrieveEnvironment(id),
    environment_template: () => core.retrieveEnvironmentTemplate(id),
    vault: () => core.retrieveVault(id),
    credential: () => core.retrieveVaultCredential(parent, id),
    skill: () => core.retrieveSkill(id),
    file: () => core.retrieveSourceFile(id),
  } satisfies Record<LookupKind, () => Promise<unknown>>)[form.lookup.kind]();
  const sessionId = form.lookup.kind === "session" || form.lookup.kind === "execution_configuration" ? id
    : form.lookup.kind === "turn" ? parent : null;
  return { status: 200, value, sessionId, agent: null };
}
