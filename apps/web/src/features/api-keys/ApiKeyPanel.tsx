import { Check, Copy, KeyRound, Plus, RefreshCw } from "lucide-react";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useApiKeyCapability, useManagedApiKeys } from "./use-api-keys";
import "./api-keys.css";

/** Key creation inside the introduction; the settings page uses `ApiKeysView`. */
export function ApiKeyPanel({ onReady }: { onReady?: (ready: boolean) => void }) {
  const { t } = useTranslation("firstRun");
  const { capability, retry } = useApiKeyCapability();
  const ready = useRef(onReady); ready.current = onReady;
  useEffect(() => {
    if (capability === "loading") ready.current?.(false);
    if (capability === "unavailable") ready.current?.(true);
  }, [capability]);
  if (capability === "available") return <ManagedApiKeyPanel onReady={onReady} />;
  return <section className="api-key-panel" aria-label={t("API keys")}>
    {capability === "unavailable" ? <>
      <header><div className="api-key-heading"><KeyRound size={18} strokeWidth={1.5} /><h3>{t("Use an existing Agent API key.")}</h3></div></header>
      <p className="api-key-caption">{t("This console cannot create API keys. Use a key supplied by your Core administrator for requests from your machine or application.")}</p>
      <p className="api-key-caption">{t("You can continue the introduction and use your signed-in console connection to create an Agent.")}</p>
    </> : capability === "loading" ? <p role="status">{t("Checking API key management…")}</p> : <>
      <p className="api-key-error" role="alert">{t("Could not check API key management. Try again.")}</p>
      <button type="button" className="button outline" onClick={retry}>{t("Try again")}</button>
    </>}
  </section>;
}

function ManagedApiKeyPanel({ onReady }: { onReady?: (ready: boolean) => void }) {
  const { t } = useTranslation("firstRun");
  const keys = useManagedApiKeys();
  const { issued, uncertain, fresh, hasKey, busy, loading } = keys;
  const ready = useRef(onReady); ready.current = onReady;
  useEffect(() => { ready.current?.(fresh && hasKey && !issued && !uncertain); }, [fresh, hasKey, issued, uncertain]);
  return <section className="api-key-panel" aria-label={t("API keys")}>
    <header><div className="api-key-heading"><KeyRound size={18} strokeWidth={1.5} /><h3>{t("Create a key for your API requests.")}</h3></div>
      <p>{t("Use this key when calling Agent API from your own machine or application.")}</p></header>
    <p className="api-key-caption">{t("Your Web password is for signing in. This key is for API requests.")}</p>
    {issued ? <div className="api-key-secret" role="status"><strong>{t("Keep this key somewhere safe. It is shown only now.")}</strong>
      <input aria-label={t("Your new API key")} value={issued.key} readOnly spellCheck={false} autoComplete="off" onFocus={(event) => event.target.select()} />
      <div className="api-key-actions"><button type="button" className="button outline" onClick={() => void keys.copy()}>{keys.copied ? <Check size={14} /> : <Copy size={14} />}{t(keys.copied ? "Copied" : "Copy key")}</button>
        <button type="button" className="button primary" onClick={keys.dismissIssued}>{t("I've saved this key")}</button></div>
      {keys.copyFailed ? <p role="alert">{t("Select the key and copy it manually.")}</p> : null}</div> :
      <form className="api-key-create" onSubmit={(event) => void keys.create(event)}><label className="field"><span>{t("Key name")}</span><input value={keys.name} onChange={(event) => keys.setName(event.target.value)} maxLength={80} required disabled={busy || Boolean(uncertain)} /></label>
        <button type="submit" className="button primary" disabled={busy || loading || !fresh || Boolean(uncertain) || !keys.name.trim()}><Plus size={14} />{t(busy ? "Creating key…" : "Create API key")}</button></form>}
    {keys.error ? <p className="api-key-error" role="alert">{keys.error}</p> : null}
    {keys.uncertainListed ? <p className="api-key-error" role="alert">{t("This key was created, but its secret cannot be shown again. Revoke it and create a new key.")}</p> : null}
    {keys.canRetryCreation ? <button type="button" className="button outline" onClick={keys.retryCreation}>{t("Retry this creation")}</button> : null}
    <div className="api-key-list-heading"><span>{t(hasKey ? "API key ready" : "API keys")}</span><button type="button" className="icon-button" aria-label={t("Refresh keys")} disabled={busy || loading} onClick={keys.refresh}><RefreshCw size={14} /></button></div>
    {loading ? <p role="status">{t("Loading API keys…")}</p> : null}
    {!keys.keys.length && fresh ? <p className="api-key-caption">{t("No API keys yet. Create one to get started.")}</p> : null}
    <ul className="api-key-list">{keys.keys.map((key) => <li key={key.id}><div><strong>{key.name}</strong><span><code>{key.prefix}…</code> · {t(key.revoked_at ? "Revoked" : "Active")}</span></div>
      {!key.revoked_at ? <button className="first-run-text-button" type="button" disabled={busy || !fresh} onClick={() => keys.setConfirm(key.id)}>{t("Revoke")}</button> : null}
      {keys.confirm === key.id ? <div className="api-key-confirm"><p>{t("Revoke this API key? Requests using it will stop working.")}</p><div className="api-key-actions"><button className="button danger" type="button" disabled={busy || !fresh} onClick={() => void keys.revoke(key.id)}>{t("Confirm revocation")}</button><button className="button outline" type="button" disabled={busy} onClick={() => keys.setConfirm(null)}>{t("Cancel")}</button></div></div> : null}</li>)}</ul>
  </section>;
}
