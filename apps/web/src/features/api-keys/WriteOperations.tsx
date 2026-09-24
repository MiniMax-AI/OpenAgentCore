import { History } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";


import { EmptyState, Section } from "../../components/console-ui";
import { CopyableId, ListToolbar, listSummary } from "../../components/list-ui";
import { formatDateTime, shortId } from "../../lib/format";
import { admin } from "../../lib/projects";
import { prefixLabel } from "./key-flows";
import { type AdminKey, type KeyRef, listWriteOperations, type OwnerResourceType, ownerResourceTypes, type WriteOperation } from "../../lib/admin-view";

const PAGE_SIZE = 50;


export const operationActions = ["create", "update", "delete", "send_events", "upload_file", "upload_version", "update_default_version"] as const;
type OperationAction = (typeof operationActions)[number];

const isType = (value: string): value is OwnerResourceType => (ownerResourceTypes as readonly string[]).includes(value);
const isAction = (value: string): value is OperationAction => (operationActions as readonly string[]).includes(value);

interface OperationsState {
  status: "loading" | "ready" | "failed";
  entries: WriteOperation[];
  cursor: string | null;
  loadingMore: boolean;
  moreFailed: boolean;
}

/** The recorded key of a write: its name, else its prefix; "unknown" when none was recorded. */
export function OperationKey({ value }: { value: KeyRef | null }) {
  const { t } = useTranslation("keys");
  if (!value) return <span className="table-muted">{t("operations.unknownKey")}</span>;
  const revoked = value.revoked_at !== null;
  return (
    <span className={revoked ? "operation-key revoked" : "operation-key"} title={[value.prefix ? prefixLabel(value.prefix) : null, revoked ? t("detail.keyStatus.revoked") : null].filter(Boolean).join(" · ") || undefined}>
      {value.name ?? prefixLabel(value.prefix)}
    </span>
  );
}

/**
 * Every successful write made in one project, newest first, with cursor
 * paging (`next_cursor`). Filters go to Core; nothing is inferred.
 */
export function WriteOperations({ projectId, keys, refreshToken }: { projectId: string; keys: readonly AdminKey[] | null; refreshToken: number }) {
  const { t, i18n } = useTranslation("keys");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const [type, setType] = useState<OwnerResourceType | "">("");
  const [keyId, setKeyId] = useState("");
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState<OperationsState>({ status: "loading", entries: [], cursor: null, loadingMore: false, moreFailed: false });
  const controller = useRef<AbortController | null>(null);

  useEffect(() => {
    controller.current?.abort();
    const current = new AbortController();
    controller.current = current;
    setState({ status: "loading", entries: [], cursor: null, loadingMore: false, moreFailed: false });
    listWriteOperations(projectId, { key_id: keyId || undefined, resource_type: type || undefined, limit: PAGE_SIZE, signal: current.signal }).then(
      (page) => setState({ status: "ready", entries: page.data, cursor: page.has_more ? page.next_cursor : null, loadingMore: false, moreFailed: false }),
      () => { if (!current.signal.aborted) setState({ status: "failed", entries: [], cursor: null, loadingMore: false, moreFailed: false }); },
    );
    return () => current.abort();
  }, [projectId, keyId, type, refreshToken, retry]);

  const loadMore = useCallback(() => {
    if (!state.cursor || state.loadingMore) return;
    const current = new AbortController();
    controller.current = current;
    const after = state.cursor;
    setState((value) => ({ ...value, loadingMore: true, moreFailed: false }));
    listWriteOperations(projectId, { key_id: keyId || undefined, resource_type: type || undefined, after, limit: PAGE_SIZE, signal: current.signal }).then(
      (page) => setState((value) => ({ ...value, entries: [...value.entries, ...page.data], cursor: page.has_more ? page.next_cursor : null, loadingMore: false })),
      () => { if (!current.signal.aborted) setState((value) => ({ ...value, loadingMore: false, moreFailed: true })); },
    );
  }, [projectId, keyId, state.cursor, state.loadingMore, type]);

  const actionLabel = (action: string) => (isAction(action) ? t(`operations.actions.${action}`) : action);
  const typeLabel = (value: string) => (isType(value) ? t(`operations.types.${value}`) : value);

  let body;
  if (state.status === "loading") {
    body = <p className="page-status" role="status">{t("operations.loading")}</p>;
  } else if (state.status === "failed") {
    body = (
      <EmptyState
        title={t("operations.failed")}
        action={<button className="button outline" type="button" onClick={() => setRetry((value) => value + 1)}>{tCommon("actions.retry")}</button>}
      />
    );
  } else if (!state.entries.length) {
    body = <EmptyState icon={History} title={t("operations.empty")} />;
  } else {
    body = (
      <>
        <div className="table-frame">
          <table className="data-table operations-table" aria-label={t("operations.title")}>
            <thead>
              <tr>
                <th scope="col">{t("operations.columns.time")}</th>
                <th scope="col">{t("operations.columns.key")}</th>
                <th scope="col">{t("operations.columns.action")}</th>
                <th scope="col">{t("operations.columns.resource")}</th>
                <th scope="col">{t("operations.columns.id")}</th>
              </tr>
            </thead>
            <tbody>
              {state.entries.map((entry) => (
                <tr key={entry.id}>
                  <td className="operations-time">{formatDateTime(entry.created_at, locale)}</td>
                  <td><OperationKey value={entry.api_key} /></td>
                  <td>{actionLabel(entry.action)}</td>
                  <td>{typeLabel(entry.resource_type)}</td>
                  <td>
                    <span className="operations-resource">
                      <CopyableId id={entry.resource_id} compact />
                      {entry.parent_id ? <span className="operations-parent" title={entry.parent_id}>{t("operations.parent", { id: shortId(entry.parent_id) })}</span> : null}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {state.cursor || state.moreFailed ? (
          <footer className="table-footer">
            {state.moreFailed ? <span role="alert">{t("operations.moreFailed")}</span> : null}
            <button className="button outline" type="button" disabled={state.loadingMore} onClick={loadMore}>
              {state.moreFailed ? tCommon("actions.retry") : tCommon("actions.loadMore")}
            </button>
          </footer>
        ) : null}
      </>
    );
  }

  return (
    <Section headingId="project-operations-heading" title={t("operations.title")} help={t("operations.help")}>
      <ListToolbar
        label={t("operations.filterLabel")}
        summary={state.status === "ready" && state.entries.length ? listSummary(tCommon, state.entries.length, state.entries.length, { hasMore: Boolean(state.cursor), locale }) : undefined}
      >
        <label className="select-control">
          <span className="visually-hidden">{t("operations.columns.key")}</span>
          <select value={keyId} onChange={(event) => setKeyId(event.target.value)}>
            <option value="">{t("operations.allKeys")}</option>
            {(keys ?? []).map((key) => (
              <option key={key.id} value={key.id}>{key.revoked_at !== null ? t("operations.revokedKeyOption", { name: key.name }) : key.name}</option>
            ))}
          </select>
        </label>
        <label className="select-control">
          <span className="visually-hidden">{t("operations.columns.resource")}</span>
          <select value={type} onChange={(event) => setType(isType(event.target.value) ? event.target.value : "")}>
            <option value="">{t("operations.allTypes")}</option>
            {ownerResourceTypes.map((value) => <option key={value} value={value}>{typeLabel(value)}</option>)}
          </select>
        </label>
      </ListToolbar>
      {body}
    </Section>
  );
}
