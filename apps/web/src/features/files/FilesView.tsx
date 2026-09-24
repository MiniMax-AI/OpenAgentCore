import { FileText, Search, Upload } from "lucide-react";
import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent } from "react";
import { useTranslation } from "react-i18next";

import type { PageOrder, SourceFileListEntry } from "@agents-core-web/agents-client";

import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, SegmentedControl, StatusDot } from "../../components/console-ui";
import { useToast } from "../../components/Toast";
import { formatBytes, formatDateTime, MISSING } from "../../lib/format";
import { CopyableId } from "./CopyableId";
import {
  classifyFilesListError,
  filesErrorReason,
  filterFiles,
  isAbortError,
  isDefiniteRejection,
  isUnrecognizedFile,
  readFilesPage,
  uploadPrecheck,
  type FilesOperations,
} from "./file-operations";
import "./files.css";

export type FilesListStatus = "loading" | "ready" | "storage-unavailable" | "unsupported" | "failed";

export interface FilesListState {
  status: FilesListStatus;
  files: SourceFileListEntry[];
  /** Cursor of the next page, or null once Core reported the end. */
  nextAfter: string | null;
  /** A first-load failure, or a refresh failure while earlier rows stay visible. */
  error: string | null;
  unsupportedStatus: number | null;
  refreshing: boolean;
  loadingMore: boolean;
  moreError: string | null;
}

export const initialFilesListState: FilesListState = {
  status: "loading",
  files: [],
  nextAfter: null,
  error: null,
  unsupportedStatus: null,
  refreshing: false,
  loadingMore: false,
  moreError: null,
};

/** The last upload attempt. An uncertain outcome is never retried automatically. */
export type FileUploadStatus =
  | { kind: "idle" }
  | { kind: "uploading"; name: string }
  | { kind: "rejected"; message: string }
  | { kind: "uncertain"; name: string };

export interface FilesListPageProps {
  state: FilesListState;
  order: PageOrder;
  query: string;
  upload: FileUploadStatus;
  /** A notice about the last deletion, shown above the table. */
  notice: string | null;
  highlightId: string | null;
  confirmId: string | null;
  deletingId: string | null;
  onOrderChange: (order: PageOrder) => void;
  onQueryChange: (query: string) => void;
  onRefresh: () => void;
  onLoadMore: () => void;
  onUpload: (file: File) => void;
  onDismissUpload: () => void;
  onConfirm: (fileId: string | null) => void;
  onDelete: (file: SourceFileListEntry) => void;
}

function fileName(file: SourceFileListEntry, unrecognized: string): string {
  return isUnrecognizedFile(file) ? unrecognized : file.filename;
}

