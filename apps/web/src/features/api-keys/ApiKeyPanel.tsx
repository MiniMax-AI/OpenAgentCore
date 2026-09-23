import { Check, Copy, KeyRound, Plus, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useLocale } from "../../lib/LocaleProvider";
import { sandboxConsoleConfig } from "../sandbox/console-config";
import { createConsoleKey, KeyRequestError, listConsoleKeys, revokeConsoleKey, safeKey, type ConsoleAPIKey, type IssuedConsoleAPIKey } from "./api-keys";
import "./api-keys.css";

export function ApiKeyPanel({ onReady }: { onReady?: (ready: boolean) => void }) {
  const { t } = useLocale();
  const [capability, setCapability] = useState<"loading" | "available" | "unavailable" | "error">("loading");
  const [revision, setRevision] = useState(0);
  const ready = useRef(onReady); ready.current = onReady;
  useEffect(() => {
    const controller = new AbortController();
    setCapability("loading"); ready.current?.(false);
    void sandboxConsoleConfig(controller.signal).then((config) => {
      if (controller.signal.aborted) return;
      setCapability(config?.api_keys === true ? "available" : config?.api_keys === false ? "unavailable" : "error");
      if (config?.api_keys === false) ready.current?.(true);
    });
    return () => controller.abort();
  }, [revision]);
  if (capability === "available") return <ManagedApiKeyPanel onReady={onReady} />;
  return <section className="api-key-panel" aria-label={t("API keys")}>
    {capability === "unavailable" ? <>
      <header><div className="api-key-heading"><KeyRound size={18} strokeWidth={1.5} /><h3>{t("Use an existing Agent API key.")}</h3></div></header>
      <p className="api-key-caption">{t("This console cannot create API keys. Use a key supplied by your Core administrator for requests from your machine or application.")}</p>
      <p className="api-key-caption">{t("You can continue the introduction and use your signed-in console connection to create an Agent.")}</p>
    </> : capability === "loading" ? <p role="status">{t("Checking API key management…")}</p> : <>
      <p className="api-key-error" role="alert">{t("Could not check API key management. Try again.")}</p>
      <button type="button" className="button outline" onClick={() => setRevision((value) => value + 1)}>{t("Try again")}</button>
    </>}
  </section>;
}

