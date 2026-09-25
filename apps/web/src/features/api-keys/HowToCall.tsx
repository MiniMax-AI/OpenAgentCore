import { useQuery } from "@tanstack/react-query";
import { useId } from "react";
import { useTranslation } from "react-i18next";

import { CopyIdButton } from "../../components/list-ui";
import { installationQuery } from "../../lib/installation";
import "./how-to-call.css";

/** The official SDK release Core is pinned to (contracts/agents-api/upstream.json). */
const SDK_PIN = "openai==3.13.0";

/**
 * What an application needs to call Core with a Project API key: the official
 * SDK's environment variables, a raw request and the SDK itself.
 */
export function callSamples(apiBaseUrl: string, apiKey: string) {
  return {
    env: `OPENAI_BASE_URL=${apiBaseUrl}\nOPENAI_API_KEY=${apiKey}`,
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
 * requests. Without a public API address it says so instead of guessing one.
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
      : <p className="how-to-call-note" role="status">{t("howToCall.loading")}</p>;
  } else if (base === null) {
    body = <p className="how-to-call-note" role="note">{t("howToCall.localOnly")}</p>;
  } else {
    const samples = callSamples(base, apiKey);
    body = <>
      <CodeSample label={t("howToCall.env")} value={samples.env} />
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

function CodeSample({ label, value }: { label: string; value: string }) {
  const { t } = useTranslation("keys");
  return (
    <div className="how-to-call-sample">
      <div className="how-to-call-sample-head">
        <span>{label}</span>
        <CopyIdButton id={value} label={t("howToCall.copy", { label })} />
      </div>
      <pre aria-label={label} tabIndex={0}><code>{value}</code></pre>
    </div>
  );
}
