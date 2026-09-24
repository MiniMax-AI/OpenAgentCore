import { Check, Copy, Plus } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { EmptyState, PageBody, PageHeader, RefreshButton, StatusDot } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { formatRelative } from "../../lib/format";
import { useApiKeyCapability, useManagedApiKeys, type ManagedApiKeys } from "./use-api-keys";
import "./api-keys.css";

/** Access › API keys: the keys callers use for the Agents API. */
export function ApiKeysView() {
  const { t } = useTranslation("firstRun");
  const { capability, retry } = useApiKeyCapability();
  if (capability === "available") return <ManagedApiKeysView />;
  return (
    <ApiKeysPage>
      {capability === "unavailable" ? (
        <EmptyState title={t("This console cannot manage API keys")} description={t("Use a key supplied by your Core administrator.")} />
      ) : capability === "loading" ? (
        <p className="detail-note" role="status">{t("Checking API key management…")}</p>
      ) : (
        <EmptyState
          title={t("Could not check API key management. Try again.")}
          action={<button type="button" className="button outline" onClick={retry}>{t("Try again")}</button>}
        />
      )}
    </ApiKeysPage>
  );
}

function ApiKeysPage({ actions, children }: { actions?: ReactNode; children: ReactNode }) {
  const { t } = useTranslation("firstRun");
  const { t: tApp } = useTranslation("app");
  return (
    <section className="page-section console-page api-keys-page" aria-labelledby="api-keys-heading">
      <PageHeader headingId="api-keys-heading" title={t("API keys")} help={tApp("apiKeysDescription")} actions={actions} />
      <PageBody>{children}</PageBody>
    </section>
  );
}

function ManagedApiKeysView() {
  const { t } = useTranslation("firstRun");
  const keys = useManagedApiKeys();
  const [dialogOpen, setDialogOpen] = useState(false);
  const { issued, busy, loading, fresh, uncertain } = keys;
  const locked = busy || loading || !fresh || Boolean(uncertain) || Boolean(issued);
  // Closing the dialog never discards a key shown only once: it stays on the
  // page until the operator confirms it was saved.
  const closeDialog = () => { if (!busy) setDialogOpen(false); };
  return (
    <ApiKeysPage
      actions={<>
        <RefreshButton onClick={keys.refresh} refreshing={loading} disabled={busy} label={t("Refresh keys")} />
        <button type="button" className="button primary" disabled={locked || dialogOpen} onClick={() => setDialogOpen(true)}>
          <Plus size={14} aria-hidden="true" />{t("Create API key")}
        </button>
      </>}
    >
      {issued && !dialogOpen ? <IssuedKey keys={keys} /> : null}
      {keys.error && !dialogOpen ? <p className="coverage-note coverage-note-error" role="alert">{keys.error}</p> : null}
      {keys.uncertainListed ? <p className="coverage-note coverage-note-error" role="alert">{t("This key was created, but its secret cannot be shown again. Revoke it and create a new key.")}</p> : null}
      {keys.canRetryCreation ? (
        <div><button type="button" className="button outline" onClick={keys.retryCreation}>{t("Retry this creation")}</button></div>
      ) : null}
      <KeyTable keys={keys} />
      <CreateKeyDialog keys={keys} open={dialogOpen} onClose={closeDialog} />
    </ApiKeysPage>
  );
}

function CreateKeyDialog({ keys, open, onClose }: { keys: ManagedApiKeys; open: boolean; onClose: () => void }) {
  const { t } = useTranslation("firstRun");
  const disabled = keys.busy || keys.loading || !keys.fresh || Boolean(keys.uncertain);
  if (keys.issued) {
    return (
      <Modal
        open={open}
        title={t("API key ready")}
        onClose={onClose}
        footer={<button type="button" className="button primary" onClick={() => { keys.dismissIssued(); onClose(); }}>{t("I've saved this key")}</button>}
      >
        <IssuedKeyBody keys={keys} />
      </Modal>
    );
  }
  return (
    <Modal
      open={open}
      title={t("Create API key")}
      onClose={onClose}
      footer={<>
        <button type="button" className="button outline" disabled={keys.busy} onClick={onClose}>{t("Cancel")}</button>
        <button type="submit" form="api-key-create-form" className="button primary" disabled={disabled || !keys.name.trim()}>{t(keys.busy ? "Creating key…" : "Create API key")}</button>
      </>}
    >
      <form id="api-key-create-form" className="api-key-dialog-form" onSubmit={(event) => void keys.create(event)}>
        <label className="field">
          <span>{t("Key name")}</span>
          <input value={keys.name} onChange={(event) => keys.setName(event.target.value)} maxLength={80} required disabled={disabled} />
        </label>
        {keys.error ? <p className="api-key-dialog-error" role="alert">{keys.error}</p> : null}
      </form>
    </Modal>
  );
}

