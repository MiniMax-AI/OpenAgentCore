import { Select, SelectOption } from "./components/ui/select";
import { tr } from "./lib/i18n";
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { AgentSession, AgentTurn } from "@oac/agents-client";
import { Search, MessageSquare, Play, X, Pencil, Trash2 } from "lucide-react";
import { api, readHistory, dateTime, sessionState } from "./lib/api";
import {
  product,
  useSessions,
  useAgents,
  type SessionRecord,
} from "./lib/product";
import { PageHeader, ErrorNotice } from "./components/shared";
import { Ledger, LedgerHeader, LedgerRow } from "./components/ui/ledger";
import { Input } from "./components/ui/input";
import { Button } from "./components/ui/button";
import { EmptyState } from "./components/ui/empty-state";
import { StatusIcon } from "./components/ui/status-icon";
import { SessionPage } from "./SessionPage";
type Observation = {
  record: SessionRecord;
  session?: AgentSession;
  turns: AgentTurn[];
  error?: string;
};
export function useExecutionRecords(rows: SessionRecord[] | undefined) {
  return useQuery({
    queryKey: [
      "execution-records",
      rows?.map((row) => `${row.id}:${row.core_session_id}`).join(","),
    ],
    enabled: !!rows,
    queryFn: async ({ signal }) => {
      const result: Observation[] = [];
      for (let i = 0; i < (rows?.length || 0); i += 4) {
        result.push(
          ...(await Promise.all(
            rows!.slice(i, i + 4).map(async (record) => {
              if (!record.core_session_id) return { record, turns: [] };
              try {
                const [session, turns] = await Promise.all([
                  api.retrieveSession(record.core_session_id, { signal }),
                  readHistory(
                    (options) =>
                      api.listTurns(record.core_session_id!, options),
                    signal,
                  ),
                ]);
                return { record, session, turns };
              } catch (error) {
                if (signal.aborted) throw error;
                return {
                  record,
                  turns: [],
                  error:
                    error instanceof Error ? error.message : tr("读取失败"),
                };
              }
            }),
          )),
        );
      }
      return result;
    },
    refetchInterval: (query) =>
      query.state.data?.some(
        (row) =>
          !row.session ||
          ["in_progress", "requires_action"].includes(row.session.status),
      )
        ? 10000
        : false,
  });
}
export function Records({
  view,
}: {
  view: "conversations" | "runs" | "usage";
}) {
  const sessions = useSessions(),
    agents = useAgents(),
    cache = useQueryClient();
  const mutate = useMutation({
    mutationFn: ({
      id,
      method,
      name,
    }: {
      id: string;
      method: string;
      name?: string;
    }) => product(`sessions/${id}`, method, name ? { name } : undefined),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["sessions"] });
      setSelected("");
    },
  });
  const [search, setSearch] = useState(""),
    [page, setPage] = useState(0),
    [selected, setSelected] = useState("");
  const [from, setFrom] = useState(""),
    [to, setTo] = useState("");
  const start = from
    ? new Date(`${from}T00:00:00`).getTime() / 1000
    : -Infinity;
  const end = to ? new Date(`${to}T23:59:59`).getTime() / 1000 : Infinity;
  const [agentFilter, setAgentFilter] = useState(""),
    [source, setSource] = useState("");
  const filtered = sessions.data?.filter(
    (row) =>
      (!agentFilter || row.agent_id === agentFilter) &&
      (!source || row.source === source) &&
      `${row.name} ${row.id}`.toLowerCase().includes(search.toLowerCase()),
  );
  const rows = filtered?.slice(page * 20, (page + 1) * 20);
  const all = view === "usage" || !!from || !!to;
  const observations = useExecutionRecords(all ? filtered : rows);
  const measured = (observations.data || [])
    .map((row) => {
      const turns = row.turns.filter(
        (turn) => turn.created_at >= start && turn.created_at <= end,
      );
      const usage =
        from || to
          ? turns.length && turns.every((turn) => turn.usage)
            ? turns.reduce(
                (sum, turn) => ({
                  input_tokens: sum.input_tokens + turn.usage!.input_tokens,
                  output_tokens: sum.output_tokens + turn.usage!.output_tokens,
                  total_tokens: sum.total_tokens + turn.usage!.total_tokens,
                }),
                { input_tokens: 0, output_tokens: 0, total_tokens: 0 },
              )
            : null
          : row.session?.usage;
      const cost =
        usage && row.record.pricing
          ? (usage.input_tokens * row.record.pricing.input +
              usage.output_tokens * row.record.pricing.output) /
            1000000
          : undefined;
      return { ...row, turns, usage, cost };
    })
    .filter(
      (row) =>
        !(from || to) ||
        (view === "conversations"
          ? row.record.created_at >= start && row.record.created_at <= end
          : row.turns.length > 0),
    );
  const total = all ? measured.length : filtered?.length || 0;
  const data = all ? measured.slice(page * 20, (page + 1) * 20) : measured;
  const title = {
    conversations: tr("对话"),
    runs: tr("运行记录"),
    usage: tr("用量"),
  }[view];
  const usage = measured.reduce(
    (sum, row) => ({
      input: sum.input + (row.usage?.input_tokens || 0),
      output: sum.output + (row.usage?.output_tokens || 0),
      unknown: sum.unknown + (row.usage ? 0 : 1),
      cost: sum.cost + (row.cost || 0),
      unpriced: sum.unpriced + (row.cost === undefined ? 1 : 0),
    }),
    { input: 0, output: 0, unknown: 0, cost: 0, unpriced: 0 },
  );
  return (
    <>
      <PageHeader title={title} />
      <ErrorNotice
        error={
          sessions.error || agents.error || observations.error || mutate.error
        }
      />
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-line px-6">
        <Search className="h-3.5 w-3.5 text-fg-muted" />
        <Input
          className="max-w-sm"
          aria-label={tr("搜索{{value0}}", { value0: title })}
          placeholder={tr("搜索{{value0}}", { value0: title })}
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setPage(0);
          }}
        />
        <Select
          aria-label={tr("筛选 Agent")}
          value={agentFilter}
          onValueChange={(value) => {
            setAgentFilter(value);
            setPage(0);
          }}
        >
          <SelectOption value="">{tr("全部 Agent")}</SelectOption>
          {agents.data?.map((agent) => (
            <SelectOption key={agent.id} value={agent.id}>
              {agent.name}
            </SelectOption>
          ))}
        </Select>
        <Select
          aria-label={tr("来源")}
          value={source}
          onValueChange={(value) => {
            setSource(value);
            setPage(0);
          }}
        >
          <SelectOption value="">{tr("全部来源")}</SelectOption>
          <SelectOption value="web">Web</SelectOption>
          <SelectOption value="mcp">MCP</SelectOption>
          <SelectOption value="feishu">Feishu</SelectOption>
          <SelectOption value="task">{tr("任务")}</SelectOption>
          <SelectOption value="schedule">{tr("定时任务")}</SelectOption>
        </Select>
      </div>
      <div className="flex flex-wrap items-center gap-3 border-b border-line px-6 py-2 text-xs">
        <label>
          {tr("开始日期")}
          <Input
            type="date"
            value={from}
            onChange={(e) => {
              setFrom(e.target.value);
              setPage(0);
            }}
          />
        </label>
        <label>
          {tr("结束日期")}
          <Input
            type="date"
            value={to}
            min={from || undefined}
            onChange={(e) => {
              setTo(e.target.value);
              setPage(0);
            }}
          />
        </label>
      </div>
      {view === "usage" && (
        <div className="flex flex-wrap gap-8 border-b border-line px-6 py-4 text-sm">
          <span>
            {tr("输入合计")}
            <strong className="font-mono">
              {usage.input.toLocaleString()}
            </strong>
          </span>
          <span>
            {tr("输出合计")}
            <strong className="font-mono">
              {usage.output.toLocaleString()}
            </strong>
          </span>
          <span className="text-fg-muted">
            {usage.unknown}
            {tr("个会话用量未知")}
          </span>
          <span>
            {tr("估算费用")}: ${usage.cost.toFixed(4)} USD · {usage.unpriced}{" "}
            {tr("个会话未定价")}
          </span>
        </div>
      )}
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <Ledger
            columns={
              view === "usage"
                ? "minmax(220px,2fr) 130px 110px 110px 120px 110px"
                : view === "runs"
                  ? "16px minmax(200px,2fr) minmax(100px,1fr) 100px 120px 90px 90px 60px"
                  : "16px minmax(200px,2fr) minmax(100px,1fr) 100px 120px 60px"
            }
          >
            <LedgerHeader>
              {view !== "usage" && <span />}
              <span>{tr("名称")}</span>
              <span>Agent</span>
              <span>{view === "usage" ? tr("输入 Token") : tr("状态")}</span>
              <span>{view === "usage" ? tr("输出 Token") : tr("时间")}</span>
              {view === "runs" && (
                <>
                  <span>{tr("耗时")}</span>
                  <span>Tokens</span>
                </>
              )}
              {view === "usage" ? (
                <>
                  <span>{tr("总 Token")}</span>
                  <span>{tr("估算费用")} (USD)</span>
                </>
              ) : (
                <span />
              )}
            </LedgerHeader>
            {data.flatMap((row) => {
              const agent =
                agents.data?.find((agent) => agent.id === row.record.agent_id)
                  ?.name || "—";
              if (view === "usage")
                return [
                  <LedgerRow key={row.record.id}>
                    <a
                      href={`#/sessions/${row.record.id}`}
                      className="truncate"
                    >
                      {row.record.name}
                    </a>
                    <span>{agent}</span>
                    <span className="font-mono">
                      {row.usage?.input_tokens ?? "—"}
                    </span>
                    <span className="font-mono">
                      {row.usage?.output_tokens ?? "—"}
                    </span>
                    <span className="font-mono" title={row.error}>
                      {row.usage?.total_tokens ?? "—"}
                    </span>
                    <span className="font-mono">
                      {row.cost?.toFixed(4) ?? "—"}
                    </span>
                  </LedgerRow>,
                ];
              const entries =
                view === "runs" && row.turns.length
                  ? [...row.turns].reverse()
                  : [undefined];
              return entries.map((turn) => {
                const state = row.session
                  ? sessionState(
                      turn &&
                        ["completed", "failed", "cancelled"].includes(
                          turn.status,
                        )
                        ? { ...row.session, status: "idle" }
                        : row.session,
                      turn || row.turns.at(-1),
                    )
                  : {
                      label: row.error ? tr("读取失败") : tr(row.record.connection_wait ? "等待连接" : "待恢复"),
                      icon: "interrupted" as const,
                    };
                return (
                  <LedgerRow
                    key={turn?.id || row.record.id}
                    className={
                      selected === row.record.id ? "bg-surface-muted" : ""
                    }
                  >
                    <StatusIcon status={state.icon} />
                    <button
                      className="min-w-0 truncate text-left"
                      onClick={() => setSelected(row.record.id)}
                    >
                      <span>{row.record.name}</span>
                      {turn && (
                        <span className="ml-2 font-mono text-xs text-fg-muted">
                          {turn.id.slice(0, 8)}
                        </span>
                      )}
                    </button>
                    <span>{agent}</span>
                    <span title={row.error}>{state.label}</span>
                    <span className="text-xs text-fg-muted">
                      {dateTime(turn?.started_at || row.record.created_at)}
                    </span>
                    {view === "runs" && (
                      <>
                        <span className="font-mono text-xs">
                          {turn?.started_at && turn.completed_at
                            ? `${Math.max(0, turn.completed_at - turn.started_at).toFixed(1)} s`
                            : "—"}
                        </span>
                        <span className="font-mono text-xs">
                          {turn?.usage?.total_tokens ?? "—"}
                        </span>
                      </>
                    )}
                    <div className="flex">
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={tr("重命名 {{value0}}", {
                          value0: row.record.name,
                        })}
                        onClick={() => {
                          const name = prompt(tr("会话名称"), row.record.name);
                          if (name?.trim())
                            mutate.mutate({
                              id: row.record.id,
                              method: "PATCH",
                              name: name.trim(),
                            });
                        }}
                      >
                        <Pencil />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={tr("删除会话 {{value0}}", {
                          value0: row.record.name,
                        })}
                        disabled={mutate.isPending}
                        onClick={() => {
                          if (
                            confirm(
                              tr(
                                "删除此会话及其执行历史？正在执行时需先取消。",
                              ),
                            )
                          )
                            mutate.mutate({
                              id: row.record.id,
                              method: "DELETE",
                            });
                        }}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  </LedgerRow>
                );
              });
            })}
            {!sessions.isPending && !observations.isPending && !total && (
              <EmptyState
                icon={view === "conversations" ? MessageSquare : Play}
                title={tr("暂无记录")}
              />
            )}
            {observations.isPending && !!rows?.length && (
              <p className="px-6 py-4 text-fg-muted">
                {tr("正在读取执行记录\u2026")}
              </p>
            )}
          </Ledger>
          <div className="flex h-10 shrink-0 items-center justify-between border-t border-line px-6 text-xs text-fg-muted">
            <span>
              {total}
              {tr("个会话 \u00B7 第")}
              {page + 1}
              {tr("页")}
            </span>
            <div className="flex gap-2">
              <Button
                variant="ghost"
                disabled={!page}
                onClick={() => setPage(page - 1)}
              >
                {tr("上一页")}
              </Button>
              <Button
                variant="ghost"
                disabled={(page + 1) * 20 >= total}
                onClick={() => setPage(page + 1)}
              >
                {tr("下一页")}
              </Button>
            </div>
          </div>
        </div>
        {selected && view !== "usage" && (
          <section
            aria-label={tr("对话详情")}
            className="fixed inset-0 z-30 flex min-h-0 flex-col bg-surface sm:static sm:z-auto sm:w-[48%] sm:min-w-96 sm:border-l sm:border-line"
          >
            <div className="flex h-10 shrink-0 items-center justify-between border-b border-line px-4 text-sm">
              <a href={`#/sessions/${selected}`}>{tr("打开完整对话")}</a>
              <Button
                variant="ghost"
                size="icon"
                aria-label={tr("关闭详情")}
                onClick={() => setSelected("")}
              >
                <X />
              </Button>
            </div>
            <SessionPage key={selected} id={selected} />
          </section>
        )}
      </div>
    </>
  );
}
export function Audit({ resourceId }: { resourceId?: string }) {
  const query = useQuery({
    queryKey: ["audit"],
    queryFn: () =>
      product<
        {
          id: string;
          method: string;
          path: string;
          created_at: number;
        }[]
      >("audit"),
    refetchInterval: 5000,
  });
  const [search, setSearch] = useState("");
  return (
    <>
      <PageHeader title={tr("审计日志")} />
      <ErrorNotice error={query.error} />
      <div className="border-b border-line px-6 py-2">
        <Input
          aria-label={tr("搜索审计")}
          placeholder={tr("搜索资源或操作")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
      </div>
      <Ledger columns="130px 100px minmax(260px,1fr)">
        <LedgerHeader>
          <span>{tr("时间")}</span>
          <span>{tr("操作")}</span>
          <span>{tr("资源")}</span>
        </LedgerHeader>
        {query.data
          ?.filter(
            (row) =>
              (!resourceId || row.path.includes(resourceId)) &&
              `${row.method} ${row.path}`
                .toLowerCase()
                .includes(search.toLowerCase()),
          )
          .map((row) => (
            <LedgerRow key={row.id}>
              <span className="text-xs text-fg-muted">
                {dateTime(row.created_at / 1000)}
              </span>
              <span className="font-mono text-xs">{row.method}</span>
              <span className="font-mono text-xs">{row.path}</span>
            </LedgerRow>
          ))}
        {!query.isPending && !query.data?.length && (
          <EmptyState title={tr("暂无操作记录")} />
        )}
      </Ledger>
    </>
  );
}
