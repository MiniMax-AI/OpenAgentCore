import { useQuery } from "@tanstack/react-query";
import { Check, Copy } from "lucide-react";
import { useEffect, useId, useRef } from "react";
import { useTranslation } from "react-i18next";

import { installationQuery } from "../../lib/installation";
import { useCopy } from "./IssuedKey";
import "./how-to-call.css";

/** The official SDK release Core is pinned to (contracts/agents-api/upstream.json). */
const SDK_PIN = "openai==3.13.0";

/**
 * What an application needs to call Core with a Project API key: the official
 * SDK's environment variables, a raw request and the SDK itself. Both
 * variables are exported together, so a shell that already exports one of
 * them never sends the new key to another base URL.
 */
export function callSamples(apiBaseUrl: string, apiKey: string) {
  return {
    shell: `export OPENAI_BASE_URL=${apiBaseUrl}\nexport OPENAI_API_KEY=${apiKey}`,
    curl: [
      'curl "$OPENAI_BASE_URL/agents" \\',
      '  -H "Authorization: Bearer $OPENAI_API_KEY" \\',
      '  -H "OpenAI-Beta: agents=v1"',
    ].join("\n"),
    python: [
      `# pip install ${SDK_PIN}`,
      "from openai import OpenAI",
      "",
      "client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY",
      "print(client.beta.agents.list().data)",
    ].join("\n"),
  };
}

/**
 * How to call Core with a key that was just issued. The key comes from the
 * issuing flow's memory and leaves with it; the console never sends these
 * requests. When Core is reachable only on its own machine it says so, and
 * without a public address it says to set one instead of guessing.
 */
export function HowToCall({ apiKey }: { apiKey: string }) {
  const { t } = useTranslation("keys");
  const { t: tCommon } = useTranslation("common");
  const headingId = useId();
  const installation = useQuery(installationQuery);
  const base = installation.data?.api_base_url ?? null;

  let body;
  if (installation.data === undefined) {
    body = installation.isError && !installation.isFetching
      ? <p className="how-to-call-note" role="alert">{t("howToCall.failed")} <button className="text-action" type="button" onClick={() => void installation.refetch()}>{tCommon("actions.retry")}</button></p>
      // The first sample's place, as the console's other first reads hold theirs.
      : <div className="how-to-call-sample how-to-call-skeleton" role="status" aria-label={t("howToCall.loading")} aria-busy="true"><span className="skeleton-bar" /><span className="skeleton-bar" /></div>;
  } else if (base === null) {
    body = <p className="how-to-call-note" role="note">{t("howToCall.noAddress")}</p>;
  } else {
    const samples = callSamples(base, apiKey);
    body = <>
      {installation.data.local_only ? <p className="how-to-call-note" role="note">{t("howToCall.localOnly")}</p> : null}
      <CodeSample label={t("howToCall.shell")} value={samples.shell} />
      <CodeSample label="curl" value={samples.curl} />
      <CodeSample label="Python" value={samples.python} />
      <p className="how-to-call-note">{t("howToCall.model")}</p>
    </>;
  }
  return (
    <section className="how-to-call" aria-labelledby={headingId}>
      <h3 id={headingId}>{t("howToCall.title")}</h3>
      {body}
    </section>
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
