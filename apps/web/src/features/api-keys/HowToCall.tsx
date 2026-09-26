import { useQuery } from "@tanstack/react-query";
import { Check, Copy } from "lucide-react";
import { useEffect, useId, useRef } from "react";
import { useTranslation } from "react-i18next";

import { Section } from "../../components/console-ui";
import { installationQuery } from "../../lib/installation";
import { useCopy } from "./IssuedKey";
import "./how-to-call.css";

/** The official SDK release Core is pinned to (contracts/agents-api/upstream.json). */
const SDK_PIN = "openai==3.13.0";

/** Stands for the model name in the Session example; the note under the samples says what to put there. */
export const MODEL_PLACEHOLDER = "<model>";

/**
 * What an application needs to call Core with a Project API key: the official
 * SDK's environment variables, then raw requests and the SDK itself, each
 * listing the project's Agents and creating a Session with a first message
 * (`POST /agents/sessions` in its official shape: an environment, an inline
 * Agent with its model, and the input). Both variables are exported together,
 * so a shell that already exports one of them never sends the new key to
 * another base URL. Without a key at hand (`apiKey` null) the export holds
 * `keyPlaceholder`, quoted so a pasted line stays valid shell.
 */
export function callSamples(apiBaseUrl: string, apiKey: string | null, keyPlaceholder = "<project API key>") {
  return {
    shell: `export OPENAI_BASE_URL=${apiBaseUrl}\nexport OPENAI_API_KEY=${apiKey ?? `"${keyPlaceholder}"`}`,
    curl: [
      "# List the project's Agents",
      'curl "$OPENAI_BASE_URL/agents" \\',
      '  -H "Authorization: Bearer $OPENAI_API_KEY" \\',
      '  -H "OpenAI-Beta: agents=v1"',
      "",
      "# Create a Session with a first message",
      'curl "$OPENAI_BASE_URL/agents/sessions" \\',
      '  -H "Authorization: Bearer $OPENAI_API_KEY" \\',
      '  -H "OpenAI-Beta: agents=v1" \\',
      '  -H "Content-Type: application/json" \\',
      "  -d '{",
      '    "environment": {"type": "openai_hosted"},',
      `    "agent": {"model": "${MODEL_PLACEHOLDER}"},`,
      '    "input": "Say hello."',
      "  }'",
    ].join("\n"),
    python: [
      `# pip install ${SDK_PIN}`,
      "from openai import OpenAI",
      "",
      "client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY",
      "print(client.beta.agents.list().data)",
      "",
      "# Create a Session with a first message",
      "session = client.beta.agents.sessions.create(",
      '    environment={"type": "openai_hosted"},',
      `    agent={"model": "${MODEL_PLACEHOLDER}"},`,
      '    input="Say hello.",',
      ")",
      "print(session.id, session.status)",
    ].join("\n"),
  };
}

/**
 * The samples, or why there are none. The console never sends these requests.
 * When Core is reachable only on its own machine it says so, and without a
 * public address it says to set one instead of guessing.
 */
function HowToCallBody({ apiKey }: { apiKey: string | null }) {
  const { t } = useTranslation("keys");
  const { t: tCommon } = useTranslation("common");
  const installation = useQuery(installationQuery);
  const base = installation.data?.api_base_url ?? null;

  if (installation.data === undefined) {
    return installation.isError && !installation.isFetching
      ? <p className="how-to-call-note" role="alert">{t("howToCall.failed")} <button className="text-action" type="button" onClick={() => void installation.refetch()}>{tCommon("actions.retry")}</button></p>
      // The first sample's place, as the console's other first reads hold theirs.
      : <div className="how-to-call-sample how-to-call-skeleton" role="status" aria-label={t("howToCall.loading")} aria-busy="true"><span className="skeleton-bar" /><span className="skeleton-bar" /></div>;
  }
  if (base === null) return <p className="how-to-call-note" role="note">{t("howToCall.noAddress")}</p>;
  const samples = callSamples(base, apiKey, t("howToCall.keyPlaceholder"));
  return (
    <>
      {installation.data.local_only ? <p className="how-to-call-note" role="note">{t("howToCall.localOnly")}</p> : null}
      {apiKey === null ? <p className="how-to-call-note">{t("howToCall.projectKey")}</p> : null}
      <CodeSample label={t("howToCall.shell")} value={samples.shell} />
      <CodeSample label="curl" value={samples.curl} />
      <CodeSample label="Python" value={samples.python} />
      <p className="how-to-call-note">{t("howToCall.model", { model: MODEL_PLACEHOLDER })}</p>
    </>
  );
}

/** How to call Core with a key that was just issued. The key comes from the issuing flow's memory and leaves with it. */
export function HowToCall({ apiKey }: { apiKey: string }) {
  const { t } = useTranslation("keys");
  const headingId = useId();
  return (
    <section className="how-to-call" aria-labelledby={headingId}>
      <h3 id={headingId}>{t("howToCall.title")}</h3>
      <HowToCallBody apiKey={apiKey} />
    </section>
  );
}

/** The heading Getting started focuses when it opens a project's samples. */
export const PROJECT_CALL_HEADING_ID = "project-call-heading";

/**
 * How to call Core with a key of this project, always on its page. It never
 * holds a key: the console shows a key only once, when it is issued, so the
 * samples carry a placeholder and say to use a key issued for this project.
 */
export function ProjectHowToCall() {
  const { t } = useTranslation("keys");
  return (
    <Section className="project-call" headingId={PROJECT_CALL_HEADING_ID} headingFocusable title={t("howToCall.title")} help={t("howToCall.projectHelp")}>
      <div className="how-to-call">
        <HowToCallBody apiKey={null} />
      </div>
    </Section>
  );
}

/** A labelled sample with a copy button; when the clipboard refuses, the sample is selected to copy by hand. */
function CodeSample({ label, value }: { label: string; value: string }) {
  const { t } = useTranslation("keys");
  const code = useRef<HTMLPreElement>(null);
  const { state, copy } = useCopy(value);
  useEffect(() => {
    if (state !== "failed" || !code.current) return;
    const range = document.createRange();
    range.selectNodeContents(code.current);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
  }, [state]);
  const name = state === "copied" ? t("issued.copied") : t("howToCall.copy", { label });
  return (
    <div className="how-to-call-sample">
      <div className="how-to-call-sample-head">
        <span>{label}</span>
        <button type="button" className="icon-button ghost copyable-id-button" aria-label={name} title={name} onClick={() => void copy()}>
          {state === "copied" ? <Check size={13} strokeWidth={1.7} aria-hidden="true" /> : <Copy size={13} strokeWidth={1.7} aria-hidden="true" />}
        </button>
      </div>
      <pre ref={code} aria-label={label} tabIndex={0}><code>{value}</code></pre>
      {state === "failed" ? <p className="how-to-call-error" role="alert">{t("howToCall.copyFailed")}</p> : null}
    </div>
  );
}
