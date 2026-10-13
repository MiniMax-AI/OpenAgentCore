import type { RequestLogEntry, RequestLogOptions } from "@oac/agents-client";
import { queryOptions, useQuery } from "@tanstack/react-query";
import { ListFilter, Search } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { ConsoleSelect } from "../../components/console-select";
import { Modal } from "../../components/Modal";
import { ReadFailure } from "../../components/ReadFailure";
import { TableSkeleton } from "../../components/Skeleton";
import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, Section, SegmentedControl, StatusDot } from "../../components/console-ui";
import { CopyableId, ListToolbar, NameCell } from "../../components/list-ui";
import { admin } from "../../lib/admin-view";
import { epochSeconds, formatClock, formatDateTime, formatDuration, formatInteger, MISSING } from "../../lib/format";
import "./request-log.css";

type Method = NonNullable<RequestLogOptions["method"]>;
const methods: readonly Method[] = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE", "OTHER"];
type Surface = "public" | "admin";
type Outcome = "all" | "errors" | "active";
const outcomes: readonly Outcome[] = ["all", "errors", "active"];

export function requestLogQuery(requestId: string, outcome: Outcome, surface: Surface = "public", method: Method | "" = "") {
  return queryOptions({
    queryKey: ["request-log", requestId, outcome, surface, method],
    queryFn: ({ signal }) => admin.listRequestLogs({ request_id: requestId || undefined, outcome, surface, method: method || undefined, limit: 200, signal }),
  });
}