function ManagedApiKeyPanel({ onReady }: { onReady?: (ready: boolean) => void }) {
  const { t } = useLocale();
  const [keys, setKeys] = useState<ConsoleAPIKey[]>([]);
  const [name, setName] = useState("My API key");
  const [issued, setIssued] = useState<IssuedConsoleAPIKey | null>(null);
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fresh, setFresh] = useState(false);
  const [uncertain, setUncertain] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const [id, setId] = useState(() => crypto.randomUUID());
  const activeRequest = useRef(false);
  const lifetime = useRef<AbortController | null>(null);
  const ready = useRef(onReady); ready.current = onReady;
  useEffect(() => {
    const controller = new AbortController(); lifetime.current = controller;
    return () => { controller.abort(); lifetime.current = null; };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setFresh(false);
    void listConsoleKeys(controller.signal).then((rows) => {
      if (!controller.signal.aborted) { setKeys(rows); setFresh(true); setError(null); }
    }).catch((cause) => {
      if (!controller.signal.aborted) setError(t(cause instanceof KeyRequestError && cause.status === 503 ? "Key management is not connected. Check the paired Core configuration." : "Could not load your keys. Try refreshing."));
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [revision, t]);
  useEffect(() => {
    if (uncertain && fresh && keys.some((key) => key.id === uncertain && key.revoked_at)) {
      setUncertain(null); setId(crypto.randomUUID());
    }
  }, [uncertain, fresh, keys]);
  const hasKey = keys.some((key) => !key.revoked_at);
  useEffect(() => { ready.current?.(fresh && hasKey && !issued && !uncertain); }, [fresh, hasKey, issued, uncertain]);
  async function create(event: FormEvent) {
    event.preventDefault();
    const controller = lifetime.current;
    if (!controller || activeRequest.current || issued || uncertain || !fresh) return;
    activeRequest.current = true; setBusy(true); setError(null);
    try {
      const result = await createConsoleKey(id, name.trim(), controller.signal);
      if (controller.signal.aborted) return;
      setIssued(result); setKeys((rows) => [safeKey(result), ...rows]); setCopied(false); setCopyFailed(false);
      setId(crypto.randomUUID());
    } catch (cause) {
      if (controller.signal.aborted) return;
      if (cause instanceof KeyRequestError && cause.status === 400) setError(t("Check the key name and try again."));
      else { setUncertain(id); setFresh(false); setError(t("Could not confirm key creation. Refresh the list before doing anything else.")); }
    } finally { activeRequest.current = false; if (!controller.signal.aborted) setBusy(false); }
  }
  async function revoke(keyId: string) {
    const controller = lifetime.current;
    if (!controller || activeRequest.current || !fresh) return;
    activeRequest.current = true; setBusy(true); setError(null);
    try {
      await revokeConsoleKey(keyId, controller.signal);
      if (controller.signal.aborted) return;
      setKeys((rows) => rows.map((row) => row.id === keyId ? { ...row, revoked_at: new Date().toISOString() } : row));
      setConfirm(null);
      if (uncertain === keyId) { setUncertain(null); setId(crypto.randomUUID()); }
      if (issued?.id === keyId) setIssued(null);
    } catch {
      if (!controller.signal.aborted) { setFresh(false); setError(t("Could not confirm revocation. Refresh the list to check its status.")); }
    } finally { activeRequest.current = false; if (!controller.signal.aborted) setBusy(false); }
  }
  async function copy() {
    const controller = lifetime.current;
    if (!issued || !controller) return;
    try { await navigator.clipboard.writeText(issued.key); if (!controller.signal.aborted) { setCopied(true); setCopyFailed(false); } }
    catch { if (!controller.signal.aborted) setCopyFailed(true); }
  }
  return <section className="api-key-panel" aria-label={t("API keys")}>
    <header><div className="api-key-heading"><KeyRound size={18} strokeWidth={1.5} /><h3>{t("Create a key for your API requests.")}</h3></div>
      <p>{t("Use this key when calling Agent API from your own machine or application.")}</p></header>
    <p className="api-key-caption">{t("Your Web password is for signing in. This key is for API requests.")}</p>
    {issued ? <div className="api-key-secret" role="status"><strong>{t("Keep this key somewhere safe. It is shown only now.")}</strong>
      <input aria-label={t("Your new API key")} value={issued.key} readOnly spellCheck={false} autoComplete="off" onFocus={(event) => event.target.select()} />
      <div className="api-key-actions"><button type="button" className="button outline" onClick={() => void copy()}>{copied ? <Check size={14} /> : <Copy size={14} />}{t(copied ? "Copied" : "Copy key")}</button>
        <button type="button" className="button primary" onClick={() => setIssued(null)}>{t("I've saved this key")}</button></div>
      {copyFailed ? <p role="alert">{t("Select the key and copy it manually.")}</p> : null}</div> :
      <form className="api-key-create" onSubmit={(event) => void create(event)}><label className="field"><span>{t("Key name")}</span><input value={name} onChange={(event) => setName(event.target.value)} maxLength={80} required disabled={busy || Boolean(uncertain)} /></label>
        <button type="submit" className="button primary" disabled={busy || loading || !fresh || Boolean(uncertain) || !name.trim()}><Plus size={14} />{t(busy ? "Creating key…" : "Create API key")}</button></form>}
    {error ? <p className="api-key-error" role="alert">{error}</p> : null}
    {uncertain && keys.some((key) => key.id === uncertain) ? <p className="api-key-error" role="alert">{t("This key was created, but its secret cannot be shown again. Revoke it and create a new key.")}</p> : null}
    {uncertain && fresh && !keys.some((key) => key.id === uncertain) ? <button type="button" className="button outline" onClick={() => { setUncertain(null); setError(null); }}>{t("Retry this creation")}</button> : null}
    <div className="api-key-list-heading"><span>{t(hasKey ? "API key ready" : "API keys")}</span><button type="button" className="icon-button" aria-label={t("Refresh keys")} disabled={busy || loading} onClick={() => setRevision((current) => current + 1)}><RefreshCw size={14} /></button></div>
    {loading ? <p role="status">{t("Loading API keys…")}</p> : null}
    {!keys.length && fresh ? <p className="api-key-caption">{t("No API keys yet. Create one to get started.")}</p> : null}
    <ul className="api-key-list">{keys.map((key) => <li key={key.id}><div><strong>{key.name}</strong><span><code>{key.prefix}…</code> · {t(key.revoked_at ? "Revoked" : "Active")}</span></div>
      {!key.revoked_at ? <button className="first-run-text-button" type="button" disabled={busy || !fresh} onClick={() => setConfirm(key.id)}>{t("Revoke")}</button> : null}
      {confirm === key.id ? <div className="api-key-confirm"><p>{t("Revoke this API key? Requests using it will stop working.")}</p><div className="api-key-actions"><button className="button danger" type="button" disabled={busy || !fresh} onClick={() => void revoke(key.id)}>{t("Confirm revocation")}</button><button className="button outline" type="button" disabled={busy} onClick={() => setConfirm(null)}>{t("Cancel")}</button></div></div> : null}</li>)}</ul>
  </section>;
}