/** Files: header, upload status, local filter, table and cursor pagination. */
export function FilesListPage({
  state, order, query, upload, notice, highlightId, confirmId, deletingId,
  onOrderChange, onQueryChange, onRefresh, onLoadMore, onUpload, onDismissUpload, onConfirm, onDelete,
}: FilesListPageProps) {
  const { t, i18n } = useTranslation("files");
  const locale = i18n.resolvedLanguage;
  const inputRef = useRef<HTMLInputElement>(null);
  const visible = useMemo(() => filterFiles(state.files, query), [query, state.files]);
  const filtering = query.trim().length > 0;
  const uploading = upload.kind === "uploading";
  const canUpload = state.status === "ready" && !uploading;

  const choose = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (file) onUpload(file);
  };

  const uploadButton = (
    <>
      <input ref={inputRef} type="file" className="visually-hidden" tabIndex={-1} aria-hidden="true" onChange={choose} />
      <button className="button primary" type="button" disabled={!canUpload} onClick={() => inputRef.current?.click()}>
        <Upload size={14} aria-hidden="true" />{uploading ? t("actions.uploading") : t("actions.upload")}
      </button>
    </>
  );
  const retry = <button className="button outline" type="button" onClick={onRefresh}>{t("actions.retry")}</button>;

  let body;
  if (state.status === "loading") {
    body = <p className="page-status" role="status">{t("list.loading")}</p>;
  } else if (state.status === "storage-unavailable") {
    body = <EmptyState icon={FileText} title={t("storage.title")} description={t("storage.description")} action={retry} />;
  } else if (state.status === "unsupported") {
    body = <EmptyState icon={FileText} title={t("unsupported.title")} description={t("unsupported.description", { status: state.unsupportedStatus ?? MISSING })} />;
  } else if (state.status === "failed") {
    body = <EmptyState title={t("list.loadFailed")} description={state.error ?? undefined} action={retry} />;
  } else {
    body = (
      <>
        {state.error ? <p className="coverage-note coverage-note-error" role="alert">{t("list.refreshFailed", { reason: state.error })}</p> : null}
        {state.files.length === 0 ? (
          <EmptyState icon={FileText} title={t("empty.title")} description={t("empty.description")} />
        ) : (
          <>
            <div className="filter-bar">
              <div className="filter-controls">
                <label className="search-control files-search">
                  <Search size={14} strokeWidth={1.5} aria-hidden="true" />
                  <input
                    type="search"
                    value={query}
                    onChange={(event) => onQueryChange(event.target.value)}
                    placeholder={t("list.filterPlaceholder")}
                    aria-label={t("list.filterLabel")}
                  />
                </label>
                <SegmentedControl
                  label={t("order.label")}
                  value={order}
                  options={[{ value: "desc", label: t("order.desc") }, { value: "asc", label: t("order.asc") }]}
                  onChange={onOrderChange}
                />
              </div>
              <span className="filter-count" role="status">
                {filtering ? t("list.count", { shown: visible.length, loaded: state.files.length }) : t("list.loaded", { count: state.files.length })}
              </span>
            </div>
            {visible.length ? (
              <div className="table-frame">
                <table className="data-table files-table" aria-label={t("list.label")}>
                  <thead>
                    <tr>
                      <th scope="col">{t("list.name")}</th>
                      <th scope="col" className="numeric">{t("list.size")}</th>
                      <th scope="col">{t("list.created")}</th>
                      <th scope="col">{t("list.purpose")}</th>
                      <th scope="col">{t("list.id")}</th>
                      <th scope="col"><span className="visually-hidden">{t("list.actions")}</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {visible.map((file) => {
                      const name = fileName(file, t("list.unrecognized"));
                      const confirming = confirmId === file.id;
                      const deleting = deletingId === file.id;
                      return (
                        <Fragment key={file.id}>
                          <tr className={highlightId === file.id ? "files-row-new" : undefined}>
                            <th scope="row">
                              {isUnrecognizedFile(file) ? (
                                <span className="status-with-help">
                                  <StatusDot tone="warning" label={name} />
                                  <HelpTip>{t("list.unrecognizedHelp")}</HelpTip>
                                </span>
                              ) : <span className="files-name" title={file.filename}>{file.filename}</span>}
                            </th>
                            <td className="numeric" title={isUnrecognizedFile(file) ? undefined : t("list.exactBytes", { bytes: file.bytes.toLocaleString(locale) })}>
                              {isUnrecognizedFile(file) ? MISSING : formatBytes(file.bytes)}
                            </td>
                            <td className="files-nowrap">{isUnrecognizedFile(file) ? MISSING : formatDateTime(file.created_at, locale)}</td>
                            <td>{isUnrecognizedFile(file) ? MISSING : <code>{file.purpose}</code>}</td>
                            <td className="files-nowrap"><CopyableId id={file.id} compact /></td>
                            <td className="row-actions">
                              {!confirming ? (
                                <button
                                  className="text-action"
                                  type="button"
                                  disabled={deletingId !== null}
                                  aria-label={t("actions.deleteLabel", { name })}
                                  onClick={() => onConfirm(file.id)}
                                >
                                  {t("actions.delete")}
                                </button>
                              ) : null}
                            </td>
                          </tr>
                          {confirming ? (
                            <tr className="files-confirm-row">
                              <td colSpan={6}>
                                <div className="files-confirm" role="group" aria-label={t("delete.prompt", { name })}>
                                  <span>
                                    <strong>{t("delete.prompt", { name })}</strong>{" "}
                                    {t("delete.consequences")}
                                  </span>
                                  <span className="files-confirm-actions">
                                    <button className="button outline" type="button" disabled={deleting} onClick={() => onConfirm(null)}>{t("actions.cancel")}</button>
                                    <button className="button danger" type="button" disabled={deleting} onClick={() => onDelete(file)}>
                                      {deleting ? t("actions.deleting") : t("actions.confirmDelete")}
                                    </button>
                                  </span>
                                </div>
                              </td>
                            </tr>
                          ) : null}
                        </Fragment>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <EmptyState
                title={t("list.noMatchTitle")}
                description={t("list.noMatchDescription")}
                action={<button className="button outline" type="button" onClick={() => onQueryChange("")}>{t("actions.clearFilter")}</button>}
              />
            )}
            {state.moreError ? <p className="coverage-note coverage-note-error" role="alert">{t("list.moreFailed", { reason: state.moreError })}</p> : null}
            {state.nextAfter ? (
              <footer className="table-footer files-footer">
                <button className="button outline" type="button" disabled={state.loadingMore} onClick={onLoadMore}>
                  {state.loadingMore ? t("actions.loadingMore") : t("actions.loadMore")}
                </button>
              </footer>
            ) : null}
          </>
        )}
      </>
    );
  }

  return (
    <section className="page-section console-page files-page" aria-labelledby="files-heading">
      <PageHeader
        headingId="files-heading"
        title={t("title")}
        help={<>{t("help")} {t("limits")}</>}
        actions={(
          <>
            <RefreshButton onClick={onRefresh} refreshing={state.status === "loading" || state.refreshing} label={t("actions.refresh")} />
            {uploadButton}
          </>
        )}
      />
      <PageBody>
        {upload.kind === "uploading" ? <p className="page-status" role="status">{t("upload.uploading", { name: upload.name })}</p> : null}
        {upload.kind === "rejected" || upload.kind === "uncertain" ? (
          <div className="coverage-note coverage-note-error files-upload-note" role="alert">
            <span>{upload.kind === "rejected" ? upload.message : t("upload.uncertain", { name: upload.name })}</span>
            <span className="files-confirm-actions">
              {upload.kind === "uncertain" ? <button className="button outline" type="button" onClick={onRefresh}>{t("actions.refresh")}</button> : null}
              <button className="button outline" type="button" onClick={onDismissUpload}>{t("actions.dismiss")}</button>
            </span>
          </div>
        ) : null}
        {notice ? <p className="coverage-note coverage-note-error" role="alert">{notice}</p> : null}
        {body}
      </PageBody>
    </section>
  );
}

export interface FilesViewProps {
  /** The browser's Agents API client; File requests carry no key of their own. */
  core: FilesOperations;
  /** Pre-fills the local filter, for example with a File ID opened from a Template. */
  initialQuery?: string;
}

/** Resources › Files: every project File with upload, copyable ID and confirmed deletion. */
export function FilesView({ core, initialQuery = "" }: FilesViewProps) {
  const { t } = useTranslation("files");
  const toast = useToast();
  const [state, setState] = useState<FilesListState>(initialFilesListState);
  const [order, setOrder] = useState<PageOrder>("desc");
  const [query, setQuery] = useState(initialQuery);
  const [upload, setUpload] = useState<FileUploadStatus>({ kind: "idle" });
  const [notice, setNotice] = useState<string | null>(null);
  const [highlightId, setHighlightId] = useState<string | null>(null);
  const [confirmId, setConfirmId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const request = useRef(0);
  const mounted = useRef(true);
  const abortRef = useRef<AbortController | null>(null);
  const moreAbortRef = useRef<AbortController | null>(null);
  const orderRef = useRef(order);
  orderRef.current = order;
  const translate = useRef(t);
  translate.current = t;
  // Stable across language changes, so switching language never re-reads the list.
  const text = useCallback((key: string) => translate.current(key as never), []);

  const reload = useCallback(async (nextOrder: PageOrder) => {
    abortRef.current?.abort();
    moreAbortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const id = request.current + 1;
    request.current = id;
    setState((current) => ({
      ...current,
      status: current.status === "ready" ? "ready" : "loading",
      refreshing: true,
      loadingMore: false,
      moreError: null,
    }));
    try {
      const page = await readFilesPage(core, [], nextOrder, undefined, controller.signal);
      if (id !== request.current) return;
      setState({ ...initialFilesListState, status: "ready", files: page.values, nextAfter: page.nextAfter });
    } catch (error) {
      if (id !== request.current || isAbortError(error)) return;
      const kind = classifyFilesListError(error);
      const reason = filesErrorReason(error, text);
      setState((current) => {
        if (current.status === "ready" && kind === "failed") return { ...current, refreshing: false, error: reason };
        if (kind === "unsupported") {
          return { ...initialFilesListState, status: "unsupported", unsupportedStatus: (error as { status?: number }).status ?? null };
        }
        return { ...initialFilesListState, status: kind, error: reason };
      });
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
    }
  }, [core, text]);

  useEffect(() => {
    mounted.current = true;
    void reload(orderRef.current);
    return () => {
      mounted.current = false;
      request.current += 1;
      abortRef.current?.abort();
      moreAbortRef.current?.abort();
    };
  }, [reload]);

  useEffect(() => {
    if (!highlightId) return;
    const timer = window.setTimeout(() => setHighlightId(null), 8000);
    return () => window.clearTimeout(timer);
  }, [highlightId]);

  const changeOrder = (next: PageOrder) => {
    if (next === order) return;
    setOrder(next);
    setConfirmId(null);
    void reload(next);
  };

  const loadMore = async () => {
    const after = state.nextAfter;
    if (!after || state.loadingMore) return;
    const loaded = state.files;
    const id = request.current;
    const controller = new AbortController();
    moreAbortRef.current = controller;
    setState((current) => ({ ...current, loadingMore: true, moreError: null }));
    try {
      const page = await readFilesPage(core, loaded, order, after, controller.signal);
      if (id !== request.current) return;
      setState((current) => ({ ...current, files: page.values, nextAfter: page.nextAfter, loadingMore: false }));
    } catch (error) {
      if (id !== request.current || isAbortError(error)) return;
      setState((current) => ({ ...current, loadingMore: false, moreError: filesErrorReason(error, text) }));
    } finally {
      if (moreAbortRef.current === controller) moreAbortRef.current = null;
    }
  };

  const uploadFile = async (file: File) => {
    if (upload.kind === "uploading") return;
    setNotice(null);
    const problem = uploadPrecheck(file);
    if (problem) {
      setUpload({ kind: "rejected", message: problem === "too-large" ? t("upload.tooLarge", { name: file.name }) : t("upload.badName") });
      return;
    }
    setUpload({ kind: "uploading", name: file.name });
    try {
      // No abort signal: once sent, the outcome must be observed rather than abandoned.
      const created = await core.uploadSourceFile({ file, filename: file.name });
      if (!mounted.current) return;
      setUpload({ kind: "idle" });
      setHighlightId(created.id);
      toast.show(t("upload.uploaded", { name: created.filename }), { tone: "success" });
      void reload(order);
    } catch (error) {
      if (!mounted.current) return;
      setUpload(isDefiniteRejection(error)
        ? { kind: "rejected", message: t("upload.rejected", { name: file.name, reason: filesErrorReason(error, text) }) }
        : { kind: "uncertain", name: file.name });
    }
  };

  const deleteFile = async (file: SourceFileListEntry) => {
    if (deletingId) return;
    const name = isUnrecognizedFile(file) ? file.id : file.filename;
    setDeletingId(file.id);
    setNotice(null);
    const drop = () => setState((current) => ({ ...current, files: current.files.filter((entry) => entry.id !== file.id) }));
    try {
      await core.deleteSourceFile(file.id);
      if (!mounted.current) return;
      drop();
      setConfirmId(null);
      toast.show(t("delete.deleted", { name }), { tone: "success" });
    } catch (error) {
      if (!mounted.current) return;
      if (isDefiniteRejection(error) && error.status === 404) {
        drop();
        setConfirmId(null);
        setNotice(t("delete.missing", { name }));
      } else if (isDefiniteRejection(error)) {
        setNotice(t("delete.rejected", { reason: filesErrorReason(error, text) }));
      } else {
        setConfirmId(null);
        setNotice(t("delete.uncertain", { name }));
      }
    } finally {
      if (mounted.current) setDeletingId(null);
    }
  };

  return (
    <FilesListPage
      state={state}
      order={order}
      query={query}
      upload={upload}
      notice={notice}
      highlightId={highlightId}
      confirmId={confirmId}
      deletingId={deletingId}
      onOrderChange={changeOrder}
      onQueryChange={setQuery}
      onRefresh={() => { setNotice(null); void reload(order); }}
      onLoadMore={() => void loadMore()}
      onUpload={(file) => void uploadFile(file)}
      onDismissUpload={() => setUpload({ kind: "idle" })}
      onConfirm={setConfirmId}
      onDelete={(file) => void deleteFile(file)}
    />
  );
}
