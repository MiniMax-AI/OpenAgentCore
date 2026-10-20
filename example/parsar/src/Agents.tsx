import { tr } from "./lib/i18n";
import { useState } from "react";
import { Plus, Search, Send, Server } from "lucide-react";
import {
  useAgents,
  useFeishuBots,
  useModels,
  useProfiles,
  useRuntimes,
  type AgentProfile,
} from "./lib/product";
import { profileSummary } from "./Profiles";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { ErrorNotice, PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
import { AgentAvatar } from "./components/AgentAvatar";
import { cn } from "./lib/utils";

export function Chip({
  icon: Icon,
  children,
  muted,
}: {
  icon?: typeof Server;
  children: React.ReactNode;
  muted?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-flex max-w-full items-center gap-1.5 rounded-full px-3 py-1 text-sm",
        muted ? "text-fg-subtle ring-1 ring-line" : "bg-surface-muted text-fg-muted",
      )}
    >
      {Icon && <Icon strokeWidth={1.7} className="h-3.5 w-3.5 shrink-0" />}
      <span className="truncate">{children}</span>
    </span>
  );
}

export function Agents() {
  const agents = useAgents(),
    models = useModels(),
    profiles = useProfiles(),
    runtimes = useRuntimes(),
    bots = useFeishuBots();
  const [search, setSearch] = useState("");
  const rows = (agents.data || []).filter((agent) =>
    `${agent.name} ${agent.description || ""}`
      .toLowerCase()
      .includes(search.toLowerCase().trim()),
  );
  return (
    <>
      <PageHeader title={tr("Agent")}>
        {(agents.data?.length || 0) > 0 && (
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-fg-subtle" />
            <Input
              aria-label={tr("搜索 Agent")}
              placeholder={tr("搜索 Agent")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-56 pl-9"
            />
          </div>
        )}
        <Button asChild>
          <a href="#/agents/new">
            <Plus />
            {tr("新建 Agent")}
          </a>
        </Button>
      </PageHeader>
      <ErrorNotice page error={agents.error || models.error} />
      <div className="mx-auto w-full max-w-6xl px-6 pb-12 sm:px-10">
        {!agents.isPending && !agents.data?.length ? (
          <EmptyState
            title={tr("还没有 Agent")}
            description={tr("Agent 组合指令、模型、工具和环境模板，可以在网页和飞书中对话。")}
            action={
              <Button asChild size="lg">
                <a href="#/agents/new">
                  <Plus />
                  {tr("新建 Agent")}
                </a>
              </Button>
            }
          />
        ) : (
          <div className="grid gap-5 pt-1 sm:grid-cols-2 xl:grid-cols-3">
            {rows.map((agent) => (
              <AgentCard
                key={agent.id}
                agent={agent}
                engine={profileSummary(
                  profiles.data?.find((row) => row.id === agent.profile_id),
                  models.data,
                )}
                runtime={runtimes.data?.find((r) => r.id === agent.runtime_id)?.name}
                bot={bots.data?.find((b) => b.agent_id === agent.id)?.name}
              />
            ))}
          </div>
        )}
      </div>
    </>
  );
}

function AgentCard({
  agent,
  engine,
  runtime,
  bot,
}: {
  agent: AgentProfile;
  engine: string;
  runtime?: string;
  bot?: string;
}) {
  const disabled = agent.enabled === false;
  return (
    <article className="group relative flex flex-col rounded-2xl border border-line bg-surface p-6 app-shadow-card transition-colors hover:border-line-strong">
      <div className="flex items-start gap-4">
        <AgentAvatar name={agent.name} size="lg" />
        <div className="min-w-0 flex-1 pt-0.5">
          <h2 className="truncate text-base font-semibold tracking-tight">
            <a
              href={`#/agents/${agent.id}`}
              className="after:absolute after:inset-0 after:rounded-2xl focus-visible:outline-none"
            >
              {agent.name}
            </a>
          </h2>
          <p className="mt-0.5 truncate text-sm text-fg-muted">
            {engine}
          </p>
        </div>
        {disabled && <Chip muted>{tr("已停用")}</Chip>}
      </div>
      <p className="mt-5 line-clamp-2 min-h-[3.25em] flex-1 text-sm leading-relaxed text-fg-muted">
        {agent.description || agent.instructions || tr("暂无描述")}
      </p>
      <div className="mt-5 flex flex-wrap gap-2">
        <Chip icon={Server} muted={!runtime}>
          {runtime || tr("未选择环境")}
        </Chip>
        {bot && <Chip icon={Send}>{bot}</Chip>}
      </div>
      <div className="relative mt-6 flex gap-2 border-t border-line-muted pt-5">
        <Button asChild size="sm" disabled={disabled}>
          <a
            href={disabled ? undefined : `#/new?agent=${agent.id}`}
            aria-disabled={disabled}
            className={disabled ? "pointer-events-none opacity-50" : undefined}
          >
            {tr("开始对话")}
          </a>
        </Button>
        <Button asChild size="sm" variant="ghost">
          <a href={`#/agents/new?from=${agent.id}`}>{tr("复制")}</a>
        </Button>
      </div>
    </article>
  );
}
