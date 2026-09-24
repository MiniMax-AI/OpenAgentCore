import { ArrowLeft, Plus, Trash2, Vault as VaultIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";

import type { Vault, VaultCredential } from "@agents-core-web/agents-client";

import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, Section } from "../../components/console-ui";
import { CopyableId, ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { OwnerCell, OwnerHeading } from "../ownership/OwnerCell";
import { forgetOwners, useOwners } from "../ownership/use-owners";
import { ErrorState } from "../../components/ErrorState";
import { Modal } from "../../components/Modal";
import { Skeleton } from "../../components/Skeleton";
import type { CoreConnectionState } from "../../lib/connection";
import { CredentialDialog } from "./CredentialDialog";
import { VaultCreateForm } from "./VaultCreateForm";
import type { VaultCatalog } from "./vault-catalog";
import { vaultName } from "./vault-catalog";
import type { VaultMetadata } from "./vault-metadata";
import "./vaults.css";

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
  /** Opens a Vault's detail directly (tests and deep links). */
  initialSelectedId?: string | null;
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
  // Same absolute format as every other resource table.
  return new Intl.DateTimeFormat(i18n.resolvedLanguage ?? "en", { dateStyle: "medium", timeStyle: "short" })
    .format(new Date(seconds * 1000));
}

function mutationError(): string {
  return i18n.t("mutationUncertain", { ns: "vaults" });
}

function VaultsLoadingSkeleton() {
  const { t } = useTranslation("vaults");
  return (
    <div className="table-frame" aria-busy="true" aria-label={t("loading")}>
      <div className="vault-skeleton">
        {Array.from({ length: 3 }).map((_, index) => <Skeleton key={index} />)}
      </div>
    </div>
  );
}

function filterVaults(vaults: readonly Vault[], query: string): Vault[] {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return [...vaults];
  return vaults.filter((vault) => [vault.name ?? "", vault.id].some((value) => value.toLocaleLowerCase().includes(needle)));
}

