import { Check, Code2, Copy, Send } from "lucide-react";
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
} from "@agents-core-web/agents-client";

import { PageBody, PageHeader, SegmentedControl, StatusDot } from "../../components/console-ui";
import { formatClock } from "../../lib/format";
import { buildModelOptionGroups } from "../../lib/model-options";
import {
  buildWorkbenchRequest,
  emptyWorkbenchForm,
  workbenchCurl,
  type LookupKind,
  type WorkbenchForm,
  type WorkbenchKind,
  type WorkbenchRequest,
} from "./workbench-requests";
import "./workbench.css";

export type WorkbenchCore = Pick<
  AgentCore,
  "createAgent" | "createSession" | "sendMessage" | "retrieveAgent" | "retrieveSession" | "retrieveVault" | "retrieveEnvironmentTemplate" | "retrieveEnvironment" | "retrieveSourceFile"
>;

type Outcome =
  | { kind: "ok"; status: number; value: unknown; sessionId: string | null; at: number }
  | { kind: "rejected"; status: number; message: string; code: string | null; at: number }
  | { kind: "unknown"; message: string; at: number };

const KINDS: readonly WorkbenchKind[] = ["agent.create", "session.create", "session.message", "object.retrieve"];
const LOOKUPS: readonly LookupKind[] = ["session", "agent", "vault", "environment_template", "environment", "file"];

/**
 * Playground › API workbench: a fixed-format request builder. The form on the
 * left produces the exact request on the right; copy it, or send it through
 * the console connection and read the response below.
 */