export function RequestLogPage() {
  const { t, i18n } = useTranslation("requestLog");
  const locale = i18n.resolvedLanguage;
  const [draft, setDraft] = useState("");
  const [requestId, setRequestId] = useState("");
  const [method, setMethod] = useState<Method | "">("");
  const [surface, setSurface] = useState<Surface>("public");
  const [outcome, setOutcome] = useState<Outcome>("all");
  const [selected, setSelected] = useState<string | null>(null);
  const query = useQuery(requestLogQuery(requestId, outcome, surface, method));
  const log = query.data;
  const entry = log?.data.find((row) => row.request_id === selected);
  const refresh = () => { void query.refetch(); };

  return <section className="page-section console-page request-log-page" aria-labelledby="request-log-heading">
    <PageHeader headingId="request-log-heading" title={t("title")} help={t("help")}
      actions={<RefreshButton refreshing={query.isFetching} updatedAt={query.dataUpdatedAt ? formatClock(query.dataUpdatedAt, locale) : null} onClick={refresh} />} />
    <PageBody>
      <p className="page-status">{t("semantics")}</p>
      <ListToolbar label={t("filters")} summary={log ? t("count", { shown: log.data.length, total: log.total_matching }) : undefined}>
        <SegmentedControl label={t("surface")} value={surface} options={[{ value: "public", label: t("public") }, { value: "admin", label: t("admin") }]} onChange={(value) => { setSelected(null); setSurface(value); }} />
        <form className="request-log-search" onSubmit={(event) => { event.preventDefault(); setSelected(null); if (draft === requestId) refresh(); else setRequestId(draft); }}>
          <label className="search-control list-search">
            <Search size={14} strokeWidth={1.75} aria-hidden="true" />
            <input type="search" aria-label={t("requestId")} placeholder={t("searchPlaceholder")} value={draft} pattern="req_[0-9a-f]{32}" maxLength={36} onChange={(event) => setDraft(event.target.value.trim())} />
          </label>
          <HelpTip>{t("searchHelp")}</HelpTip>
          <button type="submit" className="button outline">{t("search")}</button>
        </form>
        <ConsoleSelect label={t("method")} value={method} placeholder={t("allMethods")} options={[{ value: "", label: t("allMethods") }, ...methods.map((value) => ({ value, label: value }))]} onChange={(value) => { setSelected(null); setMethod(methods.find((method) => method === value) ?? ""); }} />
        <SegmentedControl label={t("outcome")} value={outcome} options={outcomes.map((value) => ({ value, label: t(value) }))} onChange={(value) => { setSelected(null); setOutcome(value); }} />
      </ListToolbar>
      {query.isError ? <ReadFailure partial={!!log} onRetry={refresh} /> : null}
      <Section headingId="request-log-recent" title={t("recent")}>
        {!log ? query.isError ? null : <TableSkeleton label={t("loading")} columns={4} /> : <>
          <p className="page-status">{t("coverage", { retained: log.retained_count, capacity: log.capacity, started: formatDateTime(epochSeconds(log.instance_started_at), locale) })}</p>
          {!log.data.length ? <EmptyState icon={ListFilter} title={t("empty")} hint={t("emptyHelp")} /> : <div className="table-frame">
            <table className="data-table request-log-table" aria-label={t("title")}>
              <thead><tr><th scope="col">{t("arrived")}</th><th scope="col">{t("request")}</th><th scope="col">{t("status")}</th><th scope="col" className="numeric">{t("duration")}</th></tr></thead>
              <tbody>{log.data.map((row) => <tr key={row.request_id}>
                <td><time dateTime={row.started_at} title={formatDateTime(epochSeconds(row.started_at), locale)}>{formatClock(Date.parse(row.started_at), locale)}</time></td>
                <th scope="row"><NameCell name={`${row.method} ${row.route}`} id={row.request_id} idLabel={t("copyRequestId")} onOpen={() => setSelected(row.request_id)} openLabel={t("open", { id: row.request_id })} /></th>
                <td><span className="name-cell"><span>{row.status ?? MISSING}</span><RequestState entry={row} /></span></td>
                <td className="numeric" title={row.state === "active" ? t("elapsed") : undefined}>{formatDuration(row.duration_ms / 1000)}</td>
              </tr>)}</tbody>
            </table>
          </div>}
          {log.has_more ? <p className="page-status">{t("limited", { shown: log.data.length })}</p> : null}
        </>}
      </Section>
    </PageBody>
    <Modal open={!!entry} title={t("details")} onClose={() => setSelected(null)}>
      {entry ? <>
        {query.isError ? <ReadFailure partial onRetry={refresh} /> : null}
        <p className="page-status">{t("snapshot", { time: formatClock(query.dataUpdatedAt, locale) })}</p>
        <dl className="resource-facts">
          <div><dt>{t("instance")}</dt><dd><CopyableId id={log!.instance_id} /></dd></div>
          <div><dt>{t("requestId")}</dt><dd><CopyableId id={entry.request_id} label={t("copyRequestId")} /></dd></div>
          <div><dt>{t("traceId")}</dt><dd><CopyableId id={entry.trace_id} label={t("copyTraceId")} /></dd></div>
          <div><dt>{t("arrived")}</dt><dd><time dateTime={entry.started_at}>{formatDateTime(epochSeconds(entry.started_at), locale)} · {formatClock(Date.parse(entry.started_at), locale)}</time></dd></div>
          <div><dt>{t("request")}</dt><dd><code>{entry.method} {entry.route}</code></dd></div>
          <div><dt>{t("state")}</dt><dd><RequestState entry={entry} /></dd></div>
          <div><dt>{t("status")}</dt><dd>{entry.status ?? MISSING}</dd></div>
          <div><dt>{t(entry.state === "active" ? "elapsed" : "duration")}</dt><dd>{formatDuration(entry.duration_ms / 1000)}</dd></div>
          <div><dt>{t("responseBytes")}</dt><dd>{formatInteger(entry.response_bytes, locale)}</dd></div>
          <div><dt>{t("errorCode")}</dt><dd>{entry.error_code ? <code>{entry.error_code}</code> : MISSING}</dd></div>
        </dl>
      </> : null}
    </Modal>
  </section>;
}

function RequestState({ entry }: { entry: RequestLogEntry }) {
  const { t } = useTranslation("requestLog");
  return <StatusDot tone={entry.state === "aborted" || (entry.status !== null && entry.status >= 400) ? "danger" : entry.state === "active" ? "pending" : "neutral"} label={t(entry.state)} />;
}
