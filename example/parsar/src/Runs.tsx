import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { randomUuid } from "@oac/agents-client";
import { Play } from "lucide-react";
import { tr } from "./lib/i18n";
import { api, dateTime } from "./lib/api";
import {
  product,
  useAgents,
  type TurnOverview,
  type TurnRow,
} from "./lib/product";
import { stageLabel, typicalWait } from "../shared/turn-status.mjs";
import { ErrorNotice, PageHeader, RecordTabs } from "./components/shared";
import { Button } from "./components/ui/button";
import { EmptyState } from "./components/ui/empty-state";
import { Select, SelectOption } from "./components/ui/select";
import { StatusIcon } from "./components/ui/status-icon";
import { cn } from "./lib/utils";

const sourceLabel = (row: TurnRow) =>
  row.source === "feishu"
    ? row.chat_type === "p2p"
      ? tr("飞书私聊")
      : tr("飞书群聊")
    : tr("网页");
// Which conversation a Turn belongs to, when its own input does not say.
const context = (row: TurnRow, agent: string) =>
  [
    row.prompt && row.prompt !== row.conversation ? row.conversation : "",
    agent,
    sourceLabel(row),
  ]
    .filter(Boolean)
    .join(" · ");
const outcome = {
  completed: { label: "完成", icon: "completed" },
  failed: { label: "失败", icon: "failed" },
  cancelled: { label: "已停止", icon: "cancelled" },
  running: { label: "进行中", icon: "running" },
} as const;

function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone?: "danger";
}) {
  return (
    <div className="px-6 py-5">
      <dt className="text-sm text-fg-muted">{label}</dt>
      <dd
        className={cn(
          "mt-1 text-2xl font-semibold tabular-nums",
          tone === "danger" && value ? "text-danger" : "text-fg",
        )}
      >
        {value}
      </dd>
    </div>
  );
}

// Conversations running at each 10-second mark of the last five minutes.
function Concurrency({ series }: { series: number[] }) {
  const max = Math.max(1, ...series);
  const points = series
    .map((n, i) => `${(i / (series.length - 1)) * 100},${36 - (n / max) * 32}`)
    .join(" ");
  return (
    <div className="min-w-56 flex-1 px-6 py-5">
      <dt className="flex justify-between text-sm text-fg-muted">
        <span>{tr("同时进行")}</span>
        <span>{tr("近 5 分钟")}</span>
      </dt>
      <dd className="mt-2">
        <svg
          viewBox="0 0 100 38"
          preserveAspectRatio="none"
          className="h-10 w-full"
          role="img"
          aria-label={tr("近 5 分钟同时进行的对话")}
        >
          <polyline
            points={points}
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            vectorEffect="non-scaling-stroke"
            className="text-status-running"
          />
        </svg>
      </dd>
    </div>
  );
}

function Running({
  row,
  agent,
  now,
}: {
  row: TurnRow;
  agent: string;
  now: number;
}) {
  const stop = useMutation({
    mutationFn: () => api.cancelTurn(row.core_session_id, randomUuid()),
  });
  const elapsed = Math.max(0, Math.round((now - row.started_at) / 1000));
  const waiting =
    !row.first_output_s &&
    row.stage !== "waiting_user" &&
    row.stage !== "retrying";
  return (
    <article className="flex items-center gap-4 px-6 py-4">
      <StatusIcon status="running" />
      <a href={`#/sessions/${row.session_id}`} className="min-w-0 flex-1">
        <h3 className="truncate text-sm font-medium text-fg">
          {row.prompt || row.conversation}
        </h3>
        <p className="mt-0.5 truncate text-sm text-fg-muted">
          {context(row, agent)}
        </p>
      </a>
      <p
        className={cn(
          "shrink-0 text-right text-sm",
          row.slow ? "text-warning" : "text-fg",
        )}
      >
        {tr(stageLabel(row))} · {tr("{{value0}} 秒", { value0: elapsed })}
        {waiting && (
          <span className="text-fg-muted">
            {" "}
            · {tr(typicalWait, { value0: row.expected_s })}
          </span>
        )}
      </p>
      {row.stage === "waiting_user" ? (
        <span className="w-16 shrink-0" />
      ) : (
        <Button
          variant="ghost"
          className="w-16 shrink-0"
          aria-label={tr("停止 {{value0}}", {
            value0: row.prompt || row.conversation,
          })}
          disabled={stop.isPending}
          onClick={() => stop.mutate()}
        >
          {tr("停止")}
        </Button>
      )}
      <ErrorNotice error={stop.error} className="mb-0" />
    </article>
  );
}