export function ApiWorkbenchView({
  core,
  baseUrl,
  agents,
  sessions,
  harnesses,
  onOpenSession,
  onChanged,
}: {
  core: WorkbenchCore;
  baseUrl: string;
  agents: readonly SavedAgent[];
  sessions: readonly AgentSession[];
  harnesses: readonly CoreHarnessKind[];
  onOpenSession: (sessionId: string) => void;
  /** A write succeeded; collections may refresh. */
  onChanged: () => void;
}) {
  const { t, i18n } = useTranslation("app");
  const locale = i18n.resolvedLanguage;
  const [kind, setKind] = useState<WorkbenchKind>("agent.create");
  const [form, setForm] = useState<WorkbenchForm>(() => emptyWorkbenchForm(
    buildModelOptionGroups([], import.meta.env.VITE_AGENT_MODEL_PRESETS, import.meta.env.VITE_AGENT_DEFAULT_MODEL).defaultModel,
  ));
  // One key per request instance: it only changes once an outcome is known, so
  // sending again after an unconfirmed attempt cannot create a duplicate.
  const [idempotencyKey, setIdempotencyKey] = useState(createIdempotencyKey);
  const [sending, setSending] = useState(false);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const live = useRef(true);
  useEffect(() => () => { live.current = false; }, []);
  useEffect(() => { setOutcome(null); }, [kind]);

  const request = useMemo(() => buildWorkbenchRequest(kind, form, idempotencyKey), [kind, form, idempotencyKey]);
  const curl = workbenchCurl(request, baseUrl);
  const json = request.body ? JSON.stringify(request.body, null, 2) : null;
  const update = <K extends keyof WorkbenchForm>(section: K, patch: Partial<WorkbenchForm[K]>) =>
    setForm((current) => ({ ...current, [section]: { ...current[section], ...patch } }));

  async function copy(id: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      if (live.current) { setCopied(id); window.setTimeout(() => live.current && setCopied((value) => (value === id ? null : value)), 1600); }
    } catch {
      if (live.current) setCopied(null);
    }
  }

  async function send() {
    if (request.problem || sending) return;
    setSending(true);
    const at = Date.now() / 1000;
    try {
      const result = await execute(core, kind, form, request);
      if (!live.current) return;
      setOutcome({ kind: "ok", ...result, at });
      setIdempotencyKey(createIdempotencyKey());
      if (kind !== "object.retrieve") onChanged();
    } catch (error) {
      if (!live.current) return;
      if (error instanceof AgentCoreError && error.status >= 400 && error.status < 500) {
        setOutcome({ kind: "rejected", status: error.status, message: error.message, code: error.code ?? null, at });
        setIdempotencyKey(createIdempotencyKey());
      } else if (error instanceof AgentCoreError) {
        setOutcome({ kind: "rejected", status: error.status, message: error.message, code: error.code ?? null, at });
      } else {
        setOutcome({ kind: "unknown", message: error instanceof Error ? error.message : String(error), at });
      }
    } finally {
      if (live.current) setSending(false);
    }
  }

  const agentOptions = agents.map((agent) => ({ id: agent.id, label: agent.name ? `${agent.name} (${agent.id})` : agent.id }));
  const sessionOptions = sessions.slice(0, 50);
  return (
    <section className="page-section console-page workbench-page" aria-labelledby="workbench-heading">
      <PageHeader headingId="workbench-heading" title={t("workbench.title")} help={<>{t("workbench.help")}<br />{t("workbench.tagged")}</>} />
      <PageBody>
        <div className="workbench">
          <section className="workbench-form" aria-labelledby="workbench-form-heading">
            <h2 id="workbench-form-heading" className="visually-hidden">{t("workbench.formTitle")}</h2>
            <SegmentedControl
              label={t("workbench.kindLabel")}
              value={kind}
              options={KINDS.map((value) => ({ value, label: t(`workbench.kinds.${value}`) }))}
              onChange={setKind}
            />
            <fieldset disabled={sending}>
              {kind === "agent.create" ? <>
                <label className="field"><span>{t("workbench.fields.name")}</span><input value={form.agent.name} onChange={(event) => update("agent", { name: event.target.value })} maxLength={128} /></label>
                <div className="workbench-row">
                  <label className="field"><span>{t("workbench.fields.model")}</span><input value={form.agent.model} onChange={(event) => update("agent", { model: event.target.value })} spellCheck={false} required /></label>
                  <label className="field"><span>{t("workbench.fields.harness")}</span>
                    <select value={form.agent.harness} onChange={(event) => update("agent", { harness: event.target.value as CoreHarnessKind | "" })}>
                      <option value="">{t("workbench.fields.deploymentDefault")}</option>
                      {harnesses.map((harness) => <option key={harness} value={harness}>{harness}</option>)}
                    </select>
                  </label>
                </div>
                <label className="field"><span>{t("workbench.fields.instructions")}</span><textarea rows={5} value={form.agent.instructions} onChange={(event) => update("agent", { instructions: event.target.value })} /></label>
              </> : kind === "session.create" ? <>
                <label className="field"><span>{t("workbench.fields.agent")}</span>
                  <select value={form.session.agentId} onChange={(event) => update("session", { agentId: event.target.value })} required>
                    <option value="">{t("workbench.fields.chooseAgent")}</option>
                    {agentOptions.map((agent) => <option key={agent.id} value={agent.id}>{agent.label}</option>)}
                  </select>
                </label>
                <div className="workbench-row">
                  <label className="field"><span>{t("workbench.fields.environment")}</span>
                    <select value={form.session.environment} onChange={(event) => update("session", { environment: event.target.value as WorkbenchForm["session"]["environment"] })}>
                      <option value="none">{t("workbench.fields.environmentNone")}</option>
                      <option value="openai_hosted">{t("workbench.fields.environmentHosted")}</option>
                    </select>
                  </label>
                  <label className="field"><span>{t("workbench.fields.template")}</span><input value={form.session.templateId} onChange={(event) => update("session", { templateId: event.target.value })} disabled={form.session.environment !== "openai_hosted"} placeholder="envtpl_…" spellCheck={false} /></label>
                </div>
                <label className="field"><span>{t("workbench.fields.input")}</span><textarea rows={4} value={form.session.input} onChange={(event) => update("session", { input: event.target.value })} /></label>
              </> : kind === "session.message" ? <>
                <label className="field"><span>{t("workbench.fields.session")}</span>
                  <input list="workbench-sessions" value={form.message.sessionId} onChange={(event) => update("message", { sessionId: event.target.value })} placeholder="sess_…" spellCheck={false} required />
                  <datalist id="workbench-sessions">{sessionOptions.map((session) => <option key={session.id} value={session.id}>{session.agent?.name ?? ""}</option>)}</datalist>
                </label>
                <label className="field"><span>{t("workbench.fields.message")}</span><textarea rows={6} value={form.message.text} onChange={(event) => update("message", { text: event.target.value })} /></label>
              </> : <>
                <div className="workbench-row">
                  <label className="field"><span>{t("workbench.fields.objectType")}</span>
                    <select value={form.lookup.kind} onChange={(event) => update("lookup", { kind: event.target.value as LookupKind })}>
                      {LOOKUPS.map((value) => <option key={value} value={value}>{t(`workbench.objects.${value}`)}</option>)}
                    </select>
                  </label>
                  <label className="field"><span>{t("workbench.fields.id")}</span><input value={form.lookup.id} onChange={(event) => update("lookup", { id: event.target.value })} spellCheck={false} required /></label>
                </div>
              </>}
            </fieldset>
          </section>

          <div className="workbench-side">
            <section className="agent-request-preview workbench-preview" aria-labelledby="workbench-request-heading">
              <header>
                <Code2 size={15} strokeWidth={1.5} aria-hidden="true" />
                <div>
                  <h2 id="workbench-request-heading">{t("workbench.request")}</h2>
                  <p><code>{request.method} /v1{request.path}</code></p>
                </div>
              </header>
              <PreviewBlock label="curl" text={curl} copied={copied === "curl"} onCopy={() => void copy("curl", curl)} copyLabel={t("workbench.copy", { name: "curl" })} />
              {json && request.bodyFile ? <PreviewBlock label={request.bodyFile} text={json} copied={copied === "body"} onCopy={() => void copy("body", json)} copyLabel={t("workbench.copy", { name: request.bodyFile })} /> : null}
              <footer className="workbench-actions">
                {request.problem ? <span className="workbench-problem">{t(`workbench.problems.${request.problem}`)}</span> : null}
                <button className="button primary" type="button" onClick={() => void send()} disabled={Boolean(request.problem) || sending}>
                  <Send size={14} aria-hidden="true" />{t(sending ? "workbench.sending" : "workbench.send")}
                </button>
              </footer>
            </section>

            {outcome ? (
              <section className="workbench-response" aria-labelledby="workbench-response-heading" aria-live="polite">
                <header>
                  <h2 id="workbench-response-heading">{t("workbench.response")}</h2>
                  {outcome.kind === "ok"
                    ? <StatusDot tone="ok" label={t("workbench.statusOk", { status: outcome.status })} />
                    : outcome.kind === "rejected"
                      ? <StatusDot tone="danger" label={t("workbench.statusRejected", { status: outcome.status })} />
                      : <StatusDot tone="warning" label={t("workbench.statusUnknown")} />}
                  <time>{formatClock(outcome.at, locale)}</time>
                </header>
                {outcome.kind === "ok" ? (
                  <>
                    {outcome.value === undefined
                      ? <p className="workbench-note">{t("workbench.accepted")}</p>
                      : <pre className="workbench-json" tabIndex={0}>{JSON.stringify(outcome.value, null, 2)}</pre>}
                    {outcome.sessionId ? (
                      <button className="button outline" type="button" onClick={() => onOpenSession(outcome.sessionId!)}>{t("workbench.openSession")}</button>
                    ) : null}
                  </>
                ) : outcome.kind === "rejected" ? (
                  <pre className="workbench-json" tabIndex={0}>{JSON.stringify({ error: { message: outcome.message, code: outcome.code } }, null, 2)}</pre>
                ) : (
                  <p className="workbench-note workbench-note-warning" role="alert">{t("workbench.unknown", { reason: outcome.message })}</p>
                )}
              </section>
            ) : null}
          </div>
        </div>
      </PageBody>
    </section>
  );
}

