import type { Vault, VaultCredential } from "@oac/agents-client";
import { ArrowLeft, Trash2, Vault as VaultIcon } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { useFailureToast } from "../../components/Toast";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, Section } from "../../components/console-ui";
import { CopyableId, ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { useDeleteFlow } from "../../lib/delete-flow";
import { formatDateTime } from "../../lib/format";
import { CreatorCell, CreatorHeading, forgetCreators, ProjectFilter, ProjectName, projectClient, readAllPages, useCreators, useProjectCollection, useProjects, type Owned } from "../../lib/projects";
import { vaultName } from "./vault-catalog";
import "./vaults.css";
import { collections } from "../../lib/queries";
import { DetailSkeleton, TableSkeleton } from "../../components/Skeleton";
import { useVaultDetail } from "../resources/detail-queries";

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Resources › Vault: every project's Vaults and their Credential metadata.
 * Tokens are write-only and never reach the console; administrators view and
 * delete (for example a leaked Credential), but never create or replace
 * Credentials on a project's behalf.
 */
export function VaultsPage() {
  const { params } = useConsoleNavigation();
  if (params.project && params.id) return <VaultDetail key={`${params.project}:${params.id}`} projectId={params.project} vaultId={params.id} />;
  return <VaultsList />;
}

function VaultsList() {
  const { t, i18n } = useTranslation("vaults");
  const { t: tPages } = useTranslation("pages");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const { params, navigate } = useConsoleNavigation();
  const { byId } = useProjects();
  const [filter, setFilter] = useState(params.project ?? "");
  const [query, setQuery] = useState("");
  const collection = useProjectCollection(collections.vaults, filter);
  const rows = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    return collection.items
      .filter((row) => !needle || [row.value.name ?? "", row.value.id].some((value) => value.toLocaleLowerCase().includes(needle)))
      .sort((a, b) => b.value.created_at - a.value.created_at);
  }, [collection.items, query]);
  const creators = useCreators("vault", useMemo(() => collection.items.map((row) => ({ projectId: row.project.id, id: row.value.id })), [collection.items]));
  const refresh = useCallback(() => { forgetCreators(); collection.refresh(); }, [collection]);
  const remove = useDeleteFlow<Owned<Vault>>(
    useCallback((row: Owned<Vault>) => projectClient(row.project.id).deleteVault(row.value.id), []),
    refresh,
    { uncertain: tCommon("list.deleteUncertain") },
  );
  const showProject = !filter;

  // Projects that could not be read are reported in a toast; the list shows the rest.
  const failedNames = collection.failures.map((failure) => failure.project.name).join(", ");
  useFailureToast(collection.items.length > 0 && collection.failures.length > 0, tCommon("project.partial", { names: failedNames }), "vaults-partial");
  let body;
  if (collection.status === "loading" && !collection.items.length) {
    body = <TableSkeleton label={t("loading")} columns={6} />;
  } else if (!collection.items.length && !collection.failures.length) {
    body = <EmptyState icon={VaultIcon} title={t("noVaults")} />;
  } else if (!collection.items.length) {
    body = <EmptyState title={tCommon("project.failed", { names: failedNames })} description={collection.failures[0]?.message} action={<button className="button outline" type="button" onClick={collection.refresh}>{tCommon("actions.retry")}</button>} />;
  } else {
    body = (
      <>
        <ListToolbar label={t("list.filterLabel")} summary={listSummary(tCommon, rows.length, collection.items.length, { locale })}>
          <ProjectFilter value={filter} onChange={setFilter} />
          <SearchField value={query} onChange={setQuery} placeholder={t("list.filterPlaceholder")} label={t("list.filterLabel")} />
        </ListToolbar>
        {rows.length ? (
          <div className="table-frame">
            <table className="data-table" aria-label={t("list.label")}>
              <thead>
                <tr>
                  <th scope="col">{t("list.name")}</th>
                  {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
                  <th scope="col">{t("list.created")}</th>
                  <th scope="col"><CreatorHeading /></th>
                  <th scope="col"><span className="visually-hidden">{t("list.actions")}</span></th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const vault = row.value;
                  const name = vaultName(vault);
                  const open = () => navigate("vaults", { project: row.project.id, id: vault.id });
                  return (
                    <tr key={`${row.project.id}:${vault.id}`} className="clickable-row" onClick={open}>
                      <th scope="row"><NameCell name={vault.name} id={vault.id} fallback={name} onOpen={open} openLabel={t("list.open", { name })} /></th>
                      {showProject ? <td><ProjectName project={byId.get(row.project.id) ?? row.project} /></td> : null}
                      <td className="vault-nowrap">{formatDateTime(vault.created_at, locale)}</td>
                      <td><CreatorCell creator={creators.creatorOf(row.project.id, vault.id)} /></td>
                      <td className="actions-cell" onClick={(event) => event.stopPropagation()}>
                        <RowActions>
                          <button className="text-action danger" type="button" aria-label={t("deleteVaultLabel", { name })} onClick={() => remove.ask(row)}>{t("delete")}</button>
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
            action={<button className="button outline" type="button" onClick={() => setQuery("")}>{tCommon("actions.clearSearch")}</button>}
          />
        )}
      </>
    );
  }

  return (
    <section className="page-section console-page vaults-page" aria-labelledby="vaults-heading">
      <PageHeader
        headingId="vaults-heading"
        title={tPages("vaults.title")}
        help={<>{tPages("vaults.subtitle")} {t("securityNote")}</>}
        actions={<RefreshButton onClick={refresh} refreshing={collection.status === "loading"} label={tPages("vaults.refresh")} />}
      />
      <PageBody>{body}</PageBody>
      <ConfirmDialog
        open={remove.target !== null}
        title={t("deleteVaultTitle")}
        confirmLabel={t("delete")}
        busyLabel={t("deleting")}
        busy={remove.busy}
        error={remove.error}
        onConfirm={() => void remove.confirm()}
        onClose={remove.cancel}
      >
        <p>{remove.target ? t("deleteVaultPrompt", { name: vaultName(remove.target.value) }) : null}</p>
      </ConfirmDialog>
    </section>
  );
}

function VaultDetail({ projectId, vaultId }: { projectId: string; vaultId: string }) {
  const { t, i18n } = useTranslation("vaults");
  const { t: tPages } = useTranslation("pages");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const { navigate, back: goBack } = useConsoleNavigation();
  const { byId } = useProjects();
  const project = byId.get(projectId);
  // The Vault opens from the cache (or its list row) at once; its Credentials read beside it.
  const { read, credentials: credentialsRead, forget } = useVaultDetail(projectId, vaultId);
  const back = useCallback(() => goBack("vaults", { project: projectId }), [goBack, projectId]);

  const { refetch: refetchVault } = read;
  const { refetch: refetchCredentials } = credentialsRead;
  const refresh = useCallback(() => { forgetCreators(); void refetchVault(); void refetchCredentials(); }, [refetchVault, refetchCredentials]);
  const refreshCredentials = useCallback(() => { forgetCreators(); void refetchCredentials(); }, [refetchCredentials]);
  const credentials = useMemo(() => credentialsRead.data ?? [], [credentialsRead.data]);
  const vaultCreators = useCreators("vault", useMemo(() => [{ projectId, id: vaultId }], [projectId, vaultId]));
  const creators = useCreators("credential", useMemo(() => credentials.map((credential) => ({ projectId, id: credential.id })), [credentials, projectId]));
  const removeVault = useDeleteFlow<Vault>(
    useCallback((vault: Vault) => projectClient(projectId).deleteVault(vault.id), [projectId]),
    useCallback(() => { back(); forget(); }, [back, forget]),
    { uncertain: tCommon("list.deleteUncertain"), reread: refresh },
  );
  const removeCredential = useDeleteFlow<VaultCredential>(
    useCallback((credential: VaultCredential) => projectClient(projectId).deleteVaultCredential(vaultId, credential.id), [projectId, vaultId]),
    refreshCredentials,
    { uncertain: tCommon("list.deleteUncertain") },
  );

  const vault = read.data ?? null;
  const failure = read.isError ? errorText(read.error) : null;
  const credentialsFailure = credentialsRead.isError ? errorText(credentialsRead.error) : null;
  // Detail reads are not polled: each failed read was asked for and is reported.
  useFailureToast(vault !== null ? failure : null, t("refreshFailed"), "vault-refresh", read.errorUpdatedAt);
  useFailureToast(credentialsRead.data ? credentialsFailure : null, t("refreshFailed"), "vault-credentials-refresh", credentialsRead.errorUpdatedAt);
  const name = vault ? vaultName(vault) : vaultId;
  return (
    <section className="page-section console-page vaults-page" aria-labelledby="vault-detail-heading">
      <PageHeader
        headingId="vault-detail-heading"
        title={(
          <>
            <button type="button" className="icon-button ghost back-button" aria-label={t("detail.back")} title={t("detail.back")} onClick={back}>
              <ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" />
            </button>
            {name}
          </>
        )}
        actions={(
          <>
            <RefreshButton onClick={refresh} refreshing={read.isFetching || credentialsRead.isFetching} label={tPages("vaults.refresh")} />
            <button className="button danger" type="button" disabled={!vault} aria-label={t("deleteVaultLabel", { name })} onClick={() => { if (vault) removeVault.ask(vault); }}>
              <Trash2 size={14} aria-hidden="true" />{t("detail.deleteVault")}
            </button>
          </>
        )}
      />
      <PageBody>
        {!vault && read.isPending ? <DetailSkeleton label={t("loading")} /> : null}
        {!vault && failure !== null ? (
          <EmptyState title={t("loadFailed")} description={failure} action={<button className="button outline" type="button" onClick={refresh}>{tCommon("actions.retry")}</button>} />
        ) : null}
        {vault ? (
          <>
            <dl className="resource-facts" aria-label={t("detail.facts")}>
              <div><dt>{t("detail.id")}</dt><dd><CopyableId id={vault.id} /></dd></div>
              <div><dt>{tCommon("project.column")}</dt><dd><ProjectName project={project} /></dd></div>
              <div><dt>{t("detail.created")}</dt><dd>{formatDateTime(vault.created_at, locale)}</dd></div>
              <div><dt><CreatorHeading /></dt><dd><CreatorCell creator={vaultCreators.creatorOf(projectId, vault.id)} /></dd></div>
              {Object.keys(vault.metadata).length ? (
                <div>
                  <dt>{t("detail.metadata")}</dt>
                  <dd className="vault-metadata">{Object.entries(vault.metadata).map(([key, value]) => <code key={key}>{key}={value}</code>)}</dd>
                </div>
              ) : null}
            </dl>
            <Section
              headingId="vault-credentials-heading"
              title={<>{t("detail.credentials")} {credentialsRead.data ? <span className="heading-count">{credentials.length}</span> : null}</>}
              help={<>{t("detail.credentialsHelp")} {t("oauthHelp")}</>}
            >
              {!credentialsRead.data ? (
                credentialsFailure !== null ? (
                  <EmptyState title={t("loadFailed")} description={credentialsFailure} action={<button className="button outline" type="button" onClick={refreshCredentials}>{tCommon("actions.retry")}</button>} />
                ) : <TableSkeleton label={t("loading")} rows={3} columns={5} />
              ) : credentials.length ? (
                <div className="table-frame">
                  <table className="data-table" aria-label={t("credentialsIn", { name })}>
                    <thead>
                      <tr>
                        <th scope="col">{t("detail.name")}</th>
                        <th scope="col">{t("detail.url")}</th>
                        <th scope="col">{t("detail.auth")}</th>
                        <th scope="col">{t("detail.updated")}</th>
                        <th scope="col"><CreatorHeading /></th>
                        <th scope="col"><span className="visually-hidden">{t("list.actions")}</span></th>
                      </tr>
                    </thead>
                    <tbody>
                      {credentials.map((credential) => (
                        <tr key={credential.id}>
                          <th scope="row"><NameCell name={credential.name} id={credential.id} /></th>
                          <td><code className="vault-url" title={credential.auth.mcp_server_url}>{credential.auth.mcp_server_url}</code></td>
                          <td className="vault-nowrap">
                            {credential.auth.type === "static_bearer" ? t("detail.staticBearer") : <span className="column-help">{t("detail.oauth")}<HelpTip>{t("oauthHelp")}</HelpTip></span>}
                          </td>
                          <td className="vault-nowrap">{formatDateTime(credential.updated_at, locale)}</td>
                          <td><CreatorCell creator={creators.creatorOf(projectId, credential.id)} /></td>
                          <td className="actions-cell">
                            <RowActions>
                              <button className="text-action danger" type="button" aria-label={t("deleteCredentialLabel", { name: credential.name })} onClick={() => removeCredential.ask(credential)}>
                                {t("delete")}
                              </button>
                            </RowActions>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : <EmptyState title={t("noCredentials")} />}
            </Section>
          </>
        ) : null}
      </PageBody>
      <ConfirmDialog
        open={removeVault.target !== null}
        title={t("deleteVaultTitle")}
        confirmLabel={t("delete")}
        busyLabel={t("deleting")}
        busy={removeVault.busy}
        error={removeVault.error}
        onConfirm={() => void removeVault.confirm()}
        onClose={removeVault.cancel}
      >
        <p>{t("deleteVaultPrompt", { name })}</p>
      </ConfirmDialog>
      <ConfirmDialog
        open={removeCredential.target !== null}
        title={t("deleteCredentialTitle")}
        confirmLabel={t("delete")}
        busyLabel={t("deleting")}
        busy={removeCredential.busy}
        error={removeCredential.error}
        onConfirm={() => void removeCredential.confirm()}
        onClose={removeCredential.cancel}
      >
        <p>{removeCredential.target ? t("deleteCredentialPrompt", { name: removeCredential.target.name }) : null}</p>
      </ConfirmDialog>
    </section>
  );
}