export function VaultsView({ busy, catalog, coreError, coreState, operations, initialSelectedId = null }: VaultsViewProps) {
  const { t } = useTranslation("vaults");
  const { t: tPages } = useTranslation("pages");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage ?? "en";
  const count = (value: number) => new Intl.NumberFormat(locale).format(value);
  const [createOpen, setCreateOpen] = useState(false);
  const [credentialDialog, setCredentialDialog] = useState<CredentialDialogState>(null);
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [query, setQuery] = useState("");
  const [selectedId, setSelectedId] = useState<string | null>(initialSelectedId);
  const credentialsByVault = useMemo(() => {
    const result = new Map<string, VaultCredential[]>();
    for (const credential of catalog?.credentials ?? []) {
      const current = result.get(credential.vault_id) ?? [];
      current.push(credential);
      result.set(credential.vault_id, current);
    }
    return result;
  }, [catalog]);
  const vaults = useMemo(() => catalog?.vaults ?? [], [catalog]);
  const visible = filterVaults(vaults, query);
  const selected = selectedId ? vaults.find((vault) => vault.id === selectedId) ?? null : null;
  const selectedCredentials = selected ? credentialsByVault.get(selected.id) ?? [] : [];
  const vaultIds = useMemo(() => vaults.map((vault) => vault.id), [vaults]);
  const credentialIds = useMemo(() => selectedCredentials.map((credential) => credential.id), [selectedCredentials]);
  const vaultOwners = useOwners("vault", vaultIds, Boolean(catalog));
  const credentialOwners = useOwners("vault_credential", credentialIds, Boolean(selected));

  const refresh = () => {
    forgetOwners("vault");
    forgetOwners("vault_credential");
    operations.refresh();
  };
  const openCreateCredential = (vault: Vault) => { setActionError(null); setCredentialDialog({ mode: "create", vault }); };
  const openDeleteVault = (vault: Vault) => { setActionError(null); setDeleteTarget({ kind: "vault", vault }); };

  const confirmDelete = async () => {
    if (!deleteTarget || submitting) return;
    setSubmitting(true);
    setActionError(null);
    try {
      if (deleteTarget.kind === "vault") {
        await operations.deleteVault(deleteTarget.vault.id);
        if (selectedId === deleteTarget.vault.id) setSelectedId(null);
      } else {
        await operations.deleteCredential(deleteTarget.vault.id, deleteTarget.credential.id);
      }
      setDeleteTarget(null);
    } catch {
      setActionError(mutationError());
    } finally {
      setSubmitting(false);
    }
  };

  const status = (
    <>
      {actionError ? <div className="session-action-error" role="alert"><strong>{t("actionNotConfirmed")}</strong><span>{actionError}</span></div> : null}
      {coreState === "failed" ? (
        <ErrorState
          title={catalog ? t("refreshFailed") : t("loadFailed")}
          description={catalog ? t("staleDescription") : t("failedDescription")}
          detail={coreError ?? undefined}
          hint={t("failedHint")}
          onRetry={refresh}
        />
      ) : null}
    </>
  );

  let page;
  if (selected) {
    const name = vaultName(selected);
    const metadata = Object.entries(selected.metadata);
    page = (
      <section className="page-section console-page vaults-page" aria-labelledby="vault-detail-heading">
        <PageHeader
          headingId="vault-detail-heading"
          title={(
            <>
              <button type="button" className="icon-button ghost back-button" aria-label={t("detail.back")} title={t("detail.back")} onClick={() => setSelectedId(null)}>
                <ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" />
              </button>
              {name}
            </>
          )}
          actions={(
            <>
              <RefreshButton onClick={refresh} refreshing={coreState === "connecting"} disabled={busy} label={tPages("vaults.refresh")} />
              <button className="button outline" type="button" onClick={() => openCreateCredential(selected)} disabled={busy || coreState !== "ready"}>
                <Plus size={14} aria-hidden="true" />{t("detail.addCredential")}
              </button>
              <button className="button danger" type="button" aria-label={t("deleteVaultLabel", { name })} onClick={() => openDeleteVault(selected)} disabled={busy}>
                <Trash2 size={14} aria-hidden="true" />{t("detail.deleteVault")}
              </button>
            </>
          )}
        />
        <PageBody>
          {status}
          <dl className="resource-facts" aria-label={t("detail.facts")}>
            <div><dt>{t("detail.id")}</dt><dd><CopyableId id={selected.id} /></dd></div>
            <div><dt>{t("detail.created")}</dt><dd>{formatTimestamp(selected.created_at)}</dd></div>
            {vaultOwners.available ? <div><dt><OwnerHeading /></dt><dd><OwnerCell record={vaultOwners.ownerOf(selected.id)} /></dd></div> : null}
            {metadata.length ? (
              <div>
                <dt>{t("detail.metadata")}</dt>
                <dd className="vault-metadata">{metadata.map(([key, value]) => <code key={key}>{key}={value}</code>)}</dd>
              </div>
            ) : null}
          </dl>
          <Section
            headingId="vault-credentials-heading"
            title={<>{t("detail.credentials")} <span className="heading-count">{count(selectedCredentials.length)}</span></>}
            help={<>{t("detail.credentialsHelp")} {t("oauthHelp")}</>}
          >
            {selectedCredentials.length ? (
              <div className="table-frame">
                <table className="data-table" aria-label={t("credentialsIn", { name })}>
                  <thead>
                    <tr>
                      <th scope="col">{t("detail.name")}</th>
                      <th scope="col">{t("detail.url")}</th>
                      <th scope="col">{t("detail.auth")}</th>
                      <th scope="col">{t("detail.updated")}</th>
                      {credentialOwners.available ? <th scope="col"><OwnerHeading /></th> : null}
                      <th scope="col"><span className="visually-hidden">{t("list.actions")}</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {selectedCredentials.map((credential) => (
                      <tr key={credential.id}>
                        <th scope="row"><NameCell name={credential.name} id={credential.id} /></th>
                        <td><code className="vault-url" title={credential.auth.mcp_server_url}>{credential.auth.mcp_server_url}</code></td>
                        <td className="vault-nowrap">
                          {credential.auth.type === "static_bearer" ? t("detail.staticBearer") : <span className="column-help">{t("detail.oauth")}<HelpTip>{t("oauthHelp")}</HelpTip></span>}
                        </td>
                        <td className="vault-nowrap">{formatTimestamp(credential.updated_at)}</td>
                        {credentialOwners.available ? <td><OwnerCell record={credentialOwners.ownerOf(credential.id)} /></td> : null}
                        <td className="actions-cell">
                          <RowActions>
                            {credential.auth.type === "static_bearer" ? (
                              <button className="text-action" type="button" aria-label={t("replaceTokenLabel", { name: credential.name })} onClick={() => { setActionError(null); setCredentialDialog({ mode: "replace", vault: selected, credential }); }} disabled={busy}>
                                {t("replaceToken")}
                              </button>
                            ) : null}
                            <button className="text-action danger" type="button" aria-label={t("deleteCredentialLabel", { name: credential.name })} onClick={() => { setActionError(null); setDeleteTarget({ kind: "credential", vault: selected, credential }); }} disabled={busy}>
                              {t("delete")}
                            </button>
                          </RowActions>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <EmptyState
                title={t("noCredentials")}
                action={<button className="button outline" type="button" onClick={() => openCreateCredential(selected)} disabled={busy || coreState !== "ready"}>{t("detail.addCredential")}</button>}
              />
            )}
          </Section>
        </PageBody>
      </section>
    );
  } else {
    let body;
    if (coreState === "connecting" && !catalog) body = <VaultsLoadingSkeleton />;
    else if (!catalog || (coreState !== "ready" && !vaults.length)) body = null;
    else if (!vaults.length) {
      body = (
        <EmptyState
          icon={VaultIcon}
          title={t("noVaults")}
          description={t("noVaultsDescription")}
          action={<button className="button outline" type="button" onClick={() => setCreateOpen(true)} disabled={busy}>{t("createVault")}</button>}
        />
      );
    } else {
      body = (
        <>
          <ListToolbar label={t("list.filterLabel")} summary={listSummary(tCommon, visible.length, vaults.length, { locale })}>
            <SearchField value={query} onChange={setQuery} placeholder={t("list.filterPlaceholder")} label={t("list.filterLabel")} />
          </ListToolbar>
          {visible.length ? (
            <div className="table-frame">
              <table className="data-table" aria-label={t("list.label")}>
                <thead>
                  <tr>
                    <th scope="col">{t("list.name")}</th>
                    <th scope="col" className="numeric">{t("list.credentials")}</th>
                    <th scope="col">{t("list.created")}</th>
                    {vaultOwners.available ? <th scope="col"><OwnerHeading /></th> : null}
                    <th scope="col"><span className="visually-hidden">{t("list.actions")}</span></th>
                  </tr>
                </thead>
                <tbody>
                  {visible.map((vault) => {
                    const name = vaultName(vault);
                    return (
                      <tr key={vault.id} className="clickable-row" onClick={() => setSelectedId(vault.id)}>
                        <th scope="row">
                          <NameCell name={vault.name} id={vault.id} fallback={name} onOpen={() => setSelectedId(vault.id)} openLabel={t("list.open", { name })} />
                        </th>
                        <td className="numeric">{count((credentialsByVault.get(vault.id) ?? []).length)}</td>
                        <td className="vault-nowrap">{formatTimestamp(vault.created_at)}</td>
                        {vaultOwners.available ? <td><OwnerCell record={vaultOwners.ownerOf(vault.id)} /></td> : null}
                        <td className="actions-cell" onClick={(event) => event.stopPropagation()}>
                          <RowActions>
                            <button className="text-action" type="button" onClick={() => openCreateCredential(vault)} disabled={busy}>{t("detail.addCredential")}</button>
                            <button className="text-action danger" type="button" aria-label={t("deleteVaultLabel", { name })} onClick={() => openDeleteVault(vault)} disabled={busy}>{t("delete")}</button>
                          </RowActions>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <EmptyState
              title={t("list.noMatch")}
              description={tCommon("list.noMatchesDescription")}
              action={<button className="button outline" type="button" onClick={() => setQuery("")}>{tCommon("actions.clearSearch")}</button>}
            />
          )}
        </>
      );
    }
    page = (
      <section className="page-section console-page vaults-page" aria-labelledby="vaults-heading">
        <PageHeader
          headingId="vaults-heading"
          title={tPages("vaults.title")}
          help={<>{tPages("vaults.subtitle")} {t("securityNote")}</>}
          actions={(
            <>
              <RefreshButton onClick={refresh} refreshing={coreState === "connecting"} disabled={busy} label={tPages("vaults.refresh")} />
              <button className="button primary" type="button" onClick={() => { setActionError(null); setCreateOpen(true); }} disabled={coreState !== "ready" || busy}>
                <Plus size={14} aria-hidden="true" />{tPages("vaults.create")}
              </button>
            </>
          )}
        />
        <PageBody>
          {status}
          {body}
        </PageBody>
      </section>
    );
  }

  return (
    <>
      {page}
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
    </>
  );
}