function Recent({ row, agent }: { row: TurnRow; agent: string }) {
  const cache = useQueryClient();
  const retry = useMutation({
    mutationFn: () => product(`sessions/${row.session_id}/retry`, "POST", {}),
    onSuccess: () => void cache.invalidateQueries({ queryKey: ["turns"] }),
  });
  const state = outcome[row.outcome];
  return (
    <article className="flex items-center gap-4 px-6 py-4">
      <StatusIcon status={state.icon} title={tr(state.label)} />
      <a href={`#/sessions/${row.session_id}`} className="min-w-0 flex-1">
        <h3 className="truncate text-sm font-medium text-fg">
          {row.prompt || row.conversation}
        </h3>
        <p
          className={cn(
            "mt-0.5 truncate text-sm",
            row.outcome === "failed" ? "text-danger" : "text-fg-muted",
          )}
        >
          {row.outcome === "failed" && row.failure
            ? row.failure
            : context(row, agent)}
        </p>
      </a>
      <p className="shrink-0 text-right text-sm tabular-nums text-fg">
        {tr("{{value0}} 秒", { value0: row.elapsed_s })}
        {row.retries > 0 && (
          <span className="text-fg-muted">
            {" "}
            · {tr("自动恢复 {{value0}} 次", { value0: row.retries })}
          </span>
        )}
      </p>
      <span className="w-24 shrink-0 text-right text-sm text-fg-muted">
        {dateTime(Math.floor((row.done_at || row.started_at) / 1000))}
      </span>
      {row.outcome === "failed" && row.latest ? (
        <Button
          variant="ghost"
          className="w-16 shrink-0"
          aria-label={tr("重试 {{value0}}", {
            value0: row.prompt || row.conversation,
          })}
          disabled={retry.isPending}
          onClick={() => retry.mutate()}
        >
          {tr("重试")}
        </Button>
      ) : (
        <span className="w-16 shrink-0" />
      )}
      <ErrorNotice error={retry.error} className="mb-0" />
    </article>
  );
}

// Every running Turn across Web and Feishu, and what recently finished.
export function Runs() {
  const agents = useAgents();
  const [agentFilter, setAgentFilter] = useState("");
  const [source, setSource] = useState("");
  const [now, setNow] = useState(Date.now());
  const overview = useQuery({
    queryKey: ["turns"],
    queryFn: () => product<TurnOverview>("turns"),
    refetchInterval: 2000,
  });
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const name = (id: string) =>
    agents.data?.find((agent) => agent.id === id)?.name || "—";
  const keep = (row: TurnRow) =>
    (!agentFilter || row.agent_id === agentFilter) &&
    (!source || row.source === source);
  const data = overview.data;
  const running = data?.running.filter(keep) || [];
  const recent = data?.recent.filter(keep) || [];
  return (
    <>
      <PageHeader title={tr("运行")} />
      <RecordTabs active="runs" />
      <ErrorNotice page error={overview.error || agents.error} />
      <div className="mx-auto w-full max-w-6xl space-y-6 px-6 pb-12 pt-5 sm:px-10">
        {data && (
          <dl className="flex flex-wrap divide-x divide-line-muted overflow-hidden rounded-2xl border border-line bg-surface app-shadow-card">
            <Stat label={tr("进行中")} value={data.stats.running} />
            <Stat label={tr("等你回复")} value={data.stats.waiting_user} />
            <Stat
              label={tr("近 5 分钟失败")}
              value={data.stats.failed_recent}
              tone="danger"
            />
            <Concurrency series={data.stats.concurrency} />
          </dl>
        )}
        <div className="flex flex-wrap gap-3">
          <div className="w-44">
            <Select
              aria-label={tr("筛选 Agent")}
              value={agentFilter}
              onValueChange={setAgentFilter}
            >
              <SelectOption value="">{tr("全部 Agent")}</SelectOption>
              {agents.data?.map((agent) => (
                <SelectOption key={agent.id} value={agent.id}>
                  {agent.name}
                </SelectOption>
              ))}
            </Select>
          </div>
          <div className="w-36">
            <Select
              aria-label={tr("来源")}
              value={source}
              onValueChange={setSource}
            >
              <SelectOption value="">{tr("全部来源")}</SelectOption>
              <SelectOption value="feishu">{tr("飞书对话")}</SelectOption>
              <SelectOption value="web">{tr("网页对话")}</SelectOption>
            </Select>
          </div>
        </div>
        <section aria-label={tr("进行中")}>
          <h2 className="mb-3 text-sm font-semibold text-fg">
            {tr("进行中 · {{value0}}", { value0: running.length })}
          </h2>
          {running.length ? (
            <div className="divide-y divide-line-muted overflow-hidden rounded-2xl border border-line bg-surface app-shadow-card">
              {running.map((row) => (
                <Running
                  key={row.id}
                  row={row}
                  agent={name(row.agent_id)}
                  now={now}
                />
              ))}
            </div>
          ) : (
            data && (
              <p className="rounded-2xl border border-dashed border-line px-6 py-5 text-sm text-fg-muted">
                {tr("现在没有正在执行的对话")}
              </p>
            )
          )}
        </section>
        <section aria-label={tr("最近完成")}>
          <h2 className="mb-3 text-sm font-semibold text-fg">
            {tr("最近完成")}
          </h2>
          {recent.length ? (
            <div className="divide-y divide-line-muted overflow-hidden rounded-2xl border border-line bg-surface app-shadow-card">
              {recent.map((row) => (
                <Recent key={row.id} row={row} agent={name(row.agent_id)} />
              ))}
            </div>
          ) : (
            data && <EmptyState icon={Play} title={tr("暂无记录")} />
          )}
        </section>
      </div>
    </>
  );
}
