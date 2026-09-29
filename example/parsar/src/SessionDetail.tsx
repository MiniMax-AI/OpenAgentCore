import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Info } from "lucide-react";
import {
  api,
  dateTime,
  readHistory,
  sessionState,
  sessionTitle,
} from "./lib/api";
import { Button } from "./components/ui/button";
import { EmptyState } from "./components/ui/empty-state";
import { StatusIcon } from "./components/ui/status-icon";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { Property, PropertyList } from "./components/ui/property-list";
import { ErrorNotice } from "./components/shared";
import { Activity, Thinking } from "./components/Activity";
import { useSendTiming, timingLabel } from "./lib/session-timing";
import { Help } from "./components/shared";
import { useLiveSession } from "./lib/live-session";
import { Composer } from "./Composer";
import { SessionFiles } from "./SessionFiles";
import { ConnectMachine } from "./ConnectMachine";
import type { SessionRecord } from "./lib/product";

export function SessionDetail({
  id,
  agentId,
  machine,
}: {
  id: string;
  agentId: string;
  machine?: SessionRecord["self_hosted"];
}) {
  const timing = useSendTiming(id);
  const [info, setInfo] = useState(false);
  const [messagePending, setMessagePending] = useState(false);
  const [files, setFiles] = useState(false);
  const [attachment, setAttachment] = useState("");
  const [machineConnected, setMachineConnected] = useState(false);
  const viewport = useRef<HTMLDivElement>(null);
  const followOutput = useRef(true);
  const query = useQuery({
    queryKey: ["session", id],
    queryFn: async ({ signal }) => {
      const [session, items, turns] = await Promise.all([
        api.retrieveSession(id, { signal }),
        readHistory((options) => api.listItems(id, options), signal),
        api.listTurns(id, { limit: 20, order: "desc", signal }),
      ]);
      return { session, items, turns: turns.data };
    },
    refetchInterval: 2000,
  });
  const data = query.data;
  const live = useLiveSession(id, data?.items);
  useEffect(() => {
    if (viewport.current && followOutput.current)
      viewport.current.scrollTop = viewport.current.scrollHeight;
  }, [live.items]);
  const session = data?.session;
  const environmentId =
    session &&
    "id" in session.environment &&
    typeof session.environment.id === "string"
      ? session.environment.id
      : undefined;
  const state = session ? sessionState(session, data?.turns[0]) : undefined;
  return (
    <>
      <header className="flex h-16 shrink-0 items-center gap-3 border-b border-line px-6">
        <Button asChild variant="ghost" size="icon">
          <a href={`#/agents/${agentId}`} aria-label="返回 Agent">
            <ArrowLeft />
          </a>
        </Button>
        <h1 className="min-w-0 flex-1 truncate text-xl font-semibold">
          {session ? sessionTitle(session) : "会话"}
        </h1>
        {state && (
          <span className="flex shrink-0 items-center gap-2 text-base">
            <StatusIcon status={state.icon} />
            {state.label}
          </span>
        )}
        {environmentId && (
          <Button variant="ghost" onClick={() => setFiles(true)}>
            文件
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          aria-label="会话详情"
          disabled={!session}
          onClick={() => setInfo(true)}
        >
          <Info />
        </Button>
      </header>
      {files && session && environmentId && (
        <SessionFiles
          sessionId={id}
          environmentId={environmentId}
          writable={
            !messagePending &&
            !query.error &&
            session.status === "idle" &&
            (!machine || machineConnected)
          }
          close={() => setFiles(false)}
          useFile={setAttachment}
        />
      )}
      {session && machine && (
        <ConnectMachine
          session={session}
          machine={machine}
          onConnected={setMachineConnected}
        />
      )}
      {live.reconnecting && (
        <p role="status" className="px-6 py-2 text-base text-fg-muted">
          正在重连实时回复，历史仍会自动更新。
        </p>
      )}
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {session?.error && <ErrorNotice error={session.error} />}
      {data?.turns[0]?.error && !session?.error && (
        <ErrorNotice error={data.turns[0].error.message} />
      )}
      {query.isPending ? (
        <EmptyState title="正在加载…" />
      ) : (
        data && (
          <>
            <div
              ref={viewport}
              onScroll={(event) => {
                const element = event.currentTarget;
                followOutput.current =
                  element.scrollHeight -
                    element.scrollTop -
                    element.clientHeight <
                  80;
              }}
              className="min-h-0 flex-1 overflow-y-auto"
              aria-label="会话过程"
            >
              <div className="mx-auto max-w-3xl px-6 py-6">
                {live.items.map((item) => (
                  <Activity key={item.id} item={item} />
                ))}
                {!live.items.length && (
                  <EmptyState title="等待执行记录" size="compact" />
                )}
                {data.session.status === "in_progress" && <Thinking />}
                {data.session.status === "requires_action" &&
                  data.session.required_actions.some(
                    (action) =>
                      !machine || action.type !== "environment_connection",
                  ) && (
                    <div role="status" className="py-4 text-base">
                      此会话需要外部操作，当前示例暂不支持处理。可取消本次执行。
                      <Button variant="ghost" onClick={() => setInfo(true)}>
                        查看所需操作
                      </Button>
                    </div>
                  )}
              </div>
            </div>
            {timing && (
              <div
                aria-label="本次发送耗时"
                className="mx-auto flex w-full max-w-3xl flex-wrap items-center gap-x-6 gap-y-2 px-6 pt-3 text-base text-fg-muted"
              >
                <span>
                  请求返回{" "}
                  <span
                    className="tabular-nums text-fg"
                    data-testid="response-time"
                  >
                    {timingLabel(timing.responseMs)}
                  </span>
                </span>
                <span>
                  首个文本增量{" "}
                  <span
                    className="tabular-nums text-fg"
                    data-testid="first-text-time"
                  >
                    {timingLabel(timing.firstTextMs)}
                  </span>
                </span>
                <Help>
                  从本次发送开始计时：请求返回记录发送接口完成；首个文本增量记录浏览器首次收到非空文本片段。未观测到的时间显示为
                  —，不使用历史轮询推算。
                </Help>
              </div>
            )}
            <Composer
              onPendingChange={setMessagePending}
              attachment={attachment}
              connectionPending={Boolean(machine) && !machineConnected}
              key={id}
              turnId={data.turns[0]?.id}
              session={data.session}
            />
          </>
        )
      )}
      {session && (
        <Dialog open={info} onOpenChange={setInfo}>
          <DialogContent aria-describedby={undefined}>
            <DialogHeader>
              <DialogTitle>会话详情</DialogTitle>
            </DialogHeader>
            <PropertyList>
              <Property label="Agent">
                {session.metadata.agent_name || session.agent.name || "Agent"}
              </Property>
              <Property label="模型">{session.agent.model}</Property>
              <Property label="运行引擎">
                {session.agent.x_agents_core?.harness || "部署默认"}
              </Property>
              <Property label="环境">{session.environment.type}</Property>
              <Property label="创建时间">
                {dateTime(session.created_at)}
              </Property>
              <Property label="Session ID" mono>
                {session.id}
              </Property>
              <Property label="Tokens" mono>
                {session.usage?.total_tokens ?? "未报告"}
              </Property>
            </PropertyList>
            {session.required_actions.some(
              (action) => !machine || action.type !== "environment_connection",
            ) && (
              <pre className="max-h-64 overflow-auto text-base">
                {JSON.stringify(
                  session.required_actions.filter(
                    (action) =>
                      !machine || action.type !== "environment_connection",
                  ),
                  null,
                  2,
                )}
              </pre>
            )}
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}
