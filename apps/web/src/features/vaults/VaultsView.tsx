import { KeyRound, Plus, RefreshCw, RotateCcw, ShieldCheck, Trash2, Vault as VaultIcon } from "lucide-react";
import { useMemo, useState } from "react";

import type { Vault, VaultCredential } from "@agents-core-web/agents-client";

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
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" })
    .format(new Date(seconds * 1000));
}

function mutationError(): string {
  return "The write outcome was not confirmed. The catalog was refreshed; review current Core state before explicitly trying again.";
}

function VaultsLoadingSkeleton() {
  return (
    <div className="vault-grid" aria-busy="true" aria-label="Loading Vaults">
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
        <div>
          <h1>Vaults</h1>
          <p className="page-subtitle">Core-owned static bearer credentials for exact HTTPS MCP destinations.</p>
        </div>
        <div className="page-actions">
          <button className="icon-button outline" type="button" onClick={operations.refresh} disabled={coreState === "connecting" || busy} aria-label="Refresh Vaults">
            <RefreshCw className={coreState === "connecting" ? "refresh-spinning" : undefined} size={14} strokeWidth={1.5} />
          </button>
          <button className="button primary" type="button" onClick={() => { setActionError(null); setCreateOpen(true); }} disabled={coreState !== "ready" || busy}>
            <Plus size={14} strokeWidth={1.5} /> New Vault
          </button>
        </div>
      </header>

      <div className="vaults-content">
        <div className="notice warning vault-security-note" role="note">
          <ShieldCheck size={15} aria-hidden="true" />
          <span>Tokens are write only. This page receives Credential metadata, never token values. Deleting a Vault also deletes all of its stored Credentials and does not revoke provider-side tokens.</span>
        </div>

        {actionError ? <div className="session-action-error" role="alert"><strong>Action not confirmed</strong><span>{actionError}</span></div> : null}
        {coreState === "connecting" && !catalog ? <VaultsLoadingSkeleton /> : null}
        {coreState === "failed" ? (
          <ErrorState
            title={catalog ? "Couldn’t refresh Vaults" : "Couldn’t load Vaults"}
            description={catalog ? "The previous safe metadata snapshot remains visible, but it cannot authorize new Sessions." : "The Web could not load the complete Vault and Credential catalog."}
            detail={coreError ?? undefined}
            hint="Check the Core connection, then refresh. Credentialed Sessions stay blocked until the complete catalog loads."
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
                      <button className="button outline" type="button" onClick={() => { setActionError(null); setCredentialDialog({ mode: "create", vault }); }} disabled={busy}><Plus size={13} /> Credential</button>
                      <button className="icon-button danger" type="button" aria-label={`Delete ${vaultName(vault)}`} onClick={() => { setActionError(null); setDeleteTarget({ kind: "vault", vault }); }} disabled={busy}><Trash2 size={14} /></button>
                    </div>
                  </header>
                  <div className="vault-metadata-row"><span>Created</span><time dateTime={new Date(vault.created_at * 1000).toISOString()}>{formatTimestamp(vault.created_at)}</time></div>
                  {Object.keys(vault.metadata).length ? <pre className="agent-structured-value">{JSON.stringify(vault.metadata, null, 2)}</pre> : null}
                  <section className="vault-credentials" aria-label={`Credentials in ${vaultName(vault)}`}>
                    <h3>Credentials <span>{credentials.length}</span></h3>
                    {credentials.length ? credentials.map((credential) => (
                      <article className="credential-row" key={credential.id}>
                        <KeyRound size={15} strokeWidth={1.5} aria-hidden="true" />
                        <div><strong>{credential.name}</strong><code>{credential.auth.mcp_server_url}</code><small>Updated {formatTimestamp(credential.updated_at)} · token hidden</small>{credential.auth.type === "mcp_oauth" ? <small>OAuth · Manage authorization and token replacement in your application.</small> : null}</div>
                        <div className="credential-actions">
                          {credential.auth.type === "static_bearer" ? <button className="icon-button outline" type="button" aria-label={`Replace token for ${credential.name}`} title="Replace token" onClick={() => { setActionError(null); setCredentialDialog({ mode: "replace", vault, credential }); }} disabled={busy}><RotateCcw size={13} /></button> : null}
                          <button className="icon-button danger" type="button" aria-label={`Delete ${credential.name}`} onClick={() => { setActionError(null); setDeleteTarget({ kind: "credential", vault, credential }); }} disabled={busy}><Trash2 size={13} /></button>
                        </div>
                      </article>
                    )) : <p className="vault-empty-credentials">No Credentials in this Vault.</p>}
                  </section>
                </article>
              );
            })}
          </div>
        ) : (
          <div className="empty-state"><VaultIcon size={24} strokeWidth={1.5} /><h2>No Vaults</h2><p>Create a Vault before adding a write-only static bearer Credential.</p><button className="button primary" type="button" onClick={() => setCreateOpen(true)} disabled={busy}>Create Vault</button></div>
        ) : null}
      </div>

      <Modal
        open={createOpen}
        onClose={() => { if (!busy) setCreateOpen(false); }}
        title="Create a Vault"
        footer={<button className="button outline" type="button" onClick={() => setCreateOpen(false)} disabled={busy}>Cancel</button>}
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
        title={deleteTarget?.kind === "vault" ? "Delete Vault?" : "Delete Credential?"}
        footer={<><button className="button outline" type="button" onClick={() => setDeleteTarget(null)} disabled={submitting}>Cancel</button><button className="button danger" type="button" onClick={() => void confirmDelete()} disabled={submitting}>{submitting ? "Deleting…" : "Delete"}</button></>}
      >
        {deleteTarget?.kind === "vault" ? <p>Delete <strong>{vaultName(deleteTarget.vault)}</strong> and all of its stored Credentials? Existing Sessions keep frozen IDs, but later secret lookup fails. Provider tokens and running work are not revoked.</p> : deleteTarget ? <p>Delete <strong>{deleteTarget.credential.name}</strong>? Existing Sessions keep its frozen identity, but later secret lookup fails. This does not revoke the token at the MCP provider.</p> : null}
      </Modal>
    </section>
  );
}
