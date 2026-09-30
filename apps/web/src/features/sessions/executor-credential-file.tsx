import type { IssuedExecutorCredential } from "@oac/agents-client";
import { Check, Copy, Download } from "lucide-react";
import { useEffect, useRef } from "react";
import { Trans, useTranslation } from "react-i18next";

import { useCopy } from "../api-keys/IssuedKey";
import { saveBlob } from "../skills/skill-operations";
import { useSelectWhenCopyFails } from "./ExecutorInstallPanel";

/** The native installer reads this exact JSON from the privately saved credential file. */
function credentialText(credential: IssuedExecutorCredential): string {
  const { key_id, environment_id, executor_token } = credential;
  return JSON.stringify({ key_id, environment_id, executor_token });
}

/** The credential is available once; installation instructions live in Connect a host. */
export function CredentialFile({ credential }: { credential: IssuedExecutorCredential }) {
  const { t } = useTranslation("sessions");
  const text = credentialText(credential);
  const { state, copy } = useCopy(text);
  const file = useRef<HTMLPreElement>(null);
  useSelectWhenCopyFails(state, file);
  // A downloaded credential's object URL goes with the credential: on Done or when this leaves the page.
  const downloads = useRef<(() => void)[]>([]);
  useEffect(() => {
    const revokes = downloads.current;
    return () => { for (const revoke of revokes.splice(0)) revoke(); };
  }, []);
  const download = () => {
    downloads.current.push(saveBlob(new Blob([`${text}\n`], { type: "application/json" }), `executor-credential-${credential.environment_id.slice(0, 8)}.json`));
  };
  return (
    <div className="executor-credential">
      <p className="executor-credential-notice">{t("executor.issued.notice")}</p>
      <p className="executor-credential-next">{t("executor.issued.next")}</p>
      <div role="region" aria-label={t("executor.issued.fileLabel")}><pre ref={file} className="executor-credential-file"><code>{text}</code></pre></div>
      <div className="executor-credential-actions">
        <button className="button outline" type="button" onClick={() => void copy()}>
          {state === "copied" ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
          {state === "copied" ? t("executor.issued.copied") : t("executor.issued.copy")}
        </button>
        <button className="button primary" type="button" onClick={download}>
          <Download size={14} aria-hidden="true" />{t("executor.issued.download")}
        </button>
      </div>
      {state === "failed" ? <p className="executor-credential-error" role="alert">{t("executor.issued.copyFailed")}</p> : null}
      <p className="executor-credential-hint">
        <Trans t={t} i18nKey="executor.issued.downloadHint" components={{ flag: <code>--credential-file &lt;absolute path&gt;</code> }} />
      </p>
    </div>
  );
}