function PreviewBlock({ label, text, copied, copyLabel, onCopy }: { label: string; text: string; copied: boolean; copyLabel: string; onCopy: () => void }) {
  return (
    <div className="agent-preview-block workbench-block">
      <span>
        {label}
        <button type="button" className="workbench-copy" aria-label={copyLabel} title={copyLabel} onClick={onCopy}>
          {copied ? <Check size={13} aria-hidden="true" /> : <Copy size={13} aria-hidden="true" />}
        </button>
      </span>
      <pre tabIndex={0}>{text}</pre>
    </div>
  );
}

async function execute(core: WorkbenchCore, kind: WorkbenchKind, form: WorkbenchForm, request: WorkbenchRequest): Promise<{ status: number; value: unknown; sessionId: string | null }> {
  if (kind === "agent.create") {
    return { status: 201, value: await core.createAgent(request.body as CreateAgentInput), sessionId: null };
  }
  if (kind === "session.create") {
    const session = await core.createSession(request.body as CreateSessionInput, request.idempotencyKey);
    return { status: 201, value: session, sessionId: session.id };
  }
  if (kind === "session.message") {
    const sessionId = form.message.sessionId.trim();
    await core.sendMessage(sessionId, form.message.text, request.idempotencyKey!);
    return { status: 202, value: undefined, sessionId };
  }
  const id = form.lookup.id.trim();
  const value = await ({
    agent: () => core.retrieveAgent(id),
    session: () => core.retrieveSession(id),
    vault: () => core.retrieveVault(id),
    environment_template: () => core.retrieveEnvironmentTemplate(id),
    environment: () => core.retrieveEnvironment(id),
    file: () => core.retrieveSourceFile(id),
  } satisfies Record<LookupKind, () => Promise<unknown>>)[form.lookup.kind]();
  return { status: 200, value, sessionId: form.lookup.kind === "session" ? id : null };
}
