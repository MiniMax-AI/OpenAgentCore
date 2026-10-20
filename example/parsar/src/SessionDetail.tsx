import {
  TurnNavRail,
  turnPreview,
  useActiveTurnKey,
} from "./components/ui/turn-nav-rail";
import { tr } from "./lib/i18n";
import { optionValue } from "./Profiles";
import { Questions } from "./Questions";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FolderOpen, Info } from "lucide-react";
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
import { SessionTimeline, Thinking } from "./components/Activity";
import { useLiveSession } from "./lib/live-session";
import { Composer } from "./Composer";
import { SessionFiles } from "./SessionFiles";
import {
  harnessNames,
  useAgents,
  useProfiles,
  useRuntimes,
  type SessionRecord,
} from "./lib/product";
export function SessionDetail({
  id,
  record,
}: {
  id: string;
  record: SessionRecord;
}) {
  const [info, setInfo] = useState(false);
  const [messagePending, setMessagePending] = useState(false);
  const [files, setFiles] = useState(false);
  const [attachment, setAttachment] = useState("");
  const [viewportElement, setViewportElement] = useState<HTMLDivElement | null>(
    null,
  );
  const followOutput = useRef(true);
  const agents = useAgents(),
    profiles = useProfiles(),
    runtimes = useRuntimes();
  const query = useQuery({
    queryKey: ["session", id],
    queryFn: async ({ signal }) => {
      const [session, items, turns] = await Promise.all([
        api.retrieveSession(id, { signal }),
        readHistory((options) => api.listItems(id, options), signal),
        readHistory((options) => api.listTurns(id, options), signal),
      ]);
      return { session, items, turns: turns.reverse() };
    },
    refetchInterval: 2000,
  });
  const data = query.data;
  const live = useLiveSession(id, data?.items);
  const userTurns = live.items
    .filter((item) => item.type === "message" && item.role === "user")
    .map((item) => ({
      key: item.id,
      preview: turnPreview(
        item.content?.map((part) => part.text || "").join("\n") || "",
      ),
    }));
  const activeTurn = useActiveTurnKey(
    viewportElement,
    userTurns.map((turn) => turn.key),
  );
  useEffect(() => {
    if (viewportElement && followOutput.current)
      viewportElement.scrollTop = viewportElement.scrollHeight;
  }, [live.items, viewportElement]);
  const session = data?.session;
  const environmentId =
    session &&
    "id" in session.environment &&
    typeof session.environment.id === "string"
      ? session.environment.id
      : undefined;
  const state = session ? sessionState(session, data?.turns[0]) : undefined;
  const configuration = record.configuration;
  const agent = agents.data?.find((row) => row.id === record.agent_id);
  const runtime = runtimes.data?.find((row) => row.id === record.runtime_id);
  const profile = profiles.data?.find((row) => row.id === agent?.profile_id);
  const outdated =
    configuration?.agent_revision !== undefined &&
    !!agent &&
    (agent.revision !== configuration.agent_revision ||
      agent.runtime_id !== record.runtime_id ||
      (!!runtime && runtime.revision !== configuration.runtime_revision) ||
      (!!profile &&
        configuration.profile_revision !== undefined &&
        (profile.name !== configuration.profile_name ||
          profile.revision !== configuration.profile_revision)));
  return (
    <>
      <header className="flex h-16 shrink-0 items-center gap-3 border-b border-line-muted px-6">
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-base font-semibold tracking-tight">
            {session ? sessionTitle(session) : record.name}
          </h1>
        </div>
        {state && (
          <span className="flex shrink-0 items-center gap-2 text-sm text-fg-muted">
            <StatusIcon status={state.icon} />
            {state.label}
          </span>
        )}
        {environmentId && (
          <Button
            variant="ghost"
            size="icon"
            aria-label={tr("文件")}
            title={tr("文件")}
            onClick={() => setFiles(true)}
          >
            <FolderOpen />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          aria-label={tr("会话详情")}
          title={tr("会话详情")}
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
            !messagePending && !query.error && session.status === "idle"
          }
          close={() => setFiles(false)}
          useFile={setAttachment}
        />
      )}
      <ErrorNotice page
        error={query.error}
        retry={() => void query.refetch()}
        className="max-w-3xl"
      />
      {session?.error && (
        <ErrorNotice page error={session.error} className="max-w-3xl" />
      )}
      {data?.turns[0]?.error && !session?.error && (
        <ErrorNotice page error={data.turns[0].error.message} className="max-w-3xl" />
      )}
      {query.isPending ? (
        <EmptyState className="flex-1" title={tr("正在加载\u2026")} />
      ) : (
        data && (
          <>
            <div className="relative flex min-h-0 flex-1 flex-col">
              {/* Short conversations need no jump navigation. */}
              {userTurns.length >= 3 && (
                <TurnNavRail
                  turns={userTurns}
                  activeKey={activeTurn}
                  onJump={(key) => {
                    followOutput.current = false;
                    document
                      .getElementById(`item-${key}`)
                      ?.scrollIntoView({ block: "start", behavior: "smooth" });
                  }}
                />
              )}
              <div
                ref={setViewportElement}
                onScroll={(event) => {
                  const element = event.currentTarget;
                  followOutput.current =
                    element.scrollHeight -
                      element.scrollTop -
                      element.clientHeight <
                    80;
                }}
                className="min-h-0 flex-1 overflow-y-auto"
                aria-label={tr("会话过程")}
              >
                <div className="mx-auto max-w-3xl px-6 py-10">
                  <SessionTimeline items={live.items} />
                  {!live.items.length && (
                    <EmptyState title={tr("等待执行记录")} size="compact" />
                  )}
                  {data.session.status === "in_progress" && <Thinking />}
                  <Questions session={data.session} />
                  {live.reconnecting && (
                    <p role="status" className="py-2 text-sm text-fg-muted">
                      {tr("正在重连实时回复，历史仍会自动更新。")}
                    </p>
                  )}
                </div>
              </div>
            </div>
            {outdated && (
              <div className="mx-auto flex w-full max-w-3xl flex-wrap items-center justify-between gap-3 px-6 pt-3">
                <p className="text-sm text-fg-muted">
                  {tr("Agent 配置已更新，新配置只对新对话生效。")}
                </p>
                <Button asChild variant="outline" size="sm">
                  <a href={`#/new?agent=${record.agent_id}`}>
                    {tr("用新配置开始新对话")}
                  </a>
                </Button>
              </div>
            )}
            <Composer
              onPendingChange={setMessagePending}
              attachment={attachment}
              key={id}
              turnId={data.turns[0]?.id}
              session={data.session}
            />
          </>
        )
      )}
      {session && (
        <Dialog open={info} onOpenChange={setInfo}>
          <DialogContent
            aria-describedby={undefined}
            className="max-w-xl"
          >
            <DialogHeader>
              <DialogTitle>{tr("会话详情")}</DialogTitle>
            </DialogHeader>
            <PropertyList>
              <Property label="Agent">
                {configuration?.agent_name ||
                  session.metadata.agent_name ||
                  "Agent"}
              </Property>
              <Property label={tr("模型")}>{session.agent.model}</Property>
              <Property label={tr("执行引擎")}>
                {configuration
                  ? harnessNames[configuration.harness]
                  : session.agent.x_agents_core?.harness || tr("部署默认")}
              </Property>
              {configuration?.profile_name && (
                <Property label={tr("执行配置")}>
                  {configuration.profile_name}
                </Property>
              )}
              <Property label={tr("推理强度")}>
                {optionValue("reasoning_effort", session.agent.reasoning?.effort || "")}
              </Property>
              <Property label={tr("网页搜索")}>
                {optionValue(
                  "web_search",
                  (session.agent.tools as { type: string; mode?: string }[])?.find(
                    (tool) => tool.type === "web_search",
                  )?.mode || "disabled",
                )}
              </Property>
              {configuration && (
                <Property label={tr("环境模板")}>
                  {configuration.environment_name}
                </Property>
              )}
              {configuration?.bot_name && (
                <Property label={tr("飞书身份")}>
                  {configuration.bot_name}
                </Property>
              )}
              <Property label={tr("来源")}>
                {record.source === "feishu"
                  ? record.feishu_chat_type === "p2p"
                    ? tr("飞书私聊")
                    : tr("飞书群聊")
                  : tr("网页")}
              </Property>
              <Property label={tr("创建时间")}>
                {dateTime(session.created_at)}
              </Property>
              <Property label="Tokens" mono>
                {session.usage?.total_tokens ?? tr("未报告")}
              </Property>
            </PropertyList>
            {configuration && (
              <details className="group rounded-xl bg-surface-subtle px-4 py-3">
                <summary className="cursor-pointer text-sm font-medium">
                  {tr("创建时的配置")}
                </summary>
                <PropertyList className="mt-3">
                  <Property label={tr("软件包")}>
                    {Object.values(configuration.packages || {})
                      .flat()
                      .join(", ") || "—"}
                  </Property>
                  <Property label={tr("初始化脚本")}>
                    {configuration.setup_configured ? tr("已配置") : "—"}
                  </Property>
                  <Property label={tr("环境变量")}>
                    {configuration.env_names?.join(", ") || "—"}
                  </Property>
                  <Property label={tr("初始文件")}>
                    {configuration.file_paths?.join(", ") || "—"}
                  </Property>
                  <Property label="Skills">
                    {configuration.skills
                      .map(
                        (skill) =>
                          `${skill.id} · ${skill.version || tr("默认版本")}`,
                      )
                      .join(", ") || "—"}
                  </Property>
                  <Property label="MCP">
                    {configuration.mcps.join(", ") || "—"}
                  </Property>
                  <Property label={tr("能力")}>
                    {configuration.capabilities
                      .map(
                        (row) => `${row.name} (${row.kind}) · v${row.version}`,
                      )
                      .join(", ") || "—"}
                  </Property>
                  <Property label="Session ID" mono>
                    {session.id}
                  </Property>
                  <Property label="Environment ID" mono>
                    {environmentId || "—"}
                  </Property>
                </PropertyList>
              </details>
            )}
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}