function IssuedKeyBody({ keys }: { keys: ManagedApiKeys }) {
  const { t } = useTranslation("firstRun");
  if (!keys.issued) return null;
  return (
    <div className="api-key-issued-body" role="status">
      <p>{t("Keep this key somewhere safe. It is shown only now.")}</p>
      <div className="api-key-issued-row">
        <input aria-label={t("Your new API key")} value={keys.issued.key} readOnly spellCheck={false} autoComplete="off" onFocus={(event) => event.target.select()} />
        <button type="button" className="button outline" onClick={() => void keys.copy()}>
          {keys.copied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}{t(keys.copied ? "Copied" : "Copy key")}
        </button>
      </div>
      {keys.copyFailed ? <p role="alert">{t("Select the key and copy it manually.")}</p> : null}
    </div>
  );
}

function IssuedKey({ keys }: { keys: ManagedApiKeys }) {
  const { t } = useTranslation("firstRun");
  if (!keys.issued) return null;
  return (
    <div className="api-key-panel-inline api-key-issued">
      <IssuedKeyBody keys={keys} />
      <div><button type="button" className="button primary" onClick={keys.dismissIssued}>{t("I've saved this key")}</button></div>
    </div>
  );
}

function KeyTable({ keys }: { keys: ManagedApiKeys }) {
  const { t, i18n } = useTranslation("firstRun");
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1000);
  if (!keys.keys.length) {
    if (keys.fresh) return <EmptyState title={t("No API keys yet")} description={t("Create a key so callers can reach the Agents API.")} />;
    return keys.loading ? <p className="detail-note" role="status">{t("Loading API keys…")}</p> : null;
  }
  return (
    <div className="table-frame">
      <table className="data-table api-key-table">
        <thead>
          <tr>
            <th scope="col">{t("Name")}</th>
            <th scope="col">{t("Key")}</th>
            <th scope="col">{t("Status")}</th>
            <th scope="col">{t("Created")}</th>
            <th scope="col"><span className="visually-hidden">{t("Actions")}</span></th>
          </tr>
        </thead>
        <tbody>
          {keys.keys.map((key) => {
            const revoked = Boolean(key.revoked_at);
            const confirming = keys.confirm === key.id;
            return [
              <tr key={key.id} className={revoked ? "api-key-revoked" : undefined}>
                <td><strong>{key.name}</strong></td>
                <td><code>{key.prefix}…</code></td>
                <td title={key.revoked_at ? new Date(key.revoked_at).toLocaleString(locale) : undefined}>
                  <StatusDot tone={revoked ? "neutral" : "ok"} label={t(revoked ? "Revoked" : "Active")} />
                </td>
                <td title={new Date(key.created_at).toLocaleString(locale)}>{formatRelative(Date.parse(key.created_at) / 1000, now, locale)}</td>
                <td className="numeric">
                  {!revoked && !confirming ? (
                    <button className="text-action" type="button" disabled={keys.busy || !keys.fresh} onClick={() => keys.setConfirm(key.id)}>{t("Revoke")}</button>
                  ) : null}
                </td>
              </tr>,
              confirming ? (
                <tr key={`${key.id}:confirm`} className="api-key-confirm-row">
                  <td colSpan={5}>
                    <div className="api-key-confirm">
                      <span>{t("Revoke this API key? Requests using it will stop working.")}</span>
                      <span className="api-key-actions">
                        <button className="button outline" type="button" disabled={keys.busy} onClick={() => keys.setConfirm(null)}>{t("Cancel")}</button>
                        <button className="button danger" type="button" disabled={keys.busy || !keys.fresh} onClick={() => void keys.revoke(key.id)}>{t("Confirm revocation")}</button>
                      </span>
                    </div>
                  </td>
                </tr>
              ) : null,
            ];
          })}
        </tbody>
      </table>
    </div>
  );
}
