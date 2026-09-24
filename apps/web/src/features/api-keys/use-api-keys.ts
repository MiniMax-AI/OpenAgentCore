import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { sandboxConsoleConfig } from "../sandbox/console-config";
import { createConsoleKey, KeyRequestError, listConsoleKeys, revokeConsoleKey, safeKey, type ConsoleAPIKey, type IssuedConsoleAPIKey } from "./api-keys";

export type ApiKeyCapability = "loading" | "available" | "unavailable" | "error";

/** Whether this console can manage keys (`/console/config`). */
export function useApiKeyCapability(): { capability: ApiKeyCapability; retry: () => void } {
  const [capability, setCapability] = useState<ApiKeyCapability>("loading");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setCapability("loading");
    void sandboxConsoleConfig(controller.signal).then((config) => {
      if (controller.signal.aborted) return;
      setCapability(config?.api_keys === true ? "available" : config?.api_keys === false ? "unavailable" : "error");
    });
    return () => controller.abort();
  }, [revision]);
  return { capability, retry: () => setRevision((value) => value + 1) };
}

/**
 * Key management shared by the settings page and the introduction. A creation
 * whose outcome is unknown is never retried automatically: the list must be
 * refreshed first, and the issued secret stays on screen until dismissed.
 */
export function useManagedApiKeys() {
  const { t } = useTranslation("firstRun");
  const defaultName = t("My API key");
  const [keys, setKeys] = useState<ConsoleAPIKey[]>([]);
  const [name, setNameValue] = useState<string>(defaultName);
  const nameEdited = useRef(false);
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
  useEffect(() => { if (!nameEdited.current) setNameValue(defaultName); }, [defaultName]);
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

  async function create(event?: FormEvent) {
    event?.preventDefault();
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

  return {
    keys,
    hasKey: keys.some((key) => !key.revoked_at),
    name,
    setName: (value: string) => { nameEdited.current = true; setNameValue(value); },
    issued,
    dismissIssued: () => setIssued(null),
    loading,
    busy,
    error,
    fresh,
    uncertain,
    /** The uncertain creation reached Core; its secret is gone and it must be revoked. */
    uncertainListed: uncertain !== null && keys.some((key) => key.id === uncertain),
    /** Allowed only after a fresh list shows the uncertain creation did not happen. */
    canRetryCreation: uncertain !== null && fresh && !keys.some((key) => key.id === uncertain),
    retryCreation: () => { setUncertain(null); setError(null); },
    confirm,
    setConfirm,
    copied,
    copyFailed,
    copy,
    create,
    revoke,
    refresh: () => setRevision((current) => current + 1),
  };
}

export type ManagedApiKeys = ReturnType<typeof useManagedApiKeys>;
