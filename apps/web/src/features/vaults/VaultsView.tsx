import { KeyRound, Plus, RotateCcw, ShieldCheck, Trash2, Vault as VaultIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";

import type { Vault, VaultCredential } from "@agents-core-web/agents-client";

import { HelpTip, RefreshButton } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { Modal } from "../../components/Modal";
import { Skeleton } from "../../components/Skeleton";
import type { CoreConnectionState } from "../../lib/connection";
import { CredentialDialog } from "./CredentialDialog";
import { VaultCreateForm } from "./VaultCreateForm";
import type { VaultCatalog } from "./vault-catalog";
import { vaultName } from "./vault-catalog";
import type { VaultMetadata } from "./vault-metadata";

export interface VaultOperations {
  createVault(name: string, metadata: VaultMetadata): Promise<void>;
  createCredential(vaultId: string, name: string, serverURL: string, token: string): Promise<void>;
  replaceCredential(vaultId: string, credentialId: string, token: string): Promise<void>;
  deleteCredential(vaultId: string, credentialId: string): Promise<void>;
  deleteVault(vaultId: string): Promise<void>;
  refresh(): void;
}

interface VaultsViewProps {
  busy: boolean;
  catalog: VaultCatalog | null;
  coreError: string | null;
  coreState: CoreConnectionState;
  operations: VaultOperations;
}

type CredentialDialogState =
  | { mode: "create"; vault: Vault }
  | { mode: "replace"; credential: VaultCredential; vault: Vault }
  | null;

type DeleteTarget =
  | { kind: "credential"; credential: VaultCredential; vault: Vault }
  | { kind: "vault"; vault: Vault }
  | null;

function formatTimestamp(seconds: number): string {
  return new Intl.DateTimeFormat(i18n.resolvedLanguage ?? "en", { dateStyle: "medium", timeStyle: "short" })
    .format(new Date(seconds * 1000));
}

function mutationError(): string {
  return i18n.t("mutationUncertain", { ns: "vaults" });
}

function VaultsLoadingSkeleton() {
  const { t } = useTranslation("vaults");
  return (
    <div className="vault-grid" aria-busy="true" aria-label={t("loading")}>
      {Array.from({ length: 3 }).map((_, index) => (
        <article className="vault-card" key={index}>
          <Skeleton className="skeleton-agent-name" />
          <Skeleton />
          <Skeleton className="skeleton-model" />
        </article>
      ))}
    </div>
  );
}

export function VaultsView({ busy, catalog, coreError, coreState, operations }: VaultsViewProps) {
  const { t } = useTranslation("vaults");
  const { t: tPages } = useTranslation("pages");
  const { t: tCommon } = useTranslation("common");
  const count = (value: number) => new Intl.NumberFormat(i18n.resolvedLanguage ?? "en").format(value);
  const [createOpen, setCreateOpen] = useState(false);
  const [credentialDialog, setCredentialDialog] = useState<CredentialDialogState>(null);
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const credentialsByVault = useMemo(() => {
    const result = new Map<string, VaultCredential[]>();
    for (const credential of catalog?.credentials ?? []) {
      const current = result.get(credential.vault_id) ?? [];
      current.push(credential);
      result.set(credential.vault_id, current);
    }
    return result;
  }, [catalog]);

  const confirmDelete = async () => {
    if (!deleteTarget || submitting) return;
    setSubmitting(true);
    setActionError(null);
    try {
      if (deleteTarget.kind === "vault") await operations.deleteVault(deleteTarget.vault.id);
      else await operations.deleteCredential(deleteTarget.vault.id, deleteTarget.credential.id);
      setDeleteTarget(null);
    } catch {
      setActionError(mutationError());
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="page-section vaults-page">
      <header className="page-header">
        <div className="console-page-heading">
          <h1>{tPages("vaults.title")}</h1>
          <HelpTip>{tPages("vaults.subtitle")}</HelpTip>
        </div>
        <div className="page-actions">
          <RefreshButton onClick={operations.refresh} refreshing={coreState === "connecting"} disabled={busy} label={tPages("vaults.refresh")} />
          <button className="button primary" type="button" onClick={() => { setActionError(null); setCreateOpen(true); }} disabled={coreState !== "ready" || busy}>
            <Plus size={14} strokeWidth={1.5} /> {tPages("vaults.create")}
          </button>
        </div>
      </header>

      <div className="vaults-content">
        <div className="notice warning vault-security-note" role="note">
          <ShieldCheck size={15} aria-hidden="true" />
          <span>{t("securityNote")}</span>
        </div>

        {actionError ? <div className="session-action-error" role="alert"><strong>{t("actionNotConfirmed")}</strong><span>{actionError}</span></div> : null}
        {coreState === "connecting" && !catalog ? <VaultsLoadingSkeleton /> : null}
        {coreState === "failed" ? (
          <ErrorState
            title={catalog ? t("refreshFailed") : t("loadFailed")}
            description={catalog ? t("staleDescription") : t("failedDescription")}
            detail={coreError ?? undefined}
            hint={t("failedHint")}
            onRetry={operations.refresh}
          />
        ) : null}

        {catalog && (coreState === "ready" || catalog.vaults.length > 0) ? catalog.vaults.length ? (
          <div className="vault-grid">
            {catalog.vaults.map((vault) => {
              const credentials = credentialsByVault.get(vault.id) ?? [];
              return (
                <article className="vault-card" key={vault.id}>
                  <header>
                    <div className="vault-card-title"><VaultIcon size={17} strokeWidth={1.5} aria-hidden="true" /><div><h2>{vaultName(vault)}</h2><code>{vault.id}</code></div></div>
                    <div className="vault-card-actions">
                      <button className="button outline" type="button" onClick={() => { setActionError(null); setCredentialDialog({ mode: "create", vault }); }} disabled={busy}><Plus size={13} /> {t("credential")}</button>
                      <button className="icon-button danger" type="button" aria-label={t("deleteVaultLabel", { name: vaultName(vault) })} onClick={() => { setActionError(null); setDeleteTarget({ kind: "vault", vault }); }} disabled={busy}><Trash2 size={14} /></button>
                    </div>
                  </header>
                  <div className="vault-metadata-row"><span>{t("created")}</span><time dateTime={new Date(vault.created_at * 1000).toISOString()}>{formatTimestamp(vault.created_at)}</time></div>
                  {Object.keys(vault.metadata).length ? <pre className="agent-structured-value">{JSON.stringify(vault.metadata, null, 2)}</pre> : null}
                  <section className="vault-credentials" aria-label={t("credentialsIn", { name: vaultName(vault) })}>
                    <h3>{t("credential")} <span>{count(credentials.length)}</span></h3>
                    {credentials.length ? credentials.map((credential) => (
                      <article className="credential-row" key={credential.id}>
                        <KeyRound size={15} strokeWidth={1.5} aria-hidden="true" />
                        <div><strong>{credential.name}</strong><code>{credential.auth.mcp_server_url}</code><small>{t("updatedHidden", { date: formatTimestamp(credential.updated_at) })}</small>{credential.auth.type === "mcp_oauth" ? <small>{t("oauthHelp")}</small> : null}</div>
                        <div className="credential-actions">
                          {credential.auth.type === "static_bearer" ? <button className="icon-button outline" type="button" aria-label={t("replaceTokenLabel", { name: credential.name })} title={t("replaceToken")} onClick={() => { setActionError(null); setCredentialDialog({ mode: "replace", vault, credential }); }} disabled={busy}><RotateCcw size={13} /></button> : null}
                          <button className="icon-button danger" type="button" aria-label={t("deleteCredentialLabel", { name: credential.name })} onClick={() => { setActionError(null); setDeleteTarget({ kind: "credential", vault, credential }); }} disabled={busy}><Trash2 size={13} /></button>
                        </div>
                      </article>
                    )) : <p className="vault-empty-credentials">{t("noCredentials")}</p>}
                  </section>
                </article>
              );
            })}
          </div>
        ) : (
          <div className="empty-state"><VaultIcon size={24} strokeWidth={1.5} /><h2>{t("noVaults")}</h2><p>{t("noVaultsDescription")}</p><button className="button primary" type="button" onClick={() => setCreateOpen(true)} disabled={busy}>{t("createVault")}</button></div>
        ) : null}
      </div>

      <Modal
        open={createOpen}
        onClose={() => { if (!busy) setCreateOpen(false); }}
        title={t("createVaultTitle")}
        footer={<button className="button outline" type="button" onClick={() => setCreateOpen(false)} disabled={busy}>{tCommon("actions.cancel")}</button>}
      >
        <VaultCreateForm
          disabled={busy || coreState !== "ready"}
          onCreate={async (name, metadata) => {
            await operations.createVault(name, metadata);
            setCreateOpen(false);
          }}
        />
      </Modal>

      <CredentialDialog
        key={credentialDialog ? `${credentialDialog.mode}:${credentialDialog.vault.id}:${credentialDialog.mode === "replace" ? credentialDialog.credential.id : "new"}` : "closed"}
        open={Boolean(credentialDialog)}
        credentialName={credentialDialog?.mode === "replace" ? credentialDialog.credential.name : undefined}
        onClose={() => setCredentialDialog(null)}
        onCreate={credentialDialog?.mode === "create" ? (name, url, token) => operations.createCredential(credentialDialog.vault.id, name, url, token) : undefined}
        onReplace={credentialDialog?.mode === "replace" ? (token) => operations.replaceCredential(credentialDialog.vault.id, credentialDialog.credential.id, token) : undefined}
      />

      <Modal
        open={Boolean(deleteTarget)}
        onClose={() => { if (!submitting) setDeleteTarget(null); }}
        title={deleteTarget?.kind === "vault" ? t("deleteVaultTitle") : t("deleteCredentialTitle")}
        footer={<><button className="button outline" type="button" onClick={() => setDeleteTarget(null)} disabled={submitting}>{tCommon("actions.cancel")}</button><button className="button danger" type="button" onClick={() => void confirmDelete()} disabled={submitting}>{submitting ? t("deleting") : t("delete")}</button></>}
      >
        {deleteTarget?.kind === "vault" ? <p>{t("deleteVaultPrompt", { name: vaultName(deleteTarget.vault) })}</p> : deleteTarget ? <p>{t("deleteCredentialPrompt", { name: deleteTarget.credential.name })}</p> : null}
      </Modal>
    </section>
  );
}
