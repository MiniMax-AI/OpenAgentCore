import type { PageOrder, SourceFileListEntry } from "@agents-core-web/agents-client";
import { FileText } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, SegmentedControl } from "../../components/console-ui";
import { ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { useDeleteFlow } from "../../lib/delete-flow";
import { formatBytes, formatDateTime, MISSING } from "../../lib/format";
import { CreatorCell, CreatorHeading, forgetCreators, ProjectFilter, ProjectName, projectClient, readAllPages, useCreators, useProjectCollection, useProjects, type Owned } from "../../lib/projects";
import { CopyDialog, type CopySource } from "../copy/CopyDialog";
import { filesPageSize, filterFiles, isUnrecognizedFile } from "./file-operations";
import "./files.css";
import { filesCollection } from "../../lib/queries";
import { TableSkeleton } from "../../components/Skeleton";

type Row = Owned<SourceFileListEntry>;

/** Resources › Files: every project's uploaded files, read-only apart from delete and copy. */
export function FilesPage() {
  const { t, i18n } = useTranslation("files");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const { params } = useConsoleNavigation();
  const { byId } = useProjects();
  const [filter, setFilter] = useState(params.project ?? "");
  const [query, setQuery] = useState(params.id ?? "");
  const [order, setOrder] = useState<PageOrder>("desc");
  const [copy, setCopy] = useState<CopySource | null>(null);

  const collection = useProjectCollection(useMemo(() => filesCollection(order, filesPageSize), [order]), filter);
  const rows = useMemo(() => {
    const visible = new Set(filterFiles(collection.items.map((row) => row.value), query));
    const matched = collection.items.filter((row) => visible.has(row.value));
    // Files from several keys interleave by creation time in the chosen order.
    return matched.sort((a, b) => {
      const at = isUnrecognizedFile(a.value) ? 0 : a.value.created_at;
      const bt = isUnrecognizedFile(b.value) ? 0 : b.value.created_at;
      return order === "desc" ? bt - at : at - bt;
    });
  }, [collection.items, order, query]);
  const showProject = !filter;
  const creators = useCreators("file", useMemo(() => collection.items.map((row) => ({ projectId: row.project.id, id: row.value.id })), [collection.items]));
  const name = (row: Row) => (isUnrecognizedFile(row.value) ? t("list.unrecognized") : row.value.filename);
  const refresh = useCallback(() => { forgetCreators(); collection.refresh(); }, [collection]);
  const remove = useDeleteFlow<Row>(
    useCallback((row: Row) => projectClient(row.project.id).deleteSourceFile(row.value.id), []),
    refresh,
    { uncertain: tCommon("list.deleteUncertain") },
  );

  let body;
  if (collection.status === "loading" && !collection.items.length) {
    body = <TableSkeleton label={t("list.loading")} columns={6} />;
  } else if (!collection.items.length && !collection.failures.length) {
    body = <EmptyState icon={FileText} title={t("empty.title")} />;
  } else {
    body = (
      <>
        <ListToolbar label={t("list.filterLabel")} summary={listSummary(tCommon, rows.length, collection.items.length, { locale })}>
          <ProjectFilter value={filter} onChange={setFilter} />
          <SearchField value={query} onChange={setQuery} placeholder={t("list.filterPlaceholder")} label={t("list.filterLabel")} />
          <SegmentedControl
            label={t("order.label")}
            value={order}
            options={[{ value: "desc", label: t("order.desc") }, { value: "asc", label: t("order.asc") }]}
            onChange={setOrder}
          />
        </ListToolbar>
        {collection.failures.length ? (
          <p className="list-failures" role="alert">{tCommon("project.partial", { names: collection.failures.map((failure) => failure.project.name).join(", ") })}</p>
        ) : null}
        {rows.length ? (
          <div className="table-frame">
            <table className="data-table files-table" aria-label={t("list.label")}>
              <thead>
                <tr>
                  <th scope="col">{t("list.name")}</th>
                  {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
                  <th scope="col" className="numeric">{t("list.size")}</th>
                  <th scope="col">{t("list.created")}</th>
                  <th scope="col">{t("list.purpose")}</th>
                  <th scope="col"><CreatorHeading /></th>
                  <th scope="col"><span className="visually-hidden">{t("list.actions")}</span></th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const file = row.value;
                  const unrecognized = isUnrecognizedFile(file);
                  const label = name(row);
                  return (
                    <tr key={`${row.project.id}:${file.id}`} className={params.id === file.id ? "files-row-new" : undefined}>
                      <th scope="row">
                        <NameCell name={unrecognized ? null : file.filename} id={file.id} fallback={label} idLabel={t("actions.copyId")}>
                          {unrecognized ? <HelpTip>{t("list.unrecognizedHelp")}</HelpTip> : null}
                        </NameCell>
                      </th>
                      {showProject ? <td><ProjectName project={byId.get(row.project.id) ?? row.project} /></td> : null}
                      <td className="numeric" title={unrecognized ? undefined : t("list.exactBytes", { bytes: file.bytes.toLocaleString(locale) })}>
                        {unrecognized ? MISSING : formatBytes(file.bytes)}
                      </td>
                      <td className="files-nowrap">{unrecognized ? MISSING : formatDateTime(file.created_at, locale)}</td>
                      <td>{unrecognized ? MISSING : <code>{file.purpose}</code>}</td>
                      <td><CreatorCell creator={creators.creatorOf(row.project.id, file.id)} /></td>
                      <td className="actions-cell">
                        <RowActions>
                          {!unrecognized ? (
                            <button className="text-action" type="button" aria-label={tCommon("copy.actionLabel", { name: label })} onClick={() => setCopy({ type: "file", id: file.id, name: label, project: row.project })}>
                              {tCommon("copy.action")}
                            </button>
                          ) : null}
                          <button className="text-action danger" type="button" aria-label={t("actions.deleteLabel", { name: label })} onClick={() => remove.ask(row)}>
                            {t("actions.delete")}
                          </button>
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
            title={t("list.noMatchTitle")}
            action={<button className="button outline" type="button" onClick={() => setQuery("")}>{tCommon("actions.clearSearch")}</button>}
          />
        )}
      </>
    );
  }

  return (
    <section className="page-section console-page files-page" aria-labelledby="files-heading">
      <PageHeader
        headingId="files-heading"
        title={t("title")}
        help={t("help")}
        actions={<RefreshButton onClick={refresh} refreshing={collection.status === "loading"} label={t("actions.refresh")} />}
      />
      <PageBody>{body}</PageBody>
      <CopyDialog source={copy} onClose={() => setCopy(null)} />
      <ConfirmDialog
        open={remove.target !== null}
        title={remove.target ? t("delete.prompt", { name: name(remove.target) }) : ""}
        confirmLabel={t("actions.confirmDelete")}
        busyLabel={t("actions.deleting")}
        busy={remove.busy}
        error={remove.error}
        onConfirm={() => void remove.confirm()}
        onClose={remove.cancel}
      >
        <p>{t("delete.consequences")}</p>
      </ConfirmDialog>
    </section>
  );
}
