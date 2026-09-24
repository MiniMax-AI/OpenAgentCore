import { History } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { EmptyState, Section } from "../../components/console-ui";
import { CopyableId, ListToolbar } from "../../components/list-ui";
import { formatDateTime, formatRelative } from "../../lib/format";
import type { ConsoleAPIKey } from "../api-keys/api-keys";
import { ActorName } from "./OwnerCell";
import {
  fetchActivity,
  ownedResourceTypes,
  OwnershipUnavailableError,
  type ActivityQuery,
  type KeyActivity,
  type OwnedResourceType,
} from "./ownership";

const PAGE = 50;
/** Key filter values: "" (all keys), "console", or an issued key ID. */
export type ActivityKeyFilter = string;

interface ActivityState {
  status: "loading" | "ready" | "failed" | "unavailable";
  entries: KeyActivity[];
  nextAfter: string | null;
  loadingMore: boolean;
}

function query(keyFilter: ActivityKeyFilter, type: OwnedResourceType | ""): ActivityQuery {
  return {
    ...(keyFilter === "console" ? { actor_type: "console" as const } : keyFilter ? { key_id: keyFilter } : {}),
    ...(type ? { resource_type: type } : {}),
    limit: PAGE,
  };
}

/**
 * Every successful write recorded per API key, newest first. Filters are sent
 * to Core; the table shows only what Core recorded and never infers activity.
 */
export function ActivityLog({
  keys,
  keyFilter,
  onKeyFilterChange,
  refreshToken,
}: {
  keys: readonly ConsoleAPIKey[];
  keyFilter: ActivityKeyFilter;
  onKeyFilterChange: (value: ActivityKeyFilter) => void;
  /** Changes when the page is refreshed. */
  refreshToken: number;
}) {
  const { t, i18n } = useTranslation("ownership");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const [type, setType] = useState<OwnedResourceType | "">("");
  const [state, setState] = useState<ActivityState>({ status: "loading", entries: [], nextAfter: null, loadingMore: false });
  const controllerRef = useRef<AbortController | null>(null);
  const now = Math.floor(Date.now() / 1000);

  useEffect(() => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setState({ status: "loading", entries: [], nextAfter: null, loadingMore: false });
    fetchActivity(query(keyFilter, type), controller.signal).then((page) => {
      setState({ status: "ready", entries: page.data, nextAfter: page.has_more ? page.last_id : null, loadingMore: false });
    }, (error: unknown) => {
      if (controller.signal.aborted) return;
      setState({ status: error instanceof OwnershipUnavailableError ? "unavailable" : "failed", entries: [], nextAfter: null, loadingMore: false });
    });
    return () => controller.abort();
  }, [keyFilter, refreshToken, type]);

  const loadMore = useCallback(() => {
    if (!state.nextAfter || state.loadingMore) return;
    const controller = new AbortController();
    controllerRef.current = controller;
    setState((current) => ({ ...current, loadingMore: true }));
    fetchActivity({ ...query(keyFilter, type), after: state.nextAfter }, controller.signal).then((page) => {
      setState((current) => ({ ...current, entries: [...current.entries, ...page.data], nextAfter: page.has_more ? page.last_id : null, loadingMore: false }));
    }, () => {
      if (!controller.signal.aborted) setState((current) => ({ ...current, loadingMore: false }));
    });
  }, [keyFilter, state.loadingMore, state.nextAfter, type]);

  const actionLabel = (action: string) => (["create", "update", "delete", "send"].includes(action) ? t(`actions.${action as "create"}`) : action);
  const typeLabel = (value: string) => ((ownedResourceTypes as readonly string[]).includes(value) ? t(`types.${value as OwnedResourceType}`) : value);

  let body;
  if (state.status === "unavailable") {
    body = <EmptyState icon={History} title={t("activity.unavailable")} description={t("activity.unavailableDescription")} />;
  } else if (state.status === "failed") {
    body = <EmptyState title={t("activity.failed")} action={<button className="button outline" type="button" onClick={() => onKeyFilterChange(keyFilter)}>{tCommon("actions.retry")}</button>} />;
  } else if (state.status === "loading") {
    body = <p className="page-status" role="status">{t("activity.loading")}</p>;
  } else if (!state.entries.length) {
    body = <EmptyState icon={History} title={t("activity.empty")} description={t("activity.emptyDescription")} />;
  } else {
    body = (
      <>
        <div className="table-frame">
          <table className="data-table activity-table" aria-label={t("activity.title")}>
            <thead>
              <tr>
                <th scope="col">{t("activity.columns.time")}</th>
                <th scope="col">{t("activity.columns.key")}</th>
                <th scope="col">{t("activity.columns.action")}</th>
                <th scope="col">{t("activity.columns.type")}</th>
                <th scope="col">{t("activity.columns.resource")}</th>
              </tr>
            </thead>
            <tbody>
              {state.entries.map((entry) => {
                const at = Date.parse(entry.created_at) / 1000;
                return (
                  <tr key={entry.id}>
                    <td className="activity-time" title={formatDateTime(at, locale)}>{formatRelative(at, now, locale)}</td>
                    <td><ActorName actor={entry.actor} /></td>
                    <td>{actionLabel(entry.action)}</td>
                    <td>{typeLabel(entry.resource_type)}</td>
                    <td>
                      <CopyableId id={entry.resource_id} compact />
                      {entry.parent_resource_id ? <span className="activity-parent" title={entry.parent_resource_id}>{t("activity.parent", { id: entry.parent_resource_id.length > 14 ? `${entry.parent_resource_id.slice(0, 6)}…${entry.parent_resource_id.slice(-4)}` : entry.parent_resource_id })}</span> : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {state.nextAfter ? (
          <footer className="table-footer">
            <button className="button outline" type="button" disabled={state.loadingMore} onClick={loadMore}>{tCommon("actions.loadMore")}</button>
          </footer>
        ) : null}
      </>
    );
  }

  return (
    <Section headingId="api-key-activity-heading" title={t("activity.title")} help={t("activity.help")} className="activity-section">
      {state.status !== "unavailable" ? (
        <ListToolbar label={t("activity.filterLabel")}>
          <label className="select-control">
            <span className="visually-hidden">{t("activity.columns.key")}</span>
            <select value={keyFilter} onChange={(event) => onKeyFilterChange(event.target.value)}>
              <option value="">{t("activity.allKeys")}</option>
              {keys.map((key) => <option key={key.id} value={key.id}>{key.revoked_at ? `${key.name} · ${t("actor.revoked")}` : key.name}</option>)}
              <option value="console">{t("actor.console")}</option>
            </select>
          </label>
          <label className="select-control">
            <span className="visually-hidden">{t("activity.columns.type")}</span>
            <select value={type} onChange={(event) => setType(event.target.value as OwnedResourceType | "")}>
              <option value="">{t("activity.allTypes")}</option>
              {ownedResourceTypes.map((value) => <option key={value} value={value}>{t(`types.${value}`)}</option>)}
            </select>
          </label>
        </ListToolbar>
      ) : null}
      {body}
    </Section>
  );
}
