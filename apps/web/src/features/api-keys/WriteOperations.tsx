import { type AdminAPIKey, type AdminKeyProvenance, type AdminResourceType, adminResourceTypes } from "@oac/agents-client";
import { useInfiniteQuery } from "@tanstack/react-query";
import { History } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";


import { useFailureToast } from "../../components/Toast";
import { EmptyState, Section } from "../../components/console-ui";
import { CopyableId, ListToolbar, listSummary } from "../../components/list-ui";
import { epochSeconds, formatDateTime, shortId } from "../../lib/format";
import { admin } from "../../lib/projects";
import { prefixLabel } from "./key-flows";
import { writeOperationsQuery } from "./project-queries";
import { TableSkeleton } from "../../components/Skeleton";
import { ConsoleSelect } from "../../components/console-select";

const isType = (value: string): value is AdminResourceType => (adminResourceTypes as readonly string[]).includes(value);

/** The recorded key of a write, by name. */
export function OperationKey({ value }: { value: AdminKeyProvenance }) {
  const { t } = useTranslation("keys");
  const revoked = value.revoked_at !== null;
  return (
    <span className={revoked ? "operation-key revoked" : "operation-key"} title={[prefixLabel(value.prefix), revoked ? t("detail.keyStatus.revoked") : null].filter(Boolean).join(" · ")}>
      {value.name}
    </span>
  );
}

/**
 * Every successful write made in one project, newest first, with cursor
 * paging (`next_cursor`). Filters go to Core; nothing is inferred.
 */
export function WriteOperations({ projectId, keys }: { projectId: string; keys: readonly AdminAPIKey[] | null }) {
  const { t, i18n } = useTranslation("keys");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const [type, setType] = useState<AdminResourceType | "">("");
  const [keyId, setKeyId] = useState("");
  const query = useInfiniteQuery(writeOperationsQuery(projectId, { keyId, type }));
  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, refetch } = query;
  const entries = useMemo(() => data?.pages.flatMap((page) => page.data) ?? null, [data]);
  // A failed next page leaves the rows already read on screen, with a retry.
  const moreFailed = query.isFetchNextPageError && !isFetchingNextPage;
  // A failed refresh keeps the rows already read; the first read failing leaves none.

  const loadMore = useCallback(() => {
    if (!hasNextPage || isFetchingNextPage) return;
    void fetchNextPage();
  }, [fetchNextPage, hasNextPage, isFetchingNextPage]);

  const typeLabel = (value: AdminResourceType) => t(`operations.types.${value}`);

  useFailureToast(Boolean(entries) && query.isRefetchError && !query.isFetching, t("operations.failed"), "write-operations-refresh");
  useFailureToast(moreFailed, t("operations.moreFailed"), "write-operations-more");
  let body;
  if (!entries && query.isError && !query.isFetching) {
    body = (
      <EmptyState
        title={t("operations.failed")}
        action={<button className="button outline" type="button" onClick={() => { void refetch(); }}>{tCommon("actions.retry")}</button>}
      />
    );
  } else if (!entries) {
    body = <TableSkeleton label={t("operations.loading")} rows={3} columns={5} />;
  } else if (!entries.length) {
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
              {entries.map((entry) => (
                <tr key={entry.id}>
                  <td className="operations-time">{formatDateTime(epochSeconds(entry.created_at), locale)}</td>
                  <td><OperationKey value={entry.api_key} /></td>
                  <td>{t(`operations.actions.${entry.action}`)}</td>
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
        {hasNextPage || moreFailed ? (
          <footer className="table-footer">
            <button className="button outline" type="button" disabled={isFetchingNextPage} onClick={loadMore}>
              {moreFailed ? tCommon("actions.retry") : tCommon("actions.loadMore")}
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
        summary={entries?.length ? listSummary(tCommon, entries.length, entries.length, { hasMore: hasNextPage, locale }) : undefined}
      >
        <ConsoleSelect
          label={t("operations.columns.key")}
          value={keyId}
          onChange={setKeyId}
          options={[
            { value: "", label: t("operations.allKeys") },
            ...(keys ?? []).map((key) => ({ value: key.id, label: key.revoked_at !== null ? t("operations.revokedKeyOption", { name: key.name }) : key.name })),
          ]}
        />
        <ConsoleSelect
          label={t("operations.columns.resource")}
          value={type}
          onChange={(value) => setType(isType(value) ? value : "")}
          options={[
            { value: "", label: t("operations.allTypes") },
            ...adminResourceTypes.map((value) => ({ value, label: typeLabel(value) })),
          ]}
        />
      </ListToolbar>
      {body}
    </Section>
  );
}
